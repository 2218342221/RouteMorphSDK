package routekit

import (
	"encoding/json"
	"errors"
	"testing"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestRejectUnknownTopLevelReportsCanonicalPath(t *testing.T) {
	err := RejectUnknownTopLevel(core.ProtocolChat, []byte(`{"model":"x","future":true}`), "model")
	if !errors.Is(err, core.ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported", err)
	}
	var conversion *core.ConversionError
	if !errors.As(err, &conversion) || conversion.Path != "$.future" {
		t.Fatalf("conversion error = %#v", conversion)
	}
}

func TestMediaAndPresenceHelpers(t *testing.T) {
	media := ParseDataURL("data:text/plain;base64,aGk=")
	if media.MIMEType != "text/plain" || media.Data != "aGk=" || DataURL(media) != "data:text/plain;base64,aGk=" {
		t.Fatalf("media = %#v", media)
	}
	if ValuePresent(json.RawMessage(` {}`)) || NonNullValue(json.RawMessage(` null `)) {
		t.Fatal("empty JSON values unexpectedly reported as present")
	}
}
