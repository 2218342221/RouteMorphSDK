package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/2218342221/RouteMorphSDK/examples/provider-sdks/internal/exampleconfig"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func main() {
	adapter, err := exampleconfig.Adapter()
	if err != nil {
		log.Fatal(err)
	}
	client := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		// This URL only gives the Anthropic SDK a safe endpoint shape. The
		// injected client intercepts supported requests before any network lookup.
		option.WithBaseURL("https://routemorph.invalid"),
		option.WithHTTPClient(adapter.HTTPClient()),
		option.WithMaxRetries(0),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     exampleconfig.ClientModel(),
		MaxTokens: 128,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Say hello in five words.")),
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, block := range message.Content {
		if block.Type == "text" {
			fmt.Print(block.Text)
		}
	}
	fmt.Println()
}
