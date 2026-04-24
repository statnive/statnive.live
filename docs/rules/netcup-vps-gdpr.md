# Netcup VPS GDPR / DNS / server-side compliance

> Upstream-processor + VPS-hardening checklist for **statnive.live on Netcup VPS 2000 G12 iv NUE** (Nuremberg, Germany — procured 2026-04-24 per [research doc 36 §4.1](../../../jaan-to/docs/research/36-devops-hetzner-saas-vps-selection-2026.md)).
>
> Explicitly disjoint from:
> - `docs/dpa-draft.md` — **customer-facing** DPA (statnive.live → customer), Phase 11 deliverable.
> - [`docs/rules/privacy-detail.md`](privacy-detail.md) — legal-chain reference (Recital 26, C-413/23, Art. 28 language).
> - [`docs/rules/security-detail.md`](security-detail.md) — 14 product-level security items.
>
> This file is the **ops runbook** for the VPS-under-our-feet: what to sign with Netcup to get a valid Art. 28(3) contract, what DNS / TLS / kernel config we own above Netcup's TOM (Annex 1), and how to verify the chain. An agent reads this when provisioning or re-provisioning the SaaS VPS, or when a sub-processor changes.

---

## 1. Controller / processor chain (who is who)

```
EU visitor on customer site
         │ (data subject)
         ▼
Customer WordPress operator      = controller       (Art. 4(7))
         │  DPA: customer → statnive.live           (docs/dpa-draft.md, Phase 11)
         ▼
statnive.live SaaS               = processor        (Art. 4(8))
         │  DPA: statnive.live → Netcup             (this doc §2)
         ▼
Netcup GmbH                      = sub-processor    (Art. 28(4))
         │  Netcup Annex 2: Netcup → Anexia chain   (inherited)
         ▼
ANEXIA Deutschland GmbH (DE, infra + personnel)
ANEXIA Internetdienstleistungs GmbH (AT, infra + personnel)
DATASIX Rechenzentrumsbetriebs GmbH (DE, DC operations)
ANX Holding GmbH (AT, support services)
```

**Consequence:** every row in Netcup's Annex 2 is a sub-processor we inherit and must disclose to our own customers under Art. 28(2). Keep §3 and `docs/compliance/subprocessor-register.md` in lockstep.

---

## 2. Article 28(3) DPA with Netcup — procedural checklist

The Netcup CCP ("Order Processing") gate blocks with "if you do not conclude a Data Processing Agreement … netcup GmbH will assume that you do not intend to … process personal data." Signing it is not optional once EU visitors hit our SaaS.

### 2.1 Login + navigation

1. Log in to [CCP](https://ccp.netcup.net/) as **Parhum Khoshbakht** — customer `365334`.
2. **Master Data → Order processing → Agreement on contract data processing**.
3. Click **Create AV Contract**.

### 2.2 Annex 3 form — verbatim values to enter

| Field | Value |
|---|---|
| **Subject of processing** | `Privacy-first web analytics SaaS (statnive.live). Hashed visitor identifiers (BLAKE3-128 over HMAC-derived daily salt), GeoIP-derived country/region/city, user-agent string, HTTP referrer. No cookies. No raw IP persisted.` |
| **Duration** | `Linked to the VPS 2000 G12 iv NUE contract term. Indefinite while the service is live; processing ceases on contract termination.` |
| **Location** | `EU / EEA only — Nuremberg (NUE) data center. No third-country transfer authorized.` — tick the EU/EEA-only box; **do not** authorize processing outside the EEA (breaks the "no Chapter V transfer" claim on statnive.live/privacy). |
| **Categories of data subjects** | Tick: `Customers`, `Interested parties`, `Visitors to the website`, `Newsletter subscribers`. **Leave unchecked**: Employees of the Client, External employees, Suppliers, Data processors, other processors. |
| **Categories of personal data** | Tick: `Contact and address data`, `Login and authentication`, `Location and geographic information data` (country + region + city only, never raw IP), `Traffic data`, `Preference and behavior data`, `Customer contract data`. **Leave unchecked**: Name data (only hashed identifiers hit storage), Date of birth, Bank and payment data, Education data, Data relevant to criminal law, Motion profile data, Photo / video / audio data. |
| **Special categories (Art. 9)** | Tick: `No special categories of personal data ("sensitive data") according to Art 9 GDPR are processed.` Do **not** tick any Art. 9 item — the platform is architecturally incapable of processing race / religion / genetic / biometric / political / union / health / sex-life data. |
| **Data-protection contact** | Parhum Khoshbakht · `wellslimstat@gmail.com` · phone from master data. |

### 2.3 Sign + archive

1. Submit via the electronic signature path — the form confirms this is sufficient. No paper copy needed.
2. Save the PDF Netcup emails back to `releases/infrastructure/netcup-dpa-2026-04-24.pdf` (gitignored per §7 — encrypted archive only, `chmod 0600`).
3. Record the signing date, customer number, and PDF SHA-256 in `docs/compliance/subprocessor-register.md` (§7 seeds this file).

### 2.4 Non-negotiable in the Netcup DPA body

- The form keeps the 14-day reminder if we "hide this message" — **do not** hide. Sign on first login.
- The Netcup DPA grants them the right to add sub-processors with 14-day written notice (§ 8). We must publish our mirrored notice window to customers: 7-day re-publish cadence to stay inside the 14-day upstream window.
- § 3.10 / § 4.3 mutual-indemnity under Art. 82 GDPR stands; the customer-facing DPA at `docs/dpa-draft.md` must carry an equivalent clause downstream.

---

## 3. Sub-processor disclosure (downstream obligation)

Art. 28(2) requires we list every sub-processor to our customers. The minimum public list at `https://statnive.live/privacy` is:

| Sub-processor | Role | Country | Legal basis |
|---|---|---|---|
| Netcup GmbH | Infrastructure / VPS / DNS (optional) | DE | Art. 28(3) DPA signed per §2 |
| ANEXIA Deutschland GmbH | Infrastructure / personnel | DE | Netcup Annex 2 (inherited) |
| ANEXIA Internetdienstleistungs GmbH | Infrastructure / personnel | AT (EEA) | Netcup Annex 2 (inherited) |
| DATASIX Rechenzentrumsbetriebs GmbH | Data-center operations | DE | Netcup Annex 2 (inherited) |
| ANX Holding GmbH | Support services (billing, regulatory) | AT (EEA) | Netcup Annex 2 (inherited) |
| Let's Encrypt / ISRG | TLS certificate issuance (DV) | US | Adequacy via EU-US DPF; `certbot --dns-01` only — no personal data transfer (see §5) |

**Update cadence.** Any change to Netcup's Annex 2 → we re-publish the list within **7 days** of receiving notice (keeps us inside the 14-day upstream notice window and the 14-day downstream window we owe customers).

**Never silently add a sub-processor.** Every new row is a commit to `docs/compliance/subprocessor-register.md` + a change-log entry on `/privacy`. CI does not police this — PR review does.

---

## 4. DNS plan for `statnive.live` (GDPR-relevant details)

### 4.1 Registrar and authoritative DNS

- **Registrar:** INWX, Netcup, or Netim (EU-located). **Not Cloudflare** — Cloudflare inserts a US-adequacy-decision-dependent sub-processor we have not disclosed, and the anti-pattern "Never Cloudflare on IR-resident paths" (CLAUDE.md § Anti-patterns) is a carryover habit worth keeping outside Iran too.
- **Authoritative NS:** registrar's own EU-located NS. Record the NS operator as a row in the sub-processor register (§7).

### 4.2 Records to publish

| Type | Name | Value | Why |
|---|---|---|---|
| `A` | `statnive.live` | Netcup VPS IPv4 | Tracker origin + dashboard |
| `AAAA` | `statnive.live` | Netcup VPS IPv6 | Both required per procurement (research doc 36 §4.1) |
| `CAA` | `statnive.live` | `0 issue "letsencrypt.org"` (v1 with `certbot`) · `0 issue ";"` (v1 if using a purchased cert, no ACME) | Narrows CA issuance surface; prevents rogue-cert issuance in DNS-hijack scenario |
| `MX` | `statnive.live` | `0 .` (null MX, RFC 7505) | Domain does not receive email; blocks spoofing + a forgotten mail-receiver becoming a data-flow Netcup does not know about |
| `TXT` | `statnive.live` | `v=spf1 -all` | Belt-and-braces with null MX — any forged mail claiming `@statnive.live` fails SPF |
| `TXT` | `_dmarc.statnive.live` | `v=DMARC1; p=reject; adkim=s; aspf=s` | Hard-reject spoofed mail. Omit `rua=` unless a mailbox is actually set up to receive aggregates — an unread mailbox is its own data-flow surface |
| `TXT` | `statnive.live` | `netcup-domain-verification=…` | Only if Netcup DNS / signup asks |

### 4.3 DNSSEC

Enable at the registrar. GDPR-relevant because a DNS hijack silently redirects traffic to an undisclosed "sub-processor" (the hijacker), breaking the Art. 28 chain without us noticing.

### 4.4 What we do NOT publish

- **No wildcard A/AAAA** — every subdomain is a potential processing surface. Create records explicitly as services land.
- **No third-party analytics / monitoring / tag-manager CNAMEs** on the apex or any subdomain. The `air-gap-validator` skill enforces this at the tracker/frontend layer; the DNS zone gets the same rule.
- **No CNAME-flattening to a CDN** without an updated sub-processor disclosure.

---

## 5. TLS posture on the VPS

CLAUDE.md § Security item 1 mandates manual PEM files via `tls.cert_file` / `tls.key_file` — one code path, zero outbound from the binary. Outside Iran, we have two operator-side options:

### 5.1 v1 — Let's Encrypt via `certbot` (recommended)

```bash
# one-shot, DNS-01 against Netcup DNS API (keeps port 80/443 free of ACME solver)
certbot certonly --dns-netcup \
    --dns-netcup-credentials /etc/letsencrypt/netcup.ini \
    -d statnive.live -d www.statnive.live \
    --agree-tos --email wellslimstat@gmail.com
# renewal: systemd timer, 60-day cadence, SIGHUP statnive-live on reload
```

- Add **ISRG / Let's Encrypt** to the sub-processor register the moment this runs.
- CAA record in §4.2 **must** authorize `letsencrypt.org` before the first issuance attempt.
- Archive renewal logs: `/var/log/statnive-live/tls-renewal.log`, append-only, rotated weekly (logrotate `copytruncate` **off**), retained 1 year per Art. 30 records-of-processing.
- The Go binary does **not** call ACME itself (that's CLAUDE.md item 1's "Autocert slips to v1.1" — in-binary ACME). External `certbot` is fine for v1.

### 5.2 v1 alternative — purchased cert (if deferring ACME)

- Buy a DV cert (ZeroSSL / BuyPass / Sectigo) for 1 year, deploy PEMs manually.
- CAA record is `0 issue ";"` until an ACME CA is whitelisted.
- Renewal reminder 30 days before expiry, tracked in the sub-processor register.

### 5.3 Protocol posture (both paths)

- TLS 1.3 only. TLS 1.2 downgrade **forbidden**. TLS 1.1 / 1.0 not compiled in.
- HSTS: `Strict-Transport-Security: max-age=31536000; includeSubDomains; preload`. Submit to the HSTS preload list only after 6 months of clean operation (opt-in is sticky).
- Monthly `testssl.sh statnive.live` snapshot committed to `releases/infrastructure/testssl/YYYY-MM-DD.txt`.
- OCSP stapling: on. Must-Staple flag on certs from a CA that supports it.

---

## 6. VPS server-side hardening (layered above Netcup's Annex 1 TOM)

Netcup's Annex 1 guarantees **their** physical access, network, and logical-access controls. The tenant VPS sits **above** that layer and is our responsibility. Every item below maps to a CLAUDE.md rule — this is the deployment-checklist view, not a new set of rules.

| # | Item | Rule reference | Why required here |
|---|---|---|---|
| 6.1 | **LUKS on `/var/lib/clickhouse`** | CLAUDE.md § Security item 9 (`LUKS optional`) | **Required** here — Netcup VPS is shared-tenant virtualization. Annex 1 physical-DC protections do not cover co-tenant VM-escape. Accept the 40–50% I/O overhead on the ClickHouse data volume only (rollups on separate non-encrypted volume is an option worth benchmarking on dogfood traffic). |
| 6.2 | **systemd hardening unit** at `ops/systemd/statnive-live.service` | CLAUDE.md § Security item 12 | `NoNewPrivileges=yes`, `ProtectSystem=strict`, `PrivateTmp=yes`, `CapabilityBoundingSet=CAP_NET_BIND_SERVICE`, `ProtectHome=yes`, `ProtectKernelTunables=yes`, `ProtectKernelModules=yes`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX` |
| 6.3 | **iptables OUTPUT default DROP** | CLAUDE.md § Isolation table | Whitelist only: Netcup NTP peer, 127.0.0.1, whatever the operator opts into via `config.outbound.allowlist`. Air-gap integration test (`iptables -P OUTPUT DROP`) must still pass. |
| 6.4 | **SSH: key-only, no password, allow-list IPs** | — | Disable root login (`PermitRootLogin no`), force pubkey (`PasswordAuthentication no`), `AllowUsers deploy`, fail2ban active. Port-knock optional. IP allow-list sourced from operator's static home/office IP(s) + any fixed bastion; recorded in `ops/ssh-allow-list.txt` (gitignored, one IP/CIDR per line with a comment explaining the source). No wildcard `0.0.0.0/0`. Update on IP change; if operator IP is dynamic, use WireGuard + allow-list the bastion only. |
| 6.5 | **Audit log append-only** | CLAUDE.md § Security item 10 | journald → `/var/log/statnive-live/audit.jsonl` via `slog` file sink. Logrotate with `copytruncate` **off** (breaks append-only integrity). `chattr +a` on the active file until rotation. |
| 6.6 | **NTP via chrony** pointed at Netcup's pool | [`privacy-detail.md` § Rule 2](privacy-detail.md) | Accurate time is load-bearing for the IRST salt-rotation boundary — Rule 2's salt derivation `HMAC(master_secret, site_id \|\| YYYY-MM-DD IRST)` breaks if the system clock drifts past the midnight boundary. Netcup publishes an internal NTP peer — prefer that over `pool.ntp.org` (reduces outbound). |
| 6.7 | **ClickHouse bound to 127.0.0.1** | CLAUDE.md § Security item 2 | Never exposed publicly. No `docker-compose.dev.yml`-style bind-all-interfaces services on production. Preact SPA is served by the Go binary, not a second process. |
| 6.8 | **Backups encrypted, EU-only** | CLAUDE.md § Security item 8 | `clickhouse-backup` + `age` + `zstd`, ship to a second EU-only location (Netcup Storage Space in Vienna, or a second Nuremberg bucket). **Never** US-located S3 — that inserts a Chapter V transfer we have not disclosed. Restore test on every release (CLAUDE.md § Test Gate → `make release`). |
| 6.9 | **Shell history hygiene** | — | `deploy` user's `~/.bash_history` is `/dev/null`-symlinked. No command-line containing a salt path, `user_id`, `master_secret`, or a visitor ID should ever end up in an ASCII file on disk. |
| 6.10 | **Unattended upgrades** for the base OS only | — | `unattended-upgrades` for security patches on the OS; application upgrades go through the release gate. Reboot window documented, not silent. |
| 6.11 | **No swap** (or `vm.swappiness=1`) | — | Avoids leaking `master_secret` or salt-memory to disk swap. If swap is required by the image, enable and encrypt it with a random-key LUKS device that regenerates at boot. |

---

## 7. Sub-processor register — new file seed

Create `docs/compliance/subprocessor-register.md` with this schema (one row per sub-processor). Keep the file in git — it contains no PII.

```markdown
# Sub-processor register — statnive.live SaaS

| Name | Role | Country | Contract date | Contract path | Notice contact | Last audit |
|---|---|---|---|---|---|---|
| Netcup GmbH | Infrastructure / VPS | DE | 2026-04-24 | releases/infrastructure/netcup-dpa-2026-04-24.pdf (encrypted) | abuse@netcup.de | ISO 27001 (TÜV NORD CERT) |
| ANEXIA Deutschland GmbH | Infrastructure / personnel | DE | 2026-04-24 (inherited) | Netcup Annex 2 | — | ISO 27001 + ISO 27701 (CIS) |
| ANEXIA Internetdienstleistungs GmbH | Infrastructure / personnel | AT | 2026-04-24 (inherited) | Netcup Annex 2 | — | ISO 27001 + ISO 27701 (CIS) |
| DATASIX Rechenzentrumsbetriebs GmbH | Data-center operations | DE | 2026-04-24 (inherited) | Netcup Annex 2 | — | ISO 27001 + ISO 27701 (CIS) |
| ANX Holding GmbH | Support services | AT | 2026-04-24 (inherited) | Netcup Annex 2 | — | — |
| ISRG / Let's Encrypt | TLS DV issuance | US (DPF) | — | n/a (public CA) | — | WebTrust for CAs |
| <registrar> | Domain registration + authoritative DNS | (EU) | <signup date> | <invoice> | — | — |
```

Link to this register from `https://statnive.live/privacy`. Update on every upstream change within 7 days of receiving notice.

**Gitignore rule** (add to `.gitignore` at repo root):

```
releases/infrastructure/*.pdf
releases/infrastructure/testssl/*.txt
```

The PDFs carry signatures and master-data; the `testssl` outputs carry cert serials. Both are sensitive enough to gitignore and archive encrypted.

---

## 8. Verification (how to know this is done)

```bash
# 1. DPA signed in CCP — human check; Netcup CCP shows "DPA signed" status
#    on Master Data → Order Processing.

# 2. Subprocessor register populated
test -f docs/compliance/subprocessor-register.md
grep -q "Netcup GmbH" docs/compliance/subprocessor-register.md
grep -q "ANEXIA Deutschland" docs/compliance/subprocessor-register.md
grep -q "DATASIX" docs/compliance/subprocessor-register.md

# 3. Archived PDF exists + is gitignored
test -f releases/infrastructure/netcup-dpa-2026-04-24.pdf
git check-ignore releases/infrastructure/netcup-dpa-2026-04-24.pdf

# 4. DNS — CAA, MX (null), SPF, DMARC, DNSSEC
dig +short statnive.live A
dig +short statnive.live AAAA
dig +short statnive.live CAA
dig +short statnive.live MX     # expect: 0 .
dig +short statnive.live TXT    | grep -q 'v=spf1 -all'
dig +short _dmarc.statnive.live TXT | grep -q 'p=reject'
dig +dnssec statnive.live | grep -q 'RRSIG'

# 5. TLS
curl -sSI https://statnive.live | grep -i 'strict-transport-security'
curl -sSI https://statnive.live | grep -i 'HTTP/2\|HTTP/3'
testssl.sh --quiet --fast statnive.live | grep -i 'TLS 1.3'

# 6. Server hardening
systemctl show statnive-live | grep -E '^(NoNewPrivileges|ProtectSystem|PrivateTmp|CapabilityBoundingSet)='
lsblk -f | grep -i 'crypto_LUKS'
iptables -L OUTPUT -v -n | head -1      # default policy DROP
chronyc sources | grep -vE 'pool.ntp.org'    # empty or Netcup-only
ss -tlnp | grep ':9000' | grep -q '127.0.0.1'   # ClickHouse localhost only

# 7. Public /privacy page renders the §3 list
curl -sS https://statnive.live/privacy | grep -q 'Netcup'
curl -sS https://statnive.live/privacy | grep -q 'ANEXIA'
```

The binary air-gap test (`iptables -A OUTPUT -j DROP`) remains authoritative for the isolation invariant — rerun after every server config change (CLAUDE.md § Isolation "Verification").

---

## 9. Cross-references (not duplicated here)

- **Legal chain** (Art. 28 language, Recital 26, C-413/23) → [`privacy-detail.md`](privacy-detail.md)
- **14 product-level security items** (incl. fallback CA discussion) → [`security-detail.md`](security-detail.md)
- **Procurement rationale + provider comparison** → [`../../../jaan-to/docs/research/36-devops-hetzner-saas-vps-selection-2026.md`](../../../jaan-to/docs/research/36-devops-hetzner-saas-vps-selection-2026.md)
- **Customer-facing DPA** (statnive.live → customer) → `docs/dpa-draft.md` (Phase 11)
- **Deployment-mode overview** (SaaS / air-gap / cost) → [`../deployment.md`](../deployment.md)
- **Enforcement tests** (6-test matrix pinning invariants) → [`enforcement-tests.md`](enforcement-tests.md)
