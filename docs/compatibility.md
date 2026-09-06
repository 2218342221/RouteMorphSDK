# Cross-protocol compatibility

This document describes RouteMorphSDK's built-in routing contract. Provider
APIs evolve, so additions require implementation and conformance tests rather
than an assumption that similarly named fields are equivalent.

## Audit baseline

The field audit current on 2026-09-05 was performed against these official Go
SDK snapshots:

- OpenAI `github.com/openai/openai-go/v3` v3.56.0
  (`f5b985771236464300abc9395c1c4abda0d65c53`);
- Anthropic `github.com/anthropics/anthropic-sdk-go` v1.70.1
  (`e9c104e7e5fb80a26ff26e398c0e4e3fe1fe7f33`);
- Gemini `google.golang.org/genai` v1.71.0
  (`bfe8a871478bdec16932d7809e8f8bff122569d3`) and the public v1beta
  Discovery schema revision `20260829`.

The SDK snapshots are the schema baseline, not evidence that every provider
model accepts every SDK field. The field audit used local conversion and stream
fixtures. A separate opt-in live suite validates a bounded common subset against
a configured Responses provider; see [Live provider testing](live-testing.md)
and the dated reports under [`test-reports`](test-reports/).

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
| System/developer instruction | message | `instructions` or message | top-level `system` | `systemInstruction` | only leading text-only instruction messages can be promoted to a destination top-level instruction; non-text or interleaved instruction messages are rejected |
| Image input | user URL/data URL | `input_image` URL/file ID | URL/base64/file ID | `inlineData`/`fileData` | retained only for a destination-supported source, MIME, role, and detail value |
| Audio input | user inline WAV/MP3 | no Create message-content member | no generic audio block | inline data or provider file URI | only the Chat/Gemini inline WAV/MP3 intersection is portable |
| General file input | user file ID or data | `input_file` ID/URL/data | document sources | inline data or provider file URI | filenames are required when needed to infer or preserve MIME; provider-scoped identifiers do not cross APIs |
| Function declarations | yes | yes | yes | yes | portable name, description and schema retained |
| Function calls/results | tool messages | input/output items | content blocks | parts | call IDs and JSON object arguments retained; result media follows the route-specific matrix below |
| OpenAI custom tools | custom tool/call | `custom`, `custom_tool_call` | no equivalent | no equivalent | non-streaming Chat↔Responses common subset only |
| Web search request | `web_search_options` | hosted `web_search` tools | provider server tool | Google search/retrieval tools | only non-streaming Chat↔unversioned Responses common fields map |
| Responses tool discovery | no | `tool_search`, `tool_search_call`, `tool_search_output`, `additional_tools` | no exact equivalent | no exact equivalent | Responses-native discovery declarations and items |
| Responses persistent configuration | no | `configuration_update` | no exact equivalent | no exact equivalent | Responses-native conversation state |
| Structured output | `response_format` | `text.format` | `output_config.format` | `responseJsonSchema` / `responseSchema` | retained or normalized only when target semantics are known |
| Reasoning request control | `reasoning_effort` | `reasoning` | adaptive/enabled thinking plus output config | thinking level/budget/include flag | only explicitly audited effort/level intersections map; exact budgets and display/include policies are not interchangeable |
| Visible reasoning text | non-standard `reasoning_content` | reasoning summary/text items | thinking blocks | thought parts | mapped only on routes with a compatible visible-text representation |
| Opaque reasoning state | none | encrypted content | signature/redacted data | `thoughtSignature` | provider-issued replay state never crosses providers |
| Usage/cache tokens | yes | yes | yes | yes | common counters normalized; provider-only counters may be unavailable |
| Native streaming | yes | yes | yes | yes | protocol-native relay |

The table is a capability overview, not a promise that every provider extension
or every combination of fields can be converted. See the dedicated
[unsupported feature inventory](unsupported.md) for explicit boundaries.

## OpenAI custom and search tools

The OpenAI SDK name is **custom tool**. Responses uses the item types
`custom_tool_call` and `custom_tool_call_output`; there is no official
`customized_tool_call` item type.

Chat↔Responses conversion supports the following custom-tool intersection only
for non-streaming requests and responses:

- declaration name and description;
- text format, or grammar format with a non-empty `definition` and `syntax` of
  `lark` or `regex`;
- named custom `tool_choice`;
- call ID, name, and arbitrary string input in request history and model output;
- matching text tool output.

The `allowed_tools` form of `tool_choice` also maps bidirectionally. Chat's
`{"type":"allowed_tools","allowed_tools":{"mode":...,"tools":[...]}}`
shape becomes Responses' flat
`{"type":"allowed_tools","mode":...,"tools":[...]}` shape, and vice
versa. The portable subset has these invariants:

- `mode` is `auto` or `required`;
- `tools` is non-empty and contains only `function` or `custom` name
  references;
- each `(type, name)` pair is unique and resolves to a same-type declaration
  in the request.

Hosted-tool references are not accepted as `allowed_tools` entries on this
route. If the allowed set includes a custom tool, the custom-tool
non-streaming restriction still applies.

When lowering a choice to Messages or Gemini, only declared function tools
participate. Messages can represent the complete required set as `any` (also
when that complete set has one member) and a one-function proper subset as a
named choice; a larger proper subset is unsupported. Gemini can preserve any
non-empty required subset as `ANY` plus `allowedFunctionNames`. For `auto`, an
allowed set equal to every declared function becomes unrestricted
`auto`/`AUTO`; a proper subset has no exact target semantics and fails closed.
In the reverse direction, Gemini `ANY` over all declarations becomes
Chat/Responses `required` or Messages `any`; a one-function proper subset
becomes named, while a larger proper subset requires Chat/Responses
`allowed_tools` and is unsupported by Messages. `required`/`any` without a
declared function and named references to an undeclared function are malformed
rather than silently broadened. Provider-native custom, hosted, or discovery
choices remain `ErrUnsupported` on routes without their union. Gemini
`VALIDATED` is a known but non-portable mode and is also `ErrUnsupported`;
unknown modes are malformed.

Portable function/custom declaration names must be unique. Missing Chat tool
discriminators, conflicting tagged-choice members, malformed known nested
choice/configuration shapes, and duplicate declaration names return
`ErrInvalidPayload` instead of being normalized into a different request.

Chat streaming chunks expose function-tool deltas but no official custom-tool
delta shape, so custom tools, calls, and outputs fail closed whenever the
converted request is streaming. Responses `async`, `defer_loading`,
`allowed_callers`, `output_schema`, prompt-cache breakpoint, program caller,
and namespace semantics have no Chat equivalent. A Responses
`caller:{"type":"direct"}` is accepted as the implicit Chat caller; a separate
Responses output-item ID may be omitted only with a diagnostic. Structured or
multimodal custom-tool output also fails when targeting Chat because a Chat
tool message can carry only text in this mapping.

Chat `web_search_options` maps to one unversioned Responses `web_search` tool,
and back, for this non-streaming request subset:

- `search_context_size`: `low`, `medium`, or `high`;
- approximate `user_location`: city, country, region, and timezone.

The versioned Responses `web_search_2025_08_26` tool, cache-only
`external_web_access:false`, non-empty domain filters, multiple web-search
tools, and streaming citation conversion are not portable. Explicit
`external_web_access:true` becomes Chat's default online-search behavior. A
Responses `web_search_call` lifecycle item is not synthesized as a Chat tool
call: when portable output accompanies it, that output is retained and the
lifecycle item produces `responses_web_search_call_not_representable`; a
lifecycle-only response fails closed.

Responses `tool_search`, `tool_search_call`, `tool_search_output`, and
`additional_tools` are native Responses tool-discovery capabilities. They are
not rewritten as function calls: in particular, `tool_search_call.arguments`
is arbitrary JSON rather than the JSON-encoded string used by an OpenAI
function call. `configuration_update` is a different category: it is
native-only persisted conversation configuration for later responses and
cannot be collapsed into either a discovery item or the current request's
top-level `reasoning` field.
Anthropic and Gemini hosted-search tools likewise retain provider-specific
execution and citation semantics and are not mapped to the OpenAI subset.

On a native Responses route, valid custom-tool, web-search, and tool-search
objects remain Responses objects; native request/response relay does not
rewrite them. When a complete native Responses body must be rendered as SSE,
the renderer emits `response.output_item.added` before item-specific events and
`response.output_item.done` afterward. It emits custom input delta/done events
for `custom_tool_call`, web-search progress/searching/completed events when
applicable, and generic output-item added/done events for `tool_search_call`,
`tool_search_output`, and `additional_tools`, which have no dedicated delta
event family. `custom_tool_call_output` is likewise a complete output item, not
a function-call delta. This native support does not imply a cross-protocol
mapping.

Native Responses validation covers the known v3.56 output-item and discovered
tool unions, including nested callable, namespace, MCP, file-search,
code-interpreter, shell, image-generation, web-search, tool-search, and
apply-patch fields. An `output_item.added` frame is checked before it is
released, while still allowing fields that are legitimately absent until the
item is complete. A terminal response must contain the same indexed items and
completed payloads observed in the stream, and buffered rendering refuses to
turn an `in_progress`, `searching`, `generating`, `interpreting`, or `calling`
item into a fabricated `done` event. Unknown top-level provider extension
fields remain pass-through; unknown tagged variants fail closed.

Portable function-tool history is validated as a ledger rather than copied as
unrelated blocks. Call IDs must be unique, every result must reference a
preceding unconsumed call, and a call can be answered only once. Where a source
result also carries a function name, it must match the declaration on the
referenced call. Required call arguments and result output must be present;
orphaned calls/results, duplicate results, and role-invalid tool blocks are
malformed payloads rather than silently repaired history.

Gemini may omit a function-call ID. RouteMorph then generates a request- or
response-local ID while avoiding every explicit ID visible in that payload. In
an incremental Gemini stream, a later explicit ID can still collide with an ID
already generated for an earlier chunk; the explicit call receives a new
stable Responses ID and a `remapped_function_call_id` diagnostic instead of
producing duplicate call IDs or rejecting the otherwise valid stream.

## Multimodal input boundaries

Multimodal conversion checks the complete source shape rather than relabeling a
payload by media type:

Known Chat content parts and Messages content/source unions are validated from
their raw JSON before decoding. Fields from a different tagged variant are
malformed even when their value is empty; unreviewed nested extensions fail
closed instead of disappearing from the destination request.

| Route pair | Portable ordinary input | Portable tool result |
|---|---|---|
| Chat ↔ Responses | user text, common image URL/data forms, and common file ID/data forms | text only |
| Chat ↔ Messages | user text, common JPEG/PNG/GIF/WebP images, and portable PDF forms | text only |
| Chat ↔ Gemini | user text, common images/files, and inline WAV/MP3 | text only |
| Responses ↔ Messages | text, common JPEG/PNG/GIF/WebP images, and portable PDF forms | ordered text/image/PDF content supported when source, detail, filename, and error semantics are representable |
| Responses ↔ Gemini | text and common image/file forms; Responses Create has no audio part | text-only result, or ordered media-only inline JPEG/PNG/GIF/WebP/PDF result |
| Messages ↔ Gemini | text, common JPEG/PNG/GIF/WebP images, and portable PDF forms | text-only result, or ordered media-only inline JPEG/PNG/GIF/WebP/PDF result |

- Chat permits multimodal content only in user messages. Images use a URL or
  data URL with `detail` `auto`, `low`, or `high`; audio is inline base64
  WAV/MP3; files use either a provider file ID or file data. A filename is
  required when a route must infer or preserve the file's MIME type.
  System, developer, assistant, and tool messages use their protocol-specific
  text/refusal shapes rather than the user multimodal union.
- Responses Create message content supports `input_text`, `input_image`, and
  `input_file`; it does **not** support `input_audio`. `input_image` selects one
  of URL or file ID and also admits `detail:"original"`, which Chat cannot
  represent. `input_file` selects one of file ID, file URL, or file data and
  has `detail` `auto`, `low`, or `high`; file detail has no Chat equivalent. A
  filename is required when raw file data must carry type information to the
  destination. Image and file URLs must be absolute HTTP(S) URLs; inline image
  data URLs and `file_data` must contain valid base64 rather than merely having
  the right JSON type.
- Messages images accept JPEG, PNG, GIF, or WebP base64, URL, or provider file
  ID sources. Documents include portable PDF forms plus provider-specific
  source forms; there is no generic Messages audio block. A Messages URL source
  has no `media_type` member. When a destination such as Gemini requires one,
  conversion derives it only from the block kind and a recognized URL
  extension; an unknown type is rejected rather than guessed.
- Gemini `inlineData` carries base64 plus a MIME type. `fileData.fileUri` is a
  provider URI, not an arbitrary public URL. This implementation sends only
  `gs://`, Google Cloud Storage HTTPS, or Gemini Files API HTTPS URIs to Gemini.
  A remote URL sent to Gemini must also have a MIME type that the converter can
  determine from the source protocol or a recognized filename/URL extension;
  it is rejected rather than emitted with a guessed or empty MIME type.
  When converting away from Gemini, only Google Cloud Storage HTTPS URLs are
  considered portable; `gs://` and Gemini Files API URIs remain provider
  scoped. Gemini also accepts MIME types and media kinds that Chat, Responses,
  or Messages may not accept.

Responses↔Messages can preserve an ordered tool-result content list containing
text plus common inline/URL images and PDF documents when both sides support
the exact source form. Messages `is_error`, OpenAI media detail, filenames, and
provider-scoped file IDs have no exact counterpart and fail closed.

Gemini function responses have a narrower portable media intersection than
ordinary message input. Responses `function_call_output` and Messages
`tool_result` can map to/from `functionResponse.parts` only when the result is
an ordered, media-only list of inline JPEG, PNG, GIF, WebP, or PDF data. For a
Responses result, the Gemini `response` object must then be empty. A Messages
result may instead use an empty error marker (`{"error":""}` or
`{"error":null}`) together with media to preserve `is_error:true`. Text-only
results use a single
`{"output":...}` or `{"error":...}` member; sibling members are rejected so
that marker interpretation cannot discard data. A text/media mixture cannot
preserve its relative order in Gemini's split representation and therefore
fails closed. Function-response `fileData`, audio/video MIME types, filenames,
detail controls, provider file IDs, and remote URLs are also outside this exact
intersection. Chat tool results remain text-only and are wrapped as
`{"output":"..."}` when sent to Gemini so JSON-looking text stays text.

For Chat and Responses inline file data, the MIME type comes from a data URL
or, when needed, is inferred from the filename extension. Chat↔Responses emits
the data-URL form whenever a MIME type is known, so the type survives the
conversion. Gemini `inlineData` and `fileData` require a bare IANA media type;
MIME parameters such as `charset` are not portable here.

Gemini's Go type exposes `displayName`, but the generateContent API baseline
used by this project does not support that field. Cross-protocol Gemini input
therefore rejects it, and an OpenAI filename that cannot otherwise be preserved
also fails closed instead of being sent as an unsupported Gemini property.

Provider file IDs and scoped file URIs are never treated as cross-provider
identifiers. Base64, MIME presence and route-specific allowlists, source
exclusivity, any conditionally required filename, URL shape, and detail values
are validated. An unsupported MIME, URI, role, output media type, or detail
control fails closed instead of being relabeled (for example, OGG is not
emitted as WAV and video is not emitted as a Messages document). Chat assistant
audio and provider-specific multimodal model output also require native routing
when the destination has no equivalent envelope.

Gemini request roles are validated explicitly: ordinary `contents` accepts an
empty role (treated as user), `user`, or `model`; `systemInstruction.role`
accepts only empty or `user`. A successful Gemini response candidate must use
an empty role or `model`. When Chat, Responses, or Messages instruction
messages are promoted into Messages `system` or Gemini `systemInstruction`,
only a leading run of text-only `system`/`developer` messages is portable; an
instruction message after a conversational turn fails closed.

## Reasoning and thinking boundaries

Reasoning support is intentionally split into four independent layers:

1. **Request control.** Effort, thinking level, exact token budget,
   adaptive/enabled mode, display policy, and `includeThoughts` are not assumed
   equivalent. The public adapter maps only route-specific audited
   intersections and rejects a lossy approximation.
2. **Visible reasoning text.** Chat `reasoning_content` is a non-standard
   extension; Responses summary/reasoning text, Messages thinking text, and
   Gemini thought text map only where the target has a compatible visible-text
   field.
3. **Opaque replay state.** Responses `encrypted_content`, Anthropic thinking
   signatures/redacted data, and Gemini `thoughtSignature` are
   provider-issued. They are never forged, translated, or silently discarded.
   Calls sent to Gemini from another protocol therefore report
   `gemini_thought_signature_unavailable` rather than inventing a signature.
4. **Usage.** Reasoning/thinking token counters map only where the destination
   counter has the same accounting semantics; their presence does not make the
   associated control or opaque state portable.

The three exact cross-provider effort intersections are:

| Protocol pair | Exact bidirectional mapping | Values outside the intersection |
|---|---|---|
| OpenAI Chat/Responses ↔ Messages | `low`, `medium`, `high`, `xhigh`, `max` map unchanged | OpenAI `none`/`minimal`; Messages adaptive/enabled thinking and exact token budgets |
| OpenAI Chat/Responses ↔ Gemini | `minimal`, `low`, `medium`, `high` ↔ `MINIMAL`, `LOW`, `MEDIUM`, `HIGH` | OpenAI `none`/`xhigh`/`max`; Gemini exact budgets and `includeThoughts` |
| Messages ↔ Gemini | `low`, `medium`, `high` ↔ `LOW`, `MEDIUM`, `HIGH` | Messages `xhigh`/`max`, Gemini `MINIMAL`, adaptive/enabled thinking, exact budgets, and `includeThoughts` |

Within OpenAI, Chat↔Responses additionally maps every audited effort value
unchanged: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`.
Responses reasoning `summary` is a separate request policy and is not inferred
from any of these effort mappings.

For visible output, Chat `reasoning_content` maps to Responses raw
`reasoning_text`, not to `summary_text`; the same raw-text interpretation is
used for an unsigned Gemini `thought` when converting to or from Responses.
Responses summaries are therefore rejected by the public strict policy instead
of being relabeled as hidden chain-of-thought. Provider-issued encrypted text
or Gemini thought signatures remain native-only.

## Directed-route boundaries

The public adapter always rejects semantic loss. The following table records
the important fields that remain intentionally unsupported even when both
protocols expose similarly named concepts. An omitted field is not implicitly
supported. Unknown top-level fields and unknown tagged-union variants are
rejected by the route validators; arbitrary future fields nested inside every
known object should not be assumed either portable or recursively rejected
until that object is explicitly audited.

| Route | Preserved or normalized | Intentionally unsupported |
|---|---|---|
| Chat ↔ Responses | common messages, function calls/results, non-streaming custom declarations/calls/text results, JSON schema and JSON object output, reasoning effort, metadata where the target envelope supports it, compatible service tiers, request-side prompt-cache options/breakpoints, moderation configuration and single-result response moderation, the non-streaming unversioned web-search request subset, non-streaming URL citations, common token counters and the stream-obfuscation request switch | Chat stop sequences; Responses state/background/prompt/context fields; frequency/presence penalties; custom tools in streams and custom async/program/namespace/output-schema semantics; Responses `tool_search`/`additional_tools` and all other hosted tools; versioned/filter/cache-only web search; streaming/non-URL annotations; Responses Create `input_audio`; incompatible media sources/detail; terminal prompt-cache options; audio/prediction/cache-write-only usage; multiple choices/moderation results; Responses-only `ultrafast`; Responses metadata in Chat streams; preserving generated SSE padding |
| Chat ↔ Messages | text/media portable to both APIs, function tools, stop sequences, JSON schema, `safety_identifier` ↔ `metadata.user_id`, common usage and reasoning-token counts | provider cache controls (including Chat prompt-cache breakpoints), container/inference/service state, `top_k`, server tools and caller constraints, signed/redacted thinking, schema-free JSON mode, matched stop-sequence identity, cache-creation/server-tool usage, Chat response annotations/metadata/moderation/service-tier/fingerprint and audio/prediction usage, stream obfuscation |
| Chat ↔ Gemini | text and the shared image/file/inline WAV-or-MP3 media subset, function declarations/calls/results, stop controls, sampling penalties, JSON/JSON-schema output, common usage | Chat metadata/storage/cache/moderation controls; Gemini request safety/cached-content/service/store controls; hosted tools; provider-issued thought signatures; unsupported MIME/file-URI/detail values and advanced media/code parts; multiple choices/candidates; response logprobs and grounding metadata under strict loss policy |
| Responses ↔ Messages | portable input/output items, function tools including direct-only caller constraints, JSON schema, `safety_identifier` ↔ `metadata.user_id`, common usage, reasoning-token counts and Responses `cache_write_tokens` ↔ Messages aggregate cache-creation usage; provider-selected `auto`/`default` response tier is omitted with a diagnostic | Responses state/background/prompt/context/cache controls and response metadata/moderation/explicit non-default service tier; Messages cache/container/inference/service controls; stop sequences; hosted tools; non-direct/program caller and toolset provenance; signed reasoning; response annotations/logprobs; matched stop-sequence identity; cache TTL breakdown and server-tool usage |
| Responses ↔ Gemini | portable text/image/file items and parts, function declarations/calls/results including ordered media-only inline image/PDF results, JSON/JSON-schema output, common usage, incremental text/tool streaming; provider-selected `auto`/`default` response tier is omitted with a diagnostic | Responses state/background/prompt/context/cache/moderation fields and explicit non-default service tier, plus nonexistent Create `input_audio`; mixed text/media function results and Gemini function-response `fileData`; Gemini request safety/cached-content/service/store controls; hosted tools; provider-issued thought signatures; unsupported MIME/file-URI/detail values and advanced media/code parts; non-URL annotations; response logprobs and grounding metadata under strict loss policy |
| Messages ↔ Gemini | portable text and shared image/PDF media, function declarations/calls/results including ordered media-only inline image/PDF results, sampling and stop controls, JSON schema, common token counters including thinking tokens | mixed text/media function results and Gemini function-response `fileData`; Messages cache/container/inference/service controls, server tools/caller constraints, provider file IDs and signed thinking; Gemini request safety/cached-content/service/store controls, hosted tools, thought signatures, unsupported MIME/file-URI values and advanced media/code parts; matched stop-sequence identity; response logprobs and grounding metadata under strict loss policy |

Some concepts are only partially equivalent:

- OpenAI Chat successful moderation uses a `moderation_results` collection,
  while Responses uses one `moderation_result`. A single result is wrapped or
  unwrapped; multiple Chat results cannot be represented by Responses.
- OpenAI service-tier values are passed only in the intersection supported by
  the source and destination APIs. For example, Responses-only `ultrafast`
  cannot be emitted as a Chat service tier. In Responses streams, an initial
  `auto` tier may resolve to the concrete tier reported by the terminal event.
  Messages and Gemini have no equivalent response field, so provider-selected
  `auto`/`default` is omitted with a diagnostic; an explicit non-default tier
  remains unsupported under the public strict policy.
- URL citations are converted only for non-streaming responses and only when
  their message-global character ranges are valid for the converted text.
  Other provider annotations remain unsupported.
- Anthropic thinking-token usage maps to OpenAI reasoning tokens and Gemini
  thought tokens. Cryptographic thinking/signature payloads never map across
  providers.
- Gemini `parametersJsonSchema` and `responseJsonSchema` are normalized as JSON
  Schema. The older Gemini `Schema` shape is converted only for keywords whose
  semantics are known.

## Fail-closed cases

Cross-protocol conversion returns `ErrUnsupported` when accepting a field would
silently change its meaning. Important examples include:

- Responses conversations, previous-response state, reusable prompts,
  background jobs and context-management state;
- provider-hosted tools such as file search, computer use, code execution, MCP,
  Responses tool-discovery items such as `tool_search`/`additional_tools`, and
  vendor-versioned server tools; the only
  cross-protocol hosted-tool exception is the Chat↔Responses unversioned web
  search request subset documented above;
- Responses `configuration_update` persistent conversation state;
- custom tools outside the non-streaming Chat↔Responses subset, including
  asynchronous execution, program/namespace provenance, and non-text results
  when the destination requires text;
- Responses encrypted reasoning, output annotations or output log probabilities
  that have no destination representation;
- Anthropic container reuse, citations, cache controls, non-direct/program
  caller and toolset provenance, and cache pre-warming requests
  (`max_tokens: 0`) outside a native Messages route. Responses↔Messages keeps
  the audited direct-only `caller`/`allowed_callers` intersection;
- Gemini cached content, provider safety policy, grounding metadata and
  provider-only executable, media-processing, transcription and server-tool
  parts outside a native Gemini route;
- signed, encrypted or redacted reasoning sent to a protocol without equivalent
  provenance semantics;
- Responses Create `input_audio`, cross-provider file IDs, unsupported media
  MIME types/URIs, and media detail controls with no destination equivalent;
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

## Streaming validation

Every upstream stream is validated against its source protocol before native
relay or cross-protocol conversion. Responses SSE requires `response.created`
first, a contiguous zero-based `sequence_number`, consistent item/part indexes
and IDs, and closure of every started content, reasoning, argument,
shell-command, shell-output, or audio stream before a successful terminal
event. Events after a terminal event, Responses `[DONE]`, or an incomplete item
lifecycle fail closed.

Buffered Chat and Gemini collectors pin the response identity, model identity,
and candidate/choice indexes across chunks. Buffered Chat additionally validates
function-call delta identity and renders `stream_options.include_usage` using
the official final usage-only chunk shape. Anthropic content-block indexes and
tool-input JSON deltas are presence- and lifecycle-checked before conversion.
Chat, Responses, and Messages receive a protocol-native error event when a
conversion fails after streaming starts. Gemini has no error frame recognized
by the official Go SDK, so its stream closes with the typed read error and does
not emit an empty success-shaped chunk.

## Usage accounting

Anthropic `input_tokens` excludes cache-read and cache-creation tokens, while
the SDK's private common accounting is inclusive. Conversion subtracts these
counters when emitting Messages usage and adds them when reading Messages.
Provider-only usage breakdowns follow field-specific loss rules. OpenAI
audio/prediction tokens and Anthropic cache-creation TTL/server-tool usage fail
closed under the public adapter. Gemini per-modality token arrays, usage service
tier and cache-token detail arrays are response observability metadata: they are
omitted with explicit diagnostics while aggregate portable counters remain.
Aggregate Anthropic cache-creation tokens map to Responses cache-write tokens.
Reasoning/thinking token counters are preserved where a destination counter has
the same accounting semantics.

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

For otherwise valid single-candidate Gemini responses, candidate safety
ratings, non-blocking prompt feedback, per-candidate token count, finish
message, model status and fine-grained usage metadata are observable metadata
omitted with diagnostics. Gemini response log probabilities and
grounding/citation/URL-context metadata are semantic output under the strict
public loss policy and therefore return `ErrUnsupported` instead.

## Limits and hosting responsibilities

The SDK enforces 32 MiB bounds on request bodies, non-streaming provider bodies,
individual SSE frames and buffered stream fallbacks. It rejects redirects,
binds upstream credentials to the configured adapter and scopes provider
control headers to a small per-provider allowlist.

It does not provide caller authentication, tenant authorization, TLS
termination, QPS/concurrency rate limiting, retry policy, circuit breaking or
provider quota management. A production gateway must supply those controls.
