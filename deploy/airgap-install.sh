#!/usr/bin/env bash
# airgap-install.sh — idempotent installer for the statnive-live air-gap
# bundle. Run as root (sudo) from the unpacked bundle directory after
# `airgap-verify-bundle.sh` succeeds.
#
# Flags:
#   --skip-ch-check       don't probe for ClickHouse (cold install)
#   --apply-iptables      apply deploy/iptables/rules.v{4,6} (off by default;
#                         most operators have their own firewalls)
#   --uninstall           remove binary + systemd unit; keeps data + config
#   -h | --help           usage
#
# Never-deletes on uninstall: /var/lib/statnive-live (rollup data +
# WAL), /etc/statnive-live (config, master.key, TLS PEMs). Operators
# `rm -rf` those manually when they mean it.

set -euo pipefail

SKIP_CH_CHECK=0
APPLY_IPTABLES=0
UNINSTALL=0

for arg in "$@"; do
	case "$arg" in
		--skip-ch-check)  SKIP_CH_CHECK=1 ;;
		--apply-iptables) APPLY_IPTABLES=1 ;;
		--uninstall)      UNINSTALL=1 ;;
		-h|--help)
			sed -n '1,20p' "$0" | sed 's/^# \{0,1\}//'
			exit 0
			;;
		*)
			echo "airgap-install: unknown arg: $arg" >&2
			exit 2
			;;
	esac
done

if [ "$(id -u)" -ne 0 ]; then
	echo "airgap-install: must run as root (sudo)" >&2
	exit 1
fi

BUNDLE_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# --- uninstall path ----------------------------------------------------------
if [ "$UNINSTALL" = "1" ]; then
	echo "airgap-install: uninstalling (keeps /var/lib/statnive-live + /etc/statnive-live)"
	systemctl disable --now statnive-live >/dev/null 2>&1 || true
	rm -f /etc/systemd/system/statnive-live.service
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
install -d -m 0700 -o statnive -g statnive /etc/statnive-live/geoip
install -d -m 0700 -o root     -g root     /etc/statnive-live/tls

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

# --- systemd unit ------------------------------------------------------------
echo "airgap-install: installing systemd unit"
install -m 0644 "$BUNDLE_ROOT/deploy/systemd/statnive-live.service" \
	/etc/systemd/system/statnive-live.service

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
cat <<-EOF

airgap-install: done. Next steps (from docs/quickstart.md § Minute 3):

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
