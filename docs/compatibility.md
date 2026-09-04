# Cross-protocol compatibility

This document describes RouteMorphSDK's built-in routing contract. Provider
APIs evolve, so additions require implementation and conformance tests rather
than an assumption that similarly named fields are equivalent.

## Route matrix

Rows are ingress/client protocols and columns are upstream protocols.

| Ingress ↓ / Upstream → | Chat | Responses | Messages | Gemini |
|---|---:|---:|---:|---:|
| Chat | native | incremental | buffered | buffered |
| Responses | incremental | native | buffered | incremental |
| Messages | buffered | incremental | native | buffered |
| Gemini | buffered | incremental | buffered | native |

The matrix contains twelve explicit cross-protocol routes plus four native
same-protocol routes:

- **native** validates and relays the provider's own protocol, preserving the
  body byte-for-byte unless an explicit model override requires rewriting;
- **incremental** converts SSE frames through a route-specific state machine;
- **buffered** validates and collects the complete source stream before
  conversion, with a 32 MiB aggregate limit and a
  `buffered_stream_conversion` diagnostic.

There is no multi-hop or intermediate-protocol fallback.

## Capability summary

| Capability | Chat | Responses | Messages | Gemini | Cross-protocol policy |
|---|---:|---:|---:|---:|---|
| Text turns | yes | yes | yes | yes | preserved |
| System/developer instruction | message | `instructions` or message | top-level `system` | `systemInstruction` | text retained; non-text instructions rejected when no equivalent exists |
| Image input | URL/data | URL/data/file ID | URL/base64 | inline/file URI | retained only when the destination supports the source form |
| Audio and general files | partial | file/audio items | document blocks | inline/file parts | rejected when the destination cannot represent the source |
| Function declarations | yes | yes | yes | yes | portable name, description and schema retained |
| Function calls/results | tool messages | input/output items | content blocks | parts | call IDs and JSON object arguments retained |
| Structured output | `response_format` | `text.format` | `output_config.format` | `responseJsonSchema` / `responseSchema` | retained or normalized only when target semantics are known |
| Reasoning/thinking | provider-specific | native | thinking blocks/config | thinking config/parts | public text may map; opaque signatures are not assumed equivalent |
| Usage/cache tokens | yes | yes | yes | yes | common counters normalized; provider-only counters may be unavailable |
| Native streaming | yes | yes | yes | yes | protocol-native relay |

The table is a capability overview, not a promise that every provider extension
or every combination of fields can be converted.

## Fail-closed cases

Cross-protocol conversion returns `ErrUnsupported` when accepting a field would
silently change its meaning. Important examples include:

- Responses conversations, previous-response state, reusable prompts,
  background jobs, context-management state and prompt-cache breakpoints;
- provider-hosted tools such as web search, file search, computer use, code
  execution, MCP and vendor-versioned server tools;
- Responses encrypted reasoning, output annotations or output log probabilities
  that have no destination representation;
- Anthropic container reuse, citations, cache controls and cache pre-warming
  requests (`max_tokens: 0`) outside a native Messages route;
- Gemini cached content, provider safety policy, grounding metadata and
  provider-only executable parts outside a native Gemini route;
- signed, encrypted or redacted reasoning sent to a protocol without equivalent
  provenance semantics;
- custom stop sequences when the destination is Responses;
- non-object function arguments and unknown content or output item types;
- Chat responses with multiple choices or Gemini responses with multiple
  candidates;
- a requested logprob representation that the destination cannot preserve.

Malformed inputs return `ErrInvalidPayload`. Invalid successful provider
responses and unsupported provider terminal states return
`ErrUpstreamResponse`. Errors may be inspected with `errors.Is` and
`errors.As` to obtain `*routemorph.ConversionError`, including its protocol,
JSON path and reason.

Native same-protocol routing does not apply cross-protocol loss checks, but it
still validates the request envelope and transport boundary.

## Terminal reasons

Portable successful outcomes map to `stop`, `length`, `tool_calls` or
`content_filter` as supported by the destination protocol.

- Anthropic `pause_turn` requires native continuation semantics and fails
  closed during conversion.
- Gemini safety and block reasons map to content filtering where the
  destination has that outcome.
- Responses `incomplete` distinguishes token limits and content filters when
  `incomplete_details` is present.
- Unknown failure or terminal reasons are not converted into a fabricated
  successful assistant response.

## Usage accounting

Anthropic `input_tokens` excludes cache-read and cache-creation tokens, while
the SDK's private common accounting is inclusive. Conversion subtracts these
counters when emitting Messages usage and adds them when reading Messages
usage. Counters that have no destination field are not invented.

## Diagnostics

`Response.Meta` reports the ingress protocol, upstream protocol, streaming flag
and selected route mode. `Response.Meta.Diagnostics()` returns a detached,
concurrency-safe snapshot. For streams, the final snapshot is available after
the body reaches EOF.

Known Responses output phases such as `final_answer` and `commentary` are
accepted. A destination without a phase field receives a
`responses_output_phase_not_representable` diagnostic. Unknown phases still
fail closed.

Diagnostics describe non-fatal, observable approximations; they do not turn an
unsupported semantic conversion into a successful one.

## Limits and hosting responsibilities

The SDK enforces 32 MiB bounds on request bodies, non-streaming provider bodies,
individual SSE frames and buffered stream fallbacks. It rejects redirects,
binds upstream credentials to the configured adapter and scopes provider
control headers to a small per-provider allowlist.

It does not provide caller authentication, tenant authorization, TLS
termination, QPS/concurrency rate limiting, retry policy, circuit breaking or
provider quota management. A production gateway must supply those controls.

