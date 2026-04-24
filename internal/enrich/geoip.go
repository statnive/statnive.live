package enrich

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ip2location/ip2location-go/v9"
)

// GeoResult is what the pipeline writes onto the EnrichedEvent. Empty
// strings mean "unknown" — the schema's LowCardinality(String) defaults
// take care of the storage shape.
type GeoResult struct {
	Province    string
	City        string
	CountryCode string // ISO-3166 alpha-2; "--" when unresolved
	ISP         string
	Carrier     string
}

// defaultGeo is what we return for empty / private / loopback IPs and for
// any lookup against the no-op enricher (when no DB BIN is configured).
var defaultGeo = GeoResult{CountryCode: "--"}

// reloadGraceDrain is the sleep between swapping the atomic.Pointer and
// closing the old handle — the upstream library uses *os.File.ReadAt
// which is safe for concurrent use, so in-flight lookups against the
// old handle complete cleanly; 1 s is a generous ceiling for the
// longest lookup observed in Phase 7a benches.
const reloadGraceDrain = time.Second

// GeoIPEnricher is the runtime contract. The interface lets the pipeline
// run without an IP2Location DB present (the no-op variant returns
// defaultGeo) — operators get GeoIP only after dropping the licensed BIN
// at the configured path.
type GeoIPEnricher interface {
	Lookup(ip string) GeoResult
	// Reload hot-swaps the GeoIP DB with the contents of binPath. The
	// swap is atomic — in-flight lookups either see the old or the new
	// handle, never a partial state. Fails closed: if the new BIN fails
	// pre-swap validation, the old handle stays active.
	Reload(binPath string) error
	Close() error
}

// noopGeoIP is the fallback used when no BIN file is configured / readable.
// We log the decision once at boot so operators see why their geo column
// is always "--".
type noopGeoIP struct{}

func (noopGeoIP) Lookup(string) GeoResult { return defaultGeo }
func (noopGeoIP) Reload(string) error     { return nil }
func (noopGeoIP) Close() error            { return nil }

// ip2locationGeoIP wraps the upstream library's *DB. Per doc 28 §Gap 1
// the handle lives behind an atomic.Pointer so SIGHUP reload can
// hot-swap without blocking the lookup hot path. The upstream library
// uses *os.File.ReadAt which is safe for concurrent use, so no mutex /
// sync.Pool is needed on the read side.
type ip2locationGeoIP struct {
	db       atomic.Pointer[ip2location.DB]
	lookups  atomic.Uint64
	missCtry atomic.Uint64
	logger   *slog.Logger
}

// NewGeoIPEnricher returns a real enricher when binPath points at a
// readable IP2Location BIN file, and a no-op enricher otherwise. The
// no-op fallback is intentional — the IP2Location DB23 is a ~600 MB
// licensed file the operator drops in separately, and we never want a
// missing optional asset to wedge ingestion at boot.
func NewGeoIPEnricher(binPath string, logger *slog.Logger) (GeoIPEnricher, error) {
	if binPath == "" {
		logger.Info("geoip disabled: no bin path configured; country_code will default to --")

		return noopGeoIP{}, nil
	}

	db, err := openValidatedDB(binPath)
	if err != nil {
		logger.Warn("geoip disabled: bin file unreadable or probes failed; country_code will default to --",
			"path", binPath, "err", err)

		return noopGeoIP{}, nil
	}

	logger.Info("geoip loaded", "path", binPath, "package_version", db.PackageVersion())

	e := &ip2locationGeoIP{logger: logger}
	e.db.Store(db)

	return e, nil
}

// Lookup returns defaults for empty / loopback / private / link-local IPs
// without consulting the DB.
func (g *ip2locationGeoIP) Lookup(ip string) GeoResult {
	if ip == "" {
		return defaultGeo
	}

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return defaultGeo
	}

	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return defaultGeo
	}

	db := g.db.Load()
	if db == nil {
		return defaultGeo
	}

	rec, err := db.Get_all(ip)
	if err != nil {
		return defaultGeo
	}

	g.lookups.Add(1)

	cc := strings.ToUpper(strings.TrimSpace(rec.Country_short))
	if cc == "" || cc == "-" {
		g.missCtry.Add(1)

		cc = "--"
	}

	return GeoResult{
		Province:    cleanGeoField(rec.Region),
		City:        cleanGeoField(rec.City),
		CountryCode: cc,
		ISP:         cleanGeoField(rec.Isp),
		Carrier:     cleanGeoField(rec.Mobilebrand),
	}
}

// Reload opens the BIN at binPath, runs pre-swap validation probes
// (doc 28 §Gap 1 — `8.8.8.8` must resolve to some non-"--" country;
// `185.143.232.1` must resolve to "IR"), atomic-swaps the DB handle,
// drains in-flight lookups for reloadGraceDrain, then closes the old
// handle. On probe failure the new handle is closed and the old one
// stays active.
func (g *ip2locationGeoIP) Reload(binPath string) error {
	if binPath == "" {
		return errors.New("geoip reload: empty binPath")
	}

	newDB, err := openValidatedDB(binPath)
	if err != nil {
		return fmt.Errorf("geoip reload: %w", err)
	}

	old := g.db.Swap(newDB)

	go func() {
		time.Sleep(reloadGraceDrain)

		if old != nil {
			old.Close()
		}
	}()

	g.logger.Info("geoip reloaded", "path", binPath, "package_version", newDB.PackageVersion())

	return nil
}

// Close releases the underlying file handle. The upstream library returns
// no error from Close so we mirror its no-op contract.
func (g *ip2locationGeoIP) Close() error {
	if db := g.db.Swap(nil); db != nil {
		db.Close()
	}

	return nil
}

// Stats returns counters used by /healthz and audit logging.
func (g *ip2locationGeoIP) Stats() (lookups, missCountry uint64) {
	return g.lookups.Load(), g.missCtry.Load()
}

// openValidatedDB opens a BIN at path and runs the two pre-swap probes
// from doc 28 §Gap 1. If either probe fails, the DB is closed and an
// error returned — the old handle stays active in the caller.
func openValidatedDB(path string) (*ip2location.DB, error) {
	db, err := ip2location.OpenDB(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}

	if err := probeDB(db); err != nil {
		db.Close()

		return nil, err
	}

	return db, nil
}

func probeDB(db *ip2location.DB) error {
	// 8.8.8.8 (Google DNS) — any resolvable country satisfies the probe.
	// A BIN that returns "--" for 8.8.8.8 is a partial/corrupt file.
	usRec, err := db.Get_all("8.8.8.8")
	if err != nil {
		return fmt.Errorf("probe 8.8.8.8: %w", err)
	}

	us := strings.ToUpper(strings.TrimSpace(usRec.Country_short))
	if us == "" || us == "-" || us == "--" {
		return errors.New("probe 8.8.8.8: country resolved to empty/--; BIN looks corrupt")
	}

	// 185.143.232.1 — picked because this /24 was observed as an
	// Iranian-routed block; used as a sanity check that the Iran
	// resolutions work (matters for SamplePlatform's audience).
	irRec, err := db.Get_all("185.143.232.1")
	if err != nil {
		return fmt.Errorf("probe 185.143.232.1: %w", err)
	}

	ir := strings.ToUpper(strings.TrimSpace(irRec.Country_short))
	if ir != "IR" {
		return fmt.Errorf("probe 185.143.232.1: expected IR, got %q", ir)
	}

	return nil
}

func cleanGeoField(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" {
		return ""
	}

	return s
}
