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

// Ingest events. Emitted by internal/ingest/handler.go +
// internal/enrich/pipeline.go.
const (
	EventHostnameUnknown EventName = "ingest.hostname_unknown"
	EventFastReject      EventName = "ingest.fast_reject"
	EventBurstDropped    EventName = "ingest.burst_dropped"
)

// WAL durability events. Emitted by internal/ingest/wal.go +
// internal/ingest/walgroup.go + internal/ingest/consumer.go. Gated by
// .claude/skills/wal-durability-review/SKILL.md (Architecture Rule 4).
const (
	// EventWALSyncFailed is emitted immediately before os.Exit(1) when
	// fsync returns EIO/ENOSPC. Pre-4.13 Linux fsync marks failed pages
	// clean on EIO and forgets the error; retrying after a Sync error
	// silently loses data (LWN 752063, fsyncgate 2018).
	EventWALSyncFailed EventName = "wal.sync_failed"

	// EventWALCorruptSkipped fires once per gap range during replay
	// when tidwall's binary log returns ErrNotFound for indices that
	// should exist (typically a torn segment after SIGKILL).
	EventWALCorruptSkipped EventName = "wal.corrupt_skipped"

	// EventCHInsertFailed fires when ClickHouse rejects a batch.
	// Consumer does NOT ack the WAL when this fires — the batch stays
	// durable and will retry on the next flush cycle.
	EventCHInsertFailed EventName = "ch.insert_failed"
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

// Auth events. Emitted by internal/auth/* handlers + middleware.
// Raw password / raw session token / raw email MUST NOT appear as attrs
// on any of these — emitters hash email (SHA-256 of lowercase trim) and
// record session_id_hash (never the raw cookie value). Privacy Rule 4.
const (
	EventLoginSuccess   EventName = "auth.login.success"
	EventLoginFailed    EventName = "auth.login.failed"
	EventLogout         EventName = "auth.logout"
	EventSessionCreated EventName = "auth.session.created"
	EventSessionExpired EventName = "auth.session.expired"
	EventSessionRevoked EventName = "auth.session.revoked"
	EventRBACDenied     EventName = "auth.rbac.denied"
	EventAuthBootstrap  EventName = "auth.bootstrap"
)
