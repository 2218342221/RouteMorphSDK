package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMessagesGeminiToolResultTextErrorAndEmptyRoundTrip(t *testing.T) {
	toGemini := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	source := []byte(`{
		"model":"claude","max_tokens":64,
		"messages":[
			{"role":"assistant","content":[
				{"type":"tool_use","id":"call_text","name":"text","input":{}},
				{"type":"tool_use","id":"call_empty","name":"empty","input":{}},
				{"type":"tool_use","id":"call_error","name":"failure","input":{}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"call_text","content":"{\"output\":\"x\"}"},
				{"type":"tool_result","tool_use_id":"call_empty"},
				{"type":"tool_result","tool_use_id":"call_error","content":"boom","is_error":true}
			]}
		]
	}`)
	converted, err := toGemini.ToUpstreamRequest(context.Background(), source, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiRequest
	if err := json.Unmarshal(converted.Body, &gemini); err != nil {
		t.Fatal(err)
	}
	if len(gemini.Contents) != 2 || len(gemini.Contents[1].Parts) != 3 {
		t.Fatalf("converted contents = %#v", gemini.Contents)
	}
	wantResponses := []string{`{"output":"{\"output\":\"x\"}"}`, `{}`, `{"error":"boom"}`}
	for index, want := range wantResponses {
		response := gemini.Contents[1].Parts[index].FunctionResponse
		if response == nil || string(response.Response) != want {
			t.Fatalf("functionResponse[%d] = %#v, want %s", index, response, want)
		}
	}

	toMessages := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	roundTrip, err := toMessages.ToUpstreamRequest(context.Background(), converted.Body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	var messages messagesRequest
	if err := json.Unmarshal(roundTrip.Body, &messages); err != nil || len(messages.Messages) != 2 {
		t.Fatalf("round-trip messages = %s; error = %v", roundTrip.Body, err)
	}
	var results []messagesBlock
	if err := json.Unmarshal(messages.Messages[1].Content, &results); err != nil || len(results) != 3 {
		t.Fatalf("round-trip tool results = %s; error = %v", messages.Messages[1].Content, err)
	}
	if rawString(results[0].Content) != `{"output":"x"}` || len(results[1].Content) != 0 || !results[2].IsError || rawString(results[2].Content) != "boom" {
		t.Fatalf("round-trip tool results = %#v", results)
	}
}

func TestResponsesGeminiJSONLookingStringToolOutputRoundTrip(t *testing.T) {
	toGemini := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	source := []byte(`{"model":"responses","input":[{"type":"function_call","call_id":"call_1","name":"inspect","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":"{\"output\":\"x\"}"}]}`)
	converted, err := toGemini.ToUpstreamRequest(context.Background(), source, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiRequest
	if err := json.Unmarshal(converted.Body, &gemini); err != nil {
		t.Fatal(err)
	}
	response := gemini.Contents[1].Parts[0].FunctionResponse
	if response == nil || string(response.Response) != `{"output":"{\"output\":\"x\"}"}` {
		t.Fatalf("function response = %#v", response)
	}

	toResponses := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	roundTrip, err := toResponses.ToUpstreamRequest(context.Background(), converted.Body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
	if err != nil {
		t.Fatal(err)
	}
	var responses responsesRequest
	if err := json.Unmarshal(roundTrip.Body, &responses); err != nil {
		t.Fatal(err)
	}
	var items []responsesItem
	if err := json.Unmarshal(responses.Input, &items); err != nil || len(items) != 2 || rawString(items[1].Output) != `{"output":"x"}` {
		t.Fatalf("round-trip Responses input = %s; items = %#v", responses.Input, items)
	}
}

func TestGeminiToolErrorCannotSilentlyBecomeSuccessfulResponsesOutput(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	for _, errorValue := range []string{`"boom"`, `null`, `{"code":1}`} {
		body := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"inspect","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"inspect","response":{"error":` + errorValue + `}}}]}]}`)
		_, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), ".functionResponse.response.error") {
			t.Fatalf("error value %s: error = %v, want ErrUnsupported at response.error", errorValue, err)
		}
	}
}

func TestMessagesEmptyAndMediaErrorToolResultsMapToGemini(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	body := []byte(`{"model":"claude","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"empty","name":"empty","input":{}},{"type":"tool_use","id":"media","name":"media","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"empty","content":[]},{"type":"tool_result","tool_use_id":"media","is_error":true,"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1n"}}]}]}]}`)
	converted, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiRequest
	if err := json.Unmarshal(converted.Body, &gemini); err != nil || len(gemini.Contents) != 2 || len(gemini.Contents[1].Parts) != 2 {
		t.Fatalf("gemini = %s; error = %v", converted.Body, err)
	}
	empty := gemini.Contents[1].Parts[0].FunctionResponse
	media := gemini.Contents[1].Parts[1].FunctionResponse
	if empty == nil || string(empty.Response) != `{}` {
		t.Fatalf("empty result = %#v", empty)
	}
	if media == nil || string(media.Response) != `{"error":""}` || len(media.Parts) != 1 || media.Parts[0].InlineData == nil {
		t.Fatalf("media error result = %#v", media)
	}
	toMessages := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	roundTrip, err := toMessages.ToUpstreamRequest(context.Background(), converted.Body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	var messages messagesRequest
	if err := json.Unmarshal(roundTrip.Body, &messages); err != nil || len(messages.Messages) != 2 {
		t.Fatalf("round-trip messages = %s; error = %v", roundTrip.Body, err)
	}
	var blocks []messagesBlock
	if err := json.Unmarshal(messages.Messages[1].Content, &blocks); err != nil || len(blocks) != 2 {
		t.Fatalf("round-trip blocks = %s; error = %v", messages.Messages[1].Content, err)
	}
	if len(blocks[0].Content) != 0 || !blocks[1].IsError {
		t.Fatalf("round-trip results = %#v", blocks)
	}
}

func TestChatGeminiToolResultPreservesExactText(t *testing.T) {
	toGemini := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
	source := []byte(`{"model":"chat","messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"inspect","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"{\"a\":1, \"a\":2}"}]}`)
	converted, err := toGemini.ToUpstreamRequest(context.Background(), source, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiRequest
	if err := json.Unmarshal(converted.Body, &gemini); err != nil {
		t.Fatal(err)
	}
	response := gemini.Contents[1].Parts[0].FunctionResponse
	if response == nil || string(response.Response) != `{"output":"{\"a\":1, \"a\":2}"}` {
		t.Fatalf("function response = %#v", response)
	}

	toChat := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	roundTrip, err := toChat.ToUpstreamRequest(context.Background(), converted.Body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(roundTrip.Body, &chat); err != nil || len(chat.Messages) != 2 {
		t.Fatalf("round-trip body = %s; error = %v", roundTrip.Body, err)
	}
	if got := rawString(chat.Messages[1].Content); got != `{"a":1, "a":2}` {
		t.Fatalf("round-trip tool text = %q", got)
	}
}

func TestGeminiToolErrorPresenceIsNeverTreatedAsChatSuccess(t *testing.T) {
	converter := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	for _, errorValue := range []string{`null`, `"boom"`, `{"code":1}`} {
		body := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"inspect","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"inspect","response":{"error":` + errorValue + `}}}]}]}`)
		_, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), ".functionResponse.response.error") {
			t.Fatalf("error value %s: error = %v", errorValue, err)
		}
	}
}

func TestGeminiStructuredToolResultUnwrapsForMessages(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	tests := []struct {
		name      string
		response  string
		wantText  string
		wantError bool
	}{
		{name: "structured output", response: `{"output":{"x":1}}`, wantText: `{"x":1}`},
		{name: "structured error", response: `{"error":{"code":1}}`, wantText: `{"code":1}`, wantError: true},
		{name: "null error", response: `{"error":null}`, wantText: `null`, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"inspect","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"inspect","response":` + test.response + `}}]}]}`)
			converted, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
			if err != nil {
				t.Fatal(err)
			}
			var messages messagesRequest
			if err := json.Unmarshal(converted.Body, &messages); err != nil || len(messages.Messages) != 2 {
				t.Fatalf("messages = %s; error = %v", converted.Body, err)
			}
			var blocks []messagesBlock
			if err := json.Unmarshal(messages.Messages[1].Content, &blocks); err != nil || len(blocks) != 1 {
				t.Fatalf("blocks = %s; error = %v", messages.Messages[1].Content, err)
			}
			if got := rawString(blocks[0].Content); got != test.wantText || blocks[0].IsError != test.wantError {
				t.Fatalf("tool result = %#v, text = %q", blocks[0], got)
			}
		})
	}
}

func TestGeminiToolResultWrapperMetadataFailsClosed(t *testing.T) {
	body := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"inspect","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"inspect","response":{"output":"ok","meta":1}}}]}]}`)
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
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), ".functionResponse.response") {
				t.Fatalf("error = %v, want ErrUnsupported at functionResponse.response", err)
			}
		})
	}

	messages := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	errorWithMetadata := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"inspect","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"inspect","response":{"error":"boom","meta":1}}}]}]}`)
	if _, err := messages.ToUpstreamRequest(context.Background(), errorWithMetadata, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "messages"}}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), ".functionResponse.response") {
		t.Fatalf("error wrapper metadata: error = %v", err)
	}
}

func TestResponsesTextContentToolOutputMapsToGemini(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	body := []byte(`{"model":"responses","input":[{"type":"function_call","call_id":"call_1","name":"inspect","arguments":"{}"},{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"hello "},{"type":"input_text","text":"world"}]}]}`)
	converted, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiRequest
	if err := json.Unmarshal(converted.Body, &gemini); err != nil {
		t.Fatal(err)
	}
	response := gemini.Contents[1].Parts[0].FunctionResponse
	if response == nil || string(response.Response) != `{"output":"hello world"}` || len(response.Parts) != 0 {
		t.Fatalf("function response = %#v", response)
	}
}
