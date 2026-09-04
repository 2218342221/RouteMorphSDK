package routemorph

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
	streamx "github.com/2218342221/RouteMorphSDK/internal/stream"
	transportx "github.com/2218342221/RouteMorphSDK/internal/transport"
)

//go:embed testdata/relay/*.json
var relayFixtures embed.FS

func mustRelayFixture(name string) string {
	data, err := relayFixtures.ReadFile("testdata/relay/" + name)
	if err != nil {
		panic(err)
	}
	return strings.TrimSpace(string(data))
}

var adapterRequestFixtures = map[Protocol]string{
	ProtocolChat:            mustRelayFixture("chat_request.json"),
	ProtocolResponses:       mustRelayFixture("responses_request.json"),
	ProtocolMessages:        mustRelayFixture("messages_request.json"),
	ProtocolGenerateContent: mustRelayFixture("generateContent_request.json"),
}

var adapterResponseFixtures = map[Protocol]string{
	ProtocolChat:            mustRelayFixture("chat_response.json"),
	ProtocolResponses:       mustRelayFixture("responses_response.json"),
	ProtocolMessages:        mustRelayFixture("messages_response.json"),
	ProtocolGenerateContent: mustRelayFixture("generateContent_response.json"),
}

func TestGatewayAdapterNonStreamingMatrix(t *testing.T) {
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, upstreamProtocol := range protocols {
		upstreamProtocol := upstreamProtocol
		for _, ingressProtocol := range protocols {
			ingressProtocol := ingressProtocol
			t.Run(string(ingressProtocol)+"_to_"+string(upstreamProtocol), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					assertAdapterUpstreamRequest(t, request, upstreamProtocol, false, "client-model")
					writer.Header().Set("Content-Type", "application/json")
					writer.Header().Set("X-Upstream-Test", "kept")
					_, _ = io.WriteString(writer, adapterResponseFixtures[upstreamProtocol])
				}))
				defer upstream.Close()
				adapter := mustNewAdapter(t, upstreamProtocol, upstream.URL, "secret")
				response, err := invokeAdapter(context.Background(), adapter, ingressProtocol, adapterRequest(ingressProtocol, false))
				if err != nil {
					t.Fatalf("invoke adapter: %v", err)
				}
				defer response.Body.Close()
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatalf("read response: %v", err)
				}
				if response.StatusCode != http.StatusOK || response.Meta.Stream {
					t.Fatalf("response status=%d stream=%v", response.StatusCode, response.Meta.Stream)
				}
				if response.Meta.IngressProtocol != ingressProtocol || response.Meta.UpstreamProtocol != upstreamProtocol {
					t.Fatalf("response meta = %#v", response.Meta)
				}
				wantMode := expectedRouteMode(ingressProtocol, upstreamProtocol)
				if response.Meta.RouteMode != wantMode || response.Header.Get("X-RouteMorph-Stream-Mode") != string(wantMode) {
					t.Fatalf("route mode meta=%q header=%q, want %q", response.Meta.RouteMode, response.Header.Get("X-RouteMorph-Stream-Mode"), wantMode)
				}
				if response.Header.Get("X-Upstream-Test") != "kept" {
					t.Fatal("safe upstream response header was not preserved")
				}
				wireAdapter := codec.New(core.Protocol(ingressProtocol))
				if err := wireAdapter.ValidateResponse(context.Background(), body); err != nil {
					t.Fatalf("invalid %s response %s: %v", ingressProtocol, body, err)
				}
			})
		}
	}
}

func TestGatewayAdapterStreamingMatrix(t *testing.T) {
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, upstreamProtocol := range protocols {
		upstreamProtocol := upstreamProtocol
		for _, ingressProtocol := range protocols {
			ingressProtocol := ingressProtocol
			t.Run(string(ingressProtocol)+"_to_"+string(upstreamProtocol), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					assertAdapterUpstreamRequest(t, request, upstreamProtocol, true, "client-model")
					writer.Header().Set("Content-Type", "text/event-stream")
					writeProtocolStream(t, writer, upstreamProtocol)
				}))
				defer upstream.Close()
				adapter := mustNewAdapter(t, upstreamProtocol, upstream.URL, "secret")
				response, err := invokeAdapter(context.Background(), adapter, ingressProtocol, adapterRequest(ingressProtocol, true))
				if err != nil {
					t.Fatalf("invoke adapter: %v", err)
				}
				body, err := io.ReadAll(response.Body)
				if err != nil {
					t.Fatalf("read stream: %v body=%s", err, body)
				}
				_ = response.Body.Close()
				if !response.Meta.Stream {
					t.Fatal("response was not marked as streaming")
				}
				wantMode := expectedRouteMode(ingressProtocol, upstreamProtocol)
				if response.Meta.RouteMode != wantMode || response.Header.Get("X-RouteMorph-Stream-Mode") != string(wantMode) {
					t.Fatalf("route mode meta=%q header=%q, want %q", response.Meta.RouteMode, response.Header.Get("X-RouteMorph-Stream-Mode"), wantMode)
				}
				terminal := map[Protocol]string{
					ProtocolChat: "[DONE]", ProtocolResponses: "response.completed",
					ProtocolMessages: "message_stop", ProtocolGenerateContent: "hello",
				}[ingressProtocol]
				if !bytes.Contains(body, []byte(terminal)) {
					t.Fatalf("stream terminal %q missing from %s", terminal, body)
				}
			})
		}
	}
}

func expectedRouteMode(from, to Protocol) RouteMode {
	if from == to {
		return RouteModeNative
	}
	switch string(from) + "->" + string(to) {
	case "chat->responses", "responses->chat", "messages->responses",
		"responses->generateContent", "generateContent->responses":
		return RouteModeIncremental
	default:
		return RouteModeBuffered
	}
}

func TestGatewayAdapterCarriesChatIncludeUsageAcrossResponsesConversion(t *testing.T) {
	for _, includeUsage := range []bool{false, true} {
		t.Run(fmt.Sprintf("include_usage_%v", includeUsage), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				assertAdapterUpstreamRequest(t, request, ProtocolResponses, true, "client-model")
				writer.Header().Set("Content-Type", "text/event-stream")
				writeProtocolStream(t, writer, ProtocolResponses)
			}))
			defer upstream.Close()

			adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "secret")
			body := fmt.Sprintf(`{"model":"client-model","messages":[{"role":"user","content":"hello"}],"stream":true,"stream_options":{"include_usage":%v}}`, includeUsage)
			response, err := adapter.OpenAIChatCompletions(context.Background(), &Request{Header: make(http.Header), Body: strings.NewReader(body)})
			if err != nil {
				t.Fatal(err)
			}
			wire, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			usageOnly := bytes.Count(wire, []byte(`"choices":[]`))
			usageFields := bytes.Count(wire, []byte(`"usage":`))
			if includeUsage {
				if usageOnly != 1 || usageFields < 2 || !bytes.Contains(wire, []byte(`"usage":null`)) || !bytes.Contains(wire, []byte(`"total_tokens":2`)) {
					t.Fatalf("invalid include_usage stream: %s", wire)
				}
			} else if usageOnly != 0 || usageFields != 0 {
				t.Fatalf("unexpected usage stream: %s", wire)
			}
		})
	}
}

func TestGatewayAdapterWithModelAndHeaderPolicy(t *testing.T) {
	inputHeader := http.Header{
		"Authorization":   {"Bearer attacker"},
		"Cookie":          {"session=secret"},
		"Connection":      {"X-Remove-Me"},
		"X-Remove-Me":     {"secret"},
		"X-Request-ID":    {"request-1"},
		"X-Custom-Header": {"kept"},
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer real-key" || request.Header.Get("Cookie") != "" || request.Header.Get("X-Remove-Me") != "" {
			t.Errorf("unsafe upstream headers: %#v", request.Header)
		}
		if request.Header.Get("X-Custom-Header") != "kept" || request.Header.Get("X-Request-ID") != "request-1" {
			t.Errorf("safe headers not preserved: %#v", request.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream body: %v", err)
			return
		}
		if body["model"] != "upstream-model" {
			t.Errorf("upstream model = %#v", body["model"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, strings.ReplaceAll(adapterResponseFixtures[ProtocolChat], "client-model", "upstream-model"))
	}))
	defer upstream.Close()
	adapter, err := NewOpenAIChatCompletionsAdapter(upstream.URL, "real-key", WithModel("upstream-model"))
	if err != nil {
		t.Fatal(err)
	}
	request := adapterRequest(ProtocolChat, false)
	request.Header = inputHeader
	response, err := adapter.OpenAIChatCompletions(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || !strings.Contains(string(body), `"model":"client-model"`) {
		t.Fatalf("response body=%s err=%v", body, readErr)
	}
	if inputHeader.Get("Authorization") != "Bearer attacker" || inputHeader.Get("Cookie") == "" {
		t.Fatalf("input headers were mutated: %#v", inputHeader)
	}
}

func TestGatewayAdapterGeminiURLAndEndpoint(t *testing.T) {
	original, err := url.Parse("/v1beta/models/publishers%2Facme%20models%2Fgemini:generateContent?client_only=true")
	if err != nil {
		t.Fatal(err)
	}
	originalString := original.String()
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" || request.URL.RawQuery != "" {
			t.Errorf("upstream URL = %s", request.URL.String())
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream body: %v", err)
			return
		}
		if body["model"] != "publishers/acme models/gemini" {
			t.Errorf("converted model = %#v", body["model"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, adapterResponseFixtures[ProtocolChat])
	}))
	defer upstream.Close()
	adapter := mustNewAdapter(t, ProtocolChat, upstream.URL, "")
	response, err := adapter.GeminiGenerateContent(context.Background(), &Request{
		URL: original, Body: strings.NewReader(adapterRequestFixtures[ProtocolGenerateContent]),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if original.String() != originalString {
		t.Fatalf("request URL was mutated: %q -> %q", originalString, original.String())
	}
	if _, err := adapter.GeminiGenerateContent(context.Background(), &Request{Body: strings.NewReader(adapterRequestFixtures[ProtocolGenerateContent])}); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("missing Gemini URL error = %v", err)
	}
}

func TestGeminiUpstreamWithModelEscapesPathOnce(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.RawPath, "%252F") || !strings.Contains(request.URL.EscapedPath(), "publishers%2Facme%20models%2Fgemini:streamGenerateContent") {
			t.Errorf("upstream URL path=%q raw_path=%q", request.URL.Path, request.URL.RawPath)
		}
		if request.URL.RawQuery != "alt=sse" {
			t.Errorf("upstream query = %q", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writeProtocolStream(t, writer, ProtocolGenerateContent)
	}))
	defer upstream.Close()
	adapter := mustNewAdapter(t, ProtocolGenerateContent, upstream.URL, "", WithModel("publishers/acme models/gemini"))
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, true))
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil || !bytes.Contains(body, []byte("client-model")) {
		t.Fatalf("body=%s err=%v", body, readErr)
	}
}

func TestGatewayAdapterUpstreamErrorsAreRelayResponses(t *testing.T) {
	redirectCalls := atomic.Int64{}
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectCalls.Add(1)
	}))
	defer redirectTarget.Close()
	for _, test := range []struct {
		name       string
		status     int
		location   string
		wantStatus int
		wantText   string
	}{
		{name: "rate_limit", status: http.StatusTooManyRequests, wantStatus: http.StatusTooManyRequests, wantText: "message"},
		{name: "redirect", status: http.StatusTemporaryRedirect, location: redirectTarget.URL, wantStatus: http.StatusBadGateway, wantText: "redirect"},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if test.location != "" {
					writer.Header().Set("Location", test.location)
				}
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, `{"error":{"message":"slow down"}}`)
			}))
			defer upstream.Close()
			adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "")
			response, err := adapter.AnthropicMessages(context.Background(), adapterRequest(ProtocolMessages, false))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode != test.wantStatus || !bytes.Contains(bytes.ToLower(body), []byte(test.wantText)) {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
		})
	}
	if redirectCalls.Load() != 0 {
		t.Fatalf("redirect target called %d times", redirectCalls.Load())
	}
}

func TestResponseWriteToClosesBodyAndPublishesTrailer(t *testing.T) {
	body := &trackingReadCloser{Reader: strings.NewReader("data")}
	response := &Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": {"text/plain"}},
		Body:       body,
		Trailer:    http.Header{"X-Test-Trailer": {"done"}},
	}
	recorder := httptest.NewRecorder()
	if err := response.WriteTo(recorder); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusCreated || recorder.Body.String() != "data" || !body.closed {
		t.Fatalf("code=%d body=%q closed=%v", recorder.Code, recorder.Body.String(), body.closed)
	}
	if recorder.Result().Trailer.Get("X-Test-Trailer") != "done" {
		t.Fatalf("trailer = %#v", recorder.Result().Trailer)
	}
}

func TestConvertedStreamPublishesAnnouncedUpstreamTrailer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Add("Trailer", "X-Upstream-Final")
		writer.Header().Add("Trailer", "Digest")
		writer.Header().Add("Trailer", "X-RouteMorph-Conversion")
		writeProtocolStream(t, writer, ProtocolResponses)
		writer.Header().Set("X-Upstream-Final", "done")
		writer.Header().Set("Digest", "sha-256=stale")
		writer.Header().Set("X-RouteMorph-Conversion", "spoofed")
	}))
	defer upstream.Close()

	adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "")
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, true))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	if err := response.WriteTo(recorder); err != nil {
		t.Fatal(err)
	}
	result := recorder.Result()
	if result.Trailer.Get("X-Upstream-Final") != "done" {
		t.Fatalf("upstream trailer = %#v", result.Trailer)
	}
	if result.Trailer.Get("X-RouteMorph-Diagnostics") == "" {
		t.Fatalf("diagnostic trailer was not published: %#v", result.Trailer)
	}
	if result.Trailer.Get("Digest") != "" || result.Trailer.Get("X-RouteMorph-Conversion") != "" {
		t.Fatalf("stale or reserved trailer was published: %#v", result.Trailer)
	}
}

func TestConvertedChatStreamDrainsDoneBeforePublishingTrailer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Add("Trailer", "X-Upstream-Final")
		writeProtocolStream(t, writer, ProtocolChat)
		writer.Header().Set("X-Upstream-Final", "done")
	}))
	defer upstream.Close()

	adapter := mustNewAdapter(t, ProtocolChat, upstream.URL, "")
	response, err := adapter.OpenAIResponses(context.Background(), adapterRequest(ProtocolResponses, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.Trailer.Get("X-Upstream-Final") != "done" {
		t.Fatalf("upstream trailer = %#v", response.Trailer)
	}
}

func TestConvertedBufferedResponseFiltersRepresentationTrailers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Add("Trailer", "Digest")
		writer.Header().Add("Trailer", "X-Upstream-Final")
		_, _ = io.WriteString(writer, adapterResponseFixtures[ProtocolResponses])
		writer.Header().Set("Digest", "sha-256=stale")
		writer.Header().Set("X-Upstream-Final", "done")
	}))
	defer upstream.Close()

	adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "")
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, false))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Trailer.Get("Digest") != "" || response.Trailer.Get("X-Upstream-Final") != "done" {
		t.Fatalf("filtered trailers = %#v", response.Trailer)
	}
}

func TestNativeStreamFiltersReservedTrailers(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Add("Trailer", "Digest")
		writer.Header().Add("Trailer", "X-RouteMorph-Diagnostics")
		writeProtocolStream(t, writer, ProtocolChat)
		writer.Header().Set("Digest", "sha-256=native")
		writer.Header().Set("X-RouteMorph-Diagnostics", "999")
	}))
	defer upstream.Close()

	adapter := mustNewAdapter(t, ProtocolChat, upstream.URL, "")
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.Trailer.Get("Digest") != "sha-256=native" || response.Trailer.Get("X-RouteMorph-Diagnostics") != "" {
		t.Fatalf("native trailers = %#v", response.Trailer)
	}
}

func TestAdapterMetadataOverwritesSpoofedDiagnosticCount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-RouteMorph-Diagnostics", "999")
		_, _ = io.WriteString(writer, adapterResponseFixtures[ProtocolChat])
	}))
	defer upstream.Close()

	adapter := mustNewAdapter(t, ProtocolChat, upstream.URL, "")
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, false))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if got := response.Header.Get("X-RouteMorph-Diagnostics"); got != "0" {
		t.Fatalf("diagnostic count=%q, want 0", got)
	}
}

func TestGatewayAdapterStreamFailureEmitsProtocolError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: not-json\n\n")
	}))
	defer upstream.Close()
	adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "")
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, true))
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr == nil || !bytes.Contains(body, []byte("stream_conversion_error")) || !bytes.Contains(body, []byte("[DONE]")) {
		t.Fatalf("body=%s err=%v", body, readErr)
	}
	var conversion *ConversionError
	if !errors.As(readErr, &conversion) {
		t.Fatalf("stream error %T does not expose *routemorph.ConversionError", readErr)
	}
	if !errors.Is(readErr, ErrUpstreamResponse) {
		t.Fatalf("stream error %v does not expose ErrUpstreamResponse", readErr)
	}
	if !errors.Is(readErr, ErrInvalidPayload) {
		t.Fatalf("stream error %v does not preserve its conversion category", readErr)
	}
	if len(response.Meta.Diagnostics()) == 0 {
		t.Fatal("stream diagnostic was not retained")
	}
}

func TestGatewayAdapterClosingStreamCancelsUpstream(t *testing.T) {
	cancelled := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	adapter := mustNewAdapter(t, ProtocolChat, upstream.URL, "")
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, true))
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("closing response body did not cancel upstream request")
	}
}

func TestConvertedStreamCancellationIsNotAnUpstreamProtocolError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "")
	response, err := adapter.OpenAIChatCompletions(ctx, adapterRequest(ProtocolChat, true))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !errors.Is(readErr, context.Canceled) {
		t.Fatalf("stream read error = %v, want context.Canceled", readErr)
	}
	if errors.Is(readErr, ErrUpstreamResponse) {
		t.Fatalf("stream cancellation was misclassified as ErrUpstreamResponse: %v", readErr)
	}
}

func TestGatewayAdapterRejectsInvalidConstructionAndOversizedBody(t *testing.T) {
	if _, err := NewOpenAIResponsesAdapter("relative", ""); err == nil {
		t.Fatal("relative base URL unexpectedly accepted")
	}
	if _, err := NewOpenAIResponsesAdapter("https://example.test", "", WithModel("")); err == nil {
		t.Fatal("empty model unexpectedly accepted")
	}
	oversized := io.LimitReader(strings.NewReader(strings.Repeat("x", 1024)), defaultAdapterMaxBodyBytes+1)
	// Avoid allocating a 32 MiB string while still exercising an exact limit in
	// the helper used by the public path.
	if _, err := transportx.ReadBody(oversized, 100); err == nil {
		t.Fatal("oversized body unexpectedly accepted")
	}
}

func TestAdapterMethodsRejectNilAndZeroValues(t *testing.T) {
	type invoke func(context.Context, *Request) (*Response, error)
	for adapterName, adapter := range map[string]*Adapter{
		"nil":  nil,
		"zero": {},
	} {
		methods := map[string]invoke{
			"chat":      adapter.OpenAIChatCompletions,
			"responses": adapter.OpenAIResponses,
			"messages":  adapter.AnthropicMessages,
			"gemini":    adapter.GeminiGenerateContent,
		}
		for methodName, call := range methods {
			t.Run(adapterName+"/"+methodName, func(t *testing.T) {
				response, err := call(context.Background(), nil)
				if response != nil || !errors.Is(err, errUninitializedAdapter) {
					t.Fatalf("response=%v error=%v, want uninitialized adapter", response, err)
				}
			})
		}
	}
}

func TestPrepareRequestRejectsOversizedBodyBeforeParsing(t *testing.T) {
	body := make([]byte, defaultAdapterMaxBodyBytes+1)
	request, info, err := PrepareRequest(context.Background(), ProtocolChat, nil, body)
	if request != nil || info != (RequestInfo{}) || !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("request=%v info=%+v error=%v", request, info, err)
	}
	var conversion *ConversionError
	if !errors.As(err, &conversion) || conversion.Protocol != ProtocolChat || conversion.Path != "$" || !strings.Contains(conversion.Reason, "body exceeds") {
		t.Fatalf("conversion error = %+v", conversion)
	}
}

func TestPreparedRequestFallsBackToValidationAfterBodyMutation(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()

	requestURL, _ := url.Parse("/v1/chat/completions")
	request, _, err := PrepareRequest(context.Background(), ProtocolChat, requestURL, []byte(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Body = strings.NewReader(`{"model":"forged"}`)
	adapter := mustNewAdapter(t, ProtocolChat, upstream.URL, "")
	if _, err := adapter.OpenAIChatCompletions(context.Background(), request); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("error=%v, want ErrInvalidPayload", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("upstream calls=%d, want 0", calls.Load())
	}
}

func mustNewAdapter(t *testing.T, protocol Protocol, baseURL, apiKey string, options ...Option) *Adapter {
	t.Helper()
	var adapter *Adapter
	var err error
	switch protocol {
	case ProtocolChat:
		adapter, err = NewOpenAIChatCompletionsAdapter(baseURL, apiKey, options...)
	case ProtocolResponses:
		adapter, err = NewOpenAIResponsesAdapter(baseURL, apiKey, options...)
	case ProtocolMessages:
		adapter, err = NewAnthropicMessagesAdapter(baseURL, apiKey, options...)
	case ProtocolGenerateContent:
		adapter, err = NewGeminiGenerateContentAdapter(baseURL, apiKey, options...)
	}
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	return adapter
}

func TestGatewayAdapterTransportBoundsResponseHeaderWaitWithoutCappingStreams(t *testing.T) {
	client := transportx.NewClient(defaultAdapterResponseHeaderTimeout)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	if transport.ResponseHeaderTimeout != defaultAdapterResponseHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %s, want %s", transport.ResponseHeaderTimeout, defaultAdapterResponseHeaderTimeout)
	}
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s; whole-response timeout would break streams", client.Timeout)
	}
}

func TestGatewayAdapterRejectsInvalidNativeSuccessPayload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `not-json`)
	}))
	defer upstream.Close()

	adapter := mustNewAdapter(t, ProtocolChat, upstream.URL, "")
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, false))
	if response != nil || !errors.Is(err, ErrUpstreamResponse) || !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("response=%#v error=%v", response, err)
	}
}

func TestGatewayAdapterRejectsInvalidNativeSuccessStreams(t *testing.T) {
	protocols := []Protocol{ProtocolChat, ProtocolResponses, ProtocolMessages, ProtocolGenerateContent}
	for _, protocol := range protocols {
		protocol := protocol
		t.Run(string(protocol), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, "data: not-json\n\n")
			}))
			defer upstream.Close()

			adapter := mustNewAdapter(t, protocol, upstream.URL, "")
			response, err := invokeAdapter(context.Background(), adapter, protocol, adapterRequest(protocol, true))
			if err != nil {
				t.Fatalf("invoke adapter: %v", err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if len(body) != 0 || !errors.Is(readErr, ErrUpstreamResponse) || !errors.Is(readErr, ErrInvalidPayload) {
				t.Fatalf("body=%q error=%v, want no body and typed upstream payload error", body, readErr)
			}
		})
	}
}

func TestGatewayAdapterRejectsInvalidNativeStreamWithModelOverride(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: not-json\n\n")
	}))
	defer upstream.Close()

	adapter, err := NewOpenAIChatCompletionsAdapter(upstream.URL, "", WithModel("provider-model"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.OpenAIChatCompletions(context.Background(), adapterRequest(ProtocolChat, true))
	if err != nil {
		t.Fatalf("invoke adapter: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(body, []byte(`"code":"stream_decode_error"`)) || !bytes.Contains(body, []byte("[DONE]")) ||
		!errors.Is(readErr, ErrUpstreamResponse) || !errors.Is(readErr, ErrInvalidPayload) {
		t.Fatalf("body=%q error=%v, want protocol error frame and typed upstream payload error", body, readErr)
	}
}

func TestGatewayAdapterModelOverrideRejectsNullNestedStreamObjects(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		payload  string
	}{
		{
			name:     "responses",
			protocol: ProtocolResponses,
			payload:  "event: response.in_progress\ndata: {\"type\":\"response.in_progress\",\"response\":null}\n\n",
		},
		{
			name:     "messages",
			protocol: ProtocolMessages,
			payload:  "event: future_event\ndata: {\"type\":\"future_event\",\"message\":null}\n\n",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, test.payload)
			}))
			defer upstream.Close()

			var adapter *Adapter
			var err error
			switch test.protocol {
			case ProtocolResponses:
				adapter, err = NewOpenAIResponsesAdapter(upstream.URL, "", WithModel("provider-model"))
			case ProtocolMessages:
				adapter, err = NewAnthropicMessagesAdapter(upstream.URL, "", WithModel("provider-model"))
			}
			if err != nil {
				t.Fatal(err)
			}
			response, err := invokeAdapter(context.Background(), adapter, test.protocol, adapterRequest(test.protocol, true))
			if err != nil {
				t.Fatalf("invoke adapter: %v", err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if len(body) == 0 || !bytes.Contains(body, []byte("must be an object")) || !errors.Is(readErr, ErrUpstreamResponse) || !errors.Is(readErr, ErrInvalidPayload) {
				t.Fatalf("body=%q error=%v, want typed model-patch error without panic", body, readErr)
			}
		})
	}
}

func TestGatewayAdapterRejectsBlockedNativeGeminiStream(t *testing.T) {
	for _, payload := range []string{
		`{"promptFeedback":{"blockReason":"SAFETY"}}`,
		`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"SAFETY"}]}`,
	} {
		payload := payload
		t.Run(payload, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(writer, "data: %s\n\n", payload)
			}))
			defer upstream.Close()

			adapter := mustNewAdapter(t, ProtocolGenerateContent, upstream.URL, "")
			response, err := adapter.GeminiGenerateContent(context.Background(), adapterRequest(ProtocolGenerateContent, true))
			if err != nil {
				t.Fatalf("invoke adapter: %v", err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if len(body) != 0 || !errors.Is(readErr, ErrUpstreamResponse) || !strings.Contains(readErr.Error(), "blockReason") {
				t.Fatalf("body=%q error=%v, want Gemini prompt block error", body, readErr)
			}
		})
	}
}

func TestIsEventStreamParsesMediaTypeExactly(t *testing.T) {
	for _, value := range []string{"text/event-stream", "text/event-stream; charset=utf-8", "TEXT/EVENT-STREAM"} {
		if !transportx.IsEventStream(value) {
			t.Errorf("isEventStream(%q) = false", value)
		}
	}
	for _, value := range []string{"", "application/json", "application/text/event-stream+json", "text/event-stream garbage"} {
		if transportx.IsEventStream(value) {
			t.Errorf("isEventStream(%q) = true", value)
		}
	}
}

func adapterRequest(protocol Protocol, stream bool) *Request {
	body := adapterRequestFixtures[protocol]
	if protocol != ProtocolGenerateContent {
		var object map[string]json.RawMessage
		_ = json.Unmarshal([]byte(body), &object)
		object["stream"], _ = json.Marshal(stream)
		encoded, _ := json.Marshal(object)
		body = string(encoded)
	}
	request := &Request{Header: make(http.Header), Body: strings.NewReader(body)}
	if protocol == ProtocolGenerateContent {
		method := "generateContent"
		if stream {
			method = "streamGenerateContent"
		}
		request.URL, _ = url.Parse("/v1beta/models/client-model:" + method + "?client_only=true")
	}
	return request
}

func invokeAdapter(ctx context.Context, adapter *Adapter, protocol Protocol, request *Request) (*Response, error) {
	switch protocol {
	case ProtocolChat:
		return adapter.OpenAIChatCompletions(ctx, request)
	case ProtocolResponses:
		return adapter.OpenAIResponses(ctx, request)
	case ProtocolMessages:
		return adapter.AnthropicMessages(ctx, request)
	case ProtocolGenerateContent:
		return adapter.GeminiGenerateContent(ctx, request)
	default:
		return nil, errors.New("unsupported test protocol")
	}
}

func assertAdapterUpstreamRequest(t *testing.T, request *http.Request, protocol Protocol, stream bool, model string) {
	t.Helper()
	if protocol == ProtocolGenerateContent {
		wantQuery := ""
		if stream {
			wantQuery = "alt=sse"
		}
		if request.URL.RawQuery != wantQuery {
			t.Errorf("Gemini query = %q, want %q", request.URL.RawQuery, wantQuery)
		}
	}
	switch protocol {
	case ProtocolChat:
		if request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("chat request path=%q headers=%#v", request.URL.Path, request.Header)
		}
	case ProtocolResponses:
		if request.URL.Path != "/v1/responses" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("responses request path=%q headers=%#v", request.URL.Path, request.Header)
		}
	case ProtocolMessages:
		if request.URL.Path != "/v1/messages" || request.Header.Get("X-API-Key") != "secret" || request.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Errorf("messages request path=%q headers=%#v", request.URL.Path, request.Header)
		}
	case ProtocolGenerateContent:
		wantMethod := ":generateContent"
		if stream {
			wantMethod = ":streamGenerateContent"
		}
		if !strings.HasSuffix(request.URL.Path, "/models/"+model+wantMethod) || request.Header.Get("X-Goog-API-Key") != "secret" {
			t.Errorf("Gemini request URL=%q headers=%#v", request.URL.String(), request.Header)
		}
		return
	}
	var object map[string]json.RawMessage
	if err := json.NewDecoder(request.Body).Decode(&object); err != nil {
		t.Errorf("decode upstream request: %v", err)
		return
	}
	var gotModel string
	_ = json.Unmarshal(object["model"], &gotModel)
	var gotStream bool
	_ = json.Unmarshal(object["stream"], &gotStream)
	if gotModel != model || gotStream != stream {
		t.Errorf("upstream model=%q stream=%v body=%#v", gotModel, gotStream, object)
	}
}

func writeProtocolStream(t *testing.T, writer io.Writer, protocol Protocol) {
	t.Helper()
	events, _, err := streamx.RenderNativeResponse(core.Protocol(protocol), []byte(adapterResponseFixtures[protocol]))
	if err != nil {
		t.Errorf("encode stream: %v", err)
		return
	}
	for _, event := range events {
		if event.Event != "" {
			_, _ = fmt.Fprintf(writer, "event: %s\n", event.Event)
		}
		_, _ = fmt.Fprintf(writer, "data: %s\n\n", event.Data)
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return nil
}
