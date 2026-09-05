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
	media, err := ParseDataURL("data:text/plain;base64,aGk=")
	if err != nil {
		t.Fatalf("ParseDataURL() error = %v", err)
	}
	if media.MIMEType != "text/plain" || media.Data != "aGk=" || DataURL(media) != "data:text/plain;base64,aGk=" {
		t.Fatalf("media = %#v", media)
	}
	if ValuePresent(json.RawMessage(` {}`)) || NonNullValue(json.RawMessage(` null `)) {
		t.Fatal("empty JSON values unexpectedly reported as present")
	}
}

func TestParseDataURLRejectsMalformedInput(t *testing.T) {
	for _, value := range []string{
		"data:image/png;base64",
		"data:image/png,not-base64",
		"data:image/png;base64,%%%",
	} {
		if _, err := ParseDataURL(value); err == nil {
			t.Fatalf("ParseDataURL(%q) unexpectedly succeeded", value)
		}
	}
}

func TestParseFileDataCarriesMediaType(t *testing.T) {
	media, err := ParseFileData("JVBERg", "brief.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if media.Data != "JVBERg" || media.MIMEType != "application/pdf" || media.Filename != "brief.pdf" {
		t.Fatalf("media = %#v", media)
	}
	media, err = ParseFileData("data:text/plain;base64,aGk=", "note.txt")
	if err != nil {
		t.Fatal(err)
	}
	if media.Data != "aGk=" || media.MIMEType != "text/plain" || media.Filename != "note.txt" {
		t.Fatalf("data URL media = %#v", media)
	}
}
