package auth

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// fakeStore is a minimal in-memory Store for unit tests. Not shipped to
// production; all the "real" store logic lives in ClickHouseStore.
type fakeStore struct {
	mu         sync.Mutex
	usersByID  map[uuid.UUID]*User
	passwords  map[uuid.UUID]string
	byEmail    map[string]uuid.UUID // site_id|email → user_id
	sessions   map[[32]byte]*Session
	revoked    map[[32]byte]bool
	nilUser    bool // when true LookupSession returns (nil, nil) — PLAN.md §53 fault
	lookupErr  error
	getByIDErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		usersByID: make(map[uuid.UUID]*User),
		passwords: make(map[uuid.UUID]string),
		byEmail:   make(map[string]uuid.UUID),
		sessions:  make(map[[32]byte]*Session),
		revoked:   make(map[[32]byte]bool),
	}
}

func (f *fakeStore) keyEmail(siteID uint32, email string) string {
	return itoaSite(siteID) + "|" + normalizeEmail(email)
}

func itoaSite(s uint32) string {
	// Avoid importing strconv just for this helper — cheap hex.
	const hex = "0123456789abcdef"

	var buf [8]byte

	for i := 7; i >= 0; i-- {
		buf[i] = hex[s&0xf]
		s >>= 4
	}

	return string(buf[:])
}

func (f *fakeStore) CreateUser(_ context.Context, u *User, pwHash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	u.Email = normalizeEmail(u.Email)
	if u.UserID == uuid.Nil {
		u.UserID = uuid.New()
	}

	if u.CreatedAt == 0 {
		u.CreatedAt = time.Now().Unix()
	}

	u.UpdatedAt = time.Now().Unix()

	key := f.keyEmail(u.SiteID, u.Email)
	if _, exists := f.byEmail[key]; exists {
		return ErrAlreadyExists
	}

	f.usersByID[u.UserID] = u
	f.passwords[u.UserID] = pwHash
	f.byEmail[key] = u.UserID

	return nil
}

func (f *fakeStore) GetUserByEmail(
	_ context.Context, siteID uint32, email string,
) (*User, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	email = normalizeEmail(email)

	// Linear scan — test data is tiny.
	for id, u := range f.usersByID {
		if u.SiteID == siteID && u.Email == email {
			pw := f.passwords[id]

			return u, pw, nil
		}
	}

	return nil, "", ErrNotFound
}

func (f *fakeStore) GetUserByID(_ context.Context, id uuid.UUID) (*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}

	u, ok := f.usersByID[id]
	if !ok {
		return nil, ErrNotFound
	}

	return u, nil
}

func (f *fakeStore) ListUsers(_ context.Context, siteID uint32) ([]*User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]*User, 0)

	for _, u := range f.usersByID {
		if u.SiteID == siteID {
			out = append(out, u)
		}
	}

	return out, nil
}

func (f *fakeStore) UpdateUserPassword(_ context.Context, id uuid.UUID, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.usersByID[id]; !ok {
		return ErrNotFound
	}

	f.passwords[id] = hash
	f.usersByID[id].UpdatedAt = time.Now().Unix()

	return nil
}

func (f *fakeStore) DisableUser(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.usersByID[id]
	if !ok {
		return ErrNotFound
	}

	u.Disabled = true
	u.UpdatedAt = time.Now().Unix()

	return nil
}

func (f *fakeStore) ChangeRole(_ context.Context, id uuid.UUID, role Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	u, ok := f.usersByID[id]
	if !ok {
		return ErrNotFound
	}

	u.Role = role
	u.UpdatedAt = time.Now().Unix()

	return nil
}

func (f *fakeStore) CreateSession(
	_ context.Context, s *Session, _ [16]byte, _ string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cp := *s
	f.sessions[s.IDHash] = &cp

	return nil
}

func (f *fakeStore) LookupSession(_ context.Context, hash [32]byte) (*SessionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.nilUser {
		return nil, nil // PLAN.md §53 fault: (nil, nil) — callers MUST reject
	}

	if f.lookupErr != nil {
		return nil, f.lookupErr
	}

	s, ok := f.sessions[hash]
	if !ok {
		return nil, ErrNotFound
	}

	if f.revoked[hash] {
		return nil, ErrRevoked
	}

	if s.ExpiresAt > 0 && s.ExpiresAt <= time.Now().Unix() {
		return nil, ErrExpired
	}

	u, ok := f.usersByID[s.UserID]
	if !ok {
		return nil, ErrNotFound
	}

	if u.Disabled {
		return nil, ErrDisabled
	}

	return &SessionInfo{User: u, Session: s}, nil
}

func (f *fakeStore) RevokeSession(_ context.Context, hash [32]byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.sessions[hash]; !ok {
		return ErrNotFound
	}

	f.revoked[hash] = true

	return nil
}

func (f *fakeStore) RevokeAllUserSessions(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for h, s := range f.sessions {
		if s.UserID == id {
			f.revoked[h] = true
		}
	}

	return nil
}

var _ Store = (*fakeStore)(nil)
var _ = errors.New // keep errors import
