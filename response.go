package routemorph

import (
	"errors"
	"io"
	"net/http"
	"sort"

	relayx "github.com/2218342221/RouteMorphSDK/internal/relay"
)

// Response is a complete relay response. Callers that do not use WriteTo must
// close Body themselves.
//
// Response is owned by the public package so the internal relay can evolve
// without silently changing the SDK contract.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
	Trailer    http.Header
	Meta       ResponseMeta
}

// ResponseMeta describes the selected conversion path. Diagnostics returns a
// concurrency-safe snapshot; for streams it becomes complete after Body ends.
type ResponseMeta struct {
	IngressProtocol  Protocol
	UpstreamProtocol Protocol
	Stream           bool
	RouteMode        RouteMode

	diagnostics func() []Diagnostic
}

// Diagnostics returns a detached snapshot of conversion diagnostics.
func (m ResponseMeta) Diagnostics() []Diagnostic {
	if m.diagnostics == nil {
		return nil
	}
	return m.diagnostics()
}

func fromRelayResponse(response *relayx.Response) *Response {
	if response == nil {
		return nil
	}
	meta := response.Meta
	diagnostics := func() []Diagnostic {
		return fromCoreDiagnostics(meta.Diagnostics())
	}
	body := response.Body
	if body != nil {
		body = &boundaryReadCloser{ReadCloser: body}
	}
	return &Response{
		StatusCode: response.StatusCode,
		Header:     response.Header,
		Body:       body,
		Trailer:    response.Trailer,
		Meta: ResponseMeta{
			IngressProtocol:  Protocol(meta.IngressProtocol),
			UpstreamProtocol: Protocol(meta.UpstreamProtocol),
			Stream:           meta.Stream,
			RouteMode:        RouteMode(meta.RouteMode),
			diagnostics:      diagnostics,
		},
	}
}

type boundaryReadCloser struct {
	io.ReadCloser
}

func (r *boundaryReadCloser) Read(buffer []byte) (int, error) {
	read, err := r.ReadCloser.Read(buffer)
	return read, toPublicError(err)
}

func (r *boundaryReadCloser) Close() error {
	return toPublicError(r.ReadCloser.Close())
}

// WriteTo writes a complete HTTP relay response and closes Body. Streaming
// bodies are flushed incrementally; trailers are published after the body.
func (r *Response) WriteTo(writer http.ResponseWriter) (resultErr error) {
	if r == nil || writer == nil {
		return errors.New("nil response or response writer")
	}
	if r.Body == nil {
		return errors.New("response body is nil")
	}
	defer func() {
		resultErr = errors.Join(resultErr, r.Body.Close())
	}()
	for name, values := range r.Header {
		writer.Header().Del(name)
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	trailerNames := make([]string, 0, len(r.Trailer))
	for name := range r.Trailer {
		trailerNames = append(trailerNames, http.CanonicalHeaderKey(name))
	}
	sort.Strings(trailerNames)
	for _, name := range trailerNames {
		writer.Header().Add("Trailer", name)
	}
	status := r.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	writer.WriteHeader(status)
	flusher, canFlush := writer.(http.Flusher)
	buffer := make([]byte, 32<<10)
	for {
		read, readErr := r.Body.Read(buffer)
		if read > 0 {
			if _, err := writer.Write(buffer[:read]); err != nil {
				resultErr = err
				break
			}
			if r.Meta.Stream && canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				resultErr = readErr
			}
			break
		}
	}
	for name, values := range r.Trailer {
		writer.Header().Del(name)
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	return resultErr
}
