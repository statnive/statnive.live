//go:build chatgpt_app

// Package oauthas is statnive's hand-rolled, stdlib-only OAuth 2.1
// authorization server — the AS half of the ChatGPT-app onboarding path. It is
// compiled ONLY with `-tags chatgpt_app`, so the default + air-gap/inside-iran
// binaries link zero OAuth-AS code and gain zero new dependencies.
//
// Why hand-rolled rather than a library (e.g. ory/fosite): the empirical vet of
// fosite's real linked tree dragged MPL-2.0 deps (hashicorp/go-retryablehttp)
// plus gRPC + the full OpenTelemetry exporter stack (~250 packages) into the
// chatgpt_app binary — breaking both the {MIT,Apache,BSD,ISC}-only license rule
// and the lean-air-gap-binary invariant. The AS surface we actually need is
// narrow (one client, one identity source = the existing dashboard session, one
// grant = auth-code + PKCE S256, one scope, one resource), and every dangerous
// primitive already exists battle-tested in-tree (RS256 sign/verify in
// cmd/statnive-live/mcp_oauth.go; subtle.ConstantTimeCompare in
// internal/auth/password.go; crypto/rand + SHA-256 in internal/auth/session.go;
// PKCE S256 = sha256 + base64url). The error-prone parts are pure logic, pinned
// by the deterministic adversarial test matrix + the J red-team.
package oauthas

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
)

// Sentinel errors. Handlers map these to OAuth error codes (invalid_grant /
// invalid_client) without string-matching the wrapped cause.
var (
	ErrClientNotFound = errors.New("oauthas: client not found")
	ErrCodeNotFound   = errors.New("oauthas: authorization code not found")
	ErrCodeConsumed   = errors.New("oauthas: authorization code already used")
	ErrCodeExpired    = errors.New("oauthas: authorization code expired")
	ErrRefreshInvalid = errors.New("oauthas: refresh token not found")
	ErrRefreshExpired = errors.New("oauthas: refresh token expired")
	// ErrRefreshReused fires when a refresh token that was already rotated (or
	// belongs to a revoked family) is presented again — the canonical
	// stolen-token signal (RFC 9700 §4.14.2). The handler revokes the whole
	// family and emits a security audit event.
	ErrRefreshReused = errors.New("oauthas: refresh token reuse detected")
)

// grant is the consented authorization carried by a code and every refresh
// token derived from it: who (UserID), for which client, scope, RFC 8707
// audience, and the scope-clamped consented site_ids. SiteIDs is the per-token
// claim that makes consent enforceable at the resource server (M1).
type grant struct {
	ClientID string
	UserID   uuid.UUID
	Scope    string
	Audience string
	SiteIDs  []uint32
}

// Client is a registered OAuth client (the ChatGPT connector). SecretHash is
// SHA-256(hex) of the client secret; "" marks a public client. RedirectURIs is
// the exact-match allow-list (no wildcards, ever).
type Client struct {
	ID           string
	SecretHash   string
	Name         string
	RedirectURIs []string
	Scopes       []string
	Revoked      bool
}

// AuthCode is a single-use authorization code bound to the client, the exact
// redirect_uri used at /authorize, and the PKCE S256 challenge.
type AuthCode struct {
	grant
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

// RefreshToken is a rotating, revocable refresh token. FamilyID groups every
// token descended from one auth-code grant so reuse-detection can revoke the
// whole lineage at once.
type RefreshToken struct {
	grant
	FamilyID  uuid.UUID
	ExpiresAt time.Time
}

// Store persists AS state in ClickHouse (migration 023) and enforces the two
// security-critical read-modify-write operations — single-use code consumption
// and refresh-token rotation — atomically. The binary is single-node
// (CLAUDE.md: "ClickHouse cluster at v1 — single-node is the rule"), so a
// process mutex is a correct serialization point; CH has no row locks. The
// table rows are the durable + audit record.
type Store struct {
	conn driver.Conn
	db   string

	// exchangeMu serializes ConsumeAuthCode + RotateRefreshToken so the
	// check-then-write (consumed/rotated flags) cannot race into a double-spend
	// (code replay / refresh reuse slipping through). One ChatGPT client ⇒
	// negligible contention.
	exchangeMu sync.Mutex

	// version is a process-monotonic ReplacingMergeTree version source. Seeded
	// from UnixNano so a same-second upsert (consume/rotate/revoke) always
	// outranks the row it supersedes, and a fresh process always outranks
	// rows written by a prior one.
	version atomic.Uint64
}

// NewStore wraps an existing CH connection (shared with the rest of the
// daemon).
func NewStore(conn driver.Conn, database string) *Store {
	if database == "" {
		database = "statnive"
	}

	s := &Store{conn: conn, db: database}
	//nolint:gosec // UnixNano is always positive; this is a monotonic version seed, not a security value.
	s.version.Store(uint64(time.Now().UnixNano()))

	return s
}

// HashToken returns the SHA-256 hex of a raw credential (client secret,
// authorization code, refresh token). FixedString(64) at rest; the raw value
// never touches the database. Exported so handlers hash before they store.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))

	return hex.EncodeToString(sum[:])
}

func (s *Store) nextVersion() uint64 { return s.version.Add(1) }

func (s *Store) q(query string) string { return fmt.Sprintf(query, s.db) }

// --- clients ---------------------------------------------------------------

// CreateClient upserts a registered client (DCR). Idempotent on client_id via
// ReplacingMergeTree(version).
func (s *Store) CreateClient(ctx context.Context, c Client) error {
	const q = `INSERT INTO %s.oauth_clients
		(client_id, client_secret_hash, client_name, redirect_uris, scopes, revoked, version)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	revoked := uint8(0)
	if c.Revoked {
		revoked = 1
	}

	if err := s.conn.Exec(ctx, s.q(q),
		c.ID, c.SecretHash, c.Name, c.RedirectURIs, c.Scopes, revoked, s.nextVersion(),
	); err != nil {
		return fmt.Errorf("insert oauth_client: %w", err)
	}

	return nil
}

// GetClient returns the active (non-revoked) client by id, or ErrClientNotFound.
func (s *Store) GetClient(ctx context.Context, id string) (Client, error) {
	const q = `SELECT client_id, client_secret_hash, client_name, redirect_uris, scopes
		FROM %s.oauth_clients FINAL
		WHERE client_id = ? AND revoked = 0
		LIMIT 1`

	row := s.conn.QueryRow(ctx, s.q(q), id)

	var c Client
	if err := row.Scan(&c.ID, &c.SecretHash, &c.Name, &c.RedirectURIs, &c.Scopes); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Client{}, ErrClientNotFound
		}

		return Client{}, fmt.Errorf("scan oauth_client: %w", err)
	}

	return c, nil
}

// --- authorization codes ---------------------------------------------------

// SaveAuthCode persists a freshly-issued code (raw hashed by the caller).
func (s *Store) SaveAuthCode(ctx context.Context, codeHash string, c AuthCode) error {
	const q = `INSERT INTO %s.oauth_auth_codes
		(code_hash, client_id, user_id, redirect_uri, code_challenge, scope, audience, site_ids, expires_at, consumed, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`

	if err := s.conn.Exec(ctx, s.q(q),
		codeHash, c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge,
		c.Scope, c.Audience, c.SiteIDs, c.ExpiresAt, s.nextVersion(),
	); err != nil {
		return fmt.Errorf("insert oauth_auth_code: %w", err)
	}

	return nil
}

// ConsumeAuthCode atomically validates and burns a code: it returns the bound
// AuthCode exactly once. A second call (replay), an expired code, or an unknown
// code returns the matching sentinel and never yields the grant. The mutex makes
// the check-then-burn atomic on this single-node binary.
func (s *Store) ConsumeAuthCode(ctx context.Context, codeHash string, now time.Time) (AuthCode, error) {
	s.exchangeMu.Lock()
	defer s.exchangeMu.Unlock()

	const sel = `SELECT client_id, user_id, redirect_uri, code_challenge, scope, audience, site_ids, expires_at, consumed
		FROM %s.oauth_auth_codes FINAL
		WHERE code_hash = ?
		LIMIT 1`

	row := s.conn.QueryRow(ctx, s.q(sel), codeHash)

	var (
		c        AuthCode
		consumed uint8
	)

	if err := row.Scan(&c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge,
		&c.Scope, &c.Audience, &c.SiteIDs, &c.ExpiresAt, &consumed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AuthCode{}, ErrCodeNotFound
		}

		return AuthCode{}, fmt.Errorf("scan oauth_auth_code: %w", err)
	}

	if consumed == 1 {
		return AuthCode{}, ErrCodeConsumed
	}

	if !now.Before(c.ExpiresAt) {
		return AuthCode{}, ErrCodeExpired
	}

	// Burn: upsert a higher-version row with consumed=1. The next call reads it
	// via FINAL and returns ErrCodeConsumed.
	const burn = `INSERT INTO %s.oauth_auth_codes
		(code_hash, client_id, user_id, redirect_uri, code_challenge, scope, audience, site_ids, expires_at, consumed, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`

	if err := s.conn.Exec(ctx, s.q(burn),
		codeHash, c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge,
		c.Scope, c.Audience, c.SiteIDs, c.ExpiresAt, s.nextVersion(),
	); err != nil {
		return AuthCode{}, fmt.Errorf("burn oauth_auth_code: %w", err)
	}

	return c, nil
}

// --- refresh tokens --------------------------------------------------------

// SaveRefreshToken persists a freshly-issued refresh token (raw hashed by the
// caller).
func (s *Store) SaveRefreshToken(ctx context.Context, tokenHash string, t RefreshToken) error {
	return s.insertRefresh(ctx, tokenHash, t, 0, 0)
}

func (s *Store) insertRefresh(ctx context.Context, tokenHash string, t RefreshToken, rotated, familyRevoked uint8) error {
	const q = `INSERT INTO %s.oauth_refresh_tokens
		(token_hash, family_id, client_id, user_id, scope, audience, site_ids, expires_at, rotated, family_revoked, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if err := s.conn.Exec(ctx, s.q(q),
		tokenHash, t.FamilyID, t.ClientID, t.UserID, t.Scope, t.Audience,
		t.SiteIDs, t.ExpiresAt, rotated, familyRevoked, s.nextVersion(),
	); err != nil {
		return fmt.Errorf("insert oauth_refresh_token: %w", err)
	}

	return nil
}

// RotateRefreshToken atomically rotates oldHash → a successor whose hash is
// newHash, returning the carried-over grant for minting the new access+refresh
// pair. clientID is the authenticated client — a token presented by the wrong
// client, an already-rotated token, or one in a revoked family returns
// ErrRefreshReused AND revokes the entire family (theft signal). Expiry /
// unknown token return their sentinels.
func (s *Store) RotateRefreshToken(ctx context.Context, oldHash, newHash, clientID string, now time.Time) (RefreshToken, error) {
	s.exchangeMu.Lock()
	defer s.exchangeMu.Unlock()

	const sel = `SELECT family_id, client_id, user_id, scope, audience, site_ids, expires_at, rotated, family_revoked
		FROM %s.oauth_refresh_tokens FINAL
		WHERE token_hash = ?
		LIMIT 1`

	row := s.conn.QueryRow(ctx, s.q(sel), oldHash)

	var (
		t                      RefreshToken
		rotated, familyRevoked uint8
	)

	if err := row.Scan(&t.FamilyID, &t.ClientID, &t.UserID, &t.Scope, &t.Audience,
		&t.SiteIDs, &t.ExpiresAt, &rotated, &familyRevoked); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RefreshToken{}, ErrRefreshInvalid
		}

		return RefreshToken{}, fmt.Errorf("scan oauth_refresh_token: %w", err)
	}

	// Reuse / theft: a rotated token, a revoked family, or the wrong client
	// presenting the token → revoke the whole lineage and signal reuse. Checked
	// before expiry so a stolen-then-expired token still trips the alarm.
	if rotated == 1 || familyRevoked == 1 || t.ClientID != clientID {
		if err := s.revokeFamilyLocked(ctx, t.FamilyID); err != nil {
			return RefreshToken{}, err
		}

		return RefreshToken{}, ErrRefreshReused
	}

	if !now.Before(t.ExpiresAt) {
		return RefreshToken{}, ErrRefreshExpired
	}

	// Mark old rotated, write successor in the same family.
	if err := s.insertRefresh(ctx, oldHash, t, 1, 0); err != nil {
		return RefreshToken{}, err
	}

	if err := s.insertRefresh(ctx, newHash, t, 0, 0); err != nil {
		return RefreshToken{}, err
	}

	return t, nil
}

// revokeFamilyLocked sets family_revoked=1 on every current row of the family.
// Caller holds exchangeMu.
func (s *Store) revokeFamilyLocked(ctx context.Context, familyID uuid.UUID) error {
	const sel = `SELECT token_hash, client_id, user_id, scope, audience, site_ids, expires_at, rotated
		FROM %s.oauth_refresh_tokens FINAL
		WHERE family_id = ? AND family_revoked = 0`

	rows, err := s.conn.Query(ctx, s.q(sel), familyID)
	if err != nil {
		return fmt.Errorf("query refresh family: %w", err)
	}

	type member struct {
		hash    string
		t       RefreshToken
		rotated uint8
	}

	members := make([]member, 0, 4)

	for rows.Next() {
		var m member

		m.t.FamilyID = familyID

		if scanErr := rows.Scan(&m.hash, &m.t.ClientID, &m.t.UserID, &m.t.Scope,
			&m.t.Audience, &m.t.SiteIDs, &m.t.ExpiresAt, &m.rotated); scanErr != nil {
			_ = rows.Close()

			return fmt.Errorf("scan refresh family row: %w", scanErr)
		}

		members = append(members, m)
	}

	_ = rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		return fmt.Errorf("iterate refresh family: %w", iterErr)
	}

	for _, m := range members {
		if err := s.insertRefresh(ctx, m.hash, m.t, m.rotated, 1); err != nil {
			return err
		}
	}

	return nil
}
