# Inject RouteMorphSDK into official provider SDKs

`Adapter.HTTPClient` turns a RouteMorph adapter into a standard `*http.Client`.
The examples in this module inject that client into the official OpenAI,
Anthropic, and Gemini Go SDKs. Application code continues to use each
provider's typed request and response types while RouteMorph converts the HTTP
request to the configured upstream protocol and converts the response back.

This is a nested Go module so the main RouteMorphSDK module remains
standard-library-only.

## Run

Configure one upstream. `UPSTREAM_MODEL` rewrites the example's client-facing
`client-model` alias to a real provider model:

```bash
cd examples/provider-sdks

export UPSTREAM_PROTOCOL=responses
export UPSTREAM_BASE_URL=https://api.openai.com/v1
export UPSTREAM_API_KEY='...'
export UPSTREAM_MODEL='your-upstream-model'

go run ./openai
go run ./anthropic
go run ./gemini
```

`UPSTREAM_PROTOCOL` accepts `chat`, `responses`, `messages`, or `gemini`.
`CLIENT_MODEL` defaults to `client-model`; set it to the actual upstream model
if `UPSTREAM_MODEL` is omitted. An empty `UPSTREAM_API_KEY` is allowed for an
unauthenticated local upstream.

Each example configures its provider SDK with a `.invalid` base URL and routes
the request through `adapter.HTTPClient()`. The ingress SDK credential is
either disabled or a documented placeholder. RouteMorph strips ingress
credentials and applies only the key configured on the upstream adapter.

## Supported SDK operations

The injected client recognizes only the four inference endpoint families that
RouteMorph implements:

- OpenAI Chat Completions and Responses;
- Anthropic Messages;
- Gemini Developer API `generateContent` and `streamGenerateContent`.

Other endpoints such as files, model listing, embeddings, Anthropic token
counting/batches, and Gemini caches/live APIs fail with
`routemorph.ErrUnsupported` before any network request. Gemini Vertex AI uses a
different URL shape and is not supported by this bridge; configure
`genai.BackendGeminiAPI` as shown.

OpenAI and Anthropic retries are disabled in these examples so a deterministic
request-conversion error is returned once. Applications may choose a retry
policy appropriate for their upstream. The returned HTTP client has no
whole-request timeout so it does not terminate long-running streams; use
context deadlines instead.
