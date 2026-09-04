package stream

import (
	"context"
	"errors"
	"fmt"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

type conversionOptions = core.ConversionOptions
type conversionResult = core.ConversionResult

// bufferedStreamFinalizer is owned by a protocol pair. The generic buffer does
// not know how to interpret or render protocol payloads.
type bufferedStreamFinalizer func(context.Context, []streamFrame) ([]streamFrame, []Diagnostic, error)
type bufferedResponseMapper func(context.Context, []byte, conversionOptions) (conversionResult, error)

// boundedFrameStream provides only bounded frame collection and lifecycle
// enforcement for routes that cannot convert incrementally.
type boundedFrameStream struct {
	protocol Protocol
	maxBytes int64
	buffer   *Buffer
	finalize bufferedStreamFinalizer
}

const defaultMaxBufferedStreamBytes int64 = 32 << 20

func (c *boundedFrameStream) Convert(_ context.Context, input streamFrame) ([]streamFrame, []Diagnostic, error) {
	err := c.buffer.Add(Frame(input))
	if errors.Is(err, ErrFinalized) {
		return nil, nil, fmt.Errorf("%w: stream already finalized", core.ErrInvalidPlan)
	}
	if errors.Is(err, ErrLimitExceeded) {
		return nil, nil, invalid(c.protocol, "$", "buffered stream exceeds the %d-byte limit", c.maxBytes)
	}
	return nil, nil, nil
}

func (c *boundedFrameStream) Finalize(ctx context.Context) ([]streamFrame, []Diagnostic, error) {
	buffered, err := c.buffer.Finalize()
	if errors.Is(err, ErrFinalized) {
		return nil, nil, fmt.Errorf("%w: stream already finalized", core.ErrInvalidPlan)
	}
	if c.finalize == nil {
		return nil, nil, fmt.Errorf("%w: buffered stream finalizer is nil", core.ErrInvalidPlan)
	}
	frames := make([]streamFrame, len(buffered))
	for index, frame := range buffered {
		frames[index] = streamFrame(frame)
	}
	return c.finalize(ctx, frames)
}

func NewBufferedRoute(spec core.RouteSpec, options core.ConversionOptions, mapper func(context.Context, []byte, core.ConversionOptions) (core.ConversionResult, error)) core.ResponseStream {
	return &boundedFrameStream{
		protocol: spec.To,
		maxBytes: defaultMaxBufferedStreamBytes,
		buffer:   NewBuffer(defaultMaxBufferedStreamBytes),
		finalize: func(ctx context.Context, frames []streamFrame) ([]streamFrame, []Diagnostic, error) {
			return finalizeBufferedRoute(ctx, spec.From, spec.To, options, mapper, frames)
		},
	}
}

func NewBoundedFrameStream(protocol Protocol, maxBytes int64, finalize bufferedStreamFinalizer) core.ResponseStream {
	return &boundedFrameStream{protocol: protocol, maxBytes: maxBytes, buffer: NewBuffer(maxBytes), finalize: finalize}
}

// finalizeBufferedRoute composes the source-native collector, pair mapper, and
// target-native renderer. The generic buffer remains protocol agnostic.
func finalizeBufferedRoute(ctx context.Context, from, to Protocol, options conversionOptions, mapper bufferedResponseMapper, frames []streamFrame) ([]streamFrame, []Diagnostic, error) {
	if mapper == nil {
		return nil, nil, fmt.Errorf("%w: buffered response mapper is nil", core.ErrInvalidPlan)
	}
	sourceBody, diagnostics, err := CollectNativeResponse(to, frames, options.LossPolicy)
	if err != nil {
		return nil, diagnostics, err
	}
	mapped, err := mapper(ctx, sourceBody, options)
	if err != nil {
		return nil, diagnostics, err
	}
	diagnostics = append(diagnostics, mapped.Diagnostics...)
	outputFrames, encodedDiagnostics, err := RenderNativeResponse(from, mapped.Body)
	if err != nil {
		return nil, diagnostics, err
	}
	diagnostics = append(diagnostics, encodedDiagnostics...)
	diagnostics = appendDiagnostic(diagnostics, "warning", "buffered_stream_conversion", "$", "route emitted a valid stream after buffering upstream events")
	return outputFrames, diagnostics, nil
}
