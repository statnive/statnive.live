package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/sites"
)

// stubLister is a minimal SiteLister for filter tests. It returns the
// configured tz for any site_id so tests can assert that the per-site
// TZ is the one used when interpreting ?from / ?to dates.
type stubLister struct {
	tz string
}

func (s stubLister) List(_ context.Context) ([]sites.Site, error) { return nil, nil }
func (s stubLister) LookupSiteByID(_ context.Context, _ uint32) (sites.SiteAdmin, error) {
	return sites.SiteAdmin{
		Site: sites.Site{ID: 1, Hostname: "stub", Enabled: true, TZ: s.tz, Currency: "EUR"},
	}, nil
}

func newLister(tz string) SiteLister { return stubLister{tz: tz} }

func TestFilterFromRequest_RequiresSite(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil)

	_, err := filterFromRequest(r, newLister("Europe/Berlin"))
	if !errors.Is(err, errBadInput) {
		t.Errorf("err = %v, want errBadInput", err)
	}
}

func TestFilterFromRequest_DefaultsLast7Days(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=1", nil)

	f, err := filterFromRequest(r, newLister("Europe/Berlin"))
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

	r := httptest.NewRequest(http.MethodGet, url, nil)

	f, err := filterFromRequest(r, newLister("Europe/Berlin"))
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

	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=1&from=not-a-date", nil)

	_, err := filterFromRequest(r, newLister("Europe/Berlin"))
	if err == nil {
		t.Fatal("expected error for unparseable date")
	}

	if !errors.Is(err, errBadInput) {
		t.Errorf("err = %v, want errBadInput", err)
	}
}

func TestFilterFromRequest_FromOnly_ToDefaultsToTomorrow(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=1&from=2026-04-01", nil)

	f, err := filterFromRequest(r, newLister("Europe/Berlin"))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	// To should be > today UTC; from is 2026-04-01 in the site's TZ.
	if !f.To.After(f.From) {
		t.Errorf("To (%s) should be after From (%s)", f.To, f.From)
	}
}

func TestFilterFromRequest_RangeTooLarge(t *testing.T) {
	t.Parallel()

	// 2-year range exceeds Filter.MaxRange of 1 year.
	r := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=1&from=2024-04-01&to=2026-04-01", nil)

	_, err := filterFromRequest(r, newLister("Europe/Berlin"))
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

// TestParseDateRange_PerSiteTimezone pins the per-site TZ contract:
// the same YYYY-MM-DD input lands on different UTC instants depending
// on the site's configured TZ. New default is Europe/Berlin (CEST in
// April = UTC+2); America/New_York is EDT in April (UTC-4).
func TestParseDateRange_PerSiteTimezone(t *testing.T) {
	t.Parallel()

	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatalf("LoadLocation Europe/Berlin: %v", err)
	}

	from, to, err := parseDateRange("2026-05-01", "2026-05-08", berlin)
	if err != nil {
		t.Fatalf("parseDateRange Berlin: %v", err)
	}

	// 2026-05-01 00:00 CEST (UTC+2) = 2026-04-30 22:00 UTC.
	wantFromUTC := time.Date(2026, 4, 30, 22, 0, 0, 0, time.UTC)
	if !from.Equal(wantFromUTC) {
		t.Errorf("Berlin from = %s, want %s", from, wantFromUTC)
	}

	if to.Sub(from) != 7*24*time.Hour {
		t.Errorf("Berlin range = %s, want 168h", to.Sub(from))
	}

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation America/New_York: %v", err)
	}

	from, _, err = parseDateRange("2026-05-01", "2026-05-08", ny)
	if err != nil {
		t.Fatalf("parseDateRange New York: %v", err)
	}

	// 2026-05-01 00:00 EDT (UTC-4) = 2026-05-01 04:00 UTC.
	wantFromUTC = time.Date(2026, 5, 1, 4, 0, 0, 0, time.UTC)
	if !from.Equal(wantFromUTC) {
		t.Errorf("NY from = %s, want %s", from, wantFromUTC)
	}
}

// TestResolveSiteTZ_FallsBackToUTC pins the safety contract: a stored
// TZ that fails LoadLocation must NOT 500 the dashboard — it falls
// back to UTC so date-range parsing still produces a valid Filter.
func TestResolveSiteTZ_FallsBackToUTC(t *testing.T) {
	t.Parallel()

	// stubLister returns the configured tz unchanged; "Mars/Olympus"
	// cannot resolve via LoadLocation.
	loc := resolveSiteTZ(context.Background(), newLister("Mars/Olympus"), 1)
	if loc != time.UTC {
		t.Errorf("loc = %s, want UTC fallback for invalid tz", loc)
	}

	// nil lister also yields UTC (defensive — main.go always wires one).
	loc = resolveSiteTZ(context.Background(), nil, 1)
	if loc != time.UTC {
		t.Errorf("nil lister loc = %s, want UTC", loc)
	}
}
