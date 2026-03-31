GO ?= go
VERSION ?= dev
RELEASE_OUT ?= dist

.PHONY: fmt lint test build release-snapshot

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

build:
	mkdir -p bin
	$(GO) build -o ./bin/mainline ./cmd/mainline
	$(GO) build -o ./bin/mq ./cmd/mq
	$(GO) build -o ./bin/mainlined ./cmd/mainlined

release-snapshot:
	./scripts/build-release.sh --version $(VERSION) --output $(RELEASE_OUT)
