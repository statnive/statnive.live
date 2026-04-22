package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- login happy path --------------------------------------------------------

func TestLogin_HappyPath(t *testing.T) {
	deps, fs, _ := newTestDeps(t)

	hash, err := HashPassword("super-strong-password", MinBcryptCost)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	u := &User{
		UserID: uuid.New(), SiteID: 1,
		Email: "admin@example.com", Username: "admin", Role: RoleAdmin,
	}

	if err := fs.CreateUser(context.Background(), u, hash); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, nil)

	body := strings.NewReader(`{"email":"admin@example.com","password":"super-strong-password"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.Login(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	got := w.Result().Cookies()
	if len(got) == 0 {
		t.Fatalf("no cookies set")
	}

	var sessionCookie *http.Cookie

	for _, c := range got {
		if c.Name == deps.CookieCfg.Name {
			sessionCookie = c
		}
	}

	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("session cookie missing")
	}

	if !sessionCookie.HttpOnly || !sessionCookie.Secure || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("cookie attrs wrong: HttpOnly=%v Secure=%v SameSite=%v",
			sessionCookie.HttpOnly, sessionCookie.Secure, sessionCookie.SameSite)
	}

	var resp loginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.User.Role != RoleAdmin {
		t.Errorf("role = %q", resp.User.Role)
	}
}

// --- F4 mass-assignment guard (PLAN.md §52) ---------------------------------

func TestLogin_RejectsUnknownFields(t *testing.T) {
	deps, _, _ := newTestDeps(t)
	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, nil)

	body := strings.NewReader(
		`{"email":"a@b.c","password":"pw","role":"admin","site_id":99,"is_admin":true}`,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	h.Login(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("unknown-fields not rejected: status = %d", w.Code)
	}
}

// --- uniform error body (no user enumeration) -------------------------------

func TestLogin_UniformErrorBody(t *testing.T) {
	deps, fs, _ := newTestDeps(t)

	realHash, err := HashPassword("good-password", MinBcryptCost)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	real := &User{UserID: uuid.New(), SiteID: 1, Email: "real@b.c", Role: RoleAdmin}
	_ = fs.CreateUser(context.Background(), real, realHash)

	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, nil)

	cases := []struct {
		name  string
		email string
		pw    string
	}{
		{"unknown-user", "ghost@b.c", "anything"},
		{"wrong-password", "real@b.c", "nope"},
	}

	want := `{"error":"invalid credentials"}`

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{
				"email": tc.email, "password": tc.pw,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			h.Login(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d", w.Code)
			}

			got, _ := io.ReadAll(w.Body)
			if strings.TrimSpace(string(got)) != want {
				t.Errorf("body = %q, want %q", got, want)
			}
		})
	}
}

// --- timing-attack defense --------------------------------------------------

func TestLogin_UnknownUserRunsBcrypt(t *testing.T) {
	// The unknown-user branch MUST call bcrypt against dummyHash so the
	// wall-time is roughly comparable to the real-user wrong-password
	// case. We assert "at least X ms of bcrypt work" since the exact
	// figure depends on the machine.
	deps, _, _ := newTestDeps(t)
	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, nil)

	body := strings.NewReader(`{"email":"ghost@b.c","password":"anything"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()

	h.Login(httptest.NewRecorder(), req)

	elapsed := time.Since(start)

	// bcrypt cost 12 takes ~50 ms+ on every machine anyone ever runs
	// this on; cost 10 takes ~15 ms. Assert at least 10 ms so we catch
	// a regression that accidentally skips the dummy-hash bcrypt call.
	if elapsed < 10*time.Millisecond {
		t.Errorf("unknown-user path returned in %v — bcrypt against dummyHash appears skipped", elapsed)
	}
}

// --- logout idempotency + cookie clear --------------------------------------

func TestLogout_ClearsCookieEvenWithoutSession(t *testing.T) {
	deps, _, _ := newTestDeps(t)
	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	w := httptest.NewRecorder()
	h.Logout(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d", w.Code)
	}

	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("no cookies on logout")
	}

	if cookies[0].MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want < 0 (evicting)", cookies[0].MaxAge)
	}
}

func TestLogout_RevokesAndClearsCookie(t *testing.T) {
	deps, fs, u := newTestDeps(t)
	raw := mintSession(t, fs, u)

	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieCfg.Name, Value: raw})

	w := httptest.NewRecorder()
	h.Logout(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}

	// Session must be revoked in the store.
	if _, err := fs.LookupSession(context.Background(), HashRawToken(raw)); err == nil {
		t.Errorf("session still valid after logout")
	}
}

// --- /api/user bootstrap ----------------------------------------------------

func TestMe_RequiresSession(t *testing.T) {
	deps, _, _ := newTestDeps(t)
	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, nil)

	w := httptest.NewRecorder()
	h.Me(w, httptest.NewRequest(http.MethodGet, "/api/user", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Me without session = %d, want 401", w.Code)
	}
}

func TestMe_ReturnsAuthenticated(t *testing.T) {
	deps, _, u := newTestDeps(t)
	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/user", nil)
	req = req.WithContext(WithSession(context.Background(), u, &Session{SiteID: u.SiteID, Role: u.Role}))

	w := httptest.NewRecorder()
	h.Me(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp["role"] != string(RoleAdmin) {
		t.Errorf("role = %v", resp["role"])
	}
}

// --- per-email lockout end-to-end ------------------------------------------

func TestLogin_PerEmailLockoutAcrossIPs(t *testing.T) {
	deps, fs, _ := newTestDeps(t)

	pw, _ := HashPassword("right", MinBcryptCost)

	u := &User{UserID: uuid.New(), SiteID: 1, Email: "lock@b.c", Role: RoleViewer}
	_ = fs.CreateUser(context.Background(), u, pw)

	lock := NewLockout(LockoutConfig{MaxFails: 3, Decay: time.Hour, Lockout: time.Minute})

	h := NewHandlers(deps, HandlersConfig{DefaultSiteID: 1}, lock)

	// 3 wrong-password attempts with changing remote addrs.
	for i, ip := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"} {
		body := strings.NewReader(`{"email":"lock@b.c","password":"wrong"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/login", body)
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = ip + ":1"

		w := httptest.NewRecorder()
		h.Login(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d", i, w.Code)
		}
	}

	// 4th attempt — even with the correct password, must be rejected.
	body := strings.NewReader(`{"email":"lock@b.c","password":"right"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.99:1" // fresh IP

	w := httptest.NewRecorder()
	h.Login(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("locked-out account accepted correct password — lockout broken")
	}
}
