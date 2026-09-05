package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
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

	responsesCreated      bool
	responsesNextSequence int64
	responsesItems        map[int]*nativeResponsesItemState
	responsesItemIDs      map[string]int
	responsesStreams      map[string]bool
}

type nativeResponsesItemState struct {
	id          string
	itemType    string
	done        bool
	doneItem    json.RawMessage
	content     map[int]*nativeResponsesPartState
	summary     map[int]*nativeResponsesPartState
	streams     map[string]bool
	streamSeen  map[string]bool
	commands    map[int]bool
	shellOutput map[int]bool
}

type nativeResponsesPartState struct {
	partType   string
	done       bool
	streamSeen bool
	streamDone bool
}

func (v *nativeStreamValidator) validate(ctx context.Context, frame core.Frame) error {
	if v.done {
		return core.Invalid(v.protocol, "$", "stream event arrived after the terminal marker")
	}
	if frame.Done || string(frame.Data) == "[DONE]" {
		if v.protocol != core.ProtocolChat {
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
	if eventType == "response.created" {
		if v.responsesCreated {
			return core.Invalid(v.protocol, "$.type", "duplicate response.created event")
		}
	} else if !v.responsesCreated {
		return core.Invalid(v.protocol, "$.type", "response.created must be the first stream event")
	}

	var event nativeResponsesEvent
	if err := decodeKnownNativeFields(v.protocol, frame.Data, &event); err != nil {
		return err
	}
	if event.SequenceNumber == nil {
		return core.Invalid(v.protocol, "$.sequence_number", "stream event sequence number is required")
	}
	if *event.SequenceNumber != v.responsesNextSequence {
		return core.Invalid(v.protocol, "$.sequence_number", "got %d, want the next contiguous sequence number %d", *event.SequenceNumber, v.responsesNextSequence)
	}
	if err := v.validateResponsesLifecycleEvent(eventType, &event); err != nil {
		return err
	}
	if eventType == "response.output_item.done" {
		if err := validateCompleteResponsesItem(event.Item); err != nil {
			return err
		}
	}

	switch eventType {
	case "error":
		return core.UpstreamResponseError(v.protocol, "$", "Responses stream returned %q", eventType)
	case "response.completed", "response.incomplete", "response.failed", "response.cancelled":
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
		if eventType == "response.completed" || eventType == "response.incomplete" {
			if err := v.validateResponsesItemsClosed(); err != nil {
				return err
			}
			for stream, done := range v.responsesStreams {
				if !done {
					return core.Invalid(v.protocol, "$.type", "terminal response arrived while %s stream is still open", stream)
				}
			}
		}
		if eventType == "response.completed" || eventType == "response.incomplete" {
			if err := v.wire.ValidateResponse(ctx, raw); err != nil {
				return err
			}
			if err := v.validateResponsesTerminalOutput(raw); err != nil {
				return err
			}
		} else {
			var terminalObject map[string]json.RawMessage
			if err := json.Unmarshal(raw, &terminalObject); err != nil || !rawJSONArray(terminalObject["output"]) {
				return core.Invalid(v.protocol, "$.response.output", "terminal response output array is required")
			}
		}
		v.sawTerminal = true
		v.responsesNextSequence++
		if eventType == "response.failed" || eventType == "response.cancelled" {
			return core.UpstreamResponseError(v.protocol, "$", "Responses stream returned %q", eventType)
		}
		return nil
	}
	if eventType == "response.created" {
		v.responsesCreated = true
	}
	v.responsesNextSequence++
	return nil
}

func (v *nativeStreamValidator) validateResponsesLifecycleEvent(eventType string, event *nativeResponsesEvent) error {
	switch eventType {
	case "response.output_item.added":
		return v.addResponsesItem(event)
	case "response.output_item.done":
		return v.finishResponsesItem(event)
	case "response.content_part.added":
		return v.addResponsesContentPart(event)
	case "response.content_part.done":
		return v.finishResponsesContentPart(event)
	case "response.reasoning_summary_part.added":
		return v.addResponsesSummaryPart(event)
	case "response.reasoning_summary_part.done":
		return v.finishResponsesSummaryPart(event)
	case "response.output_text.delta":
		return v.validateResponsesContentStream(event, "output_text", false)
	case "response.output_text.done":
		return v.validateResponsesContentStream(event, "output_text", true)
	case "response.output_text.annotation.added":
		return v.validateResponsesContentStream(event, "output_text", false)
	case "response.refusal.delta":
		return v.validateResponsesContentStream(event, "refusal", false)
	case "response.refusal.done":
		return v.validateResponsesContentStream(event, "refusal", true)
	case "response.reasoning_text.delta":
		return v.validateResponsesContentStream(event, "reasoning_text", false)
	case "response.reasoning_text.done":
		return v.validateResponsesContentStream(event, "reasoning_text", true)
	case "response.reasoning_summary_text.delta":
		return v.validateResponsesSummaryStream(event, false)
	case "response.reasoning_summary_text.done":
		return v.validateResponsesSummaryStream(event, true)
	case "response.function_call_arguments.delta":
		return v.validateResponsesItemStream(event, "function_call_arguments", false, "function_call")
	case "response.function_call_arguments.done":
		return v.validateResponsesItemStream(event, "function_call_arguments", true, "function_call")
	case "response.custom_tool_call_input.delta":
		return v.validateResponsesItemStream(event, "custom_tool_call_input", false, "custom_tool_call")
	case "response.custom_tool_call_input.done":
		return v.validateResponsesItemStream(event, "custom_tool_call_input", true, "custom_tool_call")
	case "response.code_interpreter_call_code.delta":
		return v.validateResponsesItemStream(event, "code_interpreter_call_code", false, "code_interpreter_call")
	case "response.code_interpreter_call_code.done":
		return v.validateResponsesItemStream(event, "code_interpreter_call_code", true, "code_interpreter_call")
	case "response.mcp_call_arguments.delta":
		return v.validateResponsesItemStream(event, "mcp_call_arguments", false, "mcp_call")
	case "response.mcp_call_arguments.done":
		return v.validateResponsesItemStream(event, "mcp_call_arguments", true, "mcp_call")
	case "response.code_interpreter_call.in_progress", "response.code_interpreter_call.interpreting":
		return v.validateResponsesItemStream(event, "code_interpreter_call_status", false, "code_interpreter_call")
	case "response.code_interpreter_call.completed":
		return v.validateResponsesItemStream(event, "code_interpreter_call_status", true, "code_interpreter_call")
	case "response.file_search_call.in_progress", "response.file_search_call.searching":
		return v.validateResponsesItemStream(event, "file_search_call_status", false, "file_search_call")
	case "response.file_search_call.completed":
		return v.validateResponsesItemStream(event, "file_search_call_status", true, "file_search_call")
	case "response.web_search_call.in_progress", "response.web_search_call.searching":
		return v.validateResponsesItemStream(event, "web_search_call_status", false, "web_search_call")
	case "response.web_search_call.completed":
		return v.validateResponsesItemStream(event, "web_search_call_status", true, "web_search_call")
	case "response.image_generation_call.in_progress", "response.image_generation_call.generating", "response.image_generation_call.partial_image":
		return v.validateResponsesItemStream(event, "image_generation_call_status", false, "image_generation_call")
	case "response.image_generation_call.completed":
		return v.validateResponsesItemStream(event, "image_generation_call_status", true, "image_generation_call")
	case "response.mcp_call.in_progress":
		return v.validateResponsesItemStream(event, "mcp_call_status", false, "mcp_call")
	case "response.mcp_call.completed", "response.mcp_call.failed":
		return v.validateResponsesItemStream(event, "mcp_call_status", true, "mcp_call")
	case "response.mcp_list_tools.in_progress":
		return v.validateResponsesItemStream(event, "mcp_list_tools_status", false, "mcp_list_tools")
	case "response.mcp_list_tools.completed", "response.mcp_list_tools.failed":
		return v.validateResponsesItemStream(event, "mcp_list_tools_status", true, "mcp_list_tools")
	case "response.shell_call_command.added", "response.shell_call_command.delta", "response.shell_call_command.done":
		return v.validateResponsesShellCommand(eventType, event)
	case "response.shell_call_output_content.delta", "response.shell_call_output_content.done":
		return v.validateResponsesShellOutput(eventType, event)
	case "response.audio.delta":
		return v.validateResponsesGlobalStream("audio", false)
	case "response.audio.done":
		return v.validateResponsesGlobalStream("audio", true)
	case "response.audio.transcript.delta":
		return v.validateResponsesGlobalStream("audio_transcript", false)
	case "response.audio.transcript.done":
		return v.validateResponsesGlobalStream("audio_transcript", true)
	default:
		return nil
	}
}

func (v *nativeStreamValidator) addResponsesItem(event *nativeResponsesEvent) error {
	identity, err := decodeResponsesItemIdentity(v.protocol, event.Item)
	if err != nil {
		return err
	}
	if event.OutputIndex == nil {
		return core.Invalid(v.protocol, "$.output_index", "output item index is required")
	}
	if v.responsesItems == nil {
		v.responsesItems = make(map[int]*nativeResponsesItemState)
		v.responsesItemIDs = make(map[string]int)
	}
	if _, exists := v.responsesItems[*event.OutputIndex]; exists {
		return core.Invalid(v.protocol, "$.output_index", "duplicate output item index %d", *event.OutputIndex)
	}
	if index, exists := v.responsesItemIDs[identity.id]; exists {
		return core.Invalid(v.protocol, "$.item.id", "duplicate output item id %q already belongs to output index %d", identity.id, index)
	}
	v.responsesItems[*event.OutputIndex] = &nativeResponsesItemState{
		id: identity.id, itemType: identity.itemType,
		content: make(map[int]*nativeResponsesPartState), summary: make(map[int]*nativeResponsesPartState),
		streams: make(map[string]bool), streamSeen: make(map[string]bool), commands: make(map[int]bool), shellOutput: make(map[int]bool),
	}
	v.responsesItemIDs[identity.id] = *event.OutputIndex
	return nil
}

func (v *nativeStreamValidator) finishResponsesItem(event *nativeResponsesEvent) error {
	identity, err := decodeResponsesItemIdentity(v.protocol, event.Item)
	if err != nil {
		return err
	}
	item, err := v.openResponsesItem(event.OutputIndex, identity.id)
	if err != nil {
		return err
	}
	if identity.itemType != item.itemType {
		return core.Invalid(v.protocol, "$.item.type", "output item type %q does not match added type %q", identity.itemType, item.itemType)
	}
	for index, part := range item.content {
		if !part.done {
			return core.Invalid(v.protocol, "$.output_index", "output item completed while content index %d is still open", index)
		}
	}
	for index, part := range item.summary {
		if !part.done {
			return core.Invalid(v.protocol, "$.output_index", "output item completed while summary index %d is still open", index)
		}
	}
	for stream, seen := range item.streamSeen {
		if seen && !item.streams[stream] && responsesItemStreamRequiresDone(stream) {
			return core.Invalid(v.protocol, "$.output_index", "output item completed while %s stream is still open", stream)
		}
	}
	for index, done := range item.commands {
		if !done {
			return core.Invalid(v.protocol, "$.output_index", "output item completed while shell command index %d is still open", index)
		}
	}
	for index, done := range item.shellOutput {
		if !done {
			return core.Invalid(v.protocol, "$.output_index", "output item completed while shell output index %d is still open", index)
		}
	}
	item.done = true
	item.doneItem = append(json.RawMessage(nil), event.Item...)
	return nil
}

func (v *nativeStreamValidator) validateResponsesTerminalOutput(raw json.RawMessage) error {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		return core.Invalid(v.protocol, "$.response", "invalid terminal response object")
	}
	var output []json.RawMessage
	if err := json.Unmarshal(response["output"], &output); err != nil || output == nil {
		return core.Invalid(v.protocol, "$.response.output", "terminal response output array is required")
	}
	if len(output) != len(v.responsesItems) {
		return core.Invalid(v.protocol, "$.response.output", "terminal output has %d items, but the stream completed %d items", len(output), len(v.responsesItems))
	}
	for index, rawItem := range output {
		streamed, exists := v.responsesItems[index]
		if !exists {
			return core.Invalid(v.protocol, "$.response.output", "terminal output index %d was not emitted by output_item events", index)
		}
		identity, err := decodeResponsesItemIdentity(v.protocol, rawItem)
		if err != nil {
			return err
		}
		if identity.id != streamed.id || identity.itemType != streamed.itemType {
			return core.Invalid(v.protocol, "$.response.output", "terminal output index %d identity %q/%q does not match streamed item %q/%q", index, identity.id, identity.itemType, streamed.id, streamed.itemType)
		}
		if !responsesJSONEqual(rawItem, streamed.doneItem) {
			return core.Invalid(v.protocol, "$.response.output", "terminal output index %d does not match its output_item.done payload", index)
		}
	}
	return nil
}

func responsesJSONEqual(left, right json.RawMessage) bool {
	decode := func(raw json.RawMessage) (any, bool) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		return value, true
	}
	leftValue, leftOK := decode(left)
	rightValue, rightOK := decode(right)
	return leftOK && rightOK && reflect.DeepEqual(leftValue, rightValue)
}

type responsesItemIdentity struct {
	id       string
	itemType string
}

func decodeResponsesItemIdentity(protocol core.Protocol, raw json.RawMessage) (responsesItemIdentity, error) {
	if !rawJSONObject(raw) {
		return responsesItemIdentity{}, core.Invalid(protocol, "$.item", "item object is required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return responsesItemIdentity{}, core.Invalid(protocol, "$.item", "invalid item object")
	}
	id, err := requireJSONString(protocol, "$.item.id", object["id"])
	if err != nil || id == "" {
		if err != nil {
			return responsesItemIdentity{}, err
		}
		return responsesItemIdentity{}, core.Invalid(protocol, "$.item.id", "must not be empty")
	}
	itemType, err := requireJSONString(protocol, "$.item.type", object["type"])
	if err != nil || itemType == "" {
		if err != nil {
			return responsesItemIdentity{}, err
		}
		return responsesItemIdentity{}, core.Invalid(protocol, "$.item.type", "must not be empty")
	}
	return responsesItemIdentity{id: id, itemType: itemType}, nil
}

func (v *nativeStreamValidator) openResponsesItem(outputIndex *int, itemID string, expectedTypes ...string) (*nativeResponsesItemState, error) {
	if outputIndex == nil {
		return nil, core.Invalid(v.protocol, "$.output_index", "output item index is required")
	}
	item, exists := v.responsesItems[*outputIndex]
	if !exists {
		return nil, core.Invalid(v.protocol, "$.output_index", "event arrived before output item %d was added", *outputIndex)
	}
	if item.done {
		return nil, core.Invalid(v.protocol, "$.output_index", "event arrived after output item %d was done", *outputIndex)
	}
	if itemID != "" && itemID != item.id {
		return nil, core.Invalid(v.protocol, "$.item_id", "item id %q does not match output item %q at index %d", itemID, item.id, *outputIndex)
	}
	if len(expectedTypes) > 0 {
		matched := false
		for _, expected := range expectedTypes {
			matched = matched || item.itemType == expected
		}
		if !matched {
			return nil, core.Invalid(v.protocol, "$.type", "event does not apply to output item type %q", item.itemType)
		}
	}
	return item, nil
}

func (v *nativeStreamValidator) addResponsesContentPart(event *nativeResponsesEvent) error {
	item, err := v.openResponsesItem(event.OutputIndex, event.ItemID)
	if err != nil {
		return err
	}
	if event.ContentIndex == nil {
		return core.Invalid(v.protocol, "$.content_index", "content index is required")
	}
	partType, err := decodeResponsesPartType(v.protocol, event.Part)
	if err != nil {
		return err
	}
	if err := validateResponsesPartOwner(v.protocol, item.itemType, partType); err != nil {
		return err
	}
	if _, exists := item.content[*event.ContentIndex]; exists {
		return core.Invalid(v.protocol, "$.content_index", "duplicate content index %d for output item %q", *event.ContentIndex, item.id)
	}
	item.content[*event.ContentIndex] = &nativeResponsesPartState{partType: partType}
	return nil
}

func (v *nativeStreamValidator) finishResponsesContentPart(event *nativeResponsesEvent) error {
	_, part, err := v.openResponsesContentPart(event)
	if err != nil {
		return err
	}
	partType, err := decodeResponsesPartType(v.protocol, event.Part)
	if err != nil {
		return err
	}
	if partType != part.partType {
		return core.Invalid(v.protocol, "$.part.type", "content part type %q does not match added type %q", partType, part.partType)
	}
	if part.streamSeen && !part.streamDone {
		return core.Invalid(v.protocol, "$.content_index", "content part completed before its %s stream was done", part.partType)
	}
	part.done = true
	return nil
}

func (v *nativeStreamValidator) openResponsesContentPart(event *nativeResponsesEvent) (*nativeResponsesItemState, *nativeResponsesPartState, error) {
	item, err := v.openResponsesItem(event.OutputIndex, event.ItemID)
	if err != nil {
		return nil, nil, err
	}
	if event.ContentIndex == nil {
		return nil, nil, core.Invalid(v.protocol, "$.content_index", "content index is required")
	}
	part, exists := item.content[*event.ContentIndex]
	if !exists {
		return nil, nil, core.Invalid(v.protocol, "$.content_index", "event arrived before content index %d was added", *event.ContentIndex)
	}
	if part.done {
		return nil, nil, core.Invalid(v.protocol, "$.content_index", "event arrived after content index %d was done", *event.ContentIndex)
	}
	return item, part, nil
}

func (v *nativeStreamValidator) validateResponsesContentStream(event *nativeResponsesEvent, expectedPart string, done bool) error {
	_, part, err := v.openResponsesContentPart(event)
	if err != nil {
		return err
	}
	if part.partType != expectedPart {
		return core.Invalid(v.protocol, "$.type", "event for %s does not match content part type %q", expectedPart, part.partType)
	}
	if part.streamDone {
		return core.Invalid(v.protocol, "$.type", "event arrived after the %s stream was done", expectedPart)
	}
	part.streamSeen = true
	if done {
		part.streamDone = true
	}
	return nil
}

func (v *nativeStreamValidator) addResponsesSummaryPart(event *nativeResponsesEvent) error {
	item, err := v.openResponsesItem(event.OutputIndex, event.ItemID, "reasoning")
	if err != nil {
		return err
	}
	if event.SummaryIndex == nil {
		return core.Invalid(v.protocol, "$.summary_index", "summary index is required")
	}
	partType, err := decodeResponsesPartType(v.protocol, event.Part)
	if err != nil {
		return err
	}
	if partType != "summary_text" {
		return core.Invalid(v.protocol, "$.part.type", "reasoning summary part must have type summary_text")
	}
	if _, exists := item.summary[*event.SummaryIndex]; exists {
		return core.Invalid(v.protocol, "$.summary_index", "duplicate summary index %d for output item %q", *event.SummaryIndex, item.id)
	}
	item.summary[*event.SummaryIndex] = &nativeResponsesPartState{partType: partType}
	return nil
}

func (v *nativeStreamValidator) finishResponsesSummaryPart(event *nativeResponsesEvent) error {
	_, part, err := v.openResponsesSummaryPart(event)
	if err != nil {
		return err
	}
	partType, err := decodeResponsesPartType(v.protocol, event.Part)
	if err != nil {
		return err
	}
	if partType != part.partType {
		return core.Invalid(v.protocol, "$.part.type", "summary part type %q does not match added type %q", partType, part.partType)
	}
	if part.streamSeen && !part.streamDone {
		return core.Invalid(v.protocol, "$.summary_index", "summary part completed before its text stream was done")
	}
	part.done = true
	return nil
}

func (v *nativeStreamValidator) openResponsesSummaryPart(event *nativeResponsesEvent) (*nativeResponsesItemState, *nativeResponsesPartState, error) {
	item, err := v.openResponsesItem(event.OutputIndex, event.ItemID, "reasoning")
	if err != nil {
		return nil, nil, err
	}
	if event.SummaryIndex == nil {
		return nil, nil, core.Invalid(v.protocol, "$.summary_index", "summary index is required")
	}
	part, exists := item.summary[*event.SummaryIndex]
	if !exists {
		return nil, nil, core.Invalid(v.protocol, "$.summary_index", "event arrived before summary index %d was added", *event.SummaryIndex)
	}
	if part.done {
		return nil, nil, core.Invalid(v.protocol, "$.summary_index", "event arrived after summary index %d was done", *event.SummaryIndex)
	}
	return item, part, nil
}

func (v *nativeStreamValidator) validateResponsesSummaryStream(event *nativeResponsesEvent, done bool) error {
	_, part, err := v.openResponsesSummaryPart(event)
	if err != nil {
		return err
	}
	if part.streamDone {
		return core.Invalid(v.protocol, "$.type", "event arrived after the reasoning summary text stream was done")
	}
	part.streamSeen = true
	if done {
		part.streamDone = true
	}
	return nil
}

func decodeResponsesPartType(protocol core.Protocol, raw json.RawMessage) (string, error) {
	if !rawJSONObject(raw) {
		return "", core.Invalid(protocol, "$.part", "part object is required")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", core.Invalid(protocol, "$.part", "invalid part object")
	}
	partType, err := requireJSONString(protocol, "$.part.type", object["type"])
	if err != nil {
		return "", err
	}
	if partType == "" {
		return "", core.Invalid(protocol, "$.part.type", "must not be empty")
	}
	return partType, nil
}

func validateResponsesPartOwner(protocol core.Protocol, itemType, partType string) error {
	switch partType {
	case "output_text", "refusal":
		if itemType != "message" {
			return core.Invalid(protocol, "$.part.type", "%s content requires a message output item", partType)
		}
	case "reasoning_text":
		if itemType != "reasoning" {
			return core.Invalid(protocol, "$.part.type", "reasoning_text content requires a reasoning output item")
		}
	}
	return nil
}

func (v *nativeStreamValidator) validateResponsesItemStream(event *nativeResponsesEvent, stream string, done bool, expectedTypes ...string) error {
	item, err := v.openResponsesItem(event.OutputIndex, event.ItemID, expectedTypes...)
	if err != nil {
		return err
	}
	if item.streams[stream] {
		return core.Invalid(v.protocol, "$.type", "event arrived after the %s stream was done", stream)
	}
	item.streamSeen[stream] = true
	if done {
		item.streams[stream] = true
	}
	return nil
}

func responsesItemStreamRequiresDone(stream string) bool {
	switch stream {
	case "function_call_arguments", "custom_tool_call_input", "code_interpreter_call_code", "mcp_call_arguments":
		return true
	default:
		return false
	}
}

func (v *nativeStreamValidator) validateResponsesItemsClosed() error {
	for index, item := range v.responsesItems {
		if !item.done {
			return core.Invalid(v.protocol, "$.type", "terminal response arrived before output item %d was done", index)
		}
	}
	return nil
}

func (v *nativeStreamValidator) validateResponsesShellCommand(eventType string, event *nativeResponsesEvent) error {
	item, err := v.openResponsesItem(event.OutputIndex, "", "shell_call", "local_shell_call")
	if err != nil {
		return err
	}
	return v.validateResponsesShellCommandIndex(eventType, event, item)
}

func (v *nativeStreamValidator) validateResponsesShellCommandIndex(eventType string, event *nativeResponsesEvent, item *nativeResponsesItemState) error {
	if event.CommandIndex == nil {
		return core.Invalid(v.protocol, "$.command_index", "command index is required")
	}
	commandIndex := *event.CommandIndex
	done, exists := item.commands[commandIndex]
	switch eventType {
	case "response.shell_call_command.added":
		if exists {
			return core.Invalid(v.protocol, "$.command_index", "duplicate shell command index %d", commandIndex)
		}
		item.commands[commandIndex] = false
	case "response.shell_call_command.delta", "response.shell_call_command.done":
		if !exists {
			return core.Invalid(v.protocol, "$.command_index", "event arrived before shell command index %d was added", commandIndex)
		}
		if done {
			return core.Invalid(v.protocol, "$.command_index", "event arrived after shell command index %d was done", commandIndex)
		}
		if eventType == "response.shell_call_command.done" {
			item.commands[commandIndex] = true
		}
	}
	return nil
}

func (v *nativeStreamValidator) validateResponsesShellOutput(eventType string, event *nativeResponsesEvent) error {
	item, err := v.openResponsesItem(event.OutputIndex, event.ItemID, "shell_call", "local_shell_call")
	if err != nil {
		return err
	}
	if event.CommandIndex == nil {
		return core.Invalid(v.protocol, "$.command_index", "command index is required")
	}
	commandIndex := *event.CommandIndex
	if item.shellOutput[commandIndex] {
		return core.Invalid(v.protocol, "$.command_index", "event arrived after shell output index %d was done", commandIndex)
	}
	if _, seen := item.shellOutput[commandIndex]; !seen {
		item.shellOutput[commandIndex] = false
	}
	if eventType == "response.shell_call_output_content.done" {
		item.shellOutput[commandIndex] = true
	}
	return nil
}

func (v *nativeStreamValidator) validateResponsesGlobalStream(stream string, done bool) error {
	if v.responsesStreams == nil {
		v.responsesStreams = make(map[string]bool)
	}
	if v.responsesStreams[stream] {
		return core.Invalid(v.protocol, "$.type", "event arrived after the %s stream was done", stream)
	}
	if _, seen := v.responsesStreams[stream]; !seen {
		v.responsesStreams[stream] = false
	}
	if done {
		v.responsesStreams[stream] = true
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
		if len(*message.Content) != 0 {
			return core.Invalid(v.protocol, "$.message.content", "message_start content must be empty")
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
		if rawJSONPresent(message.StopReason) || rawJSONPresent(message.StopSequence) || rawJSONPresent(message.StopDetails) {
			return core.Invalid(v.protocol, "$.message", "message_start stop fields must be null")
		}
		if err := validateOptionalNullableObject(v.protocol, "$.message.container", message.Container); err != nil {
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
		if err := validateOptionalNullableObject(v.protocol, "$.delta.container", delta.Container); err != nil {
			return err
		}
		if err := validateOptionalNullableObject(v.protocol, "$.delta.stop_details", delta.StopDetails); err != nil {
			return err
		}
		if rawJSONPresent(delta.StopDetails) && stopReason != "refusal" {
			return core.Invalid(v.protocol, "$.delta.stop_details", "is only valid when stop_reason is refusal")
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
