// Package privacy serves the visitor-facing GDPR rights endpoints
// (Art. 21 opt-out, Art. 15 access, Art. 17 erase) and the hybrid
// consent gate. Every handler routes through policyToMode so the
// Stage 3 enum flip changes one function body and zero handler files.
package privacy

import (
	"net/http"

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

// PolicyToMode resolves the consent posture for a request. Stage 3
// switches on sites.SitePolicy.ConsentMode + the request's consent
// signal. An empty ConsentMode (a row that pre-dates migration 013 or
// the backfill default itself) falls through to ModeCurrent so the 3
// live operators keep byte-for-byte identical behaviour until they
// consciously flip jurisdiction or mode via the admin UI.
//
// Hybrid splits two ways:
//   - Visitor sent _statnive_consent=v1 or X-Statnive-Consent: given
//     → ModeHybridPostConsent (mirrors ModePermissive enforcement).
//   - Otherwise → ModeHybridPreConsent (mirrors ModeConsentFree
//     enforcement: no cookie, round-to-10, event-allowlist gate).
//
// consent-required treats a present consent signal the same way:
// visitors who never accepted stay in the strict mode; visitors who
// did get the permissive treatment.
func PolicyToMode(r *http.Request, p sites.SitePolicy) Mode {
	switch p.ConsentMode {
	case sites.ConsentModeConsentFree:
		return ModeConsentFree
	case sites.ConsentModePermissive:
		return ModePermissive
	case sites.ConsentModeConsentRequired:
		if hasValidConsent(r) {
			return ModePermissive
		}

		return ModeConsentRequired
	case sites.ConsentModeHybrid:
		if hasValidConsent(r) {
			return ModeHybridPostConsent
		}

		return ModeHybridPreConsent
	default:
		// Empty / unrecognised — preserve the legacy posture so a
		// fresh deploy with a half-applied migration doesn't flip
		// every site to permissive (which would silently widen the
		// data surface on every existing operator).
		return ModeCurrent
	}
}

// hasValidConsent reports whether the visitor has accepted analytics
// in the hybrid / consent-required flow. The strictly-necessary
// _statnive_consent cookie is the canonical store; the
// X-Statnive-Consent header is a server-rendered escape hatch for
// pages that submit through the operator's own consent banner.
func hasValidConsent(r *http.Request) bool {
	if c, err := r.Cookie("_statnive_consent"); err == nil && c.Value == "v1" {
		return true
	}

	return r.Header.Get("X-Statnive-Consent") == "given"
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
