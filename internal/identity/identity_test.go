package identity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/identity"
)

const testSecret = "test-master-secret-32-bytes-long!!"

func TestVisitorHash_Deterministic(t *testing.T) {
	t.Parallel()

	const (
		ip   = "203.0.113.7"
		ua   = "Mozilla/5.0 ExampleBrowser"
		salt = "deadbeef"
	)

	a := identity.VisitorHash(ip, ua, salt)
	b := identity.VisitorHash(ip, ua, salt)

	if a != b {
		t.Errorf("same input → different hashes: %x vs %x", a, b)
	}
}

func TestVisitorHash_DifferentSaltsProduceDifferentHashes(t *testing.T) {
	t.Parallel()

	a := identity.VisitorHash("1.2.3.4", "ua", "salt-monday")
	b := identity.VisitorHash("1.2.3.4", "ua", "salt-tuesday")

	if a == b {
		t.Errorf("different salts → same hash: %x", a)
	}
}

func TestVisitorHash_DifferentInputsProduceDifferentHashes(t *testing.T) {
	t.Parallel()

	salt := "abcd"
	a := identity.VisitorHash("1.2.3.4", "ua", salt)
	b := identity.VisitorHash("1.2.3.5", "ua", salt)

	if a == b {
		t.Errorf("different IP → same hash: %x", a)
	}
}

func TestUserIDHash_TenantSeparation(t *testing.T) {
	t.Parallel()

	master := []byte(testSecret)

	a := identity.UserIDHash(master, 1, "user-42")
	b := identity.UserIDHash(master, 2, "user-42")

	if a == b {
		t.Errorf("same user_id across tenants → same hash: %x", a)
	}
}

func TestHexUserIDHash_EmptyStaysEmpty(t *testing.T) {
	t.Parallel()

	if got := identity.HexUserIDHash([]byte(testSecret), 1, ""); got != "" {
		t.Errorf("empty user_id → %q, want \"\"", got)
	}
}

func TestSaltManager_SiteSeparation(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	a := m.CurrentSalt(1)
	b := m.CurrentSalt(2)

	if a == b {
		t.Errorf("same date, different sites → same salt: %s", a)
	}

	if len(a) != 64 {
		t.Errorf("salt length = %d, want 64 (hex of HMAC-SHA256)", len(a))
	}
}

func TestSaltManager_DayBoundary(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tz, _ := time.LoadLocation("Asia/Tehran")
	day1 := time.Date(2026, 4, 17, 23, 58, 0, 0, tz)
	day2 := time.Date(2026, 4, 18, 0, 2, 0, 0, tz)

	m.SetClock(func() time.Time { return day1 })
	saltDay1 := m.CurrentSalt(1)

	m.SetClock(func() time.Time { return day2 })
	saltDay2 := m.CurrentSalt(1)
	prevSalt := m.PreviousSalt(1)

	if saltDay1 == saltDay2 {
		t.Error("salt did not rotate across IRST midnight")
	}

	if prevSalt != saltDay1 {
		t.Errorf("PreviousSalt at 00:02 IRST should equal yesterday's CurrentSalt; got %s want %s", prevSalt, saltDay1)
	}
}

func TestSaltManager_OverlapWindow(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	tz, _ := time.LoadLocation("Asia/Tehran")

	cases := []struct {
		name     string
		when     time.Time
		inWindow bool
	}{
		{"00:01 IRST", time.Date(2026, 4, 18, 0, 1, 0, 0, tz), true},
		{"00:04:59 IRST", time.Date(2026, 4, 18, 0, 4, 59, 0, tz), true},
		{"00:05 IRST", time.Date(2026, 4, 18, 0, 5, 0, 0, tz), false},
		{"23:59 IRST", time.Date(2026, 4, 18, 23, 59, 0, 0, tz), false},
	}

	//nolint:paralleltest // subtests share m.SetClock mutable state; must run sequentially
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m.SetClock(func() time.Time { return tc.when })

			if got := m.IsInOverlapWindow(); got != tc.inWindow {
				t.Errorf("IsInOverlapWindow() = %v, want %v", got, tc.inWindow)
			}
		})
	}
}

func TestSaltManager_Cache(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	a := m.CurrentSalt(1)
	b := m.CurrentSalt(1) // second call must hit cache

	if a != b {
		t.Errorf("cache miss: %s vs %s", a, b)
	}

	if !strings.HasPrefix(m.CurrentDateIRST(), "20") {
		t.Errorf("CurrentDateIRST = %q, want a YYYY-MM-DD", m.CurrentDateIRST())
	}
}

func TestSaltManager_EmptySecretRejected(t *testing.T) {
	t.Parallel()

	if _, err := identity.NewSaltManager(nil); err == nil {
		t.Error("expected error for nil secret")
	}

	if _, err := identity.NewSaltManager([]byte{}); err == nil {
		t.Error("expected error for empty secret")
	}
}
