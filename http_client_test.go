package routemorph

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

func TestHTTPClientRoutesSupportedProviderSDKEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		protocol Protocol
		path     string
	}{
		{name: "openai_chat", protocol: ProtocolChat, path: "/v1/chat/completions"},
		{name: "openai_responses", protocol: ProtocolResponses, path: "/v1/responses"},
		{name: "anthropic_messages", protocol: ProtocolMessages, path: "/v1/messages"},
		{name: "gemini_generate_content", protocol: ProtocolGenerateContent, path: "/v1beta/models/client-model:generateContent"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				assertAdapterUpstreamRequest(t, request, ProtocolResponses, false, "client-model")
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Upstream-Test", "kept")
				_, _ = io.WriteString(writer, adapterResponseFixtures[ProtocolResponses])
			}))
			defer upstream.Close()

			adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "secret")
			request, err := http.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				"https://provider.invalid"+test.path,
				strings.NewReader(adapterRequestFixtures[test.protocol]),
			)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer ingress-only")

			response, err := adapter.HTTPClient().Do(request)
			if err != nil {
				t.Fatalf("HTTPClient.Do: %v", err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if response.StatusCode != http.StatusOK || response.Status != "200 OK" {
				t.Fatalf("status=%q code=%d", response.Status, response.StatusCode)
			}
			if response.Request != request {
				t.Fatal("response does not point to the provider SDK request")
			}
			if response.Header.Get("X-Upstream-Test") != "kept" {
				t.Fatal("safe upstream header was not preserved")
			}
			if response.Header.Get("X-RouteMorph-Conversion") == "" {
				t.Fatal("conversion metadata header is missing")
			}
			wireCodec := codec.New(core.Protocol(test.protocol))
			if err := wireCodec.ValidateResponse(context.Background(), body); err != nil {
				t.Fatalf("invalid %s response %s: %v", test.protocol, body, err)
			}
		})
	}
}

func TestHTTPClientRejectsUnsupportedEndpointWithoutNetworkCall(t *testing.T) {
	var calls int
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer upstream.Close()
	adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "")
	body := &closeTrackingBody{Reader: strings.NewReader(`{"purpose":"assistants"}`)}
	request := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "https", Host: "provider.invalid", Path: "/v1/files"},
		Header: make(http.Header),
		Body:   body,
	}

	response, err := adapter.HTTPClient().Transport.RoundTrip(request)
	if response != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("response=%#v error=%v, want ErrUnsupported", response, err)
	}
	if !body.closed {
		t.Fatal("request body was not closed")
	}
	if calls != 0 {
		t.Fatalf("upstream calls=%d, want 0", calls)
	}
}

func TestHTTPClientEndpointClassification(t *testing.T) {
	tests := []struct {
		method string
		path   string
		want   Protocol
	}{
		{method: http.MethodPost, path: "/proxy/v1/chat/completions", want: ProtocolChat},
		{method: http.MethodPost, path: "/v1/responses", want: ProtocolResponses},
		{method: http.MethodPost, path: "/v1/messages", want: ProtocolMessages},
		{method: http.MethodPost, path: "/v1/models/client-model:generateContent", want: ProtocolGenerateContent},
		{method: http.MethodPost, path: "/v1beta/models/client-model:streamGenerateContent", want: ProtocolGenerateContent},
	}
	for _, test := range tests {
		request := &http.Request{Method: test.method, URL: &url.URL{Path: test.path}}
		got, err := ingressProtocolForHTTPRequest(request)
		if err != nil || got != test.want {
			t.Errorf("%s %s: protocol=%q error=%v, want %q", test.method, test.path, got, err, test.want)
		}
	}

	for _, request := range []*http.Request{
		{Method: http.MethodGet, URL: &url.URL{Path: "/v1/responses"}},
		{Method: http.MethodPost, URL: &url.URL{Path: "/v1/messages/count_tokens"}},
		{Method: http.MethodPost, URL: &url.URL{Path: "/v1beta1/projects/project/locations/us/models/model:generateContent"}},
	} {
		if _, err := ingressProtocolForHTTPRequest(request); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s %s: error=%v, want ErrUnsupported", request.Method, request.URL.Path, err)
		}
	}
}

func TestHTTPClientRejectsUninitializedAdapter(t *testing.T) {
	var adapter *Adapter
	request, err := http.NewRequest(http.MethodPost, "https://provider.invalid/v1/responses", strings.NewReader(adapterRequestFixtures[ProtocolResponses]))
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.HTTPClient().Do(request)
	if response != nil || !errors.Is(err, errUninitializedAdapter) {
		t.Fatalf("response=%#v error=%v, want uninitialized adapter error", response, err)
	}
}

func TestHTTPClientDoesNotSetWholeRequestTimeout(t *testing.T) {
	adapter := mustNewAdapter(t, ProtocolResponses, "https://example.test", "")
	client := adapter.HTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("HTTP client timeout=%s, want no whole-request timeout", client.Timeout)
	}
	if client.Transport == nil {
		t.Fatal("HTTP client transport is nil")
	}
}

func TestHTTPClientCancellationInterruptsBlockedRequestBody(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*http.Client, context.CancelFunc)
		want      error
	}{
		{
			name: "request_context",
			configure: func(_ *http.Client, cancel context.CancelFunc) {
				cancel()
			},
			want: context.Canceled,
		},
		{
			name: "client_timeout",
			configure: func(client *http.Client, _ context.CancelFunc) {
				client.Timeout = 25 * time.Millisecond
			},
			want: context.DeadlineExceeded,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("blocked request unexpectedly reached the upstream")
			}))
			defer upstream.Close()
			adapter := mustNewAdapter(t, ProtocolResponses, upstream.URL, "")
			client := adapter.HTTPClient()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := newBlockingReadCloser()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.invalid/v1/responses", body)
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "client_timeout" {
				test.configure(client, cancel)
			}

			result := make(chan error, 1)
			go func() {
				response, err := client.Do(request)
				if response != nil && response.Body != nil {
					_ = response.Body.Close()
				}
				result <- err
			}()
			select {
			case <-body.started:
			case <-time.After(2 * time.Second):
				t.Fatal("request body read did not start")
			}
			if test.name == "request_context" {
				test.configure(client, cancel)
			}
			select {
			case err := <-result:
				if !errors.Is(err, test.want) {
					t.Fatalf("HTTPClient.Do error=%v, want %v", err, test.want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("HTTPClient.Do did not stop after cancellation")
			}
			if !body.isClosed() {
				t.Fatal("request body was not closed after cancellation")
			}
		})
	}
}

func TestHTTPClientClosesAlreadyCanceledRequestBody(t *testing.T) {
	adapter := mustNewAdapter(t, ProtocolResponses, "https://example.test", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	body := &closeTrackingBody{Reader: strings.NewReader(adapterRequestFixtures[ProtocolResponses])}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.invalid/v1/responses", body)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.HTTPClient().Transport.RoundTrip(request)
	if response != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("response=%#v error=%v, want context.Canceled", response, err)
	}
	if !body.closed {
		t.Fatal("already-canceled request body was not closed")
	}
}

type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

type blockingReadCloser struct {
	started   chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (b *blockingReadCloser) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, errors.New("request body closed")
}

func (b *blockingReadCloser) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func (b *blockingReadCloser) isClosed() bool {
	select {
	case <-b.closed:
		return true
	default:
		return false
	}
}
