// Package middleware holds HTTP middleware that doesn't fit cleanly in
// a domain package — currently the CSRF double-submit guard used by
// the public /api/privacy/* endpoints + the Stage-4 CORS middleware
// for cross-origin SaaS support.
package middleware

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

// devInsecure reports whether the binary is running in localhost-dev
// mode. The flag is env-var only (never YAML) so a misconfigured
// production deploy can't silently fall back to the SameSite=Lax
// shape that breaks cross-origin SaaS.
func devInsecure() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(devInsecureEnv)))

	return v == "1" || v == "true" || v == "yes"
}

// CSRFCookieName is the strictly-necessary cookie holding the random
// token. The __Host- prefix forbids Domain= and requires Path=/ +
// Secure (RFC 6265bis) — locks the cookie to the exact host that set
// it, blocking subdomain cookie-injection variants of the
// double-submit bypass (Johansson 2017 "Double Defeat of the
// Double-Submit Cookie Pattern"). SameSite=None + Partitioned (per
// CHIPS) lets the cookie travel cross-origin from the operator's
// site to the SaaS host while preserving per-top-level-site
// isolation.
const CSRFCookieName = "__Host-statnive_csrf"

// LegacyCSRFCookieName is the pre-Stage-4 cookie name. RequireCSRF
// rejects requests carrying ONLY this name post-cutover — visitors
// reload /privacy and pick up the new __Host- cookie.
const LegacyCSRFCookieName = "_statnive_csrf"

// CSRFHeader is the request header the client echoes the cookie value
// in. Naming follows the OWASP CSRF Cheat Sheet double-submit pattern.
const CSRFHeader = "X-CSRF-Token"

// devInsecureEnv is the env-var flag that disables the __Host- prefix
// + SameSite=None;Secure requirements for HTTP localhost development.
// Documented in CLAUDE.md; never set in YAML, never set in CI.
const devInsecureEnv = "STATNIVE_DEV_INSECURE_CSRF"

// csrfRandomBytes is the random-token byte length pre-encoding. 32
// bytes of crypto/rand → 256 bits of entropy.
const csrfRandomBytes = 32

// csrfCookieMaxAge bounds the freshness of a single token. One hour
// covers a typical visitor's opt-out flow; long-lived sessions issue
// a new token on each /privacy page load.
const csrfCookieMaxAge = int((1 * time.Hour) / time.Second)

// csrfTokenSeparator splits the random-bytes prefix from the HMAC
// signature in the cookie value. Single byte so the token stays
// URL-safe + JSON-safe + meta-tag-safe.
const csrfTokenSeparator = "."

// ErrCSRFMissing is returned when the request omits the cookie OR the
// header — both are required, both must match.
var ErrCSRFMissing = errors.New("csrf token missing")

// ErrCSRFMismatch is returned when both are present but the values
// diverge — almost certainly a cross-site forgery attempt.
var ErrCSRFMismatch = errors.New("csrf token mismatch")

// ErrCSRFMalformed is returned when the cookie value is structurally
// invalid (missing the dot separator, bad base64, wrong-length parts).
// Distinct from ErrCSRFMismatch so the audit sink can distinguish
// "attacker forged a header" from "client carries a corrupted cookie".
var ErrCSRFMalformed = errors.New("csrf token malformed")

// ErrCSRFBadHMAC is returned when the cookie value parses cleanly but
// the HMAC signature doesn't verify against masterSecret — a planted
// cookie from a subdomain XSS (the Johansson attack).
var ErrCSRFBadHMAC = errors.New("csrf token signature invalid")

// IssueCSRFToken writes a fresh __Host-statnive_csrf cookie and
// returns the token value so callers (e.g. the /privacy page
// template) can echo it in a meta tag. Token shape:
//
//	base64url(random_32_bytes) + "." + base64url(HMAC-SHA256(secret, random_32))
//
// VerifyCSRF must recompute the HMAC with the same secret on every
// state-changing POST. Caller MUST pass masterSecret bound to the
// binary (config.LoadMasterSecret); a nil/empty secret is a hard
// programmer error.
func IssueCSRFToken(w http.ResponseWriter, secret []byte) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("csrf: empty master secret")
	}

	raw := make([]byte, csrfRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	rawPart := base64.RawURLEncoding.EncodeToString(raw)
	sig := base64.RawURLEncoding.EncodeToString(hmacToken(secret, raw))
	token := rawPart + csrfTokenSeparator + sig

	http.SetCookie(w, cookieFor(token))

	return token, nil
}

// cookieFor builds the Set-Cookie attributes. Two shapes:
//   - production: __Host-statnive_csrf; HttpOnly; Secure; Path=/;
//     SameSite=None; MaxAge=3600.
//   - dev (STATNIVE_DEV_INSECURE_CSRF=1): _statnive_csrf; HttpOnly;
//     Path=/; SameSite=Lax; MaxAge=3600 (no Secure → works on plain
//     HTTP localhost).
func cookieFor(token string) *http.Cookie {
	if devInsecure() {
		return &http.Cookie{
			Name:     LegacyCSRFCookieName,
			Value:    token,
			Path:     "/",
			MaxAge:   csrfCookieMaxAge,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		}
	}

	return &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   csrfCookieMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteNoneMode,
	}
}

// VerifyCSRF compares the request's cookie value against the
// X-CSRF-Token header in constant time AND verifies the HMAC
// signature on the cookie value. Returns nil on full success.
//
// The HMAC layer defends against the Johansson double-submit
// bypass: even if an attacker plants a cookie via subdomain XSS,
// they can't generate a valid HMAC without the server's secret,
// so the cookie will fail signature verification before the
// constant-time compare runs.
func VerifyCSRF(r *http.Request, secret []byte) error {
	cookie := cookieFromRequest(r)
	if cookie == "" {
		return ErrCSRFMissing
	}

	header := r.Header.Get(CSRFHeader)
	if header == "" {
		return ErrCSRFMissing
	}

	if subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) != 1 {
		return ErrCSRFMismatch
	}

	// Verify HMAC last — Johansson defence. Even if the cookie and
	// header match (attacker controlled both), the signature can't
	// be forged without masterSecret.
	return verifyHMAC(cookie, secret)
}

// cookieFromRequest reads the canonical __Host-statnive_csrf cookie
// first; falls back to the legacy _statnive_csrf cookie ONLY in dev
// mode (STATNIVE_DEV_INSECURE_CSRF=1) where the prefix can't be set
// over HTTP. Production prod-rejects legacy-named cookies.
func cookieFromRequest(r *http.Request) string {
	if c, err := r.Cookie(CSRFCookieName); err == nil && c.Value != "" {
		return c.Value
	}

	if devInsecure() {
		if c, err := r.Cookie(LegacyCSRFCookieName); err == nil && c.Value != "" {
			return c.Value
		}
	}

	return ""
}

// verifyHMAC decomposes the cookie value at the separator, re-computes
// HMAC-SHA256(secret, random_32) over the random prefix, and
// constant-time compares against the embedded signature.
func verifyHMAC(token string, secret []byte) error {
	if len(secret) == 0 {
		return ErrCSRFBadHMAC
	}

	idx := strings.IndexByte(token, csrfTokenSeparator[0])
	if idx < 0 {
		return ErrCSRFMalformed
	}

	rawPart := token[:idx]
	sigPart := token[idx+1:]

	raw, err := base64.RawURLEncoding.DecodeString(rawPart)
	if err != nil || len(raw) != csrfRandomBytes {
		return ErrCSRFMalformed
	}

	want := hmacToken(secret, raw)

	got, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil {
		return ErrCSRFMalformed
	}

	if !hmac.Equal(want, got) {
		return ErrCSRFBadHMAC
	}

	return nil
}

func hmacToken(secret, raw []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(raw)

	return mac.Sum(nil)
}

// RequireCSRF wraps next so any state-changing request (POST, PUT,
// PATCH, DELETE) is rejected with 403 unless VerifyCSRF passes.
// Read-only verbs pass through unchanged so GET /privacy can issue
// the cookie on first visit. Captures masterSecret in a closure so
// the middleware can be Use()'d on a chi route group.
func RequireCSRF(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isStateChanging(r.Method) {
				if err := VerifyCSRF(r, secret); err != nil {
					http.Error(w, "csrf token required", http.StatusForbidden)

					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}

	return false
}
