package cert_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/audit/audittest"
	"github.com/statnive/statnive.live/internal/cert"
)

func TestExpiry_FiresWarnAndCriticalOnce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "test.crt")
	keyPath := filepath.Join(dir, "test.key")
	auditPath := filepath.Join(dir, "audit.jsonl")

	notAfter := time.Now().Add(365 * 24 * time.Hour)
	writeCertAt(t, certPath, keyPath, "expiry-test", notAfter)

	auditLog, err := audit.New(auditPath)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	t.Cleanup(func() { _ = auditLog.Close() })

	loader, err := cert.New(certPath, keyPath, auditLog)
	if err != nil {
		t.Fatalf("loader: %v", err)
	}

	// Fake clock: advance progressively past each band crossing.
	var fakeNow time.Time

	w := cert.NewExpiryWatcher(loader, auditLog, func() time.Time { return fakeNow })

	// At T-365d (exactly when the cert was issued) — fresh, no event.
	fakeNow = notAfter.Add(-365 * 24 * time.Hour)
	w.CheckNow()

	// Cross into warn band: 29 days before expiry.
	fakeNow = notAfter.Add(-29 * 24 * time.Hour)
	w.CheckNow()

	// Repeat at 28 days — must NOT re-emit (level unchanged).
	fakeNow = notAfter.Add(-28 * 24 * time.Hour)
	w.CheckNow()

	// Cross into critical band: 6 days before expiry.
	fakeNow = notAfter.Add(-6 * 24 * time.Hour)
	w.CheckNow()

	// Repeat at 1 day — must NOT re-emit.
	fakeNow = notAfter.Add(-24 * time.Hour)
	w.CheckNow()

	// Force the audit log to flush before reading.
	if err := auditLog.Close(); err != nil {
		t.Fatalf("audit close: %v", err)
	}

	events := audittest.ReadEventNames(t, auditPath)

	// Expected: tls.cert_loaded (from loader.New) +
	//           tls.expiry_warning (one crossing) +
	//           tls.expiry_critical (one crossing).
	wantContains := map[string]int{
		string(audit.EventTLSCertLoaded):     1,
		string(audit.EventTLSExpiryWarn):     1,
		string(audit.EventTLSExpiryCritical): 1,
	}

	got := map[string]int{}
	for _, e := range events {
		got[e]++
	}

	for name, want := range wantContains {
		if got[name] != want {
			t.Errorf("event %q seen %d times, want %d (all events: %v)", name, got[name], want, events)
		}
	}
}

func TestExpiry_RecoversWhenCertRenewed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "test.crt")
	keyPath := filepath.Join(dir, "test.key")
	auditPath := filepath.Join(dir, "audit.jsonl")

	// Start with a cert expiring in 5 days (already in critical band).
	writeCertAt(t, certPath, keyPath, "renewable", time.Now().Add(5*24*time.Hour))

	auditLog, err := audit.New(auditPath)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	t.Cleanup(func() { _ = auditLog.Close() })

	loader, err := cert.New(certPath, keyPath, auditLog)
	if err != nil {
		t.Fatalf("loader: %v", err)
	}

	w := cert.NewExpiryWatcher(loader, auditLog, time.Now)

	// First check: fires critical.
	w.CheckNow()

	// Operator renews to 365d — reload swaps the cert in.
	writeCertAt(t, certPath, keyPath, "renewed", time.Now().Add(365*24*time.Hour))
	if err := loader.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// Re-check: no expiry event should fire (level dropped to fresh).
	w.CheckNow()

	if err := auditLog.Close(); err != nil {
		t.Fatalf("audit close: %v", err)
	}

	events := audittest.ReadEventNames(t, auditPath)

	got := map[string]int{}
	for _, e := range events {
		got[e]++
	}

	if got[string(audit.EventTLSExpiryCritical)] != 1 {
		t.Errorf("expected exactly 1 expiry_critical event before reload, got %d (events: %v)",
			got[string(audit.EventTLSExpiryCritical)], events)
	}

	if got[string(audit.EventTLSExpiryWarn)] != 0 {
		t.Errorf("expected 0 warn events post-renewal, got %d", got[string(audit.EventTLSExpiryWarn)])
	}
}

