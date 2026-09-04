package codec

import (
	"encoding/json"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func rawJSONValuePresent(raw json.RawMessage) bool {
	trimmed := string(raw)
	return len(raw) > 0 && trimmed != "null" && trimmed != "{}" && trimmed != "[]" && trimmed != `""`
}

func inspectChatStreamIncludeUsageObject(object map[string]json.RawMessage) (bool, bool, error) {
	if !rawJSONValuePresent(object["stream_options"]) {
		return false, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(object["stream_options"], &fields); err != nil {
		return false, false, invalid(ProtocolChat, "$.stream_options", "stream_options must be an object")
	}
	includeUsage, includeUsageSet := false, false
	for name, value := range fields {
		if name != "include_usage" {
			return false, false, core.Unsupported(ProtocolChat, "$.stream_options."+name, "stream option has no Responses equivalent")
		}
		if !rawJSONValuePresent(value) {
			return false, false, invalid(ProtocolChat, "$.stream_options.include_usage", "must be a boolean")
		}
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return false, false, invalid(ProtocolChat, "$.stream_options.include_usage", "must be a boolean")
		}
		includeUsage, includeUsageSet = enabled, true
	}
	return includeUsage, includeUsageSet, nil
}

func PatchResponseModel(protocol Protocol, body []byte, model string) ([]byte, error) {
	object, err := rawObject(protocol, body)
	if err != nil {
		return nil, err
	}
	field := "model"
	if protocol == ProtocolGenerateContent {
		field = "modelVersion"
	}
	object[field], _ = json.Marshal(model)
	return marshal(protocol, object)
}

func PatchStreamResponseModel(protocol Protocol, body []byte, model string) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil {
		return nil, invalid(protocol, "$", "invalid stream frame: %v", err)
	}
	if object == nil {
		return nil, invalid(protocol, "$", "stream frame must be an object")
	}
	encodedModel, _ := json.Marshal(model)
	switch protocol {
	case ProtocolChat:
		if _, exists := object["model"]; exists {
			object["model"] = encodedModel
		}
	case ProtocolResponses:
		if raw := object["response"]; len(raw) > 0 {
			var response map[string]json.RawMessage
			if err := json.Unmarshal(raw, &response); err != nil || response == nil {
				return nil, invalid(protocol, "$.response", "must be an object")
			}
			response["model"] = encodedModel
			object["response"], _ = json.Marshal(response)
		}
	case ProtocolMessages:
		if raw := object["message"]; len(raw) > 0 {
			var message map[string]json.RawMessage
			if err := json.Unmarshal(raw, &message); err != nil || message == nil {
				return nil, invalid(protocol, "$.message", "must be an object")
			}
			message["model"] = encodedModel
			object["message"], _ = json.Marshal(message)
		}
	case ProtocolGenerateContent:
		object["modelVersion"] = encodedModel
	}
	return json.Marshal(object)
}
