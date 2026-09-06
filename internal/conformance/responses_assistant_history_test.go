package conformance

import (
	"context"
	"encoding/json"
	"testing"
)

func TestResponsesIngressUsesOutputTextForAssistantHistory(t *testing.T) {
	harness, err := newTestRouterHarness()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		from Protocol
		body string
	}{
		{
			name: "chat",
			from: ProtocolChat,
			body: `{"model":"client","messages":[{"role":"user","content":"first"},{"role":"assistant","content":"ack"},{"role":"user","content":"last"}]}`,
		},
		{
			name: "messages",
			from: ProtocolMessages,
			body: `{"model":"client","max_tokens":64,"messages":[{"role":"user","content":"first"},{"role":"assistant","content":"ack"},{"role":"user","content":"last"}]}`,
		},
		{
			name: "generate_content",
			from: ProtocolGenerateContent,
			body: `{"contents":[{"role":"user","parts":[{"text":"first"}]},{"role":"model","parts":[{"text":"ack"}]},{"role":"user","parts":[{"text":"last"}]}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			execution, err := harness.ToUpstreamRequest(
				context.Background(),
				test.from,
				ProtocolResponses,
				[]byte(test.body),
				conversionOptions{Exchange: exchangeMetadata{UpstreamModel: "provider"}},
			)
			if err != nil {
				t.Fatalf("ToUpstreamRequest() error = %v", err)
			}

			var request struct {
				Input []struct {
					Role    string `json:"role"`
					Content []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"content"`
				} `json:"input"`
			}
			if err := json.Unmarshal(execution.Result.Body, &request); err != nil {
				t.Fatalf("decode Responses request: %v\nbody=%s", err, execution.Result.Body)
			}
			if len(request.Input) != 3 {
				t.Fatalf("input item count = %d, want 3\nbody=%s", len(request.Input), execution.Result.Body)
			}

			wantRoles := []string{"user", "assistant", "user"}
			wantTypes := []string{"input_text", "output_text", "input_text"}
			wantTexts := []string{"first", "ack", "last"}
			for i := range request.Input {
				if request.Input[i].Role != wantRoles[i] {
					t.Errorf("input[%d].role = %q, want %q", i, request.Input[i].Role, wantRoles[i])
				}
				if len(request.Input[i].Content) != 1 {
					t.Fatalf("input[%d].content count = %d, want 1\nbody=%s", i, len(request.Input[i].Content), execution.Result.Body)
				}
				part := request.Input[i].Content[0]
				if part.Type != wantTypes[i] || part.Text != wantTexts[i] {
					t.Errorf("input[%d].content[0] = {type:%q text:%q}, want {type:%q text:%q}", i, part.Type, part.Text, wantTypes[i], wantTexts[i])
				}
			}
		})
	}
}
