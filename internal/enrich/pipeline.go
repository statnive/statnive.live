// Package enrich runs the 6-stage event enrichment pipeline:
// identity → bloom → geo → ua → bot → channel. The order is load-bearing
// (CLAUDE.md Architecture Rule 6) and pinned by integration tests.
//
// The pipeline owns its in-channel and out-channel. Lifecycle:
//
//	p := NewPipeline(deps)        // constructor only — no goroutines yet
//	go func() { _ = p.Run(ctx) }()
//	p.Enqueue(ctx, raw)           // returns false on backpressure
//	p.Stop()                      // closes in-channel, waits for workers
//	for ev := range p.Out() {...} // out-channel drains naturally
package enrich

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
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
	Audit   *audit.Logger      // optional — nil silences burst-drop audit lines
	Logger  *slog.Logger
}

// Pipeline is the worker pool. Workers all run the same 6-stage path; we
// fan out across events, never across stages, so the order invariant
// holds without coordination between workers.
type Pipeline struct {
	deps    Deps
	in      chan *ingest.RawEvent
	out     chan ingest.EnrichedEvent
	workers int

	burstDropped atomic.Uint64

	wg        sync.WaitGroup
	closeOnce sync.Once
}

// BurstDropped returns the count of events the per-visitor cap has
// rejected since boot. Surfaced via /healthz so operators can detect
// scraper-network attacks or runaway trackers.
func (p *Pipeline) BurstDropped() uint64 { return p.burstDropped.Load() }

// NewPipeline allocates the channels but does NOT start workers. Call Run.
func NewPipeline(deps Deps) *Pipeline {
	if deps.Salt == nil || deps.Bloom == nil || deps.GeoIP == nil ||
		deps.UA == nil || deps.Bot == nil || deps.Channel == nil ||
		deps.Logger == nil {
		panic("enrich: NewPipeline called with nil dep")
	}

	workers := runtime.GOMAXPROCS(0) - 2
	if workers < 2 {
		workers = 2
	}

	return &Pipeline{
		deps:    deps,
		in:      make(chan *ingest.RawEvent, 256),
		out:     make(chan ingest.EnrichedEvent, 4096),
		workers: workers,
	}
}

// Out is the read-only side of the enriched-event channel the consumer
// drains. The channel closes after Stop drains the workers.
func (p *Pipeline) Out() <-chan ingest.EnrichedEvent { return p.out }

// Enqueue tries to hand a raw event to a worker. Returns false when the
// in-channel is full (backpressure) or the context is canceled. Never
// blocks the caller — handlers must stay responsive.
func (p *Pipeline) Enqueue(ctx context.Context, raw *ingest.RawEvent) bool {
	select {
	case p.in <- raw:
		return true
	case <-ctx.Done():
		return false
	default:
		// Saturated. Caller (handler) returns 503.
		return false
	}
}

// Run starts the worker pool and blocks until ctx is canceled. After
// cancellation it drains in-flight events and closes the out-channel so
// downstream consumers see a clean EOF.
func (p *Pipeline) Run(ctx context.Context) error {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(ctx, i)
	}

	p.deps.Logger.Info("enrich pipeline started", "workers", p.workers)

	<-ctx.Done()
	p.Stop()

	return nil
}

// Stop closes the in-channel and waits for workers to drain. Idempotent.
func (p *Pipeline) Stop() {
	p.closeOnce.Do(func() {
		close(p.in)
		p.wg.Wait()
		close(p.out)
	})
}

func (p *Pipeline) worker(ctx context.Context, id int) {
	defer p.wg.Done()

	for {
		select {
		case <-ctx.Done():
			// Drain whatever is already buffered so events that made it
			// past the handler don't get lost.
			for raw := range p.in {
				if ev, ok := p.processEvent(raw); ok {
					p.deliver(ctx, ev)
				}
			}

			return
		case raw, channelOpen := <-p.in:
			if !channelOpen {
				return
			}

			if ev, ok := p.processEvent(raw); ok {
				p.deliver(ctx, ev)
			}
		}
	}
}

func (p *Pipeline) deliver(ctx context.Context, ev ingest.EnrichedEvent) {
	select {
	case p.out <- ev:
	case <-ctx.Done():
		// Best-effort drop; the consumer is shutting down.
		p.deps.Logger.Warn("enrich pipeline drop on shutdown")
	}
}

// processEvent is the locked 6-stage path. Order MUST stay
// identity → burst → bloom → geo → ua → bot → channel (CLAUDE.md
// Rule 6 + Phase 7a burst guard insertion). Returns (event, ok=true)
// for events that should land in events_raw, and (zero, ok=false) for
// events the burst guard has rejected — the caller skips deliver().
func (p *Pipeline) processEvent(raw *ingest.RawEvent) (ingest.EnrichedEvent, bool) {
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

	return ingest.EnrichedEvent{
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
	}, true
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
	for i := 0; i < 4; i++ {
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
