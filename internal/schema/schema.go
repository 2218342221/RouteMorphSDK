// Package schema contains protocol-neutral JSON Schema normalization.
package schema

import (
	"encoding/json"
	"errors"
)

var ErrObjectRequired = errors.New("schema must be a JSON object")

// NormalizeFunctionParameters fills the object keywords required by provider
// function declarations while preserving extension fields.
func NormalizeFunctionParameters(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{"type":"object","properties":{}}`), nil
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, ErrObjectRequired
	}
	if _, exists := value["type"]; !exists {
		value["type"] = json.RawMessage(`"object"`)
	}
	if _, exists := value["properties"]; !exists {
		value["properties"] = json.RawMessage(`{}`)
	}
	return json.Marshal(value)
}
