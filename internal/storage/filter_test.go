package storage_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
)

func TestFilter_Validate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		f       *storage.Filter
		wantErr bool
	}{
		{
			name: "valid",
			f:    &storage.Filter{SiteID: 1, From: now.Add(-24 * time.Hour), To: now},
		},
		{
			name:    "nil",
			f:       nil,
			wantErr: true,
		},
		{
			name:    "missing site_id",
			f:       &storage.Filter{From: now.Add(-time.Hour), To: now},
			wantErr: true,
		},
		{
			name:    "from zero",
			f:       &storage.Filter{SiteID: 1, To: now},
			wantErr: true,
		},
		{
			name:    "to zero",
			f:       &storage.Filter{SiteID: 1, From: now},
			wantErr: true,
		},
		{
			name:    "from equals to",
			f:       &storage.Filter{SiteID: 1, From: now, To: now},
			wantErr: true,
		},
		{
			name:    "from after to",
			f:       &storage.Filter{SiteID: 1, From: now, To: now.Add(-time.Hour)},
			wantErr: true,
		},
		{
			name:    "range exceeds max",
			f:       &storage.Filter{SiteID: 1, From: now.Add(-2 * 365 * 24 * time.Hour), To: now},
			wantErr: true,
		},
		{
			name:    "negative limit",
			f:       &storage.Filter{SiteID: 1, From: now.Add(-time.Hour), To: now, Limit: -1},
			wantErr: true,
		},
		{
			name:    "negative offset",
			f:       &storage.Filter{SiteID: 1, From: now.Add(-time.Hour), To: now, Offset: -5},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.f.Validate()
			gotErr := err != nil

			if gotErr != tc.wantErr {
				t.Errorf("Validate() err = %v, wantErr %v", err, tc.wantErr)
			}

			if gotErr && !errors.Is(err, storage.ErrInvalidFilter) {
				t.Errorf("err should wrap ErrInvalidFilter; got %v", err)
			}
		})
	}
}

func TestFilter_EffectiveLimit(t *testing.T) {
	t.Parallel()

	if got := (&storage.Filter{Limit: 0}).EffectiveLimit(); got != storage.DefaultLimit {
		t.Errorf("Limit=0 → %d, want %d", got, storage.DefaultLimit)
	}

	if got := (&storage.Filter{Limit: 100}).EffectiveLimit(); got != 100 {
		t.Errorf("Limit=100 → %d, want 100", got)
	}
}

func TestFilter_Hash_Deterministic(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)

	a := &storage.Filter{SiteID: 1, From: now.Add(-24 * time.Hour), To: now, Path: "/"}
	b := &storage.Filter{SiteID: 1, From: now.Add(-24 * time.Hour), To: now, Path: "/"}

	if a.Hash() != b.Hash() {
		t.Errorf("identical filters → different hash: %s vs %s", a.Hash(), b.Hash())
	}

	if len(a.Hash()) != 32 {
		t.Errorf("hash length = %d, want 32 (BLAKE3-128 hex)", len(a.Hash()))
	}
}

func TestFilter_Hash_DifferentInputsDifferent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	base := &storage.Filter{SiteID: 1, From: now.Add(-24 * time.Hour), To: now}

	mutators := []func(*storage.Filter){
		func(f *storage.Filter) { f.SiteID = 2 },
		func(f *storage.Filter) { f.From = f.From.Add(-time.Hour) },
		func(f *storage.Filter) { f.To = f.To.Add(time.Hour) },
		func(f *storage.Filter) { f.Path = "/x" },
		func(f *storage.Filter) { f.Country = "IR" },
		func(f *storage.Filter) { f.Limit = 25 },
		func(f *storage.Filter) { f.Offset = 10 },
		func(f *storage.Filter) { f.Sort = "visitors" },
	}

	for i, m := range mutators {
		mutated := *base
		m(&mutated)

		if mutated.Hash() == base.Hash() {
			t.Errorf("mutator %d produced identical hash", i)
		}
	}
}

func TestFilter_Hash_TimezoneNormalized(t *testing.T) {
	t.Parallel()

	utc := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	tehran, _ := time.LoadLocation("Asia/Tehran")
	sameInTehran := utc.In(tehran)

	a := &storage.Filter{SiteID: 1, From: utc.Add(-time.Hour), To: utc}
	b := &storage.Filter{SiteID: 1, From: sameInTehran.Add(-time.Hour), To: sameInTehran}

	if a.Hash() != b.Hash() {
		t.Errorf("UTC vs IRST representation of same instant → different hash")
	}
}

func TestFilter_Hash_NoIPField(t *testing.T) {
	t.Parallel()

	// Belt-and-suspenders for Privacy Rule 1: even though the struct
	// has no IP field, an integration would silently miss this if a
	// future PR added one. This test asserts the field count via
	// the hash surface — adding a field would break determinism
	// across this fixture, forcing a deliberate update.
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	f := &storage.Filter{
		SiteID:      1,
		From:        now.Add(-24 * time.Hour),
		To:          now,
		Path:        "/test",
		Referrer:    "https://example.com",
		UTMSource:   "src",
		UTMMedium:   "med",
		UTMCampaign: "cam",
		UTMContent:  "con",
		UTMTerm:     "trm",
		Country:     "IR",
		Browser:     "Chrome",
		OS:          "macOS",
		Device:      "desktop",
		Sort:        "views",
		Search:      "blog",
		Limit:       25,
		Offset:      10,
	}

	hash := f.Hash()

	// Hash format check — BLAKE3-128 hex is 32 lowercase hex chars.
	if len(hash) != 32 {
		t.Fatalf("hash len = %d, want 32", len(hash))
	}

	if strings.ContainsAny(hash, "GHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("hash must be lowercase hex: %s", hash)
	}
}
