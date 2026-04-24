# Brand & Design — statnive.live Visual Identity

> Referenced from [PLAN.md](../PLAN.md). Brand specs have no direct influence on code tasks — this file is the source of truth when `web/src/tokens.css` changes, or when Phase 9/11 marketing copy goes out.

The canonical brand reference is the standalone HTML preview at [`../jaan-to/outputs/detect/design/statnive-brand-guideline/statnive-live.html`](../../jaan-to/outputs/detect/design/statnive-brand-guideline/statnive-live.html) (open in a browser to see the wordmark, color swatches, type ramp, component samples, KPI-tile, dual-bar, DeltaPill, LivePulse, SOON-tab, and sticky chrome). This operator-console variant replaced the earlier `statnive-plugin-brand-guidelines.html` as the dashboard source of truth in Phase 5e — the new direction: brand-navy ink on near-white paper surfaces, teal stays primary CTA, amber underline for Admin, channel-colored chips. The older companions are retained for historical reference.

## Where the brand applies

Every public surface that carries the statnive.live name:

- **statnive.live root** — marketing landing page (Phase 9 + Phase 11). Hero uses the wordmark + teal accent on paper; CTAs in Space Grotesk display face.
- **demo.statnive.live** — public demo dashboard (Phase 9). Same surface as tenant dashboards but with the "Public demo" banner from Phase A.
- **app.statnive.live/s/&lt;slug&gt;** — tenant dashboards (Phase 11). The Preact SPA from Phase 5 owns the implementation; brand tokens ship as CSS custom properties so per-tenant white-label (a v2 upsell) is a token swap, not a refactor.
- **SamplePlatform.statnive.live** — SamplePlatform dedicated dashboard (Phase 10). Same brand by default; SamplePlatform can request white-label co-branding under v2.
- **README + docs site** — when we eventually publish operator docs, they use the same palette + display face wordmark.

## Tokens to ship as CSS custom properties

Shipped in `web/src/tokens.css` (Phase 5e). Copy verbatim when cloning the palette elsewhere:

```css
#statnive-app {
  /* Surface — bright paper + white panels */
  --paper:     #FAFAF8;   /* primary page background */
  --paper-2:   #FFFFFF;   /* panel / card surfaces */
  --ink:       #0A2540;   /* primary text + rules — brand navy */
  --ink-2:     #12304F;   /* pressed-state ink, secondary text */
  --cream:     #EDE3D1;   /* on-navy text only (demoted to accent) */
  --rule-soft: #E8E5DC;   /* panel borders, strong dividers */
  --rule-hair: #EFECE4;   /* inner hairlines (table rows) */

  /* Accent — Brand Teal (unchanged from 5d). Primary CTA + live indicators. */
  --green:     #00A693;
  --green-dk:  #007A6C;
  --green-lt:  #B0D4CC;

  /* Semantic — alert palette for error banners + destructive chips. */
  --error:     #B0243A;
  --error-dk:  #8A1D2E;

  /* Admin accent — amber underline when Admin tab is active. */
  --amber:     #C47A0E;

  /* Chart series — visitors (navy) + revenue (green) lead. */
  --chart-visitors: #0A2540;
  --chart-revenue:  #00A693;
  --chart-ochre:    #B87B1A;
  --chart-plum:     #5F3B6E;
  --chart-rust:     #A84628;

  /* Channel palette — chips + source-table chip colors. Mirrors the
     17-step channel mapper in internal/enrich/channel.go. */
  --ch-direct:   #00A693;
  --ch-search:   #1A73E8;
  --ch-social:   #1A1A1A;
  --ch-email:    #7A4A6E;
  --ch-referral: #8B7355;
  --ch-ai:       #0A2540;
  --ch-paid:     #8A5508;

  /* Type — bundled WOFF2 subsets (fonts.css). system-ui is the
     font-display: swap fallback during first-paint. */
  --display: 'Space Grotesk', system-ui, sans-serif;
  --sans:    'DM Sans', 'IBM Plex Sans', system-ui, sans-serif;
  --mono:    'JetBrains Mono', 'IBM Plex Mono', ui-monospace, monospace;
  --serif:   'Fraunces', 'Charter', Georgia, serif;
}
```

## Typography rules

Three bundled faces, all SIL OFL 1.1, shipped as WOFF2 subsets (latin + latin-extended) under `/app/assets/*.woff2`. Attribution lives in [`LICENSE-third-party.md`](../LICENSE-third-party.md). Details:

- **Space Grotesk 500 (display)** — wordmark, KPI display numbers, panel `<h2>` titles. Letter-spacing tight, optical weight lands between traditional display faces and grotesques. Used when "this is a number that matters" reads faster than neutral body.
- **DM Sans 400 + 500 (body)** — UI body, table cells, form controls, chips, links. Friendly but technical; pairs with Space Grotesk without competing.
- **JetBrains Mono 400 + 500 (mono)** — tabular numbers, chip labels, timezone chip, DeltaPill percentages, tracker-snippet code blocks, tiny uppercase labels. Mono is load-bearing for the "fast, smart" product positioning — numbers must align in tables.
- **Fraunces (serif)** — reserved for marketing surfaces + legacy wordmark; not used in the operator dashboard.

## Logo + voice rules

- The wordmark is "statnive" with ".live" in teal (same family, no italic in the 5e variant).
- Paper + ink + rule do most of the work. Teal is the primary accent; amber is Admin-only; the secondary palette (chart series navy / ochre / plum / rust) and channel palette (direct / search / social / email / referral / ai / paid) are reserved for chart series, status badges, and category differentiation — never for primary UI chrome.
- Voice: terse, technical, confident. "Paper, ink, rule." not "Our brand uses three primary colors."

## Compliance hooks

Where this plan enforces brand consistency:

- **Phase 5 (Dashboard Frontend)** — `web/src/tokens.css` MUST originate from the swatch table above. PR review rejects hand-rolled hex values in Preact components; they must reference `var(--green)` etc.
- **Phase 5e** — `web/src/fonts.css` MUST bundle fonts via `@fontsource/*` npm packages (OFL 1.1). No external CDN URL; Vite `assetsInlineLimit: 0` forces WOFF2 to ship as separate assets (CSP `font-src 'self'` rejects `data:` URIs).
- **Phase 9 (Phase A dogfood)** — when the tracker snippet is pasted into `statnive-website/`, the surrounding marketing copy + hero use the brand palette + display wordmark.
- **Phase 11 (Phase C SaaS)** — the per-tenant slug page reuses the same token file; per-tenant white-label is a v2 feature that swaps token values, never the structure.

## Source of truth rule

If the brand guideline HTML and this file disagree, the HTML wins. Update the HTML first, then port the relevant token deltas back into this file.
