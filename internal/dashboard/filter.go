// Package dashboard wires the read-only dashboard HTTP layer over the
// internal/storage Store interface. Handlers are intentionally thin —
// they parse Filter from URL query strings, call Store, marshal the
// typed result as JSON, and emit one audit-log event per outcome.
//
// Auth is opt-in via BearerTokenMiddleware (this slice). Phase 2b
// replaces it wholesale with bcrypt + sessions + RBAC.
package dashboard

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
)

const (
	// defaultRangeDays is the look-back window when the caller omits ?from.
	defaultRangeDays = 7

	// dateLayout is the only date format the URL parser accepts.
	// IRST midnights are normalized to UTC for the Filter.
	dateLayout = "2006-01-02"
)

var (
	// irstOnce caches the *time.Location lookup. Mirrors the pattern in
	// internal/identity/salt.go — duplicated here to avoid an import
	// cycle (dashboard → identity is undesirable; identity is pure
	// crypto/identity, dashboard is HTTP).
	irstOnce sync.Once
	irstTZ   *time.Location

	// errBadInput is the umbrella sentinel for any URL-parameter parse
	// failure (missing ?site, unparseable ?from/?to, negative ?limit).
	// classifyError in errors.go maps it to HTTP 400.
	errBadInput = errors.New("dashboard: bad request")
)

// irstLocation returns the Asia/Tehran *time.Location, falling back to
// a fixed +03:30 zone when tzdata is unavailable. Same shape as
// internal/identity/salt.go:irstLocation().
func irstLocation() *time.Location {
	irstOnce.Do(func() {
		if loc, err := time.LoadLocation("Asia/Tehran"); err == nil {
			irstTZ = loc

			return
		}

		irstTZ = time.FixedZone("IRST", int(3.5*float64(time.Hour/time.Second)))
	})

	return irstTZ
}

// filterFromRequest parses URL query into a storage.Filter. Required:
// ?site (uint32). Optional dimensions all default to empty string. ?from
// + ?to are interpreted as YYYY-MM-DD IRST date midnights and converted
// to UTC half-open [from, to). Defaults: ?to = tomorrow IRST midnight;
// ?from = ?to - 7 days. The returned Filter is also Validate()d so
// downstream handlers don't need to.
func filterFromRequest(r *http.Request) (*storage.Filter, error) {
	q := r.URL.Query()

	siteID, err := parseSiteID(q.Get("site"))
	if err != nil {
		return nil, err
	}

	from, to, err := parseDateRange(q.Get("from"), q.Get("to"))
	if err != nil {
		return nil, err
	}

	limit, err := parseUintParam(q.Get("limit"), "limit")
	if err != nil {
		return nil, err
	}

	offset, err := parseUintParam(q.Get("offset"), "offset")
	if err != nil {
		return nil, err
	}

	f := &storage.Filter{
		SiteID:      siteID,
		From:        from,
		To:          to,
		Path:        q.Get("path"),
		Referrer:    q.Get("referrer"),
		Channel:     q.Get("channel"),
		UTMSource:   q.Get("utm_source"),
		UTMMedium:   q.Get("utm_medium"),
		UTMCampaign: q.Get("utm_campaign"),
		UTMContent:  q.Get("utm_content"),
		UTMTerm:     q.Get("utm_term"),
		Country:     q.Get("country"),
		Browser:     q.Get("browser"),
		OS:          q.Get("os"),
		Device:      q.Get("device"),
		Sort:        q.Get("sort"),
		Search:      q.Get("search"),
		Limit:       limit,
		Offset:      offset,
	}

	if err := f.Validate(); err != nil {
		return nil, err
	}

	return f, nil
}

// parseSiteID extracts a uint32 site_id from a query value. Empty or
// invalid value is rejected with errBadInput so writeError maps to
// HTTP 400 — every dashboard query MUST be tenant-scoped.
func parseSiteID(raw string) (uint32, error) {
	if raw == "" {
		return 0, errBadInput
	}

	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("%w: %q is not a positive uint32", errBadInput, raw)
	}

	return uint32(n), nil
}

// parseDateRange returns the [from, to) half-open interval in UTC. When
// fromRaw is empty the range defaults to (defaultRangeDays before to).
// When toRaw is empty, to defaults to tomorrow IRST midnight (so the
// range includes the entire current IRST day).
func parseDateRange(fromRaw, toRaw string) (time.Time, time.Time, error) {
	loc := irstLocation()
	now := time.Now().In(loc)

	to, err := parseIRSTDate(toRaw, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if to.IsZero() {
		// Tomorrow midnight IRST — half-open includes today.
		to = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).
			AddDate(0, 0, 1)
	}

	from, err := parseIRSTDate(fromRaw, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if from.IsZero() {
		from = to.AddDate(0, 0, -defaultRangeDays)
	}

	return from.UTC(), to.UTC(), nil
}

// parseIRSTDate parses YYYY-MM-DD as IRST midnight. Empty input returns
// the zero time.Time so callers can distinguish "missing" from "invalid".
func parseIRSTDate(raw string, loc *time.Location) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}

	t, err := time.ParseInLocation(dateLayout, raw, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", errBadInput, raw)
	}

	return t, nil
}

func parseUintParam(raw, name string) (int, error) {
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%w: ?%s = %q is not a non-negative integer", errBadInput, name, raw)
	}

	return n, nil
}
