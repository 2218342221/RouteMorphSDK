package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBufferedRouteStreamEnforcesTotalByteLimit(t *testing.T) {
	// The budget includes 64 bytes of per-frame storage overhead plus the
	// event/data bytes, so the first frame exactly fills it.
	converter := newBoundedFrameStream(ProtocolMessages, 72, nil)
	if _, _, err := converter.Convert(context.Background(), streamFrame{Event: "x", Data: []byte("1234567")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := converter.Convert(context.Background(), streamFrame{Data: []byte("x")}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("error = %v, want ErrInvalidPayload", err)
	}
}

func TestDecodeBufferedMessagesStreamRequiresTerminalLifecycle(t *testing.T) {
	valid := []streamFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude","usage":{"input_tokens":7,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Event: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`)},
		{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
	body, _, err := collectNativeStreamResponse(ProtocolMessages, valid, rejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var response messagesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Usage.InputTokens+response.Usage.CacheReadInputTokens+response.Usage.CacheCreationInputTokens != 10 || response.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %#v", response.Usage)
	}

	for _, frames := range [][]streamFrame{
		valid[:len(valid)-1],
		{{Event: "error", Data: []byte(`{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)}},
	} {
		_, _, err := collectNativeStreamResponse(ProtocolMessages, frames, rejectSemanticLoss)
		if err == nil {
			t.Fatalf("invalid stream unexpectedly succeeded: %#v", frames)
		}
		if len(frames) == 1 && (!errors.Is(err, ErrUpstreamResponse) || !strings.Contains(err.Error(), "busy")) {
			t.Fatalf("error event = %v", err)
		}
	}
}

func TestBufferedMessagesStreamAppliesResponseLossPolicy(t *testing.T) {
	base := func(startBlock, deltaBlock, stopReason, stopSequence, usage string) []streamFrame {
		return []streamFrame{
			{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude","usage":{"input_tokens":2}}}`)},
			{Event: "content_block_start", Data: []byte(startBlock)},
			{Event: "content_block_delta", Data: []byte(deltaBlock)},
			{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
			{Event: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"` + stopReason + `","stop_sequence":` + stopSequence + `},"usage":` + usage + `}`)},
			{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
		}
	}
	run := func(t *testing.T, frames []streamFrame, policy lossPolicy) ([]Diagnostic, error) {
		t.Helper()
		route := newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages})
		converter := newBufferedRouteStream(
			route.Specification(),
			conversionOptions{LossPolicy: policy},
			route.ToClientResponse,
		)
		for _, frame := range frames {
			if _, _, err := converter.Convert(context.Background(), frame); err != nil {
				return nil, err
			}
		}
		_, diagnostics, err := converter.Finalize(context.Background())
		return diagnostics, err
	}

	thinking := base(
		`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`,
		"end_turn", "null", `{"output_tokens":1}`,
	)
	if _, err := run(t, thinking, allowDocumentedLoss); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("signed thinking error = %v, want ErrUnsupported", err)
	}

	stop := base(
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		"stop_sequence", `"END"`, `{"output_tokens":1}`,
	)
	if _, err := run(t, stop, rejectSemanticLoss); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("stop sequence error = %v, want ErrUnsupported", err)
	}
	if diagnostics, err := run(t, stop, allowDocumentedLoss); err != nil || !hasBufferedDiagnostic(diagnostics, "stop_sequence_not_representable") {
		t.Fatalf("stop sequence allow diagnostics=%#v error=%v", diagnostics, err)
	}

	cache := base(
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`,
		"end_turn", "null", `{"output_tokens":1,"cache_creation_input_tokens":2}`,
	)
	if _, err := run(t, cache, rejectSemanticLoss); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("cache creation error = %v, want ErrUnsupported", err)
	}
	if diagnostics, err := run(t, cache, allowDocumentedLoss); err != nil || !hasBufferedDiagnostic(diagnostics, "cache_creation_usage_not_representable") {
		t.Fatalf("cache creation allow diagnostics=%#v error=%v", diagnostics, err)
	}
}

func hasBufferedDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func TestDecodeBufferedMessagesStreamUsesLatestCumulativeUsage(t *testing.T) {
	frames := []streamFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude","usage":{"input_tokens":7,"cache_read_input_tokens":2,"cache_creation_input_tokens":1,"output_tokens":0}}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`)},
		{Event: "content_block_stop", Data: []byte(`{"type":"content_block_stop","index":0}`)},
		{Event: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":8,"cache_read_input_tokens":3,"cache_creation_input_tokens":2,"output_tokens":5}}`)},
		{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
	body, _, err := collectNativeStreamResponse(ProtocolMessages, frames, allowDocumentedLoss)
	if err != nil {
		t.Fatal(err)
	}
	var response messagesResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Usage.InputTokens != 8 || response.Usage.CacheReadInputTokens != 3 || response.Usage.CacheCreationInputTokens != 2 || response.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestDecodeBufferedMessagesStreamValidatesEventAndDeltaTypes(t *testing.T) {
	mismatchedEvent := []streamFrame{{Event: "ping", Data: []byte(`{"type":"message_start"}`)}}
	if _, _, err := collectNativeStreamResponse(ProtocolMessages, mismatchedEvent, rejectSemanticLoss); err == nil {
		t.Fatal("mismatched SSE event name was accepted")
	}
	mismatchedDelta := []streamFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"msg_1","model":"claude","usage":{"input_tokens":1}}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Event: "content_block_delta", Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`)},
	}
	if _, _, err := collectNativeStreamResponse(ProtocolMessages, mismatchedDelta, rejectSemanticLoss); err == nil {
		t.Fatal("delta type incompatible with its content block was accepted")
	}
}
