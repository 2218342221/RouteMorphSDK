package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const responsesIdentityCreated = `{"type":"response.created","response":{"id":"resp_1","model":"provider-a","status":"in_progress","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`

func TestResponsesSourceStreamsRejectResponseIdentityChanges(t *testing.T) {
	targets := []struct {
		name      string
		converter routeConverter
	}{
		{"chat", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses})},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})},
	}
	changes := []struct {
		name     string
		path     string
		terminal string
	}{
		{"id", "$.response.id", `{"type":"response.completed","response":{"id":"resp_2","model":"provider-a","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`},
		{"model", "$.response.model", `{"type":"response.completed","response":{"id":"resp_1","model":"provider-b","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`},
	}

	for _, target := range targets {
		for _, change := range changes {
			t.Run(target.name+"/"+change.name, func(t *testing.T) {
				stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := stream.Convert(context.Background(), streamFrame{Event: "response.created", Data: []byte(responsesIdentityCreated)}); err != nil {
					t.Fatal(err)
				}
				_, _, err = stream.Convert(context.Background(), streamFrame{Event: "response.completed", Data: []byte(change.terminal)})
				if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), change.path) {
					t.Fatalf("error = %v, want identity error at %s", err, change.path)
				}
			})
		}
	}
}

func TestResponsesSourceStreamsPreserveClientModelOverride(t *testing.T) {
	targets := []struct {
		name           string
		converter      routeConverter
		modelFragment  string
		providerRender string
	}{
		{"chat", newChatResponsesRoute(routeSpec{From: ProtocolChat, To: ProtocolResponses}), `"model":"client-model"`, `"model":"provider-a"`},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses}), `"modelVersion":"client-model"`, `"modelVersion":"provider-a"`},
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses}), `"model":"client-model"`, `"model":"provider-a"`},
	}
	terminal := `{"type":"response.completed","response":{"id":"resp_1","model":"provider-a","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1}}}`

	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			stream, err := target.converter.NewClientStream(context.Background(), conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
			if err != nil {
				t.Fatal(err)
			}
			var output strings.Builder
			for _, event := range []streamFrame{
				{Event: "response.created", Data: []byte(responsesIdentityCreated)},
				{Event: "response.completed", Data: []byte(terminal)},
			} {
				frames, _, err := stream.Convert(context.Background(), event)
				if err != nil {
					t.Fatal(err)
				}
				for _, frame := range frames {
					output.Write(frame.Data)
				}
			}
			if _, _, err := stream.Finalize(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), target.modelFragment) || strings.Contains(output.String(), target.providerRender) {
				t.Fatalf("client model override was not stable: %s", output.String())
			}
		})
	}
}

func TestGeminiSourceStreamRejectsResponseIdentityChanges(t *testing.T) {
	changes := []struct {
		name     string
		path     string
		terminal string
	}{
		{"id", "$.responseId", `{"responseId":"gem_2","modelVersion":"provider-a","candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`},
		{"model", "$.modelVersion", `{"responseId":"gem_1","modelVersion":"provider-b","candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`},
	}
	first := streamFrame{Data: []byte(`{"responseId":"gem_1","modelVersion":"provider-a","candidates":[{"content":{"role":"model","parts":[{"text":"hel"}]}}]}`)}

	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			stream, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
			if err != nil {
				t.Fatal(err)
			}
			frames, _, err := stream.Convert(context.Background(), first)
			if err != nil {
				t.Fatal(err)
			}
			if joinedFrameData(frames) == "" || !strings.Contains(joinedFrameData(frames), `"model":"client-model"`) {
				t.Fatalf("response.created did not use client model override: %s", joinedFrameData(frames))
			}
			_, _, err = stream.Convert(context.Background(), streamFrame{Data: []byte(change.terminal)})
			if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), change.path) {
				t.Fatalf("error = %v, want identity error at %s", err, change.path)
			}
		})
	}
}

func TestGeminiSourceStreamPreservesClientModelOverride(t *testing.T) {
	stream, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
	if err != nil {
		t.Fatal(err)
	}
	events := []streamFrame{
		{Data: []byte(`{"responseId":"gem_1","modelVersion":"provider-a","candidates":[{"content":{"role":"model","parts":[{"text":"hel"}]}}]}`)},
		{Data: []byte(`{"responseId":"gem_1","modelVersion":"provider-a","candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`)},
	}
	var output strings.Builder
	for _, event := range events {
		frames, _, err := stream.Convert(context.Background(), event)
		if err != nil {
			t.Fatal(err)
		}
		for _, frame := range frames {
			output.Write(frame.Data)
		}
	}
	if _, _, err := stream.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"model":"client-model"`) || strings.Contains(output.String(), `"model":"provider-a"`) {
		t.Fatalf("client model override was not stable: %s", output.String())
	}
}

func TestGeminiSourceStreamPreservesMixedPartOrder(t *testing.T) {
	stream, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []streamFrame{
		{Data: []byte(`{"responseId":"gem_1","modelVersion":"provider","candidates":[{"content":{"role":"model","parts":[{"text":"before"}]}}]}`)},
		{Data: []byte(`{"responseId":"gem_1","modelVersion":"provider","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{"q":1}}}]}}]}`)},
		{Data: []byte(`{"responseId":"gem_1","modelVersion":"provider","candidates":[{"content":{"role":"model","parts":[{"text":"after"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}`)},
	}
	var addedTypes []string
	var terminal responsesResponse
	for _, input := range inputs {
		frames, _, err := stream.Convert(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		for _, frame := range frames {
			var event struct {
				Type     string            `json:"type"`
				Item     responsesItem     `json:"item"`
				Response responsesResponse `json:"response"`
			}
			if err := json.Unmarshal(frame.Data, &event); err != nil {
				t.Fatalf("decode output event: %v", err)
			}
			switch event.Type {
			case "response.output_item.added":
				addedTypes = append(addedTypes, event.Item.Type)
			case "response.completed":
				terminal = event.Response
			}
		}
	}
	if _, _, err := stream.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(addedTypes, ","), "message,function_call,message"; got != want {
		t.Fatalf("output item order = %q, want %q", got, want)
	}
	if len(terminal.Output) != 3 {
		t.Fatalf("terminal output count = %d, want 3", len(terminal.Output))
	}
	if terminal.Output[0].Type != "message" || terminal.Output[1].Type != "function_call" || terminal.Output[2].Type != "message" {
		t.Fatalf("terminal output order = %#v", terminal.Output)
	}
	for index, want := range []string{"before", "after"} {
		outputIndex := index * 2
		var parts []responsesContentPart
		if err := json.Unmarshal(terminal.Output[outputIndex].Content, &parts); err != nil {
			t.Fatalf("decode message %d content: %v", outputIndex, err)
		}
		if len(parts) != 1 || parts[0].Text != want {
			t.Fatalf("message %d content = %#v, want %q", outputIndex, parts, want)
		}
	}
}

func joinedFrameData(frames []streamFrame) string {
	var output strings.Builder
	for _, frame := range frames {
		output.Write(frame.Data)
	}
	return output.String()
}
