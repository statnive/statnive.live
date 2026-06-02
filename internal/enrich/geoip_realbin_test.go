// Build-tag gated so the common test path doesn't depend on the ~213 MB
// IPv6-combined LITE BIN being present on the developer box. CI / nightly
// runners can opt in via `go test -tags=realbin`. Path comes from
// STATNIVE_GEOIP_TEST_BIN; the test is SKIP'd (not failed) when the var
// is unset so a default `go test ./...` stays hermetic.
//
//go:build realbin

package enrich_test

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/enrich"
)

func realBINPath(t *testing.T) string {
	t.Helper()

	p := os.Getenv("STATNIVE_GEOIP_TEST_BIN")
	if p == "" {
		t.Skip("STATNIVE_GEOIP_TEST_BIN unset; skipping real-BIN GeoIP test")
	}

	if _, err := os.Stat(p); err != nil {
		t.Skipf("STATNIVE_GEOIP_TEST_BIN=%q: %v", p, err)
	}

	return p
}

// captureLogger returns a logger that writes to a bytes.Buffer the test
// can inspect — useful when NewGeoIPEnricher silently falls back to
// noopGeoIP on probe failure; the warn line is the only clue.
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// TestGeoIP_RealBIN_IPv4 — exercises the IPv4 lookup path against a real
// LITE BIN. Pinning specific country codes makes the test sensitive to
// upstream re-allocations; we pin only country_code (rarely re-allocated
// across continents) and assert that province / city are non-empty —
// catches a partial / corrupt BIN without depending on a city name.
func TestGeoIP_RealBIN_IPv4(t *testing.T) {
	t.Parallel()

	logger, buf := captureLogger()

	g, err := enrich.NewGeoIPEnricher(realBINPath(t), logger)
	if err != nil {
		t.Fatalf("NewGeoIPEnricher: %v", err)
	}

	if strings.Contains(buf.String(), "geoip disabled") {
		t.Fatalf("constructor fell back to noop:\n%s", buf.String())
	}

	t.Cleanup(func() { _ = g.Close() })

	// Country pins are chosen carefully: 8.8.8.8 lives in US Mountain View,
	// 185.143.232.1 is the Asiatech anchor used by probeDB itself. Avoid
	// pinning anycast IPs (1.1.1.1, 9.9.9.9 etc.) because IP2Location
	// resolves them to whichever CDN PoP the BIN happened to ship —
	// drift-prone across LITE refreshes.
	cases := []struct {
		ip          string
		wantCountry string
	}{
		{"8.8.8.8", "US"},          // Google public DNS, US Mountain View
		{"185.143.232.1", "IR"},    // Asiatech (probeDB anchor)
		{"203.0.113.42", ""},       // RFC 5737 docs block — assert anything
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.ip, func(t *testing.T) {
			t.Parallel()

			got := g.Lookup(tc.ip)
			if tc.wantCountry != "" && got.CountryCode != tc.wantCountry {
				t.Errorf("Lookup(%s).CountryCode = %q; want %q", tc.ip, got.CountryCode, tc.wantCountry)
			}

			// LITE DB11 doesn't carry ISP / Mobilebrand — they MUST land
			// as empty post-PR-#88 + PR #172 sentinel filtering. If
			// either column contained the LITE-tier or IPv6-on-IPv4
			// sentinel string, cleanGeoField regressed.
			if got.ISP != "" {
				t.Errorf("Lookup(%s).ISP = %q; want empty (LITE doesn't carry ISP)", tc.ip, got.ISP)
			}

			if got.Carrier != "" {
				t.Errorf("Lookup(%s).Carrier = %q; want empty (LITE doesn't carry Mobilebrand)", tc.ip, got.Carrier)
			}
		})
	}
}

// TestGeoIP_RealBIN_IPv6 — same shape as the IPv4 test but on the v6
// path. The smoking gun for the pre-2026-06-02 production regression
// (LEARN.md Lesson 38) was that every v6 lookup returned the
// "IPv6 address missing in IPv4 BIN." sentinel verbatim into
// events_raw.isp. This test asserts: (a) a v6 lookup resolves
// country_code (not "--"), (b) the ISP / Carrier columns are empty
// (no sentinel leakage), proving the BIN is v4+v6 combined.
//
// Both 2001:4860:: (Google) and 2606:4700:: (Cloudflare) are the
// canonical IPv6 ranges of well-known anycast providers — they're
// stable across LITE refreshes.
func TestGeoIP_RealBIN_IPv6(t *testing.T) {
	t.Parallel()

	g, err := enrich.NewGeoIPEnricher(realBINPath(t), discardLogger())
	if err != nil {
		t.Fatalf("NewGeoIPEnricher: %v", err)
	}

	t.Cleanup(func() { _ = g.Close() })

	cases := []string{
		"2001:4860:4860::8888", // Google public DNS v6
		"2606:4700:4700::1111", // Cloudflare DNS v6
		"2a00:1450:4001:830::200e",
		"2620:fe::fe", // Quad9
	}

	for _, ip := range cases {
		ip := ip
		t.Run(ip, func(t *testing.T) {
			t.Parallel()

			got := g.Lookup(ip)

			if got.CountryCode == "" || got.CountryCode == "--" {
				t.Errorf("Lookup(%s).CountryCode = %q; expected real ISO code (v6 BIN should resolve this)", ip, got.CountryCode)
			}

			if got.ISP != "" {
				t.Errorf("Lookup(%s).ISP = %q; want empty (sentinel filter regressed)", ip, got.ISP)
			}

			if got.Carrier != "" {
				t.Errorf("Lookup(%s).Carrier = %q; want empty (sentinel filter regressed)", ip, got.Carrier)
			}

			// Region / city may be empty for some carrier-allocated v6
			// /48s; just ensure no sentinel made it through.
			if strings.Contains(got.Province, "IPv6") || strings.Contains(got.City, "IPv6") {
				t.Errorf("Lookup(%s) leaked sentinel: province=%q city=%q", ip, got.Province, got.City)
			}
		})
	}
}
