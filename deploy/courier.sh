#!/usr/bin/env bash
# courier.sh — multi-posture courier: builds, ships, and installs an
# airgap bundle from an outside-Iran operator machine to any target VPS.
#
# Wraps `make airgap-bundle` + rsync + airgap-verify-bundle.sh +
# airgap-install.sh into a single command for all deployment postures.
#
# Why a courier wrapper:
#   - Go module proxy + GitHub + Let's Encrypt are unreachable from
#     Iranian IP space (CLAUDE.md anti-patterns). The binary MUST be
#     built outside Iran and ship as a self-contained tarball.
#   - `iranian-dc-deploy` skill items 1–2: no Cloudflare on `.ir`, no
#     ACME-from-Iran. TLS PEMs are issued on the outside-Iran
#     cert-forge and rsync'd inward; the courier stages them in the
#     same trip that ships the binary so the operator does one push,
#     not three.
#   - LEARN.md Lesson 28: scp -C / rsync --rsh='ssh -C' is mandatory
#     on Netcup→VPS; expected on the cross-border path too.
#   - LEARN.md Lesson 29: stage in /var/tmp (same-fs as /etc), not
#     /tmp (tmpfs on Asiatech). Required for airgap-update-geoip.sh's
#     atomic-mv guard.

set -euo pipefail

POSTURE="${POSTURE:-}"
VERSION="${VERSION:-}"
HOST="${HOST:-}"
LICENSE="${LICENSE:-}"
CERT_DIR="${CERT_DIR:-}"
GEOIP_PATH="${GEOIP_PATH:-}"
SKIP_BUILD="${SKIP_BUILD:-0}"
DRY_RUN="${DRY_RUN:-0}"
HEALTHZ_TIMEOUT_S="${HEALTHZ_TIMEOUT_S:-90}"
REMOTE_STAGE="${REMOTE_STAGE:-/var/tmp/statnive-courier}"

usage() {
	cat <<'EOF'
courier: multi-posture outside-Iran → VPS one-shot courier.

Required env vars:
  POSTURE=    deployment posture: saas | outside-iran | inside-iran
  VERSION=    release tag (must match VERSION_RELEASE_RE — vX.Y.Z, vX.Y.Z-rcN, vX.Y.Z-dev)
  HOST=       ssh target — [user@]host[:port]
  LICENSE=    path to license.jwt (required for outside-iran and inside-iran; omit for saas)

Optional:
  CERT_DIR=   dir containing fullchain.pem + privkey.pem (TLS PEMs from outside-Iran cert-forge)
  GEOIP_PATH= path to IP2LOCATION-LITE-DB23.BIN (skipped if unset → country_code stays "--")
  SKIP_BUILD= reuse existing build/<bundle>.tar.gz; default rebuilds via make airgap-bundle
  DRY_RUN=    print remote commands without running them (operator does still need ssh reach)

Exit codes:
  0  binary running + /healthz green via SSH tunnel
  1  precondition failed (missing env var, missing tool, ssh unreachable)
  2  bundle SHA / signature verify failed on remote
  3  airgap-install.sh failed on remote
  4  /healthz never went green within HEALTHZ_TIMEOUT_S

Usage via Makefile (preferred):
  make release-customer POSTURE=inside-iran VERSION=v0.0.15 HOST=root@1.2.3.4 \
      LICENSE=/path/to/license.jwt
  make release-customer POSTURE=saas VERSION=v0.0.15 HOST=root@1.2.3.4
EOF
}

# --- required-field validation -----------------------------------------------
case "$POSTURE" in
	saas|outside-iran|inside-iran) ;;
	*)
		usage
		echo
		echo "courier: POSTURE must be one of: saas, outside-iran, inside-iran" >&2
		exit 1
		;;
esac

if [ -z "$VERSION" ] || [ -z "$HOST" ]; then
	usage
	echo
	echo "courier: VERSION + HOST are required" >&2
	exit 1
fi

NEEDS_LICENSE=0
if [ "$POSTURE" = "outside-iran" ] || [ "$POSTURE" = "inside-iran" ]; then
	NEEDS_LICENSE=1
fi

if [ "$NEEDS_LICENSE" = "1" ] && [ -z "$LICENSE" ]; then
	usage
	echo
	echo "courier: LICENSE is required for posture=$POSTURE" >&2
	exit 1
fi

# --- tool preflight ----------------------------------------------------------
for tool in ssh scp rsync sha256sum; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "courier: missing tool: $tool" >&2
		exit 1
	fi
done

if [ "$NEEDS_LICENSE" = "1" ] && [ ! -r "$LICENSE" ]; then
	echo "courier: license not readable: $LICENSE" >&2
	exit 1
fi

if [ -n "$CERT_DIR" ]; then
	if [ ! -r "$CERT_DIR/fullchain.pem" ] || [ ! -r "$CERT_DIR/privkey.pem" ]; then
		echo "courier: CERT_DIR=$CERT_DIR missing fullchain.pem or privkey.pem" >&2
		exit 1
	fi
fi

if [ -n "$GEOIP_PATH" ] && [ ! -r "$GEOIP_PATH" ]; then
	echo "courier: GEOIP_PATH=$GEOIP_PATH not readable" >&2
	exit 1
fi

# SSH ControlMaster multiplexes every subsequent ssh/scp/rsync through one
# TCP+TLS handshake — saves ~30 s of dead time over the cross-border RTT
# on a ~10-call happy path. ControlPath sits in $TMPDIR; trap cleans it up
# alongside the staging dir.
SSH_CTRL_DIR="$(mktemp -d -t courier-ssh.XXXXXX)"
STAGE_DIR=""
trap 'rm -rf "$STAGE_DIR" "$SSH_CTRL_DIR" 2>/dev/null || true' EXIT
SSH_OPTS=(
	-o "ControlMaster=auto"
	-o "ControlPath=$SSH_CTRL_DIR/cm-%r@%h:%p"
	-o "ControlPersist=60s"
	-o "ConnectTimeout=10"
)

echo "courier: probing $HOST (warms ControlMaster)"
if ! ssh "${SSH_OPTS[@]}" -o BatchMode=yes "$HOST" 'echo ssh-ok' >/dev/null 2>&1; then
	echo "courier: ssh to $HOST failed (check key, host, network)" >&2
	exit 1
fi

# --- bundle ------------------------------------------------------------------
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUNDLE_NAME="statnive-live-${VERSION}-linux-amd64-airgap"
BUNDLE_TGZ="$ROOT/build/${BUNDLE_NAME}.tar.gz"

if [ "$SKIP_BUILD" != "1" ]; then
	echo "courier: building $BUNDLE_NAME (set SKIP_BUILD=1 to reuse existing)"
	(cd "$ROOT" && make airgap-bundle VERSION="$VERSION")
fi

if [ ! -r "$BUNDLE_TGZ" ]; then
	echo "courier: bundle not found: $BUNDLE_TGZ" >&2
	echo '  re-run without SKIP_BUILD=1, or run `make airgap-bundle VERSION=...` manually' >&2
	exit 1
fi

# --- compose courier payload -------------------------------------------------
# Stage bundle + (optional) license + certs + GeoIP under one tempdir, then
# rsync the whole tree atomically. The remote install pulls each artifact from
# $REMOTE_STAGE.
STAGE_DIR="$(mktemp -d -t courier.XXXXXX)"

cp "$BUNDLE_TGZ" "$STAGE_DIR/"
for sidecar in "$ROOT/build/SHA256SUMS" "$ROOT/build/SHA256SUMS.sig"; do
	if [ -f "$sidecar" ]; then
		cp "$sidecar" "$STAGE_DIR/"
	fi
done

if [ "$NEEDS_LICENSE" = "1" ]; then
	cp "$LICENSE" "$STAGE_DIR/license.jwt"
	chmod 0640 "$STAGE_DIR/license.jwt"
fi

if [ -n "$CERT_DIR" ]; then
	mkdir -p "$STAGE_DIR/tls"
	install -m 0644 "$CERT_DIR/fullchain.pem" "$STAGE_DIR/tls/fullchain.pem"
	install -m 0600 "$CERT_DIR/privkey.pem"   "$STAGE_DIR/tls/privkey.pem"
fi

if [ -n "$GEOIP_PATH" ]; then
	cp "$GEOIP_PATH" "$STAGE_DIR/IP2LOCATION-LITE-DB23.BIN"
fi

ls -lh "$STAGE_DIR"

# --- transfer ----------------------------------------------------------------
run_remote() {
	if [ "$DRY_RUN" = "1" ]; then
		echo "  [DRY] ssh $HOST -- $*"
		return 0
	fi
	ssh "${SSH_OPTS[@]}" "$HOST" "$@"
}

echo "courier: preparing $REMOTE_STAGE on $HOST"
run_remote "mkdir -p '$REMOTE_STAGE' && chmod 0700 '$REMOTE_STAGE'"

echo "courier: rsync $STAGE_DIR/ → $HOST:$REMOTE_STAGE (-C compression per LEARN 28)"
if [ "$DRY_RUN" = "1" ]; then
	echo "  [DRY] rsync -avz $STAGE_DIR/ $HOST:$REMOTE_STAGE/"
else
	rsync -avz \
		-e "ssh ${SSH_OPTS[*]} -C" \
		"$STAGE_DIR/" "$HOST:$REMOTE_STAGE/"
fi

# --- verify bundle on remote -------------------------------------------------
echo "courier: verifying bundle SHA on $HOST"
if ! run_remote "cd '$REMOTE_STAGE' && tar -xzf '${BUNDLE_NAME}.tar.gz' && bash '${BUNDLE_NAME}/deploy/airgap-verify-bundle.sh' '${BUNDLE_NAME}.tar.gz'"; then
	echo "courier: remote bundle verify failed" >&2
	exit 2
fi

# --- install -----------------------------------------------------------------
echo "courier: airgap-install.sh --posture=$POSTURE on $HOST"
if ! run_remote "sudo bash '$REMOTE_STAGE/${BUNDLE_NAME}/deploy/airgap-install.sh' --posture='$POSTURE'"; then
	echo "courier: airgap-install.sh failed" >&2
	exit 3
fi

# --- artifacts: license (non-saas) + cert + geoip ---------------------------
# Build a single remote bash body so all artifact installs share one
# ControlMaster-multiplexed exec rather than N separate SSH calls.
REMOTE_BODY=""

if [ "$NEEDS_LICENSE" = "1" ]; then
	# WP1's loader reads license.file from /etc/statnive-live/config.yaml.
	# Two-level grep guards re-courier idempotency AND catches the partial-key
	# edge case (block exists but file: leaf missing — would fail at boot).
	# Single-quoted to keep grep regex / printf format-strings byte-for-byte;
	# the outer <<REMOTE heredoc still expands $REMOTE_BODY into the stream.
	REMOTE_BODY="install -m 0640 -o root -g statnive '$REMOTE_STAGE/license.jwt' /etc/statnive-live/license.jwt"$'\n'
	REMOTE_BODY+='if ! grep -qE "^[[:space:]]*file:[[:space:]]*/" /etc/statnive-live/config.yaml || ! grep -q "^license:" /etc/statnive-live/config.yaml; then
  printf "\nlicense:\n  file: /etc/statnive-live/license.jwt\n" >> /etc/statnive-live/config.yaml
fi
'
fi

if [ -n "$CERT_DIR" ]; then
	REMOTE_BODY+="install -m 0644 -o root -g statnive '$REMOTE_STAGE/tls/fullchain.pem' /etc/statnive-live/tls/fullchain.pem"$'\n'
	REMOTE_BODY+="install -m 0640 -o root -g statnive '$REMOTE_STAGE/tls/privkey.pem'   /etc/statnive-live/tls/privkey.pem"$'\n'
fi

if [ -n "$GEOIP_PATH" ]; then
	REMOTE_BODY+="bash '$REMOTE_STAGE/${BUNDLE_NAME}/deploy/airgap-update-geoip.sh' '$REMOTE_STAGE/IP2LOCATION-LITE-DB23.BIN'"$'\n'
fi

if [ -n "$REMOTE_BODY" ]; then
	echo "courier: installing artifacts on $HOST"
	run_remote "sudo bash -se" <<REMOTE
set -euo pipefail
$REMOTE_BODY
REMOTE
fi

# --- start + wait ------------------------------------------------------------
echo "courier: restarting statnive-live"
run_remote "sudo systemctl restart statnive-live"

if [ "$DRY_RUN" = "1" ]; then
	echo "courier: DRY_RUN=1 — skipping healthz wait"
	exit 0
fi

# Delegate healthz discovery to statnive-deploy.sh's derive_healthz_url(),
# which already (a) honors STATNIVE_HEALTHZ_URL, (b) reads systemd drop-ins,
# (c) reads config.yaml, (d) rewrites 0.0.0.0:N → 127.0.0.1:N, (e) auto-adds
# curl -k when TLS is configured. LEARN.md Lesson 20.
# A single remote loop avoids ~45 cross-border SSH handshakes.
echo "courier: waiting up to ${HEALTHZ_TIMEOUT_S}s for /healthz (LEARN 25 — cold boot 35–50 s)"
iter_max=$(( HEALTHZ_TIMEOUT_S / 2 ))
if ! run_remote "iter_max=$iter_max; for i in \$(seq 1 \$iter_max); do
	if bash '$REMOTE_STAGE/${BUNDLE_NAME}/deploy/statnive-deploy.sh' health >/dev/null 2>&1; then
		echo \"courier: /healthz OK after \$(( i * 2 ))s\"; exit 0
	fi
	sleep 2
done
echo 'courier: /healthz never went green' >&2; exit 4"; then
	exit 4
fi

echo ""
echo "courier: $POSTURE install complete on $HOST. Verify:"
echo "  ssh $HOST 'bash $REMOTE_STAGE/${BUNDLE_NAME}/deploy/statnive-deploy.sh health'"
echo "  ssh $HOST 'sudo systemctl status statnive-live'"
if [ "$POSTURE" = "inside-iran" ]; then
	echo "  ssh $HOST 'chronyc tracking'"
	echo "  ssh $HOST 'sudo iptables -L OUTPUT -v --line-numbers'"
fi
