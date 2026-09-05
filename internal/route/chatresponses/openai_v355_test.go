package chatresponses

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestOpenAIV355CommonRequestFieldsRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		from Protocol
		to   Protocol
		body string
		want []string
	}{
		{
			name: "chat_to_responses", from: ProtocolChat, to: ProtocolResponses,
			body: `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi","prompt_cache_breakpoint":{"mode":"explicit"}}]}],"response_format":{"type":"json_object"},"prompt_cache_options":{"mode":"explicit","ttl":"30m"},"moderation":{"model":"omni-moderation-latest","policy":{"input":{"mode":"score"},"output":{"mode":"block"}}},"stream":true,"stream_options":{"include_usage":true,"include_obfuscation":false}}`,
			want: []string{`"prompt_cache_options":{"mode":"explicit","ttl":"30m"}`, `"prompt_cache_breakpoint":{"mode":"explicit"}`, `"moderation":{"model":"omni-moderation-latest"`, `"format":{"type":"json_object"}`, `"stream_options":{"include_obfuscation":false}`},
		},
		{
			name: "responses_to_chat", from: ProtocolResponses, to: ProtocolChat,
			body: `{"model":"m","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi","prompt_cache_breakpoint":{"mode":"explicit"}}]}],"text":{"format":{"type":"json_object"}},"prompt_cache_options":{"mode":"explicit","ttl":"30m"},"moderation":{"model":"omni-moderation-latest","policy":{"input":{"mode":"score"},"output":{"mode":"block"}}},"stream":true,"stream_options":{"include_obfuscation":false}}`,
			want: []string{`"prompt_cache_options":{"mode":"explicit","ttl":"30m"}`, `"prompt_cache_breakpoint":{"mode":"explicit"}`, `"moderation":{"model":"omni-moderation-latest"`, `"response_format":{"type":"json_object"}`, `"stream_options":{"include_usage":true,"include_obfuscation":false}`},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converter := New(core.RouteSpec{From: test.from, To: test.to})
			result, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), core.ConversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(string(result.Body), want) {
					t.Errorf("converted body missing %s: %s", want, result.Body)
				}
			}
		})
	}
}

func TestOpenAIV355RequestValidationFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		from Protocol
		to   Protocol
		body string
		kind error
	}{
		{"chat_stream_options_without_stream", ProtocolChat, ProtocolResponses, `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream_options":{"include_usage":true}}`, core.ErrInvalidPayload},
		{"responses_stream_options_without_stream", ProtocolResponses, ProtocolChat, `{"model":"m","input":"hi","stream_options":{"include_obfuscation":true}}`, core.ErrInvalidPayload},
		{"bad_prompt_cache_ttl", ProtocolChat, ProtocolResponses, `{"model":"m","messages":[{"role":"user","content":"hi"}],"prompt_cache_options":{"ttl":"24h"}}`, core.ErrInvalidPayload},
		{"bad_moderation_mode", ProtocolResponses, ProtocolChat, `{"model":"m","input":"hi","moderation":{"model":"omni-moderation-latest","policy":{"input":{"mode":"audit"}}}}`, core.ErrInvalidPayload},
		{"unknown_json_object_field", ProtocolResponses, ProtocolChat, `{"model":"m","input":"hi","text":{"format":{"type":"json_object","schema":{}}}}`, core.ErrUnsupported},
		{"missing_breakpoint_mode", ProtocolChat, ProtocolResponses, `{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hi","prompt_cache_breakpoint":{}}]}]}`, core.ErrInvalidPayload},
		{"chat_frequency_penalty", ProtocolChat, ProtocolResponses, `{"model":"m","messages":[{"role":"user","content":"hi"}],"frequency_penalty":0.2}`, core.ErrUnsupported},
		{"responses_presence_penalty", ProtocolResponses, ProtocolChat, `{"model":"m","input":"hi","presence_penalty":0.2}`, core.ErrUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converter := New(core.RouteSpec{From: test.from, To: test.to})
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), core.ConversionOptions{})
			if !errors.Is(err, test.kind) {
				t.Fatalf("error = %v, want %v", err, test.kind)
			}
		})
	}
}

func TestOpenAIV355NonStreamResponseEnvelopeAndCitations(t *testing.T) {
	responsesUpstream := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	responsesBody := []byte(`{"id":"resp_1","object":"response","created_at":7,"model":"provider","status":"completed","metadata":{"trace":"one"},"service_tier":"priority","moderation":{"input":{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false},"output":{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false}},"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hi ","annotations":[{"type":"url_citation","start_index":0,"end_index":2,"title":"first","url":"https://one.example"}]},{"type":"output_text","text":"there","annotations":[{"type":"url_citation","start_index":3,"end_index":8,"title":"second","url":"https://two.example"}]}]}],"usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}`)
	chatResult, err := responsesUpstream.ToClientResponse(context.Background(), responsesBody, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(chatResult.Body, &chat); err != nil {
		t.Fatal(err)
	}
	if chat["service_tier"] != "priority" || chat["metadata"].(map[string]any)["trace"] != "one" {
		t.Fatalf("response envelope was not preserved: %s", chatResult.Body)
	}
	moderationInput := chat["moderation"].(map[string]any)["input"].(map[string]any)
	if moderationInput["type"] != "moderation_results" || len(moderationInput["results"].([]any)) != 1 {
		t.Fatalf("Responses moderation was not wrapped for Chat: %#v", moderationInput)
	}
	message := chat["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "Hi there" {
		t.Fatalf("Chat response text was not flattened: %#v", message)
	}
	annotations := message["annotations"].([]any)
	second := annotations[1].(map[string]any)["url_citation"].(map[string]any)
	if second["start_index"] != float64(3) || second["end_index"] != float64(8) {
		t.Fatalf("citation offsets were not rebased: %#v", annotations)
	}

	chatUpstream := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
	chatBody := []byte(`{"id":"chat_1","object":"chat.completion","created":8,"model":"provider","metadata":{"trace":"two"},"service_tier":"flex","moderation":{"input":{"type":"moderation_results","model":"omni-moderation-latest","results":[{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false}]},"output":{"type":"moderation_results","model":"omni-moderation-latest","results":[{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false}]}},"system_fingerprint":"fp_1","choices":[{"index":0,"message":{"role":"assistant","content":"hello","annotations":[{"type":"url_citation","url_citation":{"start_index":0,"end_index":5,"title":"doc","url":"https://doc.example"}}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	responsesResult, err := chatUpstream.ToClientResponse(context.Background(), chatBody, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(responsesResult.Diagnostics) != 1 || responsesResult.Diagnostics[0].Code != "chat_system_fingerprint_not_representable" {
		t.Fatalf("diagnostics = %#v", responsesResult.Diagnostics)
	}
	for _, want := range []string{`"metadata":{"trace":"two"}`, `"service_tier":"flex"`, `"annotations":[{"type":"url_citation","start_index":0,"end_index":5,"title":"doc","url":"https://doc.example"}]`} {
		if !strings.Contains(string(responsesResult.Body), want) {
			t.Errorf("Responses body missing %s: %s", want, responsesResult.Body)
		}
	}
	var responses map[string]any
	if err := json.Unmarshal(responsesResult.Body, &responses); err != nil {
		t.Fatal(err)
	}
	moderationOutput := responses["moderation"].(map[string]any)["output"].(map[string]any)
	if moderationOutput["type"] != "moderation_result" {
		t.Fatalf("Chat moderation was not unwrapped for Responses: %#v", moderationOutput)
	}
}

func TestOpenAIV355UnrepresentableUsageFailsClosed(t *testing.T) {
	responsesUpstream := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	_, err := responsesUpstream.ToClientResponse(context.Background(), []byte(`{"id":"r","status":"completed","output":[],"usage":{"input_tokens":1,"total_tokens":1,"input_tokens_details":{"cache_write_tokens":1}}}`), core.ConversionOptions{})
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("Responses cache-write usage error = %v", err)
	}

	chatUpstream := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
	_, err = chatUpstream.ToClientResponse(context.Background(), []byte(`{"id":"c","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"completion_tokens_details":{"accepted_prediction_tokens":1}}}`), core.ConversionOptions{})
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("Chat prediction usage error = %v", err)
	}
}

func TestOpenAIV355ResponsesStreamEnvelopeMapping(t *testing.T) {
	converter := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	stream, err := converter.NewClientStream(context.Background(), core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frames, diagnostics, err := stream.Convert(context.Background(), core.Frame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","created_at":1,"model":"m","status":"in_progress","metadata":{"trace":"one"},"service_tier":"priority","moderation":{"input":{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false},"output":{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false}},"output":[]}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != "responses_metadata_not_representable_in_chat_stream" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(frames) != 1 || !strings.Contains(string(frames[0].Data), `"service_tier":"priority"`) || !strings.Contains(string(frames[0].Data), `"moderation":`) {
		t.Fatalf("Chat stream envelope was not preserved: %#v", frames)
	}
}

func TestOpenAIV355ResponseExtensionsFailClosed(t *testing.T) {
	tests := []struct {
		name string
		from Protocol
		to   Protocol
		body string
		kind error
	}{
		{
			name: "responses ultrafast tier cannot become Chat",
			from: ProtocolResponses,
			to:   ProtocolChat,
			body: `{"model":"m","input":"hi","service_tier":"ultrafast"}`,
			kind: core.ErrUnsupported,
		},
		{
			name: "annotations are response-only",
			from: ProtocolChat,
			to:   ProtocolResponses,
			body: `{"model":"m","messages":[{"role":"user","content":"hi","annotations":[]}]}`,
			kind: core.ErrInvalidPayload,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converter := New(core.RouteSpec{From: test.from, To: test.to})
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), core.ConversionOptions{})
			if !errors.Is(err, test.kind) {
				t.Fatalf("error = %v, want %v", err, test.kind)
			}
		})
	}
}

func TestOpenAIV355CitationRangesAreValidated(t *testing.T) {
	responsesUpstream := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	responsesBody := []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"你好","annotations":[{"type":"url_citation","start_index":0,"end_index":3,"title":"bad","url":"https://example.com"}]}]}]}`)
	if _, err := responsesUpstream.ToClientResponse(context.Background(), responsesBody, core.ConversionOptions{}); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("Responses citation error = %v, want ErrUpstreamResponse", err)
	}

	chatUpstream := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
	chatBody := []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"你好","annotations":[{"type":"url_citation","url_citation":{"start_index":0,"end_index":3,"title":"bad","url":"https://example.com"}}]},"finish_reason":"stop"}]}`)
	if _, err := chatUpstream.ToClientResponse(context.Background(), chatBody, core.ConversionOptions{}); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("Chat citation error = %v, want ErrUpstreamResponse", err)
	}
}

func TestOpenAIV355CommonUsageIsValidated(t *testing.T) {
	responsesUpstream := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	responsesBody := []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"input_tokens_details":{"cached_tokens":2}}}`)
	if _, err := responsesUpstream.ToClientResponse(context.Background(), responsesBody, core.ConversionOptions{}); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("Responses usage error = %v, want ErrUpstreamResponse", err)
	}

	chatUpstream := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
	chatBody := []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"completion_tokens_details":{"reasoning_tokens":2}}}`)
	if _, err := chatUpstream.ToClientResponse(context.Background(), chatBody, core.ConversionOptions{}); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("Chat usage error = %v, want ErrUpstreamResponse", err)
	}
}

func TestOpenAIV355ResponsesStreamObfuscationIsDiagnosed(t *testing.T) {
	converter := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	stream, err := converter.NewClientStream(context.Background(), core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	created := core.Frame{Event: "response.created", Data: []byte(`{"type":"response.created","obfuscation":"padding","response":{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"in_progress","output":[]}}`)}
	if _, _, err := stream.Convert(context.Background(), created); err != nil {
		t.Fatal(err)
	}
	completed := core.Frame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`)}
	if _, _, err := stream.Convert(context.Background(), completed); err != nil {
		t.Fatal(err)
	}
	_, diagnostics, err := stream.Finalize(context.Background())
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Code != "responses_stream_obfuscation_not_representable" {
		t.Fatalf("diagnostics=%#v error=%v", diagnostics, err)
	}
}

func TestOpenAIV355ResponsePromptCacheOptionsLossPolicy(t *testing.T) {
	converter := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	body := []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed","prompt_cache_options":{"mode":"explicit","ttl":"30m"},"output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	if _, err := converter.ToClientResponse(context.Background(), body, core.ConversionOptions{}); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("strict error = %v, want ErrUnsupported", err)
	}
	result, err := converter.ToClientResponse(context.Background(), body, core.ConversionOptions{LossPolicy: core.AllowDocumentedLoss})
	if err != nil || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "responses_prompt_cache_options_not_representable" {
		t.Fatalf("result=%#v error=%v", result, err)
	}

	stream, err := converter.NewClientStream(context.Background(), core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frame := core.Frame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"in_progress","prompt_cache_options":{"mode":"explicit","ttl":"30m"},"output":[]}}`)}
	if _, _, err := stream.Convert(context.Background(), frame); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("stream error = %v, want ErrUnsupported", err)
	}
}

func TestOpenAIV355CustomToolsNonStreaming(t *testing.T) {
	chatToResponses := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	chatRequestBody := []byte(`{
		"model":"m",
		"tools":[{"type":"custom","custom":{"name":"shell","description":"commands","format":{"type":"grammar","grammar":{"definition":"start: WORD","syntax":"lark"}}}}],
		"tool_choice":{"type":"custom","custom":{"name":"shell"}},
		"messages":[
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"custom","custom":{"name":"shell","input":"echo hi"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"ok"}
		]
	}`)
	converted, err := chatToResponses.ToUpstreamRequest(context.Background(), chatRequestBody, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"type":"custom","name":"shell"`,
		`"format":{"definition":"start: WORD","syntax":"lark","type":"grammar"}`,
		`"tool_choice":{"name":"shell","type":"custom"}`,
		`"type":"custom_tool_call","call_id":"call_1","name":"shell","input":"echo hi"`,
		`"type":"custom_tool_call_output","call_id":"call_1","output":"ok"`,
	} {
		if !strings.Contains(string(converted.Body), want) {
			t.Errorf("Responses request missing %s: %s", want, converted.Body)
		}
	}

	responsesToChat := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
	responsesRequestBody := []byte(`{
		"model":"m",
		"tools":[{"type":"custom","name":"shell","description":"commands","format":{"type":"grammar","definition":"start: WORD","syntax":"lark"}}],
		"tool_choice":{"type":"custom","name":"shell"},
		"input":[
			{"type":"custom_tool_call","call_id":"call_1","name":"shell","input":"echo hi","caller":{"type":"direct"}},
			{"type":"custom_tool_call_output","call_id":"call_1","output":"ok","caller":{"type":"direct"}}
		]
	}`)
	converted, err = responsesToChat.ToUpstreamRequest(context.Background(), responsesRequestBody, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"type":"custom","custom":{"name":"shell","description":"commands","format":{"grammar":{"definition":"start: WORD","syntax":"lark"},"type":"grammar"}}`,
		`"tool_choice":{"custom":{"name":"shell"},"type":"custom"}`,
		`"type":"custom","custom":{"name":"shell","input":"echo hi"}`,
		`"role":"tool","content":"ok","tool_call_id":"call_1"`,
	} {
		if !strings.Contains(string(converted.Body), want) {
			t.Errorf("Chat request missing %s: %s", want, converted.Body)
		}
	}
	if strings.Contains(string(converted.Body), `"function":{"name":""`) {
		t.Fatalf("custom tool leaked an empty function branch: %s", converted.Body)
	}
}

func TestOpenAIV355CustomToolResponses(t *testing.T) {
	chatToResponses := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	responsesBody := []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed","output":[{"id":"ctc_1","type":"custom_tool_call","call_id":"call_1","name":"shell","input":"echo hi","status":"completed","caller":{"type":"direct"}}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	converted, err := chatToResponses.ToClientResponse(context.Background(), responsesBody, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(converted.Body), `"tool_calls":[{"id":"call_1","type":"custom","custom":{"name":"shell","input":"echo hi"}}]`) {
		t.Fatalf("custom Chat tool call missing: %s", converted.Body)
	}

	responsesToChat := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
	chatBody := []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"custom","custom":{"name":"shell","input":"echo hi"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	converted, err = responsesToChat.ToClientResponse(context.Background(), chatBody, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(converted.Body), `"type":"custom_tool_call"`) || !strings.Contains(string(converted.Body), `"input":"echo hi"`) {
		t.Fatalf("custom Responses item missing: %s", converted.Body)
	}
	var response map[string]any
	if err := json.Unmarshal(converted.Body, &response); err != nil {
		t.Fatal(err)
	}
	if got := response["output"].([]any)[0].(map[string]any)["status"]; got != "completed" {
		t.Fatalf("custom Responses output status = %v, want completed: %s", got, converted.Body)
	}
}

func TestOpenAIV355WebSearchCommonSubset(t *testing.T) {
	chatToResponses := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	chatRequestBody := []byte(`{"model":"m","messages":[{"role":"user","content":"weather"}],"web_search_options":{"search_context_size":"high","user_location":{"type":"approximate","approximate":{"city":"Shanghai","country":"CN","region":"Shanghai","timezone":"Asia/Shanghai"}}}}`)
	converted, err := chatToResponses.ToUpstreamRequest(context.Background(), chatRequestBody, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"web_search"`, `"search_context_size":"high"`, `"user_location":{"city":"Shanghai","country":"CN","region":"Shanghai","timezone":"Asia/Shanghai","type":"approximate"}`} {
		if !strings.Contains(string(converted.Body), want) {
			t.Errorf("Responses web search missing %s: %s", want, converted.Body)
		}
	}

	responsesToChat := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
	responsesRequestBody := []byte(`{"model":"m","input":"weather","tools":[{"type":"web_search","external_web_access":true,"search_context_size":"low","user_location":{"type":"approximate","city":"Paris","country":"FR"}}]}`)
	converted, err = responsesToChat.ToUpstreamRequest(context.Background(), responsesRequestBody, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(converted.Body), `"web_search_options":{"search_context_size":"low","user_location":{"approximate":{"city":"Paris"`) || !strings.Contains(string(converted.Body), `"country":"FR"`) {
		t.Fatalf("Chat web_search_options missing: %s", converted.Body)
	}

	responsesOutput := []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed","output":[{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","queries":["weather"]}},{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"sunny","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	converted, err = chatToResponses.ToClientResponse(context.Background(), responsesOutput, core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(converted.Diagnostics) != 1 || converted.Diagnostics[0].Code != "responses_web_search_call_not_representable" || !strings.Contains(string(converted.Body), `"content":"sunny"`) {
		t.Fatalf("web-search response conversion=%s diagnostics=%#v", converted.Body, converted.Diagnostics)
	}
}

func TestOpenAIV355CustomAndSearchBoundariesFailClosed(t *testing.T) {
	tests := []struct {
		name string
		from Protocol
		to   Protocol
		body string
		path string
	}{
		{"chat custom stream", ProtocolChat, ProtocolResponses, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"custom","custom":{"name":"shell"}}]}`, "$.tools[0].type"},
		{"responses custom stream", ProtocolResponses, ProtocolChat, `{"model":"m","stream":true,"input":"hi","tools":[{"type":"custom","name":"shell"}]}`, "$.tools[0].type"},
		{"chat web stream", ProtocolChat, ProtocolResponses, `{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}],"web_search_options":{}}`, "$.web_search_options"},
		{"tool search", ProtocolResponses, ProtocolChat, `{"model":"m","input":"hi","tools":[{"type":"tool_search","execution":"server"}]}`, "$.tools[0].type"},
		{"web domain filter", ProtocolResponses, ProtocolChat, `{"model":"m","input":"hi","tools":[{"type":"web_search","filters":{"allowed_domains":["example.com"]}}]}`, "$.tools[0].filters.allowed_domains"},
		{"web cache only", ProtocolResponses, ProtocolChat, `{"model":"m","input":"hi","tools":[{"type":"web_search","external_web_access":false}]}`, "$.tools[0].external_web_access"},
		{"custom program caller", ProtocolResponses, ProtocolChat, `{"model":"m","input":[{"type":"custom_tool_call","call_id":"c","name":"shell","input":"x","caller":{"type":"program","caller_id":"p"}}]}`, "$.input[0].caller"},
		{"custom namespace", ProtocolResponses, ProtocolChat, `{"model":"m","input":[{"type":"custom_tool_call","call_id":"c","name":"shell","input":"x","namespace":"ns"}]}`, "$.input[0].namespace"},
		{"customized alias", ProtocolResponses, ProtocolChat, `{"model":"m","input":[{"type":"customized_tool_call","call_id":"c","name":"shell","input":"x"}]}`, "$.input[0].type"},
		{"orphan chat tool result", ProtocolChat, ProtocolResponses, `{"model":"m","messages":[{"role":"tool","tool_call_id":"c","content":"x"}]}`, "$.messages[0].tool_call_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converter := New(core.RouteSpec{From: test.from, To: test.to})
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), core.ConversionOptions{})
			if !errors.Is(err, core.ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error=%v, want ErrUnsupported at %s", err, test.path)
			}
		})
	}

	converter := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	webOnly := []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed","output":[{"id":"ws_1","type":"web_search_call","status":"completed","action":{"type":"search","queries":["x"]}}],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}`)
	if _, err := converter.ToClientResponse(context.Background(), webOnly, core.ConversionOptions{}); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("web-only response error=%v, want ErrUnsupported", err)
	}
}

func TestOpenAIV355ReasoningEffortEnum(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
		t.Run("chat_to_responses/"+effort, func(t *testing.T) {
			converter := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
			body := `{"model":"m","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"` + effort + `"}`
			result, err := converter.ToUpstreamRequest(context.Background(), []byte(body), core.ConversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(result.Body), `"reasoning":{"effort":"`+effort+`"}`) {
				t.Fatalf("reasoning effort was not preserved: %s", result.Body)
			}
		})

		t.Run("responses_to_chat/"+effort, func(t *testing.T) {
			converter := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
			body := `{"model":"m","input":"hi","reasoning":{"effort":"` + effort + `"}}`
			result, err := converter.ToUpstreamRequest(context.Background(), []byte(body), core.ConversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(result.Body), `"reasoning_effort":"`+effort+`"`) {
				t.Fatalf("reasoning effort was not preserved: %s", result.Body)
			}
		})
	}

	for _, test := range []struct {
		name string
		from Protocol
		to   Protocol
		body string
		path string
	}{
		{
			name: "chat",
			from: ProtocolChat,
			to:   ProtocolResponses,
			body: `{"model":"m","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"turbo"}`,
			path: "$.reasoning_effort",
		},
		{
			name: "responses",
			from: ProtocolResponses,
			to:   ProtocolChat,
			body: `{"model":"m","input":"hi","reasoning":{"effort":"turbo"}}`,
			path: "$.reasoning.effort",
		},
	} {
		t.Run("invalid/"+test.name, func(t *testing.T) {
			converter := New(core.RouteSpec{From: test.from, To: test.to})
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), core.ConversionOptions{})
			if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.path)
			}
		})
	}
}

func TestOpenAIV355ResponsesRequestToolCallStatus(t *testing.T) {
	calls := []struct {
		name string
		item string
	}{
		{
			name: "function_call",
			item: `{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"`,
		},
		{
			name: "custom_tool_call",
			item: `{"type":"custom_tool_call","call_id":"call_1","name":"shell","input":"echo hi"`,
		},
	}
	converter := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})

	for _, call := range calls {
		for _, status := range []string{"", "completed"} {
			name := status
			statusField := ""
			if status == "" {
				name = "omitted"
			} else {
				statusField = `,"status":"` + status + `"`
			}
			t.Run(call.name+"/accepted/"+name, func(t *testing.T) {
				body := `{"model":"m","input":[` + call.item + statusField + `}]}`
				if _, err := converter.ToUpstreamRequest(context.Background(), []byte(body), core.ConversionOptions{}); err != nil {
					t.Fatalf("status %q error = %v", status, err)
				}
			})
		}

		for _, status := range []string{"in_progress", "incomplete"} {
			t.Run(call.name+"/unsupported/"+status, func(t *testing.T) {
				body := `{"model":"m","input":[` + call.item + `,"status":"` + status + `"}]}`
				_, err := converter.ToUpstreamRequest(context.Background(), []byte(body), core.ConversionOptions{})
				if !errors.Is(err, core.ErrUnsupported) || !strings.Contains(err.Error(), "$.input[0].status") {
					t.Fatalf("error = %v, want ErrUnsupported at $.input[0].status", err)
				}
			})
		}

		t.Run(call.name+"/invalid", func(t *testing.T) {
			body := `{"model":"m","input":[` + call.item + `,"status":"queued"}]}`
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(body), core.ConversionOptions{})
			if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "$.input[0].status") {
				t.Fatalf("error = %v, want ErrInvalidPayload at $.input[0].status", err)
			}
		})
	}
}

func TestOpenAIV355ResponsesReasoningOutputUnion(t *testing.T) {
	converter := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	responseWithItem := func(item string) []byte {
		return []byte(`{"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed","output":[` + item + `],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}

	for _, status := range []string{"", "in_progress", "completed", "incomplete"} {
		name := status
		statusField := ""
		if status == "" {
			name = "omitted"
		} else {
			statusField = `,"status":"` + status + `"`
		}
		t.Run("accepted/"+name, func(t *testing.T) {
			item := `{"id":"rs_1","type":"reasoning","summary":[{"type":"summary_text","text":"summary"}],"content":[{"type":"reasoning_text","text":"detail"}]` + statusField + `}`
			result, err := converter.ToClientResponse(context.Background(), responseWithItem(item), core.ConversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(result.Body), `"reasoning_content":"summarydetail"`) {
				t.Fatalf("reasoning content was not preserved: %s", result.Body)
			}
		})
	}

	for _, test := range []struct {
		name string
		item string
		path string
	}{
		{
			name: "missing id",
			item: `{"type":"reasoning","summary":[]}`,
			path: "$.output[0].id",
		},
		{
			name: "missing summary",
			item: `{"id":"rs_1","type":"reasoning"}`,
			path: "$.output[0].summary",
		},
		{
			name: "wrong summary union member",
			item: `{"id":"rs_1","type":"reasoning","summary":[{"type":"output_text","text":"summary"}]}`,
			path: "$.output[0].summary[0].type",
		},
		{
			name: "wrong content union member",
			item: `{"id":"rs_1","type":"reasoning","summary":[],"content":[{"type":"summary_text","text":"detail"}]}`,
			path: "$.output[0].content[0].type",
		},
		{
			name: "invalid status",
			item: `{"id":"rs_1","type":"reasoning","summary":[],"status":"done"}`,
			path: "$.output[0].status",
		},
	} {
		t.Run("rejected/"+test.name, func(t *testing.T) {
			_, err := converter.ToClientResponse(context.Background(), responseWithItem(test.item), core.ConversionOptions{})
			if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrUpstreamResponse at %s", err, test.path)
			}
		})
	}
}
