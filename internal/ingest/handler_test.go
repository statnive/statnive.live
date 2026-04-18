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

// fakePipeline records every Enqueue call so the test can assert the gate
// short-circuits before reaching the real worker pool.
type fakePipeline struct {
	calls atomic.Int32
}

func (f *fakePipeline) Enqueue(_ context.Context, _ *ingest.RawEvent) bool {
	f.calls.Add(1)

	return true
}

// Fast-reject gate must return 204 with zero pipeline work for prefetch
// requests and obvious bot user agents. The receiver channel must stay
// empty for every rejected case.
func TestHandlerFastRejectGate(t *testing.T) {
	t.Parallel()

	const validUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"

	cases := []struct {
		name       string
		method     string
		ua         string
		headers    map[string]string
		body       string
		wantStatus int
		wantPiped  bool // expect the event to land on the pipeline channel
	}{
		{
			name:       "x-purpose prefetch",
			method:     http.MethodPost,
			ua:         validUA,
			headers:    map[string]string{"X-Purpose": "prefetch"},
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "purpose prefetch",
			method:     http.MethodPost,
			ua:         validUA,
			headers:    map[string]string{"Purpose": "prefetch"},
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "x-moz prefetch",
			method:     http.MethodPost,
			ua:         validUA,
			headers:    map[string]string{"X-Moz": "prefetch"},
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ua too short",
			method:     http.MethodPost,
			ua:         "tiny",
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ua too long",
			method:     http.MethodPost,
			ua:         strings.Repeat("a", 600),
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			// IPv4 UAs are short enough to be rejected by the length floor,
			// so we use IPv6 to exercise the IP-as-UA branch specifically.
			name:       "ua is ipv6",
			method:     http.MethodPost,
			ua:         "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ua is uuid",
			method:     http.MethodPost,
			ua:         "550e8400-e29b-41d4-a716-446655440000",
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ua non-ascii",
			method:     http.MethodPost,
			ua:         "Mozilla/5.0 (هذا غير ASCII; long enough to pass length)",
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "valid ua + body",
			method:     http.MethodPost,
			ua:         validUA,
			body:       validBody(),
			wantStatus: http.StatusAccepted,
			wantPiped:  true,
		},
		{
			name:       "wrong method",
			method:     http.MethodGet,
			ua:         validUA,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakePipeline{}

			// Production wires fast-reject as a chi middleware in front of
			// NewHandler. The handler test composes them by hand so the
			// 10-case fast-reject table still gates the right behavior.
			inner := ingest.NewHandler(ingest.HandlerConfig{
				Pipeline: fake,
				Sites:    ingest.StaticSiteResolver{SiteID: 1},
				Now:      func() time.Time { return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC) },
				Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			handler := ingest.FastRejectMiddleware(nil)(inner)

			req := httptest.NewRequestWithContext(context.Background(), tc.method, "/api/event", strings.NewReader(tc.body))
			req.Header.Set("User-Agent", tc.ua)
			req.Header.Set("Content-Type", "text/plain")

			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if got := rr.Code; got != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", got, tc.wantStatus, rr.Body.String())
			}

			if tc.wantPiped {
				if calls := fake.calls.Load(); calls != 1 {
					t.Fatalf("expected 1 Enqueue call, got %d", calls)
				}
			} else {
				if calls := fake.calls.Load(); calls != 0 {
					t.Errorf("rejected request leaked into pipeline (Enqueue called %d times)", calls)
				}
			}
		})
	}
}

func validBody() string {
	return `{"hostname":"example.com","pathname":"/","event_type":"pageview","event_name":"pageview"}`
}
