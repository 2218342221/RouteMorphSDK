# Changelog

All notable changes to RouteMorphSDK will be documented in this file.

The project follows [Semantic Versioning](https://semver.org/). Until the first
stable release, minor versions may include deliberate API changes documented
here.

## [v0.1.0] - 2026-09-04

### Added

- Standalone Go module packaging, release documentation and CI.
- Four protocol-specific adapter constructors and four ingress methods.
- Twelve direct cross-protocol routes and four native same-protocol routes.
- Optional request preinspection through `InspectRequest` and `PrepareRequest`.
- Bounded incremental and buffered stream conversion with typed errors and
  diagnostics.

[v0.1.0]: https://github.com/2218342221/RouteMorphSDK/releases/tag/v0.1.0
