package codec

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func InspectRequest(ctx context.Context, protocol core.Protocol, requestURL *url.URL, body []byte) (core.RequestInfo, error) {
	metadata, _, _, err := inspectRequest(ctx, protocol, requestURL, body, false)
	return metadata, err
}

// InspectRelayRequest performs the same protocol validation as InspectRequest
// and extracts the Chat stream option needed by relay conversion. All fields
// come from one decoded request object so the relay hot path does not parse the
// same payload repeatedly.
func InspectRelayRequest(ctx context.Context, protocol core.Protocol, requestURL *url.URL, body []byte) (core.RequestInfo, bool, bool, error) {
	return inspectRequest(ctx, protocol, requestURL, body, true)
}

func inspectRequest(ctx context.Context, protocol core.Protocol, requestURL *url.URL, body []byte, relay bool) (core.RequestInfo, bool, bool, error) {
	if ctx == nil {
		return core.RequestInfo{}, false, false, fmt.Errorf("nil context")
	}
	adapter, err := For(protocol)
	if err != nil {
		return core.RequestInfo{}, false, false, err
	}
	hint := core.RequestHint{}
	if protocol == core.ProtocolGenerateContent {
		model, stream, err := inspectGeminiRequestURL(requestURL)
		if err != nil {
			return core.RequestInfo{}, false, false, err
		}
		hint.Model = model
		hint.Stream = &stream
	}
	object, err := rawObject(protocol, body)
	if err != nil {
		return core.RequestInfo{}, false, false, err
	}
	metadata, err := adapter.inspectRequestObject(object, hint)
	if err != nil {
		return core.RequestInfo{}, false, false, err
	}
	if err := adapter.validateRequestObject(object); err != nil {
		return core.RequestInfo{}, false, false, err
	}
	if relay && protocol == core.ProtocolChat {
		includeUsage, includeUsageSet, err := inspectChatStreamIncludeUsageObject(object)
		return metadata, includeUsage, includeUsageSet, err
	}
	return metadata, false, false, nil
}

func inspectGeminiRequestURL(requestURL *url.URL) (string, bool, error) {
	if requestURL == nil {
		return "", false, invalid(core.ProtocolGenerateContent, "$.url", "URL is required")
	}
	escapedPath := requestURL.EscapedPath()
	var remainder string
	for _, prefix := range []string{"/v1beta/models/", "/v1/models/"} {
		if strings.HasPrefix(escapedPath, prefix) {
			remainder = strings.TrimPrefix(escapedPath, prefix)
			break
		}
	}
	if remainder == "" {
		return "", false, invalid(core.ProtocolGenerateContent, "$.url.path", "invalid Gemini generateContent path")
	}
	stream := false
	var encodedModel string
	switch {
	case strings.HasSuffix(remainder, ":streamGenerateContent"):
		stream = true
		encodedModel = strings.TrimSuffix(remainder, ":streamGenerateContent")
	case strings.HasSuffix(remainder, ":generateContent"):
		encodedModel = strings.TrimSuffix(remainder, ":generateContent")
	default:
		return "", false, invalid(core.ProtocolGenerateContent, "$.url.path", "invalid Gemini generateContent method")
	}
	model, err := url.PathUnescape(encodedModel)
	if err != nil || model == "" || containsControl(model) {
		return "", false, invalid(core.ProtocolGenerateContent, "$.url.path", "invalid Gemini model")
	}
	return model, stream, nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
