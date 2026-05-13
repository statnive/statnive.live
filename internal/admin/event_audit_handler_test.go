package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
)

type fakeEventAuditStore struct {
	rows []storage.EventNameCount
	err  error

	calls atomic.Int32
}

func (f *fakeEventAuditStore) EventNameCardinality(
	_ context.Context, _ uint32, _, _ time.Time,
) ([]storage.EventNameCount, error) {
	f.calls.Add(1)

	if f.err != nil {
		return nil, f.err
	}

	return f.rows, nil
}

func TestEventAudit_OKWithTwoEventNames(t *testing.T) {
	t.Parallel()

	store := &fakeEventAuditStore{
		rows: []storage.EventNameCount{
			{Name: "pageview", Count: 1000},
			{Name: "click", Count: 250},
		},
	}

	handler := NewEventAudit(Deps{EventAudit: store})

	req := adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=42", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body eventAuditResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if body.Distinct != 2 {
		t.Errorf("distinct = %d, want 2", body.Distinct)
	}

	if body.CapStatus != eventAuditCapStatusOK {
		t.Errorf("cap_status = %q, want %q", body.CapStatus, eventAuditCapStatusOK)
	}

	if len(body.EventNames) != 2 || body.EventNames[0].Name != "pageview" {
		t.Errorf("unexpected event_names: %+v", body.EventNames)
	}
}

func TestEventAudit_OverWithFourEventNames(t *testing.T) {
	t.Parallel()

	store := &fakeEventAuditStore{
		rows: []storage.EventNameCount{
			{Name: "pageview", Count: 10000},
			{Name: "click", Count: 5000},
			{Name: "scroll", Count: 3000},
			{Name: "video_play", Count: 1500},
		},
	}

	handler := NewEventAudit(Deps{EventAudit: store})

	req := adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=42", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body eventAuditResponse

	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	if body.CapStatus != eventAuditCapStatusOver {
		t.Errorf("cap_status = %q, want %q (4 > %d CNIL cap)",
			body.CapStatus, eventAuditCapStatusOver, eventAuditCNILCap)
	}
}

func TestEventAudit_RejectsMissingSiteID(t *testing.T) {
	t.Parallel()

	handler := NewEventAudit(Deps{EventAudit: &fakeEventAuditStore{}})

	req := adminRequest(t, http.MethodGet, "/api/admin/event-audit", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestEventAudit_RejectsZeroSiteID(t *testing.T) {
	t.Parallel()

	handler := NewEventAudit(Deps{EventAudit: &fakeEventAuditStore{}})

	req := adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=0", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestEventAudit_CacheHitOnSecondCall(t *testing.T) {
	t.Parallel()

	store := &fakeEventAuditStore{
		rows: []storage.EventNameCount{{Name: "pageview", Count: 1}},
	}

	handler := NewEventAudit(Deps{EventAudit: store})

	for i := range 3 {
		req := adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=42", "", newTestAdmin(), nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: status = %d", i, rec.Code)
		}
	}

	if got := store.calls.Load(); got != 1 {
		t.Errorf("EventNameCardinality calls = %d, want 1 (cache must absorb 2nd + 3rd)", got)
	}
}

func TestEventAudit_CacheExpiresAfterTTL(t *testing.T) {
	t.Parallel()

	store := &fakeEventAuditStore{
		rows: []storage.EventNameCount{{Name: "pageview", Count: 1}},
	}

	handler := NewEventAudit(Deps{EventAudit: store})

	clock := time.Now()
	handler.now = func() time.Time { return clock }
	handler.cache.SetClock(func() time.Time { return clock })

	// First request → miss, query, cache.
	req := adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=42", "", newTestAdmin(), nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Advance past TTL.
	clock = clock.Add(eventAuditCacheTTL + time.Second)

	req = adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=42", "", newTestAdmin(), nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if got := store.calls.Load(); got != 2 {
		t.Errorf("EventNameCardinality calls = %d, want 2 after TTL expiry", got)
	}
}

func TestEventAudit_RejectsForbiddenSite(t *testing.T) {
	t.Parallel()

	handler := NewEventAudit(Deps{EventAudit: &fakeEventAuditStore{}})

	actor := newTestAdminWithSites(7) // only site 7
	req := adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=99", "", actor, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestEventAudit_StorageErrorReturns500(t *testing.T) {
	t.Parallel()

	store := &fakeEventAuditStore{err: errors.New("clickhouse down")}
	handler := NewEventAudit(Deps{EventAudit: store})

	req := adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=42", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestEventAudit_NilEventAuditReturns503(t *testing.T) {
	t.Parallel()

	handler := NewEventAudit(Deps{EventAudit: nil})

	req := adminRequest(t, http.MethodGet, "/api/admin/event-audit?site_id=42", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
