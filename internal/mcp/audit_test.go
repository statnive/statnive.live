package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/storage"
)

// TestToolsCall_AuditCarriesNoFilterValues pins Privacy Rule 4 for the MCP
// surface: the audit event records the tool + site + non-PII actor label, but
// NEVER the filter/search/argument VALUES (which are attacker-controllable UGC
// that may carry PII). A filter value passed in the call must not appear in
// the audit JSONL.
func TestToolsCall_AuditCarriesNoFilterValues(t *testing.T) {
	t.Parallel()

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	auditLog, err := audit.New(auditPath)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}

	s := New(Config{
		Store:    &fakeStore{overview: &storage.OverviewResult{Visitors: 1}},
		Registry: newTestRegistry(),
		Audit:    auditLog,
		Version:  "t",
		Budget:   BudgetConfig{CallsPerMin: 100, RowsPerMin: 100000, WildcardFactor: 1},
		Now:      func() time.Time { return testNow },
	})

	const secret = "SUPER-SECRET-REFERRER-VALUE"

	resp := call(t, s, wildcardActor(), "tools/call", callParams{
		Name:      "overview",
		Arguments: json.RawMessage(`{"site":"1","filters":{"referrer":"` + secret + `"}}`),
	})

	if resp == nil || resp.Error != nil {
		t.Fatalf("overview call failed: %+v", resp)
	}

	if err := auditLog.Close(); err != nil {
		t.Fatalf("audit close: %v", err)
	}

	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}

	content := string(data)

	if strings.Contains(content, secret) {
		t.Errorf("audit log leaked the filter value:\n%s", content)
	}

	if !strings.Contains(content, string(audit.EventMCPToolCall)) {
		t.Errorf("audit log missing mcp.tool_call event:\n%s", content)
	}
}

// TestToolsCall_AuditDeniedEventOnCrossTenant confirms a cross-tenant denial
// emits mcp.denied (so the attempt is visible) without leaking the requested
// args.
func TestToolsCall_AuditDeniedEventOnCrossTenant(t *testing.T) {
	t.Parallel()

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	auditLog, err := audit.New(auditPath)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}

	s := New(Config{
		Store:    &fakeStore{overview: &storage.OverviewResult{}},
		Registry: newTestRegistry(),
		Audit:    auditLog,
		Version:  "t",
		Budget:   BudgetConfig{CallsPerMin: 100, RowsPerMin: 100000, WildcardFactor: 1},
		Now:      func() time.Time { return testNow },
	})

	// Scoped to site 1, requesting site 2 → denied.
	resp := call(t, s, syntheticOperator([]uint32{1}, false), "tools/call", callParams{
		Name:      "overview",
		Arguments: json.RawMessage(`{"site":"2","range":"7d"}`),
	})

	if resp == nil || resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("want -32602 cross-tenant, got %+v", resp)
	}

	if err := auditLog.Close(); err != nil {
		t.Fatalf("audit close: %v", err)
	}

	data, _ := os.ReadFile(auditPath)
	if !strings.Contains(string(data), string(audit.EventMCPDenied)) {
		t.Errorf("audit log missing mcp.denied event:\n%s", data)
	}
}
