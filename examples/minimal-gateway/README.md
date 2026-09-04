# Minimal protocol conversion gateway

This example exposes all four RouteMorphSDK client protocols and relays them to
one configured upstream protocol. It uses only `net/http` and RouteMorphSDK.

## Run

```bash
export UPSTREAM_PROTOCOL=responses
export UPSTREAM_BASE_URL=https://api.openai.com/v1
export UPSTREAM_API_KEY='...'

go run .
```

Supported values for `UPSTREAM_PROTOCOL` are `chat`, `responses`, `messages`,
and `gemini`. Optional settings:

- `UPSTREAM_MODEL`: replace every client model with a fixed provider model.
- `LISTEN_ADDR`: listen address, default `127.0.0.1:8080`.

`UPSTREAM_API_KEY` may be empty for an unauthenticated local upstream.

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
