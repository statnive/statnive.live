package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/statnive/statnive.live/internal/sites"
)

// DefaultSaltTimezone is the fallback IANA zone when a site has no tz set
// (empty string) or when the zone string fails to parse. UTC is the
// regulator-safe default — every other choice would re-introduce a
// jurisdiction signal in the public source. Per-site overrides come
// through statnive.sites.tz; see LookupSitePolicy / SitePolicy.TZ and
// the call site at internal/enrich/pipeline.go::Pipeline.Enrich.
const DefaultSaltTimezone = "UTC"

// legacyTimezone is honoured when STATNIVE_SALT_TZ_LEGACY=1. The env
// var is an emergency rollback handle: if per-site rotation produces
// an ingestion regression in production, set the var in the systemd
// unit + restart the binary to revert every site to v0.0.38's
// hardcoded Asia/Tehran behaviour. NOT picked up by SIGHUP — the
// kill switch is read at NewSaltManager time only, so production
// SOP is "set var, restart, debug, unset var, restart". Removed
// from the codebase one release after the rollout is stable
// (TODO v0.0.40+).
const legacyTimezone = "Asia/Tehran"

// OverlapWindow is the period after local midnight (in the site's tz)
// during which both today's and yesterday's salts are simultaneously
// valid for new-visitor lookups. Five minutes covers clock skew
// between the analytics host and tracker clients well past p99
// (NTP-disciplined hosts are under 50 ms; mobile clients with manual
// clocks can drift minutes but rarely past five).
const OverlapWindow = 5 * time.Minute

// SaltManager derives the per-day, per-site BLAKE3 key for VisitorHash.
//
// Why HMAC-SHA256: PRF properties give us per-tenant cryptographic
// separation when site_id is folded into the input, without per-tenant key
// management. One masterSecret, every tenant's salts derived deterministically.
//
// Why a cache: the (siteID, date) → salt map is small and read on every
// event in the hot path. Computing HMAC-SHA256 per event would burn ~300 ns
// each; a map lookup is ~20 ns. Cache eviction is intentionally absent —
// cardinality is bounded by sites_count * 2 (today + yesterday), which is
// ≤2K entries even at 1K SaaS tenants. The tz argument shifts which
// YYYY-MM-DD string each site lands on but doesn't widen the cache —
// any given site has exactly one configured tz at a time.
//
// Timezone resolution: every salt method takes a tz string (IANA name).
// Empty string or a zone outside the curated allow-list at
// internal/sites/timezones.go falls back to UTC. The legacy kill switch
// STATNIVE_SALT_TZ_LEGACY=1 overrides every call to Asia/Tehran
// (byte-identical to v0.0.38 behaviour).
type SaltManager struct {
	masterSecret []byte
	now          func() time.Time
	legacyTZ     bool

	mu    sync.RWMutex
	cache map[saltKey]string
}

type saltKey struct {
	siteID uint32
	date   string
}

// NewSaltManager constructs a manager. The master secret is required; pass
// the output of config.LoadMasterSecret. Reads STATNIVE_SALT_TZ_LEGACY at
// construction time only — to flip the kill switch, set the env var in
// the systemd unit and restart the binary (SIGHUP is not enough).
func NewSaltManager(masterSecret []byte) (*SaltManager, error) {
	if len(masterSecret) == 0 {
		return nil, errors.New("identity: master secret is empty")
	}

	return &SaltManager{
		masterSecret: append([]byte(nil), masterSecret...),
		now:          time.Now,
		legacyTZ:     os.Getenv("STATNIVE_SALT_TZ_LEGACY") == "1",
		cache:        make(map[saltKey]string, 16),
	}, nil
}

// SetClock replaces the wall-clock source. Test-only; production callers
// must not invoke this. Concurrency-safe only when called before any
// CurrentSalt / PreviousSalt call.
func (m *SaltManager) SetClock(now func() time.Time) {
	m.now = now
}

// CurrentSalt returns the salt for today (in tz) for the given site.
// Empty / unknown tz falls back to UTC. STATNIVE_SALT_TZ_LEGACY=1
// forces Asia/Tehran for backward compatibility.
func (m *SaltManager) CurrentSalt(siteID uint32, tz string) string {
	loc := m.resolveLocation(tz)

	return m.saltFor(siteID, m.dateIn(m.now(), loc))
}

// PreviousSalt returns the salt for yesterday (in tz). Callers (the bloom
// filter check) test it during the OverlapWindow so visitors crossing the
// midnight boundary aren't double-counted.
func (m *SaltManager) PreviousSalt(siteID uint32, tz string) string {
	loc := m.resolveLocation(tz)

	return m.saltFor(siteID, m.dateIn(m.now().AddDate(0, 0, -1), loc))
}

// IsInOverlapWindow reports whether the current time in tz is within
// the OverlapWindow past midnight. The pipeline uses this to short-circuit
// the previous-salt lookup outside the window.
func (m *SaltManager) IsInOverlapWindow(tz string) bool {
	loc := m.resolveLocation(tz)
	now := m.now().In(loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	return now.Sub(midnight) < OverlapWindow
}

// CurrentDate returns the YYYY-MM-DD string used as today's salt input
// for the given site. Exposed for /healthz + audit log clarity.
func (m *SaltManager) CurrentDate(tz string) string {
	loc := m.resolveLocation(tz)

	return m.dateIn(m.now(), loc)
}

// resolveLocation applies the legacy kill switch + defaults + allow-list
// lookup in one step. Reuses the package-init cachedLocations map at
// internal/sites/timezones.go::LocationFor so the hot path never calls
// time.LoadLocation. Unknown / empty tz → UTC (regulator-safe default).
func (m *SaltManager) resolveLocation(tz string) *time.Location {
	if m.legacyTZ {
		tz = legacyTimezone
	} else if tz == "" {
		tz = DefaultSaltTimezone
	}

	return sites.LocationFor(tz)
}

func (m *SaltManager) saltFor(siteID uint32, date string) string {
	key := saltKey{siteID: siteID, date: date}

	m.mu.RLock()

	if s, ok := m.cache[key]; ok {
		m.mu.RUnlock()

		return s
	}

	m.mu.RUnlock()

	salt := m.derive(siteID, date)

	m.mu.Lock()
	m.cache[key] = salt
	m.mu.Unlock()

	return salt
}

// derive feeds the HMAC the same siteIDBytes encoding that UserIDHash
// uses. Flipping salt.derive alone would silently break same-day
// deduplication while leaving the cross-day user_id_hash intact, and
// rewriting the on-disk user_id_hash to recover is impossible (raw
// user_id is not stored).
func (m *SaltManager) derive(siteID uint32, date string) string {
	mac := hmac.New(sha256.New, m.masterSecret)
	_, _ = mac.Write(siteIDBytes(siteID))
	_, _ = mac.Write([]byte("||"))
	_, _ = mac.Write([]byte(date))

	return hex.EncodeToString(mac.Sum(nil))
}

func (m *SaltManager) dateIn(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02")
}
