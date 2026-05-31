# cert-forge — outside-Iran TLS issuance bastion for `.ir` domains

`statnive-live` running inside Iran cannot reach Let's Encrypt directly (CLAUDE.md anti-pattern `iran-no-letsencrypt-in-binary`, OFAC 31 CFR 560.540 outbound restrictions). This directory is everything an operator drops onto a small **outside-Iran** VPS (Hetzner CX11, ~€4/mo) to issue + rotate TLS PEMs for the customer's `<host>.statnive.ir` domain and rsync them to the Asiatech production box.

## What runs where

```
┌─────────────────────────────┐         ┌─────────────────────────────────┐
│  Hetzner CX11 (cert-forge)  │  rsync  │  Asiatech AT-VPS-X (production) │
│  - acme.sh in Docker        │ ──────► │  - statnive-live binary         │
│  - DNS-01 via Pars.ir API   │   ssh   │  - /etc/statnive-live/tls/*.pem │
│  - daily renewal timer      │         │  - SIGHUP triggers PEM reload   │
│  outbound-allowed (LE, DNS) │         │  air-gapped, OUTPUT DROP        │
└─────────────────────────────┘         └─────────────────────────────────┘
```

Workflow:
1. `acme.sh` runs daily; if a cert is within 30 days of expiry, it requests a renewal from Let's Encrypt
2. The DNS-01 challenge is satisfied by adding a TXT record to the `.ir` zone via the Pars.ir reseller API (no Cloudflare per `iran-no-cloudflare`)
3. On success, the `renew_hook` script rsync's the new fullchain.pem + privkey.pem into `/etc/statnive-live/tls/` on the Asiatech box
4. A post-rsync ssh hook sends `SIGHUP` to the statnive-live process — the binary's `internal/cert/loader.go` hot-swaps the cert without a restart

## Why acme.sh (not Caddy)

The plan originally suggested Caddy, but Caddy/lego does not ship a Pars.ir DNS provider. `acme.sh` supports custom providers via dropping a shell script into `dnsapi/dns_${NAME}.sh`, which is the simplest path for any Iranian DNS reseller (Pars.ir, ir.nic.ir, hostiran, etc.). One script per reseller; everything else is provider-agnostic.

## Files

| Path | Purpose |
| --- | --- |
| `docker-compose.yml` | Two-container stack: acme.sh + a tiny rsync-push sidecar |
| `issue.sh` | One-shot first-issue driver — operator runs once per domain |
| `renew-hook.sh` | Invoked by acme.sh after a successful renewal; calls `rsync-push.sh` |
| `rsync-push.sh` | Pushes fullchain.pem + privkey.pem to Asiatech via SSH+rsync, then SIGHUPs the binary |
| `dnsapi/dns_parsir.sh.example` | Stub Pars.ir DNS provider — operator copies to `dns_parsir.sh` and fills in API credentials |
| `systemd/cert-forge.service` + `.timer` | Host-systemd alternative to docker-compose for operators who prefer systemd |
| `.env.example` | All env vars the operator must set before first run |

## First-time setup (operator SOP, run on the Hetzner CX11)

```bash
# 1. Clone this directory onto the bastion
git clone --depth 1 https://github.com/statnive/statnive.live /opt/cert-forge-source
sudo install -d -m 0750 /etc/cert-forge
sudo cp -r /opt/cert-forge-source/deploy/cert-forge/* /etc/cert-forge/

# 2. Wire your secrets
sudo cp /etc/cert-forge/.env.example /etc/cert-forge/.env
sudo $EDITOR /etc/cert-forge/.env             # PARSIR_API_KEY, ASIATECH_HOST, etc.
sudo chmod 0600 /etc/cert-forge/.env

# 3. Wire the Pars.ir DNS provider (or your reseller of choice)
sudo cp /etc/cert-forge/dnsapi/dns_parsir.sh.example /etc/cert-forge/dnsapi/dns_parsir.sh
sudo $EDITOR /etc/cert-forge/dnsapi/dns_parsir.sh   # fill in API endpoints
sudo chmod 0700 /etc/cert-forge/dnsapi/dns_parsir.sh

# 4. Provision the SSH key the bastion uses to push into Asiatech.
#    Generate offline, add the pubkey to root@<asiatech-host>:~/.ssh/authorized_keys
#    with command="rsync --server -avze... -P /etc/statnive-live/tls/" forcing.
sudo install -m 0600 ~/secrets/asiatech-deploy.key /etc/cert-forge/ssh/id_ed25519
sudo install -m 0644 ~/secrets/asiatech-deploy.pub /etc/cert-forge/ssh/id_ed25519.pub

# 5. First issue (interactive — confirms DNS round-trip works)
sudo bash /etc/cert-forge/issue.sh customer-1.statnive.ir

# 6. Enable the renewal timer (one of two paths)
#    (a) docker-compose path:
sudo docker compose -f /etc/cert-forge/docker-compose.yml up -d
#    (b) host-systemd path:
sudo install -m 0644 /etc/cert-forge/systemd/cert-forge.service /etc/systemd/system/
sudo install -m 0644 /etc/cert-forge/systemd/cert-forge.timer   /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now cert-forge.timer
```

## Verifying the round-trip

```bash
# On the bastion: confirm the cert lives and isn't expired
openssl x509 -in /etc/cert-forge/state/customer-1.statnive.ir/fullchain.pem -noout -dates

# Force a rotation drill (does not re-issue; just re-pushes the existing PEM)
sudo bash /etc/cert-forge/rsync-push.sh customer-1.statnive.ir

# On the Asiatech box: confirm the rsync landed + SIGHUP picked it up
ssh root@<asiatech-host> 'ls -la /etc/statnive-live/tls/'
ssh root@<asiatech-host> 'journalctl -u statnive-live --since "1 minute ago" | grep "tls reloaded"'
```

## When something goes wrong

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `acme.sh` exits with `DNS check failed` | Pars.ir API returned 4xx; TXT record not propagating | `tail /var/log/cert-forge/issue.log` — check the provider script's HTTP response |
| `rsync: connection refused` | SSH key not authorized on Asiatech | re-stage the pubkey; check `~/.ssh/authorized_keys` `command=` filter doesn't reject `--server -avz` |
| `tls reloaded` line missing from journald | binary doesn't have SIGHUP handler wired | check `internal/cert/loader.go:30+` — SIGHUP must be registered before the listener boots |
| Cert renewed but binary still serves old | rsync wrote to wrong path | the binary's `STATNIVE_TLS_CERT_FILE` env must match the rsync target exactly |
| Pars.ir API key expired mid-quarter | manual key rotation | rotate via Pars.ir dashboard; update `/etc/cert-forge/.env`; `systemctl restart cert-forge.timer` |

## Security notes

- The bastion holds the Pars.ir API key + the SSH key with write access to `/etc/statnive-live/tls/` on Asiatech. Treat it as a privileged box.
- Use a dedicated SSH keypair (`asiatech-deploy.key`) — not the operator's personal key.
- Constrain the Asiatech-side authorized_keys entry with `command="rsync --server ..."` + `from="<bastion-ip>"` so a compromised bastion can only write PEMs, not execute arbitrary commands.
- The cert-forge is intentionally OUTSIDE the Iranian DC blast radius — the Asiatech box never holds the LE account key or the Pars.ir credentials, so a CH/disk compromise inside Iran can't leak them.
