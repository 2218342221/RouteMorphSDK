package stream

import (
	"encoding/json"

	jsonx "github.com/2218342221/RouteMorphSDK/internal/jsonx"
	routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"
)

func decodeJSON(protocol Protocol, data []byte, dst any) error {
	if err := jsonx.DecodeOne(data, dst); err != nil {
		return invalid(protocol, "$", "%v", err)
	}
	return nil
}

func marshal(protocol Protocol, value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, invalid(protocol, "$", "cannot encode JSON: %v", err)
	}
	return data, nil
}

func mustJSON(value any) []byte {
	return routekit.MustJSON(value)
}

func mergeMap(left, right map[string]any) map[string]any {
	merged := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		merged[key] = value
	}
	for key, value := range right {
		merged[key] = value
	}
	return merged
}

func cloneMap(source map[string]any) map[string]any { return mergeMap(source, nil) }

func normalizeArguments(protocol Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := jsonx.NormalizeObject(raw)
	if err != nil {
		return nil, invalid(protocol, path, "tool arguments must be a JSON object: %v", err)
	}
	return normalized, nil
}

func rawString(raw json.RawMessage) string { return jsonx.RawString(raw) }

func appendDiagnostic(diags []Diagnostic, severity, code, path, message string) []Diagnostic {
	return append(diags, Diagnostic{Severity: severity, Code: code, Path: path, Message: message})
}
