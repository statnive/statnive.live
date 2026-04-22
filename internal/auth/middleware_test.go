package auth

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestDeps(t *testing.T) (MiddlewareDeps, *fakeStore, *User) {
	t.Helper()

	fs := newFakeStore()

	u := &User{
		UserID: uuid.New(),
		SiteID: 1,
		Email:  "a@b.c",
		Role:   RoleAdmin,
	}

	if err := fs.CreateUser(context.Background(), u, "stub-hash"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	deps := MiddlewareDeps{
		Store: fs,
		CookieCfg: SessionCookieConfig{
			Name:     "statnive_session",
			TTL:      time.Hour,
			Secure:   true,
			SameSite: http.SameSiteLaxMode,
		},
	}

	return deps, fs, u
}

func mintSession(t *testing.T, fs *fakeStore, u *User) (rawCookie string) {
	t.Helper()

	p, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	now := time.Now().UTC()
	s := &Session{
		IDHash:     p.Hash,
		UserID:     u.UserID,
		SiteID:     u.SiteID,
		Role:       u.Role,
		CreatedAt:  now.Unix(),
		LastUsedAt: now.Unix(),
		ExpiresAt:  now.Add(time.Hour).Unix(),
	}

	if err := fs.CreateSession(context.Background(), s, [16]byte{}, "ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	return p.Raw
}

func TestSessionMiddleware_AttachesUser(t *testing.T) {
	t.Parallel()

	deps, fs, u := newTestDeps(t)
	raw := mintSession(t, fs, u)

	var gotUser *User

	h := SessionMiddleware(deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = UserFrom(r.Context())

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieCfg.Name, Value: raw})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	if gotUser == nil || gotUser.UserID != u.UserID {
		t.Fatalf("UserFrom ctx mismatch: %+v", gotUser)
	}
}

func TestSessionMiddleware_NoCookiePassesThrough(t *testing.T) {
	t.Parallel()

	deps, _, _ := newTestDeps(t)

	var called bool

	h := SessionMiddleware(deps)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true

		if u := UserFrom(r.Context()); u != nil {
			t.Error("unexpected user on unauthenticated request")
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/user", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if !called {
		t.Error("next handler should be called even without a cookie (RequireAuthenticated is the 401 gate)")
	}
}

func TestSessionMiddleware_NilNilFaultRejects(t *testing.T) {
	t.Parallel()

	// PLAN.md §53 — CVE-2024-10924 regression.
	// Fault-inject the store to return (nil, nil) from LookupSession;
	// middleware must NOT attach a nil *User to the context.
	deps, fs, _ := newTestDeps(t)
	fs.nilUser = true

	raw, _ := NewToken()

	h := SessionMiddleware(deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := UserFrom(r.Context()); u != nil {
			t.Error("SessionMiddleware attached a user despite (nil, nil) store result")
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieCfg.Name, Value: raw.Raw})

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
}

func TestAPITokenMiddleware_AttachesSyntheticUser(t *testing.T) {
	t.Parallel()

	deps, _, _ := newTestDeps(t)

	raw := "ci-smoke-token-value"
	hash := HashRawToken(raw)

	deps.APITokens = []APIToken{
		{
			TokenHashHex: hex.EncodeToString(hash[:]),
			SiteID:       42,
			Label:        "ci-smoke",
		},
	}

	var got *User

	h := APITokenMiddleware(deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = UserFrom(r.Context())

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil)
	req.Header.Set("Authorization", "Bearer "+raw)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if got == nil || got.Role != RoleAPI || got.SiteID != 42 {
		t.Fatalf("synthetic user wrong: %+v", got)
	}
}

func TestAPITokenMiddleware_WrongTokenPassesThrough(t *testing.T) {
	t.Parallel()

	deps, _, _ := newTestDeps(t)

	hash := HashRawToken("real-token")
	deps.APITokens = []APIToken{{TokenHashHex: hex.EncodeToString(hash[:]), SiteID: 1, Label: "ci"}}

	h := APITokenMiddleware(deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u := UserFrom(r.Context()); u != nil {
			t.Error("wrong bearer attached a user")
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
}

func TestRequireAuthenticated_Gates401(t *testing.T) {
	t.Parallel()

	h := RequireAuthenticated(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}

	if w.Header().Get("WWW-Authenticate") == "" {
		t.Error("missing WWW-Authenticate challenge header")
	}
}
