GO ?= go

.PHONY: fmt lint test build

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

build:
	mkdir -p bin
	$(GO) build -o ./bin/mainline ./cmd/mainline
	$(GO) build -o ./bin/mainlined ./cmd/mainlined
