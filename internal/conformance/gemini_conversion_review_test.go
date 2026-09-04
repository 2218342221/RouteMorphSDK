package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestResponsesToGeminiParameterlessToolAndEmptyArguments(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"model":"public",
		"input":[
			{"type":"function_call","call_id":"call_1","name":"ping","arguments":""},
			{"type":"function_call_output","call_id":"call_1","output":"ok"}
		],
		"tools":[{"type":"function","name":"ping"}]
	}`)
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolResponses, ProtocolGenerateContent, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gemini"}})
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(execution.Result.Body, &request); err != nil {
		t.Fatal(err)
	}
	tools := request["tools"].([]any)
	declarations := tools[0].(map[string]any)["functionDeclarations"].([]any)
	declaration := declarations[0].(map[string]any)
	if _, exists := declaration["parameters"]; exists {
		t.Fatalf("parameterless declaration must omit parameters: %s", execution.Result.Body)
	}
	contents := request["contents"].([]any)
	call := contents[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if call["thoughtSignature"] != geminiThoughtSignatureBypass {
		t.Fatalf("thoughtSignature = %#v", call["thoughtSignature"])
	}
	args := call["functionCall"].(map[string]any)["args"].(map[string]any)
	if len(args) != 0 {
		t.Fatalf("args = %#v, want empty object", args)
	}
}

func TestGeminiToResponsesCorrelatesParallelSameNameCalls(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{
		"contents":[
			{"role":"model","parts":[
				{"functionCall":{"name":"ping","args":{}}},
				{"functionCall":{"name":"ping","args":null}}
			]},
			{"role":"user","parts":[
				{"functionResponse":{"name":"ping","response":{"n":1}}},
				{"functionResponse":{"name":"ping","response":{"n":2}}}
			]}
		],
		"tools":[{"functionDeclarations":[{"name":"ping"}]}]
	}`)
	execution, err := harness.ToUpstreamRequest(context.Background(), ProtocolGenerateContent, ProtocolResponses, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
	if err != nil {
		t.Fatal(err)
	}
	var request responsesRequest
	if err := json.Unmarshal(execution.Result.Body, &request); err != nil {
		t.Fatal(err)
	}
	var items []responsesItem
	if err := json.Unmarshal(request.Input, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 || items[0].CallID != "rm_call_1" || items[1].CallID != "rm_call_2" || items[2].CallID != "rm_call_1" || items[3].CallID != "rm_call_2" {
		t.Fatalf("items = %#v", items)
	}
	if len(request.Tools) != 1 || jsonValuePresent(request.Tools[0].Parameters) {
		t.Fatalf("tools = %#v", request.Tools)
	}
}

func TestGeminiSignedFunctionCallFailsClosed(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"name":"ping","args":{}},"thoughtSignature":"signed"}]}]}`)
	_, err := harness.ToUpstreamRequest(context.Background(), ProtocolGenerateContent, ProtocolResponses, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestGeminiResponseUsageAndPromptBlock(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolResponses, ProtocolGenerateContent)
	result, err := harness.ToClientResponse(context.Background(), plan, []byte(`{
		"responseId":"gem_1","modelVersion":"gemini",
		"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":2,"candidatesTokenCount":5,"thoughtsTokenCount":3,"totalTokenCount":20}
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var response responsesResponse
	if err := json.Unmarshal(result.Body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Usage.InputTokens != 12 || response.Usage.OutputTokens != 8 || response.Usage.OutputTokenDetails.ReasoningTokens != 3 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	_, err = harness.ToClientResponse(context.Background(), plan, []byte(`{"candidates":[],"promptFeedback":{"blockReason":"SAFETY"}}`), conversionOptions{})
	if !errors.Is(err, ErrUpstreamResponse) || !strings.Contains(err.Error(), "SAFETY") {
		t.Fatalf("error = %v, want prompt block upstream error", err)
	}
}

func TestGeminiJSONArrayStreamToResponses(t *testing.T) {
	harness, _ := newTestRouterHarness()
	plan, _ := harness.catalog().Plan(ProtocolResponses, ProtocolGenerateContent)
	stream, err := harness.NewResponseStream(context.Background(), plan, conversionOptions{Exchange: exchangeMetadata{ClientModel: "public"}})
	if err != nil {
		t.Fatal(err)
	}
	frame := streamFrame{Data: []byte(`[
		{"responseId":"gem_1","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"text":"hel"}]}}]},
		{"responseId":"gem_1","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":2,"totalTokenCount":3}}
	]`)}
	frames, _, err := stream.Convert(context.Background(), frame)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := stream.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for _, output := range frames {
		joined.Write(output.Data)
	}
	text := joined.String()
	for _, expected := range []string{"response.created", `"delta":"hel"`, `"delta":"lo"`, "response.completed", `"model":"public"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("stream missing %q: %s", expected, text)
		}
	}
}

func TestGeminiStreamAcceptsUsageChunkAfterFinish(t *testing.T) {
	converter, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{Exchange: exchangeMetadata{ClientModel: "public"}})
	if err != nil {
		t.Fatal(err)
	}
	frames, _, err := converter.Convert(context.Background(), streamFrame{Data: []byte(`{
		"responseId":"gem_1","modelVersion":"gemini",
		"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"STOP"}]
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, frame := range frames {
		if frame.Event == "response.completed" {
			t.Fatal("terminal response emitted before a possible usage-only chunk")
		}
	}
	terminal, _, err := converter.Convert(context.Background(), streamFrame{Data: []byte(`{
		"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(terminal) != 1 || terminal[0].Event != "response.completed" || !strings.Contains(string(terminal[0].Data), `"total_tokens":6`) {
		t.Fatalf("terminal frames = %#v", terminal)
	}
}

func TestGeminiStreamAcceptsEmptyTextTerminalButRejectsEmptyPart(t *testing.T) {
	converter, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	frames, _, err := converter.Convert(context.Background(), streamFrame{Data: []byte(`{
		"responseId":"gem_1","modelVersion":"gemini",
		"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":1,"totalTokenCount":1}
	}`)})
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) == 0 || frames[len(frames)-1].Event != "response.completed" {
		t.Fatalf("frames = %#v, want terminal response", frames)
	}

	emptyObject, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = emptyObject.Convert(context.Background(), streamFrame{Data: []byte(`{
		"candidates":[{"content":{"role":"model","parts":[{}]},"finishReason":"STOP"}]
	}`)})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
}

func TestGeminiToResponsesResponsePreservesPartOrder(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	result, err := converter.ToClientResponse(context.Background(), []byte(`{
		"responseId":"gem_1","modelVersion":"gemini",
		"candidates":[{"content":{"role":"model","parts":[
			{"text":"before"},
			{"functionCall":{"id":"call_1","name":"lookup","args":{"q":"x"}}},
			{"text":"after"}
		]},"finishReason":"STOP"}]
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var response responsesResponse
	if err := json.Unmarshal(result.Body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Output) != 3 || response.Output[0].Type != "message" || response.Output[1].Type != "function_call" || response.Output[2].Type != "message" {
		t.Fatalf("output order = %#v", response.Output)
	}
	if !strings.Contains(string(response.Output[0].Content), "before") || !strings.Contains(string(response.Output[2].Content), "after") {
		t.Fatalf("output content = %#v", response.Output)
	}
}

func TestGeminiToResponsesResponseRejectsDuplicateCallIDs(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	_, err := converter.ToClientResponse(context.Background(), []byte(`{
		"responseId":"gem_1","modelVersion":"gemini",
		"candidates":[{"content":{"role":"model","parts":[
			{"functionCall":{"id":"call_1","name":"first","args":{}}},
			{"functionCall":{"id":"call_1","name":"second","args":{}}}
		]},"finishReason":"STOP"}]
	}`), conversionOptions{})
	if !errors.Is(err, ErrUpstreamResponse) || !strings.Contains(err.Error(), "duplicate function call id") {
		t.Fatalf("error = %v, want duplicate call id ErrUpstreamResponse", err)
	}
}

func TestGeminiToResponsesStreamKeepsCallIDsUnique(t *testing.T) {
	stream, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []streamFrame{
		{Data: []byte(`{"responseId":"gem_1","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"rm_call_1","name":"first","args":{}}}]}}]}`)},
		{Data: []byte(`{"responseId":"gem_1","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"second","args":{}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)},
	}
	var callIDs []string
	for _, input := range inputs {
		frames, _, err := stream.Convert(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		for _, frame := range frames {
			if frame.Event != "response.output_item.added" {
				continue
			}
			var event struct {
				Item responsesItem `json:"item"`
			}
			if err := json.Unmarshal(frame.Data, &event); err != nil {
				t.Fatal(err)
			}
			callIDs = append(callIDs, event.Item.CallID)
		}
	}
	if _, _, err := stream.Finalize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(callIDs, ","), "rm_call_1,rm_call_2"; got != want {
		t.Fatalf("call ids = %q, want %q", got, want)
	}
}

func TestGeminiToResponsesStreamRejectsDuplicateCallIDs(t *testing.T) {
	stream, err := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent}).NewClientStream(context.Background(), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first := streamFrame{Data: []byte(`{"responseId":"gem_1","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"first","args":{}}}]}}]}`)}
	if _, _, err := stream.Convert(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	duplicate := streamFrame{Data: []byte(`{"responseId":"gem_1","modelVersion":"gemini","candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_1","name":"second","args":{}}}]},"finishReason":"STOP"}]}`)}
	_, _, err = stream.Convert(context.Background(), duplicate)
	if !errors.Is(err, ErrUpstreamResponse) || !strings.Contains(err.Error(), "duplicate function call id") {
		t.Fatalf("error = %v, want duplicate call id ErrUpstreamResponse", err)
	}
}

func TestGeminiToolConfigFailsClosedWithoutExactResponsesMapping(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	for _, test := range []struct {
		name string
		mode string
		want error
	}{
		{name: "validated", mode: `"mode":"VALIDATED"`, want: ErrUnsupported},
		{name: "auto restricted", mode: `"mode":"AUTO","allowedFunctionNames":["f"]`, want: ErrUnsupported},
		{name: "none restricted", mode: `"mode":"NONE","allowedFunctionNames":["f"]`, want: ErrInvalidPayload},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"f"}]}],"toolConfig":{"functionCallingConfig":{` + test.mode + `}}}`)
			_, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMessagesToGeminiPreservesResponseJSONSchemaDialect(t *testing.T) {
	converter := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent})
	result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"claude","max_tokens":16,
		"output_config":{"format":{"type":"json_schema","schema":{
			"type":"object","$defs":{"item":{"type":"string"}},
			"properties":{"value":{"$ref":"#/$defs/item"}},"additionalProperties":false
		}}},
		"messages":[{"role":"user","content":"hi"}]
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var request geminiRequest
	if err := json.Unmarshal(result.Body, &request); err != nil {
		t.Fatal(err)
	}
	schema := string(request.GenerationConfig.ResponseJSONSchema)
	for _, expected := range []string{`"type":"object"`, `"$defs"`, `"$ref"`, `"additionalProperties":false`} {
		if !strings.Contains(schema, expected) {
			t.Fatalf("responseJsonSchema lost %q: %s", expected, schema)
		}
	}
	if strings.Contains(schema, `"type":"OBJECT"`) {
		t.Fatalf("responseJsonSchema was rewritten to OpenAPI type casing: %s", schema)
	}
}

func TestGeminiMultimodalModelOutputFailsClosed(t *testing.T) {
	response := []byte(`{
		"responseId":"gem_1","modelVersion":"gemini",
		"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"aW1n"}}]},"finishReason":"STOP"}]
	}`)
	if _, err := (newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})).ToClientResponse(context.Background(), response, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Chat error = %v, want ErrUnsupported", err)
	}
	if _, err := newMessagesGeminiRoute(routeSpec{From: ProtocolMessages, To: ProtocolGenerateContent}).ToClientResponse(context.Background(), response, conversionOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Messages error = %v, want ErrUnsupported", err)
	}
}

func TestGeminiThinkingLevelUsesOfficialEnumOnly(t *testing.T) {
	responsesConverter := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	valid, err := responsesConverter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"responses","input":"hi","reasoning":{"effort":"low"}
	}`), conversionOptions{LossPolicy: allowDocumentedLoss})
	if err != nil {
		t.Fatal(err)
	}
	var request geminiRequest
	if err := json.Unmarshal(valid.Body, &request); err != nil {
		t.Fatal(err)
	}
	if request.GenerationConfig.ThinkingConfig.ThinkingLevel != "LOW" {
		t.Fatalf("thinkingLevel = %q", request.GenerationConfig.ThinkingConfig.ThinkingLevel)
	}
	_, err = responsesConverter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"responses","input":"hi","reasoning":{"effort":"xhigh"}
	}`), conversionOptions{LossPolicy: allowDocumentedLoss})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Responses error = %v, want ErrUnsupported", err)
	}
	_, err = (newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})).ToUpstreamRequest(context.Background(), []byte(`{
		"model":"chat","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"xhigh"
	}`), conversionOptions{})
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Chat error = %v, want ErrUnsupported", err)
	}
}

func TestGeminiResponseSchemaRequiresJSONMIMEType(t *testing.T) {
	converter := newResponsesGeminiRoute(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	for _, mime := range []string{"", `,"responseMimeType":"text/plain"`} {
		body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"responseJsonSchema":{"type":"object"}` + mime + `}}`)
		_, err := converter.ToUpstreamRequest(context.Background(), body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}})
		if !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("mime suffix %q: error = %v, want ErrInvalidPayload", mime, err)
		}
	}
	valid := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"responseJsonSchema":{"type":"object"},"responseMimeType":"application/json"}}`)
	if _, err := converter.ToUpstreamRequest(context.Background(), valid, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "responses"}}); err != nil {
		t.Fatal(err)
	}
}
