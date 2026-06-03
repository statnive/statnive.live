// Package ingest defines the request/event types and the WAL-backed batch
// writer that fronts the ClickHouse store. The package boundary keeps the
// HTTP handler, pipeline workers, WAL, and consumer in one place because they
// all share the EnrichedEvent contract.
package ingest

import "time"

// RawEvent is the JSON payload posted by the tracker (sendBeacon, text/plain).
// Only fields a browser can supply belong here; server-side context is filled
// in by the handler before the event enters the pipeline.
type RawEvent struct {
	Hostname      string  `json:"hostname"`
	Pathname      string  `json:"pathname"`
	Title         string  `json:"title"`
	Referrer      string  `json:"referrer"`
	UTMSource     string  `json:"utm_source"`
	UTMMedium     string  `json:"utm_medium"`
	UTMCampaign   string  `json:"utm_campaign"`
	UTMContent    string  `json:"utm_content"`
	UTMTerm       string  `json:"utm_term"`
	ViewportWidth uint16  `json:"viewport_width"`
	EventType     string  `json:"event_type"`
	EventName     string  `json:"event_name"`
	EventValue    float64 `json:"event_value"`
	IsGoal        bool    `json:"is_goal"`
	UserSegment   string  `json:"user_segment"`
	// Props is the legacy hit-scope field accepted as a deprecated alias
	// for HitProps so customers running a pre-Phase-1 tracker against a
	// new server still attribute correctly. The handler merges Props
	// into HitProps when HitProps is empty; if both are populated, the
	// new HitProps wins. Will be removed one release after Phase 1
	// goes GA. Tracked under tracker bundle Lesson 21 follow-up.
	Props        map[string]string `json:"props,omitempty"`
	HitProps     map[string]string `json:"hit_props,omitempty"`
	SessionProps map[string]string `json:"session_props,omitempty"`
	UserProps    map[string]string `json:"user_props,omitempty"`

	// UserID is the raw, customer-supplied identifier sent by the tracker
	// via statniveLive.identify(). The handler hashes it via
	// identity.HexUserIDHash and clears this field before the pipeline
	// sees the event — Privacy Rule 4 (raw user_id never logged, never
	// written to disk, never echoed to the wire). Mirrors the IP
	// contract in Privacy Rule 1 (geoip.go discards IP after lookup).
	UserID string `json:"user_id"`

	// Phase 7e load-gate oracle fields. The test/perf/generator/ tool fills
	// these on every synthesized event so the oracle queries can compute
	// loss / dupes / ordering / latency. Production tracker traffic
	// always sends the zero values, which migration 018's typed-default
	// sentinels treat as "not part of a load gate run". Doc 29 §6.1.
	TestRunID        string `json:"test_run_id,omitempty"`
	TestGeneratorSeq uint64 `json:"test_generator_seq,omitempty"`
	GeneratorNodeID  uint16 `json:"generator_node_id,omitempty"`
	SendTSMilli      int64  `json:"send_ts_ms,omitempty"`

	// Server-side fields, never decoded from JSON.
	TSUTC      time.Time `json:"-"`
	UserIDHash string    `json:"-"` // populated by handler from UserID + master_secret.
	CookieID   string    `json:"-"`
	IP         string    `json:"-"` // dropped before EnrichedEvent — Privacy Rule 1.
	UserAgent  string    `json:"-"`
	SiteID     uint32    `json:"-"`
	// TrackBots mirrors statnive.sites.track_bots for this hostname.
	// When false the pipeline drops bot events instead of flagging
	// is_bot=1 (default true preserves the post-PR-#78 behavior). Set
	// by the handler from the per-site policy lookup.
	TrackBots bool `json:"-"`
	// TZ mirrors statnive.sites.tz for this hostname (IANA name, e.g.
	// "Europe/Berlin" or "Asia/Tehran"; empty string falls back to UTC
	// at the SaltManager layer). Used by enrich Stage 1 to derive
	// per-site daily salt rotation. Set by the handler from the same
	// LookupSitePolicy call that populates TrackBots.
	TZ string `json:"-"`
}

// EnrichedEvent is the row written to ClickHouse.
//
// INVARIANT: field order here MUST match the column order in
// storage.insertStmt and the events_raw schema. Reordering any field
// without updating both call sites silently corrupts every inserted row,
// because clickhouse-go appends positionally. No Nullable types
// (Architecture Rule 5) — use zero-values + DEFAULT.
type EnrichedEvent struct {
	SiteID       uint32    // tenancy
	TSUTC        time.Time // DateTime('UTC') (PLAN.md verification 25)
	UserIDHash   string
	CookieID     string
	VisitorHash  [16]byte // FixedString(16)
	Hostname     string
	Pathname     string
	Title        string
	Referrer     string
	ReferrerName string
	Channel      string
	UTMSource    string
	UTMMedium    string
	UTMCampaign  string
	UTMContent   string
	UTMTerm      string
	Province     string
	City         string
	CountryCode  string
	ISP          string
	Carrier      string
	OS           string
	Browser      string
	DeviceType   string

	ViewportWidth uint16
	EventType     string
	EventName     string
	EventValue    uint64 // currency-neutral integer; SPA labels via site.Currency (PLAN.md feature #18)
	IsGoal        uint8
	IsNew         uint8
	PropKeys      []string
	PropVals      []string
	UserSegment   string
	IsBot         uint8

	// Phase 7e load-gate oracle (migration 018). Zero values for
	// production tracker traffic land in the typed-default sentinels and
	// engage the sparse-serialization path — ~zero cost per CLAUDE.md
	// Architecture Rule 5 carve-out.
	TestRunID        string // UUID string; empty → toUUIDOrZero('') sentinel (all-zero UUID)
	TestGeneratorSeq uint64
	GeneratorNodeID  uint16
	SendTSMilli      int64 // millisecond Unix; 0 → DateTime64(3) sentinel

	// Phase 2 of segments (migration 020). Three Map columns carry
	// custom dimensions at hit / session / user scope. >90 % of events
	// ship empty maps in the steady state — ClickHouse's sparse
	// serialisation handles this at ~zero disk cost. The existing
	// prop_keys / prop_vals arrays stay populated from HitProps for one
	// release for backward-compat with consumers reading the legacy
	// columns. Filter / queries layer reads from these Map columns
	// directly via mapKeys() + indexed map[key] access.
	HitProps     map[string]string
	SessionProps map[string]string
	UserProps    map[string]string
}
