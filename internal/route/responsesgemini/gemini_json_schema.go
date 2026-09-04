package responsesgemini

import (
	"encoding/json"
	"fmt"
	"strings"
)

func messagesGeminiSchemaToJSONSchema(raw json.RawMessage, path string) (json.RawMessage, error) {
	if !jsonValuePresent(raw) {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, invalid(ProtocolGenerateContent, path, "schema must be valid JSON: %v", err)
	}
	converted, err := convertMessagesGeminiSchemaNode(value, path, 0)
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func convertMessagesGeminiSchemaNode(value any, path string, depth int) (any, error) {
	if depth >= 64 {
		return nil, unsupported(ProtocolGenerateContent, path, "JSON schema exceeds the supported nesting depth")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, invalid(ProtocolGenerateContent, path, "schema must be an object")
	}
	converted := make(map[string]any, len(object))
	for key, child := range object {
		converted[key] = child
	}
	if rawType, exists := converted["type"]; exists {
		switch typed := rawType.(type) {
		case string:
			converted["type"] = strings.ToLower(typed)
		case []any:
			values := make([]any, len(typed))
			for index, item := range typed {
				text, ok := item.(string)
				if !ok {
					return nil, invalid(ProtocolGenerateContent, path+".type", "type array must contain strings")
				}
				values[index] = strings.ToLower(text)
			}
			converted["type"] = values
		default:
			return nil, invalid(ProtocolGenerateContent, path+".type", "type must be a string or string array")
		}
	}
	if properties, exists := converted["properties"]; exists {
		propertyMap, ok := properties.(map[string]any)
		if !ok {
			return nil, invalid(ProtocolGenerateContent, path+".properties", "must be an object")
		}
		cleaned := make(map[string]any, len(propertyMap))
		for name, property := range propertyMap {
			child, err := convertMessagesGeminiSchemaNode(property, path+".properties."+name, depth+1)
			if err != nil {
				return nil, err
			}
			cleaned[name] = child
		}
		converted["properties"] = cleaned
	}
	if items, exists := converted["items"]; exists {
		child, err := convertMessagesGeminiSchemaNode(items, path+".items", depth+1)
		if err != nil {
			return nil, err
		}
		converted["items"] = child
	}
	if anyOf, exists := converted["anyOf"]; exists {
		values, ok := anyOf.([]any)
		if !ok {
			return nil, invalid(ProtocolGenerateContent, path+".anyOf", "must be an array")
		}
		cleaned := make([]any, 0, len(values)+1)
		for index, item := range values {
			child, err := convertMessagesGeminiSchemaNode(item, fmt.Sprintf("%s.anyOf[%d]", path, index), depth+1)
			if err != nil {
				return nil, err
			}
			cleaned = append(cleaned, child)
		}
		converted["anyOf"] = cleaned
	}
	if nullable, exists := converted["nullable"]; exists {
		enabled, ok := nullable.(bool)
		if !ok {
			return nil, invalid(ProtocolGenerateContent, path+".nullable", "must be a boolean")
		}
		delete(converted, "nullable")
		if enabled {
			switch typed := converted["type"].(type) {
			case string:
				converted["type"] = []any{typed, "null"}
			case []any:
				seen := false
				for _, item := range typed {
					seen = seen || item == "null"
				}
				if !seen {
					converted["type"] = append(typed, "null")
				}
			default:
				values, _ := converted["anyOf"].([]any)
				converted["anyOf"] = append(values, map[string]any{"type": "null"})
			}
		}
	}
	return converted, nil
}
