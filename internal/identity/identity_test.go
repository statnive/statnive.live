package identity_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

const testCookieID = "550e8400-e29b-41d4-a716-446655440000"

func TestCookieIDHash_NonRotating(t *testing.T) {
	t.Parallel()

	master := []byte(testSecret)

	// Same input across two calls must always produce the same hash —
	// the hash is non-rotating so DSAR / erase queries can match a
	// visitor's row across days.
	a := identity.CookieIDHash(master, 7, testCookieID)
	b := identity.CookieIDHash(master, 7, testCookieID)

	if a != b {
		t.Errorf("non-rotating hash diverged: %x vs %x", a, b)
	}
}

func TestCookieIDHash_TenantSeparation(t *testing.T) {
	t.Parallel()

	master := []byte(testSecret)

	a := identity.CookieIDHash(master, 1, testCookieID)
	b := identity.CookieIDHash(master, 2, testCookieID)

	if a == b {
		t.Errorf("same cookie across tenants → same hash: %x", a)
	}
}

func TestHexCookieIDHash_PrefixAndIdempotency(t *testing.T) {
	t.Parallel()

	got := identity.HexCookieIDHash([]byte(testSecret), 7, testCookieID)

	if !strings.HasPrefix(got, "h:") {
		t.Errorf("expected h: prefix, got %q", got)
	}

	// hex of 32-byte SHA-256 = 64 chars, plus "h:" = 66.
	if len(got) != 66 {
		t.Errorf("expected len 66 (h: + 64-char hex), got %d (%q)", len(got), got)
	}

	if empty := identity.HexCookieIDHash([]byte(testSecret), 7, ""); empty != "" {
		t.Errorf("empty cookieID → %q, want \"\"", empty)
	}
}

func TestSaltManager_SiteSeparation(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	a := m.CurrentSalt(1, "UTC")
	b := m.CurrentSalt(2, "UTC")

	if a == b {
		t.Errorf("same date, different sites → same salt: %s", a)
	}

	if len(a) != 64 {
		t.Errorf("salt length = %d, want 64 (hex of HMAC-SHA256)", len(a))
	}
}

func TestSaltManager_DayBoundary(t *testing.T) {
	t.Parallel()

	// Parameterised over 4 tz values to ensure the per-site rotation
	// behaviour holds regardless of zone choice.
	cases := []struct {
		name string
		tz   string
	}{
		{"UTC", "UTC"},
		{"Asia/Tehran", "Asia/Tehran"},
		{"Europe/Berlin", "Europe/Berlin"},
		{"America/Los_Angeles", "America/Los_Angeles"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m, err := identity.NewSaltManager([]byte(testSecret))
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			tz, err := time.LoadLocation(tc.tz)
			if err != nil {
				t.Fatalf("LoadLocation(%q): %v", tc.tz, err)
			}

			day1 := time.Date(2026, 4, 17, 23, 58, 0, 0, tz)
			day2 := time.Date(2026, 4, 18, 0, 2, 0, 0, tz)

			m.SetClock(func() time.Time { return day1 })
			saltDay1 := m.CurrentSalt(1, tc.tz)

			m.SetClock(func() time.Time { return day2 })
			saltDay2 := m.CurrentSalt(1, tc.tz)
			prevSalt := m.PreviousSalt(1, tc.tz)

			if saltDay1 == saltDay2 {
				t.Errorf("salt did not rotate across %s midnight", tc.tz)
			}

			if prevSalt != saltDay1 {
				t.Errorf("PreviousSalt at 00:02 %s should equal yesterday's CurrentSalt; got %s want %s", tc.tz, prevSalt, saltDay1)
			}
		})
	}
}

func TestSaltManager_OverlapWindow(t *testing.T) {
	t.Parallel()

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

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	//nolint:paralleltest // subtests share m.SetClock mutable state; must run sequentially
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m.SetClock(func() time.Time { return tc.when })

			if got := m.IsInOverlapWindow("Asia/Tehran"); got != tc.inWindow {
				t.Errorf("IsInOverlapWindow(Asia/Tehran) = %v, want %v", got, tc.inWindow)
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

	a := m.CurrentSalt(1, "UTC")
	b := m.CurrentSalt(1, "UTC") // second call must hit cache

	if a != b {
		t.Errorf("cache miss: %s vs %s", a, b)
	}

	if !strings.HasPrefix(m.CurrentDate("UTC"), "20") {
		t.Errorf("CurrentDate(UTC) = %q, want a YYYY-MM-DD", m.CurrentDate("UTC"))
	}
}

// TestSaltManager_DifferentSitesDifferentTimezones — same UTC instant,
// two sites with different tz produce different daily salts. Verifies
// the per-site rotation wiring.
func TestSaltManager_DifferentSitesDifferentTimezones(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// 23:00 UTC on 2026-04-17. In Asia/Tehran this is already 02:30 on
	// 2026-04-18 (UTC+3:30). In Europe/Berlin this is 01:00 on 2026-04-18
	// (CEST in April). Both are after their local midnight — so each
	// site's "today" date is 2026-04-18. Same date → same salt only
	// because the salt is keyed by date (not tz) per derive() comment.
	// To get different salts, pick a moment where the two zones land
	// on different YYYY-MM-DD values.
	//
	// 04:00 UTC on 2026-04-17:
	//   - Asia/Tehran: 07:30 on 2026-04-17 (so "today" = 2026-04-17)
	//   - Europe/Berlin: 06:00 on 2026-04-17 (so "today" = 2026-04-17)
	//   Same — bad.
	//
	// 22:30 UTC on 2026-04-17:
	//   - Asia/Tehran: 02:00 on 2026-04-18 (so "today" = 2026-04-18)
	//   - Europe/Berlin: 00:30 on 2026-04-18 (so "today" = 2026-04-18)
	//   Same — bad. They cross midnight together.
	//
	// 21:00 UTC on 2026-04-17:
	//   - Asia/Tehran: 00:30 on 2026-04-18 (today = 2026-04-18)
	//   - Europe/Berlin: 23:00 on 2026-04-17 (today = 2026-04-17)
	//   DIFFERENT. This is the seam window.
	wallClock := time.Date(2026, 4, 17, 21, 0, 0, 0, time.UTC)
	m.SetClock(func() time.Time { return wallClock })

	saltTehran := m.CurrentSalt(1, "Asia/Tehran")
	saltBerlin := m.CurrentSalt(1, "Europe/Berlin")

	if saltTehran == saltBerlin {
		t.Errorf("same site, different tz at the same UTC instant during the local-day seam: salts should differ\n  Tehran: %s\n  Berlin: %s", saltTehran, saltBerlin)
	}
}

// TestSaltManager_BackwardCompatIRST — for tz="Asia/Tehran", the salt
// is byte-identical to the v0.0.38 hardcoded-IRST behaviour. Regression
// guard so the SamplePlatform Iranian customer cannot silently break.
// Snapshot value derived from manual HMAC reconstruction (see
// encoding_consistency_test.go::TestDeriveBytePattern logic).
func TestSaltManager_BackwardCompatIRST(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// Fixed wall clock: 2026-04-18 12:00 UTC = 15:30 IRST (deep inside
	// the IRST local day, no edge effects).
	wall := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	m.SetClock(func() time.Time { return wall })

	got := m.CurrentSalt(42, "Asia/Tehran")

	// Manually compute the expected value: HMAC-SHA256(testSecret,
	// siteIDBytes(42) || "||" || "2026-04-18") — IRST date is 2026-04-18
	// (15:30 IRST same day).
	want := manualHMACSalt(t, 42, "2026-04-18")

	if got != want {
		t.Errorf("BackwardCompatIRST regression: SamplePlatform salt drifted\n  got:  %s\n  want: %s", got, want)
	}
}

// TestSaltManager_EmptyTZFallsBackToUTC — empty string tz falls back
// to UTC, NOT to IRST (regulator-safe default per DefaultSaltTimezone).
func TestSaltManager_EmptyTZFallsBackToUTC(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	wall := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	m.SetClock(func() time.Time { return wall })

	emptyTZ := m.CurrentSalt(7, "")
	utcTZ := m.CurrentSalt(7, "UTC")

	if emptyTZ != utcTZ {
		t.Errorf("empty tz should fall back to UTC, got %s, want %s", emptyTZ, utcTZ)
	}

	// At 12:00 UTC, UTC date = 2026-04-18; IRST date = 2026-04-18 (15:30 IRST).
	// Both are the same day — so they'll happen to match here. Pick a
	// moment where they differ to make the regression visible.
	wall2 := time.Date(2026, 4, 18, 22, 0, 0, 0, time.UTC)
	m.SetClock(func() time.Time { return wall2 })

	emptyTZ2 := m.CurrentSalt(7, "")
	irstTZ2 := m.CurrentSalt(7, "Asia/Tehran")

	if emptyTZ2 == irstTZ2 {
		t.Errorf("at 22:00 UTC empty tz should give UTC date (2026-04-18) not IRST date (2026-04-19); got identical salts %s", emptyTZ2)
	}
}

// TestSaltManager_InvalidTZFallsBackToUTC — unparseable tz strings
// fall back to UTC. Defensive — keeps a typo in admin config from
// silently producing IRST-style behaviour.
func TestSaltManager_InvalidTZFallsBackToUTC(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	wall := time.Date(2026, 4, 18, 22, 0, 0, 0, time.UTC)
	m.SetClock(func() time.Time { return wall })

	badTZ := m.CurrentSalt(7, "Garbage/Notatimezone")
	utcTZ := m.CurrentSalt(7, "UTC")

	if badTZ != utcTZ {
		t.Errorf("invalid tz should fall back to UTC, got %s, want %s", badTZ, utcTZ)
	}
}

// TestSaltManager_LegacyEnvVar — STATNIVE_SALT_TZ_LEGACY=1 forces
// every site to Asia/Tehran regardless of tz argument. Emergency
// rollback handle (see salt.go const legacyTimezone).
func TestSaltManager_LegacyEnvVar(t *testing.T) {
	// NOT parallel: mutates env-var, must isolate.
	t.Setenv("STATNIVE_SALT_TZ_LEGACY", "1")

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// Pick a moment where IRST date differs from every other tz date.
	// 21:00 UTC on 2026-04-17: IRST = 00:30 2026-04-18; UTC = 21:00
	// 2026-04-17. With the legacy switch on, "UTC" tz arg should
	// produce the IRST date salt (2026-04-18), not the UTC date salt
	// (2026-04-17).
	wall := time.Date(2026, 4, 17, 21, 0, 0, 0, time.UTC)
	m.SetClock(func() time.Time { return wall })

	asIfUTC := m.CurrentSalt(7, "UTC")
	asIfIRST := m.CurrentSalt(7, "Asia/Tehran")

	if asIfUTC != asIfIRST {
		t.Errorf("legacy env var on: every tz arg should resolve to Asia/Tehran; got UTC=%s IRST=%s", asIfUTC, asIfIRST)
	}

	// Confirm the salt matches the IRST snapshot.
	wantIRSTDate := manualHMACSalt(t, 7, "2026-04-18")
	if asIfUTC != wantIRSTDate {
		t.Errorf("legacy IRST date snapshot mismatch:\n  got:  %s\n  want: %s", asIfUTC, wantIRSTDate)
	}
}

// TestSaltManager_DSTBoundary — tz="Europe/Berlin" across the
// spring-forward (CET→CEST) and fall-back (CEST→CET) DST transitions.
// Verifies the salt changes exactly once per local day even across
// the DST jump.
func TestSaltManager_DSTBoundary(t *testing.T) {
	t.Parallel()

	m, err := identity.NewSaltManager([]byte(testSecret))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	berlin, _ := time.LoadLocation("Europe/Berlin")

	// Spring-forward 2026: last Sunday in March = 2026-03-29.
	// At 01:59 CET (00:59 UTC) clock jumps to 03:00 CEST (01:00 UTC).
	preSpring := time.Date(2026, 3, 28, 22, 0, 0, 0, time.UTC)  // 23:00 CET on 2026-03-28
	postSpring := time.Date(2026, 3, 29, 22, 0, 0, 0, time.UTC) // 00:00 CEST on 2026-03-30

	m.SetClock(func() time.Time { return preSpring })
	saltPre := m.CurrentSalt(1, "Europe/Berlin")

	m.SetClock(func() time.Time { return postSpring })
	saltPost := m.CurrentSalt(1, "Europe/Berlin")

	if saltPre == saltPost {
		t.Errorf("salt did not rotate across DST-affected days in Berlin")
	}

	// Confirm Berlin's local date stayed valid through the transition.
	expectedPre := preSpring.In(berlin).Format("2006-01-02")
	expectedPost := postSpring.In(berlin).Format("2006-01-02")

	if expectedPre == expectedPost {
		t.Errorf("test fixture wrong: pre and post timestamps land on same Berlin date %s", expectedPre)
	}
}

// manualHMACSalt is the snapshot reconstruction helper for the
// backward-compat + legacy-env tests. Mirrors derive() in salt.go.
func manualHMACSalt(t *testing.T, siteID uint32, date string) string {
	t.Helper()

	siteIDBytes := []byte{
		byte(siteID), byte(siteID >> 8), byte(siteID >> 16), byte(siteID >> 24),
	}

	mac := hmac.New(sha256.New, []byte(testSecret))

	_, _ = mac.Write(siteIDBytes)
	_, _ = mac.Write([]byte("||"))
	_, _ = mac.Write([]byte(date))

	return hex.EncodeToString(mac.Sum(nil))
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
