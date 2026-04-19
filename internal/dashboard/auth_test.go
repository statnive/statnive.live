package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBearerToken_EmptyTokenIsNoop(t *testing.T) {
	t.Parallel()

	mw := BearerTokenMiddleware("", nil)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/api/stats/overview", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (empty token is no-op)", rr.Code)
	}
}

func TestBearerToken_RejectsMissingHeader(t *testing.T) {
	t.Parallel()

	mw := BearerTokenMiddleware("secret", nil)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/api/stats/overview", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}

	if !strings.HasPrefix(rr.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("missing WWW-Authenticate header: %v", rr.Header())
	}
}

func TestBearerToken_RejectsWrongToken(t *testing.T) {
	t.Parallel()

	mw := BearerTokenMiddleware("secret", nil)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/api/stats/overview", nil)
	req.Header.Set("Authorization", "Bearer wrong")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestBearerToken_AcceptsCorrectToken(t *testing.T) {
	t.Parallel()

	mw := BearerTokenMiddleware("secret", nil)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/api/stats/overview", nil)
	req.Header.Set("Authorization", "Bearer secret")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
}

func TestBearerToken_CaseInsensitiveScheme(t *testing.T) {
	t.Parallel()

	mw := BearerTokenMiddleware("secret", nil)
	handler := mw(okHandler())

	req := httptest.NewRequest("GET", "/api/stats/overview", nil)
	req.Header.Set("Authorization", "bearer secret")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (lowercase 'bearer' should work)", rr.Code)
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}
