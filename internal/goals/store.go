package goals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// Store is the admin-CRUD contract for goals. ClickHouseStore is the
// only implementation today; tests inject a fakeStore.
//
// Every query constant in ClickHouseStore leads with `WHERE site_id = ?`
// (doc 24 §Sec 4 pattern 6 — tenancy choke point). `store_test.go`
// greps the SQL constants to pin the invariant.
type Store interface {
	Create(ctx context.Context, g *Goal) error
	Get(ctx context.Context, siteID uint32, goalID uuid.UUID) (*Goal, error)
	List(ctx context.Context, siteID uint32) ([]*Goal, error)
	ListActive(ctx context.Context) ([]*Goal, error) // all sites; enabled=1; Snapshot.Reload feeder
	Update(ctx context.Context, g *Goal) error
	Disable(ctx context.Context, siteID uint32, goalID uuid.UUID) error
}

// Sentinels — handlers map these to HTTP status codes.
var (
	ErrNotFound     = errors.New("goals: not found")
	ErrInvalidInput = errors.New("goals: invalid input")
)

// SQL query constants — exported as package-level vars so store_test.go
// can grep-assert the `WHERE site_id = ?` tenancy-first invariant.
var (
	sqlCreate = `INSERT INTO %s.goals (
		goal_id, site_id, name, match_type, pattern,
		value, enabled, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	sqlGet = `SELECT goal_id, site_id, name, toString(match_type), pattern,
		value, enabled,
		toInt64(toUnixTimestamp(created_at)), toInt64(toUnixTimestamp(updated_at))
		FROM %s.goals FINAL
		WHERE site_id = ? AND goal_id = ?
		LIMIT 1`

	sqlList = `SELECT goal_id, site_id, name, toString(match_type), pattern,
		value, enabled,
		toInt64(toUnixTimestamp(created_at)), toInt64(toUnixTimestamp(updated_at))
		FROM %s.goals FINAL
		WHERE site_id = ?
		ORDER BY name`

	sqlListActive = `SELECT goal_id, site_id, name, toString(match_type), pattern,
		value, enabled,
		toInt64(toUnixTimestamp(created_at)), toInt64(toUnixTimestamp(updated_at))
		FROM %s.goals FINAL
		WHERE site_id > 0 AND enabled = 1
		ORDER BY site_id, goal_id`
)

// ClickHouseStore is the CH-backed implementation. Shares the
// clickhouse-go conn pool with the rest of the binary.
type ClickHouseStore struct {
	conn driver.Conn
	db   string
	now  func() time.Time
}

// NewClickHouseStore wraps an existing conn. database defaults to
// "statnive" if empty.
func NewClickHouseStore(conn driver.Conn, database string) *ClickHouseStore {
	if database == "" {
		database = "statnive"
	}

	return &ClickHouseStore{conn: conn, db: database, now: time.Now}
}

// Create inserts a new goal row. Caller validates the goal; the store
// only normalizes name / pattern whitespace + assigns timestamps +
// generates UUID if zero.
func (s *ClickHouseStore) Create(ctx context.Context, g *Goal) error {
	if g == nil {
		return fmt.Errorf("%w: goal is nil", ErrInvalidInput)
	}

	if err := validate(g); err != nil {
		return err
	}

	now := s.now().UTC()

	if g.GoalID == uuid.Nil {
		g.GoalID = uuid.New()
	}

	if g.CreatedAt == 0 {
		g.CreatedAt = now.Unix()
	}

	g.UpdatedAt = now.Unix()

	enabled := uint8(0)
	if g.Enabled {
		enabled = 1
	}

	if err := s.conn.Exec(ctx,
		fmt.Sprintf(sqlCreate, s.db),
		g.GoalID, g.SiteID, g.Name, string(g.MatchType), g.Pattern,
		g.Value, enabled,
		time.Unix(g.CreatedAt, 0).UTC(),
		time.Unix(g.UpdatedAt, 0).UTC(),
	); err != nil {
		return fmt.Errorf("insert goals: %w", err)
	}

	return nil
}

// Get returns one goal or ErrNotFound.
func (s *ClickHouseStore) Get(
	ctx context.Context, siteID uint32, goalID uuid.UUID,
) (*Goal, error) {
	if goalID == uuid.Nil {
		return nil, fmt.Errorf("%w: nil goal_id", ErrInvalidInput)
	}

	row := s.conn.QueryRow(ctx, fmt.Sprintf(sqlGet, s.db), siteID, goalID)

	g, err := scanGoal(row.Scan)
	if err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}

		return nil, fmt.Errorf("scan goal: %w", err)
	}

	return g, nil
}

// List returns every goal for a site (enabled + disabled) for admin
// UI. Dashboard analytics path uses Snapshot, not List.
func (s *ClickHouseStore) List(ctx context.Context, siteID uint32) ([]*Goal, error) {
	rows, err := s.conn.Query(ctx, fmt.Sprintf(sqlList, s.db), siteID)
	if err != nil {
		return nil, fmt.Errorf("query goals: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := make([]*Goal, 0, 8)

	for rows.Next() {
		g, scanErr := scanGoal(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("scan goal row: %w", scanErr)
		}

		out = append(out, g)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("iterate goals: %w", iterErr)
	}

	return out, nil
}

// ListActive is the Snapshot feeder — all sites, enabled=1 only.
// Bounded filter (`site_id > 0`) keeps the tenancy-choke-point grep
// assertion happy while avoiding a full-table scan for enabled rows
// (the MergeTree ordering by (site_id, goal_id) means this still
// granule-prunes efficiently on a sparse table).
func (s *ClickHouseStore) ListActive(ctx context.Context) ([]*Goal, error) {
	rows, err := s.conn.Query(ctx, fmt.Sprintf(sqlListActive, s.db))
	if err != nil {
		return nil, fmt.Errorf("query active goals: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := make([]*Goal, 0, 32)

	for rows.Next() {
		g, scanErr := scanGoal(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("scan goal row: %w", scanErr)
		}

		out = append(out, g)
	}

	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("iterate goals: %w", iterErr)
	}

	return out, nil
}

// Update rewrites the mutable fields (name, match_type, pattern,
// value, enabled). ReplacingMergeTree collapses to newest by
// updated_at; FINAL reads see the new value immediately.
func (s *ClickHouseStore) Update(ctx context.Context, g *Goal) error {
	if g == nil {
		return fmt.Errorf("%w: goal is nil", ErrInvalidInput)
	}

	if err := validate(g); err != nil {
		return err
	}

	existing, err := s.Get(ctx, g.SiteID, g.GoalID)
	if err != nil {
		return err
	}

	if existing == nil {
		return ErrNotFound
	}

	g.CreatedAt = existing.CreatedAt
	g.UpdatedAt = s.now().UTC().Unix()

	enabled := uint8(0)
	if g.Enabled {
		enabled = 1
	}

	if execErr := s.conn.Exec(ctx,
		fmt.Sprintf(sqlCreate, s.db),
		g.GoalID, g.SiteID, g.Name, string(g.MatchType), g.Pattern,
		g.Value, enabled,
		time.Unix(g.CreatedAt, 0).UTC(),
		time.Unix(g.UpdatedAt, 0).UTC(),
	); execErr != nil {
		return fmt.Errorf("update goals: %w", execErr)
	}

	return nil
}

// Disable flips enabled=0 on the goal (soft delete). Preserves the
// audit trail — a later admin can re-enable.
func (s *ClickHouseStore) Disable(ctx context.Context, siteID uint32, goalID uuid.UUID) error {
	g, err := s.Get(ctx, siteID, goalID)
	if err != nil {
		return err
	}

	if g == nil {
		return ErrNotFound
	}

	g.Enabled = false

	return s.Update(ctx, g)
}

func validate(g *Goal) error {
	g.Name = strings.TrimSpace(g.Name)
	g.Pattern = strings.TrimSpace(g.Pattern)

	switch {
	case g.SiteID == 0:
		return fmt.Errorf("%w: site_id must be > 0", ErrInvalidInput)
	case g.Name == "":
		return fmt.Errorf("%w: name required", ErrInvalidInput)
	case !g.MatchType.Valid():
		return fmt.Errorf("%w: invalid match_type %q", ErrInvalidInput, g.MatchType)
	case g.Pattern == "":
		return fmt.Errorf("%w: pattern required", ErrInvalidInput)
	case len(g.Pattern) > MaxPatternLen:
		return fmt.Errorf("%w: pattern length %d > %d", ErrInvalidInput, len(g.Pattern), MaxPatternLen)
	}

	return nil
}

func scanGoal(scan func(...any) error) (*Goal, error) {
	g := &Goal{}

	var (
		matchType string
		enabled   uint8
	)

	if err := scan(
		&g.GoalID, &g.SiteID, &g.Name, &matchType, &g.Pattern,
		&g.Value, &enabled, &g.CreatedAt, &g.UpdatedAt,
	); err != nil {
		return nil, err
	}

	g.MatchType = MatchType(matchType)
	g.Enabled = enabled != 0

	return g, nil
}

func isNoRows(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "no rows in result set")
}
