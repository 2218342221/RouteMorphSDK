package responsesgemini

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestFunctionOutputJSONSchemaMapsBidirectionally(t *testing.T) {
	t.Run("Responses to Gemini", func(t *testing.T) {
		converter := New(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
		result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
			"model":"responses-model",
			"input":"hi",
			"tools":[{
				"type":"function",
				"name":"lookup",
				"parameters":{"type":"object"},
				"output_schema":{
					"type":"object",
					"$defs":{"answer":{"type":["string","null"]}},
					"properties":{"answer":{"$ref":"#/$defs/answer"}},
					"additionalProperties":false
				}
			}]
		}`), conversionOptions{Exchange: core.ExchangeMetadata{UpstreamModel: "gemini-model"}})
		if err != nil {
			t.Fatal(err)
		}
		var request geminiRequest
		if err := json.Unmarshal(result.Body, &request); err != nil {
			t.Fatal(err)
		}
		declaration := request.Tools[0].FunctionDeclarations[0]
		assertJSONEqual(t, declaration.ResponseJSONSchema, `{
			"type":"object",
			"$defs":{"answer":{"type":["string","null"]}},
			"properties":{"answer":{"$ref":"#/$defs/answer"}},
			"additionalProperties":false
		}`)
		if jsonValuePresent(declaration.Response) {
			t.Fatalf("legacy response schema unexpectedly set: %s", declaration.Response)
		}
	})

	t.Run("Gemini to Responses", func(t *testing.T) {
		converter := New(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
		result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
			"contents":[{"role":"user","parts":[{"text":"hi"}]}],
			"tools":[{"functionDeclarations":[{
				"name":"lookup",
				"parametersJsonSchema":{"type":"object"},
				"responseJsonSchema":{
					"type":"object",
					"$defs":{"answer":{"type":["string","null"]}},
					"properties":{"answer":{"$ref":"#/$defs/answer"}},
					"additionalProperties":false
				}
			}]}]
		}`), conversionOptions{Exchange: core.ExchangeMetadata{UpstreamModel: "responses-model"}})
		if err != nil {
			t.Fatal(err)
		}
		var request responsesRequest
		if err := json.Unmarshal(result.Body, &request); err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, request.Tools[0].OutputSchema, `{
			"type":"object",
			"$defs":{"answer":{"type":["string","null"]}},
			"properties":{"answer":{"$ref":"#/$defs/answer"}},
			"additionalProperties":false
		}`)
	})
}

func TestResponsesFunctionFieldsFailClosedOnGeminiRoute(t *testing.T) {
	converter := New(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	tests := []struct {
		name string
		body string
		path string
	}{
		{
			name: "tool async",
			body: `{"model":"m","input":"hi","tools":[{"type":"function","name":"f","async":true}]}`,
			path: "$.tools[0].async",
		},
		{
			name: "tool allowed callers",
			body: `{"model":"m","input":"hi","tools":[{"type":"function","name":"f","allowed_callers":["direct"]}]}`,
			path: "$.tools[0].allowed_callers",
		},
		{
			name: "deferred tool",
			body: `{"model":"m","input":"hi","tools":[{"type":"function","name":"f","defer_loading":true}]}`,
			path: "$.tools[0].defer_loading",
		},
		{
			name: "call async",
			body: `{"model":"m","input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}","async":true}]}`,
			path: "$.input[0].async",
		},
		{
			name: "call caller",
			body: `{"model":"m","input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}","caller":{"type":"direct"}}]}`,
			path: "$.input[0].caller",
		},
		{
			name: "call namespace",
			body: `{"model":"m","input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}","namespace":"tools"}]}`,
			path: "$.input[0].namespace",
		},
		{
			name: "call provenance",
			body: `{"model":"m","input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}","created_by":"server"}]}`,
			path: "$.input[0].created_by",
		},
		{
			name: "incomplete call",
			body: `{"model":"m","input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}","status":"incomplete"}]}`,
			path: "$.input[0].status",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), conversionOptions{})
			if !errors.Is(err, core.ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.path)
			}
		})
	}
}

func TestResponsesOutputFunctionProvenanceFailsClosedOnGeminiRoute(t *testing.T) {
	converter := New(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	for _, test := range []struct {
		name  string
		field string
		path  string
	}{
		{name: "async", field: `"async":true`, path: "$.output[0].async"},
		{name: "caller", field: `"caller":{"type":"direct"}`, path: "$.output[0].caller"},
		{name: "namespace", field: `"namespace":"tools"`, path: "$.output[0].namespace"},
		{name: "created by", field: `"created_by":"server"`, path: "$.output[0].created_by"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := []byte(`{
				"id":"resp_1","object":"response","created_at":1,"model":"m","status":"completed",
				"output":[{
					"type":"function_call","id":"fc_1","call_id":"call_1","name":"f",
					"arguments":"{}","status":"completed",` + test.field + `
				}],
				"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
			}`)
			_, err := converter.ToClientResponse(context.Background(), body, conversionOptions{})
			if !errors.Is(err, core.ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.path)
			}
		})
	}
}

func TestFunctionOutputJSONSchemaMustBeObject(t *testing.T) {
	responsesConverter := New(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	_, err := responsesConverter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"m","input":"hi",
		"tools":[{"type":"function","name":"f","output_schema":[] }]
	}`), conversionOptions{})
	if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "$.tools[0].output_schema") {
		t.Fatalf("Responses error = %v, want ErrInvalidPayload at output_schema", err)
	}

	geminiConverter := New(routeSpec{From: ProtocolGenerateContent, To: ProtocolResponses})
	_, err = geminiConverter.ToUpstreamRequest(context.Background(), []byte(`{
		"contents":[{"role":"user","parts":[{"text":"hi"}]}],
		"tools":[{"functionDeclarations":[{"name":"f","responseJsonSchema":[]}]}]
	}`), conversionOptions{})
	if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), "$.tools[0].functionDeclarations[0].responseJsonSchema") {
		t.Fatalf("Gemini error = %v, want ErrInvalidPayload at responseJsonSchema", err)
	}
}

func TestResponsesFunctionAsyncFalseAndCompletedStatusArePortable(t *testing.T) {
	converter := New(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
	_, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"m",
		"input":[{"type":"function_call","call_id":"call_1","name":"f","arguments":"{}","async":false,"status":"completed"}],
		"tools":[{"type":"function","name":"f","async":false}]
	}`), conversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

func assertJSONEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("invalid converted JSON %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	gotCanonical, _ := json.Marshal(gotValue)
	wantCanonical, _ := json.Marshal(wantValue)
	if string(gotCanonical) != string(wantCanonical) {
		t.Fatalf("JSON mismatch\n got: %s\nwant: %s", gotCanonical, wantCanonical)
	}
}
