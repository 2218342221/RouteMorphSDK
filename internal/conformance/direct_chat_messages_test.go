package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDirectChatMessagesPreservesStopAndTools(t *testing.T) {
	chatToMessages := newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})
	result, err := chatToMessages.ToUpstreamRequest(context.Background(), []byte(`{"model":"client","stop":["END","DONE"],"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
	if err != nil {
		t.Fatal(err)
	}
	var messages messagesRequest
	if err := json.Unmarshal(result.Body, &messages); err != nil {
		t.Fatal(err)
	}
	if messages.Model != "provider" || len(messages.StopSequences) != 2 || len(messages.Tools) != 1 || !jsonValuePresent(messages.Tools[0].InputSchema) {
		t.Fatalf("Messages request = %#v", messages)
	}

	messagesToChat := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat})
	result, err = messagesToChat.ToUpstreamRequest(context.Background(), []byte(`{"model":"client","max_tokens":64,"stop_sequences":["END"],"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(result.Body, &chat); err != nil {
		t.Fatal(err)
	}
	stop, err := decodeStop(ProtocolChat, chat.Stop)
	if err != nil || len(stop) != 1 || stop[0] != "END" || len(chat.Tools) != 1 {
		t.Fatalf("Chat request = %#v, stop=%v err=%v", chat, stop, err)
	}
}

func TestDirectChatMessagesUsageRoundTrip(t *testing.T) {
	converter := newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})
	result, err := converter.ToClientResponse(context.Background(), []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"provider","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"cache_read_input_tokens":3,"cache_creation_input_tokens":2,"output_tokens":4}}`), conversionOptions{Exchange: exchangeMetadata{ClientModel: "client"}, LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatResponse
	if err := json.Unmarshal(result.Body, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Model != "client" || chat.Usage.PromptTokens != 12 || chat.Usage.PromptDetails.CachedTokens != 3 || chat.Usage.TotalTokens != 16 {
		t.Fatalf("Chat usage = %#v", chat.Usage)
	}
	if !hasChatMessagesDiagnostic(result.Diagnostics, "cache_creation_usage_not_representable") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestMessagesToChatRejectsUnsupportedLatestRequestFields(t *testing.T) {
	converter := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat})
	for _, field := range []string{
		`"top_k":10`,
		`"inference_geo":"us"`,
		`"cache_control":{"type":"ephemeral"}`,
		`"service_tier":"auto"`,
	} {
		body := []byte(`{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],` + field + `}`)
		if _, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
			t.Errorf("field %s error = %v, want ErrUnsupported", field, err)
		}
	}
}

func TestChatIngressRejectsUnknownFieldsAndAssistantAudio(t *testing.T) {
	chatToMessages := newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})
	if _, err := chatToMessages.ToUpstreamRequest(context.Background(), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"future_field":true}`), conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unknown top-level field error = %v, want ErrUnsupported", err)
	}

	body := []byte(`{"model":"m","messages":[{"role":"assistant","content":"spoken","audio":{"id":"audio_1","expires_at":123,"data":"AA==","transcript":"spoken"}}]}`)
	for _, test := range []struct {
		name      string
		converter routeConverter
		path      string
	}{
		{"responses", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses}), "$.messages[0].audio"},
		{"messages", chatToMessages, "$.messages[0].audio"},
		{"gemini", newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent}), "$.messages[0].audio"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.converter.ToUpstreamRequest(context.Background(), body, conversionOptions{})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.path)
			}
		})
	}
}

func TestMessagesToChatSplitsParallelToolResults(t *testing.T) {
	converter := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat})
	result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"claude","max_tokens":64,
		"messages":[{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"call_1","content":"one"},
			{"type":"tool_result","tool_use_id":"call_2","content":"two"}
		]}]
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var target chatRequest
	if err := json.Unmarshal(result.Body, &target); err != nil {
		t.Fatal(err)
	}
	if len(target.Messages) != 2 || target.Messages[0].Role != "tool" || target.Messages[0].ToolCallID != "call_1" || target.Messages[1].Role != "tool" || target.Messages[1].ToolCallID != "call_2" {
		t.Fatalf("messages = %#v", target.Messages)
	}
}

func TestMessagesToChatToolResultErrorUsesLossPolicy(t *testing.T) {
	converter := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat})
	body := []byte(`{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"failed","is_error":true}]}]}`)
	if _, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict error = %v, want ErrUnsupported", err)
	}
	result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	if !hasChatMessagesDiagnostic(result.Diagnostics, "tool_result_error_state_not_representable") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestMessagesToChatResponseLossUsesPolicy(t *testing.T) {
	converter := newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})
	for _, test := range []struct {
		name string
		body string
		code string
	}{
		{"stop sequence", `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"stop_sequence","stop_sequence":"END","usage":{"input_tokens":1,"output_tokens":1}}`, "stop_sequence_not_representable"},
		{"cache creation", `{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"cache_creation_input_tokens":2,"output_tokens":1}}`, "cache_creation_usage_not_representable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := converter.ToClientResponse(context.Background(), []byte(test.body), conversionOptions{}); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("strict error = %v, want ErrUnsupported", err)
			}
			result, err := converter.ToClientResponse(context.Background(), []byte(test.body), conversionOptions{LossPolicy: allowDocumentedLoss})
			if err != nil {
				t.Fatal(err)
			}
			if !hasChatMessagesDiagnostic(result.Diagnostics, test.code) {
				t.Fatalf("diagnostics = %#v", result.Diagnostics)
			}
		})
	}
}

func hasChatMessagesDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
