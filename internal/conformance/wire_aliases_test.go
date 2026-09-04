package conformance

import (
	"encoding/json"
	"reflect"

	jsonx "github.com/2218342221/RouteMorphSDK/internal/jsonx"
	chatwire "github.com/2218342221/RouteMorphSDK/internal/wire/chat"
	geminiwire "github.com/2218342221/RouteMorphSDK/internal/wire/gemini"
	messageswire "github.com/2218342221/RouteMorphSDK/internal/wire/messages"
	responseswire "github.com/2218342221/RouteMorphSDK/internal/wire/responses"
)

type chatRequest = chatwire.Request
type chatResponse = chatwire.Response
type geminiRequest = geminiwire.Request
type geminiResponse = geminiwire.Response
type messagesRequest = messageswire.Request
type messagesResponse = messageswire.Response
type messagesBlock = messageswire.Block
type responsesRequest = responseswire.Request
type responsesItem = responseswire.Item
type responsesContentPart = responseswire.ContentPart
type responsesResponse = responseswire.Response

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

const geminiThoughtSignatureBypass = geminiwire.ExternalFunctionCallSignature

func rawString(raw json.RawMessage) string { return jsonx.RawString(raw) }

func jsonValuePresent(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func jsonObjectsEqual(left, right []byte) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func decodeStop(protocol Protocol, raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, invalid(protocol, "$.stop", "invalid stop value")
		}
		return []string{value}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, invalid(protocol, "$.stop", "stop must be a string or string array")
	}
	return values, nil
}
