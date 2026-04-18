package enrich

import (
	"log/slog"
	"net/netip"
	"strings"
	"sync/atomic"

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

// GeoIPEnricher is the runtime contract. The interface lets the pipeline
// run without an IP2Location DB present (the no-op variant returns
// defaultGeo) — operators get GeoIP only after dropping the licensed BIN
// at the configured path.
type GeoIPEnricher interface {
	Lookup(ip string) GeoResult
	Close() error
}

// noopGeoIP is the fallback used when no BIN file is configured / readable.
// We log the decision once at boot so operators see why their geo column
// is always "--".
type noopGeoIP struct{}

func (noopGeoIP) Lookup(string) GeoResult { return defaultGeo }
func (noopGeoIP) Close() error            { return nil }

// ip2locationGeoIP wraps the upstream library's *DB. The library uses
// *os.File.ReadAt under the hood, which the Go stdlib guarantees is safe
// for concurrent use, so no mutex / sync.Pool is needed.
type ip2locationGeoIP struct {
	db       *ip2location.DB
	lookups  atomic.Uint64
	missCtry atomic.Uint64
}

// NewGeoIPEnricher returns a real enricher when binPath points at a
// readable IP2Location BIN file, and a no-op enricher otherwise. The
// no-op fallback is intentional — the IP2Location DB23 is a 600 MB
// licensed file the operator drops in separately, and we never want a
// missing optional asset to wedge ingestion at boot.
func NewGeoIPEnricher(binPath string, logger *slog.Logger) (GeoIPEnricher, error) {
	if binPath == "" {
		logger.Info("geoip disabled: no bin path configured; country_code will default to --")

		return noopGeoIP{}, nil
	}

	db, err := ip2location.OpenDB(binPath)
	if err != nil {
		logger.Warn("geoip disabled: bin file unreadable; country_code will default to --",
			"path", binPath, "err", err)

		return noopGeoIP{}, nil
	}

	logger.Info("geoip loaded", "path", binPath, "package_version", db.PackageVersion())

	return &ip2locationGeoIP{db: db}, nil
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

	rec, err := g.db.Get_all(ip)
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

// Close releases the underlying file handle. The upstream library returns
// no error from Close so we mirror its no-op contract.
func (g *ip2locationGeoIP) Close() error {
	g.db.Close()

	return nil
}

// Stats returns counters used by /healthz and audit logging.
func (g *ip2locationGeoIP) Stats() (lookups, missCountry uint64) {
	return g.lookups.Load(), g.missCtry.Load()
}

func cleanGeoField(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" {
		return ""
	}

	return s
}
