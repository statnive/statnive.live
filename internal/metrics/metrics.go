// Package metrics exposes Prometheus-text counters for the /api/event
// ingest funnel. Roll-your-own atomic counters + text exposition format —
// avoids pulling in github.com/prometheus/client_golang and its 10+
// transitive deps. The Phase 7e observability stack (deploy/observability)
// scrapes plain text fine.
//
// Three counters expose the funnel:
//
//	statnive_event_received_total                 — every entrance to /api/event
//	statnive_event_accepted_total{site_id="..."}  — events durable in WAL
//	statnive_event_dropped_total{reason="..."}    — events rejected before WAL
//
// received - accepted - sum(dropped by reason) = events still in flight when
// the counter was sampled (typically ≤ batch size).
//
// Reasons are stable lowercase-with-underscores tokens so log aggregators
// can group/filter without escaping. The full set is enumerated below as
// constants — drop a new reason by adding a constant + emitting it.
package metrics

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

// DropReason enumerates every reject path in /api/event. Constants stay
// in sync with the audit.EventName surface (internal/audit/events.go) and
// the load-gate-harness skill semgrep rules.
const (
	ReasonPrefetchHeader  = "prefetch_header"
	ReasonUALength        = "ua_length"
	ReasonUANonASCII      = "ua_non_ascii"
	ReasonUAAsIP          = "ua_as_ip"
	ReasonUAAsUUID        = "ua_as_uuid"
	ReasonHostnameUnknown = "hostname_unknown"
	ReasonPayloadTooLarge = "payload_too_large"
	ReasonJSONInvalid     = "json_invalid"
	ReasonEmptyBody       = "empty_body"
	ReasonRateLimited     = "rate_limited"
	ReasonWALBackpressure = "wal_backpressure"
	ReasonBurstDropped    = "burst_dropped"
	ReasonBotDropped      = "bot_dropped"       // statnive.sites.track_bots=0
	ReasonOptedOut        = "opted_out"         // visitor exercised GDPR Art. 21 (Stage 2 /api/privacy/opt-out)
	ReasonEventNotAllowed = "event_not_allowed" // event_name not on site's event_allowlist (Stage 3 CNIL cap)
	ReasonWALSyncError    = "wal_sync_error"
	ReasonBadEventName    = "bad_event_name" // event_name fails ingest charset/length (XSS-hardening gate)
	ReasonBadEventType    = "bad_event_type" // event_type fails ingest charset/length
	ReasonBadPropKey      = "bad_prop_key"   // a key in props fails charset/length
	ReasonTooManyProps    = "too_many_props" // props map exceeds the per-event entry cap
)

// Registry holds atomic counters surfaced via /metrics.
type Registry struct {
	received atomic.Uint64
	accepted sync.Map // site_id (string) -> *atomic.Uint64
	dropped  sync.Map // reason (string) -> *atomic.Uint64
}

// New returns a Registry with all counters at zero.
func New() *Registry { return &Registry{} }

// IncReceived bumps the funnel-top counter. Called once per /api/event
// invocation, before any reject gate.
func (r *Registry) IncReceived() {
	if r == nil {
		return
	}

	r.received.Add(1)
}

// IncAccepted bumps the per-site accepted counter. Called once per event
// after AppendAndWait succeeds (event is durable in WAL).
func (r *Registry) IncAccepted(siteID uint32) {
	if r == nil {
		return
	}

	key := strconv.FormatUint(uint64(siteID), 10)
	r.bumpMap(&r.accepted, key)
}

// IncDropped bumps the per-reason drop counter. Reason MUST be one of the
// Reason* constants above — emitting an unknown reason still works, but
// breaks the load-gate-harness semgrep contract.
func (r *Registry) IncDropped(reason string) {
	if r == nil {
		return
	}

	r.bumpMap(&r.dropped, reason)
}

// AcceptedFor returns the current accepted counter for siteID. Test helper.
func (r *Registry) AcceptedFor(siteID uint32) uint64 {
	if r == nil {
		return 0
	}

	key := strconv.FormatUint(uint64(siteID), 10)

	return loadCounter(&r.accepted, key)
}

// DroppedFor returns the current dropped counter for reason. Test helper.
func (r *Registry) DroppedFor(reason string) uint64 {
	if r == nil {
		return 0
	}

	return loadCounter(&r.dropped, reason)
}

// Received returns the current received counter. Test helper.
func (r *Registry) Received() uint64 {
	if r == nil {
		return 0
	}

	return r.received.Load()
}

func (r *Registry) bumpMap(m *sync.Map, key string) {
	c := loadOrCreateCounter(m, key)
	c.Add(1)
}

// loadOrCreateCounter returns the existing counter at key or initializes a
// new one. Type assertion is safe — only this package writes the map and
// only with *atomic.Uint64 values.
func loadOrCreateCounter(m *sync.Map, key string) *atomic.Uint64 {
	v, _ := m.LoadOrStore(key, new(atomic.Uint64))

	c, ok := v.(*atomic.Uint64)
	if !ok {
		// Programmer error — the map was polluted by another writer.
		return new(atomic.Uint64)
	}

	return c
}

func loadCounter(m *sync.Map, key string) uint64 {
	v, ok := m.Load(key)
	if !ok {
		return 0
	}

	c, typeOK := v.(*atomic.Uint64)
	if !typeOK {
		return 0
	}

	return c.Load()
}

// WriteText writes the registry contents in Prometheus text exposition
// format. Suitable for direct response from /metrics.
func (r *Registry) WriteText(w io.Writer) error {
	if r == nil {
		return nil
	}

	if _, err := fmt.Fprintln(w, "# HELP statnive_event_received_total Events received at /api/event before any reject gate."); err != nil {
		return err
	}

	if _, err := fmt.Fprintln(w, "# TYPE statnive_event_received_total counter"); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "statnive_event_received_total %d\n", r.received.Load()); err != nil {
		return err
	}

	if err := writeLabelled(w, &r.accepted, "statnive_event_accepted_total", "site_id",
		"Events accepted and durable in WAL, partitioned by site_id."); err != nil {
		return err
	}

	if err := writeLabelled(w, &r.dropped, "statnive_event_dropped_total", "reason",
		"Events dropped before reaching WAL, partitioned by drop reason."); err != nil {
		return err
	}

	return nil
}

func writeLabelled(w io.Writer, m *sync.Map, name, label, help string) error {
	if _, err := fmt.Fprintf(w, "# HELP %s %s\n", name, help); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "# TYPE %s counter\n", name); err != nil {
		return err
	}

	keys := make([]string, 0, 16)

	m.Range(func(k, _ any) bool {
		s, ok := k.(string)
		if ok {
			keys = append(keys, s)
		}

		return true
	})

	sort.Strings(keys)

	for _, k := range keys {
		count := loadCounter(m, k)
		if _, err := fmt.Fprintf(w, "%s{%s=%q} %d\n", name, label, k, count); err != nil {
			return err
		}
	}

	return nil
}
