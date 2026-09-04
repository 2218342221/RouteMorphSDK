package conformance

import (
	"context"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
	chatgemini "github.com/2218342221/RouteMorphSDK/internal/route/chatgemini"
	chatmessages "github.com/2218342221/RouteMorphSDK/internal/route/chatmessages"
	chatresponses "github.com/2218342221/RouteMorphSDK/internal/route/chatresponses"
	messagesgemini "github.com/2218342221/RouteMorphSDK/internal/route/messagesgemini"
	responsesgemini "github.com/2218342221/RouteMorphSDK/internal/route/responsesgemini"
	responsesmessages "github.com/2218342221/RouteMorphSDK/internal/route/responsesmessages"
)

func TestRouteConstructorsRejectMismatchedProtocolPairs(t *testing.T) {
	buffered := func(core.RouteSpec, core.ConversionOptions, func(context.Context, []byte, core.ConversionOptions) (core.ConversionResult, error)) core.ResponseStream {
		return nil
	}
	tests := []struct {
		name  string
		route core.Route
	}{
		{"chat-responses", chatresponses.New(core.RouteSpec{From: core.ProtocolChat, To: core.ProtocolMessages})},
		{"responses-gemini", responsesgemini.New(core.RouteSpec{From: core.ProtocolResponses, To: core.ProtocolChat})},
		{"chat-messages", chatmessages.New(core.RouteSpec{From: core.ProtocolChat, To: core.ProtocolResponses}, buffered)},
		{"chat-gemini", chatgemini.New(core.RouteSpec{From: core.ProtocolChat, To: core.ProtocolMessages}, buffered)},
		{"messages-gemini", messagesgemini.New(core.RouteSpec{From: core.ProtocolMessages, To: core.ProtocolChat}, buffered)},
		{"responses-messages", responsesmessages.New(core.RouteSpec{From: core.ProtocolResponses, To: core.ProtocolChat}, buffered)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.route != nil {
				t.Fatalf("mismatched route was accepted: %#v", test.route.Specification())
			}
		})
	}
}

func TestBufferedRouteConstructorsRejectNilFactory(t *testing.T) {
	tests := []struct {
		name  string
		route core.Route
	}{
		{"chat-messages", chatmessages.New(core.RouteSpec{From: core.ProtocolChat, To: core.ProtocolMessages}, nil)},
		{"chat-gemini", chatgemini.New(core.RouteSpec{From: core.ProtocolChat, To: core.ProtocolGenerateContent}, nil)},
		{"messages-gemini", messagesgemini.New(core.RouteSpec{From: core.ProtocolMessages, To: core.ProtocolGenerateContent}, nil)},
		{"responses-messages", responsesmessages.New(core.RouteSpec{From: core.ProtocolResponses, To: core.ProtocolMessages}, nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.route != nil {
				t.Fatal("route accepted a nil buffered-stream factory")
			}
		})
	}

	if route := responsesmessages.New(core.RouteSpec{From: core.ProtocolMessages, To: core.ProtocolResponses}, nil); route == nil {
		t.Fatal("incremental Messages-to-Responses route unexpectedly requires a buffered-stream factory")
	}
}
