package auth

import (
	"strings"
	"sync"
	"time"
)

// LockoutConfig tunes the per-email account lockout. Defends against
// distributed credential stuffing where IP-rate-limit alone is bypassed
// by rotating source IPs. PLAN.md § Login rate-limit.
type LockoutConfig struct {
	// MaxFails in Decay → lockout.
	MaxFails int
	Decay    time.Duration // how long a failure contributes to the count
	Lockout  time.Duration // how long after MaxFails the email is blocked

	// MaxTracked bounds the in-memory map size so an attacker cycling
	// through fresh emails can't OOM the process.
	MaxTracked int

	// Now is overridable for tests.
	Now func() time.Time
}

// DefaultLockoutConfig returns the PLAN.md defaults: 10 fails in 15 m →
// 5 m lockout, 10 k emails tracked.
func DefaultLockoutConfig() LockoutConfig {
	return LockoutConfig{
		MaxFails:   10,
		Decay:      15 * time.Minute,
		Lockout:    5 * time.Minute,
		MaxTracked: 10_000,
		Now:        time.Now,
	}
}

// Lockout is the in-memory per-email fail tracker. Safe for concurrent
// use; bounded in size by MaxTracked (LRU-style eviction).
type Lockout struct {
	cfg LockoutConfig

	mu      sync.Mutex
	entries map[string]*lockoutEntry
}

type lockoutEntry struct {
	firstFail   time.Time
	failCount   int
	lockedUntil time.Time
}

// NewLockout constructs a tracker with cfg. cfg.Now defaults to
// time.Now.
func NewLockout(cfg LockoutConfig) *Lockout {
	if cfg.MaxFails <= 0 {
		cfg.MaxFails = 10
	}

	if cfg.Decay <= 0 {
		cfg.Decay = 15 * time.Minute
	}

	if cfg.Lockout <= 0 {
		cfg.Lockout = 5 * time.Minute
	}

	if cfg.MaxTracked <= 0 {
		cfg.MaxTracked = 10_000
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &Lockout{
		cfg:     cfg,
		entries: make(map[string]*lockoutEntry),
	}
}

// Check returns nil if the email is currently allowed to attempt
// login, or ErrLockedOut if a lockout window is active.
func (l *Lockout) Check(email string) error {
	key := lockoutKey(email)
	now := l.cfg.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.entries[key]
	if !ok {
		return nil
	}

	if now.Before(e.lockedUntil) {
		return ErrLockedOut
	}

	// Window expired — reset.
	if now.Sub(e.firstFail) > l.cfg.Decay {
		delete(l.entries, key)
	}

	return nil
}

// Record registers one failed login against email. If the cumulative
// count in the decay window reaches MaxFails, starts a lockout. Callers
// should Check first, then Record on failure.
func (l *Lockout) Record(email string) {
	key := lockoutKey(email)
	now := l.cfg.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// Evict one victim if at capacity. Simple "pick any expired"
	// strategy — since we're constant-time-bound per op, this is cheaper
	// than full LRU on what is already a defensive side-channel.
	if len(l.entries) >= l.cfg.MaxTracked {
		for k, v := range l.entries {
			if now.After(v.lockedUntil) && now.Sub(v.firstFail) > l.cfg.Decay {
				delete(l.entries, k)

				break
			}
		}
		// If no entry was evictable (all fresh), drop the new record —
		// preferable to unbounded growth.
		if len(l.entries) >= l.cfg.MaxTracked {
			return
		}
	}

	e, ok := l.entries[key]
	if !ok || now.Sub(e.firstFail) > l.cfg.Decay {
		e = &lockoutEntry{firstFail: now, failCount: 0}
		l.entries[key] = e
	}

	e.failCount++

	if e.failCount >= l.cfg.MaxFails {
		e.lockedUntil = now.Add(l.cfg.Lockout)
	}
}

// Clear removes any tracking for email. Called on successful login so
// a legitimate user isn't locked out by their own previous fat-fingering.
func (l *Lockout) Clear(email string) {
	key := lockoutKey(email)

	l.mu.Lock()
	delete(l.entries, key)
	l.mu.Unlock()
}

func lockoutKey(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
