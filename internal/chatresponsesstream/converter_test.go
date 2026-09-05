package chatresponsesstream

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestTextLifecycleAndUsage(t *testing.T) {
	converter := New(Options{ClientModel: "client-model"})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-1","object":"chat.completion.chunk","created":7,"model":"provider-model","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`),
		frame(`{"id":"chatcmpl-1","model":"provider-model","choices":[{"index":0,"delta":{"content":"hel"},"finish_reason":null}]}`),
		frame(`{"id":"chatcmpl-1","model":"provider-model","choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":0}}}`),
		doneFrame(),
	)

	wantEvents := []string{
		"response.created", "response.in_progress", "response.output_item.added",
		"response.content_part.added", "response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.completed",
	}
	assertEvents(t, frames, wantEvents)
	assertSequence(t, frames)
	terminal := decode(t, frames[len(frames)-1])
	response := terminal["response"].(map[string]any)
	if response["model"] != "client-model" || response["status"] != "completed" {
		t.Fatalf("terminal response = %#v", response)
	}
	usage := response["usage"].(map[string]any)
	if usage["total_tokens"] != float64(5) {
		t.Fatalf("usage = %#v", usage)
	}
	output := response["output"].([]any)
	content := output[0].(map[string]any)["content"].([]any)
	if content[0].(map[string]any)["text"] != "hello" {
		t.Fatalf("output = %#v", output)
	}
}

func TestRefusalLifecycle(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-r","created":8,"model":"m","choices":[{"index":0,"delta":{"refusal":"not "},"finish_reason":null}]}`),
		frame(`{"id":"chatcmpl-r","model":"m","choices":[{"index":0,"delta":{"refusal":"allowed"},"finish_reason":"stop"}]}`),
		doneFrame(),
	)
	assertContainsEvents(t, frames, "response.refusal.delta", "response.refusal.done", "response.content_part.done", "response.completed")
	terminal := decode(t, frames[len(frames)-1])
	response := terminal["response"].(map[string]any)
	output := response["output"].([]any)
	part := output[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if part["type"] != "refusal" || part["refusal"] != "not allowed" {
		t.Fatalf("refusal part = %#v", part)
	}
}

func TestReasoningTextLifecycleAndTerminalOutput(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-reason","created":8,"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"plan "},"finish_reason":null}]}`),
		frame(`{"id":"chatcmpl-reason","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"done"},"finish_reason":"stop"}]}`),
		doneFrame(),
	)
	wantEvents := []string{
		"response.created", "response.in_progress", "response.output_item.added",
		"response.content_part.added", "response.reasoning_text.delta",
		"response.reasoning_text.delta", "response.reasoning_text.done",
		"response.content_part.done", "response.output_item.done", "response.completed",
	}
	assertEvents(t, frames, wantEvents)
	assertSequence(t, frames)

	terminal := decode(t, frames[len(frames)-1])
	output := terminal["response"].(map[string]any)["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output = %#v", output)
	}
	reasoning := output[0].(map[string]any)
	if reasoning["type"] != "reasoning" || reasoning["status"] != "completed" {
		t.Fatalf("reasoning item = %#v", reasoning)
	}
	if summary := reasoning["summary"].([]any); len(summary) != 0 {
		t.Fatalf("reasoning summary = %#v", summary)
	}
	content := reasoning["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["type"] != "reasoning_text" || content[0].(map[string]any)["text"] != "plan done" {
		t.Fatalf("reasoning content = %#v", content)
	}
}

func TestToolCallLifecycle(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-t","created":9,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]},"finish_reason":null}]}`),
		frame(`{"id":"chatcmpl-t","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`),
		doneFrame(),
	)
	assertContainsEvents(t, frames, "response.output_item.added", "response.function_call_arguments.delta", "response.function_call_arguments.done", "response.output_item.done", "response.completed")

	var done map[string]any
	for _, event := range frames {
		if event.Event == "response.function_call_arguments.done" {
			done = decode(t, event)
		}
	}
	if done["name"] != "lookup" || done["arguments"] != `{"q":1}` {
		t.Fatalf("arguments.done = %#v", done)
	}
	terminal := decode(t, frames[len(frames)-1])
	output := terminal["response"].(map[string]any)["output"].([]any)
	tool := output[0].(map[string]any)
	if tool["call_id"] != "call_1" || tool["status"] != "completed" {
		t.Fatalf("tool output = %#v", tool)
	}
}

func TestToolCallIDCannotBeReusedAcrossIndexes(t *testing.T) {
	converter := New(Options{})
	first, diagnostics, err := converter.Convert(context.Background(), frame(`{"id":"chatcmpl-duplicate","created":9,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"first","arguments":"{}"}}]},"finish_reason":null}]}`))
	if err != nil || len(diagnostics) != 0 || len(first) == 0 {
		t.Fatalf("first Convert() frames=%#v diagnostics=%#v error=%v", first, diagnostics, err)
	}

	frames, diagnostics, err := converter.Convert(context.Background(), frame(`{"id":"chatcmpl-duplicate","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_1","type":"function","function":{"name":"second","arguments":"{}"}}]},"finish_reason":null}]}`))
	if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), `tool call id "call_1" is reused at indexes 0 and 1`) {
		t.Fatalf("second Convert() frames=%#v diagnostics=%#v error=%v, want duplicate call-id error", frames, diagnostics, err)
	}
}

func TestToolCallIDMayRepeatAtSameIndex(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-repeat","created":9,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{"}}]},"finish_reason":null}]}`),
		frame(`{"id":"chatcmpl-repeat","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"arguments":"}"}}]},"finish_reason":"tool_calls"}]}`),
		doneFrame(),
	)
	assertContainsEvents(t, frames, "response.function_call_arguments.done", "response.completed")
}

func TestEmptyToolArgumentsBecomeJSONObject(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-e","created":9,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"ping","arguments":""}}]},"finish_reason":"tool_calls"}]}`),
		doneFrame(),
	)
	for _, event := range frames {
		if event.Event == "response.function_call_arguments.done" {
			if got := decode(t, event)["arguments"]; got != "{}" {
				t.Fatalf("arguments = %#v", got)
			}
			return
		}
	}
	t.Fatal("missing function_call_arguments.done")
}

func TestSparseToolIndexIsClosed(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-s","created":9,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":4,"id":"call_4","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`),
		doneFrame(),
	)
	assertContainsEvents(t, frames, "response.function_call_arguments.done", "response.output_item.done", "response.completed")
}

func TestInvalidToolArgumentsFailBeforeDoneEvents(t *testing.T) {
	converter := New(Options{})
	_, _, err := converter.Convert(context.Background(), frame(`{"id":"chatcmpl-bad","created":9,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{} trailing"}}]},"finish_reason":"tool_calls"}]}`))
	if !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("Convert() error = %v, want ErrInvalidPayload", err)
	}
}

func TestLengthFinishBecomesIncomplete(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-l","created":10,"model":"m","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":"length"}]}`),
		doneFrame(),
	)
	if frames[len(frames)-1].Event != "response.incomplete" {
		t.Fatalf("last event = %q", frames[len(frames)-1].Event)
	}
	response := decode(t, frames[len(frames)-1])["response"].(map[string]any)
	details := response["incomplete_details"].(map[string]any)
	if details["reason"] != "max_output_tokens" {
		t.Fatalf("incomplete_details = %#v", details)
	}
}

func TestUpstreamErrorReturnsUpstreamResponseError(t *testing.T) {
	converter := New(Options{})
	frames, diagnostics, err := converter.Convert(context.Background(), frame(`{"error":{"message":"overloaded","type":"server_error","code":"busy","param":"model"}}`))
	if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("Convert() frames=%#v diagnostics=%#v error=%v, want ErrUpstreamResponse", frames, diagnostics, err)
	}
}

func TestTextLogprobsAppearInDeltaDoneAndTerminal(t *testing.T) {
	const entry = `{"token":"hello","bytes":[104,101,108,108,111],"logprob":-0.25,"top_logprobs":[{"token":"hello","bytes":[104,101,108,108,111],"logprob":-0.25}]}`
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-lp","created":11,"model":"m","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null,"logprobs":{"content":[`+entry+`],"refusal":null}}]}`),
		frame(`{"id":"chatcmpl-lp","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		doneFrame(),
	)
	for _, eventName := range []string{"response.output_text.delta", "response.output_text.done", "response.completed"} {
		found := false
		for _, event := range frames {
			if event.Event == eventName {
				found = strings.Contains(string(event.Data), `"logprobs":[`+entry+`]`)
			}
		}
		if !found {
			t.Fatalf("%s did not preserve logprobs: %s", eventName, streamText(frames))
		}
	}
}

func TestMissingInitialIDUsesStableFallback(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`),
		doneFrame(),
	)
	created := decode(t, frames[0])["response"].(map[string]any)
	completed := decode(t, frames[len(frames)-1])["response"].(map[string]any)
	if created["id"] == "" || created["id"] != completed["id"] {
		t.Fatalf("fallback response ids differ: created=%#v completed=%#v", created["id"], completed["id"])
	}
}

func TestUnknownAndMalformedInputsFailClosed(t *testing.T) {
	tests := []struct {
		name  string
		input core.Frame
		kind  error
	}{
		{name: "unknown event", input: core.Frame{Event: "ping", Data: []byte(`{}`)}, kind: core.ErrInvalidPayload},
		{name: "unknown object", input: frame(`{"id":"x","mystery":null}`), kind: core.ErrInvalidPayload},
		{name: "unknown top level field", input: frame(`{"id":"x","model":"m","choices":[{"index":0,"delta":{},"finish_reason":null}],"vendor_extension":{"enabled":true}}`), kind: core.ErrUnsupported},
		{name: "unknown delta", input: frame(`{"id":"x","model":"m","choices":[{"index":0,"delta":{"audio":{"id":"a"}},"finish_reason":null}]}`), kind: core.ErrUnsupported},
		{name: "multiple choices", input: frame(`{"id":"x","model":"m","choices":[{"index":1,"delta":{"content":"x"},"finish_reason":null}]}`), kind: core.ErrUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := New(Options{}).Convert(context.Background(), test.input)
			if !errors.Is(err, test.kind) {
				t.Fatalf("Convert() error = %v, want %v", err, test.kind)
			}
		})
	}
}

func TestKnownChatChunkMetadataIsAccepted(t *testing.T) {
	converter := New(Options{})
	_, _, err := converter.Convert(context.Background(), frame(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","system_fingerprint":"fp","service_tier":"default","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`))
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
}

func TestOpenAIV355ChunkEnvelopeIsMappedOrDiagnosed(t *testing.T) {
	converter := New(Options{})
	frames, diagnostics, err := converter.Convert(context.Background(), frame(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","service_tier":"priority","moderation":{"input":{"type":"moderation_results","model":"omni-moderation-latest","results":[{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false}]}},"system_fingerprint":"fp","obfuscation":"padding","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 2 || diagnostics[0].Code != "chat_system_fingerprint_not_representable" || diagnostics[1].Code != "chat_stream_obfuscation_not_representable" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	if len(frames) == 0 || !strings.Contains(streamText(frames), `"service_tier":"priority"`) || !strings.Contains(streamText(frames), `"moderation":`) {
		t.Fatalf("Responses events did not preserve envelope fields: %s", streamText(frames))
	}
}

func TestOpenAIV355ModerationOnlyChunksAreAccumulated(t *testing.T) {
	converter := New(Options{})
	result := `{"type":"moderation_result","model":"omni-moderation-latest","categories":{},"category_applied_input_types":{},"category_scores":{},"flagged":false}`
	for _, side := range []string{"input", "output"} {
		chunk := `{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[],"moderation":{"` + side + `":{"type":"moderation_results","model":"omni-moderation-latest","results":[` + result + `]}}}`
		frames, _, err := converter.Convert(context.Background(), frame(chunk))
		if err != nil || len(frames) != 0 {
			t.Fatalf("%s moderation-only chunk frames=%#v error=%v", side, frames, err)
		}
	}
	if _, _, err := converter.Convert(context.Background(), frame(`{"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)); err != nil {
		t.Fatal(err)
	}
	frames, _, err := converter.Convert(context.Background(), core.Frame{Done: true, Data: []byte("[DONE]")})
	if err != nil || !strings.Contains(streamText(frames), `"moderation":{"input":`) || !strings.Contains(streamText(frames), `"output":`) {
		t.Fatalf("terminal moderation frames=%s error=%v", streamText(frames), err)
	}
}

func TestOpenAIV355ChunkUsageFailsClosed(t *testing.T) {
	converter := New(Options{})
	if _, _, err := converter.Convert(context.Background(), frame(`{"id":"x","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err := converter.Convert(context.Background(), frame(`{"id":"x","model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"completion_tokens_details":{"accepted_prediction_tokens":1}}}`))
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("Convert() error = %v, want ErrUnsupported", err)
	}
}

func TestOpenAIV355ChunkCommonUsageIsValidated(t *testing.T) {
	converter := New(Options{})
	_, _, err := converter.Convert(context.Background(), frame(`{"id":"x","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"prompt_tokens_details":{"cached_tokens":2}}}`))
	if !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("Convert() error = %v, want ErrUpstreamResponse", err)
	}
}

func TestFinalizeRequiresFinishReason(t *testing.T) {
	converter := New(Options{})
	if _, _, err := converter.Convert(context.Background(), frame(`{"id":"x","model":"m","choices":[{"index":0,"delta":{"content":"x"},"finish_reason":null}]}`)); err != nil {
		t.Fatal(err)
	}
	_, _, err := converter.Finalize(context.Background())
	if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), "finish reason") {
		t.Fatalf("Finalize() error = %v, want ErrUpstreamResponse", err)
	}
}

func convertAll(t *testing.T, converter *Converter, inputs ...core.Frame) []core.Frame {
	t.Helper()
	var result []core.Frame
	for _, input := range inputs {
		frames, diagnostics, err := converter.Convert(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if len(diagnostics) != 0 {
			t.Fatalf("diagnostics = %#v", diagnostics)
		}
		result = append(result, frames...)
	}
	return result
}

func frame(data string) core.Frame {
	return core.Frame{Data: []byte(data)}
}

func doneFrame() core.Frame {
	return core.Frame{Data: []byte("[DONE]"), Done: true}
}

func decode(t *testing.T, frame core.Frame) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(frame.Data, &result); err != nil {
		t.Fatalf("decode %q: %v", frame.Data, err)
	}
	return result
}

func assertEvents(t *testing.T, frames []core.Frame, want []string) {
	t.Helper()
	if len(frames) != len(want) {
		t.Fatalf("events = %v, want %v", eventNames(frames), want)
	}
	for index := range want {
		if frames[index].Event != want[index] {
			t.Fatalf("events = %v, want %v", eventNames(frames), want)
		}
	}
}

func assertContainsEvents(t *testing.T, frames []core.Frame, want ...string) {
	t.Helper()
	names := eventNames(frames)
	for _, event := range want {
		found := false
		for _, got := range names {
			found = found || got == event
		}
		if !found {
			t.Fatalf("events %v do not contain %q", names, event)
		}
	}
}

func assertSequence(t *testing.T, frames []core.Frame) {
	t.Helper()
	for index, event := range frames {
		payload := decode(t, event)
		if payload["sequence_number"] != float64(index) {
			t.Fatalf("event %d sequence_number = %#v", index, payload["sequence_number"])
		}
		if payload["type"] != event.Event {
			t.Fatalf("event %d type = %#v, Event = %q", index, payload["type"], event.Event)
		}
	}
}

func eventNames(frames []core.Frame) []string {
	names := make([]string, len(frames))
	for index := range frames {
		names[index] = frames[index].Event
	}
	return names
}

func streamText(frames []core.Frame) string {
	var result strings.Builder
	for _, frame := range frames {
		result.Write(frame.Data)
	}
	return result.String()
}
