package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/statnive/statnive.live/internal/sites"
)

// TestSites_Create_DefaultsCurrencyTZ pins the contract that POST
// /api/admin/sites without {currency, tz} fills in EUR + Europe/Berlin
// (sites.DefaultCurrency / sites.DefaultTimezone). Operators relying
// on the form's omit-and-default ergonomic break if this regresses.
func TestSites_Create_DefaultsCurrencyTZ(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	body := `{"hostname":"defaults.example"}`
	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites", body, admin, nil))

	if w.Code != 201 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var got siteAdminResponse

	_ = json.Unmarshal(w.Body.Bytes(), &got)

	if got.Currency != sites.DefaultCurrency {
		t.Errorf("Currency = %q, want %q (DefaultCurrency)", got.Currency, sites.DefaultCurrency)
	}

	if got.TZ != sites.DefaultTimezone {
		t.Errorf("TZ = %q, want %q (DefaultTimezone)", got.TZ, sites.DefaultTimezone)
	}
}

// TestSites_Create_AcceptsExplicitCurrencyTZ pins that valid explicit
// values override the defaults at creation time.
func TestSites_Create_AcceptsExplicitCurrencyTZ(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	body := `{"hostname":"explicit.example","currency":"USD","tz":"America/New_York"}`
	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites", body, admin, nil))

	if w.Code != 201 {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}

	var got siteAdminResponse

	_ = json.Unmarshal(w.Body.Bytes(), &got)

	if got.Currency != "USD" || got.TZ != "America/New_York" {
		t.Errorf("explicit values not applied: %+v", got)
	}
}

// TestSites_Create_RejectsBadCurrency pins the negative path: a code
// outside the allow-list returns 400, NOT a successful create with the
// raw value persisted. The dropdown trusts this.
func TestSites_Create_RejectsBadCurrency(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	body := `{"hostname":"bad-cur.example","currency":"FOO"}`
	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites", body, admin, nil))

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 for invalid currency", w.Code)
	}
}

func TestSites_Create_RejectsBadTimezone(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	body := `{"hostname":"bad-tz.example","tz":"Mars/Olympus"}`
	w := httptest.NewRecorder()
	h.Create(w, adminRequest(t, "POST", "/api/admin/sites", body, admin, nil))

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 for invalid timezone", w.Code)
	}
}

// TestSites_PatchCurrencyTZ pins the round-trip: create with defaults,
// PATCH to USD + America/New_York, GET back the updated row. This is
// the canonical user journey for "I changed my mind on currency".
func TestSites_PatchCurrencyTZ(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewSites(deps)

	cw := httptest.NewRecorder()
	h.Create(cw, adminRequest(t, "POST", "/api/admin/sites",
		`{"hostname":"patch-me.example"}`, admin, nil))

	if cw.Code != 201 {
		t.Fatalf("create: %d", cw.Code)
	}

	var created siteAdminResponse

	_ = json.Unmarshal(cw.Body.Bytes(), &created)

	idStr := strconv.FormatUint(uint64(created.SiteID), 10)
	uw := httptest.NewRecorder()
	h.Update(uw, adminRequest(t, "PATCH", "/api/admin/sites/"+idStr,
		`{"currency":"USD","tz":"America/New_York"}`, admin,
		map[string]string{"id": idStr}))

	if uw.Code != 200 {
		t.Fatalf("patch status = %d body = %s", uw.Code, uw.Body.String())
	}

	var got siteAdminResponse

	_ = json.Unmarshal(uw.Body.Bytes(), &got)

	if got.Currency != "USD" {
		t.Errorf("Currency = %q, want USD", got.Currency)
	}

	if got.TZ != "America/New_York" {
		t.Errorf("TZ = %q, want America/New_York", got.TZ)
	}
}

// TestSites_PatchBadAttribute table-pins the negative paths for both
// PATCH attributes. The two cases share the same "create + PATCH +
// expect 400" shape; folding them into a table avoids the dupl lint
// warning while keeping each variant explicit.
func TestSites_PatchBadAttribute(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		host    string
		body    string
		errLine string
	}{
		{
			name:    "currency",
			host:    "reject-cur.example",
			body:    `{"currency":"FOO"}`,
			errLine: "patch invalid currency status = %d, want 400",
		},
		{
			name:    "timezone",
			host:    "reject-tz.example",
			body:    `{"tz":"Mars/Olympus"}`,
			errLine: "patch invalid tz status = %d, want 400",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			deps, _ := newSitesDeps()
			admin := newTestAdmin()
			h := NewSites(deps)

			cw := httptest.NewRecorder()
			h.Create(cw, adminRequest(t, "POST", "/api/admin/sites",
				`{"hostname":"`+tc.host+`"}`, admin, nil))

			var created siteAdminResponse

			_ = json.Unmarshal(cw.Body.Bytes(), &created)

			idStr := strconv.FormatUint(uint64(created.SiteID), 10)
			uw := httptest.NewRecorder()
			h.Update(uw, adminRequest(t, "PATCH", "/api/admin/sites/"+idStr,
				tc.body, admin, map[string]string{"id": idStr}))

			if uw.Code != 400 {
				t.Errorf(tc.errLine, uw.Code)
			}
		})
	}
}

// TestOptions_CurrenciesEndpoint pins the GET /api/admin/currencies
// shape: {"currencies": [{code, symbol, name}, ...]} with every
// configured option present.
func TestOptions_CurrenciesEndpoint(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewOptions(deps)

	w := httptest.NewRecorder()
	h.Currencies(w, adminRequest(t, "GET", "/api/admin/currencies", "", admin, nil))

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	var body struct {
		Currencies []sites.CurrencyOption `json:"currencies"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(body.Currencies) != len(sites.Currencies) {
		t.Errorf("currencies = %d, want %d", len(body.Currencies), len(sites.Currencies))
	}

	// EUR is the default and must be present.
	var foundEUR bool

	for _, c := range body.Currencies {
		if c.Code == "EUR" {
			foundEUR = true

			if c.Symbol != "€" || c.Name != "Euro" {
				t.Errorf("EUR row malformed: %+v", c)
			}
		}
	}

	if !foundEUR {
		t.Error("EUR missing from /api/admin/currencies response")
	}
}

// TestOptions_TimezonesEndpoint pins the GET /api/admin/timezones
// shape and the live-offset population. Every entry must carry a
// non-empty Offset (the SPA renders it parenthesized).
func TestOptions_TimezonesEndpoint(t *testing.T) {
	t.Parallel()

	deps, _ := newSitesDeps()
	admin := newTestAdmin()
	h := NewOptions(deps)

	w := httptest.NewRecorder()
	h.Timezones(w, adminRequest(t, "GET", "/api/admin/timezones", "", admin, nil))

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}

	var body struct {
		Timezones []sites.TimezoneOption `json:"timezones"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(body.Timezones) != len(sites.Timezones) {
		t.Errorf("timezones = %d, want %d", len(body.Timezones), len(sites.Timezones))
	}

	for _, tz := range body.Timezones {
		if tz.Offset == "" {
			t.Errorf("Timezone %q has empty Offset", tz.IANA)
		}
	}
}
