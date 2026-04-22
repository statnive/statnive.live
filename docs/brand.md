# Brand & Design — statnive.live Visual Identity

> Referenced from [PLAN.md](../PLAN.md). Brand specs have no direct influence on code tasks — this file is the source of truth when Phase 5 authors `web/src/tokens.css` or when Phase 9/11 marketing copy goes out.

The canonical brand reference is the standalone HTML preview at [`../jaan-to/outputs/detect/design/statnive-brand-guideline/statnive-plugin-brand-guidelines.html`](../../jaan-to/outputs/detect/design/statnive-brand-guideline/statnive-plugin-brand-guidelines.html) (open in a browser to see the wordmark, summit logo, color swatches, type ramp, component samples, and pattern library). The plugin guideline replaced the earlier `statnive-live-brand-guidelines.html` as the dashboard source of truth in Phase 5d — brighter teal + warmer cream + iOS-standard error palette; the older companion is retained for historical reference. Other companions in the same directory: `Statnive Logo.html` (mark variants), `icons.jsx` (SVG icon set), `design-canvas.jsx` (source for the preview).

## Where the brand applies

Every public surface that carries the statnive.live name:

- **statnive.live root** — marketing landing page (Phase 9 + Phase 11). Hero uses the summit mark + Persian Teal accent on cream; CTAs in serif.
- **demo.statnive.live** — public demo dashboard (Phase 9). Same surface as tenant dashboards but with the "Public demo" banner from Phase A.
- **app.statnive.live/s/&lt;slug&gt;** — tenant dashboards (Phase 11). The Preact SPA from Phase 5 owns the implementation; brand tokens shipped as CSS custom properties so per-tenant white-label (a v2 upsell) is a token swap, not a refactor.
- **SamplePlatform.statnive.live** — SamplePlatform dedicated dashboard (Phase 10). Same brand by default; SamplePlatform can request white-label co-branding under v2.
- **README + docs site** — when we eventually publish operator docs, they use the same palette + Fraunces wordmark.

## Tokens to ship as CSS custom properties

Copy verbatim into `web/src/tokens.css` when Phase 5 starts:

```css
#statnive-app {
  /* Surface — warm cream + near-black ink */
  --paper:     #EDE3D1;   /* primary background — warm cream */
  --ink:       #1A1A1A;   /* primary text + rules */
  --rule-soft: #E8E5DC;   /* dividers, table borders */

  /* Accent — Brand Teal (plugin guideline). Brighter + more saturated
     than the legacy Persian Teal so the accent reads as primary CTA. */
  --green:     #00A693;
  --green-dk:  #007A6C;   /* hover / pressed */
  --green-lt:  #B0D4CC;   /* tinted backgrounds, info pills */

  /* Semantic — iOS-standard error palette for banners + destructive chips. */
  --error:     #FF3B30;
  --error-dk:  #B0243A;

  /* Secondary palette — chart series, status badges */
  --navy:      #1E3551;
  --ochre:     #B87B1A;
  --plum:      #5F3B6E;
  --rust:      #A84628;

  /* Type — DM Sans / JetBrains Mono preferred with IBM Plex fallback
     so the air-gapped bundle stays WOFF2-free today; shipping the DM
     Sans files is a follow-up slice. */
  --serif: 'Fraunces', 'Charter', Georgia, serif;
  --sans:  'DM Sans', 'IBM Plex Sans', system-ui, sans-serif;
  --mono:  'JetBrains Mono', 'IBM Plex Mono', ui-monospace, monospace;
}
```

## Typography rules

From the guideline's "Type Ramp" panel:

- **Fraunces (serif)** — wordmark, marketing headlines, dashboard panel titles. Italic for the `.live` suffix in the wordmark.
- **IBM Plex Sans** — UI body, table cells, form controls.
- **IBM Plex Mono** — numeric telemetry (visitor counts, RPV figures, latency p99), code samples, IDs, hashes. Mono is load-bearing for the "fast, smart" product positioning — numbers must align in tables.

## Logo + voice rules

From the guideline's "Three tones do ninety percent of the work" thesis:

- The summit mark (the angular peak with the Persian Teal apex dot) is **secondary** to the wordmark. Wordmark first; summit mark only as a favicon, app-tile icon, or condensed-space mark.
- Cream + ink + rule do most of the work. Persian Teal is the **only** accent; secondary palette colors (navy / ochre / plum / rust) are reserved for chart series, status badges, and category differentiation — never for primary UI chrome.
- Voice from the guideline blurb: terse, technical, confident. "Cream, ink, rule." not "Our brand uses three primary colors."

## Compliance hooks

Where this plan enforces brand consistency:

- **Phase 5 (Dashboard Frontend)** — `web/src/tokens.css` MUST originate from the swatch table above. PR review rejects hand-rolled hex values in Preact components; they must reference `var(--green)` etc.
- **Phase 9 (Phase A dogfood)** — when the tracker snippet is pasted into `statnive-website/`, the surrounding marketing copy + hero use the brand palette + Fraunces wordmark.
- **Phase 11 (Phase C SaaS)** — the per-tenant slug page reuses the same token file; per-tenant white-label is a v2 feature that swaps token values, never the structure.

## Source of truth rule

If the brand guideline HTML and this file disagree, the HTML wins. Update the HTML first, then port the relevant token deltas back into this file.
