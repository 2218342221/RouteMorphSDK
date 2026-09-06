package relay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestResponsesNativeStreamAcceptsCompleteItemAndContentLifecycles(t *testing.T) {
	v := newResponsesLifecycleValidator()
	customAdded := responsesLifecycleItem("custom_1", "custom_tool_call")
	customAdded["status"] = "in_progress"
	events := []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),

		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": responsesLifecycleItem("reasoning_1", "reasoning")}),
		responsesLifecycleFrame(t, "response.reasoning_summary_part.added", 2, map[string]any{"output_index": 0, "item_id": "reasoning_1", "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": ""}}),
		responsesLifecycleFrame(t, "response.reasoning_summary_text.delta", 3, map[string]any{"output_index": 0, "item_id": "reasoning_1", "summary_index": 0, "delta": "summary"}),
		responsesLifecycleFrame(t, "response.reasoning_summary_text.done", 4, map[string]any{"output_index": 0, "item_id": "reasoning_1", "summary_index": 0, "text": "summary"}),
		responsesLifecycleFrame(t, "response.reasoning_summary_part.done", 5, map[string]any{"output_index": 0, "item_id": "reasoning_1", "summary_index": 0, "part": map[string]any{"type": "summary_text", "text": "summary"}}),
		responsesLifecycleFrame(t, "response.content_part.added", 6, map[string]any{"output_index": 0, "item_id": "reasoning_1", "content_index": 0, "part": map[string]any{"type": "reasoning_text", "text": ""}}),
		responsesLifecycleFrame(t, "response.reasoning_text.delta", 7, map[string]any{"output_index": 0, "item_id": "reasoning_1", "content_index": 0, "delta": "details"}),
		responsesLifecycleFrame(t, "response.reasoning_text.done", 8, map[string]any{"output_index": 0, "item_id": "reasoning_1", "content_index": 0, "text": "details"}),
		responsesLifecycleFrame(t, "response.content_part.done", 9, map[string]any{"output_index": 0, "item_id": "reasoning_1", "content_index": 0, "part": map[string]any{"type": "reasoning_text", "text": "details"}}),
		responsesLifecycleFrame(t, "response.output_item.done", 10, map[string]any{"output_index": 0, "item": responsesLifecycleItem("reasoning_1", "reasoning")}),

		responsesLifecycleFrame(t, "response.output_item.added", 11, map[string]any{"output_index": 1, "item": responsesLifecycleItem("function_1", "function_call")}),
		responsesLifecycleFrame(t, "response.function_call_arguments.delta", 12, map[string]any{"output_index": 1, "item_id": "function_1", "delta": "{}"}),
		// Some Responses-compatible providers omit name here and carry it only
		// on the surrounding output_item events.
		responsesLifecycleFrame(t, "response.function_call_arguments.done", 13, map[string]any{"output_index": 1, "item_id": "function_1", "arguments": "{}"}),
		responsesLifecycleFrame(t, "response.output_item.done", 14, map[string]any{"output_index": 1, "item": responsesLifecycleItem("function_1", "function_call")}),

		responsesLifecycleFrame(t, "response.output_item.added", 15, map[string]any{"output_index": 2, "item": customAdded}),
		responsesLifecycleFrame(t, "response.custom_tool_call_input.delta", 16, map[string]any{"output_index": 2, "item_id": "custom_1", "delta": "echo hi"}),
		responsesLifecycleFrame(t, "response.custom_tool_call_input.done", 17, map[string]any{"output_index": 2, "item_id": "custom_1", "input": "echo hi"}),
		responsesLifecycleFrame(t, "response.output_item.done", 18, map[string]any{"output_index": 2, "item": responsesLifecycleItem("custom_1", "custom_tool_call")}),

		responsesLifecycleFrame(t, "response.output_item.added", 19, map[string]any{"output_index": 3, "item": responsesLifecycleItem("message_1", "message")}),
		responsesLifecycleFrame(t, "response.content_part.added", 20, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}}),
		responsesLifecycleFrame(t, "response.output_text.delta", 21, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 0, "delta": "hello", "logprobs": []any{}}),
		responsesLifecycleFrame(t, "response.output_text.annotation.added", 22, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 0, "annotation_index": 0, "annotation": map[string]any{"type": "url_citation"}}),
		responsesLifecycleFrame(t, "response.output_text.done", 23, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 0, "text": "hello", "logprobs": []any{}}),
		responsesLifecycleFrame(t, "response.content_part.done", 24, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 0, "part": map[string]any{"type": "output_text", "text": "hello", "annotations": []any{}, "logprobs": []any{}}}),
		responsesLifecycleFrame(t, "response.content_part.added", 25, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 1, "part": map[string]any{"type": "refusal", "refusal": ""}}),
		responsesLifecycleFrame(t, "response.refusal.delta", 26, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 1, "delta": "no"}),
		responsesLifecycleFrame(t, "response.refusal.done", 27, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 1, "refusal": "no"}),
		responsesLifecycleFrame(t, "response.content_part.done", 28, map[string]any{"output_index": 3, "item_id": "message_1", "content_index": 1, "part": map[string]any{"type": "refusal", "refusal": "no"}}),
		responsesLifecycleFrame(t, "response.output_item.done", 29, map[string]any{"output_index": 3, "item": responsesLifecycleItem("message_1", "message")}),

		responsesLifecycleFrame(t, "response.output_item.added", 30, map[string]any{"output_index": 4, "item": responsesLifecycleItem("web_1", "web_search_call")}),
		responsesLifecycleFrame(t, "response.web_search_call.in_progress", 31, map[string]any{"output_index": 4, "item_id": "web_1"}),
		responsesLifecycleFrame(t, "response.web_search_call.searching", 32, map[string]any{"output_index": 4, "item_id": "web_1"}),
		responsesLifecycleFrame(t, "response.web_search_call.completed", 33, map[string]any{"output_index": 4, "item_id": "web_1"}),
		responsesLifecycleFrame(t, "response.output_item.done", 34, map[string]any{"output_index": 4, "item": responsesLifecycleItem("web_1", "web_search_call")}),

		responsesLifecycleFrame(t, "response.output_item.added", 35, map[string]any{"output_index": 5, "item": responsesLifecycleItem("tool_search_1", "tool_search_call")}),
		responsesLifecycleFrame(t, "response.output_item.done", 36, map[string]any{"output_index": 5, "item": responsesLifecycleItem("tool_search_1", "tool_search_call")}),
		responsesLifecycleFrame(t, "response.output_item.added", 37, map[string]any{"output_index": 6, "item": responsesLifecycleItem("tools_1", "additional_tools")}),
		responsesLifecycleFrame(t, "response.output_item.done", 38, map[string]any{"output_index": 6, "item": responsesLifecycleItem("tools_1", "additional_tools")}),

		responsesLifecycleFrame(t, "response.completed", 39, map[string]any{"response": responsesLifecycleResponseWithItems("completed",
			responsesLifecycleItem("reasoning_1", "reasoning"),
			responsesLifecycleItem("function_1", "function_call"),
			responsesLifecycleItem("custom_1", "custom_tool_call"),
			responsesLifecycleItem("message_1", "message"),
			responsesLifecycleItem("web_1", "web_search_call"),
			responsesLifecycleItem("tool_search_1", "tool_search_call"),
			responsesLifecycleItem("tools_1", "additional_tools"),
		)}),
	}
	for index, event := range events {
		if err := v.validate(context.Background(), event); err != nil {
			t.Fatalf("event %d (%s): %v", index, event.Event, err)
		}
	}
	if err := v.finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestResponsesNativeStreamRejectsInvalidLifecycle(t *testing.T) {
	created := func(t *testing.T, sequence int64) core.Frame {
		return responsesLifecycleFrame(t, "response.created", sequence, map[string]any{"response": responsesLifecycleResponse("in_progress")})
	}
	messageAdded := func(t *testing.T, sequence int64) core.Frame {
		return responsesLifecycleFrame(t, "response.output_item.added", sequence, map[string]any{"output_index": 0, "item": responsesLifecycleItem("message_1", "message")})
	}
	textAdded := func(t *testing.T, sequence int64) core.Frame {
		return responsesLifecycleFrame(t, "response.content_part.added", sequence, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}})
	}
	textDone := func(t *testing.T, sequence int64) core.Frame {
		return responsesLifecycleFrame(t, "response.output_text.done", sequence, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "text": "done", "logprobs": []any{}})
	}
	tests := []struct {
		name   string
		events func(*testing.T) []core.Frame
		want   string
	}{
		{
			name: "created must be first",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{responsesLifecycleFrame(t, "response.in_progress", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")})}
			},
			want: "response.created must be the first",
		},
		{
			name: "created must be unique",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), created(t, 1)}
			},
			want: "duplicate response.created",
		},
		{
			name: "sequence starts at zero",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 1)}
			},
			want: "contiguous sequence number 0",
		},
		{
			name: "sequence gap",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), responsesLifecycleFrame(t, "response.in_progress", 2, map[string]any{"response": responsesLifecycleResponse("in_progress")})}
			},
			want: "contiguous sequence number 1",
		},
		{
			name: "sequence duplicate",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), responsesLifecycleFrame(t, "response.in_progress", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")})}
			},
			want: "contiguous sequence number 1",
		},
		{
			name: "item done before add",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), responsesLifecycleFrame(t, "response.output_item.done", 1, map[string]any{"output_index": 0, "item": responsesLifecycleItem("message_1", "message")})}
			},
			want: "before output item 0 was added",
		},
		{
			name: "duplicate output index",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), messageAdded(t, 2)}
			},
			want: "duplicate output item index",
		},
		{
			name: "content before item",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), textAdded(t, 1)}
			},
			want: "before output item 0 was added",
		},
		{
			name: "delta before content",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), responsesLifecycleFrame(t, "response.output_text.delta", 2, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "delta": "late", "logprobs": []any{}})}
			},
			want: "before content index 0 was added",
		},
		{
			name: "mismatched item id",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), responsesLifecycleFrame(t, "response.content_part.added", 2, map[string]any{"output_index": 0, "item_id": "other", "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}})}
			},
			want: "does not match output item",
		},
		{
			name: "delta after text done",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), textAdded(t, 2), textDone(t, 3), responsesLifecycleFrame(t, "response.output_text.delta", 4, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "delta": "late", "logprobs": []any{}})}
			},
			want: "output_text stream was done",
		},
		{
			name: "delta after content done",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), textAdded(t, 2), responsesLifecycleFrame(t, "response.content_part.done", 3, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "part": map[string]any{"type": "output_text", "text": ""}}), responsesLifecycleFrame(t, "response.output_text.delta", 4, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "delta": "late", "logprobs": []any{}})}
			},
			want: "after content index 0 was done",
		},
		{
			name: "item done with open content",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), textAdded(t, 2), responsesLifecycleFrame(t, "response.output_item.done", 3, map[string]any{"output_index": 0, "item": responsesLifecycleItem("message_1", "message")})}
			},
			want: "content index 0 is still open",
		},
		{
			name: "content part done before text done",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), textAdded(t, 2), responsesLifecycleFrame(t, "response.output_text.delta", 3, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "delta": "x", "logprobs": []any{}}), responsesLifecycleFrame(t, "response.content_part.done", 4, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "part": map[string]any{"type": "output_text", "text": "x"}})}
			},
			want: "before its output_text stream was done",
		},
		{
			name: "item done before function arguments done",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": responsesLifecycleItem("function_1", "function_call")}), responsesLifecycleFrame(t, "response.function_call_arguments.delta", 2, map[string]any{"output_index": 0, "item_id": "function_1", "delta": "{}"}), responsesLifecycleFrame(t, "response.output_item.done", 3, map[string]any{"output_index": 0, "item": responsesLifecycleItem("function_1", "function_call")})}
			},
			want: "function_call_arguments stream is still open",
		},
		{
			name: "terminal before item done",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), responsesLifecycleFrame(t, "response.completed", 2, map[string]any{"response": responsesLifecycleResponse("completed")})}
			},
			want: "before output item 0 was done",
		},
		{
			name: "delta after item done",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": responsesLifecycleItem("message_1", "message")}), responsesLifecycleFrame(t, "response.output_text.delta", 3, map[string]any{"output_index": 0, "item_id": "message_1", "content_index": 0, "delta": "late", "logprobs": []any{}})}
			},
			want: "after output item 0 was done",
		},
		{
			name: "reasoning summary delta before part",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": responsesLifecycleItem("reasoning_1", "reasoning")}), responsesLifecycleFrame(t, "response.reasoning_summary_text.delta", 2, map[string]any{"output_index": 0, "item_id": "reasoning_1", "summary_index": 0, "delta": "x"})}
			},
			want: "before summary index 0 was added",
		},
		{
			name: "function delta after done",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": responsesLifecycleItem("function_1", "function_call")}), responsesLifecycleFrame(t, "response.function_call_arguments.done", 2, map[string]any{"output_index": 0, "item_id": "function_1", "name": "lookup", "arguments": "{}"}), responsesLifecycleFrame(t, "response.function_call_arguments.delta", 3, map[string]any{"output_index": 0, "item_id": "function_1", "delta": "late"})}
			},
			want: "function_call_arguments stream was done",
		},
		{
			name: "custom event on wrong item type",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), messageAdded(t, 1), responsesLifecycleFrame(t, "response.custom_tool_call_input.delta", 2, map[string]any{"output_index": 0, "item_id": "message_1", "delta": "x"})}
			},
			want: "does not apply to output item type",
		},
		{
			name: "web status after item done",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": responsesLifecycleItem("web_1", "web_search_call")}), responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": responsesLifecycleItem("web_1", "web_search_call")}), responsesLifecycleFrame(t, "response.web_search_call.searching", 3, map[string]any{"output_index": 0, "item_id": "web_1"})}
			},
			want: "after output item 0 was done",
		},
		{
			name: "event after terminal",
			events: func(t *testing.T) []core.Frame {
				return []core.Frame{created(t, 0), responsesLifecycleFrame(t, "response.completed", 1, map[string]any{"response": responsesLifecycleResponse("completed")}), responsesLifecycleFrame(t, "response.in_progress", 2, map[string]any{"response": responsesLifecycleResponse("in_progress")})}
			},
			want: "after the terminal response",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			v := newResponsesLifecycleValidator()
			var got error
			for _, event := range test.events(t) {
				got = v.validate(context.Background(), event)
				if got != nil {
					break
				}
			}
			if !errors.Is(got, core.ErrInvalidPayload) || !strings.Contains(got.Error(), test.want) {
				t.Fatalf("error = %v, want ErrInvalidPayload containing %q", got, test.want)
			}
		})
	}
}

func TestResponsesNativeStreamRejectsDoneMarkerAfterTerminal(t *testing.T) {
	v := newResponsesLifecycleValidator()
	for _, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.completed", 1, map[string]any{"response": responsesLifecycleResponse("completed")}),
	} {
		if err := v.validate(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	err := v.validate(context.Background(), core.Frame{Data: []byte("[DONE]"), Done: true})
	if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "unexpected [DONE]") {
		t.Fatalf("error = %v, want invalid [DONE] marker", err)
	}
}

func TestResponsesNativeStreamAcceptsProviderServerToolSearchWithoutCorrelationFields(t *testing.T) {
	v := newResponsesLifecycleValidator()
	call := map[string]any{
		"id": "search_1", "type": "tool_search_call", "arguments": map[string]any{"query": "weather"},
		"call_id": nil, "execution": nil, "status": "completed",
	}
	output := map[string]any{
		"id": "tools_1", "type": "tool_search_output", "call_id": nil, "execution": nil,
		"status": "completed", "tools": []any{},
	}
	for _, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": call}),
		responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": call}),
		responsesLifecycleFrame(t, "response.output_item.added", 3, map[string]any{"output_index": 1, "item": output}),
		responsesLifecycleFrame(t, "response.output_item.done", 4, map[string]any{"output_index": 1, "item": output}),
		responsesLifecycleFrame(t, "response.completed", 5, map[string]any{"response": responsesLifecycleResponseWithItems("completed", call, output)}),
	} {
		if err := v.validate(context.Background(), event); err != nil {
			t.Fatalf("%s: %v", event.Event, err)
		}
	}
}

func TestResponsesNativeStreamRejectsInvalidOutputUnionAtEveryBoundary(t *testing.T) {
	tests := []struct {
		name string
		item string
		path string
	}{
		{
			name: "malformed file search",
			item: `{"id":"fs_1","type":"file_search_call","status":"completed","queries":"not-an-array"}`,
			path: ".queries",
		},
		{
			name: "unknown discriminator",
			item: `{"id":"future_1","type":"future_call"}`,
			path: ".type",
		},
		{
			name: "configuration update is not output",
			item: `{"id":"config_1","type":"configuration_update"}`,
			path: ".type",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(test.item)
			response := json.RawMessage(`{"status":"completed","output":[` + test.item + `]}`)
			stages := []struct {
				name string
				run  func() error
			}{
				{name: "added", run: func() error { return validatePartialResponsesItem(raw) }},
				{name: "done", run: func() error { return validateCompleteResponsesItem(raw) }},
				{name: "terminal", run: func() error { return validateKnownResponsesResponse(response, true) }},
			}
			for _, stage := range stages {
				t.Run(stage.name, func(t *testing.T) {
					err := stage.run()
					if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), test.path) {
						t.Fatalf("error = %v, want ErrUpstreamResponse containing %q", err, test.path)
					}
				})
			}
		})
	}
}

func TestResponsesNativeStreamAcceptsPartialAddedForEveryV356OutputVariant(t *testing.T) {
	itemTypes := []string{
		"message", "file_search_call", "function_call", "function_call_output", "web_search_call",
		"computer_call", "computer_call_output", "reasoning", "program", "program_output",
		"tool_search_call", "tool_search_output", "additional_tools", "compaction", "image_generation_call",
		"code_interpreter_call", "local_shell_call", "local_shell_call_output", "shell_call", "shell_call_output",
		"apply_patch_call", "apply_patch_call_output", "mcp_call", "mcp_list_tools", "mcp_approval_request",
		"mcp_approval_response", "custom_tool_call", "custom_tool_call_output",
	}
	for _, itemType := range itemTypes {
		t.Run(itemType, func(t *testing.T) {
			item, err := json.Marshal(map[string]any{"id": "item_1", "type": itemType})
			if err != nil {
				t.Fatal(err)
			}
			if err := validatePartialResponsesItem(item); err != nil {
				t.Fatalf("minimal added item rejected: %v", err)
			}
		})
	}
}

func TestResponsesNativeStreamRejectsMalformedKnownFieldsOnItemAdded(t *testing.T) {
	tests := []struct {
		name  string
		item  map[string]any
		field string
	}{
		{name: "function async", item: map[string]any{"id": "fc_1", "type": "function_call", "async": "yes"}, field: ".async"},
		{name: "function caller discriminator", item: map[string]any{"id": "fc_1", "type": "function_call", "caller": map[string]any{"type": "future"}}, field: ".caller.type"},
		{name: "function namespace", item: map[string]any{"id": "fc_1", "type": "function_call", "namespace": 7}, field: ".namespace"},
		{name: "function created by", item: map[string]any{"id": "fc_1", "type": "function_call", "created_by": "model"}, field: ".created_by"},
		{name: "custom async", item: map[string]any{"id": "ctc_1", "type": "custom_tool_call", "async": []any{}}, field: ".async"},
		{name: "custom program caller", item: map[string]any{"id": "ctc_1", "type": "custom_tool_call", "caller": map[string]any{"type": "program"}}, field: ".caller.caller_id"},
		{name: "custom status", item: map[string]any{"id": "ctc_1", "type": "custom_tool_call", "status": "queued"}, field: ".status"},
		{name: "custom created by", item: map[string]any{"id": "ctc_1", "type": "custom_tool_call", "created_by": "model"}, field: ".created_by"},
		{name: "custom output created by", item: map[string]any{"id": "ctco_1", "type": "custom_tool_call_output", "created_by": false}, field: ".created_by"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			v := newResponsesLifecycleValidator()
			if err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")})); err != nil {
				t.Fatal(err)
			}
			err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{
				"output_index": 0,
				"item":         test.item,
			}))
			if !errors.Is(err, core.ErrUpstreamResponse) || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("added error = %v, want ErrUpstreamResponse containing %q", err, test.field)
			}
			if len(v.responsesItems) != 0 {
				t.Fatalf("malformed added item mutated lifecycle state: %#v", v.responsesItems)
			}
		})
	}
}

func TestResponsesNativeStreamAcceptsPartialAddedItemWithKnownOptionalFields(t *testing.T) {
	v := newResponsesLifecycleValidator()
	partial := map[string]any{
		"id": "function_1", "type": "function_call",
		"async": false, "namespace": "weather", "caller": map[string]any{"type": "direct"},
		"future_extension": map[string]any{"enabled": true},
	}
	complete := map[string]any{
		"id": "function_1", "type": "function_call", "call_id": "call_1",
		"name": "lookup", "arguments": `{}`, "status": "completed",
		"async": false, "namespace": "weather", "caller": map[string]any{"type": "direct"},
		"future_extension": map[string]any{"enabled": true},
	}
	events := []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": partial}),
		responsesLifecycleFrame(t, "response.function_call_arguments.delta", 2, map[string]any{"output_index": 0, "item_id": "function_1", "delta": `{}`}),
		responsesLifecycleFrame(t, "response.function_call_arguments.done", 3, map[string]any{"output_index": 0, "item_id": "function_1", "name": "lookup", "arguments": `{}`}),
		responsesLifecycleFrame(t, "response.output_item.done", 4, map[string]any{"output_index": 0, "item": complete}),
		responsesLifecycleFrame(t, "response.completed", 5, map[string]any{"response": responsesLifecycleResponseWithItems("completed", complete)}),
	}
	for index, event := range events {
		if err := v.validate(context.Background(), event); err != nil {
			t.Fatalf("event %d (%s): %v", index, event.Event, err)
		}
	}
	if err := v.finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestResponsesNativeStreamReconcilesTerminalOutput(t *testing.T) {
	complete := responsesLifecycleItem("function_1", "function_call")
	tests := []struct {
		name     string
		terminal []any
		want     string
	}{
		{name: "missing item", terminal: []any{}, want: "stream completed 1 items"},
		{name: "changed item", terminal: []any{map[string]any{
			"id": "function_1", "type": "function_call", "call_id": "call_function_1",
			"name": "lookup", "arguments": `{"changed":true}`, "status": "completed",
		}}, want: "does not match its output_item.done payload"},
		{name: "dropped done field", terminal: []any{map[string]any{
			"id": "function_1", "type": "function_call", "call_id": "call_function_1",
			"name": "lookup", "arguments": `{}`,
		}}, want: "does not match its output_item.done payload"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			v := newResponsesLifecycleValidator()
			var got error
			for _, event := range []core.Frame{
				responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
				responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": complete}),
				responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": complete}),
				responsesLifecycleFrame(t, "response.completed", 3, map[string]any{"response": map[string]any{"status": "completed", "output": test.terminal}}),
			} {
				got = v.validate(context.Background(), event)
				if got != nil {
					break
				}
			}
			if !errors.Is(got, core.ErrInvalidPayload) || !strings.Contains(got.Error(), test.want) {
				t.Fatalf("error = %v, want ErrInvalidPayload containing %q", got, test.want)
			}
		})
	}
}

func TestResponsesNativeStreamAcceptsTerminalItemEnrichment(t *testing.T) {
	done := responsesLifecycleItem("message_1", "message")
	done["content"] = []any{map[string]any{
		"type": "output_text", "text": "ok", "annotations": []any{},
	}}
	terminal := responsesLifecycleItem("message_1", "message")
	terminal["phase"] = "final_answer"
	terminal["future_extension"] = map[string]any{"terminal_only": true}
	terminal["content"] = []any{map[string]any{
		"type": "output_text", "text": "ok", "annotations": []any{}, "logprobs": []any{},
	}}
	v := newResponsesLifecycleValidator()
	for index, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.completed", 3, map[string]any{"response": responsesLifecycleResponseWithItems("completed", terminal)}),
	} {
		if err := v.validate(context.Background(), event); err != nil {
			t.Fatalf("event %d (%s): %v", index, event.Event, err)
		}
	}
	if err := v.finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestResponsesNativeStreamRejectsNestedKnownFieldAddedAfterItemDone(t *testing.T) {
	done := responsesLifecycleItem("web_1", "web_search_call")
	terminal := responsesLifecycleItem("web_1", "web_search_call")
	terminal["action"] = map[string]any{"type": "open_page", "url": "https://example.com"}
	v := newResponsesLifecycleValidator()
	var got error
	for _, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.completed", 3, map[string]any{"response": responsesLifecycleResponseWithItems("completed", terminal)}),
	} {
		got = v.validate(context.Background(), event)
		if got != nil {
			break
		}
	}
	if !errors.Is(got, core.ErrInvalidPayload) || !strings.Contains(got.Error(), "$.action.url was added after output_item.done") {
		t.Fatalf("error = %v, want nested known-field terminal enrichment rejection", got)
	}
}

func TestResponsesNativeStreamRejectsNestedUnknownExtensionEnrichment(t *testing.T) {
	done := responsesLifecycleItem("message_1", "message")
	done["future_extension"] = map[string]any{"stable": true}
	terminal := responsesLifecycleItem("message_1", "message")
	terminal["future_extension"] = map[string]any{"stable": true, "late": true}
	v := newResponsesLifecycleValidator()
	var got error
	for _, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.completed", 3, map[string]any{"response": responsesLifecycleResponseWithItems("completed", terminal)}),
	} {
		got = v.validate(context.Background(), event)
		if got != nil {
			break
		}
	}
	if !errors.Is(got, core.ErrInvalidPayload) || !strings.Contains(got.Error(), "$.future_extension.late was added after output_item.done") {
		t.Fatalf("error = %v, want nested extension enrichment rejection", got)
	}
}

func TestResponsesNativeStreamRejectsOtherVariantFieldAsTerminalExtension(t *testing.T) {
	done := responsesLifecycleItem("message_1", "message")
	terminal := responsesLifecycleItem("message_1", "message")
	terminal["arguments"] = `{}`
	v := newResponsesLifecycleValidator()
	var got error
	for _, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.completed", 3, map[string]any{"response": responsesLifecycleResponseWithItems("completed", terminal)}),
	} {
		got = v.validate(context.Background(), event)
		if got != nil {
			break
		}
	}
	if !errors.Is(got, core.ErrInvalidPayload) || !strings.Contains(got.Error(), "$.arguments was added after output_item.done") {
		t.Fatalf("error = %v, want other-variant known-field rejection", got)
	}
}

func TestResponsesNativeStreamAcceptsTerminalReasoningCiphertextRefresh(t *testing.T) {
	done := responsesLifecycleItem("reasoning_1", "reasoning")
	done["encrypted_content"] = "done-ciphertext"
	terminal := responsesLifecycleItem("reasoning_1", "reasoning")
	terminal["encrypted_content"] = "terminal-ciphertext"
	v := newResponsesLifecycleValidator()
	for index, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.completed", 3, map[string]any{"response": responsesLifecycleResponseWithItems("completed", terminal)}),
	} {
		if err := v.validate(context.Background(), event); err != nil {
			t.Fatalf("event %d (%s): %v", index, event.Event, err)
		}
	}
	if err := v.finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestResponsesNativeStreamRejectsKnownFieldAddedAfterItemDone(t *testing.T) {
	done := responsesLifecycleItem("reasoning_1", "reasoning")
	terminal := responsesLifecycleItem("reasoning_1", "reasoning")
	terminal["content"] = []any{map[string]any{"type": "reasoning_text", "text": "late reasoning"}}
	v := newResponsesLifecycleValidator()
	var got error
	for _, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.completed", 3, map[string]any{"response": responsesLifecycleResponseWithItems("completed", terminal)}),
	} {
		got = v.validate(context.Background(), event)
		if got != nil {
			break
		}
	}
	if !errors.Is(got, core.ErrInvalidPayload) || !strings.Contains(got.Error(), "$.content was added after output_item.done") {
		t.Fatalf("error = %v, want known-field terminal enrichment rejection", got)
	}
}

func TestResponsesNativeStreamRejectsTerminalCiphertextChangeOutsideReasoning(t *testing.T) {
	done := responsesLifecycleItem("message_1", "message")
	done["encrypted_content"] = "done-ciphertext"
	terminal := responsesLifecycleItem("message_1", "message")
	terminal["encrypted_content"] = "terminal-ciphertext"
	v := newResponsesLifecycleValidator()
	var got error
	for _, event := range []core.Frame{
		responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")}),
		responsesLifecycleFrame(t, "response.output_item.added", 1, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.output_item.done", 2, map[string]any{"output_index": 0, "item": done}),
		responsesLifecycleFrame(t, "response.completed", 3, map[string]any{"response": responsesLifecycleResponseWithItems("completed", terminal)}),
	} {
		got = v.validate(context.Background(), event)
		if got != nil {
			break
		}
	}
	if !errors.Is(got, core.ErrInvalidPayload) || !strings.Contains(got.Error(), "$.encrypted_content changed value") {
		t.Fatalf("error = %v, want encrypted_content mismatch", got)
	}
}

func TestResponsesNativeStreamFailureIsTerminal(t *testing.T) {
	v := newResponsesLifecycleValidator()
	if err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")})); err != nil {
		t.Fatal(err)
	}
	err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.failed", 1, map[string]any{"response": map[string]any{
		"id": "resp_failed", "object": "response", "created_at": 1, "model": "gpt-5.4",
		"status": "failed", "error": map[string]any{"code": "server_error", "message": "failed"},
	}}))
	if err != nil {
		t.Fatalf("failure error = %v, want frame to pass through", err)
	}
	err = v.validate(context.Background(), responsesLifecycleFrame(t, "response.in_progress", 2, map[string]any{"response": responsesLifecycleResponse("in_progress")}))
	if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "after the terminal response") {
		t.Fatalf("post-failure error = %v, want terminal lifecycle error", err)
	}
}

func TestResponsesNativeStreamRejectsMalformedFailureAndCancellation(t *testing.T) {
	base := map[string]any{
		"id": "resp_failed", "object": "response", "created_at": 1, "model": "gpt-5.4",
		"status": "failed", "error": map[string]any{"code": "server_error", "message": "failed"},
	}
	tests := []struct {
		name     string
		event    string
		response map[string]any
		wantPath string
	}{
		{name: "missing id", event: "response.failed", response: cloneResponsesLifecycleMap(base, "id"), wantPath: "$.response.id"},
		{name: "wrong object", event: "response.failed", response: mergeResponsesLifecycleMap(base, "object", "response.error"), wantPath: "$.response.object"},
		{name: "missing created_at", event: "response.failed", response: cloneResponsesLifecycleMap(base, "created_at"), wantPath: "$.response.created_at"},
		{name: "missing error", event: "response.failed", response: cloneResponsesLifecycleMap(base, "error"), wantPath: "$.response.error"},
		{name: "empty error code", event: "response.failed", response: mergeResponsesLifecycleMap(base, "error", map[string]any{"code": "", "message": "failed"}), wantPath: "$.response.error.code"},
		{name: "cancelled missing model", event: "response.cancelled", response: map[string]any{"id": "resp_cancelled", "object": "response", "created_at": 1, "status": "cancelled"}, wantPath: "$.response.model"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			v := newResponsesLifecycleValidator()
			if err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": responsesLifecycleResponse("in_progress")})); err != nil {
				t.Fatal(err)
			}
			err := v.validate(context.Background(), responsesLifecycleFrame(t, test.event, 1, map[string]any{"response": test.response}))
			if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), test.wantPath) {
				t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.wantPath)
			}
		})
	}
}

func TestResponsesNativeStreamRejectsResponseIdentityChange(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "id", field: "id", value: "resp_changed"},
		{name: "model", field: "model", value: "gpt-changed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			v := newResponsesLifecycleValidator()
			created := map[string]any{
				"id": "resp_original", "object": "response", "created_at": 1, "model": "gpt-5.4",
				"status": "in_progress", "output": []any{},
			}
			if err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": created})); err != nil {
				t.Fatal(err)
			}
			failed := map[string]any{
				"id": "resp_original", "object": "response", "created_at": 1, "model": "gpt-5.4",
				"status": "failed", "error": map[string]any{"code": "server_error", "message": "failed"},
			}
			failed[test.field] = test.value
			err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.failed", 1, map[string]any{"response": failed}))
			if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "$.response."+test.field) {
				t.Fatalf("error = %v, want response %s mismatch", err, test.field)
			}
		})
	}
}

func TestResponsesNativeStreamInvalidCreatedDoesNotCommitIdentity(t *testing.T) {
	v := newResponsesLifecycleValidator()
	bad := map[string]any{
		"id": "resp_poison", "object": "response", "created_at": 1, "model": "gpt-poison",
		"status": "in_progress", "output": []any{},
	}
	if err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.created", 1, map[string]any{"response": bad})); !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("bad created error = %v, want ErrInvalidPayload", err)
	}
	good := map[string]any{
		"id": "resp_good", "object": "response", "created_at": 1, "model": "gpt-5.4",
		"status": "in_progress", "output": []any{},
	}
	if err := v.validate(context.Background(), responsesLifecycleFrame(t, "response.created", 0, map[string]any{"response": good})); err != nil {
		t.Fatalf("valid created after rejected frame: %v", err)
	}
}

func cloneResponsesLifecycleMap(source map[string]any, omit string) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		if key != omit {
			result[key] = value
		}
	}
	return result
}

func mergeResponsesLifecycleMap(source map[string]any, key string, value any) map[string]any {
	result := cloneResponsesLifecycleMap(source, "")
	result[key] = value
	return result
}

func newResponsesLifecycleValidator() *nativeStreamValidator {
	return &nativeStreamValidator{protocol: core.ProtocolResponses, wire: codec.New(core.ProtocolResponses)}
}

func responsesLifecycleFrame(t *testing.T, eventType string, sequence int64, fields map[string]any) core.Frame {
	t.Helper()
	payload := map[string]any{"type": eventType, "sequence_number": sequence}
	for key, value := range fields {
		payload[key] = value
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return core.Frame{Event: eventType, Data: data}
}

func responsesLifecycleResponse(status string) map[string]any {
	return map[string]any{"status": status, "output": []any{}}
}

func responsesLifecycleResponseWithItems(status string, items ...map[string]any) map[string]any {
	return map[string]any{"status": status, "output": items}
}

func responsesLifecycleItem(id, itemType string) map[string]any {
	item := map[string]any{"id": id, "type": itemType}
	switch itemType {
	case "message":
		item["role"] = "assistant"
		item["status"] = "completed"
		item["content"] = []any{}
	case "reasoning":
		item["summary"] = []any{}
		item["status"] = "completed"
	case "function_call":
		item["call_id"] = "call_" + id
		item["name"] = "lookup"
		item["arguments"] = "{}"
		item["status"] = "completed"
	case "custom_tool_call":
		item["call_id"] = "call_" + id
		item["name"] = "shell"
		item["input"] = "echo hi"
		item["status"] = "completed"
	case "web_search_call":
		item["status"] = "completed"
		item["action"] = map[string]any{"type": "open_page"}
	case "tool_search_call":
		item["call_id"] = "call_" + id
		item["arguments"] = map[string]any{}
		item["execution"] = "server"
		item["status"] = "completed"
	case "additional_tools":
		item["role"] = "assistant"
		item["tools"] = []any{}
	}
	return item
}
