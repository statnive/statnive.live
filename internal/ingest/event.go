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
	Hostname      string            `json:"hostname"`
	Pathname      string            `json:"pathname"`
	Title         string            `json:"title"`
	Referrer      string            `json:"referrer"`
	UTMSource     string            `json:"utm_source"`
	UTMMedium     string            `json:"utm_medium"`
	UTMCampaign   string            `json:"utm_campaign"`
	UTMContent    string            `json:"utm_content"`
	UTMTerm       string            `json:"utm_term"`
	ViewportWidth uint16            `json:"viewport_width"`
	EventType     string            `json:"event_type"`
	EventName     string            `json:"event_name"`
	EventValue    float64           `json:"event_value"`
	IsGoal        bool              `json:"is_goal"`
	UserSegment   string            `json:"user_segment"`
	Props         map[string]string `json:"props"`

	// Server-side fields, never decoded from JSON.
	TSUTC      time.Time `json:"-"`
	UserIDHash string    `json:"-"`
	CookieID   string    `json:"-"`
	IP         string    `json:"-"` // dropped before EnrichedEvent — Privacy Rule 1.
	UserAgent  string    `json:"-"`
	SiteID     uint32    `json:"-"`
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
	EventValue    uint64 // rials, integer (PLAN.md feature #18)
	IsGoal        uint8
	IsNew         uint8
	PropKeys      []string
	PropVals      []string
	UserSegment   string
	IsBot         uint8
}
