package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	routemorph "github.com/2218342221/RouteMorphSDK"
)

func main() {
	baseURL := getenv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	adapter, err := routemorph.NewOpenAIResponsesAdapter(baseURL, os.Getenv("OPENAI_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := adapter.OpenAIChatCompletions(ctx, &routemorph.Request{
		Header: http.Header{"X-Request-ID": {"routemorph-example"}},
		Body: strings.NewReader(`{
			"model": "your-model",
            "messages": [{"role": "user", "content": "Say hello in five words."}]
        }`),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer response.Body.Close()

	fmt.Printf("HTTP %d\n", response.StatusCode)
	if _, err := io.Copy(os.Stdout, response.Body); err != nil {
		log.Fatal(err)
	}
	fmt.Println()
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
