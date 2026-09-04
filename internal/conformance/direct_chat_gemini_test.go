package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestDirectChatToGeminiRichRequest(t *testing.T) {
	converter := newChatGeminiRoute(routeSpec{ID: "chat_to_gemini", From: ProtocolChat, To: ProtocolGenerateContent})
	input := []byte(`{
  "model":"client-model","stream":true,"max_completion_tokens":256,"temperature":0.2,"top_p":0.8,
  "stop":["END","DONE"],"n":1,"frequency_penalty":0.1,"presence_penalty":0.2,
  "response_format":{"type":"json_schema","json_schema":{"schema":{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}}},
  "tools":[{"type":"function","function":{"name":"get_weather","description":"weather lookup","parameters":{"type":"object","properties":{}},"strict":false}}],
  "tool_choice":{"type":"function","function":{"name":"get_weather"}},
  "messages":[
    {"role":"system","content":"be concise"},
    {"role":"user","content":[{"type":"text","text":"weather?"},{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1n"}}]},
    {"role":"assistant","content":null,"reasoning_content":"need tool","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":""}}]},
    {"role":"tool","tool_call_id":"call_1","content":"{\"temperature\":15}"}
  ]
}`)
	result, err := converter.ToUpstreamRequest(context.Background(), input, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gemini-upstream", Stream: true, StreamSet: true}})
	if err != nil {
		t.Fatal(err)
	}
	var got geminiRequest
	if err := json.Unmarshal(result.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.GenerationConfig == nil || got.GenerationConfig.MaxOutputTokens == nil || *got.GenerationConfig.MaxOutputTokens != 256 {
		t.Fatalf("generationConfig = %#v", got.GenerationConfig)
	}
	if strings.Join(got.GenerationConfig.StopSequences, ",") != "END,DONE" {
		t.Fatalf("stopSequences = %#v", got.GenerationConfig.StopSequences)
	}
	if got.GenerationConfig.ResponseMIMEType != "application/json" || !jsonValuePresent(got.GenerationConfig.ResponseJSONSchema) {
		t.Fatalf("response schema = %#v", got.GenerationConfig)
	}
	if len(got.Tools) != 1 || len(got.Tools[0].FunctionDeclarations) != 1 || jsonValuePresent(got.Tools[0].FunctionDeclarations[0].Parameters) {
		t.Fatalf("parameterless tool = %#v", got.Tools)
	}
	if got.ToolConfig == nil || got.ToolConfig.FunctionCallingConfig.Mode != "ANY" || strings.Join(got.ToolConfig.FunctionCallingConfig.AllowedFunctionNames, ",") != "get_weather" {
		t.Fatalf("toolConfig = %#v", got.ToolConfig)
	}
	if got.SystemInstruction == nil || got.SystemInstruction.Parts[0].Text != "be concise" || len(got.Contents) != 3 {
		t.Fatalf("contents = %#v system = %#v", got.Contents, got.SystemInstruction)
	}
	if got.Contents[0].Parts[1].InlineData == nil || got.Contents[0].Parts[1].InlineData.MIMEType != "image/png" {
		t.Fatalf("media = %#v", got.Contents[0].Parts)
	}
	callParts := got.Contents[1].Parts
	if len(callParts) != 2 || !callParts[0].Thought || callParts[0].ThoughtSignature != geminiThoughtSignatureBypass {
		t.Fatalf("reasoning parts = %#v", callParts)
	}
	if callParts[1].FunctionCall == nil || callParts[1].FunctionCall.ID != "call_1" || callParts[1].FunctionCall.Name != "get_weather" || string(callParts[1].FunctionCall.Args) != `{}` {
		t.Fatalf("functionCall = %#v", callParts[1])
	}
	response := got.Contents[2].Parts[0].FunctionResponse
	if response == nil || response.ID != "call_1" || response.Name != "get_weather" || string(response.Response) != `{"temperature":15}` {
		t.Fatalf("functionResponse = %#v", response)
	}
}

func TestDirectGeminiToChatRichRequest(t *testing.T) {
	converter := newChatGeminiRoute(routeSpec{ID: "gemini_to_chat", From: ProtocolGenerateContent, To: ProtocolChat})
	input := []byte(`{
  "systemInstruction":{"parts":[{"text":"be concise"}]},
  "contents":[
    {"role":"user","parts":[{"text":"weather?"},{"inlineData":{"mimeType":"image/png","data":"aW1n"}}]},
    {"role":"model","parts":[{"text":"need tool","thought":true,"thoughtSignature":"context_engineering_is_the_way_to_go"},{"functionCall":{"id":"call_1","name":"get_weather","args":{}},"thoughtSignature":"context_engineering_is_the_way_to_go"}]},
    {"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"get_weather","response":{"temperature":15}}}]}
  ],
  "tools":[{"functionDeclarations":[{"name":"get_weather","description":"weather lookup"}]}],
  "toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["get_weather"]}},
  "generationConfig":{"maxOutputTokens":256,"temperature":0.2,"topP":0.8,"stopSequences":["END","DONE"],"candidateCount":1,"presencePenalty":0.2,"frequencyPenalty":0.1,"responseMimeType":"application/json","responseJsonSchema":{"type":"object","properties":{"ok":{"type":"boolean"}}},"thinkingConfig":{"thinkingLevel":"LOW"}}
}`)
	result, err := converter.ToUpstreamRequest(context.Background(), input, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "chat-upstream", Stream: true, StreamSet: true}})
	if err != nil {
		t.Fatal(err)
	}
	var got chatRequest
	if err := json.Unmarshal(result.Body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "chat-upstream" || !got.Stream || got.MaxCompletion == nil || *got.MaxCompletion != 256 {
		t.Fatalf("request = %#v", got)
	}
	stop, err := decodeStop(ProtocolChat, got.Stop)
	if err != nil || strings.Join(stop, ",") != "END,DONE" {
		t.Fatalf("stop = %#v err=%v", stop, err)
	}
	if got.ReasoningEffort != "low" || got.ResponseFormat == nil {
		t.Fatalf("reasoning=%q format=%s", got.ReasoningEffort, got.ResponseFormat)
	}
	if len(got.Tools) != 1 || !directJSONObjectsEqual(got.Tools[0].Function.Parameters, []byte(`{"properties":{},"type":"object"}`)) {
		t.Fatalf("tools = %#v", got.Tools)
	}
	if len(got.Messages) != 4 || got.Messages[2].ToolCalls[0].ID != "call_1" || got.Messages[2].ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("messages = %#v", got.Messages)
	}
	if got.Messages[2].ReasoningContent != "need tool" || got.Messages[3].ToolCallID != "call_1" || got.Messages[3].Name != "get_weather" {
		t.Fatalf("tool history = %#v", got.Messages)
	}
	if rawString(got.Messages[3].Content) != `{"temperature":15}` {
		t.Fatalf("tool content = %s", got.Messages[3].Content)
	}
}

func TestDirectChatGeminiResponses(t *testing.T) {
	chatToGemini := newChatGeminiRoute(routeSpec{ID: "chat_to_gemini", From: ProtocolChat, To: ProtocolGenerateContent})
	geminiResponseBody := []byte(`{"responseId":"resp_1","modelVersion":"gemini-upstream","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"thinking","thought":true,"thoughtSignature":"context_engineering_is_the_way_to_go"},{"text":"answer"},{"functionCall":{"id":"call_1","name":"lookup","args":{}},"thoughtSignature":"context_engineering_is_the_way_to_go"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"toolUsePromptTokenCount":2,"candidatesTokenCount":3,"thoughtsTokenCount":4,"cachedContentTokenCount":1,"totalTokenCount":14}}`)
	converted, err := chatToGemini.ToClientResponse(context.Background(), geminiResponseBody, conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
	if err != nil {
		t.Fatal(err)
	}
	var chat chatResponse
	if err := json.Unmarshal(converted.Body, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Model != "client-model" || chat.Choices[0].FinishReason != "tool_calls" || chat.Choices[0].Message.ReasoningContent != "thinking" || len(chat.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("chat response = %#v", chat)
	}
	if chat.Usage.PromptTokens != 7 || chat.Usage.CompletionTokens != 7 || chat.Usage.CompletionDetails.ReasoningTokens != 4 {
		t.Fatalf("chat usage = %#v", chat.Usage)
	}

	geminiToChat := newChatGeminiRoute(routeSpec{ID: "gemini_to_chat", From: ProtocolGenerateContent, To: ProtocolChat})
	chatResponseBody := []byte(`{"id":"chat_1","object":"chat.completion","created":1,"model":"chat-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"answer","reasoning_content":"thinking","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":7,"completion_tokens":7,"total_tokens":14,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens_details":{"reasoning_tokens":4}}}`)
	converted, err = geminiToChat.ToClientResponse(context.Background(), chatResponseBody, conversionOptions{Exchange: exchangeMetadata{ClientModel: "client-model"}})
	if err != nil {
		t.Fatal(err)
	}
	var gemini geminiResponse
	if err := json.Unmarshal(converted.Body, &gemini); err != nil {
		t.Fatal(err)
	}
	if gemini.ModelVersion != "client-model" || gemini.Candidates[0].FinishReason != "STOP" || len(gemini.Candidates[0].Content.Parts) != 3 {
		t.Fatalf("Gemini response = %#v", gemini)
	}
	if !gemini.Candidates[0].Content.Parts[0].Thought || gemini.Candidates[0].Content.Parts[0].ThoughtSignature != geminiThoughtSignatureBypass {
		t.Fatalf("thought = %#v", gemini.Candidates[0].Content.Parts[0])
	}
	if call := gemini.Candidates[0].Content.Parts[2].FunctionCall; call == nil || call.ID != "call_1" || call.Name != "lookup" {
		t.Fatalf("functionCall = %#v", call)
	}
	if gemini.UsageMetadata.CandidatesTokenCount != 3 || gemini.UsageMetadata.ThoughtsTokenCount != 4 || gemini.UsageMetadata.TotalTokenCount != 14 {
		t.Fatalf("Gemini usage = %#v", gemini.UsageMetadata)
	}
}

func TestDirectChatGeminiStopSequencesPassThrough(t *testing.T) {
	chatToGemini := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
	result, err := chatToGemini.ToUpstreamRequest(context.Background(), []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stop":"<END>"}`), conversionOptions{})
	var gemini geminiRequest
	decodeErr := json.Unmarshal(result.Body, &gemini)
	if err != nil || decodeErr != nil || len(gemini.GenerationConfig.StopSequences) != 1 || gemini.GenerationConfig.StopSequences[0] != "<END>" {
		t.Fatalf("body=%s err=%v", result.Body, err)
	}
	geminiToChat := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	result, err = geminiToChat.ToUpstreamRequest(context.Background(), []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"stopSequences":["A","B"]}}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "m"}})
	if err != nil || !strings.Contains(string(result.Body), `"stop":["A","B"]`) {
		t.Fatalf("body=%s err=%v", result.Body, err)
	}
}

func TestDirectGeminiAuthenticThoughtSignatureFailsClosed(t *testing.T) {
	converter := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	body := []byte(`{"contents":[{"role":"model","parts":[{"text":"private","thought":true,"thoughtSignature":"signed-provider-state"}]}]}`)
	_, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "m"}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestDirectChatGeminiBufferedStreams(t *testing.T) {
	chatToGemini := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
	stream, err := chatToGemini.NewClientStream(context.Background(), conversionOptions{Exchange: exchangeMetadata{ClientModel: "client"}})
	if err != nil {
		t.Fatal(err)
	}
	frames, _, err := stream.Convert(context.Background(), streamFrame{Data: []byte(`{"responseId":"r","modelVersion":"upstream","candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)})
	if err != nil || len(frames) != 0 {
		t.Fatalf("Convert frames=%#v err=%v", frames, err)
	}
	frames, diagnostics, err := stream.Finalize(context.Background())
	if err != nil || len(frames) < 3 || frames[len(frames)-1].Done != true || !directHasDiagnostic(diagnostics, "buffered_stream_conversion") {
		t.Fatalf("Finalize frames=%#v diagnostics=%#v err=%v", frames, diagnostics, err)
	}

	geminiToChat := newChatGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolChat})
	stream, err = geminiToChat.NewClientStream(context.Background(), conversionOptions{Exchange: exchangeMetadata{ClientModel: "client"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range []streamFrame{
		{Data: []byte(`{"id":"r","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":""}]}`)},
		{Data: []byte(`{"id":"r","object":"chat.completion.chunk","created":1,"model":"upstream","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)},
		{Data: []byte(`[DONE]`), Done: true},
	} {
		if _, _, err := stream.Convert(context.Background(), frame); err != nil {
			t.Fatal(err)
		}
	}
	frames, diagnostics, err = stream.Finalize(context.Background())
	if err != nil || len(frames) != 1 || !strings.Contains(string(frames[0].Data), "hello") || !directHasDiagnostic(diagnostics, "buffered_stream_conversion") {
		t.Fatalf("Finalize frames=%#v diagnostics=%#v err=%v", frames, diagnostics, err)
	}
}

func directHasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func directJSONObjectsEqual(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil &&
		string(mustJSON(leftValue)) == string(mustJSON(rightValue))
}
