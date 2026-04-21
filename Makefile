# statnive-live — analytics platform Makefile
# Targets stay aligned with PLAN.md:120–131 and CLAUDE.md test gate.

GO            ?= go
GOLANGCI_LINT ?= golangci-lint
GO_LICENSES   ?= go-licenses
BIN_DIR       := bin
BIN_NAME      := statnive-live
PKG           := ./...

.PHONY: all build test test-integration lint vendor-check clean fmt licenses bench airgap-bundle release help dev-secret refresh-bot-patterns tls-test-keys tenancy-grep load-test crash-test ch-outage-test disk-full-test perf-tests audit airgap-test tracker tracker-test tracker-size tracker-install wal-killtest wal-killtest-full

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

## lint: Run golangci-lint + tenancy-grep gate
lint: tenancy-grep
	$(GOLANGCI_LINT) run $(PKG)

## tenancy-grep: CI gate — Architecture Rules 1 + 8 (no events_raw queries; whereTimeAndTenant first)
tenancy-grep:
	@if grep -rEn 'FROM[[:space:]]+(statnive\.)?events_raw' internal/storage/queries.go; then \
		echo "FAIL: dashboard queries must NOT touch events_raw (Architecture Rule 1)"; exit 1; \
	fi
	@MISSED=$$(awk '/^func \(.*clickhouseStore\) [A-Z][a-zA-Z]*\(/,/^}/' internal/storage/queries.go | \
		awk '/conn\.Query|conn\.QueryRow/,/`,/' | \
		grep -c 'FROM statnive' || true); \
	REFD=$$(grep -c 'whereTimeAndTenant' internal/storage/queries.go); \
	if [ "$$MISSED" -gt 0 ] && [ "$$REFD" -lt "$$MISSED" ]; then \
		echo "FAIL: every SELECT in queries.go must call whereTimeAndTenant (Architecture Rule 8)"; \
		echo "  found $$MISSED queries against statnive.* but only $$REFD whereTimeAndTenant calls"; \
		exit 1; \
	fi

## fmt: Auto-format with gofumpt via golangci-lint
fmt:
	$(GOLANGCI_LINT) fmt $(PKG)

## vendor-check: Verify go.sum + vendored deps are up to date (CI gate)
## --ignore-cr-at-eol so CRLF in upstream README/CHANGELOG (klauspost/cpuid,
## ClickHouse/clickhouse-go) doesn't fail the gate on Linux CI when the
## same content was committed with LF normalization on macOS.
vendor-check:
	$(GO) mod verify
	$(GO) mod vendor
	git diff --ignore-cr-at-eol --exit-code vendor/ go.mod go.sum

## licenses: Check no AGPL / strong-copyleft deps shipped (CLAUDE.md License Rules)
licenses:
	$(GO_LICENSES) check $(PKG) --disallowed_types=forbidden,restricted

## bench: Run all Go benchmarks (no integration tag — fast). Output to stdout.
bench:
	$(GO) test -mod=vendor -bench=. -benchmem -run='^$$' -timeout 5m ./internal/...

## load-test: Run the k6 7K EPS load script. Requires k6 installed +
## the binary running on $STATNIVE_URL (default http://127.0.0.1:8080).
## Pre-flight: seed the load-test site row (see test/perf/load.js header).
load-test:
	k6 run test/perf/load.js

## crash-test: Subprocess kill -9 + WAL replay. Requires Docker + the
## docker-compose ClickHouse running.
crash-test:
	$(GO) test -mod=vendor -tags=slow -timeout 5m -run TestCrashRecovery ./test/perf/...

## ch-outage-test: Stop CH mid-flow + restart + verify drain.
ch-outage-test:
	$(GO) test -mod=vendor -tags=slow -timeout 5m -run TestCHOutage ./test/perf/...

## disk-full-test: Fill WAL past cap + verify oldest dropped + binary survives.
disk-full-test:
	$(GO) test -mod=vendor -tags=slow -timeout 5m -run TestDiskFull ./test/perf/...

## perf-tests: All Phase 7a stress tests (crash + ch-outage + disk-full).
perf-tests: crash-test ch-outage-test disk-full-test

## wal-killtest: 5-iteration kill-9 smoke (chained into make audit).
## Asserts CH count >= sent * (1 - 0.0005) after each random-offset SIGKILL.
wal-killtest:
	./.claude/skills/wal-durability-review/test/kill9/harness.sh 5

## wal-killtest-full: 50-iteration kill-9 hard gate (Phase 7b1b doc 27 §Gap 1).
## Slow (~30 min); on-demand or nightly, NOT in make audit.
wal-killtest-full:
	./.claude/skills/wal-durability-review/test/kill9/harness.sh 50

## airgap-bundle: Build offline install bundle (Phase 8 — placeholder)
airgap-bundle:
	@echo "TODO Phase 8: build statically-linked binary + vendor + IP2Location DB23 + SHA256SUMS"

## release: Full release gate (Phase 8 — placeholder)
release:
	@echo "TODO Phase 8: release-gate (lint + test + test-integration + airgap-test + bundle + sign)"

## dev-secret: Generate a random 32-byte master.key for local dev (chmod 0600)
dev-secret:
	@if [ -f config/master.key ]; then \
		echo "config/master.key already exists; refusing to overwrite"; exit 1; \
	fi
	@mkdir -p config
	@openssl rand -hex 32 > config/master.key
	@chmod 0600 config/master.key
	@echo "Generated config/master.key (chmod 0600)"

## tls-test-keys: Generate self-signed cert+key for security integration tests (100y expiry)
tls-test-keys:
	mkdir -p test/tls_keys
	openssl req -x509 -newkey rsa:2048 -keyout test/tls_keys/test.key \
		-out test/tls_keys/test.crt -sha256 -days 36500 -nodes \
		-subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
	chmod 0600 test/tls_keys/test.key
	@echo "Generated test/tls_keys/test.{crt,key} (100y self-signed, localhost SAN)"

## refresh-bot-patterns: Pull latest internal/enrich/crawler-user-agents.json from monperrus/crawler-user-agents (MIT)
refresh-bot-patterns:
	curl -sSfL https://raw.githubusercontent.com/monperrus/crawler-user-agents/master/crawler-user-agents.json \
		-o internal/enrich/crawler-user-agents.json
	@echo "Refreshed internal/enrich/crawler-user-agents.json"

## audit: Hardening gate — vendor + tenancy + go vet + hot-path benches + tracker bundle size
## Re-run before opening a PR. Slow tests + CH integration excluded.
audit: vendor-check tenancy-grep tracker-size token-budget
	$(GO) vet -mod=vendor $(PKG)
	$(GO) test -mod=vendor -bench=. -benchmem -run='^$$' -benchtime=2s -timeout 5m ./internal/enrich/ ./internal/ingest/

## token-budget: AI-surface line-count + skill-description caps (CLAUDE.md/PLAN.md/tooling.md/14 SKILL.md)
token-budget:
	@bash ops/token-budget.sh

## tracker-install: Install tracker devDeps once; consumed by tracker + tracker-test
tracker-install:
	cd tracker && npm ci

## tracker: Build the IIFE tracker (Rollup + Terser → internal/tracker/dist/tracker.js)
tracker: tracker-install
	cd tracker && npm run build

## tracker-test: Run Vitest against the tracker source
tracker-test: tracker-install
	cd tracker && npm test

## tracker-size: CI gate — assert internal/tracker/dist/tracker.js stays inside the budget
tracker-size:
	@SIZE=$$(stat -f%z internal/tracker/dist/tracker.js 2>/dev/null || stat -c%s internal/tracker/dist/tracker.js); \
	GZIP=$$(gzip -c -9 internal/tracker/dist/tracker.js | wc -c | tr -d ' '); \
	echo "tracker.js: $$SIZE B min / $$GZIP B gz (budget: 1500 / 700)"; \
	if [ $$SIZE -gt 1500 ] || [ $$GZIP -gt 700 ]; then \
	  echo "FAIL: tracker bundle over budget (1500 B min / 700 B gz)"; exit 1; \
	fi

## airgap-test: MANUAL — run the binary under iptables OUTPUT DROP and
## confirm ingest + dashboard still work (loopback whitelisted). Procedure
## documented in docs/runbook.md § Air-Gap Verification (single source of truth).
airgap-test:
	@echo "Manual procedure — see docs/runbook.md § Air-Gap Verification"

## clean: Remove build + runtime artifacts (NOT vendor/, NOT config/master.key)
clean:
	rm -rf $(BIN_DIR) wal/ data/ tmp/ coverage.out coverage.html *.prof audit.jsonl

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
