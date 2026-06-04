package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// APITokenPrefix is the identifiable prefix every minted token carries
// (GitHub/Stripe pattern). It makes the token greppable by secret scanners
// and recognizable in support tickets. The prefix is part of the raw
// string that gets SHA-256-hashed, so it is covered by the hash at rest.
const APITokenPrefix = "stnv_"

// tokenRandBytes is the entropy of a minted token (256-bit). Brute-forcing a
// 2^256 space is infeasible, which is why a fast SHA-256 (not a slow KDF) is
// the correct hash at rest — entropy is the barrier, and bcrypt on every MCP
// request would be a per-request DoS.
const tokenRandBytes = 32

// MintedToken is the metadata view of one self-serve MCP token. The raw
// secret is NEVER a field here — it exists only in the Create return value,
// shown once to the user. ExpiresAt == 0 means "never expires".
type MintedToken struct {
	TokenID    uuid.UUID
	UserID     uuid.UUID
	Name       string
	SiteIDs    []uint32
	Role       Role
	CreatedAt  int64 // unix seconds, UTC
	ExpiresAt  int64 // unix seconds, UTC; 0 == never
	LastUsedAt int64 // unix seconds, UTC; 0 == never used
}

// APITokenStore is the lifecycle contract for dashboard-minted MCP tokens.
// The hot path is LookupActive (one call per token-authed MCP request,
// fronted by CachedAPITokenStore). Nil-safety invariant (PLAN.md §53):
// methods returning (_, bool, error) return (zero, false, err) on failure.
type APITokenStore interface {
	// Create mints a token for userID scoped to siteIDs at role, returning
	// the raw secret (shown once) and its metadata. ttl <= 0 ⇒ never expires.
	Create(ctx context.Context, userID uuid.UUID, name string, siteIDs []uint32, role Role, ttl time.Duration) (raw string, meta MintedToken, err error)
	// ListForUser returns the user's active (non-revoked, non-expired) tokens.
	ListForUser(ctx context.Context, userID uuid.UUID) ([]MintedToken, error)
	// Revoke marks tokenID revoked. Returns ErrNotFound if the token does not
	// exist or is not owned by userID (ownership-scoped — no cross-user revoke).
	Revoke(ctx context.Context, tokenID, userID uuid.UUID) error
	// LookupActive resolves a SHA-256 hex hash to its active token, or
	// (zero, false, nil) when unknown/revoked/expired.
	LookupActive(ctx context.Context, hashHex string) (MintedToken, bool, error)
	// CountActiveForUser backs the max-active-tokens-per-user cap.
	CountActiveForUser(ctx context.Context, userID uuid.UUID) (int, error)
}

// HashTokenHex returns the lowercase SHA-256 hex of a raw token — the value
// persisted at rest and compared on the hot path. Shared by the store and
// APITokenMiddleware so both sides hash identically.
func HashTokenHex(raw string) string {
	sum := sha256.Sum256([]byte(raw))

	return hex.EncodeToString(sum[:])
}

// generateRawToken returns a 256-bit CSPRNG token with the stnv_ prefix.
func generateRawToken() (string, error) {
	b := make([]byte, tokenRandBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("token entropy: %w", err)
	}

	return APITokenPrefix + hex.EncodeToString(b), nil
}

// ClickHouseAPITokenStore implements APITokenStore against the shared CH
// pool. Mirrors auth.ClickHouseStore (conn + db + now). Table mcp_tokens is
// a ReplacingMergeTree(version); the highest version for a token_id wins, so
// revoke is an upsert (same row, revoked=1, higher version).
type ClickHouseAPITokenStore struct {
	conn driver.Conn
	db   string
	now  func() time.Time
}

// NewClickHouseAPITokenStore wraps the shared connection.
func NewClickHouseAPITokenStore(conn driver.Conn, database string) *ClickHouseAPITokenStore {
	if database == "" {
		database = "statnive"
	}

	return &ClickHouseAPITokenStore{conn: conn, db: database, now: time.Now}
}

// version returns a monotonic-by-wall-clock version stamp. ReplacingMergeTree
// keeps the highest, so a later write (revoke) always supersedes.
func (s *ClickHouseAPITokenStore) version() uint64 {
	// UnixNano is always positive for any realistic wall clock; the cast is safe.
	return uint64(s.now().UTC().UnixNano()) //nolint:gosec // monotonic positive nanos
}

// Create mints + persists a token. See APITokenStore.Create.
func (s *ClickHouseAPITokenStore) Create(
	ctx context.Context, userID uuid.UUID, name string, siteIDs []uint32, role Role, ttl time.Duration,
) (string, MintedToken, error) {
	if userID == uuid.Nil {
		return "", MintedToken{}, fmt.Errorf("%w: nil user", ErrInvalidInput)
	}

	if !role.Valid() {
		return "", MintedToken{}, fmt.Errorf("%w: invalid role %q", ErrInvalidInput, role)
	}

	if len(siteIDs) == 0 {
		return "", MintedToken{}, fmt.Errorf("%w: empty site scope", ErrInvalidInput)
	}

	raw, err := generateRawToken()
	if err != nil {
		return "", MintedToken{}, err
	}

	now := s.now().UTC()
	meta := MintedToken{
		TokenID:   uuid.New(),
		UserID:    userID,
		Name:      name,
		SiteIDs:   siteIDs,
		Role:      role,
		CreatedAt: now.Unix(),
	}

	var expiresAt time.Time // zero value ⇒ DateTime(0) ⇒ "never"

	if ttl > 0 {
		expiresAt = now.Add(ttl)
		meta.ExpiresAt = expiresAt.Unix()
	}

	const q = `INSERT INTO %s.mcp_tokens (
		token_id, user_id, token_hash_hex, name, site_ids,
		role, created_at, expires_at, last_used_at, revoked, version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if err := s.conn.Exec(ctx, fmt.Sprintf(q, s.db),
		meta.TokenID, userID, HashTokenHex(raw), name, siteIDs,
		string(role), now, expiresAt, time.Unix(0, 0).UTC(), uint8(0), s.version(),
	); err != nil {
		return "", MintedToken{}, fmt.Errorf("insert mcp_tokens: %w", err)
	}

	return raw, meta, nil
}

// activePredicate is the shared "not revoked and not expired" SQL fragment.
// expires_at == DateTime(0) is the never-expires sentinel.
const activePredicate = `revoked = 0 AND (toUnixTimestamp(expires_at) = 0 OR expires_at > now())`

// LookupActive resolves a hash to its active token. See APITokenStore.
func (s *ClickHouseAPITokenStore) LookupActive(
	ctx context.Context, hashHex string,
) (MintedToken, bool, error) {
	// Latest version for this hash: token_hash_hex leads the primary key, so
	// WHERE token_hash_hex=? is a bounded range seek (1-2 rows: the create row
	// + an optional revoke tombstone), then ORDER BY version DESC LIMIT 1 picks
	// the newest — no table-wide FINAL needed. NOTE: `version` is deliberately
	// NOT in the table's ORDER BY (dedup) key — putting it there would make
	// every version a distinct ReplacingMergeTree row that never collapses,
	// growing the table unbounded. The dedup key is (token_hash_hex, token_id);
	// versions of the same token collapse to the highest.
	const q = `SELECT token_id, user_id, name, site_ids, role,
		toInt64(toUnixTimestamp(created_at)), toInt64(toUnixTimestamp(expires_at)), toInt64(toUnixTimestamp(last_used_at)), revoked
		FROM %s.mcp_tokens
		WHERE token_hash_hex = ?
		ORDER BY version DESC
		LIMIT 1`

	var (
		m       MintedToken
		roleStr string
		revoked uint8
	)

	row := s.conn.QueryRow(ctx, fmt.Sprintf(q, s.db), hashHex)
	if err := row.Scan(&m.TokenID, &m.UserID, &m.Name, &m.SiteIDs, &roleStr,
		&m.CreatedAt, &m.ExpiresAt, &m.LastUsedAt, &revoked); err != nil {
		if isNoRows(err) {
			return MintedToken{}, false, nil
		}

		return MintedToken{}, false, fmt.Errorf("lookup mcp_token: %w", err)
	}

	m.Role = Role(roleStr)

	if revoked == 1 {
		return MintedToken{}, false, nil
	}

	if m.ExpiresAt != 0 && m.ExpiresAt <= s.now().UTC().Unix() {
		return MintedToken{}, false, nil
	}

	return m, true, nil
}

// ListForUser returns the user's active tokens. See APITokenStore.
func (s *ClickHouseAPITokenStore) ListForUser(
	ctx context.Context, userID uuid.UUID,
) ([]MintedToken, error) {
	const q = `SELECT token_id, user_id, name, site_ids, role,
		toInt64(toUnixTimestamp(created_at)), toInt64(toUnixTimestamp(expires_at)), toInt64(toUnixTimestamp(last_used_at))
		FROM %s.mcp_tokens FINAL
		WHERE user_id = ? AND ` + activePredicate + `
		ORDER BY created_at DESC`

	rows, err := s.conn.Query(ctx, fmt.Sprintf(q, s.db), userID)
	if err != nil {
		return nil, fmt.Errorf("list mcp_tokens: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := make([]MintedToken, 0)

	for rows.Next() {
		var (
			m       MintedToken
			roleStr string
		)

		if err := rows.Scan(&m.TokenID, &m.UserID, &m.Name, &m.SiteIDs, &roleStr,
			&m.CreatedAt, &m.ExpiresAt, &m.LastUsedAt); err != nil {
			return nil, fmt.Errorf("scan mcp_token: %w", err)
		}

		m.Role = Role(roleStr)
		out = append(out, m)
	}

	return out, rows.Err()
}

// CountActiveForUser backs the per-user cap. See APITokenStore.
func (s *ClickHouseAPITokenStore) CountActiveForUser(
	ctx context.Context, userID uuid.UUID,
) (int, error) {
	const q = `SELECT count() FROM %s.mcp_tokens FINAL
		WHERE user_id = ? AND ` + activePredicate

	var n uint64

	row := s.conn.QueryRow(ctx, fmt.Sprintf(q, s.db), userID)
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("count mcp_tokens: %w", err)
	}

	return int(n), nil //nolint:gosec // count bounded by the per-user cap
}

// Revoke marks a token revoked (ownership-scoped). See APITokenStore.
func (s *ClickHouseAPITokenStore) Revoke(
	ctx context.Context, tokenID, userID uuid.UUID,
) error {
	// Read the current (highest-version) row so the revoke tombstone carries
	// the same token_hash_hex — otherwise LookupActive (keyed by hash) would
	// never observe the revocation.
	const sel = `SELECT user_id, token_hash_hex, name, site_ids, role,
		created_at, expires_at, last_used_at, revoked
		FROM %s.mcp_tokens
		WHERE token_id = ?
		ORDER BY version DESC
		LIMIT 1`

	var (
		owner     uuid.UUID
		hashHex   string
		name      string
		siteIDs   []uint32
		roleStr   string
		createdAt time.Time
		expiresAt time.Time
		lastUsed  time.Time
		revoked   uint8
	)

	row := s.conn.QueryRow(ctx, fmt.Sprintf(sel, s.db), tokenID)
	if err := row.Scan(&owner, &hashHex, &name, &siteIDs, &roleStr,
		&createdAt, &expiresAt, &lastUsed, &revoked); err != nil {
		if isNoRows(err) {
			return ErrNotFound
		}

		return fmt.Errorf("read mcp_token for revoke: %w", err)
	}

	// Ownership scope: a user can only revoke their own tokens. Report
	// ErrNotFound (not Forbidden) so the endpoint does not leak existence.
	if owner != userID {
		return ErrNotFound
	}

	if revoked == 1 {
		return nil // already revoked — idempotent
	}

	const ins = `INSERT INTO %s.mcp_tokens (
		token_id, user_id, token_hash_hex, name, site_ids,
		role, created_at, expires_at, last_used_at, revoked, version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if err := s.conn.Exec(ctx, fmt.Sprintf(ins, s.db),
		tokenID, owner, hashHex, name, siteIDs,
		roleStr, createdAt, expiresAt, lastUsed, uint8(1), s.version(),
	); err != nil {
		return fmt.Errorf("revoke mcp_token: %w", err)
	}

	return nil
}

// Grants returns the MintedToken's site_ids as the auth grant map the
// middleware attaches to the synthetic *User (so ActorCanReadSite takes the
// grant-map branch, scoped to exactly these sites — never wildcard).
func (m MintedToken) Grants() map[uint32]Role {
	g := make(map[uint32]Role, len(m.SiteIDs))
	for _, id := range m.SiteIDs {
		g[id] = m.Role
	}

	return g
}
