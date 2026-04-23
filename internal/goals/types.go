// Package goals owns the admin-configured goal definitions that the
// ingest pipeline consults to set `is_goal=1` + `event_value` on
// matching events. Source of truth is the `statnive.goals` table
// (migration 005); the pipeline never touches ClickHouse on the hot
// path — it reads from an in-memory Snapshot refreshed on SIGHUP + on
// every admin mutation.
//
// v1 scope: match_type = "event_name_equals" only (doc 17 row 17 +
// doc 18). v1.1 extends with path_equals + path_prefix; v2 adds
// path_regex. Enum8 is extensible — no migration needed for the
// string-matching family.
package goals

import "github.com/google/uuid"

// MatchType mirrors the sessions table Enum8 exactly. Keep string
// form aligned with the ClickHouse enum so a round-trip via
// toString(match_type) scans cleanly.
type MatchType string

// Only one match type in v1. v1.1 adds MatchTypePathEquals +
// MatchTypePathPrefix; v2 adds MatchTypePathRegex.
const (
	MatchTypeEventNameEquals MatchType = "event_name_equals"
)

// Valid reports whether m is one of the known match types.
func (m MatchType) Valid() bool {
	// v1 has exactly one match type; kept as an equality check so v1.1
	// (path_equals / path_prefix) can extend this to a switch cleanly.
	return m == MatchTypeEventNameEquals
}

// MaxPatternLen is the admin-UI + decoder cap on `pattern` length.
// Keeps the hot-path string-equality linear scan sub-microsecond and
// rejects adversarial-large inputs (defense-in-depth for when v2
// adds regex compilation — keeping the cap in place from v1 avoids
// an implicit surface area change).
const MaxPatternLen = 128

// Goal is one admin-configured goal. Raw UUIDs; the pattern is a
// cleartext event-name string (NOT visitor PII — operator input).
type Goal struct {
	GoalID     uuid.UUID
	SiteID     uint32
	Name       string
	MatchType  MatchType
	Pattern    string
	ValueRials uint64
	Enabled    bool
	CreatedAt  int64 // unix seconds, UTC
	UpdatedAt  int64
}
