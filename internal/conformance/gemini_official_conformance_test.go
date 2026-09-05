package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const advancedGeminiFunctionSchema = `{"type":"object","$defs":{"coordinate":{"type":"number","enum":[1,2.5]}},"properties":{"latitude":{"$ref":"#/$defs/coordinate"}},"additionalProperties":false}`

func TestOfficialGeminiParametersJSONSchemaFromEveryProtocol(t *testing.T) {
	tests := []struct {
		name      string
		converter routeConverter
		body      string
	}{
		{
			name:      "chat",
			converter: newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent}),
			body:      `{"model":"chat","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"locate","parameters":` + advancedGeminiFunctionSchema + `}}]}`,
		},
		{
			name:      "messages",
			converter: newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent}),
			body:      `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"locate","input_schema":` + advancedGeminiFunctionSchema + `}]}`,
		},
		{
			name:      "responses",
			converter: newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}),
			body:      `{"model":"responses","input":"hi","tools":[{"type":"function","name":"locate","parameters":` + advancedGeminiFunctionSchema + `}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.converter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			var request geminiRequest
			if err := json.Unmarshal(result.Body, &request); err != nil {
				t.Fatal(err)
			}
			declaration := request.Tools[0].FunctionDeclarations[0]
			if jsonValuePresent(declaration.Parameters) {
				t.Fatalf("legacy parameters unexpectedly emitted: %s", declaration.Parameters)
			}
			if !jsonObjectsEqual(declaration.ParametersJSONSchema, []byte(advancedGeminiFunctionSchema)) {
				t.Fatalf("parametersJsonSchema = %s", declaration.ParametersJSONSchema)
			}
		})
	}
}

func TestOfficialGeminiParametersJSONSchemaToEveryProtocol(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"locate","parametersJsonSchema":` + advancedGeminiFunctionSchema + `}]}]}`)
	tests := []struct {
		name      string
		converter routeConverter
		extract   func([]byte) json.RawMessage
	}{
		{
			name:      "chat",
			converter: newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat}),
			extract: func(body []byte) json.RawMessage {
				var request chatRequest
				_ = json.Unmarshal(body, &request)
				return request.Tools[0].Function.Parameters
			},
		},
		{
			name:      "messages",
			converter: newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages}),
			extract: func(body []byte) json.RawMessage {
				var request messagesRequest
				_ = json.Unmarshal(body, &request)
				return request.Tools[0].InputSchema
			},
		},
		{
			name:      "responses",
			converter: newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses}),
			extract: func(body []byte) json.RawMessage {
				var request responsesRequest
				_ = json.Unmarshal(body, &request)
				return request.Tools[0].Parameters
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "upstream"}})
			if err != nil {
				t.Fatal(err)
			}
			if got := test.extract(result.Body); !jsonObjectsEqual(got, []byte(advancedGeminiFunctionSchema)) {
				t.Fatalf("converted schema = %s", got)
			}
		})
	}
}

func TestExternalFunctionCallsNeverForgeGeminiThoughtSignatures(t *testing.T) {
	tests := []struct {
		name      string
		converter routeConverter
		body      string
	}{
		{"chat", newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent}), `{"model":"chat","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}`},
		{"messages", newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent}), `{"model":"claude","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"lookup","input":{}}]}]}`},
		{"responses", newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}), `{"model":"responses","input":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.converter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(result.Body), `"thoughtSignature"`) {
				t.Fatalf("forged thoughtSignature: %s", result.Body)
			}
			if !directHasDiagnostic(result.Diagnostics, "gemini_thought_signature_unavailable") {
				t.Fatalf("diagnostics = %#v", result.Diagnostics)
			}
		})
	}
}

func TestProviderGeminiThoughtSignaturesNeverDropUnderLossPolicy(t *testing.T) {
	requestBody := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{}},"thoughtSignature":"provider-encrypted-state"}]}]}`)
	responseBody := []byte(`{"responseId":"r","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"lookup","args":{}},"thoughtSignature":"provider-encrypted-state"}]},"finishReason":"STOP"}],"usageMetadata":{}}`)
	for _, test := range []struct {
		name              string
		requestConverter  routeConverter
		responseConverter routeConverter
	}{
		{"chat", newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat}), newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})},
		{"messages", newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages}), newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})},
		{"responses", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses}), newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.requestConverter.ToUpstreamRequest(context.Background(), requestBody, conversionOptions{LossPolicy: allowDocumentedLoss, Exchange: exchangeMetadata{UpstreamModel: "upstream"}})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "thoughtSignature") {
				t.Fatalf("request error = %v, want thoughtSignature ErrUnsupported", err)
			}
			_, err = test.responseConverter.ToClientResponse(context.Background(), responseBody, conversionOptions{LossPolicy: allowDocumentedLoss})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "thoughtSignature") {
				t.Fatalf("response error = %v, want thoughtSignature ErrUnsupported", err)
			}
		})
	}
}

func TestLatestOfficialGeminiFieldsFailClosedWithPrecisePaths(t *testing.T) {
	tests := []struct {
		name               string
		body               string
		path               string
		responsesSupported bool
	}{
		{"service tier", `{"serviceTier":"flex","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, "$.serviceTier", false},
		{"agent tool call", `{"contents":[{"role":"user","parts":[{"toolCall":{"id":"1","toolName":"browser","toolType":"COMPUTER_USE","args":{}}}]}]}`, "$.contents[0].parts[0].toolCall", false},
		{"function output schema", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"lookup","responseJsonSchema":{"type":"object"}}]}]}`, "$.tools[0].functionDeclarations[0].responseJsonSchema", true},
		{"response format", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"responseFormat":{"type":"json"}}}`, "$.generationConfig.responseFormat", false},
	}
	converters := []struct {
		name      string
		converter routeConverter
	}{
		{"chat", newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})},
		{"messages", newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})},
		{"responses", newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})},
	}
	for _, protocol := range converters {
		for _, test := range tests {
			t.Run(protocol.name+"/"+test.name, func(t *testing.T) {
				_, err := protocol.converter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "upstream"}})
				if test.responsesSupported && protocol.name == "responses" {
					if err != nil {
						t.Fatalf("error = %v, want supported conversion", err)
					}
					return
				}
				if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
					t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.path)
				}
			})
		}
	}
}

func TestMessagesGeminiTopKIsBidirectional(t *testing.T) {
	toGemini := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	result, err := toGemini.ToUpstreamRequest(context.Background(), []byte(`{"model":"claude","max_tokens":64,"top_k":37,"messages":[{"role":"user","content":"hi"}]}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiRequest
	_ = json.Unmarshal(result.Body, &gemini)
	if gemini.GenerationConfig == nil || gemini.GenerationConfig.TopK == nil || *gemini.GenerationConfig.TopK != 37 {
		t.Fatalf("generationConfig = %#v", gemini.GenerationConfig)
	}

	toMessages := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	result, err = toMessages.ToUpstreamRequest(context.Background(), []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"topK":37}}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	var messages messagesRequest
	_ = json.Unmarshal(result.Body, &messages)
	if messages.TopK == nil || *messages.TopK != 37 {
		t.Fatalf("top_k = %#v", messages.TopK)
	}
}

func TestGeminiLatestFinishReasons(t *testing.T) {
	converter := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
	for _, reason := range []string{"ESCALATION", "PUP_LIMITED_DISABLED"} {
		body := []byte(`{"responseId":"r","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"text":"blocked"}]},"finishReason":"` + reason + `"}],"usageMetadata":{}}`)
		result, err := converter.ToClientResponse(context.Background(), body, conversionOptions{})
		if err != nil {
			t.Fatalf("%s: %v", reason, err)
		}
		var response chatResponse
		_ = json.Unmarshal(result.Body, &response)
		if response.Choices[0].FinishReason != "content_filter" {
			t.Fatalf("%s mapped to %q", reason, response.Choices[0].FinishReason)
		}
	}
	body := []byte(`{"responseId":"r","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"text":"bad"}]},"finishReason":"MALFORMED_RESPONSE"}],"usageMetadata":{}}`)
	if _, err := converter.ToClientResponse(context.Background(), body, conversionOptions{}); !errors.Is(err, ErrUpstreamResponse) {
		t.Fatalf("error = %v, want ErrUpstreamResponse", err)
	}
}

func TestGeminiFileDisplayNameFailsClosed(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	body := []byte(`{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"application/pdf","data":"aGk=","displayName":"brief.pdf"}}]}]}`)
	_, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$.contents[0].parts[0].inlineData.displayName") {
		t.Fatalf("error = %v, want ErrUnsupported at inlineData.displayName", err)
	}
}

func TestGeminiResponseCandidateRoleMustBeModel(t *testing.T) {
	body := []byte(`{"responseId":"r","modelVersion":"gemini","candidates":[{"content":{"role":"user","parts":[{"text":"wrong role"}]},"finishReason":"STOP"}],"usageMetadata":{}}`)
	for _, test := range []struct {
		name      string
		converter routeConverter
	}{
		{"chat", newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})},
		{"responses", newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})},
	} {
		t.Run(test.name+"/nonstream", func(t *testing.T) {
			_, err := test.converter.ToClientResponse(context.Background(), body, conversionOptions{})
			if !errors.Is(err, ErrUpstreamResponse) || !strings.Contains(err.Error(), "$.candidates[0].content.role") {
				t.Fatalf("error = %v, want ErrUpstreamResponse at candidate content role", err)
			}
		})

		t.Run(test.name+"/stream", func(t *testing.T) {
			stream, err := test.converter.NewClientStream(context.Background(), conversionOptions{})
			if err != nil {
				t.Fatal(err)
			}
			_, _, conversionErr := stream.Convert(context.Background(), streamFrame{Data: body})
			if conversionErr == nil {
				_, _, conversionErr = stream.Finalize(context.Background())
			}
			if !errors.Is(conversionErr, ErrUpstreamResponse) || !strings.Contains(conversionErr.Error(), "$.candidates[0].content.role") {
				t.Fatalf("error = %v, want ErrUpstreamResponse at candidate content role", conversionErr)
			}
		})
	}
}

func TestChatExtensionsDoNotSilentlyDisappearOnGeminiRoutes(t *testing.T) {
	requestConverter := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
	for _, test := range []struct {
		name string
		body string
		path string
	}{
		{"message annotations", `{"model":"chat","messages":[{"role":"user","content":"hi","annotations":[{"type":"url_citation"}]}]}`, "$.messages[0].annotations"},
		{"content cache breakpoint", `{"model":"chat","messages":[{"role":"user","content":[{"type":"text","text":"hi","prompt_cache_breakpoint":{"type":"ephemeral"}}]}]}`, "$.messages[0].content[0].prompt_cache_breakpoint"},
		{"prompt cache options", `{"model":"chat","prompt_cache_options":{"mode":"explicit","ttl":"30m"},"messages":[{"role":"user","content":"hi"}]}`, "$.prompt_cache_options"},
		{"moderation", `{"model":"chat","moderation":{"type":"auto"},"messages":[{"role":"user","content":"hi"}]}`, "$.moderation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := requestConverter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.path)
			}
		})
	}

	responseConverter := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	for _, test := range []struct {
		name string
		body string
		path string
		code string
	}{
		{"metadata", `{"id":"c","object":"chat.completion","model":"chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{},"metadata":{"k":"v"}}`, "$.metadata", "chat_response_metadata_not_representable"},
		{"moderation", `{"id":"c","object":"chat.completion","model":"chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{},"moderation":{"flagged":false}}`, "$.moderation", "chat_response_moderation_not_representable"},
		{"service tier", `{"id":"c","object":"chat.completion","model":"chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{},"service_tier":"default"}`, "$.service_tier", "chat_response_service_tier_not_representable"},
		{"fingerprint", `{"id":"c","object":"chat.completion","model":"chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{},"system_fingerprint":"fp"}`, "$.system_fingerprint", "chat_system_fingerprint_not_representable"},
		{"annotations", `{"id":"c","object":"chat.completion","model":"chat","choices":[{"index":0,"message":{"role":"assistant","content":"ok","annotations":[{"type":"url_citation"}]},"finish_reason":"stop"}],"usage":{}}`, "$.choices[0].message.annotations", "chat_annotations_not_representable"},
	} {
		t.Run("response/"+test.name, func(t *testing.T) {
			_, err := responseConverter.ToClientResponse(context.Background(), []byte(test.body), conversionOptions{})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("strict error = %v, want ErrUnsupported at %s", err, test.path)
			}
			result, err := responseConverter.ToClientResponse(context.Background(), []byte(test.body), conversionOptions{LossPolicy: allowDocumentedLoss})
			if err != nil || !directHasDiagnostic(result.Diagnostics, test.code) {
				t.Fatalf("allow result=%s diagnostics=%#v err=%v", result.Body, result.Diagnostics, err)
			}
		})
	}
}

func TestMessagesExtensionsDoNotSilentlyDisappearOnGeminiRoutes(t *testing.T) {
	requestConverter := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	for _, test := range []struct {
		name string
		body string
		path string
	}{
		{"block title", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"hi","title":"source"}]}]}`, "$.messages[0].content[0].title"},
		{"block context", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"hi","context":"private"}]}]}`, "$.messages[0].content[0].context"},
		{"block transformations", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":[{"type":"text","text":"hi","transformations":[{"type":"redact"}]}]}]}`, "$.messages[0].content[0].transformations"},
		{"tool eager streaming", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"eager_input_streaming":true}]}`, "$.tools[0].eager_input_streaming"},
		{"tool defer loading", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"defer_loading":true}]}`, "$.tools[0].defer_loading"},
		{"tool allowed callers", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"allowed_callers":["code_execution_20250825"]}]}`, "$.tools[0].allowed_callers"},
		{"tool input examples", `{"model":"claude","max_tokens":64,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"},"input_examples":[{}]}]}`, "$.tools[0].input_examples"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := requestConverter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.path)
			}
		})
	}

	responseConverter := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	for _, test := range []struct {
		name string
		body string
		path string
		code string
	}{
		{"stop details", `{"id":"m","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_details":{"reason":"pause"},"usage":{"input_tokens":1,"output_tokens":1}}`, "$.stop_details", "messages_stop_details_not_representable"},
		{"container", `{"id":"m","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","container":{"id":"ctr"},"usage":{"input_tokens":1,"output_tokens":1}}`, "$.container", "messages_response_container_not_representable"},
		{"cache breakdown", `{"id":"m","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0}}}`, "$.usage.cache_creation", "messages_cache_creation_breakdown_not_representable"},
		{"inference geo", `{"id":"m","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"inference_geo":"us"}}`, "$.usage.inference_geo", "messages_inference_geo_not_representable"},
		{"service tier", `{"id":"m","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1,"service_tier":"standard"}}`, "$.usage.service_tier", "messages_usage_service_tier_not_representable"},
	} {
		t.Run("response/"+test.name, func(t *testing.T) {
			_, err := responseConverter.ToClientResponse(context.Background(), []byte(test.body), conversionOptions{})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("strict error = %v, want ErrUnsupported at %s", err, test.path)
			}
			result, err := responseConverter.ToClientResponse(context.Background(), []byte(test.body), conversionOptions{LossPolicy: allowDocumentedLoss})
			if err != nil || !directHasDiagnostic(result.Diagnostics, test.code) {
				t.Fatalf("diagnostics=%#v err=%v", result.Diagnostics, err)
			}
		})
	}
}

func TestMessagesThinkingTokenDetailsMapExactlyToGemini(t *testing.T) {
	toGemini := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	body := []byte(`{"id":"m","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":7,"output_tokens_details":{"thinking_tokens":3}}}`)
	result, err := toGemini.ToClientResponse(context.Background(), body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiResponse
	_ = json.Unmarshal(result.Body, &gemini)
	if gemini.UsageMetadata.CandidatesTokenCount != 4 || gemini.UsageMetadata.ThoughtsTokenCount != 3 || gemini.UsageMetadata.TotalTokenCount != 12 {
		t.Fatalf("usage = %#v", gemini.UsageMetadata)
	}

	toMessages := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	body = []byte(`{"responseId":"g","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":4,"thoughtsTokenCount":3,"totalTokenCount":12}}`)
	result, err = toMessages.ToClientResponse(context.Background(), body, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var messages messagesResponse
	_ = json.Unmarshal(result.Body, &messages)
	if messages.Usage.OutputTokens != 7 || messages.Usage.OutputTokensDetails == nil || messages.Usage.OutputTokensDetails.ThinkingTokens != 3 {
		t.Fatalf("usage = %#v", messages.Usage)
	}
}

func TestResponsesExtensionsDoNotSilentlyDisappearOnGeminiRoutes(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	body := []byte(`{"id":"r","object":"response","model":"responses","status":"completed","output":[{"type":"message","id":"msg","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"metadata":{"k":"v"},"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	_, err := converter.ToClientResponse(context.Background(), body, conversionOptions{})
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "$.metadata") {
		t.Fatalf("strict error = %v", err)
	}
	result, err := converter.ToClientResponse(context.Background(), body, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil || !directHasDiagnostic(result.Diagnostics, "responses_metadata_not_representable") {
		t.Fatalf("diagnostics=%#v err=%v", result.Diagnostics, err)
	}

	stream, err := converter.NewClientStream(context.Background(), conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	frames, diagnostics, err := stream.Convert(context.Background(), streamFrame{Event: "response.created", Data: []byte(`{"type":"response.created","response":{"id":"r","object":"response","model":"responses","status":"in_progress","output":[],"metadata":{"k":"v"},"usage":{}}}`)})
	if err != nil || len(frames) != 0 || !directHasDiagnostic(diagnostics, "responses_metadata_not_representable") {
		t.Fatalf("frames=%#v diagnostics=%#v err=%v", frames, diagnostics, err)
	}
}
