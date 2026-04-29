package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/audit/audittest"
	"github.com/statnive/statnive.live/internal/ratelimit"
)

func TestMiddleware_AllowsUnderLimit(t *testing.T) {
	t.Parallel()

	mw, err := ratelimit.Middleware(10, time.Minute, ratelimit.Config{})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}

	handler := mw(okHandler())

	for i := range 5 {
		req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.1")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want 200", i, rr.Code)
		}
	}
}

func TestMiddleware_Blocks429AfterLimit(t *testing.T) {
	t.Parallel()

	mw, err := ratelimit.Middleware(3, time.Minute, ratelimit.Config{})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}

	handler := mw(okHandler())

	statuses := make(map[int]int)

	for range 10 {
		req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.5")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		statuses[rr.Code]++
	}

	if statuses[http.StatusOK] == 0 {
		t.Errorf("expected some 200s, got %v", statuses)
	}

	if statuses[http.StatusTooManyRequests] == 0 {
		t.Errorf("expected some 429s after limit exceeded, got %v", statuses)
	}
}

func TestMiddleware_KeyByXForwardedFor(t *testing.T) {
	t.Parallel()

	mw, err := ratelimit.Middleware(2, time.Minute, ratelimit.Config{})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}

	handler := mw(okHandler())

	// Burn the budget for client A.
	for range 3 {
		req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
		req.Header.Set("X-Forwarded-For", "1.1.1.1")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	// Client B should still get through.
	req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
	req.Header.Set("X-Forwarded-For", "2.2.2.2")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("client B status = %d, want 200 (budget should be per-IP)", rr.Code)
	}
}

func TestMiddleware_EmitsAuditEventOn429(t *testing.T) {
	t.Parallel()

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")

	auditLog, err := audit.New(auditPath)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}

	t.Cleanup(func() { _ = auditLog.Close() })

	mw, err := ratelimit.Middleware(1, time.Minute, ratelimit.Config{Audit: auditLog})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}

	handler := mw(okHandler())

	for range 5 {
		req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
		req.Header.Set("X-Forwarded-For", "192.0.2.42")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	if err := auditLog.Close(); err != nil {
		t.Fatalf("audit close: %v", err)
	}

	events := audittest.ReadEventNames(t, auditPath)

	got := 0

	for _, e := range events {
		if e == string(audit.EventRateLimited) {
			got++
		}
	}

	if got == 0 {
		t.Errorf("expected at least one ratelimit.exceeded event, got %v", events)
	}
}

func TestMiddleware_RejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	if _, err := ratelimit.Middleware(0, time.Minute, ratelimit.Config{}); err == nil {
		t.Error("expected error for requestsPerWindow=0")
	}

	if _, err := ratelimit.Middleware(10, 0, ratelimit.Config{}); err == nil {
		t.Error("expected error for window=0")
	}
}

func TestMiddleware_AllowlistBypassesLimit(t *testing.T) {
	t.Parallel()

	mw, err := ratelimit.Middleware(2, time.Minute, ratelimit.Config{
		AllowlistedIPs: []string{"203.0.113.99"},
	})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}

	handler := mw(okHandler())

	statuses := make(map[int]int)

	for range 10 {
		req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.99")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		statuses[rr.Code]++
	}

	if statuses[http.StatusOK] != 10 {
		t.Errorf("allowlisted IP got 200=%d, want 10. statuses=%v", statuses[http.StatusOK], statuses)
	}

	if statuses[http.StatusTooManyRequests] != 0 {
		t.Errorf("allowlisted IP got %d 429s, want 0", statuses[http.StatusTooManyRequests])
	}

	// Sanity: a non-allowlisted IP still hits the limiter.
	for range 5 {
		req := httptest.NewRequest(http.MethodPost, "/api/event", nil)
		req.Header.Set("X-Forwarded-For", "203.0.113.5")

		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		statuses[rr.Code]++
	}

	if statuses[http.StatusTooManyRequests] == 0 {
		t.Errorf("non-allowlisted IP should still hit 429, got statuses=%v", statuses)
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}
