# Design brief — statnive-live mobile app UI (M6 / Phase 12)

> **How to use this doc.** Paste the body of § "Prompt to hand to Claude Code" below into a fresh Claude Code session at the repo root. The design agent (or `jaan-to:frontend-design` / `ui-ux-pro-max` skill) will produce a screen inventory, design-token JSON, component library spec, and reference React Native + Skia code for each screen. Iterate by re-running with focused follow-ups (e.g., "now produce the empty / loading / error states for ServerListScreen").
>
> **Authoritative references** — do not let the design drift from these:
> - [`statnive-live/PLAN.md` § Phase 12](../PLAN.md) — week-by-week deliverables, performance gates, security gates
> - [`jaan-to/docs/research/37-mobile-app-rn-architecture-perf-security.md`](../../jaan-to/docs/research/37-mobile-app-rn-architecture-perf-security.md) — canonical architecture / perf / security / motion spec
> - [`statnive-live/docs/brand.md`](brand.md) — wordmark, logo, color palette, type ramp, voice rules
> - [`jaan-to/docs/research/35-ui-ux-dashboard-evidence-synthesis.md`](../../jaan-to/docs/research/35-ui-ux-dashboard-evidence-synthesis.md) — UX gap analysis the web dashboard already uses; mobile inherits
> - [`statnive-live/packages/web/src/tokens.css`](../packages/web/) — Phase 5e palette + type tokens (when restructure lands; today at `web/src/tokens.css`)

---

## Prompt to hand to Claude Code

```
Design the complete native UI for the statnive-live mobile app (Milestone 6 / Phase 12). 
Treat this as a production design brief, not a sketch. Output should be implementable on 
React Native + Skia + Reanimated v4 within the constraints below.

================================================================================
PRODUCT CONTEXT
================================================================================
statnive-live is a privacy-first analytics platform — Go binary + ClickHouse — that 
operators run either on statnive.live SaaS or self-hosted (inside or outside Iran). 
The mobile app is the OPERATOR DASHBOARD CLIENT, like the Atlassian Jira mobile app: 
the same APK / IPA connects to any statnive-live server — SaaS, self-hosted Hetzner, 
self-hosted Iranian DC — by entering a server URL. Multi-server. No special builds.

The app is for site owners and analytics admins to monitor traffic, conversions, 
and goal completion on the go. It is READ-MOSTLY: there are some admin CRUD surfaces 
(users, sites, goals, flags) but 90% of usage is "open app, glance at numbers, 
close." Sessions are short. Numbers must be legible at a glance.

================================================================================
HARD CONSTRAINTS (non-negotiable — do not break these)
================================================================================
1. NATIVE PERFORMANCE — no WebView. React Native New Architecture (Fabric + Hermes + 
   JSI). All charts via @shopify/react-native-skia. All long lists via 
   @shopify/flash-list. All animations via react-native-reanimated v4 worklets on the 
   UI thread (never the JS thread, never the legacy Animated API).

2. PERF BUDGETS (release-blocking):
   - Cold launch <2.0s p95 mid-range Android (Pixel 5)
   - Warm start <1.0s
   - ≥55fps p90 sustained scroll
   - Chart first-frame <50ms
   - JS bundle <5MB gzipped, APK <30MB, IPA <50MB
   Every design decision must defend these. If a screen needs 12 simultaneous 
   animated KPIs, redesign — don't optimize.

3. AIR-GAP — bundle ships in IPA / APK. NO Capacitor Live Updates, NO CodePush, 
   NO Firebase, NO Sentry, NO Crashlytics, NO third-party analytics, NO push (v2). 
   Designs that require a remote feature flag service or remote config = rejected.

4. JIRA-STYLE MULTI-SERVER — first launch shows "Add server" (URL + label). User can 
   add multiple servers (statnive.live SaaS + self-hosted A + self-hosted B + 
   self-hosted Iran). Switch between them. Each server has its own session token + 
   TLS pin in OS-secure storage. The SAME UI works for SaaS and self-hosted — no 
   geo-detection, no special-case rendering for ".ir" hosts.

5. SECURITY UI HOOKS — biometric unlock per server (TouchID / FaceID / 
   BiometricPrompt). TLS pin trust-on-first-use prompt. Cert-rotation override 
   prompt. Background-mode lock screen ("Statnive — locked" placeholder, never the 
   real data). FLAG_SECURE on all dashboard views (blocks screenshots + recents 
   thumbnail). Force-update blocking screen if /api/about returns higher 
   min_app_version.

6. i18n — English (default), German, French. LTR ONLY. NO RTL, NO PERSIAN. German 
   has long compound words — design CTAs and KPI labels with overflow tolerance.

7. REDUCED-MOTION — every animation honors AccessibilityInfo.isReduceMotionEnabled(). 
   When on: springs become 200ms crossfades, all haptics suppressed, LivePulse 
   becomes a static dot.

================================================================================
DESIGN LANGUAGE (inherit from web Phase 5e — the dashboard already shipped this)
================================================================================
COLORS (canonical — copy verbatim into @statnive/core/tokens):
  --paper:       #FAFAF7   (near-white background)
  --ink:         #0E1B2C   (navy primary text)
  --ink-soft:    #4A586B   (secondary text)
  --teal:        #1F9AA0   (primary CTA, accent)
  --teal-deep:   #16777C   (CTA pressed)
  --amber:       #C2710C   (Admin-tab accent — mode-shift signal)
  --green:       #2D9B6E   (success, LivePulse, ↑ delta)
  --red:         #C44539   (error, ↓ delta, destructive)
  --warning:     #C29A0C   (warning, attention)
  --hairline:    #E4E5DE   (1px borders, dividers)

CHANNEL PALETTE (7 entries, used in Sources / Campaigns dual-bar charts):
  Organic Search    #1F9AA0
  Direct            #4A586B
  Referral          #7C5CD3
  Social            #C44539
  Email             #2D9B6E
  Paid              #C2710C
  Other             #98A0A8

TYPOGRAPHY:
  Display / Headers     Space Grotesk 500
  Body                  DM Sans 400 / 500
  Numbers / Mono        JetBrains Mono 400 / 500

  Sizes (mobile-tuned, larger than web for thumb-distance reading):
    xs    11pt
    sm    13pt
    base  15pt
    lg    18pt
    xl    24pt
    2xl   32pt   (KPI primary number)
    3xl   44pt   (full-bleed hero number)

  All fonts SHIPPED IN BUNDLE (SIL OFL 1.1) — never linked from a CDN.

ELEVATION — minimal. Mobile is flat-first:
  Card:     no shadow; 1px hairline border, 12px radius
  Sheet:    one drop-shadow when presented (rgba(14,27,44,0.08), blur 24, y 8)
  Modal:    same as sheet
  No Material Design depth ladder. No iOS bevels.

SPACING (4pt grid):
  4 / 8 / 12 / 16 / 24 / 32 / 48 / 64

ICONS — Lucide React Native ONLY (consistent with web). Two weights: 1.5px stroke 
(default), 2px stroke (selected tab). No emoji icons. No Material Icons.

================================================================================
MOTION + INTERACTION TOKENS
================================================================================
SPRING PRESETS (Reanimated v4, on UI thread):
  quick    { damping: 20, stiffness: 300 }   buttons, toggles, tab taps
  default  { damping: 15, stiffness: 150 }   page transitions, cards
  lazy     { damping: 20, mass: 1.2, stiffness: 80 }   sheets

DURATIONS (when spring is wrong — e.g. crossfade):
  instant  50ms     state toggle
  fast     150ms    button feedback
  normal   250ms    card expand, tab switch, DeltaPill flash
  slow     500ms    modal enter, sheet present, server-switch

HAPTIC HIERARCHY (used SPARINGLY — read-mostly app, not gameful):
  selection   server / site switcher item snap
  light       tab tap, panel switch
  medium      pull-to-refresh release, panel-action confirm
  warning     TLS-pin override approval, server-remove confirm
  error       API error, network lost
  
  No success haptic for normal actions. No achievement / streak haptics. No 
  haptic for scrolling.

TRANSITIONS:
  Server switch          slide horizontal, lazy spring, 350ms
  Tab switch             instant (no transition — native bottom-tab)
  Panel scroll → top     scroll-to-top on tab re-tap, native iOS pattern
  Pull-to-refresh        native UIRefreshControl / SwipeRefreshLayout
  Sheet present          lazy spring, scrim opacity 0→0.4 over 200ms

================================================================================
SCREEN INVENTORY (the full set — design every one)
================================================================================
ONBOARDING / SERVER MANAGEMENT
  1. WelcomeScreen          first-launch only — wordmark + 1 sentence + "Add server" CTA
  2. AddServerScreen        URL input + optional label + "Continue"
  3. ServerProbeScreen      transient — "Reaching your server…" + spinner; failure states
  4. TlsTrustScreen         "First time connecting" — show cert SHA256 + subject + issuer + Trust button
  5. TlsMismatchScreen      "Certificate has changed" — show old + new fingerprints; Approve / Cancel
  6. LoginScreen            email + password + "Enable biometric next time" toggle
  7. BiometricUnlockScreen  per-server unlock; FaceID / TouchID / fallback to passcode
  8. ServerListScreen       list of all configured servers; default at top; +Add; long-press menu
  9. ServerEditSheet        edit label + re-pin + remove (destructive)

DASHBOARD (the 90% surface)
 10. OverviewScreen         KPI grid (visitors, pageviews, sessions, bounce, RPV — see § "RPV first")
                            + visitors trend Skia chart (24h / 7d / 30d / 90d toggle)
                            + LivePulse "23 active visitors" row
 11. SourcesScreen          dual-bar list: visitor count + revenue per channel; FlashList
 12. PagesScreen            top pages: views + visitors + RPV; FlashList; tap → page detail
 13. SeoScreen              keywords + impressions + CTR + position (placeholder until GSC = v2)
 14. CampaignsScreen        UTM-grouped dual-bar (visitor + revenue); FlashList
 15. RealtimeScreen         live active visitors + last 30 events stream + pulse
 16. PageDetailScreen       per-path: trend chart + top referrers + device split
 17. SettingsScreen         locale (en/de/fr), biometric toggle, app version, force-update banner

ADMIN (role-gated — admin only)
 18. AdminUsersScreen       list + create + edit + disable (FlashList)
 19. AdminSitesScreen       list + create site + tracker snippet display + enable/disable
 20. AdminGoalsScreen       list + create + edit + disable
 21. AdminFlagsScreen       (M5 dependency) — list + create + edit
 22. AdminTokensScreen      (v1.1-tokens dependency) — API token rotation

SUPPORTING
 23. SiteSwitcherSheet      switch site within a server (existing web siteSignal pattern)
 24. ServerSwitcherSheet    switch server (across configured Jira-style accounts)
 25. FilterStrip            date range + comparison + custom range; sticky under header
 26. EmptyState             reusable: no events yet, no goals yet, no flags yet — illustration + CTA
 27. ErrorState             reusable: network error, 401, 403, 500, force-update
 28. LoadingState           reusable: skeleton (matching panel grid)
 29. NoticeToast            transient bottom toast (success / warning / error / status)
 30. AppLockedView          background-mode placeholder ("Statnive — locked" — see § Security)
 31. ForceUpdateScreen      blocking — "Update required" + store deep link
 32. AboutScreen            app version + legal + license attribution + privacy link

================================================================================
NAVIGATION SHAPE (React Navigation v7, native-stack)
================================================================================
Root stack:
  ServerList (initial)
   └── PerServer stack (after pick + biometric)
        ├── BottomTabs (Overview | Sources | Pages | Realtime | Admin)
        │    └── per-tab native-stack
        ├── ServerSwitcherSheet (modal)
        ├── SiteSwitcherSheet (modal)
        └── Settings (push)

  Modal stack overlay:
    AddServer / TlsTrust / TlsMismatch / Login / BiometricUnlock
    ForceUpdate (blocking — covers everything when triggered)

Bottom tabs ALWAYS visible on dashboard scope. Admin tab only renders for 
role===admin. Tab labels translate per locale.

================================================================================
RPV FIRST (philosophy — drives KPI hierarchy)
================================================================================
Per CLAUDE.md product philosophy #1: Revenue per Visitor (RPV) over total revenue. 
On Overview, lead the KPI grid with:

  PRIMARY    Today's visitors (3xl number) + ↕ delta vs yesterday
  SECONDARY  Today's RPV (xl)
  TERTIARY   Today's pageviews / sessions / goal completions

NOT bounce rate, NOT time-on-site, NOT pages-per-session (per philosophy #2 — 
reject vanity metrics). If the operator wants those, they tap into a panel.

Channel-grouping default: Sources panel always shows raw referrers grouped into 
the 7-channel taxonomy (philosophy #5). NEVER raw URL strings on first read.

Dual-bar visualization (philosophy #6): Sources / Pages / Campaigns show visitors 
AND revenue side-by-side, in the channel-palette colors above. Different colors 
for visitor bar vs revenue bar — never overlapping the same color.

================================================================================
ACCESSIBILITY (release-blocking)
================================================================================
- Every Skia chart ships an accessibilityLabel: "Visitors trend, last 30 days, 
  peak Wednesday 2,400 visitors." Screen readers get parity with sighted users.
- Every KpiTile: accessibilityLabel + accessibilityValue: 
  "1,234 visitors today, up 12 percent from yesterday."
- Every Tab: accessibilityRole="tab" + accessibilityState={ selected: true|false }.
- No color-only state. ↑ / ↓ deltas always paired with arrow icon AND text.
- Dynamic Type respected (no allowFontScaling={false}). Layouts tested at 
  iOS Accessibility XXL + Android 200% font scale in en / de / fr.
- WCAG 2.2 AA contrast minimum (Phase 5e palette is already AAA at ~27:1).
- Reduced-motion suppresses springs + haptics.

================================================================================
DELIVERABLES EXPECTED FROM YOU (Claude Code)
================================================================================
Phase 1 — Information Architecture (start here):
  1. Confirm the screen inventory above OR propose adds/removes with rationale.
  2. Sketch the navigation graph as Mermaid.
  3. List the components needed (atomic + molecule + organism level).

Phase 2 — Design tokens (single deliverable):
  4. Output `packages/core/tokens/colors.ts` + `typography.ts` + `spacing.ts` 
     + `motion.ts` + `haptics.ts` as TS modules. Verbatim values from § Design 
     Language above.

Phase 3 — Per-screen specs (one screen per response, in order of priority):
  5. For each screen: ASCII wireframe + component list + state matrix 
     (loading / empty / error / success / offline) + interactions + 
     animation entry/exit + a11y notes + reduced-motion fallback.

Phase 4 — Component library (deliver after screens 1, 2, 8, 10, 11 are done):
  6. Each component: TS interface (props), state matrix, motion behavior, 
     haptic trigger, a11y labels, RN code skeleton (StyleSheet.create + 
     useReducedMotion + useHaptic helpers).

Phase 5 — Skia chart specs (after Overview screen lands):
  7. Visitors trend: SkPath construction, animation entry, axis label rendering, 
     a11y mirror. Code skeleton.
  8. Dual-bar: same.

Phase 6 — Reference implementation (highest-leverage screen first):
  9. Working RN + Skia code for ServerListScreen + AddServerScreen + 
     OverviewScreen — three screens that exercise every primitive.

================================================================================
CONSTRAINTS YOU MUST RESPECT WHEN DESIGNING
================================================================================
- Do not invent new colors, fonts, spring presets, or haptics. Use only the 
  tokens above. If you find a gap, propose the new token EXPLICITLY in a 
  separate section labeled "PROPOSED TOKEN ADDITIONS" — do not silently add.
- Do not propose third-party SDKs. If you need a capability (e.g. blur view), 
  use bare RN APIs (e.g. `expo-blur` is rejected — use react-native's 
  built-in `BlurView` or implement via Skia).
- Do not propose Tamagui, NativeBase, gluestack, or any other component lib. 
  Plain RN + StyleSheet only — bundle-budget critical.
- Do not propose iconography systems other than Lucide.
- Do not propose Lottie or any external animation runtime. Reanimated v4 + 
  Skia ONLY.
- Do not propose celebration / streak / badge / gamification UX — see doc 37 
  § 12 explicit out-of-scope.
- Do not propose offline mode in v1 (v2 SQLCipher cache is the agreed path).
- Do not propose push notifications in v1 (v2 — APNs + FCM opt-in).
- Do not propose dark mode in v1 unless you can deliver both palettes. (v1 = 
  light mode only matching Phase 5e web; dark mode is M6.1.)
- Do not propose tablet / iPad layouts. v1 = phone-first responsive; tablet = v2.

================================================================================
ANTI-INSPIRATION (what NOT to look like)
================================================================================
- Google Analytics mobile app — too dense, too many tabs, no RPV.
- Mixpanel mobile — overstuffed event-builder UX; we are read-only.
- PostHog — too many products in one shell.
- Plausible — too web-bound, no native gestures.
- Generic SaaS-dashboard apps with stock illustrations and "Boost your 
  conversions!" CTAs.
- Hyper-skeuomorphic banking apps with gradients + neumorphism.

================================================================================
INSPIRATION (what to look like)
================================================================================
- Linear iOS — clean ink-on-paper, restraint, fast.
- Atlassian Jira mobile — multi-account flow + server switcher (we mirror this).
- Apple Stocks — type-led numeric hierarchy, great chart density.
- 1Password mobile — biometric unlock UX, vault-list pattern (we mirror for 
  server list).
- Stripe Dashboard mobile — KPI tile rhythm + dual-color delta pills.
- Things 3 — motion restraint; springs that feel inevitable, not showy.

================================================================================
START HERE
================================================================================
Begin with Phase 1 (Information Architecture). Confirm the screen inventory, 
output the Mermaid nav graph, and list components. After that delivers, 
I will say "proceed to Phase 2" and you produce the tokens. We progress 
phase-by-phase to keep each output reviewable.

Read these references before you start:
- statnive-live/PLAN.md § Phase 12 (M6 deliverables, perf gates, security gates)
- jaan-to/docs/research/37-mobile-app-rn-architecture-perf-security.md (all 13 §)
- statnive-live/docs/brand.md (palette, type, voice)
- statnive-live/CLAUDE.md (product philosophy, privacy rules, isolation)
- packages/web/src/tokens.css (current Phase 5e palette — when restructure 
  lands; today at web/src/tokens.css)
```

---

## Tips for running this prompt

- **Run in the parent statnive-workflow repo root**, not inside `statnive-live/`. The agent needs visibility into both `statnive-live/PLAN.md` and `jaan-to/docs/research/37-…md`.
- **Keep responses phase-by-phase.** Don't ask for "all 32 screens at once" — output quality collapses past ~5 screens per response. The "START HERE" footer enforces this.
- **Iterate via focused follow-ups**, not full rewrites. Examples:
  - "Take ServerListScreen and produce the empty / loading / error / cert-mismatch states."
  - "OverviewScreen — the visitors-trend Skia chart needs a hover / press detail tooltip. Spec it."
  - "Re-do the AdminUsersScreen state matrix — the create-user flow needs a confirm step before disable."
- **Reject any output that violates a hard constraint.** If the agent proposes Lottie, dark mode in v1, or a third-party SDK, push back; the constraint list is non-negotiable.
- **Hand the final per-screen specs to `/jaan-to:frontend-task-breakdown`** to produce the implementation tickets for Week 35 / 37 / 38 of Phase 12.

---

## When to update this brief

- **Tokens change.** If the web dashboard tokens shift (Phase 6-polish-* updates), update § Design Language → COLORS / TYPOGRAPHY here in lockstep. Mobile must stay byte-identical to web on shared tokens.
- **New constraint surfaces.** If an App Store / Play Store review surfaces a new mandatory UI (e.g. a new privacy disclosure screen), add it to § Screen Inventory.
- **Out-of-scope flips to in-scope.** If push / dark mode / tablet layout flips from "v2" to "v1.x", update § Constraints + § Screen Inventory in the same commit that flips the roadmap.

This file is the single source for what the mobile UI should look like. Do not duplicate its contents in CLAUDE.md, PLAN.md, or doc 37 — they reference this file by path.
