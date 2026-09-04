package builtin

import (
	"sync"

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

var (
	defaultOnce    sync.Once
	defaultCatalog *Catalog
	defaultError   error
)

// Default returns the shared immutable built-in catalog. Route implementations
// keep no request state, so constructing the same 12-route catalog per Adapter is
// unnecessary.
func Default() (*Catalog, error) {
	defaultOnce.Do(func() {
		defaultCatalog, defaultError = NewDefault()
	})
	return defaultCatalog, defaultError
}

// NewDefault constructs the complete immutable catalog shipped by RouteMorphSDK.
// Every ordered protocol pair has one direct converter; no graph search or
// implicit intermediate representation is involved.
func NewDefault() (*Catalog, error) {
	registrations := defaultRouteRegistrations()
	routes := make([]core.Route, 0, len(registrations))
	for _, registration := range registrations {
		converter := registration.factory(registration.spec)
		if converter == nil {
			return nil, core.ErrInvalidPlan
		}
		routes = append(routes, converter)
	}
	return New(routes, func(protocol core.Protocol) (core.Codec, error) { return codec.For(protocol) })
}

type routeFactory func(core.RouteSpec) core.Route

type routeRegistration struct {
	spec    core.RouteSpec
	factory routeFactory
}

// defaultRouteRegistrations binds each route contract to its implementation.
// Catalog validation independently derives the complete directed matrix, so a
// missing or mistyped registration cannot validate itself.
func defaultRouteRegistrations() []routeRegistration {
	bufferedResponsesMessages := func(spec core.RouteSpec) core.Route {
		return responsesmessages.New(spec, streamx.NewBufferedRoute)
	}
	bufferedChatGemini := func(spec core.RouteSpec) core.Route {
		return chatgemini.New(spec, streamx.NewBufferedRoute)
	}
	bufferedMessagesGemini := func(spec core.RouteSpec) core.Route {
		return messagesgemini.New(spec, streamx.NewBufferedRoute)
	}
	bufferedChatMessages := func(spec core.RouteSpec) core.Route {
		return chatmessages.New(spec, streamx.NewBufferedRoute)
	}
	return []routeRegistration{
		{spec: core.RouteSpec{ID: "chat_to_responses", From: core.ProtocolChat, To: core.ProtocolResponses, StreamMode: core.RouteModeIncremental}, factory: chatresponses.New},
		{spec: core.RouteSpec{ID: "responses_to_chat", From: core.ProtocolResponses, To: core.ProtocolChat, StreamMode: core.RouteModeIncremental}, factory: chatresponses.New},
		{spec: core.RouteSpec{ID: "responses_to_messages", From: core.ProtocolResponses, To: core.ProtocolMessages, StreamMode: core.RouteModeBuffered}, factory: bufferedResponsesMessages},
		{spec: core.RouteSpec{ID: "messages_to_responses", From: core.ProtocolMessages, To: core.ProtocolResponses, StreamMode: core.RouteModeIncremental}, factory: bufferedResponsesMessages},
		{spec: core.RouteSpec{ID: "responses_to_gemini", From: core.ProtocolResponses, To: core.ProtocolGenerateContent, StreamMode: core.RouteModeIncremental}, factory: responsesgemini.New},
		{spec: core.RouteSpec{ID: "gemini_to_responses", From: core.ProtocolGenerateContent, To: core.ProtocolResponses, StreamMode: core.RouteModeIncremental}, factory: responsesgemini.New},
		{spec: core.RouteSpec{ID: "chat_to_gemini", From: core.ProtocolChat, To: core.ProtocolGenerateContent, StreamMode: core.RouteModeBuffered}, factory: bufferedChatGemini},
		{spec: core.RouteSpec{ID: "gemini_to_chat", From: core.ProtocolGenerateContent, To: core.ProtocolChat, StreamMode: core.RouteModeBuffered}, factory: bufferedChatGemini},
		{spec: core.RouteSpec{ID: "messages_to_gemini", From: core.ProtocolMessages, To: core.ProtocolGenerateContent, StreamMode: core.RouteModeBuffered}, factory: bufferedMessagesGemini},
		{spec: core.RouteSpec{ID: "gemini_to_messages", From: core.ProtocolGenerateContent, To: core.ProtocolMessages, StreamMode: core.RouteModeBuffered}, factory: bufferedMessagesGemini},
		{spec: core.RouteSpec{ID: "chat_to_messages", From: core.ProtocolChat, To: core.ProtocolMessages, StreamMode: core.RouteModeBuffered}, factory: bufferedChatMessages},
		{spec: core.RouteSpec{ID: "messages_to_chat", From: core.ProtocolMessages, To: core.ProtocolChat, StreamMode: core.RouteModeBuffered}, factory: bufferedChatMessages},
	}
}

func defaultRouteSpecs() []core.RouteSpec {
	registrations := defaultRouteRegistrations()
	specs := make([]core.RouteSpec, len(registrations))
	for index, registration := range registrations {
		specs[index] = registration.spec
	}
	return specs
}

func newDefaultRoute(spec core.RouteSpec) core.Route {
	for _, registration := range defaultRouteRegistrations() {
		if registration.spec == spec {
			return registration.factory(spec)
		}
	}
	return nil
}
