# Sub-processor register — statnive.live SaaS

> Canonical list of every sub-processor in the statnive.live processing chain. Mirrors the public list at `https://statnive.live/privacy` and the Annex 2 inheritance chain from Netcup's DPA.
>
> Update cadence: **within 7 days** of receiving upstream notice (keeps us inside Netcup's 14-day Annex 2 window and the 14-day customer-notice window we owe downstream — see [`docs/rules/netcup-vps-gdpr.md` § 3](../rules/netcup-vps-gdpr.md#3-sub-processor-disclosure-downstream-obligation)).
>
> This file contains **no PII** and is checked into the repo. Signed Netcup DPA PDFs are gitignored under `releases/infrastructure/*.pdf` per [`docs/rules/netcup-vps-gdpr.md` § 7](../rules/netcup-vps-gdpr.md#7-sub-processor-register--new-file-seed).

## Active sub-processors (statnive.live SaaS, D1 — Netcup VPS 2000 G12 NUE)

| Name | Role | Country | Contract date | Contract path | Notice contact | Last audit |
|---|---|---|---|---|---|---|
| Netcup GmbH | Infrastructure / VPS (8 vCore EPYC / 16 GB / 512 GB NVMe / Nuremberg) | DE | 2026-04-24 (DPA pending operator signature in CCP — see runbook § Sign Netcup DPA) | `releases/infrastructure/netcup-dpa-2026-04-24.pdf` (encrypted, gitignored) | abuse@netcup.de | ISO 27001 (TÜV NORD CERT) |
| ANEXIA Deutschland GmbH | Infrastructure / personnel (Netcup Annex 2 inheritance) | DE | 2026-04-24 (inherited) | Netcup Annex 2 | — | ISO 27001 + ISO 27701 (CIS) |
| ANEXIA Internetdienstleistungs GmbH | Infrastructure / personnel (Netcup Annex 2 inheritance) | AT (EEA) | 2026-04-24 (inherited) | Netcup Annex 2 | — | ISO 27001 + ISO 27701 (CIS) |
| DATASIX Rechenzentrumsbetriebs GmbH | Data-center operations (Netcup Annex 2 inheritance) | DE | 2026-04-24 (inherited) | Netcup Annex 2 | — | ISO 27001 + ISO 27701 (CIS) |
| ANX Holding GmbH | Support services (billing, regulatory) (Netcup Annex 2 inheritance) | AT (EEA) | 2026-04-24 (inherited) | Netcup Annex 2 | — | — |
| ISRG / Let's Encrypt | TLS DV certificate issuance for `statnive.live` + `app.statnive.live` + `demo.statnive.live` (3-SAN cert via `certbot --dns-…`) | US (DPF) | _to be filled when first cert issues_ | n/a (public CA, no PII transfer in DV issuance) | — | WebTrust for CAs |
| Cloudflare, Inc. | Authoritative DNS (DNS-only / grey-cloud) for `statnive.live` zone — no proxy, no Workers, no Analytics. EU visitors' DNS query metadata in Cloudflare's logs. | US (DPF) | _to be filled when zone delegated_ | [`deploy/dns/statnive.live.zone`](../../deploy/dns/statnive.live.zone) | abuse@cloudflare.com | SOC 2 Type II / ISO 27001 / DPF certified |
| MailerLite Limited | Newsletter signup form on `https://statnive.live/` (pre-launch waitlist) and transactional + marketing email delivery to opted-in subscribers. Receives: subscriber email, IP at submission, browser UA, timestamp. Embed loads `assets.mlcdn.com` + `groot.mailerlite.com`; submit posts to `assets.mailerlite.com`. | EU (IE / LT) | _to be filled when DPA archived from MailerLite portal_ | [`internal/landing/index.html`](../../internal/landing/index.html), [`internal/landing/landing.go`](../../internal/landing/landing.go) | support@mailerlite.com | SOC 2 Type II / ISO 27001 / GDPR DPA template |

> **Cloudflare carve-out.** [`docs/rules/netcup-vps-gdpr.md` § 4.1](../rules/netcup-vps-gdpr.md#41-registrar-and-authoritative-dns) originally said "Not Cloudflare." On 2026-04-25 the project decided to use Cloudflare's free tier as the authoritative NS for `.live` (zone is **DNS-only / grey cloud**, no orange-cloud proxy, no TLS termination, no Workers, no Cloudflare Analytics — Netcup origin terminates LE-issued TLS itself). Cloudflare is a US-resident sub-processor under DPF; this register entry + the public `/privacy` page disclose the transfer per Art. 28(2). The rule file has been updated to reflect this decision; the carve-out applies to `.live` only — `.ir` zones remain off Cloudflare per the `iran-no-cloudflare` Semgrep rule.

> **MailerLite carve-out.** Added 2026-05-04 alongside the public coming-soon page at [`internal/landing/`](../../internal/landing/). MailerLite is loaded **lazily** by the landing JS — drive-by visitors fire zero requests to MailerLite hosts. Only visitors who focus the email input or interact with the submit button trigger `assets.mlcdn.com` / `groot.mailerlite.com` script loads, and only submitters cross-border-transfer their email + IP to MailerLite (EU-resident: registered in IE, processing in LT). The Iranian-DC air-gap binary does not register the landing route, so MailerLite is unreachable from any IR-resident code path — no `iran-no-cloudflare`-style sanctions exposure.

## Future sub-processors (planned, register before activation)

| Name | Role | Country | Trigger | Disclosure due |
|---|---|---|---|---|
| Polar.sh | Merchant-of-record checkout + Polar webhook (Phase 11b) | US | First call to `POST /api/billing/checkout` | Before Phase 11b cutover |
| Outside-Iran release bastion | Courier host bridging GitHub → Iranian VPS for `make release-iran-vps` (Phase 10 P1) | (host-dependent — register the actual provider) | First courier run | Before Phase 10 P1 cutover (per [`PLAN.md:358`](../../PLAN.md#L358)) |
| IP2Location DB23 paid distribution endpoint | DB23 BIN download (replaces LITE at Phase 10) | US (DPF) | First paid DB23 download | Before Phase 10 procurement |
| SMTP provider (TBD) | Transactional email (signup verification, password reset, weekly digest) — gated by `email.enabled` | (provider-dependent) | First call to `email.Send` | Before Phase 11a `email.enabled=true` |
| Telegram Bot API | Optional alert fan-out (v1.1, gated by `alerts.telegram.enabled`) | UK (operator-elected) | First operator opt-in | Before flipping the config flag |

## Procedural reminders

- **Never silently add a sub-processor.** Every new row above is a commit to this file + a change-log entry on `https://statnive.live/privacy`. Re-publish the public list within 7 days of receiving upstream notice. PR review (not CI) polices this.
- **Operator action: sign the Netcup DPA in CCP** ([https://ccp.netcup.net/](https://ccp.netcup.net/), customer 365334) per [`docs/rules/netcup-vps-gdpr.md` § 2.2](../rules/netcup-vps-gdpr.md#22-annex-3-form--verbatim-values-to-enter) — the runbook supplies verbatim Annex 3 values. Save the returned PDF to `releases/infrastructure/netcup-dpa-2026-04-24.pdf` (chmod 0600), record signing date + customer 365334 + PDF SHA-256 in the Netcup row above.
- **Customer-facing DPA** (statnive.live → customer) lives at [`docs/dpa-draft.md`](../dpa-draft.md) and lists this register by reference; updates here propagate via the next signed DPA revision.
- **Verification:** [`docs/rules/netcup-vps-gdpr.md` § 8](../rules/netcup-vps-gdpr.md#8-verification-how-to-know-this-is-done) lists the `grep` checks against this file (`Netcup GmbH`, `ANEXIA`, `DATASIX` must all be present).
