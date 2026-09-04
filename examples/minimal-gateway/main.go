package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	routemorph "github.com/2218342221/RouteMorphSDK"
)

type adapterCall func(context.Context, *routemorph.Request) (*routemorph.Response, error)

func main() {
	adapter, err := newAdapter()
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("POST /v1/chat/completions", relay(adapter.OpenAIChatCompletions))
	mux.HandleFunc("POST /v1/responses", relay(adapter.OpenAIResponses))
	mux.HandleFunc("POST /v1/messages", relay(adapter.AnthropicMessages))
	mux.HandleFunc("POST /v1/models/", relay(adapter.GeminiGenerateContent))
	mux.HandleFunc("POST /v1beta/models/", relay(adapter.GeminiGenerateContent))

	server := &http.Server{
		Addr:              getenv("LISTEN_ADDR", "127.0.0.1:8080"),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("RouteMorph minimal gateway listening on http://%s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func newAdapter() (*routemorph.Adapter, error) {
	protocol, err := routemorph.ParseProtocol(getenv("UPSTREAM_PROTOCOL", "responses"))
	if err != nil {
		return nil, err
	}
	baseURL := os.Getenv("UPSTREAM_BASE_URL")
	if baseURL == "" {
		return nil, errors.New("UPSTREAM_BASE_URL is required")
	}
	apiKey := os.Getenv("UPSTREAM_API_KEY")
	options := make([]routemorph.Option, 0, 1)
	if model := os.Getenv("UPSTREAM_MODEL"); model != "" {
		options = append(options, routemorph.WithModel(model))
	}
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

func relay(call adapterCall) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		response, err := call(request.Context(), &routemorph.Request{
			Header: request.Header,
			URL:    request.URL,
			Body:   request.Body,
		})
		if err != nil {
			writeError(writer, adapterErrorStatus(err), err)
			return
		}
		if err := response.WriteTo(writer); err != nil {
			log.Printf("relay %s: %v", request.URL.Path, err)
		}
	}
}

func adapterErrorStatus(err error) int {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout
	case errors.Is(err, routemorph.ErrInvalidPayload), errors.Is(err, routemorph.ErrUnsupported):
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

func writeError(writer http.ResponseWriter, status int, err error) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]string{"type": "adapter_error", "message": err.Error()},
	})
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
