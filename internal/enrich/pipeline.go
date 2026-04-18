// Package enrich is where the 6-stage enrichment pipeline lives once the next
// slice ships: identity → bloom → geo → ua → bot → channel. The order is
// load-bearing (PLAN.md:166, doc 24 §GAP 1) and pinned by an integration test
// that the next plan iteration will add.
//
// v1 slice: STUB. Single-pass copy from RawEvent → EnrichedEvent with no
// enrichment. Just enough to compile + exercise the WAL → ClickHouse path.
package enrich

import (
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
)

// Stub is the temporary pipeline. Replace with the real 6-worker version
// before closing Phase 1.
type Stub struct {
	hasher *identity.HasherStub
}

// NewStub returns a pipeline that does the minimum to keep the contract.
func NewStub(hasher *identity.HasherStub) *Stub {
	return &Stub{hasher: hasher}
}

// ProcessEvent translates a RawEvent + server-side context into the
// EnrichedEvent the consumer expects. The IP field is intentionally dropped
// here and never reaches the consumer or ClickHouse — Privacy Rule 1
// forbids persisting raw IPs. When the real GeoIP step lands, it will
// receive the IP, look up the country/province, and return only those
// derived values.
func (s *Stub) ProcessEvent(raw *ingest.RawEvent) ingest.EnrichedEvent {
	visitorHash := s.hasher.VisitorHash(raw.SiteID, raw.IP, raw.UserAgent)

	eventType := raw.EventType
	if eventType == "" {
		eventType = "pageview"
	}

	eventName := raw.EventName
	if eventName == "" {
		eventName = eventType
	}

	var isGoal uint8
	if raw.IsGoal {
		isGoal = 1
	}

	keys, vals := flattenProps(raw.Props)

	return ingest.EnrichedEvent{
		SiteID:      raw.SiteID,
		TSUTC:       raw.TSUTC,
		UserIDHash:  raw.UserIDHash,
		CookieID:    raw.CookieID,
		VisitorHash: visitorHash,

		Hostname: raw.Hostname,
		Pathname: raw.Pathname,
		Title:    raw.Title,

		Referrer:    raw.Referrer,
		UTMSource:   raw.UTMSource,
		UTMMedium:   raw.UTMMedium,
		UTMCampaign: raw.UTMCampaign,
		UTMContent:  raw.UTMContent,
		UTMTerm:     raw.UTMTerm,

		// Audience fields populated by the real pipeline; defaults match schema.
		CountryCode: "IR",

		ViewportWidth: raw.ViewportWidth,
		EventType:     eventType,
		EventName:     eventName,
		EventValue:    uint64(raw.EventValue),
		IsGoal:        isGoal,
		PropKeys:      keys,
		PropVals:      vals,
		UserSegment:   raw.UserSegment,
	}
}

func flattenProps(props map[string]string) (keys, vals []string) {
	if len(props) == 0 {
		return nil, nil
	}

	keys = make([]string, 0, len(props))
	vals = make([]string, 0, len(props))

	for k, v := range props {
		keys = append(keys, k)
		vals = append(vals, v)
	}

	return keys, vals
}
