package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestReasoningEffortIntersectionResponsesMessages(t *testing.T) {
	ctx := context.Background()
	responsesToMessages := newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages})
	messagesToResponses := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})

	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		t.Run("responses_to_messages/"+effort, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"gpt","input":"hi","max_output_tokens":64,"reasoning":{"effort":%q}}`, effort))
			result, err := responsesToMessages.ToUpstreamRequest(ctx, body, conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var got messagesRequest
			if err := json.Unmarshal(result.Body, &got); err != nil {
				t.Fatal(err)
			}
			if got.OutputConfig == nil || got.OutputConfig.Effort != effort {
				t.Fatalf("output_config = %#v, want effort %q", got.OutputConfig, effort)
			}
		})

		t.Run("messages_to_responses/"+effort, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"output_config":{"effort":%q}}`, effort))
			result, err := messagesToResponses.ToUpstreamRequest(ctx, body, conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var got responsesRequest
			if err := json.Unmarshal(result.Body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Reasoning == nil || got.Reasoning.Effort != effort {
				t.Fatalf("reasoning = %#v, want effort %q", got.Reasoning, effort)
			}
		})
	}

	for _, test := range []struct {
		name string
		body string
		want error
		path string
	}{
		{"responses none", `{"model":"gpt","input":"hi","reasoning":{"effort":"none"}}`, ErrUnsupported, "$.reasoning.effort"},
		{"responses minimal", `{"model":"gpt","input":"hi","reasoning":{"effort":"minimal"}}`, ErrUnsupported, "$.reasoning.effort"},
		{"responses unknown", `{"model":"gpt","input":"hi","reasoning":{"effort":"turbo"}}`, ErrInvalidPayload, "$.reasoning.effort"},
		{"responses summary", `{"model":"gpt","input":"hi","reasoning":{"effort":"low","summary":"auto"}}`, ErrUnsupported, "$.reasoning.summary"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := responsesToMessages.ToUpstreamRequest(ctx, []byte(test.body), conversionOptions{})
			assertReasoningBoundaryError(t, err, test.want, test.path)
		})
	}

	for _, test := range []struct {
		name string
		body string
		want error
		path string
	}{
		{"messages minimal", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"output_config":{"effort":"minimal"}}`, ErrInvalidPayload, "$.output_config.effort"},
		{"messages unknown", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"output_config":{"effort":"turbo"}}`, ErrInvalidPayload, "$.output_config.effort"},
		{"messages adaptive thinking", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"}}`, ErrUnsupported, "$.thinking"},
		{"messages budgeted thinking", `{"model":"claude","max_tokens":2048,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":1024}}`, ErrUnsupported, "$.thinking"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := messagesToResponses.ToUpstreamRequest(ctx, []byte(test.body), conversionOptions{})
			assertReasoningBoundaryError(t, err, test.want, test.path)
		})
	}
}

func TestReasoningEffortIntersectionResponsesGemini(t *testing.T) {
	ctx := context.Background()
	responsesToGemini := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	geminiToResponses := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})

	for _, effort := range []string{"minimal", "low", "medium", "high"} {
		level := strings.ToUpper(effort)
		t.Run("responses_to_gemini/"+effort, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"gpt","input":"hi","reasoning":{"effort":%q}}`, effort))
			result, err := responsesToGemini.ToUpstreamRequest(ctx, body, conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var got geminiRequest
			if err := json.Unmarshal(result.Body, &got); err != nil {
				t.Fatal(err)
			}
			if got.GenerationConfig == nil || got.GenerationConfig.ThinkingConfig == nil || got.GenerationConfig.ThinkingConfig.ThinkingLevel != level {
				t.Fatalf("generationConfig = %#v, want thinkingLevel %q", got.GenerationConfig, level)
			}
		})

		t.Run("gemini_to_responses/"+level, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":%q}}}`, level))
			result, err := geminiToResponses.ToUpstreamRequest(ctx, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gpt"}})
			if err != nil {
				t.Fatal(err)
			}
			var got responsesRequest
			if err := json.Unmarshal(result.Body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Reasoning == nil || got.Reasoning.Effort != effort {
				t.Fatalf("reasoning = %#v, want effort %q", got.Reasoning, effort)
			}
		})
	}

	for _, effort := range []string{"none", "xhigh", "max"} {
		t.Run("unsupported responses effort/"+effort, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"gpt","input":"hi","reasoning":{"effort":%q}}`, effort))
			_, err := responsesToGemini.ToUpstreamRequest(ctx, body, conversionOptions{})
			assertReasoningBoundaryError(t, err, ErrUnsupported, "$.reasoning.effort")
		})
	}
	_, err := responsesToGemini.ToUpstreamRequest(ctx, []byte(`{"model":"gpt","input":"hi","reasoning":{"effort":"low","summary":"auto"}}`), conversionOptions{})
	assertReasoningBoundaryError(t, err, ErrUnsupported, "$.reasoning.summary")

	for _, test := range []struct {
		name   string
		config string
		path   string
	}{
		{"thinking budget", `"thinkingBudget":1024`, "$.generationConfig.thinkingConfig.thinkingBudget"},
		{"include thoughts", `"includeThoughts":true`, "$.generationConfig.thinkingConfig.includeThoughts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{` + test.config + `}}}`)
			_, err := geminiToResponses.ToUpstreamRequest(ctx, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gpt"}})
			assertReasoningBoundaryError(t, err, ErrUnsupported, test.path)
		})
	}
	for _, level := range []string{"ULTRA", "low", " LOW "} {
		body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":%q}}}`, level))
		_, err := geminiToResponses.ToUpstreamRequest(ctx, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gpt"}})
		assertReasoningBoundaryError(t, err, ErrInvalidPayload, "$.generationConfig.thinkingConfig.thinkingLevel")
	}
	unspecified, err := geminiToResponses.ToUpstreamRequest(ctx, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"THINKING_LEVEL_UNSPECIFIED"}}}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gpt"}})
	if err != nil {
		t.Fatal(err)
	}
	var unspecifiedResponses responsesRequest
	if err := json.Unmarshal(unspecified.Body, &unspecifiedResponses); err != nil {
		t.Fatal(err)
	}
	if unspecifiedResponses.Reasoning != nil {
		t.Fatalf("unspecified Gemini level invented Responses reasoning: %#v", unspecifiedResponses.Reasoning)
	}
}

func TestReasoningEffortIntersectionMessagesGemini(t *testing.T) {
	ctx := context.Background()
	messagesToGemini := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	geminiToMessages := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})

	for _, effort := range []string{"low", "medium", "high"} {
		level := strings.ToUpper(effort)
		t.Run("messages_to_gemini/"+effort, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"output_config":{"effort":%q}}`, effort))
			result, err := messagesToGemini.ToUpstreamRequest(ctx, body, conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var got geminiRequest
			if err := json.Unmarshal(result.Body, &got); err != nil {
				t.Fatal(err)
			}
			if got.GenerationConfig == nil || got.GenerationConfig.ThinkingConfig == nil || got.GenerationConfig.ThinkingConfig.ThinkingLevel != level {
				t.Fatalf("generationConfig = %#v, want thinkingLevel %q", got.GenerationConfig, level)
			}
		})

		t.Run("gemini_to_messages/"+level, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":64,"thinkingConfig":{"thinkingLevel":%q}}}`, level))
			result, err := geminiToMessages.ToUpstreamRequest(ctx, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
			if err != nil {
				t.Fatal(err)
			}
			var got messagesRequest
			if err := json.Unmarshal(result.Body, &got); err != nil {
				t.Fatal(err)
			}
			if got.OutputConfig == nil || got.OutputConfig.Effort != effort {
				t.Fatalf("output_config = %#v, want effort %q", got.OutputConfig, effort)
			}
		})
	}

	for _, test := range []struct {
		effort string
		want   error
	}{
		{"xhigh", ErrUnsupported},
		{"max", ErrUnsupported},
		{"minimal", ErrInvalidPayload},
		{"turbo", ErrInvalidPayload},
	} {
		t.Run("messages boundary/"+test.effort, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"output_config":{"effort":%q}}`, test.effort))
			_, err := messagesToGemini.ToUpstreamRequest(ctx, body, conversionOptions{})
			assertReasoningBoundaryError(t, err, test.want, "$.output_config.effort")
		})
	}

	_, err := messagesToGemini.ToUpstreamRequest(ctx, []byte(`{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"adaptive"}}`), conversionOptions{})
	assertReasoningBoundaryError(t, err, ErrUnsupported, "$.thinking")

	for _, test := range []struct {
		name   string
		config string
		path   string
	}{
		{"minimal level", `"thinkingLevel":"MINIMAL"`, "$.generationConfig.thinkingConfig.thinkingLevel"},
		{"thinking budget", `"thinkingBudget":1024`, "$.generationConfig.thinkingConfig.thinkingBudget"},
		{"include thoughts", `"includeThoughts":true`, "$.generationConfig.thinkingConfig.includeThoughts"},
	} {
		t.Run("gemini boundary/"+test.name, func(t *testing.T) {
			body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":64,"thinkingConfig":{` + test.config + `}}}`)
			_, err := geminiToMessages.ToUpstreamRequest(ctx, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
			assertReasoningBoundaryError(t, err, ErrUnsupported, test.path)
		})
	}
	for _, level := range []string{"ULTRA", "low", " LOW "} {
		body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":64,"thinkingConfig":{"thinkingLevel":%q}}}`, level))
		_, err := geminiToMessages.ToUpstreamRequest(ctx, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
		assertReasoningBoundaryError(t, err, ErrInvalidPayload, "$.generationConfig.thinkingConfig.thinkingLevel")
	}
	unspecified, err := geminiToMessages.ToUpstreamRequest(ctx, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":64,"thinkingConfig":{"thinkingLevel":"THINKING_LEVEL_UNSPECIFIED"}}}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	var unspecifiedMessages messagesRequest
	if err := json.Unmarshal(unspecified.Body, &unspecifiedMessages); err != nil {
		t.Fatal(err)
	}
	if unspecifiedMessages.OutputConfig != nil && unspecifiedMessages.OutputConfig.Effort != "" {
		t.Fatalf("unspecified Gemini level invented Messages effort: %#v", unspecifiedMessages.OutputConfig)
	}
}

func TestReasoningEffortIntersectionChatGemini(t *testing.T) {
	converter := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	for _, level := range []string{"MINIMAL", "LOW", "MEDIUM", "HIGH"} {
		t.Run(level, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":%q}}}`, level))
			result, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
			if err != nil {
				t.Fatal(err)
			}
			var got chatRequest
			if err := json.Unmarshal(result.Body, &got); err != nil {
				t.Fatal(err)
			}
			if got.ReasoningEffort != strings.ToLower(level) {
				t.Fatalf("reasoning_effort = %q", got.ReasoningEffort)
			}
		})
	}
	for _, level := range []string{"ULTRA", "low", " LOW "} {
		body := []byte(fmt.Sprintf(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":%q}}}`, level))
		_, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
		assertReasoningBoundaryError(t, err, ErrInvalidPayload, "$.generationConfig.thinkingConfig.thinkingLevel")
	}
	result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"THINKING_LEVEL_UNSPECIFIED"}}}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	var got chatRequest
	if err := json.Unmarshal(result.Body, &got); err != nil || got.ReasoningEffort != "" {
		t.Fatalf("unspecified level invented reasoning_effort: %s", result.Body)
	}
}

func assertReasoningBoundaryError(t *testing.T, err, want error, path string) {
	t.Helper()
	if !errors.Is(err, want) || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want %v at %s", err, want, path)
	}
}
