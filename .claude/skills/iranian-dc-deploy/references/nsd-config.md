# NSD configuration — AT-VPS-B1 Tehran secondary

NLnetLabs NSD (BSD-3) as the authoritative-secondary nameserver inside NIN. Source: doc 28 §Gap 2 lines 480–504.

## `/etc/nsd/nsd.conf`

```yaml
server:
    ip-address: 185.88.153.10           # AT-VPS-B1 public v4
    ip-address: 2a02:ec0:300::10        # AT-VPS-B1 public v6
    hide-version: yes
    hide-identity: yes
    username: nsd
    chroot: "/etc/nsd"
    zonesdir: "/etc/nsd/zones"
    logfile: "/var/log/nsd.log"
    pidfile: "/run/nsd/nsd.pid"
    verbosity: 1

key:
    name: "statnive-axfr."
    algorithm: hmac-sha256
    secret: "REPLACE_WITH_BASE64_32B_SECRET"     # rotate quarterly, store in 1Password

zone:
    name: "statnive.live"
    zonefile: "zones/statnive.live.signed"
    allow-notify: 88.99.1.2 statnive-axfr.
    request-xfr: AXFR 88.99.1.2 statnive-axfr.
    allow-axfr-fallback: yes

zone:
    name: "statnive.ir"
    zonefile: "zones/statnive.ir.signed"
    allow-notify: 88.99.1.2 statnive-axfr.
    request-xfr: AXFR 88.99.1.2 statnive-axfr.
```

**`88.99.1.2`** is the Hetzner hidden-primary public v4 — replace with the actual address.

## systemd unit — `/etc/systemd/system/nsd.service`

```ini
[Unit]
Description=NSD authoritative DNS server (statnive-live Tehran secondary)
After=network-online.target
Wants=network-online.target

[Service]
Type=forking
PIDFile=/run/nsd/nsd.pid
ExecStartPre=/usr/sbin/nsd-checkconf /etc/nsd/nsd.conf
ExecStart=/usr/sbin/nsd -c /etc/nsd/nsd.conf
ExecReload=/usr/sbin/nsd-control reload
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/log/nsd /var/lib/nsd /run/nsd
PrivateTmp=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_SETUID CAP_SETGID
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
```

## Companion timer — `/etc/systemd/system/nsd-xfr-watch.timer`

Pulls AXFR on boot + hourly, ensuring post-blackout convergence even when the hidden-primary can't push NOTIFY through NIN.

```ini
[Unit]
Description=NSD periodic AXFR pull (blackout-safe zone sync)

[Timer]
OnBootSec=30s
OnUnitActiveSec=1h
Persistent=true

[Install]
WantedBy=timers.target
```

Paired with `/etc/systemd/system/nsd-xfr-watch.service`:

```ini
[Unit]
Description=NSD AXFR pull
After=nsd.service

[Service]
Type=oneshot
ExecStart=/usr/sbin/nsd-control transfer statnive.live
ExecStart=/usr/sbin/nsd-control transfer statnive.ir
```

## Firewall — `ufw` rules on AT-VPS-B1

```bash
ufw default deny incoming
ufw default allow outgoing
ufw allow from 88.99.1.2 to any port 53 proto tcp  # AXFR from hidden-primary only
ufw allow 53/udp                                    # public DNS queries
ufw allow 53/tcp                                    # DNSSEC / large responses
ufw allow from <ops-jump> to any port 22 proto tcp  # SSH from ops jumphost only
ufw enable
```

## Verification

```bash
# From AT-VPS-B1
sudo nsd-checkconf /etc/nsd/nsd.conf               # syntax check
sudo systemctl status nsd                          # running, no errors
sudo nsd-control status                            # zones loaded
sudo journalctl -u nsd --since "1h ago"            # no AXFR failures

# From any resolver
dig @185.88.153.10 statnive.live SOA +norec        # serial matches hidden-primary
dig @185.88.153.10 ns-tehran.statnive.live A       # glue record resolves
```

## License note

NSD itself is **BSD-3** (NLnetLabs). Distributed separately from the Go binary; the license boundary stays clean.

## Research anchor

Doc 28 §Gap 2 lines 480–510 (NSD config) + lines 337–355 (provider + DNS strategy).