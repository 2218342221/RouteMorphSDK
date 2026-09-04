package chatresponses

import (
	core "github.com/2218342221/RouteMorphSDK/internal/core"
	routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"
)

type Protocol = core.Protocol
type routeSpec = core.RouteSpec
type conversionOptions = core.ConversionOptions
type conversionResult = core.ConversionResult
type responseStreamConverter = core.ResponseStream
type streamFrame = core.Frame

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
	rawObject             = routekit.RawObject
	jsonValuePresent      = routekit.ValuePresent
	rawJSONValuePresent   = routekit.ValuePresent
	mustJSONString        = routekit.MustJSONString
	resolveExchangeStream = routekit.ResolveExchangeStream
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
