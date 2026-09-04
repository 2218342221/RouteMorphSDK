package routemorph

import core "github.com/2218342221/RouteMorphSDK/internal/core"

// Protocol identifies one of the HTTP payload families supported by RouteMorphSDK.
type Protocol string

// RouteMode describes how RouteMorphSDK converts a streaming response.
type RouteMode string

const (
	// ProtocolChat is the OpenAI Chat Completions protocol.
	ProtocolChat Protocol = "chat"
	// ProtocolResponses is the OpenAI Responses protocol.
	ProtocolResponses Protocol = "responses"
	// ProtocolMessages is the Anthropic Messages protocol.
	ProtocolMessages Protocol = "messages"
	// ProtocolGenerateContent is the Gemini generateContent protocol.
	ProtocolGenerateContent Protocol = "generateContent"

	// RouteModeNative forwards a response without cross-protocol conversion.
	RouteModeNative RouteMode = "native"
	// RouteModeIncremental converts a streaming response frame by frame.
	RouteModeIncremental RouteMode = "incremental"
	// RouteModeBuffered buffers a stream before converting the complete response.
	RouteModeBuffered RouteMode = "buffered"
)

// Valid reports whether p identifies a supported protocol.
func (p Protocol) Valid() bool {
	return core.Protocol(p).Valid()
}

// ParseProtocol accepts the canonical names and the common singular aliases
// used by provider configuration files.
func ParseProtocol(value string) (Protocol, error) {
	protocol, err := core.ParseProtocol(value)
	return Protocol(protocol), err
}

func toCoreProtocol(protocol Protocol) core.Protocol { return core.Protocol(protocol) }
