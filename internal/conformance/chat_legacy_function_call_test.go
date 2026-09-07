package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDirectChatDeprecatedFunctionCallFailsClosed(t *testing.T) {
	request := []byte(`{"model":"m","messages":[{"role":"assistant","content":null,"function_call":{"name":"old","arguments":"{}"}}]}`)
	response := []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"function_call":{"name":"old","arguments":"{}"}},"finish_reason":"function_call"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)

	for _, test := range []struct {
		name          string
		requestRoute  routeConverter
		responseRoute routeConverter
		requestPath   string
		responsePath  string
	}{
		{
			name:          "gemini",
			requestRoute:  newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent}),
			responseRoute: newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat}),
			requestPath:   "$.messages[0].function_call",
			responsePath:  "$.choices[0].message.function_call",
		},
		{
			name:          "messages",
			requestRoute:  newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages}),
			responseRoute: newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat}),
			requestPath:   "$.messages[0].function_call",
			responsePath:  "$.choices[0].message.function_call",
		},
	} {
		t.Run(test.name+" request", func(t *testing.T) {
			_, err := test.requestRoute.ToUpstreamRequest(context.Background(), request, conversionOptions{LossPolicy: allowDocumentedLoss})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.requestPath) {
				t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.requestPath)
			}
		})
		t.Run(test.name+" response", func(t *testing.T) {
			_, err := test.responseRoute.ToClientResponse(context.Background(), response, conversionOptions{LossPolicy: allowDocumentedLoss})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.responsePath) {
				t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.responsePath)
			}
		})
	}
}

func TestDirectChatToolMessagesRejectAssistantOnlyFields(t *testing.T) {
	for _, route := range []struct {
		name      string
		converter routeConverter
	}{
		{"responses", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})},
		{"gemini", newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})},
		{"messages", newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})},
	} {
		for _, field := range []struct {
			name string
			json string
			path string
			want error
		}{
			{"tool_calls", `"tool_calls":[{"id":"call_2","type":"function","function":{"name":"lookup","arguments":"{}"}}]`, "$.messages[1].tool_calls", ErrInvalidPayload},
			{"refusal", `"refusal":"declined"`, "$.messages[1].refusal", ErrInvalidPayload},
			{"reasoning_content", `"reasoning_content":"hidden"`, "$.messages[1].reasoning_content", ErrInvalidPayload},
			{"function_call", `"function_call":{"name":"lookup","arguments":"{}"}`, "$.messages[1].function_call", ErrUnsupported},
		} {
			t.Run(route.name+"/"+field.name, func(t *testing.T) {
				body := []byte(`{"model":"m","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"ok",` + field.json + `}]}`)
				_, err := route.converter.ToUpstreamRequest(context.Background(), body, conversionOptions{LossPolicy: allowDocumentedLoss})
				if !errors.Is(err, field.want) || !strings.Contains(err.Error(), field.path) {
					t.Fatalf("error = %v, want %v at %s", err, field.want, field.path)
				}
			})
		}
	}
}

func TestDirectChatRejectsRoleInvalidToolMetadata(t *testing.T) {
	routes := []struct {
		name      string
		converter routeConverter
	}{
		{"responses", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})},
		{"gemini", newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})},
		{"messages", newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})},
	}
	tests := []struct {
		name string
		body string
		path string
	}{
		{
			name: "user tool calls",
			body: `{"model":"m","messages":[{"role":"user","content":"hi","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}`,
			path: "$.messages[0].tool_calls",
		},
		{
			name: "assistant tool call id",
			body: `{"model":"m","messages":[{"role":"assistant","content":"hi","tool_call_id":"call_1"}]}`,
			path: "$.messages[0].tool_call_id",
		},
	}
	for _, route := range routes {
		for _, test := range tests {
			t.Run(route.name+"/"+test.name, func(t *testing.T) {
				_, err := route.converter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{LossPolicy: allowDocumentedLoss})
				if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), test.path) {
					t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.path)
				}
			})
		}
	}
}
