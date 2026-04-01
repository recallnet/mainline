GO ?= go
VERSION ?= dev
RELEASE_OUT ?= dist
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS ?= -X github.com/recallnet/mainline/internal/app.Version=$(VERSION) -X github.com/recallnet/mainline/internal/app.Commit=$(COMMIT) -X github.com/recallnet/mainline/internal/app.Date=$(DATE)

.PHONY: fmt lint test test-invariants test-stress soak soak-randomized certify-matrix build release-snapshot package-release goreleaser-check goreleaser-snapshot install-hooks install-launch-agent test-hooks

fmt:
	$(GO) fmt ./...

lint:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-invariants:
	$(GO) test ./internal/app -run TestInvariant

test-stress:
	$(GO) test ./internal/app -run TestStress -count=1

soak:
	./scripts/run-soak.sh --runs $${SOAK_RUNS:-25} --output $${SOAK_OUT:-artifacts/soak}

soak-randomized:
	./scripts/run-soak.sh --randomized --runs $${SOAK_RUNS:-25} --seed-base $${SOAK_SEED_BASE:-20260331} --output $${SOAK_OUT:-artifacts/soak-randomized}

certify-matrix: build
	python3 ./scripts/run-certification-matrix.py --mq-bin $${MQ_BIN:-./bin/mq} --matrix $${CERT_MATRIX:-docs/certification/matrix.json} --output $${CERT_OUT:-docs/certification/latest-report.json}

build:
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o ./bin/mainline ./cmd/mainline
	$(GO) build -ldflags "$(LDFLAGS)" -o ./bin/mq ./cmd/mq
	$(GO) build -ldflags "$(LDFLAGS)" -o ./bin/mainlined ./cmd/mainlined

release-snapshot:
	./scripts/build-release.sh --version $(VERSION) --output $(RELEASE_OUT)

package-release:
	./scripts/build-release.sh --version $(VERSION) --output $(RELEASE_OUT)
	./scripts/generate-homebrew-formula.sh --version $(VERSION) --checksums $(RELEASE_OUT)/SHA256SUMS --output $(RELEASE_OUT)/mainline.rb
	./scripts/generate-release-manifest.sh --version $(VERSION) --checksums $(RELEASE_OUT)/SHA256SUMS --output $(RELEASE_OUT)/release-manifest.json
	./scripts/package-release-assets.sh --version $(VERSION) --dist $(RELEASE_OUT)

goreleaser-check:
	go run github.com/goreleaser/goreleaser/v2@latest check

goreleaser-snapshot:
	RELEASE_VERSION_OVERRIDE=$(VERSION) go run github.com/goreleaser/goreleaser/v2@latest release --clean --snapshot

install-hooks:
	./scripts/install-hooks.sh

install-launch-agent:
	./scripts/install-launch-agent.sh

test-hooks:
	./scripts/test-hook-checks.sh
