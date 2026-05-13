package privacy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/statnive/statnive.live/internal/sites"
)

type fakeSitesResolver struct {
	siteID uint32
	policy sites.SitePolicy
	err    error
}

func (f *fakeSitesResolver) LookupSitePolicy(_ context.Context, _ string) (uint32, sites.SitePolicy, error) {
	if f.err != nil {
		return 0, sites.SitePolicy{}, f.err
	}

	return f.siteID, f.policy, nil
}

func newTestHandlers(t *testing.T) *Handlers {
	t.Helper()

	supp, err := NewSuppressionList(filepath.Join(t.TempDir(), "suppression.wal"))
	if err != nil {
		t.Fatalf("suppression: %v", err)
	}
	t.Cleanup(func() { _ = supp.Close() })

	h, err := NewHandlers(Config{
		Sites:        &fakeSitesResolver{siteID: 42},
		MasterSecret: []byte("test-master-secret-32-bytes-long!"),
		Suppression:  supp,
	})
	if err != nil {
		t.Fatalf("new handlers: %v", err)
	}

	return h
}

func TestOptOut_SetsCookieAndSuppresses(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/privacy/opt-out", nil)
	req.AddCookie(&http.Cookie{Name: "_statnive", Value: "550e8400-e29b-41d4-a716-446655440000"})
	rec := httptest.NewRecorder()

	h.OptOut(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	var optoutCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "_statnive_optout" {
			optoutCookie = c
			break
		}
	}

	if optoutCookie == nil {
		t.Fatalf("missing _statnive_optout cookie in response")
	}

	if optoutCookie.Value != "v1" {
		t.Errorf("cookie value = %q, want v1", optoutCookie.Value)
	}

	if !optoutCookie.HttpOnly {
		t.Errorf("opt-out cookie should be HttpOnly")
	}

	if h.cfg.Suppression.Len() != 1 {
		t.Errorf("suppression Len = %d, want 1", h.cfg.Suppression.Len())
	}
}

func TestOptOut_RejectsRequestWithoutCookie(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/privacy/opt-out", nil)
	rec := httptest.NewRecorder()

	h.OptOut(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestOptOut_RejectsUnknownHost(t *testing.T) {
	t.Parallel()

	supp, err := NewSuppressionList(filepath.Join(t.TempDir(), "suppression.wal"))
	if err != nil {
		t.Fatalf("suppression: %v", err)
	}
	t.Cleanup(func() { _ = supp.Close() })

	h, _ := NewHandlers(Config{
		Sites:        &fakeSitesResolver{err: sites.ErrUnknownHostname},
		MasterSecret: []byte("test-master-secret-32-bytes-long!"),
		Suppression:  supp,
	})

	req := httptest.NewRequest(http.MethodPost, "https://unknown.example/api/privacy/opt-out", nil)
	req.AddCookie(&http.Cookie{Name: "_statnive", Value: "v"})
	rec := httptest.NewRecorder()

	h.OptOut(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestAccess_AcknowledgesRequest(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodGet, "https://example.com/api/privacy/access", nil)
	req.AddCookie(&http.Cookie{Name: "_statnive", Value: "550e8400-e29b-41d4-a716-446655440000"})
	rec := httptest.NewRecorder()

	h.Access(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body["status"] != "received" {
		t.Errorf("status = %v, want received", body["status"])
	}

	if got, ok := body["cookie_id_hash"].(string); !ok || got == "" {
		t.Errorf("missing cookie_id_hash in body: %+v", body)
	}
}

func TestErase_NotConfiguredReturns503(t *testing.T) {
	t.Parallel()

	h := newTestHandlers(t)

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/privacy/erase", nil)
	req.AddCookie(&http.Cookie{Name: "_statnive", Value: "v"})
	rec := httptest.NewRecorder()

	h.Erase(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (erase enumerator not wired)", rec.Code)
	}
}

func TestNewHandlers_RequiresDeps(t *testing.T) {
	t.Parallel()

	supp, err := NewSuppressionList(filepath.Join(t.TempDir(), "suppression.wal"))
	if err != nil {
		t.Fatalf("suppression: %v", err)
	}
	t.Cleanup(func() { _ = supp.Close() })

	cases := []struct {
		name string
		cfg  Config
	}{
		{"missing sites", Config{MasterSecret: []byte("x"), Suppression: supp}},
		{"missing master secret", Config{Sites: &fakeSitesResolver{}, Suppression: supp}},
		{"missing suppression", Config{Sites: &fakeSitesResolver{}, MasterSecret: []byte("x")}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewHandlers(c.cfg); err == nil {
				t.Errorf("expected error for %s", c.name)
			}
		})
	}
}
