package storage

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Migrations holds the embedded SQL files. They are referenced by their
// numeric prefix (001_*.sql, 002_*.sql, …). Each file is a Go text/template
// rendered with the cluster name from config so the same DDL works in both
// single-node (PLAN.md verification 26) and Distributed-cluster modes.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// MigrationData is the template context passed to every embedded SQL file.
// .Cluster == "" for single-node — the template's {{if .Cluster}} branches
// degrade gracefully.
type MigrationData struct {
	Cluster string
}

// MigrationConfig is read by main.go from the YAML config.
type MigrationConfig struct {
	// Cluster is the ClickHouse cluster name for ON CLUSTER / Replicated DDL.
	// Empty in single-node mode (the v1 default).
	Cluster string
	// Database is the schema name; "statnive" by default.
	Database string
}

// MigrationRunner applies numbered migrations in order. Idempotent — already-
// applied migrations are skipped by version. Advisory locks (PLAN.md:161)
// are deferred to the next slice; the v1 single-developer dev box doesn't
// race on startup.
type MigrationRunner struct {
	conn   driver.Conn
	cfg    MigrationConfig
	logger *slog.Logger
	files  embed.FS
}

// NewMigrationRunner constructs a runner against the given ClickHouse store.
func NewMigrationRunner(conn driver.Conn, cfg MigrationConfig, logger *slog.Logger) *MigrationRunner {
	if cfg.Database == "" {
		cfg.Database = "statnive"
	}

	return &MigrationRunner{conn: conn, cfg: cfg, logger: logger, files: Migrations}
}

// Run applies every embedded migration whose version is greater than the
// most recent row in schema_migrations. The first migration creates the
// schema_migrations table itself, so the runner bootstraps the database.
func (r *MigrationRunner) Run(ctx context.Context) error {
	entries, err := r.files.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	type planned struct {
		version uint32
		name    string
		body    string
	}

	plans := make([]planned, 0, len(entries))
	tmplData := MigrationData{Cluster: r.cfg.Cluster}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}

		version, parseErr := parseVersion(e.Name())
		if parseErr != nil {
			return fmt.Errorf("migration %q: %w", e.Name(), parseErr)
		}

		raw, readErr := r.files.ReadFile("migrations/" + e.Name())
		if readErr != nil {
			return fmt.Errorf("read migration %q: %w", e.Name(), readErr)
		}

		body, renderErr := renderMigration(e.Name(), raw, tmplData)
		if renderErr != nil {
			return fmt.Errorf("render migration %q: %w", e.Name(), renderErr)
		}

		plans = append(plans, planned{version: version, name: e.Name(), body: body})
	}

	sort.Slice(plans, func(i, j int) bool { return plans[i].version < plans[j].version })

	applied, err := r.appliedVersions(ctx)
	if err != nil {
		return err
	}

	for _, p := range plans {
		if _, ok := applied[p.version]; ok {
			r.logger.Debug("migration already applied", "version", p.version, "name", p.name)

			continue
		}

		r.logger.Info("applying migration", "version", p.version, "name", p.name)

		if execErr := r.applyOne(ctx, p.body); execErr != nil {
			return fmt.Errorf("apply migration %q: %w", p.name, execErr)
		}

		if recordErr := r.recordApplied(ctx, p.version); recordErr != nil {
			return fmt.Errorf("record migration %q: %w", p.name, recordErr)
		}
	}

	return nil
}

func (r *MigrationRunner) appliedVersions(ctx context.Context) (map[uint32]struct{}, error) {
	out := make(map[uint32]struct{})

	rows, err := r.conn.Query(ctx, `SELECT version FROM `+r.cfg.Database+`.schema_migrations`)
	if err != nil {
		// First-run: the database / table doesn't exist yet — return an
		// empty set so the bootstrap migration runs.
		if isMissingTable(err) {
			return out, nil
		}

		return nil, fmt.Errorf("read applied migrations: %w", err)
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var v uint32
		if scanErr := rows.Scan(&v); scanErr != nil {
			return nil, fmt.Errorf("scan migration version: %w", scanErr)
		}

		out[v] = struct{}{}
	}

	return out, rows.Err()
}

func (r *MigrationRunner) applyOne(ctx context.Context, body string) error {
	for _, stmt := range splitStatements(body) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		if execErr := r.conn.Exec(ctx, stmt); execErr != nil {
			return fmt.Errorf("exec %q: %w", firstLine(stmt), execErr)
		}
	}

	return nil
}

func (r *MigrationRunner) recordApplied(ctx context.Context, version uint32) error {
	return r.conn.Exec(ctx,
		`INSERT INTO `+r.cfg.Database+`.schema_migrations (version, dirty, sequence) VALUES (?, 0, ?)`,
		version, version,
	)
}

// parseVersion extracts the leading uint from filenames like "001_initial.sql".
func parseVersion(name string) (uint32, error) {
	idx := strings.IndexByte(name, '_')
	if idx <= 0 {
		return 0, errors.New(`expected "NNN_name.sql" prefix`)
	}

	v, err := strconv.ParseUint(name[:idx], 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse version: %w", err)
	}

	return uint32(v), nil
}

// RenderMigration is the exported test seam for renderMigration. Tests
// that need to apply a subset of migrations (data-preservation gate)
// reuse this so the rendering path stays bit-identical to production.
func RenderMigration(name string, body []byte, cluster string) (string, error) {
	return renderMigration(name, body, MigrationData{Cluster: cluster})
}

// SplitStatements is the exported test seam for splitStatements. Strips
// line comments BEFORE splitting on ';' so a literal ';' inside a
// comment doesn't truncate a real statement (the inverse order silently
// breaks migration 001 which contains "Go text/template;" in a comment).
func SplitStatements(body string) []string { return splitStatements(body) }

func renderMigration(name string, body []byte, data MigrationData) (string, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(body))
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}

	var buf bytes.Buffer
	if execErr := tmpl.Execute(&buf, data); execErr != nil {
		return "", fmt.Errorf("execute template: %w", execErr)
	}

	return buf.String(), nil
}

// splitStatements strips line comments first, then splits on ";". Stripping
// before splitting matters: a "--" comment containing a literal ";" would
// otherwise truncate the SQL statement that follows it.
func splitStatements(body string) []string {
	parts := strings.Split(stripLineComments(body), ";")

	out := make([]string, 0, len(parts))

	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}

		out = append(out, p)
	}

	return out
}

func stripLineComments(s string) string {
	out := make([]string, 0)

	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}

		out = append(out, line)
	}

	return strings.Join(out, "\n")
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}

	return s
}

// isMissingTable returns true for the "Unknown table" / "Database … doesn't
// exist" / "Unknown table expression identifier" errors clickhouse-go
// surfaces on first run (varies across CH versions).
func isMissingTable(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()

	return strings.Contains(msg, "UNKNOWN_TABLE") ||
		strings.Contains(msg, "UNKNOWN_DATABASE") ||
		strings.Contains(msg, "Unknown table") ||
		strings.Contains(msg, "Unknown database") ||
		strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "code: 60") ||
		strings.Contains(msg, "code: 81")
}
