# Product

## Register

product

## Users

**Primary — Customer-viewer.** A non-technical site owner of a high-traffic web or streaming property. They reach the panel two ways:

- **SaaS path** (`app.statnive.live/s/<slug>/`, Phase 11a onward): self-served signup, owns one site, opens the panel weekly to check who's visiting and what's earning. Browser, desktop or laptop, daylight room. Their other tabs are Stripe, Shopify, Polar, their CMS. They are not an SRE.
- **Self-hosted viewer-role**: their operator set them up with `viewer` RBAC on a self-hosted install. Same screens, same expectations, no admin chrome.

What they want from any session: *"Did the work I shipped last week move the number? Where did the visitors come from? Is anything obviously broken?"* Sessions are short (1–3 minutes), repeat-visit cadence is daily or weekly, not real-time.

**Secondary — Admin.** RBAC `admin`. Same person as the customer-viewer in single-site SaaS; a separate ops/marketing lead in larger self-hosted deployments. Adds extra screens (Sites, Users, Goals, future Health). When admin is active, the chrome shifts (amber underline on Nav, amber Admin sub-tab row) so the user knows they are in a higher-privilege mode. Admin surfaces stay engineer-grade.

**Tertiary — Operator / SRE.** Same person as Admin in small deployments; dedicated role at SamplePlatform-scale. Cares about ingest health, WAL fill, GeoIP reload, TLS expiry. Currently lives in `/var/log/statnive-live/alerts.jsonl` and `/healthz`. The future Health panel (`v1.1-frustration-panel`, `6-polish-5`) is their on-panel surface. Strictly instrument-panel; not the design centre of gravity.

**Quaternary — API role.** No UI. Token-only. Mentioned only so the panel never assumes "if not admin, then viewer" without checking — a `role` chip in TopBar (`6-polish-6`) is the affordance.

**Geographic skew (doc 30 calibration):** ~62% Iran, ~38% diaspora on the SamplePlatform reference profile. The panel itself is operator-facing English in v1; v1.1-rtl unlocks Persian for the customer-viewer surface. RTL plumbing is a v1.1 hard requirement, not a stretch goal.

## Product Purpose

statnive-live is a self-hosted-or-SaaS analytics platform that gives a non-technical site owner the answers a marketing team would otherwise pull from Google Analytics or Plausible, without sending visitor data to a third party and without the analyst-grade complexity of PostHog or Mixpanel.

Success looks like:

1. **First-event activation under 5 minutes** from install or signup, with a wait-for-first-event gate that explains *why* the dashboard is empty (doc 35 R1 — the single highest-leverage onboarding move).
2. **A weekly check-in that takes under 60 seconds**: Overview KPIs with deltas, then either Sources or Pages depending on what moved, then back to other work.
3. **Revenue attribution that survives translation to a board slide**: the dual-bar Visitors/Revenue idiom on Sources / Pages / Campaigns answers "which channel is actually paying?" without requiring a multi-touch model.
4. **Operator can sleep**: WAL durability, TLS rotation, GeoIP reload, and rate-limit tiering happen in the binary, surface as alerts in `/healthz` and the alerts file, and never page the customer-viewer.

What this product is **not**: not a session-replay tool, not a feature-flag-and-experimentation suite, not an SRE observability platform, not a kitchen-sink "everything app" in the PostHog mold, not a WordPress plugin (the sister `statnive/` repo is a separate product on a separate cadence).

## Brand Personality

**Three words: calm, instrumented, sovereign.**

- **Calm.** No shocking gradients, no urgency reds on routine state, no auto-playing anything. DeltaPill carries a ±1% deadband so a rounding-error change doesn't render as a green arrow. WITH FILL on time-series gaps so a missing hour reads as missing, not as a zero plunge. (Reject the "hero metric template" + neon-SRE register — both are absolute bans in the impeccable laws.)
- **Instrumented.** Numbers are honest. Bounce rate, time-on-site, pages-per-session never appear as headline KPIs (vanity metrics, doc 17 §68, doc 35 §6). When a panel is unimplemented (Geo, Devices, Funnel until v1.1/v2), it renders as a `SOON` mono pill — never as a faux chart with synthetic data.
- **Sovereign.** First-party fonts (no CDN), tracker via `go:embed`, no third-party telemetry pixel on the panel itself, version + commit-sha visible, air-gap mode is a first-class deployment posture (not a stripped-down variant). The panel never phones home. This shows up in details, not in copy.

**Voice and vocabulary.** Operator-vocab honest, with tooltip-style helpers for the customer-viewer reader. Nav labels stay engineer-mental-model: Sources, Pages, SEO, Campaigns, Realtime, Admin (matches URL hash slugs, matches the channel mapper, matches the docs). On hover, each nav label and each table column header offers a one-line plain-language explanation ("Sources — where your visitors arrived from"). Error and empty-state copy explains *why* and *how to fix*, never just "could not load — see logs" (doc 35 §2.12, doc 33 §3.1). No marketing copy in the dashboard. No celebratory exclamation marks on routine state. No em dashes (impeccable copy law).

**Brand-first principle (doc 33 §3.5).** Never piss the user off. No dark patterns. No "Are you sure you want to leave?" intercepts. No AI-mascot floating chatbot. No sales CTAs in the operator console. No countdown timers on free-tier limits. When in doubt, refund.

## Anti-references

The panel must NOT read like:

- **Datadog / Grafana / New Relic** — SRE-dark + neon, the first-order observability training-data reflex. We are not a metrics-and-traces platform; we are a customer-facing analytics product. Any time the panel starts looking like an oncall console, it has drifted.
- **PostHog** — purple-saturated kitchen-sink, AI-mascot floating chatbot ("Max"-style), session-replay sidebar. Doc 32 §"What I'd change" and doc 33 §"What I'd change" both publicly land here. Not us.
- **Plausible / Fathom / Simple Analytics** — marketing-cute green over sparse charts, "we're your friendly privacy-first analytics buddy" register. statnive-live is operator-grade and sovereign; the cuteness register reads as undersized for a streaming-platform tenant.
- **Mixpanel / Amplitude** — shocking-color gradients, dense funnel-builder interfaces, "events analyst" vocabulary as primary navigation.
- **Vercel / Linear monochrome dark** — the second-order "we're not Datadog so we're greyscale-and-dark" trap. We are light-paper navy-ink because the brand says so, not because dark mode is safe.
- **WordPress admin chrome** — the sister `statnive/` plugin lives inside `wp-admin`. statnive-live is a separate self-contained product; nothing in the panel should read as a wp-admin extension (no `.notice`, no `.button-primary`, no Dashicons).
- **Session-replay surfaces, autocapture-by-default UIs** — banned by privacy posture (doc 32/34 §E, doc 35 line 304). The frustration-signals Health panel (rage clicks, dead clicks, p75 INP/LCP) is the deliberate substitute (doc 35 R7).
- **Remote agentic install wizard** — corrupts `.env` and frontmatter in public issues (doc 34 §A). The deterministic `v1.1-install-cli` is the alternative.

**Positive anchor.** When picking between options, ask "what would Stripe Dashboard do?" — data-density with warmth, operator vocabulary that survives translation to a non-technical reader, calm color, generous spacing, no celebratory animation on routine state. Stripe is the customer-viewer comp we are aiming to beat on warmth-per-byte.

## Design Principles

1. **Customer-viewer first; operator surfaces second.** The default boot path (login → Overview) must read calmly to a non-technical site owner. Engineer-grade affordances (Health, Admin, audit log) live behind a deliberate route or an amber-underlined Admin tab — never on the front page. When a design choice has to break a tie, break it for the customer-viewer.
2. **Brand-first: never piss the user off (doc 33 §3.5).** No dark patterns, no AI mascot, no sales CTA in the panel, no autoplay anything, no countdown timers on routine state. When refunds, exports, or account deletion are involved, default to the user's side. This rule outranks every other rule in this document.
3. **Calm instrumentation, not vanity dashboards.** Reject bounce rate, time-on-site, and pages-per-session as headline KPIs. DeltaPill ±1% deadband. WITH FILL on time-series gaps. `SOON` pills on unimplemented surfaces, not faux data. Frustration-signals Health panel replaces session replay as the diagnostic surface (doc 35 R7).
4. **Token + bundle discipline as the design substrate.** Every color, font, spacing, and radius routes through `var(--*)` in `web/src/tokens.css` (`make brand-grep` is the gate). The 7-channel chip palette (`--ch-*`) is reserved-role and locks to the 17-step `internal/enrich/channel.go` mapper — chip color change requires the Go mapper to change too. Bundle size limits in `web/.size-limit.json` are a design constraint (no React-toast libraries, no lottie, no icon webfonts), not a perf afterthought.
5. **Sovereignty visible in the details, not the copy.** First-party WOFF2 fonts under `/app/assets/`, no CDN, no third-party telemetry pixel on the panel itself, version + commit-sha in the footer, air-gap mode is first-class. The panel never says "self-hosted" or "privacy-first" in marketing voice — it just behaves that way.
6. **Rare summit moments, brand-coherent, customer-only.** The peak/summit logo metaphor (mountain peak with teal apex dot) may surface as occasional signature moments on customer-viewer surfaces — a tasteful first-10K-visitors milestone toast, a sparkline end-cap that nods to the apex, the `.live` teal accent reappearing in achievement chips. Operator/SRE surfaces (Health, Admin, audit) stay strictly instrument-panel. Never decorative; never on routine state; never more than one per session.

## Accessibility & Inclusion

**WCAG 2.2 AA is the floor**, with the v1.1-a11y slice (PLAN.md) scheduled before Phase 11a SaaS launch as a prerequisite for an internet-facing dashboard. Specifics:

- **Contrast locked.** Navy `#0A2540` on paper `#FAFAF8` is ~27:1 (AAA). Error `#B0243A` on paper `#FAFAF8` is ~6.5:1 (AA). These ratios are part of the token contract; PR review rejects any text/background pairing not derivable from the token table.
- **Color is never the only carrier.** Every `role="alert"` row, every error/success state, every chart series carries an icon prefix or label glyph. The `6-polish-4` slice ships this primitive.
- **`@axe-core/playwright` zero-violation gate** wired into the dashboard-e2e CI job (`v1.1-a11y` slice). New panels block merge on first violation.
- **Hidden `<table>` a11y mirror** behind every uPlot chart. Screen readers and keyboard-only users get the underlying numbers in semantic order; sighted users get the visualisation.
- **`prefers-reduced-motion` honored beyond LivePulse.** Sparkline draw-in, modal fade, toast slide, DeltaPill flash — all motion respects the user preference. The v1.1-a11y slice extends this everywhere; new motion lands disabled-under-reduced-motion by default.
- **Keyboard-trap audit on the four-row sticky chrome.** TopBar / DateBar / Nav / FilterStrip all accept tab order without trapping; sticky positioning never hides focused elements.
- **Role chip in TopBar (`6-polish-6`).** A viewer who doesn't see the Admin tab understands why because the chip says `viewer` next to their email.
- **RTL plumbing in v1.1-rtl** for the Persian customer-viewer surface (doc 35 R14, doc 33 §3.4). `dir="rtl"` plumbing, Vazirmatn or Estedad WOFF2 subset, locale switcher chip in TopBar, en/fa string catalogue, full RTL audit. Iran market unlock. The font and locale layer is first-party and bundled; no CDN.
- **Reduced-cognitive-load for the customer-viewer**: hover tooltips on every nav label and column header (operator-vocab + plain-language helper). Empty-state copy distinguishes "no data in this date range" from "filter excludes all rows" from "site has not received any events yet" — three distinct messages with three distinct fixes (doc 35 R2).
