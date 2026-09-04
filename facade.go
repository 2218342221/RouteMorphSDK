package routemorph

import (
	"context"
	"errors"
	"fmt"
	"time"

	builtin "github.com/2218342221/RouteMorphSDK/internal/builtin"
	relayx "github.com/2218342221/RouteMorphSDK/internal/relay"
	transportx "github.com/2218342221/RouteMorphSDK/internal/transport"
)

const (
	defaultAdapterMaxBodyBytes = transportx.DefaultMaxBodyBytes
	// This limits only the wait for response headers; it does not cap a
	// streaming response after headers arrive.
	defaultAdapterResponseHeaderTimeout = 30 * time.Minute
)

// http.Client and its Transport are safe for concurrent use and should be
// reused so independently constructed adapters share connection pools.
var defaultAdapterHTTPClient = transportx.NewClient(defaultAdapterResponseHeaderTimeout)

var errUninitializedAdapter = errors.New("routemorph: adapter is nil or uninitialized")

// Adapter relays any supported client protocol to the upstream protocol fixed
// by its constructor. Its fields are private so every instance is validated by
// one of the four protocol-specific constructors.
type Adapter struct {
	relay *relayx.Service
}

// Option is intentionally sealed. WithModel is the only supported option in
// this release; the interface leaves room for compatible additions later.
type Option interface {
	apply(*adapterConfig) error
}

type optionFunc func(*adapterConfig) error

func (f optionFunc) apply(config *adapterConfig) error { return f(config) }

// WithModel fixes the model sent to the upstream. Responses are mapped back to
// the model supplied by the client request.
func WithModel(model string) Option {
	return optionFunc(func(config *adapterConfig) error {
		if model == "" {
			return errors.New("upstream model must not be empty")
		}
		if containsControl(model) {
			return errors.New("upstream model must not contain control characters")
		}
		config.model = model
		return nil
	})
}

type adapterConfig struct {
	model string
}

// NewOpenAIChatCompletionsAdapter creates an adapter whose upstream speaks the
// OpenAI Chat Completions protocol.
func NewOpenAIChatCompletionsAdapter(baseURL, apiKey string, opts ...Option) (*Adapter, error) {
	return newAdapter(ProtocolChat, baseURL, apiKey, opts...)
}

// NewOpenAIResponsesAdapter creates an adapter whose upstream speaks the
// OpenAI Responses protocol.
func NewOpenAIResponsesAdapter(baseURL, apiKey string, opts ...Option) (*Adapter, error) {
	return newAdapter(ProtocolResponses, baseURL, apiKey, opts...)
}

// NewAnthropicMessagesAdapter creates an adapter whose upstream speaks the
// Anthropic Messages protocol.
func NewAnthropicMessagesAdapter(baseURL, apiKey string, opts ...Option) (*Adapter, error) {
	return newAdapter(ProtocolMessages, baseURL, apiKey, opts...)
}

// NewGeminiGenerateContentAdapter creates an adapter whose upstream speaks the
// Gemini generateContent protocol.
func NewGeminiGenerateContentAdapter(baseURL, apiKey string, opts ...Option) (*Adapter, error) {
	return newAdapter(ProtocolGenerateContent, baseURL, apiKey, opts...)
}

func newAdapter(upstream Protocol, rawBaseURL, apiKey string, options ...Option) (*Adapter, error) {
	baseURL, err := transportx.ParseBaseURL(rawBaseURL)
	if err != nil {
		return nil, err
	}
	config := adapterConfig{}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("option %d is nil", index)
		}
		if err := option.apply(&config); err != nil {
			return nil, fmt.Errorf("option %d: %w", index, err)
		}
	}
	router, err := builtin.Default()
	if err != nil {
		return nil, fmt.Errorf("construct route catalog: %w", err)
	}
	service := relayx.New(relayx.Config{
		Upstream: toCoreProtocol(upstream), BaseURL: baseURL, APIKey: apiKey, Model: config.model,
		Client: defaultAdapterHTTPClient, Catalog: router, MaxBodyBytes: defaultAdapterMaxBodyBytes,
	})
	return &Adapter{relay: service}, nil
}

// OpenAIChatCompletions relays an OpenAI Chat Completions request.
func (a *Adapter) OpenAIChatCompletions(ctx context.Context, request *Request) (*Response, error) {
	return a.invoke(ctx, ProtocolChat, request)
}

// OpenAIResponses relays an OpenAI Responses request.
func (a *Adapter) OpenAIResponses(ctx context.Context, request *Request) (*Response, error) {
	return a.invoke(ctx, ProtocolResponses, request)
}

// AnthropicMessages relays an Anthropic Messages request.
func (a *Adapter) AnthropicMessages(ctx context.Context, request *Request) (*Response, error) {
	return a.invoke(ctx, ProtocolMessages, request)
}

// GeminiGenerateContent relays a Gemini generateContent request.
func (a *Adapter) GeminiGenerateContent(ctx context.Context, request *Request) (*Response, error) {
	return a.invoke(ctx, ProtocolGenerateContent, request)
}

func (a *Adapter) invoke(ctx context.Context, ingress Protocol, request *Request) (*Response, error) {
	if a == nil || a.relay == nil {
		return nil, errUninitializedAdapter
	}
	response, err := a.relay.Invoke(ctx, toCoreProtocol(ingress), toRelayRequest(request))
	return fromRelayResponse(response), toPublicError(err)
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
