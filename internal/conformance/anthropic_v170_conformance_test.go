package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAnthropicV170SafetyIdentifier(t *testing.T) {
	ctx := context.Background()
	messagesToChat := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat})
	result, err := messagesToChat.ToUpstreamRequest(ctx, []byte(`{
		"model":"claude","max_tokens":32,"metadata":{"user_id":"user-1"},
		"messages":[{"role":"user","content":"hi"}]
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(result.Body, &chat); err != nil {
		t.Fatal(err)
	}
	if string(chat.SafetyIdentifier) != `"user-1"` {
		t.Fatalf("Chat request = %s", result.Body)
	}

	chatToMessages := newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})
	result, err = chatToMessages.ToUpstreamRequest(ctx, []byte(`{
		"model":"gpt","safety_identifier":"user-2",
		"messages":[{"role":"user","content":"hi"}]
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var messages messagesRequest
	if err := json.Unmarshal(result.Body, &messages); err != nil {
		t.Fatal(err)
	}
	if messages.Metadata["user_id"] != "user-2" {
		t.Fatalf("Messages request = %s", result.Body)
	}

	for name, body := range map[string]string{
		"openai metadata":    `{"model":"gpt","metadata":{"trace":"x"},"messages":[{"role":"user","content":"hi"}]}`,
		"anthropic metadata": `{"model":"claude","max_tokens":32,"metadata":{"tenant":"x"},"messages":[{"role":"user","content":"hi"}]}`,
		"long user id":       `{"model":"claude","max_tokens":32,"metadata":{"user_id":"` + strings.Repeat("x", 65) + `"},"messages":[{"role":"user","content":"hi"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			converter := chatToMessages
			if strings.HasPrefix(name, "anthropic") || name == "long user id" {
				converter = messagesToChat
			}
			if _, err := converter.ToUpstreamRequest(ctx, []byte(body), conversionOptions{}); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestAnthropicV170RequestExtensionsFailClosed(t *testing.T) {
	ctx := context.Background()
	base := `"model":"claude","max_tokens":32,"messages":[{"role":"user","content":"hi"}]`
	for name, field := range map[string]string{
		"top_k":         `"top_k":4`,
		"cache_control": `"cache_control":{"type":"ephemeral"}`,
		"inference_geo": `"inference_geo":"us"`,
		"service_tier":  `"service_tier":"auto"`,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{` + base + `,` + field + `}`)
			for _, converter := range []routeConverter{
				newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat}),
				newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses}),
			} {
				if _, err := converter.ToUpstreamRequest(ctx, body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
					t.Fatalf("error = %v, want ErrUnsupported", err)
				}
			}
		})
	}

	for _, field := range []string{
		`"eager_input_streaming":true`, `"defer_loading":true`,
		`"allowed_callers":["code_execution_20260120"]`, `"input_examples":[{"q":"x"}]`,
	} {
		body := []byte(`{` + base + `,"tools":[{"name":"lookup","input_schema":{},` + field + `}]}`)
		if _, err := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses}).ToUpstreamRequest(ctx, body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("tool field %s error = %v, want ErrUnsupported", field, err)
		}
	}

	for _, body := range [][]byte{
		[]byte(`{"model":"claude","messages":[{"role":"user","content":"hi"}]}`),
		[]byte(`{"model":"claude","max_tokens":null,"messages":[{"role":"user","content":"hi"}]}`),
	} {
		if err := newProtocolCodec(ProtocolMessages).ValidateRequest(ctx, body, requestHint{}); !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("ValidateRequest(%s) error = %v", body, err)
		}
	}
}

func TestAnthropicV170SystemRoleAndCallerSemantics(t *testing.T) {
	ctx := context.Background()
	converter := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	result, err := converter.ToUpstreamRequest(ctx, []byte(`{
		"model":"claude","max_tokens":32,
		"messages":[
			{"role":"system","content":"policy"},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{},"caller":{"type":"direct"}}]},
			{"role":"user","content":"continue"}
		]
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Body), `"role":"developer"`) || !strings.Contains(string(result.Body), `"call_id":"call_1"`) {
		t.Fatalf("Responses request = %s", result.Body)
	}

	programmatic := []byte(`{
		"model":"claude","max_tokens":32,
		"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{},"caller":{"type":"code_execution_20260120","tool_id":"srv_1"}}]}]
	}`)
	if _, err := converter.ToUpstreamRequest(ctx, programmatic, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("programmatic caller error = %v, want ErrUnsupported", err)
	}
}

func TestAnthropicV170ResponseUsageAndExtensions(t *testing.T) {
	ctx := context.Background()
	messagesBody := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude",
		"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
		"usage":{"input_tokens":3,"output_tokens":8,"output_tokens_details":{"thinking_tokens":5}}
	}`)
	toChat := newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})
	chatResult, err := toChat.ToClientResponse(ctx, messagesBody, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatResponse
	if err := json.Unmarshal(chatResult.Body, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Usage.CompletionDetails.ReasoningTokens != 5 {
		t.Fatalf("Chat usage = %#v", chat.Usage)
	}

	toResponses := newResponsesMessagesRoute(routeSpec{From: ProtocolResponses, To: ProtocolMessages})
	responsesResult, err := toResponses.ToClientResponse(ctx, messagesBody, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var responses responsesResponse
	if err := json.Unmarshal(responsesResult.Body, &responses); err != nil {
		t.Fatal(err)
	}
	if responses.Usage.OutputTokenDetails.ReasoningTokens != 5 {
		t.Fatalf("Responses usage = %#v", responses.Usage)
	}

	extended := []byte(`{
		"id":"msg_1","type":"message","role":"assistant","model":"claude",
		"content":[{"type":"text","text":"no"}],"stop_reason":"refusal",
		"container":{"id":"container_1"},"stop_details":{"type":"refusal","category":"bio"},
		"usage":{"input_tokens":3,"output_tokens":2,"inference_geo":"us","service_tier":"standard","server_tool_use":{"web_search_requests":1}}
	}`)
	if _, err := toResponses.ToClientResponse(ctx, extended, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("default extension error = %v, want ErrUnsupported", err)
	}
	allowed, err := toResponses.ToClientResponse(ctx, extended, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"container_not_representable", "stop_details_not_representable", "inference_geo_not_representable", "service_tier_not_representable", "server_tool_usage_not_representable"} {
		found := false
		for _, diagnostic := range allowed.Diagnostics {
			found = found || diagnostic.Code == code
		}
		if !found {
			t.Fatalf("missing diagnostic %q in %#v", code, allowed.Diagnostics)
		}
	}
}

func TestAnthropicV170ReasoningUsageMapsBackToMessages(t *testing.T) {
	ctx := context.Background()
	chatBody := []byte(`{
		"id":"chat_1","object":"chat.completion","model":"gpt",
		"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":2,"completion_tokens":6,"total_tokens":8,"completion_tokens_details":{"reasoning_tokens":4}}
	}`)
	result, err := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat}).ToClientResponse(ctx, chatBody, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Body), `"output_tokens_details":{"thinking_tokens":4}`) {
		t.Fatalf("Messages response = %s", result.Body)
	}

	responsesBody := []byte(`{
		"id":"resp_1","object":"response","model":"gpt","status":"completed",
		"output":[{"id":"m1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],
		"usage":{"input_tokens":2,"output_tokens":6,"total_tokens":8,"output_tokens_details":{"reasoning_tokens":4}}
	}`)
	result, err = newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses}).ToClientResponse(ctx, responsesBody, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Body), `"output_tokens_details":{"thinking_tokens":4}`) {
		t.Fatalf("Messages response = %s", result.Body)
	}
}

func TestMessagesRoutesRejectOrDiagnoseOpenAIOnlyExtensions(t *testing.T) {
	ctx := context.Background()
	chatToMessages := newChatMessagesRoute(routeSpec{From: ProtocolChat, To: ProtocolMessages})
	for name, body := range map[string]string{
		"prompt cache breakpoint": `{"model":"gpt","messages":[{"role":"user","content":[{"type":"text","text":"hi","prompt_cache_breakpoint":{"type":"default"}}]}]}`,
		"stream obfuscation":      `{"model":"gpt","stream":true,"stream_options":{"include_obfuscation":true},"messages":[{"role":"user","content":"hi"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := chatToMessages.ToUpstreamRequest(ctx, []byte(body), conversionOptions{}); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
		})
	}
	allowed, err := chatToMessages.ToUpstreamRequest(ctx, []byte(`{"model":"gpt","stream":true,"stream_options":{"include_obfuscation":true},"messages":[{"role":"user","content":"hi"}]}`), conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil || !hasBufferedDiagnostic(allowed.Diagnostics, "chat_stream_obfuscation_not_representable") {
		t.Fatalf("allowed=%#v error=%v", allowed, err)
	}
	if _, err := chatToMessages.ToUpstreamRequest(ctx, []byte(`{"model":"gpt","stream":true,"stream_options":{"include_obfuscation":false},"messages":[{"role":"user","content":"hi"}]}`), conversionOptions{}); err != nil {
		t.Fatalf("include_obfuscation=false error = %v", err)
	}

	chatResponseBody := []byte(`{
		"id":"chat_1","object":"chat.completion","model":"gpt","metadata":{"trace":"x"},
		"moderation":{"results":[]},"service_tier":"default","system_fingerprint":"fp_1",
		"choices":[{"index":0,"message":{"role":"assistant","content":"ok","annotations":[{"type":"url_citation","url":"https://example.com"}]},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11,
			"prompt_tokens_details":{"audio_tokens":1,"cached_tokens":2},
			"completion_tokens_details":{"accepted_prediction_tokens":1,"audio_tokens":1,"reasoning_tokens":2,"rejected_prediction_tokens":1}}
	}`)
	toMessages := newChatMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolChat})
	if _, err := toMessages.ToClientResponse(ctx, chatResponseBody, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict Chat response error = %v, want ErrUnsupported", err)
	}
	allowed, err = toMessages.ToClientResponse(ctx, chatResponseBody, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{
		"chat_annotations_not_representable", "chat_metadata_not_representable", "chat_moderation_not_representable",
		"chat_service_tier_not_representable", "chat_system_fingerprint_not_representable",
		"chat_input_audio_usage_not_representable", "chat_output_audio_usage_not_representable",
		"chat_accepted_prediction_usage_not_representable", "chat_rejected_prediction_usage_not_representable",
	} {
		if !hasBufferedDiagnostic(allowed.Diagnostics, code) {
			t.Fatalf("missing diagnostic %q in %#v", code, allowed.Diagnostics)
		}
	}
}

func TestResponsesMessagesExtensionsAndCacheWriteAccounting(t *testing.T) {
	ctx := context.Background()
	body := []byte(`{
		"id":"resp_1","object":"response","model":"gpt","status":"completed",
		"metadata":{"trace":"x"},"moderation":{"results":[]},"service_tier":"default",
		"output":[{"id":"m1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],
		"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11,
			"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":2},"output_tokens_details":{"reasoning_tokens":0}}
	}`)
	toMessages := newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})
	if _, err := toMessages.ToClientResponse(ctx, body, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict Responses response error = %v, want ErrUnsupported", err)
	}
	result, err := toMessages.ToClientResponse(ctx, body, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"responses_metadata_not_representable", "responses_moderation_not_representable", "responses_service_tier_not_representable"} {
		if !hasBufferedDiagnostic(result.Diagnostics, code) {
			t.Fatalf("missing diagnostic %q in %#v", code, result.Diagnostics)
		}
	}
	var converted messagesResponse
	if err := json.Unmarshal(result.Body, &converted); err != nil {
		t.Fatal(err)
	}
	if converted.Usage.InputTokens != 5 || converted.Usage.CacheReadInputTokens != 3 || converted.Usage.CacheCreationInputTokens != 2 {
		t.Fatalf("Messages usage = %#v", converted.Usage)
	}
}

func TestBufferedChatStreamPreservesExtensionsAndDiagnosesObfuscation(t *testing.T) {
	frames := []streamFrame{
		{Data: []byte(`{"id":"chat_1","object":"chat.completion.chunk","model":"gpt","metadata":{"trace":"x"},"moderation":{"results":[]},"service_tier":"default","system_fingerprint":"fp_1","choices":[{"index":0,"delta":{"role":"assistant","content":"ok","annotations":[{"type":"url_citation","url":"https://example.com"}]},"finish_reason":null}]}`)},
		{Data: []byte(`{"id":"chat_1","object":"chat.completion.chunk","model":"gpt","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7,"prompt_tokens_details":{"audio_tokens":1,"cached_tokens":2},"completion_tokens_details":{"accepted_prediction_tokens":1,"audio_tokens":1,"reasoning_tokens":0,"rejected_prediction_tokens":1}}}`)},
	}
	body, diagnostics, err := collectNativeStreamResponse(ProtocolChat, frames, rejectSemanticLoss)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("body=%s diagnostics=%#v error=%v", body, diagnostics, err)
	}
	var response chatResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Metadata["trace"] != "x" || response.SystemFingerprint != "fp_1" || !jsonValuePresent(response.Moderation) || !jsonValuePresent(response.ServiceTier) || !jsonValuePresent(response.Choices[0].Message.Annotations) || response.Usage.PromptDetails.AudioTokens != 1 || response.Usage.CompletionDetails.AcceptedPredictionTokens != 1 {
		t.Fatalf("collected Chat response = %#v", response)
	}

	obfuscated := append([]streamFrame(nil), frames...)
	obfuscated[0].Data = []byte(strings.Replace(string(obfuscated[0].Data), `"choices"`, `"obfuscation":"padding","choices"`, 1))
	if _, _, err := collectNativeStreamResponse(ProtocolChat, obfuscated, rejectSemanticLoss); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict obfuscation error = %v, want ErrUnsupported", err)
	}
	_, diagnostics, err = collectNativeStreamResponse(ProtocolChat, obfuscated, allowDocumentedLoss)
	if err != nil || !hasBufferedDiagnostic(diagnostics, "chat_stream_obfuscation_not_representable") {
		t.Fatalf("obfuscation diagnostics=%#v error=%v", diagnostics, err)
	}
}

func TestResponsesTerminalPromptCacheOptionsLossPolicy(t *testing.T) {
	ctx := context.Background()
	terminalResponse := `{"id":"r","object":"response","model":"responses","status":"completed","prompt_cache_options":{"mode":"explicit","ttl":"30m"},"output":[{"id":"msg","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
	createdEvent := streamFrame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"r","object":"response","model":"responses","status":"in_progress","output":[],"usage":{}}}`)}
	terminalEvent := streamFrame{Event: "response.completed", Data: []byte(`{"type":"response.completed","response":` + terminalResponse + `}`)}

	converters := []struct {
		name      string
		converter routeConverter
	}{
		{"messages", newResponsesMessagesRoute(routeSpec{From: ProtocolMessages, To: ProtocolResponses})},
		{"gemini", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
	}
	for _, test := range converters {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.converter.ToClientResponse(ctx, []byte(terminalResponse), conversionOptions{}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$.prompt_cache_options") {
				t.Fatalf("strict response error = %v", err)
			}
			result, err := test.converter.ToClientResponse(ctx, []byte(terminalResponse), conversionOptions{LossPolicy: allowDocumentedLoss})
			if err != nil || !hasBufferedDiagnostic(result.Diagnostics, "responses_prompt_cache_options_not_representable") {
				t.Fatalf("response diagnostics=%#v error=%v", result.Diagnostics, err)
			}

			strictStream, err := test.converter.NewClientStream(ctx, conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := strictStream.Convert(ctx, createdEvent); err != nil {
				t.Fatal(err)
			}
			if _, _, err := strictStream.Convert(ctx, terminalEvent); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$.response.prompt_cache_options") {
				t.Fatalf("strict stream error = %v", err)
			}

			allowStream, err := test.converter.NewClientStream(ctx, conversionOptions{LossPolicy: allowDocumentedLoss})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := allowStream.Convert(ctx, createdEvent); err != nil {
				t.Fatal(err)
			}
			_, diagnostics, err := allowStream.Convert(ctx, terminalEvent)
			if err != nil || !hasBufferedDiagnostic(diagnostics, "responses_prompt_cache_options_not_representable") {
				t.Fatalf("stream diagnostics=%#v error=%v", diagnostics, err)
			}
		})
	}
}
