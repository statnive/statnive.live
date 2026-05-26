#!/usr/bin/env bash
# install.sh — provision NSD as authoritative primary for statnive.ir
# on AT-VPS-B1 (Asiatech, Tehran).
#
# A6 production-readiness — Architecture C single-primary DNS. No
# Cloudflare, no AXFR-out, no DNSSEC inline-signing in v1 (operator
# pre-signs offline if DNSSEC ships in Phase 10 polish).
#
# Run as root on AT-VPS-B1. Operator-side; this script does NOT run
# inside the statnive-live binary or as part of the courier path.
#
# Usage:
#   sudo PRIMARY_NS_IPV4=1.2.3.4 \
#        PRIMARY_NS_IPV6=2a01:7e00::1 \
#        ADMIN_EMAIL=ops@statnive.ir \
#        CUSTOMER_SLUG=sampleplatform \
#        CUSTOMER_VPS_IPV4=1.2.3.5 \
#        CUSTOMER_VPS_IPV6=2a01:7e00::2 \
#        ZONE_SERIAL=2026052601 \
#        deploy/dns/at-vps-b1/install.sh
#
# Idempotent. Re-running with new env vars (e.g. bumped ZONE_SERIAL,
# new customer) re-renders the zone file + reloads NSD.

set -euo pipefail

# --- inputs ------------------------------------------------------------------
: "${PRIMARY_NS_IPV4:?PRIMARY_NS_IPV4 is required (AT-VPS-B1 IPv4)}"
: "${ADMIN_EMAIL:?ADMIN_EMAIL is required (e.g. ops@statnive.ir)}"
: "${CUSTOMER_SLUG:?CUSTOMER_SLUG is required (e.g. sampleplatform)}"
: "${CUSTOMER_VPS_IPV4:?CUSTOMER_VPS_IPV4 is required (Asiatech G2 IPv4)}"

# IPv6 addresses are optional — Asiatech G2 dual-stack is standard but
# not contractual. If unset/empty, the corresponding AAAA record is
# dropped from the rendered zone (rather than substituted to empty and
# producing malformed RDATA).
PRIMARY_NS_IPV6="${PRIMARY_NS_IPV6:-}"
CUSTOMER_VPS_IPV6="${CUSTOMER_VPS_IPV6:-}"

# ZONE_SERIAL is YYYYMMDDNN by convention. If unset, default to today + 01.
ZONE_SERIAL="${ZONE_SERIAL:-$(date -u +%Y%m%d)01}"

if [ "$(id -u)" -ne 0 ]; then
	echo "install.sh: must run as root" >&2
	exit 1
fi

# RFC 1035 SOA RNAME field encodes 'ops@statnive.ir' as 'ops.statnive.ir.'.
# Substitute the first @ for a dot. Operators rarely have @ in the local
# part; cheap one-pass.
ADMIN_EMAIL_DOTTED="$(printf '%s.' "$ADMIN_EMAIL" | sed 's/@/./')"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ZONE_TPL="$SCRIPT_DIR/statnive.ir.zone.template"
CONF_TPL="$SCRIPT_DIR/nsd.conf.template"

for f in "$ZONE_TPL" "$CONF_TPL"; do
	if [ ! -f "$f" ]; then
		echo "install.sh: template missing: $f" >&2
		exit 1
	fi
done

# --- preflight: NSD installed? ------------------------------------------------
if ! command -v nsd-checkconf >/dev/null 2>&1; then
	# Debian + Ubuntu both ship `nsd` as the package name.
	if [ -r /etc/os-release ]; then
		# shellcheck disable=SC1091
		. /etc/os-release
	fi
	case "${ID:-unknown}" in
		debian|ubuntu)
			echo "install.sh: NSD not present; installing via apt"
			DEBIAN_FRONTEND=noninteractive apt-get install -y nsd
			;;
		*)
			echo "install.sh: nsd-checkconf missing on ${ID:-unknown} — install NSD4 manually then re-run" >&2
			exit 1
			;;
	esac
fi

# --- substitute zone file ----------------------------------------------------
ZONE_DST=/etc/nsd/zones/statnive.ir.zone

install -d -m 0750 -o nsd -g nsd /etc/nsd/zones

ZONE_TMP="$(mktemp)"
trap 'rm -f "$ZONE_TMP"' EXIT

# Drop AAAA placeholder lines when the corresponding IPv6 input is
# empty — leaving the placeholder + empty substitution would render a
# malformed AAAA record. Done BEFORE substitution so the s|...| pass
# never sees the doomed line.
cp "$ZONE_TPL" "$ZONE_TMP"

if [ -z "$PRIMARY_NS_IPV6" ]; then
	grep -v '{{PRIMARY_NS_IPV6}}' "$ZONE_TMP" > "$ZONE_TMP.new" && mv "$ZONE_TMP.new" "$ZONE_TMP"
fi

if [ -z "$CUSTOMER_VPS_IPV6" ]; then
	grep -v '{{CUSTOMER_VPS_IPV6}}' "$ZONE_TMP" > "$ZONE_TMP.new" && mv "$ZONE_TMP.new" "$ZONE_TMP"
fi

# Sed substitution; '|' delimiter chosen because no accepted input
# (IPv4, IPv6, RFC-1035 label, RFC-5321 local-part email) can contain
# '|', '\', or '&' — the three sed-replacement metacharacters. If a
# future placeholder accepts free-form text, switch to envsubst or
# escape inputs.
sed -i.bak \
	-e "s|{{ZONE_SERIAL}}|$ZONE_SERIAL|g" \
	-e "s|{{PRIMARY_NS_IPV4}}|$PRIMARY_NS_IPV4|g" \
	-e "s|{{PRIMARY_NS_IPV6}}|$PRIMARY_NS_IPV6|g" \
	-e "s|{{ADMIN_EMAIL_DOTTED}}|$ADMIN_EMAIL_DOTTED|g" \
	-e "s|{{CUSTOMER_SLUG}}|$CUSTOMER_SLUG|g" \
	-e "s|{{CUSTOMER_VPS_IPV4}}|$CUSTOMER_VPS_IPV4|g" \
	-e "s|{{CUSTOMER_VPS_IPV6}}|$CUSTOMER_VPS_IPV6|g" \
	"$ZONE_TMP"
rm -f "$ZONE_TMP.bak"

# Reject any unsubstituted placeholders before clobbering the live zone.
if grep -q '{{' "$ZONE_TMP"; then
	echo "install.sh: unsubstituted placeholders remain — check the env-var matrix" >&2
	grep '{{' "$ZONE_TMP" | head -5 >&2
	exit 1
fi

install -m 0644 -o nsd -g nsd "$ZONE_TMP" "$ZONE_DST"

# --- install nsd.conf if absent ----------------------------------------------
# Don't overwrite an existing operator-tuned nsd.conf; print a hint instead.
CONF_DST=/etc/nsd/nsd.conf

if [ ! -f "$CONF_DST" ]; then
	install -m 0644 -o root -g nsd "$CONF_TPL" "$CONF_DST"
	echo "install.sh: seeded $CONF_DST"
else
	echo "install.sh: $CONF_DST exists — leaving operator config intact"
	echo "  diff against $CONF_TPL if you want to apply upstream changes."
fi

# --- syntax check ------------------------------------------------------------
echo "install.sh: nsd-checkconf"
nsd-checkconf "$CONF_DST"

echo "install.sh: nsd-checkzone statnive.ir"
nsd-checkzone statnive.ir "$ZONE_DST"

# --- enable + reload ---------------------------------------------------------
systemctl enable nsd >/dev/null
if systemctl is-active --quiet nsd; then
	echo "install.sh: nsd running — reloading zone"
	# nsd-control needs remote-control: enabled (off by default in our
	# template). Fall back to a systemctl reload which sends SIGHUP.
	if nsd-control status >/dev/null 2>&1; then
		nsd-control reload statnive.ir
	else
		systemctl reload nsd 2>/dev/null || systemctl restart nsd
	fi
else
	echo "install.sh: starting nsd"
	systemctl start nsd
fi

# --- verify locally ----------------------------------------------------------
# +norec asserts authoritative-only behaviour — recursion-desired
# would test resolver semantics, which we don't run.
echo "install.sh: dig +norec @127.0.0.1 SOA statnive.ir"
if ! dig +short +norec @127.0.0.1 SOA statnive.ir | grep -q "ns1.statnive.ir."; then
	echo "install.sh: local SOA lookup failed — check journalctl -u nsd" >&2
	exit 1
fi

# Soft-warn if PRIMARY_NS_IPV4:53 isn't reachable from this host. The
# common case is "this host IS the NS" + host firewall blocks self-
# connect; not fatal. A typo'd IP would fail outside-Iran resolution
# without showing here either way; this catches the operator-fat-
# finger case where a TCP-blocking iptables rule is already in place.
if ! timeout 5 bash -c "echo > /dev/tcp/$PRIMARY_NS_IPV4/53" 2>/dev/null; then
	echo "install.sh: WARN — $PRIMARY_NS_IPV4:53 not reachable from this host (may be a host firewall; verify with an outside-Iran resolver before IRNIC submission)" >&2
fi

cat <<EOF

install.sh: done. Verify externally + register glue at IRNIC:

  1. From an outside-Iran resolver:
       dig @$PRIMARY_NS_IPV4 SOA statnive.ir
       dig @$PRIMARY_NS_IPV4 A $CUSTOMER_SLUG.statnive.ir

  2. At IRNIC (https://www.nic.ir/), register the .ir delegation:
       NS:      ns1.statnive.ir.
       Glue:    ns1.statnive.ir. -> $PRIMARY_NS_IPV4, $PRIMARY_NS_IPV6
       Email:   $ADMIN_EMAIL

  3. CAA + DS records (DNSSEC) — Phase 10 polish; not required for v1.
EOF
