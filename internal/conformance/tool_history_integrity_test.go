package conformance

import (
	"context"
	"errors"
	"testing"
)

func TestCrossProtocolToolResultsAreConsumedAtMostOnce(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	chat := []byte(`{"model":"gpt","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"one"},{"role":"tool","tool_call_id":"call_1","content":"two"}]}`)
	for _, target := range []Protocol{ProtocolResponses, ProtocolMessages, ProtocolGenerateContent} {
		t.Run("Chat to "+string(target), func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, target, chat, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("error = %v, want ErrInvalidPayload", err)
			}
		})
	}

	messages := []byte(`{"model":"claude","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"one"},{"type":"tool_result","tool_use_id":"call_1","content":"two"}]}]}`)
	for _, target := range []Protocol{ProtocolChat, ProtocolResponses, ProtocolGenerateContent} {
		t.Run("Messages to "+string(target), func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), ProtocolMessages, target, messages, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("error = %v, want ErrInvalidPayload", err)
			}
		})
	}

	gemini := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"lookup","response":{"result":"one"}}},{"functionResponse":{"id":"call_1","name":"lookup","response":{"result":"two"}}}]}]}`)
	for _, target := range []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages} {
		t.Run("Gemini to "+string(target), func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), ProtocolGenerateContent, target, gemini, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("error = %v, want ErrInvalidPayload", err)
			}
		})
	}
}
