package cert

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/statnive/statnive.live/internal/alerts"
	"github.com/statnive/statnive.live/internal/audit"
)

// Expiry-watcher thresholds. Crossing into the warning band emits one
// audit event; crossing further into critical emits another. We never
// re-emit at the same level — operators don't need a wake-up every six
// hours for the same fact.
const (
	expiryWarnThreshold     = 30 * 24 * time.Hour
	expiryCriticalThreshold = 7 * 24 * time.Hour
	expiryCheckInterval     = 6 * time.Hour
)

// expiryLevel tracks which audit event we last emitted so the watcher
// fires once per band-crossing, not on every check.
type expiryLevel int

const (
	expiryFresh expiryLevel = iota
	expiryWarn
	expiryCritical
)

// ExpiryWatcher checks the loader's leaf certificate NotAfter on a slow
// ticker and emits audit events as the cert approaches expiry. No
// outbound notifications — this is file-sink only per air-gap rules.
// Phase 8: the same events also fan out to the alerts.Sink so the
// Notice UI (6-polish-5) can surface cert-expiry as a persistent
// warning.
type ExpiryWatcher struct {
	loader *Loader
	audit  *audit.Logger
	alerts *alerts.Sink
	clock  func() time.Time

	mu        sync.Mutex
	lastLevel expiryLevel
}

// NewExpiryWatcher constructs the watcher. clock is injectable for tests;
// callers in production should pass time.Now (or nil to default to it).
// alertsSink may be nil (no-op) — the audit fan-out remains load-bearing.
func NewExpiryWatcher(loader *Loader, auditLog *audit.Logger, alertsSink *alerts.Sink, clock func() time.Time) *ExpiryWatcher {
	if clock == nil {
		clock = time.Now
	}

	return &ExpiryWatcher{
		loader:    loader,
		audit:     auditLog,
		alerts:    alertsSink,
		clock:     clock,
		lastLevel: expiryFresh,
	}
}

// Run blocks until ctx is canceled. Performs an immediate check on entry
// so a binary booted with an already-expiring cert raises the alert at
// startup, not 6 hours later.
func (w *ExpiryWatcher) Run(ctx context.Context) error {
	w.check()

	t := time.NewTicker(expiryCheckInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			w.check()
		}
	}
}

// CheckNow performs a single check synchronously. Exposed for tests so
// they don't have to wait on the ticker.
func (w *ExpiryWatcher) CheckNow() { w.check() }

func (w *ExpiryWatcher) check() {
	notAfter := w.loader.NotAfter()
	if notAfter.IsZero() {
		return
	}

	remaining := notAfter.Sub(w.clock())
	level := classify(remaining)

	w.mu.Lock()
	defer w.mu.Unlock()

	if level == w.lastLevel {
		return
	}

	switch level {
	case expiryCritical:
		w.emit(audit.EventTLSExpiryCritical, notAfter, remaining)
		w.alert(alerts.NameTLSExpiryCritical, alerts.SeverityCritical, false, notAfter, remaining)
	case expiryWarn:
		w.emit(audit.EventTLSExpiryWarn, notAfter, remaining)
		w.alert(alerts.NameTLSExpiryWarn, alerts.SeverityWarn, false, notAfter, remaining)
	case expiryFresh:
		// Cert was renewed past 30d — recover silently in the audit
		// log (tls.cert_loaded serves as the confirmation). Emit an
		// explicit `resolved=true` alert so the Notice UI can
		// auto-dismiss the warn/critical banner it was showing.
		if w.lastLevel == expiryWarn || w.lastLevel == expiryCritical {
			w.alert(alerts.NameTLSExpiryWarn, alerts.SeverityInfo, true, notAfter, remaining)
		}
	}

	w.lastLevel = level
}

func (w *ExpiryWatcher) alert(name string, sev alerts.Severity, resolved bool, notAfter time.Time, remaining time.Duration) {
	if w.alerts == nil {
		return
	}

	w.alerts.Emit(context.Background(), name, sev, resolved,
		slog.Time("not_after", notAfter),
		slog.String("remaining", remaining.Round(time.Hour).String()),
		slog.String("subject", w.loader.Subject()),
	)
}

func (w *ExpiryWatcher) emit(name audit.EventName, notAfter time.Time, remaining time.Duration) {
	if w.audit == nil {
		return
	}

	w.audit.Event(context.Background(), name,
		slog.Time("not_after", notAfter),
		slog.String("remaining", remaining.Round(time.Hour).String()),
		slog.String("subject", w.loader.Subject()),
	)
}

func classify(remaining time.Duration) expiryLevel {
	switch {
	case remaining <= expiryCriticalThreshold:
		return expiryCritical
	case remaining <= expiryWarnThreshold:
		return expiryWarn
	default:
		return expiryFresh
	}
}
