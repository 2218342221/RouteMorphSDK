package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/2218342221/RouteMorphSDK/examples/provider-sdks/internal/exampleconfig"
	"google.golang.org/genai"
)

func main() {
	adapter, err := exampleconfig.Adapter()
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Backend:    genai.BackendGeminiAPI,
		APIKey:     "intercepted-by-routemorph",
		HTTPClient: adapter.HTTPClient(),
		HTTPOptions: genai.HTTPOptions{
			BaseURL:    "https://routemorph.invalid",
			APIVersion: "v1beta",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	response, err := client.Models.GenerateContent(
		ctx,
		exampleconfig.ClientModel(),
		genai.Text("Say hello in five words."),
		nil,
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(response.Text())
}
