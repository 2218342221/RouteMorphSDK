// Package routemorph provides embeddable HTTP adapters among OpenAI Chat
// Completions, OpenAI Responses, Anthropic Messages, and Gemini
// generateContent. An adapter constructor selects the upstream protocol and an
// adapter method selects the client protocol. The package owns request and
// response conversion, authentication, HTTP relay, and streaming, is
// independent from the RouteMorph server, and has no third-party dependencies.
package routemorph
