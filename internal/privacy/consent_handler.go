package privacy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
)

// LegacyConsentCookieName is the pre-Stage-4 single-cookie name.
// Stage-4 switches to per-site naming (consentCookieName(siteID))
// for multi-tenancy isolation — a visitor consenting on site A
// must NOT auto-consent on site B served from the same SaaS host.
// The legacy name is kept here for the dual-read window so existing
// visitors don't get re-prompted mid-session.
const LegacyConsentCookieName = "_statnive_consent"

// LegacyOptoutCookieName is the pre-Stage-4 single opt-out cookie.
// Same rationale as LegacyConsentCookieName.
const LegacyOptoutCookieName = "_statnive_optout"

// consentCookieValue is the wire value the handler sets and reads
// back. Constant — no per-visitor entropy — because the cookie's
// presence (not its value) is what carries the consent signal.
const consentCookieValue = "v1"

// consentCookieName returns the per-site Stage-4 consent cookie name.
// Per-site naming prevents cross-tenant consent leakage on SaaS
// instances where one visitor browser interacts with multiple
// operator sites. Combined with Partitioned (CHIPS) attribute the
// cookie is also browser-isolated by top-level site.
func consentCookieName(siteID uint32) string {
	return LegacyConsentCookieName + "_" + strconv.FormatUint(uint64(siteID), 10)
}

// optoutCookieName returns the per-site Stage-4 opt-out cookie name.
// Mirrors consentCookieName for shape symmetry.
func optoutCookieName(siteID uint32) string {
	return LegacyOptoutCookieName + "_" + strconv.FormatUint(uint64(siteID), 10)
}

// consentCookieMaxAge bounds the freshness of a single consent
// decision. One year matches the CNIL guidance ceiling and the
// _statnive cookie's own lifetime.
const consentCookieMaxAge = int(365 * 24 * time.Hour / time.Second)

type consentRequest struct {
	Action string `json:"action"` // "give" | "withdraw"
}

// Consent handles POST /api/privacy/consent. Body shape:
//
//	{"action": "give"}      → sets _statnive_consent=v1
//	{"action": "withdraw"}  → clears _statnive_consent + _statnive,
//	                          adds the visitor to the suppression list
//
// CSRF is enforced by middleware upstream of this handler. The
// handler itself is response-shape stable so a misconfigured client
// can't enumerate consent state by diffing response bodies — both
// actions return 204.
func (h *Handlers) Consent(w http.ResponseWriter, r *http.Request) {
	siteID, hash, ok := h.resolveSiteAndCookie(w, r)
	if !ok {
		// resolveSiteAndCookie already wrote the error response.
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<10))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	var req consentRequest
	if jsonErr := json.Unmarshal(body, &req); jsonErr != nil {
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	secure := isHTTPS(r)

	switch req.Action {
	case "give":
		http.SetCookie(w, &http.Cookie{
			Name:        consentCookieName(siteID),
			Value:       consentCookieValue,
			Path:        "/",
			MaxAge:      consentCookieMaxAge,
			HttpOnly:    true,
			Secure:      secure,
			SameSite:    http.SameSiteNoneMode,
			Partitioned: true,
		})

		h.emit(r.Context(), audit.EventConsentGiven, siteID, hash)

	case "withdraw":
		// Clear both the per-site consent marker and the legacy
		// single cookie (defang any dual-read tokens). Identifier
		// cookie also cleared.
		expireCookie(w, consentCookieName(siteID), secure)
		expireCookie(w, LegacyConsentCookieName, secure)
		expireCookie(w, "_statnive", secure)

		if addErr := h.cfg.Suppression.Add(hash); addErr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:        optoutCookieName(siteID),
			Value:       "v1",
			Path:        "/",
			MaxAge:      int(365 * 24 * time.Hour / time.Second),
			HttpOnly:    true,
			Secure:      secure,
			SameSite:    http.SameSiteNoneMode,
			Partitioned: true,
		})

		h.emit(r.Context(), audit.EventConsentWithdrawn, siteID, hash)

	default:
		http.Error(w, "action must be 'give' or 'withdraw'", http.StatusBadRequest)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func expireCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:        name,
		Value:       "",
		Path:        "/",
		MaxAge:      -1,
		HttpOnly:    true,
		Secure:      secure,
		SameSite:    http.SameSiteNoneMode,
		Partitioned: true,
	})
}

// Compile-time guard: keep the errors-package import live across
// future iterations.
var _ = errors.Is
