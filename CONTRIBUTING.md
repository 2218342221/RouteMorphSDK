# Contributing to RouteMorphSDK

Thanks for helping improve RouteMorphSDK. Changes should preserve a small public
API, explicit protocol semantics and fail-closed behavior.

## Before opening a change

For a substantial API, route or compatibility change, open an issue describing:

- the ingress and upstream protocols;
- the exact provider fields or stream events involved;
- the official protocol documentation or public wire-format evidence;
- whether the mapping is exact, approximate or impossible;
- the compatibility and resource-limit tests you plan to add.

Do not copy implementation code from third-party protocol gateways. Protocol
work must be independently implemented from official documentation, official
SDK type definitions and publicly observable wire formats. Record any source
used for compatibility research in the pull request and update
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) when appropriate.

## Development

RouteMorphSDK requires Go 1.25.13 or newer and intentionally has no third-party
Go module dependencies.

Run the complete local check from the repository root:

```bash
make check
```

The command checks formatting, runs `go vet`, executes normal and race-enabled
tests, and builds every package and example with `GOWORK=off`.

Focused fuzzing is also encouraged for parser or conversion changes:

```bash
GOWORK=off go test ./internal/conformance -run=^$ -fuzz=FuzzSSEDecoder
GOWORK=off go test ./internal/conformance -run=^$ -fuzz=FuzzProtocolCodecs
GOWORK=off go test ./internal/conformance -run=^$ -fuzz=FuzzRouterConversion
```

Choose an explicit `-fuzztime` suitable for your environment.

## Design rules

- Keep exported symbols within the deliberately small facade; update the public
  API allowlist only for an intentional, reviewed API change.
- Implement every cross-protocol direction directly. Do not route through an
  intermediate protocol.
- Keep wire DTOs in `internal/wire` and semantic decisions in the owning
  `internal/route/<pair>` package.
- Put only protocol-neutral mechanics in `internal/routekit`.
- Reject unknown or unrepresentable semantics instead of silently dropping
  them. A documented approximation must produce a diagnostic when appropriate.
- Preserve native pass-through behavior unless an explicit option requires
  rewriting.
- Bound all newly buffered input and validate stream terminal state.
- Add regression tests for request, response, stream, failure and resource-limit
  behavior.

## Pull requests

Keep changes focused and explain user-visible behavior. Include:

- tests and commands run;
- compatibility assumptions and source links;
- public API or wire-behavior changes;
- security, memory or latency implications;
- documentation and changelog updates where needed.

By contributing, you agree that your contribution is submitted under the
project's [MIT license](LICENSE).
