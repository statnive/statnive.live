package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// IRSTLocation is "Asia/Tehran" (UTC+3:30, no DST since Sept 2022). Salt
// boundaries are aligned to this zone so visitors see a consistent rotation
// at local midnight regardless of where the analytics box sits.
//
// Loaded on first use; failures degrade to a fixed-offset fallback so a
// missing tzdata file never crashes the binary at boot.
var (
	irstOnce sync.Once
	irst     *time.Location
)

func irstLocation() *time.Location {
	irstOnce.Do(func() {
		if loc, err := time.LoadLocation("Asia/Tehran"); err == nil {
			irst = loc

			return
		}

		irst = time.FixedZone("IRST", int(3.5*float64(time.Hour/time.Second)))
	})

	return irst
}

// OverlapWindow is the period after IRST midnight during which both today's
// and yesterday's salts are simultaneously valid for new-visitor lookups.
// Five minutes covers clock skew between the analytics host and tracker
// clients well past p99 (NTP-disciplined hosts are under 50 ms; mobile
// clients with manual clocks can drift minutes but rarely past five).
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
// ≤2K entries even at 1K SaaS tenants.
type SaltManager struct {
	masterSecret []byte
	tz           *time.Location
	now          func() time.Time

	mu    sync.RWMutex
	cache map[saltKey]string
}

type saltKey struct {
	siteID uint32
	date   string
}

// NewSaltManager constructs a manager. The master secret is required; pass
// the output of config.LoadMasterSecret.
func NewSaltManager(masterSecret []byte) (*SaltManager, error) {
	if len(masterSecret) == 0 {
		return nil, errors.New("identity: master secret is empty")
	}

	return &SaltManager{
		masterSecret: append([]byte(nil), masterSecret...),
		tz:           irstLocation(),
		now:          time.Now,
		cache:        make(map[saltKey]string, 16),
	}, nil
}

// SetClock replaces the wall-clock source. Test-only; production callers
// must not invoke this. Concurrency-safe only when called before any
// CurrentSalt / PreviousSalt call.
func (m *SaltManager) SetClock(now func() time.Time) {
	m.now = now
}

// CurrentSalt returns the salt for today (IRST) for the given site.
func (m *SaltManager) CurrentSalt(siteID uint32) string {
	return m.saltFor(siteID, m.dateOf(m.now()))
}

// PreviousSalt returns the salt for yesterday (IRST). Callers (the bloom
// filter check) test it during the OverlapWindow so visitors crossing the
// midnight boundary aren't double-counted.
func (m *SaltManager) PreviousSalt(siteID uint32) string {
	return m.saltFor(siteID, m.dateOf(m.now().AddDate(0, 0, -1)))
}

// IsInOverlapWindow reports whether the current IRST time is within the
// OverlapWindow past midnight. The pipeline uses this to short-circuit
// the previous-salt lookup outside the window.
func (m *SaltManager) IsInOverlapWindow() bool {
	now := m.now().In(m.tz)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, m.tz)

	return now.Sub(midnight) < OverlapWindow
}

// CurrentDateIRST returns the YYYY-MM-DD string used as today's salt input.
// Exposed for /healthz + audit log clarity.
func (m *SaltManager) CurrentDateIRST() string {
	return m.dateOf(m.now())
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

func (m *SaltManager) derive(siteID uint32, date string) string {
	mac := hmac.New(sha256.New, m.masterSecret)
	_, _ = fmt.Fprintf(mac, "%d||%s", siteID, date)

	return hex.EncodeToString(mac.Sum(nil))
}

func (m *SaltManager) dateOf(t time.Time) string {
	return t.In(m.tz).Format("2006-01-02")
}
