# Responses-provider E2E request corpus

This directory is the source of truth for the request side of the opt-in
Responses-provider E2E suite. Every outbound test invocation has one complete
ingress request body in a `*.request.json` file and one entry in `catalog.json`.
The fixtures intentionally contain the client-facing protocol shape: the
RouteMorph conversion to an upstream Responses request is part of what the
tests exercise.

Suites:

- `core`: baseline text, function call/result, and structured-output coverage;
- `extended`: stable multi-turn, complex-tool, multi-result, and request-option
  coverage;
- `tools`: Responses custom tools, web search, and server/client tool search.

Fixtures use the fixed client model alias `routemorph-live-client`. Provider
URLs, credentials, deployment model names, and headers must never be stored
here. Markers and `stream` values are also fixed per file so each fixture is a
reviewable request, not a partial object assembled by Go code.

The only dynamic fixture is the second turn of client-executed tool search. It
uses two typed placeholders because the first response supplies their values:

- `{"$live_fixture":"CLIENT_TOOL_SEARCH_CALL"}` injects the complete original
  `tool_search_call` object;
- `{{CLIENT_TOOL_SEARCH_CALL_ID}}` injects its correlation ID into the matching
  `tool_search_output`.

The ordinary test suite locks the exact 63-case matrix, rejects orphan or
duplicate fixtures and unresolved placeholders, scans decoded raw and rendered
values for credential/provider material, checks each request with the public
inspector, and sends every rendered fixture through the public Adapter to a
local Responses mock. It then checks the converted Responses request for the
capability-specific semantics declared by `assertion`. Real provider calls
remain opt-in through the `integration` build tag and
`ROUTEMORPH_LIVE_RESPONSES=1`.
