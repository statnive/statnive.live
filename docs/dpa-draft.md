# Data Processing Agreement (DPA) — statnive.live → Customer

> **Status: DRAFT.** This is the customer-facing DPA template (statnive.live = processor, customer = controller). Required as a Phase 11a hard gate per [`PLAN.md` § Phase 11a](../PLAN.md) — every paying or free-tier SaaS customer accepts this DPA at signup. Not yet legal-reviewed; do not present to a customer until reviewed.
>
> **Cross-references**
> - **Upstream chain** (statnive.live → Netcup) — [`docs/rules/netcup-vps-gdpr.md`](rules/netcup-vps-gdpr.md). Mirror the §2.4 indemnity clause downstream.
> - **Sub-processor list** — [`docs/compliance/subprocessor-register.md`](compliance/subprocessor-register.md). Disclosed to customer by reference (Schedule A).
> - **Privacy + identity legal chain** (Recital 26, C-413/23, Art. 28 language) — [`docs/rules/privacy-detail.md`](rules/privacy-detail.md).
> - **Public privacy notice** — `https://statnive.live/privacy`.
> - **Template seed** — `.claude/skills/grc-gdpr/references/dpa-template.md`.

---

## Parties

**Controller**: the natural or legal person operating a website that has installed the statnive.live tracker on at least one site (`site_id`) registered to the Controller's account ("Customer", "Controller").

**Processor**: statnive.live SaaS, operated by Parhum Khoshbakht, customer 365334 of Netcup GmbH ("statnive.live", "Processor").

This DPA is co-terminus with the Customer's subscription to statnive.live (Free, Starter, Growth, Business, Scale, or Enterprise tier per [`docs/deployment.md` § SaaS Model](deployment.md#saas-model-statnive-live-cloud)) and continues until all Customer data has been deleted per § 5.7.

---

## 1. Subject Matter and Duration (Art. 28(3))

Privacy-first web-analytics processing — collection of pseudonymous visitor identifiers, GeoIP-derived country/region/city, user-agent strings, and HTTP referrers from EU visitors to Customer's website, on the Customer's documented instruction (the act of installing the tracker JS).

Duration: co-terminus with the Customer's active subscription. Processing ceases on subscription termination.

## 2. Nature and Purpose of Processing (Art. 28(3))

Processor will process personal data only for:

- Aggregating visitor counts, page-view counts, source/channel attribution, and goal/funnel completions on the Customer's behalf.
- Surfacing those aggregates in the Customer's dashboard at `/s/<slug>/`.
- Storing the underlying pseudonymous events in ClickHouse and rolling them up into materialized views for query performance.

Processor will not process the data for any other purpose, including: targeted advertising, profiling for third-party use, training of machine-learning models, or sale to data brokers.

## 3. Categories of Personal Data (Art. 28(3))

| Category | Form stored | Retention |
|---|---|---|
| Visitor identifier (BLAKE3-128 hash of `master_secret \|\| site_id \|\| user_id`) | `FixedString(16)` | Raw event 30 days; rollups indefinite (HLL state, anonymous per Recital 26 + CJEU C-413/23) |
| Source IP address | **Never persisted.** Used only for GeoIP lookup, then discarded before the batch writer sees the row. | Zero |
| GeoIP-derived country / region / city | `LowCardinality(String)` | Raw event 30 days; rollup `daily_geo` indefinite |
| User-agent string + parsed UA fields (browser, OS, device class) | `String` + `LowCardinality(String)` | Raw event 30 days; rollup `daily_devices` indefinite |
| HTTP referrer + parsed channel grouping | `String` + `LowCardinality(String)` | Raw event 30 days; rollup `daily_sources` indefinite |
| Custom event names + values (Customer-defined goals) | `String` + numeric | Raw event 30 days; rollup goal-state indefinite |

**No special categories (Art. 9)** are processed. The platform is architecturally incapable of processing race / religion / genetic / biometric / political / union / health / sex-life data.

**No cookies, localStorage, sessionStorage, or fingerprinting** (canvas / WebGL / font enumeration) per the Privacy Rules (Non-Negotiable) in `CLAUDE.md`.

## 4. Categories of Data Subjects (Art. 28(3))

- Visitors to Customer's website(s) registered under the Customer's `site_id`.

## 5. Processor Obligations (Art. 28(3))

### 5.1 Instructions Only (Art. 28(3)(a))

Processor processes personal data only on documented instructions from Controller. Installing the tracker script with a `data-site-id` attribute, configuring goals, and configuring funnels are the documented instructions for the corresponding processing operations. International transfers are limited to those identified in § 6.

### 5.2 Confidentiality (Art. 28(3)(b))

All Processor personnel with access to Customer data are bound by written confidentiality obligations.

### 5.3 Security (Art. 28(3)(c); Art. 32)

Processor implements the technical and organisational measures detailed in [`docs/rules/security-detail.md`](rules/security-detail.md), including at minimum:

- TLS 1.3 only on all customer-facing endpoints; HSTS preloaded.
- ClickHouse bound to `127.0.0.1`; never publicly exposed.
- LUKS encryption of the ClickHouse data volume on the Netcup VPS (shared-tenant virt — required tier per [`docs/luks.md`](luks.md)).
- Encrypted backups (`clickhouse-backup` + `age` + `zstd`) shipped to an EU-only second location; restore drill on every release.
- Per-IP rate limiting, CGNAT-aware tiering, mass-assignment guards, OWASP A10 SSRF guard on all opt-in outbound paths.
- BLAKE3-128 hashing of visitor identifiers with daily-rotating HMAC salt; raw IP never persisted.
- systemd hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `CapabilityBoundingSet=CAP_NET_BIND_SERVICE`).
- Append-only audit log with `chattr +a` discipline; logrotate `copytruncate=off`.

### 5.4 Sub-processors (Art. 28(2), 28(3)(d))

Customer hereby gives general written authorisation for Processor to engage the sub-processors listed in [Schedule A](compliance/subprocessor-register.md). Processor will publish notice of any new sub-processor or replacement at `https://statnive.live/privacy` at least 14 days before the change takes effect; Customer may object in writing within that window. Processor imposes equivalent data-protection obligations on each sub-processor and remains liable to Customer for sub-processor failures.

### 5.5 Data Subject Rights (Art. 28(3)(e))

Processor assists Customer in responding to data-subject requests under Arts. 15–22 by providing:

- `GET /api/privacy/export?user_id=…` — visitor-scoped data export (CSV / JSON).
- `DELETE /api/privacy/erase?user_id=…` — visitor-scoped erasure across raw + rollup tables (CASCADE), with `system.tables` enumerated dynamically so a forgotten table fails the integration test by construction (per [`PLAN.md:585`](../PLAN.md#L585) DSAR completeness gate).

### 5.6 Assistance with Controller Obligations (Art. 28(3)(f))

Processor assists Customer with Art. 32 (security), Art. 33–34 (breach notification), and Art. 35–36 (DPIA) on reasonable request, taking into account the nature of processing and information available.

### 5.7 Deletion or Return (Art. 28(3)(g))

On termination of the subscription, Customer may export all data via the standard CSV/JSON export endpoint within 30 days. After 30 days, Processor deletes all Customer data from raw tables, rollup tables, backups (next backup cycle ≤ 24h), and audit logs, except where Union or Member State law requires retention.

### 5.8 Audit Rights (Art. 28(3)(h))

Processor makes available all information necessary to demonstrate Art. 28 compliance, including this DPA, the sub-processor register, and the privacy-detail / security-detail technical specs. Customer may audit on 30 days' notice, no more than once per year absent cause; reasonable costs of an audit beyond review of the published documentation are borne by Customer.

## 6. International Transfers (Art. 44–49)

All processing of EU personal data occurs in **Nuremberg, Germany** on the Netcup VPS 2000 G12 NUE. There is **no Chapter V transfer** of stored personal data outside the EEA.

The following sub-processors are US-resident and are disclosed under EU-US Data Privacy Framework (DPF) adequacy (Art. 45):

- **ISRG / Let's Encrypt** — TLS DV certificate issuance only. No personal data is transferred during ACME issuance (DNS-01 challenge).
- **Cloudflare, Inc.** — authoritative DNS for the `statnive.live` zone in DNS-only / grey-cloud mode. Cloudflare receives DNS query metadata (resolver IP, queried name) but no application payload; no proxy, no Workers, no Cloudflare Analytics.

## 7. Breach Notification (Art. 33)

Processor will notify Customer of any personal data breach **without undue delay** and in any event within **48 hours** of becoming aware, providing the information required by Art. 33(3) to the extent available at that time.

## 8. Mutual Indemnity (Art. 82)

Each party will indemnify the other against losses arising from its own breach of this DPA, mirroring the §3.10 / §4.3 indemnity in the upstream Netcup DPA per [`docs/rules/netcup-vps-gdpr.md` § 2.4](rules/netcup-vps-gdpr.md#24-non-negotiable-in-the-netcup-dpa-body).

## 9. Governing Law

This DPA is governed by the laws of [JURISDICTION TBD — pending legal review]. Disputes will be resolved exclusively in the courts of [VENUE TBD].

---

## Schedule A — Sub-processor list

See [`docs/compliance/subprocessor-register.md`](compliance/subprocessor-register.md). Updated within 7 days of any upstream change; the version in force at the time of a customer agreement is the snapshot at that commit SHA.

## Schedule B — Standard Contractual Clauses

Not currently invoked — all processing of stored data is intra-EEA. If Processor's sub-processor chain ever requires SCCs (e.g. an SMTP provider outside DPF), Processor will publish the applicable SCC module here and provide 14 days' notice per § 5.4.
