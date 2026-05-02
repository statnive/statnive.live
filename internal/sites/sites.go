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

// SitePolicy is the per-site privacy + bot-tracking posture, written
// to statnive.sites by migration 006. Replaces the global
// cfg.consent.respect_* / cfg.consent.track_bots flags so multi-tenant
// operators can serve EU + non-EU customers from the same binary.
//
// Defaults (operator unset): RespectDNT=false, RespectGPC=false,
// TrackBots=true — preserves the post-PR-#78 SaaS posture (count every
// visit, suppress identity only on operator-flipped opt-in).
type SitePolicy struct {
	RespectDNT bool `json:"respect_dnt"`
	RespectGPC bool `json:"respect_gpc"`
	TrackBots  bool `json:"track_bots"`
}

// SiteAdmin is the richer projection returned to /api/admin/sites. Adds
// slug + plan + created_at + per-site privacy/bot policy on top of Site
// for the Admin UI. The policy fields drive the three Site Settings
// checkboxes (CLAUDE.md Privacy Rule 6 + migration 006).
type SiteAdmin struct {
	Site
	SitePolicy

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
// ErrUnknownHostname if none is registered / enabled. Kept for callers
// that don't yet need the per-site policy (admin paths, tests). The
// hot ingest path uses LookupSitePolicy instead.
func (r *Registry) LookupSiteIDByHostname(ctx context.Context, hostname string) (uint32, error) {
	siteID, _, err := r.LookupSitePolicy(ctx, hostname)

	return siteID, err
}

// LookupSitePolicy returns the site_id AND the per-site privacy +
// bot-tracking policy in a single round-trip. The hot ingest path
// (internal/ingest/handler.go) calls this so the consent gate +
// burst-vs-bot decision happen against per-site flags rather than the
// (now-removed) global cfg.consent.respect_* surface.
//
// Returns ErrUnknownHostname if no enabled row matches. Sites without
// migration 006 columns yet (very old deployments mid-upgrade) get the
// default policy (count every visit, no DNT/GPC suppression, bots
// flagged-not-dropped).
func (r *Registry) LookupSitePolicy(ctx context.Context, hostname string) (uint32, SitePolicy, error) {
	var policy SitePolicy

	if hostname == "" {
		return 0, policy, ErrUnknownHostname
	}

	var (
		siteID                            uint32
		respectDNT, respectGPC, trackBots uint8
	)

	row := r.conn.QueryRow(ctx,
		`SELECT site_id, respect_dnt, respect_gpc, track_bots
		 FROM statnive.sites
		 WHERE hostname = ? AND enabled = 1 LIMIT 1`,
		hostname,
	)

	if err := row.Scan(&siteID, &respectDNT, &respectGPC, &trackBots); err != nil {
		// ClickHouse driver returns a generic error on no-rows; treat any
		// scan failure as ErrUnknownHostname. The handler then 204s the
		// event silently. Real connection failures bubble through ping/health.
		return 0, policy, ErrUnknownHostname
	}

	if siteID == 0 {
		return 0, policy, ErrUnknownHostname
	}

	policy.RespectDNT = respectDNT != 0
	policy.RespectGPC = respectGPC != 0
	policy.TrackBots = trackBots != 0

	return siteID, policy, nil
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

// UpdateSitePolicy mutates the per-site privacy + bot flags
// (statnive.sites.respect_dnt / respect_gpc / track_bots — migration
// 006). Uses synchronous ALTER UPDATE so the change is visible to the
// next LookupSitePolicy call. ErrUnknownHostname semantics for
// non-existent site_id.
func (r *Registry) UpdateSitePolicy(ctx context.Context, siteID uint32, policy SitePolicy) error {
	if siteID == 0 {
		return ErrUnknownHostname
	}

	var exists uint64

	if err := r.conn.QueryRow(ctx,
		`SELECT count() FROM statnive.sites WHERE site_id = ?`,
		siteID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("sites existence: %w", err)
	}

	if exists == 0 {
		return ErrUnknownHostname
	}

	if err := r.conn.Exec(ctx,
		`ALTER TABLE statnive.sites
		 UPDATE respect_dnt = ?, respect_gpc = ?, track_bots = ?
		 WHERE site_id = ?
		 SETTINGS mutations_sync = 2`,
		boolU8(policy.RespectDNT), boolU8(policy.RespectGPC), boolU8(policy.TrackBots),
		siteID,
	); err != nil {
		return fmt.Errorf("sites update policy: %w", err)
	}

	return nil
}

func boolU8(b bool) uint8 {
	if b {
		return 1
	}

	return 0
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
		`SELECT count() FROM statnive.sites WHERE site_id = ?`,
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

// LookupSiteByID returns a single SiteAdmin row keyed by site_id, the
// efficient single-row complement to ListAdmin used by admin PATCH
// flows that need to read-modify-write a specific site without
// scanning the whole table. Returns ErrUnknownHostname (semantic
// site-not-found) when site_id doesn't exist.
func (r *Registry) LookupSiteByID(ctx context.Context, siteID uint32) (SiteAdmin, error) {
	if siteID == 0 {
		return SiteAdmin{}, ErrUnknownHostname
	}

	var (
		sa                                         SiteAdmin
		enabled, respectDNT, respectGPC, trackBots uint8
	)

	row := r.conn.QueryRow(ctx,
		`SELECT site_id, hostname, slug, plan, enabled, tz,
		        toInt64(toUnixTimestamp(created_at)),
		        respect_dnt, respect_gpc, track_bots
		 FROM statnive.sites WHERE site_id = ? LIMIT 1`,
		siteID,
	)

	if err := row.Scan(
		&sa.ID, &sa.Hostname, &sa.Slug, &sa.Plan, &enabled, &sa.TZ, &sa.CreatedAt,
		&respectDNT, &respectGPC, &trackBots,
	); err != nil {
		return SiteAdmin{}, ErrUnknownHostname
	}

	if sa.ID == 0 {
		return SiteAdmin{}, ErrUnknownHostname
	}

	sa.Enabled = enabled != 0
	sa.RespectDNT = respectDNT != 0
	sa.RespectGPC = respectGPC != 0
	sa.TrackBots = trackBots != 0

	return sa, nil
}

// ListAdmin returns every site with the richer SiteAdmin projection —
// adds slug, plan, created_at, and the per-site privacy + bot policy
// (migration 006) on top of List() for /api/admin/sites.
func (r *Registry) ListAdmin(ctx context.Context) ([]SiteAdmin, error) {
	// statnive.sites is plain MergeTree — FINAL is rejected. Duplicate
	// rows can only appear if migration 001 changes engines; the
	// integration test pins that invariant.
	rows, err := r.conn.Query(ctx,
		`SELECT site_id, hostname, slug, plan, enabled, tz,
		        toInt64(toUnixTimestamp(created_at)),
		        respect_dnt, respect_gpc, track_bots
		 FROM statnive.sites ORDER BY site_id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("sites list admin: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []SiteAdmin

	for rows.Next() {
		var (
			sa                                         SiteAdmin
			enabled, respectDNT, respectGPC, trackBots uint8
		)

		if scanErr := rows.Scan(
			&sa.ID, &sa.Hostname, &sa.Slug, &sa.Plan, &enabled, &sa.TZ, &sa.CreatedAt,
			&respectDNT, &respectGPC, &trackBots,
		); scanErr != nil {
			return nil, fmt.Errorf("sites scan admin: %w", scanErr)
		}

		sa.Enabled = enabled != 0
		sa.RespectDNT = respectDNT != 0
		sa.RespectGPC = respectGPC != 0
		sa.TrackBots = trackBots != 0

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
