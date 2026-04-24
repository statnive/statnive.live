# Third-Party Licenses

statnive-live ships first-party data + code only — no remote CDN, no
runtime outbound required (CLAUDE.md § Isolation). Every third-party
asset included in the binary or served from `/app/*` carries the
attribution required by its license below. Attribution surfaces are
listed per asset so PR review can verify each delivery channel.

## Dashboard fonts (WOFF2, served from /app/assets/)

The dashboard SPA bundles three typefaces. All three are SIL Open Font
License 1.1 (the `@fontsource/*` npm packages redistribute the upstream
Google Fonts artifacts). Subsets are Latin + Latin Extended; CSS
`@font-face` `unicode-range` controls which subset each glyph falls
into so the browser only downloads what's rendered.

### DM Sans

- **License:** SIL Open Font License 1.1 (OFL-1.1)
- **Copyright:** 2014 Colophon Foundry, 2014–2017 Google Fonts, 2019 Indian Type Foundry
- **Source:** `node_modules/@fontsource/dm-sans/LICENSE`
- **Delivery:** `/app/assets/dm-sans-*.woff2` (Vite-hashed, long-cached)
- **Attribution surface:** this file; CSS `@font-face { font-family: 'DM Sans' }` declaration in `/app/assets/*.css`

### Space Grotesk

- **License:** SIL Open Font License 1.1 (OFL-1.1)
- **Copyright:** 2020 The Space Grotesk Project Authors (https://github.com/floriankarsten/space-grotesk)
- **Source:** `node_modules/@fontsource/space-grotesk/LICENSE`
- **Delivery:** `/app/assets/space-grotesk-*.woff2`
- **Attribution surface:** this file; CSS `@font-face { font-family: 'Space Grotesk' }` declaration in `/app/assets/*.css`

### JetBrains Mono

- **License:** SIL Open Font License 1.1 (OFL-1.1)
- **Copyright:** 2020 The JetBrains Mono Project Authors (https://github.com/JetBrains/JetBrainsMono)
- **Source:** `node_modules/@fontsource/jetbrains-mono/LICENSE`
- **Delivery:** `/app/assets/jetbrains-mono-*.woff2`
- **Attribution surface:** this file; CSS `@font-face { font-family: 'JetBrains Mono' }` declaration in `/app/assets/*.css`

**OFL-1.1 compliance note.** OFL §1 permits use, study, modification,
and redistribution including embedding in a larger document. OFL §4
requires the complete unaltered license to accompany any redistribution
of the font itself. The upstream `LICENSE` text ships inside each
`@fontsource/*` package under `node_modules/` and is included in the
dependency audit trail; it is not re-embedded in the compiled Go
binary because OFL §5 "Embedded in a Document" excepts derivative
documents (our compiled SPA) from §4's verbatim-reshipping clause.

## IP2Location LITE (GeoIP, optional, not bundled)

When operators drop an IP2Location LITE `.BIN` file into `config/geoip/`
and set `enrichment.geoip.db = ip2location_lite` in `config.yaml`, this
attribution MUST appear in every delivery surface below (CC-BY-SA-4.0
§3(a)(1), CLAUDE.md § License Rules):

> This site or product includes IP2Location LITE data available from
> https://lite.ip2location.com.

- **Delivery 1 (this file):** rendered above.
- **Delivery 2 (`/about` JSON endpoint):** returned as the `ip2location_lite_attribution` field.
- **Delivery 3 (dashboard footer):** rendered when `enrichment.geoip.db = ip2location_lite`.

Paid DB23 Site License (Phase 10 SamplePlatform cutover) waives the
three-surface requirement per IP2Location's commercial terms; until then
LITE is default and all three surfaces are CI-enforced via the
`geoip-attribution-string-present` Semgrep rule (see
`.claude/skills/geoip-pipeline-review/references/attribution.md`).

## Binary-linked Go dependencies

Full per-dependency manifest is produced by `go-licenses` at release time
and shipped in `releases/<version>/licenses.json`. Only MIT / Apache-2.0
/ BSD-2/3-Clause / ISC licensed code is linked (CLAUDE.md § License
Rules). AGPL / CC-BY-SA modules are rejected at CI time by
`go-licenses report` in `make licenses`.
