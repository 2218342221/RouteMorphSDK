package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

// validatingSSEBody preserves the upstream SSE representation while validating
// each complete event before exposing it. It buffers at most one bounded frame,
// so native routes remain incremental without trusting malformed 2xx streams.
type validatingSSEBody struct {
	ctx       context.Context
	body      io.ReadCloser
	reader    *bufio.Reader
	validator nativeStreamValidator
	maxBytes  int
	pending   []byte
	stickyErr error
	eof       bool
}

func newValidatingSSEBody(ctx context.Context, body io.ReadCloser, protocol core.Protocol, wire core.Codec, maxFrameBytes int64) io.ReadCloser {
	limit := int(maxFrameBytes)
	if limit <= 0 {
		limit = 4 << 20
	}
	return &validatingSSEBody{
		ctx: ctx, body: body, reader: bufio.NewReaderSize(body, 64<<10),
		validator: nativeStreamValidator{protocol: protocol, wire: wire},
		maxBytes:  limit,
	}
}

func (b *validatingSSEBody) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if len(b.pending) > 0 {
		return b.copyPending(destination), nil
	}
	if b.stickyErr != nil {
		return 0, b.stickyErr
	}
	if b.eof {
		return 0, io.EOF
	}
	for {
		event, physicalEOF, err := b.nextEvent()
		if err != nil {
			return 0, b.remember(classifyStreamUpstreamError(err))
		}
		if event.hasFrame {
			if err := b.validator.validate(b.ctx, event.frame); err != nil {
				return 0, b.remember(classifyStreamUpstreamError(err))
			}
		}
		if physicalEOF {
			b.eof = true
			if err := b.validator.finalize(); err != nil {
				b.stickyErr = classifyStreamUpstreamError(err)
			}
		}
		if len(event.raw) > 0 {
			b.pending = event.raw
			return b.copyPending(destination), nil
		}
		if b.stickyErr != nil {
			return 0, b.stickyErr
		}
		if b.eof {
			return 0, io.EOF
		}
	}
}

func (b *validatingSSEBody) copyPending(destination []byte) int {
	n := copy(destination, b.pending)
	b.pending = b.pending[n:]
	return n
}

func (b *validatingSSEBody) remember(err error) error {
	b.stickyErr = err
	return err
}

func (b *validatingSSEBody) Close() error {
	if b.body == nil {
		return errors.New("response body is nil")
	}
	return b.body.Close()
}

type rawSSEEvent struct {
	raw      []byte
	frame    core.Frame
	hasFrame bool
}

func (b *validatingSSEBody) nextEvent() (rawSSEEvent, bool, error) {
	var raw bytes.Buffer
	var data strings.Builder
	eventName := ""
	sawData := false
	for {
		if err := b.ctx.Err(); err != nil {
			return rawSSEEvent{}, false, err
		}
		line, err := readBoundedSSELine(b.reader, b.maxBytes-raw.Len(), b.maxBytes, b.validator.protocol)
		if len(line) > 0 {
			raw.Write(line)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return rawSSEEvent{}, false, err
		}
		if len(line) == 0 && errors.Is(err, io.EOF) {
			if raw.Len() == 0 {
				return rawSSEEvent{}, true, nil
			}
			return makeRawSSEEvent(raw.Bytes(), eventName, data.String(), sawData), true, nil
		}

		text := strings.TrimSuffix(string(line), "\n")
		text = strings.TrimSuffix(text, "\r")
		if text == "" {
			return makeRawSSEEvent(raw.Bytes(), eventName, data.String(), sawData), errors.Is(err, io.EOF), nil
		}
		if !strings.HasPrefix(text, ":") {
			field, value, found := strings.Cut(text, ":")
			if found {
				value = strings.TrimPrefix(value, " ")
			}
			switch field {
			case "event":
				eventName = value
			case "data":
				sawData = true
				data.WriteString(value)
				data.WriteByte('\n')
			}
		}
		if errors.Is(err, io.EOF) {
			return makeRawSSEEvent(raw.Bytes(), eventName, data.String(), sawData), true, nil
		}
	}
}

func makeRawSSEEvent(raw []byte, eventName, data string, sawData bool) rawSSEEvent {
	payload := strings.TrimSuffix(data, "\n")
	return rawSSEEvent{
		raw:      raw,
		frame:    core.Frame{Event: eventName, Data: []byte(payload), Done: payload == "[DONE]"},
		hasFrame: sawData || eventName != "",
	}
}

func readBoundedSSELine(reader *bufio.Reader, remaining, limit int, protocol core.Protocol) ([]byte, error) {
	if remaining <= 0 {
		if _, err := reader.Peek(1); errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, core.UpstreamResponseError(protocol, "$", "SSE frame exceeds %d bytes", limit)
	}
	line := make([]byte, 0, min(remaining, 4096))
	for {
		if len(line) == remaining {
			if _, err := reader.Peek(1); errors.Is(err, io.EOF) {
				return line, io.EOF
			}
			return nil, core.UpstreamResponseError(protocol, "$", "SSE frame exceeds %d bytes", limit)
		}
		character, err := reader.ReadByte()
		if err != nil {
			return line, err
		}
		line = append(line, character)
		switch character {
		case '\n':
			return line, nil
		case '\r':
			next, peekErr := reader.Peek(1)
			if peekErr == nil && next[0] == '\n' {
				if len(line) == remaining {
					return nil, core.UpstreamResponseError(protocol, "$", "SSE frame exceeds %d bytes", limit)
				}
				lineFeed, readErr := reader.ReadByte()
				if readErr != nil {
					return line, readErr
				}
				line = append(line, lineFeed)
			}
			if errors.Is(peekErr, io.EOF) {
				return line, io.EOF
			}
			if peekErr != nil {
				return line, peekErr
			}
			return line, nil
		}
	}
}

type nativeStreamValidator struct {
	protocol core.Protocol
	wire     core.Codec

	sawPayload  bool
	sawTerminal bool
	done        bool

	chatChoices   map[int]bool
	chatSawChoice bool

	messageStarted    bool
	messageStopped    bool
	messageBlocks     map[int]string
	messageSeenBlocks map[int]struct{}

	geminiCandidates   map[int]bool
	geminiSawCandidate bool
}

func (v *nativeStreamValidator) validate(ctx context.Context, frame core.Frame) error {
	if v.done {
		return core.Invalid(v.protocol, "$", "stream event arrived after the terminal marker")
	}
	if frame.Done || string(frame.Data) == "[DONE]" {
		if v.protocol != core.ProtocolChat && !(v.protocol == core.ProtocolResponses && v.sawTerminal) {
			return core.Invalid(v.protocol, "$", "unexpected [DONE] terminal marker")
		}
		if v.protocol == core.ProtocolChat && !v.sawTerminal {
			return core.Invalid(v.protocol, "$", "[DONE] arrived before a finish_reason")
		}
		v.done = true
		return nil
	}

	object, err := decodeNativeStreamObject(v.protocol, frame.Data)
	if err != nil {
		return err
	}
	v.sawPayload = true
	if rawJSONPresent(object["error"]) {
		if err := validateNativeProtocolError(v.protocol, object["error"]); err != nil {
			return err
		}
		return core.UpstreamResponseError(v.protocol, "$.error", "successful HTTP stream contains a protocol error")
	}

	switch v.protocol {
	case core.ProtocolChat:
		return v.validateChat(frame.Data, object)
	case core.ProtocolResponses:
		return v.validateResponses(ctx, frame, object)
	case core.ProtocolMessages:
		return v.validateMessages(frame, frame.Data, object)
	case core.ProtocolGenerateContent:
		return v.validateGemini(frame.Data, object)
	default:
		return core.Invalid(v.protocol, "$", "unsupported stream protocol")
	}
}

func (v *nativeStreamValidator) validateChat(data []byte, object map[string]json.RawMessage) error {
	raw, exists := object["choices"]
	if !exists || !rawJSONArray(raw) {
		return core.Invalid(v.protocol, "$.choices", "choices must be an array")
	}
	var chunk nativeChatChunk
	if err := decodeKnownNativeFields(v.protocol, data, &chunk); err != nil {
		return err
	}
	if err := validateKnownChatContainers(data); err != nil {
		return err
	}
	if v.chatChoices == nil {
		v.chatChoices = make(map[int]bool)
	}
	seen := make(map[int]struct{}, len(chunk.Choices))
	for _, choice := range chunk.Choices {
		if choice == nil || choice.Index == nil || choice.Delta == nil {
			return core.Invalid(v.protocol, "$.choices[]", "choice requires an integer index and delta object")
		}
		index := *choice.Index
		if index < 0 {
			return core.Invalid(v.protocol, "$.choices[].index", "choice index must not be negative")
		}
		if _, duplicate := seen[index]; duplicate {
			return core.Invalid(v.protocol, "$.choices[].index", "duplicate choice index %d in one chunk", index)
		}
		seen[index] = struct{}{}
		if v.chatChoices[index] {
			return core.Invalid(v.protocol, "$.choices", "choice %d arrived after its terminal chunk", index)
		}
		if choice.Delta.Role != "" && choice.Delta.Role != "assistant" {
			return core.UpstreamResponseError(v.protocol, "$.choices[].delta.role", "unexpected role %q", choice.Delta.Role)
		}
		for _, call := range choice.Delta.ToolCalls {
			if call == nil || call.Index == nil {
				return core.Invalid(v.protocol, "$.choices[].delta.tool_calls[]", "tool call requires an integer index")
			}
			if *call.Index < 0 {
				return core.Invalid(v.protocol, "$.choices[].delta.tool_calls[].index", "tool-call index must not be negative")
			}
			if call.Type != "" && call.Type != "function" {
				return core.UpstreamResponseError(v.protocol, "$.choices[].delta.tool_calls[].type", "unsupported tool-call type %q", call.Type)
			}
		}
		v.chatSawChoice = true
		if _, exists := v.chatChoices[index]; !exists {
			v.chatChoices[index] = false
		}
		if choice.FinishReason != "" {
			v.chatChoices[index] = true
		}
	}
	v.sawTerminal = v.chatSawChoice
	for _, finished := range v.chatChoices {
		v.sawTerminal = v.sawTerminal && finished
	}
	return nil
}

func (v *nativeStreamValidator) validateResponses(ctx context.Context, frame core.Frame, object map[string]json.RawMessage) error {
	eventType := rawJSONString(object["type"])
	if eventType == "" {
		return core.Invalid(v.protocol, "$.type", "stream event type is required")
	}
	if frame.Event != "" && frame.Event != eventType {
		return core.Invalid(v.protocol, "$.type", "SSE event %q does not match payload type %q", frame.Event, eventType)
	}
	if err := validateKnownResponsesEvent(frame.Data, eventType); err != nil {
		return err
	}
	if v.sawTerminal {
		return core.Invalid(v.protocol, "$.type", "event %q arrived after the terminal response", eventType)
	}
	switch eventType {
	case "error", "response.failed", "response.cancelled":
		return core.UpstreamResponseError(v.protocol, "$", "Responses stream returned %q", eventType)
	case "response.completed", "response.incomplete":
		raw := object["response"]
		if !rawJSONObject(raw) {
			return core.Invalid(v.protocol, "$.response", "terminal response object is required")
		}
		var terminal struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(raw, &terminal); err != nil {
			return core.Invalid(v.protocol, "$.response", "invalid terminal response: %v", err)
		}
		wantStatus := strings.TrimPrefix(eventType, "response.")
		if terminal.Status != wantStatus {
			return core.Invalid(v.protocol, "$.response.status", "terminal event %q does not match status %q", eventType, terminal.Status)
		}
		if err := v.wire.ValidateResponse(ctx, raw); err != nil {
			return err
		}
		v.sawTerminal = true
	}
	return nil
}

func (v *nativeStreamValidator) validateMessages(frame core.Frame, data []byte, object map[string]json.RawMessage) error {
	var event nativeMessagesEvent
	if err := decodeKnownNativeFields(v.protocol, data, &event); err != nil {
		return err
	}
	eventType := event.Type
	if eventType == "" {
		return core.Invalid(v.protocol, "$.type", "stream event type is required")
	}
	if frame.Event != "" && frame.Event != eventType {
		return core.Invalid(v.protocol, "$.type", "SSE event %q does not match payload type %q", frame.Event, eventType)
	}
	if v.messageStopped || (v.sawTerminal && eventType != "message_stop" && eventType != "ping") {
		return core.Invalid(v.protocol, "$.type", "event %q arrived after terminal state", eventType)
	}
	switch eventType {
	case "error":
		return core.UpstreamResponseError(v.protocol, "$.error", "Messages stream returned an error event")
	case "message_start":
		if v.messageStarted {
			return core.Invalid(v.protocol, "$.type", "duplicate message_start event")
		}
		messageRaw := object["message"]
		if !rawJSONObject(messageRaw) {
			return core.Invalid(v.protocol, "$.message", "message_start requires a message object")
		}
		message := event.Message
		if message == nil || message.Content == nil || message.Usage == nil {
			return core.Invalid(v.protocol, "$.message", "message_start requires content and usage")
		}
		if message.ID == "" || message.Model == "" || message.Type != "message" || message.Role != "assistant" {
			return core.Invalid(v.protocol, "$.message", "message_start requires an assistant message with id and model")
		}
		for _, content := range *message.Content {
			if !rawJSONObject(content) {
				return core.Invalid(v.protocol, "$.message.content[]", "content block must be an object")
			}
		}
		if err := validateMessagesUsage(v.protocol, "$.message.usage", message.Usage); err != nil {
			return err
		}
		if err := validateOptionalNullableString(v.protocol, "$.message.stop_reason", message.StopReason); err != nil {
			return err
		}
		if err := validateOptionalNullableString(v.protocol, "$.message.stop_sequence", message.StopSequence); err != nil {
			return err
		}
		v.messageStarted = true
	case "content_block_start":
		if !v.messageStarted || v.sawTerminal || event.Index == nil || *event.Index < 0 {
			return core.Invalid(v.protocol, "$.index", "invalid content block start")
		}
		index := *event.Index
		if v.messageBlocks == nil {
			v.messageBlocks = make(map[int]string)
		}
		if v.messageSeenBlocks == nil {
			v.messageSeenBlocks = make(map[int]struct{})
		}
		if _, duplicate := v.messageSeenBlocks[index]; duplicate {
			return core.Invalid(v.protocol, "$.index", "duplicate content block index %d", index)
		}
		blockRaw := object["content_block"]
		if !rawJSONObject(blockRaw) {
			return core.Invalid(v.protocol, "$.content_block", "content_block_start requires a content block object")
		}
		block := event.ContentBlock
		if block == nil {
			return core.Invalid(v.protocol, "$.content_block", "content_block_start requires a content block object")
		}
		if block.Type == "" {
			return core.Invalid(v.protocol, "$.content_block.type", "content block type is required")
		}
		if (block.Type == "tool_use" || block.Type == "server_tool_use" || block.Type == "mcp_tool_use") && rawJSONPresent(block.Input) && !rawJSONObject(block.Input) {
			return core.Invalid(v.protocol, "$.content_block.input", "tool input must be an object")
		}
		if err := validateMessagesBlockPayload(v.protocol, block); err != nil {
			return err
		}
		v.messageBlocks[index] = block.Type
		v.messageSeenBlocks[index] = struct{}{}
	case "content_block_delta":
		if event.Index == nil {
			return core.Invalid(v.protocol, "$.index", "content block index is required")
		}
		index := *event.Index
		blockType, open := v.messageBlocks[index]
		if !open {
			return core.Invalid(v.protocol, "$.index", "content block delta arrived before its start")
		}
		deltaRaw := object["delta"]
		if !rawJSONObject(deltaRaw) {
			return core.Invalid(v.protocol, "$.delta", "content_block_delta requires a delta object")
		}
		delta := event.Delta
		if delta == nil {
			return core.Invalid(v.protocol, "$.delta", "content_block_delta requires a delta object")
		}
		if delta.Type == "" {
			return core.Invalid(v.protocol, "$.delta.type", "delta type is required")
		}
		if !messagesDeltaMatchesBlock(delta.Type, blockType) {
			return core.Invalid(v.protocol, "$.delta.type", "%s does not match content block type %q", delta.Type, blockType)
		}
		if err := validateMessagesDeltaPayload(v.protocol, delta); err != nil {
			return err
		}
	case "content_block_stop":
		if event.Index == nil {
			return core.Invalid(v.protocol, "$.index", "content block index is required")
		}
		index := *event.Index
		if _, open := v.messageBlocks[index]; !open {
			return core.Invalid(v.protocol, "$.index", "content block stop arrived before its start")
		}
		delete(v.messageBlocks, index)
	case "message_delta":
		if !v.messageStarted || v.sawTerminal || len(v.messageBlocks) != 0 {
			return core.Invalid(v.protocol, "$.type", "invalid terminal message_delta")
		}
		deltaRaw := object["delta"]
		if !rawJSONObject(deltaRaw) {
			return core.Invalid(v.protocol, "$.delta", "message_delta requires a delta object")
		}
		delta := event.Delta
		if delta == nil {
			return core.Invalid(v.protocol, "$.delta", "message_delta requires a delta object")
		}
		stopReason, err := requireJSONString(v.protocol, "$.delta.stop_reason", delta.StopReason)
		if err != nil {
			return err
		}
		if err := validateOptionalNullableString(v.protocol, "$.delta.stop_sequence", delta.StopSequence); err != nil {
			return err
		}
		stopSequence := rawJSONString(delta.StopSequence)
		if stopReason == "stop_sequence" && stopSequence == "" {
			return core.UpstreamResponseError(v.protocol, "$.delta.stop_sequence", "stop_sequence is required")
		}
		if err := validateMessagesUsage(v.protocol, "$.usage", event.Usage); err != nil {
			return err
		}
		v.sawTerminal = true
	case "message_stop":
		if !v.messageStarted || !v.sawTerminal || len(v.messageBlocks) != 0 {
			return core.Invalid(v.protocol, "$.type", "message_stop arrived before the stream was complete")
		}
		v.messageStopped = true
	case "ping":
	default:
		// Unknown native events are forwarded so provider extensions do not
		// require an SDK release. Known events still receive strict validation.
	}
	return nil
}

func messagesDeltaMatchesBlock(deltaType, blockType string) bool {
	switch deltaType {
	case "text_delta", "citations_delta":
		return blockType == "text"
	case "thinking_delta", "signature_delta":
		return blockType == "thinking"
	case "input_json_delta":
		return blockType == "tool_use" || blockType == "server_tool_use" || blockType == "mcp_tool_use"
	default:
		return true
	}
}

func (v *nativeStreamValidator) validateGemini(data []byte, object map[string]json.RawMessage) error {
	if err := validateKnownGeminiChunk(data); err != nil {
		return err
	}
	candidatesRaw, hasCandidates := object["candidates"]
	usagePresent := rawJSONPresent(object["usageMetadata"])
	promptFeedbackPresent := rawJSONPresent(object["promptFeedback"])
	if promptFeedbackPresent {
		var feedback struct {
			BlockReason string `json:"blockReason"`
		}
		if err := json.Unmarshal(object["promptFeedback"], &feedback); err != nil {
			return core.Invalid(v.protocol, "$.promptFeedback", "invalid prompt feedback: %v", err)
		}
		if feedback.BlockReason != "" && feedback.BlockReason != "BLOCK_REASON_UNSPECIFIED" {
			return core.UpstreamResponseError(v.protocol, "$.promptFeedback.blockReason", "request blocked by Gemini API: %s", feedback.BlockReason)
		}
	}
	if !hasCandidates && !usagePresent {
		return core.Invalid(v.protocol, "$", "Gemini stream chunk has neither candidates nor usageMetadata")
	}
	if hasCandidates {
		if !rawJSONArray(candidatesRaw) {
			return core.Invalid(v.protocol, "$.candidates", "candidates must be an array")
		}
		var candidates []struct {
			Index        int    `json:"index"`
			FinishReason string `json:"finishReason"`
		}
		if err := json.Unmarshal(candidatesRaw, &candidates); err != nil {
			return core.Invalid(v.protocol, "$.candidates", "candidates must be an array: %v", err)
		}
		if v.geminiCandidates == nil {
			v.geminiCandidates = make(map[int]bool)
		}
		seen := make(map[int]struct{}, len(candidates))
		for _, candidate := range candidates {
			if candidate.Index < 0 {
				return core.Invalid(v.protocol, "$.candidates[].index", "candidate index must not be negative")
			}
			if _, duplicate := seen[candidate.Index]; duplicate {
				return core.Invalid(v.protocol, "$.candidates[].index", "duplicate candidate index %d in one chunk", candidate.Index)
			}
			seen[candidate.Index] = struct{}{}
			if v.geminiCandidates[candidate.Index] {
				return core.Invalid(v.protocol, "$.candidates", "candidate %d arrived after its terminal chunk", candidate.Index)
			}
			v.geminiSawCandidate = true
			if _, exists := v.geminiCandidates[candidate.Index]; !exists {
				v.geminiCandidates[candidate.Index] = false
			}
			if candidate.FinishReason != "" && candidate.FinishReason != "FINISH_REASON_UNSPECIFIED" {
				if err := validateGeminiFinishReason(v.protocol, candidate.FinishReason); err != nil {
					return err
				}
				v.geminiCandidates[candidate.Index] = true
			}
		}
		v.sawTerminal = v.geminiSawCandidate
		for _, finished := range v.geminiCandidates {
			v.sawTerminal = v.sawTerminal && finished
		}
	}
	return nil
}

func validateGeminiFinishReason(protocol core.Protocol, reason string) error {
	switch reason {
	case "STOP", "MAX_TOKENS", "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "RECITATION", "LANGUAGE", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_OTHER", "NO_IMAGE", "IMAGE_RECITATION":
		return nil
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "TOO_MANY_TOOL_CALLS", "MISSING_THOUGHT_SIGNATURE", "OTHER":
		return core.UpstreamResponseError(protocol, "$.candidates[].finishReason", "generation failed with %q", reason)
	default:
		// Unknown non-empty reasons are terminal for native pass-through. Known
		// provider failure reasons above still surface as upstream errors.
		return nil
	}
}

func (v *nativeStreamValidator) finalize() error {
	if !v.sawPayload {
		return core.Invalid(v.protocol, "$", "stream ended before a protocol payload")
	}
	if v.protocol == core.ProtocolMessages && !v.messageStopped {
		return core.Invalid(v.protocol, "$", "stream ended before message_stop")
	}
	if !v.sawTerminal {
		return core.Invalid(v.protocol, "$", "stream ended before a terminal event")
	}
	return nil
}

func decodeNativeStreamObject(protocol core.Protocol, data []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("JSON value is not an object")
		}
		return nil, core.Invalid(protocol, "$", "invalid stream event: %v", err)
	}
	return object, nil
}

func rawJSONPresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func rawJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func rawJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func rawJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
