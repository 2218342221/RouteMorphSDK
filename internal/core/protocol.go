// Package core contains the small immutable value vocabulary shared by the
// facade, relay, route, and stream layers. It deliberately contains no request
// or response aggregate representation.
package core

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrRouteNotFound = errors.New("conversion route not found")
	ErrInvalidPlan   = errors.New("invalid conversion plan")
)

type Protocol string

const (
	ProtocolChat            Protocol = "chat"
	ProtocolResponses       Protocol = "responses"
	ProtocolMessages        Protocol = "messages"
	ProtocolGenerateContent Protocol = "generateContent"
)

func ParseProtocol(value string) (Protocol, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "chat", "chatcompletion", "chatcompletions", "openai_chat":
		return ProtocolChat, nil
	case "response", "responses", "openai_response", "openai_responses":
		return ProtocolResponses, nil
	case "message", "messages", "anthropic", "anthropic_messages":
		return ProtocolMessages, nil
	case "generatecontent", "generate_content", "gemini":
		return ProtocolGenerateContent, nil
	default:
		return "", fmt.Errorf("unsupported protocol %q", value)
	}
}

func (p Protocol) Valid() bool {
	switch p {
	case ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent:
		return true
	default:
		return false
	}
}

type RouteMode string

const (
	RouteModeNative      RouteMode = "native"
	RouteModeIncremental RouteMode = "incremental"
	RouteModeBuffered    RouteMode = "buffered"
)

type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
}
