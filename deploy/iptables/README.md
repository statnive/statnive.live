# deploy/iptables — host firewall templates

Default-deny templates for the statnive-live host. Allow only SSH (22),
HTTP (80), HTTPS (443), and rate-limited ICMP echo. Everything else
drops. Internal binds stay on `127.0.0.1` (`ListenAddress: 127.0.0.1:8080`
for the app; ClickHouse on `127.0.0.1:9000`) and are reachable only via
the loopback accept rule — no port-forwarding required.

## Apply (Debian / Ubuntu)

```bash
sudo apt-get install -y iptables-persistent
sudo iptables-restore  < deploy/iptables/rules.v4
sudo ip6tables-restore < deploy/iptables/rules.v6
sudo netfilter-persistent save
```

Persist location on Debian: `/etc/iptables/rules.v4` and
`/etc/iptables/rules.v6` (loaded at boot by `netfilter-persistent`).

## Apply (RHEL / CentOS 8+)

RHEL-family ships `firewalld` by default. Prefer the iptables direct
rules (no firewalld) on statnive-live hosts — `firewalld` reorders
rules on reload, which can re-allow dropped traffic during a zone
transition. Disable it and install `iptables-services` instead:

```bash
sudo systemctl disable --now firewalld
sudo dnf install -y iptables-services
sudo iptables-restore  < deploy/iptables/rules.v4
sudo ip6tables-restore < deploy/iptables/rules.v6
sudo service iptables save
sudo systemctl enable --now iptables ip6tables
```

## nftables conversion

If the host is nftables-only (no `iptables-legacy`), translate the
files and load the nft ruleset:

```bash
iptables-restore-translate  -f deploy/iptables/rules.v4  > /etc/nftables.conf.v4
ip6tables-restore-translate -f deploy/iptables/rules.v6 >> /etc/nftables.conf.v4
sudo cp /etc/nftables.conf.v4 /etc/nftables.conf
sudo systemctl enable --now nftables
```

## Verification

```bash
sudo iptables -L -n -v --line-numbers
sudo ip6tables -L -n -v --line-numbers

# From an outside host:
curl -sSf https://HOST/healthz        # expect 200 (TLS terminator on 443)
nc -vz HOST 8080                      # expect refused (localhost-only bind)
nc -vz HOST 9000                      # expect refused (ClickHouse localhost-only)
```

The air-gap test in [`docs/runbook.md`](../../docs/runbook.md) §
Air-Gap Verification uses a stricter OUTPUT DROP rule to prove the
binary has no required outbound — the templates here are for the
normal production posture where outbound is allowed but the app-layer
allow-list (`internal/httpclient/guarded.go`, CLAUDE.md Security #14)
gates every opt-in destination.
