// Package jsonx contains protocol-neutral strict JSON primitives.
package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var ErrObjectRequired = errors.New("JSON object is required")

// DecodeOne decodes exactly one JSON value and preserves JSON numbers.
func DecodeOne(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func Object(data []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := DecodeOne(data, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, ErrObjectRequired
	}
	return object, nil
}

// NormalizeObject accepts an object or a JSON string containing an object.
// Empty and null values become an empty object.
func NormalizeObject(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return json.RawMessage(`{}`), nil
	}
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return nil, fmt.Errorf("invalid JSON string: %w", err)
		}
		raw = bytes.TrimSpace([]byte(encoded))
		if len(raw) == 0 {
			return json.RawMessage(`{}`), nil
		}
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if object == nil {
		return json.RawMessage(`{}`), nil
	}
	return append(json.RawMessage(nil), raw...), nil
}

func Present(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) && !bytes.Equal(trimmed, []byte("{}")) && !bytes.Equal(trimmed, []byte("[]")) && !bytes.Equal(trimmed, []byte(`""`))
}

func RawString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return value
	}
	return string(raw)
}

func String(value string) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
