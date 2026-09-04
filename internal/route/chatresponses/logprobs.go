package chatresponses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// Chat and Responses use the same token-logprob entry shape. Chat wraps the
// arrays in a choice-level object, while Responses stores the content array on
// each output_text part.
type chatLogprobs struct {
	Content json.RawMessage `json:"content"`
	Refusal json.RawMessage `json:"refusal"`
}

func decodeChatLogprobs(raw json.RawMessage, path string) (content, refusal []json.RawMessage, err error) {
	if !jsonValuePresent(raw) {
		return nil, nil, nil
	}
	var envelope chatLogprobs
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, invalid(ProtocolChat, path, "logprobs must be an object: %v", err)
	}
	content, err = decodeLogprobArray(ProtocolChat, path+".content", envelope.Content)
	if err != nil {
		return nil, nil, err
	}
	refusal, err = decodeLogprobArray(ProtocolChat, path+".refusal", envelope.Refusal)
	return content, refusal, err
}

func decodeLogprobArray(protocol Protocol, path string, raw json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, invalid(protocol, path, "logprobs must be an array or null: %v", err)
	}
	for index, entry := range entries {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(entry, &object); err != nil || object == nil {
			return nil, invalid(protocol, fmt.Sprintf("%s[%d]", path, index), "logprob entry must be an object")
		}
	}
	return entries, nil
}

func encodeChatLogprobs(content, refusal []json.RawMessage) json.RawMessage {
	if len(content) == 0 && len(refusal) == 0 {
		return nil
	}
	value := map[string]any{"content": nil, "refusal": nil}
	if len(content) > 0 {
		value["content"] = content
	}
	if len(refusal) > 0 {
		value["refusal"] = refusal
	}
	return mustJSON(value)
}

func responsesContentAndLogprobs(raw json.RawMessage, path string) ([]portablePart, []json.RawMessage, error) {
	if !jsonValuePresent(raw) {
		return nil, nil, nil
	}
	var content []responsesContentPart
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, nil, invalid(ProtocolResponses, path, "content must be an array: %v", err)
	}
	var logprobs []json.RawMessage
	for index := range content {
		entries, err := decodeLogprobArray(ProtocolResponses, fmt.Sprintf("%s[%d].logprobs", path, index), content[index].Logprobs)
		if err != nil {
			return nil, nil, err
		}
		if len(entries) > 0 && content[index].Type != "output_text" {
			return nil, nil, unsupported(ProtocolResponses, fmt.Sprintf("%s[%d].logprobs", path, index), "only output_text logprobs have a Chat equivalent")
		}
		logprobs = append(logprobs, entries...)
		content[index].Logprobs = nil
	}
	clean, err := json.Marshal(content)
	if err != nil {
		return nil, nil, invalid(ProtocolResponses, path, "invalid content: %v", err)
	}
	parts, err := decodeResponsesContentRaw(clean, path, false)
	return parts, logprobs, err
}

func attachResponsesLogprobs(content []responsesContentPart, entries []json.RawMessage, path string) ([]responsesContentPart, error) {
	if len(entries) == 0 {
		return content, nil
	}
	for index := range content {
		if content[index].Type == "output_text" {
			content[index].Logprobs = mustJSON(entries)
			return content, nil
		}
	}
	return nil, upstreamResponseError(ProtocolChat, path, "content logprobs were returned without output text")
}

func logprobSuffix(emitted, complete []json.RawMessage, path string) ([]json.RawMessage, error) {
	if len(complete) == 0 {
		return nil, nil
	}
	if len(emitted) > len(complete) {
		return nil, upstreamResponseError(ProtocolResponses, path, "terminal logprobs are shorter than streamed logprobs")
	}
	for index := range emitted {
		if !jsonObjectsEqual(emitted[index], complete[index]) {
			return nil, upstreamResponseError(ProtocolResponses, fmt.Sprintf("%s[%d]", path, index), "terminal logprobs do not match streamed logprobs")
		}
	}
	return complete[len(emitted):], nil
}

func jsonObjectsEqual(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
