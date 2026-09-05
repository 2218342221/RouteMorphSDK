package conformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestGeminiResponsesUnsignedReasoningRoundTrip(t *testing.T) {
	toResponses := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	converted, err := toResponses.ToClientResponse(context.Background(), []byte(`{
		"responseId":"gem_1","modelVersion":"gemini",
		"candidates":[{"content":{"role":"model","parts":[{"text":"think","thought":true},{"text":"answer"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"thoughtsTokenCount":1,"totalTokenCount":3}
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var responses responsesResponse
	if err := json.Unmarshal(converted.Body, &responses); err != nil {
		t.Fatal(err)
	}
	if len(responses.Output) != 2 || responses.Output[0].Type != "reasoning" || responses.Output[1].Type != "message" {
		t.Fatalf("output = %#v", responses.Output)
	}
	if string(responses.Output[0].Summary) != `[]` || !strings.Contains(string(responses.Output[0].Content), `"type":"reasoning_text"`) || !strings.Contains(string(responses.Output[0].Content), `"text":"think"`) {
		t.Fatalf("reasoning item = %#v", responses.Output[0])
	}

	toGemini := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	roundTrip, err := toGemini.ToClientResponse(context.Background(), converted.Body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiResponse
	if err := json.Unmarshal(roundTrip.Body, &gemini); err != nil {
		t.Fatal(err)
	}
	parts := gemini.Candidates[0].Content.Parts
	if len(parts) != 2 || !parts[0].Thought || parts[0].Text != "think" || parts[1].Thought || parts[1].Text != "answer" {
		t.Fatalf("Gemini parts = %#v", parts)
	}
}

func TestGeminiToResponsesStreamEmitsReasoningTextLifecycle(t *testing.T) {
	stream, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frames, _, err := stream.Convert(context.Background(), streamFrame{Data: []byte(`{
		"responseId":"gem_1","modelVersion":"gemini",
		"candidates":[{"content":{"role":"model","parts":[{"text":"think","thought":true},{"text":"answer"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"thoughtsTokenCount":1,"totalTokenCount":3}
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := stream.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, frame := range frames {
		joined.WriteString(frame.Event)
		joined.Write(frame.Data)
	}
	for _, want := range []string{"response.reasoning_text.delta", "response.reasoning_text.done", `"type":"reasoning_text"`, `"summary":[]`, "response.completed"} {
		if !strings.Contains(joined.String(), want) {
			t.Fatalf("stream missing %q: %s", want, joined.String())
		}
	}
}

func TestResponsesToGeminiStreamEmitsUnsignedThought(t *testing.T) {
	stream, err := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	itemAdded := `{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[],"content":[]}`
	itemDone := `{"id":"rs_1","type":"reasoning","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"think"}]}`
	events := []streamFrame{
		{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"responses","status":"in_progress","output":[]}}`)},
		{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item_id":"rs_1","item":` + itemAdded + `}`)},
		{Event: "response.content_part.added", Data: []byte(`{"type":"response.content_part.added","item_id":"rs_1","part":{"type":"reasoning_text","text":""}}`)},
		{Event: "response.reasoning_text.delta", Data: []byte(`{"type":"response.reasoning_text.delta","item_id":"rs_1","delta":"think"}`)},
		{Event: "response.reasoning_text.done", Data: []byte(`{"type":"response.reasoning_text.done","item_id":"rs_1","text":"think"}`)},
		{Event: "response.content_part.done", Data: []byte(`{"type":"response.content_part.done","item_id":"rs_1","part":{"type":"reasoning_text","text":"think"}}`)},
		{Event: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","item_id":"rs_1","item":` + itemDone + `}`)},
		{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"responses","status":"completed","output":[` + itemDone + `],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"output_tokens_details":{"reasoning_tokens":1}}}}`)},
	}
	var joined strings.Builder
	for _, event := range events {
		frames, _, err := stream.Convert(context.Background(), event)
		if err != nil {
			t.Fatalf("%s: %v", event.Event, err)
		}
		for _, frame := range frames {
			joined.Write(frame.Data)
		}
	}
	if _, _, err := stream.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joined.String(), `"text":"think"`) || !strings.Contains(joined.String(), `"thought":true`) {
		t.Fatalf("Gemini stream = %s", joined.String())
	}
}
