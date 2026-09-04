package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDirectMessagesToGeminiRequestPreservesPortableSemantics(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{ID: "messages_to_gemini", From: ProtocolMessages, To: ProtocolGenerateContent})
	input := []byte(`{
		"model":"claude-client","max_tokens":321,"temperature":0.2,"top_p":0.8,
		"stop_sequences":["END"],"system":[{"type":"text","text":"be exact"}],
		"tools":[{"name":"weather","description":"lookup","input_schema":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}],
		"tool_choice":{"type":"tool","name":"weather"},
		"messages":[
			{"role":"user","content":[{"type":"text","text":"weather?"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGVsbG8="}}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"weather","input":{"city":"Paris"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"{\"temp\":20}"}]}
		]
	}`)
	result, err := converter.ToUpstreamRequest(context.Background(), input, conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var target geminiRequest
	if err := json.Unmarshal(result.Body, &target); err != nil {
		t.Fatal(err)
	}
	if target.GenerationConfig == nil || target.GenerationConfig.MaxOutputTokens == nil || *target.GenerationConfig.MaxOutputTokens != 321 || target.GenerationConfig.Temperature == nil || *target.GenerationConfig.Temperature != 0.2 || target.GenerationConfig.TopP == nil || *target.GenerationConfig.TopP != 0.8 {
		t.Fatalf("generationConfig = %#v", target.GenerationConfig)
	}
	if len(target.GenerationConfig.StopSequences) != 1 || target.GenerationConfig.StopSequences[0] != "END" {
		t.Fatalf("stopSequences = %#v", target.GenerationConfig.StopSequences)
	}
	if target.SystemInstruction == nil || len(target.SystemInstruction.Parts) != 1 || target.SystemInstruction.Parts[0].Text != "be exact" {
		t.Fatalf("systemInstruction = %#v", target.SystemInstruction)
	}
	if len(target.Tools) != 1 || len(target.Tools[0].FunctionDeclarations) != 1 || !strings.Contains(string(target.Tools[0].FunctionDeclarations[0].Parameters), `"type":"OBJECT"`) {
		t.Fatalf("tools = %#v", target.Tools)
	}
	if target.ToolConfig == nil || target.ToolConfig.FunctionCallingConfig.Mode != "ANY" || len(target.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) != 1 || target.ToolConfig.FunctionCallingConfig.AllowedFunctionNames[0] != "weather" {
		t.Fatalf("toolConfig = %#v", target.ToolConfig)
	}
	if len(target.Contents) != 3 || target.Contents[0].Parts[1].InlineData == nil {
		t.Fatalf("contents = %#v", target.Contents)
	}
	call := target.Contents[1].Parts[0]
	if call.FunctionCall == nil || call.FunctionCall.ID != "call_1" || call.ThoughtSignature != geminiThoughtSignatureBypass {
		t.Fatalf("functionCall = %#v", call)
	}
	response := target.Contents[2].Parts[0].FunctionResponse
	if response == nil || response.ID != "call_1" || response.Name != "weather" || string(response.Response) != `{"temp":20}` {
		t.Fatalf("functionResponse = %#v", response)
	}
	if !hasMessagesGeminiDiagnostic(result.Diagnostics, "gemini_thought_signature_bypass_added") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestDirectGeminiToMessagesRequestPreservesPortableSemantics(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{ID: "gemini_to_messages", From: ProtocolGenerateContent, To: ProtocolMessages})
	input := []byte(`{
		"systemInstruction":{"parts":[{"text":"be exact"}]},
		"generationConfig":{"maxOutputTokens":222,"temperature":0.3,"topP":0.9,"stopSequences":["END"],"responseMimeType":"application/json","responseJsonSchema":{"type":"OBJECT","properties":{"answer":{"type":"STRING","nullable":true}}}},
		"tools":[{"functionDeclarations":[{"name":"weather","description":"lookup","parameters":{"type":"OBJECT","properties":{"city":{"type":"STRING"}},"required":["city"]}}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["weather"]}},
		"contents":[
			{"role":"user","parts":[{"text":"weather?"},{"inlineData":{"mimeType":"image/png","data":"aGVsbG8="}}]},
			{"role":"model","parts":[{"functionCall":{"name":"weather","args":{"city":"Paris"}},"thoughtSignature":"context_engineering_is_the_way_to_go"}]},
			{"role":"user","parts":[{"functionResponse":{"name":"weather","response":{"output":{"temp":20}}}}]}
		]
	}`)
	result, err := converter.ToUpstreamRequest(context.Background(), input, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude-upstream", Stream: true}})
	if err != nil {
		t.Fatal(err)
	}
	var target messagesRequest
	if err := json.Unmarshal(result.Body, &target); err != nil {
		t.Fatal(err)
	}
	if target.Model != "claude-upstream" || target.MaxTokens != 222 || !target.Stream || target.Temperature == nil || *target.Temperature != 0.3 || target.TopP == nil || *target.TopP != 0.9 || len(target.StopSequences) != 1 {
		t.Fatalf("request fields = %#v", target)
	}
	if target.OutputConfig == nil || target.OutputConfig.Format == nil || !strings.Contains(string(target.OutputConfig.Format.Schema), `"type":["string","null"]`) {
		t.Fatalf("output_config = %#v", target.OutputConfig)
	}
	if len(target.Tools) != 1 || !strings.Contains(string(target.Tools[0].InputSchema), `"type":"string"`) {
		t.Fatalf("tools = %#v", target.Tools)
	}
	var choice struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(target.ToolChoice, &choice); err != nil || choice.Type != "tool" || choice.Name != "weather" {
		t.Fatalf("tool_choice = %s", target.ToolChoice)
	}
	if len(target.Messages) != 3 {
		t.Fatalf("messages = %#v", target.Messages)
	}
	var callBlocks, resultBlocks []messagesBlock
	_ = json.Unmarshal(target.Messages[1].Content, &callBlocks)
	_ = json.Unmarshal(target.Messages[2].Content, &resultBlocks)
	if len(callBlocks) != 1 || callBlocks[0].ID == "" || len(resultBlocks) != 1 || resultBlocks[0].ToolUseID != callBlocks[0].ID || rawString(resultBlocks[0].Content) != `{"temp":20}` {
		t.Fatalf("call=%#v result=%#v", callBlocks, resultBlocks)
	}
	if !hasMessagesGeminiDiagnostic(result.Diagnostics, "function_call_id_generated") || !hasMessagesGeminiDiagnostic(result.Diagnostics, "gemini_thought_signature_bypass_removed") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestDirectMessagesGeminiRequestsFailClosed(t *testing.T) {
	messages := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	gemini := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	tests := []struct {
		name      string
		Converter routeConverter
		body      string
		options   conversionOptions
	}{
		{"messages thinking", messages, `{"model":"x","max_tokens":2048,"thinking":{"type":"enabled","budget_tokens":1024},"messages":[{"role":"user","content":"hi"}]}`, conversionOptions{}},
		{"messages cache", messages, `{"model":"x","max_tokens":10,"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}]}`, conversionOptions{}},
		{"messages metadata", messages, `{"model":"x","max_tokens":10,"metadata":{"user_id":"u"},"messages":[{"role":"user","content":"hi"}]}`, conversionOptions{}},
		{"gemini safety", gemini, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"safetySettings":[{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"OFF"}]}`, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "x"}}},
		{"gemini cache", gemini, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"cachedContent":"cachedContents/1"}`, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "x"}}},
		{"gemini thinking", gemini, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"includeThoughts":true}}}`, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "x"}}},
		{"gemini signature", gemini, `{"contents":[{"role":"model","parts":[{"functionCall":{"name":"f","args":{}},"thoughtSignature":"opaque-vendor-signature"}]}]}`, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "x"}}},
		{"gemini validated choice", gemini, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"f","parameters":{"type":"OBJECT"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"VALIDATED"}}}`, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "x"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.Converter.ToUpstreamRequest(context.Background(), []byte(test.body), test.options)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestDirectMessagesToGeminiResponsePreservesUsageAndFunctionCall(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	input := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"text","text":"checking"},{"type":"tool_use","id":"call_1","name":"weather","input":{"city":"Paris"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":3}}`)
	result, err := converter.ToClientResponse(context.Background(), input, conversionOptions{Exchange: exchangeMetadata{ClientModel: "gemini-client"}})
	if err != nil {
		t.Fatal(err)
	}
	var target geminiResponse
	if err := json.Unmarshal(result.Body, &target); err != nil {
		t.Fatal(err)
	}
	if target.ResponseID != "msg_1" || target.ModelVersion != "gemini-client" || target.Candidates[0].FinishReason != "STOP" {
		t.Fatalf("response = %#v", target)
	}
	call := target.Candidates[0].Content.Parts[1]
	if call.FunctionCall == nil || call.FunctionCall.ID != "call_1" || call.ThoughtSignature != geminiThoughtSignatureBypass {
		t.Fatalf("function call = %#v", call)
	}
	if target.UsageMetadata.PromptTokenCount != 13 || target.UsageMetadata.CandidatesTokenCount != 4 || target.UsageMetadata.TotalTokenCount != 17 || target.UsageMetadata.CachedContentTokenCount != 3 {
		t.Fatalf("usage = %#v", target.UsageMetadata)
	}
}

func TestDirectGeminiToMessagesResponsePreservesUsageAndFinish(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	input := []byte(`{"responseId":"gem_1","modelVersion":"gemini-upstream","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"weather","args":{"city":"Paris"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":2,"candidatesTokenCount":4,"thoughtsTokenCount":1,"totalTokenCount":17,"cachedContentTokenCount":3}}`)
	result, err := converter.ToClientResponse(context.Background(), input, conversionOptions{Exchange: exchangeMetadata{ClientModel: "claude-client"}})
	if err != nil {
		t.Fatal(err)
	}
	var target messagesResponse
	if err := json.Unmarshal(result.Body, &target); err != nil {
		t.Fatal(err)
	}
	if target.ID != "gem_1" || target.Model != "claude-client" || target.StopReason != "tool_use" {
		t.Fatalf("response = %#v", target)
	}
	var blocks []messagesBlock
	_ = json.Unmarshal(target.Content, &blocks)
	if len(blocks) != 1 || blocks[0].ID != "call_1" || blocks[0].Name != "weather" {
		t.Fatalf("content = %#v", blocks)
	}
	if target.Usage.InputTokens != 9 || target.Usage.OutputTokens != 5 || target.Usage.CacheReadInputTokens != 3 {
		t.Fatalf("usage = %#v", target.Usage)
	}
}

func TestDirectGeminiToMessagesResponseRejectsImpossibleCachedUsage(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	input := []byte(`{"responseId":"gem_1","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"toolUsePromptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":4,"cachedContentTokenCount":4}}`)
	_, err := converter.ToClientResponse(context.Background(), input, conversionOptions{})
	if !errors.Is(err, ErrUpstreamResponse) {
		t.Fatalf("error = %v, want ErrUpstreamResponse", err)
	}
}

func TestDirectMessagesToGeminiResponseCacheCreationNeedsLossPolicy(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages})
	input := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":1,"cache_creation_input_tokens":2}}`)
	if _, err := converter.ToClientResponse(context.Background(), input, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("strict error = %v, want ErrUnsupported", err)
	}
	result, err := converter.ToClientResponse(context.Background(), input, conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	if !hasMessagesGeminiDiagnostic(result.Diagnostics, "cache_creation_usage_not_representable") {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	var target geminiResponse
	if err := json.Unmarshal(result.Body, &target); err != nil {
		t.Fatal(err)
	}
	if target.UsageMetadata.PromptTokenCount != 7 || target.UsageMetadata.CandidatesTokenCount != 1 || target.UsageMetadata.TotalTokenCount != 8 {
		t.Fatalf("usage = %#v", target.UsageMetadata)
	}
}

func TestDirectMessagesGeminiStreamsUseBufferedFallback(t *testing.T) {
	for _, converter := range []routeConverter{
		newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent}),
		newMessagesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolMessages}),
	} {
		stream, err := converter.NewClientStream(context.Background(), conversionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if stream == nil {
			t.Fatal("buffered stream converter is nil")
		}
	}
}

func hasMessagesGeminiDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
