package chatresponses

import "encoding/json"

func rejectResponsesState(source responsesRequest) error {
	for path, set := range map[string]bool{
		"$.conversation":         len(source.Conversation) > 0 && string(source.Conversation) != "null",
		"$.previous_response_id": source.PreviousResponse != "",
		"$.prompt":               len(source.Prompt) > 0 && string(source.Prompt) != "null",
		"$.context_management":   len(source.ContextManagement) > 0 && string(source.ContextManagement) != "null",
		"$.background":           source.Background,
	} {
		if set {
			return unsupported(ProtocolResponses, path, "stateful Responses fields require a native Responses provider")
		}
	}
	return nil
}

func responseInputItems(raw json.RawMessage) ([]responsesItem, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, invalid(ProtocolResponses, "$.input", "input is required")
	}
	if raw[0] == '"' {
		return []responsesItem{{Type: "message", Role: "user", Content: mustJSON([]responsesContentPart{{Type: "input_text", Text: rawString(raw)}})}}, nil
	}
	var items []responsesItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, invalid(ProtocolResponses, "$.input", "input must be a string or item array: %v", err)
	}
	if err := validateResponsesItems(items, "$.input"); err != nil {
		return nil, err
	}
	return items, nil
}
