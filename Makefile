# statnive-live — analytics platform Makefile
# Targets stay aligned with PLAN.md:120–131 and CLAUDE.md test gate.

GO            ?= go
BIN_DIR       := bin
BIN_NAME      := statnive-live

# `make tools` installs golangci-lint / go-licenses / govulncheck / semgrep into
# $(go env GOPATH)/bin. Resolve those tool paths once here so recipes work even
# when the user hasn't added GOPATH/bin to their shell PATH (otherwise GNU make
# 3.81's direct-exec path on macOS fails to find the binaries).
GOPATH_BIN := $(shell go env GOPATH)/bin
export PATH := $(GOPATH_BIN):$(PATH)

# Prefer the GOPATH-installed binary when present, else fall back to $PATH.
GOLANGCI_LINT ?= $(if $(wildcard $(GOPATH_BIN)/golangci-lint),$(GOPATH_BIN)/golangci-lint,golangci-lint)
GO_LICENSES   ?= $(if $(wildcard $(GOPATH_BIN)/go-licenses),$(GOPATH_BIN)/go-licenses,go-licenses)
GOVULNCHECK   ?= $(if $(wildcard $(GOPATH_BIN)/govulncheck),$(GOPATH_BIN)/govulncheck,govulncheck)

.PHONY: all build test test-integration lint vendor-check clean fmt licenses bench airgap-bundle airgap-bundle-verify airgap-install-test release help dev-secret refresh-bot-patterns tls-test-keys tenancy-grep identity-gate privacy-gate privacy-gate-selftest legacy-site-id-grep skill-sanitizer skill-sanitizer-selftest load-test crash-test ch-outage-test disk-full-test perf-tests audit airgap-test tracker tracker-test tracker-size tracker-install wal-killtest wal-killtest-full web-install web-build web-test web-lint web-e2e bundle-gate brand-grep web-airgap-grep smoke smoke-metrics systemd-verify seed-backup-drill backup-drill-local tools tools-check govulncheck ch-up ch-down ch-reset ci-local ci-local-fast hooks

all: lint test build

## build: Compile the statnive-live binary (offline-capable, vendored).
## Host-platform — for fast laptop iteration. Production releases go
## through `airgap-bundle` which routes via `build-linux` so a Mac dev
## still produces a Linux/amd64 tarball (LEARN.md Lesson 1).
## Depends on web-build unconditionally so //go:embed all:dist picks up
## fresh SPA assets on every compile — a stale or missing dist/ would
## either fail to compile or ship outdated SPA bytes.
build: web-build
	CGO_ENABLED=0 $(GO) build -mod=vendor -o $(BIN_DIR)/$(BIN_NAME) ./cmd/statnive-live

## build-linux: Cross-compile for linux/amd64 explicitly. Used by
## airgap-bundle so a Mac developer doesn't ship a darwin/arm64 binary
## inside a linux-amd64-named tarball (Milestone 1 cutover Bug #3).
## Output goes to bin/statnive-live-linux-amd64 to avoid clobbering the
## host-platform `make build` artifact.
build-linux: web-build
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -mod=vendor -trimpath \
		-o $(BIN_DIR)/$(BIN_NAME)-linux-amd64 ./cmd/statnive-live
	@file $(BIN_DIR)/$(BIN_NAME)-linux-amd64

## test: Run unit tests with race detector (target <5s wall time)
test:
	$(GO) test -mod=vendor -race -timeout 60s ./...

## test-integration: Run integration tests (requires `docker compose up -d clickhouse`).
## Depends on web-build so //go:embed all:dist/* in internal/dashboard/spa has
## files to embed — integration-tagged tests compile every package in the tree.
## `-v` so CI surfaces per-test PASS/FAIL lines (otherwise only FAIL shows,
## making it hard to tell which test in a suite got to which state).
## Timeout bumped to 240s so the multi-tenant post-ingest-smoke wait has
## headroom on slow CI runners (happy path still finishes in <30s).
test-integration: web-build
	$(GO) test -mod=vendor -race -tags=integration -v -timeout 240s ./test/...

## lint: Run golangci-lint + tenancy-grep + identity-gate + privacy-gate
##       + legacy-site-id-grep (v0.0.9 per-site-admin grant gate)
# golangci-lint wants filesystem paths, not the import paths `go list`
# returns; `./...` gets it to walk the tree itself and honor
# .golangci.yml's exclude list (which already covers web/node_modules).
lint: tenancy-grep identity-gate privacy-gate legacy-site-id-grep
	$(GOLANGCI_LINT) run ./...

## identity-gate: auth-return nil-guard regression (CVE-2024-10924, PLAN.md §53).
## Advisory by default (semgrep optional on dev laptops). Set STRICT_GATES=1
## (or run via `make ci-local`) to fail hard on missing semgrep — that mirrors
## .github/workflows/security-gate.yml exactly.
identity-gate:
	@if ! command -v semgrep >/dev/null 2>&1; then \
		if [ "$$STRICT_GATES" = "1" ]; then \
			echo "FAIL: semgrep missing — run 'make tools' to install pinned versions"; exit 1; \
		else \
			echo "identity-gate: semgrep not installed, skipping (run 'make tools' to enable)"; exit 0; \
		fi; \
	fi; \
	semgrep --quiet --error --config=.claude/skills/blake3-hmac-identity-review/semgrep \
		internal/ || (echo "FAIL: auth-return-nil-guard regression"; exit 1)

## privacy-gate: slog-no-raw-pii regression (Phase 7d F3, Privacy Rule 4).
## Blocks any new slog call that writes a raw PII value. Same STRICT_GATES
## semantics as identity-gate.
privacy-gate:
	@if ! command -v semgrep >/dev/null 2>&1; then \
		if [ "$$STRICT_GATES" = "1" ]; then \
			echo "FAIL: semgrep missing — run 'make tools' to install pinned versions"; exit 1; \
		else \
			echo "privacy-gate: semgrep not installed, skipping (run 'make tools' to enable)"; exit 0; \
		fi; \
	fi; \
	semgrep --quiet --error --config=.claude/skills/gdpr-code-review/semgrep \
		internal/ cmd/ || (echo "FAIL: slog-no-raw-pii regression"; exit 1)

## privacy-gate-selftest: assert the rule fires on should-trigger fixtures
## and does NOT fire on should-not-trigger fixtures. Run this after any
## change to the slog-no-raw-pii rule to guard against false-positive /
## false-negative regressions.
privacy-gate-selftest:
	@if ! command -v semgrep >/dev/null 2>&1; then \
		echo "privacy-gate-selftest: semgrep not installed"; exit 2; \
	fi
	@semgrep --quiet --error --config=.claude/skills/gdpr-code-review/semgrep \
		.claude/skills/gdpr-code-review/test/fixtures/should-trigger/ \
		&& (echo "FAIL: rule did not fire on should-trigger fixtures"; exit 1) \
		|| echo "OK: slog-no-raw-pii fires on raw-PII fixtures"
	@semgrep --quiet --error --config=.claude/skills/gdpr-code-review/semgrep \
		.claude/skills/gdpr-code-review/test/fixtures/should-not-trigger/ \
		|| (echo "FAIL: rule fired on should-not-trigger fixtures"; exit 1)
	@echo "OK: slog-no-raw-pii clean on hashed-PII fixtures"

## skill-sanitizer: supply-chain guard on skill content (Phase 7d F6).
## Scans .claude/skills/** + docs/** for Unicode Tag Block / zero-width /
## bidi codepoints. CI runs it via .github/workflows/security-gate.yml.
skill-sanitizer:
	@./scripts/skill-sanitizer.sh

## skill-sanitizer-selftest: assert the scanner fires on should-trigger
## fixtures and does NOT fire on should-not-trigger fixtures. Run after
## any change to the scanner regex.
skill-sanitizer-selftest:
	@echo "==> should-trigger fixtures (expect exit 1)"
	@if ./scripts/skill-sanitizer.sh --selftest test/fixtures/skill-sanitizer/should-trigger/ >/dev/null 2>&1; then \
		echo "FAIL: scanner did not fire on should-trigger fixtures"; exit 1; \
	else \
		echo "OK: scanner fires on should-trigger"; \
	fi
	@echo "==> should-not-trigger fixtures (expect exit 0)"
	@./scripts/skill-sanitizer.sh --selftest test/fixtures/skill-sanitizer/should-not-trigger/ >/dev/null \
		|| (echo "FAIL: scanner fired on should-not-trigger"; exit 1)
	@echo "OK: scanner clean on should-not-trigger"

## legacy-site-id-grep: CI gate — migration 010 / per-site-admin (v0.0.9).
## Asserts no NEW code reads the legacy users.site_id column outside the
## allow-listed legacy paths; new code uses auth.SitesStore / actor.Sites.
legacy-site-id-grep:
	@./scripts/check-legacy-site-id.sh

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
	$(GOLANGCI_LINT) fmt ./...

## vendor-check: Verify go.sum + vendored deps are up to date (CI gate)
## --ignore-cr-at-eol so CRLF in upstream README/CHANGELOG (klauspost/cpuid,
## ClickHouse/clickhouse-go) doesn't fail the gate on Linux CI when the
## same content was committed with LF normalization on macOS.
vendor-check:
	$(GO) mod verify
	$(GO) mod vendor
	git diff --ignore-cr-at-eol --exit-code vendor/ go.mod go.sum

## licenses: Check no AGPL / strong-copyleft deps shipped (CLAUDE.md License Rules).
## GOFLAGS=-mod=mod overrides the auto-enabled -mod=vendor (vendor/ exists);
## go-licenses can't read module metadata under vendor mode, mirrors the
## per-job env override in .github/workflows/ci.yml `licenses` job.
##
## --ignore self-reference: the module itself has no license file in the
## working tree (the project LICENSE at repo root applies to the binary).
## --ignore github.com/segmentio/asm: MIT-No-Attribution at the module
## root; v2's classifier doesn't propagate it to sub-packages, but the
## license is permissive (verified: vendor/github.com/segmentio/asm/LICENSE).
licenses:
	GOFLAGS=-mod=mod $(GO_LICENSES) check ./internal/... ./cmd/... \
		--disallowed_types=forbidden,restricted \
		--ignore github.com/statnive/statnive.live \
		--ignore github.com/segmentio/asm

## bench: Run all Go benchmarks (no integration tag — fast). Output to stdout.
bench:
	$(GO) test -mod=vendor -bench=. -benchmem -run='^$$' -timeout 5m ./internal/...

## load-test: Run the k6 7K EPS load script. Requires k6 installed +
## the binary running on $STATNIVE_URL (default http://127.0.0.1:8080).
## Pre-flight: seed the load-test site row (see test/perf/load.js header).
load-test:
	k6 run test/perf/load.js

## fast-probe: Phase 7e capacity-probe ramp (50→500 EPS over ~75 min)
## against $STATNIVE_URL. Default points at production app.statnive.live.
## Hostname load-test.example.com is unseeded → events land in
## dropped_total{reason=hostname_unknown}, no customer-data pollution.
## Plan: ~/.claude/plans/phase-7e-load-gate-scaffolding-wise-puppy.md
fast-probe:
	STATNIVE_URL=$${STATNIVE_URL:-https://app.statnive.live} k6 run test/perf/fast-probe.js

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

## airgap-install-test: Docker matrix smoke for deploy/airgap-install.sh.
## Spins up `ubuntu:24.04` AND `debian:13-slim` containers, runs the
## installer twice (first + idempotency pass), asserts /etc/statnive-live
## perm matrix (parent 0755, tls/geoip 0750 root:statnive) and that the
## `statnive` service user can traverse + read protected files. Catches
## the perm + distro-delta bug class from LEARN.md Lessons 7 & 9.
## Requires docker; runs ~30 s per distro on a warm cache.
airgap-install-test:
	./deploy/scripts/test-fresh-install.sh

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

## airgap-bundle: Assemble the offline install tarball + SHA256SUMS.
## Override VERSION=v0.8.0 to pin; defaults to `git describe` or `dev`.
## Optional: ENABLE_VENDOR_TAR=1 includes a vendor.tar.gz for source audits.
## Optional: SIGNING_KEY=/path/to/ed25519.key ssh-keygen -Y signs SHA256SUMS.
##
## Output: build/statnive-live-<VERSION>-linux-amd64-airgap/   (unpacked tree)
##         build/statnive-live-<VERSION>-linux-amd64-airgap.tar.gz
##         build/SHA256SUMS
##         build/SHA256SUMS.sig                                 (if SIGNING_KEY set)
# VERSION default — git tag if present, else "v0.0.0-dev" (NOT bare "dev"
# which collides with the v* glob in cutover scripts; LEARN.md Lesson 3).
# `make airgap-bundle` enforces a stricter regex below (release tags only).
VERSION ?= $(shell git describe --tags --dirty 2>/dev/null || echo v0.0.0-dev)
BUNDLE_NAME := statnive-live-$(VERSION)-linux-amd64-airgap
BUNDLE_DIR  := build/$(BUNDLE_NAME)

# VERSION_RELEASE_RE matches v<MAJOR>.<MINOR>.<PATCH> with optional -rcN
# or -dev suffix. The bundle target enforces this so a dirty/no-tag
# build can't produce a tarball with a malformed name. Test-only builds
# can pass VERSION=v0.0.0-dev explicitly.
VERSION_RELEASE_RE := ^v[0-9]+\.[0-9]+\.[0-9]+(-(rc[0-9]+|dev))?$$

airgap-bundle: build-linux
	@if ! echo "$(VERSION)" | grep -Eq '$(VERSION_RELEASE_RE)'; then \
		echo "airgap-bundle: VERSION='$(VERSION)' does not match $(VERSION_RELEASE_RE)"; \
		echo "  set a release tag (git tag vX.Y.Z) or pass VERSION=vX.Y.Z explicitly"; \
		exit 1; \
	fi
	@bash deploy/airgap-bundle.sh \
		VERSION="$(VERSION)" \
		BUNDLE_DIR="$(BUNDLE_DIR)" \
		BUNDLE_NAME="$(BUNDLE_NAME)" \
		BIN="$(BIN_DIR)/$(BIN_NAME)-linux-amd64" \
		ENABLE_VENDOR_TAR="$(ENABLE_VENDOR_TAR)" \
		SIGNING_KEY="$(SIGNING_KEY)"
	@$(MAKE) airgap-bundle-verify BUNDLE_DIR="$(BUNDLE_DIR)" BUNDLE_NAME="$(BUNDLE_NAME)"

## airgap-bundle-verify: Two checks: (1) SHA256SUMS matches unpacked
## tree, (2) bundle completeness — every file the install path references
## actually exists, and bin/statnive-live is ELF amd64 (not Mach-O).
## Catches Milestone 1 cutover Bugs #1, #2, #3 at build time, not at
## install time on the VPS (LEARN.md Lesson 2).
airgap-bundle-verify:
	@cd build && sha256sum -c SHA256SUMS
	@bash deploy/airgap-bundle-completeness.sh "$(BUNDLE_DIR)"

## release: Full release gate — web-build + lint + test + integration + audit + airgap-bundle.
## web-build runs first so internal/dashboard/spa/dist/ exists before any Go
## compilation step (lint / test / audit) hits the //go:embed all:dist
## directive in internal/dashboard/spa/dashboard.go. ci.yml does the same
## ordering by running `make build` (which depends on web-build) ahead of
## `make test` — this keeps `make release` self-contained.
## Does NOT push; CI is the publishing surface. Local sanity gate only.
release: web-build lint test test-integration audit airgap-bundle
	@echo "release: $(VERSION) bundle + SHA256SUMS at build/"

## release-fresh: Wipe build artifacts and run the full release gate as if
## on a clean checkout. Run this BEFORE pushing any release tag — it is
## the only validated predictor of the release.yml outcome (avoids the
## "make ci-local works locally because dist/ is cached, then release.yml
## fails on a fresh runner" trap that cost us PRs #64-#73 + this PR).
##
## Usage:
##   SIGNING_KEY=$$HOME/.ssh/statnive-release VERSION=v0.0.1-rc1 make release-fresh
release-fresh:
	rm -rf bin/ build/ internal/dashboard/spa/dist/ web/dist/
	$(MAKE) release

## ops-install-release-key: One-time per-VPS prereq for GHA-driven deploys.
## Pushes deploy/keys/release-signing.pub to /etc/statnive/release-key.pub on
## the target host so the on-box airgap-verify-bundle.sh can verify the
## Ed25519 signature produced by release.yml. Idempotent; safe to re-run.
## Required before the first release.yml run on any new VPS — without it,
## every deploy fails with "Ed25519 signature mismatch — REJECT" (LEARN.md
## Lesson 21).
##
## Usage:
##   VPS_HOST=ops@94.16.108.78 make ops-install-release-key
ops-install-release-key:
	@if [ -z "$(VPS_HOST)" ]; then \
		echo "ops-install-release-key: VPS_HOST required (e.g. VPS_HOST=ops@94.16.108.78)"; \
		exit 1; \
	fi
	scp deploy/keys/release-signing.pub "$(VPS_HOST):/tmp/release-key.pub"
	ssh "$(VPS_HOST)" 'sudo install -d -m 0755 /etc/statnive && sudo install -m 0644 /tmp/release-key.pub /etc/statnive/release-key.pub && rm /tmp/release-key.pub && echo "installed:" && cat /etc/statnive/release-key.pub'

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
	$(GO) vet -mod=vendor ./...
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
## Budget bumped 700 → 750 gz on PR D (regen after PR-E #59 endpoint-derivation chain
## landed; that PR shipped without regenerating dist, so the previous 700 gz reading
## was stale). Doc-comment header expansion (LEARN.md Lesson 24 attribution) is
## comment-only and stripped at minification — minified size was unaffected.
##
## Bumped again 1500 → 2100 / 750 → 1000 in Stage 3: the consent-free
## flow added a GPC client-probe (gated by data-statnive-honour-gpc=1)
## plus statniveLive.acceptConsent / withdrawConsent helpers that POST
## to /api/privacy/consent. ~500 B min / ~190 B gz net. Revisit in v1.1
## if the bundle keeps growing — gating the consent helpers behind a
## second data attribute would let permissive sites stay smaller.
tracker-size:
	@SIZE=$$(stat -f%z internal/tracker/dist/tracker.js 2>/dev/null || stat -c%s internal/tracker/dist/tracker.js); \
	GZIP=$$(gzip -c -9 internal/tracker/dist/tracker.js | wc -c | tr -d ' '); \
	echo "tracker.js: $$SIZE B min / $$GZIP B gz (budget: 2100 / 1000)"; \
	if [ $$SIZE -gt 2100 ] || [ $$GZIP -gt 1000 ]; then \
	  echo "FAIL: tracker bundle over budget (2100 B min / 1000 B gz)"; exit 1; \
	fi

## airgap-test: MANUAL — run the binary under iptables OUTPUT DROP and
## confirm ingest + dashboard still work (loopback whitelisted). Procedure
## documented in docs/runbook.md § Air-Gap Verification (single source of truth).
airgap-test:
	@echo "Manual procedure — see docs/runbook.md § Air-Gap Verification"

## clean: Remove build + runtime artifacts (NOT vendor/, NOT config/master.key)
clean:
	rm -rf $(BIN_DIR) wal/ data/ tmp/ coverage.out coverage.html *.prof audit.jsonl

## systemd-verify: Phase 2c — grep-based hardening check for deploy/systemd/statnive-live.service
systemd-verify:
	@bash deploy/systemd/harden-verify.sh deploy/systemd/statnive-live.service

## seed-backup-drill: Phase 2c — seed ~10 K synthetic events for the backup drill (needs docker CH up)
seed-backup-drill:
	@bash test/seed/backup-drill.sh

## backup-drill-local: Phase 2c — run the CI backup drill locally (needs CH + MinIO up on 19000/9001)
backup-drill-local:
	@bash deploy/backup/drill.sh --config=deploy/backup/config-ci.yml --mode=full

# ---------------------------------------------------------------------------
# Local CI parity (`make ci-local`)
# ---------------------------------------------------------------------------
# Mirrors .github/workflows/ci.yml + .github/workflows/security-gate.yml so a
# green local run predicts a green CI run. CodeQL stays GitHub-only.

GOPATH_BIN := $(shell go env GOPATH)/bin

## tools: Install pinned versions of golangci-lint, govulncheck, go-licenses, semgrep, playwright.
tools:
	@bash scripts/install-dev-tools.sh

## tools-check: Verify pinned tool versions are on PATH (fast-fail before ci-local).
tools-check:
	@missing=0; \
	for t in golangci-lint govulncheck go-licenses semgrep; do \
		if ! command -v "$$t" >/dev/null 2>&1 && [ ! -x "$(GOPATH_BIN)/$$t" ]; then \
			echo "MISSING: $$t (run 'make tools')"; missing=1; \
		fi; \
	done; \
	if [ "$$missing" = "1" ]; then exit 1; fi
	@echo "tools-check: all pinned tools present"

## govulncheck: Go supply-chain CVE scan (security-gate.yml job 1).
govulncheck:
	$(GOVULNCHECK) ./...

## ch-up: Bring up dev ClickHouse (deploy/docker-compose.dev.yml) and wait until ready.
ch-up:
	@docker compose -f deploy/docker-compose.dev.yml up -d --wait clickhouse
	@echo "Waiting for ClickHouse to accept connections..."
	@for i in $$(seq 1 30); do \
		if docker exec statnive-clickhouse-dev clickhouse-client --port 9000 -q "SELECT 1" >/dev/null 2>&1; then \
			echo "ClickHouse is ready (after $$i tries)"; exit 0; \
		fi; sleep 1; \
	done; \
	echo "FAIL: ClickHouse did not become ready in 30s"; exit 1

## ch-down: Tear down dev ClickHouse and remove volumes.
ch-down:
	@docker compose -f deploy/docker-compose.dev.yml down -v

## ch-reset: Stop+restart the dev ClickHouse cleanly.
ch-reset: ch-down ch-up

## web-e2e: Playwright E2E (mirrors ci.yml dashboard-e2e job).
## Requires `make ch-up` first (binary serves dashboard against dev CH).
web-e2e: web-install
	cd web && npx playwright install --with-deps chromium && npm run e2e

## ci-local-fast: Fast subset of ci-local — skips Docker-dependent jobs (CH up,
## test-integration, wal-killtest, smoke, web-e2e). Target <60s on a warm cache.
## Use as the default pre-push gate via STATNIVE_PREPUSH_FAST=1.
ci-local-fast:
	@echo "==> ci-local-fast (mirrors ci.yml minus Docker-dependent jobs)"
	$(MAKE) tools-check
	$(MAKE) vendor-check
	STRICT_GATES=1 $(MAKE) lint
	$(MAKE) skill-sanitizer
	$(MAKE) test
	$(MAKE) licenses
	$(MAKE) govulncheck
	$(MAKE) tracker-test
	$(MAKE) web-test web-lint web-build bundle-gate
	$(MAKE) brand-grep web-airgap-grep
	@echo "==> ci-local-fast PASSED"

## ci-local: Full local CI mirror — every job from ci.yml + security-gate.yml
## except CodeQL (GitHub-hosted). Requires Docker for CH-dependent steps.
## Run before push: `git push` triggers it via .githooks/pre-push.
ci-local:
	@echo "==> ci-local (full mirror of ci.yml + security-gate.yml; CodeQL stays GitHub-only)"
	$(MAKE) tools-check
	$(MAKE) vendor-check
	STRICT_GATES=1 $(MAKE) lint
	$(MAKE) skill-sanitizer
	$(MAKE) test
	$(MAKE) licenses
	$(MAKE) govulncheck
	$(MAKE) tracker-test
	$(MAKE) web-test web-lint web-build bundle-gate
	$(MAKE) brand-grep web-airgap-grep
	$(MAKE) airgap-install-test
	$(MAKE) ch-up
	@set -e; \
	trap '$(MAKE) ch-down' EXIT; \
		$(MAKE) build && \
		$(MAKE) test-integration && \
		$(MAKE) wal-killtest && \
		$(MAKE) smoke && \
		$(MAKE) web-e2e
	@echo "==> ci-local PASSED — note: CodeQL still runs only on GitHub"

## hooks: Activate the in-tree git hooks (.githooks/pre-push runs ci-local on git push).
hooks:
	@git config core.hooksPath .githooks
	@chmod +x .githooks/* 2>/dev/null || true
	@echo "Hooks activated. .githooks/pre-push will run on every 'git push'."
	@echo "Bypass once: git push --no-verify   |   Use fast subset: STATNIVE_PREPUSH_FAST=1 git push"

## help: Show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
