# Canonical GeoIP wrapper — atomic.Pointer hot-swap with pre-swap validation

Reference Go implementation for `internal/enrich/geoip.go`. Body verbatim from doc 28 §Gap 1 lines 154–235. Copy when authoring the production file in Phase 8.

## Full wrapper

```go
package enrich

import (
    "crypto/sha256"
    "errors"
    "fmt"
    "net/netip"
    "os"
    "sync/atomic"
    "time"

    "github.com/ip2location/ip2location-go/v9"
)

// Record is the only shape that leaves GeoIP. No IP field, ever.
type Record struct {
    CountryShort, CountryLong, Region, City string
}

// dbHandle is the immutable snapshot held by atomic.Pointer.
type dbHandle struct {
    db      *ip2location.DB
    version string
    loaded  time.Time
}

// GeoIP is the hot-swap wrapper. Construct via New(); reload via Reload().
type GeoIP struct {
    path string
    cur  atomic.Pointer[dbHandle]
}

// New constructs a GeoIP with the configured BIN path. Does NOT open the DB —
// caller must call Reload() explicitly, or accept no-op Lookup() until the
// operator drops in a BIN and sends SIGHUP.
func New(path string) *GeoIP {
    return &GeoIP{path: path}
}

// Lookup returns a geo Record for the given IP. Hot path.
// Zero allocation on the success path; pure (no logs, no files, no external).
func (g *GeoIP) Lookup(ip netip.Addr) (Record, error) {
    h := g.cur.Load()
    if h == nil {
        noopFallbackTotal.Inc()
        return Record{}, nil // no-op fallback; pipeline keeps flowing
    }
    rec, err := h.db.Get_city(ip.String())
    if err != nil {
        if errors.Is(err, ip2location.NoMatchError) {
            return Record{}, nil
        }
        return Record{}, fmt.Errorf("geoip lookup: %w", err) // NO ip in message
    }
    return Record{
        CountryShort: rec.Country_short,
        CountryLong:  rec.Country_long,
        Region:       rec.Region,
        City:         rec.City,
    }, nil
}

const minExpectedBytes = 50 * 1024 * 1024 // 50MB LITE DB23 floor

// Reload validates a new BIN file and atomically swaps it in.
// Bad file → old DB retained; metric incremented; no silent no-geo degradation.
// Called by the SIGHUP handler goroutine in main.
func (g *GeoIP) Reload() error {
    // Size floor — guards truncated downloads + partial SCP
    fi, err := os.Stat(g.path)
    if err != nil || fi.Size() < minExpectedBytes {
        reloadTotal.WithLabelValues("rejected_size").Inc()
        return fmt.Errorf("geoip too small or missing: %w", err)
    }

    // Open independent handle — does NOT disturb the current handle
    newDB, err := ip2location.OpenDB(g.path)
    if err != nil {
        reloadTotal.WithLabelValues("failed_open").Inc()
        return err
    }

    // Probe validation — both must pass
    for _, p := range []struct{ ip, want string }{
        {"8.8.8.8", "US"},
        {"185.143.232.1", "IR"},
    } {
        r, err := newDB.Get_country_short(p.ip)
        if err != nil || r.Country_short != p.want {
            newDB.Close()
            reloadTotal.WithLabelValues("rejected_validation").Inc()
            return fmt.Errorf("probe %s want=%s got=%s", p.ip, p.want, r.Country_short)
        }
    }

    // Version monotonicity — prevent accidental downgrade
    newH := &dbHandle{
        db:      newDB,
        version: newDB.DatabaseVersion(),
        loaded:  time.Now(),
    }
    if old := g.cur.Load(); old != nil && newH.version < old.version {
        newDB.Close()
        reloadTotal.WithLabelValues("rejected_older_version").Inc()
        return fmt.Errorf("version regression")
    }

    // Atomic swap + 1s grace close of old (>2× p99 lookup budget 500ms)
    old := g.cur.Swap(newH)
    reloadTotal.WithLabelValues("ok").Inc()
    if old != nil {
        go func() {
            time.Sleep(1 * time.Second)
            _ = old.db.Close()
        }()
    }
    return nil
}
```

## Prometheus metrics

```go
var (
    noopFallbackTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "statnive_geoip_noop_fallback_total",
        Help: "Lookups that returned zero Record because no BIN is loaded.",
    })
    reloadTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
        Name: "statnive_geoip_reload_total",
        Help: "GeoIP reload outcomes.",
    }, []string{"result"}) // ok | rejected_size | failed_open |
                          // rejected_validation | rejected_older_version
)
```

Alert rule (add to `prometheus/geoip.rules.yml`):

```yaml
- alert: GeoIPStaleReload
  expr: time() - statnive_geoip_db_loaded_seconds > 86400 * 40   # 40 days
  for: 15m
  labels: { severity: warning }
  annotations:
    summary: "GeoIP BIN is >40 days old — monthly refresh missed"
```

## SIGHUP wiring in main

```go
func main() {
    // ... flag parsing, config load ...

    geo := enrich.New(cfg.GeoIPPath)
    // Initial load — non-fatal; service boots without BIN
    if err := geo.Reload(); err != nil {
        slog.Warn("initial geoip load failed; service will return no-geo Records until BIN is available", "err", err)
    }

    hup := make(chan os.Signal, 1)
    signal.Notify(hup, syscall.SIGHUP)
    defer signal.Stop(hup)
    go func() {
        for range hup {
            if err := geo.Reload(); err != nil {
                slog.Error("geoip reload failed; keeping previous", "err", err)
                continue
            }
            slog.Info("geoip reloaded")
        }
    }()

    // ... server.ListenAndServe ...
}
```

## Blocked patterns (enforced by Semgrep)

```go
// ❌ geoip-must-use-atomic-pointer + geoip-get-all-banned
type GeoIP struct {
    mu sync.RWMutex
    db *ip2location.DB
}
func (g *GeoIP) Lookup(ip string) (Record, error) {
    g.mu.RLock()
    defer g.mu.RUnlock()
    return g.db.Get_all(ip) // cache-line bounce + 3x cost
}

// ❌ geoip-no-fsnotify-on-bin
w, _ := fsnotify.NewWatcher()
w.Add(cfg.GeoIPPath)

// ❌ geoip-ip-in-log
slog.Error("lookup failed", "remote_addr", req.RemoteAddr, "err", err)

// ❌ geoip-ip-field-in-persisted-struct
type IngestedEvent struct {
    Timestamp time.Time
    IP        netip.Addr   // ← no
    UA        string
}

// ❌ geoip-no-ip-key-cache
cache[ip.String()] = record
```

## Why no RLock here

9K EPS sustained → ~9000 RLock acquisitions per second. `sync.RWMutex.RLock` touches a shared cache line (the reader count). Under contention, that line bounces across all CPU cores serving lookups — 20–100ns per acquisition on typical server hardware.

`atomic.Pointer[T].Load()` is a single pointer load — ~1–3ns, no cache line ownership transfer (Go 1.19+ uses hardware atomic loads that can satisfy multiple readers concurrently without coherence traffic).

At 9K EPS × (100 – 3) ns saved per call ≈ **0.9ms CPU/sec saved** — not enormous, but it's pure win: simpler code, lower latency jitter, no reader-writer starvation corner case.

## Research anchor

Doc 28 §Gap 1 lines 154–235 (full wrapper code) + lines 325–331 (opinionated defaults).