package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssueCSRFToken_WritesCookie(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()

	token, err := IssueCSRFToken(rec, false)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if len(token) == 0 {
		t.Fatalf("empty token")
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
		return // unreachable; staticcheck SA5011 doesn't see t.Fatalf as terminal
	}

	if got.Value != token {
		t.Errorf("cookie value %q != returned token %q", got.Value, token)
	}

	if !got.HttpOnly {
		t.Errorf("cookie should be HttpOnly")
	}

	if got.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", got.SameSite)
	}
}

func TestVerifyCSRF(t *testing.T) {
	t.Parallel()

	const goodToken = "good-token-value-that-is-long-enough"

	cases := []struct {
		name       string
		cookieVal  string
		headerVal  string
		wantErr    error
		skipCookie bool
	}{
		{name: "match → ok", cookieVal: goodToken, headerVal: goodToken, wantErr: nil},
		{name: "missing header", cookieVal: goodToken, headerVal: "", wantErr: ErrCSRFMissing},
		{name: "missing cookie", skipCookie: true, headerVal: goodToken, wantErr: ErrCSRFMissing},
		{name: "empty cookie value", cookieVal: "", headerVal: goodToken, wantErr: ErrCSRFMissing},
		{name: "mismatch", cookieVal: goodToken, headerVal: "other-token-value", wantErr: ErrCSRFMismatch},
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

			err := VerifyCSRF(req)
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

	RequireCSRF(next).ServeHTTP(rec, req)

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

	RequireCSRF(next).ServeHTTP(rec, req)

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

	const token = "verified-token"

	req := httptest.NewRequest(http.MethodPost, "/api/privacy/opt-out", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: token})
	req.Header.Set(CSRFHeader, token)

	rec := httptest.NewRecorder()

	RequireCSRF(next).ServeHTTP(rec, req)

	if !called {
		t.Errorf("next handler not invoked despite valid CSRF token")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
