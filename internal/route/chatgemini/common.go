package chatgemini

import (
	"context"
	"encoding/json"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"
)

type Protocol = core.Protocol
type routeSpec = core.RouteSpec
type conversionOptions = core.ConversionOptions
type conversionResult = core.ConversionResult
type responseStreamConverter = core.ResponseStream
type lossPolicy = core.LossPolicy

const (
	ProtocolChat            = core.ProtocolChat
	ProtocolGenerateContent = core.ProtocolGenerateContent
	ProtocolResponses       = core.ProtocolResponses
	ProtocolMessages        = core.ProtocolMessages
	rejectSemanticLoss      = core.RejectSemanticLoss
)

type BufferedFactory func(core.RouteSpec, core.ConversionOptions, func(context.Context, []byte, core.ConversionOptions) (core.ConversionResult, error)) core.ResponseStream

func New(spec core.RouteSpec, buffered BufferedFactory) core.Route {
	if buffered == nil {
		return nil
	}
	switch {
	case spec.From == core.ProtocolChat && spec.To == core.ProtocolGenerateContent:
		return &chatToGeminiConverter{spec: spec, buffered: buffered}
	case spec.From == core.ProtocolGenerateContent && spec.To == core.ProtocolChat:
		return &geminiToChatConverter{spec: spec, buffered: buffered}
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

func joinText(parts []portablePart) string {
	var result string
	for _, part := range parts {
		if part.Kind == partText {
			result += part.Text
		}
	}
	return result
}

func validateChatStreamOptions(input []byte) error {
	object, err := rawObject(ProtocolChat, input)
	if err != nil || !rawJSONValuePresent(object["stream_options"]) {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(object["stream_options"], &fields); err != nil {
		return invalid(ProtocolChat, "$.stream_options", "stream_options must be an object")
	}
	for name, value := range fields {
		if name != "include_usage" {
			return unsupported(ProtocolChat, "$.stream_options."+name, "stream option has no portable equivalent")
		}
		var enabled bool
		if err := json.Unmarshal(value, &enabled); err != nil {
			return invalid(ProtocolChat, "$.stream_options.include_usage", "must be a boolean")
		}
	}
	return nil
}
