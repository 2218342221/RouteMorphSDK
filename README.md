# RouteMorphSDK

RouteMorphSDK is an embeddable, standard-library-only Go SDK for relaying requests
among four LLM HTTP protocols:

- OpenAI Chat Completions
- OpenAI Responses
- Anthropic Messages
- Gemini `generateContent` / `streamGenerateContent`

An adapter constructor selects the **upstream protocol**. The method called on
that adapter selects the **client protocol**. Request conversion, upstream
authentication, HTTP transport, response conversion, and SSE processing remain
inside the SDK.

## Requirements and installation

- Go 1.25.13 or newer
- No third-party dependencies

```bash
go get github.com/2218342221/RouteMorphSDK@latest
```

The module path is `github.com/2218342221/RouteMorphSDK` and the Go package name
is `routemorph`:

```go
import routemorph "github.com/2218342221/RouteMorphSDK"
```

The module root contains only the public facade. Protocol codecs, route-pair
implementations, stream state machines, transport policy, and white-box test
suites live under `internal/` according to their ownership boundaries.

## API at a glance

The public API is intentionally small:

- four constructors select the upstream protocol;
- four methods on `*Adapter` select the ingress/client protocol;
- `Request` and `Response` are public-owned HTTP boundary types;
- `InspectRequest` and `PrepareRequest` expose the model and streaming flag for
  provider selection without exposing internal protocol DTOs;
- `EncodeError` creates an ingress-native error envelope;
- `WithModel` is the only constructor option;
- typed error categories, `ConversionError` and `ResponseMeta.Diagnostics()`
  expose failures and non-fatal approximations.

The project ships no public route registry, provider abstraction or canonical
request/response model.

## Quick start

This example accepts an OpenAI Chat Completions body and sends it to an OpenAI
Responses upstream:

```go
adapter, err := routemorph.NewOpenAIResponsesAdapter(
    "https://api.openai.com/v1",
    os.Getenv("OPENAI_API_KEY"),
)
if err != nil {
    return err
}

response, err := adapter.OpenAIChatCompletions(ctx, &routemorph.Request{
    Header: http.Header{"X-Request-ID": {"request-123"}},
    Body: strings.NewReader(`{
        "model":"your-model",
        "messages":[{"role":"user","content":"Hello"}]
    }`),
})
if err != nil {
    return err
}
defer response.Body.Close()

_, err = io.Copy(os.Stdout, response.Body)
return err
```

For an HTTP handler, `Response.WriteTo` relays the status, headers, body, stream
flushes, and trailers, and closes the response body:

```go
response, err := adapter.AnthropicMessages(request.Context(), &routemorph.Request{
    Header: request.Header,
    URL:    request.URL,
    Body:   request.Body,
})
if err != nil {
    http.Error(writer, err.Error(), http.StatusBadGateway)
    return
}
if err := response.WriteTo(writer); err != nil {
    log.Printf("relay response: %v", err)
}
```

## Constructors

| Constructor | Upstream endpoint |
|---|---|
| `NewOpenAIChatCompletionsAdapter` | `POST /v1/chat/completions` |
| `NewOpenAIResponsesAdapter` | `POST /v1/responses` |
| `NewAnthropicMessagesAdapter` | `POST /v1/messages` |
| `NewGeminiGenerateContentAdapter` | `POST /v1beta/models/{model}:generateContent` or `:streamGenerateContent` |

Every constructor returns the same concrete `*Adapter` type:

```go
adapter, err := routemorph.NewOpenAIResponsesAdapter(baseURL, apiKey)
response, err := adapter.OpenAIChatCompletions(ctx, request)
```

`baseURL` may be an origin such as `https://api.openai.com` or include the API
version such as `https://api.openai.com/v1`. It must not contain credentials,
query parameters, or a fragment. An empty API key is allowed for local upstreams
without authentication.

## Request contract

```go
type Request struct {
    Header http.Header
    URL    *url.URL
    Body   io.Reader
}
```

- `Body` contains the unmodified JSON document in the client protocol.
- `URL` describes the client-facing endpoint. It is required for Gemini ingress
  because the model and stream mode are encoded in the path; it is optional for
  OpenAI and Anthropic ingress.
- Client URL query parameters are not forwarded to the upstream.
- The SDK consumes, but never closes, `Body`; it does not mutate `Header` or
  `URL`.
- Request bodies, buffered responses, and individual SSE frames are limited to
  32 MiB.

Example Gemini URL:

```go
requestURL, _ := url.Parse(
    "/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse",
)
```

Gateways that inspect a request before selecting an adapter can use
`InspectRequest`, or avoid parsing the JSON twice with `PrepareRequest`:

```go
prepared, info, err := routemorph.PrepareRequest(ctx, protocol, request.URL, body)
if err != nil {
    return err
}
prepared.Header = request.Header
// Select the provider with info.Model, then pass prepared to the adapter.
```

Prepared metadata is reused only when the protocol, URL path, and body digest
still match. If the caller changes the request, the adapter performs normal
validation again. Prepared metadata is an optimization, not an authentication
token.

## Model override

`Option` is reserved for constructor-level behavior. `WithModel` is the only
supported option in this release:

```go
adapter, err := routemorph.NewAnthropicMessagesAdapter(
    baseURL,
    apiKey,
    routemorph.WithModel("provider-model-name"),
)
```

The configured model replaces the client model on the upstream request. Model
fields in upstream responses and stream events are restored to the original
client model. If `WithModel` is repeated, the last value wins.

## Response and errors

```go
type Response struct {
    StatusCode int
    Header     http.Header
    Body       io.ReadCloser
    Trailer    http.Header
    Meta       ResponseMeta
}
```

- Successful non-streaming responses are converted before the adapter method
  returns.
- Streaming responses are converted lazily while `Body` is read. Closing Body
  cancels the upstream request.
- Upstream 4xx/5xx responses return a non-nil `Response` with the original
  status and an error envelope in the client protocol.
- Redirects are not followed and return a 502 client-protocol response.
- Invalid input, unsupported semantic conversion, transport failure, and
  invalid successful upstream output return a Go error.
- A conversion failure after streaming begins emits the client protocol's error
  event and is also returned by `Body.Read` or `WriteTo`.
- `Response.Meta.Diagnostics()` is concurrency-safe. Stream diagnostics are
  complete after Body reaches EOF.

Cross-protocol conversion rejects unsupported semantic loss. Native requests
remain pass-through unless `WithModel` requires model rewriting.

## Route matrix: 12 + 4

Rows are ingress/client protocols and columns are upstream protocols:

| Ingress ↓ / Upstream → | Chat | Responses | Messages | Gemini |
|---|---:|---:|---:|---:|
| Chat | native | incremental | buffered | buffered |
| Responses | incremental | native | buffered | incremental |
| Messages | buffered | incremental | native | buffered |
| Gemini | buffered | incremental | buffered | native |

The matrix comprises twelve explicit, single-hop cross-protocol converters plus
four native same-protocol routes. There is no graph search or intermediate
protocol fallback. Incremental routes convert SSE frames as they arrive;
buffered routes enforce terminal validation and a 32 MiB aggregate bound before
rendering the target stream.

See [Cross-protocol compatibility](docs/compatibility.md) for field-level
behavior and fail-closed cases.

## Resource limits and failure policy

The 32 MiB bound applies independently to:

- each client request body;
- each non-streaming provider response body;
- each individual SSE frame;
- the aggregate input to a buffered stream conversion.

The default HTTP client has a 30-minute response-header timeout. This does not
cap the total lifetime of a streaming response. Cancelling the request context
or closing `Response.Body` cancels the upstream request.

These are memory and parsing limits, not traffic rate limits. RouteMorphSDK
does not implement QPS, concurrency, token-budget or provider-quota limiting;
the hosting gateway must apply those controls.

## Header trust and authentication

The adapter starts from a copy of safe end-to-end request headers. It removes
hop-by-hop headers and names nominated by `Connection`, `Host`,
`Content-Length`, cookies, content-coding negotiation and all inbound provider
credentials before installing the adapter's configured authentication:

| Upstream | Adapter-installed or required headers |
|---|---|
| OpenAI Chat / Responses | `Authorization: Bearer ...` when an API key is configured |
| Anthropic Messages | `X-API-Key` when configured; `Anthropic-Version` defaults to `2023-06-01` |
| Gemini | `X-Goog-API-Key` when an API key is configured |

Provider-control headers are scoped to the selected upstream. All other
`OpenAI-*`, `Anthropic-*` and `X-Goog-*` controls are dropped:

| Upstream | Trusted caller-supplied controls |
|---|---|
| OpenAI Chat / Responses | `OpenAI-Organization`, `OpenAI-Project`, `OpenAI-Beta` |
| Anthropic Messages | `Anthropic-Version`, `Anthropic-Beta` |
| Gemini | `X-Goog-User-Project` |

For Anthropic, a caller-supplied `Anthropic-Version` is preserved; otherwise
the SDK uses `2023-06-01`. The allowlist prevents cross-provider control-header
leakage, but it is not tenant authorization. A hosting gateway must decide
whether its callers may set even these allowlisted values.

## Examples

- [`examples/direct-call`](examples/direct-call) performs one Chat Completions
  request through a Responses upstream and prints the converted response.
- [`examples/minimal-gateway`](examples/minimal-gateway) is a runnable minimal
  conversion service. It exposes all four client endpoints and can target any
  one of the four upstream protocols.

```bash
export OPENAI_API_KEY='...'
go run ./examples/direct-call

# Configure one upstream, then call any supported endpoint on 127.0.0.1:8080.
UPSTREAM_PROTOCOL=responses \
UPSTREAM_BASE_URL=https://api.openai.com/v1 \
UPSTREAM_API_KEY="$OPENAI_API_KEY" \
go run ./examples/minimal-gateway
```

The direct example accepts `OPENAI_BASE_URL`. The gateway accepts
`UPSTREAM_PROTOCOL`, `UPSTREAM_BASE_URL`, `UPSTREAM_API_KEY`, `LISTEN_ADDR`, and
optional `UPSTREAM_MODEL`; see its directory README for details.

## Development

All commands run from this directory without the parent repository:

```bash
make check
```

`BenchmarkBuiltinRoutes` measures request and response conversion for all 12
ordered cross-protocol routes. `BenchmarkBuiltinRouteStreams` measures their
stream conversion paths.

See [Architecture](docs/architecture.md) for package ownership and
[Contributing](CONTRIBUTING.md) for protocol-change requirements.

## Security

RouteMorphSDK is a library, not a complete internet-facing gateway. The host
must provide caller authentication, tenant authorization, TLS termination,
network egress policy, rate limiting, secret management and operational retry
policy. See [SECURITY.md](SECURITY.md) for reporting and trust boundaries.

## License and provenance

RouteMorphSDK is distributed under the [MIT License](LICENSE).
[Third-party notices](THIRD_PARTY_NOTICES.md) transparently record protocol
compatibility research, including comparison with the AGPL-3.0-licensed
`QuantumNous/new-api` project. The notice describes provenance and is not a
legal guarantee.
