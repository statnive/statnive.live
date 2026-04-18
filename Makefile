# statnive-live — analytics platform Makefile
# Targets stay aligned with PLAN.md:120–131 and CLAUDE.md test gate.

GO            ?= go
GOLANGCI_LINT ?= golangci-lint
GO_LICENSES   ?= go-licenses
BIN_DIR       := bin
BIN_NAME      := statnive-live
PKG           := ./...

.PHONY: all build test test-integration lint vendor-check clean fmt licenses bench airgap-bundle release help

all: lint test build

## build: Compile the statnive-live binary (offline-capable, vendored)
build:
	CGO_ENABLED=0 $(GO) build -mod=vendor -o $(BIN_DIR)/$(BIN_NAME) ./cmd/statnive-live

## test: Run unit tests with race detector (target <5s wall time)
test:
	$(GO) test -mod=vendor -race -timeout 60s $(PKG)

## test-integration: Run integration tests (requires `docker compose up -d clickhouse`)
test-integration:
	$(GO) test -mod=vendor -race -tags=integration -timeout 120s ./test/...

## lint: Run golangci-lint across the module
lint:
	$(GOLANGCI_LINT) run $(PKG)

## fmt: Auto-format with gofumpt via golangci-lint
fmt:
	$(GOLANGCI_LINT) fmt $(PKG)

## vendor-check: Verify go.sum + vendored deps are up to date (CI gate)
vendor-check:
	$(GO) mod verify
	$(GO) mod vendor
	git diff --exit-code vendor/ go.mod go.sum

## licenses: Check no AGPL / strong-copyleft deps shipped (CLAUDE.md License Rules)
licenses:
	$(GO_LICENSES) check $(PKG) --disallowed_types=forbidden,restricted

## bench: Benchmark suite (Phase 7 — placeholder for now)
bench:
	@echo "TODO Phase 7: benchmark enrichment + rollup queries"

## airgap-bundle: Build offline install bundle (Phase 8 — placeholder)
airgap-bundle:
	@echo "TODO Phase 8: build statically-linked binary + vendor + IP2Location DB23 + SHA256SUMS"

## release: Full release gate (Phase 8 — placeholder)
release:
	@echo "TODO Phase 8: release-gate (lint + test + test-integration + airgap-test + bundle + sign)"

## clean: Remove build + runtime artifacts (NOT vendor/)
clean:
	rm -rf $(BIN_DIR) wal/ data/ tmp/ coverage.out coverage.html *.prof audit.jsonl

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
