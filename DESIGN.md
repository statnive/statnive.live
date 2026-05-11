---
name: statnive-live
description: Calm, instrumented, sovereign analytics panel — light paper, navy ink, Persian-Teal accent, peak/summit logo
colors:
  paper: "#FAFAF8"
  paper-2: "#FFFFFF"
  ink: "#0A2540"
  ink-2: "#12304F"
  cream: "#EDE3D1"
  rule-soft: "#E8E5DC"
  rule-hair: "#EFECE4"
  green: "#00A693"
  green-dk: "#007A6C"
  green-lt: "#B0D4CC"
  error: "#B0243A"
  error-dk: "#8A1D2E"
  amber: "#C47A0E"
  chart-visitors: "#0A2540"
  chart-revenue: "#00A693"
  chart-ochre: "#B87B1A"
  chart-plum: "#5F3B6E"
  chart-rust: "#A84628"
  ch-direct: "#00A693"
  ch-search: "#1A73E8"
  ch-social: "#1A1A1A"
  ch-email: "#7A4A6E"
  ch-referral: "#8B7355"
  ch-ai: "#0A2540"
  ch-paid: "#8A5508"
typography:
  display:
    fontFamily: "Space Grotesk, system-ui, sans-serif"
    fontWeight: 500
    letterSpacing: "-0.02em"
    lineHeight: 1
  headline:
    fontFamily: "Space Grotesk, system-ui, sans-serif"
    fontSize: "34px"
    fontWeight: 500
    letterSpacing: "-0.02em"
    lineHeight: 1
    fontFeature: "tnum"
  title:
    fontFamily: "Space Grotesk, system-ui, sans-serif"
    fontSize: "20px"
    fontWeight: 500
    letterSpacing: "-0.01em"
    lineHeight: 1.2
  body:
    fontFamily: "DM Sans, IBM Plex Sans, system-ui, sans-serif"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.45
  label:
    fontFamily: "JetBrains Mono, IBM Plex Mono, ui-monospace, monospace"
    fontSize: "10px"
    fontWeight: 500
    letterSpacing: "0.08em"
rounded:
  hair: "4px"
  card: "8px"
  surface: "12px"
  pill: "999px"
spacing:
  s-1: "4px"
  s-2: "8px"
  s-3: "16px"
  s-4: "24px"
  s-5: "32px"
  s-6: "48px"
components:
  button-primary:
    backgroundColor: "{colors.green}"
    textColor: "{colors.paper-2}"
    typography: "{typography.body}"
    rounded: "{rounded.card}"
    padding: "10px 20px"
  button-primary-hover:
    backgroundColor: "{colors.green-dk}"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.ink-2}"
    rounded: "{rounded.hair}"
    padding: "4px 8px"
  button-ghost-hover:
    textColor: "{colors.ink}"
  chip-pill:
    backgroundColor: "{colors.rule-hair}"
    textColor: "{colors.ink}"
    typography: "{typography.label}"
    rounded: "{rounded.pill}"
    padding: "2px 8px"
  chip-pill-active:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.paper-2}"
  card-kpi:
    backgroundColor: "{colors.paper-2}"
    textColor: "{colors.ink}"
    rounded: "{rounded.card}"
    padding: "16px 24px"
  card-login:
    backgroundColor: "{colors.paper-2}"
    textColor: "{colors.ink}"
    rounded: "{rounded.surface}"
    padding: "32px"
  table-header:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink-2}"
    typography: "{typography.label}"
    padding: "8px 16px"
  delta-pill-up:
    backgroundColor: "rgb(0 166 147 / 12%)"
    textColor: "{colors.green-dk}"
    rounded: "{rounded.pill}"
    padding: "2px 6px"
  delta-pill-down:
    backgroundColor: "rgb(176 36 58 / 12%)"
    textColor: "{colors.error}"
    rounded: "{rounded.pill}"
    padding: "2px 6px"
  delta-pill-flat:
    backgroundColor: "{colors.rule-hair}"
    textColor: "{colors.ink-2}"
    rounded: "{rounded.pill}"
    padding: "2px 6px"
  nav-tab:
    backgroundColor: "transparent"
    textColor: "{colors.ink-2}"
    typography: "{typography.body}"
    padding: "0 16px"
    height: "48px"
  nav-tab-active:
    textColor: "{colors.ink}"
  nav-tab-active-admin:
    textColor: "{colors.amber}"
---

# Design System: statnive-live

## 1. Overview

**Creative North Star: "The Summit Instrument"**

The panel reads as a calibrated instrument an experienced operator would mount in daylight: warm off-white paper, navy ink at near-black contrast, a single sliver of Persian-Teal that identifies live state and primary action. The peak/summit logo (a mountain with a teal apex dot) is the only place the brand metaphor surfaces decoratively; everywhere else the surface earns its space by carrying real numbers in honest typography. Density sits between Stripe Dashboard and Plausible: more data per row than Plausible permits, less analyst-grade builder UI than Mixpanel demands. Spacing is generous around section heads, tight inside tables, and never uniform — the rhythm tells the customer-viewer where to look.

What this system rejects, on sight: SRE-dark with neon (Datadog, Grafana — the first-order observability training-data reflex), purple kitchen-sink with floating AI mascot (PostHog), marketing-cute green over sparse charts (Plausible / Fathom — the privacy-buddy register reads as undersized for a streaming-platform tenant), shocking gradients (Mixpanel / Amplitude), monochrome dark as the safe-second answer (Vercel / Linear), WordPress admin chrome (the sister `statnive/` plugin lives in wp-admin and must not visually leak in here). Per the impeccable absolute bans: no side-stripe borders, no gradient text, no decorative glassmorphism, no hero-metric template, no identical card grids, no modal-as-first-thought. The panel never says "self-hosted" or "privacy-first" in marketing voice; sovereignty shows up in the details (first-party WOFF2 fonts, no CDN, no telemetry pixel, version + commit-sha in the footer).

**Key Characteristics:**
- Light paper (`#FAFAF8`), navy ink (`#0A2540`), one teal accent (`#00A693`) reserved for live state, primary CTA, the wordmark `.live` suffix, and the direct-channel chip
- Sticky four-row chrome: TopBar (60px) → DateBar (44px) → Nav (48px) → FilterStrip (40px) → main
- Three-family type system: Space Grotesk display, DM Sans body, JetBrains Mono labels — never a fourth
- Flat by default; the only standing shadow is the Login card on full-bleed navy
- Channel chips are reserved-role and lock to the 17-step `internal/enrich/channel.go` mapper
- WCAG 2.2 AA floor on every text/background pair; v1.1-a11y slice extends to chart-mirror tables and motion-reduction
- No per-component hex values: `make brand-grep` rejects hand-rolled colors in PRs

## 2. Colors: The Daylight Summit Palette

A warm off-white paper carries near-black navy ink; one teal accent reserves itself for live state and primary action. Tonal layering — three near-white surfaces stacked with hairline borders — does the depth work that shadows would do elsewhere.

### Primary
- **Persian Teal** (`#00A693`): the only saturated colour the system spends. Live indicators (LivePulse, Realtime panel), primary CTA backgrounds, the wordmark `.live` suffix, the `chart-revenue` series, the direct-channel chip. ≤10% of any given screen by rule.
- **Persian Teal — Pressed** (`#007A6C`): hover and pressed state for primary actions and DeltaPill upward arrows.
- **Persian Teal — Tint** (`#B0D4CC`): info pills, demo banners, soft confirmation backgrounds.

### Secondary
- **Brand Navy** (`#0A2540`): primary text, primary chart series (`chart-visitors`), `chip-pill-active` background, the wordmark `statnive` (everything except the `.live` suffix).
- **Pressed Navy** (`#12304F`): muted text, secondary labels, table-header text, nav-tab inactive text.

### Tertiary
- **Admin Amber** (`#C47A0E`): the underline and active-tab text colour when the user is on the Admin tab. Carries no other role; its rarity is the affordance ("you are now in a higher-privilege mode").
- **Alert Red** (`#B0243A`): error states, DeltaPill downward, destructive chips. Always paired with an icon prefix; never the only carrier.
- **Cream** (`#EDE3D1`): on-navy text. Used on the Login screen's full-bleed navy backdrop and reserved for any future on-dark surface (e.g. demo overlay).

### Chart Series (reserved roles, do not reassign)
- `chart-visitors` `#0A2540` — navy. The visitors line/bar across every panel.
- `chart-revenue` `#00A693` — teal. The revenue line/bar in dual-bar visualizations.
- `chart-ochre` `#B87B1A`, `chart-plum` `#5F3B6E`, `chart-rust` `#A84628` — the three additional series used on Campaigns / Pages / Sources comparative rows. Picked for distinguishability under deuteranopia and protanopia, not for hue family.

### Channel Chips (reserved roles, lock to `internal/enrich/channel.go` 17-step mapper)
- `ch-direct` `#00A693`, `ch-search` `#1A73E8`, `ch-social` `#1A1A1A`, `ch-email` `#7A4A6E`, `ch-referral` `#8B7355`, `ch-ai` `#0A2540`, `ch-paid` `#8A5508`.
- A chip colour cannot change without the Go mapper changing too. CI gate (`make brand-grep`) plus the 17-step rule (PRODUCT.md Design Principle 4) enforce this.

### Neutral
- **Paper** (`#FAFAF8`): the page background, the table-header background, the DateBar background. The warmest white in the system.
- **Surface** (`#FFFFFF`): card and panel surfaces. Sits one tonal step above paper; the difference is what creates depth in the absence of shadow.
- **Rule Soft** (`#E8E5DC`): panel borders, datebar dividers, the strong horizontal rule under the Nav row.
- **Rule Hair** (`#EFECE4`): inner table-row dividers, chip default background, the sticky-thead under-line.

### Named Rules

**The One Teal Rule.** Persian Teal occupies ≤10% of any single screen. Its rarity is the affordance — it identifies "this is live" and "this is the primary action you can take." If three teal elements are visible at once, one of them is decorative and must be removed.

**The Channel-Chip Lock.** Channel chip colours are wired to `internal/enrich/channel.go`. A chip recolour requires the Go mapper to recolour. Visual changes follow data-shape changes, never the other way around.

**The Amber-Means-Admin Rule.** Amber `#C47A0E` appears only when the active route is Admin. It tells the user they are in a higher-privilege mode without copy. Forbidden anywhere else.

**The Daylight Rule.** No dark-mode toggle, no auto-switching to dark at night. The panel is for a customer-viewer in a daylight room; the operator's terminal-side surfaces (Health, audit log) live in their actual terminal, not in a colour-flipped dashboard.

## 3. Typography

**Display Font:** Space Grotesk 500 (with `system-ui, sans-serif` fallback)
**Body Font:** DM Sans 400 + 500 (with `IBM Plex Sans, system-ui, sans-serif` fallback)
**Label / Mono Font:** JetBrains Mono 400 + 500 (with `IBM Plex Mono, ui-monospace, monospace` fallback)

**Character.** Space Grotesk's slightly geometric, slightly humanist rhythm carries the wordmark and KPI numbers without feeling editorial. DM Sans handles tables and copy with neutral warmth — a customer-viewer should never read a row and think "I am inside a developer tool." JetBrains Mono is reserved entirely for system labels in tracked uppercase: column headers, KPI captions, role chips, version strings, the SOON pill, the DateBar's Tehran-timezone badge. The pairing reads "instrumented but warm," not "terminal app."

All three families are bundled WOFF2 subsets under `/app/assets/*.woff2` via `@fontsource/*` (SIL OFL 1.1). No CDN, no `data:` inline. The font budget sits outside the 14 KB initial-JS / 5 KB CSS gate; subsetting + unicode-range keeps each face under ~30 KB on the wire.

### Hierarchy

- **Wordmark** (Space Grotesk 500, 22px in TopBar / 28px on Login, `letter-spacing: -0.02em`): renders as `statnive` in ink + `.live` in teal. The only place teal meets the wordmark.
- **Headline (KPI Primary)** (Space Grotesk 500, 34px / 1.0 / `-0.02em` / `tabular-nums`): the four primary KPI tiles on Overview (Visitors, Conversion %, Revenue, RPV).
- **Title (h2)** (Space Grotesk 500, 20px / 1.2 / `-0.01em`): section headings inside panels.
- **KPI Secondary** (Space Grotesk 500, 22px / 1.0 / `tabular-nums`): demoted KPIs under the primary tier (Pageviews, Goals).
- **Body** (DM Sans 400, 13px / 1.45): table cells, form values, demo banner copy, error sentences. Cap line length at 65–75ch on prose surfaces.
- **Label** (JetBrains Mono 500, 10px UPPERCASE, `letter-spacing: 0.08em`): table column headers, KPI tile labels, filter labels, login form labels, the user-chip role suffix.
- **Micro Pill** (JetBrains Mono 500, 9px UPPERCASE, `letter-spacing: 0.08em`): the SOON pill on unimplemented Nav tabs.
- **Footer Brand Stamp** (JetBrains Mono 400, ~12px): the version + commit-sha in the footer. Sovereignty stamp.

### Named Rules

**The Three-Family Lock.** Space Grotesk (display), DM Sans (body), JetBrains Mono (label). A fourth family is forbidden until a v1.1-rtl Persian face (Vazirmatn or Estedad WOFF2 subset) is added — and it counts as a Persian-language alternate, not a fourth display family.

**The Mono-Means-System Rule.** JetBrains Mono only renders for system-authored labels and metadata: column headers, KPI captions, version stamps, SOON pills, role chips. User-authored content (site names, page paths, source URLs) is never in mono. Mono in body copy reads as "this is system speech," not "this is data."

**The Tabular-Nums Rule.** Every numeric cell, KPI value, dual-bar value, DeltaPill value, and table count uses `font-variant-numeric: tabular-nums`. Numbers must be vertically alignable when stacked. No exceptions.

**The Plain-Sentence Error Rule.** Errors are full sentences in DM Sans 400 + Alert Red, never mono. Errors explain *why* and *how to fix* (doc 35 R2). Forbidden: "Error 500. See logs." Required: "Couldn't load Sources for the last 7 days. ClickHouse is reachable but returned no rows for site 802 — check the date range or confirm the tracker is firing on the property."

## 4. Elevation

**Flat by default.** The panel is overwhelmingly flat: depth comes from tonal layering of three near-white surfaces (paper `#FAFAF8` → surface `#FFFFFF` → cards on surface) divided by hairline 1px borders (`rule-soft #E8E5DC` for panel boundaries, `rule-hair #EFECE4` for table rows). The only standing shadow in the system is on the Login card, where it sits over a full-bleed navy backdrop and earns the lift.

### Shadow Vocabulary

- **Login Card** (`box-shadow: 0 1px 2px rgb(0 0 0 / 20%), 0 12px 48px rgb(0 0 0 / 28%)`): a two-layer ambient + cast that lifts the white card off the navy login backdrop. Because the backdrop is `#0A2540` not paper, the card needs the lift to read.
- **Sticky-row hairline** (`border-bottom: 1px solid var(--rule-soft)`): the four sticky chrome rows (TopBar / DateBar / Nav / FilterStrip) each end in a single 1px rule. No shadow under the sticky band; the rule does the work.

### Named Rules

**The Flat-Until-Earned Rule.** Shadows appear only when an element is over a non-paper surface (e.g. Login card on navy). On any paper or surface background, the answer is a hairline border, not a shadow. Forbidden: drop shadows on cards over paper, hover-lift shadows on rows, glassmorphic blur on any sticky chrome.

**The No-Glassmorphism Rule.** `backdrop-filter: blur()` is forbidden on any standing surface. The sticky chrome is opaque `paper-2` with a hairline rule beneath; readability survives high-contrast accessibility settings only because there is no blur layer.

**The Hover-Tone Rule.** Hover state is communicated by tonal shift, not lift. Table rows hover to `rule-hair` background. Nav tabs hover to ink text + a soft border-bottom. Logout button hovers to ink text + ink border. No `transform: translateY()` on hover, no shadow grow.

## 5. Components

Every component below is wired to `var(--*)` tokens — hand-rolled hex in a Preact component is rejected by `make brand-grep`. The token vocabulary in `web/src/tokens.css:19-73` is the single source of truth.

### Primary Button
- **Shape:** 8px radius (`{rounded.card}`), 10px × 20px padding.
- **Default:** Persian Teal background (`#00A693`), surface text (`#FFFFFF`), DM Sans 500 / 13px.
- **Hover:** Persian Teal Pressed (`#007A6C`). Tonal shift only, no scale or shadow.
- **Focus:** ink outline `outline: 2px solid var(--ink); outline-offset: 2px`. Visible focus ring is non-negotiable.

### Ghost Button (Logout, secondary actions)
- **Shape:** 4px radius, 4px × 8px padding, 1px `rule-soft` border.
- **Default:** transparent background, `ink-2` text, 12px.
- **Hover:** ink text + ink border. No fill.

### Channel Chip
- **Style:** mono 10px UPPERCASE on `rule-hair` background, 999px pill, 2px × 8px padding, 1px transparent border.
- **Variants:** colour by channel role (`ch-direct` teal, `ch-search` blue, `ch-social` near-black, `ch-email` plum, `ch-referral` taupe, `ch-ai` navy, `ch-paid` ochre). Background stays `rule-hair`; the colour applies to text + a 1px coloured border, not the fill.
- **Forbidden:** chip recoloured for decorative reasons. Channel colour locks to the Go mapper.

### Filter / DatePicker Pill (selectable)
- **Style:** DM Sans 500 / 12px on transparent in a `rule-soft`-bordered pill group, 4px × 16px padding.
- **Active:** ink fill (`#0A2540`), surface text (`#FFFFFF`). The ink-on-paper inversion is the loudest selection cue in the system.
- **Hover:** ink text only, no background shift. Active state is the strong signal.

### KPI Card (Overview, primary tier)
- **Shape:** 8px radius, 1px `rule-soft` border, surface fill.
- **Padding:** 16px × 24px on primary tier; 8px × 16px on secondary tier (visible demotion through density).
- **Internal:** Mono label (10px UPPERCASE) on first row, optional DeltaPill on the right of the same row, then the Display 34px value with `tabular-nums`. No icon, no badge, no decoration.

### Table
- **Shape:** 8px radius, 1px `rule-soft` border, surface fill, `overflow: hidden` so corners hold under sticky-thead.
- **Header:** sticky, paper background, mono 10px UPPERCASE label text (`ink-2`), 1px `rule-soft` underline.
- **Rows:** DM Sans 13px on surface, 1px `rule-hair` divider between rows, 8px × 16px cell padding, `font-variant-numeric: tabular-nums` on every cell, first-column rendered in DM Sans 500 ink (the row identifier).
- **Hover:** `rule-hair` background, 80ms ease transition.
- **Performance:** `content-visibility: auto; contain-intrinsic-size: 0 42px` on `tbody tr` — long lists skip offscreen layout.

### DualBar (Sources / Pages / Campaigns rows)
- **Layout:** two stacked horizontal bars (Visitors navy, Revenue teal), 6px tall on `rule-hair` track, 3px radius on fills, mono 11px value to the right (`tabular-nums`).
- **Forbidden:** stacked single bar. Small-but-valuable revenue sources must stay legible (PRODUCT.md Anti-references).

### Nav Tab (sticky Nav row)
- **Style:** 48px row height, DM Sans 500 / 13px, `ink-2` default colour, 2px transparent bottom border that rises -1px to overlap the row's `rule-soft` divider on active.
- **Active:** ink text, teal underline (`green` border-bottom).
- **Active Admin:** amber text, amber underline. The route owns the colour — Admin's sub-tab row also picks up an amber accent.
- **SOON variant:** opacity 0.55, no border on hover, paired with a SOON mono pill (9px UPPERCASE on `rule-hair`). No route, no click handler.

### DeltaPill
- **Shape:** 999px pill, 2px × 6px padding, mono 11px, `tabular-nums`.
- **Up:** `green-dk` text on `rgb(0 166 147 / 12%)`. Arrow `↑`.
- **Down:** `error` text on `rgb(176 36 58 / 12%)`. Arrow `↓`.
- **Flat:** `ink-2` text on `rule-hair`. Arrow `→`.
- **Behaviour:** ±1% deadband — changes inside ±1% render as Flat, never as Up or Down. The pill never lies about a rounding-error change.

### LivePulse
- **Shape:** 8px Persian Teal dot, inline-block, 2s pulse via `box-shadow` ring expansion (`cubic-bezier(0.4, 0, 0.6, 1)`).
- **Reduced-motion:** animation disabled under `prefers-reduced-motion: reduce`.
- **Reserved use:** Realtime panel + Nav-row Realtime tab. Forbidden anywhere else; the dot is the system's only animation.

### User Chip + Role Pill (TopBar)
- **User name:** DM Sans 500 / 12px in ink, on `rule-hair` 999px pill, 4px × 8px padding.
- **Role suffix:** mono 10px UPPERCASE in `ink-2`, separated by a 1px `rule-soft` left rule. Renders the literal role: `admin`, `viewer`, `api`. The viewer who doesn't see the Admin tab understands why because the chip says `viewer`.

### Footer (sovereignty stamp)
- **Style:** centered, 12px, `ink-muted` text on `rule-soft` top border. JetBrains Mono carries the brand stamp + version + commit-sha; DM Sans carries the licence-attribution sentence (CC-BY-SA-4.0 IP2Location LITE — load-bearing licence requirement).
- **Don't:** add marketing CTA, "made with love," social icons, or third-party badges. The footer is a sovereignty stamp, not a sales surface.

### Login Surface (signature, customer-viewer first impression)
- **Backdrop:** full-bleed brand navy (`#0A2540`) with cream text — the only on-dark surface in the system.
- **Card:** 12px radius, surface fill, two-layer shadow (`0 1px 2px rgb(0 0 0 / 20%), 0 12px 48px rgb(0 0 0 / 28%)`), 32px padding, fixed `min(400px, 100%)` width.
- **Wordmark:** Space Grotesk 500 / 28px, ink, `.live` suffix in teal.
- **Demo banner (legacy violation, see Don'ts):** the existing `border-left: 3px solid var(--green)` is an absolute-ban side-stripe. The brand-coherent rewrite is a teal tint background (`rgb(0 166 147 / 10%)`) with a leading `▸` glyph in teal-pressed and zero left border. Track as a polish slice.

## 6. Do's and Don'ts

### Do:
- **Do** route every colour, font, spacing, and radius through `var(--*)` in `tokens.css`. `make brand-grep` rejects hand-rolled hex.
- **Do** keep Persian Teal `#00A693` to ≤10% of any screen. Reserve it for primary CTA, LivePulse, the wordmark `.live`, the `chart-revenue` series, and the `ch-direct` chip.
- **Do** lock channel chip colours to the 17-step `internal/enrich/channel.go` mapper. A chip recolour requires the Go mapper to recolour.
- **Do** use Amber `#C47A0E` only when the user is on Admin. It is the affordance for "you are in a higher-privilege mode."
- **Do** apply `font-variant-numeric: tabular-nums` to every numeric cell, KPI value, DeltaPill, and table count. Numbers must vertically align.
- **Do** carry the ±1% DeltaPill deadband. Render small changes as Flat; never as Up or Down.
- **Do** render unimplemented panels as `SOON` pills. Never as faux charts with synthetic data.
- **Do** use the dual-bar Visitors/Revenue idiom on Sources / Pages / Campaigns. Two adjacent bars per row.
- **Do** lift the Login card with the existing two-layer shadow over the full-bleed navy backdrop. That is the system's only standing shadow.
- **Do** carry full sentences in errors that explain *why* and *how to fix*. DM Sans 400, Alert Red, never mono.
- **Do** pair every error / success / chart series with an icon prefix. Colour is never the only carrier.
- **Do** honour `prefers-reduced-motion: reduce` on every transition. New motion lands disabled-under-reduced-motion by default.
- **Do** keep all three font families bundled as first-party WOFF2 subsets under `/app/assets/`. CDN imports are forbidden by air-gap invariant.
- **Do** keep the version + commit-sha in the footer. Sovereignty shows up in the details, not the copy.

### Don't:
- **Don't** use `border-left` or `border-right` greater than 1px as a coloured accent on any card, list item, callout, or alert. (The existing `Login.css` demo-banner side-stripe is a known violation; rewrite to background tint + leading glyph.) Side-stripe borders are an impeccable absolute ban.
- **Don't** use gradient text (`background-clip: text` over a gradient). Forbidden anywhere on the panel.
- **Don't** use glassmorphism (`backdrop-filter: blur()`) on standing surfaces. Forbidden on the sticky chrome, KPI cards, modals.
- **Don't** ship the hero-metric template (one giant number + small label + supporting stats + gradient accent). It is the SaaS cliché the panel rejects by construction.
- **Don't** ship identical card grids. The KPI tier on Overview is intentionally two grids of different densities (primary tier 220px-min, secondary tier 160px-min). Sameness is monotony.
- **Don't** reach for a modal as the first thought. Inline / progressive surfaces first; modal only when the action genuinely requires modal context.
- **Don't** introduce a fourth font family. Three is the lock. (v1.1-rtl Persian Vazirmatn / Estedad is a locale alternate, not a fourth display family.)
- **Don't** use mono in user-authored content (site names, page paths, source URLs). Mono is system speech only.
- **Don't** read like Datadog / Grafana / New Relic. SRE-dark + neon is the first-order observability reflex; the panel is a customer-facing analytics product, not an oncall console.
- **Don't** read like PostHog. No purple kitchen-sink, no AI-mascot floating chatbot, no session-replay sidebar. The frustration-signals Health panel is the deliberate substitute.
- **Don't** read like Plausible / Fathom / Simple Analytics. Marketing-cute green over sparse charts under-sizes the product for a streaming-platform tenant.
- **Don't** read like Mixpanel / Amplitude. No shocking gradients, no funnel-builder UI as primary navigation, no events-analyst vocabulary.
- **Don't** read like Vercel / Linear monochrome dark. The "we're greyscale because we're not Datadog" trap is the second-order observability reflex.
- **Don't** read like WordPress admin. No `.notice`, no `.button-primary`, no Dashicons, no `wp-admin` chrome creep. The sister `statnive/` plugin lives there; statnive-live does not.
- **Don't** ship dark mode. The panel is for a customer-viewer in a daylight room. (Operator surfaces live in the operator's actual terminal.)
- **Don't** ship session-replay surfaces, autocapture-by-default UIs, AI-mascot chatbot, or remote agentic install wizards. All four are banned by privacy posture and PRODUCT.md.
- **Don't** add a third-party telemetry pixel, analytics tag, intercom widget, or marketing iframe to the panel itself. The panel never phones home.
- **Don't** use celebratory animation on routine state. Summit moments (PRODUCT.md Design Principle 6) are rare, brand-coherent, customer-only, and never more than one per session.
- **Don't** write copy with em dashes or `--`. Use commas, colons, semicolons, periods, parentheses.
- **Don't** redefine a token value in a Preact component. Every colour, font, spacing, and radius routes through `var(--*)`. PR review rejects regressions.
