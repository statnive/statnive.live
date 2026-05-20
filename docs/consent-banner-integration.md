# Consent banner integration

Operator-facing guide for wiring an existing cookie / consent banner on
your site to statnive.live's privacy endpoints. After this, the
visitor's Accept / Reject / Dismiss click flows through the
cross-origin SaaS layer (Stage 4) and you see post-consent traffic in
your dashboard.

> **Prerequisite:** your site is registered in `app.statnive.live/admin`
> and you've pasted at least one entry into the new **Allowed origins**
> textarea on the site's Compliance card (e.g.
> `https://www.your-site.com`). Without that the browser blocks the
> consent POST cross-origin and the banner buttons fail silently.

## Pick a pattern

There are three legally-clean banner shapes. They differ only in which
button IDs you put in your HTML — the JS snippet at the bottom is the
same for all three. Anything you omit stays dormant.

| Pattern | Buttons | Legal posture | Best for |
|---|---|---|---|
| **A — Accept + Dismiss** | `consent_accept`, `consent_dismiss` | Cleanest under EU/DE law. Visitor never expresses an explicit "no" (GDPR Art. 21), so pre-consent anonymous tracking continues for everyone who hasn't accepted. | German B2B, SaaS landing pages, content sites. **Pattern televika.com uses.** |
| **B — Accept + Reject + Dismiss** | all three | Full operator control. Reject triggers full opt-out (Art. 21 objection → all tracking stops). Dismissers keep getting counted in the anonymous baseline. | Sites that want to honour an explicit "no" while still measuring undecided visitors. |
| **C — Accept + Reject** | `consent_accept`, `consent_reject` | Strictest. No "maybe later" — visitor must pick. Common in CNIL-strict French B2C. | Operators who interpret CNIL/EDPB conservatively. |

Most operators outside France pick Pattern A. It's what we recommend
for televika.com because the legitimate-interest baseline under TDDDG
§ 25(2) + GDPR Art. 6(1)(f) covers visitors who haven't clicked
anything, including those who dismissed the banner.

## The snippet (paste once on every page)

Drop this into your site's shared layout (e.g. `BaseLayout.astro`,
`_app.tsx`, or your CMS's footer template). It's self-contained — no
external dependencies, no framework.

```html
<script defer>
(function () {
  var APP_BASE = 'https://app.statnive.live';
  // Namespace localStorage by host so a visitor's decision on
  // shop-a.example doesn't suppress shop-b.example's banner when both
  // sites live under the same parent domain.
  var DECISION_KEY = 'statnive-decision:' + window.location.host;
  var csrfPromise = null;

  function getCSRF() {
    if (csrfPromise) return csrfPromise;
    csrfPromise = fetch(APP_BASE + '/privacy', { credentials: 'include' })
      .then(function (r) { return r.text(); })
      .then(function (html) {
        var m = html.match(/name="csrf-token" content="([^"]+)"/);
        // Treat empty / missing meta as a soft failure so the next
        // click re-fetches instead of POSTing with an empty token.
        if (!m || !m[1]) {
          csrfPromise = null;
          return '';
        }
        return m[1];
      })
      .catch(function () { csrfPromise = null; return ''; });
    return csrfPromise;
  }

  function hideBanner() {
    var b = document.getElementById('consent_banner');
    if (b) b.style.display = 'none';
  }

  function wire() {
    // Auto-hide on return visit (any prior decision suppresses the banner).
    if (localStorage.getItem(DECISION_KEY)) {
      hideBanner();
      return;
    }

    function wireOnce(el, handler) {
      // HMR / double-include guard — don't stack listeners.
      if (!el || el.dataset.statniveWired === '1') return;
      el.dataset.statniveWired = '1';
      el.addEventListener('click', handler);
    }

    wireOnce(document.getElementById('consent_accept'), function () {
      getCSRF().then(function (t) {
        if (!t) return; // CSRF unavailable — leave banner visible
        if (window.statniveLive && window.statniveLive.acceptConsent) {
          window.statniveLive.acceptConsent(t);
        }
        localStorage.setItem(DECISION_KEY, 'given');
        hideBanner();
      });
    });

    wireOnce(document.getElementById('consent_reject'), function () {
      getCSRF().then(function (t) {
        if (!t) return;
        return fetch(APP_BASE + '/api/privacy/opt-out', {
          method: 'POST',
          credentials: 'include',
          headers: { 'X-CSRF-Token': t },
        });
      }).then(function (r) {
        // Only persist + hide when the opt-out actually landed; a
        // failed POST means tracking continues, so the banner should
        // still show on the next visit for the operator to retry.
        if (r && r.ok) {
          localStorage.setItem(DECISION_KEY, 'rejected');
          hideBanner();
        }
      }).catch(function () { /* silent — banner stays */ });
    });

    wireOnce(document.getElementById('consent_dismiss'), function () {
      // Legal note: dismiss does NOT count as objection under GDPR
      // Art. 21. The visitor stays in pre-consent anonymous mode
      // under legitimate interest (Art. 6(1)(f) + TDDDG § 25(2)).
      localStorage.setItem(DECISION_KEY, 'dismissed');
      hideBanner();
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', wire);
  } else {
    wire();
  }
})();
</script>
```

## The HTML (your existing banner)

Use whatever markup + styles your site already has. The wiring above
only cares about three things:

1. The banner container's `id` is `consent_banner` (so the snippet can
   hide it after a click).
2. Buttons have `id="consent_accept"`, `id="consent_reject"`, or
   `id="consent_dismiss"`. Pick the subset matching your pattern.
3. The tracker `<script src="https://app.statnive.live/tracker.js">` is
   loaded on every page (`acceptConsent` lives on `window.statniveLive`).

### Pattern A example (televika.com)

```html
<div id="consent_banner" role="dialog" aria-live="polite">
  <p>
    Wir verwenden anonymisierte Reichweitenmessung. Mit Ihrer
    Einwilligung können wir die Nutzung detaillierter analysieren —
    Sie können diese jederzeit widerrufen.
    <a href="/cookiepolicy">Mehr erfahren</a>
  </p>
  <button id="consent_accept" type="button">Einwilligen</button>
  <button id="consent_dismiss" type="button">Weiter ohne Einwilligung</button>
</div>
```

### Pattern B example

```html
<div id="consent_banner" role="dialog">
  <p>This site uses privacy-first analytics. <a href="/privacy">Learn more</a></p>
  <button id="consent_accept" type="button">Accept</button>
  <button id="consent_reject" type="button">Reject</button>
  <button id="consent_dismiss" type="button">Maybe later</button>
</div>
```

### Pattern C example

```html
<div id="consent_banner" role="dialog">
  <p>Voulez-vous accepter la mesure d'audience anonyme ? <a href="/confidentialite">En savoir plus</a></p>
  <button id="consent_accept" type="button">Accepter</button>
  <button id="consent_reject" type="button">Refuser</button>
</div>
```

## Verify your integration

After deploying, open your site in Chrome with DevTools → Network:

1. **Banner shows on first visit.** localStorage is empty so the snippet
   leaves the banner visible.
2. **Accept click**:
   - `GET https://app.statnive.live/privacy` → 200 (CSRF fetch).
   - `POST https://app.statnive.live/api/privacy/consent` → **204**.
   - Response `Set-Cookie: _statnive_consent_<your-site-id>=v1; HttpOnly; Secure; SameSite=None; Partitioned`.
   - Subsequent pageviews include the consent cookie automatically.
   - Dashboard "With consent" column counts your visit (exact, not rounded-to-10).
3. **Reject click** (if you wired `#consent_reject`):
   - `POST https://app.statnive.live/api/privacy/opt-out` → **204**.
   - Response `Set-Cookie: _statnive_optout_<your-site-id>=v1`.
   - Subsequent pageviews are silently dropped at ingest.
4. **Dismiss click** (if you wired `#consent_dismiss`):
   - No network call. Banner hides. localStorage gets `dismissed`.
   - Subsequent pageviews still POST `/api/event` (anonymous mode — no cookie,
     rounded counts, allowlist enforced).
5. **Reload** after any of the three:
   - Banner stays hidden (localStorage decision flag set).

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Accept click does nothing, browser console shows `CORS error` | Your site's origin isn't in this site's **Allowed origins** | Open `app.statnive.live/admin` → your site → Compliance → Allowed origins textarea → paste `https://your-site.com` on its own line. |
| Accept fires, `/api/privacy/consent` returns 403 | CSRF cookie wasn't set on the prior `/privacy` GET | Visitor's browser blocks third-party cookies entirely (Safari ITP default; Firefox Total Cookie Protection without partition support). See "Safari users" below. |
| Banner shows on every page load even after click | localStorage being cleared between sessions (incognito) or your snippet runs in a different storage partition | Expected in incognito. Otherwise check that the snippet is loaded on every page, not just the homepage. |
| Status: rejected but events still ingested | Opt-out cookie expired (max 1 year) or visitor cleared cookies | Re-show the banner if `localStorage` is `rejected` but `document.cookie` doesn't contain `_statnive_optout_`. |

## Safari users (ITP)

Safari's Intelligent Tracking Prevention blocks all third-party cookies
by default — `__Host-statnive_csrf` and `_statnive_consent_*` won't be
stored when set by `app.statnive.live` from a top-level page on
`your-site.com`. The CHIPS `Partitioned` attribute we ship doesn't help
Safari (only Chrome 114+ and Firefox via TCP).

For Safari coverage you have two options:

1. **Self-host on `track.your-site.com`** — run the statnive.live
   binary under your own subdomain. Cookies become first-party. See
   [deployment.md](deployment.md).
2. **Accept the Safari loss** — Safari visitors stay in anonymous mode
   forever (legitimate-interest baseline). This is fine for most German
   B2B audiences where Safari is < 15% of traffic.

## Legal disclaimer

This document describes the *technical* integration. It is not legal
advice. Privacy-banner copy and the choice of pattern depend on your
jurisdiction (TDDDG, CNIL Sheet n°16, ICO PECR, etc.) and your
audience. Have your DPO or counsel review the banner text before
launching publicly.

See also:

- [`/privacy`](/privacy) — visitor-facing disclosure page (rendered by
  the statnive.live binary).
- [`/legal/lia`](/legal/lia) — Legitimate Interest Assessment template.
- [`/legal/dpa`](/legal/dpa) — Data Processing Agreement template.
- [tracker/README.md](../tracker/README.md) — tracker public API
  (`statniveLive.acceptConsent`, `withdrawConsent`, `track`,
  `identify`).
