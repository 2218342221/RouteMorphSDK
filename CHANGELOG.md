# Changelog

All notable changes to RouteMorphSDK will be documented in this file.

The project follows [Semantic Versioning](https://semver.org/). Until the first
stable release, minor versions may include deliberate API changes documented
here.

## [Unreleased]

### Added

- Updated protocol schemas and conversion coverage for OpenAI Go SDK v3.56.0
  (`f5b985771236464300abc9395c1c4abda0d65c53`),
  Anthropic Go SDK v1.70.1, and Gemini Go SDK v1.71.0 / Discovery revision
  20260829.
- Added non-streaming OpenAI Chat↔Responses conversion for the shared custom
  tool declaration, choice, call, and text-output shapes, including text and
  grammar formats.
- Added non-streaming Chat `web_search_options` ↔ unversioned Responses
  `web_search` conversion for shared context-size and approximate-location
  fields, with diagnostics for non-representable web-search lifecycle output.
- Explicit unsupported-feature documentation and regression coverage for new
  request, response, usage, and stream extensions.

### Changed

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
- Validate portable function-tool histories as a call/result ledger: unique
  call IDs, preceding references, single consumption, matching names, required
  arguments/output, and protocol-valid roles.
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
