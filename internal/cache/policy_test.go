package cache_test

import (
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/cache"
)

func TestResolveTTL_Buckets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 18, 14, 30, 0, 0, time.UTC) // 14:30 UTC

	cases := []struct {
		name string
		to   time.Time
		want time.Duration
	}{
		{
			name: "to is current hour",
			to:   now, // 14:30 — same hour
			want: cache.TTLRealtime,
		},
		{
			name: "to is exactly current hour boundary",
			to:   now.Truncate(time.Hour), // 14:00
			want: cache.TTLRealtime,
		},
		{
			name: "to is end of today",
			to:   now.Truncate(24 * time.Hour).Add(24 * time.Hour), // tomorrow midnight
			want: cache.TTLRealtime, // > current hour
		},
		{
			name: "to is mid-today, before current hour",
			to:   now.Truncate(24 * time.Hour).Add(8 * time.Hour), // 08:00 today
			want: cache.TTLToday,
		},
		{
			name: "to is end of yesterday",
			to:   now.Truncate(24 * time.Hour), // today midnight = yesterday end
			want: cache.TTLToday,
		},
		{
			name: "to is mid-yesterday",
			to:   now.Truncate(24 * time.Hour).Add(-12 * time.Hour),
			want: cache.TTLYesterday,
		},
		{
			name: "to is 2 days ago",
			to:   now.Truncate(24 * time.Hour).Add(-2 * 24 * time.Hour),
			want: cache.TTLHistorical,
		},
		{
			name: "to is a month ago",
			to:   now.Add(-30 * 24 * time.Hour),
			want: cache.TTLHistorical,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cache.ResolveTTL(now, tc.to); got != tc.want {
				t.Errorf("ResolveTTL(now, %s) = %s, want %s", tc.to, got, tc.want)
			}
		})
	}
}

func TestResolveTTL_TimezoneNormalized(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 18, 14, 30, 0, 0, time.UTC)
	tehran, _ := time.LoadLocation("Asia/Tehran")
	nowInTehran := now.In(tehran)

	if cache.ResolveTTL(now, now) != cache.ResolveTTL(nowInTehran, nowInTehran) {
		t.Error("ResolveTTL must be timezone-invariant")
	}
}
