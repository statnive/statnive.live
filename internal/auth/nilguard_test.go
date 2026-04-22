package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestNilGuard_StoreReturnsNilNil — PLAN.md Verification §53 / CVE-2024-
// 10924. If Store.LookupSession ever returns (nil, nil), every upstream
// call site must reject the request. This test fault-injects that exact
// shape and asserts each layer rejects.
func TestNilGuard_StoreReturnsNilNil(t *testing.T) {
	fs := newFakeStore()
	fs.nilUser = true

	// 1. The session middleware must not attach a user from (nil, nil).
	deps, _, _ := newTestDeps(t)
	deps.Store = fs

	h := SessionMiddleware(deps)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()) != nil {
			t.Error("middleware attached *User despite (nil, nil) store result")
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview", nil)
	req.AddCookie(&http.Cookie{Name: deps.CookieCfg.Name, Value: "anything"})

	h.ServeHTTP(httptest.NewRecorder(), req)

	// 2. Direct CachedStore.LookupSession must surface an error, never
	// (nil, nil), so that callers pattern-matching on `if err != nil` +
	// dereferencing `info.User` don't hit a nil panic OR silently allow
	// an unauthenticated request through.
	cs := NewCachedStore(fs,time.Second)

	info, err := cs.LookupSession(context.Background(), [32]byte{1})
	if err == nil {
		t.Errorf("CachedStore let (nil, nil) through: info=%+v", info)
	}

	if info != nil {
		t.Errorf("CachedStore returned non-nil info on fault: %+v", info)
	}
}

// TestNilGuard_StoreSentinelErrors — the happy-path error cases
// (expired, revoked, disabled) must also return (nil, err), never
// return a dangling pointer.
func TestNilGuard_SentinelErrors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*fakeStore, uuid.UUID, [32]byte)
		wantErr error
	}{
		{
			"expired",
			func(fs *fakeStore, _ uuid.UUID, h [32]byte) {
				fs.sessions[h] = &Session{
					IDHash:    h,
					UserID:    uuid.New(),
					ExpiresAt: time.Now().Add(-time.Hour).Unix(),
				}
			},
			ErrExpired,
		},
		{
			"revoked",
			func(fs *fakeStore, uid uuid.UUID, h [32]byte) {
				fs.sessions[h] = &Session{
					IDHash:    h,
					UserID:    uid,
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
				}
				fs.usersByID[uid] = &User{UserID: uid, Role: RoleAdmin}
				fs.revoked[h] = true
			},
			ErrRevoked,
		},
		{
			"disabled",
			func(fs *fakeStore, uid uuid.UUID, h [32]byte) {
				fs.sessions[h] = &Session{
					IDHash:    h,
					UserID:    uid,
					ExpiresAt: time.Now().Add(time.Hour).Unix(),
				}
				fs.usersByID[uid] = &User{UserID: uid, Role: RoleAdmin, Disabled: true}
			},
			ErrDisabled,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeStore()
			uid := uuid.New()

			var h [32]byte

			h[0] = byte(len(tc.name)) // deterministic per-test hash

			tc.setup(fs, uid, h)

			info, err := fs.LookupSession(context.Background(), h)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}

			if info != nil {
				t.Errorf("info = %+v, want nil", info)
			}
		})
	}
}
