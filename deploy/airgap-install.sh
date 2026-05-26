#!/usr/bin/env bash
# airgap-install.sh — idempotent installer for the statnive-live air-gap
# bundle. Run as root (sudo) from the unpacked bundle directory after
# `airgap-verify-bundle.sh` succeeds.
#
# Flags:
#   --posture=NAME        deployment posture: saas | outside-iran | inside-iran
#                         (default: empty — legacy/dev; sets STATNIVE_POSTURE
#                         in the systemd drop-in and implies posture-specific
#                         defaults for --ntp-profile and --apply-iptables:
#                           saas         → no implied flag changes
#                           outside-iran → no implied flag changes
#                           inside-iran  → implies --ntp-profile=asiatech
#                                          + --apply-iptables unless either
#                                          is already set explicitly)
#   --skip-ch-check       don't probe for ClickHouse (cold install)
#   --apply-iptables      apply deploy/iptables/rules.v{4,6} (off by default;
#                         most operators have their own firewalls)
#   --ntp-profile=NAME    install Iranian-DC chrony.conf when NAME=asiatech
#                         (default: leave host chrony.conf untouched —
#                         Netcup / Hetzner / international keeps its distro
#                         defaults; only Iranian-DC ops opt in)
#   --uninstall           remove binary + systemd unit; keeps data + config
#   -h | --help           usage
#
# Never-deletes on uninstall: /var/lib/statnive-live (rollup data +
# WAL), /etc/statnive-live (config, master.key, TLS PEMs). Operators
# `rm -rf` those manually when they mean it.

set -euo pipefail

POSTURE=""
SKIP_CH_CHECK=0
APPLY_IPTABLES=0
NTP_PROFILE=""
UNINSTALL=0
EXPLICIT_APPLY_IPTABLES=0
EXPLICIT_NTP_PROFILE=0

for arg in "$@"; do
	case "$arg" in
		--posture=*)            POSTURE="${arg#--posture=}" ;;
		--skip-ch-check)        SKIP_CH_CHECK=1 ;;
		--apply-iptables)       APPLY_IPTABLES=1; EXPLICIT_APPLY_IPTABLES=1 ;;
		--ntp-profile=*)        NTP_PROFILE="${arg#--ntp-profile=}"; EXPLICIT_NTP_PROFILE=1 ;;
		--uninstall)            UNINSTALL=1 ;;
		-h|--help)
			sed -n '1,28p' "$0" | sed 's/^# \{0,1\}//'
			exit 0
			;;
		*)
			echo "airgap-install: unknown arg: $arg" >&2
			exit 2
			;;
	esac
done

case "$POSTURE" in
	""|saas|outside-iran|inside-iran) ;;
	*)
		echo "airgap-install: --posture=$POSTURE not recognized (allowed: saas, outside-iran, inside-iran, or empty)" >&2
		exit 2
		;;
esac

# inside-iran implies asiatech NTP + OUTPUT DROP iptables unless the
# operator explicitly overrode either flag.
if [ "$POSTURE" = "inside-iran" ]; then
	if [ "$EXPLICIT_NTP_PROFILE" != "1" ]; then
		NTP_PROFILE="asiatech"
	fi
	if [ "$EXPLICIT_APPLY_IPTABLES" != "1" ]; then
		APPLY_IPTABLES=1
	fi
fi

case "$NTP_PROFILE" in
	""|netcup|asiatech) ;;
	*)
		echo "airgap-install: --ntp-profile=$NTP_PROFILE not recognized (allowed: asiatech, netcup, or empty)" >&2
		exit 2
		;;
esac

if [ "$(id -u)" -ne 0 ]; then
	echo "airgap-install: must run as root (sudo)" >&2
	exit 1
fi

BUNDLE_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Distro detection — Debian and Ubuntu share most of the install path
# but diverge on iptables packaging (Debian minimal images miss the
# package; Ubuntu Server includes it) and netplan vs systemd-networkd.
DISTRO_ID="unknown"
if [ -r /etc/os-release ]; then
	# shellcheck disable=SC1091
	. /etc/os-release
	DISTRO_ID="${ID:-unknown}"
fi

# --- uninstall path ----------------------------------------------------------
if [ "$UNINSTALL" = "1" ]; then
	echo "airgap-install: uninstalling (keeps /var/lib/statnive-live + /etc/statnive-live)"
	systemctl disable --now statnive-live >/dev/null 2>&1 || true
	rm -f /etc/systemd/system/statnive-live.service
	rm -f /etc/systemd/system/statnive-live.service.d/posture.conf
	rmdir /etc/systemd/system/statnive-live.service.d 2>/dev/null || true
	systemctl daemon-reload >/dev/null 2>&1 || true
	rm -f /usr/local/bin/statnive-live
	echo "airgap-install: uninstalled. Data + config retained; \`rm -rf /var/lib/statnive-live /etc/statnive-live\` to purge."
	exit 0
fi

# --- preflight ---------------------------------------------------------------
echo "airgap-install: preflight"

KERNEL="$(uname -r)"
MAJOR="${KERNEL%%.*}"
if [ "$MAJOR" -lt 5 ]; then
	echo "airgap-install: kernel $KERNEL < 5.x unsupported (openat2 / os.Root needs 5.x+)" >&2
	exit 1
fi

if ! command -v systemctl >/dev/null 2>&1; then
	echo "airgap-install: systemd required (systemctl not found)" >&2
	exit 1
fi

if [ "$SKIP_CH_CHECK" != "1" ]; then
	# Any of: clickhouse-client on PATH, or a 127.0.0.1:9000 listener.
	if command -v clickhouse-client >/dev/null 2>&1; then
		if ! clickhouse-client --query 'SELECT 1' >/dev/null 2>&1; then
			echo "airgap-install: clickhouse-client present but SELECT 1 failed — start clickhouse-server or pass --skip-ch-check" >&2
			exit 1
		fi
	elif command -v ss >/dev/null 2>&1 && ss -tln | grep -q '127.0.0.1:9000'; then
		: # listener present, proceed
	else
		echo "airgap-install: no ClickHouse detected on 127.0.0.1:9000 — install CH first or pass --skip-ch-check" >&2
		exit 1
	fi
fi

# --- user + directory layout -------------------------------------------------
echo "airgap-install: provisioning statnive user + directories"
if ! id statnive >/dev/null 2>&1; then
	useradd --system --no-create-home --shell /usr/sbin/nologin statnive
fi

install -d -m 0750 -o statnive -g statnive /var/lib/statnive-live
install -d -m 0755 -o statnive -g statnive /var/log/statnive-live
install -d -m 0755 -o root     -g root     /etc/statnive-live
# /etc/statnive-live/{geoip,tls} are 0750 root:statnive: parent traversal
# requires execute on /etc/statnive-live (0755 above) AND on the subdir
# itself; the service user reaches files via the statnive group, not the
# owning user. Mode 0700 here makes the subdir unreachable to systemd's
# User=statnive process even when individual files are mode 0644.
install -d -m 0750 -o root -g statnive /etc/statnive-live/geoip
install -d -m 0750 -o root -g statnive /etc/statnive-live/tls

# --- binary ------------------------------------------------------------------
echo "airgap-install: installing binary"
install -m 0755 "$BUNDLE_ROOT/bin/statnive-live" /usr/local/bin/statnive-live

# --- config (copy-once; never overwrite existing operator edits) -------------
CONFIG_DST="/etc/statnive-live/config.yaml"
if [ ! -f "$CONFIG_DST" ]; then
	install -m 0640 -o root -g statnive \
		"$BUNDLE_ROOT/config/statnive-live.yaml.example" "$CONFIG_DST"
	echo "airgap-install: seeded $CONFIG_DST (edit before starting)"
else
	echo "airgap-install: $CONFIG_DST exists — leaving operator edits intact"
fi

SOURCES_DST="/etc/statnive-live/sources.yaml"
if [ -f "$BUNDLE_ROOT/config/sources.yaml" ] && [ ! -f "$SOURCES_DST" ]; then
	install -m 0640 -o root -g statnive \
		"$BUNDLE_ROOT/config/sources.yaml" "$SOURCES_DST"
fi

# --- NTP profile (opt-in) ----------------------------------------------------
# Only --ntp-profile=asiatech installs a chrony.conf. The default (no
# flag) and --ntp-profile=netcup are explicit no-ops so existing
# deploys keep their distro chrony config byte-for-byte. The binary's
# IRST-keyed salt rotation (CLAUDE.md Privacy Rule 2) needs Stratum <=4
# within 60 s of boot; this step gets the Iranian-DC operator there.
#
# Ordering invariant: this block runs BEFORE --apply-iptables. The apt
# install path below needs egress to the package mirror; the Iranian
# OUTPUT-DROP policy (CLAUDE.md Isolation table) lands after this
# script and the operator has staged the binary + GeoIP. For a true
# zero-egress install, pre-stage `chrony_*.deb` and switch the apt
# call to `dpkg -i` — Phase 10 polish.
if [ "$NTP_PROFILE" = "asiatech" ]; then
	NTP_SRC="$BUNDLE_ROOT/deploy/chrony.conf.asiatech"
	if [ ! -f "$NTP_SRC" ]; then
		echo "airgap-install: --ntp-profile=asiatech but $NTP_SRC missing from bundle" >&2
		exit 1
	fi

	if ! command -v chronyc >/dev/null 2>&1; then
		case "$DISTRO_ID" in
			debian|ubuntu)
				echo "airgap-install: chrony not present; installing via apt (must precede --apply-iptables / OUTPUT DROP)"
				DEBIAN_FRONTEND=noninteractive apt-get install -y chrony
				;;
			*)
				echo "airgap-install: chrony missing on $DISTRO_ID — install it manually before --ntp-profile=asiatech" >&2
				exit 1
				;;
		esac
	fi

	echo "airgap-install: installing Iranian-DC chrony.conf (sources: time.asiatech.ir, ntp.nic.ir, ntp.aut.ac.ir, 0.ir.pool.ntp.org)"
	# Back up the distro default once so an operator can roll back without
	# the bundle. The pre-existence guard makes re-runs idempotent.
	if [ -f /etc/chrony/chrony.conf ] && [ ! -f /etc/chrony/chrony.conf.airgap-install.bak ]; then
		cp -p /etc/chrony/chrony.conf /etc/chrony/chrony.conf.airgap-install.bak
	fi
	install -m 0644 -o root -g root "$NTP_SRC" /etc/chrony/chrony.conf
	systemctl restart chrony >/dev/null 2>&1 || \
		echo "airgap-install: chrony restart failed (systemd in container?); operator must restart on the live host"

	echo "airgap-install: NTP profile applied. Verify with: chronyc tracking"
fi

# --- systemd unit ------------------------------------------------------------
echo "airgap-install: installing systemd unit"
install -m 0644 "$BUNDLE_ROOT/deploy/systemd/statnive-live.service" \
	/etc/systemd/system/statnive-live.service

# --- posture drop-in (sets STATNIVE_POSTURE in the systemd environment) ------
# Written regardless of whether posture is empty so that re-installs with a
# different posture always overwrite stale drop-ins rather than leaving the
# old value active. An empty posture writes an empty env var — the binary
# treats that identically to "unset" (legacy/dev path).
install -d -m 0755 /etc/systemd/system/statnive-live.service.d
install -m 0644 /dev/stdin \
	/etc/systemd/system/statnive-live.service.d/posture.conf <<EOF
[Service]
Environment="STATNIVE_POSTURE=$POSTURE"
EOF
if [ -n "$POSTURE" ]; then
	echo "airgap-install: posture=$POSTURE set in systemd drop-in"
fi

if systemctl daemon-reload 2>/dev/null; then
	systemctl enable statnive-live >/dev/null
	echo "airgap-install: enabled (not started yet)"
else
	# Happens inside containers + chroots where PID 1 isn't systemd.
	# The unit file is installed; operator enables it when this install
	# is done in a system with systemd booted.
	echo "airgap-install: systemd not running (container/chroot?); unit file installed at /etc/systemd/system/statnive-live.service"
	echo "airgap-install: on the live host, run: sudo systemctl daemon-reload && sudo systemctl enable statnive-live"
fi

# --- iptables (opt-in) -------------------------------------------------------
if [ "$APPLY_IPTABLES" = "1" ]; then
	# Debian minimal images don't ship iptables; Ubuntu Server does. Auto-
	# install on Debian rather than failing with cryptic command-not-found.
	if ! command -v iptables-restore >/dev/null 2>&1; then
		case "$DISTRO_ID" in
			debian|ubuntu)
				echo "airgap-install: iptables not present; installing via apt"
				DEBIAN_FRONTEND=noninteractive apt-get install -y iptables
				;;
			*)
				echo "airgap-install: iptables-restore missing on $DISTRO_ID — install it manually before --apply-iptables" >&2
				exit 1
				;;
		esac
	fi
	echo "airgap-install: applying iptables rules.v4 + rules.v6"
	iptables-restore < "$BUNDLE_ROOT/deploy/iptables/rules.v4"
	ip6tables-restore < "$BUNDLE_ROOT/deploy/iptables/rules.v6"
else
	cat <<-EOF
	airgap-install: iptables NOT applied (use --apply-iptables to opt in).
	  To apply manually:
	    sudo iptables-restore  < $BUNDLE_ROOT/deploy/iptables/rules.v4
	    sudo ip6tables-restore < $BUNDLE_ROOT/deploy/iptables/rules.v6
	EOF
fi

# --- hints -------------------------------------------------------------------
POSTURE_HINT=""
if [ -n "$POSTURE" ]; then
	POSTURE_HINT="  Posture:  $POSTURE (STATNIVE_POSTURE set in systemd drop-in)"
fi

cat <<-EOF

airgap-install: done. Next steps (from docs/quickstart.md § Minute 3):
${POSTURE_HINT}

  1. Edit $CONFIG_DST — at minimum, set TLS cert paths or clear them
     for HTTP-behind-proxy.

  2. Drop the master secret (choose ONE):
       openssl rand -hex 32 > /etc/statnive-live/master.key
       chmod 0600 /etc/statnive-live/master.key
       chown statnive:statnive /etc/statnive-live/master.key
     OR set STATNIVE_MASTER_SECRET via the systemd drop-in unit.

  3. First-run bootstrap — set once, clear after first boot:
       STATNIVE_BOOTSTRAP_ADMIN_EMAIL=ops@example.com
       STATNIVE_BOOTSTRAP_ADMIN_PASSWORD=<32+ chars>
       STATNIVE_BOOTSTRAP_ADMIN_USERNAME=ops

     A drop-in file at /etc/systemd/system/statnive-live.service.d/env.conf
     can hold these:
       [Service]
       Environment="STATNIVE_BOOTSTRAP_ADMIN_EMAIL=ops@example.com"
       Environment="STATNIVE_BOOTSTRAP_ADMIN_PASSWORD=<32+ chars>"
       Environment="STATNIVE_BOOTSTRAP_ADMIN_USERNAME=ops"
     After first boot, comment out the bootstrap envs; they're one-shot.

  4. Download GeoIP BIN (optional, but country_code stays "--" without it):
       scp IP2LOCATION-LITE-DB23.BIN root@host:/tmp/
       sudo $BUNDLE_ROOT/deploy/airgap-update-geoip.sh /tmp/IP2LOCATION-LITE-DB23.BIN

  5. Start the service:
       sudo systemctl start statnive-live
       sudo systemctl status statnive-live
       curl http://127.0.0.1:8080/healthz

Run docs/runbook.md § Air-gap bundle install for the full checklist
including TLS rotation, backup cron, LUKS, and verification.
EOF
