//go:build integration

// CH-backed integration tests for the self-serve MCP token store (PR-A).
// Proves CRUD + revoke (version-row supersession) + expiry + cross-user
// isolation + DSAR erasure against real ClickHouse. Run: make test-integration
package integration_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/privacy"
	"github.com/statnive/statnive.live/internal/storage"
)

func migratedTokenStore(t *testing.T) (*auth.ClickHouseAPITokenStore, *storage.ClickHouseStore) {
	t.Helper()

	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(testWriter{t}, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{clickhouseAddr()},
		Database: testDatabase,
		Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("open CH: %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	mig := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: testDatabase}, logger)
	if err := mig.Run(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return auth.NewClickHouseAPITokenStore(store.Conn(), testDatabase), store
}

// testWriter adapts *testing.T to io.Writer for the slog handler.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) { w.t.Logf("%s", p); return len(p), nil }

func TestMCPTokenStore_CRUDRevokeExpiry(t *testing.T) {
	ts, _ := migratedTokenStore(t)
	ctx := context.Background()
	user := uuid.New() // unique per run ⇒ no cross-run accumulation

	// Create.
	raw, meta, err := ts.Create(ctx, user, "laptop", []uint32{1, 4}, auth.RoleAPI, 24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if raw == "" || meta.TokenID == uuid.Nil {
		t.Fatal("Create returned empty raw/id")
	}

	// LookupActive by hash → scoped token.
	got, ok, err := ts.LookupActive(ctx, auth.HashTokenHex(raw))
	if err != nil || !ok {
		t.Fatalf("LookupActive miss: ok=%v err=%v", ok, err)
	}

	if got.UserID != user || len(got.SiteIDs) != 2 || got.Role != auth.RoleAPI {
		t.Errorf("scope wrong: %+v", got)
	}

	// Count + List.
	if n, _ := ts.CountActiveForUser(ctx, user); n != 1 {
		t.Errorf("count = %d, want 1", n)
	}

	if list, _ := ts.ListForUser(ctx, user); len(list) != 1 {
		t.Errorf("list = %d, want 1", len(list))
	}

	// Cross-user revoke is ErrNotFound (ownership scope).
	if err := ts.Revoke(ctx, meta.TokenID, uuid.New()); err != auth.ErrNotFound {
		t.Errorf("cross-user revoke = %v, want ErrNotFound", err)
	}

	// Owner revoke → token vanishes from LookupActive + List (higher-version
	// tombstone supersedes).
	if err := ts.Revoke(ctx, meta.TokenID, user); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if _, ok, _ := ts.LookupActive(ctx, auth.HashTokenHex(raw)); ok {
		t.Error("revoked token still active")
	}

	if n, _ := ts.CountActiveForUser(ctx, user); n != 0 {
		t.Errorf("count after revoke = %d, want 0", n)
	}
}

func TestMCPTokenStore_Expiry(t *testing.T) {
	ts, _ := migratedTokenStore(t)
	ctx := context.Background()
	user := uuid.New()

	raw, _, err := ts.Create(ctx, user, "shortlived", []uint32{1}, auth.RoleAPI, 1*time.Second)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	time.Sleep(1500 * time.Millisecond)

	if _, ok, _ := ts.LookupActive(ctx, auth.HashTokenHex(raw)); ok {
		t.Error("expired token still active")
	}
}

func TestMCPTokenStore_DSAREraseByUserID(t *testing.T) {
	ts, store := migratedTokenStore(t)
	ctx := context.Background()
	user := uuid.New()

	raw, _, err := ts.Create(ctx, user, "to-erase", []uint32{1}, auth.RoleAPI, 24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	eraser := privacy.NewEraseEnumerator(store.Conn(), testDatabase)
	if err := eraser.EraseTokensByUserID(ctx, user); err != nil {
		t.Fatalf("EraseTokensByUserID: %v", err)
	}

	// The ALTER ... DELETE mutation is async; poll briefly until the row is gone.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok, _ := ts.LookupActive(ctx, auth.HashTokenHex(raw)); !ok {
			return // erased
		}

		time.Sleep(250 * time.Millisecond)
	}

	t.Error("token survived DSAR erase after 10s")
}
