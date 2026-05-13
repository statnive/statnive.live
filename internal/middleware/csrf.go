// Package middleware holds HTTP middleware that doesn't fit cleanly in
// a domain package — currently the CSRF double-submit guard used by the
// public /api/privacy/* endpoints (visitor not authenticated; cannot
// reuse the session-token-based admin auth).
package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"time"
)

// CSRFCookieName is the strictly-necessary cookie holding the random
// token. Browser MUST echo this value in the X-CSRF-Token request
// header on any state-changing POST so the handler can constant-time
// compare. SameSite=Lax + HttpOnly mean the cookie is not exposed to
// JS; the server-rendered /privacy page echoes the token in a meta
// tag so the page's inline JS can put it on the header.
const CSRFCookieName = "_statnive_csrf"

// CSRFHeader is the request header the client echoes the cookie value
// in. Naming follows the OWASP CSRF-Cheat-Sheet double-submit pattern.
const CSRFHeader = "X-CSRF-Token"

// csrfTokenBytes is the random-token byte length pre-encoding. 32
// bytes of crypto/rand → 256 bits of entropy → 43-char base64-URL
// (no padding). Matches the session-id surface in internal/auth.
const csrfTokenBytes = 32

// csrfCookieMaxAge bounds the freshness of a single token. One hour
// covers a typical visitor's opt-out flow; long-lived sessions issue
// a new token on each /privacy page load.
const csrfCookieMaxAge = int((1 * time.Hour) / time.Second)

// ErrCSRFMissing is returned when the request omits the cookie OR the
// header — both are required, both must match.
var ErrCSRFMissing = errors.New("csrf token missing")

// ErrCSRFMismatch is returned when both are present but the values
// diverge — almost certainly a cross-site forgery attempt.
var ErrCSRFMismatch = errors.New("csrf token mismatch")

// IssueCSRFToken writes a fresh _statnive_csrf cookie and returns the
// token value so callers (e.g. the /privacy page template) can echo
// it in a meta tag. Idempotent — call once per server-rendered page
// load. Caller MUST call before writing the response body.
func IssueCSRFToken(w http.ResponseWriter, secure bool) (string, error) {
	buf := make([]byte, csrfTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	token := base64.RawURLEncoding.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   csrfCookieMaxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	return token, nil
}

// VerifyCSRF compares the request's cookie value against the
// X-CSRF-Token header in constant time. Returns nil on match,
// ErrCSRFMissing if either is absent, ErrCSRFMismatch on divergence.
//
// The double-submit pattern relies on the attacker being unable to
// read the visitor's cookie (HttpOnly + same-origin) AND unable to
// set the request header on a cross-site POST (the latter is
// browser-enforced for non-simple headers per CORS).
func VerifyCSRF(r *http.Request) error {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return ErrCSRFMissing
	}

	header := r.Header.Get(CSRFHeader)
	if header == "" {
		return ErrCSRFMissing
	}

	if subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
		return ErrCSRFMismatch
	}

	return nil
}

// RequireCSRF wraps next so any state-changing request (POST, PUT,
// PATCH, DELETE) is rejected with 403 unless VerifyCSRF passes.
// Read-only verbs pass through unchanged so GET /privacy can issue
// the cookie on first visit.
func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStateChanging(r.Method) {
			if err := VerifyCSRF(r); err != nil {
				http.Error(w, "csrf token required", http.StatusForbidden)

				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}

	return false
}
