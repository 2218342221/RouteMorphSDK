// Package routekit contains protocol-neutral mechanics shared by direct route
// packages. Semantic compatibility decisions remain owned by each protocol
// pair so sharing these helpers cannot introduce an implicit conversion path.
package routekit

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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

func ParseDataURL(value string) *core.Media {
	if !strings.HasPrefix(value, "data:") {
		return &core.Media{URL: value}
	}
	header, data, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return &core.Media{URL: value}
	}
	mimeType, _, _ := strings.Cut(header, ";")
	if strings.Contains(header, ";base64") {
		if _, err := base64.StdEncoding.DecodeString(data); err == nil {
			return &core.Media{MIMEType: mimeType, Data: data}
		}
	}
	return &core.Media{URL: value}
}
