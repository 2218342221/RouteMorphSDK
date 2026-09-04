// Package builtin owns the immutable catalog of built-in direct routes.
package builtin

import (
	"context"
	"fmt"

	core "github.com/2218342221/RouteMorphSDK/internal/core"
)

type routeKey struct{ from, to core.Protocol }

type routeExpectation struct {
	id   string
	mode core.RouteMode
}

type CodecFor func(core.Protocol) (core.Codec, error)

type Catalog struct {
	routes   map[routeKey]core.RoutePlan
	codecFor CodecFor
}

func New(routes []core.Route, codecFor CodecFor) (*Catalog, error) {
	if codecFor == nil {
		return nil, fmt.Errorf("%w: codec factory is nil", core.ErrInvalidPlan)
	}
	want := expectedRoutes()
	registered := make(map[routeKey]core.RoutePlan, len(want))
	for _, route := range routes {
		if route == nil {
			return nil, fmt.Errorf("%w: nil route", core.ErrInvalidPlan)
		}
		spec := route.Specification()
		key := routeKey{from: spec.From, to: spec.To}
		expectation, expected := want[key]
		if !expected || expectation.id != spec.ID || expectation.mode != spec.StreamMode {
			return nil, fmt.Errorf("%w: unexpected route %s", core.ErrInvalidPlan, spec.ID)
		}
		if _, duplicate := registered[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate route %s -> %s", core.ErrInvalidPlan, spec.From, spec.To)
		}
		registered[key] = core.RoutePlan{From: spec.From, To: spec.To, Converter: route, StreamMode: spec.StreamMode}
	}
	if len(registered) != len(want) {
		return nil, fmt.Errorf("%w: got %d routes, want %d", core.ErrInvalidPlan, len(registered), len(want))
	}
	return &Catalog{routes: registered, codecFor: codecFor}, nil
}

func expectedRoutes() map[routeKey]routeExpectation {
	protocols := []core.Protocol{
		core.ProtocolChat,
		core.ProtocolResponses,
		core.ProtocolMessages,
		core.ProtocolGenerateContent,
	}
	names := map[core.Protocol]string{
		core.ProtocolChat:            "chat",
		core.ProtocolResponses:       "responses",
		core.ProtocolMessages:        "messages",
		core.ProtocolGenerateContent: "gemini",
	}
	expected := make(map[routeKey]routeExpectation, len(protocols)*(len(protocols)-1))
	for _, from := range protocols {
		for _, to := range protocols {
			if from == to {
				continue
			}
			expected[routeKey{from: from, to: to}] = routeExpectation{
				id:   names[from] + "_to_" + names[to],
				mode: expectedStreamMode(from, to),
			}
		}
	}
	return expected
}

// expectedStreamMode is deliberately independent from the route registration
// table. Catalog construction therefore fails when a registration silently
// omits a pair or advertises a mode that differs from the public matrix.
func expectedStreamMode(from, to core.Protocol) core.RouteMode {
	if from == core.ProtocolResponses && (to == core.ProtocolChat || to == core.ProtocolGenerateContent) ||
		to == core.ProtocolResponses && (from == core.ProtocolChat || from == core.ProtocolMessages || from == core.ProtocolGenerateContent) {
		return core.RouteModeIncremental
	}
	return core.RouteModeBuffered
}

func (c *Catalog) Plan(from, to core.Protocol) (core.RoutePlan, error) {
	if !from.Valid() || !to.Valid() {
		return core.RoutePlan{}, fmt.Errorf("%w: %s -> %s", core.ErrRouteNotFound, from, to)
	}
	if from == to {
		return core.RoutePlan{From: from, To: to, StreamMode: core.RouteModeNative}, nil
	}
	plan, ok := c.routes[routeKey{from: from, to: to}]
	if !ok {
		return core.RoutePlan{}, fmt.Errorf("%w: %s -> %s", core.ErrRouteNotFound, from, to)
	}
	return plan, nil
}

func (c *Catalog) ConvertRequest(ctx context.Context, plan core.RoutePlan, body []byte, options core.ConversionOptions) (core.ConversionResult, error) {
	if plan.From == plan.To {
		codec, err := c.codecFor(plan.From)
		if err != nil {
			return core.ConversionResult{}, err
		}
		patch := core.RequestPatch{Model: optionalString(options.Exchange.UpstreamModel), Stream: optionalStream(options.Exchange)}
		if patch.Model == nil && patch.Stream == nil {
			if err := codec.ValidateRequest(ctx, body, core.RequestHint{Model: options.Exchange.ClientModel}); err != nil {
				return core.ConversionResult{}, err
			}
			return core.ConversionResult{Body: append([]byte(nil), body...)}, nil
		}
		patched, err := codec.PatchRequest(ctx, body, patch)
		return core.ConversionResult{Body: patched}, err
	}
	result, err := plan.Converter.ToUpstreamRequest(ctx, body, options)
	if err != nil {
		return core.ConversionResult{}, fmt.Errorf("converter %s request: %w", plan.Converter.Specification().ID, err)
	}
	return result, nil
}

func (c *Catalog) ConvertResponse(ctx context.Context, plan core.RoutePlan, body []byte, options core.ConversionOptions) (core.ConversionResult, error) {
	if plan.From == plan.To {
		return core.ConversionResult{Body: append([]byte(nil), body...)}, nil
	}
	result, err := plan.Converter.ToClientResponse(ctx, body, options)
	if err != nil {
		return core.ConversionResult{}, fmt.Errorf("converter %s response: %w", plan.Converter.Specification().ID, err)
	}
	return result, nil
}

func (c *Catalog) NewResponseStream(ctx context.Context, plan core.RoutePlan, options core.ConversionOptions) (core.ResponseStream, error) {
	if plan.From == plan.To {
		return &passthroughStream{}, nil
	}
	stream, err := plan.Converter.NewClientStream(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("converter %s stream: %w", plan.Converter.Specification().ID, err)
	}
	return stream, nil
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func optionalStream(exchange core.ExchangeMetadata) *bool {
	if !exchange.StreamSet && !exchange.Stream {
		return nil
	}
	return &exchange.Stream
}

type passthroughStream struct{ finalized bool }

func (s *passthroughStream) Convert(_ context.Context, input core.Frame) ([]core.Frame, []core.Diagnostic, error) {
	if s.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", core.ErrInvalidPlan)
	}
	return []core.Frame{{Event: input.Event, Data: append([]byte(nil), input.Data...), Done: input.Done}}, nil, nil
}
func (s *passthroughStream) Finalize(context.Context) ([]core.Frame, []core.Diagnostic, error) {
	if s.finalized {
		return nil, nil, fmt.Errorf("%w: stream already finalized", core.ErrInvalidPlan)
	}
	s.finalized = true
	return nil, nil, nil
}
