package admin

// Coverage for the allowed_origins surface on POST /api/admin/sites +
// PATCH /api/admin/sites/{id}. The collision 409 path is the
// highest-value invariant — without it two sites could register the
// same origin and CORS would non-deterministically route to whichever
// site OriginIndex rebuilt last.

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/statnive/statnive.live/internal/sites"
)

func TestSites_Create_WithAllowedOrigins(t *testing.T) {
	t.Parallel()

	deps, ss := newSitesDeps()
	deps.OriginIndex = sites.NewOriginIndex()

	h := NewSites(deps)
	body := `{"hostname":"televika.com","allowed_origins":["https://televika.com","https://www.televika.com"]}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites", body, newTestAdmin(), nil))

	if w.Code != 201 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var got siteAdminResponse

	_ = json.Unmarshal(w.Body.Bytes(), &got)

	if len(got.AllowedOrigins) != 2 {
		t.Fatalf("response AllowedOrigins len = %d, want 2", len(got.AllowedOrigins))
	}

	// The DAO stores the NormalizeOrigin canonical form — uppercased
	// or trailing-slash inputs would converge here. Body used a clean
	// form so the round-trip just confirms persistence.
	list, _ := ss.ListAdmin(context.Background())
	if len(list) != 1 || len(list[0].AllowedOrigins) != 2 {
		t.Errorf("expected 1 site with 2 origins, got %+v", list)
	}

	// OriginIndex rebuild was triggered post-write: Lookup of the
	// canonical origin must resolve to the new site_id.
	if id, ok := deps.OriginIndex.Lookup("https://televika.com"); !ok || id != got.SiteID {
		t.Errorf("OriginIndex.Lookup(televika) = (%d, %v); want (%d, true)", id, ok, got.SiteID)
	}
}

func TestSites_Create_InvalidOriginReturns400(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	h := NewSites(deps)
	body := `{"hostname":"televika.com","allowed_origins":["http://televika.com"]}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites", body, newTestAdmin(), nil))

	if w.Code != 400 {
		t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}

func TestSites_Create_OriginCollisionReturns409(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	deps.OriginIndex = sites.NewOriginIndex()

	h := NewSites(deps)

	// Seed first site with televika origins.
	w1 := httptest.NewRecorder()
	h.Create(w1, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"televika.com","allowed_origins":["https://televika.com"]}`, newTestAdmin(), nil))

	if w1.Code != 201 {
		t.Fatalf("first create: %d %s", w1.Code, w1.Body.String())
	}

	// Second site tries to claim the same origin — must 409.
	w2 := httptest.NewRecorder()
	h.Create(w2, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"other.example","allowed_origins":["https://televika.com"]}`, newTestAdmin(), nil))

	if w2.Code != 409 {
		t.Fatalf("collision create status = %d, want 409; body = %s", w2.Code, w2.Body.String())
	}
}

func TestSites_Patch_AllowedOrigins(t *testing.T) {
	t.Parallel()

	deps, ss := newSitesDeps()
	deps.OriginIndex = sites.NewOriginIndex()

	h := NewSites(deps)

	// Seed a site with no origins yet.
	w0 := httptest.NewRecorder()
	h.Create(w0, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"televika.com"}`, newTestAdmin(), nil))

	if w0.Code != 201 {
		t.Fatalf("seed: %d %s", w0.Code, w0.Body.String())
	}

	var seeded siteAdminResponse

	_ = json.Unmarshal(w0.Body.Bytes(), &seeded)

	// PATCH adds origins.
	patchBody := `{"allowed_origins":["https://televika.com","https://www.televika.com"]}`

	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/sites/"+strconv.Itoa(int(seeded.SiteID)), patchBody, newTestAdmin(), map[string]string{"id": strconv.Itoa(int(seeded.SiteID))}))

	if w.Code != 200 {
		t.Fatalf("patch status = %d, body = %s", w.Code, w.Body.String())
	}

	saved, _ := ss.LookupSiteByID(context.Background(), seeded.SiteID)
	if len(saved.AllowedOrigins) != 2 {
		t.Errorf("saved origins = %v, want 2", saved.AllowedOrigins)
	}

	// OriginIndex rebuilt post-PATCH.
	if id, ok := deps.OriginIndex.Lookup("https://www.televika.com"); !ok || id != seeded.SiteID {
		t.Errorf("OriginIndex.Lookup post-patch = (%d, %v)", id, ok)
	}
}

func TestSites_Patch_OriginCollisionReturns409(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	h := NewSites(deps)

	// Site A claims televika.
	wa := httptest.NewRecorder()
	h.Create(wa, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"a.example","allowed_origins":["https://televika.com"]}`, newTestAdmin(), nil))

	if wa.Code != 201 {
		t.Fatalf("seed A: %d %s", wa.Code, wa.Body.String())
	}

	// Site B exists with no origins.
	wb := httptest.NewRecorder()
	h.Create(wb, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"b.example"}`, newTestAdmin(), nil))

	if wb.Code != 201 {
		t.Fatalf("seed B: %d %s", wb.Code, wb.Body.String())
	}

	var seededB siteAdminResponse

	_ = json.Unmarshal(wb.Body.Bytes(), &seededB)

	// B tries to claim the same origin as A.
	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/sites/"+strconv.Itoa(int(seededB.SiteID)),
		`{"allowed_origins":["https://televika.com"]}`, newTestAdmin(),
		map[string]string{"id": strconv.Itoa(int(seededB.SiteID))}))

	if w.Code != 409 {
		t.Fatalf("collision patch status = %d, want 409; body = %s", w.Code, w.Body.String())
	}
}

// TestSites_Patch_NoOp_SameOriginsDoesNotSelfCollide pins the
// ignoreID exception in checkOriginCollision — a no-op PATCH that
// re-asserts the site's own origins must NOT be self-rejected as a
// collision with itself.
func TestSites_Patch_NoOp_SameOriginsDoesNotSelfCollide(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	h := NewSites(deps)

	wa := httptest.NewRecorder()
	h.Create(wa, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"a.example","allowed_origins":["https://televika.com"]}`, newTestAdmin(), nil))

	if wa.Code != 201 {
		t.Fatalf("seed: %d %s", wa.Code, wa.Body.String())
	}

	var seeded siteAdminResponse

	_ = json.Unmarshal(wa.Body.Bytes(), &seeded)

	// Repeat the same set — should be a clean 200, not a collision.
	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/sites/"+strconv.Itoa(int(seeded.SiteID)),
		`{"allowed_origins":["https://televika.com"]}`, newTestAdmin(),
		map[string]string{"id": strconv.Itoa(int(seeded.SiteID))}))

	if w.Code != 200 {
		t.Errorf("no-op patch status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

// TestSites_Patch_AllowedOriginsCapEnforced — Validate's 10-entry
// ceiling fires for an 11-entry PATCH.
func TestSites_Patch_AllowedOriginsCapEnforced(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	h := NewSites(deps)

	w0 := httptest.NewRecorder()
	h.Create(w0, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"a.example"}`, newTestAdmin(), nil))

	if w0.Code != 201 {
		t.Fatalf("seed: %d %s", w0.Code, w0.Body.String())
	}

	var seeded siteAdminResponse

	_ = json.Unmarshal(w0.Body.Bytes(), &seeded)

	body := `{"allowed_origins":[` +
		`"https://o1.example","https://o2.example","https://o3.example",` +
		`"https://o4.example","https://o5.example","https://o6.example",` +
		`"https://o7.example","https://o8.example","https://o9.example",` +
		`"https://o10.example","https://o11.example"]}`

	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/sites/"+strconv.Itoa(int(seeded.SiteID)),
		body, newTestAdmin(), map[string]string{"id": strconv.Itoa(int(seeded.SiteID))}))

	if w.Code != 400 {
		t.Errorf("11-entry patch status = %d, want 400; body = %s", w.Code, w.Body.String())
	}
}
