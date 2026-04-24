package ingest_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
)

// stubEnricher records Enrich calls without doing real 6-stage work.
// Lets the handler benchmark measure parsing + validation cost in
// isolation from the enrichment pipeline.
type stubEnricher struct{ calls atomic.Int64 }

func (s *stubEnricher) Enrich(raw *ingest.RawEvent) (ingest.EnrichedEvent, bool) {
	s.calls.Add(1)

	return ingest.EnrichedEvent{SiteID: raw.SiteID}, true
}

// stubWAL records AppendAndWait calls without fsyncing anything.
type stubWAL struct{ calls atomic.Int64 }

func (s *stubWAL) AppendAndWait(_ context.Context, _ ingest.EnrichedEvent) (uint64, error) {
	//nolint:gosec // atomic.Int64 monotonic counter; never negative in this stub.
	return uint64(s.calls.Add(1)), nil
}

// BenchmarkHandler_FullPath measures one POST /api/event roundtrip
// through fastReject + parse + Enrich + AppendAndWait (both stubbed).
// At 7K EPS the per-request budget is ~140 µs; this benchmark shows
// handler-internal overhead, excluding the real enrichment + fsync.
func BenchmarkHandler_FullPath(b *testing.B) {
	enr := &stubEnricher{}
	wal := &stubWAL{}
	logger := slog.New(slog.DiscardHandler)

	inner := ingest.NewHandler(ingest.HandlerConfig{
		Pipeline: enr,
		WAL:      wal,
		Sites:    ingest.StaticSiteResolver{SiteID: 1},
		Logger:   logger,
	})
	handler := ingest.FastRejectMiddleware(nil)(inner)

	body := `{"hostname":"bench.example.com","pathname":"/","event_type":"pageview","event_name":"pageview"}`

	b.ReportAllocs()

	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(body))
		req.Header.Set("User-Agent", "Mozilla/5.0 (BenchTest/1.0) BrowserLike")
		req.Header.Set("Content-Type", "text/plain")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}

// BenchmarkBurstGuard_Allow — per-event hot-path cost. Plan budget:
// <100 ns/call.
func BenchmarkBurstGuard_Allow(b *testing.B) {
	g := ingest.NewBurstGuard(500)
	now := time.Now()

	b.ReportAllocs()

	var i int

	for b.Loop() {
		var h [16]byte

		h[0] = byte(i)
		h[1] = byte(i >> 8)
		g.Allow(h, now)

		i++
	}
}
