package stream

import core "github.com/2218342221/RouteMorphSDK/internal/core"

type Protocol = core.Protocol
type streamFrame = core.Frame
type lossPolicy = core.LossPolicy

const (
	ProtocolChat            = core.ProtocolChat
	ProtocolResponses       = core.ProtocolResponses
	ProtocolMessages        = core.ProtocolMessages
	ProtocolGenerateContent = core.ProtocolGenerateContent
	rejectSemanticLoss      = core.RejectSemanticLoss
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
