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

// Admin CRUD events. Emitted by internal/admin/* handlers + the
// goals-snapshot reload path.
//
// Field-shape invariants (Phase 3c):
//   - actor_user_id, target_user_id, target_goal_id — RAW UUID strings
//     via uuid.UUID.String(). These are operator/metadata surrogates
//     (doc 24 §admin_users), NOT visitor identity. Privacy Rule 4 bans
//     raw visitor user_id from audit sinks; operator admin UUIDs are a
//     distinct concept and never match the pii_leak_test.go regex set.
//   - email_hash — SHA-256 of lowercase-trimmed email (matches Phase 2b
//     EventLogin* shape); raw email never in audit.
//   - goal_pattern_hash — SHA-256 of the cleartext pattern. Hashed
//     defensively in case a malicious admin stuffs visitor data into
//     the pattern field; admin UI still sees the raw pattern.
//   - Never log old/new password hash or raw session tokens.
const (
	EventAdminUserCreated  EventName = "admin.user.created"
	EventAdminUserUpdated  EventName = "admin.user.updated"
	EventAdminUserDisabled EventName = "admin.user.disabled"
	EventAdminUserEnabled  EventName = "admin.user.enabled"
	EventAdminUserPwReset  EventName = "admin.user.password_reset"
	EventAdminGoalCreated  EventName = "admin.goal.created"
	EventAdminGoalUpdated  EventName = "admin.goal.updated"
	EventAdminGoalDisabled EventName = "admin.goal.disabled"
	EventAdminGoalFired    EventName = "admin.goal.fired"    // ingest-side, per event match
	EventAdminGoalRejected EventName = "admin.goal.rejected" // write-time validation fail

	EventAdminSiteCreated  EventName = "admin.site.created"
	EventAdminSiteUpdated  EventName = "admin.site.updated"
	EventAdminSiteRejected EventName = "admin.site.rejected" // write-time validation fail

	EventGoalsReloadOK     EventName = "goals.reload_succeeded"
	EventGoalsReloadFailed EventName = "goals.reload_failed"
)

// GeoIP hot-reload events. Emitted by internal/enrich/geoip.go on
// SIGHUP; the pre-swap validation probe (doc 28 §Gap 1) means a failed
// reload leaves the previous DB active — emit the reload_failed event
// but keep serving lookups from the old handle.
const (
	EventGeoIPReloaded     EventName = "geoip.reloaded"
	EventGeoIPReloadFailed EventName = "geoip.reload_failed"
)

// Alert event names live in internal/alerts (not here) — alerts are a
// separate JSONL sink with a different schema than audit. The audit
// package owns completed-action events; alerts own ops-should-know-now
// conditions. See internal/alerts/sink.go.
