package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

type fakeJurisdictionNoticeStore struct {
	dismissed    atomic.Bool
	getErr       error
	setErr       error
	dismissedFor uuid.UUID
}

func (f *fakeJurisdictionNoticeStore) IsJurisdictionNoticeDismissed(
	_ context.Context, _ uuid.UUID,
) (bool, error) {
	if f.getErr != nil {
		return false, f.getErr
	}

	return f.dismissed.Load(), nil
}

func (f *fakeJurisdictionNoticeStore) DismissJurisdictionNotice(
	_ context.Context, id uuid.UUID,
) error {
	if f.setErr != nil {
		return f.setErr
	}

	f.dismissed.Store(true)
	f.dismissedFor = id

	return nil
}

func TestJurisdictionNotice_GetReportsFalseInitially(t *testing.T) {
	t.Parallel()

	store := &fakeJurisdictionNoticeStore{}
	h := NewJurisdictionNotice(Deps{JurisdictionNotice: store})

	req := adminRequest(t, http.MethodGet, "/api/admin/jurisdiction-notice", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body jurisdictionNoticeResponse

	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	if body.Dismissed {
		t.Errorf("fresh user should see dismissed=false")
	}
}

func TestJurisdictionNotice_DismissFlipsFlag(t *testing.T) {
	t.Parallel()

	store := &fakeJurisdictionNoticeStore{}
	h := NewJurisdictionNotice(Deps{JurisdictionNotice: store})

	req := adminRequest(t, http.MethodPost, "/api/admin/jurisdiction-notice/dismiss", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	h.Dismiss(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	if !store.dismissed.Load() {
		t.Errorf("store.dismissed should be true after Dismiss")
	}

	// Subsequent GET reflects the change.
	req2 := adminRequest(t, http.MethodGet, "/api/admin/jurisdiction-notice", "", newTestAdmin(), nil)
	rec2 := httptest.NewRecorder()
	h.Get(rec2, req2)

	var body jurisdictionNoticeResponse

	_ = json.Unmarshal(rec2.Body.Bytes(), &body)

	if !body.Dismissed {
		t.Errorf("GET after Dismiss should report dismissed=true")
	}
}

func TestJurisdictionNotice_NilStoreGetIsGracefulFalse(t *testing.T) {
	t.Parallel()

	h := NewJurisdictionNotice(Deps{JurisdictionNotice: nil})

	req := adminRequest(t, http.MethodGet, "/api/admin/jurisdiction-notice", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (nil store should still respond)", rec.Code)
	}

	var body jurisdictionNoticeResponse

	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	if body.Dismissed {
		t.Errorf("nil-store fallback should be dismissed=false")
	}
}

func TestJurisdictionNotice_NilStoreDismissIs503(t *testing.T) {
	t.Parallel()

	h := NewJurisdictionNotice(Deps{JurisdictionNotice: nil})

	req := adminRequest(t, http.MethodPost, "/api/admin/jurisdiction-notice/dismiss", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	h.Dismiss(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestJurisdictionNotice_StoreErrorReturns500(t *testing.T) {
	t.Parallel()

	store := &fakeJurisdictionNoticeStore{setErr: errors.New("ch down")}
	h := NewJurisdictionNotice(Deps{JurisdictionNotice: store})

	req := adminRequest(t, http.MethodPost, "/api/admin/jurisdiction-notice/dismiss", "", newTestAdmin(), nil)
	rec := httptest.NewRecorder()
	h.Dismiss(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
