#!/usr/bin/env bash
# airgap-bundle.sh — compose the offline install tarball.
#
# Invoked by `make airgap-bundle`; not usually run directly. The Makefile
# passes VERSION / BUNDLE_DIR / BUNDLE_NAME / BIN as k=v args so the
# script is self-contained and doesn't depend on Makefile environment.

set -euo pipefail

# --- parse KEY=VALUE args ----------------------------------------------------
for arg in "$@"; do
	case "$arg" in
		*=*) export "$arg" ;;
		*) echo "airgap-bundle: bad arg '$arg' (expected KEY=VALUE)" >&2; exit 2 ;;
	esac
done

: "${VERSION:?VERSION is required}"
: "${BUNDLE_DIR:?BUNDLE_DIR is required}"
: "${BUNDLE_NAME:?BUNDLE_NAME is required}"
: "${BIN:?BIN is required (path to compiled binary)}"
ENABLE_VENDOR_TAR="${ENABLE_VENDOR_TAR:-}"
SIGNING_KEY="${SIGNING_KEY:-}"

if [ ! -x "$BIN" ]; then
	echo "airgap-bundle: binary not found at $BIN — run 'make build' first" >&2
	exit 1
fi

ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
OUT_TGZ="build/${BUNDLE_NAME}.tar.gz"

echo "airgap-bundle: composing $BUNDLE_DIR"
rm -rf "$BUNDLE_DIR"
mkdir -p "$BUNDLE_DIR"/{bin,config,deploy/systemd,deploy/iptables,deploy/backup,docs,internal/enrich,third_party}

# --- binary + version stamp --------------------------------------------------
# $BIN is the cross-compiled output (e.g. bin/statnive-live-linux-amd64
# from `make build-linux`); strip the GOOS/GOARCH suffix at copy-time so
# the install script + systemd unit see the canonical name `statnive-live`.
cp "$BIN" "$BUNDLE_DIR/bin/statnive-live"
chmod 0755 "$BUNDLE_DIR/bin/statnive-live"

GIT_SHA="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
cat > "$BUNDLE_DIR/VERSION" <<EOF
version:    $VERSION
git_sha:    $GIT_SHA
built_at:   $BUILD_TS
go_version: $(go version | awk '{print $3}')
EOF

# --- licenses + attribution --------------------------------------------------
cp "$ROOT/LICENSE" "$BUNDLE_DIR/LICENSE" 2>/dev/null || true
if [ -f "$ROOT/LICENSE-third-party.md" ]; then
	cp "$ROOT/LICENSE-third-party.md" "$BUNDLE_DIR/LICENSE-third-party.md"
fi

# --- docs --------------------------------------------------------------------
cp "$ROOT/docs/quickstart.md"  "$BUNDLE_DIR/docs/quickstart.md"
cp "$ROOT/docs/runbook.md"     "$BUNDLE_DIR/docs/runbook.md"
cp "$ROOT/docs/deployment.md"  "$BUNDLE_DIR/docs/deployment.md"

# --- config templates --------------------------------------------------------
# statnive-live.yaml.example ships as the production config seed; dev
# defaults (short TTLs, insecure cookies, etc.) never leave the repo.
if [ -f "$ROOT/config/statnive-live.yaml.example" ]; then
	cp "$ROOT/config/statnive-live.yaml.example" "$BUNDLE_DIR/config/statnive-live.yaml.example"
else
	echo "airgap-bundle: config/statnive-live.yaml.example missing — produce one before bundling" >&2
	exit 1
fi

# sources.yaml (channel mapper) is embedded via go:embed but we drop a
# copy for operators who want to customize referrer classification.
if [ -f "$ROOT/config/sources.yaml" ]; then
	cp "$ROOT/config/sources.yaml" "$BUNDLE_DIR/config/sources.yaml"
fi

# --- deploy scripts ----------------------------------------------------------
cp "$ROOT/deploy/systemd/statnive-live.service" "$BUNDLE_DIR/deploy/systemd/"
cp "$ROOT/deploy/iptables/rules.v4"             "$BUNDLE_DIR/deploy/iptables/"
cp "$ROOT/deploy/iptables/rules.v6"             "$BUNDLE_DIR/deploy/iptables/"
cp "$ROOT/deploy/airgap-install.sh"             "$BUNDLE_DIR/deploy/"
cp "$ROOT/deploy/airgap-update-geoip.sh"        "$BUNDLE_DIR/deploy/"
cp "$ROOT/deploy/airgap-verify-bundle.sh"       "$BUNDLE_DIR/deploy/"
# statnive-deploy.sh is the on-box deploy primitive used by the GHA
# pipeline + manual cutover (step-b § B.4 installs it to /usr/local/bin).
# Cutover Bug #1 — was missing from the bundle.
cp "$ROOT/deploy/statnive-deploy.sh"            "$BUNDLE_DIR/deploy/"
chmod 0755 "$BUNDLE_DIR/deploy/"*.sh

# --- bot detector pattern data ----------------------------------------------
# Crawler JSON is //go:embed'd into the binary, but operators may want
# to inspect / refresh the pattern file out-of-band (it's MIT-licensed
# data from monperrus/crawler-user-agents). Bundle a copy alongside.
# Cutover Bug #2 — was missing from the bundle.
#
# Size guard: <1 KB indicates a stub (the file lives at 3 bytes in dev
# trees that haven't run `make refresh-bot-patterns`). Embedding a stub
# silently degrades the bot detector to a fallback of ~60 patterns
# instead of the full ~700. Fail loudly so the next bundle build can't
# inherit the same regression.
JSON_SRC="$ROOT/internal/enrich/crawler-user-agents.json"
if [ ! -s "$JSON_SRC" ]; then
	echo "airgap-bundle: $JSON_SRC missing or empty — run 'make refresh-bot-patterns'" >&2
	exit 1
fi
JSON_BYTES="$(stat -f%z "$JSON_SRC" 2>/dev/null || stat -c%s "$JSON_SRC")"
if [ "$JSON_BYTES" -lt 1024 ]; then
	echo "airgap-bundle: $JSON_SRC is ${JSON_BYTES} bytes (<1024) — stub detected, run 'make refresh-bot-patterns' before bundling" >&2
	exit 1
fi
cp "$JSON_SRC" "$BUNDLE_DIR/internal/enrich/crawler-user-agents.json"

# backup config example (operator edits encryption key + S3 endpoint)
if [ -f "$ROOT/deploy/backup/config.yml" ]; then
	cp "$ROOT/deploy/backup/config.yml" "$BUNDLE_DIR/deploy/backup/config.yml.example"
fi

# --- third-party attribution (IP2Location BIN is NOT bundled) ----------------
cat > "$BUNDLE_DIR/third_party/IP2LOCATION-LITE-DB23.BIN.README" <<'EOF'
IP2Location LITE DB23 GeoIP database (CC-BY-SA-4.0)

The BIN file is not included in this bundle. Download it once per month
from https://lite.ip2location.com (free account required), SCP to the
server, then run:

    sudo ./deploy/airgap-update-geoip.sh /path/to/IP2LOCATION-LITE-DB23.BIN

The bundle's LICENSE-third-party.md contains the verbatim attribution
string required under CC-BY-SA-4.0 §3(a)(1); the dashboard footer and
/about endpoint render the same string at runtime.
EOF

# --- optional: vendor tarball (opt-in; ~100MB) ------------------------------
if [ -n "$ENABLE_VENDOR_TAR" ]; then
	echo "airgap-bundle: including vendor.tar.gz (ENABLE_VENDOR_TAR=$ENABLE_VENDOR_TAR)"
	tar --exclude='*.test' -czf "$BUNDLE_DIR/vendor.tar.gz" -C "$ROOT" vendor
fi

# --- checksums + tarball -----------------------------------------------------
echo "airgap-bundle: computing SHA256SUMS"
(
	cd "$BUNDLE_DIR"
	find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum > SHA256SUMS
)

echo "airgap-bundle: writing $OUT_TGZ"
rm -f "$OUT_TGZ"
tar -czf "$OUT_TGZ" -C build "$BUNDLE_NAME"

# Top-level SHA256SUMS covers the tarball itself; operators verify this
# before unpacking. Inner SHA256SUMS covers the unpacked tree so a
# partial transfer still fails verify-bundle.sh.
TGZ_SHA="$(sha256sum "$OUT_TGZ" | awk '{print $1}')"
echo "$TGZ_SHA  ${BUNDLE_NAME}.tar.gz" > build/SHA256SUMS

# --- optional: Ed25519 signature --------------------------------------------
# Uses ssh-keygen -Y sign (OpenSSH 8.0+) — no custom tooling. Signing
# key lives outside CI per the plan; operators run this with
# SIGNING_KEY=/path/to/age-decrypted-ed25519-key.
if [ -n "$SIGNING_KEY" ] && [ -f "$SIGNING_KEY" ]; then
	echo "airgap-bundle: signing SHA256SUMS with $SIGNING_KEY"
	# ssh-keygen -Y sign writes the signature to <FILE>.sig directly,
	# so build/SHA256SUMS.sig already lands at the expected path. A
	# stray `mv` here would be a self-mv that GNU coreutils rejects
	# with "are the same file" (exit 1) on Linux runners.
	ssh-keygen -Y sign -f "$SIGNING_KEY" -n statnive-live-airgap build/SHA256SUMS
else
	echo "airgap-bundle: SIGNING_KEY unset — skipping Ed25519 signature (SHA256-only bundle)"
fi

BYTES="$(stat -f%z "$OUT_TGZ" 2>/dev/null || stat -c%s "$OUT_TGZ")"
printf 'airgap-bundle: done — %s (%d bytes)\n' "$OUT_TGZ" "$BYTES"
printf '  SHA256: %s\n' "$TGZ_SHA"
