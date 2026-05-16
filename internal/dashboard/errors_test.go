package dashboard

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
)

// readAuditJSONL drains every JSONL record from the file the audit
// logger writes. The audit.Logger flushes on Close().
func readAuditJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()

	// G304: path is always a t.TempDir()/audit.jsonl from the test
	// setup; no untrusted input ever reaches this call site.
	f, err := os.Open(path) //nolint:gosec // test-only, path is t.TempDir()
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}

	defer func() { _ = f.Close() }()

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 4096), 1<<20)

	var out []map[string]any

	for scan.Scan() {
		var rec map[string]any

		if err := json.Unmarshal(scan.Bytes(), &rec); err != nil {
			t.Fatalf("unmarshal audit line %q: %v", scan.Text(), err)
		}

		out = append(out, rec)
	}

	return out
}

func newDepsWithAuditPath(t *testing.T) (Deps, string) {
	t.Helper()

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	lg, err := audit.New(auditPath)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}

	t.Cleanup(func() { _ = lg.Close() })

	return Deps{
		Store:  &countingStore{},
		Sites:  stubLister{tz: "UTC"},
		Audit:  lg,
		Logger: newSilentLogger(),
	}, auditPath
}

// TestWriteOK_EmitsSiteIDAttr — the canonical Lesson-35 forensic gate.
// Every dashboard.ok event must carry site_id + actor_user_id so a
// cross-tenant attempt is visible after the fact.
func TestWriteOK_EmitsSiteIDAttr(t *testing.T) {
	t.Parallel()

	deps, auditPath := newDepsWithAuditPath(t)

	actor := actorOnSites(auth.RoleAdmin, 4)
	r := authzWith(http.MethodGet, "/api/stats/overview?site=4", actor, 4)
	w := httptest.NewRecorder()

	writeOK(w, r, deps, "overview", map[string]any{"x": 1})

	// Close the audit logger so the file is flushed before we read.
	_ = deps.Audit.Close()

	rows := readAuditJSONL(t, auditPath)

	var found bool

	for _, rec := range rows {
		if rec["event"] != "dashboard.ok" {
			continue
		}

		if got, _ := rec["site_id"].(float64); uint32(got) != 4 {
			t.Errorf("dashboard.ok site_id = %v, want 4", rec["site_id"])
		}

		if got, _ := rec["actor_user_id"].(string); got != actor.UserID.String() {
			t.Errorf("dashboard.ok actor_user_id = %v, want %s", rec["actor_user_id"], actor.UserID.String())
		}

		found = true

		break
	}

	if !found {
		t.Fatalf("dashboard.ok event not emitted; rows = %+v", rows)
	}
}

// TestWriteError_EmitsForbidden_OnSentinelError — the forensic record
// for an IDOR attempt: status 403, EventDashboardForbidden, site_id
// present.
func TestWriteError_EmitsForbidden_OnSentinelError(t *testing.T) {
	t.Parallel()

	deps, auditPath := newDepsWithAuditPath(t)

	actor := actorOnSites(auth.RoleAdmin, 4)
	r := authzWith(http.MethodGet, "/api/stats/overview?site=5", actor, 5)
	w := httptest.NewRecorder()

	writeError(w, r, deps, "overview", errForbiddenSite)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}

	_ = deps.Audit.Close()

	rows := readAuditJSONL(t, auditPath)

	var found bool

	for _, rec := range rows {
		if rec["event"] != "dashboard.forbidden" {
			continue
		}

		if got, _ := rec["site_id"].(float64); uint32(got) != 5 {
			t.Errorf("dashboard.forbidden site_id = %v, want 5", rec["site_id"])
		}

		found = true

		break
	}

	if !found {
		t.Fatalf("dashboard.forbidden event not emitted; rows = %+v", rows)
	}
}

// TestWriteError_OmitsSiteID_IfMissingFromContext — /api/sites listing
// has no ?site; the audit record must not invent a 0. Listing route
// doesn't go through the per-site middleware, so ActiveSiteIDFromContext
// returns false.
func TestWriteError_OmitsSiteID_IfMissingFromContext(t *testing.T) {
	t.Parallel()

	deps, auditPath := newDepsWithAuditPath(t)

	actor := &auth.User{UserID: uuid.New(), Sites: map[uint32]auth.Role{1: auth.RoleAdmin}}
	r := httptest.NewRequest(http.MethodGet, "/api/sites", nil)
	r = r.WithContext(auth.WithSession(r.Context(), actor, &auth.Session{}))
	w := httptest.NewRecorder()

	writeOK(w, r, deps, "sites", sitesResponse{})

	_ = deps.Audit.Close()

	rows := readAuditJSONL(t, auditPath)

	for _, rec := range rows {
		if rec["event"] != "dashboard.ok" {
			continue
		}

		if _, has := rec["site_id"]; has {
			t.Errorf("dashboard.ok carried site_id on /api/sites route: %v", rec)
		}

		return
	}

	t.Fatal("dashboard.ok event not emitted for sites listing")
}
