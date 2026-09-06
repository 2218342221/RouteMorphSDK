# RouteMorphSDK architecture

RouteMorphSDK is an embeddable Go library for relaying HTTP requests among four
LLM protocol families:

- OpenAI Chat Completions;
- OpenAI Responses;
- Anthropic Messages;
- Gemini `generateContent` and `streamGenerateContent`.

The project deliberately keeps provider selection outside the library. An
adapter constructor fixes one upstream protocol, while the method invoked on
that adapter identifies the ingress protocol.

## Request and response flow

```text
hosting gateway
    │
    ├── optional InspectRequest / PrepareRequest
    │
    ▼
public Adapter facade
    │
    ▼
internal/relay ───────── request lifetime and response ownership
    │
    ├── internal/transport ── endpoint, header and HTTP policy
    ├── internal/builtin ──── immutable route catalog
    ├── internal/codec ────── protocol envelope validation and SSE framing
    └── internal/route/* ──── pair-specific semantic conversion
                                  │
                                  ▼
                            provider HTTP API
```

Provider SDKs have a second ingress path: `Adapter.HTTPClient` installs an
unexported `http.RoundTripper` that classifies a supported inference endpoint
and invokes the same public adapter route. It maps the converted `Response`
back to `http.Response` without pre-reading streaming bodies. Unsupported SDK
endpoints fail before network I/O; this layer does not turn RouteMorph into a
general-purpose provider transport.

The response follows the reverse route. Non-streaming responses are converted
before the adapter method returns. Streaming responses are converted as the
caller reads `Response.Body`, except for routes whose compatibility policy
requires a bounded, terminal-validated buffer.

## Public boundary

The root package name is `routemorph`. Its public surface is intentionally
small:

- `Protocol`, `ParseProtocol` and the four protocol constants;
- `NewOpenAIChatCompletionsAdapter`, `NewOpenAIResponsesAdapter`,
  `NewAnthropicMessagesAdapter` and `NewGeminiGenerateContentAdapter`;
- `Adapter.OpenAIChatCompletions`, `OpenAIResponses`,
  `AnthropicMessages` and `GeminiGenerateContent`;
- `Adapter.HTTPClient` for injection into provider SDKs that accept a custom
  `*http.Client`;
- `Request`, `Response`, `ResponseMeta` and `Diagnostic`;
- `InspectRequest`, `PrepareRequest` and `EncodeError`;
- `WithModel`, the explicit `WithCodingAgentCompatibility` policy, and the
  documented error categories.

`Request` and `Response` are owned by the public package rather than aliases of
internal relay types. That keeps the API contract stable while transport and
conversion internals evolve. Callers that use `Response.WriteTo` transfer body
copying, streaming flushes, trailer publication and body closure to the SDK.
Other callers must close `Response.Body` themselves.

The HTTP client bridge owns and closes the provider SDK request body, as
required by `http.RoundTripper`, while ownership of the returned response body
passes to the provider SDK. It recognizes only POST requests for the four
supported inference endpoint families. The returned client deliberately has no
whole-request timeout; request contexts bound both non-streaming and streaming
calls.

## Request inspection and prepared requests

`InspectRequest` validates a protocol-native body and extracts only the model
and stream flag needed for provider selection. `PrepareRequest` performs the
same validation and returns a `Request` carrying an opaque cache of that
inspection.

The cache is not a bearer credential or a general trust token. Before reusing
it, the relay compares:

1. the ingress protocol;
2. the URL's escaped path;
3. the SHA-256 digest of the complete request body.

Any mismatch causes a full reinspection. Headers are assigned independently
and still pass through transport filtering. This arrangement avoids duplicate
JSON parsing without allowing stale request metadata after a body or path
change.

## Route model: 12 + 4

Four protocols produce twelve ordered cross-protocol pairs. Each pair has one
explicit, single-hop converter. There is no graph search, intermediate
protocol fallback, public plugin registry or user-defined conversion plan.

The catalog also handles four same-protocol routes in native mode. Native
requests and responses remain protocol-native; the request is only rewritten
when an explicit option such as `WithModel` requires it.

For a cross-protocol route `A -> B`, request conversion maps A to B, while
response and stream conversion map B back to A. Catalog construction validates
the complete matrix, route identity, stream mode and duplicate registration.
See [Compatibility](compatibility.md) for the route matrix.

## Ownership boundaries

- `internal/core` defines private route, stream, error and semantic contracts.
- `internal/builtin` constructs and shares the immutable route catalog.
- `internal/codec` validates protocol envelopes, inspects requests, emits
  protocol-native errors and owns shared SSE framing.
- `internal/relay` orchestrates one request and owns response lifetime,
  diagnostics and conversion selection.
- `internal/transport` owns base URL validation, endpoint generation, safe
  headers, HTTP behavior, bounded reads and cancellation-aware bodies.
- `internal/wire/{chat,responses,messages,gemini}` owns protocol JSON shapes.
- `internal/route/<pair>` contains the six pair packages. A pair owns semantic
  compatibility for both directions and does not import another pair.
- `internal/chatresponsesstream` owns the incremental Chat-to-Responses state
  machine while remaining independent from pair-package implementation details.
- `internal/routekit` contains protocol-neutral mechanics such as strict JSON
  decoding, function-argument normalization, data URL handling and diagnostic
  construction. It does not decide whether a protocol feature is semantically
  portable.
- `internal/stream` owns protocol-native collection/rendering, terminal-state
  validation and the bounded buffered-route lifecycle.
- `internal/jsonx` and `internal/schema` provide narrow validation utilities.
- `internal/conformance` exercises the route matrix and architectural
  invariants. Root tests protect the public facade.

Dependencies point inward from the public facade. Wire packages do not import
each other, and no global public intermediate request/response model couples
the four protocols.

## Streaming

Incremental converters are explicit, per-request state machines. One source
frame may produce zero, one or several destination frames. Finalization is
single-use and validates that the source reached a supported terminal state.

Buffered routes collect at most 32 MiB, validate the terminal event, apply the
same fail-closed semantic policy as non-streaming conversion, then render the
destination stream. They add a `buffered_stream_conversion` diagnostic so
callers can observe the latency tradeoff.

The default HTTP client waits at most 30 minutes for response headers. That
setting does not impose a total duration on a streaming body. Closing the
public response body or cancelling the context cancels the upstream request.
After a stream has started, Chat, Responses, and Messages can carry a
protocol-native error event. Gemini cannot; its body closes with the typed read
error and does not emit a JSON error envelope that the official SDK would
misread as an empty successful chunk.

## Transport and header trust

The transport starts from copied end-to-end client headers, then removes:

- hop-by-hop headers and names nominated by `Connection`;
- `Host` and `Content-Length`;
- client credentials (`Authorization`, `Proxy-Authorization`, `X-API-Key`
  and `X-Goog-API-Key`);
- cookies and content-coding negotiation.

It then drops provider-control headers that are not allowlisted for the selected
upstream:

| Upstream | Trusted client controls |
|---|---|
| OpenAI Chat / Responses | `OpenAI-Organization`, `OpenAI-Project`, `OpenAI-Beta` |
| Anthropic Messages | `Anthropic-Version`, `Anthropic-Beta` |
| Gemini | `X-Goog-User-Project` |

Finally, it installs the adapter's configured credentials and protocol-required
headers. Cross-provider controls are never forwarded. This is a provider-header
trust boundary, not gateway authentication or authorization: the hosting
service must decide whether its caller may set even the allowlisted controls.

Converted response headers and trailers have stale representation metadata such as
`Content-Length`, `Content-Encoding`, `ETag`, `Content-MD5` and `Digest`
removed. Redirects are not followed.

## Failure and resource policy

Cross-protocol conversion accepts exact mappings and explicitly documented,
observable approximations. Unknown or unrepresentable semantics fail with a
typed error and protocol/JSON-path context. Invalid successful provider
payloads and unsupported terminal states never become fabricated successful
model output.

The 32 MiB limit applies to each client request body, non-streaming provider
body, individual SSE frame and aggregate buffered fallback. These are memory
and parsing safeguards. RouteMorphSDK does not implement request-rate,
concurrency, token-budget or provider-quota limiting; those policies belong to
the hosting gateway.
