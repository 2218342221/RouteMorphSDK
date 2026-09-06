# Unsupported cross-protocol features

RouteMorphSDK preserves a field only when the destination protocol has the
same semantics. Cross-protocol requests use the strict policy and return
`ErrUnsupported` instead of silently dropping state. Response-only
observability metadata may be omitted only with an explicit diagnostic.
Malformed input returns `ErrInvalidPayload`; malformed successful provider
output returns `ErrUpstreamResponse` at the public adapter boundary.

Native same-protocol routes remain pass-through and are not subject to the
cross-protocol restrictions below.

## Limits shared by multiple routes

- Provider-hosted tools are generally not translated: file search, computer
  use, code execution, MCP, URL context, Google Maps, vendor-versioned server
  tools, and Anthropic/Gemini hosted search require their native protocol. The
  sole current exception is the non-streaming Chat `web_search_options` ↔
  unversioned Responses `web_search` request subset described below.
- Opaque reasoning state is not interchangeable. Anthropic signatures and
  redacted-thinking blocks, Responses encrypted reasoning, and Gemini
  `thoughtSignature` values are never forged or discarded across providers.
- A function call sent *to* Gemini from another protocol has no genuine Gemini
  thought signature. RouteMorph emits
  `gemini_thought_signature_unavailable`; Gemini 3 may reject replayed history.
- Multiple Chat choices and multiple Gemini candidates cannot be collapsed
  into one output without changing semantics.
- Unknown top-level fields, tagged content/output variants, finish reasons, and
  non-object function arguments fail closed where the route validates them.
  This is not a claim that every future field nested inside every known object
  is recursively rejected; nested extensions require an explicit audit before
  they are considered portable.
- Gemini response log probabilities and grounding/citation/URL-context metadata
  are rejected by the strict public loss policy when the target has no exact
  representation. On otherwise valid single-candidate responses, Gemini
  candidate safety ratings, non-blocking prompt feedback, model status, finish
  message, per-candidate token counts, and fine-grained usage metadata are
  observability fields and are instead omitted with explicit diagnostics.
  Request-side Gemini safety policy remains native-only.

## OpenAI Chat Completions and Responses

Supported request fields include common message/function-tool history, JSON
object and JSON Schema output, reasoning effort, metadata, the shared service
tier values, prompt-cache options and explicit content breakpoints, moderation
configuration, and `stream_options.include_obfuscation`.

### Custom tools

The official Responses item names are `custom_tool_call` and
`custom_tool_call_output`, not `customized_tool_call`. Chat↔Responses supports a
non-streaming common subset:

- custom declaration name and description;
- default/text format and grammar `{definition, syntax}` with `syntax` `lark`
  or `regex`;
- named custom `tool_choice`;
- call ID, name, arbitrary string input, and matching text output in request
  history;
- model-output custom calls, with any separate Responses item ID omitted only
  under an explicit diagnostic.

Chat↔Responses also converts `tool_choice.type:"allowed_tools"` in both
directions, but only for a non-empty, duplicate-free list of declared
`function`/`custom` name references and `mode` `auto` or `required`. Hosted-tool
references, an empty list, duplicate `(type, name)` references, an undeclared
name, or a declaration/reference type mismatch fail closed. Referencing a
custom tool does not relax the non-streaming custom-tool boundary.

Across Messages and Gemini routes, an allowed set is reduced only when its
meaning stays exact. Messages represents the all-functions required set as
`any`, including a one-member complete set; a one-function proper subset is a
named choice, while a larger proper subset is unsupported. Gemini represents
any non-empty required subset as `ANY` plus names. Auto can become unrestricted
`auto`/`AUTO` only when the allowed set equals all declarations. In the reverse
direction, Gemini `ANY` over all declarations becomes Chat/Responses
`required` or Messages `any`; a singleton proper subset becomes named, and a
larger proper subset is expressible only by Chat/Responses `allowed_tools`.
A required or any choice with no function declarations, a missing choice
discriminator/name, an unknown mode, or an undeclared function reference is
malformed and returns `ErrInvalidPayload`. Provider-native custom, hosted, and
discovery choices remain `ErrUnsupported` where the target has no such union.
Gemini `VALIDATED` is a known but non-portable mode and returns
`ErrUnsupported`, not `ErrInvalidPayload`.

Portable function/custom declaration names must be unique. A missing Chat tool
type, conflicting members from multiple tagged-choice variants, and unreviewed
nested fields in known tool-choice, thinking, or structured-output config
objects fail closed instead of disappearing during JSON decoding.

The conversion is intentionally unavailable for streaming because official
Chat chunks have no custom-tool delta representation. This also means a
buffered Responses custom call cannot be rendered as a valid Chat stream: the
renderer returns `ErrUnsupported` instead of disguising it as a function call.
Responses-only `async`,
`defer_loading`, `allowed_callers`, `output_schema`, prompt-cache breakpoint,
program caller, and namespace semantics also fail closed. A direct caller is
treated as Chat's implicit caller. Responses custom outputs containing media or
structured content cannot become a Chat tool message, whose supported shared
shape here is text.

Custom tools have no equivalent arbitrary-string invocation shape in Messages
or Gemini and therefore require native routing on those route pairs.

### Web search and tool discovery

Chat `web_search_options` and one unversioned Responses `web_search` tool map in
non-streaming requests for `search_context_size` (`low`, `medium`, `high`) and
an approximate location's city, country, region, and timezone.

The following remain Responses-native or fail closed when targeting Chat:

- `web_search_2025_08_26` and other versioned search variants;
- cache-only `external_web_access:false`, non-empty domain filters, and more
  than one search configuration;
- streaming web-search requests;
- the tool-discovery declaration/items `tool_search`, `tool_search_call`,
  `tool_search_output`, and `additional_tools`.

Responses `configuration_update` is not a tool-discovery item. It is
native-only persistent configuration state for subsequent responses, so
treating it as either a discovered tool or the current request's top-level
`reasoning` field would change its scope and timing.

Explicit `external_web_access:true` is represented by Chat's default online
search behavior. `tool_search_call.arguments` is arbitrary JSON and is not
coerced into the JSON-encoded argument string of a function call. A Responses
`web_search_call` lifecycle item is omitted with
`responses_web_search_call_not_representable` only when portable output is also
present; a lifecycle-only response fails closed.

Native Responses routes preserve these objects as their real Responses types.
A buffered native body can render `custom_tool_call` input
delta/done and `web_search_call` progress events; `tool_search_call`,
`tool_search_output`, `additional_tools`, and complete custom-tool outputs use
the generic output-item added/done lifecycle. No item is relabeled as a
function call merely to cross a protocol boundary. `configuration_update` is
a conversation item, not a `Response.output` item.

The native validator understands the audited OpenAI v3.56 output-item and
provider tool-definition unions. It validates known nested fields before an
SSE `output_item.added` frame can escape, reconciles completed items with the
terminal response, and rejects non-terminal item statuses during buffered SSE
rendering. Future top-level extension fields can pass through, but an unknown
union discriminator is not assumed safe.

Supported response extensions are deliberately narrower:

- non-streaming URL citations are converted; Responses indexes are interpreted
  as message-global character offsets and are range checked;
- a single successful moderation result is wrapped or unwrapped between the
  APIs; multiple Chat moderation results are unsupported;
- Responses-only `ultrafast` service tier is unsupported when targeting Chat;
- Responses stream snapshots may report `service_tier:"auto"` before the
  terminal response reports the concrete selected tier such as `default`;
  this one-way resolution is accepted, while unrelated concrete-tier changes
  remain invalid upstream output;
- Messages and Gemini omit provider-selected Responses `auto`/`default`
  response tiers with `responses_service_tier_not_representable`; explicit
  non-default tiers still fail closed because those protocols have no
  equivalent response field;
- Responses terminal `prompt_cache_options` has no Chat response field;
- Chat audio-token and prediction-token usage and Responses cache-write usage
  have no counterpart in the other API;
- Chat `system_fingerprint`, Responses metadata in Chat streams, and actual SSE
  obfuscation padding cannot be recreated. Internal characterization mode
  reports diagnostics; the public adapter otherwise remains fail-closed where
  semantic data would be lost;
- streaming URL-annotation events cannot be emitted as valid Chat chunks and
  are unsupported. URL citations are therefore non-streaming-only.

Chat stop sequences and Responses conversation, previous-response, reusable
prompt, background, context-management, truncation, client-metadata, and
maximum-tool-call state are not portable. Other hosted tools,
frequency/presence penalties, encrypted reasoning, and response phases without
a Chat field are also unsupported or explicitly diagnosed as documented in
the compatibility guide.

## Multimodal boundaries

Multimodal support is an intersection of role, content union, source form,
MIME type, URL/file provenance, and detail controls. Matching words such as
"file" or "audio" are not enough to make two shapes equivalent.

Known Chat and Messages content/source unions are checked before unmarshalling
into the portable model. A payload that mixes members from two tagged variants
is malformed, and an unreviewed nested extension is unsupported; neither is
silently discarded.

The supported ordinary-input intersections are:

- Chat↔Responses: user text, common image URL/data sources, and common file
  ID/data sources;
- Chat↔Messages: user text, JPEG/PNG/GIF/WebP images, and portable PDFs;
- Chat↔Gemini: user text, common images/files, and inline WAV/MP3;
- Responses↔Messages: text, JPEG/PNG/GIF/WebP images, and portable PDFs;
- Responses↔Gemini: text and common image/file forms, but no Responses Create
  audio;
- Messages↔Gemini: text, JPEG/PNG/GIF/WebP images, and portable PDFs.

### System and developer instructions

When converting Chat, Responses, or Messages instruction/system messages to a
protocol with a top-level instruction field, only a leading run of text-only
`system`/`developer` messages can be promoted to Messages `system` or Gemini
`systemInstruction`. Non-text instruction content and instruction messages
interleaved after a conversational turn fail closed.

### Chat

- Only user messages use the multimodal content union. System, developer, and
  tool messages are text-only in the portable subset; assistant messages use
  text/refusal and provider output fields.
- Images use a URL or data URL and `detail` `auto`, `low`, or `high`.
- Input audio is non-empty standard base64 and format `wav` or `mp3` only.
- File input uses a provider file ID or file data. A filename is required when
  a route must infer or preserve the data's MIME type. A provider file ID is
  not portable to another API.
- Assistant audio has Chat-specific ID, data, expiration, and transcript
  semantics and is not synthesized from generic provider media.

### Responses Create

- Message content admits `input_text`, `input_image`, and `input_file`.
  `input_audio` is not in the Responses Create request union; a similarly named
  SDK type used by other API surfaces does not make it valid here.
- `input_image` selects URL or file ID. `detail:"original"` has no Chat
  equivalent and fails when targeting Chat. URLs must be absolute HTTP(S), and
  inline data URLs must contain valid base64 in a supported image MIME type.
- `input_file` selects file ID, file URL, or file data. A filename is required
  when raw data must carry type information to the destination. Its `detail`
  values `auto`, `low`, and `high` fail on routes whose destination has no
  matching file-detail control. File URLs must be absolute HTTP(S); raw or
  data-URL file payloads must be valid base64.

### Messages and Gemini

- Messages image base64 is limited to JPEG, PNG, GIF, and WebP. URL and
  provider file-ID image sources exist, but IDs are provider-scoped. Messages
  documents have portable PDF forms and additional native-only source forms;
  Messages has no generic audio block. URL sources do not carry `media_type`.
- Gemini `inlineData` requires a bare IANA MIME type and base64 data;
  `fileData` also requires a bare IANA MIME type. For `fileData`, this
  implementation accepts `gs://`, Google Cloud Storage HTTPS, and Gemini Files
  API HTTPS URIs when targeting Gemini. A remote URL targeting Gemini must have
  a MIME type determinable from the source protocol or a recognized
  filename/URL extension; an unknown MIME is not guessed. Only Google Cloud
  Storage HTTPS URLs are treated as portable when converting away from Gemini;
  `gs://` and Gemini Files API URIs remain provider scoped. Gemini MIME types
  beyond a destination's accepted set remain native-only.
- Gemini ordinary `contents` roles are empty (treated as user), `user`, or
  `model`; `systemInstruction.role` is empty or `user`. A successful response
  candidate role is empty or `model`. Other role values fail validation.

Converters validate source exclusivity, base64, MIME presence and
route-specific allowlists, any conditionally required filename, URL/URI shape,
and detail values. They reject unsupported media rather than
relabeling it: for example, OGG is not called WAV, video is not emitted as a
Messages document, and a provider file ID is not copied into another
provider's namespace. Multimodal tool-result parts are portable only on routes
whose destination can preserve their order and exact media source; Chat custom
and function tool outputs use the text-only shared subset.

Responses↔Messages supports ordered text/image/PDF tool-result content when
the exact source form is accepted by both protocols. Messages `is_error`,
OpenAI detail controls and filenames, provider file IDs, and otherwise
unsupported MIME/source forms fail closed.

For Responses↔Gemini and Messages↔Gemini, that portable tool-result subset is
specifically an ordered, media-only list of inline JPEG, PNG, GIF, WebP, or PDF
data. It maps to Gemini `functionResponse.parts` with an empty `response`
object for Responses, or with the exact empty `error` marker needed to preserve
a Messages `is_error:true` result (`{"error":""}` or `{"error":null}`).
Text-only results use exactly one `output`
or `error` member; a marker plus sibling data fails closed. Mixed text/media
results, other non-empty `response` data plus media parts, `fileData`,
audio/video, filenames, detail controls, provider file IDs, remote URLs, and
malformed or mixed part variants fail closed because their semantics or
ordering cannot be preserved.

## Reasoning and thinking boundaries

Reasoning is not one transferable field. Four layers are handled separately:

- **Request control:** Chat/Responses effort, Anthropic adaptive/enabled mode
  and token budget/display policy, and Gemini level/budget/`includeThoughts`
  are not generally equivalent. Exact budgets and display/include policies
  require native routing. The exact bidirectional intersections are:
  OpenAI Chat/Responses↔Messages = `low`, `medium`, `high`, `xhigh`, `max`;
  OpenAI Chat/Responses↔Gemini = `minimal`, `low`, `medium`, `high` mapped to
  uppercase Gemini levels; Messages↔Gemini = `low`, `medium`, `high` mapped to
  uppercase Gemini levels. Chat↔Responses itself preserves all audited OpenAI
  values unchanged: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, and
  `max`. OpenAI `summary`, Messages adaptive/enabled thinking and token budget,
  and Gemini exact budget/`includeThoughts` do not follow from an effort match.
  Values outside each listed intersection fail closed.
- **Visible text:** Chat `reasoning_content` is a non-standard extension.
  Responses reasoning summary/text, Messages thinking text, and Gemini thought
  text map only where a destination has a compatible visible-text field. In
  particular, Chat `reasoning_content` and unsigned Gemini thoughts map to
  Responses raw `reasoning_text`; a Responses `summary_text` is not silently
  reclassified as raw reasoning.
- **Opaque replay state:** Responses `encrypted_content`, Anthropic thinking
  signatures/redacted data, and Gemini `thoughtSignature` are provider-issued
  state. Cross-provider conversion never forges or silently drops them.
- **Usage:** reasoning/thinking token counters map only when accounting
  semantics match. A portable counter does not make request controls or opaque
  state portable.

A function call sent to Gemini from a different protocol cannot acquire a
genuine thought signature. RouteMorph therefore reports
`gemini_thought_signature_unavailable`; replay may still be rejected by models
that require provider-issued signed history.

## Anthropic Messages routes

The following fields require a native Messages provider or fail closed on a
cross-protocol route:

- top-level cache control, container reuse, inference geography, and Messages
  service tier;
- block cache controls, citations, caller/toolset provenance, document title,
  context, transformations, file IDs, and provider-specific document sources;
- server tools and the tool extensions `eager_input_streaming`,
  `defer_loading`, `allowed_callers`, and `input_examples` when the target
  cannot express them;
- `pause_turn`, structured `stop_details`, signed/redacted thinking, and cache
  pre-warming requests using `max_tokens: 0`;
- cache-creation TTL breakdowns, inference geography, service tier, and
  server-tool usage in responses.

The aggregate `cache_creation_input_tokens` value maps exactly to Responses
`cache_write_tokens`; the 5-minute/1-hour breakdown does not. Anthropic
`output_tokens_details.thinking_tokens` maps to the equivalent OpenAI reasoning
or Gemini thought-token counter. `top_k` maps only between Messages and Gemini.

## Gemini routes

The following Gemini concepts remain native-only:

- cached-content state, safety policy, Gemini service tier and storage policy;
- hosted tools and agent tool parts (`toolCall`, `toolResponse`);
- executable-code, code-execution-result, audio-transcription,
  media-processing, per-part media-resolution/video metadata, and other
  advanced media payloads;
- function response schemas/behavior, retrieval configuration, speech/image/
  translation/affective-dialog generation controls, and internal response
  schemas;
- response log probabilities and grounding/citation/URL-context metadata under
  the strict public loss policy.

On an otherwise valid single-candidate response, candidate safety ratings,
non-blocking prompt feedback, candidate token count, finish message, model
status, usage service tier, per-modality token arrays, and cache-token detail
arrays are response observability metadata. Cross-protocol conversion omits
them with explicit diagnostics instead of rejecting an otherwise portable
response.

Gemini `parametersJsonSchema` and `responseJsonSchema` are supported as full
JSON Schema. The older `Schema` representation is converted only for the
keywords whose semantics are known. Although the Go SDK type exposes
`displayName`, the generateContent API baseline does not support it; the field
is not emitted or accepted on cross-protocol routes. OpenAI filenames therefore
require native OpenAI routing when they cannot otherwise be represented.

## Streaming-specific boundaries

- Chat↔Responses is incremental for text, refusal, reasoning, function calls,
  common usage, service tier, and moderation. Moderation-only Chat chunks are
  accepted and accumulated.
- OpenAI custom tools are non-streaming-only because Chat chunks have no
  official custom-call delta shape. Web-search request conversion and URL
  citation conversion are also non-streaming-only. Responses web-search
  lifecycle events can produce a diagnostic, but are not emitted as fabricated
  Chat tool calls.
- Responses↔Gemini is incremental for portable text and function-call events.
- A Responses-compatible provider may omit `name` from
  `response.function_call_arguments.done` when the surrounding
  `output_item.added`/`output_item.done` supplies the function identity. The
  validator accepts that observed shape but still requires a valid referenced
  function-call lifecycle and validates `name` when it is present.
- All other cross-protocol streams are buffered up to 32 MiB, validated as a
  terminal native response, and then rendered in the destination protocol.
- Buffered Gemini stream conversion requires exactly one candidate. Multiple
  candidates cannot be represented by the single-output renderer and fail
  with `ErrUnsupported`.
- Anthropic `signature_delta` and citation deltas are collected so they can be
  validated; they are still rejected if the destination cannot preserve their
  semantics.
- Event padding/obfuscation is transport-shape data. The request switch maps
  between Chat and Responses, but generated events cannot retain the original
  padding guarantee and report a diagnostic.

This list is intentionally conservative. A field not listed as supported is
not assumed portable merely because another provider uses a similar name.
