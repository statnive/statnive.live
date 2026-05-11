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
