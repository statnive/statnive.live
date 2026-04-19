package ingest_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
)

// stubPipeline records Enqueue calls without doing any real work.
// Lets the handler benchmark measure parsing + validation cost in
// isolation from the enrichment pipeline.
type stubPipeline struct{ calls atomic.Int64 }

func (s *stubPipeline) Enqueue(_ context.Context, _ *ingest.RawEvent) bool {
	s.calls.Add(1)

	return true
}

// BenchmarkHandler_FullPath measures one POST /api/event roundtrip
// through fastReject + parse + Enqueue with no pipeline work behind it.
// At 7K EPS the per-request budget is ~140 µs — this benchmark shows
// how much headroom the handler actually has.
func BenchmarkHandler_FullPath(b *testing.B) {
	fake := &stubPipeline{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	inner := ingest.NewHandler(ingest.HandlerConfig{
		Pipeline: fake,
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
