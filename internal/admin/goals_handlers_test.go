package admin

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/goals"
)

func newGoalsDeps() (Deps, *fakeGoalsStore) {
	as := newFakeAuthStore()
	gs := newFakeGoalsStore()

	return Deps{Auth: as, Goals: gs}, gs
}

func TestGoals_List(t *testing.T) {
	t.Parallel()

	deps, gs := newGoalsDeps()
	admin := newTestAdmin()
	ctx := context.Background()

	_ = gs.Create(ctx, &goals.Goal{
		SiteID: 1, Name: "Purchase", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "purchase", Enabled: true,
	})
	_ = gs.Create(ctx, &goals.Goal{
		SiteID: 2, Name: "Other-Site", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "x", Enabled: true,
	})

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.List(w, adminRequest(t, "GET", "/api/admin/goals", "", admin, nil))

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	var body struct {
		Goals []goalResponse `json:"goals"`
	}

	_ = json.Unmarshal(w.Body.Bytes(), &body)

	if len(body.Goals) != 1 || body.Goals[0].Name != "Purchase" {
		t.Errorf("unexpected goals list: %+v", body.Goals)
	}
}

func TestGoals_CreateHappy(t *testing.T) {
	t.Parallel()

	deps, gs := newGoalsDeps()
	admin := newTestAdmin()

	h := NewGoals(deps)
	body := `{"name":"Purchase","match_type":"event_name_equals","pattern":"purchase","value_rials":500000,"enabled":true}`

	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/goals", body, admin, nil))

	if w.Code != 201 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var got goalResponse
	_ = json.Unmarshal(w.Body.Bytes(), &got)

	if got.SiteID != admin.SiteID {
		t.Errorf("site_id = %d, want %d (session-derived)", got.SiteID, admin.SiteID)
	}

	list, _ := gs.List(context.Background(), admin.SiteID)
	if len(list) != 1 {
		t.Errorf("goal not persisted: len = %d", len(list))
	}
}

// TestGoals_Create_RejectsOversizedPattern — write-time rejection of
// any pattern > MaxPatternLen. 200 chars rejected even though the
// v1-only `event_name_equals` doesn't compile a regex.
func TestGoals_Create_RejectsOversizedPattern(t *testing.T) {
	t.Parallel()

	deps, _ := newGoalsDeps()
	admin := newTestAdmin()

	pattern := strings.Repeat("x", goals.MaxPatternLen+1)
	body := `{"name":"A","match_type":"event_name_equals","pattern":"` + pattern + `","value_rials":0,"enabled":true}`

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/goals", body, admin, nil))

	if w.Code != 400 {
		t.Errorf("oversized pattern: status = %d, want 400", w.Code)
	}
}

// TestGoals_Create_RejectsUnknownMatchType — v1 accepts only
// `event_name_equals`. `path_regex` must be rejected at write time.
func TestGoals_Create_RejectsUnknownMatchType(t *testing.T) {
	t.Parallel()

	deps, _ := newGoalsDeps()
	admin := newTestAdmin()

	body := `{"name":"A","match_type":"path_regex","pattern":"^.*$","value_rials":0,"enabled":true}`

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/goals", body, admin, nil))

	if w.Code != 400 {
		t.Errorf("unknown match_type: status = %d, want 400", w.Code)
	}
}

// TestGoals_Create_RejectsMassAssignment — admin can't slip site_id=99.
func TestGoals_Create_RejectsMassAssignment(t *testing.T) {
	t.Parallel()

	deps, _ := newGoalsDeps()
	admin := newTestAdmin()

	body := `{"name":"A","match_type":"event_name_equals","pattern":"p","value_rials":0,"enabled":true,"site_id":99}`

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/goals", body, admin, nil))

	if w.Code != 400 {
		t.Errorf("mass-assignment: status = %d, want 400", w.Code)
	}
}

func TestGoals_UpdateHappy(t *testing.T) {
	t.Parallel()

	deps, gs := newGoalsDeps()
	admin := newTestAdmin()
	ctx := context.Background()

	g := &goals.Goal{
		SiteID: 1, Name: "Purchase", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "purchase", Enabled: true,
	}
	_ = gs.Create(ctx, g)

	body := `{"name":"Purchase-v2","match_type":"event_name_equals","pattern":"purchase","value_rials":1000000,"enabled":true}`

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/goals/"+g.GoalID.String(),
		body, admin, map[string]string{"id": g.GoalID.String()}))

	if w.Code != 200 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	updated, _ := gs.Get(ctx, 1, g.GoalID)
	if updated.Name != "Purchase-v2" || updated.ValueRials != 1_000_000 {
		t.Errorf("not updated: %+v", updated)
	}
}

func TestGoals_Update_CrossSiteReturnsNotFound(t *testing.T) {
	t.Parallel()

	deps, gs := newGoalsDeps()
	admin := newTestAdmin()

	// Goal owned by a different site.
	other := &goals.Goal{
		SiteID: 2, Name: "O", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "x", Enabled: true,
	}
	_ = gs.Create(context.Background(), other)

	body := `{"name":"hijacked","match_type":"event_name_equals","pattern":"x","value_rials":0,"enabled":true}`

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.Update(w, adminRequest(t, "PATCH", "/api/admin/goals/"+other.GoalID.String(),
		body, admin, map[string]string{"id": other.GoalID.String()}))

	if w.Code != 404 {
		t.Errorf("cross-site Update: status = %d, want 404", w.Code)
	}
}

func TestGoals_Disable(t *testing.T) {
	t.Parallel()

	deps, gs := newGoalsDeps()
	admin := newTestAdmin()
	ctx := context.Background()

	g := &goals.Goal{
		SiteID: 1, Name: "P", MatchType: goals.MatchTypeEventNameEquals,
		Pattern: "p", Enabled: true,
	}
	_ = gs.Create(ctx, g)

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.Disable(w, adminRequest(t, "POST", "/api/admin/goals/"+g.GoalID.String()+"/disable",
		"", admin, map[string]string{"id": g.GoalID.String()}))

	if w.Code != 204 {
		t.Fatalf("status = %d", w.Code)
	}

	got, _ := gs.Get(ctx, 1, g.GoalID)
	if got.Enabled {
		t.Error("Disable did not flip enabled")
	}
}

func TestGoals_InvalidUUID_Returns400(t *testing.T) {
	t.Parallel()

	deps, _ := newGoalsDeps()
	admin := newTestAdmin()

	h := NewGoals(deps)
	w := httptest.NewRecorder()
	h.Disable(w, adminRequest(t, "POST", "/api/admin/goals/not-a-uuid/disable",
		"", admin, map[string]string{"id": "not-a-uuid"}))

	if w.Code != 400 {
		t.Errorf("invalid UUID: status = %d", w.Code)
	}

	_ = uuid.Nil // keep import
}
