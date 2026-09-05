GO ?= go
GOWORK ?= off

.PHONY: build test test-race vet provider-sdk-examples fmt-check check

build:
	GOWORK=$(GOWORK) $(GO) build ./...

test:
	GOWORK=$(GOWORK) $(GO) test -count=1 ./...

test-race:
	GOWORK=$(GOWORK) $(GO) test -race -count=1 ./...

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

check: fmt-check vet test test-race build provider-sdk-examples
