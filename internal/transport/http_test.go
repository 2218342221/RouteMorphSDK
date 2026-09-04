package transport

import (
	"net/http"
	"net/url"
	"testing"
)

func TestBuildRequestHeadersDropsCredentialsAndContentCodings(t *testing.T) {
	source := http.Header{
		"Authorization":    {"Bearer client"},
		"Cookie":           {"session=client"},
		"Content-Encoding": {"gzip"},
		"Accept-Encoding":  {"gzip"},
		"X-Trace":          {"keep"},
	}
	got := BuildRequestHeaders(source, "responses", "provider-key", true)
	if got.Get("Authorization") != "Bearer provider-key" {
		t.Fatalf("Authorization = %q", got.Get("Authorization"))
	}
	for _, name := range []string{"Cookie", "Content-Encoding", "Accept-Encoding"} {
		if got.Get(name) != "" {
			t.Fatalf("%s unexpectedly forwarded", name)
		}
	}
	if got.Get("X-Trace") != "keep" || got.Get("Accept") != "text/event-stream" {
		t.Fatalf("headers = %#v", got)
	}
}

func TestBuildRequestHeadersScopesProviderControls(t *testing.T) {
	source := http.Header{
		"OpenAI-Organization":   {"org_123"},
		"OpenAI-Project":        {"proj_123"},
		"OpenAI-Beta":           {"responses=v1"},
		"Anthropic-Version":     {"2024-01-01"},
		"Anthropic-Beta":        {"feature"},
		"X-Goog-User-Project":   {"billing-project"},
		"X-Goog-Api-Client":     {"spoofed"},
		"Anthropic-Unsupported": {"drop"},
	}

	tests := []struct {
		protocol string
		kept     []string
	}{
		{"responses", []string{"OpenAI-Organization", "OpenAI-Project", "OpenAI-Beta"}},
		{"messages", []string{"Anthropic-Version", "Anthropic-Beta"}},
		{"generateContent", []string{"X-Goog-User-Project"}},
	}
	for _, test := range tests {
		t.Run(test.protocol, func(t *testing.T) {
			got := BuildRequestHeaders(source, test.protocol, "", false)
			want := make(map[string]bool, len(test.kept))
			for _, name := range test.kept {
				want[http.CanonicalHeaderKey(name)] = true
			}
			for name := range source {
				canonical := http.CanonicalHeaderKey(name)
				if got.Get(name) != "" && !want[canonical] {
					t.Fatalf("%s unexpectedly forwarded to %s", name, test.protocol)
				}
				if want[canonical] && got.Get(name) == "" {
					t.Fatalf("%s was not forwarded to %s", name, test.protocol)
				}
			}
		})
	}
}

func TestBuildEndpointEscapesGeminiModelAndDropsQuery(t *testing.T) {
	base, err := url.Parse("https://example.test/prefix")
	if err != nil {
		t.Fatal(err)
	}
	got := BuildEndpoint(base, "generateContent", "models/team alpha", true)
	want := "https://example.test/prefix/v1beta/models/models%2Fteam%20alpha:streamGenerateContent?alt=sse"
	if got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestCopyResponseTrailersAppliesTrustAndRepresentationPolicy(t *testing.T) {
	source := http.Header{
		"Digest":                    {"sha-256=stale"},
		"ETag":                      {`"stale"`},
		"X-Upstream-Final":          {"done"},
		"X-RouteMorph-Conversion":   {"spoofed"},
		"X-RouteMorph-Stream-Mode":  {"spoofed"},
		"X-RouteMorph-Diagnostics":  {"999"},
		"X-Announced-Without-Value": nil,
	}

	converted := CopyResponseTrailers(source, true)
	for _, name := range []string{"Digest", "ETag", "X-RouteMorph-Conversion", "X-RouteMorph-Stream-Mode", "X-RouteMorph-Diagnostics"} {
		if _, ok := converted[http.CanonicalHeaderKey(name)]; ok {
			t.Fatalf("converted trailer retained %s: %#v", name, converted)
		}
	}
	if converted.Get("X-Upstream-Final") != "done" {
		t.Fatalf("safe trailer missing: %#v", converted)
	}
	if values, ok := converted["X-Announced-Without-Value"]; !ok || values != nil {
		t.Fatalf("announced trailer was not preserved: %#v", converted)
	}

	native := CopyResponseTrailers(source, false)
	if native.Get("Digest") != "sha-256=stale" || native.Get("ETag") != `"stale"` {
		t.Fatalf("native representation trailers were not preserved: %#v", native)
	}
	for _, name := range []string{"X-RouteMorph-Conversion", "X-RouteMorph-Stream-Mode", "X-RouteMorph-Diagnostics"} {
		if _, ok := native[http.CanonicalHeaderKey(name)]; ok {
			t.Fatalf("native trailer retained reserved metadata %s: %#v", name, native)
		}
	}
}
