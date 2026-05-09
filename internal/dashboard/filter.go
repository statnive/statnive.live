// Package dashboard wires the read-only dashboard HTTP layer over the
// internal/storage Store interface. Handlers are intentionally thin —
// they parse Filter from URL query strings, call Store, marshal the
// typed result as JSON, and emit one audit-log event per outcome.
//
// Auth is opt-in via BearerTokenMiddleware (this slice). Phase 2b
// replaces it wholesale with bcrypt + sessions + RBAC.
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
)

const (
	// defaultRangeDays is the look-back window when the caller omits ?from.
	defaultRangeDays = 7

	// dateLayout is the only date format the URL parser accepts.
	// Per-site TZ midnights are normalized to UTC for the Filter.
	dateLayout = "2006-01-02"
)

// errBadInput is the umbrella sentinel for any URL-parameter parse
// failure (missing ?site, unparseable ?from/?to, negative ?limit).
// classifyError in errors.go maps it to HTTP 400.
var errBadInput = errors.New("dashboard: bad request")

// resolveSiteTZ looks up the site's IANA TZ from the registry and
// converts to *time.Location. Falls back to UTC if the registry lookup
// fails OR the stored zone fails LoadLocation — both are non-fatal
// because the dashboard would otherwise 500 on every request after a
// stray sites-table corruption. Operator sees boundary drift, not an
// outage.
func resolveSiteTZ(ctx context.Context, lister SiteLister, siteID uint32) *time.Location {
	if lister == nil {
		return time.UTC
	}

	site, err := lister.LookupSiteByID(ctx, siteID)
	if err != nil {
		return time.UTC
	}

	loc, err := time.LoadLocation(site.TZ)
	if err != nil || loc == nil {
		return time.UTC
	}

	return loc
}

// filterFromRequest parses URL query into a storage.Filter. Required:
// ?site (uint32). Optional dimensions all default to empty string. ?from
// + ?to are interpreted as YYYY-MM-DD midnights in the site's
// configured TZ (defaults to Europe/Berlin per
// sites.DefaultTimezone) and converted to UTC half-open [from, to).
// Defaults: ?to = tomorrow site-TZ midnight; ?from = ?to - 7 days. The
// returned Filter is also Validate()d so downstream handlers don't
// need to.
func filterFromRequest(r *http.Request, lister SiteLister) (*storage.Filter, error) {
	q := r.URL.Query()

	siteID, err := parseSiteID(q.Get("site"))
	if err != nil {
		return nil, err
	}

	loc := resolveSiteTZ(r.Context(), lister, siteID)

	from, to, err := parseDateRange(q.Get("from"), q.Get("to"), loc)
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
// When toRaw is empty, to defaults to tomorrow midnight in the supplied
// loc (so the range includes the entire current local-day for the
// site's configured TZ).
func parseDateRange(fromRaw, toRaw string, loc *time.Location) (time.Time, time.Time, error) {
	now := time.Now().In(loc)

	to, err := parseLocalDate(toRaw, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if to.IsZero() {
		// Tomorrow local midnight — half-open includes today.
		to = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).
			AddDate(0, 0, 1)
	}

	from, err := parseLocalDate(fromRaw, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	if from.IsZero() {
		from = to.AddDate(0, 0, -defaultRangeDays)
	}

	return from.UTC(), to.UTC(), nil
}

// parseLocalDate parses YYYY-MM-DD as midnight in loc. Empty input
// returns the zero time.Time so callers can distinguish "missing" from
// "invalid".
func parseLocalDate(raw string, loc *time.Location) (time.Time, error) {
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
