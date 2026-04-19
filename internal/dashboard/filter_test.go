package dashboard

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFilterFromRequest_RequiresSite(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/api/stats/overview", nil)

	_, err := filterFromRequest(r)
	if !errors.Is(err, errBadInput) {
		t.Errorf("err = %v, want errBadInput", err)
	}
}

func TestFilterFromRequest_DefaultsLast7Days(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/api/stats/overview?site=1", nil)

	f, err := filterFromRequest(r)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	if got := f.To.Sub(f.From); got != 7*24*time.Hour {
		t.Errorf("range = %s, want 168h", got)
	}
}

func TestFilterFromRequest_ParsesAllDimensions(t *testing.T) {
	t.Parallel()

	url := "/api/stats/overview?site=42&from=2026-04-01&to=2026-04-08" +
		"&path=/foo&referrer=https://example.com" +
		"&utm_source=src&utm_medium=med&utm_campaign=cam&utm_content=con&utm_term=trm" +
		"&country=IR&browser=Chrome&os=macOS&device=desktop" +
		"&sort=visitors&search=blog&limit=25&offset=10"

	r := httptest.NewRequest("GET", url, nil)

	f, err := filterFromRequest(r)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	if f.SiteID != 42 {
		t.Errorf("SiteID = %d, want 42", f.SiteID)
	}

	if f.Path != "/foo" || f.UTMCampaign != "cam" || f.Country != "IR" {
		t.Errorf("dimension fields not populated correctly: %+v", f)
	}

	if f.Limit != 25 || f.Offset != 10 {
		t.Errorf("Limit/Offset = %d/%d, want 25/10", f.Limit, f.Offset)
	}
}

func TestFilterFromRequest_BadDate(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/api/stats/overview?site=1&from=not-a-date", nil)

	_, err := filterFromRequest(r)
	if err == nil {
		t.Fatal("expected error for unparseable date")
	}

	if !errors.Is(err, errBadInput) {
		t.Errorf("err = %v, want errBadInput", err)
	}
}

func TestFilterFromRequest_FromOnly_ToDefaultsToTomorrow(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest("GET", "/api/stats/overview?site=1&from=2026-04-01", nil)

	f, err := filterFromRequest(r)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// To should be > today UTC; from is 2026-04-01 IRST.
	if !f.To.After(f.From) {
		t.Errorf("To (%s) should be after From (%s)", f.To, f.From)
	}
}

func TestFilterFromRequest_RangeTooLarge(t *testing.T) {
	t.Parallel()

	// 2-year range exceeds Filter.MaxRange of 1 year.
	r := httptest.NewRequest("GET", "/api/stats/overview?site=1&from=2024-04-01&to=2026-04-01", nil)

	_, err := filterFromRequest(r)
	if err == nil {
		t.Fatal("expected error for range > max")
	}
}

func TestParseSiteID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    uint32
		wantErr bool
	}{
		{"1", 1, false},
		{"4294967295", 4294967295, false},
		{"", 0, true},
		{"0", 0, true},
		{"-1", 0, true},
		{"abc", 0, true},
	}

	for _, tc := range cases {
		got, err := parseSiteID(tc.in)
		gotErr := err != nil

		if gotErr != tc.wantErr {
			t.Errorf("parseSiteID(%q) err = %v, wantErr %v", tc.in, err, tc.wantErr)
		}

		if got != tc.want {
			t.Errorf("parseSiteID(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseDateRange_IRSTNormalization(t *testing.T) {
	t.Parallel()

	from, to, err := parseDateRange("2026-04-01", "2026-04-08")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// 2026-04-01 00:00 IRST = 2026-03-31 20:30 UTC.
	wantFromUTC := time.Date(2026, 3, 31, 20, 30, 0, 0, time.UTC)
	if !from.Equal(wantFromUTC) {
		t.Errorf("from = %s, want %s", from, wantFromUTC)
	}

	if to.Sub(from) != 7*24*time.Hour {
		t.Errorf("range = %s, want 168h", to.Sub(from))
	}
}
