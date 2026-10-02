.PHONY: build test race vet fmt fmt-check check demo clean

GO ?= go
VERSION ?= dev

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/findrail ./cmd/findrail

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

fmt-check:
	@test -z "$$(gofmt -l $$(git ls-files '*.go'))"

check: fmt-check vet test

demo: build
	./bin/findrail index --data-dir .findrail examples/notes
	./bin/findrail search --data-dir .findrail "webhook"

clean:
	rm -rf bin dist
