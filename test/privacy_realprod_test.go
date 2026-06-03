//go:build integration

// privacy_realprod_test.go — ephemeral-layer matrix tests for the
// privacy + tracker contract Televika depends on, per
// plan-to-design-real-production-eager-pine.md categories B + C + D.
//
// Each test boots the real cmd/statnive-live router via
// newIntegrationStack against docker ClickHouse, fires HTTP requests
// through the live handler chain, and asserts the contract via the
// ClickHouse-Oracle (events_raw rows) + the audit-JSONL oracle
// (audittest.WaitForEvent / ReadEventNames).
//
// Scope: B1/B2/B3/B4 fast-reject + B5/B6 GPC/DNT identity suppression
// + C4 opt-out audit event. Positive-tracking (A1/A2) is covered by
// the existing TestIngestPipelineSmoke + TestMultitenantVisitorHash
// Separation. /api/about and /metrics (D1/D2) are covered by
// test/smoke/harness.sh.

package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/audit/audittest"
	"github.com/statnive/statnive.live/internal/ingest"
)

// postEventRaw is the per-test helper for the matrix below: builds a
// JSON RawEvent body, sets headers (UA, prefetch, GPC, DNT, ...), and
// returns the HTTP status without polluting the test body with
// boilerplate. nil hostHeader means "use the request URL's host".
func postEventRaw(
	t *testing.T,
	srv string,
	hostname string,
	headers map[string]string,
	cookies []*http.Cookie,
) int {
	t.Helper()

	body, _ := json.Marshal(ingest.RawEvent{
		Hostname:  hostname,
		Pathname:  "/probe",
		EventType: "pageview",
		EventName: "pageview",
	})

	req, err := http.NewRequest(http.MethodPost, srv+"/api/event", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "text/plain")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if _, hasUA := headers["User-Agent"]; !hasUA {
		req.Header.Set("User-Agent", "Mozilla/5.0 (PrivacyRealProdTest) BrowserLike")
	}

	if hostname != "" {
		req.Host = hostname
	}

	for _, c := range cookies {
		req.AddCookie(c)
	}

	resp, err := (&http.Client{Timeout: testHTTPTimeout}).Do(req)
	if err != nil {
		t.Fatalf("do POST: %v", err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	return resp.StatusCode
}

// ---------- B — Negative tracking gates ----------

// TestTrack_PrefetchHeader_Drops204 (matrix B1). Pre-pipeline
// fast-reject middleware MUST 204 every prefetch variant before any
// hash work — handler.go:388-391. CLAUDE.md Architecture Rule 6 +
// doc 24 §Sec 1 item 6.
func TestTrack_PrefetchHeader_Drops204(t *testing.T) {
	const (
		siteID   uint32 = 5201
		hostname        = "stage4-prefetch.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	cases := []map[string]string{
		{"X-Purpose": "prefetch"},
		{"Purpose": "prefetch"},
		{"X-Moz": "prefetch"},
	}

	for _, headers := range cases {
		name := ""
		for k := range headers {
			name = k
		}

		t.Run(name, func(t *testing.T) {
			status := postEventRaw(t, stack.srv.URL, hostname, headers, nil)
			if status != http.StatusNoContent {
				t.Errorf("status = %d, want 204 (prefetch header %s should fast-reject)", status, name)
			}
		})
	}

	// Belt-and-braces: zero rows landed in CH for this site.
	time.Sleep(200 * time.Millisecond)

	var rows uint64
	if err := stack.store.Conn().QueryRow(stack.ctx,
		`SELECT count() FROM statnive.events_raw WHERE site_id = ?`, siteID).Scan(&rows); err != nil {
		t.Fatalf("oracle scan: %v", err)
	}

	if rows != 0 {
		t.Errorf("events_raw rows = %d, want 0 (prefetch must not persist)", rows)
	}
}

// TestTrack_UAReject_Variants (matrix B2 + B3 combined). Every
// fastReject UA branch MUST drop the event with 204 before any
// pipeline work — handler.go:395-409.
func TestTrack_UAReject_Variants(t *testing.T) {
	const (
		siteID   uint32 = 5202
		hostname        = "stage4-uareject.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	cases := []struct {
		name string
		ua   string
	}{
		// B2: length + character-set gates
		{"too_short", "Mozilla"},                         // <16
		{"too_long", strings.Repeat("Mozilla/5.0 ", 60)}, // >500
		{"non_ascii", "Mozilla/5.0 \xc3\xa4 Browser"},    // umlaut byte
		// B3: shape-based gates
		{"ip_shaped", "203.0.113.42"},
		{"uuid_shaped", "550e8400-e29b-41d4-a716-446655440000"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status := postEventRaw(t, stack.srv.URL, hostname, map[string]string{
				"User-Agent": c.ua,
			}, nil)

			if status != http.StatusNoContent {
				t.Errorf("status = %d, want 204 (UA %q should fast-reject)", status, c.ua)
			}
		})
	}

	time.Sleep(200 * time.Millisecond)

	var rows uint64
	if err := stack.store.Conn().QueryRow(stack.ctx,
		`SELECT count() FROM statnive.events_raw WHERE site_id = ?`, siteID).Scan(&rows); err != nil {
		t.Fatalf("oracle scan: %v", err)
	}

	if rows != 0 {
		t.Errorf("events_raw rows = %d, want 0 (UA-rejected events must not persist)", rows)
	}
}

// TestTrack_UnknownHostname_Drops204 (matrix B4). The handler's
// hostname-lookup at handler.go:216-228 MUST 204 + emit
// EventHostnameUnknown when r.Host doesn't resolve to any registered
// site. The audit event is what tells the operator someone is
// misconfigured.
func TestTrack_UnknownHostname_Drops204(t *testing.T) {
	const (
		siteID   uint32 = 5203
		hostname        = "stage4-known.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	// Send with a hostname that is NOT seeded. fastReject doesn't
	// gate on hostname; the unknown-host check fires inside the
	// handler proper.
	const bogusHost = "no-such-site-12345.example.invalid"

	status := postEventRaw(t, stack.srv.URL, bogusHost, nil, nil)
	if status != http.StatusNoContent {
		t.Errorf("status = %d, want 204 (unknown hostname must drop)", status)
	}

	// Wait briefly — the handler emits to the audit log inline, so
	// the file should be ready immediately. Use WaitForEvent anyway
	// for any minor scheduling slack.
	if !audittest.WaitForEvent(t, stack.auditPath, string(audit.EventHostnameUnknown), 2*time.Second) {
		t.Fatalf("EventHostnameUnknown not emitted — got %v",
			audittest.ReadEventNames(t, stack.auditPath))
	}
}

// TestTrack_SecGPC_IdentitySuppressed (matrix B5). When the site
// policy has respect_gpc=1 and the visitor sends Sec-GPC: 1, the
// handler at handler.go:516-518 MUST short-circuit identity before
// hash computation — Privacy Rule 9. The event still lands (no
// data destruction), but cookie_id stays empty.
func TestTrack_SecGPC_IdentitySuppressed(t *testing.T) {
	const (
		siteID   uint32 = 5204
		hostname        = "stage4-gpc.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	// Flip the site's respect_gpc=1 — newIntegrationStack defaults to
	// 0 via the column DEFAULT.
	if err := stack.store.Conn().Exec(stack.ctx,
		`ALTER TABLE statnive.sites UPDATE respect_gpc = 1 WHERE site_id = ? SETTINGS mutations_sync = 2`,
		siteID,
	); err != nil {
		t.Fatalf("set respect_gpc: %v", err)
	}

	status := postEventRaw(t, stack.srv.URL, hostname, map[string]string{
		"Sec-GPC": "1",
	}, []*http.Cookie{
		{Name: "_statnive", Value: "gpc-test-cookie-uuid"},
	})

	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (event accepted but identity suppressed)", status)
	}

	// Wait for the consumer to drain the WAL batch — waitForCount
	// fatals on timeout.
	waitForCount(t, stack.ctx, stack.store, siteID, 1, 5*time.Second)

	var cookieID string
	if err := stack.store.Conn().QueryRow(stack.ctx,
		`SELECT cookie_id FROM statnive.events_raw WHERE site_id = ?`, siteID).Scan(&cookieID); err != nil {
		t.Fatalf("oracle scan cookie_id: %v", err)
	}

	if cookieID != "" {
		t.Errorf("cookie_id = %q, want empty (Sec-GPC=1 must suppress identity)", cookieID)
	}
}

// TestTrack_DNT_IdentitySuppressed (matrix B6). Mirror of B5 for the
// DNT header. handler.go:520-522.
func TestTrack_DNT_IdentitySuppressed(t *testing.T) {
	const (
		siteID   uint32 = 5205
		hostname        = "stage4-dnt.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	if err := stack.store.Conn().Exec(stack.ctx,
		`ALTER TABLE statnive.sites UPDATE respect_dnt = 1 WHERE site_id = ? SETTINGS mutations_sync = 2`,
		siteID,
	); err != nil {
		t.Fatalf("set respect_dnt: %v", err)
	}

	status := postEventRaw(t, stack.srv.URL, hostname, map[string]string{
		"DNT": "1",
	}, []*http.Cookie{
		{Name: "_statnive", Value: "dnt-test-cookie-uuid"},
	})

	if status != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", status)
	}

	waitForCount(t, stack.ctx, stack.store, siteID, 1, 5*time.Second)

	var cookieID string
	if err := stack.store.Conn().QueryRow(stack.ctx,
		`SELECT cookie_id FROM statnive.events_raw WHERE site_id = ?`, siteID).Scan(&cookieID); err != nil {
		t.Fatalf("oracle scan cookie_id: %v", err)
	}

	if cookieID != "" {
		t.Errorf("cookie_id = %q, want empty (DNT=1 must suppress identity)", cookieID)
	}
}

// ---------- C — Privacy APIs (audit-event coverage) ----------

// TestPrivacy_OptOut_AuditEventEmitted (matrix C4). The OptOut
// handler MUST emit EventOptOutReceived synchronously — operators
// grep for this event to prove an Art. 21 objection was honoured.
func TestPrivacy_OptOut_AuditEventEmitted(t *testing.T) {
	const (
		siteID   uint32 = 5206
		hostname        = "stage4-optout-audit.example.com"
	)

	stack := newIntegrationStack(t, siteID, hostname)
	defer stack.shutdown()

	resp := postJSON(t, stack.srv, "/api/privacy/opt-out", "", nil, hostname)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("opt-out status = %d, want 204", resp.StatusCode)
	}

	if !audittest.WaitForEvent(t, stack.auditPath, string(audit.EventOptOutReceived), 2*time.Second) {
		t.Fatalf("EventOptOutReceived not emitted — got %v",
			audittest.ReadEventNames(t, stack.auditPath))
	}
}
