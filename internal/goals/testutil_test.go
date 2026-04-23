package goals

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// fakeStore is a minimal in-memory Store for unit tests. Never shipped.
// Not concurrent-safe beyond the mutex — tests don't exercise parallel
// writes against the same fakeStore instance.
type fakeStore struct {
	mu       sync.Mutex
	byID     map[uuid.UUID]*Goal
	listErr  error // injectable for Snapshot.Reload fail-closed tests
}

func newFakeStore() *fakeStore {
	return &fakeStore{byID: make(map[uuid.UUID]*Goal)}
}

func (f *fakeStore) Create(_ context.Context, g *Goal) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if g.GoalID == uuid.Nil {
		g.GoalID = uuid.New()
	}

	if g.CreatedAt == 0 {
		g.CreatedAt = time.Now().Unix()
	}

	g.UpdatedAt = time.Now().Unix()

	cp := *g
	f.byID[g.GoalID] = &cp

	return nil
}

func (f *fakeStore) Get(_ context.Context, siteID uint32, id uuid.UUID) (*Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	g, ok := f.byID[id]
	if !ok || g.SiteID != siteID {
		return nil, ErrNotFound
	}

	cp := *g

	return &cp, nil
}

func (f *fakeStore) List(_ context.Context, siteID uint32) ([]*Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*Goal, 0, 4)

	for _, g := range f.byID {
		if g.SiteID == siteID {
			cp := *g
			out = append(out, &cp)
		}
	}

	return out, nil
}

func (f *fakeStore) ListActive(_ context.Context) ([]*Goal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.listErr != nil {
		return nil, f.listErr
	}

	out := make([]*Goal, 0, len(f.byID))

	for _, g := range f.byID {
		if g.Enabled {
			cp := *g
			out = append(out, &cp)
		}
	}

	return out, nil
}

func (f *fakeStore) Update(_ context.Context, g *Goal) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	existing, ok := f.byID[g.GoalID]
	if !ok || existing.SiteID != g.SiteID {
		return ErrNotFound
	}

	g.CreatedAt = existing.CreatedAt
	g.UpdatedAt = time.Now().Unix()
	cp := *g
	f.byID[g.GoalID] = &cp

	return nil
}

func (f *fakeStore) Disable(ctx context.Context, siteID uint32, id uuid.UUID) error {
	g, err := f.Get(ctx, siteID, id)
	if err != nil {
		return err
	}

	g.Enabled = false

	return f.Update(ctx, g)
}

var _ Store = (*fakeStore)(nil)
var _ = errors.New // keep errors import reserved for future fault-injection
