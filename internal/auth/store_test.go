package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The CachedStore wrapper is the contract-enforcing layer: every
// privilege-changing call (UpdateUserPassword, DisableUser, ChangeRole)
// MUST cascade to RevokeAllUserSessions. These tests pin that
// contract against the fakeStore backing.

func TestCachedStore_CascadesOnPasswordChange(t *testing.T) {
	fs := newFakeStore()
	cs := NewCachedStore(fs,time.Second)
	ctx := context.Background()

	u := &User{UserID: uuid.New(), SiteID: 1, Email: "a@b.c", Role: RoleAdmin}
	if err := cs.CreateUser(ctx, u, "old-hash"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Create a session.
	p, _ := NewToken()

	sess := &Session{
		IDHash: p.Hash, UserID: u.UserID, SiteID: 1, Role: RoleAdmin,
		CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}

	if err := cs.CreateSession(ctx, sess, [16]byte{}, "ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if _, err := cs.LookupSession(ctx, p.Hash); err != nil {
		t.Fatalf("baseline lookup: %v", err)
	}

	// Password change should cascade.
	if err := cs.UpdateUserPassword(ctx, u.UserID, "new-hash"); err != nil {
		t.Fatalf("UpdateUserPassword: %v", err)
	}

	if _, err := cs.LookupSession(ctx, p.Hash); !errors.Is(err, ErrRevoked) && !errors.Is(err, ErrNotFound) {
		t.Errorf("session not revoked after password change: %v", err)
	}
}

func TestCachedStore_CascadesOnDisable(t *testing.T) {
	fs := newFakeStore()
	cs := NewCachedStore(fs,time.Second)
	ctx := context.Background()

	u := &User{UserID: uuid.New(), SiteID: 1, Email: "a@b.c", Role: RoleViewer}
	_ = cs.CreateUser(ctx, u, "hash")

	p, _ := NewToken()
	sess := &Session{
		IDHash: p.Hash, UserID: u.UserID, SiteID: 1, Role: RoleViewer,
		CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	_ = cs.CreateSession(ctx, sess, [16]byte{}, "ua")

	if err := cs.DisableUser(ctx, u.UserID); err != nil {
		t.Fatalf("DisableUser: %v", err)
	}

	info, err := cs.LookupSession(ctx, p.Hash)
	if err == nil {
		t.Fatalf("disabled user's session still valid (info=%+v)", info)
	}
}

func TestCachedStore_CascadesOnRoleChange(t *testing.T) {
	fs := newFakeStore()
	cs := NewCachedStore(fs,time.Second)
	ctx := context.Background()

	u := &User{UserID: uuid.New(), SiteID: 1, Email: "a@b.c", Role: RoleAdmin}
	_ = cs.CreateUser(ctx, u, "hash")

	p, _ := NewToken()
	sess := &Session{
		IDHash: p.Hash, UserID: u.UserID, SiteID: 1, Role: RoleAdmin,
		CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	_ = cs.CreateSession(ctx, sess, [16]byte{}, "ua")

	if err := cs.ChangeRole(ctx, u.UserID, RoleViewer); err != nil {
		t.Fatalf("ChangeRole: %v", err)
	}

	if _, err := cs.LookupSession(ctx, p.Hash); err == nil {
		t.Error("role change did not revoke existing session — CVE-2024-10924 shape")
	}
}

func TestCachedStore_Caches(t *testing.T) {
	fs := newFakeStore()

	now := time.Unix(1_700_000_000, 0).UTC()
	cs := NewCachedStore(fs,60*time.Second)
	cs.now = func() time.Time { return now }

	ctx := context.Background()

	u := &User{UserID: uuid.New(), SiteID: 1, Email: "a@b.c", Role: RoleAdmin}
	_ = cs.CreateUser(ctx, u, "hash")

	p, _ := NewToken()

	sess := &Session{IDHash: p.Hash, UserID: u.UserID, SiteID: 1, Role: RoleAdmin, CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix()}
	_ = cs.CreateSession(ctx, sess, [16]byte{}, "ua")

	// First lookup populates the cache.
	if _, err := cs.LookupSession(ctx, p.Hash); err != nil {
		t.Fatalf("first lookup: %v", err)
	}

	// Break the inner store — cache-hit path should not care.
	fs.getByIDErr = errors.New("boom")

	if _, err := cs.LookupSession(ctx, p.Hash); err != nil {
		t.Errorf("cache hit failed: %v", err)
	}

	// Advance past TTL — now the broken inner store is reached.
	now = now.Add(61 * time.Second)

	if _, err := cs.LookupSession(ctx, p.Hash); err == nil {
		t.Error("TTL expiry should re-hit inner store and fail on broken getByID")
	}
}
