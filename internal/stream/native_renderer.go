package stream

import (
	"bytes"
	"encoding/json"
	"fmt"

	routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"
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
	rawOutput, err := validateNativeResponsesOutput(body)
	if err != nil {
		return nil, nil, err
	}
	diagnostics := ensureNativeResponsesStreamingItemIDs(rawOutput)
	var response responsesResponse
	if err := decodeJSON(ProtocolResponses, body, &response); err != nil {
		return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$", "invalid response object: %v", err)
	}
	if err := validateResponsesTerminal(response); err != nil {
		return nil, diagnostics, err
	}
	if len(rawOutput) != len(response.Output) {
		return nil, diagnostics, upstreamResponseError(ProtocolResponses, "$.output", "decoded item count changed unexpectedly")
	}
	var completed map[string]json.RawMessage
	if err := json.Unmarshal(body, &completed); err != nil {
		return nil, diagnostics, invalid(ProtocolResponses, "$", "invalid response object: %v", err)
	}
	completed["output"] = mustJSON(rawOutput)
	for index := range response.Output {
		response.Output[index].ID, _ = nativeResponsesRequiredString(rawOutput[index], "id", fmt.Sprintf("$.output[%d].id", index))
	}
	created := cloneNativeResponsesObject(completed)
	created["status"], created["output"], created["usage"] = mustJSON("in_progress"), json.RawMessage(`[]`), json.RawMessage(`null`)
	created["error"], created["incomplete_details"], created["completed_at"] = json.RawMessage(`null`), json.RawMessage(`null`), json.RawMessage(`null`)
	frames := []streamFrame{nativeResponseEvent("response.created", 0, map[string]any{"response": created})}
	sequence := 1
	appendEvent := func(event string, fields map[string]any) {
		frames = append(frames, nativeResponseEvent(event, sequence, fields))
		sequence++
	}
	for outputIndex, item := range response.Output {
		rawItem := rawOutput[outputIndex]
		switch item.Type {
		case "message":
			parts, err := decodeNativeResponsesParts(item.Content, fmt.Sprintf("$.output[%d].content", outputIndex), "output_text", "refusal")
			if err != nil {
				return nil, nil, err
			}
			inProgress := cloneNativeResponsesObject(rawItem)
			inProgress["status"], inProgress["content"] = mustJSON("in_progress"), json.RawMessage(`[]`)
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			for contentIndex, part := range parts {
				partPath := fmt.Sprintf("$.output[%d].content[%d]", outputIndex, contentIndex)
				if err := appendNativeResponsesContentPart(appendEvent, item.ID, outputIndex, contentIndex, part, partPath); err != nil {
					return nil, nil, err
				}
			}
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": rawItem})
		case "reasoning":
			summary, err := decodeNativeResponsesParts(item.Summary, fmt.Sprintf("$.output[%d].summary", outputIndex), "summary_text")
			if err != nil {
				return nil, nil, err
			}
			var content []nativeResponsesContentPart
			if contentRaw, exists := rawItem["content"]; exists {
				if nativeResponsesNull(contentRaw) {
					return nil, nil, upstreamResponseError(ProtocolResponses, fmt.Sprintf("$.output[%d].content", outputIndex), "must be an array when present")
				}
				content, err = decodeNativeResponsesParts(item.Content, fmt.Sprintf("$.output[%d].content", outputIndex), "reasoning_text")
				if err != nil {
					return nil, nil, err
				}
			}
			inProgress := cloneNativeResponsesObject(rawItem)
			if _, hasStatus := inProgress["status"]; hasStatus {
				inProgress["status"] = mustJSON("in_progress")
			}
			inProgress["summary"] = json.RawMessage(`[]`)
			if _, exists := inProgress["content"]; exists {
				inProgress["content"] = json.RawMessage(`[]`)
			}
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			for summaryIndex, part := range summary {
				if part.Type != "summary_text" {
					return nil, nil, upstreamResponseError(ProtocolResponses, fmt.Sprintf("$.output[%d].summary[%d].type", outputIndex, summaryIndex), "expected summary_text, got %q", part.Type)
				}
				blank := cloneNativeResponsesObject(part.Fields)
				blank["text"] = mustJSON("")
				fields := map[string]any{"item_id": item.ID, "output_index": outputIndex, "summary_index": summaryIndex}
				appendEvent("response.reasoning_summary_part.added", mergeMap(fields, map[string]any{"part": blank}))
				appendEvent("response.reasoning_summary_text.delta", mergeMap(fields, map[string]any{"delta": part.Text}))
				appendEvent("response.reasoning_summary_text.done", mergeMap(fields, map[string]any{"text": part.Text}))
				appendEvent("response.reasoning_summary_part.done", mergeMap(fields, map[string]any{"part": part.Fields}))
			}
			for contentIndex, part := range content {
				if part.Type != "reasoning_text" {
					return nil, nil, upstreamResponseError(ProtocolResponses, fmt.Sprintf("$.output[%d].content[%d].type", outputIndex, contentIndex), "expected reasoning_text, got %q", part.Type)
				}
				if err := appendNativeResponsesContentPart(appendEvent, item.ID, outputIndex, contentIndex, part, fmt.Sprintf("$.output[%d].content[%d]", outputIndex, contentIndex)); err != nil {
					return nil, nil, err
				}
			}
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": rawItem})
		case "function_call":
			inProgress := cloneNativeResponsesObject(rawItem)
			if _, hasStatus := inProgress["status"]; hasStatus {
				inProgress["status"] = mustJSON("in_progress")
			}
			inProgress["arguments"] = json.RawMessage(`""`)
			arguments := rawString(item.Arguments)
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			appendEvent("response.function_call_arguments.delta", map[string]any{"item_id": item.ID, "output_index": outputIndex, "delta": arguments})
			appendEvent("response.function_call_arguments.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "name": item.Name, "arguments": arguments})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": rawItem})
		case "custom_tool_call":
			var input string
			input, _ = decodeNativeJSONString(item.Input)
			inProgress := cloneNativeResponsesObject(rawItem)
			if _, hasStatus := inProgress["status"]; hasStatus {
				inProgress["status"] = mustJSON("in_progress")
			}
			inProgress["input"] = json.RawMessage(`""`)
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			appendEvent("response.custom_tool_call_input.delta", map[string]any{"item_id": item.ID, "output_index": outputIndex, "delta": input})
			appendEvent("response.custom_tool_call_input.done", map[string]any{"item_id": item.ID, "output_index": outputIndex, "input": input})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": rawItem})
		case "web_search_call":
			inProgress := cloneNativeResponsesObject(rawItem)
			inProgress["status"] = mustJSON("in_progress")
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			appendEvent("response.web_search_call.in_progress", map[string]any{"item_id": item.ID, "output_index": outputIndex})
			switch item.Status {
			case "searching":
				appendEvent("response.web_search_call.searching", map[string]any{"item_id": item.ID, "output_index": outputIndex})
			case "completed":
				appendEvent("response.web_search_call.searching", map[string]any{"item_id": item.ID, "output_index": outputIndex})
				appendEvent("response.web_search_call.completed", map[string]any{"item_id": item.ID, "output_index": outputIndex})
			}
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": rawItem})
		case "tool_search_call", "tool_search_output":
			inProgress := cloneNativeResponsesObject(rawItem)
			inProgress["status"] = mustJSON("in_progress")
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": inProgress})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": rawItem})
		case "additional_tools":
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": rawItem})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": rawItem})
		default:
			appendEvent("response.output_item.added", map[string]any{"output_index": outputIndex, "item": rawItem})
			appendEvent("response.output_item.done", map[string]any{"output_index": outputIndex, "item": rawItem})
		}
	}
	terminalEvent := "response.completed"
	if response.Status == "incomplete" {
		terminalEvent = "response.incomplete"
	}
	frames = append(frames, nativeResponseEvent(terminalEvent, sequence, map[string]any{"response": completed}))
	return frames, diagnostics, nil
}

type nativeResponsesContentPart struct {
	Type    string
	Text    string
	Refusal string
	Fields  map[string]json.RawMessage
}

func decodeNativeResponsesParts(raw json.RawMessage, path string, allowedTypes ...string) ([]nativeResponsesContentPart, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, upstreamResponseError(ProtocolResponses, path, "must be an array")
	}
	var rawParts []json.RawMessage
	if err := json.Unmarshal(trimmed, &rawParts); err != nil || rawParts == nil {
		return nil, upstreamResponseError(ProtocolResponses, path, "must be an array: %v", err)
	}
	parts := make([]nativeResponsesContentPart, 0, len(rawParts))
	allowed := make(map[string]struct{}, len(allowedTypes))
	for _, partType := range allowedTypes {
		allowed[partType] = struct{}{}
	}
	for index, rawPart := range rawParts {
		partPath := fmt.Sprintf("%s[%d]", path, index)
		fields, err := decodeNativeResponsesObject(rawPart, partPath)
		if err != nil {
			return nil, err
		}
		partType, err := nativeResponsesRequiredString(fields, "type", partPath+".type")
		if err != nil || partType == "" {
			if err != nil {
				return nil, err
			}
			return nil, upstreamResponseError(ProtocolResponses, partPath+".type", "content part type is required")
		}
		if len(allowed) > 0 {
			if _, ok := allowed[partType]; !ok {
				return nil, upstreamResponseError(ProtocolResponses, partPath+".type", "content part type %q is not valid in this context", partType)
			}
		}
		part := nativeResponsesContentPart{Type: partType, Fields: fields}
		switch partType {
		case "output_text":
			part.Text, err = nativeResponsesRequiredString(fields, "text", partPath+".text")
			if err == nil {
				var annotations []json.RawMessage
				annotations, err = nativeResponsesJSONArrayField(fields, "annotations", partPath+".annotations", true)
				if err == nil {
					for annotationIndex, annotation := range annotations {
						if _, objectErr := decodeNativeResponsesObject(annotation, fmt.Sprintf("%s.annotations[%d]", partPath, annotationIndex)); objectErr != nil {
							err = objectErr
							break
						}
					}
				}
			}
			if err == nil {
				var logprobs []json.RawMessage
				logprobs, err = nativeResponsesJSONArrayField(fields, "logprobs", partPath+".logprobs", false)
				if err == nil {
					for logprobIndex, logprob := range logprobs {
						if _, objectErr := decodeNativeResponsesObject(logprob, fmt.Sprintf("%s.logprobs[%d]", partPath, logprobIndex)); objectErr != nil {
							err = objectErr
							break
						}
					}
				}
			}
		case "refusal":
			part.Refusal, err = nativeResponsesRequiredString(fields, "refusal", partPath+".refusal")
		case "summary_text", "reasoning_text":
			part.Text, err = nativeResponsesRequiredString(fields, "text", partPath+".text")
		}
		if err != nil {
			return nil, err
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func appendNativeResponsesContentPart(appendEvent func(string, map[string]any), itemID string, outputIndex, contentIndex int, part nativeResponsesContentPart, path string) error {
	fields := map[string]any{"item_id": itemID, "output_index": outputIndex, "content_index": contentIndex}
	blank := cloneNativeResponsesObject(part.Fields)
	switch part.Type {
	case "output_text":
		blank["text"], blank["annotations"], blank["logprobs"] = mustJSON(""), json.RawMessage(`[]`), json.RawMessage(`[]`)
		appendEvent("response.content_part.added", mergeMap(fields, map[string]any{"part": blank}))
		logprobs, err := nativeResponsesJSONArrayField(part.Fields, "logprobs", path+".logprobs", false)
		if err != nil {
			return err
		}
		annotations, err := nativeResponsesJSONArrayField(part.Fields, "annotations", path+".annotations", true)
		if err != nil {
			return err
		}
		appendEvent("response.output_text.delta", mergeMap(fields, map[string]any{"delta": part.Text, "logprobs": logprobs}))
		for annotationIndex, annotation := range annotations {
			appendEvent("response.output_text.annotation.added", mergeMap(fields, map[string]any{"annotation_index": annotationIndex, "annotation": annotation}))
		}
		appendEvent("response.output_text.done", mergeMap(fields, map[string]any{"text": part.Text, "logprobs": logprobs}))
		appendEvent("response.content_part.done", mergeMap(fields, map[string]any{"part": part.Fields}))
	case "refusal":
		blank["refusal"] = mustJSON("")
		appendEvent("response.content_part.added", mergeMap(fields, map[string]any{"part": blank}))
		appendEvent("response.refusal.delta", mergeMap(fields, map[string]any{"delta": part.Refusal}))
		appendEvent("response.refusal.done", mergeMap(fields, map[string]any{"refusal": part.Refusal}))
		appendEvent("response.content_part.done", mergeMap(fields, map[string]any{"part": part.Fields}))
	case "reasoning_text":
		blank["text"] = mustJSON("")
		appendEvent("response.content_part.added", mergeMap(fields, map[string]any{"part": blank}))
		appendEvent("response.reasoning_text.delta", mergeMap(fields, map[string]any{"delta": part.Text}))
		appendEvent("response.reasoning_text.done", mergeMap(fields, map[string]any{"text": part.Text}))
		appendEvent("response.content_part.done", mergeMap(fields, map[string]any{"part": part.Fields}))
	default:
		return upstreamResponseError(ProtocolResponses, path+".type", "unsupported content part type %q", part.Type)
	}
	return nil
}

func nativeResponsesJSONArrayField(fields map[string]json.RawMessage, field, path string, required bool) ([]json.RawMessage, error) {
	raw, exists := fields[field]
	if !exists {
		if required {
			return nil, upstreamResponseError(ProtocolResponses, path, "field is required and must be an array")
		}
		return []json.RawMessage{}, nil
	}
	if nativeResponsesNull(raw) {
		return nil, upstreamResponseError(ProtocolResponses, path, "must be an array")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, upstreamResponseError(ProtocolResponses, path, "must be an array")
	}
	if values == nil {
		return nil, upstreamResponseError(ProtocolResponses, path, "must be an array")
	}
	return values, nil
}

func validateNativeResponsesOutput(body []byte) ([]map[string]json.RawMessage, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil || envelope == nil {
		return nil, upstreamResponseError(ProtocolResponses, "$", "response must be an object")
	}
	raw, exists := envelope["output"]
	if !exists || nativeResponsesNull(raw) {
		return nil, upstreamResponseError(ProtocolResponses, "$.output", "field is required and must be an array")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, upstreamResponseError(ProtocolResponses, "$.output", "must be an array")
	}
	items := make([]map[string]json.RawMessage, len(values))
	itemIDs := make(map[string]int, len(values))
	for index, value := range values {
		path := fmt.Sprintf("$.output[%d]", index)
		item, err := decodeNativeResponsesObject(value, path)
		if err != nil {
			return nil, err
		}
		if err := validateNativeResponsesToolItem(item, fmt.Sprintf("$.output[%d]", index)); err != nil {
			return nil, err
		}
		itemType, _ := nativeResponsesRequiredString(item, "type", path+".type")
		itemID, hasID, err := nativeResponsesOptionalItemID(item, path+".id")
		if err != nil {
			return nil, err
		}
		if !hasID && itemType != "function_call" && itemType != "custom_tool_call" {
			return nil, upstreamResponseError(ProtocolResponses, path+".id", "output item id is required")
		}
		if hasID {
			if previous, duplicate := itemIDs[itemID]; duplicate {
				return nil, upstreamResponseError(ProtocolResponses, path+".id", "duplicate output item id %q already used at $.output[%d]", itemID, previous)
			}
			itemIDs[itemID] = index
		}
		items[index] = item
	}
	return items, nil
}

func nativeResponsesOptionalItemID(fields map[string]json.RawMessage, path string) (string, bool, error) {
	raw, exists := fields["id"]
	if !exists {
		return "", false, nil
	}
	if nativeResponsesNull(raw) {
		return "", false, upstreamResponseError(ProtocolResponses, path, "output item id must be a non-empty string when present")
	}
	var id string
	if err := json.Unmarshal(raw, &id); err != nil || id == "" {
		return "", false, upstreamResponseError(ProtocolResponses, path, "output item id must be a non-empty string when present")
	}
	return id, true, nil
}

func ensureNativeResponsesStreamingItemIDs(items []map[string]json.RawMessage) []Diagnostic {
	used := make(map[string]struct{}, len(items))
	for index, item := range items {
		if id, present, err := nativeResponsesOptionalItemID(item, fmt.Sprintf("$.output[%d].id", index)); err == nil && present {
			used[id] = struct{}{}
		}
	}
	var diagnostics []Diagnostic
	for index, item := range items {
		if _, present := item["id"]; present {
			continue
		}
		itemType, _ := nativeResponsesRequiredString(item, "type", fmt.Sprintf("$.output[%d].type", index))
		if itemType != "function_call" && itemType != "custom_tool_call" {
			continue
		}
		prefix := "fc"
		if itemType == "custom_tool_call" {
			prefix = "ctc"
		}
		candidate := fmt.Sprintf("%s_generated_%d", prefix, index)
		for suffix := 2; ; suffix++ {
			if _, exists := used[candidate]; !exists {
				break
			}
			candidate = fmt.Sprintf("%s_generated_%d_%d", prefix, index, suffix)
		}
		item["id"] = mustJSON(candidate)
		used[candidate] = struct{}{}
		diagnostics = appendDiagnostic(diagnostics, "warning", "responses_output_item_id_generated", fmt.Sprintf("$.output[%d].id", index), "generated a stable output item id required by synthesized streaming delta events")
	}
	return diagnostics
}

func decodeNativeResponsesObject(raw json.RawMessage, path string) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, upstreamResponseError(ProtocolResponses, path, "must be an object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil || fields == nil {
		return nil, upstreamResponseError(ProtocolResponses, path, "must be an object")
	}
	return fields, nil
}

func cloneNativeResponsesObject(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source))
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func nativeResponsesNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func nativeResponsesRequiredString(fields map[string]json.RawMessage, field, path string) (string, error) {
	raw, exists := fields[field]
	if !exists || nativeResponsesNull(raw) {
		return "", upstreamResponseError(ProtocolResponses, path, "field is required and must be a string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", upstreamResponseError(ProtocolResponses, path, "must be a string")
	}
	return value, nil
}

func rejectUnknownNativeResponsesFields(fields map[string]json.RawMessage, path string, allowed ...string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range fields {
		if _, ok := known[field]; !ok {
			return upstreamResponseError(ProtocolResponses, path+"."+field, "field is not valid for this Responses union variant")
		}
	}
	return nil
}

func validateNativeResponsesStringArray(fields map[string]json.RawMessage, field, path string) error {
	values, err := nativeResponsesJSONArrayField(fields, field, path, true)
	if err != nil {
		return err
	}
	for index, value := range values {
		var decoded string
		if err := json.Unmarshal(value, &decoded); err != nil {
			return upstreamResponseError(ProtocolResponses, fmt.Sprintf("%s[%d]", path, index), "must be a string")
		}
	}
	return nil
}

func nativeResponsesRequiredNonEmptyString(fields map[string]json.RawMessage, field, path string) (string, error) {
	value, err := nativeResponsesRequiredString(fields, field, path)
	if err != nil {
		return "", err
	}
	if value == "" {
		return "", upstreamResponseError(ProtocolResponses, path, "must not be empty")
	}
	return value, nil
}

func validateNativeResponsesOptionalString(fields map[string]json.RawMessage, field, path string, nullable bool) error {
	raw, exists := fields[field]
	if !exists {
		return nil
	}
	if nativeResponsesNull(raw) {
		if nullable {
			return nil
		}
		return upstreamResponseError(ProtocolResponses, path, "must be a string")
	}
	if _, ok := decodeNativeJSONString(raw); !ok {
		return upstreamResponseError(ProtocolResponses, path, "must be a string")
	}
	return nil
}

func nativeResponsesRequiredInteger(fields map[string]json.RawMessage, field, path string) (int64, error) {
	raw, exists := fields[field]
	if !exists || nativeResponsesNull(raw) {
		return 0, upstreamResponseError(ProtocolResponses, path, "field is required and must be an integer")
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, upstreamResponseError(ProtocolResponses, path, "must be an integer")
	}
	return value, nil
}

func validateNativeResponsesOptionalInteger(fields map[string]json.RawMessage, field, path string, nullable bool) error {
	raw, exists := fields[field]
	if !exists {
		return nil
	}
	if nativeResponsesNull(raw) {
		if nullable {
			return nil
		}
		return upstreamResponseError(ProtocolResponses, path, "must be an integer")
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return upstreamResponseError(ProtocolResponses, path, "must be an integer")
	}
	return nil
}

func nativeResponsesRequiredBool(fields map[string]json.RawMessage, field, path string) (bool, error) {
	raw, exists := fields[field]
	if !exists || nativeResponsesNull(raw) {
		return false, upstreamResponseError(ProtocolResponses, path, "field is required and must be a boolean")
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, upstreamResponseError(ProtocolResponses, path, "must be a boolean")
	}
	return value, nil
}

func validateNativeResponsesNullableStringArray(fields map[string]json.RawMessage, field, path string) error {
	raw, exists := fields[field]
	if !exists || nativeResponsesNull(raw) {
		return nil
	}
	return validateNativeResponsesStringArray(fields, field, path)
}

func validateNativeResponsesStringMap(raw json.RawMessage, path string) error {
	if nativeResponsesNull(raw) {
		return upstreamResponseError(ProtocolResponses, path, "must be an object")
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return upstreamResponseError(ProtocolResponses, path, "must be an object")
	}
	for key, value := range values {
		if _, ok := decodeNativeJSONString(value); !ok {
			return upstreamResponseError(ProtocolResponses, path+"."+key, "must be a string")
		}
	}
	return nil
}

func validateNativeResponsesCaller(raw json.RawMessage, path string) error {
	if !jsonValuePresent(raw) {
		return nil
	}
	fields, err := decodeNativeResponsesObject(raw, path)
	if err != nil {
		return err
	}
	callerType, err := nativeResponsesRequiredString(fields, "type", path+".type")
	if err != nil {
		return err
	}
	switch callerType {
	case "direct":
		return rejectUnknownNativeResponsesFields(fields, path, "type")
	case "program":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "caller_id"); err != nil {
			return err
		}
		_, err = nativeResponsesRequiredNonEmptyString(fields, "caller_id", path+".caller_id")
		return err
	default:
		return upstreamResponseError(ProtocolResponses, path+".type", "unsupported caller type %q", callerType)
	}
}

func validateNativeResponsesWebSearchAction(raw json.RawMessage, path string) error {
	fields, err := decodeNativeResponsesObject(raw, path)
	if err != nil {
		return err
	}
	actionType, err := nativeResponsesRequiredString(fields, "type", path+".type")
	if err != nil {
		return err
	}
	switch actionType {
	case "search":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "query", "queries", "sources"); err != nil {
			return err
		}
		if _, exists := fields["query"]; exists {
			if _, err := nativeResponsesRequiredString(fields, "query", path+".query"); err != nil {
				return err
			}
		}
		if _, exists := fields["queries"]; exists {
			if err := validateNativeResponsesStringArray(fields, "queries", path+".queries"); err != nil {
				return err
			}
		}
		if _, exists := fields["sources"]; exists {
			sources, err := nativeResponsesJSONArrayField(fields, "sources", path+".sources", true)
			if err != nil {
				return err
			}
			for index, source := range sources {
				sourcePath := fmt.Sprintf("%s.sources[%d]", path, index)
				sourceFields, err := decodeNativeResponsesObject(source, sourcePath)
				if err != nil {
					return err
				}
				if err := rejectUnknownNativeResponsesFields(sourceFields, sourcePath, "type", "url"); err != nil {
					return err
				}
				sourceType, err := nativeResponsesRequiredString(sourceFields, "type", sourcePath+".type")
				if err != nil {
					return err
				}
				if sourceType != "url" {
					return upstreamResponseError(ProtocolResponses, sourcePath+".type", "must be url")
				}
				url, err := nativeResponsesRequiredString(sourceFields, "url", sourcePath+".url")
				if err != nil {
					return err
				}
				if url == "" {
					return upstreamResponseError(ProtocolResponses, sourcePath+".url", "must not be empty")
				}
			}
		}
	case "open_page":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "url"); err != nil {
			return err
		}
		rawURL, exists := fields["url"]
		if exists && !nativeResponsesNull(rawURL) {
			var url string
			if err := json.Unmarshal(rawURL, &url); err != nil {
				return upstreamResponseError(ProtocolResponses, path+".url", "must be a string or null")
			}
		}
	case "find_in_page":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "url", "pattern"); err != nil {
			return err
		}
		url, urlErr := nativeResponsesRequiredString(fields, "url", path+".url")
		pattern, patternErr := nativeResponsesRequiredString(fields, "pattern", path+".pattern")
		if urlErr != nil || patternErr != nil || url == "" || pattern == "" {
			return upstreamResponseError(ProtocolResponses, path, "find_in_page action requires non-empty url and pattern")
		}
	default:
		return upstreamResponseError(ProtocolResponses, path+".type", "unsupported web search action %q", actionType)
	}
	return nil
}

func validateNativeResponsesTerminalItemStatus(fields map[string]json.RawMessage, itemType, path string) error {
	type statusPolicy struct {
		required bool
		nullable bool
		values   []string
	}
	policies := map[string]statusPolicy{
		"message":                 {required: true, values: []string{"completed", "incomplete"}},
		"file_search_call":        {required: true, values: []string{"completed", "incomplete", "failed"}},
		"function_call":           {values: []string{"completed", "incomplete"}},
		"function_call_output":    {required: true, values: []string{"completed", "incomplete"}},
		"web_search_call":         {required: true, values: []string{"completed", "failed"}},
		"computer_call":           {required: true, values: []string{"completed", "incomplete"}},
		"computer_call_output":    {required: true, values: []string{"completed", "incomplete", "failed"}},
		"reasoning":               {values: []string{"completed", "incomplete"}},
		"program_output":          {required: true, values: []string{"completed", "incomplete"}},
		"tool_search_call":        {required: true, values: []string{"completed", "incomplete"}},
		"tool_search_output":      {required: true, values: []string{"completed", "incomplete"}},
		"image_generation_call":   {required: true, values: []string{"completed", "failed"}},
		"code_interpreter_call":   {required: true, values: []string{"completed", "incomplete", "failed"}},
		"local_shell_call":        {required: true, values: []string{"completed", "incomplete"}},
		"local_shell_call_output": {nullable: true, values: []string{"completed", "incomplete"}},
		"shell_call":              {required: true, values: []string{"completed", "incomplete"}},
		"shell_call_output":       {required: true, values: []string{"completed", "incomplete"}},
		"apply_patch_call":        {required: true, values: []string{"completed"}},
		"apply_patch_call_output": {required: true, values: []string{"completed", "failed"}},
		"mcp_call":                {values: []string{"completed", "incomplete", "failed"}},
		"custom_tool_call_output": {required: true, values: []string{"completed", "incomplete"}},
	}
	policy, hasStatus := policies[itemType]
	raw, present := fields["status"]
	if !hasStatus {
		if present {
			return upstreamResponseError(ProtocolResponses, path+".status", "field is not valid for terminal %s items", itemType)
		}
		return nil
	}
	if !present {
		if policy.required {
			return upstreamResponseError(ProtocolResponses, path+".status", "terminal %s status is required", itemType)
		}
		return nil
	}
	if nativeResponsesNull(raw) {
		if policy.nullable {
			return nil
		}
		return upstreamResponseError(ProtocolResponses, path+".status", "terminal %s status must be a string", itemType)
	}
	var status string
	if err := json.Unmarshal(raw, &status); err != nil {
		return upstreamResponseError(ProtocolResponses, path+".status", "terminal %s status must be a string", itemType)
	}
	for _, terminal := range policy.values {
		if status == terminal {
			return nil
		}
	}
	return upstreamResponseError(ProtocolResponses, path+".status", "%s status %q is not terminal", itemType, status)
}

func validateNativeResponsesFileSearchItem(fields map[string]json.RawMessage, path string) error {
	if err := validateNativeResponsesStringArray(fields, "queries", path+".queries"); err != nil {
		return err
	}
	rawResults, exists := fields["results"]
	if !exists || nativeResponsesNull(rawResults) {
		return nil
	}
	results, err := nativeResponsesJSONArrayField(fields, "results", path+".results", true)
	if err != nil {
		return err
	}
	for index, rawResult := range results {
		resultPath := fmt.Sprintf("%s.results[%d]", path, index)
		result, err := decodeNativeResponsesObject(rawResult, resultPath)
		if err != nil {
			return err
		}
		if err := rejectUnknownNativeResponsesFields(result, resultPath, "attributes", "file_id", "filename", "score", "text"); err != nil {
			return err
		}
		for _, field := range []string{"file_id", "filename", "text"} {
			if err := validateNativeResponsesOptionalString(result, field, resultPath+"."+field, false); err != nil {
				return err
			}
		}
		if score, present := result["score"]; present {
			var value float64
			if nativeResponsesNull(score) || json.Unmarshal(score, &value) != nil {
				return upstreamResponseError(ProtocolResponses, resultPath+".score", "must be a number")
			}
		}
		if attributes, present := result["attributes"]; present && !nativeResponsesNull(attributes) {
			var values map[string]json.RawMessage
			if json.Unmarshal(attributes, &values) != nil || values == nil {
				return upstreamResponseError(ProtocolResponses, resultPath+".attributes", "must be an object or null")
			}
			for key, rawValue := range values {
				trimmed := bytes.TrimSpace(rawValue)
				if len(trimmed) == 0 || trimmed[0] == '{' || trimmed[0] == '[' || bytes.Equal(trimmed, []byte("null")) {
					return upstreamResponseError(ProtocolResponses, resultPath+".attributes."+key, "must be a string, number, or boolean")
				}
				var value any
				if json.Unmarshal(trimmed, &value) != nil {
					return upstreamResponseError(ProtocolResponses, resultPath+".attributes."+key, "must be a string, number, or boolean")
				}
				switch value.(type) {
				case string, float64, bool:
				default:
					return upstreamResponseError(ProtocolResponses, resultPath+".attributes."+key, "must be a string, number, or boolean")
				}
			}
		}
	}
	return nil
}

func validateNativeResponsesSafetyChecks(fields map[string]json.RawMessage, field, path string, required bool) error {
	checks, err := nativeResponsesJSONArrayField(fields, field, path, required)
	if err != nil {
		return err
	}
	for index, rawCheck := range checks {
		checkPath := fmt.Sprintf("%s[%d]", path, index)
		check, err := decodeNativeResponsesObject(rawCheck, checkPath)
		if err != nil {
			return err
		}
		if err := rejectUnknownNativeResponsesFields(check, checkPath, "id", "code", "message"); err != nil {
			return err
		}
		if _, err := nativeResponsesRequiredNonEmptyString(check, "id", checkPath+".id"); err != nil {
			return err
		}
		for _, optional := range []string{"code", "message"} {
			if err := validateNativeResponsesOptionalString(check, optional, checkPath+"."+optional, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateNativeResponsesComputerAction(raw json.RawMessage, path string) error {
	fields, err := decodeNativeResponsesObject(raw, path)
	if err != nil {
		return err
	}
	actionType, err := nativeResponsesRequiredString(fields, "type", path+".type")
	if err != nil {
		return err
	}
	requireCoordinates := func(names ...string) error {
		for _, name := range names {
			if _, err := nativeResponsesRequiredInteger(fields, name, path+"."+name); err != nil {
				return err
			}
		}
		return nil
	}
	validateKeys := func(required bool) error {
		if !required {
			return validateNativeResponsesNullableStringArray(fields, "keys", path+".keys")
		}
		return validateNativeResponsesStringArray(fields, "keys", path+".keys")
	}
	switch actionType {
	case "click":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "button", "x", "y", "keys"); err != nil {
			return err
		}
		button, err := nativeResponsesRequiredString(fields, "button", path+".button")
		if err != nil {
			return err
		}
		if button != "left" && button != "right" && button != "wheel" && button != "back" && button != "forward" {
			return upstreamResponseError(ProtocolResponses, path+".button", "unsupported mouse button %q", button)
		}
		if err := requireCoordinates("x", "y"); err != nil {
			return err
		}
		return validateKeys(false)
	case "double_click":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "x", "y", "keys"); err != nil {
			return err
		}
		if err := requireCoordinates("x", "y"); err != nil {
			return err
		}
		return validateKeys(true)
	case "drag":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "path", "keys"); err != nil {
			return err
		}
		points, err := nativeResponsesJSONArrayField(fields, "path", path+".path", true)
		if err != nil {
			return err
		}
		for index, rawPoint := range points {
			pointPath := fmt.Sprintf("%s.path[%d]", path, index)
			point, err := decodeNativeResponsesObject(rawPoint, pointPath)
			if err != nil {
				return err
			}
			if err := rejectUnknownNativeResponsesFields(point, pointPath, "x", "y"); err != nil {
				return err
			}
			for _, coordinate := range []string{"x", "y"} {
				if _, err := nativeResponsesRequiredInteger(point, coordinate, pointPath+"."+coordinate); err != nil {
					return err
				}
			}
		}
		return validateKeys(false)
	case "keypress":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "keys"); err != nil {
			return err
		}
		return validateKeys(true)
	case "move":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "x", "y", "keys"); err != nil {
			return err
		}
		if err := requireCoordinates("x", "y"); err != nil {
			return err
		}
		return validateKeys(false)
	case "screenshot", "wait":
		return rejectUnknownNativeResponsesFields(fields, path, "type")
	case "scroll":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "scroll_x", "scroll_y", "x", "y", "keys"); err != nil {
			return err
		}
		if err := requireCoordinates("scroll_x", "scroll_y", "x", "y"); err != nil {
			return err
		}
		return validateKeys(false)
	case "type":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "text"); err != nil {
			return err
		}
		_, err := nativeResponsesRequiredString(fields, "text", path+".text")
		return err
	default:
		return upstreamResponseError(ProtocolResponses, path+".type", "unsupported computer action type %q", actionType)
	}
}

func validateNativeResponsesComputerCall(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "call_id", path+".call_id"); err != nil {
		return err
	}
	if err := validateNativeResponsesSafetyChecks(fields, "pending_safety_checks", path+".pending_safety_checks", true); err != nil {
		return err
	}
	if action, exists := fields["action"]; exists {
		if nativeResponsesNull(action) {
			return upstreamResponseError(ProtocolResponses, path+".action", "must be an object when present")
		}
		if err := validateNativeResponsesComputerAction(action, path+".action"); err != nil {
			return err
		}
	}
	if actions, exists := fields["actions"]; exists {
		if nativeResponsesNull(actions) {
			return upstreamResponseError(ProtocolResponses, path+".actions", "must be an array when present")
		}
		values, err := nativeResponsesJSONArrayField(fields, "actions", path+".actions", true)
		if err != nil {
			return err
		}
		for index, action := range values {
			if err := validateNativeResponsesComputerAction(action, fmt.Sprintf("%s.actions[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateNativeResponsesComputerOutput(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "call_id", path+".call_id"); err != nil {
		return err
	}
	output, exists := fields["output"]
	if !exists || nativeResponsesNull(output) {
		return upstreamResponseError(ProtocolResponses, path+".output", "computer screenshot output is required")
	}
	screenshot, err := decodeNativeResponsesObject(output, path+".output")
	if err != nil {
		return err
	}
	outputType, err := nativeResponsesRequiredString(screenshot, "type", path+".output.type")
	if err != nil || outputType != "computer_screenshot" {
		if err != nil {
			return err
		}
		return upstreamResponseError(ProtocolResponses, path+".output.type", "must be computer_screenshot")
	}
	if err := rejectUnknownNativeResponsesFields(screenshot, path+".output", "type", "file_id", "image_url"); err != nil {
		return err
	}
	for _, field := range []string{"file_id", "image_url"} {
		if err := validateNativeResponsesOptionalString(screenshot, field, path+".output."+field, false); err != nil {
			return err
		}
	}
	if checks, exists := fields["acknowledged_safety_checks"]; exists {
		if nativeResponsesNull(checks) {
			return upstreamResponseError(ProtocolResponses, path+".acknowledged_safety_checks", "must be an array when present")
		}
		if err := validateNativeResponsesSafetyChecks(fields, "acknowledged_safety_checks", path+".acknowledged_safety_checks", true); err != nil {
			return err
		}
	}
	return validateNativeResponsesOptionalString(fields, "created_by", path+".created_by", false)
}

func validateNativeResponsesProgramItem(fields map[string]json.RawMessage, path string, output bool) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "call_id", path+".call_id"); err != nil {
		return err
	}
	field := "code"
	if output {
		field = "result"
	}
	_, err := nativeResponsesRequiredString(fields, field, path+"."+field)
	if err != nil {
		return err
	}
	if !output {
		_, err = nativeResponsesRequiredString(fields, "fingerprint", path+".fingerprint")
	}
	return err
}

func validateNativeResponsesCompactionItem(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredString(fields, "encrypted_content", path+".encrypted_content"); err != nil {
		return err
	}
	return validateNativeResponsesOptionalString(fields, "created_by", path+".created_by", false)
}

func validateNativeResponsesImageGenerationItem(fields map[string]json.RawMessage, path string) error {
	_, err := nativeResponsesRequiredString(fields, "result", path+".result")
	return err
}

func validateNativeResponsesCodeInterpreterItem(fields map[string]json.RawMessage, path string) error {
	code, exists := fields["code"]
	if !exists {
		return upstreamResponseError(ProtocolResponses, path+".code", "field is required and must be a string or null")
	}
	if !nativeResponsesNull(code) {
		if _, ok := decodeNativeJSONString(code); !ok {
			return upstreamResponseError(ProtocolResponses, path+".code", "must be a string or null")
		}
	}
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "container_id", path+".container_id"); err != nil {
		return err
	}
	outputs, exists := fields["outputs"]
	if !exists {
		return upstreamResponseError(ProtocolResponses, path+".outputs", "field is required and must be an array or null")
	}
	if nativeResponsesNull(outputs) {
		return nil
	}
	values, err := nativeResponsesJSONArrayField(fields, "outputs", path+".outputs", true)
	if err != nil {
		return err
	}
	for index, rawOutput := range values {
		outputPath := fmt.Sprintf("%s.outputs[%d]", path, index)
		output, err := decodeNativeResponsesObject(rawOutput, outputPath)
		if err != nil {
			return err
		}
		outputType, err := nativeResponsesRequiredString(output, "type", outputPath+".type")
		if err != nil {
			return err
		}
		switch outputType {
		case "logs":
			if err := rejectUnknownNativeResponsesFields(output, outputPath, "type", "logs"); err != nil {
				return err
			}
			if _, err := nativeResponsesRequiredString(output, "logs", outputPath+".logs"); err != nil {
				return err
			}
		case "image":
			if err := rejectUnknownNativeResponsesFields(output, outputPath, "type", "url"); err != nil {
				return err
			}
			if _, err := nativeResponsesRequiredNonEmptyString(output, "url", outputPath+".url"); err != nil {
				return err
			}
		default:
			return upstreamResponseError(ProtocolResponses, outputPath+".type", "unsupported code interpreter output type %q", outputType)
		}
	}
	return nil
}

func validateNativeResponsesLocalShellAction(raw json.RawMessage, path string) error {
	fields, err := decodeNativeResponsesObject(raw, path)
	if err != nil {
		return err
	}
	actionType, err := nativeResponsesRequiredString(fields, "type", path+".type")
	if err != nil || actionType != "exec" {
		if err != nil {
			return err
		}
		return upstreamResponseError(ProtocolResponses, path+".type", "must be exec")
	}
	if err := rejectUnknownNativeResponsesFields(fields, path, "type", "command", "env", "timeout_ms", "user", "working_directory"); err != nil {
		return err
	}
	if err := validateNativeResponsesStringArray(fields, "command", path+".command"); err != nil {
		return err
	}
	env, exists := fields["env"]
	if !exists {
		return upstreamResponseError(ProtocolResponses, path+".env", "field is required and must be an object")
	}
	if err := validateNativeResponsesStringMap(env, path+".env"); err != nil {
		return err
	}
	if err := validateNativeResponsesOptionalInteger(fields, "timeout_ms", path+".timeout_ms", true); err != nil {
		return err
	}
	for _, field := range []string{"user", "working_directory"} {
		if err := validateNativeResponsesOptionalString(fields, field, path+"."+field, true); err != nil {
			return err
		}
	}
	return nil
}

func validateNativeResponsesLocalShellCall(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "call_id", path+".call_id"); err != nil {
		return err
	}
	action, exists := fields["action"]
	if !exists || nativeResponsesNull(action) {
		return upstreamResponseError(ProtocolResponses, path+".action", "local shell action is required")
	}
	return validateNativeResponsesLocalShellAction(action, path+".action")
}

func validateNativeResponsesLocalShellOutput(fields map[string]json.RawMessage, path string) error {
	_, err := nativeResponsesRequiredString(fields, "output", path+".output")
	return err
}

func validateNativeResponsesShellEnvironment(raw json.RawMessage, path string) error {
	fields, err := decodeNativeResponsesObject(raw, path)
	if err != nil {
		return err
	}
	environmentType, err := nativeResponsesRequiredString(fields, "type", path+".type")
	if err != nil {
		return err
	}
	switch environmentType {
	case "local":
		return rejectUnknownNativeResponsesFields(fields, path, "type")
	case "container_reference":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "container_id"); err != nil {
			return err
		}
		_, err = nativeResponsesRequiredNonEmptyString(fields, "container_id", path+".container_id")
		return err
	default:
		return upstreamResponseError(ProtocolResponses, path+".type", "unsupported shell environment type %q", environmentType)
	}
}

func validateNativeResponsesShellCall(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "call_id", path+".call_id"); err != nil {
		return err
	}
	actionRaw, exists := fields["action"]
	if !exists || nativeResponsesNull(actionRaw) {
		return upstreamResponseError(ProtocolResponses, path+".action", "shell action is required")
	}
	action, err := decodeNativeResponsesObject(actionRaw, path+".action")
	if err != nil {
		return err
	}
	if err := rejectUnknownNativeResponsesFields(action, path+".action", "commands", "max_output_length", "timeout_ms"); err != nil {
		return err
	}
	if err := validateNativeResponsesStringArray(action, "commands", path+".action.commands"); err != nil {
		return err
	}
	for _, field := range []string{"max_output_length", "timeout_ms"} {
		if _, err := nativeResponsesRequiredInteger(action, field, path+".action."+field); err != nil {
			return err
		}
	}
	environment, exists := fields["environment"]
	if !exists || nativeResponsesNull(environment) {
		return upstreamResponseError(ProtocolResponses, path+".environment", "shell environment is required")
	}
	if err := validateNativeResponsesShellEnvironment(environment, path+".environment"); err != nil {
		return err
	}
	if err := validateNativeResponsesCaller(fields["caller"], path+".caller"); err != nil {
		return err
	}
	return validateNativeResponsesOptionalString(fields, "created_by", path+".created_by", false)
}

func validateNativeResponsesShellOutput(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "call_id", path+".call_id"); err != nil {
		return err
	}
	if _, err := nativeResponsesRequiredInteger(fields, "max_output_length", path+".max_output_length"); err != nil {
		return err
	}
	outputs, err := nativeResponsesJSONArrayField(fields, "output", path+".output", true)
	if err != nil {
		return err
	}
	for index, rawOutput := range outputs {
		outputPath := fmt.Sprintf("%s.output[%d]", path, index)
		output, err := decodeNativeResponsesObject(rawOutput, outputPath)
		if err != nil {
			return err
		}
		if err := rejectUnknownNativeResponsesFields(output, outputPath, "outcome", "stderr", "stdout", "created_by"); err != nil {
			return err
		}
		for _, field := range []string{"stdout", "stderr"} {
			if _, err := nativeResponsesRequiredString(output, field, outputPath+"."+field); err != nil {
				return err
			}
		}
		outcomeRaw, exists := output["outcome"]
		if !exists || nativeResponsesNull(outcomeRaw) {
			return upstreamResponseError(ProtocolResponses, outputPath+".outcome", "shell outcome is required")
		}
		outcome, err := decodeNativeResponsesObject(outcomeRaw, outputPath+".outcome")
		if err != nil {
			return err
		}
		outcomeType, err := nativeResponsesRequiredString(outcome, "type", outputPath+".outcome.type")
		if err != nil {
			return err
		}
		switch outcomeType {
		case "timeout":
			if err := rejectUnknownNativeResponsesFields(outcome, outputPath+".outcome", "type"); err != nil {
				return err
			}
		case "exit":
			if err := rejectUnknownNativeResponsesFields(outcome, outputPath+".outcome", "type", "exit_code"); err != nil {
				return err
			}
			if _, err := nativeResponsesRequiredInteger(outcome, "exit_code", outputPath+".outcome.exit_code"); err != nil {
				return err
			}
		default:
			return upstreamResponseError(ProtocolResponses, outputPath+".outcome.type", "unsupported shell outcome type %q", outcomeType)
		}
		if err := validateNativeResponsesOptionalString(output, "created_by", outputPath+".created_by", false); err != nil {
			return err
		}
	}
	if err := validateNativeResponsesCaller(fields["caller"], path+".caller"); err != nil {
		return err
	}
	return validateNativeResponsesOptionalString(fields, "created_by", path+".created_by", false)
}

func validateNativeResponsesApplyPatchCall(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "call_id", path+".call_id"); err != nil {
		return err
	}
	operationRaw, exists := fields["operation"]
	if !exists || nativeResponsesNull(operationRaw) {
		return upstreamResponseError(ProtocolResponses, path+".operation", "apply patch operation is required")
	}
	operation, err := decodeNativeResponsesObject(operationRaw, path+".operation")
	if err != nil {
		return err
	}
	operationType, err := nativeResponsesRequiredString(operation, "type", path+".operation.type")
	if err != nil {
		return err
	}
	if _, err := nativeResponsesRequiredNonEmptyString(operation, "path", path+".operation.path"); err != nil {
		return err
	}
	switch operationType {
	case "create_file", "update_file":
		if err := rejectUnknownNativeResponsesFields(operation, path+".operation", "type", "path", "diff"); err != nil {
			return err
		}
		if _, err := nativeResponsesRequiredString(operation, "diff", path+".operation.diff"); err != nil {
			return err
		}
	case "delete_file":
		if err := rejectUnknownNativeResponsesFields(operation, path+".operation", "type", "path"); err != nil {
			return err
		}
	default:
		return upstreamResponseError(ProtocolResponses, path+".operation.type", "unsupported apply patch operation %q", operationType)
	}
	if err := validateNativeResponsesCaller(fields["caller"], path+".caller"); err != nil {
		return err
	}
	return validateNativeResponsesOptionalString(fields, "created_by", path+".created_by", false)
}

func validateNativeResponsesApplyPatchOutput(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "call_id", path+".call_id"); err != nil {
		return err
	}
	if err := validateNativeResponsesCaller(fields["caller"], path+".caller"); err != nil {
		return err
	}
	if err := validateNativeResponsesOptionalString(fields, "created_by", path+".created_by", false); err != nil {
		return err
	}
	return validateNativeResponsesOptionalString(fields, "output", path+".output", true)
}

func validateNativeResponsesMCPError(raw json.RawMessage, path string) error {
	if !jsonValuePresent(raw) {
		return nil
	}
	fields, err := decodeNativeResponsesObject(raw, path)
	if err != nil {
		return err
	}
	errorType, err := nativeResponsesRequiredString(fields, "type", path+".type")
	if err != nil {
		return err
	}
	switch errorType {
	case "mcp_protocol_error", "http_error":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "code", "message"); err != nil {
			return err
		}
		if _, err := nativeResponsesRequiredInteger(fields, "code", path+".code"); err != nil {
			return err
		}
		_, err = nativeResponsesRequiredString(fields, "message", path+".message")
		return err
	case "mcp_tool_execution_error":
		if err := rejectUnknownNativeResponsesFields(fields, path, "type", "content"); err != nil {
			return err
		}
		if content, exists := fields["content"]; !exists || len(bytes.TrimSpace(content)) == 0 {
			return upstreamResponseError(ProtocolResponses, path+".content", "field is required")
		}
		return nil
	default:
		return upstreamResponseError(ProtocolResponses, path+".type", "unsupported MCP error type %q", errorType)
	}
}

func validateNativeResponsesMCPCall(fields map[string]json.RawMessage, path string) error {
	for _, field := range []string{"arguments", "name", "server_label"} {
		if _, err := nativeResponsesRequiredNonEmptyString(fields, field, path+"."+field); err != nil {
			return err
		}
	}
	for _, field := range []string{"approval_request_id", "output"} {
		if err := validateNativeResponsesOptionalString(fields, field, path+"."+field, true); err != nil {
			return err
		}
	}
	return validateNativeResponsesMCPError(fields["error"], path+".error")
}

func validateNativeResponsesMCPListTools(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "server_label", path+".server_label"); err != nil {
		return err
	}
	tools, err := nativeResponsesJSONArrayField(fields, "tools", path+".tools", true)
	if err != nil {
		return err
	}
	for index, rawTool := range tools {
		toolPath := fmt.Sprintf("%s.tools[%d]", path, index)
		tool, err := decodeNativeResponsesObject(rawTool, toolPath)
		if err != nil {
			return err
		}
		if err := rejectUnknownNativeResponsesFields(tool, toolPath, "input_schema", "name", "annotations", "description"); err != nil {
			return err
		}
		if inputSchema, exists := tool["input_schema"]; !exists || len(bytes.TrimSpace(inputSchema)) == 0 {
			return upstreamResponseError(ProtocolResponses, toolPath+".input_schema", "field is required")
		}
		if _, err := nativeResponsesRequiredNonEmptyString(tool, "name", toolPath+".name"); err != nil {
			return err
		}
		if err := validateNativeResponsesOptionalString(tool, "description", toolPath+".description", true); err != nil {
			return err
		}
	}
	return validateNativeResponsesOptionalString(fields, "error", path+".error", true)
}

func validateNativeResponsesMCPApprovalRequest(fields map[string]json.RawMessage, path string) error {
	for _, field := range []string{"arguments", "name", "server_label"} {
		if _, err := nativeResponsesRequiredNonEmptyString(fields, field, path+"."+field); err != nil {
			return err
		}
	}
	return nil
}

func validateNativeResponsesMCPApprovalResponse(fields map[string]json.RawMessage, path string) error {
	if _, err := nativeResponsesRequiredNonEmptyString(fields, "approval_request_id", path+".approval_request_id"); err != nil {
		return err
	}
	if _, err := nativeResponsesRequiredBool(fields, "approve", path+".approve"); err != nil {
		return err
	}
	return validateNativeResponsesOptionalString(fields, "reason", path+".reason", true)
}

func nativeResponsesOutputItemTypeSupported(itemType string) bool {
	switch itemType {
	case "message", "file_search_call", "function_call", "function_call_output", "web_search_call",
		"computer_call", "computer_call_output", "reasoning", "program", "program_output",
		"tool_search_call", "tool_search_output", "additional_tools", "compaction", "image_generation_call",
		"code_interpreter_call", "local_shell_call", "local_shell_call_output", "shell_call", "shell_call_output",
		"apply_patch_call", "apply_patch_call_output", "mcp_call", "mcp_list_tools", "mcp_approval_request",
		"mcp_approval_response", "custom_tool_call", "custom_tool_call_output":
		return true
	default:
		return false
	}
}

// ValidateNativeResponsesOutputItem validates one complete native Responses
// output item against the v3.56 output-item union. Unlike the route-level
// validator, this native boundary is strict about unknown discriminators while
// retaining unknown top-level extension fields on known variants.
func ValidateNativeResponsesOutputItem(raw json.RawMessage) error {
	fields, err := decodeNativeResponsesObject(raw, "$.item")
	if err != nil {
		return err
	}
	return validateNativeResponsesToolItem(fields, "$.item")
}

func validateNativeResponsesToolItem(fields map[string]json.RawMessage, path string) error {
	itemType, err := nativeResponsesRequiredString(fields, "type", path+".type")
	if err != nil {
		return err
	}
	if !nativeResponsesOutputItemTypeSupported(itemType) {
		return upstreamResponseError(ProtocolResponses, path+".type", "type %q is not part of the Responses output item union", itemType)
	}
	if err := validateNativeResponsesTerminalItemStatus(fields, itemType, path); err != nil {
		return err
	}
	requireStatus := func(statuses ...string) error {
		status, err := nativeResponsesRequiredString(fields, "status", path+".status")
		if err != nil {
			return err
		}
		for _, allowed := range statuses {
			if status == allowed {
				return nil
			}
		}
		return upstreamResponseError(ProtocolResponses, path+".status", "unsupported %s status %q", itemType, status)
	}
	validateOptionalStatus := func(statuses ...string) error {
		raw, exists := fields["status"]
		if !exists {
			return nil
		}
		if nativeResponsesNull(raw) {
			return upstreamResponseError(ProtocolResponses, path+".status", "%s status must be a string when present", itemType)
		}
		var status string
		if err := json.Unmarshal(raw, &status); err != nil {
			return upstreamResponseError(ProtocolResponses, path+".status", "%s status must be a string when present", itemType)
		}
		for _, allowed := range statuses {
			if status == allowed {
				return nil
			}
		}
		return upstreamResponseError(ProtocolResponses, path+".status", "unsupported %s status %q", itemType, status)
	}
	requireToolArray := func(field string) error {
		raw, exists := fields[field]
		if !exists {
			return upstreamResponseError(ProtocolResponses, path+"."+field, "field is required and must be an array")
		}
		return routekit.ValidateResponsesProviderToolDefinitions(ProtocolResponses, raw, path+"."+field)
	}
	switch itemType {
	case "message":
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		role, err := nativeResponsesRequiredString(fields, "role", path+".role")
		if err != nil {
			return err
		}
		if role != "assistant" {
			return upstreamResponseError(ProtocolResponses, path+".role", "output message role must be assistant")
		}
		if phase, exists := fields["phase"]; exists && !nativeResponsesNull(phase) {
			value, err := nativeResponsesRequiredString(fields, "phase", path+".phase")
			if err != nil {
				return err
			}
			if value != "commentary" && value != "final_answer" {
				return upstreamResponseError(ProtocolResponses, path+".phase", "unsupported message phase %q", value)
			}
		}
		if _, err := decodeNativeResponsesParts(fields["content"], path+".content", "output_text", "refusal"); err != nil {
			return err
		}
	case "reasoning":
		if err := validateOptionalStatus("completed", "incomplete"); err != nil {
			return err
		}
		if _, err := decodeNativeResponsesParts(fields["summary"], path+".summary", "summary_text"); err != nil {
			return err
		}
		if content, exists := fields["content"]; exists {
			if nativeResponsesNull(content) {
				return upstreamResponseError(ProtocolResponses, path+".content", "must be an array when present")
			}
			if _, err := decodeNativeResponsesParts(content, path+".content", "reasoning_text"); err != nil {
				return err
			}
		}
		if encrypted, exists := fields["encrypted_content"]; exists && !nativeResponsesNull(encrypted) {
			var value string
			if err := json.Unmarshal(encrypted, &value); err != nil {
				return upstreamResponseError(ProtocolResponses, path+".encrypted_content", "must be a string or null")
			}
		}
	case "function_call":
		if err := validateNativeResponsesKnownCallFields(fields, itemType); err != nil {
			return err
		}
		if _, exists := fields["created_by"]; exists {
			return upstreamResponseError(ProtocolResponses, path+".created_by", "field is not valid for function_call")
		}
		if err := validateOptionalStatus("completed", "incomplete"); err != nil {
			return err
		}
		callID, err := nativeResponsesRequiredString(fields, "call_id", path+".call_id")
		if err != nil || callID == "" {
			return upstreamResponseError(ProtocolResponses, path+".call_id", "function_call call_id is required")
		}
		name, err := nativeResponsesRequiredString(fields, "name", path+".name")
		if err != nil || name == "" {
			return upstreamResponseError(ProtocolResponses, path+".name", "function_call name is required")
		}
		if _, ok := decodeNativeJSONString(fields["arguments"]); !ok {
			return upstreamResponseError(ProtocolResponses, path+".arguments", "function_call arguments must be a JSON string")
		}
	case "custom_tool_call":
		for _, field := range []string{"status", "created_by"} {
			if _, exists := fields[field]; exists {
				return upstreamResponseError(ProtocolResponses, path+"."+field, "field is not valid for custom_tool_call")
			}
		}
		if err := validateNativeResponsesKnownCallFields(fields, itemType); err != nil {
			return err
		}
		callID, err := nativeResponsesRequiredString(fields, "call_id", path+".call_id")
		if err != nil || callID == "" {
			return upstreamResponseError(ProtocolResponses, path+".call_id", "custom_tool_call call_id is required")
		}
		name, err := nativeResponsesRequiredString(fields, "name", path+".name")
		if err != nil || name == "" {
			return upstreamResponseError(ProtocolResponses, path+".name", "custom_tool_call name is required")
		}
		if _, ok := decodeNativeJSONString(fields["input"]); !ok {
			return upstreamResponseError(ProtocolResponses, path+".input", "custom_tool_call input must be a JSON string")
		}
	case "function_call_output":
		if err := validateNativeResponsesKnownCallFields(fields, itemType); err != nil {
			return err
		}
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		if err := routekit.ValidateResponsesProviderToolOutput(ProtocolResponses, fields["output"], path+".output"); err != nil {
			return err
		}
	case "custom_tool_call_output":
		if err := validateNativeResponsesKnownCallFields(fields, itemType); err != nil {
			return err
		}
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		callID, err := nativeResponsesRequiredString(fields, "call_id", path+".call_id")
		if err != nil || callID == "" {
			return upstreamResponseError(ProtocolResponses, path+".call_id", "custom_tool_call_output call_id is required")
		}
		if err := routekit.ValidateResponsesProviderToolOutput(ProtocolResponses, fields["output"], path+".output"); err != nil {
			return err
		}
	case "web_search_call":
		if err := requireStatus("completed", "failed"); err != nil {
			return err
		}
		action, exists := fields["action"]
		if !exists || nativeResponsesNull(action) {
			return upstreamResponseError(ProtocolResponses, path+".action", "web_search_call action object with type is required")
		}
		return validateNativeResponsesWebSearchAction(action, path+".action")
	case "file_search_call":
		if err := requireStatus("completed", "incomplete", "failed"); err != nil {
			return err
		}
		return validateNativeResponsesFileSearchItem(fields, path)
	case "computer_call":
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		return validateNativeResponsesComputerCall(fields, path)
	case "computer_call_output":
		if err := requireStatus("completed", "incomplete", "failed"); err != nil {
			return err
		}
		return validateNativeResponsesComputerOutput(fields, path)
	case "program":
		return validateNativeResponsesProgramItem(fields, path, false)
	case "program_output":
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		return validateNativeResponsesProgramItem(fields, path, true)
	case "tool_search_call":
		if err := validateNativeResponsesKnownCallFields(fields, itemType); err != nil {
			return err
		}
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		callID, err := nativeResponsesRequiredString(fields, "call_id", path+".call_id")
		if err != nil || callID == "" {
			return upstreamResponseError(ProtocolResponses, path+".call_id", "tool_search_call call_id is required")
		}
		execution, err := nativeResponsesRequiredString(fields, "execution", path+".execution")
		if err != nil || execution != "server" && execution != "client" {
			return upstreamResponseError(ProtocolResponses, path+".execution", "tool_search_call execution must be server or client")
		}
		arguments, exists := fields["arguments"]
		if !exists || nativeResponsesNull(arguments) {
			return upstreamResponseError(ProtocolResponses, path+".arguments", "tool_search_call arguments are required")
		}
	case "tool_search_output":
		if err := validateNativeResponsesKnownCallFields(fields, itemType); err != nil {
			return err
		}
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		callID, err := nativeResponsesRequiredString(fields, "call_id", path+".call_id")
		if err != nil || callID == "" {
			return upstreamResponseError(ProtocolResponses, path+".call_id", "tool_search_output call_id is required")
		}
		execution, err := nativeResponsesRequiredString(fields, "execution", path+".execution")
		if err != nil || execution != "server" && execution != "client" {
			return upstreamResponseError(ProtocolResponses, path+".execution", "tool_search_output execution must be server or client")
		}
		return requireToolArray("tools")
	case "additional_tools":
		if err := validateNativeResponsesKnownCallFields(fields, itemType); err != nil {
			return err
		}
		role, err := nativeResponsesRequiredString(fields, "role", path+".role")
		if err != nil {
			return err
		}
		switch role {
		case "unknown", "user", "assistant", "system", "critic", "discriminator", "developer", "tool":
		default:
			return upstreamResponseError(ProtocolResponses, path+".role", "unsupported additional_tools role %q", role)
		}
		return requireToolArray("tools")
	case "compaction":
		return validateNativeResponsesCompactionItem(fields, path)
	case "image_generation_call":
		if err := requireStatus("completed", "failed"); err != nil {
			return err
		}
		return validateNativeResponsesImageGenerationItem(fields, path)
	case "code_interpreter_call":
		if err := requireStatus("completed", "incomplete", "failed"); err != nil {
			return err
		}
		return validateNativeResponsesCodeInterpreterItem(fields, path)
	case "local_shell_call":
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		return validateNativeResponsesLocalShellCall(fields, path)
	case "local_shell_call_output":
		if rawStatus, exists := fields["status"]; exists && !nativeResponsesNull(rawStatus) {
			if err := validateOptionalStatus("completed", "incomplete"); err != nil {
				return err
			}
		}
		return validateNativeResponsesLocalShellOutput(fields, path)
	case "shell_call":
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		return validateNativeResponsesShellCall(fields, path)
	case "shell_call_output":
		if err := requireStatus("completed", "incomplete"); err != nil {
			return err
		}
		return validateNativeResponsesShellOutput(fields, path)
	case "apply_patch_call":
		if err := requireStatus("completed"); err != nil {
			return err
		}
		return validateNativeResponsesApplyPatchCall(fields, path)
	case "apply_patch_call_output":
		if err := requireStatus("completed", "failed"); err != nil {
			return err
		}
		return validateNativeResponsesApplyPatchOutput(fields, path)
	case "mcp_call":
		if err := validateOptionalStatus("completed", "incomplete", "failed"); err != nil {
			return err
		}
		return validateNativeResponsesMCPCall(fields, path)
	case "mcp_list_tools":
		return validateNativeResponsesMCPListTools(fields, path)
	case "mcp_approval_request":
		return validateNativeResponsesMCPApprovalRequest(fields, path)
	case "mcp_approval_response":
		return validateNativeResponsesMCPApprovalResponse(fields, path)
	}
	return nil
}

// validateNativeResponsesKnownCallFields reuses the complete Responses item
// schema for call-shaped variants while filtering only unknown top-level
// extension fields. This keeps native pass-through extensible without letting
// malformed known fields (async, caller, namespace, created_by, and so on)
// enter synthesized SSE events.
func validateNativeResponsesKnownCallFields(fields map[string]json.RawMessage, itemType string) error {
	allowedByType := map[string][]string{
		"function_call":           {"type", "id", "call_id", "name", "arguments", "async", "namespace", "caller", "status"},
		"function_call_output":    {"type", "id", "call_id", "name", "namespace", "caller", "status", "output", "created_by"},
		"custom_tool_call":        {"type", "id", "call_id", "name", "input", "async", "namespace", "caller"},
		"custom_tool_call_output": {"type", "id", "call_id", "output", "caller", "status", "created_by"},
		"tool_search_call":        {"type", "id", "call_id", "arguments", "execution", "status", "created_by"},
		"tool_search_output":      {"type", "id", "call_id", "execution", "status", "tools", "created_by"},
		"additional_tools":        {"type", "id", "role", "tools"},
	}
	allowed, ok := allowedByType[itemType]
	if !ok {
		return nil
	}
	filtered := make(map[string]json.RawMessage, len(allowed))
	for _, field := range allowed {
		if value, exists := fields[field]; exists {
			filtered[field] = value
		}
	}
	return routekit.ValidateResponsesOutputItems(ProtocolResponses, mustJSON(map[string]any{"output": []any{filtered}}))
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
