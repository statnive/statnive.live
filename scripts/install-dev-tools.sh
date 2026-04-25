#!/usr/bin/env bash
# Install the dev toolchain pinned to the exact versions GitHub Actions uses,
# so `make ci-local` is byte-equivalent to a CI run on the same SHA.
#
# Sources of truth (bump together):
#   - .github/workflows/ci.yml          (golangci-lint, go-licenses, Node, Go)
#   - .github/workflows/security-gate.yml (govulncheck, semgrep)
#
# Idempotent: each tool checks the installed version and skips if it matches.

set -euo pipefail

# Pinned versions — keep in sync with workflow files.
GOLANGCI_LINT_VERSION="v2.5.0"
GOVULNCHECK_VERSION="v1.1.4"
GO_LICENSES_VERSION="3e084b0caf710f7bfead967567539214f598c0a2"  # v2.0.1 SHA used in ci.yml
SEMGREP_VERSION="1.91.0"

GOPATH_BIN="$(go env GOPATH)/bin"
mkdir -p "$GOPATH_BIN"

log() { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
ok()  { printf '\033[1;32mOK\033[0m  %s\n' "$*"; }
skip(){ printf '\033[1;33m--\033[0m  %s\n' "$*"; }

# --- golangci-lint -----------------------------------------------------------
have_golangci=""
if [ -x "$GOPATH_BIN/golangci-lint" ]; then
  have_golangci="$("$GOPATH_BIN/golangci-lint" version 2>&1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || true)"
fi
if [ "$have_golangci" = "$GOLANGCI_LINT_VERSION" ]; then
  skip "golangci-lint $GOLANGCI_LINT_VERSION already installed"
else
  log "Installing golangci-lint $GOLANGCI_LINT_VERSION → $GOPATH_BIN"
  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \
    | sh -s -- -b "$GOPATH_BIN" "$GOLANGCI_LINT_VERSION"
  ok "golangci-lint $GOLANGCI_LINT_VERSION"
fi

# --- govulncheck -------------------------------------------------------------
have_govulncheck=""
if [ -x "$GOPATH_BIN/govulncheck" ]; then
  have_govulncheck="$("$GOPATH_BIN/govulncheck" -version 2>&1 | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -n1 || true)"
fi
if [ "$have_govulncheck" = "$GOVULNCHECK_VERSION" ]; then
  skip "govulncheck $GOVULNCHECK_VERSION already installed"
else
  log "Installing govulncheck $GOVULNCHECK_VERSION"
  GOFLAGS=-mod=mod go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
  ok "govulncheck $GOVULNCHECK_VERSION"
fi

# --- go-licenses -------------------------------------------------------------
# v2.0.1 (3e084b0) supersedes the older v1.6.0 (5348b744...) pin: v2 handles
# Go 1.21+ toolchain split where stdlib lives in $GOPATH/pkg/mod/golang.org/
# toolchain@... and v1 errored on "Package X does not have module info" for
# every stdlib import. Install path is /v2 per the module-version layout.
# Idempotent re-install if SHA changes (force overwrite).
have_licenses_sha=""
if [ -x "$GOPATH_BIN/go-licenses" ] && [ -f "$GOPATH_BIN/go-licenses.sha" ]; then
  have_licenses_sha="$(cat "$GOPATH_BIN/go-licenses.sha" 2>/dev/null || true)"
fi
if [ "$have_licenses_sha" = "$GO_LICENSES_VERSION" ]; then
  skip "go-licenses $GO_LICENSES_VERSION already installed"
else
  log "Installing go-licenses $GO_LICENSES_VERSION (v2)"
  GOFLAGS=-mod=mod go install "github.com/google/go-licenses/v2@${GO_LICENSES_VERSION}"
  # printf no trailing newline so cat readback compares bytes-exact.
  printf '%s' "$GO_LICENSES_VERSION" > "$GOPATH_BIN/go-licenses.sha"
  ok "go-licenses $GO_LICENSES_VERSION"
fi

# --- semgrep -----------------------------------------------------------------
have_semgrep=""
if command -v semgrep >/dev/null 2>&1; then
  have_semgrep="$(semgrep --version 2>/dev/null | head -n1 || true)"
fi
if [ "$have_semgrep" = "$SEMGREP_VERSION" ]; then
  skip "semgrep $SEMGREP_VERSION already installed"
else
  log "Installing semgrep $SEMGREP_VERSION"
  if command -v pipx >/dev/null 2>&1; then
    pipx install --force "semgrep==${SEMGREP_VERSION}"
  elif command -v python3 >/dev/null 2>&1; then
    python3 -m pip install --user --upgrade "semgrep==${SEMGREP_VERSION}"
  else
    echo "ERROR: need pipx or python3 to install semgrep" >&2
    exit 1
  fi
  ok "semgrep $SEMGREP_VERSION"
fi

# --- Playwright chromium ------------------------------------------------------
# Browsers cache under ~/Library/Caches/ms-playwright (mac) or ~/.cache/ms-playwright (linux).
# `playwright install` is itself idempotent — skip the no-op only if the cache
# directory already has a chromium bundle.
PW_CACHE_MAC="$HOME/Library/Caches/ms-playwright"
PW_CACHE_LINUX="$HOME/.cache/ms-playwright"
if [ -d "$PW_CACHE_MAC/chromium-"* ] 2>/dev/null || [ -d "$PW_CACHE_LINUX/chromium-"* ] 2>/dev/null; then
  skip "playwright chromium browser already cached"
else
  log "Installing playwright chromium (with-deps)"
  ( cd web && npm ci --prefer-offline --no-audit --no-fund && npx playwright install --with-deps chromium )
  ok "playwright chromium installed"
fi

echo
ok "all dev tools installed at pinned versions"
echo "Run 'make tools-check' to verify on every shell."
