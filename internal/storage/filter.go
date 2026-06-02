package storage

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
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

	// Segments Phase 2 — custom-dimension filters keyed at hit / session /
	// user scope. Empty map (or nil) means no constraint; HasPropFilter()
	// reports whether ANY scope carries a key. When at least one prop
	// filter is active, dashboard handlers route to the events_raw
	// raw-fallback path (cached 1h, range capped at 90 days) instead of
	// the rollup. Rollup queries stay byte-identical when all three
	// maps are empty.
	HitProps     map[string]string
	SessionProps map[string]string
	UserProps    map[string]string

	// UI knobs.
	Sort   string // "views" | "visitors" | "goals" | "revenue" | "rpv" | <dim col> | ""
	Dir    string // "asc" | "desc" | "" → defaults to "desc" when Sort is set
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

// MaxPropFilterRange is the longest [From, To) span we accept when ANY
// prop filter is active (Filter.HasPropFilter() == true). The raw-
// fallback query path scans events_raw directly, so the range cap is
// tighter than the rollup default — the plan's § 4 cost-guardrail
// budget pegs P5 worst-case at ~18B rows over 90 days, with the bloom
// indexes (deferred to a follow-up migration) bringing that down ~100×
// in practice.
const MaxPropFilterRange = 90 * 24 * time.Hour

// MaxPropFiltersTotal caps the combined entries across HitProps,
// SessionProps, UserProps. Plausible-precedent; aligns with the
// /api/event server-side validator cap.
const MaxPropFiltersTotal = 30

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

	if f.HasPropFilter() {
		if span := f.To.Sub(f.From); span > MaxPropFilterRange {
			return fmt.Errorf("%w: range %s exceeds prop-filter cap %s (raw-fallback path is range-bounded)",
				ErrInvalidFilter, span, MaxPropFilterRange)
		}

		if len(f.HitProps)+len(f.SessionProps)+len(f.UserProps) > MaxPropFiltersTotal {
			return fmt.Errorf("%w: %d prop filters exceeds cap %d",
				ErrInvalidFilter,
				len(f.HitProps)+len(f.SessionProps)+len(f.UserProps),
				MaxPropFiltersTotal)
		}
	}

	if f.Limit < 0 || f.Offset < 0 {
		return fmt.Errorf("%w: limit and offset must be >= 0", ErrInvalidFilter)
	}

	switch f.Dir {
	case "", "asc", "desc":
	default:
		return fmt.Errorf("%w: dir must be asc, desc, or empty", ErrInvalidFilter)
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

// HasPropFilter reports whether any of the three scoped prop maps
// carries a constraint. When true, dashboard handlers must route the
// query through the raw-fallback path (cached 1h, range-bounded by
// MaxPropFilterRange) instead of the static-schema rollup. When false,
// the rollup path runs byte-identical to pre-Phase-2 behaviour.
func (f *Filter) HasPropFilter() bool {
	if f == nil {
		return false
	}

	return len(f.HitProps)+len(f.SessionProps)+len(f.UserProps) > 0
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
	writeStr(h, "dir", f.Dir)
	writeStr(h, "search", f.Search)
	writeInt(h, "limit", f.EffectiveLimit())
	writeInt(h, "offset", f.Offset)

	// Segments Phase 2 — three scoped prop maps. Sort keys so the same
	// filter from two callers hashes to the same value regardless of
	// map-iteration order. Scope prefix prevents key collisions between
	// scopes (e.g. hit:plan vs user:plan are distinct constraints).
	writePropMap(h, "h", f.HitProps)
	writePropMap(h, "s", f.SessionProps)
	writePropMap(h, "u", f.UserProps)

	sum := h.Sum(nil)

	return hex.EncodeToString(sum)
}

// writePropMap hashes a scoped prop map in key-sorted order so the
// cache key is independent of map-iteration order. Empty maps still
// emit the scope marker so two filters that differ only by an empty
// scope hash identically (a non-issue in practice).
func writePropMap(h *blake3.Hasher, scope string, m map[string]string) {
	if len(m) == 0 {
		writeStr(h, "prop:"+scope, "")

		return
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		writeStr(h, "prop:"+scope+":"+k, m[k])
	}
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
