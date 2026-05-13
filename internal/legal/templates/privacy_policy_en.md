# Privacy notice — statnive.live analytics

> **Status:** template — counsel review required before publication.
> Operator-specific data (legal entity name, contact, DPO, EU
> representative, sub-processor list) is left blank in this template.

## Who processes your data

The operator of this website — referred to below as "we" — is the
controller for any analytics processing on this site. We deploy
`statnive.live`, a privacy-first web-analytics platform, as the
processor. The relationship is governed by a Data Processing
Agreement (DPA) — see `/legal/dpa`.

## What we process

When you load a page, your browser sends:

- The page URL (path, no query string).
- The referring URL (host only — no path, no query string).
- Your IP address, used **once** for a country/region/city lookup
  and then discarded before any row is written to disk.
- A short-lived per-day pseudonymous visitor identifier derived
  via BLAKE3-128 keyed by a salt that rotates every 24 hours.
- A first-party `_statnive` cookie containing a per-visitor UUID,
  used to recognise repeat visits within a session. We store only
  a SHA-256 hash of this value alongside your events.

## What we do NOT process

- We do not run canvas, WebGL, or font-availability probes.
- We do not enumerate `navigator.plugins` or `navigator.mimeTypes`.
- We do not store your raw IP address.
- We do not share your data with advertising networks.
- We do not transfer your data to data brokers.

## Lawful basis

GDPR Art. 6(1)(f) — legitimate interest. Our balancing test is
publicly available at `/legal/lia`.

## Retention

- Raw event rows: 180 days.
- Aggregated daily/hourly visitor rollups: 750 days (~24.6 months).
- Audit log: per the operator's logrotate policy.

## Your rights

- **Object** (Art. 21): `POST /api/privacy/opt-out` — sets a
  strictly-necessary cookie that suppresses further events from
  your browser.
- **Access** (Art. 15): `GET /api/privacy/access` — receive a copy
  of the events tied to your `_statnive` cookie.
- **Erasure** (Art. 17): `POST /api/privacy/erase` — delete all
  events tied to your `_statnive` cookie.

## Contact

For data-protection enquiries, contact the operator at the email
address listed on this site. To file a complaint with a supervisory
authority, the relevant EU regulator depends on your residence —
the list is available at `https://edpb.europa.eu/about-edpb/board/members_en`.
