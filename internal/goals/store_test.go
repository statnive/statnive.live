package goals

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestSQLConstants_LeadWithSiteID grep-pins the tenancy choke point:
// every SQL read constant must start its WHERE clause with `site_id`.
// Mirrors the `tenancy-grep` Makefile target but scoped to this
// package's SQL.
func TestSQLConstants_LeadWithSiteID(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"sqlGet":        sqlGet,
		"sqlList":       sqlList,
		"sqlListActive": sqlListActive,
	}

	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			where := strings.Index(q, "WHERE")
			if where == -1 {
				t.Fatalf("%s: no WHERE clause", name)
			}

			after := strings.TrimSpace(q[where+len("WHERE"):])
			if !strings.HasPrefix(after, "site_id") {
				t.Errorf("%s: first WHERE predicate is %q, want site_id", name, after[:min(40, len(after))])
			}
		})
	}
}

func TestValidate_Happy(t *testing.T) {
	t.Parallel()

	g := &Goal{
		SiteID:    1,
		Name:      "Purchase",
		MatchType: MatchTypeEventNameEquals,
		Pattern:   "purchase",
	}

	if err := validate(g); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidate_RejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		g    *Goal
	}{
		{"zero site_id", &Goal{Name: "A", MatchType: MatchTypeEventNameEquals, Pattern: "a"}},
		{"empty name", &Goal{SiteID: 1, MatchType: MatchTypeEventNameEquals, Pattern: "a"}},
		{"unknown match_type", &Goal{SiteID: 1, Name: "A", MatchType: "path_regex", Pattern: "a"}},
		{"empty pattern", &Goal{SiteID: 1, Name: "A", MatchType: MatchTypeEventNameEquals}},
		{
			"pattern too long",
			&Goal{
				SiteID: 1, Name: "A",
				MatchType: MatchTypeEventNameEquals,
				Pattern:   strings.Repeat("x", MaxPatternLen+1),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := validate(tc.g); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("%s: got %v, want ErrInvalidInput", tc.name, err)
			}
		})
	}
}

func createOneTestGoal(t *testing.T, fs *fakeStore) *Goal {
	t.Helper()

	g := &Goal{
		SiteID: 1, Name: "Purchase", MatchType: MatchTypeEventNameEquals,
		Pattern: "purchase", ValueRials: 500_000, Enabled: true,
	}

	if err := fs.Create(context.Background(), g); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if g.GoalID == uuid.Nil {
		t.Fatal("Create did not assign goal_id")
	}

	return g
}

func TestFakeStore_CreateGetList(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	ctx := context.Background()
	g := createOneTestGoal(t, fs)

	got, err := fs.Get(ctx, 1, g.GoalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != "Purchase" {
		t.Errorf("Get.Name = %q", got.Name)
	}

	list, err := fs.List(ctx, 1)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: len=%d err=%v", len(list), err)
	}

	active, err := fs.ListActive(ctx)
	if err != nil || len(active) != 1 {
		t.Fatalf("ListActive: len=%d err=%v", len(active), err)
	}
}

func TestFakeStore_UpdateThenDisable(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	ctx := context.Background()
	g := createOneTestGoal(t, fs)

	g.Name = "Purchase-v2"
	if err := fs.Update(ctx, g); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := fs.Get(ctx, 1, g.GoalID)
	if got.Name != "Purchase-v2" {
		t.Errorf("Update Name = %q", got.Name)
	}

	if err := fs.Disable(ctx, 1, g.GoalID); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	active, _ := fs.ListActive(ctx)
	if len(active) != 0 {
		t.Errorf("after Disable, ListActive len=%d, want 0", len(active))
	}

	got, _ = fs.Get(ctx, 1, g.GoalID)
	if got.Enabled {
		t.Error("Disable did not flip enabled to false")
	}
}

func TestFakeStore_GetCrossSiteReturnsNotFound(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	ctx := context.Background()

	g := &Goal{SiteID: 1, Name: "A", MatchType: MatchTypeEventNameEquals, Pattern: "a", Enabled: true}
	_ = fs.Create(ctx, g)

	// Request same goal_id but different site_id → must 404.
	if _, err := fs.Get(ctx, 2, g.GoalID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-site Get: got %v, want ErrNotFound", err)
	}
}

// min is a Go 1.21+ builtin — no local helper needed.
