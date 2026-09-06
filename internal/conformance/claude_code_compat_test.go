package conformance

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestClaudeCodeFirstTurnMessagesToResponsesCompatibility(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	userID := claudeCodeMetadataUserID(t)
	body := claudeCodeFirstTurnRequest(t, userID)

	if _, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict error = %v, want ErrUnsupported", err)
	}

	options := conversionOptions{
		Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4", Stream: true, StreamSet: true},
		CodingAgentCompatibility: true,
	}
	result, err := converter.ToUpstreamRequest(context.Background(), body, options)
	if err != nil {
		t.Fatal(err)
	}

	var converted struct {
		Model             string          `json:"model"`
		MaxOutputTokens   int             `json:"max_output_tokens"`
		Stream            bool            `json:"stream"`
		Instructions      string          `json:"instructions"`
		SafetyIdentifier  string          `json:"safety_identifier"`
		ContextManagement json.RawMessage `json:"context_management"`
		Reasoning         *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
		Tools []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result.Body, &converted); err != nil {
		t.Fatal(err)
	}
	if converted.Model != "gpt-5.4" || converted.MaxOutputTokens != 32000 || !converted.Stream {
		t.Fatalf("converted request model=%q max_output_tokens=%d stream=%v", converted.Model, converted.MaxOutputTokens, converted.Stream)
	}
	if converted.Reasoning == nil || converted.Reasoning.Effort != "medium" {
		t.Fatalf("reasoning = %#v, want medium compatibility approximation", converted.Reasoning)
	}
	for _, want := range []string{"You are Claude Code.", "Use tools when needed.", "Repository instructions."} {
		if !strings.Contains(converted.Instructions, want) {
			t.Fatalf("instructions do not contain %q: %q", want, converted.Instructions)
		}
	}
	if len(converted.Input) != 1 || converted.Input[0].Type != "message" || converted.Input[0].Role != "user" || len(converted.Input[0].Content) != 4 {
		t.Fatalf("unexpected converted input: %#v", converted.Input)
	}
	for index, part := range converted.Input[0].Content {
		if part.Type != "input_text" {
			t.Fatalf("input content[%d].type = %q, want input_text", index, part.Type)
		}
	}
	if len(converted.Tools) != 2 || converted.Tools[0].Type != "function" || converted.Tools[0].Name != "Read" || converted.Tools[1].Name != "Bash" {
		t.Fatalf("converted tools = %#v", converted.Tools)
	}
	if len(converted.ContextManagement) != 0 && string(converted.ContextManagement) != "null" {
		t.Fatalf("no-op context_management leaked upstream: %s", converted.ContextManagement)
	}
	if strings.Contains(string(result.Body), "cache_control") {
		t.Fatalf("Messages cache controls leaked upstream: %s", result.Body)
	}
	assertClaudeCodeSafetyIdentifier(t, converted.SafetyIdentifier, userID)

	second, err := converter.ToUpstreamRequest(context.Background(), body, options)
	if err != nil {
		t.Fatal(err)
	}
	var repeated struct {
		SafetyIdentifier string `json:"safety_identifier"`
	}
	if err := json.Unmarshal(second.Body, &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.SafetyIdentifier != converted.SafetyIdentifier {
		t.Fatalf("safety_identifier is not deterministic: %q != %q", repeated.SafetyIdentifier, converted.SafetyIdentifier)
	}

	for _, want := range []struct {
		code string
		path string
	}{
		{"thinking_policy_approximated", "$.thinking"},
		{"thinking_display_not_representable", "$.thinking.display"},
		{"cache_control_not_representable", "$.system[1].cache_control"},
		{"cache_control_not_representable", "$.system[2].cache_control"},
		{"cache_control_not_representable", "$.messages[0].content[3].cache_control"},
		{"metadata_user_id_hashed", "$.metadata.user_id"},
	} {
		requireClaudeCodeDiagnostic(t, result.Diagnostics, want.code, want.path)
	}
}

func TestClaudeCodeErrorToolResultMessagesToResponsesCompatibility(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})

	// Remove the cache marker in the strict probe so is_error is the first lossy
	// field encountered and the strict behavior is independently locked down.
	strictBody := claudeCodeToolErrorRequest(t, false)
	if _, err := converter.ToUpstreamRequest(context.Background(), strictBody, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict error = %v, want ErrUnsupported for tool_result.is_error", err)
	}

	body := claudeCodeToolErrorRequest(t, true)
	result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{
		Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4"},
		CodingAgentCompatibility: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var converted struct {
		Model string `json:"model"`
		Input []struct {
			Type      string          `json:"type"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Output    json.RawMessage `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(result.Body, &converted); err != nil {
		t.Fatal(err)
	}
	if converted.Model != "gpt-5.4" || len(converted.Input) != 3 {
		t.Fatalf("unexpected converted tool history: %s", result.Body)
	}
	call, output := converted.Input[1], converted.Input[2]
	if call.Type != "function_call" || call.CallID != "toolu_read_1" || call.Name != "Read" || string(call.Arguments) != `"{\"file_path\":\"/missing.txt\"}"` {
		t.Fatalf("converted function call = %#v", call)
	}
	if output.Type != "function_call_output" || output.CallID != "toolu_read_1" || string(output.Output) != `"File does not exist."` {
		t.Fatalf("converted function output = %#v", output)
	}
	if strings.Contains(string(result.Body), "is_error") || strings.Contains(string(result.Body), "cache_control") {
		t.Fatalf("Messages-only tool result fields leaked upstream: %s", result.Body)
	}
	requireClaudeCodeDiagnostic(t, result.Diagnostics, "tool_result_error_state_not_representable", "$.messages[2].content[0].is_error")
	requireClaudeCodeDiagnostic(t, result.Diagnostics, "cache_control_not_representable", "$.messages[2].content[0].cache_control")
}

func TestClaudeCodeNestedToolResultCacheControlCompatibility(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	body := []byte(`{
		"model":"claude-sonnet-4-5","max_tokens":64,
		"messages":[
			{"role":"user","content":"Read a missing file."},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_nested_1","name":"Read","input":{"file_path":"/missing.txt"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_nested_1","cache_control":{"type":"ephemeral"},"content":[{"type":"text","text":"File does not exist.","cache_control":{"type":"ephemeral"}}]}]}
		],
		"tools":[{"name":"Read","input_schema":{"type":"object"}}]
	}`)

	if _, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict error = %v, want ErrUnsupported", err)
	}
	result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{
		Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4"},
		CodingAgentCompatibility: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result.Body), "cache_control") {
		t.Fatalf("nested Messages cache controls leaked upstream: %s", result.Body)
	}
	var converted struct {
		Input []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(result.Body, &converted); err != nil {
		t.Fatal(err)
	}
	if len(converted.Input) != 3 || converted.Input[2].Type != "function_call_output" || converted.Input[2].CallID != "toolu_nested_1" || len(converted.Input[2].Output) != 1 || converted.Input[2].Output[0].Type != "input_text" || converted.Input[2].Output[0].Text != "File does not exist." {
		t.Fatalf("unexpected converted nested tool result: %s", result.Body)
	}
	requireClaudeCodeDiagnostic(t, result.Diagnostics, "cache_control_not_representable", "$.messages[2].content[0].cache_control")
	requireClaudeCodeDiagnostic(t, result.Diagnostics, "cache_control_not_representable", "$.messages[2].content[0].content[0].cache_control")
}

func TestClaudeCodeCurrentAdaptiveThinkingCompatibility(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	body := []byte(`{
		"model":"claude-sonnet-4-5","max_tokens":32000,
		"thinking":{"type":"adaptive","display":"omitted"},
		"output_config":{"effort":"high"},
		"messages":[{"role":"user","content":"hello"}]
	}`)
	result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{
		Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4"},
		CodingAgentCompatibility: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Body), `"reasoning":{"effort":"high"}`) {
		t.Fatalf("adaptive thinking effort was not preserved: %s", result.Body)
	}
	requireClaudeCodeDiagnostic(t, result.Diagnostics, "thinking_policy_approximated", "$.thinking")
	requireClaudeCodeDiagnostic(t, result.Diagnostics, "thinking_display_not_representable", "$.thinking.display")
}

func TestClaudeCodeResponsesReasoningNonStreamCompatibility(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	response := claudeCodeReasoningResponse()

	if _, err := converter.ToClientResponse(context.Background(), response, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict error = %v, want ErrUnsupported for Responses reasoning", err)
	}

	result, err := converter.ToClientResponse(context.Background(), response, conversionOptions{
		Exchange:                 exchangeMetadata{ClientModel: "claude-sonnet-4-5"},
		CodingAgentCompatibility: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertClaudeCodeMessagesReasoningResult(t, result.Body)
	if strings.Contains(string(result.Body), "private reasoning summary") || strings.Contains(string(result.Body), "private detailed reasoning") || strings.Contains(string(result.Body), "opaque-reasoning-state") {
		t.Fatalf("Responses reasoning leaked into the Messages response: %s", result.Body)
	}
	requireClaudeCodeDiagnostic(t, result.Diagnostics, "responses_reasoning_not_representable", "$.output[0]")
}

func TestClaudeCodeResponsesReasoningStreamCompatibility(t *testing.T) {
	events := claudeCodeReasoningStreamEvents()
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})

	strict, err := converter.NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := strict.Convert(context.Background(), events[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := strict.Convert(context.Background(), events[1]); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict reasoning stream error = %v, want ErrUnsupported", err)
	}

	compatible, err := converter.NewClientStream(context.Background(), conversionOptions{
		Exchange:                 exchangeMetadata{ClientModel: "claude-sonnet-4-5"},
		CodingAgentCompatibility: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var outputFrames []streamFrame
	var diagnostics []Diagnostic
	for index, event := range events {
		frames, eventDiagnostics, err := compatible.Convert(context.Background(), event)
		if err != nil {
			t.Fatalf("event[%d] %s: %v", index, event.Event, err)
		}
		outputFrames = append(outputFrames, frames...)
		diagnostics = append(diagnostics, eventDiagnostics...)
	}
	finalFrames, finalDiagnostics, err := compatible.Finalize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	outputFrames = append(outputFrames, finalFrames...)
	diagnostics = append(diagnostics, finalDiagnostics...)

	if countClaudeCodeDiagnostic(diagnostics, "responses_reasoning_not_representable") != 1 {
		t.Fatalf("reasoning diagnostics = %#v, want exactly one omission diagnostic", diagnostics)
	}
	for _, frame := range outputFrames {
		if strings.Contains(string(frame.Data), "private reasoning summary") || strings.Contains(string(frame.Data), "private detailed reasoning") || strings.Contains(string(frame.Data), "opaque-reasoning-state") {
			t.Fatalf("Responses reasoning leaked into Messages SSE frame %s: %s", frame.Event, frame.Data)
		}
	}

	body, _, err := collectNativeStreamResponse(ProtocolMessages, outputFrames, rejectSemanticLoss)
	if err != nil {
		t.Fatalf("converted Messages stream is not parseable: %v", err)
	}
	assertClaudeCodeMessagesReasoningResult(t, body)
}

func TestClaudeCodeReasoningStreamRejectsUnknownOrWrongItem(t *testing.T) {
	tests := []struct {
		name      string
		added     string
		reasoning string
	}{
		{
			name:      "missing item id",
			reasoning: `{"type":"response.reasoning_text.delta","delta":"secret"}`,
		},
		{
			name:      "unknown item id",
			reasoning: `{"type":"response.reasoning_text.delta","item_id":"rs_unknown","delta":"secret"}`,
		},
		{
			name:      "message item",
			added:     `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
			reasoning: `{"type":"response.reasoning_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"secret"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
			stream, err := converter.NewClientStream(context.Background(), conversionOptions{CodingAgentCompatibility: true})
			if err != nil {
				t.Fatal(err)
			}
			created := streamFrame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4","status":"in_progress","output":[],"usage":{}}}`)}
			if _, _, err := stream.Convert(context.Background(), created); err != nil {
				t.Fatal(err)
			}
			if test.added != "" {
				if _, _, err := stream.Convert(context.Background(), streamFrame{Event: "response.output_item.added", Data: []byte(test.added)}); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err = stream.Convert(context.Background(), streamFrame{Event: "response.reasoning_text.delta", Data: []byte(test.reasoning)})
			if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "$.item_id") {
				t.Fatalf("error = %v, want ErrInvalidPayload at $.item_id", err)
			}
		})
	}
}

func TestClaudeCodeCompatibilityStillRejectsStateChangingControls(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	tests := []struct {
		name string
		body string
	}{
		{
			name: "non-noop context edit",
			body: `{"model":"claude-sonnet-4-5","max_tokens":64,"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":1}]},"messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "non-ephemeral cache",
			body: `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"persistent"}}]}]}`,
		},
		{
			name: "ephemeral cache with unsupported ttl",
			body: `{"model":"claude-sonnet-4-5","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`,
		},
		{
			name: "adaptive thinking without explicit omitted display",
			body: `{"model":"claude-sonnet-4-5","max_tokens":64,"thinking":{"type":"adaptive"},"messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "summarized thinking display",
			body: `{"model":"claude-sonnet-4-5","max_tokens":64,"thinking":{"type":"enabled","budget_tokens":1024,"display":"summarized"},"messages":[{"role":"user","content":"hello"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{
				Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4"},
				CodingAgentCompatibility: true,
			})
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestDocumentedLossCharacterizationStillAcceptsAdaptiveThinking(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	body := []byte(`{"model":"claude-sonnet-4-5","max_tokens":64,"thinking":{"type":"adaptive","display":"summarized"},"messages":[{"role":"user","content":"hello"}]}`)
	result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{
		Exchange:   exchangeMetadata{UpstreamModel: "gpt-5.4"},
		LossPolicy: allowDocumentedLoss,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Body), `"reasoning":{"effort":"medium"}`) {
		t.Fatalf("documented-loss reasoning characterization changed: %s", result.Body)
	}
}

func claudeCodeMetadataUserID(t *testing.T) string {
	t.Helper()
	value := `{"device_id":"` + strings.Repeat("d", 36) + `","account_uuid":"` + strings.Repeat("a", 36) + `","session_id":"` + strings.Repeat("s", 28) + `"}`
	if len([]byte(value)) != 150 {
		t.Fatalf("test metadata user_id is %d bytes, want 150", len([]byte(value)))
	}
	return value
}

func claudeCodeFirstTurnRequest(t *testing.T, userID string) []byte {
	t.Helper()
	request := map[string]any{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 32000,
		"stream":     true,
		"thinking": map[string]any{
			"type": "enabled", "budget_tokens": 31999, "display": "omitted",
		},
		"context_management": map[string]any{
			"edits": []any{map[string]any{"type": "clear_thinking_20251015", "keep": "all"}},
		},
		"metadata": map[string]any{"user_id": userID},
		"system": []any{
			map[string]any{"type": "text", "text": "You are Claude Code."},
			map[string]any{"type": "text", "text": "Use tools when needed.", "cache_control": map[string]any{"type": "ephemeral"}},
			map[string]any{"type": "text", "text": "Repository instructions.", "cache_control": map[string]any{"type": "ephemeral"}},
		},
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{
				map[string]any{"type": "text", "text": "Environment context."},
				map[string]any{"type": "text", "text": "Workspace context."},
				map[string]any{"type": "text", "text": "User request follows."},
				map[string]any{"type": "text", "text": "Read go.mod.", "cache_control": map[string]any{"type": "ephemeral"}},
			},
		}},
		"tools": []any{
			map[string]any{
				"name": "Read", "description": "Read a file from the local filesystem.",
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string"}}, "required": []string{"file_path"}},
			},
			map[string]any{
				"name": "Bash", "description": "Run a shell command.",
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []string{"command"}},
			},
		},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func claudeCodeToolErrorRequest(t *testing.T, cacheControl bool) []byte {
	t.Helper()
	result := map[string]any{
		"type": "tool_result", "tool_use_id": "toolu_read_1", "content": "File does not exist.", "is_error": true,
	}
	if cacheControl {
		result["cache_control"] = map[string]any{"type": "ephemeral"}
	}
	request := map[string]any{
		"model":      "claude-sonnet-4-5",
		"max_tokens": 32000,
		"messages": []any{
			map[string]any{"role": "user", "content": "Read a missing file."},
			map[string]any{"role": "assistant", "content": []any{map[string]any{
				"type": "tool_use", "id": "toolu_read_1", "name": "Read", "input": map[string]any{"file_path": "/missing.txt"},
			}}},
			map[string]any{"role": "user", "content": []any{result}},
		},
		"tools": []any{map[string]any{
			"name": "Read", "description": "Read a file from the local filesystem.",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{"file_path": map[string]any{"type": "string"}}, "required": []string{"file_path"}},
		}},
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func claudeCodeReasoningResponse() []byte {
	return []byte(`{
		"id":"resp_claude_code","object":"response","created_at":1,"model":"gpt-5.4","status":"completed",
		"output":[
			{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"private reasoning summary"}],"content":[{"type":"reasoning_text","text":"private detailed reasoning"}],"encrypted_content":"opaque-reasoning-state"},
			{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I will inspect the file.","annotations":[]}]},
			{"id":"fc_1","type":"function_call","status":"completed","call_id":"toolu_read_1","name":"Read","arguments":"{\"file_path\":\"go.mod\"}"}
		],
		"usage":{"input_tokens":12,"output_tokens":8,"total_tokens":20,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":2}}
	}`)
}

func claudeCodeReasoningStreamEvents() []streamFrame {
	terminal := string(claudeCodeReasoningResponse())
	return []streamFrame{
		{
			Event: "response.created",
			Data:  []byte(`{"type":"response.created","response":{"id":"resp_claude_code","object":"response","created_at":1,"model":"gpt-5.4","status":"in_progress","output":[],"usage":{"input_tokens":12,"output_tokens":0,"total_tokens":12,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}}}`),
		},
		{
			Event: "response.output_item.added",
			Data:  []byte(`{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[],"encrypted_content":"opaque-reasoning-state"}}`),
		},
		{
			Event: "response.content_part.added",
			Data:  []byte(`{"type":"response.content_part.added","item_id":"rs_1","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":""}}`),
		},
		{
			Event: "response.reasoning_text.delta",
			Data:  []byte(`{"type":"response.reasoning_text.delta","item_id":"rs_1","output_index":0,"content_index":0,"delta":"private detailed reasoning"}`),
		},
		{
			Event: "response.reasoning_text.done",
			Data:  []byte(`{"type":"response.reasoning_text.done","item_id":"rs_1","output_index":0,"content_index":0,"text":"private detailed reasoning"}`),
		},
		{
			Event: "response.content_part.done",
			Data:  []byte(`{"type":"response.content_part.done","item_id":"rs_1","output_index":0,"content_index":0,"part":{"type":"reasoning_text","text":"private detailed reasoning"}}`),
		},
		{
			Event: "response.reasoning_summary_part.added",
			Data:  []byte(`{"type":"response.reasoning_summary_part.added","item_id":"rs_1","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":""}}`),
		},
		{
			Event: "response.reasoning_summary_text.delta",
			Data:  []byte(`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","output_index":0,"summary_index":0,"delta":"private reasoning summary"}`),
		},
		{
			Event: "response.reasoning_summary_text.done",
			Data:  []byte(`{"type":"response.reasoning_summary_text.done","item_id":"rs_1","output_index":0,"summary_index":0,"text":"private reasoning summary"}`),
		},
		{
			Event: "response.reasoning_summary_part.done",
			Data:  []byte(`{"type":"response.reasoning_summary_part.done","item_id":"rs_1","output_index":0,"summary_index":0,"part":{"type":"summary_text","text":"private reasoning summary"}}`),
		},
		{
			Event: "response.output_item.done",
			Data:  []byte(`{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"private reasoning summary"}],"content":[{"type":"reasoning_text","text":"private detailed reasoning"}],"encrypted_content":"opaque-reasoning-state"}}`),
		},
		{
			Event: "response.output_item.added",
			Data:  []byte(`{"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`),
		},
		{
			Event: "response.content_part.added",
			Data:  []byte(`{"type":"response.content_part.added","item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`),
		},
		{
			Event: "response.output_text.delta",
			Data:  []byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":1,"content_index":0,"delta":"I will inspect the file."}`),
		},
		{
			Event: "response.content_part.done",
			Data:  []byte(`{"type":"response.content_part.done","item_id":"msg_1","output_index":1,"content_index":0,"part":{"type":"output_text","text":"I will inspect the file.","annotations":[]}}`),
		},
		{
			Event: "response.output_item.done",
			Data:  []byte(`{"type":"response.output_item.done","output_index":1,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"I will inspect the file.","annotations":[]}]}}`),
		},
		{
			Event: "response.output_item.added",
			Data:  []byte(`{"type":"response.output_item.added","output_index":2,"item":{"id":"fc_1","type":"function_call","status":"in_progress","call_id":"toolu_read_1","name":"Read","arguments":""}}`),
		},
		{
			Event: "response.function_call_arguments.delta",
			Data:  []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","output_index":2,"delta":"{\"file_path\":\"go.mod\"}"}`),
		},
		{
			Event: "response.function_call_arguments.done",
			Data:  []byte(`{"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":2,"arguments":"{\"file_path\":\"go.mod\"}"}`),
		},
		{
			Event: "response.output_item.done",
			Data:  []byte(`{"type":"response.output_item.done","output_index":2,"item":{"id":"fc_1","type":"function_call","status":"completed","call_id":"toolu_read_1","name":"Read","arguments":"{\"file_path\":\"go.mod\"}"}}`),
		},
		{
			Event: "response.completed",
			Data:  []byte(`{"type":"response.completed","response":` + terminal + `}`),
		},
	}
}

func assertClaudeCodeMessagesReasoningResult(t *testing.T, body []byte) {
	t.Helper()
	var response struct {
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("invalid Messages response: %v: %s", err, body)
	}
	if response.Model != "claude-sonnet-4-5" || response.StopReason != "tool_use" || len(response.Content) != 2 {
		t.Fatalf("unexpected Messages response: %s", body)
	}
	if response.Content[0].Type != "text" || response.Content[0].Text != "I will inspect the file." {
		t.Fatalf("text block = %#v", response.Content[0])
	}
	tool := response.Content[1]
	if tool.Type != "tool_use" || tool.ID != "toolu_read_1" || tool.Name != "Read" || string(tool.Input) != `{"file_path":"go.mod"}` {
		t.Fatalf("tool block = %#v", tool)
	}
}

func assertClaudeCodeSafetyIdentifier(t *testing.T, got, source string) {
	t.Helper()
	if len(got) != 64 || strings.ToLower(got) != got {
		t.Fatalf("safety_identifier = %q, want 64 lowercase hexadecimal characters", got)
	}
	if _, err := hex.DecodeString(got); err != nil {
		t.Fatalf("safety_identifier = %q, want hexadecimal digest: %v", got, err)
	}
	if got == source || strings.Contains(got, strings.Repeat("d", 8)) || strings.Contains(got, strings.Repeat("a", 8)) || strings.Contains(got, strings.Repeat("s", 8)) {
		t.Fatalf("safety_identifier was not de-identified: %q", got)
	}
}

func requireClaudeCodeDiagnostic(t *testing.T, diagnostics []Diagnostic, code, path string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Path == path {
			return
		}
	}
	t.Fatalf("missing diagnostic code=%q path=%q in %#v", code, path, diagnostics)
}

func countClaudeCodeDiagnostic(diagnostics []Diagnostic, code string) int {
	count := 0
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			count++
		}
	}
	return count
}
