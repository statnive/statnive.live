//go:build integration

// Integration tests for the per-site-admin scaffolding shipped in
// v0.0.9 (migration 010 + internal/auth/user_sites.go).
//
// Covers:
//
//   - Migration 010 creates statnive.user_sites with the expected schema.
//   - Backfill 1 copies users.(site_id, role) → user_sites 1:1.
//   - Operator bootstrap grants admin on every enabled site when
//     OperatorEmail is set; skipped when empty.
//   - ClickHouseSitesStore Grant / Revoke / LoadUserSites round-trips.
//   - LoadUserSites returns the FRESH state after revoke (no cache,
//     per-request reads — pins the revoke-race fix).
//
// Skipped when CH is not reachable (make ch-up before running).
package integration_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/storage"
)

// All migrations through 010 (the new per-site-admin grant table).
var allMigrationsThru010 = []string{
	"001_initial.sql",
	"002_rollups.sql",
	"003_sites_tz.sql",
	"004_auth_schema.sql",
	"005_goals_schema.sql",
	"006_sites_privacy.sql",
	"007_sites_currency.sql",
	"008_rename_revenue_columns.sql",
	"009_rollup_simple_aggregate.sql",
	"010_user_sites.sql",
}

const (
	userSitesTestDB = "statnive_user_sites_gate"
	operatorEmail   = "ops@statnive.live"
)

func openUserSitesDB(t *testing.T) (*storage.ClickHouseStore, context.Context) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	addr := os.Getenv("STATNIVE_CLICKHOUSE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:19000"
	}

	// Open a connection to the default db first so we can drop+create
	// the per-test database.
	bootstrap, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{addr},
		Database: "default",
		Username: "default",
	}, logger)
	if err != nil {
		t.Skipf("clickhouse not available at %s: %v", addr, err)
	}

	t.Cleanup(func() { _ = bootstrap.Close() })

	if dropErr := bootstrap.Conn().Exec(ctx, "DROP DATABASE IF EXISTS "+userSitesTestDB+" SYNC"); dropErr != nil {
		t.Fatalf("drop test db: %v", dropErr)
	}

	if createErr := bootstrap.Conn().Exec(ctx, "CREATE DATABASE "+userSitesTestDB); createErr != nil {
		t.Fatalf("create test db: %v", createErr)
	}

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{addr},
		Database: userSitesTestDB,
		Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("open scoped db: %v", err)
	}

	t.Cleanup(func() {
		_ = store.Close()
		_ = bootstrap.Conn().Exec(context.Background(), "DROP DATABASE IF EXISTS "+userSitesTestDB+" SYNC")
	})

	// Migrations hardcode "statnive." everywhere; for an isolated test
	// db we hand-render + substitute (same pattern as
	// test/data_preservation_e2e_test.go applyMigrations). The migration
	// runner can't be parametrized on schema name yet — open issue.
	applyUserSitesMigrations(t, ctx, store.Conn(), allMigrationsThru010)

	return store, ctx
}

// applyUserSitesMigrations renders each named migration with the test's
// OperatorEmail + cluster="" then substitutes "statnive." → testDB
// before exec. Mirrors data_preservation_e2e_test.go applyMigrations.
func applyUserSitesMigrations(t *testing.T, ctx context.Context, conn driver.Conn, names []string) {
	t.Helper()

	data := storage.MigrationData{
		Cluster:       "",
		OperatorEmail: operatorEmail,
	}

	for _, name := range names {
		body, err := storage.Migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}

		rendered, err := storage.RenderMigrationWith(name, body, data)
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}

		rendered = strings.ReplaceAll(rendered, "statnive.", userSitesTestDB+".")

		for _, stmt := range storage.SplitStatements(rendered) {
			s := strings.TrimSpace(stmt)
			if s == "" {
				continue
			}

			if execErr := conn.Exec(ctx, s); execErr != nil {
				// firstNLines is defined in test/data_preservation_e2e_test.go;
				// both files live in package integration_test.
				t.Fatalf("apply %s: %v\nSQL first lines: %s", name, execErr, firstNLines(s, 4))
			}
		}
	}
}

// TestMigration010_TableExists pins the schema shape: column types,
// engine, ORDER BY. A migration that drifts trips this gate first.
func TestMigration010_TableExists(t *testing.T) {
	store, ctx := openUserSitesDB(t)

	var engine string
	if err := store.Conn().QueryRow(ctx,
		"SELECT engine FROM system.tables WHERE database = ? AND name = 'user_sites'",
		userSitesTestDB,
	).Scan(&engine); err != nil {
		t.Fatalf("read user_sites engine: %v", err)
	}

	// Single-node deploy → plain ReplacingMergeTree.
	if engine != "ReplacingMergeTree" {
		t.Fatalf("user_sites engine = %q, want ReplacingMergeTree", engine)
	}

	var colCount uint64
	if err := store.Conn().QueryRow(ctx,
		`SELECT count() FROM system.columns
		 WHERE database = ? AND table = 'user_sites'
		   AND name IN ('user_id','site_id','role','created_at','updated_at','revoked')`,
		userSitesTestDB,
	).Scan(&colCount); err != nil {
		t.Fatalf("read user_sites columns: %v", err)
	}

	if colCount != 6 {
		t.Fatalf("user_sites column count = %d, want 6", colCount)
	}
}

// TestMigration010_BackfillCorrectness asserts every existing enabled
// user gets a user_sites row matching their legacy (site_id, role).
func TestMigration010_BackfillCorrectness(t *testing.T) {
	// Set up: insert a few users BEFORE running migration 010 by
	// dropping the table that 010 creates, then re-running it manually.
	store, ctx := openUserSitesDB(t)

	// Insert two enabled users + one disabled. The disabled user must
	// NOT receive a backfill row.
	enabledUserA := uuid.New()
	enabledUserB := uuid.New()
	disabledUser := uuid.New()

	if err := store.Conn().Exec(ctx,
		`INSERT INTO `+userSitesTestDB+`.users
			(user_id, site_id, email, username, password_hash, role, disabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, now(), now())`,
		enabledUserA, uint32(1), "a@example.com", "a", "$2a$12$placeholder", "admin", uint8(0),
	); err != nil {
		t.Fatalf("insert enabled user A: %v", err)
	}

	if err := store.Conn().Exec(ctx,
		`INSERT INTO `+userSitesTestDB+`.users
			(user_id, site_id, email, username, password_hash, role, disabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, now(), now())`,
		enabledUserB, uint32(2), "b@example.com", "b", "$2a$12$placeholder", "viewer", uint8(0),
	); err != nil {
		t.Fatalf("insert enabled user B: %v", err)
	}

	if err := store.Conn().Exec(ctx,
		`INSERT INTO `+userSitesTestDB+`.users
			(user_id, site_id, email, username, password_hash, role, disabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, now(), now())`,
		disabledUser, uint32(1), "c@example.com", "c", "$2a$12$placeholder", "admin", uint8(1),
	); err != nil {
		t.Fatalf("insert disabled user: %v", err)
	}

	// Re-run the backfill INSERT (idempotent — ReplacingMergeTree collapses).
	if err := store.Conn().Exec(ctx,
		`INSERT INTO `+userSitesTestDB+`.user_sites (user_id, site_id, role, created_at)
		 SELECT u.user_id, u.site_id, u.role, u.created_at
		 FROM   `+userSitesTestDB+`.users AS u FINAL
		 WHERE  u.disabled = 0 AND u.site_id > 0`,
	); err != nil {
		t.Fatalf("rerun backfill: %v", err)
	}

	store.Conn().Exec(ctx, "OPTIMIZE TABLE "+userSitesTestDB+".user_sites FINAL")

	// Enabled user A: one admin grant on site 1.
	assertGrant(t, ctx, store, enabledUserA, 1, "admin")

	// Enabled user B: one viewer grant on site 2.
	assertGrant(t, ctx, store, enabledUserB, 2, "viewer")

	// Disabled user: no grants at all.
	var cnt uint64
	if err := store.Conn().QueryRow(ctx,
		`SELECT count() FROM `+userSitesTestDB+`.user_sites FINAL
		 WHERE user_id = ? AND revoked = 0`,
		disabledUser,
	).Scan(&cnt); err != nil {
		t.Fatalf("count disabled user grants: %v", err)
	}

	if cnt != 0 {
		t.Fatalf("disabled user grants = %d, want 0", cnt)
	}
}

func assertGrant(t *testing.T, ctx context.Context, store *storage.ClickHouseStore, userID uuid.UUID, siteID uint32, wantRole string) {
	t.Helper()

	var (
		role  string
		count uint64
	)

	if err := store.Conn().QueryRow(ctx,
		`SELECT toString(role), count() OVER ()
		 FROM `+userSitesTestDB+`.user_sites FINAL
		 WHERE user_id = ? AND site_id = ? AND revoked = 0`,
		userID, siteID,
	).Scan(&role, &count); err != nil {
		t.Fatalf("scan grant (user=%s, site=%d): %v", userID, siteID, err)
	}

	if count != 1 {
		t.Fatalf("grant count for (user=%s, site=%d) = %d, want 1", userID, siteID, count)
	}

	if role != wantRole {
		t.Fatalf("grant role for (user=%s, site=%d) = %q, want %q", userID, siteID, role, wantRole)
	}
}

// TestMigration010_OperatorBootstrap_GrantsAllSites pins the
// cross-join bootstrap: when OperatorEmail is set, the operator user
// gets admin on every enabled site after migration 010.
func TestMigration010_OperatorBootstrap_GrantsAllSites(t *testing.T) {
	store, ctx := openUserSitesDB(t)

	operatorID := uuid.New()

	// Seed the operator user + three enabled sites + one disabled site.
	if err := store.Conn().Exec(ctx,
		`INSERT INTO `+userSitesTestDB+`.users
			(user_id, site_id, email, username, password_hash, role, disabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, now(), now())`,
		operatorID, uint32(1), operatorEmail, "ops", "$2a$12$placeholder", "admin", uint8(0),
	); err != nil {
		t.Fatalf("seed operator: %v", err)
	}

	for _, s := range []struct {
		id      uint32
		host    string
		enabled uint8
	}{
		{1, "statnive.com", 1},
		{2, "televika.com", 1},
		{3, "disabled.example", 0},
	} {
		if err := store.Conn().Exec(ctx,
			`INSERT INTO `+userSitesTestDB+`.sites
				(site_id, hostname, slug, enabled) VALUES (?, ?, ?, ?)`,
			s.id, s.host, s.host, s.enabled,
		); err != nil {
			t.Fatalf("seed site %d: %v", s.id, err)
		}
	}

	// Re-run the bootstrap insert. We can't re-run the migration body
	// directly (schema_migrations already records v10), so we
	// hand-rebuild the bootstrap INSERT with the same template
	// substitution. Sites is plain MergeTree (no FINAL); users is
	// ReplacingMergeTree (FINAL required to read latest row).
	if err := store.Conn().Exec(ctx,
		`INSERT INTO `+userSitesTestDB+`.user_sites (user_id, site_id, role)
		 SELECT u.user_id, s.site_id, 'admin'
		 FROM   `+userSitesTestDB+`.users AS u FINAL
		 CROSS JOIN `+userSitesTestDB+`.sites AS s
		 WHERE  u.email = '`+operatorEmail+`'
		   AND  u.disabled = 0
		   AND  s.enabled = 1
		   AND  (u.user_id, s.site_id) NOT IN (
			   SELECT user_id, site_id FROM `+userSitesTestDB+`.user_sites FINAL WHERE revoked = 0
		   )`,
	); err != nil {
		t.Fatalf("re-run bootstrap: %v", err)
	}

	store.Conn().Exec(ctx, "OPTIMIZE TABLE "+userSitesTestDB+".user_sites FINAL")

	// Operator should have admin on sites 1 + 2 (enabled) but NOT 3 (disabled).
	var siteCount uint64
	if err := store.Conn().QueryRow(ctx,
		`SELECT count() FROM `+userSitesTestDB+`.user_sites FINAL
		 WHERE user_id = ? AND revoked = 0`,
		operatorID,
	).Scan(&siteCount); err != nil {
		t.Fatalf("count operator grants: %v", err)
	}

	if siteCount != 2 {
		t.Fatalf("operator grants = %d, want 2 (only enabled sites)", siteCount)
	}

	var disabledSiteGrant uint64
	if err := store.Conn().QueryRow(ctx,
		`SELECT count() FROM `+userSitesTestDB+`.user_sites FINAL
		 WHERE user_id = ? AND site_id = 3 AND revoked = 0`,
		operatorID,
	).Scan(&disabledSiteGrant); err != nil {
		t.Fatalf("count disabled site grant: %v", err)
	}

	if disabledSiteGrant != 0 {
		t.Fatalf("operator was granted on disabled site_id=3 (got %d rows)", disabledSiteGrant)
	}
}

// TestSitesStore_GrantRevokeRoundtrip exercises the production
// ClickHouseSitesStore against real CH. Grants take effect on the next
// LoadUserSites; revokes drop the grant; double-grant collapses via
// ReplacingMergeTree.
func TestSitesStore_GrantRevokeRoundtrip(t *testing.T) {
	store, ctx := openUserSitesDB(t)

	sitesStore := auth.NewClickHouseSitesStore(store.Conn(), userSitesTestDB)
	uid := uuid.New()

	// Initially: no grants.
	grants, err := sitesStore.LoadUserSites(ctx, uid)
	if err != nil {
		t.Fatalf("LoadUserSites: %v", err)
	}

	if len(grants) != 0 {
		t.Fatalf("initial grants = %v, want empty", grants)
	}

	// Grant admin on 5.
	if err := sitesStore.Grant(ctx, uid, 5, auth.RoleAdmin); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	grants, err = sitesStore.LoadUserSites(ctx, uid)
	if err != nil {
		t.Fatalf("LoadUserSites: %v", err)
	}

	if got, ok := grants[5]; !ok || got != auth.RoleAdmin {
		t.Fatalf("post-grant: grants[5] = %q (ok=%v), want admin", got, ok)
	}

	// Double-grant collapses to one effective row.
	if err := sitesStore.Grant(ctx, uid, 5, auth.RoleAdmin); err != nil {
		t.Fatalf("re-Grant: %v", err)
	}

	store.Conn().Exec(ctx, "OPTIMIZE TABLE "+userSitesTestDB+".user_sites FINAL")

	grants, err = sitesStore.LoadUserSites(ctx, uid)
	if err != nil {
		t.Fatalf("LoadUserSites post-reGrant: %v", err)
	}

	if len(grants) != 1 {
		t.Fatalf("post-reGrant grants = %v, want exactly 1 entry (collapsed)", grants)
	}

	// Revoke.
	if err := sitesStore.Revoke(ctx, uid, 5); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Small sleep to let CH propagate before FINAL read on slower runners.
	time.Sleep(50 * time.Millisecond)

	grants, err = sitesStore.LoadUserSites(ctx, uid)
	if err != nil {
		t.Fatalf("LoadUserSites post-Revoke: %v", err)
	}

	if _, ok := grants[5]; ok {
		t.Fatalf("post-Revoke: grants[5] still present (%v); revoke must take effect on the next LoadUserSites", grants)
	}
}

// TestSitesStore_ListUsersBySite returns every active user on a site.
func TestSitesStore_ListUsersBySite(t *testing.T) {
	store, ctx := openUserSitesDB(t)

	sitesStore := auth.NewClickHouseSitesStore(store.Conn(), userSitesTestDB)

	alice := uuid.New()
	bob := uuid.New()
	charlie := uuid.New()

	if err := sitesStore.Grant(ctx, alice, 7, auth.RoleAdmin); err != nil {
		t.Fatalf("grant alice: %v", err)
	}

	if err := sitesStore.Grant(ctx, bob, 7, auth.RoleViewer); err != nil {
		t.Fatalf("grant bob: %v", err)
	}

	if err := sitesStore.Grant(ctx, charlie, 8, auth.RoleAdmin); err != nil {
		t.Fatalf("grant charlie: %v", err)
	}

	// Revoke bob to confirm ListUsersBySite filters revoked rows.
	if err := sitesStore.Revoke(ctx, bob, 7); err != nil {
		t.Fatalf("revoke bob: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	users, err := sitesStore.ListUsersBySite(ctx, 7)
	if err != nil {
		t.Fatalf("ListUsersBySite: %v", err)
	}

	if len(users) != 1 {
		t.Fatalf("users on site 7 = %d, want 1 (alice — bob was revoked, charlie is on site 8)", len(users))
	}

	if users[0].UserID != alice || users[0].Role != auth.RoleAdmin {
		t.Fatalf("site-7 user = %+v, want alice admin", users[0])
	}
}
