package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestValidatingSSEBodyPassesThroughResponsesFailureTerminals(t *testing.T) {
	tests := []struct {
		name     string
		event    string
		terminal string
	}{
		{
			name:     "context length exceeded",
			event:    "response.failed",
			terminal: `{"type":"response.failed","sequence_number":1,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.4","status":"failed","error":{"code":"context_length_exceeded","message":"input exceeds the context window"}}}`,
		},
		{
			name:     "insufficient quota",
			event:    "response.failed",
			terminal: `{"type":"response.failed","sequence_number":1,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.4","status":"failed","error":{"code":"insufficient_quota","message":"quota exceeded"}}}`,
		},
		{
			name:     "cancelled without output",
			event:    "response.cancelled",
			terminal: `{"type":"response.cancelled","sequence_number":1,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.4","status":"cancelled"}}`,
		},
	}

	const created = `{"type":"response.created","sequence_number":0,"response":{"id":"resp_1","object":"response","created_at":1,"model":"gpt-5.4","status":"in_progress","output":[]}}`
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := []byte("event: response.created\ndata: " + created + "\n\n" +
				"event: " + test.event + "\ndata: " + test.terminal + "\n\n")
			body := newValidatingSSEBody(
				context.Background(),
				io.NopCloser(bytes.NewReader(payload)),
				core.ProtocolResponses,
				codec.New(core.ProtocolResponses),
				int64(len(payload)),
			)
			t.Cleanup(func() { _ = body.Close() })

			got, err := io.ReadAll(body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("ReadAll() changed SSE bytes:\n got: %q\nwant: %q", got, payload)
			}

			buffer := make([]byte, 1)
			if n, err := body.Read(buffer); n != 0 || !errors.Is(err, io.EOF) {
				t.Fatalf("Read() after terminal = (%d, %v), want (0, EOF)", n, err)
			}
		})
	}
}
