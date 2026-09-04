package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const testTokenLogprob = `{"token":"hello","bytes":[104,101,108,108,111],"logprob":-0.25,"top_logprobs":[{"token":"hello","bytes":[104,101,108,108,111],"logprob":-0.25}]}`

func TestChatResponsesNonStreamLogprobsRoundTrip(t *testing.T) {
	chatUpstream := newChatResponsesRoute(routeSpec{From: ProtocolResponses, To: ProtocolChat})
	chatBody := []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop","logprobs":{"content":[` + testTokenLogprob + `],"refusal":null}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	responsesResult, err := chatUpstream.ToClientResponse(context.Background(), chatBody, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var responses responsesResponse
	if err := json.Unmarshal(responsesResult.Body, &responses); err != nil {
		t.Fatal(err)
	}
	var content []responsesContentPart
	if len(responses.Output) != 1 || json.Unmarshal(responses.Output[0].Content, &content) != nil || len(content) != 1 {
		t.Fatalf("unexpected Responses output: %s", responsesResult.Body)
	}
	var entries []json.RawMessage
	err = json.Unmarshal(content[0].Logprobs, &entries)
	if err != nil || len(entries) != 1 || !jsonObjectsEqual(entries[0], []byte(testTokenLogprob)) {
		t.Fatalf("Responses logprobs = %s, err=%v", content[0].Logprobs, err)
	}

	responsesUpstream := newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})
	chatResult, err := responsesUpstream.ToClientResponse(context.Background(), responsesResult.Body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatResponse
	if err := json.Unmarshal(chatResult.Body, &chat); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Content []json.RawMessage `json:"content"`
		Refusal []json.RawMessage `json:"refusal"`
	}
	err = json.Unmarshal(chat.Choices[0].Logprobs, &envelope)
	entries, refusal := envelope.Content, envelope.Refusal
	if err != nil || len(entries) != 1 || len(refusal) != 0 || !jsonObjectsEqual(entries[0], []byte(testTokenLogprob)) {
		t.Fatalf("Chat logprobs = %s, err=%v", chat.Choices[0].Logprobs, err)
	}
}

func TestChatResponsesStreamLogprobsRoundTrip(t *testing.T) {
	chatUpstream := newChatResponsesRoute(routeSpec{From: ProtocolResponses, To: ProtocolChat})
	toResponses, err := chatUpstream.NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []streamFrame{
		{Data: []byte(`{"id":"chat_1","model":"m","created":1,"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`)},
		{Data: []byte(`{"id":"chat_1","model":"m","created":1,"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null,"logprobs":{"content":[` + testTokenLogprob + `],"refusal":null}}]}`)},
		{Data: []byte(`{"id":"chat_1","model":"m","created":1,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)},
		{Data: []byte(`[DONE]`), Done: true},
	}
	var responseFrames []streamFrame
	for _, input := range inputs {
		frames, _, err := toResponses.Convert(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		responseFrames = append(responseFrames, frames...)
	}
	joined := streamFrameText(responseFrames)
	if !strings.Contains(joined, `"response.output_text.delta"`) || !strings.Contains(joined, `"logprobs":[`+testTokenLogprob+`]`) {
		t.Fatalf("Responses stream lost logprobs: %s", joined)
	}

	responsesUpstream := newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})
	toChat, err := responsesUpstream.NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var chatFrames []streamFrame
	for _, input := range responseFrames {
		frames, _, err := toChat.Convert(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		chatFrames = append(chatFrames, frames...)
	}
	joined = streamFrameText(chatFrames)
	if !strings.Contains(joined, `"content":"hello"`) || !strings.Contains(joined, `"logprobs":{"content":[`+testTokenLogprob+`],"refusal":null}`) {
		t.Fatalf("Chat stream lost logprobs: %s", joined)
	}
}

func TestChatRefusalLogprobsFailClosedForResponses(t *testing.T) {
	converter := newChatResponsesRoute(routeSpec{From: ProtocolResponses, To: ProtocolChat})
	body := []byte(`{"id":"chat_1","model":"m","choices":[{"index":0,"message":{"role":"assistant","refusal":"no"},"finish_reason":"stop","logprobs":{"content":null,"refusal":[` + testTokenLogprob + `]}}]}`)
	if _, err := converter.ToClientResponse(context.Background(), body, conversionOptions{}); err == nil {
		t.Fatal("expected refusal logprobs to fail closed")
	}
}

func TestChatGeminiLogprobsFailClosedOrReportDocumentedLoss(t *testing.T) {
	chatToGemini := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
	geminiBody := []byte(`{"responseId":"r","modelVersion":"m","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP","avgLogprobs":-0.25,"logprobsResult":{"topCandidates":[{"candidates":[{"token":"hello","tokenId":1,"logProbability":-0.25}]}],"chosenCandidates":[{"token":"hello","tokenId":1,"logProbability":-0.25}]}}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	if _, err := chatToGemini.ToClientResponse(context.Background(), geminiBody, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict Gemini->Chat logprobs error = %v, want ErrUnsupported", err)
	}
	result, err := chatToGemini.ToClientResponse(context.Background(), geminiBody, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil || !directHasDiagnostic(result.Diagnostics, "gemini_logprobs_not_representable") {
		t.Fatalf("documented Gemini->Chat loss result=%#v err=%v", result, err)
	}

	geminiToChat := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	chatBody := []byte(`{"id":"c","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop","logprobs":{"content":[` + testTokenLogprob + `],"refusal":null}}]}`)
	if _, err := geminiToChat.ToClientResponse(context.Background(), chatBody, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict Chat->Gemini logprobs error = %v, want ErrUnsupported", err)
	}
	result, err = geminiToChat.ToClientResponse(context.Background(), chatBody, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil || !directHasDiagnostic(result.Diagnostics, "chat_logprobs_not_representable") {
		t.Fatalf("documented Chat->Gemini loss result=%#v err=%v", result, err)
	}
}

func streamFrameText(frames []streamFrame) string {
	var result strings.Builder
	for _, frame := range frames {
		result.Write(frame.Data)
	}
	return result.String()
}
