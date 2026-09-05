package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestMalformedKnownToolChoicesAreInvalidAcrossTargets(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		from Protocol
		body string
		path string
	}{
		{"chat function missing name", ProtocolChat, `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{}}}`, "$.tool_choice.function.name"},
		{"responses function missing name", ProtocolResponses, `{"model":"m","input":"hi","tool_choice":{"type":"function"}}`, "$.tool_choice.name"},
		{"messages missing type", ProtocolMessages, `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"tool_choice":{}}`, "$.tool_choice.type"},
		{"gemini invalid mode", ProtocolGenerateContent, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"toolConfig":{"functionCallingConfig":{"mode":"TURBO"}}}`, "$.toolConfig.functionCallingConfig.mode"},
		{"chat invalid allowed mode", ProtocolChat, `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"none","tools":[{"type":"function","function":{"name":"f"}}]}}}`, "mode"},
		{"responses invalid allowed mode", ProtocolResponses, `{"model":"m","input":"hi","tool_choice":{"type":"allowed_tools","mode":"none","tools":[{"type":"function","name":"f"}]}}`, "mode"},
	}
	targets := map[Protocol][]Protocol{
		ProtocolChat:            {ProtocolMessages, ProtocolResponses, ProtocolGenerateContent},
		ProtocolResponses:       {ProtocolChat, ProtocolMessages, ProtocolGenerateContent},
		ProtocolMessages:        {ProtocolChat, ProtocolResponses, ProtocolGenerateContent},
		ProtocolGenerateContent: {ProtocolChat, ProtocolMessages, ProtocolResponses},
	}
	for _, test := range tests {
		for _, target := range targets[test.from] {
			t.Run(test.name+"/"+string(target), func(t *testing.T) {
				_, err := harness.ToUpstreamRequest(context.Background(), test.from, target, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
				if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), test.path) {
					t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.path)
				}
			})
		}
	}
}

func TestRequiredAndNamedToolChoicesRequireDeclarations(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		from Protocol
		body string
		path string
	}{
		{"chat required empty", ProtocolChat, `{"model":"m","messages":[{"role":"user","content":"hi"}],"tool_choice":"required"}`, "$.tool_choice"},
		{"responses required empty", ProtocolResponses, `{"model":"m","input":"hi","tool_choice":"required"}`, "$.tool_choice"},
		{"messages any empty", ProtocolMessages, `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"any"}}`, "$.tool_choice"},
		{"gemini any empty", ProtocolGenerateContent, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`, "$.toolConfig.functionCallingConfig.mode"},
		{"chat named undeclared", ProtocolChat, `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"other","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"missing"}}}`, "name"},
		{"responses named undeclared", ProtocolResponses, `{"model":"m","input":"hi","tool_choice":{"type":"function","name":"missing"}}`, "name"},
		{"messages named undeclared", ProtocolMessages, `{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"other","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"missing"}}`, "name"},
		{"gemini named undeclared", ProtocolGenerateContent, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"other","parametersJsonSchema":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["missing"]}}}`, "allowedFunctionNames[0]"},
	}
	targets := map[Protocol][]Protocol{
		ProtocolChat:            {ProtocolMessages, ProtocolResponses, ProtocolGenerateContent},
		ProtocolResponses:       {ProtocolChat, ProtocolMessages, ProtocolGenerateContent},
		ProtocolMessages:        {ProtocolChat, ProtocolResponses, ProtocolGenerateContent},
		ProtocolGenerateContent: {ProtocolChat, ProtocolMessages, ProtocolResponses},
	}
	for _, test := range tests {
		for _, target := range targets[test.from] {
			t.Run(test.name+"/"+string(target), func(t *testing.T) {
				_, err := harness.ToUpstreamRequest(context.Background(), test.from, target, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
				if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), test.path) {
					t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.path)
				}
			})
		}
	}
}

func TestRedundantAllowedFunctionSetsDegradeExactly(t *testing.T) {
	t.Run("chat auto all to Gemini", func(t *testing.T) {
		converter := newChatGeminiRoute(routeSpec{From: ProtocolChat, To: ProtocolGenerateContent})
		result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
			"model":"m","messages":[{"role":"user","content":"hi"}],
			"tools":[{"type":"function","function":{"name":"a","parameters":{"type":"object"}}},{"type":"function","function":{"name":"b","parameters":{"type":"object"}}}],
			"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"function","function":{"name":"a"}},{"type":"function","function":{"name":"b"}}]}}
		}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gemini"}})
		if err != nil {
			t.Fatal(err)
		}
		var got geminiRequest
		if err := json.Unmarshal(result.Body, &got); err != nil {
			t.Fatal(err)
		}
		if got.ToolConfig == nil || got.ToolConfig.FunctionCallingConfig.Mode != "AUTO" || len(got.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) != 0 {
			t.Fatalf("toolConfig = %#v", got.ToolConfig)
		}
	})

	t.Run("responses auto all to Gemini", func(t *testing.T) {
		converter := newResponsesGeminiRoute(routeSpec{From: ProtocolResponses, To: ProtocolGenerateContent})
		result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
			"model":"m","input":"hi",
			"tools":[{"type":"function","name":"a","parameters":{"type":"object"}},{"type":"function","name":"b","parameters":{"type":"object"}}],
			"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"a"},{"type":"function","name":"b"}]}
		}`), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "gemini"}})
		if err != nil {
			t.Fatal(err)
		}
		var got geminiRequest
		if err := json.Unmarshal(result.Body, &got); err != nil {
			t.Fatal(err)
		}
		if got.ToolConfig == nil || got.ToolConfig.FunctionCallingConfig.Mode != "AUTO" || len(got.ToolConfig.FunctionCallingConfig.AllowedFunctionNames) != 0 {
			t.Fatalf("toolConfig = %#v", got.ToolConfig)
		}
	})

	for _, test := range []struct {
		name string
		from Protocol
		body string
	}{
		{"chat required all", ProtocolChat, `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"a","parameters":{"type":"object"}}},{"type":"function","function":{"name":"b","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"a"}},{"type":"function","function":{"name":"b"}}]}}}`},
		{"responses required all", ProtocolResponses, `{"model":"m","input":"hi","tools":[{"type":"function","name":"a","parameters":{"type":"object"}},{"type":"function","name":"b","parameters":{"type":"object"}}],"tool_choice":{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"a"},{"type":"function","name":"b"}]}}`},
		{"gemini any all", ProtocolGenerateContent, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"a","parametersJsonSchema":{"type":"object"}},{"name":"b","parametersJsonSchema":{"type":"object"}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["a","b"]}}}`},
	} {
		t.Run(test.name+" to Messages", func(t *testing.T) {
			harness, _ := newTestRouterHarness()
			result, err := harness.ToUpstreamRequest(context.Background(), test.from, ProtocolMessages, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "claude"}})
			if err != nil {
				t.Fatal(err)
			}
			var request messagesRequest
			if err := json.Unmarshal(result.Result.Body, &request); err != nil {
				t.Fatal(err)
			}
			var choice struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(request.ToolChoice, &choice); err != nil || choice.Type != "any" {
				t.Fatalf("tool_choice = %s", request.ToolChoice)
			}
		})
	}
}

func TestRestrictedAutoSubsetsAndValidatedRemainUnsupported(t *testing.T) {
	harness, _ := newTestRouterHarness()
	tests := []struct {
		name string
		from Protocol
		to   Protocol
		body string
		path string
	}{
		{"chat auto subset", ProtocolChat, ProtocolGenerateContent, `{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"a","parameters":{"type":"object"}}},{"type":"function","function":{"name":"b","parameters":{"type":"object"}}}],"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"function","function":{"name":"a"}}]}}}`, "allowed_tools.tools"},
		{"responses auto subset", ProtocolResponses, ProtocolGenerateContent, `{"model":"m","input":"hi","tools":[{"type":"function","name":"a","parameters":{"type":"object"}},{"type":"function","name":"b","parameters":{"type":"object"}}],"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"a"}]}}`, "tool_choice.tools"},
		{"gemini auto subset to Chat", ProtocolGenerateContent, ProtocolChat, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"a"},{"name":"b"}]}],"toolConfig":{"functionCallingConfig":{"mode":"AUTO","allowedFunctionNames":["a"]}}}`, "allowedFunctionNames"},
		{"gemini auto subset to Messages", ProtocolGenerateContent, ProtocolMessages, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"a"},{"name":"b"}]}],"toolConfig":{"functionCallingConfig":{"mode":"AUTO","allowedFunctionNames":["a"]}}}`, "allowedFunctionNames"},
		{"gemini auto subset to Responses", ProtocolGenerateContent, ProtocolResponses, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"a"},{"name":"b"}]}],"toolConfig":{"functionCallingConfig":{"mode":"AUTO","allowedFunctionNames":["a"]}}}`, "allowedFunctionNames"},
		{"gemini validated to Chat", ProtocolGenerateContent, ProtocolChat, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"a"}]}],"toolConfig":{"functionCallingConfig":{"mode":"VALIDATED"}}}`, "mode"},
		{"gemini validated to Messages", ProtocolGenerateContent, ProtocolMessages, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"a"}]}],"toolConfig":{"functionCallingConfig":{"mode":"VALIDATED"}}}`, "mode"},
		{"gemini validated to Responses", ProtocolGenerateContent, ProtocolResponses, `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"tools":[{"functionDeclarations":[{"name":"a"}]}],"toolConfig":{"functionCallingConfig":{"mode":"VALIDATED"}}}`, "mode"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), test.from, test.to, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrUnsupported at %s", err, test.path)
			}
		})
	}
}

func TestProviderNativeToolChoicesRemainUnsupportedRatherThanInvalid(t *testing.T) {
	harness, _ := newTestRouterHarness()
	tests := []struct {
		name string
		from Protocol
		to   Protocol
		body string
	}{
		{
			"chat custom choice to Messages", ProtocolChat, ProtocolMessages,
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"custom","custom":{"name":"shell"}}],"tool_choice":{"type":"custom","custom":{"name":"shell"}}}`,
		},
		{
			"responses custom choice to Gemini", ProtocolResponses, ProtocolGenerateContent,
			`{"model":"m","input":"hi","tools":[{"type":"custom","name":"shell"}],"tool_choice":{"type":"custom","name":"shell"}}`,
		},
		{
			"responses tool search choice to Messages", ProtocolResponses, ProtocolMessages,
			`{"model":"m","input":"hi","tools":[{"type":"tool_search","execution":"server"}],"tool_choice":{"type":"tool_search"}}`,
		},
		{
			"messages server tool choice to Chat", ProtocolMessages, ProtocolChat,
			`{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search_20250305","name":"web_search","input_schema":{"type":"object"}}],"tool_choice":{"type":"tool","name":"web_search"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), test.from, test.to, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("error = %v, want ErrUnsupported", err)
			}
		})
	}
}

func TestDuplicatePortableToolDeclarationsAreInvalid(t *testing.T) {
	harness, _ := newTestRouterHarness()
	tests := []struct {
		name    string
		from    Protocol
		targets []Protocol
		body    string
		path    string
	}{
		{
			"chat duplicate functions", ProtocolChat,
			[]Protocol{ProtocolMessages, ProtocolResponses, ProtocolGenerateContent},
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"same","parameters":{"type":"object"}}},{"type":"function","function":{"name":"same","parameters":{"type":"object"}}}]}`,
			"$.tools[1].function.name",
		},
		{
			"chat duplicate custom tools", ProtocolChat,
			[]Protocol{ProtocolMessages, ProtocolResponses},
			`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"custom","custom":{"name":"same"}},{"type":"custom","custom":{"name":"same"}}]}`,
			"$.tools[1].custom.name",
		},
		{
			"messages duplicate functions", ProtocolMessages,
			[]Protocol{ProtocolChat, ProtocolResponses, ProtocolGenerateContent},
			`{"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"same","input_schema":{"type":"object"}},{"type":"custom","name":"same","input_schema":{"type":"object"}}]}`,
			"$.tools[1].name",
		},
	}
	for _, test := range tests {
		for _, target := range test.targets {
			t.Run(test.name+"/"+string(target), func(t *testing.T) {
				_, err := harness.ToUpstreamRequest(context.Background(), test.from, target, []byte(test.body), conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
				if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), test.path) {
					t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.path)
				}
			})
		}
	}
}

func TestMessagesNativeToolNamesAreNotTreatedAsPortableDuplicates(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{
		"model":"m","max_tokens":32,"messages":[{"role":"user","content":"hi"}],
		"tools":[
			{"name":"same","input_schema":{"type":"object"}},
			{"type":"web_search_20250305","name":"same","input_schema":{"type":"object"}}
		]
	}`)
	for _, target := range []Protocol{ProtocolChat, ProtocolResponses, ProtocolGenerateContent} {
		t.Run(string(target), func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), ProtocolMessages, target, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("error = %v, want native-tool ErrUnsupported", err)
			}
		})
	}
}

func TestChatToolDeclarationMissingTypeIsInvalid(t *testing.T) {
	harness, _ := newTestRouterHarness()
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tools":[{"function":{"name":"f","parameters":{"type":"object"}}}]}`)
	for _, target := range []Protocol{ProtocolMessages, ProtocolResponses, ProtocolGenerateContent} {
		t.Run(string(target), func(t *testing.T) {
			_, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, target, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
			if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "$.tools[0].type") {
				t.Fatalf("error = %v, want ErrInvalidPayload at $.tools[0].type", err)
			}
		})
	}
}

func TestChatCustomChoiceHybridIsInvalidForMessagesAndGemini(t *testing.T) {
	harness, _ := newTestRouterHarness()
	choices := []string{
		`{"type":"custom","custom":{"name":"shell"},"function":{"name":"f"}}`,
		`{"type":"custom","custom":{"name":"shell"},"allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"f"}}]}}`,
	}
	for choiceIndex, choice := range choices {
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"tool_choice":` + choice + `}`)
		for _, target := range []Protocol{ProtocolMessages, ProtocolGenerateContent} {
			t.Run(fmt.Sprintf("choice_%d/%s", choiceIndex, target), func(t *testing.T) {
				_, err := harness.ToUpstreamRequest(context.Background(), ProtocolChat, target, body, conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}})
				if !errors.Is(err, ErrInvalidPayload) || !strings.Contains(err.Error(), "$.tool_choice") {
					t.Fatalf("error = %v, want ErrInvalidPayload at $.tool_choice", err)
				}
			})
		}
	}
}
