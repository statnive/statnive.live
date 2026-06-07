package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/httpapi"
	"github.com/statnive/statnive.live/internal/specgen"
)

// specStubDeps is the canonical all-stub SpecMode RouterDeps, shared with
// cmd/specgen via specgen.StubDeps() so the two never drift. Tests mutate a
// copy (e.g. d.SpecMode = false) for the production-path cases.
func specStubDeps() httpapi.RouterDeps { return specgen.StubDeps() }

// walkRoutes returns the sorted "METHOD path" list of every route on the mux.
func walkRoutes(t *testing.T, mux *chi.Mux) []string {
	t.Helper()

	var out []string
	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}

	sort.Strings(out)
	return out
}

func TestBuildRouter_SpecMode_NoDeps(t *testing.T) {
	t.Parallel()

	mux, err := httpapi.BuildRouter(specStubDeps())
	if err != nil {
		t.Fatalf("BuildRouter: %v", err)
	}
	if mux == nil {
		t.Fatal("nil mux")
	}

	routes := walkRoutes(t, mux)
	if len(routes) < 60 {
		t.Fatalf("SpecMode walk yielded %d routes, want >= 60", len(routes))
	}

	// Spot-check that flag-gated groups are all present in SpecMode.
	want := []string{
		"POST /api/event",
		"GET /api/user",
		"GET /api/stats/overview",
		"GET /api/admin/users",
		"POST /api/mcp/tokens",
		"POST /api/privacy/consent",
		"GET /privacy",
		"GET /legal/privacy-policy/{lang}",
		"GET /healthz",
		"GET /metrics",
	}
	got := strings.Join(routes, "\n")
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("SpecMode router missing %q", w)
		}
	}
}

// TestBuildRouter_GoldenRouteSet pins the full SpecMode route set. Regenerate
// with STATNIVE_UPDATE_GOLDEN=1 go test ./internal/httpapi/... after an
// intentional route change.
func TestBuildRouter_GoldenRouteSet(t *testing.T) {
	t.Parallel()

	mux, err := httpapi.BuildRouter(specStubDeps())
	if err != nil {
		t.Fatalf("BuildRouter: %v", err)
	}

	got := strings.Join(walkRoutes(t, mux), "\n") + "\n"
	golden := filepath.Join("testdata", "routes.golden")

	if os.Getenv("STATNIVE_UPDATE_GOLDEN") == "1" {
		if mkErr := os.MkdirAll("testdata", 0o755); mkErr != nil {
			t.Fatalf("mkdir testdata: %v", mkErr)
		}
		if wErr := os.WriteFile(golden, []byte(got), 0o644); wErr != nil {
			t.Fatalf("write golden: %v", wErr)
		}
		t.Logf("updated %s", golden)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (run with STATNIVE_UPDATE_GOLDEN=1 to create): %v", err)
	}
	if string(want) != got {
		t.Errorf("route set drift vs %s:\n--- want ---\n%s\n--- got ---\n%s", golden, want, got)
	}
}

// TestBuildRouter_MiddlewareOrder asserts the injected middleware chain order
// on the ingest group (CORS → FastReject → RateLimit → Backpressure) and the
// authed group (Session → APIToken → RequireAuthed). chi.Walk cannot see
// middleware order, so this drives a real request through a recording chain.
func TestBuildRouter_MiddlewareOrder(t *testing.T) {
	t.Parallel()

	var order []string
	rec := func(name string) httpapi.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	d := specStubDeps()
	d.SpecMode = false // exercise the production mount path
	d.CORS = rec("cors")
	d.FastReject = rec("fastreject")
	d.RateLimit = rec("ratelimit")
	d.Backpressure = rec("backpressure")
	d.Session = rec("session")
	d.APIToken = rec("apitoken")
	d.RequireAuthed = rec("requireauthed")

	mux, err := httpapi.BuildRouter(d)
	if err != nil {
		t.Fatalf("BuildRouter: %v", err)
	}

	// Ingest group order.
	order = nil
	req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)
	if got := strings.Join(order, ","); got != "cors,fastreject,ratelimit,backpressure" {
		t.Errorf("ingest middleware order = %q, want cors,fastreject,ratelimit,backpressure", got)
	}

	// Authed group order (/api/user).
	order = nil
	req = httptest.NewRequest(http.MethodGet, "/api/user", nil)
	mux.ServeHTTP(httptest.NewRecorder(), req)
	if got := strings.Join(order, ","); got != "session,apitoken,requireauthed" {
		t.Errorf("/api/user middleware order = %q, want session,apitoken,requireauthed", got)
	}
}

// TestBuildRouter_SecurityMiddlewareWired guards against a SpecMode-leak (or a
// future refactor) that drops a security middleware: a stub RequireAuthed that
// 401s must gate /api/user, and a stub RequireCSRF that 403s must gate the
// privacy write path.
func TestBuildRouter_SecurityMiddlewareWired(t *testing.T) {
	t.Parallel()

	deny := func(code int) httpapi.Middleware {
		return func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
		}
	}

	d := specStubDeps()
	d.SpecMode = false
	d.Flags.PrivacyAPI = true // mount the privacy group in production mode
	d.RequireAuthed = deny(http.StatusUnauthorized)
	d.RequireCSRF = deny(http.StatusForbidden)

	mux, err := httpapi.BuildRouter(d)
	if err != nil {
		t.Fatalf("BuildRouter: %v", err)
	}

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/user", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("/api/user without auth = %d, want 401 (RequireAuthed not wired)", rr.Code)
	}

	rr = httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/privacy/opt-out", nil))
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST /api/privacy/opt-out without CSRF = %d, want 403 (RequireCSRF not wired)", rr.Code)
	}
}

// TestBuildRouter_FlagsOff_NoSpaDep asserts a flags-OFF production boot succeeds
// with nil SPA/privacy deps and omits the flag-gated routes (P1-3 regression).
func TestBuildRouter_FlagsOff_NoSpaDep(t *testing.T) {
	t.Parallel()

	d := specStubDeps()
	d.SpecMode = false
	d.Flags = httpapi.RouterFlags{} // everything off
	d.Spa = nil
	d.PrivacyPage = nil
	d.PrivacyOptOut = nil
	d.PrivacyAccess = nil
	d.PrivacyErase = nil
	d.PrivacyConsent = nil

	mux, err := httpapi.BuildRouter(d)
	if err != nil {
		t.Fatalf("BuildRouter flags-off: %v", err)
	}

	got := strings.Join(walkRoutes(t, mux), "\n")
	for _, absent := range []string{"/app/", "/api/privacy/", "/api/mcp/tokens", "GET /privacy"} {
		if strings.Contains(got, absent) {
			t.Errorf("flags-off router should not contain %q", absent)
		}
	}
	for _, present := range []string{"POST /api/event", "GET /healthz", "GET /api/user", "GET /api/admin/users"} {
		if !strings.Contains(got, present) {
			t.Errorf("flags-off router missing always-on route %q", present)
		}
	}
}
