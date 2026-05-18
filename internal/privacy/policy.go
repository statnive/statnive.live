// Package privacy serves the visitor-facing GDPR rights endpoints
// (Art. 21 opt-out, Art. 15 access, Art. 17 erase) and the hybrid
// consent gate. Every handler routes through policyToMode so the
// Stage 3 enum flip changes one function body and zero handler files.
package privacy

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/statnive/statnive.live/internal/sites"
)

// Mode is the consent posture the request must be served under.
// Handler bodies read a Mode and decide what to render / accept /
// refuse — they never read sites.SitePolicy directly so the eventual
// Stage 3 jurisdiction enum lands in exactly one place.
type Mode int

const (
	// ModeCurrent is the Stage 2 default — every site behaves
	// identically to its pre-Stage-2 posture. Handlers MUST treat
	// this as the operator-current default (no rounding, no
	// allow-list enforcement, identifier cookie permitted) since
	// the 3 live sites are pinned here until Stage 3 ships.
	ModeCurrent Mode = iota
	// ModeConsentFree — Stage 3, CNIL audience-measurement exemption.
	ModeConsentFree
	// ModeConsentRequired — Stage 3, block until explicit consent.
	ModeConsentRequired
	// ModePermissive — Stage 3, current-style permissive.
	ModePermissive
	// ModeHybridPreConsent — Stage 3, hybrid mode before the visitor
	// accepted analytics. Mirrors ModeConsentFree for enforcement.
	ModeHybridPreConsent
	// ModeHybridPostConsent — Stage 3, hybrid mode after the visitor
	// accepted analytics. Mirrors ModePermissive for enforcement.
	ModeHybridPostConsent
)

// String makes Mode log-friendly without exposing iota's number.
func (m Mode) String() string {
	switch m {
	case ModeCurrent:
		return "current"
	case ModeConsentFree:
		return "consent-free"
	case ModeConsentRequired:
		return "consent-required"
	case ModePermissive:
		return "permissive"
	case ModeHybridPreConsent:
		return "hybrid-pre-consent"
	case ModeHybridPostConsent:
		return "hybrid-post-consent"
	default:
		return "unknown"
	}
}

// defaultLegacyCookieCutoff is the deploy-date + 30 days cutoff after
// which the dual-read window stops honouring the pre-Stage-4 single
// _statnive_consent cookie. Atomic pointer so SetLegacyCookieCutoff
// can extend the window without redeploying when ops observes the
// legacy population is still significant. Stage-4 plan §6.
var legacyCookieCutoff atomic.Pointer[time.Time]

// LegacyCookieReadObserver is the callback the dual-read path fires
// each time it hits the legacy cookie. main.go wires a Prometheus
// counter increment here; tests inject a counter for assertions.
// Nil-safe — no-op when unset.
var legacyCookieReadObserver atomic.Pointer[func(siteID uint32)]

func init() {
	// Default: 30 days from Stage-4-B deploy date. Operators who
	// observe csrf_legacy_cookie_reads_total > 1% past this date can
	// extend via SetLegacyCookieCutoff.
	t := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	legacyCookieCutoff.Store(&t)
}

// SetLegacyCookieCutoff lets main.go (or tests) override the
// hardcoded 2026-06-17 default. Pass a zero time.Time to disable the
// dual-read window entirely (read only per-site cookies).
func SetLegacyCookieCutoff(t time.Time) {
	legacyCookieCutoff.Store(&t)
}

// SetLegacyCookieReadObserver wires the metric callback. Production
// passes the Prometheus counter's Inc; tests pass a closure that
// accumulates calls. Nil clears the observer.
func SetLegacyCookieReadObserver(f func(siteID uint32)) {
	if f == nil {
		legacyCookieReadObserver.Store(nil)

		return
	}

	legacyCookieReadObserver.Store(&f)
}

// PolicyToMode resolves the consent posture for a request. Stage 3
// switches on sites.SitePolicy.ConsentMode + the request's consent
// signal. Stage 4 takes siteID so the per-site consent cookie name
// (_statnive_consent_<id>) can be read; the dual-read fallback to the
// legacy single cookie applies until legacyCookieCutoff.
//
// An empty ConsentMode (a row that pre-dates migration 013 or the
// backfill default itself) falls through to ModeCurrent so the 3
// live operators keep byte-for-byte identical behaviour until they
// consciously flip jurisdiction or mode via the admin UI.
func PolicyToMode(r *http.Request, siteID uint32, p sites.SitePolicy) Mode {
	switch p.ConsentMode {
	case sites.ConsentModeConsentFree:
		return ModeConsentFree
	case sites.ConsentModePermissive:
		return ModePermissive
	case sites.ConsentModeConsentRequired:
		if hasValidConsent(r, siteID) {
			return ModePermissive
		}

		return ModeConsentRequired
	case sites.ConsentModeHybrid:
		if hasValidConsent(r, siteID) {
			return ModeHybridPostConsent
		}

		return ModeHybridPreConsent
	default:
		return ModeCurrent
	}
}

// hasValidConsent reports whether the visitor accepted analytics. Stage-4
// priority chain:
//
//  1. Per-site cookie `_statnive_consent_<siteID>=v1` (canonical).
//  2. X-Statnive-Consent: given header (operator-banner escape hatch).
//  3. Legacy single `_statnive_consent=v1` cookie — ONLY before
//     legacyCookieCutoff (Stage-4 dual-read window).
//
// Each legacy-cookie hit increments the observer counter so ops can
// confirm the legacy population has shrunk below threshold before
// retiring the dual-read path.
func hasValidConsent(r *http.Request, siteID uint32) bool {
	perSite := LegacyConsentCookieName + "_" + strconv.FormatUint(uint64(siteID), 10)
	if c, err := r.Cookie(perSite); err == nil && c.Value == consentCookieValue {
		return true
	}

	if r.Header.Get("X-Statnive-Consent") == "given" {
		return true
	}

	cutoff := legacyCookieCutoff.Load()
	if cutoff == nil || time.Now().After(*cutoff) {
		return false
	}

	if c, err := r.Cookie(LegacyConsentCookieName); err == nil && c.Value == consentCookieValue {
		if obs := legacyCookieReadObserver.Load(); obs != nil {
			(*obs)(siteID)
		}

		return true
	}

	return false
}

// AnonymousCount reports whether the Mode's storage profile treats
// per-visitor counts as anonymous-only — i.e. dashboard surfaces
// must round to nearest 10 (CNIL guidance for the audience-
// measurement exemption). True for ConsentFree + HybridPreConsent;
// false for every other mode (Current, Permissive, ConsentRequired
// post-consent, HybridPostConsent).
func (m Mode) AnonymousCount() bool {
	return m == ModeConsentFree || m == ModeHybridPreConsent
}

// AllowsIdentifier reports whether the Mode permits the _statnive
// identifier cookie to be set + the hashed cookie_id to land in
// events_raw. False for consent-free + consent-required (pre-consent)
// + hybrid pre-consent; true for current + permissive + hybrid
// post-consent.
func (m Mode) AllowsIdentifier() bool {
	switch m {
	case ModeCurrent, ModePermissive, ModeHybridPostConsent:
		return true
	case ModeConsentFree, ModeConsentRequired, ModeHybridPreConsent:
		return false
	}

	return false
}

// EnforcesEventAllowlist reports whether the Mode requires every
// ingested event_name to be a member of the site's
// SitePolicy.EventAllowlist. True for consent-free + hybrid
// pre-consent; false elsewhere.
func (m Mode) EnforcesEventAllowlist() bool {
	return m == ModeConsentFree || m == ModeHybridPreConsent
}
