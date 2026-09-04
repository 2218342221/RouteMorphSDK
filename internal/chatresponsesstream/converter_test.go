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

func TestReasoningSummaryLifecycleAndTerminalOutput(t *testing.T) {
	converter := New(Options{})
	frames := convertAll(t, converter,
		frame(`{"id":"chatcmpl-reason","created":8,"model":"m","choices":[{"index":0,"delta":{"reasoning_content":"plan "},"finish_reason":null}]}`),
		frame(`{"id":"chatcmpl-reason","model":"m","choices":[{"index":0,"delta":{"reasoning_content":"done"},"finish_reason":"stop"}]}`),
		doneFrame(),
	)
	wantEvents := []string{
		"response.created", "response.in_progress", "response.output_item.added",
		"response.reasoning_summary_part.added", "response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.delta", "response.reasoning_summary_text.done",
		"response.reasoning_summary_part.done", "response.output_item.done", "response.completed",
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
	summary := reasoning["summary"].([]any)
	if len(summary) != 1 || summary[0].(map[string]any)["text"] != "plan done" {
		t.Fatalf("reasoning summary = %#v", summary)
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
