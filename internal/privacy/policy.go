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

// PolicyToMode resolves the consent posture for a request. Stage 2
// stubs this to ModeCurrent for every site; Stage 3 replaces the body
// to switch on sites.SitePolicy.ConsentMode + the request's consent
// signal. The signature already takes *http.Request so the Stage 3
// hasValidConsent check (cookie + header) can land without touching
// any caller.
func PolicyToMode(_ *http.Request, _ sites.SitePolicy) Mode {
	return ModeCurrent
}
