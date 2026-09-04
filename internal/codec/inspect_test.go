package codec

import (
	"context"
	"errors"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestInspectRelayRequestReturnsChatStreamOptions(t *testing.T) {
	info, includeUsage, includeUsageSet, err := InspectRelayRequest(
		context.Background(),
		core.ProtocolChat,
		nil,
		[]byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"stream":true,"stream_options":{"include_usage":true}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "client-model" || !info.Stream || !includeUsage || !includeUsageSet {
		t.Fatalf("info=%#v include_usage=%v set=%v", info, includeUsage, includeUsageSet)
	}
}

func TestInspectRelayRequestRejectsUnsupportedChatStreamOption(t *testing.T) {
	_, _, _, err := InspectRelayRequest(
		context.Background(),
		core.ProtocolChat,
		nil,
		[]byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"stream_options":{"include_obfuscation":true}}`),
	)
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("error=%v, want ErrUnsupported", err)
	}
}
