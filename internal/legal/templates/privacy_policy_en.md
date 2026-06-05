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

## Recipients and connected AI assistants

We do not sell your data or share it with advertising networks or data
brokers. The only third party that may receive analytics derived from
your visits is an **AI assistant the site operator chooses to connect**
to their own dashboard:

- If the operator connects an AI assistant — for example **ChatGPT
  (OpenAI, Inc., United States)** — to their `statnive.live` analytics,
  that assistant can read the operator's **aggregate** analytics through
  a read-only, operator-authorised connection. This never includes your
  raw IP address or any raw identifier (those are never stored; see
  above).
- The operator initiates and authorises the connection on a consent
  screen; no analytics are sent to any assistant until they do.
- OpenAI is a United States recipient; any such transfer relies on the
  EU-US Data Privacy Framework adequacy decision (GDPR Art. 45), with the
  Art. 46 / 49 safeguards as fallback. This integration is **gated and
  off by default**: it is not active until an operator enables it. The DPA
  (`/legal/dpa` § 6) sets out the OpenAI sub-processor terms, and OpenAI
  is added to our public sub-processor register (mirrored at
  `https://statnive.live/privacy`) before the integration is offered.

### Data a connected assistant can read

When connected, the assistant reads only these **aggregate** categories
(the same set enumerated in the DPA § 3), never your individual raw
events:

- Visitor and pageview counts, and repeat-vs-new visitor splits.
- Conversions and goal completions, including operator-defined goal
  names.
- Revenue and revenue-per-visitor, where the operator tracks
  e-commerce.
- Traffic source / channel grouping and UTM campaign attribution.
- Approximate geography (country, region, city) from the one-time IP
  lookup described above.
- Custom event names and custom-property **sample values**. Sample
  values are content the operator's own site supplies; a mis-instrumented
  site could place personal data there, so operators are advised never to
  put personal data in event properties.

## Lawful basis

GDPR Art. 6(1)(f) — legitimate interest. Our balancing test is
publicly available at `/legal/lia`.

## Retention

- Raw event rows: 180 days.
- Aggregated daily/hourly visitor rollups: 750 days (~24.6 months).
- Audit log: per the operator's logrotate policy.

## Hybrid consent flow (when the operator enables it)

Some sites running `statnive.live` use **hybrid consent mode**, where
analytics behave differently before and after you accept:

- **Before you accept** (or if you never do): we collect only
  anonymous aggregate counts. No `_statnive` identifier cookie is
  set, your `Sec-GPC: 1` header is honoured, dashboard counts are
  rounded to the nearest 10, and only the three event types in the
  operator's allow-list are accepted. Stored visitor counts cannot
  be tied back to your individual visit.
- **After you accept**: we set a privacy-preserving `_statnive`
  cookie (the at-rest value is a per-tenant SHA-256 hash; the
  cookie itself never leaves your browser as a plain identifier)
  so we can recognise repeat visits within 13 months. Counts
  become exact and any event type the site instruments is accepted.
- **Withdrawing acceptance** clears both cookies and adds your
  visitor to a server-side suppression list — subsequent events
  from your browser are silently dropped. Past data is not
  automatically erased; use the Erasure endpoint below.

You can accept or withdraw via `POST /api/privacy/consent` with
JSON body `{"action": "give"}` or `{"action": "withdraw"}`. The
operator's site UI typically exposes a button for this.

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
