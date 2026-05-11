package auth

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// SitesStore is the per-user-site grant store backed by
// statnive.user_sites (migration 010). Every read goes through FINAL
// so revokes (insert with revoked=1) take effect on the next request.
// The table is bounded by enabled-users × enabled-sites — FINAL costs
// microseconds at SaaS scale.
//
// Tenancy choke point (CLAUDE.md Architecture Rule 8) does NOT apply:
// user_sites is an authorization-grant table, not a dashboard analytics
// path. Authoritative WHERE leads with user_id.
//
// Nil-safety invariant: every method returning (*T, error) returns
// (nil, err) on failure. Map-returning methods return a non-nil empty
// map on success-with-zero-rows.
type SitesStore interface {
	// LoadUserSites returns the active (non-revoked) per-site role
	// grants for userID. Empty map (non-nil) when the user has no
	// grants. Called per admin request — must be fast.
	LoadUserSites(ctx context.Context, userID uuid.UUID) (map[uint32]Role, error)

	// Grant upserts a (userID, siteID, role) row. Calling Grant twice
	// with the same (userID, siteID) collapses to one effective row via
	// ReplacingMergeTree on the next merge; readers via FINAL see the
	// latest immediately.
	Grant(ctx context.Context, userID uuid.UUID, siteID uint32, role Role) error

	// Revoke inserts a revoked=1 row for (userID, siteID) with
	// updated_at=now() so future LoadUserSites calls skip the grant.
	Revoke(ctx context.Context, userID uuid.UUID, siteID uint32) error

	// ListUsersBySite returns the (user_id, role) grants for every
	// active user on siteID. Used by GET /api/admin/users?site_id=X.
	ListUsersBySite(ctx context.Context, siteID uint32) ([]UserSiteGrant, error)
}

// UserSiteGrant is the projection ListUsersBySite returns.
type UserSiteGrant struct {
	UserID uuid.UUID
	Role   Role
}

// ClickHouseSitesStore implements SitesStore against the production
// CH connection. Shares the same driver.Conn as the rest of auth.
type ClickHouseSitesStore struct {
	conn driver.Conn
	db   string
}

// NewClickHouseSitesStore wraps an existing CH connection. Mirrors
// NewClickHouseStore's shape so main.go can hand both stores the same
// driver.Conn / database name.
func NewClickHouseSitesStore(conn driver.Conn, database string) *ClickHouseSitesStore {
	if database == "" {
		database = "statnive"
	}

	return &ClickHouseSitesStore{conn: conn, db: database}
}

// LoadUserSites queries every active grant for userID. Latency budget:
// a few hundred µs against a tiny FINAL-merged table.
func (s *ClickHouseSitesStore) LoadUserSites(
	ctx context.Context, userID uuid.UUID,
) (map[uint32]Role, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("%w: nil user_id", ErrInvalidInput)
	}

	const q = `SELECT site_id, toString(role)
		FROM %s.user_sites FINAL
		WHERE user_id = ? AND revoked = 0`

	rows, err := s.conn.Query(ctx, fmt.Sprintf(q, s.db), userID)
	if err != nil {
		return nil, fmt.Errorf("query user_sites: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := make(map[uint32]Role, 4)

	for rows.Next() {
		var (
			siteID  uint32
			roleStr string
		)

		if scanErr := rows.Scan(&siteID, &roleStr); scanErr != nil {
			return nil, fmt.Errorf("scan user_sites row: %w", scanErr)
		}

		r := Role(roleStr)
		if !r.Valid() {
			// Fail closed: an unknown role on disk → drop the grant
			// rather than treat it as admin.
			continue
		}

		out[siteID] = r
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("iterate user_sites: %w", iterErr)
	}

	return out, nil
}

// Grant inserts a fresh row. The caller is the audit/audit-trail owner
// — the store does not emit audit events itself.
func (s *ClickHouseSitesStore) Grant(
	ctx context.Context, userID uuid.UUID, siteID uint32, role Role,
) error {
	if userID == uuid.Nil {
		return fmt.Errorf("%w: nil user_id", ErrInvalidInput)
	}

	if siteID == 0 {
		return fmt.Errorf("%w: zero site_id", ErrInvalidInput)
	}

	if !role.Valid() {
		return fmt.Errorf("%w: invalid role %q", ErrInvalidInput, role)
	}

	const q = `INSERT INTO %s.user_sites
		(user_id, site_id, role, revoked) VALUES (?, ?, ?, 0)`

	if err := s.conn.Exec(ctx, fmt.Sprintf(q, s.db), userID, siteID, string(role)); err != nil {
		return fmt.Errorf("insert user_sites grant: %w", err)
	}

	return nil
}

// Revoke inserts a row with revoked=1. ReplacingMergeTree collapses
// to the new row on merge; FINAL reads see the revoke immediately.
//
// Two CH round-trips by design: loadOneGrant first, then INSERT. The
// audit trail wants "admin grant on site X was revoked" — not "unknown
// role revoked" — so the revoke row preserves the original role.
// Revoke is operator-rare (not per-request), so the extra round-trip
// is acceptable; an "always insert role=admin" shortcut would lose
// audit fidelity.
func (s *ClickHouseSitesStore) Revoke(
	ctx context.Context, userID uuid.UUID, siteID uint32,
) error {
	if userID == uuid.Nil {
		return fmt.Errorf("%w: nil user_id", ErrInvalidInput)
	}

	if siteID == 0 {
		return fmt.Errorf("%w: zero site_id", ErrInvalidInput)
	}

	current, err := s.loadOneGrant(ctx, userID, siteID)
	if err != nil {
		return err
	}

	if current == "" {
		// No active grant — revoke is a no-op. Don't insert a phantom
		// row so the audit doesn't double-count.
		return nil
	}

	const q = `INSERT INTO %s.user_sites
		(user_id, site_id, role, revoked) VALUES (?, ?, ?, 1)`

	if execErr := s.conn.Exec(ctx, fmt.Sprintf(q, s.db),
		userID, siteID, string(current),
	); execErr != nil {
		return fmt.Errorf("insert user_sites revoke: %w", execErr)
	}

	return nil
}

// loadOneGrant returns the active role for (user, site) — empty string
// if no active grant. Used by Revoke to preserve the role on the
// revoke-row.
func (s *ClickHouseSitesStore) loadOneGrant(
	ctx context.Context, userID uuid.UUID, siteID uint32,
) (Role, error) {
	const q = `SELECT toString(role) FROM %s.user_sites FINAL
		WHERE user_id = ? AND site_id = ? AND revoked = 0
		LIMIT 1`

	row := s.conn.QueryRow(ctx, fmt.Sprintf(q, s.db), userID, siteID)

	var roleStr string
	if err := row.Scan(&roleStr); err != nil {
		if isNoRows(err) {
			return "", nil
		}

		return "", fmt.Errorf("scan user_sites grant: %w", err)
	}

	return Role(roleStr), nil
}

// ListUsersBySite returns one row per user with an active grant on
// siteID. Used by GET /api/admin/users?site_id=X to render the per-site
// user list.
func (s *ClickHouseSitesStore) ListUsersBySite(
	ctx context.Context, siteID uint32,
) ([]UserSiteGrant, error) {
	if siteID == 0 {
		return nil, fmt.Errorf("%w: zero site_id", ErrInvalidInput)
	}

	const q = `SELECT user_id, toString(role)
		FROM %s.user_sites FINAL
		WHERE site_id = ? AND revoked = 0
		ORDER BY user_id`

	rows, err := s.conn.Query(ctx, fmt.Sprintf(q, s.db), siteID)
	if err != nil {
		return nil, fmt.Errorf("query user_sites by site: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := make([]UserSiteGrant, 0, 8)

	for rows.Next() {
		var (
			g       UserSiteGrant
			roleStr string
		)

		if scanErr := rows.Scan(&g.UserID, &roleStr); scanErr != nil {
			return nil, fmt.Errorf("scan user_sites row: %w", scanErr)
		}

		g.Role = Role(roleStr)
		if !g.Role.Valid() {
			continue
		}

		out = append(out, g)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("iterate user_sites: %w", iterErr)
	}

	return out, nil
}
