// Package audit emits append-only JSONL audit events to a file sink. The
// file sink is the only sink in v1; remote sinks (syslog, Telegram) ship in
// v1.1 per CLAUDE.md / PLAN.md §Air-Gap.
//
// Events are typed by EventName so /simplify and reviewers can grep the full
// audit surface from one file. Adding a new event is a one-line addition
// here; emit sites pick up the constant by import.
package audit

// EventName is a stable identifier for an audited action. The string value
// is what lands in the JSONL "event" key — keep it dotted-lowercase so log
// aggregators can group / filter without escaping.
type EventName string

// TLS lifecycle events. Emitted by internal/tls/loader.go + expiry.go.
const (
	EventTLSCertLoaded     EventName = "tls.cert_loaded"
	EventTLSCertLoadFailed EventName = "tls.cert_load_failed"
	EventTLSReloadRequest  EventName = "tls.reload_requested"
	EventTLSReloadOK       EventName = "tls.reload_succeeded"
	EventTLSReloadFailed   EventName = "tls.reload_failed"
	EventTLSExpiryWarn     EventName = "tls.expiry_warning"  // <30d
	EventTLSExpiryCritical EventName = "tls.expiry_critical" // <7d
)

// Rate-limit events. Emitted by internal/ratelimit/ratelimit.go.
const (
	EventRateLimited EventName = "ratelimit.exceeded"
)

// Ingest events. Emitted by internal/ingest/handler.go.
const (
	EventHostnameUnknown EventName = "ingest.hostname_unknown"
	EventFastReject      EventName = "ingest.fast_reject"
)

// Audit-log internal events — emitted by the audit package itself when the
// file sink is re-opened (e.g., after a SIGHUP from logrotate).
const (
	EventReopenOK     EventName = "audit.reopen_succeeded"
	EventReopenFailed EventName = "audit.reopen_failed"
)

// Dashboard events. Emitted by internal/dashboard/* handlers + middleware.
const (
	EventDashboardOK             EventName = "dashboard.ok"
	EventDashboardBadRequest     EventName = "dashboard.bad_request"
	EventDashboardNotImplemented EventName = "dashboard.not_implemented"
	EventDashboardError          EventName = "dashboard.error"
	EventDashboardUnauthorized   EventName = "dashboard.unauthorized"
)
