package metrics_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/metrics"
)

func TestRegistry_Received(t *testing.T) {
	t.Parallel()

	r := metrics.New()
	for range 5 {
		r.IncReceived()
	}

	if got := r.Received(); got != 5 {
		t.Fatalf("Received() = %d, want 5", got)
	}
}

func TestRegistry_AcceptedPerSite(t *testing.T) {
	t.Parallel()

	r := metrics.New()

	r.IncAccepted(1)
	r.IncAccepted(1)
	r.IncAccepted(2)

	if got := r.AcceptedFor(1); got != 2 {
		t.Errorf("AcceptedFor(1) = %d, want 2", got)
	}

	if got := r.AcceptedFor(2); got != 1 {
		t.Errorf("AcceptedFor(2) = %d, want 1", got)
	}

	if got := r.AcceptedFor(99); got != 0 {
		t.Errorf("AcceptedFor(99) = %d, want 0", got)
	}
}

func TestRegistry_DroppedPerReason(t *testing.T) {
	t.Parallel()

	r := metrics.New()

	r.IncDropped(metrics.ReasonHostnameUnknown)
	r.IncDropped(metrics.ReasonHostnameUnknown)
	r.IncDropped(metrics.ReasonUALength)
	r.IncDropped(metrics.ReasonBadEventName)
	r.IncDropped(metrics.ReasonBadEventType)
	r.IncDropped(metrics.ReasonBadPropKey)
	r.IncDropped(metrics.ReasonTooManyProps)

	if got := r.DroppedFor(metrics.ReasonHostnameUnknown); got != 2 {
		t.Errorf("DroppedFor(hostname_unknown) = %d, want 2", got)
	}

	if got := r.DroppedFor(metrics.ReasonUALength); got != 1 {
		t.Errorf("DroppedFor(ua_length) = %d, want 1", got)
	}

	for _, reason := range []string{
		metrics.ReasonBadEventName,
		metrics.ReasonBadEventType,
		metrics.ReasonBadPropKey,
		metrics.ReasonTooManyProps,
	} {
		if got := r.DroppedFor(reason); got != 1 {
			t.Errorf("DroppedFor(%s) = %d, want 1", reason, got)
		}
	}
}

func TestRegistry_WriteText(t *testing.T) {
	t.Parallel()

	r := metrics.New()
	r.IncReceived()
	r.IncReceived()
	r.IncAccepted(1)
	r.IncDropped(metrics.ReasonHostnameUnknown)

	var buf bytes.Buffer
	if err := r.WriteText(&buf); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	got := buf.String()

	wantSubstrings := []string{
		"# TYPE statnive_event_received_total counter",
		"statnive_event_received_total 2",
		"statnive_event_accepted_total{site_id=\"1\"} 1",
		"statnive_event_dropped_total{reason=\"hostname_unknown\"} 1",
	}

	for _, s := range wantSubstrings {
		if !strings.Contains(got, s) {
			t.Errorf("WriteText missing %q\n--- got:\n%s", s, got)
		}
	}
}

func TestRegistry_NilSafe(t *testing.T) {
	t.Parallel()

	var r *metrics.Registry

	r.IncReceived()
	r.IncAccepted(1)
	r.IncDropped(metrics.ReasonRateLimited)

	if got := r.Received(); got != 0 {
		t.Errorf("nil Received() = %d, want 0", got)
	}

	if got := r.AcceptedFor(1); got != 0 {
		t.Errorf("nil AcceptedFor(1) = %d, want 0", got)
	}

	if got := r.DroppedFor(metrics.ReasonRateLimited); got != 0 {
		t.Errorf("nil DroppedFor(rate_limited) = %d, want 0", got)
	}

	var buf bytes.Buffer
	if err := r.WriteText(&buf); err != nil {
		t.Errorf("nil WriteText: %v", err)
	}
}

func TestHandler_DisabledWhenTokenEmpty(t *testing.T) {
	t.Parallel()

	r := metrics.New()
	h := metrics.Handler(r, "")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestHandler_RejectsMissingToken(t *testing.T) {
	t.Parallel()

	r := metrics.New()
	h := metrics.Handler(r, "secret")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestHandler_RejectsWrongToken(t *testing.T) {
	t.Parallel()

	r := metrics.New()
	h := metrics.Handler(r, "secret")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer wrong")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestHandler_AcceptsCorrectToken(t *testing.T) {
	t.Parallel()

	r := metrics.New()
	r.IncReceived()

	h := metrics.Handler(r, "secret")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret")

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "statnive_event_received_total 1") {
		t.Errorf("body missing received counter:\n%s", rr.Body.String())
	}
}
