package exampleconfig

import (
	"errors"
	"os"

	routemorph "github.com/2218342221/RouteMorphSDK"
)

// Adapter builds the RouteMorph upstream selected by the shared example
// environment variables.
func Adapter() (*routemorph.Adapter, error) {
	protocol, err := routemorph.ParseProtocol(Getenv("UPSTREAM_PROTOCOL", "responses"))
	if err != nil {
		return nil, err
	}
	baseURL := os.Getenv("UPSTREAM_BASE_URL")
	if baseURL == "" {
		return nil, errors.New("UPSTREAM_BASE_URL is required")
	}
	options := make([]routemorph.Option, 0, 1)
	if model := os.Getenv("UPSTREAM_MODEL"); model != "" {
		options = append(options, routemorph.WithModel(model))
	}
	apiKey := os.Getenv("UPSTREAM_API_KEY")
	switch protocol {
	case routemorph.ProtocolChat:
		return routemorph.NewOpenAIChatCompletionsAdapter(baseURL, apiKey, options...)
	case routemorph.ProtocolResponses:
		return routemorph.NewOpenAIResponsesAdapter(baseURL, apiKey, options...)
	case routemorph.ProtocolMessages:
		return routemorph.NewAnthropicMessagesAdapter(baseURL, apiKey, options...)
	case routemorph.ProtocolGenerateContent:
		return routemorph.NewGeminiGenerateContentAdapter(baseURL, apiKey, options...)
	default:
		return nil, errors.New("unsupported upstream protocol")
	}
}

// ClientModel is the model name encoded by the ingress provider SDK. Set
// UPSTREAM_MODEL when this is only a client-facing alias.
func ClientModel() string {
	return Getenv("CLIENT_MODEL", "client-model")
}

func Getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
