package routemorph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCodingAgentCompatibilityOptionThreadsThroughPublicAdapter(t *testing.T) {
	tests := []struct {
		name        string
		protocol    Protocol
		body        string
		diagnostics []string
	}{
		{
			name:     "claude_code",
			protocol: ProtocolMessages,
			body: `{
				"model":"claude-sonnet-4-5","max_tokens":32000,"stream":false,
				"thinking":{"type":"enabled","budget_tokens":31999,"display":"omitted"},
				"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},
				"metadata":{"user_id":"` + strings.Repeat("u", 150) + `"},
				"system":[{"type":"text","text":"system","cache_control":{"type":"ephemeral"}}],
				"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}]
			}`,
			diagnostics: []string{"thinking_policy_approximated", "thinking_display_not_representable", "cache_control_not_representable", "metadata_user_id_hashed"},
		},
		{
			name:     "gemini_cli",
			protocol: ProtocolGenerateContent,
			body: `{
				"contents":[{"role":"user","parts":[{"text":"hello"}]}],
				"generationConfig":{"temperature":1,"topP":0.95,"topK":64,"thinkingConfig":{"includeThoughts":true,"thinkingBudget":8192}}
			}`,
			diagnostics: []string{"gemini_top_k_ignored", "gemini_include_thoughts_not_preserved", "gemini_thinking_budget_not_preserved"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode upstream request: %v", err)
				}
				if body["model"] != "gpt-5.4" {
					t.Errorf("upstream model = %#v", body["model"])
				}
				encoded, _ := json.Marshal(body)
				for _, forbidden := range []string{"context_management", "cache_control", "topK", "includeThoughts", "thinkingBudget"} {
					if strings.Contains(string(encoded), forbidden) {
						t.Errorf("client-only field %q leaked upstream: %s", forbidden, encoded)
					}
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, strings.ReplaceAll(adapterResponseFixtures[ProtocolResponses], "client-model", "gpt-5.4"))
			}))
			defer upstream.Close()

			strict, err := NewOpenAIResponsesAdapter(upstream.URL, "", WithModel("gpt-5.4"))
			if err != nil {
				t.Fatal(err)
			}
			requestURL := (*url.URL)(nil)
			if test.protocol == ProtocolGenerateContent {
				requestURL = &url.URL{Path: "/v1beta/models/gpt-5.4:generateContent"}
			}
			if _, err := invokeAdapter(context.Background(), strict, test.protocol, &Request{URL: requestURL, Body: strings.NewReader(test.body)}); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("strict error = %v, want ErrUnsupported", err)
			}

			compatible, err := NewOpenAIResponsesAdapter(upstream.URL, "", WithModel("gpt-5.4"), WithCodingAgentCompatibility())
			if err != nil {
				t.Fatal(err)
			}
			response, err := invokeAdapter(context.Background(), compatible, test.protocol, &Request{URL: requestURL, Body: strings.NewReader(test.body)})
			if err != nil {
				t.Fatal(err)
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			codes := make(map[string]bool)
			for _, diagnostic := range response.Meta.Diagnostics() {
				codes[diagnostic.Code] = true
			}
			for _, code := range test.diagnostics {
				if !codes[code] {
					t.Fatalf("missing diagnostic %q in %#v", code, response.Meta.Diagnostics())
				}
			}
		})
	}
}

func TestCodingAgentCompatibilityDoesNotEnableUnrelatedDocumentedLoss(t *testing.T) {
	responseBody := `{
		"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.4","status":"completed",
		"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"hello","annotations":[]}]}],
		"prompt_cache_options":{"retention":"24h"},
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer upstream.Close()

	adapter, err := NewOpenAIResponsesAdapter(
		upstream.URL,
		"",
		WithModel("gpt-5.4"),
		WithCodingAgentCompatibility(),
	)
	if err != nil {
		t.Fatal(err)
	}
	response, invokeErr := adapter.OpenAIChatCompletions(context.Background(), &Request{
		Body: strings.NewReader(adapterRequestFixtures[ProtocolChat]),
	})
	if invokeErr == nil && response != nil {
		_, invokeErr = io.ReadAll(response.Body)
		_ = response.Body.Close()
	}
	if !errors.Is(invokeErr, ErrUnsupported) {
		t.Fatalf("coding-agent profile unexpectedly enabled unrelated Responses-to-Chat loss: %v", invokeErr)
	}
}
