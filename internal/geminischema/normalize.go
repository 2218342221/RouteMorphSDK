// Package geminischema converts ordinary JSON Schema documents into the
// OpenAPI Schema subset accepted by Gemini function declarations.
package geminischema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultMaxDepth = 32
	DefaultMaxNodes = 4096
	DefaultMaxBytes = 1 << 20
)

var (
	ErrInvalidSchema      = errors.New("invalid JSON schema")
	ErrUnsupportedKeyword = errors.New("JSON schema keyword is not supported by Gemini")
	ErrLimitExceeded      = errors.New("JSON schema resource limit exceeded")
)

// Violation identifies the schema location and category of a normalization
// failure so protocol routes can preserve a precise public JSON path.
type Violation struct {
	Kind   error
	Path   string
	Reason string
}

func (e *Violation) Error() string {
	return fmt.Sprintf("%v: %s: %s", e.Kind, e.Path, e.Reason)
}

func (e *Violation) Unwrap() error { return e.Kind }

// Limits bounds work before normalization starts. A zero field selects the
// package default; negative values are invalid.
type Limits struct {
	MaxDepth int
	MaxNodes int
	MaxBytes int
}

// Normalize validates raw as one JSON value and returns a deterministic Gemini
// Schema document. Unsupported validation keywords are rejected rather than
// silently discarded.
func Normalize(raw json.RawMessage, limits Limits) (json.RawMessage, error) {
	limits, err := resolvedLimits(limits)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, invalid("$", "schema is empty")
	}
	if len(raw) > limits.MaxBytes {
		return nil, violation(ErrLimitExceeded, "$", "input is %d bytes; maximum is %d", len(raw), limits.MaxBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, invalid("$", "decode: %v", err)
	}
	if err := requireEOF(decoder); err != nil {
		return nil, err
	}
	meter := budget{limits: limits}
	if err := meter.measure(document, 1, "$"); err != nil {
		return nil, err
	}

	root, ok := document.(map[string]any)
	if !ok {
		return nil, invalid("$", "schema must be an object")
	}
	normalized, err := normalizeSchema(root, "$")
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(normalized)
	if err != nil {
		return nil, invalid("$", "encode: %v", err)
	}
	return result, nil
}

// NormalizeParameters prepares a function-declaration parameters schema.
// Gemini expects parameterless functions to omit parameters entirely, so an
// empty or type-only object is returned as nil after full validation.
func NormalizeParameters(raw json.RawMessage, limits Limits) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	normalized, err := Normalize(raw, limits)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(normalized, &object); err != nil {
		return nil, invalid("$", "normalized schema is not an object: %v", err)
	}
	properties, hasProperties := object["properties"]
	propertyCount := 0
	if hasProperties {
		var values map[string]json.RawMessage
		if err := json.Unmarshal(properties, &values); err != nil {
			return nil, invalid("$.properties", "normalized properties are not an object")
		}
		propertyCount = len(values)
	}
	_, hasType := object["type"]
	if len(object) == 0 || ((!hasProperties || propertyCount == 0) && len(object) <= 2 && (hasType || hasProperties)) {
		return nil, nil
	}
	return normalized, nil
}

func resolvedLimits(limits Limits) (Limits, error) {
	if limits.MaxDepth < 0 || limits.MaxNodes < 0 || limits.MaxBytes < 0 {
		return Limits{}, invalid("$", "limits cannot be negative")
	}
	if limits.MaxDepth == 0 {
		limits.MaxDepth = DefaultMaxDepth
	}
	if limits.MaxNodes == 0 {
		limits.MaxNodes = DefaultMaxNodes
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = DefaultMaxBytes
	}
	return limits, nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return invalid("$", "multiple JSON values are not allowed")
		}
		return invalid("$", "trailing data: %v", err)
	}
	return nil
}

type budget struct {
	limits Limits
	nodes  int
}

func (b *budget) measure(value any, depth int, path string) error {
	if depth > b.limits.MaxDepth {
		return violation(ErrLimitExceeded, path, "exceeds maximum depth %d", b.limits.MaxDepth)
	}
	b.nodes++
	if b.nodes > b.limits.MaxNodes {
		return violation(ErrLimitExceeded, path, "schema exceeds maximum node count %d", b.limits.MaxNodes)
	}
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if err := b.measure(child, depth+1, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range value {
			if err := b.measure(child, depth+1, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

var allowedKeywords = map[string]struct{}{
	"type": {}, "format": {}, "title": {}, "description": {}, "nullable": {},
	"enum": {}, "minimum": {}, "maximum": {}, "minLength": {}, "maxLength": {},
	"pattern": {}, "minItems": {}, "maxItems": {}, "minProperties": {},
	"maxProperties": {}, "properties": {}, "propertyOrdering": {}, "required": {},
	"items": {}, "anyOf": {}, "additionalProperties": {}, "default": {}, "example": {},
}

func normalizeSchema(source map[string]any, path string) (map[string]any, error) {
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, ok := allowedKeywords[key]; !ok {
			return nil, violation(ErrUnsupportedKeyword, path+"."+key, "keyword is not supported by Gemini")
		}
	}

	target := make(map[string]any, len(source))
	nullable, err := optionalBool(source, "nullable", path)
	if err != nil {
		return nil, err
	}
	var nonNullTypes []string
	var typeAllowsNull bool
	if value, ok := source["type"]; ok {
		nonNullTypes, typeAllowsNull, err = schemaTypes(value, path+".type")
		if err != nil {
			return nil, err
		}
	}
	if typeAllowsNull {
		nullable = true
	}

	for _, key := range []string{"format", "title", "description", "pattern"} {
		if value, ok := source[key]; ok {
			text, ok := value.(string)
			if !ok {
				return nil, invalid(path+"."+key, "must be a string")
			}
			target[key] = text
		}
	}
	for _, key := range []string{"minimum", "maximum", "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties"} {
		if value, ok := source[key]; ok {
			number, ok := value.(json.Number)
			if !ok {
				return nil, invalid(path+"."+key, "must be a number")
			}
			switch key {
			case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
				integer, err := strconv.ParseInt(number.String(), 10, 64)
				if err != nil || integer < 0 {
					return nil, invalid(path+"."+key, "must be a non-negative integer")
				}
			case "minimum", "maximum":
				if _, err := strconv.ParseFloat(number.String(), 64); err != nil {
					return nil, invalid(path+"."+key, "must be a finite JSON number")
				}
			}
			target[key] = number
		}
	}
	for _, key := range []string{"default", "example"} {
		if value, ok := source[key]; ok {
			target[key] = value
		}
	}

	if value, ok := source["enum"]; ok {
		enumeration, allowsNull, err := normalizeEnum(value, path+".enum")
		if err != nil {
			return nil, err
		}
		nullable = nullable || allowsNull
		if len(enumeration) == 0 {
			return nil, violation(ErrUnsupportedKeyword, path+".enum", "contains only null")
		}
		target["enum"] = enumeration
	}

	var properties map[string]any
	_, hasProperties := source["properties"]
	if hasProperties {
		properties, err = normalizeProperties(source["properties"], path+".properties")
		if err != nil {
			return nil, err
		}
		target["properties"] = properties
	}
	if value, ok := source["required"]; ok {
		required, err := stringList(value, path+".required", true)
		if err != nil {
			return nil, err
		}
		for _, name := range required {
			if _, exists := properties[name]; !hasProperties || !exists {
				return nil, invalid(path+".required", "property %q is not declared", name)
			}
		}
		target["required"] = required
	}
	if value, ok := source["propertyOrdering"]; ok {
		ordering, err := stringList(value, path+".propertyOrdering", true)
		if err != nil {
			return nil, err
		}
		for _, name := range ordering {
			if _, exists := properties[name]; !hasProperties || !exists {
				return nil, invalid(path+".propertyOrdering", "property %q is not declared", name)
			}
		}
		target["propertyOrdering"] = ordering
	}

	if value, ok := source["items"]; ok {
		item, ok := value.(map[string]any)
		if !ok {
			return nil, violation(ErrUnsupportedKeyword, path+".items", "tuple schemas and booleans are not supported")
		}
		normalized, err := normalizeSchema(item, path+".items")
		if err != nil {
			return nil, err
		}
		target["items"] = normalized
	}

	var anyOf []any
	var anyOfAllowsNull bool
	if value, ok := source["anyOf"]; ok {
		anyOf, anyOfAllowsNull, err = normalizeAnyOf(value, path+".anyOf")
		if err != nil {
			return nil, err
		}
	}
	nullable = nullable || anyOfAllowsNull
	if len(anyOf) > 0 {
		target["anyOf"] = anyOf
	}

	if value, ok := source["additionalProperties"]; ok {
		allowed, ok := value.(bool)
		if !ok || allowed {
			return nil, violation(ErrUnsupportedKeyword, path+".additionalProperties", "only the boolean false is supported")
		}
		// Gemini's OpenAPI Schema has no additionalProperties field. The common
		// closed-object marker is safe to omit while retaining declared properties.
	}

	if len(nonNullTypes) > 1 && len(anyOf) > 0 {
		return nil, violation(ErrUnsupportedKeyword, path, "a multi-valued type cannot be combined with anyOf")
	}
	if len(nonNullTypes) == 1 {
		target["type"] = strings.ToUpper(nonNullTypes[0])
	} else if len(nonNullTypes) > 1 {
		branches := make([]any, 0, len(nonNullTypes))
		for _, typeName := range nonNullTypes {
			branches = append(branches, map[string]any{"type": strings.ToUpper(typeName)})
		}
		target["anyOf"] = branches
	} else if _, present := source["type"]; present && typeAllowsNull {
		target["type"] = "NULL"
		nullable = false
	} else if hasProperties {
		target["type"] = "OBJECT"
	} else if _, ok := source["items"]; ok {
		target["type"] = "ARRAY"
	}
	if nullable {
		target["nullable"] = true
	}
	return target, nil
}

func schemaTypes(value any, path string) ([]string, bool, error) {
	if value == nil {
		return nil, false, invalid(path, "must be a string or array of strings")
	}
	var values []any
	switch value := value.(type) {
	case string:
		values = []any{value}
	case []any:
		if len(value) == 0 {
			return nil, false, invalid(path, "must not be empty")
		}
		values = value
	default:
		return nil, false, invalid(path, "must be a string or array of strings")
	}
	seen := make(map[string]bool)
	types := make([]string, 0, len(values))
	nullable := false
	for index, raw := range values {
		name, ok := raw.(string)
		if !ok {
			return nil, false, invalid(fmt.Sprintf("%s[%d]", path, index), "must be a string")
		}
		name = strings.ToLower(name)
		if name == "null" {
			nullable = true
			continue
		}
		switch name {
		case "object", "array", "string", "number", "integer", "boolean":
		default:
			return nil, false, invalid(path, "unsupported type %q", name)
		}
		if !seen[name] {
			seen[name] = true
			types = append(types, name)
		}
	}
	return types, nullable, nil
}

func normalizeProperties(value any, path string) (map[string]any, error) {
	source, ok := value.(map[string]any)
	if !ok {
		return nil, invalid(path, "must be an object")
	}
	target := make(map[string]any, len(source))
	for name, raw := range source {
		property, ok := raw.(map[string]any)
		if !ok {
			return nil, invalid(path+"."+name, "must be a schema object")
		}
		normalized, err := normalizeSchema(property, path+"."+name)
		if err != nil {
			return nil, err
		}
		target[name] = normalized
	}
	return target, nil
}

func normalizeAnyOf(value any, path string) ([]any, bool, error) {
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return nil, false, invalid(path, "must be a non-empty array")
	}
	result := make([]any, 0, len(values))
	nullable := false
	for index, raw := range values {
		branch, ok := raw.(map[string]any)
		if !ok {
			return nil, false, invalid(fmt.Sprintf("%s[%d]", path, index), "must be a schema object")
		}
		if onlyNullSchema(branch) {
			nullable = true
			continue
		}
		normalized, err := normalizeSchema(branch, fmt.Sprintf("%s[%d]", path, index))
		if err != nil {
			return nil, false, err
		}
		result = append(result, normalized)
	}
	if len(result) == 0 {
		return nil, false, violation(ErrUnsupportedKeyword, path, "contains only null")
	}
	return result, nullable, nil
}

func onlyNullSchema(schema map[string]any) bool {
	if len(schema) != 1 {
		return false
	}
	typeName, ok := schema["type"].(string)
	return ok && strings.EqualFold(typeName, "null")
}

func normalizeEnum(value any, path string) ([]string, bool, error) {
	values, ok := value.([]any)
	if !ok || len(values) == 0 {
		return nil, false, invalid(path, "must be a non-empty array")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	nullable := false
	for index, value := range values {
		var text string
		switch value := value.(type) {
		case nil:
			nullable = true
			continue
		case string:
			text = value
		case json.Number:
			text = value.String()
		case bool:
			text = strconv.FormatBool(value)
		default:
			return nil, false, invalid(fmt.Sprintf("%s[%d]", path, index), "must be a primitive value")
		}
		if !seen[text] {
			seen[text] = true
			result = append(result, text)
		}
	}
	return result, nullable, nil
}

func stringList(value any, path string, unique bool) ([]string, error) {
	values, ok := value.([]any)
	if !ok {
		return nil, invalid(path, "must be an array of strings")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for index, value := range values {
		text, ok := value.(string)
		if !ok || text == "" {
			return nil, invalid(fmt.Sprintf("%s[%d]", path, index), "must be a non-empty string")
		}
		if unique && seen[text] {
			return nil, invalid(path, "contains duplicate %q", text)
		}
		seen[text] = true
		result = append(result, text)
	}
	return result, nil
}

func optionalBool(source map[string]any, key, path string) (bool, error) {
	value, ok := source[key]
	if !ok {
		return false, nil
	}
	result, ok := value.(bool)
	if !ok {
		return false, invalid(path+"."+key, "must be a boolean")
	}
	return result, nil
}

func invalid(path, format string, args ...any) error {
	return violation(ErrInvalidSchema, path, format, args...)
}

func violation(kind error, path, format string, args ...any) error {
	return &Violation{Kind: kind, Path: path, Reason: fmt.Sprintf(format, args...)}
}
