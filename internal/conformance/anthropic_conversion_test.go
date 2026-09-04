package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestResponsesToMessagesNormalizesParameterlessTools(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages})
	for _, body := range []string{
		`{"model":"gpt","input":"use it","max_output_tokens":32,"tools":[{"type":"function","name":"clock"}]}`,
		`{"model":"gpt","input":"use it","max_output_tokens":32,"tools":[{"type":"function","name":"clock","parameters":{"additionalProperties":false}}]}`,
	} {
		result, err := converter.ToUpstreamRequest(context.Background(), []byte(body), conversionOptions{})
		if err != nil {
			t.Fatalf("ToUpstreamRequest() error = %v", err)
		}
		var got messagesRequest
		if err := json.Unmarshal(result.Body, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Tools) != 1 {
			t.Fatalf("tools = %#v", got.Tools)
		}
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(got.Tools[0].InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if string(schema["type"]) != `"object"` || string(schema["properties"]) != `{}` {
			t.Fatalf("input_schema = %s", got.Tools[0].InputSchema)
		}
	}
}

func TestMessagesMaxTokensZeroIsUnsupportedForCrossProtocolConversion(t *testing.T) {
	body := []byte(`{"model":"claude","max_tokens":0,"messages":[{"role":"user","content":"warm cache"}]}`)
	chatRoute := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat})
	if _, err := chatRoute.ToUpstreamRequest(context.Background(), body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("decodePortableRequest error = %v, want ErrUnsupported", err)
	}
	if _, err := (newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})).ToUpstreamRequest(context.Background(), body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Messages -> Responses error = %v, want ErrUnsupported", err)
	}
}

func TestMessagesThinkingAndEffortValidation(t *testing.T) {
	valid := []byte(`{
		"model":"claude","max_tokens":2048,
		"thinking":{"type":"enabled","budget_tokens":1024,"display":"summarized"},
		"output_config":{"effort":"xhigh"},
		"messages":[{"role":"user","content":"hi"}]
	}`)
	chatRoute := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat})
	if _, err := chatRoute.ToUpstreamRequest(context.Background(), valid, conversionOptions{LossPolicy: allowDocumentedLoss}); err != nil {
		t.Fatalf("valid thinking config: %v", err)
	}
	for _, test := range []struct {
		name string
		body string
		want error
	}{
		{"budget below minimum", `{"model":"claude","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1023},"messages":[{"role":"user","content":"hi"}]}`, ErrInvalidPayload},
		{"budget equals max tokens", `{"model":"claude","max_tokens":1024,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hi"}]}`, ErrInvalidPayload},
		{"display on disabled", `{"model":"claude","max_tokens":2048,"thinking":{"type":"disabled","display":"omitted"},"messages":[{"role":"user","content":"hi"}]}`, ErrInvalidPayload},
		{"unknown effort", `{"model":"claude","max_tokens":2048,"output_config":{"effort":"extreme"},"messages":[{"role":"user","content":"hi"}]}`, ErrInvalidPayload},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := chatRoute.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{LossPolicy: allowDocumentedLoss}); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMessagesToolChoicePreservesParallelPolicy(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"claude","max_tokens":32,
		"messages":[{"role":"user","content":"go"}],
		"tools":[{"name":"clock","input_schema":{}}],
		"tool_choice":{"type":"tool","name":"clock","disable_parallel_tool_use":true}
	}`), conversionOptions{})
	if err != nil {
		t.Fatalf("ToUpstreamRequest() error = %v", err)
	}
	var got responsesRequest
	if err := json.Unmarshal(result.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ParallelToolCalls == nil || *got.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls = %#v", got.ParallelToolCalls)
	}
	if string(got.ToolChoice) != `{"name":"clock","type":"function"}` {
		t.Fatalf("tool_choice = %s", got.ToolChoice)
	}
	if string(got.Tools[0].Parameters) != `{"properties":{},"type":"object"}` {
		t.Fatalf("parameters = %s", got.Tools[0].Parameters)
	}
}

func TestStructuredToolResultsRoundTripAcrossMessagesAndResponses(t *testing.T) {
	toResponses := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	result, err := toResponses.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"claude","max_tokens":32,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"inspect","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":[{"type":"text","text":"ok"},{"type":"image","source":{"type":"url","url":"https://example.test/a.png"}}]}]}
		]
	}`), conversionOptions{})
	if err != nil {
		t.Fatalf("Messages -> Responses: %v", err)
	}
	var responses responsesRequest
	if err := json.Unmarshal(result.Body, &responses); err != nil {
		t.Fatal(err)
	}
	var items []responsesItem
	if err := json.Unmarshal(responses.Input, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || !strings.Contains(string(items[1].Output), `"input_image"`) {
		t.Fatalf("Responses items = %s", responses.Input)
	}

	toMessages := newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages})
	back, err := toMessages.ToUpstreamRequest(context.Background(), result.Body, conversionOptions{})
	if err != nil {
		t.Fatalf("Responses -> Messages: %v", err)
	}
	if !strings.Contains(string(back.Body), `"type":"image"`) || !strings.Contains(string(back.Body), `"tool_use_id":"call_1"`) {
		t.Fatalf("Messages body = %s", back.Body)
	}
}

func TestMessagesRequestRejectsInvalidBlockRoleAndMissingContent(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	for _, body := range []string{
		`{"model":"claude","max_tokens":32,"messages":[{"role":"user"}]}`,
		`{"model":"claude","max_tokens":32,"messages":[{"role":"user","content":[{"type":"tool_use","id":"call_1","name":"x","input":{}}]}]}`,
		`{"model":"claude","max_tokens":32,"messages":[{"role":"assistant","content":[{"type":"tool_result","tool_use_id":"call_1","content":"x"}]}]}`,
	} {
		if _, err := converter.ToUpstreamRequest(context.Background(), []byte(body), conversionOptions{}); err == nil {
			t.Fatalf("ToUpstreamRequest(%s) succeeded", body)
		}
	}
}

func TestMessagesResponseValidationAndDocumentedLoss(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages})
	invalidResponse := []byte(`{"id":"msg_1","type":"message","role":"user","model":"claude","content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	if _, err := converter.ToClientResponse(context.Background(), invalidResponse, conversionOptions{}); !errors.Is(err, ErrUpstreamResponse) {
		t.Fatalf("invalid role error = %v", err)
	}

	stopSequence := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"stop_sequence","stop_sequence":"END","usage":{"input_tokens":1,"output_tokens":1}}`)
	if _, err := converter.ToClientResponse(context.Background(), stopSequence, conversionOptions{}); err == nil {
		t.Fatal("stop sequence semantic loss was accepted by default")
	}
	result, err := converter.ToClientResponse(context.Background(), stopSequence, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatalf("documented loss conversion: %v", err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "stop_sequence_not_representable" {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestResponsesToMessagesStreamLifecycleAndContentFilter(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	stream, err := converter.NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	created := streamFrame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt","status":"in_progress","output":[],"usage":{"input_tokens":2}}}`)}
	if _, _, err := stream.Convert(context.Background(), created); err != nil {
		t.Fatal(err)
	}
	terminal := streamFrame{Event: "response.incomplete", Data: []byte(`{"type":"response.incomplete","response":{"id":"resp_1","model":"gpt","status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[],"usage":{"input_tokens":2,"output_tokens":1}}}`)}
	frames, _, err := stream.Convert(context.Background(), terminal)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || !strings.Contains(string(frames[0].Data), `"stop_reason":"refusal"`) || frames[1].Event != "message_stop" {
		t.Fatalf("terminal frames = %#v", frames)
	}
	if _, _, err := stream.Convert(context.Background(), terminal); err == nil {
		t.Fatal("event after terminal was accepted")
	}
	if _, _, err := stream.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAnthropicAndOpenAIUsageUseTheirNativeCacheAccounting(t *testing.T) {
	toResponses := newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages})
	result, err := toResponses.ToClientResponse(context.Background(), []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":10,"cache_read_input_tokens":3,"cache_creation_input_tokens":2,"output_tokens":5}
	}`), conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	var responses responsesResponse
	if err := json.Unmarshal(result.Body, &responses); err != nil {
		t.Fatal(err)
	}
	if responses.Usage.InputTokens != 15 || responses.Usage.TotalTokens != 20 || responses.Usage.InputTokenDetails.CachedTokens != 3 {
		t.Fatalf("Responses usage = %#v", responses.Usage)
	}

	toMessages := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	back, err := toMessages.ToClientResponse(context.Background(), []byte(`{
		"id":"resp_1","object":"response","model":"gpt","status":"completed","output":[{"id":"m1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],
		"usage":{"input_tokens":13,"output_tokens":5,"total_tokens":18,"input_tokens_details":{"cached_tokens":3}}
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var messages messagesResponse
	if err := json.Unmarshal(back.Body, &messages); err != nil {
		t.Fatal(err)
	}
	if messages.Usage.InputTokens != 10 || messages.Usage.CacheReadInputTokens != 3 || messages.Usage.OutputTokens != 5 {
		t.Fatalf("Messages usage = %#v", messages.Usage)
	}
}
