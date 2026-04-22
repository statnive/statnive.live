# statnive-live — analytics platform Makefile
# Targets stay aligned with PLAN.md:120–131 and CLAUDE.md test gate.

GO            ?= go
GOLANGCI_LINT ?= golangci-lint
GO_LICENSES   ?= go-licenses
BIN_DIR       := bin
BIN_NAME      := statnive-live
PKG           := $(shell go list -mod=vendor ./... 2>/dev/null | grep -v '/web/node_modules/')

.PHONY: all build test test-integration lint vendor-check clean fmt licenses bench airgap-bundle release help dev-secret refresh-bot-patterns tls-test-keys tenancy-grep load-test crash-test ch-outage-test disk-full-test perf-tests audit airgap-test tracker tracker-test tracker-size tracker-install wal-killtest wal-killtest-full web-install web-build web-test web-lint bundle-gate brand-grep web-airgap-grep smoke

all: lint test build

## build: Compile the statnive-live binary (offline-capable, vendored).
## Depends on web-build unconditionally so //go:embed all:dist picks up
## fresh SPA assets on every compile — a stale or missing dist/ would
## either fail to compile or ship outdated SPA bytes.
build: web-build
	CGO_ENABLED=0 $(GO) build -mod=vendor -o $(BIN_DIR)/$(BIN_NAME) ./cmd/statnive-live

## test: Run unit tests with race detector (target <5s wall time)
test:
	$(GO) test -mod=vendor -race -timeout 60s $(PKG)

## test-integration: Run integration tests (requires `docker compose up -d clickhouse`).
## Depends on web-build so //go:embed all:dist/* in internal/dashboard/spa has
## files to embed — integration-tagged tests compile every package in the tree.
## `-v` so CI surfaces per-test PASS/FAIL lines (otherwise only FAIL shows,
## making it hard to tell which test in a suite got to which state).
## Timeout bumped to 240s so the multi-tenant post-ingest-smoke wait has
## headroom on slow CI runners (happy path still finishes in <30s).
test-integration: web-build
	$(GO) test -mod=vendor -race -tags=integration -v -timeout 240s ./test/...

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

## smoke: End-to-end boot smoke against docker-compose ClickHouse.
## Builds the binary, seeds CH, probes every prod surface on the REAL
## cmd/statnive-live router graph (/healthz, /tracker.js, /app/ + hashed
## asset, POST /api/event × 10 with CH count-back, /api/stats/overview
## bearer-auth enforcement). Requires Docker daemon running. Leaves CH
## up (same convention as wal-killtest).
smoke: build
	./test/smoke/harness.sh

## wal-killtest: 5-iteration kill-9 smoke (chained into make audit).
## Asserts CH count >= sent * (1 - 0.0005) after each random-offset SIGKILL.
wal-killtest:
	./.claude/skills/wal-durability-review/test/kill9/harness.sh 5

## wal-killtest-full: 50-iteration kill-9 hard gate (Phase 7b1b doc 27 §Gap 1).
## Slow (~30 min); on-demand or nightly, NOT in make audit.
wal-killtest-full:
	./.claude/skills/wal-durability-review/test/kill9/harness.sh 50

## web-install: Install SPA devDeps once; consumed by web-build + web-test.
web-install:
	cd web && npm ci

## web-build: Build the Preact SPA (Vite → internal/dashboard/spa/dist/).
## Runs unconditionally from `make build` so //go:embed all:dist picks up
## fresh assets. If npm ci already ran, re-running is a no-op on the dep
## side; vite build is ~150ms so the latency cost is negligible.
web-build: web-install
	cd web && npm run build

## web-test: Vitest SPA component + api-client + tokens tests
web-test: web-install
	cd web && npm test

## web-lint: ESLint over web/src
web-lint: web-install
	cd web && npm run lint

## bundle-gate: size-limit gate. Four buckets (web/.size-limit.json):
## initial JS ≤13 KB gz, uPlot chart chunk ≤25 KB gz (loads on first
## chart mount only via LazyChart — not in the initial bundle), lazy
## panel chunks ≤8 KB gz total, CSS ≤5 KB gz. Chained into `make audit`.
bundle-gate: web-build
	cd web && npm run bundle-gate

## brand-grep: Reject hand-rolled hex values outside tokens.css.
## docs/brand.md is the source of truth; components reference var(--*).
## tokens.test.ts is allowlisted (it asserts the hex values match brand.md).
brand-grep:
	@BAD=$$(grep -rEn '#[0-9a-fA-F]{3,8}\b' web/src --include='*.ts' --include='*.tsx' --include='*.css' 2>/dev/null \
		| grep -v '^web/src/tokens.css:' \
		| grep -v '^web/src/__tests__/tokens.test.ts:' || true); \
	if [ -n "$$BAD" ]; then \
		echo "FAIL: hand-rolled hex outside tokens.css (reference var(--*) instead):"; \
		echo "$$BAD"; \
		exit 1; \
	fi
	@echo "brand-grep: clean"

## web-airgap-grep: Scan built SPA for CDN URLs that slipped past code
## review. Complements the in-browser CSP connect-src 'self' at runtime.
web-airgap-grep: web-build
	@BAD=$$(grep -rEn 'fonts\.googleapis\.com|cdn\.|unpkg\.|jsdelivr\.|cdnjs\.' internal/dashboard/spa/dist/ 2>/dev/null || true); \
	if [ -n "$$BAD" ]; then \
		echo "FAIL: CDN URL detected in web/dist (air-gap violation):"; \
		echo "$$BAD"; \
		exit 1; \
	fi
	@echo "web-airgap-grep: clean"

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
audit: vendor-check tenancy-grep tracker-size token-budget bundle-gate brand-grep web-airgap-grep
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
