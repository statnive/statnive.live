// statnive.live tracker — vanilla JS IIFE, ≤1.5 KB minified / ≤750 B gzipped.
//
// Privacy contract:
//   - Short-circuits BEFORE any observable side effect on navigator.webdriver
//     and _phantom (anti-automation; not a privacy policy).
//   - DNT and Sec-GPC are NOT consulted client-side. Browsers attach the
//     `DNT: 1` / `Sec-GPC: 1` request headers automatically; the server
//     honors them per the operator's `consent.respect_dnt` /
//     `consent.respect_gpc` config (default off — opt-in per deployment).
//     This moves the policy decision from the tracker bundle (one-size-
//     fits-all, can't be tuned per site) to the binary, where each
//     operator can configure their stance for their jurisdiction.
//   - No cookies / localStorage / sessionStorage / IndexedDB.
//   - No fingerprinting (canvas, WebGL, font enum, navigator.plugins,
//     AudioContext, deviceMemory, hardwareConcurrency).
//   - sendBeacon + fetch keepalive only — no XMLHttpRequest.
//
// Public surface (window.statniveLive — namespaced to the product domain
// statnive.live so the SaaS tracker cannot collide with the unrelated WP
// plugin tracker that some same-brand customers also load at window.statnive
// as a queue stub):
//   .track(name, props, value)  — custom event; pageview is fired automatically.
//                                 `value` is a positive integer; the dashboard
//                                 renders it via Intl.NumberFormat using the
//                                 site's configured currency code as a display
//                                 label (no minor-unit math). Goal-matched
//                                 events have their value overwritten by the
//                                 admin-configured goal.value (server-authoritative).
//   .identify(uid)              — store raw uid; sent in user_id field; server
//                                 hashes via identity.HexUserIDHash and clears
//                                 the raw value before pipeline (Privacy Rule 4).
(function (w, d) {
  if (w.statniveLive) return;
  if (w.navigator.webdriver === true || w._phantom || w.callPhantom) {
    w.statniveLive = { track: function () {}, identify: function () {} };
    return;
  }

  // Endpoint resolution chain:
  //   1. explicit data-statnive-endpoint attribute (canonical)
  //   2. derive from <script src="…/tracker.js"> (when the tracker is
  //      loaded cross-origin from app.statnive.live but the operator
  //      forgot the data attribute — closes Bug #18 silent /api/event
  //      relative-path 404 sink on cross-origin marketing sites)
  //   3. relative /api/event (works on same-origin self-hosted)
  var script = d.currentScript || d.querySelector('script[data-statnive-endpoint]') || d.querySelector('script[src*="/tracker.js"]');
  var attr = script && script.getAttribute('data-statnive-endpoint');
  var src = script && script.src;
  var derived = src && src.match(/^(.+?)\/tracker\.js(?:\?.*)?$/);
  var endpoint = attr || (derived && derived[1] + '/api/event') || '/api/event';
  // base is the origin the consent endpoints share with /api/event so
  // statniveLive.acceptConsent() / withdrawConsent() reach the same
  // tenant. Derived once at boot; static across the page lifetime.
  var base = endpoint.replace(/\/api\/event.*$/, '');
  // GPC opt-in: when ANY script tag on the page sets data-statnive-honour-gpc=1
  // AND the visitor's browser sends `Sec-GPC: 1`, client-side short-
  // circuit suppresses ALL tracker activity. The independent query
  // (not the endpoint-discovery chain) means the operator can put the
  // attribute on a separate <script> tag that doesn't need a src — a
  // common shape when the tracker is injected lazily.
  if (w.navigator && w.navigator.globalPrivacyControl === true &&
      d.querySelector('script[data-statnive-honour-gpc="1"]')) {
    w.statniveLive = { track: function () {}, identify: function () {}, acceptConsent: function () {}, withdrawConsent: function () {} };
    return;
  }
  var pv = 'pageview';
  var userId = '';
  // Cached UTM params — re-parsed on every history change since the
  // query string can shift on SPA navigation. Reused across all events
  // fired between navigations (typically 90% of all sends).
  var q = new URLSearchParams(w.location.search);

  function send(name, props, value) {
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
      props: props || {},
    });
    if (w.navigator.sendBeacon) {
      w.navigator.sendBeacon(endpoint, new Blob([body], { type: 'text/plain' }));
    } else {
      w.fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'text/plain' },
        body: body,
        keepalive: true,
      });
    }
  }

  // Sentinel makes the pagehide backstop idempotent with the inline
  // pageview() call, and refresh() resets it so SPA route changes fire.
  var pageviewed = false;
  function refresh() { q = new URLSearchParams(w.location.search); pageviewed = false; pageview(); }
  function pageview() { if (pageviewed) return; pageviewed = true; send(pv); }

  var pushState = w.history.pushState;
  var replaceState = w.history.replaceState;
  w.history.pushState = function () { pushState.apply(this, arguments); refresh(); };
  w.history.replaceState = function () { replaceState.apply(this, arguments); refresh(); };
  w.addEventListener('popstate', refresh);
  // Backstop for async/defer trackers losing the inline pageview() to a
  // fast bouncer who unloads before our script finishes evaluating.
  w.addEventListener('pagehide', pageview);

  // Consent helpers — invoke from the operator's consent banner JS.
  // The visitor MUST have visited /privacy first to seed the CSRF
  // cookie (and the meta-tag echo); the operator's banner reads the
  // token from that meta tag and passes it here. Returns the Promise
  // the operator can chain UI feedback on.
  function consent(action, csrfToken) {
    return w.fetch(base + '/api/privacy/consent', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrfToken || '' },
      body: JSON.stringify({ action: action }),
    });
  }

  w.statniveLive = {
    track: function (name, props, value) { send(name, props, value); },
    identify: function (uid) { userId = String(uid || ''); },
    acceptConsent: function (csrfToken) { return consent('give', csrfToken); },
    withdrawConsent: function (csrfToken) { return consent('withdraw', csrfToken); },
  };
  pageview();
})(window, document);
