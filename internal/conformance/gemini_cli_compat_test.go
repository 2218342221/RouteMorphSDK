package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const geminiCLISyntheticThoughtSignature = "skip_thought_signature_validator"

func TestGeminiCLICompatibilityAcceptsDefaultGenerationConfig(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}

	// Gemini CLI 0.22.5 falls back to its chat-base model config for the custom
	// model name gpt-5.4. This is the captured generateContent configuration.
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"Call ping when useful."}]}],
		"systemInstruction":{"parts":[{"text":"You are Gemini CLI."}]},
		"tools":[{"functionDeclarations":[{
			"name":"ping",
			"description":"Return the supplied integer.",
			"parametersJsonSchema":{
				"type":"object",
				"properties":{"x":{"type":"integer"}},
				"required":["x"]
			}
		}]}],
		"generationConfig":{
			"temperature":1,
			"topP":0.95,
			"topK":64,
			"thinkingConfig":{"includeThoughts":true,"thinkingBudget":8192}
		}
	}`)

	execution, err := harness.ToUpstreamRequest(
		context.Background(),
		ProtocolGenerateContent,
		ProtocolResponses,
		body,
		conversionOptions{
			CodingAgentCompatibility: true,
			Exchange: exchangeMetadata{
				UpstreamModel: "gpt-5.4",
				Stream:        true,
				StreamSet:     true,
			},
		},
	)
	if err != nil {
		t.Fatalf("Gemini CLI default request: %v", err)
	}

	var request responsesRequest
	if err := json.Unmarshal(execution.Result.Body, &request); err != nil {
		t.Fatal(err)
	}
	if request.Model != "gpt-5.4" || !request.Stream {
		t.Fatalf("model/stream = %q/%v, want gpt-5.4/true: %s", request.Model, request.Stream, execution.Result.Body)
	}
	if request.Temperature == nil || *request.Temperature != 1 {
		t.Fatalf("temperature = %#v, want 1", request.Temperature)
	}
	if request.TopP == nil || *request.TopP != 0.95 {
		t.Fatalf("top_p = %#v, want 0.95", request.TopP)
	}
	if request.Reasoning != nil {
		t.Fatalf("includeThoughts must not be presented as equivalent Responses reasoning: %#v", request.Reasoning)
	}
	if strings.Contains(string(execution.Result.Body), `"topK"`) || strings.Contains(string(execution.Result.Body), `"includeThoughts"`) {
		t.Fatalf("Gemini-only generation settings leaked upstream: %s", execution.Result.Body)
	}
	requireGeminiCLIDiagnostic(t, execution.Result.Diagnostics, "gemini_top_k_ignored", "$.generationConfig.topK")
	requireGeminiCLIDiagnostic(t, execution.Result.Diagnostics, "gemini_include_thoughts_not_preserved", "$.generationConfig.thinkingConfig.includeThoughts")
	requireGeminiCLIDiagnostic(t, execution.Result.Diagnostics, "gemini_thinking_budget_not_preserved", "$.generationConfig.thinkingConfig.thinkingBudget")
}

func TestGeminiCLICompatibilityAcceptsFocusedThinkingBudgetFixture(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hello"}]}],
		"generationConfig":{"thinkingConfig":{"includeThoughts":true,"thinkingBudget":8192}}
	}`)
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolGenerateContent, ProtocolResponses, body, conversionOptions{
		CodingAgentCompatibility: true,
		Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(execution.Result.Body), "thinkingBudget") {
		t.Fatalf("Gemini thinking budget leaked upstream: %s", execution.Result.Body)
	}
	requireGeminiCLIDiagnostic(t, execution.Result.Diagnostics, "gemini_thinking_budget_not_preserved", "$.generationConfig.thinkingConfig.thinkingBudget")
	requireGeminiCLIDiagnostic(t, execution.Result.Diagnostics, "gemini_include_thoughts_not_preserved", "$.generationConfig.thinkingConfig.includeThoughts")
}

func TestGeminiCLICompatibilityAcceptsSyntheticSignatureToolContinuation(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}

	// Gemini CLI adds this documented validator-bypass sentinel when an
	// external model returns a function call without a Gemini thought signature.
	body := []byte(`{
		"contents":[
			{"role":"user","parts":[{"text":"Run ping."}]},
			{"role":"model","parts":[{
				"functionCall":{"id":"call_1","name":"ping","args":{"x":1}},
				"thoughtSignature":"` + geminiCLISyntheticThoughtSignature + `"
			}]},
			{"role":"user","parts":[{
				"functionResponse":{"id":"call_1","name":"ping","response":{"output":"done"}}
			}]}
		],
		"tools":[{"functionDeclarations":[{
			"name":"ping",
			"parametersJsonSchema":{"type":"object","properties":{"x":{"type":"integer"}}}
		}]}]
	}`)

	execution, err := harness.ToUpstreamRequest(
		context.Background(),
		ProtocolGenerateContent,
		ProtocolResponses,
		body,
		conversionOptions{
			CodingAgentCompatibility: true,
			Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4"},
		},
	)
	if err != nil {
		t.Fatalf("Gemini CLI signed tool continuation: %v", err)
	}
	if strings.Contains(string(execution.Result.Body), geminiCLISyntheticThoughtSignature) || strings.Contains(string(execution.Result.Body), "thoughtSignature") {
		t.Fatalf("synthetic Gemini signature leaked upstream: %s", execution.Result.Body)
	}
	requireGeminiCLIDiagnostic(t, execution.Result.Diagnostics, "gemini_synthetic_thought_signature_stripped", "$.contents[1].parts[0].thoughtSignature")

	var request responsesRequest
	if err := json.Unmarshal(execution.Result.Body, &request); err != nil {
		t.Fatal(err)
	}
	var items []responsesItem
	if err := json.Unmarshal(request.Input, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("input item count = %d, want user message + function call + function output: %s", len(items), request.Input)
	}
	if items[1].Type != "function_call" || items[1].CallID != "call_1" || items[1].Name != "ping" || rawString(items[1].Arguments) != `{"x":1}` {
		t.Fatalf("function call was not preserved: %#v", items[1])
	}
	if items[2].Type != "function_call_output" || items[2].CallID != "call_1" || rawString(items[2].Output) != "done" {
		t.Fatalf("function result was not preserved: %#v", items[2])
	}
}

func TestGeminiCLICompatibilityRejectsNonSyntheticThoughtSignatures(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		signature string
	}{
		{name: "near match suffix", signature: geminiCLISyntheticThoughtSignature + "_v2"},
		{name: "near match case", signature: "SKIP_THOUGHT_SIGNATURE_VALIDATOR"},
		{name: "provider opaque signature", signature: "provider-encrypted-state"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{
				"contents":[
					{"role":"model","parts":[{
						"functionCall":{"id":"call_1","name":"ping","args":{}},
						"thoughtSignature":` + string(mustJSON(test.signature)) + `
					}]}
				]
			}`)
			_, err := harness.ToUpstreamRequest(
				context.Background(),
				ProtocolGenerateContent,
				ProtocolResponses,
				body,
				conversionOptions{
					CodingAgentCompatibility: true,
					Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4"},
				},
			)
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$.contents[0].parts[0].thoughtSignature") {
				t.Fatalf("error = %v, want thoughtSignature ErrUnsupported", err)
			}
		})
	}
}

func TestGeminiCLISyntheticSignatureRequiresCompatibilityPolicy(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"contents":[{"role":"model","parts":[{
			"functionCall":{"id":"call_1","name":"ping","args":{}},
			"thoughtSignature":"` + geminiCLISyntheticThoughtSignature + `"
		}]}]
	}`)
	_, err = harness.ToUpstreamRequest(
		context.Background(),
		ProtocolGenerateContent,
		ProtocolResponses,
		body,
		conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gpt-5.4"}},
	)
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$.contents[0].parts[0].thoughtSignature") {
		t.Fatalf("error = %v, want strict thoughtSignature ErrUnsupported", err)
	}
}

func TestGeminiCLICompatibilityPreservesToolErrorPayload(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"contents":[
			{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"read_file","args":{"path":"missing"}},"thoughtSignature":"skip_thought_signature_validator"}]},
			{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"read_file","response":{"error":"ENOENT: missing file"}}}]}
		]
	}`)
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolGenerateContent, ProtocolResponses, body, conversionOptions{
		CodingAgentCompatibility: true,
		Exchange:                 exchangeMetadata{UpstreamModel: "gpt-5.4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var request responsesRequest
	if err := json.Unmarshal(execution.Result.Body, &request); err != nil {
		t.Fatal(err)
	}
	var items []responsesItem
	if err := json.Unmarshal(request.Input, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].Type != "function_call_output" || !strings.Contains(rawString(items[1].Output), "ENOENT: missing file") {
		t.Fatalf("tool error output was not preserved: %#v", items)
	}
	requireGeminiCLIDiagnostic(t, execution.Result.Diagnostics, "gemini_function_error_state_not_representable", "$.contents[1].parts[0].functionResponse.response.error")
}

func TestGeminiCLICompatibilityConvertsResponsesReasoningSummaryStream(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	strict, err := converter.NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prefix := []streamFrame{
		{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4","status":"in_progress","output":[]}}`)},
		{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item_id":"rs_1","item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[]}}`)},
	}
	for _, event := range prefix {
		if _, _, err := strict.Convert(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	summaryDelta := streamFrame{Event: "response.reasoning_summary_text.delta", Data: []byte(`{"type":"response.reasoning_summary_text.delta","item_id":"rs_1","delta":"brief rationale"}`)}
	if _, _, err := strict.Convert(context.Background(), summaryDelta); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict summary delta error = %v, want ErrUnsupported", err)
	}

	reasoningDone := `{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"brief rationale"}],"encrypted_content":"opaque-provider-state"}`
	messageDone := `{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[]}]}`
	events := append(prefix, summaryDelta,
		streamFrame{Event: "response.reasoning_summary_text.done", Data: []byte(`{"type":"response.reasoning_summary_text.done","item_id":"rs_1","text":"brief rationale"}`)},
		streamFrame{Event: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","item_id":"rs_1","item":` + reasoningDone + `}`)},
		streamFrame{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item_id":"msg_1","item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`)},
		streamFrame{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_1","delta":"answer"}`)},
		streamFrame{Event: "response.output_text.done", Data: []byte(`{"type":"response.output_text.done","item_id":"msg_1","text":"answer"}`)},
		streamFrame{Event: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","item_id":"msg_1","item":` + messageDone + `}`)},
		streamFrame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.4","status":"completed","output":[` + reasoningDone + `,` + messageDone + `],"usage":{"input_tokens":1,"output_tokens":3,"total_tokens":4,"output_tokens_details":{"reasoning_tokens":2}}}}`)},
	)
	compatible, err := converter.NewClientStream(context.Background(), conversionOptions{CodingAgentCompatibility: true})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	var diagnostics []Diagnostic
	for index, event := range events {
		frames, eventDiagnostics, err := compatible.Convert(context.Background(), event)
		if err != nil {
			t.Fatalf("event[%d] %s: %v", index, event.Event, err)
		}
		for _, frame := range frames {
			output.Write(frame.Data)
		}
		diagnostics = append(diagnostics, eventDiagnostics...)
	}
	if _, _, err := compatible.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"text":"brief rationale"`) || !strings.Contains(output.String(), `"thought":true`) || !strings.Contains(output.String(), `"text":"answer"`) {
		t.Fatalf("Gemini stream did not preserve compatible thought/text output: %s", output.String())
	}
	requireGeminiCLIDiagnostic(t, diagnostics, "responses_reasoning_summary_approximated_as_gemini_thought", "$.type")
	requireGeminiCLIDiagnostic(t, diagnostics, "responses_reasoning_encrypted_content_not_representable", "$.item.encrypted_content")
}

func requireGeminiCLIDiagnostic(t *testing.T, diagnostics []Diagnostic, code, path string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Path == path {
			if diagnostic.Severity != "warning" {
				t.Fatalf("diagnostic %q severity = %q, want warning", code, diagnostic.Severity)
			}
			return
		}
	}
	t.Fatalf("missing diagnostic code=%q path=%q in %#v", code, path, diagnostics)
}
