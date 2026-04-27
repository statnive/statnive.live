#!/usr/bin/env bash
# statnive-deploy.sh — on-box deploy primitive for the Phase 8b GHA pipeline.
#
# Installed at /usr/local/bin/statnive-deploy on the SaaS Netcup VPS.
# Invoked over SSH by .github/workflows/deploy-saas.yml after a release
# tarball is SCP'd into /opt/statnive-bundles/incoming/. Idempotent;
# safe to re-invoke against the same version.
#
# Subcommands:
#   deploy <version>   verify + extract + atomic-swap + restart + healthz wait
#                      auto-reverts to previous version on healthz failure
#   rollback <version> swap back to a previously-installed version
#   versions           list installed bundle versions, current marked
#   health             curl /healthz and exit non-zero on degraded
#
# Layout (created by airgap-install.sh + this script):
#   /etc/statnive/release-key.pub          Ed25519 pubkey (OpenSSH format)
#   /opt/statnive-bundles/incoming/        SCP drop dir (writable by deploy user)
#   /opt/statnive-bundles/<version>/       extracted bundle tree (kept for rollback)
#   /opt/statnive-live/current             symlink → /opt/statnive-bundles/<version>
#   /usr/local/bin/statnive-live           binary (copy from current/bin/)
#
# Trust model: invoked via `sudo /usr/local/bin/statnive-deploy <args>`.
# The deploy user has NOPASSWD sudoers for this one path. Argument
# validation here is the boundary; treat <version> as untrusted input.

set -euo pipefail

INCOMING_DIR=/opt/statnive-bundles/incoming
BUNDLES_DIR=/opt/statnive-bundles
CURRENT_LINK=/opt/statnive-live/current
PUBKEY=/etc/statnive/release-key.pub
CONFIG_FILE="${STATNIVE_CONFIG_FILE:-/etc/statnive-live/config.yaml}"
HEALTHZ_TIMEOUT_S="${STATNIVE_HEALTHZ_TIMEOUT_S:-30}"

# derive_healthz_url derives the probe URL from the runtime config so the
# probe matches the binary's actual listen address + scheme. The hardcoded
# http://127.0.0.1:8080 default in earlier versions caused every successful
# release deploy to be reported as failed when the binary listens on
# 0.0.0.0:443 with TLS (LEARN.md Lesson 20). Order:
#   1. STATNIVE_HEALTHZ_URL env var (operator override).
#   2. server.listen + tls.cert_file from /etc/statnive-live/config.yaml.
#   3. Fallback to http://127.0.0.1:8080/healthz (matches binary defaults).
# CURL_OPTS gets `-k` whenever the scheme is https, since the probe targets
# 127.0.0.1 but the cert is typically issued for the public hostname.
derive_healthz_url() {
	if [ -n "${STATNIVE_HEALTHZ_URL:-}" ]; then
		HEALTHZ_URL="$STATNIVE_HEALTHZ_URL"
		case "$HEALTHZ_URL" in https://*) CURL_OPTS="-k";; *) CURL_OPTS="";; esac
		return 0
	fi

	local listen scheme
	listen=""
	scheme="http"

	if [ -r "$CONFIG_FILE" ]; then
		# server: { listen: "host:port" } — match the FIRST listen: line under server.
		listen="$(awk '
			/^server:/        { in_s=1; next }
			in_s && /^[^[:space:]]/ { in_s=0 }
			in_s && /^[[:space:]]+listen:/ {
				sub(/.*listen:[[:space:]]*/, "")
				sub(/[[:space:]]*#.*/, "")
				gsub(/^"|"$/, "")
				gsub(/^'\''|'\''$/, "")
				gsub(/[[:space:]]+$/, "")
				print; exit
			}' "$CONFIG_FILE")"
		# tls: { cert_file: "..." } — presence flips scheme to https.
		if grep -qE '^[[:space:]]*cert_file:[[:space:]]*[^[:space:]#]' "$CONFIG_FILE"; then
			scheme="https"
		fi
	fi

	# Bind 0.0.0.0 / :: → probe via 127.0.0.1 / [::1] for loopback.
	case "$listen" in
		"0.0.0.0:"*) listen="127.0.0.1:${listen#0.0.0.0:}";;
		"[::]:"*)    listen="[::1]:${listen#\[::\]:}";;
		"")          listen="127.0.0.1:8080";;
	esac

	HEALTHZ_URL="${scheme}://${listen}/healthz"

	if [ "$scheme" = "https" ]; then
		CURL_OPTS="-k"
	else
		CURL_OPTS=""
	fi
}

derive_healthz_url

log()  { printf 'statnive-deploy: %s\n' "$*"; }
fail() { printf 'statnive-deploy: %s\n' "$*" >&2; exit 1; }

require_root() {
	[ "$(id -u)" -eq 0 ] || fail "must run as root (sudo)"
}

# Validate <version> matches the airgap-bundle naming: vX.Y.Z[-suffix]
valid_version() {
	[[ "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]]
}

bundle_basename() {
	printf 'statnive-live-%s-linux-amd64-airgap' "$1"
}

current_version() {
	if [ -L "$CURRENT_LINK" ]; then
		basename "$(readlink -f "$CURRENT_LINK")"
	else
		echo none
	fi
}

healthz_wait() {
	local deadline=$(( $(date +%s) + HEALTHZ_TIMEOUT_S ))
	while [ "$(date +%s)" -lt "$deadline" ]; do
		if body="$(curl -fsS $CURL_OPTS "$HEALTHZ_URL" 2>/dev/null)"; then
			# Accept either {"status":"ok",...} or {"clickhouse":"up",...}.
			if printf '%s' "$body" | grep -qE '"(status|clickhouse)"\s*:\s*"(ok|up)"'; then
				log "healthz OK: $body"
				return 0
			fi
		fi
		sleep 1
	done
	log "healthz timed out after ${HEALTHZ_TIMEOUT_S}s; last body: ${body:-<none>}"
	return 1
}

# Atomic swap to <bundle_dir>: symlink → install binary → install unit if changed → restart.
# Used by both the deploy main path, the deploy auto-revert path, and the rollback path.
swap_to() {
	local target_dir="$1"

	# ln -sfn replaces the symlink atomically on the same FS.
	ln -sfn "$target_dir" "$CURRENT_LINK"

	# Install binary via .new + mv to keep the swap atomic on POSIX.
	install -m 0755 "$target_dir/bin/statnive-live" /usr/local/bin/statnive-live.new
	mv -f /usr/local/bin/statnive-live.new /usr/local/bin/statnive-live

	# Reload systemd only when the unit actually changed; cheap on no-op
	# but avoids a needless reload on every deploy.
	local new_unit="$target_dir/deploy/systemd/statnive-live.service"
	local cur_unit=/etc/systemd/system/statnive-live.service
	if [ -f "$new_unit" ] && ! cmp -s "$new_unit" "$cur_unit" 2>/dev/null; then
		install -m 0644 "$new_unit" "$cur_unit"
		systemctl daemon-reload
	fi

	systemctl restart statnive-live
}

do_deploy() {
	local version="$1"
	valid_version "$version" || fail "bad version: $version"

	local base; base="$(bundle_basename "$version")"
	local tarball="$INCOMING_DIR/${base}.tar.gz"
	local sums="$INCOMING_DIR/SHA256SUMS"
	local sig="$INCOMING_DIR/SHA256SUMS.sig"
	local target_dir="$BUNDLES_DIR/${base}"
	local prev_version; prev_version="$(current_version)"

	[ -f "$tarball" ] || fail "missing $tarball — SCP the bundle first"
	[ -f "$sums" ]    || fail "missing $sums"
	[ -f "$PUBKEY" ]  || fail "missing $PUBKEY — provision the release pubkey first"

	log "verifying $tarball with $PUBKEY"
	# verify-bundle.sh ships INSIDE the bundle, so use the one from the
	# already-installed `current` (avoids chicken-and-egg on first deploy).
	local verifier="$CURRENT_LINK/deploy/airgap-verify-bundle.sh"
	if [ ! -x "$verifier" ]; then
		# First-deploy fallback: extract verify-bundle.sh from the tarball
		# without unpacking the whole thing.
		local tmp; tmp="$(mktemp -d)"
		tar -xzf "$tarball" -C "$tmp" "${base}/deploy/airgap-verify-bundle.sh"
		verifier="$tmp/${base}/deploy/airgap-verify-bundle.sh"
		chmod +x "$verifier"
	fi
	"$verifier" "$tarball" "$PUBKEY" || fail "bundle verification failed"

	if [ -d "$target_dir" ]; then
		log "$version already extracted at $target_dir — skipping unpack"
	else
		log "extracting to $target_dir"
		mkdir -p "$BUNDLES_DIR"
		tar -xzf "$tarball" -C "$BUNDLES_DIR"
		[ -d "$target_dir" ] || fail "extracted tree not at $target_dir — bundle name mismatch?"
	fi

	mkdir -p "$(dirname "$CURRENT_LINK")"
	swap_to "$target_dir"
	log "current → $version"

	if ! healthz_wait; then
		log "deploy failed — auto-reverting to $prev_version"
		if [ "$prev_version" != "none" ] && [ -d "$BUNDLES_DIR/$prev_version" ]; then
			swap_to "$BUNDLES_DIR/$prev_version"
			healthz_wait || log "WARNING: revert healthz also failed — manual intervention required"
		else
			log "no previous version to revert to (first deploy)"
		fi
		exit 1
	fi

	# Clean the tarball + checksums from incoming/ on success; bundle dir stays for rollback.
	rm -f "$tarball" "$sums" "$sig"
	log "deploy $version complete"
}

do_rollback() {
	local version="$1"
	valid_version "$version" || fail "bad version: $version"
	local base; base="$(bundle_basename "$version")"
	local target_dir="$BUNDLES_DIR/${base}"

	[ -d "$target_dir" ] || fail "$version not installed at $target_dir; run \`versions\` to list"

	log "rolling back to $version"
	swap_to "$target_dir"
	healthz_wait || fail "rollback healthz failed"
	log "rollback to $version complete"
}

do_versions() {
	local current; current="$(current_version)"
	if [ ! -d "$BUNDLES_DIR" ]; then
		echo "no bundles installed"
		return 0
	fi
	for d in "$BUNDLES_DIR"/statnive-live-*-linux-amd64-airgap; do
		[ -d "$d" ] || continue
		local name; name="$(basename "$d")"
		if [ "$name" = "$current" ]; then
			printf '* %s (current)\n' "$name"
		else
			printf '  %s\n' "$name"
		fi
	done
}

do_health() {
	curl -fsS $CURL_OPTS "$HEALTHZ_URL" || fail "healthz unreachable at $HEALTHZ_URL"
	echo
}

main() {
	local cmd="${1:-}"
	case "$cmd" in
		deploy)   require_root; do_deploy   "${2:-}" ;;
		rollback) require_root; do_rollback "${2:-}" ;;
		versions) do_versions ;;
		health)   do_health ;;
		-h|--help|"")
			sed -n '1,30p' "$0" | sed 's/^# \{0,1\}//'
			;;
		*)
			fail "unknown subcommand: $cmd (try: deploy|rollback|versions|health)"
			;;
	esac
}

main "$@"
