package relay

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestKnownResponsesEventsRequireOfficialCommonFields(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		payload   string
		path      string
	}{
		{
			name:      "sequence number",
			eventType: "response.created",
			payload:   `{"type":"response.created","response":{}}`,
			path:      "$.sequence_number",
		},
		{
			name:      "output index",
			eventType: "response.output_item.added",
			payload:   `{"type":"response.output_item.added","sequence_number":0,"item":{"type":"message"}}`,
			path:      "$.output_index",
		},
		{
			name:      "item id",
			eventType: "response.function_call_arguments.delta",
			payload:   `{"type":"response.function_call_arguments.delta","sequence_number":0,"output_index":0,"delta":"{}"}`,
			path:      "$.item_id",
		},
		{
			name:      "content index",
			eventType: "response.output_text.delta",
			payload:   `{"type":"response.output_text.delta","sequence_number":0,"output_index":0,"item_id":"msg_1","delta":"x"}`,
			path:      "$.content_index",
		},
		{
			name:      "summary index",
			eventType: "response.reasoning_summary_text.delta",
			payload:   `{"type":"response.reasoning_summary_text.delta","sequence_number":0,"output_index":0,"item_id":"rs_1","delta":"x"}`,
			path:      "$.summary_index",
		},
		{
			name:      "command index",
			eventType: "response.shell_call_command.delta",
			payload:   `{"type":"response.shell_call_command.delta","sequence_number":0,"output_index":0,"delta":"x"}`,
			path:      "$.command_index",
		},
		{
			name:      "partial image index",
			eventType: "response.image_generation_call.partial_image",
			payload:   `{"type":"response.image_generation_call.partial_image","sequence_number":0,"output_index":0,"item_id":"ig_1"}`,
			path:      "$.partial_image_index",
		},
		{
			name:      "annotation index",
			eventType: "response.output_text.annotation.added",
			payload:   `{"type":"response.output_text.annotation.added","sequence_number":0,"output_index":0,"content_index":0,"item_id":"msg_1"}`,
			path:      "$.annotation_index",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateKnownResponsesEvent([]byte(test.payload), test.eventType)
			if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.path)
			}
		})
	}
}

func TestAllOpenAIGoV356ResponsesEventsRequireSequenceNumber(t *testing.T) {
	eventTypes := []string{
		"response.audio.delta",
		"response.audio.done",
		"response.audio.transcript.delta",
		"response.audio.transcript.done",
		"response.code_interpreter_call_code.delta",
		"response.code_interpreter_call_code.done",
		"response.code_interpreter_call.completed",
		"response.code_interpreter_call.in_progress",
		"response.code_interpreter_call.interpreting",
		"response.completed",
		"response.content_part.added",
		"response.content_part.done",
		"response.created",
		"error",
		"response.file_search_call.completed",
		"response.file_search_call.in_progress",
		"response.file_search_call.searching",
		"response.function_call_arguments.delta",
		"response.function_call_arguments.done",
		"response.shell_call_command.added",
		"response.shell_call_command.delta",
		"response.shell_call_command.done",
		"response.shell_call_output_content.delta",
		"response.shell_call_output_content.done",
		"response.in_progress",
		"response.failed",
		"response.incomplete",
		"response.output_item.added",
		"response.output_item.done",
		"response.reasoning_summary_part.added",
		"response.reasoning_summary_part.done",
		"response.reasoning_summary_text.delta",
		"response.reasoning_summary_text.done",
		"response.reasoning_text.delta",
		"response.reasoning_text.done",
		"response.refusal.delta",
		"response.refusal.done",
		"response.output_text.delta",
		"response.output_text.done",
		"response.web_search_call.completed",
		"response.web_search_call.in_progress",
		"response.web_search_call.searching",
		"response.image_generation_call.completed",
		"response.image_generation_call.generating",
		"response.image_generation_call.in_progress",
		"response.image_generation_call.partial_image",
		"response.mcp_call_arguments.delta",
		"response.mcp_call_arguments.done",
		"response.mcp_call.completed",
		"response.mcp_call.failed",
		"response.mcp_call.in_progress",
		"response.mcp_list_tools.completed",
		"response.mcp_list_tools.failed",
		"response.mcp_list_tools.in_progress",
		"response.output_text.annotation.added",
		"response.queued",
		"response.custom_tool_call_input.delta",
		"response.custom_tool_call_input.done",
	}
	for _, eventType := range eventTypes {
		t.Run(eventType, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{"type": eventType})
			if err != nil {
				t.Fatal(err)
			}
			err = validateKnownResponsesEvent(payload, eventType)
			if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "$.sequence_number") {
				t.Fatalf("error = %v, want missing sequence_number", err)
			}
		})
	}
}

func TestKnownResponsesEventsRequireLifecycleAndDonePayloadFields(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		payload   string
		path      string
	}{
		{
			name:      "queued response",
			eventType: "response.queued",
			payload:   `{"type":"response.queued","sequence_number":0}`,
			path:      "$.response",
		},
		{
			name:      "function name",
			eventType: "response.function_call_arguments.done",
			payload:   `{"type":"response.function_call_arguments.done","sequence_number":0,"output_index":0,"item_id":"fc_1","arguments":"{}"}`,
			path:      "$.name",
		},
		{
			name:      "error code",
			eventType: "error",
			payload:   `{"type":"error","sequence_number":0,"message":"bad","param":"input"}`,
			path:      "$.code",
		},
		{
			name:      "error message",
			eventType: "error",
			payload:   `{"type":"error","sequence_number":0,"code":"bad","param":"input"}`,
			path:      "$.message",
		},
		{
			name:      "error param",
			eventType: "error",
			payload:   `{"type":"error","sequence_number":0,"code":"bad","message":"bad"}`,
			path:      "$.param",
		},
		{
			name:      "text logprobs",
			eventType: "response.output_text.delta",
			payload:   `{"type":"response.output_text.delta","sequence_number":0,"output_index":0,"content_index":0,"item_id":"msg_1","delta":"x"}`,
			path:      "$.logprobs",
		},
		{
			name:      "shell output delta",
			eventType: "response.shell_call_output_content.delta",
			payload:   `{"type":"response.shell_call_output_content.delta","sequence_number":0,"output_index":0,"command_index":0,"item_id":"sh_1"}`,
			path:      "$.delta",
		},
		{
			name:      "partial image payload",
			eventType: "response.image_generation_call.partial_image",
			payload:   `{"type":"response.image_generation_call.partial_image","sequence_number":0,"output_index":0,"partial_image_index":0,"item_id":"ig_1"}`,
			path:      "$.partial_image_b64",
		},
		{
			name:      "annotation payload",
			eventType: "response.output_text.annotation.added",
			payload:   `{"type":"response.output_text.annotation.added","sequence_number":0,"output_index":0,"content_index":0,"annotation_index":0,"item_id":"msg_1"}`,
			path:      "$.annotation",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateKnownResponsesEvent([]byte(test.payload), test.eventType)
			if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.path)
			}
		})
	}
}

func TestKnownResponsesEventCommonFieldsAcceptOfficialShapes(t *testing.T) {
	tests := []struct {
		eventType string
		payload   string
	}{
		{"response.created", `{"type":"response.created","sequence_number":0,"response":{}}`},
		{"response.output_item.added", `{"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"type":"message"}}`},
		{"response.content_part.added", `{"type":"response.content_part.added","sequence_number":2,"output_index":0,"content_index":0,"item_id":"msg_1","part":{"type":"output_text"}}`},
		{"response.reasoning_summary_text.delta", `{"type":"response.reasoning_summary_text.delta","sequence_number":3,"output_index":1,"summary_index":0,"item_id":"rs_1","delta":"x"}`},
		{"response.function_call_arguments.done", `{"type":"response.function_call_arguments.done","sequence_number":4,"output_index":2,"item_id":"fc_1","name":"lookup","arguments":"{}"}`},
		{"response.web_search_call.searching", `{"type":"response.web_search_call.searching","sequence_number":5,"output_index":3,"item_id":"ws_1"}`},
		{"response.custom_tool_call_input.done", `{"type":"response.custom_tool_call_input.done","sequence_number":6,"output_index":4,"item_id":"ctc_1","input":"run"}`},
		{"response.output_text.done", `{"type":"response.output_text.done","sequence_number":7,"output_index":5,"content_index":0,"item_id":"msg_2","text":"done","logprobs":[]}`},
		{"response.shell_call_output_content.delta", `{"type":"response.shell_call_output_content.delta","sequence_number":8,"output_index":6,"command_index":0,"item_id":"sh_1","delta":{"stdout":"ok"}}`},
		{"response.shell_call_output_content.done", `{"type":"response.shell_call_output_content.done","sequence_number":9,"output_index":6,"command_index":0,"item_id":"sh_1","output":[]}`},
		{"response.image_generation_call.partial_image", `{"type":"response.image_generation_call.partial_image","sequence_number":10,"output_index":7,"partial_image_index":0,"item_id":"ig_1","partial_image_b64":"AA=="}`},
		{"response.output_text.annotation.added", `{"type":"response.output_text.annotation.added","sequence_number":11,"output_index":8,"content_index":0,"annotation_index":0,"item_id":"msg_3","annotation":{"type":"url_citation"}}`},
		{"error", `{"type":"error","sequence_number":12,"code":"bad","message":"bad request","param":"input"}`},
	}
	for _, test := range tests {
		t.Run(test.eventType, func(t *testing.T) {
			if err := validateKnownResponsesEvent([]byte(test.payload), test.eventType); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnknownResponsesEventKeepsForwardCompatibleCommonFields(t *testing.T) {
	payload := map[string]any{"type": "response.future.delta", "future_index": 0}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateKnownResponsesEvent(data, "response.future.delta"); err != nil {
		t.Fatal(err)
	}
}
