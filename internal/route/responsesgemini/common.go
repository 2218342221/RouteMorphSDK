package responsesgemini

import (
	"strings"

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
	ProtocolResponses       = core.ProtocolResponses
	ProtocolGenerateContent = core.ProtocolGenerateContent
	ProtocolChat            = core.ProtocolChat
	ProtocolMessages        = core.ProtocolMessages
	rejectSemanticLoss      = core.RejectSemanticLoss
)

var ErrInvalidPlan = core.ErrInvalidPlan

func New(spec core.RouteSpec) core.Route {
	switch {
	case spec.From == core.ProtocolResponses && spec.To == core.ProtocolGenerateContent:
		return &responsesToGeminiConverter{spec: spec}
	case spec.From == core.ProtocolGenerateContent && spec.To == core.ProtocolResponses:
		return &geminiToResponsesConverter{spec: spec}
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
	rawObject             = routekit.RawObject
	rawJSONValuePresent   = routekit.ValuePresent
	jsonValuePresent      = routekit.NonNullValue
	mustJSONString        = routekit.MustJSONString
	resolveExchangeStream = routekit.ResolveExchangeStream
)

func consumeStreamPrefix(emitted, complete, path string) (suffix, remaining string, err error) {
	if emitted == "" {
		return complete, "", nil
	}
	if strings.HasPrefix(emitted, complete) {
		return "", strings.TrimPrefix(emitted, complete), nil
	}
	if strings.HasPrefix(complete, emitted) {
		return strings.TrimPrefix(complete, emitted), "", nil
	}
	return "", emitted, upstreamResponseError(ProtocolResponses, path, "terminal output does not match streamed deltas")
}

func joinText(parts []portablePart) string {
	var result string
	for _, part := range parts {
		if part.Kind == partText {
			result += part.Text
		}
	}
	return result
}
