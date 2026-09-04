package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	codec "github.com/2218342221/RouteMorphSDK/internal/codec"
	core "github.com/2218342221/RouteMorphSDK/internal/core"
	transportx "github.com/2218342221/RouteMorphSDK/internal/transport"
)

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Config struct {
	Upstream     core.Protocol
	BaseURL      *url.URL
	APIKey       string
	Model        string
	Client       Doer
	Catalog      core.Catalog
	MaxBodyBytes int64
}

type Service struct {
	upstream     core.Protocol
	baseURL      *url.URL
	apiKey       string
	model        string
	client       Doer
	catalog      core.Catalog
	maxBodyBytes int64
}

type upstreamError struct{ err error }

func (e *upstreamError) Error() string {
	return fmt.Sprintf("convert upstream response: %v", e.err)
}

func (e *upstreamError) Unwrap() []error { return []error{core.ErrUpstreamResponse, e.err} }

func wrapUpstreamResponse(err error) error { return &upstreamError{err: err} }

func New(config Config) *Service {
	return &Service{
		upstream: config.Upstream, baseURL: config.BaseURL, apiKey: config.APIKey,
		model: config.Model, client: config.Client, catalog: config.Catalog,
		maxBodyBytes: config.MaxBodyBytes,
	}
}

type Request struct {
	Header   http.Header
	URL      *url.URL
	Body     io.Reader
	Prepared *PreparedRequest
}

// PreparedRequest is an opaque hand-off from the public package. Invoke only
// trusts it after matching the protocol, URL path, and request-body digest.
type PreparedRequest struct {
	Protocol                  core.Protocol
	EscapedPath               string
	BodyDigest                [sha256.Size]byte
	Info                      core.RequestInfo
	ChatStreamIncludeUsage    bool
	ChatStreamIncludeUsageSet bool
}

func (a *Service) Invoke(ctx context.Context, ingress core.Protocol, request *Request) (*Response, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if request == nil || request.Body == nil {
		return nil, core.Invalid(ingress, "$", "request body is required")
	}
	body, err := transportx.ReadBody(request.Body, a.maxBodyBytes)
	if err != nil {
		return nil, core.Invalid(ingress, "$", "%v", err)
	}
	metadata, chatStreamIncludeUsage, chatStreamIncludeUsageSet, prepared := reusablePreparedRequest(ingress, request.URL, body, request.Prepared)
	if !prepared {
		metadata, chatStreamIncludeUsage, chatStreamIncludeUsageSet, err = codec.InspectRelayRequest(ctx, ingress, request.URL, body)
		if err != nil {
			return nil, err
		}
	}
	wireCodec, err := codec.For(ingress)
	if err != nil {
		return nil, err
	}
	upstreamModel := metadata.Model
	if a.model != "" {
		upstreamModel = a.model
	}
	options := core.ConversionOptions{
		Exchange: core.ExchangeMetadata{
			RequestID:                 sanitizeAdapterRequestID(request.Header.Get("X-Request-ID")),
			ClientModel:               metadata.Model,
			UpstreamModel:             upstreamModel,
			Stream:                    metadata.Stream,
			StreamSet:                 true,
			ChatStreamIncludeUsage:    chatStreamIncludeUsage,
			ChatStreamIncludeUsageSet: chatStreamIncludeUsageSet,
		},
		LossPolicy: core.RejectSemanticLoss,
	}
	plan, err := a.catalog.Plan(ingress, a.upstream)
	if err != nil {
		return nil, err
	}
	convertedBody := body
	diagnostics := make([]core.Diagnostic, 0)
	if ingress == a.upstream {
		if a.model != "" && ingress != core.ProtocolGenerateContent {
			convertedBody, err = wireCodec.PatchRequest(ctx, convertedBody, core.RequestPatch{Model: &upstreamModel})
			if err != nil {
				return nil, err
			}
		}
	} else {
		result, convertErr := a.catalog.ConvertRequest(ctx, plan, convertedBody, options)
		if convertErr != nil {
			return nil, convertErr
		}
		convertedBody = result.Body
		diagnostics = append(diagnostics, result.Diagnostics...)
	}

	callContext, cancel := context.WithCancel(ctx)
	endpoint := transportx.BuildEndpoint(a.baseURL, string(a.upstream), upstreamModel, metadata.Stream)
	upstreamRequest, err := http.NewRequestWithContext(callContext, http.MethodPost, endpoint, bytes.NewReader(convertedBody))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("build upstream request: %w", err)
	}
	upstreamRequest.Header = transportx.BuildRequestHeaders(request.Header, string(a.upstream), a.apiKey, metadata.Stream)
	upstreamResponse, err := a.client.Do(upstreamRequest)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("call upstream: %w", err)
	}
	store := newDiagnosticStore(diagnostics)
	meta := ResponseMeta{
		IngressProtocol: ingress, UpstreamProtocol: a.upstream,
		Stream: metadata.Stream, RouteMode: plan.StreamMode, diagnostics: store,
	}

	if upstreamResponse.StatusCode >= 300 && upstreamResponse.StatusCode < 400 {
		cancel()
		defer upstreamResponse.Body.Close()
		return a.redirectResponse(ingress, meta, options.Exchange.RequestID, upstreamResponse), nil
	}
	if upstreamResponse.StatusCode < 200 || upstreamResponse.StatusCode >= 300 {
		defer cancel()
		defer upstreamResponse.Body.Close()
		return a.errorResponse(ingress, meta, options.Exchange.RequestID, upstreamResponse)
	}
	isSSE := transportx.IsEventStream(upstreamResponse.Header.Get("Content-Type"))
	if metadata.Stream != isSSE {
		cancel()
		upstreamResponse.Body.Close()
		if metadata.Stream {
			return nil, core.UpstreamResponseError(a.upstream, "$", "streaming upstream returned a non-SSE response")
		}
		return nil, core.UpstreamResponseError(a.upstream, "$", "non-streaming upstream returned an SSE response")
	}
	if isSSE {
		return a.streamResponse(callContext, cancel, ingress, metadata.Model, plan, options, meta, upstreamResponse)
	}
	defer cancel()
	defer upstreamResponse.Body.Close()
	return a.bufferedResponse(ctx, ingress, metadata.Model, plan, options, meta, upstreamResponse)
}

func reusablePreparedRequest(ingress core.Protocol, requestURL *url.URL, body []byte, prepared *PreparedRequest) (core.RequestInfo, bool, bool, bool) {
	if prepared == nil || prepared.Protocol != ingress || prepared.BodyDigest != sha256.Sum256(body) {
		return core.RequestInfo{}, false, false, false
	}
	escapedPath := ""
	if requestURL != nil {
		escapedPath = requestURL.EscapedPath()
	}
	if prepared.EscapedPath != escapedPath {
		return core.RequestInfo{}, false, false, false
	}
	return prepared.Info, prepared.ChatStreamIncludeUsage, prepared.ChatStreamIncludeUsageSet, true
}

func (a *Service) bufferedResponse(ctx context.Context, ingress core.Protocol, clientModel string, plan core.RoutePlan, options core.ConversionOptions, meta ResponseMeta, upstream *http.Response) (*Response, error) {
	body, err := transportx.ReadBody(upstream.Body, a.maxBodyBytes)
	if err != nil {
		return nil, core.UpstreamResponseError(a.upstream, "$", "%v", err)
	}
	converted := ingress != a.upstream
	if converted {
		result, err := a.catalog.ConvertResponse(ctx, plan, body, options)
		if err != nil {
			return nil, wrapUpstreamResponse(err)
		}
		body = result.Body
		meta.diagnostics.add(result.Diagnostics...)
	} else {
		adapter, err := codec.For(a.upstream)
		if err != nil {
			return nil, err
		}
		if err := adapter.ValidateResponse(ctx, body); err != nil {
			return nil, wrapUpstreamResponse(err)
		}
	}
	if !converted && a.model != "" && clientModel != a.model {
		body, err = codec.PatchResponseModel(ingress, body, clientModel)
		if err != nil {
			return nil, err
		}
		converted = true
	}
	header := transportx.CopyResponseHeaders(upstream.Header, converted)
	if converted {
		header.Set("Content-Type", "application/json")
	}
	setAdapterMetadataHeaders(header, meta)
	return &Response{
		StatusCode: upstream.StatusCode,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Trailer:    transportx.CopyResponseTrailers(upstream.Trailer, converted),
		Meta:       meta,
	}, nil
}

func (a *Service) streamResponse(ctx context.Context, cancel context.CancelFunc, ingress core.Protocol, clientModel string, plan core.RoutePlan, options core.ConversionOptions, meta ResponseMeta, upstream *http.Response) (*Response, error) {
	converted := ingress != a.upstream || (a.model != "" && clientModel != a.model)
	header := transportx.CopyResponseHeaders(upstream.Header, converted)
	if converted {
		header.Set("Content-Type", "text/event-stream; charset=utf-8")
		header.Set("Cache-Control", "no-cache")
		header.Set("X-Accel-Buffering", "no")
	}
	setAdapterMetadataHeaders(header, meta)
	source, err := codec.For(a.upstream)
	if err != nil {
		cancel()
		upstream.Body.Close()
		return nil, err
	}
	if ingress == a.upstream {
		upstream.Body = newValidatingSSEBody(ctx, upstream.Body, a.upstream, source, a.maxBodyBytes)
	}
	if !converted {
		response := &Response{
			StatusCode: upstream.StatusCode,
			Header:     header,
			Trailer:    transportx.CopyResponseTrailers(upstream.Trailer, false),
			Meta:       meta,
		}
		body := newFinalizingBody(upstream.Body, func() {
			finalizeNativeTrailers(response, upstream.Trailer)
		})
		response.Body = transportx.NewManagedBody(body, cancel)
		return response, nil
	}

	target, err := codec.For(ingress)
	if err != nil {
		cancel()
		upstream.Body.Close()
		return nil, err
	}
	decoder, err := source.NewStreamDecoder(upstream.Body, core.StreamOptions{MaxFrameBytes: int(a.maxBodyBytes)})
	if err != nil {
		cancel()
		upstream.Body.Close()
		return nil, err
	}
	converter, err := a.catalog.NewResponseStream(ctx, plan, options)
	if err != nil {
		cancel()
		upstream.Body.Close()
		return nil, err
	}
	reader, writer := io.Pipe()
	trailer := transportx.CopyResponseTrailers(upstream.Trailer, true)
	for name := range trailer {
		trailer[name] = nil
	}
	trailer[http.CanonicalHeaderKey("X-RouteMorph-Diagnostics")] = nil
	response := &Response{
		StatusCode: upstream.StatusCode,
		Header:     header,
		Trailer:    trailer,
		Meta:       meta,
	}
	response.Body = transportx.NewManagedBodyWithPeer(reader, upstream.Body, cancel)

	go a.runStream(ctx, writer, upstream, ingress, clientModel, options.Exchange.RequestID, target, decoder, converter, response)
	return response, nil
}

func (a *Service) runStream(ctx context.Context, writer *io.PipeWriter, upstream *http.Response, ingress core.Protocol, clientModel, requestID string, target core.Codec, decoder core.StreamDecoder, converter core.ResponseStream, response *Response) {
	defer upstream.Body.Close()
	encoder, err := target.NewStreamEncoder(writer, core.StreamOptions{MaxFrameBytes: int(a.maxBodyBytes)})
	if err != nil {
		writer.CloseWithError(err)
		return
	}
	writeFrames := func(frames []core.Frame) error {
		for _, frame := range frames {
			if a.model != "" && clientModel != a.model && len(frame.Data) > 0 && string(frame.Data) != "[DONE]" {
				patched, patchErr := codec.PatchStreamResponseModel(ingress, frame.Data, clientModel)
				if patchErr != nil {
					return classifyStreamUpstreamError(patchErr)
				}
				frame.Data = patched
			}
			if err := encoder.Write(ctx, frame); err != nil {
				return err
			}
			if err := encoder.Flush(); err != nil {
				return err
			}
		}
		return nil
	}
	fail := func(code string, streamErr error) {
		response.Meta.diagnostics.add(core.Diagnostic{Severity: "error", Code: code, Path: "$", Message: streamErr.Error()})
		frame, encodeErr := target.EncodeStreamError(core.ProtocolError{
			Type: "upstream_error", Code: code, Message: streamErr.Error(), RequestID: requestID, StatusCode: http.StatusBadGateway,
		})
		if encodeErr == nil {
			frames := []core.Frame{frame}
			if ingress == core.ProtocolChat {
				frames = append(frames, core.Frame{Data: []byte("[DONE]"), Done: true})
			}
			errorContext := context.Background()
			for _, errorFrame := range frames {
				if encoder.Write(errorContext, errorFrame) != nil {
					break
				}
				_ = encoder.Flush()
			}
		}
		finalizeAdapterTrailers(response, upstream.Trailer)
		writer.CloseWithError(streamErr)
	}
	for {
		frame, readErr := decoder.Next(ctx)
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			fail("stream_decode_error", classifyStreamUpstreamError(readErr))
			return
		}
		frames, diagnostics, convertErr := converter.Convert(ctx, frame)
		response.Meta.diagnostics.add(diagnostics...)
		if convertErr != nil {
			fail("stream_conversion_error", classifyStreamUpstreamError(convertErr))
			return
		}
		if err := writeFrames(frames); err != nil {
			fail("stream_write_error", err)
			return
		}
	}
	frames, diagnostics, err := converter.Finalize(ctx)
	response.Meta.diagnostics.add(diagnostics...)
	if err != nil {
		fail("stream_finalization_error", classifyStreamUpstreamError(err))
		return
	}
	if err := writeFrames(frames); err != nil {
		fail("stream_write_error", err)
		return
	}
	finalizeAdapterTrailers(response, upstream.Trailer)
	writer.Close()
}

func classifyStreamUpstreamError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, core.ErrUpstreamResponse) {
		return err
	}
	return wrapUpstreamResponse(err)
}

func (a *Service) redirectResponse(ingress core.Protocol, meta ResponseMeta, requestID string, upstream *http.Response) *Response {
	meta.Stream = false
	wireCodec, _ := codec.For(ingress)
	encoded, _ := wireCodec.EncodeError(core.ProtocolError{
		Type: protocolErrorType(ingress, http.StatusBadGateway), Code: "upstream_redirect",
		Message: "upstream redirects are not allowed", RequestID: requestID, StatusCode: http.StatusBadGateway,
	})
	header := transportx.CopyResponseHeaders(upstream.Header, true)
	header.Del("Location")
	header.Set("Content-Type", encoded.ContentType)
	setAdapterMetadataHeaders(header, meta)
	return &Response{StatusCode: http.StatusBadGateway, Header: header, Body: io.NopCloser(bytes.NewReader(encoded.Body)), Trailer: make(http.Header), Meta: meta}
}

func (a *Service) errorResponse(ingress core.Protocol, meta ResponseMeta, requestID string, upstream *http.Response) (*Response, error) {
	body, err := transportx.ReadBody(upstream.Body, a.maxBodyBytes)
	if err != nil {
		return nil, core.UpstreamResponseError(a.upstream, "$", "%v", err)
	}
	wireCodec, err := codec.For(ingress)
	if err != nil {
		return nil, err
	}
	encoded, err := wireCodec.EncodeError(core.ProtocolError{
		Type: protocolErrorType(ingress, upstream.StatusCode), Code: "upstream_error",
		Message: adapterUpstreamMessage(body, upstream.Status), RequestID: requestID, StatusCode: upstream.StatusCode,
	})
	if err != nil {
		return nil, err
	}
	meta.Stream = false
	header := transportx.CopyResponseHeaders(upstream.Header, true)
	header.Set("Content-Type", encoded.ContentType)
	setAdapterMetadataHeaders(header, meta)
	return &Response{
		StatusCode: upstream.StatusCode, Header: header,
		Body: io.NopCloser(bytes.NewReader(encoded.Body)), Trailer: transportx.CopyResponseTrailers(upstream.Trailer, true), Meta: meta,
	}, nil
}

func protocolErrorType(protocol core.Protocol, status int) string {
	if protocol == core.ProtocolChat || protocol == core.ProtocolResponses {
		if status >= http.StatusInternalServerError {
			return "server_error"
		}
		return "invalid_request_error"
	}
	return "upstream_error"
}

func adapterUpstreamMessage(body []byte, fallback string) string {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Error) > 0 {
		var object struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &object) == nil && object.Message != "" {
			return object.Message
		}
		var text string
		if json.Unmarshal(envelope.Error, &text) == nil && text != "" {
			return text
		}
	}
	return fmt.Sprintf("upstream returned %s", fallback)
}

func finalizeAdapterTrailers(response *Response, upstream http.Header) {
	replaceTrailers(response.Trailer, transportx.CopyResponseTrailers(upstream, true))
	response.Trailer.Set("X-RouteMorph-Diagnostics", fmt.Sprintf("%d", len(response.Meta.Diagnostics())))
}

func finalizeNativeTrailers(response *Response, upstream http.Header) {
	replaceTrailers(response.Trailer, transportx.CopyResponseTrailers(upstream, false))
}

func replaceTrailers(destination, source http.Header) {
	for name := range destination {
		delete(destination, name)
	}
	for name, values := range source {
		destination[name] = append([]string(nil), values...)
	}
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func sanitizeAdapterRequestID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 128 || containsControl(value) {
		return ""
	}
	return value
}
