package providersdks_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	routemorph "github.com/2218342221/RouteMorphSDK"
	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go/v3"
	openaioption "github.com/openai/openai-go/v3/option"
	"google.golang.org/genai"
)

const responsesReply = `{"id":"resp_1","object":"response","created_at":1,"model":"upstream-model","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`

const responsesStream = `event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_1","object":"response","created_at":1,"model":"upstream-model","status":"in_progress","output":[],"usage":null}}

event: response.output_item.added
data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"in_progress","content":[]}}

event: response.content_part.added
data: {"type":"response.content_part.added","sequence_number":2,"item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[],"logprobs":[]}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":3,"item_id":"msg_1","output_index":0,"content_index":0,"delta":"hello","logprobs":[]}

event: response.output_text.done
data: {"type":"response.output_text.done","sequence_number":4,"item_id":"msg_1","output_index":0,"content_index":0,"text":"hello","logprobs":[]}

event: response.content_part.done
data: {"type":"response.content_part.done","sequence_number":5,"item_id":"msg_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}}

event: response.output_item.done
data: {"type":"response.output_item.done","sequence_number":6,"output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}}

event: response.completed
data: {"type":"response.completed","sequence_number":7,"response":{"id":"resp_1","object":"response","created_at":1,"model":"upstream-model","status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[],"logprobs":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}

`

func TestOfficialSDKInjection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
			t.Errorf("upstream request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer upstream-key" || request.Header.Get("X-API-Key") != "" || request.Header.Get("X-Goog-API-Key") != "" {
			t.Errorf("upstream credentials = %#v", request.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if body["model"] != "upstream-model" {
			t.Errorf("upstream model = %#v", body["model"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, responsesReply)
	}))
	defer upstream.Close()

	adapter, err := routemorph.NewOpenAIResponsesAdapter(
		upstream.URL,
		"upstream-key",
		routemorph.WithModel("upstream-model"),
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("openai", func(t *testing.T) {
		client := openai.NewClient(
			openaioption.WithBaseURL("https://routemorph.invalid/v1"),
			openaioption.WithAPIKey("intercepted-by-routemorph"),
			openaioption.WithHTTPClient(adapter.HTTPClient()),
			openaioption.WithMaxRetries(0),
		)
		completion, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
			Model: "client-model",
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say hello."),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(completion.Choices) != 1 || completion.Choices[0].Message.Content != "hello" || completion.Model != "client-model" {
			t.Fatalf("completion = %#v", completion)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		client := anthropic.NewClient(
			anthropicoption.WithoutEnvironmentDefaults(),
			anthropicoption.WithBaseURL("https://routemorph.invalid"),
			anthropicoption.WithHTTPClient(adapter.HTTPClient()),
			anthropicoption.WithMaxRetries(0),
		)
		message, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
			Model:     "client-model",
			MaxTokens: 128,
			Messages: []anthropic.MessageParam{
				anthropic.NewUserMessage(anthropic.NewTextBlock("Say hello.")),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(message.Content) != 1 || message.Content[0].Text != "hello" || message.Model != "client-model" {
			t.Fatalf("message = %#v", message)
		}
	})

	t.Run("gemini", func(t *testing.T) {
		client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
			Backend:     genai.BackendGeminiAPI,
			APIKey:      "intercepted-by-routemorph",
			HTTPClient:  adapter.HTTPClient(),
			HTTPOptions: genai.HTTPOptions{BaseURL: "https://routemorph.invalid", APIVersion: "v1beta"},
		})
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Models.GenerateContent(context.Background(), "client-model", genai.Text("Say hello."), nil)
		if err != nil {
			t.Fatal(err)
		}
		if response.Text() != "hello" || response.ModelVersion != "client-model" {
			t.Fatalf("response = %#v", response)
		}
	})
}

func TestOpenAISDKStreamingInjection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if request.URL.Path != "/v1/responses" || body["model"] != "upstream-model" || body["stream"] != true {
			t.Errorf("upstream request path=%q body=%#v", request.URL.Path, body)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, responsesStream)
	}))
	defer upstream.Close()

	adapter, err := routemorph.NewOpenAIResponsesAdapter(upstream.URL, "", routemorph.WithModel("upstream-model"))
	if err != nil {
		t.Fatal(err)
	}
	client := openai.NewClient(
		openaioption.WithBaseURL("https://routemorph.invalid/v1"),
		openaioption.WithAPIKey("intercepted-by-routemorph"),
		openaioption.WithHTTPClient(adapter.HTTPClient()),
		openaioption.WithMaxRetries(0),
	)
	stream := client.Chat.Completions.NewStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model: "client-model",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Say hello."),
		},
	})
	defer stream.Close()

	var text strings.Builder
	for stream.Next() {
		for _, choice := range stream.Current().Choices {
			text.WriteString(choice.Delta.Content)
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if text.String() != "hello" {
		t.Fatalf("streamed text = %q", text.String())
	}
}

func TestGeminiSDKStreamingFailureIsNotAnEmptySuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"sequence_number\":0,\"output_index\":0,\"item_id\":\"msg_1\",\"content_index\":0,\"delta\":\"late\",\"logprobs\":[]}\n\n")
	}))
	defer upstream.Close()
	adapter, err := routemorph.NewOpenAIResponsesAdapter(upstream.URL, "", routemorph.WithModel("upstream-model"))
	if err != nil {
		t.Fatal(err)
	}
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		Backend:     genai.BackendGeminiAPI,
		APIKey:      "intercepted-by-routemorph",
		HTTPClient:  adapter.HTTPClient(),
		HTTPOptions: genai.HTTPOptions{BaseURL: "https://routemorph.invalid", APIVersion: "v1beta"},
	})
	if err != nil {
		t.Fatal(err)
	}

	successes := 0
	var streamErr error
	for response, err := range client.Models.GenerateContentStream(context.Background(), "client-model", genai.Text("Say hello."), nil) {
		if err != nil {
			streamErr = err
			break
		}
		if response != nil {
			successes++
		}
	}
	if successes != 0 || !errors.Is(streamErr, routemorph.ErrUpstreamResponse) {
		t.Fatalf("successful responses=%d error=%v, want no response and ErrUpstreamResponse", successes, streamErr)
	}
}
