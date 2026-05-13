package privacy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
)

// consentCookieName is the strictly-necessary cookie that flips a
// hybrid-mode (or consent-required) visitor from pre-consent to
// post-consent. Value v1 is the only currently-recognised marker;
// future revisions bump the version so an old browser tab can't
// replay a stale consent.
const consentCookieName = "_statnive_consent"

// consentCookieValue is the wire value the handler sets and reads
// back. Constant — no per-visitor entropy — because the cookie's
// presence (not its value) is what carries the consent signal.
const consentCookieValue = "v1"

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
			Name:     consentCookieName,
			Value:    consentCookieValue,
			Path:     "/",
			MaxAge:   consentCookieMaxAge,
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
		})

		h.emit(r.Context(), audit.EventConsentGiven, siteID, hash)

	case "withdraw":
		// Clear both the consent marker and the identifier cookie.
		// SameSite=Lax + Path=/ + Max-Age=-1 is the standard
		// browser-side "delete" recipe (Set-Cookie with the same
		// attributes + a zero Expires).
		expireCookie(w, consentCookieName, secure)
		expireCookie(w, "_statnive", secure)

		// Add to suppression so subsequent events from this browser
		// are dropped at the ingest gate; mirrors POST /opt-out.
		if addErr := h.cfg.Suppression.Add(hash); addErr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     "_statnive_optout",
			Value:    "v1",
			Path:     "/",
			MaxAge:   int(365 * 24 * time.Hour / time.Second),
			HttpOnly: true,
			Secure:   secure,
			SameSite: http.SameSiteLaxMode,
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
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// Compile-time guard: keep the errors-package import live across
// future iterations.
var _ = errors.Is
