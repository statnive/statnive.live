//go:build integration

// Gate 2 E1/H1/J1 remediation: prove the OAuth grant tables (migration 023) are
// reachable by a user-scoped DSAR erasure (GDPR Art. 17). The eraser
// (privacy.EraseOAuthGrantsByUserID) is build-agnostic — this test runs WITHOUT
// the chatgpt_app tag, inserting rows by raw CH and confirming a user's
// auth-codes + refresh-tokens are deleted while another user's survive.
package integration_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/privacy"
	"github.com/statnive/statnive.live/internal/storage"
)

func TestDSAR_EraseOAuthGrantsByUserID(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs: []string{clickhouseAddr()}, Database: testDatabase, Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	defer func() { _ = store.Close() }()

	if mErr := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: testDatabase}, logger).Run(ctx); mErr != nil {
		t.Fatalf("migrate: %v", mErr)
	}

	conn := store.Conn()
	victim, bystander := uuid.New(), uuid.New()

	hash := func(seed string) string { // 64-char FixedString
		return (seed + strings.Repeat("0", 64))[:64]
	}

	insAuthCode := func(u uuid.UUID, seed string) {
		t.Helper()

		if err := conn.Exec(ctx,
			"INSERT INTO "+testDatabase+".oauth_auth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, scope, audience, site_ids, expires_at, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			hash(seed), "c1", u, "https://x/cb", "chal", "analytics:read", "aud", []uint32{1}, time.Now().Add(time.Minute), uint64(time.Now().UnixNano()),
		); err != nil {
			t.Fatalf("insert auth_code: %v", err)
		}
	}

	insRefresh := func(u uuid.UUID, seed string) {
		t.Helper()

		if err := conn.Exec(ctx,
			"INSERT INTO "+testDatabase+".oauth_refresh_tokens (token_hash, family_id, client_id, user_id, scope, audience, site_ids, expires_at, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			hash(seed), uuid.New(), "c1", u, "analytics:read", "aud", []uint32{1}, time.Now().Add(time.Hour), uint64(time.Now().UnixNano()),
		); err != nil {
			t.Fatalf("insert refresh: %v", err)
		}
	}

	insAuthCode(victim, "victimcode")
	insRefresh(victim, "victimref")
	insAuthCode(bystander, "bycode")
	insRefresh(bystander, "byref")

	count := func(table string, u uuid.UUID) uint64 {
		t.Helper()

		var n uint64
		if err := conn.QueryRow(ctx,
			fmt.Sprintf("SELECT count() FROM %s.%s FINAL WHERE user_id = ?", testDatabase, table), u).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}

		return n
	}

	if count("oauth_auth_codes", victim) == 0 || count("oauth_refresh_tokens", victim) == 0 {
		t.Fatal("seed precondition failed: victim rows not present")
	}

	results, err := privacy.NewEraseEnumerator(conn, testDatabase).EraseOAuthGrantsByUserID(ctx, victim)
	if err != nil {
		t.Fatalf("EraseOAuthGrantsByUserID: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 table results, got %d: %+v", len(results), results)
	}

	for _, r := range results {
		if !r.MutationSent || r.Err != "" {
			t.Errorf("erase on %s not dispatched: %+v", r.Table, r)
		}
	}

	// ALTER ... DELETE is async — poll until the victim's rows are gone.
	deadline := time.Now().Add(20 * time.Second)
	for {
		if count("oauth_auth_codes", victim) == 0 && count("oauth_refresh_tokens", victim) == 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("victim rows not erased within deadline (codes=%d refresh=%d)",
				count("oauth_auth_codes", victim), count("oauth_refresh_tokens", victim))
		}

		time.Sleep(250 * time.Millisecond)
	}

	// The bystander's grants must be untouched (user-scoped, not global).
	if count("oauth_auth_codes", bystander) != 1 || count("oauth_refresh_tokens", bystander) != 1 {
		t.Errorf("bystander rows were erased (cross-user erase): codes=%d refresh=%d",
			count("oauth_auth_codes", bystander), count("oauth_refresh_tokens", bystander))
	}

	// Nil user_id is rejected (no accidental mass-delete).
	if _, err := privacy.NewEraseEnumerator(conn, testDatabase).EraseOAuthGrantsByUserID(ctx, uuid.Nil); err == nil {
		t.Error("nil user_id accepted; want rejection")
	}
}
