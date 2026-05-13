package storage

import (
	"strings"
	"testing"
)

// TestRenderMigration_010_OperatorEmail asserts that the operator-bootstrap
// branch of migration 010 is gated on a non-empty OperatorEmail. The
// guard lets dev / CI / fresh-tenant deploys skip the bootstrap entirely
// without phantom-granting an empty-string user.
func TestRenderMigration_010_OperatorEmail(t *testing.T) {
	t.Parallel()

	body, err := Migrations.ReadFile("migrations/010_user_sites.sql")
	if err != nil {
		t.Fatalf("read migration 010: %v", err)
	}

	t.Run("non-empty email renders bootstrap insert", func(t *testing.T) {
		t.Parallel()

		got, renderErr := RenderMigrationWith("010_user_sites.sql", body, MigrationData{
			OperatorEmail: "ops@statnive.live",
		})
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}

		// Bootstrap branch present.
		if !strings.Contains(got, "u.email = 'ops@statnive.live'") {
			t.Fatalf("operator-email literal missing from rendered SQL:\n%s", got)
		}

		// CROSS JOIN sites — the bootstrap shape — present.
		if !strings.Contains(got, "CROSS JOIN statnive.sites AS s") {
			t.Fatalf("bootstrap CROSS JOIN missing")
		}
	})

	t.Run("empty email skips bootstrap insert entirely", func(t *testing.T) {
		t.Parallel()

		got, renderErr := RenderMigrationWith("010_user_sites.sql", body, MigrationData{
			OperatorEmail: "",
		})
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}

		// First backfill (the 1:1 from users) still runs.
		if !strings.Contains(got, "FROM   statnive.users AS u FINAL") {
			t.Fatalf("primary backfill should still run when OperatorEmail empty:\n%s", got)
		}

		// Bootstrap branch absent.
		if strings.Contains(got, "CROSS JOIN statnive.sites AS s") {
			t.Fatalf("bootstrap CROSS JOIN must be skipped when OperatorEmail empty")
		}
	})
}

// TestRenderMigration_010_ClusterTemplate pins the {{if .Cluster}}
// branch shape so the single-node → Distributed flip stays a config
// switch (CLAUDE.md Stack — "single-node → Distributed is a config
// flip"). Asserts both branches.
func TestRenderMigration_010_ClusterTemplate(t *testing.T) {
	t.Parallel()

	body, err := Migrations.ReadFile("migrations/010_user_sites.sql")
	if err != nil {
		t.Fatalf("read migration 010: %v", err)
	}

	t.Run("single-node uses plain ReplacingMergeTree", func(t *testing.T) {
		t.Parallel()

		got, renderErr := RenderMigrationWith("010_user_sites.sql", body, MigrationData{})
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}

		if !strings.Contains(got, "ENGINE = ReplacingMergeTree(updated_at)") {
			t.Fatalf("single-node engine missing:\n%s", got)
		}

		if strings.Contains(got, "ON CLUSTER") {
			t.Fatalf("single-node render must not contain ON CLUSTER")
		}
	})

	t.Run("cluster mode emits Replicated + ON CLUSTER", func(t *testing.T) {
		t.Parallel()

		got, renderErr := RenderMigrationWith("010_user_sites.sql", body, MigrationData{
			Cluster: "statnive_cluster",
		})
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}

		if !strings.Contains(got, "ON CLUSTER statnive_cluster") {
			t.Fatalf("cluster mode missing ON CLUSTER clause")
		}

		if !strings.Contains(got, "ReplicatedReplacingMergeTree(") {
			t.Fatalf("cluster mode missing Replicated engine")
		}
	})
}

// TestRenderMigration_LegacySignature pins the back-compat shim — the
// existing 3-arg RenderMigration(name, body, cluster) call sites in
// test/data_preservation_e2e_test.go + test/rollup_sum_e2e_test.go
// continue to work after MigrationData grew the OperatorEmail field.
func TestRenderMigration_LegacySignature(t *testing.T) {
	t.Parallel()

	body, err := Migrations.ReadFile("migrations/001_initial.sql")
	if err != nil {
		t.Fatalf("read migration 001: %v", err)
	}

	got, err := RenderMigration("001_initial.sql", body, "")
	if err != nil {
		t.Fatalf("legacy render: %v", err)
	}

	if got == "" {
		t.Fatalf("legacy render returned empty body")
	}
}

// TestRenderMigration_010_SplitsCleanly asserts the rendered SQL
// survives the migration runner's statement-splitter without truncation
// (LEARN.md migration 004 lesson — inline parens in -- comments are a
// known foot-gun; 010 carefully avoids inline column comments).
func TestRenderMigration_010_SplitsCleanly(t *testing.T) {
	t.Parallel()

	body, err := Migrations.ReadFile("migrations/010_user_sites.sql")
	if err != nil {
		t.Fatalf("read migration 010: %v", err)
	}

	got, err := RenderMigrationWith("010_user_sites.sql", body, MigrationData{
		OperatorEmail: "ops@statnive.live",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	stmts := SplitStatements(got)

	// Expected statements: CREATE TABLE, primary backfill INSERT,
	// operator-bootstrap INSERT. Anything else means the splitter
	// either kept a stripped comment as a "statement" or truncated.
	if len(stmts) != 3 {
		t.Fatalf("SplitStatements returned %d, want 3 (CREATE + 2 INSERTs)", len(stmts))
	}

	if !strings.Contains(stmts[0], "CREATE TABLE") {
		t.Fatalf("first statement should be CREATE TABLE, got: %s", stmts[0])
	}

	if !strings.Contains(stmts[1], "INSERT INTO statnive.user_sites") {
		t.Fatalf("second statement should be backfill INSERT")
	}

	if !strings.Contains(stmts[2], "CROSS JOIN statnive.sites AS s") {
		t.Fatalf("third statement should be operator-bootstrap INSERT, got: %s", stmts[2])
	}
}

// TestRenderMigration_011_RollupTTL pins the three ALTER statements of
// migration 011 and the cluster-template branch. Order-agnostic — the
// rollup column (hour vs day) is asserted alongside the table name.
func TestRenderMigration_011_RollupTTL(t *testing.T) {
	t.Parallel()

	body, err := Migrations.ReadFile("migrations/011_rollup_ttl.sql")
	if err != nil {
		t.Fatalf("read migration 011: %v", err)
	}

	t.Run("single-node renders three plain ALTERs", func(t *testing.T) {
		t.Parallel()

		got, renderErr := RenderMigrationWith("011_rollup_ttl.sql", body, MigrationData{})
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}

		stmts := SplitStatements(got)
		if len(stmts) != 3 {
			t.Fatalf("SplitStatements returned %d, want 3 (one ALTER per rollup)", len(stmts))
		}

		joined := strings.Join(stmts, "\n")

		if strings.Contains(joined, "ON CLUSTER") {
			t.Fatalf("single-node statements must not contain ON CLUSTER")
		}

		for _, want := range []string{
			"statnive.hourly_visitors",
			"statnive.daily_pages",
			"statnive.daily_sources",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing ALTER for %s", want)
			}
		}

		// hour column for hourly_visitors, day for the two daily rollups.
		if !strings.Contains(joined, "MODIFY TTL hour + INTERVAL 750 DAY DELETE") {
			t.Errorf("hourly_visitors TTL clause missing")
		}

		if strings.Count(joined, "MODIFY TTL day + INTERVAL 750 DAY DELETE") != 2 {
			t.Errorf("expected day-column TTL twice (daily_pages + daily_sources)")
		}
	})

	t.Run("cluster mode emits ON CLUSTER three times", func(t *testing.T) {
		t.Parallel()

		got, renderErr := RenderMigrationWith("011_rollup_ttl.sql", body, MigrationData{
			Cluster: "statnive_cluster",
		})
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}

		joined := strings.Join(SplitStatements(got), "\n")
		if strings.Count(joined, "ON CLUSTER statnive_cluster") != 3 {
			t.Fatalf("cluster mode should emit ON CLUSTER 3 times, got:\n%s", joined)
		}
	})
}

// TestRenderMigration_012_ScrubCookieID pins the one-shot mutation that
// clears legacy raw-UUID cookie_id values. The filter `NOT LIKE 'h:%'`
// is what makes this idempotent — re-running matches zero rows.
func TestRenderMigration_012_ScrubCookieID(t *testing.T) {
	t.Parallel()

	body, err := Migrations.ReadFile("migrations/012_scrub_unhashed_cookieid.sql")
	if err != nil {
		t.Fatalf("read migration 012: %v", err)
	}

	got, err := RenderMigrationWith("012_scrub_unhashed_cookieid.sql", body, MigrationData{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	stmts := SplitStatements(got)
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}

	joined := strings.Join(stmts, "\n")

	for _, want := range []string{
		"ALTER TABLE statnive.events_raw",
		"UPDATE cookie_id = ''",
		"cookie_id NOT LIKE 'h:%'",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in rendered SQL", want)
		}
	}

	// The migration deliberately omits mutations_sync so the runner
	// doesn't block on a multi-GB rewrite (header comment explains why).
	if strings.Contains(joined, "mutations_sync") {
		t.Errorf("migration must not set mutations_sync (would stall startup)")
	}
}

// TestRenderMigration_013_SitesJurisdiction pins the three new columns,
// the OTHER-NON-EU/permissive backfill predicate, and the cluster-mode
// ON CLUSTER expansion.
func TestRenderMigration_013_SitesJurisdiction(t *testing.T) {
	t.Parallel()

	body, err := Migrations.ReadFile("migrations/013_sites_jurisdiction.sql")
	if err != nil {
		t.Fatalf("read migration 013: %v", err)
	}

	t.Run("single-node adds 3 cols + backfill", func(t *testing.T) {
		t.Parallel()

		got, renderErr := RenderMigrationWith("013_sites_jurisdiction.sql", body, MigrationData{})
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}

		stmts := SplitStatements(got)
		if len(stmts) != 2 {
			t.Fatalf("SplitStatements returned %d, want 2 (ALTER ADD COLUMNs + ALTER UPDATE)", len(stmts))
		}

		joined := strings.Join(stmts, "\n")

		for _, want := range []string{
			"ADD COLUMN IF NOT EXISTS jurisdiction",
			"ADD COLUMN IF NOT EXISTS consent_mode",
			"ADD COLUMN IF NOT EXISTS event_allowlist",
			"LowCardinality(String) DEFAULT 'OTHER-NON-EU'",
			"LowCardinality(String) DEFAULT 'permissive'",
			"UPDATE jurisdiction = 'OTHER-NON-EU'",
			"consent_mode = 'permissive'",
			"WHERE jurisdiction = '' OR consent_mode = ''",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("missing %q in rendered SQL", want)
			}
		}

		if strings.Contains(joined, "ON CLUSTER") {
			t.Errorf("single-node statements must not contain ON CLUSTER")
		}
	})

	t.Run("cluster mode emits ON CLUSTER twice", func(t *testing.T) {
		t.Parallel()

		got, renderErr := RenderMigrationWith("013_sites_jurisdiction.sql", body, MigrationData{
			Cluster: "statnive_cluster",
		})
		if renderErr != nil {
			t.Fatalf("render: %v", renderErr)
		}

		joined := strings.Join(SplitStatements(got), "\n")
		if strings.Count(joined, "ON CLUSTER statnive_cluster") != 2 {
			t.Fatalf("cluster mode should emit ON CLUSTER twice (ADD + UPDATE), got:\n%s", joined)
		}
	})
}

// TestRenderMigration_014_UsersJurisdictionNotice pins the one-column
// addition that drives the Stage-3 one-time admin notice dismissal.
// Default 0 = not dismissed, so every existing admin sees the prompt
// exactly once after Stage 3 lands.
func TestRenderMigration_014_UsersJurisdictionNotice(t *testing.T) {
	t.Parallel()

	body, err := Migrations.ReadFile("migrations/014_users_jurisdiction_notice.sql")
	if err != nil {
		t.Fatalf("read migration 014: %v", err)
	}

	got, renderErr := RenderMigrationWith("014_users_jurisdiction_notice.sql", body, MigrationData{})
	if renderErr != nil {
		t.Fatalf("render: %v", renderErr)
	}

	joined := strings.Join(SplitStatements(got), "\n")

	for _, want := range []string{
		"ALTER TABLE statnive.users",
		"ADD COLUMN IF NOT EXISTS jurisdiction_notice_dismissed UInt8 DEFAULT 0",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in rendered SQL", want)
		}
	}
}
