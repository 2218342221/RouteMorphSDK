# Live Responses-provider testing

The deterministic test suite uses local HTTP fixtures and runs with
`make check`. A separate opt-in suite verifies the public adapter against a
real OpenAI Responses-compatible endpoint. It is billable and never runs as
part of the default test or CI targets.

The live catalog contains 63 independently maintained request files under
`testdata/e2e/responses`, split into core (32), extended (18), and tools (13).
Each `*.request.json` file is one complete client request and maps to exactly
one provider HTTP call.

## Core matrix: 32 HTTP calls

The core suite fixes the upstream protocol to Responses and exercises every
ingress protocol:

| Scenario | Chat | Responses | Messages | Gemini |
|---|---:|---:|---:|---:|
| Text, non-streaming | yes | yes | yes | yes |
| Text, streaming | yes | yes | yes | yes |
| Forced function call, non-streaming | yes | yes | yes | yes |
| Forced function call, streaming | yes | yes | yes | yes |
| Function result continuation, non-streaming | yes | yes | yes | yes |
| Function result continuation, streaming | yes | yes | yes | yes |
| JSON Schema output, non-streaming | yes | yes | yes | yes |
| JSON Schema output, streaming | yes | yes | yes | yes |

These 32 calls check HTTP success, route metadata, protocol-valid output,
client-model restoration, exact usage accounting, terminal state, an allowlist
of expected conversion diagnostics, and semantic content. Streaming responses
are decoded as they arrive, checked for the target protocol's lifecycle, and
then reconstructed with that protocol's stream collector before final
assertions.

## Extended matrix: 18 HTTP calls

The extended suite deepens history, tool-ledger, and request-metadata coverage:

| Scenario | Ingress | Mode | HTTP calls | Principal assertions |
|---|---|---|---:|---|
| Multi-turn conversation/history | all four protocols | non-streaming + streaming | 8 | exact continuation text and protocol-valid lifecycle |
| Complex nested function call | all four protocols | non-streaming | 4 | function identity and nested argument preservation |
| Parallel tool-result continuation | all four protocols | non-streaming | 4 | call/result correlation and exact final text |
| Metadata + service tier | Chat, Responses | non-streaming | 2 | metadata preservation and provider-selected tier |
| **Total** |  |  | **18** | **18 logical cases and real requests** |

## Tool matrix: 13 HTTP calls

The tools suite contains 12 logical Go cases. The client `tool_search`
continuation uses two provider requests, so the suite makes 13 HTTP calls:

| Scenario | Ingress | Mode | HTTP calls | Principal assertions |
|---|---|---|---:|---|
| `custom_tool_call` | Responses | non-streaming | 1 | completed item identity, raw input and status |
| `custom_tool_call` | Responses | streaming | 1 | added → input delta/done → item done → terminal lifecycle |
| custom tool conversion | Chat | non-streaming | 1 | Chat `custom` call, input and terminal reason |
| `custom_tool_call_output` continuation | Responses | non-streaming | 1 | call/output correlation and exact final text |
| `custom_tool_call_output` continuation | Responses | streaming | 1 | output history and terminal streaming lifecycle |
| custom tool-output conversion | Chat | non-streaming | 1 | Chat custom result conversion and exact final text |
| `web_search` | Responses | non-streaming | 1 | search action, answer ordering and URL citation |
| `web_search` | Responses | streaming | 1 | in-progress/searching/completed events and citation lifecycle |
| web-search conversion | Chat | non-streaming | 1 | answer/citation preservation and no fabricated function call |
| server `tool_search` | Responses | non-streaming | 1 | call → output → discovered function-call order |
| server `tool_search` | Responses | streaming | 1 | generic output-item lifecycle and terminal reconciliation |
| client `tool_search` continuation | Responses | two non-streaming turns | 2 | correlated call, returned definitions and final function call |
| **Total** |  |  | **13** | **12 logical cases, 13 real requests** |

For this repository revision, the three groups contain 62 logical cases and
make 63 HTTP requests. The only difference is the client `tool_search`
continuation, which uses two request files and two provider turns in one logical
case. Ten of the 13 tool requests are native Responses calls; the other three
are non-streaming Chat→Responses calls. The three official-SDK example calls
are reported separately and are not part of the 63-call live regression total.

Custom tools, web search, and tool search have different portability:

- Native Responses custom-tool streams use
  `response.custom_tool_call_input.delta` and `.done`. The shared Chat↔Responses
  custom-tool mapping is non-streaming only; Messages and Gemini have no exact
  custom-tool equivalent. The live matrix exercises both custom calls and
  `custom_tool_call_output` continuations, including native Responses streams
  and the supported non-streaming Chat conversion. Malformed forms remain
  deterministic-only coverage.
- Native Responses web search preserves `web_search_call` progress, the final
  answer, and URL citations. A non-streaming Chat request can map the common
  `web_search_options` subset to Responses. On the return path Chat keeps the
  answer and citation but cannot represent the provider lifecycle, so the SDK
  emits `responses_web_search_call_not_representable`. Provider-hosted search
  types in Messages and Gemini are not treated as interchangeable.
- Server `tool_search` lets the provider discover deferred tools and is
  expected to produce `tool_search_call`, `tool_search_output`, then the
  selected ordinary `function_call` in this regression scenario. Compatible
  providers may omit or return null for server `call_id` and `execution`; when
  non-null, those fields are still type/enum checked and correlated when both
  sides provide an ID. `arguments` remains arbitrary JSON, and returned tool
  definitions retain `defer_loading`.
- Client `tool_search` stops after a correlated `tool_search_call`. The caller
  returns a `tool_search_output` with the same `call_id`, supplies the discovered
  tool through `additional_tools`, and makes a second request; the regression
  requires that turn to produce the intended ordinary function call.
  `tool_search_call`, `tool_search_output`, and `additional_tools` are
  Responses-native and fail closed when a different client protocol cannot
  preserve them. Tool-search streams use generic
  `response.output_item.added`/`done` events rather than a dedicated argument
  delta event.

## File-backed request catalog

[`catalog.json`](../testdata/e2e/responses/catalog.json) records each fixture's
stable ID, suite, capability, ingress protocol, streaming mode, request path,
and assertion. The request paths are unique and mirror their catalog IDs. The
core, extended, and tools directories therefore contain 32, 18, and 13 complete
request bodies respectively, rather than generating a shared request template
inside the live test.

Fixtures use the client-side alias `routemorph-live-client`. They must not
contain an authorization header, API key, base URL, provider deployment model,
or `sk-` material. The live adapter replaces the model and installs the
provider endpoint and credential only at runtime. The sole dynamic fixture is
the second client `tool_search` turn: its raw `tool_search_call` and `call_id`
come from the first live response and are declared as catalog placeholders.
Catalog validation fails on missing/orphan files, duplicate paths, unsafe paths,
forbidden credential/provider material, invalid JSON, unresolved placeholders,
or requests that fail public inspection.

## Deterministic regression coverage

Repository-local fixtures provide the following deterministic coverage:

- `TestE2ERequestFixtureCatalog` validates all 63 catalog entries and their
  standalone request files; `TestE2ERequestFixturesConvertThroughPublicAdapter`
  converts all 63 through a local `httptest` Responses endpoint with no live
  credentials or provider traffic.
- `internal/conformance/responses_provider_regression_test.go` fixes the
  observed custom status, web action/citation, and provider-shaped tool-search
  boundaries, including native acceptance before cross-protocol rejection.
- `internal/route/chatresponses/openai_v355_test.go` covers the non-streaming
  Chat↔Responses custom and web-search common subsets and their fail-closed
  edges.
- `internal/routekit/routekit_test.go` and `internal/stream/native_test.go`
  validate the complete item/tool unions, provider-compatible optional
  tool-search correlation fields, required payloads, enum values, statuses,
  and synthesized native SSE events.
- `internal/relay/native_event_responses_test.go`,
  `internal/relay/native_stream_responses_lifecycle_test.go`, and
  `internal/relay/response_test.go` cover event field types, ordered item
  lifecycles, terminal reconciliation, malformed forms, and byte-preserving
  native relay.

The live suite complements rather than replaces deterministic tests. Invalid
payloads, unsupported fields, error envelopes, cancellation, size limits,
headers, trailers, malformed streams, and fuzzing stay in the local suite so a
regression run does not intentionally consume quota or depend on provider
availability.

The current expanded catalog is recorded in the
[2026-09-06 gpt-5.4 file-backed E2E report](test-reports/2026-09-06-gpt-5.4-file-backed-e2e.md):
all 63 requests passed in one complete live run. The earlier
[2026-09-05 report](test-reports/2026-09-05-gpt-5.4-responses.md) is retained as
historical evidence for the preceding 38-call revision.

## Running

Provide credentials through the environment; do not put them in a command-line
argument, fixture, report, or committed file:

```bash
export ROUTEMORPH_LIVE_BASE_URL='https://provider.example/v1'
export ROUTEMORPH_LIVE_API_KEY='...'
export ROUTEMORPH_LIVE_MODEL='gpt-5.4'       # defaults to gpt-5.4
export ROUTEMORPH_LIVE_X_SOURCE=''           # optional deployment header

make test-live-responses-core                # 32 HTTP calls
make test-live-responses-extended            # 18 HTTP calls
make test-live-responses-tools               # 13 HTTP calls
make test-live-responses                      # combined: 63 HTTP calls
```

The explicit `integration` build tag and `ROUTEMORPH_LIVE_RESPONSES=1` guard
prevent accidental provider calls. All four Make targets set the guard after
checking that the base URL and key are present. `test-live-responses-core` runs
only the four core matrix tests, `test-live-responses-extended` runs the extended
integration matrix, `test-live-responses-tools` runs the three tool integration
tests, and `test-live-responses` runs all three groups. The test client performs
no automatic retries and applies a three-minute context deadline to each call.
Every target uses `-failfast` to stop after the first failure. The base URL must
use HTTPS and cannot contain user info, a query, or a fragment.

`make check` compiles and vets the integration-tagged test without selecting
any live test, so ordinary CI detects source drift without making provider
calls.

To reproduce the file/catalog checks and integration compilation without a
credential or provider request:

```bash
GOWORK=off go test -count=1 \
  -run '^(TestE2E|TestValidateE2E)' .
GOWORK=off make test-integration-compile
```

Do not publish raw provider payloads without removing credentials, request
identifiers, internal hostnames, deployment model aliases, and sensitive model
content. Reports should retain only the logical model, protocol, status,
event/item kinds, aggregate usage, diagnostics, and pass/fail result needed to
reproduce the conclusion.
