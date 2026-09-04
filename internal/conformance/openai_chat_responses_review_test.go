package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestChatToResponsesPreservesCompatibleRequestFieldsAndParameterlessTools(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"model":"public","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"ping","arguments":""}}]},{"role":"tool","tool_call_id":"call_1","content":"pong"}],
		"tools":[{"type":"function","function":{"name":"ping","parameters":{}}}],"n":1,"stream":true,"stream_options":{"include_usage":true},
		"frequency_penalty":0.2,"presence_penalty":0.3,"verbosity":"low","user":"u1","service_tier":"flex","store":false,
		"prompt_cache_key":"cache","prompt_cache_retention":"24h","safety_identifier":"safe"
	}`)
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolResponses, body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(execution.Result.Body, &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"frequency_penalty", "presence_penalty", "user", "service_tier", "store", "prompt_cache_key", "prompt_cache_retention", "safety_identifier"} {
		if _, ok := got[field]; !ok {
			t.Errorf("missing converted field %q in %s", field, execution.Result.Body)
		}
	}
	if !strings.Contains(string(execution.Result.Body), `"verbosity":"low"`) {
		t.Fatalf("verbosity not converted: %s", execution.Result.Body)
	}
	if !strings.Contains(string(execution.Result.Body), `"properties":{}`) || !strings.Contains(string(execution.Result.Body), `"type":"object"`) {
		t.Fatalf("parameterless tool schema not normalized: %s", execution.Result.Body)
	}
	if !strings.Contains(string(execution.Result.Body), `"arguments":"{}"`) {
		t.Fatalf("empty tool arguments not normalized: %s", execution.Result.Body)
	}
	if !strings.Contains(string(execution.Result.Body), `"strict":false`) {
		t.Fatalf("omitted Chat strict must be made explicit for Responses: %s", execution.Result.Body)
	}
}

func TestResponsesToChatGroupsToolCallsAndPreservesArrayToolOutput(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{
		"model":"public","input":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"checking"}]},
			{"type":"function_call","call_id":"call_1","name":"one","arguments":""},
			{"type":"function_call","call_id":"call_2","name":"two","arguments":"{\"x\":1}"},
			{"type":"function_call_output","call_id":"call_2","output":[{"type":"input_text","text":"ok"}]}
		],"tools":[{"type":"function","name":"one","strict":false}],"text":{"verbosity":"medium"}
	}`)
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got chatRequest
	if err := json.Unmarshal(execution.Result.Body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 2 || len(got.Messages[0].ToolCalls) != 2 || got.Messages[1].Role != "tool" {
		t.Fatalf("tool conversation was fragmented: %s", execution.Result.Body)
	}
	if rawString(got.Verbosity) != "medium" {
		t.Fatalf("verbosity not preserved: %s", execution.Result.Body)
	}
	if rawString(got.Messages[1].Content) != "ok" {
		t.Fatalf("array tool output not preserved as Chat content: %s", got.Messages[1].Content)
	}
	if !strings.Contains(string(execution.Result.Body), `"properties":{}`) || !strings.Contains(string(execution.Result.Body), `"type":"object"`) {
		t.Fatalf("parameterless Responses tool schema not normalized: %s", execution.Result.Body)
	}
}

func TestChatResponsesLogprobsRequestsArePreserved(t *testing.T) {
	harness, _ := newTestRouterHarness()
	requests := []struct {
		From, To Protocol
		body     string
		want     []string
	}{
		{ProtocolChat, ProtocolResponses, `{"model":"m","messages":[{"role":"user","content":"hi"}],"logprobs":true,"top_logprobs":3}`, []string{`"include":["message.output_text.logprobs"]`, `"top_logprobs":3`}},
		{ProtocolResponses, ProtocolChat, `{"model":"m","input":"hi","include":["message.output_text.logprobs"],"top_logprobs":3}`, []string{`"logprobs":true`, `"top_logprobs":3`}},
	}
	for _, request := range requests {
		execution, err := harness.ToUpstreamRequest(context.Background(), request.From, request.To, []byte(request.body), conversionOptions{})
		if err != nil {
			t.Fatalf("ToUpstreamRequest(%s,%s) error = %v", request.From, request.To, err)
		}
		for _, want := range request.want {
			if !strings.Contains(string(execution.Result.Body), want) {
				t.Errorf("ToUpstreamRequest(%s,%s) missing %s in %s", request.From, request.To, want, execution.Result.Body)
			}
		}
	}
}

func TestResponsesToChatRejectsUnrepresentableReasoningAndInclude(t *testing.T) {
	harness, _ := newTestRouterHarness()
	for _, body := range []string{
		`{"model":"x","input":"hi","reasoning":{"effort":"high","summary":"detailed"}}`,
		`{"model":"x","input":"hi","reasoning":{"mode":"trace"}}`,
		`{"model":"x","input":"hi","include":["reasoning.encrypted_content"]}`,
		`{"model":"x","input":"hi","stream_options":{"include_obfuscation":true}}`,
	} {
		_, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, []byte(body), conversionOptions{})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("body=%s error=%v, want ErrUnsupported", body, err)
		}
	}
}

func TestChatToResponsesStreamIsIncrementalAndComplete(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolResponses, ProtocolChat)
	stream, err := harness.NewResponseStream(context.Background(), plan, conversionOptions{Exchange: exchangeMetadata{ClientModel: "public"}})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []streamFrame{
		{Data: []byte(`{"id":"chat_1","object":"chat.completion.chunk","created":7,"model":"provider","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)},
		{Data: []byte(`{"id":"chat_1","object":"chat.completion.chunk","created":7,"model":"provider","choices":[{"index":0,"delta":{"reasoning_content":"think","content":"hello","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ping","arguments":""}}]},"finish_reason":null}]}`)},
		{Data: []byte(`{"id":"chat_1","object":"chat.completion.chunk","created":7,"model":"provider","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"completion_tokens_details":{"reasoning_tokens":1}}}`)},
		{Data: []byte(`[DONE]`), Done: true},
	}
	var frames []streamFrame
	for index, input := range inputs {
		got, diagnostics, err := stream.Convert(context.Background(), input)
		if err != nil {
			t.Fatalf("input %d: %v", index, err)
		}
		if len(diagnostics) != 0 {
			t.Fatalf("input %d diagnostics=%#v", index, diagnostics)
		}
		frames = append(frames, got...)
	}
	if _, _, err := stream.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(frames) < 8 || frames[0].Event != "response.created" {
		t.Fatalf("stream was not incremental: %#v", frames)
	}
	joined := ""
	eventPositions := make(map[string]int)
	functionDoneHasName := false
	for _, frame := range frames {
		joined += frame.Event + string(frame.Data)
		if _, seen := eventPositions[frame.Event]; !seen {
			eventPositions[frame.Event] = len(eventPositions)
		}
		if frame.Event == "response.function_call_arguments.done" {
			var event struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(frame.Data, &event)
			functionDoneHasName = event.Name == "ping"
		}
	}
	for _, want := range []string{"response.reasoning_summary_part.added", "response.reasoning_summary_text.delta", "response.reasoning_summary_part.done", "response.output_text.delta", "response.function_call_arguments.done", "response.completed", `"name":"ping"`, `"arguments":"{}"`, `"total_tokens":5`, `"model":"public"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("stream missing %q: %s", want, joined)
		}
	}
	if !(eventPositions["response.reasoning_summary_part.added"] < eventPositions["response.reasoning_summary_text.delta"] &&
		eventPositions["response.reasoning_summary_text.delta"] < eventPositions["response.reasoning_summary_text.done"] &&
		eventPositions["response.reasoning_summary_text.done"] < eventPositions["response.reasoning_summary_part.done"]) {
		t.Fatalf("reasoning event lifecycle is out of order: %#v", eventPositions)
	}
	if !functionDoneHasName {
		t.Fatalf("function_call_arguments.done omitted name: %s", joined)
	}
}

func TestResponsesToChatRequestUsesResolvedStreamForUsage(t *testing.T) {
	harness, _ := newTestRouterHarness()
	for _, test := range []struct {
		name          string
		body          string
		exchange      exchangeMetadata
		wantStream    bool
		wantUsageOpts bool
	}{
		{name: "override_on", body: `{"model":"m","input":"hi","stream":false}`, exchange: exchangeMetadata{Stream: true, StreamSet: true}, wantStream: true, wantUsageOpts: true},
		{name: "override_off", body: `{"model":"m","input":"hi","stream":true}`, exchange: exchangeMetadata{Stream: false, StreamSet: true}, wantStream: false, wantUsageOpts: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, []byte(test.body), conversionOptions{Exchange: test.exchange})
			if err != nil {
				t.Fatal(err)
			}
			var got chatRequest
			if err := json.Unmarshal(execution.Result.Body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Stream != test.wantStream || (got.StreamOptions != nil) != test.wantUsageOpts {
				t.Fatalf("stream=%v stream_options=%#v body=%s", got.Stream, got.StreamOptions, execution.Result.Body)
			}
		})
	}
}

func TestResponsesToChatStreamUsageFollowsOriginalChatRequest(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolChat, ProtocolResponses)
	terminal := streamFrame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"model":"provider","status":"completed","output":[],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`)}
	for _, includeUsage := range []bool{false, true} {
		stream, err := harness.NewResponseStream(context.Background(), plan, conversionOptions{Exchange: exchangeMetadata{ClientModel: "public", ChatStreamIncludeUsage: includeUsage, ChatStreamIncludeUsageSet: true}})
		if err != nil {
			t.Fatal(err)
		}
		frames, _, err := stream.Convert(context.Background(), terminal)
		if err != nil {
			t.Fatal(err)
		}
		jsonFrames := frames[:len(frames)-1]
		usageChunks := 0
		for index, frame := range jsonFrames {
			var chunk map[string]json.RawMessage
			if err := json.Unmarshal(frame.Data, &chunk); err != nil {
				t.Fatalf("frame %d: %v", index, err)
			}
			_, hasUsage := chunk["usage"]
			if includeUsage != hasUsage {
				t.Fatalf("include_usage=%v frame=%s has_usage=%v", includeUsage, frame.Data, hasUsage)
			}
			var choices []json.RawMessage
			_ = json.Unmarshal(chunk["choices"], &choices)
			if len(choices) == 0 {
				usageChunks++
				if string(chunk["usage"]) == "null" || !strings.Contains(string(chunk["usage"]), `"total_tokens":5`) {
					t.Fatalf("invalid usage-only chunk: %s", frame.Data)
				}
			}
		}
		if includeUsage && usageChunks != 1 || !includeUsage && usageChunks != 0 {
			t.Fatalf("include_usage=%v usage_chunks=%d frames=%#v", includeUsage, usageChunks, frames)
		}
	}
}

func TestDeprecatedChatFunctionCallFailsClosed(t *testing.T) {
	harness, _ := newTestRouterHarness()
	request := []byte(`{"model":"m","messages":[{"role":"assistant","content":null,"function_call":{"name":"old","arguments":"{}"}}]}`)
	if _, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolResponses, request, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("request error=%v, want ErrUnsupported", err)
	}
	plan, _ := harness.catalog().Plan(ProtocolResponses, ProtocolChat)
	response := []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":null,"function_call":{"name":"old","arguments":"{}"}},"finish_reason":"function_call"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	if _, err := harness.ToClientResponse(context.Background(), plan, response, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("response error=%v, want ErrUnsupported", err)
	}
	stream, _ := harness.NewResponseStream(context.Background(), plan, conversionOptions{})
	_, _, err := stream.Convert(context.Background(), streamFrame{Data: []byte(`{"id":"chat_1","model":"m","choices":[{"index":0,"delta":{"function_call":{"name":"old","arguments":"{}"}},"finish_reason":null}]}`)})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("stream error=%v, want ErrUnsupported", err)
	}
}

func TestOpenAIFunctionStrictDefaultsArePreserved(t *testing.T) {
	harness, _ := newTestRouterHarness()
	chat := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{}}}]}`)
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, ProtocolResponses, chat, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(execution.Result.Body), `"strict":false`) {
		t.Fatalf("Chat default strict was not made explicit: %s", execution.Result.Body)
	}
	responses := []byte(`{"model":"m","input":"hi","tools":[{"type":"function","name":"f","parameters":{}}]}`)
	if _, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, responses, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Responses omitted strict error=%v, want ErrUnsupported", err)
	}
}

func TestResponsesToolOutputRejectsNonTextChatToolMessage(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{"model":"m","input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_image","image_url":"data:image/png;base64,aW1n"}]}]}`)
	if _, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolChat, body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error=%v, want ErrUnsupported", err)
	}
}

func TestChatToResponsesStreamContentIndexesFollowFirstAppearance(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolResponses, ProtocolChat)
	stream, _ := harness.NewResponseStream(context.Background(), plan, conversionOptions{})
	inputs := []streamFrame{
		{Data: []byte(`{"id":"chat_1","created":1,"model":"m","choices":[{"index":0,"delta":{"refusal":"no"},"finish_reason":null}]}`)},
		{Data: []byte(`{"id":"chat_1","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)},
		{Data: []byte(`[DONE]`), Done: true},
	}
	var events []streamFrame
	for _, input := range inputs {
		frames, _, err := stream.Convert(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, frames...)
	}
	wantIndexes := map[string]int{"response.refusal.delta": 0, "response.output_text.delta": 1, "response.refusal.done": 0, "response.output_text.done": 1}
	seenIndexes := make(map[string]bool)
	for _, event := range events {
		want, ok := wantIndexes[event.Event]
		if !ok {
			continue
		}
		var fields struct {
			ContentIndex int `json:"content_index"`
		}
		if err := json.Unmarshal(event.Data, &fields); err != nil || fields.ContentIndex != want {
			t.Fatalf("event=%s data=%s index=%d want=%d err=%v", event.Event, event.Data, fields.ContentIndex, want, err)
		}
		seenIndexes[event.Event] = true
	}
	if len(seenIndexes) != len(wantIndexes) {
		t.Fatalf("missing indexed events: seen=%#v want=%#v", seenIndexes, wantIndexes)
	}
	for _, event := range events {
		if event.Event != "response.completed" {
			continue
		}
		var terminal struct {
			Response responsesResponse `json:"response"`
		}
		if err := json.Unmarshal(event.Data, &terminal); err != nil {
			t.Fatal(err)
		}
		var content []responsesContentPart
		if err := json.Unmarshal(terminal.Response.Output[0].Content, &content); err != nil {
			t.Fatal(err)
		}
		if len(content) != 2 || content[0].Type != "refusal" || content[1].Type != "output_text" {
			t.Fatalf("terminal content order=%#v", content)
		}
	}
}

func TestResponsesSSEFunctionArgumentsDoneIncludesName(t *testing.T) {
	events, _, err := renderNativeResponseStream(ProtocolResponses, []byte(`{"id":"resp_1","object":"response","model":"m","status":"completed","output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"ping","arguments":"{}"}],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Event != "response.function_call_arguments.done" {
			continue
		}
		var fields struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(event.Data, &fields); err != nil || fields.Name != "ping" {
			t.Fatalf("event=%s name=%q err=%v", event.Data, fields.Name, err)
		}
		return
	}
	t.Fatal("response.function_call_arguments.done was not emitted")
}

func TestOpenAIErrorEnvelopesAndIncompleteStreamAreHandled(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolResponses, ProtocolChat)
	_, err := harness.ToClientResponse(context.Background(), plan, []byte(`{"error":{"message":"provider exploded","type":"server_error"}}`), conversionOptions{})
	if !errors.Is(err, ErrUpstreamResponse) || !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("non-stream error=%v", err)
	}

	plan, _ = harness.catalog().Plan(ProtocolChat, ProtocolResponses)
	stream, _ := harness.NewResponseStream(context.Background(), plan, conversionOptions{})
	_, _, _ = stream.Convert(context.Background(), streamFrame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"m","created_at":1,"status":"in_progress","output":[]}}`)})
	frames, _, err := stream.Convert(context.Background(), streamFrame{Event: "response.incomplete", Data: []byte(`{"type":"response.incomplete","response":{"id":"resp_1","model":"m","created_at":1,"status":"incomplete","incomplete_details":{"reason":"content_filter"},"output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 || !strings.Contains(string(frames[0].Data), `"finish_reason":"content_filter"`) || !frames[1].Done {
		t.Fatalf("incomplete stream frames=%#v", frames)
	}
}
