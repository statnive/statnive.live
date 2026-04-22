// Package sites is the DAO for the statnive.sites table. Hostname → site_id
// resolution happens on every incoming event in the hot path; callers are
// expected to cache.
//
// v1 slice: lookup + list. Create/disable/slug generation land in Phase 11
// (SaaS signup).
package sites

import (
	"context"
	"errors"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ErrUnknownHostname is returned when no row in statnive.sites matches the
// request hostname. Callers map this to HTTP 400.
var ErrUnknownHostname = errors.New("unknown hostname")

// Site is the JSON-serialized row the dashboard's site-switcher consumes.
// TZ is an IANA zone name — the dashboard's date picker renders midnight
// boundaries in this zone (Asia/Tehran for Iran-hosted SamplePlatform,
// operator-set for SaaS tenants outside Iran).
type Site struct {
	ID       uint32 `json:"id"`
	Hostname string `json:"hostname"`
	Enabled  bool   `json:"enabled"`
	TZ       string `json:"tz"`
}

// Registry resolves hostnames to site_id. Backed by a live ClickHouse
// connection; the in-memory cache is deliberately deferred until we have
// benchmarks that show it matters.
type Registry struct {
	conn driver.Conn
}

// New constructs a Registry on top of an existing ClickHouse connection.
func New(conn driver.Conn) *Registry {
	return &Registry{conn: conn}
}

// LookupSiteIDByHostname returns the site_id for a given hostname, or
// ErrUnknownHostname if none is registered / enabled.
func (r *Registry) LookupSiteIDByHostname(ctx context.Context, hostname string) (uint32, error) {
	if hostname == "" {
		return 0, ErrUnknownHostname
	}

	var siteID uint32

	row := r.conn.QueryRow(ctx,
		`SELECT site_id FROM statnive.sites WHERE hostname = ? AND enabled = 1 LIMIT 1`,
		hostname,
	)

	if err := row.Scan(&siteID); err != nil {
		// ClickHouse driver returns a generic error on no-rows; treat any
		// scan failure as ErrUnknownHostname. The handler then 204s the
		// event silently. Real connection failures bubble through ping/health.
		return 0, ErrUnknownHostname
	}

	if siteID == 0 {
		return 0, ErrUnknownHostname
	}

	return siteID, nil
}

// List returns every registered site, ordered by site_id for stable UI
// render. Includes disabled rows — the dashboard renders them greyed out
// so the operator can see the full tenancy picture.
func (r *Registry) List(ctx context.Context) ([]Site, error) {
	rows, err := r.conn.Query(ctx,
		`SELECT site_id, hostname, enabled, tz FROM statnive.sites ORDER BY site_id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("sites list: %w", err)
	}
	defer rows.Close()

	var out []Site

	for rows.Next() {
		var s Site

		var enabled uint8

		if scanErr := rows.Scan(&s.ID, &s.Hostname, &enabled, &s.TZ); scanErr != nil {
			return nil, fmt.Errorf("sites scan: %w", scanErr)
		}

		s.Enabled = enabled != 0

		out = append(out, s)
	}

	return out, rows.Err()
}
