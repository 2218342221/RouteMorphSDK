GO ?= go
GOWORK ?= off

.PHONY: build test test-race test-integration-compile test-live-responses test-live-responses-core test-live-responses-extended test-live-responses-tools vet provider-sdk-examples fmt-check check

build:
	GOWORK=$(GOWORK) $(GO) build ./...

test:
	GOWORK=$(GOWORK) $(GO) test -count=1 ./...

test-race:
	GOWORK=$(GOWORK) $(GO) test -race -count=1 ./...

test-integration-compile:
	GOWORK=$(GOWORK) $(GO) vet -tags=integration .
	GOWORK=$(GOWORK) $(GO) test -tags=integration -run '^$$' .

test-live-responses:
	@test -n "$$ROUTEMORPH_LIVE_BASE_URL" || { echo "ROUTEMORPH_LIVE_BASE_URL is required" >&2; exit 2; }
	@test -n "$$ROUTEMORPH_LIVE_API_KEY" || { echo "ROUTEMORPH_LIVE_API_KEY is required" >&2; exit 2; }
	ROUTEMORPH_LIVE_RESPONSES=1 ROUTEMORPH_LIVE_MODEL="$${ROUTEMORPH_LIVE_MODEL:-gpt-5.4}" \
		GOWORK=$(GOWORK) $(GO) test -tags=integration -count=1 -failfast -timeout=90m -run '^TestLiveResponsesProvider' -v .

test-live-responses-core:
	@test -n "$$ROUTEMORPH_LIVE_BASE_URL" || { echo "ROUTEMORPH_LIVE_BASE_URL is required" >&2; exit 2; }
	@test -n "$$ROUTEMORPH_LIVE_API_KEY" || { echo "ROUTEMORPH_LIVE_API_KEY is required" >&2; exit 2; }
	ROUTEMORPH_LIVE_RESPONSES=1 ROUTEMORPH_LIVE_MODEL="$${ROUTEMORPH_LIVE_MODEL:-gpt-5.4}" \
		GOWORK=$(GOWORK) $(GO) test -tags=integration -count=1 -failfast -timeout=90m \
		-run '^TestLiveResponsesProvider(Text|ToolCall|ToolResult|StructuredOutput)Matrix$$' -v .

test-live-responses-extended:
	@test -n "$$ROUTEMORPH_LIVE_BASE_URL" || { echo "ROUTEMORPH_LIVE_BASE_URL is required" >&2; exit 2; }
	@test -n "$$ROUTEMORPH_LIVE_API_KEY" || { echo "ROUTEMORPH_LIVE_API_KEY is required" >&2; exit 2; }
	ROUTEMORPH_LIVE_RESPONSES=1 ROUTEMORPH_LIVE_MODEL="$${ROUTEMORPH_LIVE_MODEL:-gpt-5.4}" \
		GOWORK=$(GOWORK) $(GO) test -tags=integration -count=1 -failfast -timeout=90m \
		-run '^TestLiveResponsesProviderExtendedIntegration$$' -v .

test-live-responses-tools:
	@test -n "$$ROUTEMORPH_LIVE_BASE_URL" || { echo "ROUTEMORPH_LIVE_BASE_URL is required" >&2; exit 2; }
	@test -n "$$ROUTEMORPH_LIVE_API_KEY" || { echo "ROUTEMORPH_LIVE_API_KEY is required" >&2; exit 2; }
	ROUTEMORPH_LIVE_RESPONSES=1 ROUTEMORPH_LIVE_MODEL="$${ROUTEMORPH_LIVE_MODEL:-gpt-5.4}" \
		GOWORK=$(GOWORK) $(GO) test -tags=integration -count=1 -failfast -timeout=90m \
		-run '^TestLiveResponsesProvider(CustomTool|WebSearch|ToolSearch)Integration$$' -v .

vet:
	GOWORK=$(GOWORK) $(GO) vet ./...

provider-sdk-examples:
	cd examples/provider-sdks && GOWORK=$(GOWORK) $(GO) vet ./...
	cd examples/provider-sdks && GOWORK=$(GOWORK) $(GO) test -race -count=1 ./...
	cd examples/provider-sdks && GOWORK=$(GOWORK) $(GO) build ./...

fmt-check:
	@files="$$(find . -type f -name '*.go' -not -path './vendor/*' -print)"; \
	unformatted="$$(gofmt -l $$files)"; \
	test -z "$$unformatted" || { echo "Go files require gofmt:" >&2; echo "$$unformatted" >&2; exit 1; }

check: fmt-check vet test test-race test-integration-compile build provider-sdk-examples
