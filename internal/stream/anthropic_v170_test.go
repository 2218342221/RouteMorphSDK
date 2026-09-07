package stream

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnthropicV170CollectorPreservesNewStreamFields(t *testing.T) {
	frames := []streamFrame{
		{Event: "message_start", Data: []byte(`{
			"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[],
			"container":null,"stop_reason":null,"stop_sequence":null,"stop_details":null,
			"usage":{"input_tokens":10,"output_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,
			"cache_creation":{"ephemeral_5m_input_tokens":2,"ephemeral_1h_input_tokens":0},"inference_geo":"us","service_tier":"standard"}}
		}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"","citations":[]}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"type":"char_location","cited_text":"ok","document_index":0,"document_title":"doc","start_char_index":0,"end_char_index":2}}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Event: "message_delta", Data: []byte(`{
			"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null,"stop_details":null,"container":{"id":"container_1"}},
			"usage":{"input_tokens":12,"output_tokens":9,"cache_creation_input_tokens":4,"cache_read_input_tokens":5,
			"output_tokens_details":{"thinking_tokens":7},"server_tool_use":{"web_search_requests":2,"web_fetch_requests":1}}
		}`)},
		{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
	body, _, err := collectMessagesStreamResponse(frames)
	if err != nil {
		t.Fatal(err)
	}
	var got messagesResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Usage.OutputTokensDetails == nil || got.Usage.OutputTokensDetails.ThinkingTokens != 7 || got.Usage.InferenceGeo != "us" || got.Usage.ServiceTier != "standard" {
		t.Fatalf("usage = %#v", got.Usage)
	}
	if !strings.Contains(string(got.Container), `"id":"container_1"`) || !strings.Contains(string(got.Content), `"citations":[{`) {
		t.Fatalf("response = %s", body)
	}
}

func TestAnthropicV170RendererUsesSignatureDeltaAndPreservesTerminalState(t *testing.T) {
	body := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude",
		"content":[{"type":"thinking","thinking":"work","signature":"sig"}],
		"stop_reason":"stop_sequence","stop_sequence":"END","container":{"id":"container_1"},
		"usage":{"input_tokens":3,"output_tokens":8,"output_tokens_details":{"thinking_tokens":5},"server_tool_use":{"web_search_requests":1}}
	}`)
	frames, _, err := renderMessagesResponseStream(body)
	if err != nil {
		t.Fatal(err)
	}
	var sawEmptyStart, sawThinking, sawSignature, sawStop bool
	for _, frame := range frames {
		text := string(frame.Data)
		sawEmptyStart = sawEmptyStart || frame.Event == "content_block_start" && strings.Contains(text, `"signature":""`)
		sawThinking = sawThinking || strings.Contains(text, `"type":"thinking_delta"`)
		sawSignature = sawSignature || strings.Contains(text, `"type":"signature_delta"`) && strings.Contains(text, `"signature":"sig"`)
		sawStop = sawStop || frame.Event == "message_delta" && strings.Contains(text, `"stop_sequence":"END"`) && strings.Contains(text, `"thinking_tokens":5`) && strings.Contains(text, `"container":{"id":"container_1"}`)
	}
	if !sawEmptyStart || !sawThinking || !sawSignature || !sawStop {
		t.Fatalf("frames = %#v", frames)
	}

	roundTrip, _, err := collectMessagesStreamResponse(frames)
	if err != nil {
		t.Fatal(err)
	}
	var got messagesResponse
	if err := json.Unmarshal(roundTrip, &got); err != nil {
		t.Fatal(err)
	}
	if got.StopSequence != "END" || got.Usage.OutputTokensDetails == nil || got.Usage.OutputTokensDetails.ThinkingTokens != 5 {
		t.Fatalf("round trip = %s", roundTrip)
	}
}
