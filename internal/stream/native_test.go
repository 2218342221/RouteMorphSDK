package stream

import (
	"encoding/json"
	"errors"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestNativeCollectRenderMatrix(t *testing.T) {
	responses := map[Protocol][]byte{
		ProtocolChat:            []byte(`{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`),
		ProtocolResponses:       []byte(`{"id":"r","model":"m","status":"completed","output":[{"id":"i","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`),
		ProtocolMessages:        []byte(`{"id":"a","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`),
		ProtocolGenerateContent: []byte(`{"responseId":"g","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`),
	}
	for protocol, response := range responses {
		t.Run(string(protocol), func(t *testing.T) {
			frames, _, err := RenderNativeResponse(protocol, response)
			if err != nil || len(frames) == 0 {
				t.Fatalf("render frames=%#v error=%v", frames, err)
			}
			body, _, err := CollectNativeResponse(protocol, frames, core.RejectSemanticLoss)
			if err != nil || len(body) == 0 {
				t.Fatalf("collect body=%s error=%v", body, err)
			}
		})
	}
}

func TestNativeCollectorsRejectInvalidTerminalState(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		frames   []streamFrame
		kind     error
	}{
		{"chat missing", ProtocolChat, []streamFrame{{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"x"}}]}`)}}, core.ErrInvalidPayload},
		{"chat provider error", ProtocolChat, []streamFrame{{Data: []byte(`{"error":{"message":"boom"}}`)}}, core.ErrUpstreamResponse},
		{"responses missing", ProtocolResponses, []streamFrame{{Event: "response.created", Data: []byte(`{"type":"response.created"}`)}}, core.ErrInvalidPayload},
		{"responses failed", ProtocolResponses, []streamFrame{{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"status":"failed","error":{"message":"boom"},"output":[]}}`)}}, core.ErrUpstreamResponse},
		{"messages missing", ProtocolMessages, []streamFrame{{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"m","model":"x","usage":{}}}`)}}, core.ErrInvalidPayload},
		{"messages provider error", ProtocolMessages, []streamFrame{{Event: "error", Data: []byte(`{"type":"error","error":{"message":"boom"}}`)}}, core.ErrUpstreamResponse},
		{"gemini missing", ProtocolGenerateContent, []streamFrame{{Data: []byte(`{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}`)}}, core.ErrInvalidPayload},
		{"gemini blocked", ProtocolGenerateContent, []streamFrame{{Data: []byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`)}}, core.ErrUpstreamResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := CollectNativeResponse(test.protocol, test.frames, core.RejectSemanticLoss); !errors.Is(err, test.kind) {
				t.Fatalf("error=%v, want %v", err, test.kind)
			}
		})
	}
	if _, _, err := CollectNativeResponse(Protocol("unknown"), nil, core.RejectSemanticLoss); !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("unknown protocol error=%v", err)
	}
	if _, _, err := RenderNativeResponse(Protocol("unknown"), nil); !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("unknown protocol error=%v", err)
	}
}

func TestFinishReasonValidation(t *testing.T) {
	for _, value := range []string{"length", "max_tokens", "tool_calls", "function_call", "content_filter", "stop"} {
		if _, err := parseChatFinish(value); err != nil {
			t.Fatalf("chat %q: %v", value, err)
		}
	}
	if _, err := parseChatFinish("unknown"); !errors.Is(err, core.ErrUpstreamResponse) {
		t.Fatalf("chat unknown=%v", err)
	}
	for _, value := range []string{"end_turn", "stop_sequence", "max_tokens", "tool_use", "refusal"} {
		if _, err := parseMessagesFinish(value); err != nil {
			t.Fatalf("messages %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "pause_turn", "unknown"} {
		if _, err := parseMessagesFinish(value); !errors.Is(err, core.ErrUpstreamResponse) {
			t.Fatalf("messages %q=%v", value, err)
		}
	}
	for _, value := range []string{"STOP", "MAX_TOKENS", "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY"} {
		if _, err := parseGeminiFinish(value); err != nil {
			t.Fatalf("gemini %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "MALFORMED_FUNCTION_CALL", "OTHER"} {
		if _, err := parseGeminiFinish(value); !errors.Is(err, core.ErrUpstreamResponse) {
			t.Fatalf("gemini %q=%v", value, err)
		}
	}
}

func TestNativeRichResponseRoundTrips(t *testing.T) {
	tests := []struct {
		protocol Protocol
		body     string
	}{
		{ProtocolChat, `{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"thought","refusal":"no","tool_calls":[{"id":"call","type":"function","function":{"name":"lookup","arguments":"{\"q\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":2}}}`},
		{ProtocolResponses, `{"id":"r","model":"m","status":"completed","output":[{"id":"call","type":"function_call","status":"completed","call_id":"call","name":"lookup","arguments":"{\"q\":1}"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`},
		{ProtocolMessages, `{"id":"a","type":"message","role":"assistant","model":"m","content":[{"type":"thinking","thinking":"thought","signature":"sig"},{"type":"tool_use","id":"call","name":"lookup","input":{"q":1}}],"stop_reason":"tool_use","usage":{"input_tokens":2,"output_tokens":3}}`},
	}
	for _, test := range tests {
		t.Run(string(test.protocol), func(t *testing.T) {
			frames, _, err := RenderNativeResponse(test.protocol, []byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := CollectNativeResponse(test.protocol, frames, core.RejectSemanticLoss); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeRenderRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		protocol Protocol
		body     string
		kind     error
	}{
		{ProtocolChat, `{"error":{"message":"boom"}}`, core.ErrUpstreamResponse},
		{ProtocolChat, `{"choices":[]}`, core.ErrUnsupported},
		{ProtocolResponses, `{"status":"failed","error":{"message":"boom"},"output":[]}`, core.ErrUpstreamResponse},
		{ProtocolMessages, `{"type":"message","role":"user","content":[],"stop_reason":"end_turn"}`, core.ErrUpstreamResponse},
		{ProtocolGenerateContent, `{"candidates":[{},{}]}`, core.ErrUnsupported},
	}
	for _, test := range tests {
		if _, _, err := RenderNativeResponse(test.protocol, []byte(test.body)); !errors.Is(err, test.kind) {
			t.Errorf("%s body=%s error=%v, want %v", test.protocol, test.body, err, test.kind)
		}
	}
}

func TestProtocolEnvelopeValidationBranches(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(`[]`)} {
		if _, err := decodeMessagesBlocks(raw, "$.content"); err == nil {
			t.Fatalf("messages content %s unexpectedly accepted", raw)
		}
	}
	blocks, err := decodeMessagesBlocks(json.RawMessage(`"text"`), "$.content")
	if err != nil || len(blocks) != 1 || blocks[0].Text != "text" {
		t.Fatalf("blocks=%#v error=%v", blocks, err)
	}

	invalidMessages := []messagesResponse{
		{Type: "error"},
		{Type: "message", Role: "user"},
		{Type: "message", Role: "assistant", ID: "id"},
		{Type: "message", Role: "assistant", ID: "id", Model: "m", Content: json.RawMessage(`{}`)},
		{Type: "message", Role: "assistant", ID: "id", Model: "m", Content: json.RawMessage(`[]`), StopReason: "end_turn", Usage: struct {
			InputTokens              int64           `json:"input_tokens"`
			OutputTokens             int64           `json:"output_tokens"`
			CacheCreationInputTokens int64           `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int64           `json:"cache_read_input_tokens"`
			ServerToolUse            json.RawMessage `json:"server_tool_use,omitempty"`
		}{InputTokens: -1}},
		{Type: "message", Role: "assistant", ID: "id", Model: "m", Content: json.RawMessage(`[]`), StopReason: "stop_sequence"},
	}
	for _, response := range invalidMessages {
		if err := validateMessagesResponse(response); err == nil {
			t.Fatalf("invalid Messages response accepted: %#v", response)
		}
	}

	items := []responsesItem{
		{Type: "message", Role: "user"},
		{Type: "message", Role: "assistant", Phase: "unknown"},
		{Type: "message", Role: "assistant", EncryptedContent: json.RawMessage(`"secret"`)},
		{Type: "function_call"},
		{Type: "function_call_output"},
	}
	for _, item := range items {
		if err := validateResponsesItems([]responsesItem{item}, "$.output"); err == nil {
			t.Fatalf("invalid Responses item accepted: %#v", item)
		}
	}
	for _, response := range []responsesResponse{
		{Status: "cancelled"},
		{Status: "queued"},
		{Status: "incomplete", IncompleteDetails: &struct {
			Reason string `json:"reason"`
		}{Reason: "unknown"}},
	} {
		if err := validateResponsesTerminal(response); err == nil {
			t.Fatalf("invalid terminal response accepted: %#v", response)
		}
	}

	average := -0.5
	gemini := &geminiResponse{
		Candidates: []geminiCandidate{{
			AvgLogprobs:        &average,
			SafetyRatings:      json.RawMessage(`[{"category":"safe"}]`),
			CitationMetadata:   json.RawMessage(`{"source":"x"}`),
			GroundingMetadata:  json.RawMessage(`{"source":"x"}`),
			URLContextMetadata: json.RawMessage(`{"source":"x"}`),
		}},
		PromptFeedback: json.RawMessage(`{"blockReason":"BLOCK_REASON_UNSPECIFIED"}`),
	}
	gemini.UsageMetadata.PromptTokensDetails = json.RawMessage(`[{"modality":"TEXT","tokenCount":1}]`)
	if _, err := validateGeminiResponseEnvelope(gemini, core.RejectSemanticLoss); !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("strict Gemini metadata error=%v", err)
	}
	diagnostics, err := validateGeminiResponseEnvelope(gemini, core.AllowDocumentedLoss)
	if err != nil || len(diagnostics) < 4 {
		t.Fatalf("diagnostics=%#v error=%v", diagnostics, err)
	}
	if _, err := validateGeminiResponseEnvelope(nil, core.RejectSemanticLoss); !errors.Is(err, core.ErrInvalidPayload) {
		t.Fatalf("nil Gemini response error=%v", err)
	}
}
