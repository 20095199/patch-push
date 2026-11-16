GO      ?= go
BINARY  ?= patchpush
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test vet lint fmt tidy clean release

all: test build

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

lint:
	staticcheck ./...

fmt:
	gofmt -w cmd repo cli

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY)
	rm -rf dist

release:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64   ./cmd
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64   ./cmd
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-amd64  ./cmd
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64  ./cmd
