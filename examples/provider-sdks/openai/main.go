package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/2218342221/RouteMorphSDK/examples/provider-sdks/internal/exampleconfig"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func main() {
	adapter, err := exampleconfig.Adapter()
	if err != nil {
		log.Fatal(err)
	}
	client := openai.NewClient(
		// This URL only gives the OpenAI SDK a safe endpoint shape. The injected
		// client intercepts supported requests before any network lookup.
		option.WithBaseURL("https://routemorph.invalid/v1"),
		option.WithAPIKey("intercepted-by-routemorph"),
		option.WithHTTPClient(adapter.HTTPClient()),
		option.WithMaxRetries(0),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: exampleconfig.ClientModel(),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("Say hello in five words."),
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	if len(completion.Choices) == 0 {
		log.Fatal("response has no choices")
	}
	fmt.Println(completion.Choices[0].Message.Content)
}
