package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// Store is the auth-package contract. Implementations live in this
// package (ClickHouseStore) and in testing helpers (internal/auth/mock
// will land if we need it; integration tests use the real CH store).
//
// Nil-safety invariant (PLAN.md §53 / CVE-2024-10924): every method
// returning (*T, error) returns (nil, err) on failure — never
// (nil, nil). The unit tests at nilguard_test.go assert this.
type Store interface {
	// User CRUD.
	CreateUser(ctx context.Context, u *User, passwordHash string) error
	GetUserByEmail(ctx context.Context, siteID uint32, email string) (*User, string, error) // user, password_hash, err
	GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error)
	ListUsers(ctx context.Context, siteID uint32) ([]*User, error)

	// User mutations — each of these MUST cascade-revoke all active
	// sessions for the affected user. The cascade is a contract, not an
	// optional convenience: admin demoting a user from admin → viewer
	// without revoking sessions is the cross-tenant escalation shape
	// CVE-2024-10924 embodies.
	UpdateUserPassword(ctx context.Context, userID uuid.UUID, newHash string) error
	DisableUser(ctx context.Context, userID uuid.UUID) error
	ChangeRole(ctx context.Context, userID uuid.UUID, newRole Role) error

	// Session CRUD.
	CreateSession(ctx context.Context, s *Session, ipHash [16]byte, userAgent string) error
	LookupSession(ctx context.Context, hash [32]byte) (*SessionInfo, error)
	RevokeSession(ctx context.Context, hash [32]byte) error
	RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error
}

// ClickHouseStore implements Store against ClickHouse via the existing
// connection pool opened in internal/storage. A wrapping cache layer
// (see NewCachedStore) is what production middleware actually holds —
// this type is the cache-miss backing.
type ClickHouseStore struct {
	conn driver.Conn
	db   string // schema name; "statnive" in all deploys today
	now  func() time.Time
}

// NewClickHouseStore wraps an existing clickhouse-go connection.
// main.go shares the same driver.Conn between the events_raw insert
// pipeline and the auth store — one pool, one healthcheck, one set of
// connection settings.
func NewClickHouseStore(conn driver.Conn, database string) *ClickHouseStore {
	if database == "" {
		database = "statnive"
	}

	return &ClickHouseStore{conn: conn, db: database, now: time.Now}
}

// normalizeEmail lowercases + trims whitespace. Applied on every write
// AND every lookup so case variants collide at the application layer
// (ClickHouse doesn't enforce uniqueness — we do).
func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// CreateUser inserts a new users row. Two admins racing on the same
// email is possible (CH has no uniqueness constraint) — in Phase 2b
// users are admin-seeded and the race is acceptably rare; the caller
// is expected to GetUserByEmail first and reject ErrAlreadyExists.
func (s *ClickHouseStore) CreateUser(ctx context.Context, u *User, passwordHash string) error {
	if u == nil {
		return fmt.Errorf("%w: user is nil", ErrInvalidInput)
	}

	if !u.Role.Valid() {
		return fmt.Errorf("%w: invalid role %q", ErrInvalidInput, u.Role)
	}

	u.Email = normalizeEmail(u.Email)
	if u.Email == "" {
		return fmt.Errorf("%w: empty email", ErrInvalidInput)
	}

	if passwordHash == "" {
		return fmt.Errorf("%w: empty password hash", ErrInvalidInput)
	}

	now := s.now().UTC()
	if u.CreatedAt == 0 {
		u.CreatedAt = now.Unix()
	}

	u.UpdatedAt = now.Unix()

	if u.UserID == uuid.Nil {
		u.UserID = uuid.New()
	}

	const q = `INSERT INTO %s.users (
		user_id, site_id, email, username, password_hash,
		role, disabled, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	disabled := uint8(0)
	if u.Disabled {
		disabled = 1
	}

	if err := s.conn.Exec(ctx,
		fmt.Sprintf(q, s.db),
		u.UserID, u.SiteID, u.Email, u.Username, passwordHash,
		string(u.Role), disabled,
		time.Unix(u.CreatedAt, 0).UTC(),
		time.Unix(u.UpdatedAt, 0).UTC(),
	); err != nil {
		return fmt.Errorf("insert users: %w", err)
	}

	return nil
}

// GetUserByEmail returns the newest row for (site_id, email) via FINAL
// (ReplacingMergeTree merges newest by updated_at). Used by login; hot-
// path callers wrap this in the session cache for other reads.
func (s *ClickHouseStore) GetUserByEmail(
	ctx context.Context, siteID uint32, email string,
) (*User, string, error) {
	email = normalizeEmail(email)
	if email == "" {
		return nil, "", fmt.Errorf("%w: empty email", ErrInvalidInput)
	}

	const q = `SELECT user_id, site_id, email, username, password_hash,
		toString(role), disabled, toInt64(toUnixTimestamp(created_at)), toInt64(toUnixTimestamp(updated_at))
		FROM %s.users FINAL
		WHERE site_id = ? AND email = ?
		LIMIT 1`

	row := s.conn.QueryRow(ctx, fmt.Sprintf(q, s.db), siteID, email)

	u := &User{}

	var (
		roleStr  string
		disabled uint8
		pwHash   string
	)

	if err := row.Scan(
		&u.UserID, &u.SiteID, &u.Email, &u.Username, &pwHash,
		&roleStr, &disabled, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		if isNoRows(err) {
			return nil, "", ErrNotFound
		}

		return nil, "", fmt.Errorf("scan user: %w", err)
	}

	u.Role = Role(roleStr)
	u.Disabled = disabled != 0

	return u, pwHash, nil
}

// GetUserByID looks up by user_id, scanning across sites (user_id is
// unique globally via uuid.New()). Used by session lookup to refresh
// the *User pointer on cache miss.
func (s *ClickHouseStore) GetUserByID(ctx context.Context, userID uuid.UUID) (*User, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("%w: nil user_id", ErrInvalidInput)
	}

	const q = `SELECT user_id, site_id, email, username,
		toString(role), disabled, toInt64(toUnixTimestamp(created_at)), toInt64(toUnixTimestamp(updated_at))
		FROM %s.users FINAL
		WHERE user_id = ?
		LIMIT 1`

	row := s.conn.QueryRow(ctx, fmt.Sprintf(q, s.db), userID)

	u := &User{}

	var (
		roleStr  string
		disabled uint8
	)

	if err := row.Scan(
		&u.UserID, &u.SiteID, &u.Email, &u.Username,
		&roleStr, &disabled, &u.CreatedAt, &u.UpdatedAt,
	); err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}

		return nil, fmt.Errorf("scan user: %w", err)
	}

	u.Role = Role(roleStr)
	u.Disabled = disabled != 0

	return u, nil
}

// ListUsers returns all users for a site (admin CRUD preview — Phase 3c
// adds pagination). For v1 admin deployments with 10s of users this is
// a full scan of a tiny table.
func (s *ClickHouseStore) ListUsers(ctx context.Context, siteID uint32) ([]*User, error) {
	const q = `SELECT user_id, site_id, email, username,
		toString(role), disabled, toInt64(toUnixTimestamp(created_at)), toInt64(toUnixTimestamp(updated_at))
		FROM %s.users FINAL
		WHERE site_id = ?
		ORDER BY email`

	rows, err := s.conn.Query(ctx, fmt.Sprintf(q, s.db), siteID)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	out := make([]*User, 0, 16)

	for rows.Next() {
		u := &User{}

		var (
			roleStr  string
			disabled uint8
		)

		if scanErr := rows.Scan(
			&u.UserID, &u.SiteID, &u.Email, &u.Username,
			&roleStr, &disabled, &u.CreatedAt, &u.UpdatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan user row: %w", scanErr)
		}

		u.Role = Role(roleStr)
		u.Disabled = disabled != 0
		out = append(out, u)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate users: %w", rowsErr)
	}

	return out, nil
}

// mutateUser is the shared "load → apply change → re-insert" helper
// behind UpdateUserPassword / DisableUser / ChangeRole. Pass newHash
// == "" to preserve the existing password; newRole == "" to preserve
// the existing role; disable flips Disabled to true when requested.
//
// Callers MUST pair the mutation with RevokeAllUserSessions — the
// CachedStore wrapper does that cascade (CVE-2024-10924 class).
// Bare ClickHouseStore calls bypass the cascade and are test-only.
func (s *ClickHouseStore) mutateUser(
	ctx context.Context, userID uuid.UUID, newHash string, newRole Role, disable bool,
) error {
	if userID == uuid.Nil {
		return fmt.Errorf("%w: nil user_id", ErrInvalidInput)
	}

	if newRole != "" && !newRole.Valid() {
		return fmt.Errorf("%w: invalid role %q", ErrInvalidInput, newRole)
	}

	u, err := s.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	if u == nil {
		// Belt-and-braces nil-guard (PLAN.md §53).
		return ErrNotFound
	}

	pwHash := newHash
	if pwHash == "" {
		pwHash, err = s.selectPasswordHash(ctx, userID)
		if err != nil {
			return err
		}
	}

	if newRole != "" {
		u.Role = newRole
	}

	if disable {
		u.Disabled = true
	}

	u.UpdatedAt = s.now().UTC().Unix()

	return s.insertUserRow(ctx, u, pwHash)
}

// UpdateUserPassword writes a new updated_at row with a new
// password_hash. ReplacingMergeTree collapses to the new hash on the
// next merge; FINAL reads see the new value immediately.
func (s *ClickHouseStore) UpdateUserPassword(
	ctx context.Context, userID uuid.UUID, newHash string,
) error {
	if newHash == "" {
		return fmt.Errorf("%w: empty password hash", ErrInvalidInput)
	}

	return s.mutateUser(ctx, userID, newHash, "", false)
}

// DisableUser flips disabled=1 and bumps updated_at.
func (s *ClickHouseStore) DisableUser(ctx context.Context, userID uuid.UUID) error {
	return s.mutateUser(ctx, userID, "", "", true)
}

// ChangeRole swaps the role on a user. RBAC demotion that doesn't
// revoke sessions is the CVE-2024-10924 escalation shape — CachedStore
// wrapper enforces the cascade.
func (s *ClickHouseStore) ChangeRole(
	ctx context.Context, userID uuid.UUID, newRole Role,
) error {
	return s.mutateUser(ctx, userID, "", newRole, false)
}

func (s *ClickHouseStore) insertUserRow(ctx context.Context, u *User, pwHash string) error {
	const q = `INSERT INTO %s.users (
		user_id, site_id, email, username, password_hash,
		role, disabled, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	disabled := uint8(0)
	if u.Disabled {
		disabled = 1
	}

	if err := s.conn.Exec(ctx,
		fmt.Sprintf(q, s.db),
		u.UserID, u.SiteID, u.Email, u.Username, pwHash,
		string(u.Role), disabled,
		time.Unix(u.CreatedAt, 0).UTC(),
		time.Unix(u.UpdatedAt, 0).UTC(),
	); err != nil {
		return fmt.Errorf("insert users (update): %w", err)
	}

	return nil
}

func (s *ClickHouseStore) selectPasswordHash(ctx context.Context, userID uuid.UUID) (string, error) {
	const q = `SELECT password_hash FROM %s.users FINAL
		WHERE user_id = ? LIMIT 1`

	row := s.conn.QueryRow(ctx, fmt.Sprintf(q, s.db), userID)

	var hash string
	if err := row.Scan(&hash); err != nil {
		if isNoRows(err) {
			return "", ErrNotFound
		}

		return "", fmt.Errorf("scan password_hash: %w", err)
	}

	return hash, nil
}

// CreateSession inserts a new sessions row. The caller has already
// hashed the raw token; Session.IDHash is written verbatim.
func (s *ClickHouseStore) CreateSession(
	ctx context.Context, sess *Session, ipHash [16]byte, userAgent string,
) error {
	if sess == nil {
		return fmt.Errorf("%w: session is nil", ErrInvalidInput)
	}

	if sess.UserID == uuid.Nil {
		return fmt.Errorf("%w: session user_id is nil", ErrInvalidInput)
	}

	const q = `INSERT INTO %s.sessions (
		session_id_hash, user_id, site_id, role,
		created_at, last_used_at, expires_at, revoked_at, updated_at,
		ip_hash, user_agent
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	now := s.now().UTC()
	if sess.CreatedAt == 0 {
		sess.CreatedAt = now.Unix()
	}

	if sess.LastUsedAt == 0 {
		sess.LastUsedAt = now.Unix()
	}

	if err := s.conn.Exec(ctx,
		fmt.Sprintf(q, s.db),
		string(sess.IDHash[:]), sess.UserID, sess.SiteID, string(sess.Role),
		time.Unix(sess.CreatedAt, 0).UTC(),
		time.Unix(sess.LastUsedAt, 0).UTC(),
		time.Unix(sess.ExpiresAt, 0).UTC(),
		time.Unix(sess.RevokedAt, 0).UTC(),
		now,
		string(ipHash[:]), userAgent,
	); err != nil {
		return fmt.Errorf("insert sessions: %w", err)
	}

	return nil
}

// LookupSession loads the newest row for a hash and joins the user.
// Returns ErrNotFound if the session was never seen, ErrRevoked if
// revoked_at > 0, ErrExpired if past expires_at, ErrDisabled if the
// owning user is disabled. On any success the returned SessionInfo has
// both pointers non-nil.
func (s *ClickHouseStore) LookupSession(ctx context.Context, hash [32]byte) (*SessionInfo, error) {
	const q = `SELECT
		s.session_id_hash, s.user_id, s.site_id, toString(s.role),
		toInt64(toUnixTimestamp(s.created_at)), toInt64(toUnixTimestamp(s.last_used_at)),
		toInt64(toUnixTimestamp(s.expires_at)), toInt64(toUnixTimestamp(s.revoked_at))
		FROM %s.sessions AS s FINAL
		WHERE s.session_id_hash = ?
		LIMIT 1`

	row := s.conn.QueryRow(ctx, fmt.Sprintf(q, s.db), string(hash[:]))

	sess := &Session{}

	var (
		hashOut string
		roleStr string
	)

	if err := row.Scan(
		&hashOut, &sess.UserID, &sess.SiteID, &roleStr,
		&sess.CreatedAt, &sess.LastUsedAt,
		&sess.ExpiresAt, &sess.RevokedAt,
	); err != nil {
		if isNoRows(err) {
			return nil, ErrNotFound
		}

		return nil, fmt.Errorf("scan session: %w", err)
	}

	if len(hashOut) != 32 || !constantTimeEq([]byte(hashOut), hash[:]) {
		// Defense in depth: CH returned a different hash than we asked
		// for. Should be unreachable.
		return nil, ErrNotFound
	}

	copy(sess.IDHash[:], hashOut)
	sess.Role = Role(roleStr)

	now := s.now().UTC().Unix()

	switch {
	case sess.RevokedAt > 0:
		return nil, ErrRevoked
	case sess.ExpiresAt > 0 && sess.ExpiresAt <= now:
		return nil, ErrExpired
	}

	u, err := s.GetUserByID(ctx, sess.UserID)
	if err != nil {
		return nil, err
	}

	if u == nil { // PLAN.md §53 nil-guard.
		return nil, ErrNotFound
	}

	if u.Disabled {
		return nil, ErrDisabled
	}

	// Session.Role is authoritative for the REQUEST: if an admin
	// demoted the user, the session was revoked by cascade before we
	// got here. The user.Role at lookup time is the latest truth.
	if u.Role != sess.Role {
		// Role drift without session revoke means someone bypassed the
		// cascade. Fail closed — treat as revoked.
		return nil, ErrRevoked
	}

	return &SessionInfo{User: u, Session: sess}, nil
}

// RevokeSession flips revoked_at = now on one session_id_hash.
func (s *ClickHouseStore) RevokeSession(ctx context.Context, hash [32]byte) error {
	info, err := s.LookupSession(ctx, hash)
	if err != nil && !errors.Is(err, ErrExpired) && !errors.Is(err, ErrRevoked) {
		return err
	}

	// Already revoked / expired → idempotent no-op.
	if info == nil {
		return nil
	}

	now := s.now().UTC()
	info.Session.RevokedAt = now.Unix()

	const q = `INSERT INTO %s.sessions (
		session_id_hash, user_id, site_id, role,
		created_at, last_used_at, expires_at, revoked_at, updated_at,
		ip_hash, user_agent
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	// Re-insert with revoked_at set; preserve other fields. ip_hash +
	// user_agent aren't recoverable without a second select — leave
	// zero and empty; ReplacingMergeTree FINAL uses the newest by
	// updated_at regardless of other columns.
	var zeroIP [16]byte

	if execErr := s.conn.Exec(ctx,
		fmt.Sprintf(q, s.db),
		string(info.Session.IDHash[:]), info.Session.UserID, info.Session.SiteID,
		string(info.Session.Role),
		time.Unix(info.Session.CreatedAt, 0).UTC(),
		time.Unix(info.Session.LastUsedAt, 0).UTC(),
		time.Unix(info.Session.ExpiresAt, 0).UTC(),
		time.Unix(info.Session.RevokedAt, 0).UTC(),
		now,
		string(zeroIP[:]), "",
	); execErr != nil {
		return fmt.Errorf("revoke session: %w", execErr)
	}

	return nil
}

// RevokeAllUserSessions marks every active session for a user revoked.
// Cascaded from UpdateUserPassword / DisableUser / ChangeRole by the
// CachedStore wrapper. Loops because ClickHouse doesn't do UPDATE on
// ReplacingMergeTree — each revoke is a new row insert.
func (s *ClickHouseStore) RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	const q = `SELECT session_id_hash FROM %s.sessions FINAL
		WHERE user_id = ? AND revoked_at = toDateTime(0)`

	rows, err := s.conn.Query(ctx, fmt.Sprintf(q, s.db), userID)
	if err != nil {
		return fmt.Errorf("query active sessions: %w", err)
	}

	var hashes [][32]byte

	for rows.Next() {
		var h string
		if scanErr := rows.Scan(&h); scanErr != nil {
			rows.Close()

			return fmt.Errorf("scan session hash: %w", scanErr)
		}

		if len(h) != 32 {
			continue
		}

		var fixed [32]byte

		copy(fixed[:], h)
		hashes = append(hashes, fixed)
	}

	rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		return fmt.Errorf("iterate sessions: %w", iterErr)
	}

	for _, h := range hashes {
		if revErr := s.RevokeSession(ctx, h); revErr != nil {
			return revErr
		}
	}

	return nil
}

// isNoRows detects the clickhouse-go "sql: no rows in result set"
// style error without importing database/sql directly (avoids a new
// dep surface).
func isNoRows(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(err.Error(), "no rows in result set")
}

// CachedStore wraps a Store with an in-memory LRU for LookupSession
// (the hot path called on every authenticated request). Invalidates on
// revoke / role change / disable / password change so a 60-second TTL
// bounds the staleness window.
//
// The underlying cache is the existing internal/cache/lru.go — we don't
// reinvent the wheel.
type CachedStore struct {
	Store

	mu    sync.RWMutex
	items map[[32]byte]cachedSession

	// usersIndex is (user_id → active session hashes); lets
	// RevokeAllUserSessions invalidate by user without iterating the
	// whole map on each call. Updated on CreateSession + invalidated
	// on RevokeSession / RevokeAllUserSessions.
	usersIndex map[uuid.UUID]map[[32]byte]struct{}

	ttl time.Duration
	now func() time.Time
}

type cachedSession struct {
	info      *SessionInfo
	expiresAt time.Time
}

// NewCachedStore wraps an inner Store with a TTL-bounded cache.
// ttl <= 0 uses 60 s (PLAN.md § Session cache). A capacity cap ships
// in v1.1 alongside the LRU migration; today the cache is bounded by
// 14-day session TTL + per-user revoke + lockout pressure.
func NewCachedStore(inner Store, ttl time.Duration) *CachedStore {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}

	return &CachedStore{
		Store:      inner,
		items:      make(map[[32]byte]cachedSession),
		usersIndex: make(map[uuid.UUID]map[[32]byte]struct{}),
		ttl:        ttl,
		now:        time.Now,
	}
}

// LookupSession short-circuits on cache hit. On miss, falls through to
// the inner store, populates the cache, and returns.
func (c *CachedStore) LookupSession(ctx context.Context, hash [32]byte) (*SessionInfo, error) {
	now := c.now()

	c.mu.RLock()
	cached, ok := c.items[hash]
	c.mu.RUnlock()

	if ok && now.Before(cached.expiresAt) {
		return cached.info, nil
	}

	info, err := c.Store.LookupSession(ctx, hash)
	if err != nil {
		// On revoked/expired/disabled/not-found, flush any stale entry.
		c.mu.Lock()
		delete(c.items, hash)
		c.mu.Unlock()

		return nil, err
	}

	if info == nil || info.User == nil || info.Session == nil {
		return nil, ErrNotFound
	}

	c.mu.Lock()

	c.items[hash] = cachedSession{info: info, expiresAt: now.Add(c.ttl)}
	if _, exists := c.usersIndex[info.User.UserID]; !exists {
		c.usersIndex[info.User.UserID] = make(map[[32]byte]struct{})
	}

	c.usersIndex[info.User.UserID][hash] = struct{}{}
	c.mu.Unlock()

	return info, nil
}

// CreateSession writes through to the inner store AND primes the cache
// so the immediately-next authenticated request is a cache hit.
func (c *CachedStore) CreateSession(
	ctx context.Context, s *Session, ipHash [16]byte, userAgent string,
) error {
	if err := c.Store.CreateSession(ctx, s, ipHash, userAgent); err != nil {
		return err
	}

	u, err := c.Store.GetUserByID(ctx, s.UserID)
	if err != nil {
		return err
	}

	if u == nil {
		return ErrNotFound
	}

	info := &SessionInfo{User: u, Session: s}

	c.mu.Lock()

	c.items[s.IDHash] = cachedSession{info: info, expiresAt: c.now().Add(c.ttl)}
	if _, exists := c.usersIndex[u.UserID]; !exists {
		c.usersIndex[u.UserID] = make(map[[32]byte]struct{})
	}

	c.usersIndex[u.UserID][s.IDHash] = struct{}{}
	c.mu.Unlock()

	return nil
}

// RevokeSession flushes cache by hash, then calls through.
func (c *CachedStore) RevokeSession(ctx context.Context, hash [32]byte) error {
	c.mu.Lock()

	if entry, ok := c.items[hash]; ok && entry.info != nil && entry.info.User != nil {
		if set := c.usersIndex[entry.info.User.UserID]; set != nil {
			delete(set, hash)
		}
	}

	delete(c.items, hash)
	c.mu.Unlock()

	return c.Store.RevokeSession(ctx, hash)
}

// RevokeAllUserSessions flushes cache by user, then calls through.
func (c *CachedStore) RevokeAllUserSessions(ctx context.Context, userID uuid.UUID) error {
	c.mu.Lock()

	for hash := range c.usersIndex[userID] {
		delete(c.items, hash)
	}

	delete(c.usersIndex, userID)
	c.mu.Unlock()

	return c.Store.RevokeAllUserSessions(ctx, userID)
}

// cascadeRevoke runs a privilege-changing mutation and, on success,
// revokes every session for that user. Hoisted out of
// UpdateUserPassword / DisableUser / ChangeRole so the CVE-2024-10924
// cascade contract lives in exactly one place.
func (c *CachedStore) cascadeRevoke(
	ctx context.Context, userID uuid.UUID, mutate func() error,
) error {
	if err := mutate(); err != nil {
		return err
	}

	return c.RevokeAllUserSessions(ctx, userID)
}

func (c *CachedStore) UpdateUserPassword(
	ctx context.Context, userID uuid.UUID, newHash string,
) error {
	return c.cascadeRevoke(ctx, userID, func() error {
		return c.Store.UpdateUserPassword(ctx, userID, newHash)
	})
}

func (c *CachedStore) DisableUser(ctx context.Context, userID uuid.UUID) error {
	return c.cascadeRevoke(ctx, userID, func() error {
		return c.Store.DisableUser(ctx, userID)
	})
}

func (c *CachedStore) ChangeRole(
	ctx context.Context, userID uuid.UUID, newRole Role,
) error {
	return c.cascadeRevoke(ctx, userID, func() error {
		return c.Store.ChangeRole(ctx, userID, newRole)
	})
}

// InvalidateAll drops every cached entry. Test-only hook; production
// code invalidates by hash or user_id.
func (c *CachedStore) InvalidateAll() {
	c.mu.Lock()

	c.items = make(map[[32]byte]cachedSession)
	c.usersIndex = make(map[uuid.UUID]map[[32]byte]struct{})
	c.mu.Unlock()
}
