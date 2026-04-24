// Package sites is the DAO for the statnive.sites table. Hostname → site_id
// resolution happens on every incoming event in the hot path; callers are
// expected to cache.
//
// v1 slice: lookup + list. Phase 6-polish added CreateSite / ListAdmin /
// UpdateSiteEnabled for the operator first-run UX. Phase 11 SaaS signup
// reuses GenerateSlug / IsSlugAvailable / ReserveSlug and a future signup-
// specific CreateSite variant.
package sites

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ErrUnknownHostname is returned when no row in statnive.sites matches the
// request hostname. Callers map this to HTTP 400.
var ErrUnknownHostname = errors.New("unknown hostname")

// ErrHostnameTaken is returned when a CreateSite tries to claim a hostname
// already present in statnive.sites. The API maps this to HTTP 409.
var ErrHostnameTaken = errors.New("sites: hostname taken")

// ErrInvalidHostname is returned when a CreateSite rejects malformed
// input — empty, too long, or containing characters that the hostname
// validator on /api/event would itself reject. Maps to HTTP 400.
var ErrInvalidHostname = errors.New("sites: invalid hostname")

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

// SiteAdmin is the richer projection returned to /api/admin/sites. Adds
// slug + plan + created_at on top of Site for the Admin UI.
type SiteAdmin struct {
	Site

	Slug      string `json:"slug"`
	Plan      string `json:"plan"`
	CreatedAt int64  `json:"created_at"`
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

// ValidateHostname runs the same cheap shape checks the /api/event
// handler applies at ingest. Exported so fakeSitesStore + admin
// handler tests validate identically to the production Registry.
func ValidateHostname(h string) error {
	h = strings.TrimSpace(h)
	if h == "" || len(h) > 253 {
		return ErrInvalidHostname
	}

	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-':
		default:
			return ErrInvalidHostname
		}
	}

	return nil
}

// CreateSite inserts a new row in statnive.sites. Returns ErrHostnameTaken
// if the hostname already exists (any enabled flag), ErrSlugTaken if the
// proposed slug collides with another site's row, or ErrInvalidHostname
// for shape-invalid input. Site ID is allocated server-side via a
// max(site_id)+1 select — safe on single-node; Phase 11 SaaS will
// revisit once multi-writer contention matters.
func (r *Registry) CreateSite(ctx context.Context, hostname, slug, tz string) (uint32, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if err := ValidateHostname(hostname); err != nil {
		return 0, err
	}

	if _, err := r.LookupSiteIDByHostname(ctx, hostname); err == nil {
		return 0, ErrHostnameTaken
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		slug = GenerateSlug(hostname)
	}

	if slug == "" {
		return 0, ErrInvalidHostname
	}

	if _, reserved := reservedSlugs[slug]; reserved {
		return 0, ErrSlugTaken
	}

	if ok, err := r.IsSlugAvailable(ctx, slug); err != nil {
		return 0, err
	} else if !ok {
		return 0, ErrSlugTaken
	}

	tz = strings.TrimSpace(tz)
	if tz == "" {
		tz = "Asia/Tehran"
	}

	var maxID uint32

	if err := r.conn.QueryRow(ctx,
		`SELECT coalesce(max(site_id), 0) FROM statnive.sites`,
	).Scan(&maxID); err != nil {
		return 0, fmt.Errorf("sites max id: %w", err)
	}

	siteID := maxID + 1

	if err := r.conn.Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, plan, enabled, created_at, tz)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		siteID, hostname, slug, "free", uint8(1), time.Now().UTC(), tz,
	); err != nil {
		return 0, fmt.Errorf("sites insert: %w", err)
	}

	return siteID, nil
}

// UpdateSiteEnabled toggles the enabled flag for an existing row. Uses a
// synchronous ALTER UPDATE mutation so the change is visible to the next
// LookupSiteIDByHostname call. Returns ErrUnknownHostname semantics
// (site not found) when site_id doesn't exist.
func (r *Registry) UpdateSiteEnabled(ctx context.Context, siteID uint32, enabled bool) error {
	if siteID == 0 {
		return ErrUnknownHostname
	}

	var exists uint64

	if err := r.conn.QueryRow(ctx,
		`SELECT count() FROM statnive.sites FINAL WHERE site_id = ?`,
		siteID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("sites existence: %w", err)
	}

	if exists == 0 {
		return ErrUnknownHostname
	}

	flag := uint8(0)
	if enabled {
		flag = 1
	}

	if err := r.conn.Exec(ctx,
		`ALTER TABLE statnive.sites UPDATE enabled = ? WHERE site_id = ? SETTINGS mutations_sync = 2`,
		flag, siteID,
	); err != nil {
		return fmt.Errorf("sites update enabled: %w", err)
	}

	return nil
}

// ListAdmin returns every site with the richer SiteAdmin projection —
// adds slug, plan, created_at on top of List() for /api/admin/sites.
func (r *Registry) ListAdmin(ctx context.Context) ([]SiteAdmin, error) {
	rows, err := r.conn.Query(ctx,
		`SELECT site_id, hostname, slug, plan, enabled, tz, toInt64(toUnixTimestamp(created_at))
		 FROM statnive.sites FINAL ORDER BY site_id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("sites list admin: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []SiteAdmin

	for rows.Next() {
		var (
			sa      SiteAdmin
			enabled uint8
		)

		if scanErr := rows.Scan(
			&sa.ID, &sa.Hostname, &sa.Slug, &sa.Plan, &enabled, &sa.TZ, &sa.CreatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("sites scan admin: %w", scanErr)
		}

		sa.Enabled = enabled != 0

		out = append(out, sa)
	}

	return out, rows.Err()
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

	defer func() { _ = rows.Close() }()

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
