package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testSecret = []byte("test-master-secret-32-bytes-long!!")

func makeValidToken(t *testing.T, secret []byte) string {
	t.Helper()

	raw := make([]byte, csrfRandomBytes)
	for i := range raw {
		raw[i] = byte(i + 1)
	}

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(raw)

	return base64.RawURLEncoding.EncodeToString(raw) + csrfTokenSeparator +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestIssueCSRFToken_WritesSecureCookie(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()

	token, err := IssueCSRFToken(rec, testSecret)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if !strings.Contains(token, csrfTokenSeparator) {
		t.Fatalf("token missing HMAC separator: %q", token)
	}

	res := rec.Result()

	defer func() { _ = res.Body.Close() }()

	var got *http.Cookie

	for _, c := range res.Cookies() {
		if c.Name == CSRFCookieName {
			got = c
			break
		}
	}

	if got == nil {
		t.Fatalf("no %s cookie set", CSRFCookieName)
	}

	if got.Value != token {
		t.Errorf("cookie value mismatch")
	}

	if !got.HttpOnly {
		t.Errorf("cookie should be HttpOnly")
	}

	if !got.Secure {
		t.Errorf("cookie should be Secure (cross-origin invariant)")
	}

	if got.SameSite != http.SameSiteNoneMode {
		t.Errorf("SameSite = %v, want None", got.SameSite)
	}
}

func TestIssueCSRFToken_EmptySecretRejected(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()

	if _, err := IssueCSRFToken(rec, nil); err == nil {
		t.Errorf("expected error on empty secret")
	}
}

func TestVerifyCSRF(t *testing.T) {
	t.Parallel()

	goodToken := makeValidToken(t, testSecret)

	cases := []struct {
		name       string
		cookieVal  string
		headerVal  string
		wantErr    error
		skipCookie bool
	}{
		{name: "match + valid HMAC → ok", cookieVal: goodToken, headerVal: goodToken, wantErr: nil},
		{name: "missing header", cookieVal: goodToken, headerVal: "", wantErr: ErrCSRFMissing},
		{name: "missing cookie", skipCookie: true, headerVal: goodToken, wantErr: ErrCSRFMissing},
		{name: "empty cookie value", cookieVal: "", headerVal: goodToken, wantErr: ErrCSRFMissing},
		{name: "mismatch", cookieVal: goodToken, headerVal: "other-token", wantErr: ErrCSRFMismatch},
		{name: "malformed (no separator)", cookieVal: "no-dot-here", headerVal: "no-dot-here", wantErr: ErrCSRFMalformed},
		{name: "valid format but wrong HMAC (Johansson attack)", cookieVal: makeValidToken(t, []byte("wrong-secret-32-bytes-padding!!!")), headerVal: makeValidToken(t, []byte("wrong-secret-32-bytes-padding!!!")), wantErr: ErrCSRFBadHMAC},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/x", nil)
			if !c.skipCookie {
				req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: c.cookieVal})
			}

			if c.headerVal != "" {
				req.Header.Set(CSRFHeader, c.headerVal)
			}

			err := VerifyCSRF(req, testSecret)
			if !errors.Is(err, c.wantErr) {
				t.Errorf("VerifyCSRF = %v, want %v", err, c.wantErr)
			}
		})
	}
}

func TestRequireCSRF_GETBypassesCheck(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/privacy", nil)
	rec := httptest.NewRecorder()

	RequireCSRF(testSecret)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET should bypass CSRF, got status %d", rec.Code)
	}
}

func TestRequireCSRF_POSTWithoutTokenIs403(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/privacy/opt-out", nil)
	rec := httptest.NewRecorder()

	RequireCSRF(testSecret)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("POST without token should be 403, got %d", rec.Code)
	}

	if !strings.Contains(rec.Body.String(), "csrf") {
		t.Errorf("response body should mention csrf, got %q", rec.Body.String())
	}
}

func TestRequireCSRF_POSTWithValidTokenPasses(t *testing.T) {
	t.Parallel()

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusOK)
	})

	token := makeValidToken(t, testSecret)

	req := httptest.NewRequest(http.MethodPost, "/api/privacy/opt-out", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: token})
	req.Header.Set(CSRFHeader, token)

	rec := httptest.NewRecorder()

	RequireCSRF(testSecret)(next).ServeHTTP(rec, req)

	if !called {
		t.Errorf("next handler not invoked despite valid CSRF token")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestRequireCSRF_POSTWithPlantedCookieRejected — Johansson defence.
// Attacker plants a cookie via subdomain XSS, then POSTs with both
// cookie and matching header set to the planted value. Without HMAC
// the request would pass; with HMAC it MUST fail.
func TestRequireCSRF_POSTWithPlantedCookieRejected(t *testing.T) {
	t.Parallel()

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	planted := "attacker-controlled-value-no-hmac"

	req := httptest.NewRequest(http.MethodPost, "/api/privacy/opt-out", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: planted})
	req.Header.Set(CSRFHeader, planted)

	rec := httptest.NewRecorder()

	RequireCSRF(testSecret)(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("planted cookie should be 403 (HMAC defence), got %d", rec.Code)
	}
}
