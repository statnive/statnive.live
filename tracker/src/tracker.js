// statnive.live tracker — vanilla JS IIFE, ≤3.4 KB minified / ≤1.2 KB gzipped.
//
// Privacy contract:
//   - Short-circuits BEFORE any observable side effect on navigator.webdriver
//     and _phantom (anti-automation; not a privacy policy).
//   - DNT and Sec-GPC are NOT consulted client-side. Browsers attach the
//     `DNT: 1` / `Sec-GPC: 1` request headers automatically; the server
//     honors them per the per-site `respect_dnt` / `respect_gpc` columns
//     (Lesson 24). This moves the policy decision from the bundle to the
//     binary, where each operator can configure per-jurisdiction.
//   - Implicit cookies / sessionStorage / IndexedDB: NONE. The only client
//     storage is operator-driven .setSession / .identify state, persisted
//     ONLY after .acceptConsent() resolves. `sn_consent`, `sn_user_props`
//     (first-party cookies, SameSite=Lax, no Domain attr) and
//     sessionStorage[sn_sess_props] are written exclusively by the public
//     API, never inferred from visitor behaviour or device. Air-gap
//     deployments may set consent.require_for_segments=false server-side
//     to short-circuit the gate.
//   - No fingerprinting (canvas, WebGL, font enum, navigator.plugins,
//     AudioContext, deviceMemory, hardwareConcurrency).
//   - sendBeacon + fetch keepalive only — no XMLHttpRequest.
//
// Public surface (window.statniveLive — namespaced to the product domain
// statnive.live so the SaaS tracker cannot collide with the unrelated WP
// plugin tracker that some same-brand customers also load at window.statnive
// as a queue stub):
//   .track(name, hitProps, value)  — custom event; pageview is fired
//                                    automatically. hitProps is hit-scope
//                                    (per-event). Goal-matched events
//                                    have their value overwritten by the
//                                    admin-configured goal.value
//                                    (server-authoritative).
//   .identify(uid, userProps)      — store raw uid + (optional) user-scope
//                                    props. Server hashes uid (Privacy
//                                    Rule 4). userProps persisted to
//                                    sn_user_props cookie ONLY when
//                                    getConsent() === 'resolved'.
//   .setSession(sessProps)         — session-scope props; persisted to
//                                    sessionStorage[sn_sess_props] ONLY
//                                    when getConsent() === 'resolved'.
//   .getConsent()                  — 'idle' | 'resolved' | 'withdrawn'.
//   .acceptConsent(csrfToken)      — POST /api/privacy/consent{action:'give'},
//                                    flip sn_consent cookie to 'resolved'.
//   .withdrawConsent(csrfToken)    — POST /api/privacy/consent{action:'withdraw'},
//                                    clear all sn_* storage.
(function (w, d) {
  if (w.statniveLive) return;

  // Hoisted constants — Terser inlines short identifiers across multiple
  // call sites cheaper than repeated string literals.
  var R = 'resolved', I = 'idle', WD = 'withdrawn';
  var KC = 'sn_consent', KU = 'sn_user_props', KS = 'sn_sess_props';
  var COOK = '; SameSite=Lax; Max-Age=31536000; Path=/';

  function noopApi(c) {
    var n = function () {};
    return { track: n, identify: n, setSession: n, getConsent: function () { return c; }, acceptConsent: n, withdrawConsent: n };
  }
  if (w.navigator.webdriver === true || w._phantom || w.callPhantom) {
    w.statniveLive = noopApi(I);
    return;
  }

  // Endpoint resolution chain:
  //   1. explicit data-statnive-endpoint attribute (canonical)
  //   2. derive from <script src="…/tracker.js"> (cross-origin marketing)
  //   3. relative /api/event (same-origin self-hosted)
  var script = d.currentScript || d.querySelector('script[data-statnive-endpoint]') || d.querySelector('script[src*="/tracker.js"]');
  var attr = script && script.getAttribute('data-statnive-endpoint');
  var src = script && script.src;
  var derived = src && src.match(/^(.+?)\/tracker\.js(?:\?.*)?$/);
  var endpoint = attr || (derived && derived[1] + '/api/event') || '/api/event';
  var base = endpoint.replace(/\/api\/event.*$/, '');

  // GPC opt-in: when ANY script tag on the page sets data-statnive-honour-gpc=1
  // AND the visitor's browser sends `Sec-GPC: 1`, client-side short-
  // circuit suppresses ALL tracker activity.
  if (w.navigator && w.navigator.globalPrivacyControl === true &&
      d.querySelector('script[data-statnive-honour-gpc="1"]')) {
    w.statniveLive = noopApi(WD);
    return;
  }

  var pv = 'pageview';
  var userId = '';
  var q = new URLSearchParams(w.location.search);

  // ─── Consent-gated client storage ─────────────────────────────────────
  // Two storage backends, three keys, one gate (getConsent() === R).
  //
  // ck(k)     read cookie k          ck(k, v)  set cookie k=v (1y SameSite=Lax)
  // ck(k,'') clear cookie k
  // ss(k)     read sessionStorage   ss(k, v)   set       ss(k, '') clear
  function ck(k, v) {
    if (v === undefined) {
      var p = d.cookie.split('; ');
      for (var i = 0; i < p.length; i++) {
        var x = p[i].split('=');
        if (x[0] === k) return decodeURIComponent(x[1] || '');
      }
      return '';
    }
    d.cookie = v ? k + '=' + encodeURIComponent(v) + COOK : k + '=; Max-Age=0; Path=/';
  }
  function ss(k, v) {
    try {
      if (v === undefined) return w.sessionStorage.getItem(k) || '';
      if (v) w.sessionStorage.setItem(k, v); else w.sessionStorage.removeItem(k);
    } catch (e) { return ''; }
  }
  function jp(s) { try { return s ? JSON.parse(s) : {}; } catch (e) { return {}; } }
  function getConsent() { return ck(KC) || I; }

  function fire(name, hitProps, value) {
    var resolved = getConsent() === R;
    var body = JSON.stringify({
      hostname: w.location.hostname,
      pathname: w.location.pathname,
      title: d.title,
      referrer: d.referrer,
      utm_source: q.get('utm_source') || '',
      utm_medium: q.get('utm_medium') || '',
      utm_campaign: q.get('utm_campaign') || '',
      utm_content: q.get('utm_content') || '',
      utm_term: q.get('utm_term') || '',
      event_type: name === pv ? pv : 'custom',
      event_name: name,
      event_value: value || 0,
      user_id: userId,
      hit_props: hitProps || {},
      session_props: resolved ? jp(ss(KS)) : {},
      user_props: resolved ? jp(ck(KU)) : {},
    });
    // sendBeacon does not accept custom headers; the consent signal
    // travels via the sn_consent cookie that acceptConsent set on its
    // response (browser auto-attaches). Beacon path remains coherent.
    if (w.navigator.sendBeacon) {
      w.navigator.sendBeacon(endpoint, new Blob([body], { type: 'text/plain' }));
    } else {
      var h = { 'Content-Type': 'text/plain' };
      if (resolved) h['X-Statnive-Consent'] = 'given';
      w.fetch(endpoint, { method: 'POST', headers: h, body: body, keepalive: true });
    }
  }

  // Sentinel makes the pagehide backstop idempotent with the inline
  // pageview() call, and refresh() resets it so SPA route changes fire.
  var pageviewed = false;
  function refresh() { q = new URLSearchParams(w.location.search); pageviewed = false; pageview(); }
  function pageview() { if (pageviewed) return; pageviewed = true; fire(pv); }

  var pushState = w.history.pushState;
  var replaceState = w.history.replaceState;
  w.history.pushState = function () { pushState.apply(this, arguments); refresh(); };
  w.history.replaceState = function () { replaceState.apply(this, arguments); refresh(); };
  w.addEventListener('popstate', refresh);
  // Backstop for async/defer trackers losing the inline pageview() to a
  // fast bouncer who unloads before our script finishes evaluating.
  w.addEventListener('pagehide', pageview);

  function consent(action, csrfToken) {
    return w.fetch(base + '/api/privacy/consent', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken || '' },
      body: JSON.stringify({ action: action }),
    }).then(function (r) {
      if (r && r.ok) {
        if (action === 'give') {
          ck(KC, R);
        } else {
          ck(KC, WD);
          ck(KU, '');
          ss(KS, '');
        }
      }
      return r;
    });
  }

  w.statniveLive = {
    track: function (name, hitProps, value) { fire(name, hitProps, value); },
    identify: function (uid, userProps) {
      userId = String(uid || '');
      if (userProps && getConsent() === R) ck(KU, JSON.stringify(userProps));
    },
    setSession: function (sessProps) {
      if (sessProps && getConsent() === R) ss(KS, JSON.stringify(sessProps));
    },
    getConsent: getConsent,
    acceptConsent: function (csrfToken) { return consent('give', csrfToken); },
    withdrawConsent: function (csrfToken) { return consent('withdraw', csrfToken); },
  };
  pageview();
})(window, document);
