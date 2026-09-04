package responsesmessages

import (
	"context"
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
	ProtocolMessages        = core.ProtocolMessages
	ProtocolChat            = core.ProtocolChat
	ProtocolGenerateContent = core.ProtocolGenerateContent
	rejectSemanticLoss      = core.RejectSemanticLoss
)

var ErrInvalidPlan = core.ErrInvalidPlan

type BufferedFactory func(core.RouteSpec, core.ConversionOptions, func(context.Context, []byte, core.ConversionOptions) (core.ConversionResult, error)) core.ResponseStream

func New(spec core.RouteSpec, buffered BufferedFactory) core.Route {
	switch {
	case spec.From == core.ProtocolResponses && spec.To == core.ProtocolMessages && buffered != nil:
		return &responsesToMessagesConverter{spec: spec, buffered: buffered}
	case spec.From == core.ProtocolMessages && spec.To == core.ProtocolResponses:
		return &messagesToResponsesConverter{spec: spec}
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
	jsonValuePresent      = routekit.ValuePresent
	rawJSONValuePresent   = routekit.ValuePresent
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
func remainingStreamText(emitted, complete, path string) (string, error) {
	if emitted == "" {
		return complete, nil
	}
	if !strings.HasPrefix(complete, emitted) {
		return "", upstreamResponseError(ProtocolResponses, path, "terminal output does not match streamed deltas")
	}
	return strings.TrimPrefix(complete, emitted), nil
}
