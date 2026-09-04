package responsesgemini

import (
	"encoding/json"

	jsonx "github.com/2218342221/RouteMorphSDK/internal/jsonx"
)

func normalizeOpenAIToolArguments(protocol Protocol, path string, raw json.RawMessage) (json.RawMessage, error) {
	normalized, err := jsonx.NormalizeObject(raw)
	if err != nil {
		return nil, invalid(protocol, path, "tool arguments must be an object or an object encoded as a string")
	}
	return normalized, nil
}
