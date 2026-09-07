package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestChatGeminiRequiredAllowedToolsRoundTrip(t *testing.T) {
	chatToGemini := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
	result, err := chatToGemini.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"client",
		"messages":[{"role":"user","content":"use one tool"}],
		"tools":[
			{"type":"function","function":{"name":"alpha","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"beta","parameters":{"type":"object"}}},
			{"type":"function","function":{"name":"gamma","parameters":{"type":"object"}}}
		],
		"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[
			{"type":"function","function":{"name":"alpha"}},
			{"type":"function","function":{"name":"beta"}}
		]}}
	}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gemini"}})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiRequest
	if err := json.Unmarshal(result.Body, &gemini); err != nil {
		t.Fatal(err)
	}
	if gemini.ToolConfig == nil || gemini.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Fatalf("tool config = %#v", gemini.ToolConfig)
	}
	if got := gemini.ToolConfig.FunctionCallingConfig.AllowedFunctionNames; len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("allowed names = %#v", got)
	}

	geminiToChat := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	result, err = geminiToChat.ToUpstreamRequest(context.Background(), []byte(`{
		"contents":[{"role":"user","parts":[{"text":"use one tool"}]}],
		"tools":[{"functionDeclarations":[
			{"name":"alpha","parameters":{"type":"OBJECT"}},
			{"name":"beta","parameters":{"type":"OBJECT"}},
			{"name":"gamma","parameters":{"type":"OBJECT"}}
		]}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["alpha","beta"]}}
	}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(result.Body, &chat); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"alpha"}},{"type":"function","function":{"name":"beta"}}]}}`)
	if !directJSONObjectsEqual(chat.ToolChoice, want) {
		t.Fatalf("tool_choice = %s", chat.ToolChoice)
	}
}

func TestResponsesGeminiRequiredAllowedToolsRoundTrip(t *testing.T) {
	responsesToGemini := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	result, err := responsesToGemini.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"client",
		"input":"use one tool",
		"tools":[
			{"type":"function","name":"alpha","parameters":{"type":"object"},"strict":false},
			{"type":"function","name":"beta","parameters":{"type":"object"},"strict":false},
			{"type":"function","name":"gamma","parameters":{"type":"object"},"strict":false}
		],
		"tool_choice":{"type":"allowed_tools","mode":"required","tools":[
			{"type":"function","name":"alpha"},
			{"type":"function","name":"beta"}
		]}
	}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gemini"}, LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiRequest
	if err := json.Unmarshal(result.Body, &gemini); err != nil {
		t.Fatal(err)
	}
	if gemini.ToolConfig == nil || gemini.ToolConfig.FunctionCallingConfig.Mode != "ANY" {
		t.Fatalf("tool config = %#v", gemini.ToolConfig)
	}
	if got := gemini.ToolConfig.FunctionCallingConfig.AllowedFunctionNames; len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("allowed names = %#v", got)
	}

	geminiToResponses := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	result, err = geminiToResponses.ToUpstreamRequest(context.Background(), []byte(`{
		"contents":[{"role":"user","parts":[{"text":"use one tool"}]}],
		"tools":[{"functionDeclarations":[
			{"name":"alpha","parameters":{"type":"OBJECT"}},
			{"name":"beta","parameters":{"type":"OBJECT"}},
			{"name":"gamma","parameters":{"type":"OBJECT"}}
		]}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["alpha","beta"]}}
	}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
	if err != nil {
		t.Fatal(err)
	}
	var responses responsesRequest
	if err := json.Unmarshal(result.Body, &responses); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"alpha"},{"type":"function","name":"beta"}]}`)
	if !directJSONObjectsEqual(responses.ToolChoice, want) {
		t.Fatalf("tool_choice = %s", responses.ToolChoice)
	}
}

func TestGeminiAnyWithoutDeclaredFunctionsFailsClosed(t *testing.T) {
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"use a tool"}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}
	}`)
	tests := []struct {
		name      string
		converter routeConverter
	}{
		{name: "chat", converter: newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})},
		{name: "messages", converter: newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})},
		{name: "responses", converter: newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "upstream"}})
			if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "$.toolConfig.functionCallingConfig.mode") {
				t.Fatalf("error = %v, want ErrInvalidPayload at function-calling mode", err)
			}
		})
	}
}
