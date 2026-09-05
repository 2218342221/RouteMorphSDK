package stream

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// renderNativeResponseStream turns a pair mapper's native response DTO into a
// valid target stream without passing through a cross-protocol response IR.
func RenderNativeResponse(protocol Protocol, body []byte) ([]streamFrame, []Diagnostic, error) {
	return renderNativeResponseWithOptions(protocol, body, conversionOptions{})
}

func renderNativeResponseWithOptions(protocol Protocol, body []byte, options conversionOptions) ([]streamFrame, []Diagnostic, error) {
	switch protocol {
	case ProtocolChat:
		return renderChatResponseStream(body, options.Exchange.ChatStreamIncludeUsage)
	case ProtocolResponses:
		return renderResponsesResponseStream(body)
	case ProtocolMessages:
		return renderMessagesResponseStream(body)
	case ProtocolGenerateContent:
		var response geminiResponse
		if err := decodeJSON(protocol, body, &response); err != nil {
			return nil, nil, err
		}
		if err := validateNativeGeminiTerminal(&response); err != nil {
			return nil, nil, err
		}
		return []streamFrame{{Data: append([]byte(nil), body...)}}, nil, nil
	default:
		return nil, nil, invalid(protocol, "$", "unsupported stream protocol")
	}
}

// Native Gemini rendering preserves the response byte-for-byte. Do not use the
// cross-protocol envelope validator here: its loss checks reject native-only
// metadata even though this path does not omit it.
func validateNativeGeminiTerminal(response *geminiResponse) error {
	if response == nil {
		return invalid(ProtocolGenerateContent, "$", "response is required")
	}
	if len(response.Candidates) == 0 {
		if reason := geminiPromptBlockReason(response.PromptFeedback); reason != "" {
			return upstreamResponseError(ProtocolGenerateContent, "$.promptFeedback.blockReason", "request blocked by Gemini API: %s", reason)
		}
		return upstreamResponseError(ProtocolGenerateContent, "$.candidates", "Gemini returned no candidates")
	}
	if len(response.Candidates) != 1 {
		return unsupported(ProtocolGenerateContent, "$.candidates", "stream rendering requires exactly one candidate")
	}
	if role := response.Candidates[0].Content.Role; role != "" && role != "model" {
		return upstreamResponseError(ProtocolGenerateContent, "$.candidates[0].content.role", "expected model role, got %q", role)
	}
	_, err := parseGeminiFinish(response.Candidates[0].FinishReason)
	return err
}

func renderChatResponseStream(body []byte, includeUsage bool) ([]streamFrame, []Diagnostic, error) {
	var response chatResponse
	if err := decodeJSON(ProtocolChat, body, &response); err != nil {
		return nil, nil, err
	}
	if response.Error != nil {
		return nil, nil, upstreamResponseError(ProtocolChat, "$.error", "%s", response.Error.Message)
	}
	if len(response.Choices) != 1 {
		return nil, nil, unsupported(ProtocolChat, "$.choices", "stream rendering requires exactly one choice")
	}
	choice := response.Choices[0]
	if choice.Message.Role != "assistant" {
		return nil, nil, upstreamResponseError(ProtocolChat, "$.choices[0].message.role", "expected assistant role, got %q", choice.Message.Role)
	}
	if _, err := parseChatFinish(choice.FinishReason); err != nil {
		return nil, nil, err
	}
	content, ok := decodeNativeNullableJSONString(choice.Message.Content)
	if !ok {
		return nil, nil, upstreamResponseError(ProtocolChat, "$.choices[0].message.content", "must be a string or null")
	}
	base := map[string]any{"id": response.ID, "object": "chat.completion.chunk", "created": response.Created, "model": response.Model}
	if len(response.Metadata) > 0 {
		base["metadata"] = response.Metadata
	}
	if jsonValuePresent(response.Moderation) {
		base["moderation"] = response.Moderation
	}
	if jsonValuePresent(response.ServiceTier) {
		base["service_tier"] = response.ServiceTier
	}
	if response.SystemFingerprint != "" {
		base["system_fingerprint"] = response.SystemFingerprint
	}
	frame := func(delta map[string]any, finish any, usage any) streamFrame {
		payload := map[string]any{"choices": []any{map[string]any{"index": choice.Index, "delta": delta, "finish_reason": finish}}}
		if usage != nil {
			payload["usage"] = usage
		} else if includeUsage {
			payload["usage"] = nil
		}
		return streamFrame{Data: mustJSON(mergeMap(base, payload))}
	}
	frames := []streamFrame{frame(map[string]any{"role": "assistant"}, nil, nil)}
	if content != "" {
		frames = append(frames, frame(map[string]any{"content": content}, nil, nil))
	}
	if choice.Message.ReasoningContent != "" {
		frames = append(frames, frame(map[string]any{"reasoning_content": choice.Message.ReasoningContent}, nil, nil))
	}
	if choice.Message.Refusal != "" {
		frames = append(frames, frame(map[string]any{"refusal": choice.Message.Refusal}, nil, nil))
	}
	if jsonValuePresent(choice.Message.Annotations) {
		frames = append(frames, frame(map[string]any{"annotations": choice.Message.Annotations}, nil, nil))
	}
	for index, call := range choice.Message.ToolCalls {
		callPath := fmt.Sprintf("$.choices[0].message.tool_calls[%d]", index)
		switch call.Type {
		case "custom":
			return nil, nil, unsupported(ProtocolChat, callPath+".type", "Chat custom tool calls have no official streaming delta representation")
		case "function":
			if call.ID == "" || call.Function.Name == "" {
				return nil, nil, upstreamResponseError(ProtocolChat, callPath, "function tool call requires id and name")
			}
			arguments, ok := decodeNativeJSONString(call.Function.Arguments)
			if !ok {
				return nil, nil, upstreamResponseError(ProtocolChat, callPath+".function.arguments", "must be a JSON string")
			}
			frames = append(frames, frame(map[string]any{"tool_calls": []any{map[string]any{
				"index": index, "id": call.ID, "type": "function",
				"function": map[string]any{"name": call.Function.Name, "arguments": arguments},
			}}}, nil, nil))
		default:
			return nil, nil, upstreamResponseError(ProtocolChat, callPath+".type", "unsupported tool call type %q", call.Type)
		}
	}
	usage := map[string]any{
		"prompt_tokens": response.Usage.PromptTokens, "completion_tokens": response.Usage.CompletionTokens,
		"total_tokens": response.Usage.TotalTokens,
		"prompt_tokens_details": map[string]any{
			"audio_tokens": response.Usage.PromptDetails.AudioTokens, "cached_tokens": response.Usage.PromptDetails.CachedTokens,
		},
		"completion_tokens_details": map[string]any{
			"accepted_prediction_tokens": response.Usage.CompletionDetails.AcceptedPredictionTokens,
			"audio_tokens":               response.Usage.CompletionDetails.AudioTokens,
			"reasoning_tokens":           response.Usage.CompletionDetails.ReasoningTokens,
			"rejected_prediction_tokens": response.Usage.CompletionDetails.RejectedPredictionTokens,
		},
	}
	frames = append(frames, frame(map[string]any{}, choice.FinishReason, nil))
	if includeUsage {
		frames = append(frames, streamFrame{Data: mustJSON(mergeMap(base, map[string]any{"choices": []any{}, "usage": usage}))})
	}
	frames = append(frames, streamFrame{Data: []byte("[DONE]"), Done: true})
	return frames, nil, nil
}

func renderResponsesResponseStream(body []byte) ([]streamFrame, []Diagnostic, error) {
	var response responsesResponse
	if err := decodeJSON(ProtocolResponses, body, &response); err != nil {
		return nil, nil, err
	}
	if err := validateResponsesTerminal(response); err != nil {
		return nil, nil, err
	}
	var completed map[string]any
	if err := json.Unmarshal(body, &completed); err != nil {
		return nil, nil, invalid(ProtocolResponses, "$", "invalid response object: %v", err)
	}
	created := cloneMap(completed)
	created["status"], created["output"], created["usage"] = "in_progress", []any{}, nil
	frames := []streamFrame{nativeResponseEvent("response.created", 0, map[string]any{"response": created})}
	sequence := 1
	appendEvent := func(event string, fields map[string]any) {
		frames = append(frames, nativeResponseEvent(event, sequence, fields))
		sequence++
	}
	for outputIndex, item := range response.Output {
		if err := validateNativeResponsesToolItem(item, outputIndex); err != nil {
			return nil, nil, err
		}
		switch item.Type {
		case "function_call":
			inProgress := item
			inProgress.Status, inProgress.Arguments = "in_progress", json.RawMessage(`""`)
			arguments := rawString(item.Arguments)
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			appendEvent("response.function_call_arguments.delta", map[string]any{"item_id": item.ID, "output_index": outputIndex, "delta": arguments})
			appendEvent("response.function_call_arguments.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "name": item.Name, "arguments": arguments})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": item})
		case "custom_tool_call":
			var input string
			input, _ = decodeNativeJSONString(item.Input)
			inProgress := item
			inProgress.Status, inProgress.Input = "in_progress", json.RawMessage(`""`)
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			appendEvent("response.custom_tool_call_input.delta", map[string]any{"item_id": item.ID, "output_index": outputIndex, "delta": input})
			appendEvent("response.custom_tool_call_input.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "input": input})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": item})
		case "web_search_call":
			inProgress := item
			inProgress.Status = "in_progress"
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			appendEvent("response.web_search_call.in_progress", map[string]any{"item_id": item.ID, "output_index": outputIndex})
			switch item.Status {
			case "searching":
				appendEvent("response.web_search_call.searching", map[string]any{"item_id": item.ID, "output_index": outputIndex})
			case "completed":
				appendEvent("response.web_search_call.searching", map[string]any{"item_id": item.ID, "output_index": outputIndex})
				appendEvent("response.web_search_call.completed", map[string]any{"item_id": item.ID, "output_index": outputIndex})
			}
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": item})
		case "tool_search_call", "tool_search_output":
			inProgress := item
			inProgress.Status = "in_progress"
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": item})
		case "additional_tools":
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": item})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": item})
		default:
			continue
		}
	}
	terminalEvent := "response.completed"
	if response.Status == "incomplete" {
		terminalEvent = "response.incomplete"
	}
	frames = append(frames, nativeResponseEvent(terminalEvent, sequence, map[string]any{"response": completed}))
	return frames, nil, nil
}

func validateNativeResponsesToolItem(item responsesItem, outputIndex int) error {
	path := fmt.Sprintf("$.output[%d]", outputIndex)
	requireIDAndStatus := func(statuses ...string) error {
		if item.ID == "" {
			return upstreamResponseError(ProtocolResponses, path+".id", "output item id is required")
		}
		for _, status := range statuses {
			if item.Status == status {
				return nil
			}
		}
		return upstreamResponseError(ProtocolResponses, path+".status", "unsupported %s status %q", item.Type, item.Status)
	}
	requireJSONArray := func(raw json.RawMessage, field string) error {
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			return upstreamResponseError(ProtocolResponses, path+"."+field, "%s is required and must be an array", field)
		}
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return upstreamResponseError(ProtocolResponses, path+"."+field, "%s must be an array", field)
		}
		for index, value := range values {
			var tool struct {
				Type string `json:"type"`
			}
			trimmedTool := bytes.TrimSpace(value)
			if len(trimmedTool) == 0 || trimmedTool[0] != '{' || json.Unmarshal(trimmedTool, &tool) != nil || tool.Type == "" {
				return upstreamResponseError(ProtocolResponses, fmt.Sprintf("%s.%s[%d]", path, field, index), "tool must be an object with a type")
			}
		}
		return nil
	}
	switch item.Type {
	case "function_call":
		if err := requireIDAndStatus("in_progress", "completed", "incomplete"); err != nil {
			return err
		}
		if item.CallID == "" || item.Name == "" {
			return upstreamResponseError(ProtocolResponses, path, "function_call requires call_id and name")
		}
		if _, ok := decodeNativeJSONString(item.Arguments); !ok {
			return upstreamResponseError(ProtocolResponses, path+".arguments", "function_call arguments must be a JSON string")
		}
	case "custom_tool_call":
		if err := requireIDAndStatus("in_progress", "completed", "incomplete"); err != nil {
			return err
		}
		if item.CallID == "" || item.Name == "" {
			return upstreamResponseError(ProtocolResponses, path, "custom_tool_call requires call_id and name")
		}
		if _, ok := decodeNativeJSONString(item.Input); !ok {
			return upstreamResponseError(ProtocolResponses, path+".input", "custom_tool_call input must be a JSON string")
		}
	case "web_search_call":
		if err := requireIDAndStatus("in_progress", "searching", "completed", "failed"); err != nil {
			return err
		}
		var action struct {
			Type    string `json:"type"`
			URL     string `json:"url"`
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(item.Action, &action); err != nil || action.Type == "" {
			return upstreamResponseError(ProtocolResponses, path+".action", "web_search_call action object with type is required")
		}
		switch action.Type {
		case "search", "open_page":
		case "find_in_page":
			if action.URL == "" || action.Pattern == "" {
				return upstreamResponseError(ProtocolResponses, path+".action", "find_in_page action requires url and pattern")
			}
		default:
			return upstreamResponseError(ProtocolResponses, path+".action.type", "unsupported web search action %q", action.Type)
		}
	case "tool_search_call":
		if err := requireIDAndStatus("in_progress", "completed", "incomplete"); err != nil {
			return err
		}
		if item.CallID == "" {
			return upstreamResponseError(ProtocolResponses, path+".call_id", "tool_search_call call_id is required")
		}
		if item.Execution != "server" && item.Execution != "client" {
			return upstreamResponseError(ProtocolResponses, path+".execution", "tool_search_call execution must be server or client")
		}
		if !jsonValuePresent(item.Arguments) {
			return upstreamResponseError(ProtocolResponses, path+".arguments", "tool_search_call arguments are required")
		}
	case "tool_search_output":
		if err := requireIDAndStatus("in_progress", "completed", "incomplete"); err != nil {
			return err
		}
		if item.CallID == "" {
			return upstreamResponseError(ProtocolResponses, path+".call_id", "tool_search_output call_id is required")
		}
		if item.Execution != "server" && item.Execution != "client" {
			return upstreamResponseError(ProtocolResponses, path+".execution", "tool_search_output execution must be server or client")
		}
		return requireJSONArray(item.Tools, "tools")
	case "additional_tools":
		if item.ID == "" {
			return upstreamResponseError(ProtocolResponses, path+".id", "output item id is required")
		}
		switch item.Role {
		case "unknown", "user", "assistant", "system", "critic", "discriminator", "developer", "tool":
		default:
			return upstreamResponseError(ProtocolResponses, path+".role", "unsupported additional_tools role %q", item.Role)
		}
		return requireJSONArray(item.Tools, "tools")
	}
	return nil
}

func decodeNativeJSONString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", false
	}
	return value, true
}

func decodeNativeNullableJSONString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", true
	}
	return decodeNativeJSONString(trimmed)
}

func renderMessagesResponseStream(body []byte) ([]streamFrame, []Diagnostic, error) {
	var response messagesResponse
	if err := decodeJSON(ProtocolMessages, body, &response); err != nil {
		return nil, nil, err
	}
	if err := validateMessagesResponse(response); err != nil {
		return nil, nil, err
	}
	blocks, err := decodeMessagesBlocks(response.Content, "$.content")
	if err != nil {
		return nil, nil, err
	}
	startUsage := map[string]any{
		"input_tokens": response.Usage.InputTokens, "output_tokens": int64(0),
		"cache_creation_input_tokens": response.Usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     response.Usage.CacheReadInputTokens,
	}
	if response.Usage.CacheCreation != nil {
		startUsage["cache_creation"] = response.Usage.CacheCreation
	}
	if response.Usage.InferenceGeo != "" {
		startUsage["inference_geo"] = response.Usage.InferenceGeo
	}
	if response.Usage.OutputTokensDetails != nil {
		startUsage["output_tokens_details"] = map[string]any{"thinking_tokens": int64(0)}
	}
	if response.Usage.ServiceTier != "" {
		startUsage["service_tier"] = response.Usage.ServiceTier
	}
	start := map[string]any{
		"id": response.ID, "type": "message", "role": "assistant", "model": response.Model,
		"content": []any{}, "container": nil, "stop_reason": nil, "stop_sequence": nil, "stop_details": nil,
		"usage": startUsage,
	}
	frames := []streamFrame{{Event: "message_start", Data: mustJSON(map[string]any{"type": "message_start", "message": start})}}
	for index, block := range blocks {
		initial := block
		var deltas []map[string]any
		switch block.Type {
		case "text":
			initial.Text = ""
			initial.Citations = json.RawMessage(`[]`)
			deltas = append(deltas, map[string]any{"type": "text_delta", "text": block.Text})
			if jsonValuePresent(block.Citations) {
				var citations []json.RawMessage
				if err := json.Unmarshal(block.Citations, &citations); err != nil {
					return nil, nil, upstreamResponseError(ProtocolMessages, fmt.Sprintf("$.content[%d].citations", index), "citations must be an array")
				}
				for _, citation := range citations {
					trimmed := bytes.TrimSpace(citation)
					if !jsonValuePresent(trimmed) || len(trimmed) == 0 || trimmed[0] != '{' {
						return nil, nil, upstreamResponseError(ProtocolMessages, fmt.Sprintf("$.content[%d].citations", index), "citation must be an object")
					}
					deltas = append(deltas, map[string]any{"type": "citations_delta", "citation": citation})
				}
			}
		case "thinking":
			initial.Thinking = ""
			initial.Signature = ""
			deltas = append(deltas, map[string]any{"type": "thinking_delta", "thinking": block.Thinking})
			if block.Signature != "" {
				deltas = append(deltas, map[string]any{"type": "signature_delta", "signature": block.Signature})
			}
		case "tool_use", "server_tool_use", "mcp_tool_use":
			initial.Input = json.RawMessage(`{}`)
			deltas = append(deltas, map[string]any{"type": "input_json_delta", "partial_json": string(block.Input)})
		case "redacted_thinking":
		default:
			// Result and provider-extension blocks are complete in the start event.
		}
		var initialObject map[string]any
		if err := json.Unmarshal(mustJSON(initial), &initialObject); err != nil {
			return nil, nil, upstreamResponseError(ProtocolMessages, fmt.Sprintf("$.content[%d]", index), "cannot encode content block")
		}
		switch block.Type {
		case "text":
			initialObject["text"] = ""
		case "thinking":
			initialObject["thinking"], initialObject["signature"] = "", ""
		case "tool_use", "server_tool_use", "mcp_tool_use":
			initialObject["input"] = map[string]any{}
		}
		frames = append(frames, streamFrame{Event: "content_block_start", Data: mustJSON(map[string]any{"type": "content_block_start", "index": index, "content_block": initialObject})})
		for _, delta := range deltas {
			frames = append(frames, streamFrame{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": index, "delta": delta})})
		}
		frames = append(frames, streamFrame{Event: "content_block_stop", Data: mustJSON(map[string]any{"type": "content_block_stop", "index": index})})
	}
	delta := map[string]any{"stop_reason": response.StopReason, "stop_sequence": nil, "container": nil, "stop_details": nil}
	if response.StopSequence != "" {
		delta["stop_sequence"] = response.StopSequence
	}
	if jsonValuePresent(response.Container) {
		delta["container"] = response.Container
	}
	if jsonValuePresent(response.StopDetails) {
		delta["stop_details"] = response.StopDetails
	}
	usage := map[string]any{
		"input_tokens": response.Usage.InputTokens, "output_tokens": response.Usage.OutputTokens,
		"cache_creation_input_tokens": response.Usage.CacheCreationInputTokens,
		"cache_read_input_tokens":     response.Usage.CacheReadInputTokens,
	}
	if response.Usage.OutputTokensDetails != nil {
		usage["output_tokens_details"] = response.Usage.OutputTokensDetails
	}
	if jsonValuePresent(response.Usage.ServerToolUse) {
		usage["server_tool_use"] = response.Usage.ServerToolUse
	}
	frames = append(frames,
		streamFrame{Event: "message_delta", Data: mustJSON(map[string]any{"type": "message_delta", "delta": delta, "usage": usage})},
		streamFrame{Event: "message_stop", Data: mustJSON(map[string]any{"type": "message_stop"})},
	)
	return frames, nil, nil
}

func nativeResponseEvent(event string, sequence int, fields map[string]any) streamFrame {
	payload := map[string]any{"type": event, "sequence_number": sequence}
	for key, value := range fields {
		payload[key] = value
	}
	return streamFrame{Event: event, Data: mustJSON(payload)}
}
