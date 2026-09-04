package codec

import (
	"errors"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestPatchStreamResponseModelRejectsNullObjects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		protocol core.Protocol
		body     string
	}{
		{"responses", core.ProtocolResponses, `{"type":"response.in_progress","response":null}`},
		{"messages", core.ProtocolMessages, `{"type":"ping","message":null}`},
		{"root", core.ProtocolGenerateContent, `null`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := PatchStreamResponseModel(test.protocol, []byte(test.body), "client-model"); !errors.Is(err, core.ErrInvalidPayload) {
				t.Fatalf("error=%v, want ErrInvalidPayload", err)
			}
		})
	}
}
