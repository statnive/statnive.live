package enrich_test

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/statnive/statnive.live/internal/enrich"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestGeoIP_NoPathReturnsNoop(t *testing.T) {
	t.Parallel()

	g, err := enrich.NewGeoIPEnricher("", discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := g.Lookup("8.8.8.8")
	if got.CountryCode != "--" {
		t.Errorf("noop should return --; got %q", got.CountryCode)
	}
}

func TestGeoIP_MissingFileFallsBackToNoop(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing.BIN")
	g, err := enrich.NewGeoIPEnricher(missing, discardLogger())
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}

	if got := g.Lookup("203.0.113.1"); got.CountryCode != "--" {
		t.Errorf("missing-file fallback should return --; got %q", got.CountryCode)
	}
}

func TestGeoIP_PrivateIPsShortCircuit(t *testing.T) {
	t.Parallel()

	g, err := enrich.NewGeoIPEnricher("", discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "::1", "fe80::1", "0.0.0.0"} {
		got := g.Lookup(ip)
		if got.CountryCode != "--" {
			t.Errorf("private/loopback %s should return --; got %q", ip, got.CountryCode)
		}
	}
}
