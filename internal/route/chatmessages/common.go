package chatmessages

import (
	"context"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	routekit "github.com/2218342221/RouteMorphSDK/internal/routekit"
)

type Protocol = core.Protocol
type Diagnostic = core.Diagnostic
type routeSpec = core.RouteSpec
type conversionOptions = core.ConversionOptions
type conversionResult = core.ConversionResult
type responseStreamConverter = core.ResponseStream

const (
	ProtocolChat       = core.ProtocolChat
	ProtocolMessages   = core.ProtocolMessages
	rejectSemanticLoss = core.RejectSemanticLoss
)

type lossPolicy = core.LossPolicy

type BufferedFactory func(core.RouteSpec, core.ConversionOptions, func(context.Context, []byte, core.ConversionOptions) (core.ConversionResult, error)) core.ResponseStream

func New(spec core.RouteSpec, buffered BufferedFactory) core.Route {
	if buffered == nil {
		return nil
	}
	switch {
	case spec.From == core.ProtocolChat && spec.To == core.ProtocolMessages:
		return &chatToMessagesConverter{spec: spec, buffered: buffered}
	case spec.From == core.ProtocolMessages && spec.To == core.ProtocolChat:
		return &messagesToChatConverter{spec: spec, buffered: buffered}
	default:
		return nil
	}
}

type semanticRole = core.Role

const (
	roleSystem    = core.RoleSystem
	roleDeveloper = core.RoleDeveloper
	roleUser      = core.RoleUser
	roleAssistant = core.RoleAssistant
	roleTool      = core.RoleTool
)

const (
	partText       = core.PartText
	partImage      = core.PartImage
	partAudio      = core.PartAudio
	partFile       = core.PartFile
	partToolCall   = core.PartToolCall
	partToolResult = core.PartToolResult
	partReasoning  = core.PartReasoning
	partRefusal    = core.PartRefusal
)

type portableMessage = core.Message
type portablePart = core.Part
type portableMedia = core.Media
type portableToolCall = core.ToolCall
type portableToolResult = core.ToolResult
type toolChoiceMode = core.ToolChoiceMode

const (
	toolChoiceAuto     = core.ToolChoiceAuto
	toolChoiceNone     = core.ToolChoiceNone
	toolChoiceRequired = core.ToolChoiceRequired
	toolChoiceNamed    = core.ToolChoiceNamed
)

type toolChoice = core.ToolChoice
type jsonSchemaFormat = core.JSONSchemaFormat
type finishReason = core.FinishReason

const (
	finishStop          = core.FinishStop
	finishLength        = core.FinishLength
	finishToolCalls     = core.FinishToolCalls
	finishContentFilter = core.FinishContentFilter
	finishError         = core.FinishError
)

func invalid(protocol Protocol, path, format string, args ...any) error {
	return core.Invalid(protocol, path, format, args...)
}
func unsupported(protocol Protocol, path, format string, args ...any) error {
	return core.Unsupported(protocol, path, format, args...)
}
func upstreamResponseError(protocol Protocol, path, format string, args ...any) error {
	return core.UpstreamResponseError(protocol, path, format, args...)
}

var resolveExchangeStream = routekit.ResolveExchangeStream
