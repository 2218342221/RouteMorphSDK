# Changelog

All notable changes to RouteMorphSDK will be documented in this file.

The project follows [Semantic Versioning](https://semver.org/). Until the first
stable release, minor versions may include deliberate API changes documented
here.

## [Unreleased]

### Added

- Added a credential-safe catalog of 63 independently maintained JSON request
  fixtures for Responses-provider end-to-end regression: core 32, extended 18,
  and tools 13. Opt-in live runners expose separate
  `test-live-responses-core`, `test-live-responses-extended`, and
  `test-live-responses-tools` targets plus the combined 63-call
  `test-live-responses` target; deterministic tests validate every fixture and
  its local public-adapter conversion without provider credentials.
- Added `Adapter.HTTPClient` for injecting RouteMorph protocol conversion into
  the official OpenAI, Anthropic, and Gemini Go SDKs, with fail-closed endpoint
  routing and HTTP transport contract tests.
- Added a nested `examples/provider-sdks` module with compiling examples and a
  local end-to-end interoperability test for all three official SDKs.
- Updated protocol schemas and conversion coverage for OpenAI Go SDK v3.56.0
  (`f5b985771236464300abc9395c1c4abda0d65c53`),
  Anthropic Go SDK v1.70.1, and Gemini Go SDK v1.71.0 / Discovery revision
  20260829.
- Added non-streaming OpenAI Chat↔Responses conversion for the shared custom
  tool declaration, choice, call, and text-output shapes, including text and
  grammar formats.
- Added exact `allowed_tools` conversion, declaration-aware named/required
  choice validation, and safe all-functions lowering across Chat, Responses,
  Messages, and Gemini.
- Added non-streaming Chat `web_search_options` ↔ unversioned Responses
  `web_search` conversion for shared context-size and approximate-location
  fields, with diagnostics for non-representable web-search lifecycle output.
- Added ordered multimodal tool-result conversion for the compatible
  Responses↔Messages and Responses/Messages↔Gemini image/PDF intersections.
- Explicit unsupported-feature documentation and regression coverage for new
  request, response, usage, and stream extensions.

### Changed

- Encode prior assistant/model text as Responses `output_text` on the
  Chat-, Messages-, and Gemini-to-Responses routes. This preserves the semantic
  distinction from user `input_text` and matches the live provider's accepted
  multi-turn request shape. Chat assistant history carrying an input-only
  `prompt_cache_breakpoint` now fails closed instead of emitting an invalid
  Responses output-text part.
- Accept Responses streams that resolve `service_tier` from `auto` to the
  concrete terminal tier, and diagnose omission of provider-selected
  `auto`/`default` tiers when converting responses to Messages or Gemini.
- Accept provider-compatible `response.function_call_arguments.done` events
  without a duplicate `name` when the function identity is established by the
  output-item lifecycle.
- Accept the provider-returned `status` lifecycle on `custom_tool_call` items
  and validate `response.custom_tool_call_input.delta`/`done` without
  reclassifying custom calls as ordinary function calls.
- Accept Responses-compatible server `tool_search_call` and
  `tool_search_output` items whose `call_id` and `execution` are omitted or
  null. Non-null values remain validated, and explicit client execution still
  requires a correlation ID. Tool discovery remains Responses-native and
  fails closed at cross-protocol boundaries.
- Gemini stream failures now close with the typed read error without emitting a
  JSON error payload that the official Gemini SDK interprets as an empty
  successful response.
- Preserve newly compatible prompt-cache, moderation, citation, schema,
  service-tier, usage, and Anthropic/Gemini fields where semantics match.
- Treat Responses Create message content as text/image/file only; reject
  `input_audio`, provider-scoped file identifiers, unsupported media MIME/URI
  shapes, and unrepresentable media detail rather than relabeling or dropping
  them.
- Document Responses `tool_search`, `tool_search_call`, `tool_search_output`,
  and `additional_tools` as native-only tool discovery, and
  `configuration_update` as native-only persistent configuration state. Split
  thinking compatibility into request control, visible text, opaque replay
  state, and usage accounting.
- Document leading text-only instruction promotion, Gemini role and remote-MIME
  constraints, and the distinction between strictly rejected Gemini
  logprob/grounding output and diagnostic-only omission of response safety and
  fine-grained usage metadata.
- Fail closed or emit an explicit diagnostic for provider-only fields instead
  of silently dropping them; provider-issued reasoning signatures are never
  forged.
- Preserve Chat `reasoning_content` and unsigned Gemini thoughts as Responses
  raw `reasoning_text`; keep Responses summaries distinct from raw reasoning.
- Validate portable function-tool histories as a call/result ledger: unique
  call IDs, preceding references, single consumption, matching names, required
  arguments/output, and protocol-valid roles.
- Deep-validate Responses v3.56 output-item and provider tool-definition
  unions, media URL/base64 sources, partial SSE additions, terminal item
  reconciliation, and buffered terminal statuses before emitting events.
- Validate every upstream stream against its source protocol before native
  relay or cross-protocol conversion. Enforce Responses SSE creation, sequence,
  item/part, delta/done, and terminal lifecycles; pin buffered Chat/Gemini
  identities; and render Chat `include_usage` with a separate final usage-only
  chunk.

## [v0.1.0] - 2026-09-04

### Added

- Standalone Go module packaging, release documentation and CI.
- Four protocol-specific adapter constructors and four ingress methods.
- Twelve direct cross-protocol routes and four native same-protocol routes.
- Optional request preinspection through `InspectRequest` and `PrepareRequest`.
- Bounded incremental and buffered stream conversion with typed errors and
  diagnostics.

[v0.1.0]: https://github.com/2218342221/RouteMorphSDK/releases/tag/v0.1.0
