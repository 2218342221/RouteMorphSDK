package chatresponses

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestAllowedToolsToolChoiceChatToResponses(t *testing.T) {
	converter := New(core.RouteSpec{From: ProtocolChat, To: ProtocolResponses})
	result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"m",
		"messages":[{"role":"user","content":"hello"}],
		"tools":[
			{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},
			{"type":"custom","custom":{"name":"shell","format":{"type":"text"}}}
		],
		"tool_choice":{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[
			{"type":"function","function":{"name":"lookup"}},
			{"type":"custom","custom":{"name":"shell"}}
		]}}
	}`), core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		ToolChoice struct {
			Type  string `json:"type"`
			Mode  string `json:"mode"`
			Tools []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"tool_choice"`
	}
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.ToolChoice.Type != "allowed_tools" || body.ToolChoice.Mode != "required" || len(body.ToolChoice.Tools) != 2 {
		t.Fatalf("tool_choice = %#v; body=%s", body.ToolChoice, result.Body)
	}
	if body.ToolChoice.Tools[0].Type != "function" || body.ToolChoice.Tools[0].Name != "lookup" ||
		body.ToolChoice.Tools[1].Type != "custom" || body.ToolChoice.Tools[1].Name != "shell" {
		t.Fatalf("allowed tools = %#v; body=%s", body.ToolChoice.Tools, result.Body)
	}
}

func TestAllowedToolsToolChoiceResponsesToChat(t *testing.T) {
	converter := New(core.RouteSpec{From: ProtocolResponses, To: ProtocolChat})
	result, err := converter.ToUpstreamRequest(context.Background(), []byte(`{
		"model":"m",
		"input":"hello",
		"tools":[
			{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false},
			{"type":"custom","name":"shell","format":{"type":"text"}}
		],
		"tool_choice":{"type":"allowed_tools","mode":"auto","tools":[
			{"type":"function","name":"lookup"},
			{"type":"custom","name":"shell"}
		]}
	}`), core.ConversionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		ToolChoice struct {
			Type         string `json:"type"`
			AllowedTools struct {
				Mode  string `json:"mode"`
				Tools []struct {
					Type     string `json:"type"`
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
					Custom struct {
						Name string `json:"name"`
					} `json:"custom"`
				} `json:"tools"`
			} `json:"allowed_tools"`
		} `json:"tool_choice"`
	}
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatal(err)
	}
	choice := body.ToolChoice
	if choice.Type != "allowed_tools" || choice.AllowedTools.Mode != "auto" || len(choice.AllowedTools.Tools) != 2 {
		t.Fatalf("tool_choice = %#v; body=%s", choice, result.Body)
	}
	if choice.AllowedTools.Tools[0].Type != "function" || choice.AllowedTools.Tools[0].Function.Name != "lookup" ||
		choice.AllowedTools.Tools[1].Type != "custom" || choice.AllowedTools.Tools[1].Custom.Name != "shell" {
		t.Fatalf("allowed tools = %#v; body=%s", choice.AllowedTools.Tools, result.Body)
	}
}

func TestAllowedToolsToolChoiceValidation(t *testing.T) {
	chatPrefix := `{"model":"m","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":`
	responsesPrefix := `{"model":"m","input":"hello","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"},"strict":false}],"tool_choice":`
	tests := []struct {
		name string
		from Protocol
		to   Protocol
		body string
		kind error
		path string
	}{
		{
			name: "chat invalid mode", from: ProtocolChat, to: ProtocolResponses,
			body: chatPrefix + `{"type":"allowed_tools","allowed_tools":{"mode":"none","tools":[{"type":"function","function":{"name":"lookup"}}]}}}`,
			kind: core.ErrInvalidPayload, path: "$.tool_choice.allowed_tools.mode",
		},
		{
			name: "chat empty list", from: ProtocolChat, to: ProtocolResponses,
			body: chatPrefix + `{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[]}}}`,
			kind: core.ErrInvalidPayload, path: "$.tool_choice.allowed_tools.tools",
		},
		{
			name: "chat duplicate", from: ProtocolChat, to: ProtocolResponses,
			body: chatPrefix + `{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"function","function":{"name":"lookup"}},{"type":"function","function":{"name":"lookup"}}]}}}`,
			kind: core.ErrInvalidPayload, path: "$.tool_choice.allowed_tools.tools[1].function.name",
		},
		{
			name: "chat undeclared", from: ProtocolChat, to: ProtocolResponses,
			body: chatPrefix + `{"type":"allowed_tools","allowed_tools":{"mode":"required","tools":[{"type":"function","function":{"name":"missing"}}]}}}`,
			kind: core.ErrInvalidPayload, path: "$.tool_choice.allowed_tools.tools[0].function.name",
		},
		{
			name: "chat hosted tool", from: ProtocolChat, to: ProtocolResponses,
			body: chatPrefix + `{"type":"allowed_tools","allowed_tools":{"mode":"auto","tools":[{"type":"mcp","server_label":"docs"}]}}}`,
			kind: core.ErrUnsupported, path: "$.tool_choice.allowed_tools.tools[0].type",
		},
		{
			name: "responses invalid mode", from: ProtocolResponses, to: ProtocolChat,
			body: responsesPrefix + `{"type":"allowed_tools","mode":"none","tools":[{"type":"function","name":"lookup"}]}}`,
			kind: core.ErrInvalidPayload, path: "$.tool_choice.mode",
		},
		{
			name: "responses empty list", from: ProtocolResponses, to: ProtocolChat,
			body: responsesPrefix + `{"type":"allowed_tools","mode":"auto","tools":[]}}`,
			kind: core.ErrInvalidPayload, path: "$.tool_choice.tools",
		},
		{
			name: "responses duplicate", from: ProtocolResponses, to: ProtocolChat,
			body: responsesPrefix + `{"type":"allowed_tools","mode":"auto","tools":[{"type":"function","name":"lookup"},{"type":"function","name":"lookup"}]}}`,
			kind: core.ErrInvalidPayload, path: "$.tool_choice.tools[1].name",
		},
		{
			name: "responses undeclared", from: ProtocolResponses, to: ProtocolChat,
			body: responsesPrefix + `{"type":"allowed_tools","mode":"required","tools":[{"type":"function","name":"missing"}]}}`,
			kind: core.ErrInvalidPayload, path: "$.tool_choice.tools[0].name",
		},
		{
			name: "responses hosted tool", from: ProtocolResponses, to: ProtocolChat,
			body: responsesPrefix + `{"type":"allowed_tools","mode":"auto","tools":[{"type":"mcp","server_label":"docs"}]}}`,
			kind: core.ErrUnsupported, path: "$.tool_choice.tools[0].type",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converter := New(core.RouteSpec{From: test.from, To: test.to})
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), core.ConversionOptions{})
			if !errors.Is(err, test.kind) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want %v at %s", err, test.kind, test.path)
			}
		})
	}
}

func TestNamedToolChoiceMustReferenceMatchingDeclaration(t *testing.T) {
	tests := []struct {
		name string
		from Protocol
		to   Protocol
		body string
		path string
	}{
		{
			name: "chat missing function", from: ProtocolChat, to: ProtocolResponses,
			body: `{"model":"m","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"missing"}}}`,
			path: "$.tool_choice.function.name",
		},
		{
			name: "chat kind mismatch", from: ProtocolChat, to: ProtocolResponses,
			body: `{"model":"m","messages":[{"role":"user","content":"hello"}],"tools":[{"type":"custom","custom":{"name":"lookup"}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`,
			path: "$.tool_choice.function.name",
		},
		{
			name: "responses missing custom", from: ProtocolResponses, to: ProtocolChat,
			body: `{"model":"m","input":"hello","tools":[{"type":"custom","name":"shell"}],"tool_choice":{"type":"custom","name":"missing"}}`,
			path: "$.tool_choice.name",
		},
		{
			name: "responses kind mismatch", from: ProtocolResponses, to: ProtocolChat,
			body: `{"model":"m","input":"hello","tools":[{"type":"custom","name":"lookup"}],"tool_choice":{"type":"function","name":"lookup"}}`,
			path: "$.tool_choice.name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			converter := New(core.RouteSpec{From: test.from, To: test.to})
			_, err := converter.ToUpstreamRequest(context.Background(), []byte(test.body), core.ConversionOptions{})
			if !errors.Is(err, core.ErrInvalidPayload) || !strings.Contains(err.Error(), test.path) {
				t.Fatalf("error = %v, want ErrInvalidPayload at %s", err, test.path)
			}
		})
	}
}
