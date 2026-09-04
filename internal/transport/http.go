package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const DefaultMaxBodyBytes = int64(32 << 20)

func NewClient(responseHeaderTimeout time.Duration) *http.Client {
	base := http.DefaultTransport
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok && defaultTransport != nil {
		cloned := defaultTransport.Clone()
		cloned.ResponseHeaderTimeout = responseHeaderTimeout
		base = cloned
	}
	return &http.Client{
		Transport: base,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func ParseBaseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimRight(value, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("baseURL must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("baseURL scheme must be http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("baseURL must not contain credentials, query, or fragment")
	}
	return parsed, nil
}

func BuildEndpoint(base *url.URL, protocol, model string, stream bool) string {
	copyURL := *base
	basePath := strings.TrimRight(copyURL.Path, "/")
	escapedBasePath := strings.TrimRight(copyURL.EscapedPath(), "/")
	var suffix, escapedSuffix string
	switch protocol {
	case "chat":
		suffix = versionedSuffix(basePath, "/v1/chat/completions", "/chat/completions")
	case "responses":
		suffix = versionedSuffix(basePath, "/v1/responses", "/responses")
	case "messages":
		suffix = versionedSuffix(basePath, "/v1/messages", "/messages")
	case "generateContent":
		method := "generateContent"
		if stream {
			method = "streamGenerateContent"
		}
		version := "/v1beta"
		if strings.HasSuffix(basePath, "/v1") || strings.HasSuffix(basePath, "/v1beta") {
			version = ""
		}
		suffix = version + "/models/" + model + ":" + method
		escapedSuffix = version + "/models/" + url.PathEscape(model) + ":" + method
	}
	copyURL.Path = basePath + suffix
	if escapedSuffix != "" {
		copyURL.RawPath = escapedBasePath + escapedSuffix
	} else {
		copyURL.RawPath = ""
	}
	if protocol == "generateContent" && stream {
		copyURL.RawQuery = "alt=sse"
	} else {
		copyURL.RawQuery = ""
	}
	return copyURL.String()
}

func versionedSuffix(basePath, full, afterVersion string) string {
	if strings.HasSuffix(basePath, "/v1") {
		return afterVersion
	}
	return full
}

func BuildRequestHeaders(source http.Header, protocol, apiKey string, stream bool) http.Header {
	header := CopyEndToEndHeaders(source)
	for _, name := range []string{"Authorization", "Proxy-Authorization", "X-API-Key", "X-Goog-API-Key", "Cookie", "Set-Cookie", "Content-Encoding", "Accept-Encoding"} {
		header.Del(name)
	}
	filterProviderControlHeaders(header, protocol)
	header.Set("Content-Type", "application/json")
	if stream {
		header.Set("Accept", "text/event-stream")
	}
	if header.Get("User-Agent") == "" {
		header.Set("User-Agent", "RouteMorph-SDK/1.0")
	}
	switch protocol {
	case "chat", "responses":
		if apiKey != "" {
			header.Set("Authorization", "Bearer "+apiKey)
		}
	case "messages":
		if apiKey != "" {
			header.Set("X-API-Key", apiKey)
		}
		if header.Get("Anthropic-Version") == "" {
			header.Set("Anthropic-Version", "2023-06-01")
		}
	case "generateContent":
		if apiKey != "" {
			header.Set("X-Goog-API-Key", apiKey)
		}
	}
	return header
}

// filterProviderControlHeaders prevents a client header for one provider from
// changing another provider's account, API version, or feature policy. A small
// same-provider allowlist retains controls that callers commonly need.
func filterProviderControlHeaders(header http.Header, protocol string) {
	allowed := map[string]struct{}{}
	switch protocol {
	case "chat", "responses":
		allowed["Openai-Organization"] = struct{}{}
		allowed["Openai-Project"] = struct{}{}
		allowed["Openai-Beta"] = struct{}{}
	case "messages":
		allowed["Anthropic-Version"] = struct{}{}
		allowed["Anthropic-Beta"] = struct{}{}
	case "generateContent":
		allowed["X-Goog-User-Project"] = struct{}{}
	}
	for name := range header {
		canonical := http.CanonicalHeaderKey(name)
		lower := strings.ToLower(canonical)
		if !strings.HasPrefix(lower, "openai-") && !strings.HasPrefix(lower, "anthropic-") && !strings.HasPrefix(lower, "x-goog-") {
			continue
		}
		if _, ok := allowed[canonical]; !ok {
			header.Del(name)
		}
	}
}

func CopyResponseHeaders(source http.Header, converted bool) http.Header {
	header := CopyEndToEndHeaders(source)
	header.Del("Trailer")
	dropAdapterMetadataHeaders(header)
	if converted {
		dropRepresentationMetadata(header)
	}
	return header
}

// CopyResponseTrailers returns detached, end-to-end trailers that remain valid
// for the response body exposed to the caller. Representation metadata is
// discarded after conversion because it describes the upstream bytes.
func CopyResponseTrailers(source http.Header, converted bool) http.Header {
	trailer := CopyEndToEndHeaders(source)
	dropAdapterMetadataHeaders(trailer)
	if converted {
		dropRepresentationMetadata(trailer)
	}
	return trailer
}

func dropRepresentationMetadata(header http.Header) {
	for _, name := range []string{"Content-Length", "Content-Encoding", "ETag", "Content-MD5", "Digest"} {
		header.Del(name)
	}
}

func dropAdapterMetadataHeaders(header http.Header) {
	for _, name := range []string{"X-RouteMorph-Conversion", "X-RouteMorph-Stream-Mode", "X-RouteMorph-Diagnostics"} {
		header.Del(name)
	}
}

func CopyEndToEndHeaders(source http.Header) http.Header {
	result := make(http.Header)
	connectionHeaders := make(map[string]struct{})
	for _, value := range source.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			connectionHeaders[http.CanonicalHeaderKey(strings.TrimSpace(name))] = struct{}{}
		}
	}
	for name, values := range source {
		canonical := http.CanonicalHeaderKey(name)
		if isHopByHopHeader(canonical) || canonical == "Host" || canonical == "Content-Length" {
			continue
		}
		if _, drop := connectionHeaders[canonical]; drop {
			continue
		}
		if values == nil {
			result[canonical] = nil
			continue
		}
		for _, value := range values {
			result.Add(canonical, value)
		}
	}
	return result
}

func isHopByHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}

func ReadBody(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, errors.New("failed to read body")
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("body exceeds %d bytes", limit)
	}
	return body, nil
}

func IsEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && strings.EqualFold(mediaType, "text/event-stream")
}

type managedBody struct {
	reader io.ReadCloser
	peer   io.Closer
	cancel context.CancelFunc
	once   sync.Once
}

func NewManagedBody(reader io.ReadCloser, cancel context.CancelFunc) io.ReadCloser {
	return &managedBody{reader: reader, cancel: cancel}
}

func NewManagedBodyWithPeer(reader io.ReadCloser, peer io.Closer, cancel context.CancelFunc) io.ReadCloser {
	return &managedBody{reader: reader, peer: peer, cancel: cancel}
}

func (b *managedBody) Read(buffer []byte) (int, error) {
	read, err := b.reader.Read(buffer)
	if err != nil {
		b.cleanup()
	}
	return read, err
}

func (b *managedBody) Close() error {
	err := b.reader.Close()
	b.cleanup()
	return err
}

func (b *managedBody) cleanup() {
	b.once.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}
		if b.peer != nil {
			_ = b.peer.Close()
		}
	})
}
