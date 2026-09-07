package routemorph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// HTTPClient returns an HTTP client that routes supported inference requests
// through the adapter. It can be injected into provider SDKs that accept a
// custom *http.Client, including the official OpenAI, Anthropic, and Gemini Go
// SDKs.
//
// The client recognizes POST requests to the OpenAI Chat Completions and
// Responses endpoints, the Anthropic Messages endpoint, and the Gemini
// Developer API generateContent endpoints. Other endpoints fail with
// ErrUnsupported and are never sent to the network. The adapter's configured
// upstream credentials replace credentials added by the ingress SDK.
//
// The returned client has no whole-request timeout so streaming responses are
// not capped. Callers should use request contexts for deadlines. A new client
// is returned on every call and may be customized before it is passed to a
// provider SDK.
func (a *Adapter) HTTPClient() *http.Client {
	return &http.Client{
		Transport: adapterRoundTripper{adapter: a},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type adapterRoundTripper struct {
	adapter *Adapter
}

func (t adapterRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("routemorph: nil HTTP request")
	}
	ctx := request.Context()
	var requestBody io.Reader = request.Body
	if request.Body != nil {
		body := &onceReadCloser{ReadCloser: request.Body}
		requestBody = body
		stopClosingBody := context.AfterFunc(ctx, func() {
			_ = body.Close()
		})
		defer func() {
			stopClosingBody()
			_ = body.Close()
		}()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ingress, err := ingressProtocolForHTTPRequest(request)
	if err != nil {
		return nil, err
	}
	response, err := t.adapter.invoke(request.Context(), ingress, &Request{
		Header: request.Header,
		URL:    request.URL,
		Body:   requestBody,
	})
	if contextErr := ctx.Err(); contextErr != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, contextErr
	}
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, errors.New("routemorph: adapter returned an incomplete HTTP response")
	}

	statusCode := response.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	return &http.Response{
		Status:        httpStatus(statusCode),
		StatusCode:    statusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        response.Header,
		Body:          response.Body,
		ContentLength: responseContentLength(response.Header),
		Trailer:       response.Trailer,
		Request:       request,
	}, nil
}

type onceReadCloser struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (r *onceReadCloser) Close() error {
	r.once.Do(func() {
		r.err = r.ReadCloser.Close()
	})
	return r.err
}

func ingressProtocolForHTTPRequest(request *http.Request) (Protocol, error) {
	if request.Method != http.MethodPost {
		return "", fmt.Errorf("%w: provider SDK endpoint %s is not supported", ErrUnsupported, request.Method)
	}
	if request.URL == nil {
		return "", errors.New("routemorph: HTTP request URL is nil")
	}
	path := request.URL.EscapedPath()
	switch {
	case strings.HasSuffix(path, "/chat/completions"):
		return ProtocolChat, nil
	case strings.HasSuffix(path, "/responses"):
		return ProtocolResponses, nil
	case strings.HasSuffix(path, "/messages"):
		return ProtocolMessages, nil
	case (strings.HasPrefix(path, "/v1/models/") || strings.HasPrefix(path, "/v1beta/models/")) &&
		(strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent")):
		return ProtocolGenerateContent, nil
	default:
		return "", fmt.Errorf("%w: provider SDK endpoint POST %q is not supported", ErrUnsupported, path)
	}
}

func httpStatus(statusCode int) string {
	text := http.StatusText(statusCode)
	if text == "" {
		return strconv.Itoa(statusCode)
	}
	return strconv.Itoa(statusCode) + " " + text
}

func responseContentLength(header http.Header) int64 {
	value := header.Get("Content-Length")
	if value == "" {
		return -1
	}
	length, err := strconv.ParseInt(value, 10, 64)
	if err != nil || length < 0 {
		return -1
	}
	return length
}
