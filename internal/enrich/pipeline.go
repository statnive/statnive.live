// Package enrich runs the 6-stage event enrichment pipeline:
// identity → bloom → geo → ua → bot → channel. The order is load-bearing
// (CLAUDE.md Architecture Rule 6) and pinned by integration tests.
//
// Phase 7b1b made the pipeline a synchronous library: callers run
// Enrich on their own goroutine, then immediately call WAL.AppendAndWait
// (which blocks until fsync). The previous worker-pool + in/out channels
// were removed because net/http already creates one goroutine per
// request — adding another fan-out only added queueing latency without
// parallelism benefit, and made the ack-after-fsync chain harder to reason
// about (handler had to wait for a worker to wait for a fsync).
package enrich

import (
	"context"
	"encoding/hex"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/metrics"
)

// Deps groups the runtime collaborators the pipeline reads from. All
// fields are required; nil deps are a programmer error and we panic in
// the constructor rather than silently producing wrong rows.
type Deps struct {
	Salt    *identity.SaltManager
	Bloom   *NewVisitorFilter
	GeoIP   GeoIPEnricher
	UA      *UAParser
	Bot     *BotDetector
	Channel *ChannelMapper
	Burst   *ingest.BurstGuard // optional — nil disables the per-visitor cap
	Goals   GoalMatcher        // optional — nil disables goal marking; prod injects *goals.Snapshot
	Audit   *audit.Logger      // optional — nil silences burst-drop audit lines
	Metrics *metrics.Registry  // optional — nil silences burst_dropped counter
	Logger  *slog.Logger
}

// GoalMatcher is the ingest-side contract the pipeline calls after
// channel attribution. internal/goals.Snapshot implements this; test
// paths inject goals.NopMatcher. Kept in enrich to avoid a direct
// import of internal/goals here (enrich would otherwise form a
// larger import surface; this way the dependency flows inward).
type GoalMatcher interface {
	Match(siteID uint32, eventName string) (goalID uuid.UUID, valueRials uint64, ok bool)
}

// Pipeline owns no goroutines and no channels. Construct once, call
// Enrich from any goroutine — internal state (bloom + burst-guard +
// channel-mapper atomic.Pointer + salt-cache mutex) handles concurrency.
type Pipeline struct {
	deps         Deps
	burstDropped atomic.Uint64
}

// BurstDropped returns the count of events the per-visitor cap has
// rejected since boot. Surfaced via /healthz so operators can detect
// scraper-network attacks or runaway trackers.
func (p *Pipeline) BurstDropped() uint64 { return p.burstDropped.Load() }

// NewPipeline validates required deps + returns a ready-to-use pipeline.
func NewPipeline(deps Deps) *Pipeline {
	if deps.Salt == nil || deps.Bloom == nil || deps.GeoIP == nil ||
		deps.UA == nil || deps.Bot == nil || deps.Channel == nil ||
		deps.Logger == nil {
		panic("enrich: NewPipeline called with nil dep")
	}

	return &Pipeline{deps: deps}
}

// Enrich runs the 6-stage pipeline inline. Returns ok=false when the
// burst guard rejects the event (caller drops it without sending to
// WAL). Order MUST stay identity → burst → bloom → geo → ua → bot →
// channel (CLAUDE.md Architecture Rule 6).
//
//nolint:gocyclo // PR D2 added the track_bots gate (extra branch + audit + metrics) which bumped cyclomatic complexity from 14 to 15. The 6-stage order is load-bearing per Architecture Rule 6 and pinned by integration tests; splitting stages into helpers would obscure the rule.
func (p *Pipeline) Enrich(raw *ingest.RawEvent) (ingest.EnrichedEvent, bool) {
	// Stage 1 — identity (BLAKE3 keyed by today's salt).
	saltToday := p.deps.Salt.CurrentSalt(raw.SiteID)
	saltPrev := p.deps.Salt.PreviousSalt(raw.SiteID)
	visitorHash := identity.VisitorHash(raw.IP, raw.UserAgent, saltToday)
	prevHash := identity.VisitorHash(raw.IP, raw.UserAgent, saltPrev)

	// Burst guard sits between identity and bloom: needs visitor_hash
	// to key by, must NOT pollute the bloom (which would mis-flag a
	// future legitimate event from the same visitor as returning).
	if p.deps.Burst != nil && !p.deps.Burst.Allow(visitorHash, time.Now()) {
		p.burstDropped.Add(1)

		if p.deps.Audit != nil {
			p.deps.Audit.Event(context.Background(), audit.EventBurstDropped,
				slog.Uint64("site_id", uint64(raw.SiteID)),
				slog.String("visitor_hash", encodeHashPrefix(visitorHash)),
			)
		}

		p.deps.Metrics.IncDropped(metrics.ReasonBurstDropped)

		return ingest.EnrichedEvent{}, false
	}

	// Stage 2 — bloom (cross-day grace).
	isNew := p.deps.Bloom.CheckAndMark(visitorHash, prevHash)

	// Stage 3 — GeoIP (IP discarded after this stage; never persisted).
	geo := p.deps.GeoIP.Lookup(raw.IP)

	// Stage 4 — UA parse.
	ua := p.deps.UA.Parse(raw.UserAgent)

	// Stage 5 — Bot detection (cheap-first inside).
	isBot, _ := p.deps.Bot.IsBot(raw.UserAgent, raw.IP)
	if !isBot && ua.IsBot {
		isBot = true
	}

	// Per-site track_bots gate (migration 006). Default true keeps
	// today's behavior — bots flow through with is_bot=1 so the
	// dashboard can surface them separately. Operators that don't
	// want bot rows in events_raw at all flip to false, and bots
	// drop here with metrics.ReasonBotDropped.
	if isBot && !raw.TrackBots {
		if p.deps.Audit != nil {
			p.deps.Audit.Event(context.Background(), audit.EventBotDropped,
				slog.Uint64("site_id", uint64(raw.SiteID)),
				slog.String("hostname", raw.Hostname),
			)
		}

		p.deps.Metrics.IncDropped(metrics.ReasonBotDropped)

		return ingest.EnrichedEvent{}, false
	}

	// Stage 6 — Channel attribution.
	channel := p.deps.Channel.Classify(raw.Referrer, raw.UTMSource, raw.UTMMedium, raw.UTMCampaign, "")

	eventType := raw.EventType
	if eventType == "" {
		eventType = "pageview"
	}

	eventName := raw.EventName
	if eventName == "" {
		eventName = eventType
	}

	keys, vals := flattenProps(raw.Props)

	ev := ingest.EnrichedEvent{
		SiteID:      raw.SiteID,
		TSUTC:       raw.TSUTC,
		UserIDHash:  raw.UserIDHash,
		CookieID:    raw.CookieID,
		VisitorHash: visitorHash,

		Hostname: raw.Hostname,
		Pathname: raw.Pathname,
		Title:    raw.Title,

		Referrer:     raw.Referrer,
		ReferrerName: channel.ReferrerName,
		Channel:      channel.Channel,
		UTMSource:    raw.UTMSource,
		UTMMedium:    raw.UTMMedium,
		UTMCampaign:  raw.UTMCampaign,
		UTMContent:   raw.UTMContent,
		UTMTerm:      raw.UTMTerm,

		Province:    geo.Province,
		City:        geo.City,
		CountryCode: geo.CountryCode,
		ISP:         geo.ISP,
		Carrier:     geo.Carrier,

		OS:         ua.OS,
		Browser:    ua.Browser,
		DeviceType: ua.Device,

		ViewportWidth: raw.ViewportWidth,
		EventType:     eventType,
		EventName:     eventName,
		EventValue:    uint64(raw.EventValue),
		IsGoal:        boolU8(raw.IsGoal),
		IsNew:         boolU8(isNew),
		PropKeys:      keys,
		PropVals:      vals,
		UserSegment:   raw.UserSegment,
		IsBot:         boolU8(isBot),
	}

	// Stage 7 — goal matching. Server-authoritative on event_value:
	// /api/event has no request signature (CLAUDE.md Security #3), so
	// a client-supplied event_value is untrusted. When a goal matches,
	// the admin-configured value_rials wins over whatever the tracker
	// sent. Matching only runs on non-bot, non-pageview shape events
	// (pageviews aren't goal candidates in v1 per doc 17 row 17).
	if p.deps.Goals != nil && ev.IsBot == 0 {
		if gID, val, matched := p.deps.Goals.Match(ev.SiteID, ev.EventName); matched {
			ev.IsGoal = 1
			ev.EventValue = val

			if p.deps.Audit != nil {
				p.deps.Audit.Event(context.Background(), audit.EventAdminGoalFired,
					slog.Uint64("site_id", uint64(ev.SiteID)),
					slog.String("target_goal_id", gID.String()),
					slog.String("visitor_hash", hex.EncodeToString(visitorHash[:])),
				)
			}
		}
	}

	return ev, true
}

func boolU8(b bool) uint8 {
	if b {
		return 1
	}

	return 0
}

// encodeHashPrefix returns the first 8 hex chars of a visitor hash for
// audit log identifiers. Full hash would leak more identity than needed
// for forensics; 32 bits of prefix is enough to distinguish bursts in
// the log without making the log a re-identification vector.
func encodeHashPrefix(h [16]byte) string {
	const hex = "0123456789abcdef"

	out := [8]byte{}
	for i := range 4 {
		out[i*2] = hex[h[i]>>4]
		out[i*2+1] = hex[h[i]&0x0f]
	}

	return string(out[:])
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
