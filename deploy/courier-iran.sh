#!/usr/bin/env bash
# courier-iran.sh — outside-Iran → Iranian-VPS one-shot courier.
#
# Wraps `make airgap-bundle` + rsync + airgap-verify-bundle.sh +
# airgap-install.sh into a single command an operator runs from their
# laptop (or an outside-Iran GHA self-hosted runner — v1.1).
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
#
# Two-mode operation (A2):
#   - First courier (no /opt/statnive-live/current symlink) → run
#     airgap-install.sh to provision user / dirs / NTP / iptables /
#     systemd unit + install statnive-deploy to /usr/local/bin.
#   - Re-courier (symlink exists) → delegate to statnive-deploy deploy
#     <version> for atomic-swap, version history, and auto-revert on
#     /healthz failure. Provisioning steps don't re-run.

set -euo pipefail

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
courier-iran: outside-Iran → Iranian-VPS one-shot courier.

Required env vars:
  VERSION=    release tag (must match VERSION_RELEASE_RE — vX.Y.Z, vX.Y.Z-rcN, vX.Y.Z-dev)
  HOST=       ssh target — [user@]host[:port]
  LICENSE=    path to license.jwt to install at /etc/statnive-live/license.jwt

Optional:
  CERT_DIR=   dir containing fullchain.pem + privkey.pem (TLS PEMs from outside-Iran cert-forge)
  GEOIP_PATH= path to IP2LOCATION-LITE-DB23.BIN (skipped if unset → country_code stays "--")
  SKIP_BUILD= reuse existing build/<bundle>.tar.gz; default rebuilds via make airgap-bundle
  DRY_RUN=    print remote commands without running them (operator does still need ssh reach)

Exit codes:
  0  binary running + /healthz green via SSH tunnel
  1  precondition failed (missing flag, missing tool, ssh unreachable)
  2  bundle SHA / signature verify failed on remote
  3  first-install airgap-install.sh failed on remote
  4  /healthz never went green within HEALTHZ_TIMEOUT_S (first-install only)
  5  re-deploy (statnive-deploy deploy) failed on remote (auto-revert may have fired)

Usage via Makefile (preferred):
  make release-iran-vps VERSION=v0.0.15-dev HOST=root@1.2.3.4 LICENSE=/path/to/license.jwt
EOF
}

if [ -z "$VERSION" ] || [ -z "$HOST" ] || [ -z "$LICENSE" ]; then
	usage
	echo
	echo "courier-iran: VERSION + HOST + LICENSE are required" >&2
	exit 1
fi

# --- preflight ---------------------------------------------------------------
for tool in ssh scp rsync sha256sum; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "courier-iran: missing tool: $tool" >&2
		exit 1
	fi
done

if [ ! -r "$LICENSE" ]; then
	echo "courier-iran: license not readable: $LICENSE" >&2
	exit 1
fi

if [ -n "$CERT_DIR" ]; then
	if [ ! -r "$CERT_DIR/fullchain.pem" ] || [ ! -r "$CERT_DIR/privkey.pem" ]; then
		echo "courier-iran: CERT_DIR=$CERT_DIR missing fullchain.pem or privkey.pem" >&2
		exit 1
	fi
fi

if [ -n "$GEOIP_PATH" ] && [ ! -r "$GEOIP_PATH" ]; then
	echo "courier-iran: GEOIP_PATH=$GEOIP_PATH not readable" >&2
	exit 1
fi

# SSH ControlMaster multiplexes every subsequent ssh/scp/rsync through one
# TCP+TLS handshake — saves ~30 s of dead time over the cross-border RTT
# on a ~10-call happy path. ControlPath sits in $TMPDIR; trap cleans it up
# alongside the staging dir.
SSH_CTRL_DIR="$(mktemp -d -t courier-iran-ssh.XXXXXX)"
STAGE_DIR=""
trap 'rm -rf "$STAGE_DIR" "$SSH_CTRL_DIR" 2>/dev/null || true' EXIT
SSH_OPTS=(
	-o "ControlMaster=auto"
	-o "ControlPath=$SSH_CTRL_DIR/cm-%r@%h:%p"
	-o "ControlPersist=60s"
	-o "ConnectTimeout=10"
)

echo "courier-iran: probing $HOST (warms ControlMaster)"
if ! ssh "${SSH_OPTS[@]}" -o BatchMode=yes "$HOST" 'echo ssh-ok' >/dev/null 2>&1; then
	echo "courier-iran: ssh to $HOST failed (check key, host, network)" >&2
	exit 1
fi

# --- bundle ------------------------------------------------------------------
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BUNDLE_NAME="statnive-live-${VERSION}-linux-amd64-airgap"
BUNDLE_TGZ="$ROOT/build/${BUNDLE_NAME}.tar.gz"

if [ "$SKIP_BUILD" != "1" ]; then
	echo "courier-iran: building $BUNDLE_NAME (set SKIP_BUILD=1 to reuse existing)"
	(cd "$ROOT" && make airgap-bundle VERSION="$VERSION")
fi

if [ ! -r "$BUNDLE_TGZ" ]; then
	echo "courier-iran: bundle not found: $BUNDLE_TGZ" >&2
	echo '  re-run without SKIP_BUILD=1, or run `make airgap-bundle VERSION=...` manually' >&2
	exit 1
fi

# --- compose courier payload -------------------------------------------------
# Stage bundle + license + (optional) certs + (optional) GeoIP under one
# tempdir, then rsync the whole tree atomically. The remote install pulls
# each artifact from $REMOTE_STAGE.
STAGE_DIR="$(mktemp -d -t courier-iran.XXXXXX)"

cp "$BUNDLE_TGZ" "$STAGE_DIR/"
for sidecar in "$ROOT/build/SHA256SUMS" "$ROOT/build/SHA256SUMS.sig"; do
	if [ -f "$sidecar" ]; then
		cp "$sidecar" "$STAGE_DIR/"
	fi
done

cp "$LICENSE" "$STAGE_DIR/license.jwt"
chmod 0640 "$STAGE_DIR/license.jwt"

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

echo "courier-iran: preparing $REMOTE_STAGE on $HOST"
run_remote "mkdir -p '$REMOTE_STAGE' && chmod 0700 '$REMOTE_STAGE'"

echo "courier-iran: rsync $STAGE_DIR/ → $HOST:$REMOTE_STAGE (-C compression per LEARN 28)"
if [ "$DRY_RUN" = "1" ]; then
	echo "  [DRY] rsync -avz $STAGE_DIR/ $HOST:$REMOTE_STAGE/"
else
	rsync -avz \
		-e "ssh ${SSH_OPTS[*]} -C" \
		"$STAGE_DIR/" "$HOST:$REMOTE_STAGE/"
fi

# --- verify bundle on remote -------------------------------------------------
echo "courier-iran: verifying bundle SHA on $HOST"
if ! run_remote "cd '$REMOTE_STAGE' && tar -xzf '${BUNDLE_NAME}.tar.gz' && bash '${BUNDLE_NAME}/deploy/airgap-verify-bundle.sh' '${BUNDLE_NAME}.tar.gz'"; then
	echo "courier-iran: remote bundle verify failed" >&2
	exit 2
fi

# Build the artifact-install heredoc body once; invoked in both branches
# at the appropriate point. Strings interpolate $REMOTE_STAGE +
# $BUNDLE_NAME at heredoc construction time; the inner script runs as
# root on the box via sudo bash -s.
CERT_INSTALL=""
if [ -n "$CERT_DIR" ]; then
	CERT_INSTALL="install -m 0644 -o root -g statnive '$REMOTE_STAGE/tls/fullchain.pem' /etc/statnive-live/tls/fullchain.pem
install -m 0640 -o root -g statnive '$REMOTE_STAGE/tls/privkey.pem'   /etc/statnive-live/tls/privkey.pem"
fi

GEOIP_INSTALL=""
if [ -n "$GEOIP_PATH" ]; then
	GEOIP_INSTALL="bash '$REMOTE_STAGE/${BUNDLE_NAME}/deploy/airgap-update-geoip.sh' '$REMOTE_STAGE/IP2LOCATION-LITE-DB23.BIN'"
fi

# WP1's loader reads license.file from /etc/statnive-live/config.yaml.
# Two-level grep guards re-courier idempotency AND catches the partial-key
# edge case (block exists but file: leaf missing — would fail at boot).
WIRE_LICENSE='
if ! grep -qE "^[[:space:]]*file:[[:space:]]*/" /etc/statnive-live/config.yaml || ! grep -q "^license:" /etc/statnive-live/config.yaml; then
	printf "\nlicense:\n  file: /etc/statnive-live/license.jwt\n" >> /etc/statnive-live/config.yaml
fi
'

install_artifacts() {
	echo "courier-iran: installing license / TLS / GeoIP / config.yaml license.file:"
	run_remote "sudo bash -se" <<REMOTE
set -euo pipefail
install -m 0640 -o root -g statnive '$REMOTE_STAGE/license.jwt' /etc/statnive-live/license.jwt
$CERT_INSTALL
$GEOIP_INSTALL
$WIRE_LICENSE
REMOTE
}

# --- install OR re-deploy ---------------------------------------------------
# Detect whether the VPS is already provisioned. /opt/statnive-live/current
# is the canonical symlink statnive-deploy maintains (statnive-deploy.sh:160
# uses the same test); its presence means we've installed before and should
# use the atomic-swap path. Single-operator assumption — no flock guard.
if run_remote "test -L /opt/statnive-live/current" 2>/dev/null; then
	REDEPLOY=1
else
	REDEPLOY=0
fi

if [ "$REDEPLOY" = "1" ]; then
	echo "courier-iran: re-deploy mode — using statnive-deploy (atomic swap + auto-revert)"

	# Install license + cert + GeoIP BEFORE statnive-deploy so the new
	# binary starts up reading the new files in one motion. Doing this
	# AFTER the deploy would leave the new binary briefly reading the
	# OLD cert + GeoIP until the next SIGHUP — and statnive-deploy
	# doesn't fire SIGHUP, only restart, so the old state would persist
	# across the deploy.
	install_artifacts

	# statnive-deploy expects the bundle tarball at /opt/statnive-bundles/incoming/
	# (its conventional drop dir, created by airgap-install.sh on first install).
	if ! run_remote "sudo install -m 0644 -o root -g root '$REMOTE_STAGE/${BUNDLE_NAME}.tar.gz' /opt/statnive-bundles/incoming/${BUNDLE_NAME}.tar.gz"; then
		echo "courier-iran: stage tarball into /opt/statnive-bundles/incoming/ failed" >&2
		exit 5
	fi

	# Stage matching SHA + sig — statnive-deploy.sh:223 invokes
	# airgap-verify-bundle.sh which reads SHA256SUMS (mandatory) and
	# SHA256SUMS.sig (optional, when SIGNING_KEY was set at build).
	for sidecar in SHA256SUMS SHA256SUMS.sig; do
		if run_remote "test -f '$REMOTE_STAGE/$sidecar'" >/dev/null 2>&1; then
			if ! run_remote "sudo install -m 0644 -o root -g root '$REMOTE_STAGE/$sidecar' /opt/statnive-bundles/incoming/$sidecar"; then
				echo "courier-iran: WARN — staging sidecar $sidecar failed (non-fatal; statnive-deploy will report missing if required)" >&2
			fi
		fi
	done

	if ! run_remote "sudo /usr/local/bin/statnive-deploy deploy '$VERSION'"; then
		echo "courier-iran: statnive-deploy deploy '$VERSION' failed (the script auto-reverts to the previous version on /healthz failure — check journalctl)" >&2
		exit 5
	fi
else
	# Keep --ntp-profile=asiatech in sync with the allow-list at
	# deploy/airgap-install.sh:46.
	INSTALL_FLAGS="--ntp-profile=asiatech --apply-iptables"

	echo "courier-iran: first-install mode — airgap-install.sh $INSTALL_FLAGS on $HOST"
	if ! run_remote "sudo bash '$REMOTE_STAGE/${BUNDLE_NAME}/deploy/airgap-install.sh' $INSTALL_FLAGS"; then
		echo "courier-iran: airgap-install.sh failed" >&2
		exit 3
	fi

	# First-install: airgap-install.sh enabled the unit but did NOT
	# start it. The artifact install + the explicit systemctl restart
	# below are the cold start.
	install_artifacts
fi

# --- start + wait ------------------------------------------------------------
# Re-deploy mode: statnive-deploy already did restart + healthz wait +
# auto-revert. Don't restart again (would defeat the auto-revert by
# replacing the binary the deploy script is monitoring) and don't poll
# again — the deploy subcommand's exit code already decided.
if [ "$REDEPLOY" = "1" ]; then
	echo "courier-iran: re-deploy completed (statnive-deploy handled restart + healthz)"
else
	echo "courier-iran: restarting statnive-live"
	run_remote "sudo systemctl restart statnive-live"

	if [ "$DRY_RUN" = "1" ]; then
		echo "courier-iran: DRY_RUN=1 — skipping healthz wait"
		exit 0
	fi

	# Delegate healthz discovery to statnive-deploy.sh's derive_healthz_url(),
	# which already (a) honors STATNIVE_HEALTHZ_URL, (b) reads systemd
	# drop-ins, (c) reads config.yaml, (d) rewrites 0.0.0.0:N → 127.0.0.1:N,
	# (e) auto-adds curl -k when TLS is configured. LEARN.md Lesson 20.
	# A single remote loop avoids ~45 cross-border SSH handshakes and means
	# the comment math (1 poll per 2 s) is honest.
	echo "courier-iran: waiting up to ${HEALTHZ_TIMEOUT_S}s for /healthz (LEARN 25 — cold boot 35–50 s)"
	iter_max=$(( HEALTHZ_TIMEOUT_S / 2 ))
	if ! run_remote "iter_max=$iter_max; for i in \$(seq 1 \$iter_max); do
		if bash '$REMOTE_STAGE/${BUNDLE_NAME}/deploy/statnive-deploy.sh' health >/dev/null 2>&1; then
			echo \"courier-iran: /healthz OK after \$(( i * 2 ))s\"; exit 0
		fi
		sleep 2
	done
	echo 'courier-iran: /healthz never went green' >&2; exit 4"; then
		exit 4
	fi
fi

cat <<EOF

courier-iran: dry-run complete. On the box:
  ssh $HOST 'bash $REMOTE_STAGE/${BUNDLE_NAME}/deploy/statnive-deploy.sh health'
  ssh $HOST 'sudo systemctl status statnive-live'
  ssh $HOST 'chronyc tracking'
  ssh $HOST 'sudo iptables -L OUTPUT -v --line-numbers'
EOF
