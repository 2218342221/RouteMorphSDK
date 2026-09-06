# Minimal protocol conversion gateway

This example exposes all four RouteMorphSDK client protocols and relays them to
one configured upstream protocol. It uses only `net/http` and RouteMorphSDK.

## Run

```bash
export UPSTREAM_PROTOCOL=responses
export UPSTREAM_BASE_URL=https://api.openai.com/v1
export UPSTREAM_API_KEY='...'
export UPSTREAM_MODEL=gpt-5.4
export CODING_AGENT_COMPATIBILITY=true

go run .
```

Supported values for `UPSTREAM_PROTOCOL` are `chat`, `responses`, `messages`,
and `gemini`. Optional settings:

- `UPSTREAM_MODEL`: replace every client model with a fixed provider model.
- `CODING_AGENT_COMPATIBILITY`: when `true`, enable the explicit, documented
  compatibility profile required by Claude Code and Gemini CLI. The default is
  strict and fail-closed.
- `LISTEN_ADDR`: listen address, default `127.0.0.1:8080`.

`UPSTREAM_API_KEY` may be empty for an unauthenticated local upstream.

When conversion makes a documented approximation, this example logs only the
diagnostic severity, code, and structural field path after the response body is
finished. Production integrations must likewise consume
`response.Meta.Diagnostics()`; do not discard these signals or log diagnostic
messages without applying the deployment's payload-redaction policy.

This minimal gateway is a local-development example. It has no caller
authentication, TLS termination, or rate limiting. Do not bind it to a
non-loopback address unless it is behind a trusted proxy that provides all
three controls; otherwise any reachable caller could spend the configured
upstream credential.

## Call through another protocol

With a Responses upstream, send an OpenAI Chat Completions request:

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "your-model",
    "messages": [{"role": "user", "content": "Say hello."}],
    "stream": false
  }'
```

The same process also accepts:

- `POST /v1/responses`
- `POST /v1/messages`
- `POST /v1/models/{model}:generateContent`
- `POST /v1beta/models/{model}:generateContent`
- the corresponding Gemini `:streamGenerateContent` paths

`GET /healthz` provides a local health check. `SIGINT` and `SIGTERM` trigger a
graceful shutdown.

## Coding-agent clients

For a Responses upstream fixed to `gpt-5.4`, point each client at the local
gateway while using only a local placeholder credential. The gateway replaces
that credential with `UPSTREAM_API_KEY`:

```bash
# Claude Code
ANTHROPIC_BASE_URL=http://127.0.0.1:8080 \
ANTHROPIC_API_KEY=local-placeholder \
claude --model gpt-5.4

# Gemini CLI
GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:8080 \
GEMINI_API_KEY=local-placeholder \
gemini --model gpt-5.4
```

For Codex, configure a custom provider with
`base_url = "http://127.0.0.1:8080/v1"`, `wire_api = "responses"`, and a local
placeholder `env_key`. Keep provider-specific routing headers on the gateway or
in each client's custom-header setting; do not embed the real upstream key in a
client configuration.
