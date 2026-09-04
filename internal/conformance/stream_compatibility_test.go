package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const terminalOnlyResponsesEvent = `{"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":1,"model":"provider","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello"}]},{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"lookup","arguments":"{\"q\":1}"}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`

func TestResponsesTerminalOnlyStreamReplaysOutput(t *testing.T) {
	tests := []struct {
		name      string
		Converter routeConverter
		want      []string
	}{
		{"chat", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses}), []string{`"content":"hello"`, `"arguments":"{\"q\":1}"`}},
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses}), []string{`"text":"hello"`, `"partial_json":"{\"q\":1}"`}},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses}), []string{`"text":"hello"`, `"args":{"q":1}`}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream, err := test.Converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			frames, _, err := stream.Convert(context.Background(), streamFrame{Event: "response.completed", Data: []byte(terminalOnlyResponsesEvent)})
			if err != nil {
				t.Fatal(err)
			}
			joined := ""
			for _, frame := range frames {
				joined += string(frame.Data)
			}
			for _, want := range test.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("frames do not contain %s: %s", want, joined)
				}
			}
			if strings.Index(joined, test.want[0]) > strings.Index(joined, test.want[1]) {
				t.Fatalf("terminal output order was not preserved: %s", joined)
			}
			if _, _, err := stream.Finalize(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResponsesToMessagesAcceptsDeltaFirstStream(t *testing.T) {
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	stream, err := converter.NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frames, diagnostics, err := stream.Convert(context.Background(), streamFrame{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","delta":"hello"}`)})
	if err != nil || len(frames) < 3 || len(diagnostics) == 0 {
		t.Fatalf("frames=%#v diagnostics=%#v err=%v", frames, diagnostics, err)
	}
	if _, _, err := stream.Convert(context.Background(), streamFrame{Event: "response.completed", Data: []byte(terminalOnlyResponsesEvent)}); err != nil {
		t.Fatal(err)
	}
}

func TestChatToResponsesRejectsTruncatedStream(t *testing.T) {
	converter := newChatResponsesRoute(routeSpec{From: ProtocolResponses, To: ProtocolChat})
	stream, err := converter.NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := stream.Convert(context.Background(), streamFrame{Data: []byte(`{"id":"chat_1","model":"m","choices":[{"index":0,"delta":{"content":"partial"}}]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := stream.Finalize(context.Background()); !errors.Is(err, ErrUpstreamResponse) {
		t.Fatalf("Finalize error = %v, want ErrUpstreamResponse", err)
	}
}

func TestBufferedGeminiAcceptsIncrementalChunks(t *testing.T) {
	frames := []streamFrame{
		{Data: []byte(`{"responseId":"r1","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"hel"}]}}]}`)},
		{Data: []byte(`{"responseId":"r1","modelVersion":"m","candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}]}`)},
		{Data: []byte(`{"responseId":"r1","modelVersion":"m","usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`)},
	}
	body, _, err := collectNativeStreamResponse(ProtocolGenerateContent, frames, rejectSemanticLoss)
	if err != nil {
		t.Fatal(err)
	}
	var response geminiResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Candidates) != 1 || len(response.Candidates[0].Content.Parts) != 2 || response.Candidates[0].Content.Parts[0].Text+response.Candidates[0].Content.Parts[1].Text != "hello" || response.UsageMetadata.TotalTokenCount != 3 {
		t.Fatalf("response = %#v", response)
	}
}

func TestBufferedStreamsRejectContentAfterTerminal(t *testing.T) {
	geminiFrames := []streamFrame{
		{Data: []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)},
		{Data: []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"late"}]}}]}`)},
	}
	if _, _, err := collectNativeStreamResponse(ProtocolGenerateContent, geminiFrames, rejectSemanticLoss); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("Gemini error = %v, want ErrInvalidPayload", err)
	}
	chatFrames := []streamFrame{
		{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)},
		{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"late"}}]}`)},
	}
	if _, _, err := collectNativeStreamResponse(ProtocolChat, chatFrames, rejectSemanticLoss); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("Chat error = %v, want ErrInvalidPayload", err)
	}
}

func TestGeminiToResponsesRejectsOrphanToolResult(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	_, err := converter.ToUpstreamRequest(context.Background(), []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"c1","name":"foo","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"c2","name":"bar","response":{"ok":true}}}]}]}`), conversionOptions{})
	if !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("error = %v, want ErrInvalidPayload", err)
	}
}

func TestCrossProtocolResponseConfigurationIsFailClosed(t *testing.T) {
	responses := []byte(`{"model":"m","input":"hi","text":{"verbosity":"high","format":{"type":"json_schema","name":"named","strict":true,"schema":{"type":"object"}}}}`)
	for _, converter := range []routeConverter{
		newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages}),
		newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}),
	} {
		if _, err := converter.ToUpstreamRequest(context.Background(), responses, conversionOptions{LossPolicy: rejectSemanticLoss}); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%T error = %v, want ErrUnsupported", converter, err)
		}
	}
}

func TestBufferedStreamsApplyResponseLossPolicy(t *testing.T) {
	converter := newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})
	stream, err := converter.NewClientStream(context.Background(), conversionOptions{LossPolicy: rejectSemanticLoss})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = stream.Convert(context.Background(), streamFrame{Data: []byte(`{"id":"m1","model":"claude","type":"message_start","message":{"id":"m1","model":"claude","usage":{"input_tokens":1}}}`), Event: "message_start"})
	_, _, _ = stream.Convert(context.Background(), streamFrame{Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":"sig"}}`), Event: "content_block_start"})
	_, _, _ = stream.Convert(context.Background(), streamFrame{Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"secret"}}`), Event: "content_block_delta"})
	_, _, _ = stream.Convert(context.Background(), streamFrame{Data: []byte(`{"type":"content_block_stop","index":0}`), Event: "content_block_stop"})
	_, _, _ = stream.Convert(context.Background(), streamFrame{Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`), Event: "message_delta"})
	_, _, _ = stream.Convert(context.Background(), streamFrame{Data: []byte(`{"type":"message_stop"}`), Event: "message_stop"})
	if _, _, err := stream.Finalize(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Finalize error = %v, want ErrUnsupported", err)
	}
}

func TestIncrementalStreamsRejectContentAfterTerminal(t *testing.T) {
	chatSource, err := newChatResponsesRoute(routeSpec{From: ProtocolResponses, To: ProtocolChat}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := chatSource.Convert(context.Background(), streamFrame{Data: []byte(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := chatSource.Convert(context.Background(), streamFrame{Data: []byte(`{"choices":[{"index":0,"delta":{"content":"late"}}]}`)}); !errors.Is(err, ErrUpstreamResponse) {
		t.Fatalf("Chat late-content error = %v, want ErrUpstreamResponse", err)
	}

	responsesSource, err := newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := responsesSource.Convert(context.Background(), streamFrame{Event: "response.completed", Data: []byte(terminalOnlyResponsesEvent)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := responsesSource.Convert(context.Background(), streamFrame{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","delta":"late"}`)}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("Responses late-content error = %v, want ErrInvalidPayload", err)
	}

	messagesFrames := []streamFrame{
		{Event: "message_start", Data: []byte(`{"type":"message_start","message":{"id":"m1","model":"claude","usage":{"input_tokens":1}}}`)},
		{Event: "message_delta", Data: []byte(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`)},
		{Event: "content_block_start", Data: []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":"late"}}`)},
		{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
	}
	if _, _, err := collectNativeStreamResponse(ProtocolMessages, messagesFrames, rejectSemanticLoss); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("Messages late-content error = %v, want ErrInvalidPayload", err)
	}
}

func TestResponsesClientStreamsRequireMatchingEventType(t *testing.T) {
	for _, target := range []struct {
		name      string
		converter routeConverter
	}{
		{"chat", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})},
	} {
		t.Run(target.name+"/missing", func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = stream.Convert(context.Background(), streamFrame{Event: "response.in_progress", Data: []byte(`{"response":{"status":"in_progress"}}`)})
			if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "$.type") {
				t.Fatalf("error = %v, want missing type error", err)
			}
		})
		t.Run(target.name+"/mismatch", func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = stream.Convert(context.Background(), streamFrame{Event: "response.queued", Data: []byte(`{"type":"response.in_progress"}`)})
			if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "does not match") {
				t.Fatalf("error = %v, want event/type mismatch", err)
			}
		})
	}
}

func TestResponsesClientStreamsFailClosedForUnsupportedEvents(t *testing.T) {
	tests := []struct {
		name  string
		event streamFrame
	}{
		{"unknown", streamFrame{Event: "response.future.delta", Data: []byte(`{"type":"response.future.delta","delta":"secret"}`)}},
		{"legacy done", streamFrame{Event: "response.done", Data: []byte(`{"type":"response.done"}`)}},
		{"legacy cancelled", streamFrame{Event: "response.cancelled", Data: []byte(`{"type":"response.cancelled"}`)}},
		{"hosted tool", streamFrame{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"ws_1","type":"web_search_call","status":"in_progress"}}`)}},
		{"audio delta", streamFrame{Event: "response.audio.delta", Data: []byte(`{"type":"response.audio.delta","delta":"AA=="}`)}},
	}
	for _, target := range []struct {
		name      string
		converter routeConverter
	}{
		{"chat", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
	} {
		for _, test := range tests {
			t.Run(target.name+"/"+test.name, func(t *testing.T) {
				stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
				if err != nil {
					t.Fatal(err)
				}
				_, _, err = stream.Convert(context.Background(), test.event)
				if !errors.Is(err, ErrUnsupported) {
					t.Fatalf("error = %v, want ErrUnsupported", err)
				}
			})
		}
	}
}

func TestResponsesClientStreamsValidatePartAndItemIdentity(t *testing.T) {
	for _, target := range []struct {
		name      string
		converter routeConverter
	}{
		{"chat", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})},
	} {
		t.Run(target.name+"/annotation", func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			added := streamFrame{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`)}
			if _, _, err := stream.Convert(context.Background(), added); err != nil {
				t.Fatal(err)
			}
			part := streamFrame{Event: "response.content_part.added", Data: []byte(`{"type":"response.content_part.added","item_id":"msg_1","content_index":0,"part":{"type":"output_text","text":"","annotations":[{"type":"url_citation","url":"https://example.com"}]}}`)}
			_, _, err = stream.Convert(context.Background(), part)
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "annotations") {
				t.Fatalf("error = %v, want annotation ErrUnsupported", err)
			}
		})
		t.Run(target.name+"/identity", func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			added := streamFrame{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":""}}`)}
			if _, _, err := stream.Convert(context.Background(), added); err != nil {
				t.Fatal(err)
			}
			done := streamFrame{Event: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","item_id":"fc_1","item":{"id":"fc_1","type":"function_call","call_id":"call_2","name":"lookup","arguments":"{}"}}`)}
			_, _, err = stream.Convert(context.Background(), done)
			if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "identity") {
				t.Fatalf("error = %v, want identity error", err)
			}
		})
	}
}

func TestResponsesClientStreamsRequireCompletedToolArguments(t *testing.T) {
	for _, target := range responsesClientStreamTargets() {
		t.Run(target.name, func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			added := streamFrame{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":""}}`)}
			if _, _, err := stream.Convert(context.Background(), added); err != nil {
				t.Fatal(err)
			}
			done := streamFrame{Event: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","item_id":"fc_1","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup"}}`)}
			_, _, err = stream.Convert(context.Background(), done)
			if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "arguments") {
				t.Fatalf("error = %v, want missing arguments error", err)
			}
		})
	}
}

func TestResponsesClientStreamsRevalidateCompletedToolAtTerminal(t *testing.T) {
	for _, target := range responsesClientStreamTargets() {
		t.Run(target.name, func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			events := []streamFrame{
				{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":""}}`)},
				{Event: "response.function_call_arguments.delta", Data: []byte(`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"q\":1}"}`)},
				{Event: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","item_id":"fc_1","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":1}"}}`)},
			}
			for _, event := range events {
				if _, _, err := stream.Convert(context.Background(), event); err != nil {
					t.Fatalf("Convert(%s): %v", event.Event, err)
				}
			}
			terminal := streamFrame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"provider","status":"completed","output":[{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":2}"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)}
			_, _, err = stream.Convert(context.Background(), terminal)
			if !errors.Is(err, ErrUpstreamResponse) || !strings.Contains(err.Error(), "arguments") {
				t.Fatalf("error = %v, want terminal argument mismatch", err)
			}
		})
	}
}

func TestResponsesClientStreamsRequireStreamedItemsAtTerminal(t *testing.T) {
	for _, target := range responsesClientStreamTargets() {
		t.Run(target.name, func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			events := []streamFrame{
				{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":""}}`)},
				{Event: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","item_id":"fc_1","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}}`)},
			}
			for _, event := range events {
				if _, _, err := stream.Convert(context.Background(), event); err != nil {
					t.Fatalf("Convert(%s): %v", event.Event, err)
				}
			}
			terminal := streamFrame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"provider","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)}
			_, _, err = stream.Convert(context.Background(), terminal)
			if !errors.Is(err, ErrUpstreamResponse) || !strings.Contains(err.Error(), "missing streamed item") {
				t.Fatalf("error = %v, want missing streamed item error", err)
			}
		})
	}
}

func responsesClientStreamTargets() []struct {
	name      string
	converter routeConverter
} {
	return []struct {
		name      string
		converter routeConverter
	}{
		{"chat", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})},
	}
}

func TestResponsesClientStreamsAcceptOfficialTextLifecycle(t *testing.T) {
	events := []streamFrame{
		{Event: "response.queued", Data: []byte(`{"type":"response.queued"}`)},
		{Event: "response.in_progress", Data: []byte(`{"type":"response.in_progress"}`)},
		{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"resp_1","model":"provider","status":"in_progress","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`)},
		{Event: "response.output_item.added", Data: []byte(`{"type":"response.output_item.added","item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`)},
		{Event: "response.content_part.added", Data: []byte(`{"type":"response.content_part.added","item_id":"msg_1","content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`)},
		{Event: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_1","content_index":0,"delta":"hello"}`)},
		{Event: "response.output_text.done", Data: []byte(`{"type":"response.output_text.done","item_id":"msg_1","content_index":0,"text":"hello"}`)},
		{Event: "response.content_part.done", Data: []byte(`{"type":"response.content_part.done","item_id":"msg_1","content_index":0,"part":{"type":"output_text","text":"hello","annotations":[]}}`)},
		{Event: "response.output_item.done", Data: []byte(`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}}`)},
		{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"provider","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)},
	}
	for _, target := range []struct {
		name      string
		converter routeConverter
	}{
		{"chat", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})},
	} {
		t.Run(target.name, func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var joined string
			for _, event := range events {
				frames, _, err := stream.Convert(context.Background(), event)
				if err != nil {
					t.Fatalf("Convert(%s): %v", event.Event, err)
				}
				for _, frame := range frames {
					joined += string(frame.Data)
				}
			}
			if _, _, err := stream.Finalize(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(joined, "hello") {
				t.Fatalf("converted stream omitted text: %s", joined)
			}
		})
	}
}
