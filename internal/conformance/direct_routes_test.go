package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestResponsesProviderRequestMatrixUsesDirectedRoutes(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		From Protocol
		body string
	}{
		{"chat", ProtocolChat, `{"model":"public","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`},
		{"messages", ProtocolMessages, `{"model":"public","max_tokens":128,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]},{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{"key":"x"}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"ok"}]}]}`},
		{"gemini", ProtocolGenerateContent, `{"contents":[{"role":"user","parts":[{"text":"hello"}]},{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"key":"x"}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"value":"ok"}}}]}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution, err := harness.ToUpstreamRequest(context.Background(), test.From, ProtocolResponses, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider", Stream: true}})
			if err != nil {
				t.Fatalf("ToUpstreamRequest() error = %v", err)
			}
			var request responsesRequest
			if err := json.Unmarshal(execution.Result.Body, &request); err != nil {
				t.Fatalf("response request JSON: %v", err)
			}
			if request.Model != "provider" || !request.Stream || len(request.Input) == 0 {
				t.Fatalf("upstream request = %s", execution.Result.Body)
			}
		})
	}
}

func TestResponsesProviderResponseMatrix(t *testing.T) {
	harness, _ := newTestRouterHarness()
	response := []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"provider","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]},{"id":"fc_item","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"key\":\"x\"}","status":"completed"}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`)
	for _, from := range []Protocol{ProtocolChat, ProtocolMessages, ProtocolGenerateContent} {
		t.Run(string(from), func(t *testing.T) {
			plan, err := harness.catalog().Plan(from, ProtocolResponses)
			if err != nil {
				t.Fatal(err)
			}
			result, err := harness.ToClientResponse(context.Background(), plan, response, conversionOptions{Exchange: exchangeMetadata{ClientModel: "public"}})
			if err != nil {
				t.Fatalf("ToClientResponse() error = %v", err)
			}
			if !strings.Contains(string(result.Body), "hello") || !strings.Contains(string(result.Body), "lookup") || !strings.Contains(string(result.Body), "public") {
				t.Fatalf("client response = %s", result.Body)
			}
		})
	}
}

func TestResponsesStreamsAreIncrementalForPrimaryIngressProtocols(t *testing.T) {
	harness, _ := newTestRouterHarness()
	created := streamFrame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","object":"response","created_at":1,"model":"provider","status":"in_progress","output":[],"usage":{"input_tokens":3,"output_tokens":0,"total_tokens":3}}}`)}
	itemAdded := streamFrame{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`)}
	delta := streamFrame{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hello"}`)}
	partAdded := streamFrame{Event: "response.content_part.added", Data: []byte(`{"type":"response.content_part.added","item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`)}
	completed := streamFrame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"model":"provider","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`)}
	for _, from := range []Protocol{ProtocolChat, ProtocolMessages, ProtocolGenerateContent} {
		t.Run(string(from), func(t *testing.T) {
			plan, _ := harness.catalog().Plan(from, ProtocolResponses)
			stream, err := harness.NewResponseStream(context.Background(), plan, conversionOptions{Exchange: exchangeMetadata{ClientModel: "public"}})
			if err != nil {
				t.Fatal(err)
			}
			var output []streamFrame
			for _, input := range []streamFrame{created, itemAdded, partAdded, delta, completed} {
				frames, _, err := stream.Convert(context.Background(), input)
				if err != nil {
					t.Fatalf("Convert(%s) error = %v", input.Event, err)
				}
				output = append(output, frames...)
			}
			if _, _, err := stream.Finalize(context.Background()); err != nil {
				t.Fatalf("Finalize() error = %v", err)
			}
			joined := ""
			for _, frame := range output {
				joined += frame.Event + string(frame.Data)
			}
			if !strings.Contains(joined, "hello") {
				t.Fatalf("stream output = %s", joined)
			}
		})
	}
}

func TestSemanticLossFailsClosed(t *testing.T) {
	harness, _ := newTestRouterHarness()
	tests := []struct {
		From Protocol
		body string
	}{
		{ProtocolChat, `{"model":"x","messages":[{"role":"user","content":"hi"}],"stop":"END"}`},
		{ProtocolMessages, `{"model":"x","max_tokens":10,"thinking":{"type":"enabled","budget_tokens":100},"messages":[{"role":"user","content":"hi"}]}`},
		{ProtocolGenerateContent, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"safetySettings":[{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"BLOCK_NONE"}]}`},
	}
	for _, test := range tests {
		_, err := harness.ToUpstreamRequest(context.Background(), test.From, ProtocolResponses, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("from %s error = %v, want ErrUnsupported", test.From, err)
		}
	}
}

func TestMessagesCacheControlsFailClosed(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{"model":"x","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`)
	_, err := harness.ToUpstreamRequest(context.Background(), ProtocolMessages, ProtocolResponses, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestProviderFailureIsNotFabricatedAsCompletion(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolChat, ProtocolResponses)
	failed := []byte(`{"id":"resp_1","object":"response","status":"failed","error":{"code":"server_error","message":"generation failed"},"output":[]}`)
	_, err := harness.ToClientResponse(context.Background(), plan, failed, conversionOptions{})
	if !errors.Is(err, ErrUpstreamResponse) {
		t.Fatalf("error = %v, want ErrUpstreamResponse", err)
	}
}

func TestMessagesCacheCreationUsageRequiresExplicitLossPolicy(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolResponses, ProtocolMessages)
	response := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":2,"cache_creation_input_tokens":4}}`)
	if _, err := harness.ToClientResponse(context.Background(), plan, response, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict error = %v, want ErrUnsupported", err)
	}
	result, err := harness.ToClientResponse(context.Background(), plan, response, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "cache_creation_usage_not_representable" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestResponsesContextStateFieldsFailClosed(t *testing.T) {
	harness, _ := newTestRouterHarness()
	tests := []string{
		`{"model":"x","input":[{"type":"message","role":"user","phase":"commentary","content":[{"type":"input_text","text":"hi"}]}]}`,
		`{"model":"x","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi","prompt_cache_breakpoint":{"type":"default"}}]}]}`,
		`{"model":"x","input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"defer_loading":true}]}`,
	}
	for _, body := range tests {
		_, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, []byte(body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("body=%s error=%v, want ErrUnsupported", body, err)
		}
	}
}

func TestEncryptedReasoningOutputFailsClosed(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolChat, ProtocolResponses)
	response := []byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"id":"rs_1","type":"reasoning","encrypted_content":"opaque","summary":[]}]}`)
	_, err := harness.ToClientResponse(context.Background(), plan, response, conversionOptions{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestKnownResponsesOutputPhaseIsDiagnosed(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolChat, ProtocolResponses)
	response := []byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"hello","annotations":[]}]}]}`)
	result, err := harness.ToClientResponse(context.Background(), plan, response, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "responses_output_phase_not_representable" {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestChatImageDetailPreservedToResponses(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{"model":"x","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://example.test/image.png","detail":"high"}}]}]}`)
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolResponses, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(execution.Result.Body), `"detail":"high"`) {
		t.Fatalf("upstream request = %s", execution.Result.Body)
	}
}

func TestResponsesFileIDImageFailsClosedForChat(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{"model":"x","input":[{"type":"message","role":"user","content":[{"type":"input_image","file_id":"file_123"}]}]}`)
	_, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestEngineAllDirectedRoutes(t *testing.T) {
	harness, _ := newTestRouterHarness()
	requests := map[Protocol]string{
		ProtocolChat:            `{"model":"public","messages":[{"role":"user","content":"hello"}]}`,
		ProtocolResponses:       `{"model":"public","input":"hello"}`,
		ProtocolMessages:        `{"model":"public","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`,
		ProtocolGenerateContent: `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
	}
	responses := map[Protocol]string{
		ProtocolChat:            `{"id":"chat_1","object":"chat.completion","created":1,"model":"provider","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		ProtocolResponses:       `{"id":"resp_1","object":"response","created_at":1,"model":"provider","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`,
		ProtocolMessages:        `{"id":"msg_1","type":"message","role":"assistant","model":"provider","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`,
		ProtocolGenerateContent: `{"responseId":"gemini_1","modelVersion":"provider","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
	}
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, from := range protocols {
		for _, to := range protocols {
			if from == to {
				continue
			}
			t.Run(string(from)+"_to_"+string(to), func(t *testing.T) {
				options := conversionOptions{Exchange: exchangeMetadata{ClientModel: "public", UpstreamModel: "provider"}}
				execution, err := harness.ToUpstreamRequest(context.Background(), from, to, []byte(requests[from]), options)
				if err != nil {
					t.Fatalf("ToUpstreamRequest() error = %v", err)
				}
				if !json.Valid(execution.Result.Body) {
					t.Fatalf("invalid upstream JSON: %s", execution.Result.Body)
				}
				result, err := harness.ToClientResponse(context.Background(), execution.Plan, []byte(responses[to]), options)
				if err != nil {
					t.Fatalf("ToClientResponse() error = %v", err)
				}
				if !json.Valid(result.Body) || !strings.Contains(string(result.Body), "hello") {
					t.Fatalf("client response = %s", result.Body)
				}
			})
		}
	}
}
