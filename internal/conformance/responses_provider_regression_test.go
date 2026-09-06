package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestResponsesProviderCustomToolCallCompletedStatus(t *testing.T) {
	converter := newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})
	response := []byte(`{
		"id":"resp_1","object":"response","created_at":1,"model":"provider-model","status":"completed",
		"output":[{"id":"ctc_1","type":"custom_tool_call","call_id":"call_1","name":"record_marker_text","input":"marker","status":"completed"}],
		"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
	}`)
	result, err := converter.ToClientResponse(context.Background(), response, conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
	if err != nil {
		t.Fatal(err)
	}
	var chat struct {
		Model   string `json:"model"`
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					ID     string `json:"id"`
					Type   string `json:"type"`
					Custom struct {
						Name  string `json:"name"`
						Input string `json:"input"`
					} `json:"custom"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(result.Body, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Model != "client-model" || len(chat.Choices) != 1 || chat.Choices[0].FinishReason != "tool_calls" || len(chat.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("unexpected custom-tool Chat envelope: %s", result.Body)
	}
	call := chat.Choices[0].Message.ToolCalls[0]
	if call.ID != "call_1" || call.Type != "custom" || call.Custom.Name != "record_marker_text" || call.Custom.Input != "marker" {
		t.Fatalf("custom tool call was not preserved: %s", result.Body)
	}
}

func TestResponsesProviderWebSearchActionAndCitationShape(t *testing.T) {
	converter := newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})
	response := []byte(`{
		"id":"resp_1","object":"response","created_at":1,"model":"provider-model","status":"completed",
		"output":[
			{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","query":"Go language","queries":["Go language","golang official site"]}},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Go","annotations":[{"type":"url_citation","start_index":0,"end_index":2,"title":"The Go Programming Language","url":"https://go.dev/"}]}]}
		],
		"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
	}`)
	result, err := converter.ToClientResponse(context.Background(), response, conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "responses_web_search_call_not_representable" {
		t.Fatalf("web-search diagnostics = %#v", result.Diagnostics)
	}
	for _, want := range []string{`"content":"Go"`, `"type":"url_citation"`, `"url":"https://go.dev/"`} {
		if !strings.Contains(string(result.Body), want) {
			t.Fatalf("converted Chat response missing %s: %s", want, result.Body)
		}
	}
}

func TestResponsesProviderToolSearchShapeFailsClosedOnlyAtCrossProtocolBoundary(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	response := []byte(`{
		"id":"resp_1","object":"response","created_at":1,"model":"provider-model","status":"completed",
		"output":[
			{"id":"tsc_1","type":"tool_search_call","arguments":{"query":"record_marker"},"status":"completed"},
			{"id":"tso_1","type":"tool_search_output","status":"completed","tools":[{"type":"function","name":"record_marker","parameters":{"type":"object"},"strict":true,"defer_loading":true}]}
		],
		"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
	}`)
	for _, target := range []Protocol{ProtocolChat, ProtocolMessages, ProtocolGenerateContent} {
		t.Run(string(target), func(t *testing.T) {
			plan, err := harness.catalog().Plan(target, ProtocolResponses)
			if err != nil {
				t.Fatal(err)
			}
			_, err = harness.ToClientResponse(context.Background(), plan, response, conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want native-only ErrUnsupported", err)
			}
			if errors.Is(err, ErrUpstreamResponse) {
				t.Fatalf("provider-shaped tool search was rejected as malformed: %v", err)
			}
		})
	}
}

func TestResponsesProviderServiceTierCompatibility(t *testing.T) {
	routes := []struct {
		name      string
		converter routeConverter
	}{
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
	}
	response := `{
		"id":"resp_1","object":"response","created_at":1,"model":"provider-model","status":"completed",
		"service_tier":"SERVICE_TIER",
		"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],
		"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3,"input_tokens_details":{"cached_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}
	}`
	body := func(tier string) []byte {
		return []byte(strings.Replace(response, "SERVICE_TIER", tier, 1))
	}

	for _, route := range routes {
		route := route
		t.Run(route.name, func(t *testing.T) {
			for _, tier := range []string{"auto", "default"} {
				t.Run(tier, func(t *testing.T) {
					result, err := route.converter.ToClientResponse(context.Background(), body(tier), conversionOptions{})
					if err != nil {
						t.Fatal(err)
					}
					count := 0
					for _, diagnostic := range result.Diagnostics {
						if diagnostic.Code == "responses_service_tier_not_representable" {
							count++
						}
					}
					if count != 1 {
						t.Fatalf("service-tier diagnostic count=%d, want 1: %#v", count, result.Diagnostics)
					}
				})
			}

			if _, err := route.converter.ToClientResponse(context.Background(), body("priority"), conversionOptions{}); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("priority tier error=%v, want ErrUnsupported", err)
			}
			if _, err := route.converter.ToClientResponse(context.Background(), body("future-tier"), conversionOptions{}); !errors.Is(err, ErrUpstreamResponse) {
				t.Fatalf("unknown tier error=%v, want ErrUpstreamResponse", err)
			}
		})
	}
}
