package core

import (
	"context"
	"io"
)

// LossPolicy is an internal route-conformance control. Its zero value rejects
// any semantic loss. The public Adapter intentionally always uses that strict
// policy; AllowDocumentedLoss exists only for characterization and lower-level
// route tests.
type LossPolicy uint8

const (
	RejectSemanticLoss LossPolicy = iota
	AllowDocumentedLoss
)

type ExchangeMetadata struct {
	RequestID                 string
	ClientModel               string
	UpstreamModel             string
	Stream                    bool
	StreamSet                 bool
	ChatStreamIncludeUsage    bool
	ChatStreamIncludeUsageSet bool
}

type ConversionOptions struct {
	Exchange   ExchangeMetadata
	LossPolicy LossPolicy
}

type ConversionResult struct {
	Body        []byte
	Diagnostics []Diagnostic
}

type RouteSpec struct {
	ID         string
	From       Protocol
	To         Protocol
	StreamMode RouteMode
}

// Route is one directed request route; responses travel in the reverse
// direction. Implementations live in protocol-pair packages.
type Route interface {
	Specification() RouteSpec
	ToUpstreamRequest(context.Context, []byte, ConversionOptions) (ConversionResult, error)
	ToClientResponse(context.Context, []byte, ConversionOptions) (ConversionResult, error)
	NewClientStream(context.Context, ConversionOptions) (ResponseStream, error)
}

type RoutePlan struct {
	From       Protocol
	To         Protocol
	Converter  Route
	StreamMode RouteMode
}

type Catalog interface {
	Plan(Protocol, Protocol) (RoutePlan, error)
	ConvertRequest(context.Context, RoutePlan, []byte, ConversionOptions) (ConversionResult, error)
	ConvertResponse(context.Context, RoutePlan, []byte, ConversionOptions) (ConversionResult, error)
	NewResponseStream(context.Context, RoutePlan, ConversionOptions) (ResponseStream, error)
}

type RequestHint struct {
	Model  string
	Stream *bool
}

type RequestPatch struct {
	Model  *string
	Stream *bool
}

type RequestInfo struct {
	Model  string
	Stream bool
}

type ProtocolError struct {
	Type       string
	Code       string
	Message    string
	RequestID  string
	StatusCode int
}

type EncodedError struct {
	ContentType string
	Body        []byte
}

type Codec interface {
	ValidateRequest(context.Context, []byte, RequestHint) error
	PatchRequest(context.Context, []byte, RequestPatch) ([]byte, error)
	ValidateResponse(context.Context, []byte) error
	NewStreamDecoder(io.Reader, StreamOptions) (StreamDecoder, error)
	NewStreamEncoder(io.Writer, StreamOptions) (StreamEncoder, error)
	EncodeError(ProtocolError) (EncodedError, error)
	EncodeStreamError(ProtocolError) (Frame, error)
}

type StreamOptions struct {
	MaxFrameBytes int
	Flusher       interface{ Flush() }
}

type Frame struct {
	Event string
	Data  []byte
	Done  bool
}

type StreamDecoder interface {
	Next(context.Context) (Frame, error)
}

type StreamEncoder interface {
	Write(context.Context, Frame) error
	Flush() error
}

type ResponseStream interface {
	Convert(context.Context, Frame) ([]Frame, []Diagnostic, error)
	Finalize(context.Context) ([]Frame, []Diagnostic, error)
}
