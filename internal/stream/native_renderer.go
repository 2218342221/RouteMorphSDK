package stream

import (
	"encoding/json"
	"fmt"
)

// renderNativeResponseStream turns a pair mapper's native response DTO into a
// valid target stream without passing through a cross-protocol response IR.
func RenderNativeResponse(protocol Protocol, body []byte) ([]streamFrame, []Diagnostic, error) {
	switch protocol {
	case ProtocolChat:
		return renderChatResponseStream(body)
	case ProtocolResponses:
		return renderResponsesResponseStream(body)
	case ProtocolMessages:
		return renderMessagesResponseStream(body)
	case ProtocolGenerateContent:
		var response geminiResponse
		if err := decodeJSON(protocol, body, &response); err != nil {
			return nil, nil, err
		}
		if _, err := validateGeminiResponseEnvelope(&response, rejectSemanticLoss); err != nil {
			return nil, nil, err
		}
		return []streamFrame{{Data: append([]byte(nil), body...)}}, nil, nil
	default:
		return nil, nil, invalid(protocol, "$", "unsupported stream protocol")
	}
}

func renderChatResponseStream(body []byte) ([]streamFrame, []Diagnostic, error) {
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
	base := map[string]any{"id": response.ID, "object": "chat.completion.chunk", "created": response.Created, "model": response.Model}
	frame := func(delta map[string]any, finish any, usage any) streamFrame {
		payload := map[string]any{"choices": []any{map[string]any{"index": choice.Index, "delta": delta, "finish_reason": finish}}}
		if usage != nil {
			payload["usage"] = usage
		}
		return streamFrame{Data: mustJSON(mergeMap(base, payload))}
	}
	frames := []streamFrame{frame(map[string]any{"role": "assistant"}, nil, nil)}
	if text := rawString(choice.Message.Content); text != "" && text != "null" {
		frames = append(frames, frame(map[string]any{"content": text}, nil, nil))
	}
	if choice.Message.ReasoningContent != "" {
		frames = append(frames, frame(map[string]any{"reasoning_content": choice.Message.ReasoningContent}, nil, nil))
	}
	if choice.Message.Refusal != "" {
		frames = append(frames, frame(map[string]any{"refusal": choice.Message.Refusal}, nil, nil))
	}
	for index, call := range choice.Message.ToolCalls {
		frames = append(frames, frame(map[string]any{"tool_calls": []any{map[string]any{
			"index": index, "id": call.ID, "type": "function",
			"function": map[string]any{"name": call.Function.Name, "arguments": rawString(call.Function.Arguments)},
		}}}, nil, nil))
	}
	usage := map[string]any{
		"prompt_tokens": response.Usage.PromptTokens, "completion_tokens": response.Usage.CompletionTokens,
		"total_tokens":              response.Usage.TotalTokens,
		"prompt_tokens_details":     map[string]any{"cached_tokens": response.Usage.PromptDetails.CachedTokens},
		"completion_tokens_details": map[string]any{"reasoning_tokens": response.Usage.CompletionDetails.ReasoningTokens},
	}
	frames = append(frames, frame(map[string]any{}, choice.FinishReason, usage), streamFrame{Data: []byte("[DONE]"), Done: true})
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
	for outputIndex, item := range response.Output {
		if item.Type != "function_call" {
			continue
		}
		inProgress := item
		inProgress.Status, inProgress.Arguments = "in_progress", json.RawMessage(`""`)
		arguments := rawString(item.Arguments)
		frames = append(frames,
			nativeResponseEvent("response.output_item.added", sequence, map[string]any{"output_index": outputIndex, "item": inProgress}),
			nativeResponseEvent("response.function_call_arguments.delta", sequence+1, map[string]any{"item_id": item.ID, "output_index": outputIndex, "delta": arguments}),
			nativeResponseEvent("response.function_call_arguments.done", sequence+2, map[string]any{"item_id": item.ID, "output_index": outputIndex, "name": item.Name, "arguments": arguments}),
			nativeResponseEvent("response.output_item.done", sequence+3, map[string]any{"output_index": outputIndex, "item": item}),
		)
		sequence += 4
	}
	frames = append(frames, nativeResponseEvent("response.completed", sequence, map[string]any{"response": completed}))
	return frames, nil, nil
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
	start := response
	start.Content = json.RawMessage(`[]`)
	start.StopReason, start.StopSequence = "", ""
	start.Usage.OutputTokens = 0
	frames := []streamFrame{{Event: "message_start", Data: mustJSON(map[string]any{"type": "message_start", "message": start})}}
	for index, block := range blocks {
		initial := block
		var delta map[string]any
		switch block.Type {
		case "text":
			initial.Text = ""
			delta = map[string]any{"type": "text_delta", "text": block.Text}
		case "thinking":
			initial.Thinking = ""
			delta = map[string]any{"type": "thinking_delta", "thinking": block.Thinking}
		case "tool_use":
			initial.Input = json.RawMessage(`{}`)
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(block.Input)}
		default:
			return nil, nil, unsupported(ProtocolMessages, fmt.Sprintf("$.content[%d].type", index), "cannot render block %q", block.Type)
		}
		frames = append(frames,
			streamFrame{Event: "content_block_start", Data: mustJSON(map[string]any{"type": "content_block_start", "index": index, "content_block": initial})},
			streamFrame{Event: "content_block_delta", Data: mustJSON(map[string]any{"type": "content_block_delta", "index": index, "delta": delta})},
			streamFrame{Event: "content_block_stop", Data: mustJSON(map[string]any{"type": "content_block_stop", "index": index})},
		)
	}
	frames = append(frames,
		streamFrame{Event: "message_delta", Data: mustJSON(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": response.StopReason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": response.Usage.OutputTokens}})},
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
