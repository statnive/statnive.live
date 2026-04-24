package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"
)

func newSitesDeps() (Deps, *fakeSitesStore) {
	ss := newFakeSitesStore()

	return Deps{Sites: ss}, ss
}

func TestSites_CreateHappy(t *testing.T) {
	t.Parallel()

	deps, ss := newSitesDeps()
	admin := newTestAdmin()

	h := NewSites(deps)
	body := `{"hostname":"Example.com","tz":"UTC"}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites", body, admin, nil))

	if w.Code != 201 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var got siteAdminResponse

	_ = json.Unmarshal(w.Body.Bytes(), &got)

	if got.Hostname != "example.com" {
		t.Errorf("hostname not normalized to lower: %q", got.Hostname)
	}

	if got.Slug == "" {
		t.Error("slug should be auto-generated when omitted")
	}

	if !got.Enabled {
		t.Error("new sites must be enabled by default")
	}

	if got.TZ != "UTC" {
		t.Errorf("tz = %q, want UTC (caller-supplied)", got.TZ)
	}

	if got.SiteID == 0 {
		t.Error("site_id must be non-zero after insert")
	}

	_, _ = ss.ListAdmin(context.Background())
}

// TestSites_CreateHostnameTaken + TestSites_CreateSlugTaken diverge
// only on which body field collides (hostname vs slug) — a table
// pair isn't worth the ceremony for two cases, and the explicit
// probes read clearer in failure output.
//
//nolint:dupl // intentional symmetry; see comment above.
func TestSites_CreateHostnameTaken(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	w1 := httptest.NewRecorder()
	h.Create(w1, adminRequest(t, "POST", "/api/admin/sites", `{"hostname":"dup.example"}`, admin, nil))

	if w1.Code != 201 {
		t.Fatalf("first create failed: %d", w1.Code)
	}

	w2 := httptest.NewRecorder()
	h.Create(w2, adminRequest(t, "POST", "/api/admin/sites", `{"hostname":"dup.example"}`, admin, nil))

	if w2.Code != 409 {
		t.Fatalf("second create status = %d, want 409", w2.Code)
	}
}

//nolint:dupl // symmetric with TestSites_CreateHostnameTaken.
func TestSites_CreateSlugTaken(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	w1 := httptest.NewRecorder()
	h.Create(w1, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"foo.example","slug":"myslug"}`, admin, nil))

	if w1.Code != 201 {
		t.Fatalf("first create failed: %d", w1.Code)
	}

	w2 := httptest.NewRecorder()
	h.Create(w2, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"bar.example","slug":"myslug"}`, admin, nil))

	if w2.Code != 409 {
		t.Fatalf("slug collision status = %d, want 409", w2.Code)
	}
}

func TestSites_CreateInvalidHostname(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"inv lid!!!"}`, admin, nil))

	if w.Code != 400 {
		t.Fatalf("invalid hostname status = %d, want 400", w.Code)
	}
}

func TestSites_CreateRejectsUnknownField(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	w := httptest.NewRecorder()
	// `enabled` is server-set — body must NOT accept it (F4 guard).
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"ok.example","enabled":false}`, admin, nil))

	if w.Code != 400 {
		t.Fatalf("unknown-field status = %d, want 400", w.Code)
	}
}

func TestSites_List(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	for _, host := range []string{"a.example", "b.example", "c.example"} {
		w := httptest.NewRecorder()
		h.Create(w, adminRequest(t, "POST", "/api/admin/sites",
			`{"hostname":"`+host+`"}`, admin, nil))

		if w.Code != 201 {
			t.Fatalf("seed %s failed: %d", host, w.Code)
		}
	}

	w := httptest.NewRecorder()
	h.List(w, adminRequest(t, "GET", "/api/admin/sites", "", admin, nil))

	if w.Code != 200 {
		t.Fatalf("list status = %d", w.Code)
	}

	var body struct {
		Sites []siteAdminResponse `json:"sites"`
	}

	_ = json.Unmarshal(w.Body.Bytes(), &body)

	if len(body.Sites) != 3 {
		t.Errorf("expected 3 sites, got %d", len(body.Sites))
	}
}

func TestSites_UpdateToggleEnabled(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	cw := httptest.NewRecorder()
	h.Create(cw, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"toggleme.example"}`, admin, nil))

	if cw.Code != 201 {
		t.Fatalf("create: %d", cw.Code)
	}

	var created siteAdminResponse

	_ = json.Unmarshal(cw.Body.Bytes(), &created)

	// Disable.
	uw := httptest.NewRecorder()
	h.Update(uw, adminRequest(t, "PATCH",
		"/api/admin/sites/"+strconv.FormatUint(uint64(created.SiteID), 10),
		`{"enabled":false}`, admin,
		map[string]string{"id": strconv.FormatUint(uint64(created.SiteID), 10)}))

	if uw.Code != 200 {
		t.Fatalf("disable status = %d body = %s", uw.Code, uw.Body.String())
	}

	var got siteAdminResponse

	_ = json.Unmarshal(uw.Body.Bytes(), &got)

	if got.Enabled {
		t.Error("site should be disabled after update")
	}

	// Re-enable.
	rw := httptest.NewRecorder()
	h.Update(rw, adminRequest(t, "PATCH",
		"/api/admin/sites/"+strconv.FormatUint(uint64(created.SiteID), 10),
		`{"enabled":true}`, admin,
		map[string]string{"id": strconv.FormatUint(uint64(created.SiteID), 10)}))

	if rw.Code != 200 {
		t.Fatalf("enable status = %d", rw.Code)
	}
}

func TestSites_UpdateNotFound(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/sites/9999",
		`{"enabled":false}`, admin, map[string]string{"id": "9999"}))

	if w.Code != 404 {
		t.Fatalf("update missing site status = %d, want 404", w.Code)
	}
}

func TestSites_UpdateBadID(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/sites/abc",
		`{"enabled":false}`, admin, map[string]string{"id": "abc"}))

	if w.Code != 400 {
		t.Fatalf("bad id status = %d, want 400", w.Code)
	}
}
