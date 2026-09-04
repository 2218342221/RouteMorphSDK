# Security policy

## Supported versions

Security fixes are applied to `main`. The latest minor release is supported
unless a security advisory states otherwise.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use GitHub's private
vulnerability reporting for the
[RouteMorphSDK repository](https://github.com/2218342221/RouteMorphSDK/security/advisories/new).
Include:

- affected version or commit;
- impact and threat model;
- minimal reproduction or request/response samples with credentials removed;
- whether streaming, protocol conversion or native relay is involved;
- any suggested mitigation.

The maintainers aim to acknowledge complete reports within five business days.
Timelines for validation and remediation depend on severity and reproducibility.
Please allow a coordinated fix before public disclosure.

## Security boundary

RouteMorphSDK validates protocol payloads, bounds request/response processing,
does not follow redirects, replaces client-supplied provider credentials and
filters provider-control headers. These controls reduce relay-specific risk but
do not make an embedding service a complete security gateway.

The host application remains responsible for:

- caller authentication and tenant authorization;
- deciding whether allowlisted provider-control headers are trusted;
- TLS termination and network egress restrictions;
- QPS, concurrency, token-budget and provider-quota limiting;
- secret storage, rotation and log redaction;
- deployment-specific timeouts, retries and circuit breaking.

Never include live API keys, authorization headers or sensitive model content
in an issue, test fixture or vulnerability report.
