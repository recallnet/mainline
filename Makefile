GO ?= go
VERSION ?= dev
RELEASE_OUT ?= dist
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS ?= -X github.com/recallnet/mainline/internal/app.Version=$(VERSION) -X github.com/recallnet/mainline/internal/app.Commit=$(COMMIT) -X github.com/recallnet/mainline/internal/app.Date=$(DATE)

.PHONY: fmt lint test test-invariants build release-snapshot install-hooks test-hooks

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-invariants:
	$(GO) test ./internal/app -run TestInvariant

build:
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o ./bin/mainline ./cmd/mainline
	$(GO) build -ldflags "$(LDFLAGS)" -o ./bin/mq ./cmd/mq
	$(GO) build -ldflags "$(LDFLAGS)" -o ./bin/mainlined ./cmd/mainlined

release-snapshot:
	./scripts/build-release.sh --version $(VERSION) --output $(RELEASE_OUT)

install-hooks:
	./scripts/install-hooks.sh

test-hooks:
	./scripts/test-hook-checks.sh
