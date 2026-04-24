package storage

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"lukechampine.com/blake3"
)

// Filter is the dimension + range scope every dashboard query reads from.
// Field names align with Pirsch (doc 24 §Sec 4 pattern 2) so external
// examples port over with one rename — ClientID → SiteID. Per CLAUDE.md
// Privacy Rule 1, there is intentionally NO IP field; rate-limit /
// audit-log / GeoIP are the only sites that touch raw IP and none of
// them persist it.
type Filter struct {
	SiteID uint32

	// Half-open: events at exactly From are included, events at exactly
	// To are not. Use day boundaries for daily rollups, hour boundaries
	// for hourly rollups (the query helper handles the column choice).
	From, To time.Time

	// Optional dimension filters — empty string means no constraint.
	Path        string
	Referrer    string
	Channel     string // e.g. "Direct" / "Organic Search" / "Social Media" — matches daily_sources.channel
	UTMSource   string
	UTMMedium   string
	UTMCampaign string
	UTMContent  string
	UTMTerm     string
	Country     string
	Browser     string
	OS          string
	Device      string

	// UI knobs.
	Sort   string // "views" | "visitors" | "goals" | "revenue" | ""
	Search string // case-insensitive substring across the leading dimension
	Limit  int    // 0 → DefaultLimit
	Offset int
}

// DefaultLimit caps result rows when the caller doesn't ask for one.
// Picked to match a single full-screen dashboard table without paging.
const DefaultLimit = 50

// MaxRange is the longest [From, To) span we accept. Past this, the
// caller almost certainly means a different question (per-month rollup,
// year-over-year compare); refusing keeps an accidental "give me 5
// years of pages" from sweeping the cluster.
const MaxRange = 365 * 24 * time.Hour

// ErrInvalidFilter is the umbrella error returned by Validate; callers
// can errors.Is against it to distinguish bad input from infra failure.
var ErrInvalidFilter = errors.New("storage: invalid filter")

// Validate enforces the invariants every query depends on. Called by
// each Store method before its first DB roundtrip — catches caller bugs
// at the API boundary, not deep inside ClickHouse.
func (f *Filter) Validate() error {
	if f == nil {
		return fmt.Errorf("%w: nil filter", ErrInvalidFilter)
	}

	if f.SiteID == 0 {
		return fmt.Errorf("%w: site_id is required", ErrInvalidFilter)
	}

	if f.From.IsZero() || f.To.IsZero() {
		return fmt.Errorf("%w: from and to are required", ErrInvalidFilter)
	}

	if !f.From.Before(f.To) {
		return fmt.Errorf("%w: from must be strictly before to", ErrInvalidFilter)
	}

	if f.To.Sub(f.From) > MaxRange {
		return fmt.Errorf("%w: range %s exceeds max %s", ErrInvalidFilter, f.To.Sub(f.From), MaxRange)
	}

	if f.Limit < 0 || f.Offset < 0 {
		return fmt.Errorf("%w: limit and offset must be >= 0", ErrInvalidFilter)
	}

	return nil
}

// EffectiveLimit returns Limit or DefaultLimit when Limit is 0.
func (f *Filter) EffectiveLimit() int {
	if f.Limit <= 0 {
		return DefaultLimit
	}

	return f.Limit
}

// Hash returns a stable BLAKE3-128 hex digest of the Filter's fields.
// Used as the cache key by CachedStore — order of struct field changes
// MUST NOT alter the hash, so we serialize fields by name explicitly
// rather than relying on struct layout. UTC-normalize timestamps so
// two equivalent filters in different time zones key the same.
func (f *Filter) Hash() string {
	h := blake3.New(16, nil)

	writeUint32(h, "site_id", f.SiteID)
	writeTime(h, "from", f.From)
	writeTime(h, "to", f.To)
	writeStr(h, "path", f.Path)
	writeStr(h, "referrer", f.Referrer)
	writeStr(h, "channel", f.Channel)
	writeStr(h, "utm_source", f.UTMSource)
	writeStr(h, "utm_medium", f.UTMMedium)
	writeStr(h, "utm_campaign", f.UTMCampaign)
	writeStr(h, "utm_content", f.UTMContent)
	writeStr(h, "utm_term", f.UTMTerm)
	writeStr(h, "country", f.Country)
	writeStr(h, "browser", f.Browser)
	writeStr(h, "os", f.OS)
	writeStr(h, "device", f.Device)
	writeStr(h, "sort", f.Sort)
	writeStr(h, "search", f.Search)
	writeInt(h, "limit", f.EffectiveLimit())
	writeInt(h, "offset", f.Offset)

	sum := h.Sum(nil)

	return hex.EncodeToString(sum)
}

func writeStr(h *blake3.Hasher, key, val string) {
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{'='})
	_, _ = h.Write([]byte(val))
	_, _ = h.Write([]byte{'\x00'})
}

func writeUint32(h *blake3.Hasher, key string, val uint32) {
	writeStr(h, key, strconv.FormatUint(uint64(val), 10))
}

func writeInt(h *blake3.Hasher, key string, val int) {
	writeStr(h, key, strconv.Itoa(val))
}

func writeTime(h *blake3.Hasher, key string, val time.Time) {
	writeStr(h, key, val.UTC().Format(time.RFC3339Nano))
}
