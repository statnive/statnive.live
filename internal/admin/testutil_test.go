package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/goals"
)

// fakeAuthStore is a minimal in-memory auth.Store for handler tests.
// Cascade-revoke is not simulated — integration tests hit the real
// auth.CachedStore against docker-compose ClickHouse.
type fakeAuthStore struct {
	mu        sync.Mutex
	usersByID map[uuid.UUID]*auth.User
	passwords map[uuid.UUID]string
	sessions  map[[32]byte]*auth.Session
	revoked   map[[32]byte]bool
}

func newFakeAuthStore() *fakeAuthStore {
	return &fakeAuthStore{
		usersByID: make(map[uuid.UUID]*auth.User),
		passwords: make(map[uuid.UUID]string),
		sessions:  make(map[[32]byte]*auth.Session),
		revoked:   make(map[[32]byte]bool),
	}
}

func (f *fakeAuthStore) CreateUser(_ context.Context, u *auth.User, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if u.UserID == uuid.Nil {
		u.UserID = uuid.New()
	}

	u.CreatedAt = time.Now().Unix()
	u.UpdatedAt = u.CreatedAt
	cp := *u
	f.usersByID[u.UserID] = &cp
	f.passwords[u.UserID] = hash

	return nil
}

func (f *fakeAuthStore) GetUserByEmail(_ context.Context, siteID uint32, email string) (*auth.User, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id, u := range f.usersByID {
		if u.SiteID == siteID && u.Email == email {
			cp := *u

			return &cp, f.passwords[id], nil
		}
	}

	return nil, "", auth.ErrNotFound
}

func (f *fakeAuthStore) GetUserByID(_ context.Context, id uuid.UUID) (*auth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.usersByID[id]
	if !ok {
		return nil, auth.ErrNotFound
	}

	cp := *u

	return &cp, nil
}

func (f *fakeAuthStore) ListUsers(_ context.Context, siteID uint32) ([]*auth.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*auth.User, 0)

	for _, u := range f.usersByID {
		if u.SiteID == siteID {
			cp := *u
			out = append(out, &cp)
		}
	}

	return out, nil
}

func (f *fakeAuthStore) UpdateUserPassword(_ context.Context, id uuid.UUID, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.usersByID[id]; !ok {
		return auth.ErrNotFound
	}

	f.passwords[id] = hash
	f.usersByID[id].UpdatedAt = time.Now().Unix()

	return nil
}

func (f *fakeAuthStore) DisableUser(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.usersByID[id]
	if !ok {
		return auth.ErrNotFound
	}

	u.Disabled = true
	u.UpdatedAt = time.Now().Unix()

	return nil
}

func (f *fakeAuthStore) ChangeRole(_ context.Context, id uuid.UUID, role auth.Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.usersByID[id]
	if !ok {
		return auth.ErrNotFound
	}

	u.Role = role
	u.UpdatedAt = time.Now().Unix()

	return nil
}

func (f *fakeAuthStore) CreateSession(_ context.Context, s *auth.Session, _ [16]byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cp := *s
	f.sessions[s.IDHash] = &cp

	return nil
}

func (f *fakeAuthStore) LookupSession(_ context.Context, hash [32]byte) (*auth.SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s, ok := f.sessions[hash]
	if !ok {
		return nil, auth.ErrNotFound
	}

	u, ok := f.usersByID[s.UserID]
	if !ok {
		return nil, auth.ErrNotFound
	}

	return &auth.SessionInfo{User: u, Session: s}, nil
}

func (f *fakeAuthStore) RevokeSession(_ context.Context, hash [32]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.revoked[hash] = true

	return nil
}

func (f *fakeAuthStore) RevokeAllUserSessions(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for h, s := range f.sessions {
		if s.UserID == id {
			f.revoked[h] = true
		}
	}

	return nil
}

// fakeGoalsStore is a minimal goals.Store for handler tests.
type fakeGoalsStore struct {
	mu   sync.Mutex
	byID map[uuid.UUID]*goals.Goal
}

func newFakeGoalsStore() *fakeGoalsStore {
	return &fakeGoalsStore{byID: make(map[uuid.UUID]*goals.Goal)}
}

func (f *fakeGoalsStore) Create(_ context.Context, g *goals.Goal) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if g.SiteID == 0 || strings.TrimSpace(g.Name) == "" ||
		strings.TrimSpace(g.Pattern) == "" || !g.MatchType.Valid() ||
		len(g.Pattern) > goals.MaxPatternLen {
		return goals.ErrInvalidInput
	}

	if g.GoalID == uuid.Nil {
		g.GoalID = uuid.New()
	}

	now := time.Now().Unix()
	g.CreatedAt = now
	g.UpdatedAt = now
	cp := *g
	f.byID[g.GoalID] = &cp

	return nil
}

func (f *fakeGoalsStore) Get(_ context.Context, siteID uint32, id uuid.UUID) (*goals.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	g, ok := f.byID[id]
	if !ok || g.SiteID != siteID {
		return nil, goals.ErrNotFound
	}

	cp := *g

	return &cp, nil
}

func (f *fakeGoalsStore) List(_ context.Context, siteID uint32) ([]*goals.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*goals.Goal, 0)

	for _, g := range f.byID {
		if g.SiteID == siteID {
			cp := *g
			out = append(out, &cp)
		}
	}

	return out, nil
}

func (f *fakeGoalsStore) ListActive(_ context.Context) ([]*goals.Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*goals.Goal, 0)

	for _, g := range f.byID {
		if g.Enabled {
			cp := *g
			out = append(out, &cp)
		}
	}

	return out, nil
}

func (f *fakeGoalsStore) Update(_ context.Context, g *goals.Goal) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	existing, ok := f.byID[g.GoalID]
	if !ok || existing.SiteID != g.SiteID {
		return goals.ErrNotFound
	}

	if g.SiteID == 0 || strings.TrimSpace(g.Name) == "" ||
		strings.TrimSpace(g.Pattern) == "" || !g.MatchType.Valid() ||
		len(g.Pattern) > goals.MaxPatternLen {
		return goals.ErrInvalidInput
	}

	g.CreatedAt = existing.CreatedAt
	g.UpdatedAt = time.Now().Unix()
	cp := *g
	f.byID[g.GoalID] = &cp

	return nil
}

func (f *fakeGoalsStore) Disable(ctx context.Context, siteID uint32, id uuid.UUID) error {
	g, err := f.Get(ctx, siteID, id)
	if err != nil {
		return err
	}

	g.Enabled = false

	return f.Update(ctx, g)
}

var (
	_ auth.Store  = (*fakeAuthStore)(nil)
	_ goals.Store = (*fakeGoalsStore)(nil)
)

// adminRequest builds a request with an admin *User pre-attached to
// the context + optional chi URL params.
func adminRequest(t *testing.T, method, target, body string, admin *auth.User, pathParams map[string]string) *http.Request {
	t.Helper()

	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	ctx := auth.WithSession(r.Context(), admin, &auth.Session{SiteID: admin.SiteID, Role: admin.Role})

	if len(pathParams) > 0 {
		rctx := chi.NewRouteContext()
		for k, v := range pathParams {
			rctx.URLParams.Add(k, v)
		}

		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}

	return r.WithContext(ctx)
}
