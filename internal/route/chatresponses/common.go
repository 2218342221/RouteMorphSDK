package chatresponses

import (
	"bytes"
	"encoding/json"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"
)

type Protocol = core.Protocol
type routeSpec = core.RouteSpec
type conversionOptions = core.ConversionOptions
type conversionResult = core.ConversionResult
type responseStreamConverter = core.ResponseStream
type streamFrame = core.Frame
type lossPolicy = core.LossPolicy

const (
	ProtocolChat            = core.ProtocolChat
	ProtocolResponses       = core.ProtocolResponses
	ProtocolMessages        = core.ProtocolMessages
	ProtocolGenerateContent = core.ProtocolGenerateContent
	rejectSemanticLoss      = core.RejectSemanticLoss
)

var ErrInvalidPlan = core.ErrInvalidPlan

func New(spec core.RouteSpec) core.Route {
	switch {
	case spec.From == core.ProtocolChat && spec.To == core.ProtocolResponses:
		return &chatToResponsesConverter{spec: spec}
	case spec.From == core.ProtocolResponses && spec.To == core.ProtocolChat:
		return &responsesToChatConverter{spec: spec}
	default:
		return nil
	}
}

func invalid(protocol Protocol, path, format string, args ...any) error {
	return core.Invalid(protocol, path, format, args...)
}
func unsupported(protocol Protocol, path, format string, args ...any) error {
	return core.Unsupported(protocol, path, format, args...)
}
func upstreamResponseError(protocol Protocol, path, format string, args ...any) error {
	return core.UpstreamResponseError(protocol, path, format, args...)
}

var (
	rawObject                        = routekit.RawObject
	jsonValuePresent                 = routekit.ValuePresent
	rawJSONValuePresent              = routekit.ValuePresent
	mustJSONString                   = routekit.MustJSONString
	resolveExchangeStream            = routekit.ResolveExchangeStream
	validateResponsesContentArray    = routekit.ValidateResponsesContentArray
	validateResponsesInputItems      = routekit.ValidateResponsesInputItems
	validateResponsesOutputItems     = routekit.ValidateResponsesOutputItems
	validateResponsesToolOutput      = routekit.ValidateResponsesToolOutput
	validateResponsesToolsShape      = routekit.ValidateResponsesTools
	validateChatMessageContentFields = routekit.ValidateChatMessageContentFields
)

func joinText(parts []portablePart) string {
	var result string
	for _, part := range parts {
		if part.Kind == partText {
			result += part.Text
		}
	}
	return result
}

func validateOpenAIReasoningEffort(protocol Protocol, path, effort string) error {
	switch effort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return nil
	default:
		return invalid(protocol, path, "unsupported reasoning effort %q", effort)
	}
}

func validatePromptCacheBreakpoint(protocol Protocol, path string, raw json.RawMessage) error {
	if !nonNullJSON(raw) {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return invalid(protocol, path, "prompt_cache_breakpoint must be an object")
	}
	for name := range fields {
		if name != "mode" {
			return unsupported(protocol, path+"."+name, "prompt cache breakpoint field has no portable equivalent")
		}
	}
	var mode string
	if err := json.Unmarshal(fields["mode"], &mode); err != nil || mode != "explicit" {
		return invalid(protocol, path+".mode", "must be %q", "explicit")
	}
	return nil
}

func nonNullJSON(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func rejectUnknownObjectFields(protocol Protocol, path string, raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, invalid(protocol, path, "must be an object")
	}
	known := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		known[name] = struct{}{}
	}
	for name, value := range fields {
		if _, ok := known[name]; !ok && nonNullJSON(value) {
			return nil, unsupported(protocol, path+"."+name, "field has no portable equivalent")
		}
	}
	return fields, nil
}
