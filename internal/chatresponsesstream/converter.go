// Package chatresponsesstream converts OpenAI Chat Completions SSE chunks to
// OpenAI Responses SSE events.
package chatresponsesstream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"

	"github.com/2218342221/RouteMorphSDK/internal/core"
)

// Options supplies request-side facts that are not present in every Chat SSE
// chunk. ClientModel, when set, is exposed instead of the provider model.
type Options struct {
	ClientModel string
}

// Converter owns one stream's state and implements core.ResponseStream.
type Converter struct {
	options Options

	started        bool
	terminal       bool
	sequence       int64
	responseID     string
	upstreamID     string
	providerModel  string
	createdAt      int64
	finishReason   string
	reasoning      *reasoningState
	message        *messageState
	tools          map[int]*toolState
	outputs        []*outputSlot
	usage          *chatUsage
	sawNormalChunk bool
}

type messageState struct {
	id          string
	outputIndex int
	parts       []*contentState
	text        *contentState
	refusal     *contentState
	closed      bool
}

type contentState struct {
	kind     string
	index    int
	value    strings.Builder
	logprobs []json.RawMessage
}

type reasoningState struct {
	id          string
	outputIndex int
	text        strings.Builder
	closed      bool
}

type toolState struct {
	id          string
	callID      string
	name        string
	outputIndex int
	arguments   strings.Builder
	opened      bool
	closed      bool
}

type outputSlot struct {
	reasoning *reasoningState
	message   *messageState
	tool      *toolState
}

type chatChunk struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []chatChoice    `json:"choices"`
	Usage   *chatUsage      `json:"usage"`
	Error   *chatError      `json:"error"`
	Raw     json.RawMessage `json:"-"`
}

type chatChoice struct {
	Index        int             `json:"index"`
	Delta        json.RawMessage `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs"`
}

type chatDelta struct {
	Role             string          `json:"role"`
	Content          *string         `json:"content"`
	Refusal          *string         `json:"refusal"`
	ToolCalls        []chatToolDelta `json:"tool_calls"`
	FunctionCall     json.RawMessage `json:"function_call"`
	ReasoningContent *string         `json:"reasoning_content"`
}

type chatToolDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string  `json:"name"`
		Arguments *string `json:"arguments"`
	} `json:"function"`
}

type chatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	PromptDetails    struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type chatError struct {
	Message string          `json:"message"`
	Type    string          `json:"type"`
	Code    json.RawMessage `json:"code"`
	Param   json.RawMessage `json:"param"`
}

// New creates an isolated converter for one upstream response stream.
func New(options Options) *Converter {
	return &Converter{options: options, tools: make(map[int]*toolState)}
}

// Convert consumes one decoded Chat SSE frame.
func (c *Converter) Convert(ctx context.Context, frame core.Frame) ([]core.Frame, []core.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if c.terminal {
		return nil, nil, core.Invalid(core.ProtocolChat, "$", "stream already reached a terminal event")
	}
	data := bytes.TrimSpace(frame.Data)
	if frame.Done || bytes.Equal(data, []byte("[DONE]")) {
		frames, err := c.finishStream()
		return frames, nil, err
	}
	if frame.Event != "" && frame.Event != "message" {
		return nil, nil, core.Invalid(core.ProtocolChat, "$.event", "unexpected SSE event %q", frame.Event)
	}
	if len(data) == 0 {
		return nil, nil, core.Invalid(core.ProtocolChat, "$", "empty SSE data")
	}

	var chunk chatChunk
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, nil, core.Invalid(core.ProtocolChat, "$", "decode stream chunk: %v", err)
	}
	if err := validateChunkShape(data); err != nil {
		return nil, nil, err
	}
	if chunk.Error != nil {
		frames, err := c.convertError(chunk.Error)
		return frames, nil, err
	}
	if len(chunk.Choices) == 0 && chunk.Usage == nil {
		return nil, nil, core.Invalid(core.ProtocolChat, "$", "chunk has neither choices, usage, nor error")
	}
	if len(chunk.Choices) > 1 {
		return nil, nil, core.Unsupported(core.ProtocolChat, "$.choices", "multiple Chat choices cannot be represented as one Responses output")
	}
	if err := c.observeEnvelope(chunk); err != nil {
		return nil, nil, err
	}

	var frames []core.Frame
	if !c.started {
		if len(chunk.Choices) == 0 {
			return nil, nil, core.Invalid(core.ProtocolChat, "$.choices", "usage cannot precede the first completion chunk")
		}
		frames = append(frames, c.startEvents()...)
	}
	c.sawNormalChunk = true
	if chunk.Usage != nil {
		copy := *chunk.Usage
		c.usage = &copy
	}
	for index := range chunk.Choices {
		converted, err := c.convertChoice(chunk.Choices[index])
		if err != nil {
			return nil, nil, err
		}
		frames = append(frames, converted...)
	}
	return frames, nil, nil
}

func validateChunkShape(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return core.Invalid(core.ProtocolChat, "$", "stream chunk must be an object")
	}
	for name, value := range fields {
		switch name {
		case "id", "object", "created", "model", "choices", "usage", "error", "system_fingerprint", "service_tier":
		default:
			if meaningfulJSON(value) {
				return core.Unsupported(core.ProtocolChat, "$."+name, "unknown Chat stream chunk field")
			}
		}
	}
	return nil
}

// Finalize closes a stream whose transport ended without an explicit [DONE].
func (c *Converter) Finalize(ctx context.Context) ([]core.Frame, []core.Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if c.terminal {
		return nil, nil, nil
	}
	frames, err := c.finishStream()
	return frames, nil, err
}

func (c *Converter) observeEnvelope(chunk chatChunk) error {
	if chunk.ID != "" {
		if c.upstreamID != "" && c.upstreamID != chunk.ID {
			return core.Invalid(core.ProtocolChat, "$.id", "changed from %q to %q", c.upstreamID, chunk.ID)
		}
		c.upstreamID = chunk.ID
	}
	if !c.started {
		c.responseID = stableID("resp", c.upstreamID, 0)
		c.createdAt = chunk.Created
		c.providerModel = chunk.Model
	} else if chunk.Model != "" && c.providerModel != "" && chunk.Model != c.providerModel {
		return core.Invalid(core.ProtocolChat, "$.model", "changed from %q to %q", c.providerModel, chunk.Model)
	} else if c.providerModel == "" {
		c.providerModel = chunk.Model
	}
	return nil
}

func (c *Converter) startEvents() []core.Frame {
	c.started = true
	response := c.responseObject("in_progress", false)
	return []core.Frame{
		c.event("response.created", map[string]any{"response": response}),
		c.event("response.in_progress", map[string]any{"response": response}),
	}
}

func (c *Converter) convertChoice(choice chatChoice) ([]core.Frame, error) {
	if choice.Index != 0 {
		return nil, core.Unsupported(core.ProtocolChat, "$.choices", "multiple choices cannot be represented as one Responses output")
	}
	textLogprobs, err := decodeChatLogprobs(choice.Logprobs)
	if err != nil {
		return nil, err
	}
	var delta chatDelta
	if len(choice.Delta) == 0 || bytes.Equal(bytes.TrimSpace(choice.Delta), []byte("null")) {
		choice.Delta = []byte(`{}`)
	}
	if err := json.Unmarshal(choice.Delta, &delta); err != nil {
		return nil, core.Invalid(core.ProtocolChat, "$.choices[0].delta", "decode: %v", err)
	}
	if err := validateDeltaShape(choice.Delta, delta); err != nil {
		return nil, err
	}
	if delta.Role != "" && delta.Role != "assistant" {
		return nil, core.Invalid(core.ProtocolChat, "$.choices[0].delta.role", "expected assistant, got %q", delta.Role)
	}
	if c.finishReason != "" && (delta.ReasoningContent != nil || delta.Content != nil || delta.Refusal != nil || len(delta.ToolCalls) != 0) {
		return nil, core.UpstreamResponseError(core.ProtocolChat, "$.choices[0].delta", "received output after finish_reason")
	}
	if len(textLogprobs) > 0 && (delta.Content == nil || *delta.Content == "") {
		return nil, core.Invalid(core.ProtocolChat, "$.choices[0].logprobs.content", "token logprobs require a content delta")
	}

	var frames []core.Frame
	if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
		frames = append(frames, c.appendReasoning(*delta.ReasoningContent)...)
	}
	if delta.Content != nil && *delta.Content != "" {
		frames = append(frames, c.appendContent("output_text", *delta.Content, textLogprobs)...)
	}
	if delta.Refusal != nil && *delta.Refusal != "" {
		frames = append(frames, c.appendContent("refusal", *delta.Refusal, nil)...)
	}
	for _, call := range delta.ToolCalls {
		converted, err := c.appendTool(call)
		if err != nil {
			return nil, err
		}
		frames = append(frames, converted...)
	}
	if choice.FinishReason != nil {
		closed, err := c.observeFinish(*choice.FinishReason)
		if err != nil {
			return nil, err
		}
		frames = append(frames, closed...)
	}
	return frames, nil
}

func validateDeltaShape(raw json.RawMessage, delta chatDelta) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return core.Invalid(core.ProtocolChat, "$.choices[0].delta", "must be an object")
	}
	for name, value := range fields {
		switch name {
		case "role", "content", "refusal", "tool_calls", "reasoning_content":
		default:
			if meaningfulJSON(value) {
				return core.Unsupported(core.ProtocolChat, "$.choices[0].delta."+name, "delta field has no supported Responses mapping")
			}
		}
	}
	if meaningfulJSON(delta.FunctionCall) {
		return core.Unsupported(core.ProtocolChat, "$.choices[0].delta.function_call", "deprecated function_call is not supported")
	}
	return nil
}

func decodeChatLogprobs(raw json.RawMessage) ([]json.RawMessage, error) {
	if !meaningfulJSON(raw) {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope == nil {
		return nil, core.Invalid(core.ProtocolChat, "$.choices[0].logprobs", "must be an object")
	}
	for name, value := range envelope {
		if name != "content" && name != "refusal" && meaningfulJSON(value) {
			return nil, core.Unsupported(core.ProtocolChat, "$.choices[0].logprobs."+name, "unknown Chat logprobs field")
		}
	}
	content, err := decodeLogprobList(envelope["content"], "$.choices[0].logprobs.content")
	if err != nil {
		return nil, err
	}
	refusal, err := decodeLogprobList(envelope["refusal"], "$.choices[0].logprobs.refusal")
	if err != nil {
		return nil, err
	}
	if len(refusal) > 0 {
		return nil, core.Unsupported(core.ProtocolChat, "$.choices[0].logprobs.refusal", "Responses refusal events cannot preserve Chat refusal logprobs")
	}
	return content, nil
}

func decodeLogprobList(raw json.RawMessage, path string) ([]json.RawMessage, error) {
	if !meaningfulJSON(raw) {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, core.Invalid(core.ProtocolChat, path, "must be an array")
	}
	for index, entry := range entries {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(entry, &object); err != nil || object == nil {
			return nil, core.Invalid(core.ProtocolChat, fmt.Sprintf("%s[%d]", path, index), "must be an object")
		}
	}
	return entries, nil
}

func (c *Converter) appendReasoning(delta string) []core.Frame {
	reasoning := c.reasoning
	frames := make([]core.Frame, 0, 3)
	if reasoning == nil {
		reasoning = &reasoningState{
			id:          stableID("rs", c.upstreamID, 0),
			outputIndex: len(c.outputs),
		}
		c.reasoning = reasoning
		c.outputs = append(c.outputs, &outputSlot{reasoning: reasoning})
		frames = append(frames,
			c.event("response.output_item.added", map[string]any{
				"output_index": reasoning.outputIndex,
				"item":         c.reasoningSnapshot(reasoning, "in_progress"),
			}),
			c.event("response.reasoning_summary_part.added", map[string]any{
				"item_id": reasoning.id, "output_index": reasoning.outputIndex, "summary_index": 0,
				"part": map[string]any{"type": "summary_text", "text": ""},
			}),
		)
	}
	reasoning.text.WriteString(delta)
	frames = append(frames, c.event("response.reasoning_summary_text.delta", map[string]any{
		"item_id": reasoning.id, "output_index": reasoning.outputIndex, "summary_index": 0, "delta": delta,
	}))
	return frames
}

func (c *Converter) appendContent(kind, delta string, logprobs []json.RawMessage) []core.Frame {
	message, opened := c.ensureMessage()
	frames := make([]core.Frame, 0, 3)
	if opened {
		frames = append(frames, c.event("response.output_item.added", map[string]any{
			"output_index": message.outputIndex,
			"item":         c.messageSnapshot(message, "in_progress"),
		}))
	}
	part := message.text
	if kind == "refusal" {
		part = message.refusal
	}
	if part == nil {
		part = &contentState{kind: kind, index: len(message.parts)}
		message.parts = append(message.parts, part)
		if kind == "refusal" {
			message.refusal = part
		} else {
			message.text = part
		}
		frames = append(frames, c.event("response.content_part.added", map[string]any{
			"item_id": message.id, "output_index": message.outputIndex, "content_index": part.index,
			"part": c.partSnapshot(part),
		}))
	}
	part.value.WriteString(delta)
	part.logprobs = append(part.logprobs, logprobs...)
	eventName := "response.output_text.delta"
	payload := map[string]any{
		"item_id": message.id, "output_index": message.outputIndex, "content_index": part.index, "delta": delta,
	}
	if kind == "output_text" {
		payload["logprobs"] = nonNilLogprobs(logprobs)
	} else {
		eventName = "response.refusal.delta"
	}
	frames = append(frames, c.event(eventName, payload))
	return frames
}

func (c *Converter) ensureMessage() (*messageState, bool) {
	if c.message != nil {
		return c.message, false
	}
	c.message = &messageState{
		id:          stableID("msg", c.upstreamID, 0),
		outputIndex: len(c.outputs),
	}
	c.outputs = append(c.outputs, &outputSlot{message: c.message})
	return c.message, true
}

func (c *Converter) appendTool(delta chatToolDelta) ([]core.Frame, error) {
	if delta.Index == nil || *delta.Index < 0 {
		return nil, core.Invalid(core.ProtocolChat, "$.choices[0].delta.tool_calls[].index", "non-negative index is required")
	}
	if delta.Type != "" && delta.Type != "function" {
		return nil, core.Unsupported(core.ProtocolChat, "$.choices[0].delta.tool_calls[].type", "tool type %q is not a function", delta.Type)
	}
	tool := c.tools[*delta.Index]
	if tool == nil {
		tool = &toolState{outputIndex: -1}
		c.tools[*delta.Index] = tool
	}
	if tool.closed {
		return nil, core.Invalid(core.ProtocolChat, "$.choices[0].delta.tool_calls", "received data for a closed tool call")
	}
	if err := mergeIdentity(&tool.callID, delta.ID, "id"); err != nil {
		return nil, err
	}
	if err := mergeIdentity(&tool.name, delta.Function.Name, "function.name"); err != nil {
		return nil, err
	}
	var appended string
	if delta.Function.Arguments != nil {
		appended = *delta.Function.Arguments
		tool.arguments.WriteString(appended)
	}

	var frames []core.Frame
	if !tool.opened && tool.callID != "" && tool.name != "" {
		tool.opened = true
		tool.id = stableID("fc", tool.callID, *delta.Index)
		tool.outputIndex = len(c.outputs)
		c.outputs = append(c.outputs, &outputSlot{tool: tool})
		frames = append(frames, c.event("response.output_item.added", map[string]any{
			"output_index": tool.outputIndex,
			"item":         c.toolSnapshot(tool, "in_progress", ""),
		}))
		appended = tool.arguments.String()
	}
	if tool.opened && appended != "" {
		frames = append(frames, c.event("response.function_call_arguments.delta", map[string]any{
			"item_id": tool.id, "output_index": tool.outputIndex, "delta": appended,
		}))
	}
	return frames, nil
}

func mergeIdentity(current *string, incoming, field string) error {
	if incoming == "" {
		return nil
	}
	if *current != "" && *current != incoming {
		return core.Invalid(core.ProtocolChat, "$.choices[0].delta.tool_calls[]."+field, "changed during stream")
	}
	*current = incoming
	return nil
}

func (c *Converter) observeFinish(reason string) ([]core.Frame, error) {
	switch reason {
	case "stop", "tool_calls", "length", "content_filter":
	case "function_call":
		return nil, core.Unsupported(core.ProtocolChat, "$.choices[0].finish_reason", "deprecated function_call is not supported")
	default:
		return nil, core.Unsupported(core.ProtocolChat, "$.choices[0].finish_reason", "unknown finish reason %q", reason)
	}
	if c.finishReason != "" {
		if c.finishReason != reason {
			return nil, core.Invalid(core.ProtocolChat, "$.choices[0].finish_reason", "changed from %q to %q", c.finishReason, reason)
		}
		return nil, nil
	}
	c.finishReason = reason
	itemStatus := "completed"
	if reason == "length" || reason == "content_filter" {
		itemStatus = "incomplete"
	}
	return c.closeOutputs(itemStatus)
}

func (c *Converter) closeOutputs(status string) ([]core.Frame, error) {
	toolIndexes := make([]int, 0, len(c.tools))
	completedArguments := make(map[int]string, len(c.tools))
	for index, tool := range c.tools {
		if tool == nil || tool.closed {
			continue
		}
		if !tool.opened {
			return nil, core.Invalid(core.ProtocolChat, "$.choices[0].delta.tool_calls", "tool call %d never supplied both id and name", index)
		}
		arguments, err := completeArguments(tool.arguments.String())
		if err != nil {
			return nil, core.Invalid(core.ProtocolChat, "$.choices[0].delta.tool_calls", "tool call %d arguments: %v", index, err)
		}
		toolIndexes = append(toolIndexes, index)
		completedArguments[index] = arguments
	}
	sort.Ints(toolIndexes)

	var frames []core.Frame
	if reasoning := c.reasoning; reasoning != nil && !reasoning.closed {
		text := reasoning.text.String()
		frames = append(frames,
			c.event("response.reasoning_summary_text.done", map[string]any{
				"item_id": reasoning.id, "output_index": reasoning.outputIndex, "summary_index": 0, "text": text,
			}),
		)
		partDone := map[string]any{
			"item_id": reasoning.id, "output_index": reasoning.outputIndex, "summary_index": 0,
			"part": map[string]any{"type": "summary_text", "text": text},
		}
		if status == "incomplete" {
			partDone["status"] = "incomplete"
		}
		frames = append(frames, c.event("response.reasoning_summary_part.done", partDone))
		reasoning.closed = true
		frames = append(frames, c.event("response.output_item.done", map[string]any{
			"output_index": reasoning.outputIndex,
			"item":         c.reasoningSnapshot(reasoning, status),
		}))
	}
	if message := c.message; message != nil && !message.closed {
		for _, part := range message.parts {
			payload := map[string]any{
				"item_id": message.id, "output_index": message.outputIndex, "content_index": part.index,
			}
			if part.kind == "refusal" {
				payload["refusal"] = part.value.String()
				frames = append(frames, c.event("response.refusal.done", payload))
			} else {
				payload["text"] = part.value.String()
				payload["logprobs"] = nonNilLogprobs(part.logprobs)
				frames = append(frames, c.event("response.output_text.done", payload))
			}
			frames = append(frames, c.event("response.content_part.done", map[string]any{
				"item_id": message.id, "output_index": message.outputIndex, "content_index": part.index,
				"part": c.partSnapshot(part),
			}))
		}
		message.closed = true
		frames = append(frames, c.event("response.output_item.done", map[string]any{
			"output_index": message.outputIndex,
			"item":         c.messageSnapshot(message, status),
		}))
	}
	for _, index := range toolIndexes {
		tool := c.tools[index]
		arguments := completedArguments[index]
		tool.closed = true
		frames = append(frames,
			c.event("response.function_call_arguments.done", map[string]any{
				"item_id": tool.id, "output_index": tool.outputIndex, "name": tool.name, "arguments": arguments,
			}),
			c.event("response.output_item.done", map[string]any{
				"output_index": tool.outputIndex,
				"item":         c.toolSnapshot(tool, status, arguments),
			}),
		)
	}
	return frames, nil
}

func completeArguments(arguments string) (string, error) {
	if strings.TrimSpace(arguments) == "" {
		return "{}", nil
	}
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return "", err
	}
	if object == nil {
		return "", fmt.Errorf("must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("contains multiple JSON values")
		}
		return "", fmt.Errorf("trailing data: %w", err)
	}
	return arguments, nil
}

func (c *Converter) finishStream() ([]core.Frame, error) {
	if c.terminal {
		return nil, core.Invalid(core.ProtocolChat, "$", "stream already reached a terminal event")
	}
	if !c.sawNormalChunk || !c.started {
		return nil, core.UpstreamResponseError(core.ProtocolChat, "$", "stream ended before a completion chunk")
	}
	if c.finishReason == "" {
		return nil, core.UpstreamResponseError(core.ProtocolChat, "$.choices[0].finish_reason", "stream ended before a finish reason")
	}
	status := "completed"
	eventName := "response.completed"
	if c.finishReason == "length" || c.finishReason == "content_filter" {
		status = "incomplete"
		eventName = "response.incomplete"
	}
	c.terminal = true
	return []core.Frame{c.event(eventName, map[string]any{"response": c.responseObject(status, true)})}, nil
}

func (c *Converter) convertError(source *chatError) ([]core.Frame, error) {
	if source.Message == "" {
		return nil, core.Invalid(core.ProtocolChat, "$.error.message", "is required")
	}
	c.terminal = true
	return nil, core.UpstreamResponseError(core.ProtocolChat, "$.error", "%s", source.Message)
}

func (c *Converter) responseObject(status string, terminal bool) map[string]any {
	output := make([]any, 0, len(c.outputs))
	itemStatus := "in_progress"
	if terminal {
		itemStatus = "completed"
		if status == "incomplete" {
			itemStatus = "incomplete"
		}
	}
	for _, slot := range c.outputs {
		if slot.reasoning != nil {
			output = append(output, c.reasoningSnapshot(slot.reasoning, itemStatus))
		} else if slot.message != nil {
			output = append(output, c.messageSnapshot(slot.message, itemStatus))
		} else {
			arguments := slot.tool.arguments.String()
			if complete, err := completeArguments(arguments); err == nil && terminal {
				arguments = complete
			}
			output = append(output, c.toolSnapshot(slot.tool, itemStatus, arguments))
		}
	}
	response := map[string]any{
		"id": c.responseID, "object": "response", "created_at": c.createdAt,
		"status": status, "error": nil, "incomplete_details": nil,
		"model": c.displayModel(), "output": output,
		"parallel_tool_calls": true,
	}
	if terminal {
		response["usage"] = c.usageSnapshot()
	} else {
		response["usage"] = nil
	}
	if status == "incomplete" {
		reason := "max_output_tokens"
		if c.finishReason == "content_filter" {
			reason = "content_filter"
		}
		response["incomplete_details"] = map[string]any{"reason": reason}
	}
	return response
}

func (c *Converter) reasoningSnapshot(reasoning *reasoningState, status string) map[string]any {
	summary := make([]any, 0, 1)
	if reasoning.text.Len() > 0 {
		summary = append(summary, map[string]any{"type": "summary_text", "text": reasoning.text.String()})
	}
	return map[string]any{
		"id": reasoning.id, "type": "reasoning", "status": status,
		"summary": summary, "encrypted_content": nil,
	}
}

func (c *Converter) messageSnapshot(message *messageState, status string) map[string]any {
	content := make([]any, 0, len(message.parts))
	for _, part := range message.parts {
		content = append(content, c.partSnapshot(part))
	}
	return map[string]any{
		"id": message.id, "type": "message", "role": "assistant", "status": status, "content": content,
	}
}

func (c *Converter) partSnapshot(part *contentState) map[string]any {
	if part.kind == "refusal" {
		return map[string]any{"type": "refusal", "refusal": part.value.String()}
	}
	return map[string]any{"type": "output_text", "text": part.value.String(), "annotations": []any{}, "logprobs": nonNilLogprobs(part.logprobs)}
}

func (c *Converter) toolSnapshot(tool *toolState, status, arguments string) map[string]any {
	return map[string]any{
		"id": tool.id, "type": "function_call", "status": status,
		"call_id": tool.callID, "name": tool.name, "arguments": arguments,
	}
}

func (c *Converter) usageSnapshot() any {
	if c.usage == nil {
		return nil
	}
	return map[string]any{
		"input_tokens": c.usage.PromptTokens,
		"input_tokens_details": map[string]any{
			"cached_tokens": c.usage.PromptDetails.CachedTokens,
		},
		"output_tokens": c.usage.CompletionTokens,
		"output_tokens_details": map[string]any{
			"reasoning_tokens": c.usage.CompletionDetails.ReasoningTokens,
		},
		"total_tokens": c.usage.TotalTokens,
	}
}

func (c *Converter) displayModel() string {
	if c.options.ClientModel != "" {
		return c.options.ClientModel
	}
	return c.providerModel
}

func (c *Converter) event(eventType string, fields map[string]any) core.Frame {
	fields["type"] = eventType
	fields["sequence_number"] = c.sequence
	c.sequence++
	data, _ := json.Marshal(fields)
	return core.Frame{Event: eventType, Data: data}
}

func stableID(prefix, source string, index int) string {
	var cleaned strings.Builder
	for _, char := range source {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_' {
			cleaned.WriteRune(char)
		}
	}
	value := strings.TrimPrefix(cleaned.String(), "chatcmpl-")
	if value == "" {
		value = "stream"
	}
	if len(value) > 48 {
		value = value[:48]
	}
	return fmt.Sprintf("%s_%s_%d", prefix, value, index)
}

func meaningfulJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func nonNilLogprobs(logprobs []json.RawMessage) []json.RawMessage {
	if logprobs == nil {
		return []json.RawMessage{}
	}
	return logprobs
}

var _ core.ResponseStream = (*Converter)(nil)
