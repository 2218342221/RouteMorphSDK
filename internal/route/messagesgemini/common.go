package messagesgemini

import (
	"context"

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
	ProtocolMessages        = core.ProtocolMessages
	ProtocolGenerateContent = core.ProtocolGenerateContent
	ProtocolChat            = core.ProtocolChat
	ProtocolResponses       = core.ProtocolResponses
	rejectSemanticLoss      = core.RejectSemanticLoss
)

type BufferedFactory func(core.RouteSpec, core.ConversionOptions, func(context.Context, []byte, core.ConversionOptions) (core.ConversionResult, error)) core.ResponseStream

func New(spec core.RouteSpec, buffered BufferedFactory) core.Route {
	if buffered == nil {
		return nil
	}
	switch {
	case spec.From == core.ProtocolMessages && spec.To == core.ProtocolGenerateContent:
		return &messagesToGeminiConverter{spec: spec, buffered: buffered}
	case spec.From == core.ProtocolGenerateContent && spec.To == core.ProtocolMessages:
		return &geminiToMessagesConverter{spec: spec, buffered: buffered}
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
	jsonValuePresent      = routekit.NonNullValue
	resolveExchangeStream = routekit.ResolveExchangeStream
)
