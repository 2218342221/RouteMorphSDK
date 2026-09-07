// Package routekit contains protocol-neutral mechanics shared by direct route
// packages. Semantic compatibility decisions remain owned by each protocol
// pair so sharing these helpers cannot introduce an implicit conversion path.
package routekit

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"strings"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	jsonx "github.com/2218342221/RouteMorphSDK/internal/jsonx"
	schemax "github.com/2218342221/RouteMorphSDK/internal/schema"
)

func DecodeJSON(protocol core.Protocol, data []byte, destination any) error {
	if err := jsonx.DecodeOne(data, destination); err != nil {
		return core.Invalid(protocol, "$", "%v", err)
	}
	return nil
}

func Marshal(protocol core.Protocol, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, core.Invalid(protocol, "$", "cannot encode JSON: %v", err)
	}
	return data, nil
}

// MustJSON is reserved for internal values whose complete type graph is known
// to be JSON encodable. Panicking makes a broken internal invariant visible
// instead of silently emitting a nil or malformed protocol payload.
func MustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("routekit: internal value is not JSON encodable: %v", err))
	}
	return data
}

func MustJSONString(value string) string { return string(MustJSON(value)) }

func NormalizeArguments(protocol core.Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := jsonx.NormalizeObject(raw)
	if err != nil {
		return nil, core.Invalid(protocol, path, "tool arguments must be a JSON object: %v", err)
	}
	return normalized, nil
}

func NormalizeOpenAIToolArguments(protocol core.Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > 0 && raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, core.Invalid(protocol, path, "invalid argument string: %v", err)
		}
		if value == "" {
			return json.RawMessage(`{}`), nil
		}
	}
	return NormalizeArguments(protocol, path, raw)
}

func NormalizeFunctionParameters(protocol core.Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := schemax.NormalizeFunctionParameters(raw)
	if err != nil {
		return nil, core.Invalid(protocol, path, "function parameters must be a JSON object")
	}
	return normalized, nil
}

func RawString(raw json.RawMessage) string { return jsonx.RawString(raw) }

func RawObject(protocol core.Protocol, data []byte) (map[string]json.RawMessage, error) {
	object, err := jsonx.Object(data)
	if err != nil {
		return nil, core.Invalid(protocol, "$", "%v", err)
	}
	return object, nil
}

func ValuePresent(raw json.RawMessage) bool { return jsonx.Present(raw) }

func NonNullValue(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null"
}

func AppendDiagnostic(diagnostics []core.Diagnostic, severity, code, path, message string) []core.Diagnostic {
	return append(diagnostics, core.Diagnostic{Severity: severity, Code: code, Path: path, Message: message})
}

func RejectUnknownTopLevel(protocol core.Protocol, data []byte, allowed ...string) error {
	object, err := RawObject(protocol, data)
	if err != nil {
		return err
	}
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range object {
		if _, ok := known[field]; !ok {
			return core.Unsupported(protocol, "$."+field, "field is not supported by this cross-protocol route")
		}
	}
	return nil
}

// RejectUnknownObjectFields validates a nested JSON object before a wire DTO
// can discard extension fields during unmarshalling.
func RejectUnknownObjectFields(protocol core.Protocol, raw json.RawMessage, path string, allowed ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, core.Invalid(protocol, path, "must be an object")
	}
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range object {
		if _, ok := known[field]; !ok {
			return nil, core.Unsupported(protocol, path+"."+field, "field is not supported by this cross-protocol route")
		}
	}
	return object, nil
}

// ValidateMessagesOutputConfigFields prevents unrecognized Anthropic output
// configuration fields from disappearing when a cross-protocol route decodes
// the request into its intentionally narrow wire DTO.
func ValidateMessagesOutputConfigFields(protocol core.Protocol, data []byte) error {
	root, err := RawObject(protocol, data)
	if err != nil {
		return err
	}
	raw, present := root["output_config"]
	if !present {
		return nil
	}
	fields, err := RejectUnknownObjectFields(protocol, raw, "$.output_config", "effort", "format")
	if err != nil {
		return err
	}
	format, present := fields["format"]
	if !present {
		return nil
	}
	_, err = RejectUnknownObjectFields(protocol, format, "$.output_config.format", "type", "schema")
	return err
}

// ValidateMessagesThinkingFields rejects unreviewed fields before the
// Messages thinking union is decoded into the shared narrow DTO.
func ValidateMessagesThinkingFields(protocol core.Protocol, data []byte) error {
	root, err := RawObject(protocol, data)
	if err != nil {
		return err
	}
	raw, present := root["thinking"]
	if !present {
		return nil
	}
	_, err = RejectUnknownObjectFields(protocol, raw, "$.thinking", "type", "budget_tokens", "display")
	return err
}

// ValidateChatResponseFormatFields protects the nested Chat structured-output
// shape from silent field loss on routes that use a compact wire DTO.
func ValidateChatResponseFormatFields(protocol core.Protocol, data []byte) error {
	root, err := RawObject(protocol, data)
	if err != nil {
		return err
	}
	raw := root["response_format"]
	if !ValuePresent(raw) {
		return nil
	}
	fields, err := RejectUnknownObjectFields(protocol, raw, "$.response_format", "type", "json_schema")
	if err != nil {
		return err
	}
	jsonSchema, present := fields["json_schema"]
	if !present {
		return nil
	}
	_, err = RejectUnknownObjectFields(protocol, jsonSchema, "$.response_format.json_schema", "name", "description", "schema", "strict")
	return err
}

// ValidateResponsesTextConfigFields protects the nested Responses text-format
// union from silent field loss on cross-protocol routes.
func ValidateResponsesTextConfigFields(protocol core.Protocol, data []byte) error {
	root, err := RawObject(protocol, data)
	if err != nil {
		return err
	}
	raw := root["text"]
	if !ValuePresent(raw) {
		return nil
	}
	fields, err := RejectUnknownObjectFields(protocol, raw, "$.text", "format", "verbosity")
	if err != nil {
		return err
	}
	format, present := fields["format"]
	if !present {
		return nil
	}
	_, err = RejectUnknownObjectFields(protocol, format, "$.text.format", "type", "name", "description", "schema", "strict")
	return err
}

// ValidateChatMessageContentFields validates known Chat content-part union
// members before a broad wire DTO can discard fields from another variant.
func ValidateChatMessageContentFields(protocol core.Protocol, data []byte) error {
	root, err := RawObject(protocol, data)
	if err != nil {
		return err
	}
	rawMessages, present := root["messages"]
	if !present {
		return nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil || messages == nil {
		return core.Invalid(protocol, "$.messages", "must be an array")
	}
	for messageIndex, rawMessage := range messages {
		messagePath := fmt.Sprintf("$.messages[%d]", messageIndex)
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil || message == nil {
			return core.Invalid(protocol, messagePath, "must be an object")
		}
		rawContent, ok := message["content"]
		if !ok {
			continue
		}
		trimmed := bytes.TrimSpace(rawContent)
		if len(trimmed) == 0 || trimmed[0] != '[' {
			continue
		}
		var parts []json.RawMessage
		if err := json.Unmarshal(trimmed, &parts); err != nil || parts == nil {
			return core.Invalid(protocol, messagePath+".content", "must be a string, null, or content-part array")
		}
		for partIndex, rawPart := range parts {
			partPath := fmt.Sprintf("%s.content[%d]", messagePath, partIndex)
			if err := validateChatContentPartFields(protocol, rawPart, partPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateChatContentPartFields(protocol core.Protocol, raw json.RawMessage, path string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return core.Invalid(protocol, path, "content part must be an object")
	}
	partType, err := requiredUnionString(protocol, fields, "type", path)
	if err != nil {
		return err
	}
	known := stringSet("type", "text", "image_url", "input_audio", "file", "prompt_cache_breakpoint")
	var allowed map[string]struct{}
	switch partType {
	case "text":
		allowed = stringSet("type", "text", "prompt_cache_breakpoint")
	case "image_url":
		allowed = stringSet("type", "image_url", "prompt_cache_breakpoint")
	case "input_audio":
		allowed = stringSet("type", "input_audio", "prompt_cache_breakpoint")
	case "file":
		allowed = stringSet("type", "file", "prompt_cache_breakpoint")
	default:
		return nil
	}
	if err := rejectUnionFields(protocol, fields, path, partType, allowed, known); err != nil {
		return err
	}
	switch partType {
	case "image_url":
		_, err = RejectUnknownObjectFields(protocol, fields["image_url"], path+".image_url", "url", "detail")
	case "input_audio":
		_, err = RejectUnknownObjectFields(protocol, fields["input_audio"], path+".input_audio", "data", "format")
	case "file":
		_, err = RejectUnknownObjectFields(protocol, fields["file"], path+".file", "file_id", "file_data", "filename")
	}
	return err
}

// ValidateMessagesContentBlockFields validates known Messages content-block
// and media-source unions, including nested tool-result content.
func ValidateMessagesContentBlockFields(protocol core.Protocol, data []byte) error {
	root, err := RawObject(protocol, data)
	if err != nil {
		return err
	}
	if rawSystem, present := root["system"]; present {
		if err := validateMessagesContentBlocks(protocol, rawSystem, "$.system"); err != nil {
			return err
		}
	}
	rawMessages, present := root["messages"]
	if !present {
		return nil
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil || messages == nil {
		return core.Invalid(protocol, "$.messages", "must be an array")
	}
	for index, rawMessage := range messages {
		path := fmt.Sprintf("$.messages[%d]", index)
		var message map[string]json.RawMessage
		if err := json.Unmarshal(rawMessage, &message); err != nil || message == nil {
			return core.Invalid(protocol, path, "must be an object")
		}
		if rawContent, present := message["content"]; present {
			if err := validateMessagesContentBlocks(protocol, rawContent, path+".content"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMessagesContentBlocks(protocol core.Protocol, raw json.RawMessage, path string) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(trimmed, &blocks); err != nil || blocks == nil {
		return core.Invalid(protocol, path, "must be a string or content-block array")
	}
	// Payload members from another tagged variant make the union malformed.
	// Metadata/extensions that are merely unavailable on this route remain an
	// unsupported semantic instead of being reclassified as malformed.
	known := stringSet("type", "text", "thinking", "signature", "data", "id", "name", "input", "tool_use_id", "content", "is_error", "source")
	for index, rawBlock := range blocks {
		blockPath := fmt.Sprintf("%s[%d]", path, index)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(rawBlock, &fields); err != nil || fields == nil {
			return core.Invalid(protocol, blockPath, "content block must be an object")
		}
		blockType, err := requiredUnionString(protocol, fields, "type", blockPath)
		if err != nil {
			return err
		}
		var allowed map[string]struct{}
		switch blockType {
		case "text":
			allowed = stringSet("type", "text", "cache_control", "citations")
		case "image":
			allowed = stringSet("type", "source", "cache_control", "transformations")
		case "document":
			allowed = stringSet("type", "source", "cache_control", "citations", "title", "context", "transformations")
		case "tool_use":
			allowed = stringSet("type", "id", "name", "input", "cache_control", "caller", "toolset_name")
		case "tool_result":
			allowed = stringSet("type", "tool_use_id", "content", "is_error", "cache_control")
		case "thinking":
			allowed = stringSet("type", "thinking", "signature")
		case "redacted_thinking":
			allowed = stringSet("type", "data")
		default:
			continue
		}
		if err := rejectUnionFields(protocol, fields, blockPath, blockType, allowed, known); err != nil {
			return err
		}
		if (blockType == "image" || blockType == "document") && fields["source"] != nil {
			if err := validateMessagesMediaSourceFields(protocol, fields["source"], blockPath+".source"); err != nil {
				return err
			}
		}
		if blockType == "tool_result" {
			if content, present := fields["content"]; present {
				if err := validateMessagesContentBlocks(protocol, content, blockPath+".content"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateMessagesMediaSourceFields(protocol core.Protocol, raw json.RawMessage, path string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return core.Invalid(protocol, path, "source must be an object")
	}
	sourceType, err := requiredUnionString(protocol, fields, "type", path)
	if err != nil {
		return err
	}
	known := stringSet("type", "media_type", "data", "url", "file_id", "content")
	var allowed map[string]struct{}
	switch sourceType {
	case "base64", "text":
		allowed = stringSet("type", "media_type", "data")
	case "url":
		allowed = stringSet("type", "url")
	case "file":
		allowed = stringSet("type", "file_id")
	case "content":
		allowed = stringSet("type", "content")
	default:
		return nil
	}
	return rejectUnionFields(protocol, fields, path, sourceType, allowed, known)
}

func requiredUnionString(protocol core.Protocol, fields map[string]json.RawMessage, field, path string) (string, error) {
	raw, present := fields[field]
	if !present {
		return "", core.Invalid(protocol, path+"."+field, "%s is required", field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", core.Invalid(protocol, path+"."+field, "%s must be a non-empty string", field)
	}
	return value, nil
}

func rejectUnionFields(protocol core.Protocol, fields map[string]json.RawMessage, path, discriminator string, allowed, known map[string]struct{}) error {
	for field := range fields {
		if _, ok := allowed[field]; ok {
			continue
		}
		if _, isKnownVariantField := known[field]; isKnownVariantField {
			return core.Invalid(protocol, path+"."+field, "%s is not valid for %q", field, discriminator)
		}
		return core.Unsupported(protocol, path+"."+field, "field is not supported by this cross-protocol route")
	}
	return nil
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// ValidateResponsesContentArray checks the discriminated Responses content
// union, including fields that an ordinary json.Unmarshal into a broad DTO
// would otherwise silently discard.
func ValidateResponsesContentArray(protocol core.Protocol, raw json.RawMessage, path string, allowedTypes ...string) error {
	return validateResponsesContentArray(protocol, raw, path, false, allowedTypes...)
}

func validateResponsesContentArray(protocol core.Protocol, raw json.RawMessage, path string, upstream bool, allowedTypes ...string) error {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return responsesValidationError(protocol, path, upstream, "must be an array")
	}
	allowed := make(map[string]struct{}, len(allowedTypes))
	for _, partType := range allowedTypes {
		allowed[partType] = struct{}{}
	}
	for index, value := range values {
		partPath := fmt.Sprintf("%s[%d]", path, index)
		var rawFields map[string]json.RawMessage
		if err := json.Unmarshal(value, &rawFields); err != nil || rawFields == nil {
			return responsesValidationError(protocol, partPath, upstream, "content part must be an object")
		}
		partType, err := responsesRequiredString(protocol, rawFields, "type", partPath, upstream, true)
		if err != nil {
			return err
		}
		if len(allowed) > 0 {
			if _, ok := allowed[partType]; !ok {
				return responsesUnsupportedError(protocol, partPath+".type", upstream, "content part %q is not supported in this context", partType)
			}
		}
		var fields []string
		switch partType {
		case "input_text":
			fields = []string{"type", "text", "prompt_cache_breakpoint"}
		case "input_image":
			fields = []string{"type", "image_url", "file_id", "detail", "prompt_cache_breakpoint"}
		case "input_file":
			fields = []string{"type", "file_id", "file_url", "file_data", "filename", "detail", "prompt_cache_breakpoint"}
		case "output_text":
			fields = []string{"type", "text", "annotations", "logprobs"}
		case "refusal":
			fields = []string{"type", "refusal"}
		case "summary_text", "reasoning_text", "text":
			fields = []string{"type", "text"}
		case "input_audio":
			fields = []string{"type", "input_audio", "prompt_cache_breakpoint"}
		default:
			return responsesUnsupportedError(protocol, partPath+".type", upstream, "content part %q is not portable", partType)
		}
		partFields, err := responsesObjectFields(protocol, value, partPath, upstream, fields...)
		if err != nil {
			return err
		}
		switch partType {
		case "input_text":
			if _, err := responsesRequiredString(protocol, partFields, "text", partPath, upstream, false); err != nil {
				return err
			}
			if err := validateResponsesPromptCacheBreakpoint(protocol, partFields, partPath, upstream); err != nil {
				return err
			}
		case "input_image":
			if err := validateResponsesInputImage(protocol, partFields, partPath, upstream); err != nil {
				return err
			}
		case "input_file":
			if err := validateResponsesInputFile(protocol, partFields, partPath, upstream); err != nil {
				return err
			}
		case "output_text":
			if _, err := responsesRequiredString(protocol, partFields, "text", partPath, upstream, false); err != nil {
				return err
			}
			if upstream {
				if _, err := responsesRequiredArray(protocol, partFields, "annotations", partPath, true); err != nil {
					return err
				}
			} else if err := responsesOptionalArray(protocol, partFields, "annotations", partPath, false); err != nil {
				return err
			}
			if err := responsesOptionalArray(protocol, partFields, "logprobs", partPath, upstream); err != nil {
				return err
			}
		case "refusal":
			if _, err := responsesRequiredString(protocol, partFields, "refusal", partPath, upstream, false); err != nil {
				return err
			}
		case "summary_text", "reasoning_text", "text":
			if _, err := responsesRequiredString(protocol, partFields, "text", partPath, upstream, false); err != nil {
				return err
			}
		case "input_audio":
			if err := validateResponsesInputAudio(protocol, partFields, partPath, upstream); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateResponsesToolOutput validates the official Responses function/custom
// output union: either a JSON string or a list of input_text/input_image/input_file
// content parts.
func ValidateResponsesToolOutput(protocol core.Protocol, raw json.RawMessage, path string) error {
	trimmed := []byte(strings.TrimSpace(string(raw)))
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return core.Invalid(protocol, path, "tool output is required")
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return core.Invalid(protocol, path, "tool output string is invalid")
		}
		return nil
	}
	if trimmed[0] != '[' {
		return core.Invalid(protocol, path, "tool output must be a string or content array")
	}
	return validateResponsesContentArray(protocol, trimmed, path, false, "input_text", "input_image", "input_file")
}

func responsesValidationError(protocol core.Protocol, path string, upstream bool, format string, args ...any) error {
	if upstream {
		return core.UpstreamResponseError(protocol, path, format, args...)
	}
	return core.Invalid(protocol, path, format, args...)
}

func responsesUnsupportedError(protocol core.Protocol, path string, upstream bool, format string, args ...any) error {
	if upstream {
		return core.UpstreamResponseError(protocol, path, format, args...)
	}
	return core.Unsupported(protocol, path, format, args...)
}

func responsesObjectFields(protocol core.Protocol, raw json.RawMessage, path string, upstream bool, allowed ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, responsesValidationError(protocol, path, upstream, "must be an object")
	}
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range object {
		if _, ok := known[field]; !ok {
			return nil, responsesUnsupportedError(protocol, path+"."+field, upstream, "field is not valid for this object")
		}
	}
	return object, nil
}

func responsesRequiredRaw(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream bool) (json.RawMessage, error) {
	raw, ok := object[field]
	trimmed := bytes.TrimSpace(raw)
	if !ok || len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, responsesValidationError(protocol, path+"."+field, upstream, "%s is required and must not be null", field)
	}
	return raw, nil
}

func responsesRequiredString(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream, nonEmpty bool) (string, error) {
	raw, err := responsesRequiredRaw(protocol, object, field, path, upstream)
	if err != nil {
		return "", err
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", responsesValidationError(protocol, path+"."+field, upstream, "%s must be a string", field)
	}
	if nonEmpty && strings.TrimSpace(value) == "" {
		return "", responsesValidationError(protocol, path+"."+field, upstream, "%s must not be empty", field)
	}
	return value, nil
}

func responsesOptionalString(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream, nullable bool) (string, bool, error) {
	raw, ok := object[field]
	if !ok {
		return "", false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if nullable {
			return "", false, nil
		}
		return "", false, responsesValidationError(protocol, path+"."+field, upstream, "%s must not be null", field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, responsesValidationError(protocol, path+"."+field, upstream, "%s must be a string", field)
	}
	return value, true, nil
}

func responsesRequiredArray(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream bool) ([]json.RawMessage, error) {
	raw, err := responsesRequiredRaw(protocol, object, field, path, upstream)
	if err != nil {
		return nil, err
	}
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, responsesValidationError(protocol, path+"."+field, upstream, "%s must be an array", field)
	}
	return values, nil
}

func responsesOptionalArray(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream bool) error {
	if _, ok := object[field]; !ok {
		return nil
	}
	_, err := responsesRequiredArray(protocol, object, field, path, upstream)
	return err
}

func responsesOptionalBool(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream bool) error {
	raw, ok := object[field]
	if !ok {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must not be null", field)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must be a boolean", field)
	}
	return nil
}

func validateResponsesPromptCacheBreakpoint(protocol core.Protocol, object map[string]json.RawMessage, path string, upstream bool) error {
	raw, ok := object["prompt_cache_breakpoint"]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	fields, err := responsesObjectFields(protocol, raw, path+".prompt_cache_breakpoint", upstream, "mode")
	if err != nil {
		return err
	}
	mode, err := responsesRequiredString(protocol, fields, "mode", path+".prompt_cache_breakpoint", upstream, true)
	if err != nil {
		return err
	}
	if mode != "explicit" {
		return responsesUnsupportedError(protocol, path+".prompt_cache_breakpoint.mode", upstream, "unsupported prompt cache breakpoint mode %q", mode)
	}
	return nil
}

func validateResponsesInputImage(protocol core.Protocol, object map[string]json.RawMessage, path string, upstream bool) error {
	imageURL, hasImageURL, err := responsesOptionalString(protocol, object, "image_url", path, upstream, true)
	if err != nil {
		return err
	}
	fileID, hasFileID, err := responsesOptionalString(protocol, object, "file_id", path, upstream, true)
	if err != nil {
		return err
	}
	sources := 0
	if hasImageURL && strings.TrimSpace(imageURL) != "" {
		sources++
	}
	if hasFileID && strings.TrimSpace(fileID) != "" {
		sources++
	}
	if sources != 1 {
		return responsesValidationError(protocol, path, upstream, "input_image requires exactly one non-empty image_url or file_id")
	}
	if hasImageURL && strings.TrimSpace(imageURL) != "" {
		media, err := ParseDataURL(imageURL)
		if err != nil {
			return responsesValidationError(protocol, path+".image_url", upstream, "image_url must be an absolute HTTP(S) URL or valid base64 data URL: %v", err)
		}
		if media.Data != "" && !ValidOpenAIImageMIMEType(media.MIMEType) {
			return responsesUnsupportedError(protocol, path+".image_url", upstream, "unsupported inline image media type %q", media.MIMEType)
		}
	}
	if detail, present, err := responsesOptionalString(protocol, object, "detail", path, upstream, false); err != nil {
		return err
	} else if present && detail != "auto" && detail != "low" && detail != "high" && detail != "original" {
		return responsesUnsupportedError(protocol, path+".detail", upstream, "unsupported image detail %q", detail)
	}
	return validateResponsesPromptCacheBreakpoint(protocol, object, path, upstream)
}

func validateResponsesInputFile(protocol core.Protocol, object map[string]json.RawMessage, path string, upstream bool) error {
	filename, _, err := responsesOptionalString(protocol, object, "filename", path, upstream, true)
	if err != nil {
		return err
	}
	sources := 0
	var fileURL, fileData string
	for _, field := range []string{"file_id", "file_url", "file_data"} {
		value, present, err := responsesOptionalString(protocol, object, field, path, upstream, true)
		if err != nil {
			return err
		}
		if present && strings.TrimSpace(value) != "" {
			sources++
			switch field {
			case "file_url":
				fileURL = value
			case "file_data":
				fileData = value
			}
		}
	}
	if sources != 1 {
		return responsesValidationError(protocol, path, upstream, "input_file requires exactly one non-empty file_id, file_url, or file_data")
	}
	if fileURL != "" && !ValidHTTPURL(fileURL) {
		return responsesValidationError(protocol, path+".file_url", upstream, "file_url must be an absolute HTTP(S) URL")
	}
	if fileData != "" {
		if _, err := ParseFileData(fileData, filename); err != nil {
			return responsesValidationError(protocol, path+".file_data", upstream, "%v", err)
		}
	}
	if detail, present, err := responsesOptionalString(protocol, object, "detail", path, upstream, true); err != nil {
		return err
	} else if present && detail != "auto" && detail != "low" && detail != "high" {
		return responsesUnsupportedError(protocol, path+".detail", upstream, "unsupported file detail %q", detail)
	}
	return validateResponsesPromptCacheBreakpoint(protocol, object, path, upstream)
}

func validateResponsesInputAudio(protocol core.Protocol, object map[string]json.RawMessage, path string, upstream bool) error {
	raw, err := responsesRequiredRaw(protocol, object, "input_audio", path, upstream)
	if err != nil {
		return err
	}
	audio, err := responsesObjectFields(protocol, raw, path+".input_audio", upstream, "data", "format")
	if err != nil {
		return err
	}
	if _, err := responsesRequiredString(protocol, audio, "data", path+".input_audio", upstream, false); err != nil {
		return err
	}
	format, err := responsesRequiredString(protocol, audio, "format", path+".input_audio", upstream, true)
	if err != nil {
		return err
	}
	if format != "mp3" && format != "wav" {
		return responsesUnsupportedError(protocol, path+".input_audio.format", upstream, "unsupported audio format %q", format)
	}
	return validateResponsesPromptCacheBreakpoint(protocol, object, path, upstream)
}

// ValidateResponsesInputItems checks fields of the portable Responses input
// item variants before the shared wide wire struct can erase union-specific
// extensions.
func ValidateResponsesInputItems(protocol core.Protocol, raw json.RawMessage, path string) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return core.Invalid(protocol, path, "must be an item array")
	}
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		var discriminator struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &discriminator); err != nil {
			return core.Invalid(protocol, itemPath, "must be an object")
		}
		var fields []string
		switch discriminator.Type {
		case "", "message":
			fields = []string{"type", "role", "content", "status", "phase", "id"}
		case "function_call":
			fields = []string{"type", "id", "call_id", "name", "arguments", "async", "namespace", "caller", "status"}
		case "function_call_output":
			fields = []string{"type", "id", "call_id", "name", "namespace", "caller", "status", "output"}
		case "custom_tool_call":
			fields = []string{"type", "id", "call_id", "name", "input", "async", "namespace", "caller"}
		case "custom_tool_call_output":
			fields = []string{"type", "id", "call_id", "output", "caller"}
		case "reasoning":
			fields = []string{"type", "id", "summary", "content", "encrypted_content", "status"}
		default:
			// Unsupported variants are rejected by the route-specific semantic
			// validator; do not guess at their provider-native field sets here.
			continue
		}
		if _, err := RejectUnknownObjectFields(protocol, item, itemPath, fields...); err != nil {
			return err
		}
	}
	return nil
}

// ValidateResponsesReasoningConfig prevents new nested reasoning controls from
// being mistaken for the currently portable effort/summary subset.
func ValidateResponsesReasoningConfig(protocol core.Protocol, request []byte) error {
	object, err := RawObject(protocol, request)
	if err != nil || !ValuePresent(object["reasoning"]) {
		return err
	}
	_, err = RejectUnknownObjectFields(protocol, object["reasoning"], "$.reasoning", "effort", "summary")
	return err
}

// ValidateResponsesTools checks the fields of Responses tool union members
// before decoding them into the intentionally broad wire Tool struct.
func ValidateResponsesTools(protocol core.Protocol, request []byte) error {
	object, err := RawObject(protocol, request)
	if err != nil || !ValuePresent(object["tools"]) {
		return err
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(object["tools"], &tools); err != nil {
		return core.Invalid(protocol, "$.tools", "must be an array")
	}
	seenNamed := make(map[string]struct{})
	for index, tool := range tools {
		path := fmt.Sprintf("$.tools[%d]", index)
		var discriminator struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(tool, &discriminator); err != nil || discriminator.Type == "" {
			return core.Invalid(protocol, path+".type", "tool type is required")
		}
		var fields []string
		switch discriminator.Type {
		case "function":
			fields = []string{"type", "name", "description", "parameters", "strict", "async", "defer_loading", "allowed_callers", "output_schema"}
		case "custom":
			fields = []string{"type", "name", "description", "async", "defer_loading", "allowed_callers", "format"}
		case "web_search", "web_search_2025_08_26":
			fields = []string{"type", "external_web_access", "filters", "user_location", "search_context_size"}
		case "tool_search":
			fields = []string{"type", "description", "parameters", "execution"}
		default:
			// Route-specific validation reports unsupported hosted/native tools.
			continue
		}
		if _, err := RejectUnknownObjectFields(protocol, tool, path, fields...); err != nil {
			return err
		}
		if discriminator.Type == "function" || discriminator.Type == "custom" {
			var named struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(tool, &named); err == nil && named.Name != "" {
				key := discriminator.Type + "\x00" + named.Name
				if _, duplicate := seenNamed[key]; duplicate {
					return core.Invalid(protocol, path+".name", "duplicate %s tool name %q", discriminator.Type, named.Name)
				}
				seenNamed[key] = struct{}{}
			}
		}
	}
	return nil
}

// ValidateResponsesOutputItems rejects unknown fields in output variants and
// validates their nested content unions before route DTO decoding can discard
// them. Unsupported item types remain the responsibility of the destination
// route so native Responses can evolve independently.
func ValidateResponsesOutputItems(protocol core.Protocol, response []byte) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(response, &object); err != nil || object == nil {
		return core.UpstreamResponseError(protocol, "$", "response must be a JSON object")
	}
	output, ok := object["output"]
	if !ok || bytes.Equal(bytes.TrimSpace(output), []byte("null")) {
		return core.UpstreamResponseError(protocol, "$.output", "output is required and must not be null")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(output, &items); err != nil || items == nil {
		return core.UpstreamResponseError(protocol, "$.output", "output must be an array")
	}
	for index, item := range items {
		path := fmt.Sprintf("$.output[%d]", index)
		var rawFields map[string]json.RawMessage
		if err := json.Unmarshal(item, &rawFields); err != nil || rawFields == nil {
			return core.UpstreamResponseError(protocol, path, "output item must be an object")
		}
		itemType, err := responsesRequiredString(protocol, rawFields, "type", path, true, true)
		if err != nil {
			return err
		}
		var fields []string
		switch itemType {
		case "message":
			fields = []string{"type", "id", "role", "content", "status", "phase"}
		case "function_call":
			fields = []string{"type", "id", "call_id", "name", "arguments", "async", "namespace", "caller", "status"}
		case "function_call_output":
			fields = []string{"type", "id", "call_id", "name", "namespace", "caller", "status", "output", "created_by"}
		case "custom_tool_call":
			fields = []string{"type", "id", "call_id", "name", "input", "async", "namespace", "caller", "status"}
		case "custom_tool_call_output":
			fields = []string{"type", "id", "call_id", "output", "caller", "status", "created_by"}
		case "web_search_call":
			fields = []string{"type", "id", "status", "action"}
		case "reasoning":
			fields = []string{"type", "id", "summary", "content", "encrypted_content", "status"}
		case "tool_search_call":
			fields = []string{"type", "id", "call_id", "arguments", "execution", "status", "created_by"}
		case "tool_search_output":
			fields = []string{"type", "id", "call_id", "execution", "status", "tools", "created_by"}
		case "additional_tools":
			fields = []string{"type", "id", "role", "tools"}
		default:
			continue
		}
		values, err := responsesObjectFields(protocol, item, path, true, fields...)
		if err != nil {
			return err
		}
		switch itemType {
		case "message":
			if _, err := responsesRequiredString(protocol, values, "id", path, true, true); err != nil {
				return err
			}
			if err := responsesRequireEnum(protocol, values, "role", path, true, "assistant"); err != nil {
				return err
			}
			if err := responsesRequireEnum(protocol, values, "status", path, true, "in_progress", "completed", "incomplete"); err != nil {
				return err
			}
			if err := responsesOptionalEnum(protocol, values, "phase", path, true, true, "commentary", "final_answer"); err != nil {
				return err
			}
			content, err := responsesRequiredRaw(protocol, values, "content", path, true)
			if err != nil {
				return err
			}
			if err := validateResponsesContentArray(protocol, content, path+".content", true, "output_text", "refusal"); err != nil {
				return err
			}
		case "function_call":
			if err := validateResponsesCallItem(protocol, values, path, false); err != nil {
				return err
			}
		case "function_call_output":
			if err := validateResponsesCallOutputItem(protocol, values, path, false); err != nil {
				return err
			}
		case "custom_tool_call":
			if err := validateResponsesCallItem(protocol, values, path, true); err != nil {
				return err
			}
		case "custom_tool_call_output":
			if err := validateResponsesCallOutputItem(protocol, values, path, true); err != nil {
				return err
			}
		case "web_search_call":
			if err := validateResponsesWebSearchCall(protocol, values, path); err != nil {
				return err
			}
		case "reasoning":
			if _, err := responsesRequiredString(protocol, values, "id", path, true, true); err != nil {
				return err
			}
			summary, err := responsesRequiredRaw(protocol, values, "summary", path, true)
			if err != nil {
				return err
			}
			if err := validateResponsesContentArray(protocol, summary, path+".summary", true, "summary_text"); err != nil {
				return err
			}
			if content, ok := values["content"]; ok {
				if err := validateResponsesContentArray(protocol, content, path+".content", true, "reasoning_text"); err != nil {
					return err
				}
			}
			if err := responsesOptionalStringField(protocol, values, "encrypted_content", path, true, true); err != nil {
				return err
			}
			if err := responsesOptionalEnum(protocol, values, "status", path, true, false, "in_progress", "completed", "incomplete"); err != nil {
				return err
			}
		case "tool_search_call":
			if err := validateResponsesToolSearchCall(protocol, values, path); err != nil {
				return err
			}
		case "tool_search_output":
			if err := validateResponsesToolSearchOutput(protocol, values, path); err != nil {
				return err
			}
		case "additional_tools":
			if _, err := responsesRequiredString(protocol, values, "id", path, true, true); err != nil {
				return err
			}
			if err := responsesRequireEnum(protocol, values, "role", path, true,
				"unknown", "user", "assistant", "system", "critic", "discriminator", "developer", "tool"); err != nil {
				return err
			}
			if err := validateResponsesToolDefinitions(protocol, values, "tools", path); err != nil {
				return err
			}
		}
	}
	return nil
}

func responsesRequireEnum(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream bool, allowed ...string) error {
	value, err := responsesRequiredString(protocol, object, field, path, upstream, true)
	if err != nil {
		return err
	}
	if !responsesStringIn(value, allowed...) {
		return responsesUnsupportedError(protocol, path+"."+field, upstream, "unsupported %s %q", field, value)
	}
	return nil
}

func responsesOptionalEnum(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream, nullable bool, allowed ...string) error {
	value, present, err := responsesOptionalString(protocol, object, field, path, upstream, nullable)
	if err != nil || !present {
		return err
	}
	if !responsesStringIn(value, allowed...) {
		return responsesUnsupportedError(protocol, path+"."+field, upstream, "unsupported %s %q", field, value)
	}
	return nil
}

func responsesOptionalStringField(protocol core.Protocol, object map[string]json.RawMessage, field, path string, upstream, nullable bool) error {
	_, _, err := responsesOptionalString(protocol, object, field, path, upstream, nullable)
	return err
}

func responsesStringIn(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func validateResponsesCallItem(protocol core.Protocol, values map[string]json.RawMessage, path string, custom bool) error {
	for _, field := range []string{"call_id", "name"} {
		if _, err := responsesRequiredString(protocol, values, field, path, true, true); err != nil {
			return err
		}
	}
	if err := responsesOptionalStringField(protocol, values, "id", path, true, false); err != nil {
		return err
	}
	payloadField := "arguments"
	if custom {
		payloadField = "input"
	}
	if _, err := responsesRequiredString(protocol, values, payloadField, path, true, false); err != nil {
		return err
	}
	if err := responsesOptionalEnum(protocol, values, "status", path, true, false, "in_progress", "completed", "incomplete"); err != nil {
		return err
	}
	if err := responsesOptionalBool(protocol, values, "async", path, true); err != nil {
		return err
	}
	for _, field := range []string{"namespace"} {
		if err := responsesOptionalStringField(protocol, values, field, path, true, false); err != nil {
			return err
		}
	}
	return validateResponsesCaller(protocol, values, path)
}

func validateResponsesCallOutputItem(protocol core.Protocol, values map[string]json.RawMessage, path string, custom bool) error {
	if _, err := responsesRequiredString(protocol, values, "id", path, true, true); err != nil {
		return err
	}
	if custom {
		if _, err := responsesRequiredString(protocol, values, "call_id", path, true, true); err != nil {
			return err
		}
	} else {
		for _, field := range []string{"call_id", "name", "namespace"} {
			if err := responsesOptionalStringField(protocol, values, field, path, true, false); err != nil {
				return err
			}
		}
	}
	if err := responsesRequireEnum(protocol, values, "status", path, true, "in_progress", "completed", "incomplete"); err != nil {
		return err
	}
	if err := responsesOptionalStringField(protocol, values, "created_by", path, true, false); err != nil {
		return err
	}
	output, err := responsesRequiredRaw(protocol, values, "output", path, true)
	if err != nil {
		return err
	}
	if err := validateResponsesToolOutput(protocol, output, path+".output", true); err != nil {
		return err
	}
	return validateResponsesCaller(protocol, values, path)
}

func validateResponsesToolOutput(protocol core.Protocol, raw json.RawMessage, path string, upstream bool) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return responsesValidationError(protocol, path, upstream, "tool output is required")
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return responsesValidationError(protocol, path, upstream, "tool output string is invalid")
		}
		return nil
	}
	if trimmed[0] != '[' {
		return responsesValidationError(protocol, path, upstream, "tool output must be a string or content array")
	}
	return validateResponsesContentArray(protocol, trimmed, path, upstream, "input_text", "input_image", "input_file")
}

// ValidateResponsesProviderToolOutput validates a Responses tool-output union
// received from an upstream provider. It is shared with the native buffered
// renderer so native and cross-protocol paths enforce the same wire schema.
func ValidateResponsesProviderToolOutput(protocol core.Protocol, raw json.RawMessage, path string) error {
	return validateResponsesToolOutput(protocol, raw, path, true)
}

func validateResponsesCaller(protocol core.Protocol, values map[string]json.RawMessage, path string) error {
	raw, ok := values["caller"]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawFields); err != nil || rawFields == nil {
		return core.UpstreamResponseError(protocol, path+".caller", "caller must be an object or null")
	}
	callerType, err := responsesRequiredString(protocol, rawFields, "type", path+".caller", true, true)
	if err != nil {
		return err
	}
	allowed := []string{"type"}
	if callerType == "program" {
		allowed = append(allowed, "caller_id")
	} else if callerType != "direct" {
		return core.UpstreamResponseError(protocol, path+".caller.type", "unsupported caller type %q", callerType)
	}
	fields, err := responsesObjectFields(protocol, raw, path+".caller", true, allowed...)
	if err != nil {
		return err
	}
	if callerType == "program" {
		_, err = responsesRequiredString(protocol, fields, "caller_id", path+".caller", true, true)
	}
	return err
}

func validateResponsesWebSearchCall(protocol core.Protocol, values map[string]json.RawMessage, path string) error {
	if _, err := responsesRequiredString(protocol, values, "id", path, true, true); err != nil {
		return err
	}
	if err := responsesRequireEnum(protocol, values, "status", path, true, "in_progress", "searching", "completed", "failed"); err != nil {
		return err
	}
	raw, err := responsesRequiredRaw(protocol, values, "action", path, true)
	if err != nil {
		return err
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawFields); err != nil || rawFields == nil {
		return core.UpstreamResponseError(protocol, path+".action", "action must be an object")
	}
	actionType, err := responsesRequiredString(protocol, rawFields, "type", path+".action", true, true)
	if err != nil {
		return err
	}
	switch actionType {
	case "search":
		fields, err := responsesObjectFields(protocol, raw, path+".action", true, "type", "query", "queries", "sources")
		if err != nil {
			return err
		}
		if err := responsesOptionalStringField(protocol, fields, "query", path+".action", true, false); err != nil {
			return err
		}
		if queries, ok := fields["queries"]; ok {
			var values []string
			if err := json.Unmarshal(queries, &values); err != nil || values == nil {
				return core.UpstreamResponseError(protocol, path+".action.queries", "queries must be an array of strings")
			}
		}
		if sources, ok := fields["sources"]; ok {
			var values []json.RawMessage
			if err := json.Unmarshal(sources, &values); err != nil || values == nil {
				return core.UpstreamResponseError(protocol, path+".action.sources", "sources must be an array")
			}
			for index, source := range values {
				sourcePath := fmt.Sprintf("%s.action.sources[%d]", path, index)
				sourceFields, err := responsesObjectFields(protocol, source, sourcePath, true, "type", "url")
				if err != nil {
					return err
				}
				if err := responsesRequireEnum(protocol, sourceFields, "type", sourcePath, true, "url"); err != nil {
					return err
				}
				if _, err := responsesRequiredString(protocol, sourceFields, "url", sourcePath, true, true); err != nil {
					return err
				}
			}
		}
	case "open_page":
		fields, err := responsesObjectFields(protocol, raw, path+".action", true, "type", "url")
		if err != nil {
			return err
		}
		if err := responsesOptionalStringField(protocol, fields, "url", path+".action", true, true); err != nil {
			return err
		}
	case "find_in_page":
		fields, err := responsesObjectFields(protocol, raw, path+".action", true, "type", "url", "pattern")
		if err != nil {
			return err
		}
		for _, field := range []string{"url", "pattern"} {
			if _, err := responsesRequiredString(protocol, fields, field, path+".action", true, true); err != nil {
				return err
			}
		}
	default:
		return core.UpstreamResponseError(protocol, path+".action.type", "unsupported web search action type %q", actionType)
	}
	return nil
}

// Server-executed tool search responses in the wild can omit or null call_id
// and execution. Preserve that provider shape, while keeping explicit
// client-side discovery correlated and validating either non-null field.
func validateResponsesToolSearchCall(protocol core.Protocol, values map[string]json.RawMessage, path string) error {
	if _, err := responsesRequiredString(protocol, values, "id", path, true, true); err != nil {
		return err
	}
	callID, hasCallID, err := responsesOptionalString(protocol, values, "call_id", path, true, true)
	if err != nil {
		return err
	}
	if hasCallID && strings.TrimSpace(callID) == "" {
		return core.UpstreamResponseError(protocol, path+".call_id", "call_id must not be empty")
	}
	if _, err := responsesRequiredRaw(protocol, values, "arguments", path, true); err != nil {
		return err
	}
	execution, hasExecution, err := responsesOptionalString(protocol, values, "execution", path, true, true)
	if err != nil {
		return err
	}
	if hasExecution && !responsesStringIn(execution, "server", "client") {
		return core.UpstreamResponseError(protocol, path+".execution", "unsupported execution %q", execution)
	}
	if execution == "client" && !hasCallID {
		return core.UpstreamResponseError(protocol, path+".call_id", "client tool_search_call requires call_id")
	}
	if err := responsesRequireEnum(protocol, values, "status", path, true, "in_progress", "completed", "incomplete"); err != nil {
		return err
	}
	return responsesOptionalStringField(protocol, values, "created_by", path, true, false)
}

func validateResponsesToolSearchOutput(protocol core.Protocol, values map[string]json.RawMessage, path string) error {
	if _, err := responsesRequiredString(protocol, values, "id", path, true, true); err != nil {
		return err
	}
	callID, hasCallID, err := responsesOptionalString(protocol, values, "call_id", path, true, true)
	if err != nil {
		return err
	}
	if hasCallID && strings.TrimSpace(callID) == "" {
		return core.UpstreamResponseError(protocol, path+".call_id", "call_id must not be empty")
	}
	execution, hasExecution, err := responsesOptionalString(protocol, values, "execution", path, true, true)
	if err != nil {
		return err
	}
	if hasExecution && !responsesStringIn(execution, "server", "client") {
		return core.UpstreamResponseError(protocol, path+".execution", "unsupported execution %q", execution)
	}
	if execution == "client" && !hasCallID {
		return core.UpstreamResponseError(protocol, path+".call_id", "client tool_search_output requires call_id")
	}
	if err := responsesRequireEnum(protocol, values, "status", path, true, "in_progress", "completed", "incomplete"); err != nil {
		return err
	}
	if err := responsesOptionalStringField(protocol, values, "created_by", path, true, false); err != nil {
		return err
	}
	return validateResponsesToolDefinitions(protocol, values, "tools", path)
}

func validateResponsesToolDefinitions(protocol core.Protocol, values map[string]json.RawMessage, field, path string) error {
	raw, err := responsesRequiredRaw(protocol, values, field, path, true)
	if err != nil {
		return err
	}
	return ValidateResponsesProviderToolDefinitions(protocol, raw, path+"."+field)
}

// ValidateResponsesProviderToolDefinitions validates the Responses ToolUnion
// returned by tool_search_output/additional_tools. Unknown discriminators and
// missing required fields must not be relayed as apparently valid tools.
func ValidateResponsesProviderToolDefinitions(protocol core.Protocol, raw json.RawMessage, path string) error {
	var tools []json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil || tools == nil {
		return core.UpstreamResponseError(protocol, path, "tools must be an array")
	}
	for index, tool := range tools {
		toolPath := fmt.Sprintf("%s[%d]", path, index)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(tool, &fields); err != nil || fields == nil {
			return core.UpstreamResponseError(protocol, toolPath, "tool definition must be an object")
		}
		toolType, err := responsesRequiredString(protocol, fields, "type", toolPath, true, true)
		if err != nil {
			return err
		}
		switch toolType {
		case "function":
			if err := validateResponsesFunctionToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "custom":
			if err := validateResponsesCustomToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "file_search":
			if err := validateResponsesFileSearchToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "computer_use_preview":
			if err := validateResponsesComputerUseToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "namespace":
			if err := validateResponsesNamespaceToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "mcp":
			if err := validateResponsesMCPToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "code_interpreter":
			if err := validateResponsesCodeInterpreterToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "web_search", "web_search_2025_08_26":
			if err := validateResponsesWebSearchToolDefinition(protocol, fields, toolPath, false); err != nil {
				return err
			}
		case "web_search_preview", "web_search_preview_2025_03_11":
			if err := validateResponsesWebSearchToolDefinition(protocol, fields, toolPath, true); err != nil {
				return err
			}
		case "image_generation":
			if err := validateResponsesImageGenerationToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "shell":
			if err := validateResponsesShellToolDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "tool_search":
			if err := validateResponsesToolSearchDefinition(protocol, fields, toolPath); err != nil {
				return err
			}
		case "apply_patch":
			if err := validateResponsesAllowedCallers(protocol, fields, toolPath); err != nil {
				return err
			}
		case "computer", "programmatic_tool_calling", "local_shell":
			// The v3.56 response variants contain only their discriminator.
		default:
			return core.UpstreamResponseError(protocol, toolPath+".type", "tool type %q is not part of the Responses tool union", toolType)
		}
	}
	return nil
}

func validateResponsesFunctionToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	if _, err := responsesRequiredString(protocol, fields, "name", path, true, true); err != nil {
		return err
	}
	parameters, err := responsesRequiredRaw(protocol, fields, "parameters", path, true)
	if err != nil {
		return err
	}
	var schema map[string]json.RawMessage
	if json.Unmarshal(parameters, &schema) != nil || schema == nil {
		return core.UpstreamResponseError(protocol, path+".parameters", "parameters must be an object")
	}
	strict, err := responsesRequiredRaw(protocol, fields, "strict", path, true)
	if err != nil {
		return err
	}
	var strictValue bool
	if json.Unmarshal(strict, &strictValue) != nil {
		return core.UpstreamResponseError(protocol, path+".strict", "strict must be a boolean")
	}
	if err := validateResponsesCallableToolOptions(protocol, fields, path); err != nil {
		return err
	}
	if err := responsesOptionalStringField(protocol, fields, "description", path, true, true); err != nil {
		return err
	}
	if err := responsesOptionalObject(protocol, fields, "output_schema", path, true, true); err != nil {
		return err
	}
	return nil
}

func validateResponsesCustomToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	if _, err := responsesRequiredString(protocol, fields, "name", path, true, true); err != nil {
		return err
	}
	if err := validateResponsesCallableToolOptions(protocol, fields, path); err != nil {
		return err
	}
	if err := responsesOptionalStringField(protocol, fields, "description", path, true, false); err != nil {
		return err
	}
	raw, ok := fields["format"]
	if !ok {
		return nil
	}
	format, err := responsesProviderObject(protocol, raw, path+".format", false)
	if err != nil {
		return err
	}
	formatType, err := responsesRequiredString(protocol, format, "type", path+".format", true, true)
	if err != nil {
		return err
	}
	switch formatType {
	case "text":
		return nil
	case "grammar":
		if _, err := responsesRequiredString(protocol, format, "definition", path+".format", true, false); err != nil {
			return err
		}
		return responsesRequireEnum(protocol, format, "syntax", path+".format", true, "lark", "regex")
	default:
		return core.UpstreamResponseError(protocol, path+".format.type", "unsupported custom tool format %q", formatType)
	}
}

func validateResponsesCallableToolOptions(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	if err := validateResponsesAllowedCallers(protocol, fields, path); err != nil {
		return err
	}
	for _, field := range []string{"async", "defer_loading"} {
		if err := responsesOptionalBool(protocol, fields, field, path, true); err != nil {
			return err
		}
	}
	return nil
}

func validateResponsesAllowedCallers(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	return responsesOptionalStringArray(protocol, fields, "allowed_callers", path, true, true, true, "direct", "programmatic")
}

func validateResponsesFileSearchToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	if err := responsesRequiredStringArray(protocol, fields, "vector_store_ids", path, true, true); err != nil {
		return err
	}
	if raw, ok := fields["filters"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := validateResponsesFileSearchFilter(protocol, raw, path+".filters"); err != nil {
			return err
		}
	}
	if err := responsesOptionalInteger(protocol, fields, "max_num_results", path, true, 1, 50); err != nil {
		return err
	}
	raw, ok := fields["ranking_options"]
	if !ok {
		return nil
	}
	ranking, err := responsesProviderObject(protocol, raw, path+".ranking_options", false)
	if err != nil {
		return err
	}
	if err := responsesOptionalEnum(protocol, ranking, "ranker", path+".ranking_options", true, false, "auto", "default-2024-11-15"); err != nil {
		return err
	}
	if err := responsesOptionalNumber(protocol, ranking, "score_threshold", path+".ranking_options", true, 0, 1); err != nil {
		return err
	}
	if hybridRaw, ok := ranking["hybrid_search"]; ok {
		hybrid, err := responsesProviderObject(protocol, hybridRaw, path+".ranking_options.hybrid_search", false)
		if err != nil {
			return err
		}
		for _, field := range []string{"embedding_weight", "text_weight"} {
			if err := responsesRequiredNumber(protocol, hybrid, field, path+".ranking_options"); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateResponsesFileSearchFilter(protocol core.Protocol, raw json.RawMessage, path string) error {
	filter, err := responsesProviderObject(protocol, raw, path, false)
	if err != nil {
		return err
	}
	filterType, err := responsesRequiredString(protocol, filter, "type", path, true, true)
	if err != nil {
		return err
	}
	switch filterType {
	case "eq", "ne", "gt", "gte", "lt", "lte", "in", "nin":
		if _, err := responsesRequiredString(protocol, filter, "key", path, true, true); err != nil {
			return err
		}
		_, err := responsesRequiredRaw(protocol, filter, "value", path, true)
		return err
	case "and", "or":
		children, err := responsesRequiredArray(protocol, filter, "filters", path, true)
		if err != nil {
			return err
		}
		for index, child := range children {
			if err := validateResponsesFileSearchFilter(protocol, child, fmt.Sprintf("%s.filters[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	default:
		return core.UpstreamResponseError(protocol, path+".type", "unsupported file search filter type %q", filterType)
	}
}

func validateResponsesComputerUseToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	for _, field := range []string{"display_height", "display_width"} {
		raw, err := responsesRequiredRaw(protocol, fields, field, path, true)
		if err != nil {
			return err
		}
		var value int64
		if json.Unmarshal(raw, &value) != nil {
			return core.UpstreamResponseError(protocol, path+"."+field, "%s must be an integer", field)
		}
	}
	return responsesRequireEnum(protocol, fields, "environment", path, true, "windows", "mac", "linux", "ubuntu", "browser")
}

func validateResponsesNamespaceToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	for _, field := range []string{"description", "name"} {
		if _, err := responsesRequiredString(protocol, fields, field, path, true, true); err != nil {
			return err
		}
	}
	nested, err := responsesRequiredArray(protocol, fields, "tools", path, true)
	if err != nil {
		return err
	}
	for index, raw := range nested {
		nestedPath := fmt.Sprintf("%s.tools[%d]", path, index)
		nestedFields, err := responsesProviderObject(protocol, raw, nestedPath, false)
		if err != nil {
			return err
		}
		nestedType, err := responsesRequiredString(protocol, nestedFields, "type", nestedPath, true, true)
		if err != nil {
			return err
		}
		switch nestedType {
		case "function":
			if _, err := responsesRequiredString(protocol, nestedFields, "name", nestedPath, true, true); err != nil {
				return err
			}
			if err := validateResponsesCallableToolOptions(protocol, nestedFields, nestedPath); err != nil {
				return err
			}
			if err := responsesOptionalObject(protocol, nestedFields, "output_schema", nestedPath, true, true); err != nil {
				return err
			}
			if err := responsesOptionalBoolNullable(protocol, nestedFields, "strict", nestedPath, true, true); err != nil {
				return err
			}
			if err := responsesOptionalStringField(protocol, nestedFields, "description", nestedPath, true, true); err != nil {
				return err
			}
		case "custom":
			if err := validateResponsesCustomToolDefinition(protocol, nestedFields, nestedPath); err != nil {
				return err
			}
		default:
			return core.UpstreamResponseError(protocol, nestedPath+".type", "unsupported namespace tool type %q", nestedType)
		}
	}
	return nil
}

func validateResponsesMCPToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	if _, err := responsesRequiredString(protocol, fields, "server_label", path, true, true); err != nil {
		return err
	}
	if err := validateResponsesAllowedCallers(protocol, fields, path); err != nil {
		return err
	}
	if err := responsesOptionalBool(protocol, fields, "defer_loading", path, true); err != nil {
		return err
	}
	for _, field := range []string{"authorization", "server_description"} {
		if err := responsesOptionalStringField(protocol, fields, field, path, true, false); err != nil {
			return err
		}
	}
	endpointCount := 0
	for _, field := range []string{"server_url", "connector_id", "tunnel_id"} {
		value, present, err := responsesOptionalString(protocol, fields, field, path, true, false)
		if err != nil {
			return err
		}
		if present {
			if strings.TrimSpace(value) == "" {
				return core.UpstreamResponseError(protocol, path+"."+field, "%s must not be empty", field)
			}
			endpointCount++
		}
	}
	if endpointCount != 1 {
		return core.UpstreamResponseError(protocol, path, "exactly one of server_url, connector_id, or tunnel_id is required")
	}
	if err := responsesOptionalEnum(protocol, fields, "connector_id", path, true, false,
		"connector_dropbox", "connector_gmail", "connector_googlecalendar", "connector_googledrive",
		"connector_microsoftteams", "connector_outlookcalendar", "connector_outlookemail", "connector_sharepoint"); err != nil {
		return err
	}
	if err := responsesOptionalStringMap(protocol, fields, "headers", path, true, true); err != nil {
		return err
	}
	if err := validateResponsesMCPAllowedTools(protocol, fields, path); err != nil {
		return err
	}
	return validateResponsesMCPRequireApproval(protocol, fields, path)
}

func validateResponsesMCPAllowedTools(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	raw, ok := fields["allowed_tools"]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
		return validateResponsesRawStringArray(protocol, raw, path+".allowed_tools", true, false, nil)
	}
	filter, err := responsesProviderObject(protocol, raw, path+".allowed_tools", false)
	if err != nil {
		return err
	}
	if err := responsesOptionalBool(protocol, filter, "read_only", path+".allowed_tools", true); err != nil {
		return err
	}
	if names, ok := filter["tool_names"]; ok {
		return validateResponsesRawStringArray(protocol, names, path+".allowed_tools.tool_names", true, false, nil)
	}
	return nil
}

func validateResponsesMCPRequireApproval(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	raw, ok := fields["require_approval"]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("\"")) {
		value, _, err := responsesOptionalString(protocol, fields, "require_approval", path, true, false)
		if err != nil {
			return err
		}
		if !responsesStringIn(value, "always", "never") {
			return core.UpstreamResponseError(protocol, path+".require_approval", "unsupported require_approval %q", value)
		}
		return nil
	}
	approval, err := responsesProviderObject(protocol, raw, path+".require_approval", false)
	if err != nil {
		return err
	}
	for _, field := range []string{"always", "never"} {
		filterRaw, ok := approval[field]
		if !ok {
			continue
		}
		filter, err := responsesProviderObject(protocol, filterRaw, path+".require_approval."+field, false)
		if err != nil {
			return err
		}
		if err := responsesOptionalBool(protocol, filter, "read_only", path+".require_approval."+field, true); err != nil {
			return err
		}
		if names, ok := filter["tool_names"]; ok {
			if err := validateResponsesRawStringArray(protocol, names, path+".require_approval."+field+".tool_names", true, false, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateResponsesCodeInterpreterToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	raw, err := responsesRequiredRaw(protocol, fields, "container", path, true)
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var value string
		if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
			return core.UpstreamResponseError(protocol, path+".container", "container must be a non-empty string or auto configuration object")
		}
	} else {
		container, err := responsesProviderObject(protocol, raw, path+".container", false)
		if err != nil {
			return err
		}
		if err := responsesRequireEnum(protocol, container, "type", path+".container", true, "auto"); err != nil {
			return err
		}
		if err := responsesOptionalStringArray(protocol, container, "file_ids", path+".container", true, false, true); err != nil {
			return err
		}
		if err := responsesOptionalEnum(protocol, container, "memory_limit", path+".container", true, true, "1g", "4g", "16g", "64g"); err != nil {
			return err
		}
		if networkRaw, ok := container["network_policy"]; ok {
			if err := validateResponsesNetworkPolicy(protocol, networkRaw, path+".container.network_policy"); err != nil {
				return err
			}
		}
	}
	return validateResponsesAllowedCallers(protocol, fields, path)
}

func validateResponsesNetworkPolicy(protocol core.Protocol, raw json.RawMessage, path string) error {
	policy, err := responsesProviderObject(protocol, raw, path, false)
	if err != nil {
		return err
	}
	policyType, err := responsesRequiredString(protocol, policy, "type", path, true, true)
	if err != nil {
		return err
	}
	switch policyType {
	case "disabled":
		return nil
	case "allowlist":
		if err := responsesRequiredStringArray(protocol, policy, "allowed_domains", path, true, true); err != nil {
			return err
		}
		secrets, ok := policy["domain_secrets"]
		if !ok {
			return nil
		}
		var entries []json.RawMessage
		if json.Unmarshal(secrets, &entries) != nil || entries == nil {
			return core.UpstreamResponseError(protocol, path+".domain_secrets", "domain_secrets must be an array")
		}
		for index, entry := range entries {
			entryPath := fmt.Sprintf("%s.domain_secrets[%d]", path, index)
			secret, err := responsesProviderObject(protocol, entry, entryPath, false)
			if err != nil {
				return err
			}
			for _, field := range []string{"domain", "name", "value"} {
				if _, err := responsesRequiredString(protocol, secret, field, entryPath, true, true); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return core.UpstreamResponseError(protocol, path+".type", "unsupported network policy type %q", policyType)
	}
}

func validateResponsesWebSearchToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string, preview bool) error {
	if err := responsesOptionalEnum(protocol, fields, "search_context_size", path, true, false, "low", "medium", "high"); err != nil {
		return err
	}
	if preview {
		if err := responsesOptionalStringArray(protocol, fields, "search_content_types", path, true, false, false, "text", "image"); err != nil {
			return err
		}
	} else {
		if err := responsesOptionalBool(protocol, fields, "external_web_access", path, true); err != nil {
			return err
		}
		if filtersRaw, ok := fields["filters"]; ok && !bytes.Equal(bytes.TrimSpace(filtersRaw), []byte("null")) {
			filters, err := responsesProviderObject(protocol, filtersRaw, path+".filters", false)
			if err != nil {
				return err
			}
			if err := responsesOptionalStringArray(protocol, filters, "allowed_domains", path+".filters", true, true, true); err != nil {
				return err
			}
		}
	}
	locationRaw, ok := fields["user_location"]
	if !ok || bytes.Equal(bytes.TrimSpace(locationRaw), []byte("null")) {
		return nil
	}
	location, err := responsesProviderObject(protocol, locationRaw, path+".user_location", false)
	if err != nil {
		return err
	}
	if err := responsesOptionalEnum(protocol, location, "type", path+".user_location", true, false, "approximate"); err != nil {
		return err
	}
	for _, field := range []string{"city", "country", "region", "timezone"} {
		if err := responsesOptionalStringField(protocol, location, field, path+".user_location", true, true); err != nil {
			return err
		}
	}
	return nil
}

func validateResponsesImageGenerationToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	for field, allowed := range map[string][]string{
		"action":         {"generate", "edit", "auto"},
		"background":     {"transparent", "opaque", "auto"},
		"input_fidelity": {"high", "low"},
		"model":          {"gpt-image-1", "gpt-image-1-mini", "gpt-image-1.5", "gpt-image-2", "gpt-image-2-2026-04-21", "chatgpt-image-latest"},
		"moderation":     {"auto", "low"},
		"output_format":  {"png", "webp", "jpeg"},
		"quality":        {"low", "medium", "high", "auto"},
	} {
		nullable := field == "input_fidelity"
		if err := responsesOptionalEnum(protocol, fields, field, path, true, nullable, allowed...); err != nil {
			return err
		}
	}
	if err := responsesOptionalStringField(protocol, fields, "size", path, true, false); err != nil {
		return err
	}
	if err := responsesOptionalInteger(protocol, fields, "output_compression", path, true, 0, 100); err != nil {
		return err
	}
	if err := responsesOptionalInteger(protocol, fields, "partial_images", path, true, 0, 3); err != nil {
		return err
	}
	if maskRaw, ok := fields["input_image_mask"]; ok {
		mask, err := responsesProviderObject(protocol, maskRaw, path+".input_image_mask", false)
		if err != nil {
			return err
		}
		for _, field := range []string{"file_id", "image_url"} {
			if err := responsesOptionalStringField(protocol, mask, field, path+".input_image_mask", true, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateResponsesShellToolDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	if err := validateResponsesAllowedCallers(protocol, fields, path); err != nil {
		return err
	}
	raw, ok := fields["environment"]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	environment, err := responsesProviderObject(protocol, raw, path+".environment", false)
	if err != nil {
		return err
	}
	environmentType, err := responsesRequiredString(protocol, environment, "type", path+".environment", true, true)
	if err != nil {
		return err
	}
	switch environmentType {
	case "container_reference":
		_, err := responsesRequiredString(protocol, environment, "container_id", path+".environment", true, true)
		return err
	case "container_auto":
		if err := responsesOptionalStringArray(protocol, environment, "file_ids", path+".environment", true, false, true); err != nil {
			return err
		}
		if err := responsesOptionalEnum(protocol, environment, "memory_limit", path+".environment", true, false, "1g", "4g", "16g", "64g"); err != nil {
			return err
		}
		if networkRaw, ok := environment["network_policy"]; ok {
			if err := validateResponsesNetworkPolicy(protocol, networkRaw, path+".environment.network_policy"); err != nil {
				return err
			}
		}
		if err := responsesOptionalArray(protocol, environment, "skills", path+".environment", true); err != nil {
			return err
		}
		return nil
	case "local":
		return responsesOptionalArray(protocol, environment, "skills", path+".environment", true)
	default:
		return core.UpstreamResponseError(protocol, path+".environment.type", "unsupported shell environment type %q", environmentType)
	}
}

func validateResponsesToolSearchDefinition(protocol core.Protocol, fields map[string]json.RawMessage, path string) error {
	if err := responsesOptionalStringField(protocol, fields, "description", path, true, true); err != nil {
		return err
	}
	return responsesOptionalEnum(protocol, fields, "execution", path, true, false, "server", "client")
}

func responsesProviderObject(protocol core.Protocol, raw json.RawMessage, path string, nullable bool) (map[string]json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if nullable {
			return nil, nil
		}
		return nil, core.UpstreamResponseError(protocol, path, "must not be null")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, core.UpstreamResponseError(protocol, path, "must be an object")
	}
	return object, nil
}

func responsesOptionalObject(protocol core.Protocol, fields map[string]json.RawMessage, field, path string, upstream, nullable bool) error {
	raw, ok := fields[field]
	if !ok {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && nullable {
		return nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must be an object", field)
	}
	return nil
}

func responsesRequiredStringArray(protocol core.Protocol, fields map[string]json.RawMessage, field, path string, upstream, nonEmptyItems bool) error {
	raw, err := responsesRequiredRaw(protocol, fields, field, path, upstream)
	if err != nil {
		return err
	}
	return validateResponsesRawStringArray(protocol, raw, path+"."+field, upstream, nonEmptyItems, nil)
}

func responsesOptionalStringArray(protocol core.Protocol, fields map[string]json.RawMessage, field, path string, upstream, nullable, nonEmptyItems bool, allowed ...string) error {
	raw, ok := fields[field]
	if !ok {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if nullable {
			return nil
		}
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must not be null", field)
	}
	return validateResponsesRawStringArray(protocol, raw, path+"."+field, upstream, nonEmptyItems, allowed)
}

func validateResponsesRawStringArray(protocol core.Protocol, raw json.RawMessage, path string, upstream, nonEmptyItems bool, allowed []string) error {
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return responsesValidationError(protocol, path, upstream, "must be an array")
	}
	for index, rawValue := range values {
		var value string
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if json.Unmarshal(rawValue, &value) != nil {
			return responsesValidationError(protocol, itemPath, upstream, "must be a string")
		}
		if nonEmptyItems && strings.TrimSpace(value) == "" {
			return responsesValidationError(protocol, itemPath, upstream, "must not be empty")
		}
		if len(allowed) > 0 && !responsesStringIn(value, allowed...) {
			return responsesUnsupportedError(protocol, itemPath, upstream, "unsupported value %q", value)
		}
	}
	return nil
}

func responsesOptionalBoolNullable(protocol core.Protocol, fields map[string]json.RawMessage, field, path string, upstream, nullable bool) error {
	raw, ok := fields[field]
	if !ok {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if nullable {
			return nil
		}
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must not be null", field)
	}
	return responsesOptionalBool(protocol, fields, field, path, upstream)
}

func responsesOptionalStringMap(protocol core.Protocol, fields map[string]json.RawMessage, field, path string, upstream, nullable bool) error {
	raw, ok := fields[field]
	if !ok {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if nullable {
			return nil
		}
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must not be null", field)
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must be an object", field)
	}
	for key, rawValue := range values {
		var value string
		if json.Unmarshal(rawValue, &value) != nil {
			return responsesValidationError(protocol, path+"."+field+"."+key, upstream, "header value must be a string")
		}
	}
	return nil
}

func responsesOptionalInteger(protocol core.Protocol, fields map[string]json.RawMessage, field, path string, upstream bool, min, max int64) error {
	raw, ok := fields[field]
	if !ok {
		return nil
	}
	var value int64
	if json.Unmarshal(raw, &value) != nil {
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must be an integer", field)
	}
	if value < min || value > max {
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must be between %d and %d", field, min, max)
	}
	return nil
}

func responsesOptionalNumber(protocol core.Protocol, fields map[string]json.RawMessage, field, path string, upstream bool, min, max float64) error {
	raw, ok := fields[field]
	if !ok {
		return nil
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil {
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must be a number", field)
	}
	if value < min || value > max {
		return responsesValidationError(protocol, path+"."+field, upstream, "%s must be between %v and %v", field, min, max)
	}
	return nil
}

func responsesRequiredNumber(protocol core.Protocol, fields map[string]json.RawMessage, field, path string) error {
	raw, err := responsesRequiredRaw(protocol, fields, field, path, true)
	if err != nil {
		return err
	}
	var value float64
	if json.Unmarshal(raw, &value) != nil {
		return core.UpstreamResponseError(protocol, path+"."+field, "%s must be a number", field)
	}
	return nil
}

func ResolveExchangeStream(source bool, exchange core.ExchangeMetadata) bool {
	if exchange.StreamSet || exchange.Stream {
		return exchange.Stream
	}
	return source
}

func TextParts(text string) []core.Part {
	if text == "" {
		return nil
	}
	return []core.Part{{Kind: core.PartText, Text: text}}
}

func DataURL(media *core.Media) string {
	if media == nil {
		return ""
	}
	if media.URL != "" {
		return media.URL
	}
	if media.Data == "" {
		return ""
	}
	mimeType := media.MIMEType
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return "data:" + mimeType + ";base64," + media.Data
}

func ChatAudioMIMEType(format string) (string, bool) {
	switch strings.ToLower(format) {
	case "wav":
		return "audio/wav", true
	case "mp3":
		return "audio/mpeg", true
	default:
		return "", false
	}
}

func ChatAudioFormat(mimeType string) (string, bool) {
	switch strings.ToLower(mimeType) {
	case "audio/wav", "audio/x-wav":
		return "wav", true
	case "audio/mp3", "audio/mpeg":
		return "mp3", true
	default:
		return "", false
	}
}

func ValidBase64(value string) bool {
	if value == "" {
		return false
	}
	if _, err := base64.StdEncoding.DecodeString(value); err == nil {
		return true
	}
	_, err := base64.RawStdEncoding.DecodeString(value)
	return err == nil
}

func ValidOpenAIImageMIMEType(value string) bool {
	switch strings.ToLower(value) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

// ValidMIMEType reports whether value is a bare IANA-style media type. Gemini
// inlineData/fileData carry the media type separately and cannot preserve MIME
// parameters such as charset without changing the wire value.
func ValidMIMEType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(value))
	return err == nil && strings.Contains(mediaType, "/") && len(parameters) == 0
}

// MIMETypeFromFilename returns a normalized media type inferred from a file
// extension. An empty result means that the type cannot be inferred safely.
func MIMETypeFromFilename(filename string) string {
	mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if mediaType == "" {
		return ""
	}
	normalized, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return ""
	}
	return strings.ToLower(normalized)
}

// MIMETypeFromURL infers a media type from the URL path without treating query
// parameters or fragments as part of the filename.
func MIMETypeFromURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return MIMETypeFromFilename(parsed.Path)
}

// ValidHTTPURL reports whether value is an absolute HTTP(S) URL.
func ValidHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != ""
}

// OpenAIFileData preserves an explicit media type by emitting the documented
// data-URL form. Raw base64 remains raw when no media type is known.
func OpenAIFileData(media *core.Media) string {
	if media == nil || media.Data == "" {
		return ""
	}
	if media.MIMEType == "" {
		return media.Data
	}
	return DataURL(media)
}

// ParseFileData validates inline file data and carries a MIME type when it is
// encoded in a data URL or can be inferred from the filename.
func ParseFileData(value, filename string) (*core.Media, error) {
	if strings.HasPrefix(value, "data:") {
		media, err := ParseDataURL(value)
		if err != nil {
			return nil, err
		}
		media.Filename = filename
		return media, nil
	}
	if !ValidBase64(value) {
		return nil, fmt.Errorf("file data is not valid base64")
	}
	return &core.Media{Data: value, MIMEType: MIMETypeFromFilename(filename), Filename: filename}, nil
}

// GeminiFileURICompatible reports whether a URI is in one of the forms used by
// Gemini FileData: a Gemini Files API URI or a Google Cloud Storage URI.
func GeminiFileURICompatible(value string) bool {
	if strings.HasPrefix(value, "gs://") {
		return len(strings.TrimPrefix(value, "gs://")) > 0
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "storage.googleapis.com" || strings.HasSuffix(host, ".storage.googleapis.com") || host == "generativelanguage.googleapis.com"
}

// PortableGeminiFileURI reports whether a Gemini FileData URI is also a plain
// HTTPS URL that another provider can fetch without Gemini Files credentials.
func PortableGeminiFileURI(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "storage.googleapis.com" || strings.HasSuffix(host, ".storage.googleapis.com")
}

func ParseDataURL(value string) (*core.Media, error) {
	if !strings.HasPrefix(value, "data:") {
		if !ValidHTTPURL(value) {
			return nil, fmt.Errorf("URL must be an absolute HTTP(S) URL or a base64 data URL")
		}
		return &core.Media{URL: value}, nil
	}
	header, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return nil, fmt.Errorf("data URL is missing a comma")
	}
	mimeType, parameters, _ := strings.Cut(header, ";")
	if !ValidMIMEType(mimeType) || !strings.EqualFold(parameters, "base64") {
		return nil, fmt.Errorf("data URL must declare a media type and base64 encoding")
	}
	if !ValidBase64(data) {
		return nil, fmt.Errorf("data URL payload is not valid base64")
	}
	return &core.Media{MIMEType: mimeType, Data: data}, nil
}
