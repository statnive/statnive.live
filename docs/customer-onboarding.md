# Customer onboarding

Operator-facing how-to for taking a new customer from "signed SOW" to "ingesting events on their own VPS." Covers both `outside-iran` and `inside-iran` postures. SaaS onboarding (a new `site_id` on statnive.live) lives in `docs/runbook.md § Auth + RBAC operator SOPs`.

## Pre-conditions (legal / commercial, before any technical work)

- Signed SOW or commercial agreement (sales-driven, not in this doc)
- For EU-visitor traffic: signed DPA (`docs/dpa-draft.md`) + sub-processor disclosure if you'll route through any other parties (`docs/compliance/subprocessor-register.md`)
- For Iranian customer entity: OFAC + EU + UK sanctions screening (manual, outside scope)
- Customer's `site_id` allocated (next free integer in `statnive.sites`)
- Customer's chosen hostname registered + DNS configured

## Pick the posture

Use this decision tree:

```
Is the VPS inside Iran?
├── Yes  → inside-iran   (see § Inside-Iran cutover below; covered by docs/runbook.md § Phase 10)
└── No
    └── Does the customer host themselves (vs us)?
        ├── Yes  → outside-iran  (see § Outside-Iran cutover below)
        └── No   → saas           (add site_id to statnive.live; not in this doc)
```

The three postures share the same binary; only the install knobs differ. See `docs/deployment-postures.md` for the reference table.

## Outside-Iran cutover

### Operator-side (outside customer's environment)

1. **Generate the customer's license JWT** on your trusted laptop (offline-only — never via Claude):
   ```bash
   make statnive-license-build
   ./bin/statnive-license sign \
       --priv=/path/to/age-decrypted/license-signing.key \
       --customer="${CUSTOMER_NAME}" \
       --site="${CUSTOMER_SITE_ID}" \
       --max-events-per-day="${CUSTOMER_DAU_BUDGET}" \
       --features=dashboard,tracker,geoip-db23 \
       --exp="$(date -d '+1 year' +%F)" \
       > "${CUSTOMER_NAME}.license.jwt"
   ```
2. **Stage TLS PEMs** — if the customer wants statnive-live to terminate TLS directly (vs behind their reverse proxy), the operator either issues a Let's Encrypt cert in advance OR provides a CSR template for the customer to issue themselves. ACME-from-the-binary is v1.1 (`autocert` slip).
3. **Push the courier**:
   ```bash
   make release-customer POSTURE=outside-iran \
       VERSION=v0.0.15 \
       HOST=root@customer-host.example.com \
       LICENSE=./out/${CUSTOMER_NAME}.license.jwt \
       CERT_DIR=./out/${CUSTOMER_NAME}-tls/ \
       GEOIP_PATH=/path/to/IP2LOCATION-LITE-DB23.BIN
   ```
4. **Verify on customer side** — SSH in once, confirm:
   ```bash
   ssh root@customer-host 'systemctl status statnive-live'
   ssh root@customer-host 'curl -fsS https://127.0.0.1:8080/healthz'
   ssh root@customer-host 'grep posture /var/log/statnive-live/audit.jsonl | head -1'
   ```
5. **Hand off** — send the customer their dashboard URL + initial admin credentials (from `STATNIVE_BOOTSTRAP_ADMIN_*` envs in the systemd drop-in). Tell them to rotate the password immediately.

### Customer-side (their VPS preparation)

The customer's prep — they don't do the install themselves; you do:

- Provision Ubuntu 22.04+ or Debian 12+ VPS with at least 4 vCPU / 8 GB RAM / 50 GB disk (CLAUDE.md § "Stack").
- Open inbound 443 (HTTPS) and 80 (HTTP for ACME challenge if applicable).
- Create an SSH key + share the pubkey with us.
- DNS A/AAAA records pointing their tracker hostname at the VPS.
- (If terminating TLS via reverse proxy) configure their nginx/Caddy to proxy `/api/event` + `/admin` + `/app` to `127.0.0.1:8080`.

## Inside-Iran cutover

This is a **distinct workflow** that requires more operator-side prep. The full SOP is in `docs/runbook.md § Phase 10` — that section is the source of truth, do not duplicate the steps here. Key differences from outside-Iran:

- TLS PEMs MUST come from the outside-Iran `cert-forge` bastion (no ACME-from-Iran)
- NTP MUST be `chrony.conf.asiatech` (Iranian sources only; Iranian DCs unreachable from `time.cloudflare.com` etc.)
- Outbound MUST be `iptables -P OUTPUT DROP` + allowlist
- License JWT exp + features list bake in the customer's contract terms
- `consent.required=false` per Privacy Rule 5 (Iran has no GDPR)

The courier command differs only by `POSTURE=inside-iran` — `--apply-iptables` + `--ntp-profile=asiatech` are implied (L2):

```bash
make release-customer POSTURE=inside-iran \
    VERSION=v0.0.15 \
    HOST=root@customer-asiatech-host \
    LICENSE=./out/${CUSTOMER_NAME}.license.jwt \
    CERT_DIR=./out/${CUSTOMER_NAME}-tls-from-cert-forge/ \
    GEOIP_PATH=/path/to/IP2LOCATION-LITE-DB23.BIN
```

## Post-cutover checklist (all postures)

- [ ] First 30 days: monitor the `/healthz` posture announce daily — confirms the customer hasn't bricked their config
- [ ] Week 1: audit the first day of events for `consent` / `user_id` / IP hashing correctness via the DB-oracle queries in `docs/runbook.md § Air-Gap Verification`
- [ ] Week 2: confirm GeoIP enrichment is producing non-`--` country codes (means the IP2Location DB23 BIN is staged correctly)
- [ ] Month 1: customer dashboard adoption — log in count > 0, otherwise flag for re-onboarding call

## When the customer leaves

- Revoke their license JWT (no remote-revocation in v1; we just stop issuing renewals — the JWT exp does the rest)
- Audit-log all `/api/admin/*` calls during the offboarding window
- Issue a final data export (CSV via dashboard or `clickhouse-client SELECT … INTO OUTFILE`)
- Customer manually runs `airgap-install.sh --uninstall` (binary + systemd drop-in removed; data + config retained for the contractual retention window)

## See also

- `docs/deployment-postures.md` — the three postures, side-by-side
- `docs/runbook.md § Phase 10` — inside-iran cutover SOP (canonical)
- `docs/runbook.md § Phase 10b` — outside-iran cutover SOP (this doc's expanded sibling)
- `docs/dpa-draft.md` — DPA template for EU-visitor-bearing customers
- `cmd/statnive-license/README.md` — the license-signing CLI's full flag reference
