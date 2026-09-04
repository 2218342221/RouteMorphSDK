package conformance

import (
	"context"
	"io"

	builtin "github.com/2218342221/RouteMorphSDK/internal/builtin"
	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
	chatgemini "github.com/2218342221/RouteMorphSDK/internal/route/chatgemini"
	chatmessages "github.com/2218342221/RouteMorphSDK/internal/route/chatmessages"
	chatresponses "github.com/2218342221/RouteMorphSDK/internal/route/chatresponses"
	messagesgemini "github.com/2218342221/RouteMorphSDK/internal/route/messagesgemini"
	responsesgemini "github.com/2218342221/RouteMorphSDK/internal/route/responsesgemini"
	responsesmessages "github.com/2218342221/RouteMorphSDK/internal/route/responsesmessages"
	streamx "github.com/2218342221/RouteMorphSDK/internal/stream"
)

type Protocol = core.Protocol
type RouteMode = core.RouteMode
type Diagnostic = core.Diagnostic
type ProtocolError = core.ProtocolError
type RequestInfo = core.RequestInfo
type routePlan = core.RoutePlan
type routeSpec = core.RouteSpec
type routeConverter = core.Route
type conversionOptions = core.ConversionOptions
type conversionResult = core.ConversionResult
type exchangeMetadata = core.ExchangeMetadata
type responseStreamConverter = core.ResponseStream
type streamOptions = core.StreamOptions
type streamFrame = core.Frame
type streamDecoder = core.StreamDecoder
type lossPolicy = core.LossPolicy
type requestHint = core.RequestHint
type requestPatch = core.RequestPatch
type protocolCodec = codec.Codec

const (
	ProtocolChat            = core.ProtocolChat
	ProtocolResponses       = core.ProtocolResponses
	ProtocolMessages        = core.ProtocolMessages
	ProtocolGenerateContent = core.ProtocolGenerateContent
	RouteModeNative         = core.RouteModeNative
	RouteModeIncremental    = core.RouteModeIncremental
	RouteModeBuffered       = core.RouteModeBuffered
	rejectSemanticLoss      = core.RejectSemanticLoss
	allowDocumentedLoss     = core.AllowDocumentedLoss
)

var (
	ErrInvalidPayload   = core.ErrInvalidPayload
	ErrUnsupported      = core.ErrUnsupported
	ErrUpstreamResponse = core.ErrUpstreamResponse
	ErrInvalidPlan      = core.ErrInvalidPlan
)

type router = builtin.Catalog

func newBuiltinRouter() (*router, error) {
	return builtin.NewDefault()
}

func newProtocolCodec(protocol Protocol) *protocolCodec { return codec.New(protocol) }

func newSSEDecoder(reader io.Reader, options streamOptions) streamDecoder {
	decoder, _ := codec.New(ProtocolChat).NewStreamDecoder(reader, options)
	return decoder
}

func newBoundedFrameStream(protocol Protocol, maxBytes int64, finalize func(context.Context, []streamFrame) ([]streamFrame, []Diagnostic, error)) responseStreamConverter {
	return streamx.NewBoundedFrameStream(protocol, maxBytes, finalize)
}

func newBufferedRouteStream(spec routeSpec, options conversionOptions, mapper func(context.Context, []byte, conversionOptions) (conversionResult, error)) responseStreamConverter {
	return streamx.NewBufferedRoute(spec, options, mapper)
}

func newChatResponsesRoute(spec routeSpec) routeConverter   { return chatresponses.New(spec) }
func newResponsesGeminiRoute(spec routeSpec) routeConverter { return responsesgemini.New(spec) }
func newChatMessagesRoute(spec routeSpec) routeConverter {
	return chatmessages.New(spec, streamx.NewBufferedRoute)
}
func newChatGeminiRoute(spec routeSpec) routeConverter {
	return chatgemini.New(spec, streamx.NewBufferedRoute)
}
func newResponsesMessagesRoute(spec routeSpec) routeConverter {
	return responsesmessages.New(spec, streamx.NewBufferedRoute)
}
func newMessagesGeminiRoute(spec routeSpec) routeConverter {
	return messagesgemini.New(spec, streamx.NewBufferedRoute)
}

func invalid(protocol Protocol, path, format string, args ...any) error {
	return core.Invalid(protocol, path, format, args...)
}
