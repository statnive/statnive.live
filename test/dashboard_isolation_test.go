//go:build integration

// Dashboard query isolation test — pins Architecture Rule 8 + Privacy
// Rule 2 end-to-end. Seed two sites with disjoint paths + referrers,
// query every implemented Store method scoped by each site_id, assert
// no row from the other site leaks. Also verifies the cache hit path
// avoids redundant ClickHouse roundtrips.
package integration_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
	"github.com/statnive/statnive.live/internal/storage/storagetest"
)

const (
	siteA = uint32(401)
	siteB = uint32(402)
)

func TestDashboard_MultitenantIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{clickhouseAddr()},
		Database: testDatabase,
		Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: testDatabase}, logger)
	if migErr := migrator.Run(ctx); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	storagetest.CleanSiteEvents(t, ctx, store.Conn(), siteA)
	storagetest.CleanSiteEvents(t, ctx, store.Conn(), siteB)
	storagetest.SeedSite(t, ctx, store.Conn(), siteA, "tenant-a.example.com")
	storagetest.SeedSite(t, ctx, store.Conn(), siteB, "tenant-b.example.com")

	now := time.Now().UTC().Truncate(time.Hour)

	// Site A: 5 events on /a-only with chat.openai.com referrer.
	// Site B: 5 events on /b-only with bing.com referrer.
	// Cross-tenant leak test: no /a-only row appears for site B,
	// no chat.openai.com appears for site B, etc.
	events := make([]storagetest.SeedEvent, 0, 10)

	for i := 0; i < 5; i++ {
		events = append(events, storagetest.SeedEvent{
			SiteID:       siteA,
			Time:         now.Add(time.Duration(i) * time.Minute),
			Pathname:     "/a-only",
			Referrer:     "https://chat.openai.com/",
			ReferrerName: "chatgpt",
			Channel:      "AI",
			UTMCampaign:  "tenant-a-launch",
			IsGoal:       i%2 == 0,
			RevenueRials: 1000,
			VisitorHash:  [16]byte{byte('A'), byte(i)},
		})
		events = append(events, storagetest.SeedEvent{
			SiteID:       siteB,
			Time:         now.Add(time.Duration(i) * time.Minute),
			Pathname:     "/b-only",
			Referrer:     "https://bing.com/",
			ReferrerName: "bing",
			Channel:      "Organic Search",
			UTMCampaign:  "tenant-b-launch",
			IsGoal:       false,
			RevenueRials: 500,
			VisitorHash:  [16]byte{byte('B'), byte(i)},
		})
	}

	storagetest.WriteEvents(t, ctx, store.Conn(), events)

	queryStore := storage.NewClickhouseQueryStore(store)

	filter := func(siteID uint32) *storage.Filter {
		return &storage.Filter{
			SiteID: siteID,
			From:   now.Add(-time.Hour),
			To:     now.Add(time.Hour),
			Limit:  50,
		}
	}

	// --- Overview --- both sites have 5 events; aggregations match per-site.
	overviewA, err := queryStore.Overview(ctx, filter(siteA))
	if err != nil {
		t.Fatalf("overview siteA: %v", err)
	}

	overviewB, err := queryStore.Overview(ctx, filter(siteB))
	if err != nil {
		t.Fatalf("overview siteB: %v", err)
	}

	if overviewA.Pageviews != 5 {
		t.Errorf("siteA pageviews = %d, want 5", overviewA.Pageviews)
	}

	if overviewB.Pageviews != 5 {
		t.Errorf("siteB pageviews = %d, want 5", overviewB.Pageviews)
	}

	if overviewA.RevenueRials != 5000 {
		t.Errorf("siteA revenue = %d, want 5000", overviewA.RevenueRials)
	}

	if overviewB.RevenueRials != 2500 {
		t.Errorf("siteB revenue = %d, want 2500", overviewB.RevenueRials)
	}

	// --- Pages --- siteA only sees /a-only; siteB only sees /b-only.
	pagesA, err := queryStore.Pages(ctx, filter(siteA))
	if err != nil {
		t.Fatalf("pages siteA: %v", err)
	}

	for _, p := range pagesA {
		if p.Pathname == "/b-only" {
			t.Errorf("CRITICAL: siteA Pages leaked siteB pathname %q", p.Pathname)
		}
	}

	pagesB, err := queryStore.Pages(ctx, filter(siteB))
	if err != nil {
		t.Fatalf("pages siteB: %v", err)
	}

	for _, p := range pagesB {
		if p.Pathname == "/a-only" {
			t.Errorf("CRITICAL: siteB Pages leaked siteA pathname %q", p.Pathname)
		}
	}

	// --- Sources --- siteA's chatgpt referrer must not appear in siteB.
	sourcesB, err := queryStore.Sources(ctx, filter(siteB))
	if err != nil {
		t.Fatalf("sources siteB: %v", err)
	}

	for _, s := range sourcesB {
		if strings.EqualFold(s.ReferrerName, "chatgpt") {
			t.Errorf("CRITICAL: siteB Sources leaked siteA referrer %q", s.ReferrerName)
		}
	}

	// --- Campaigns --- siteA's tenant-a-launch must not appear for siteB.
	campaignsB, err := queryStore.Campaigns(ctx, filter(siteB))
	if err != nil {
		t.Fatalf("campaigns siteB: %v", err)
	}

	for _, c := range campaignsB {
		if c.UTMCampaign == "tenant-a-launch" {
			t.Errorf("CRITICAL: siteB Campaigns leaked siteA campaign %q", c.UTMCampaign)
		}
	}

	// --- ErrNotImplemented for v1.1+ endpoints ---
	if _, err := queryStore.Geo(ctx, filter(siteA)); err != storage.ErrNotImplemented {
		t.Errorf("Geo err = %v, want ErrNotImplemented", err)
	}

	if _, err := queryStore.Devices(ctx, filter(siteA)); err != storage.ErrNotImplemented {
		t.Errorf("Devices err = %v, want ErrNotImplemented", err)
	}

	if _, err := queryStore.Funnel(ctx, filter(siteA), []string{"/a-only"}); err != storage.ErrNotImplemented {
		t.Errorf("Funnel err = %v, want ErrNotImplemented", err)
	}

	cancel()
}

func TestDashboard_CachedStoreShortcuts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{clickhouseAddr()},
		Database: testDatabase,
		Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: testDatabase}, logger)
	if migErr := migrator.Run(ctx); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	storagetest.CleanSiteEvents(t, ctx, store.Conn(), siteA)
	storagetest.SeedSite(t, ctx, store.Conn(), siteA, "tenant-a.example.com")

	now := time.Now().UTC().Truncate(time.Hour)
	events := []storagetest.SeedEvent{{
		SiteID:       siteA,
		Time:         now,
		Pathname:     "/cache-test",
		ReferrerName: "direct",
		Channel:      "Direct",
		IsGoal:       true,
		RevenueRials: 100,
		VisitorHash:  [16]byte{1},
	}}
	storagetest.WriteEvents(t, ctx, store.Conn(), events)

	counter := &countingStore{Store: storage.NewClickhouseQueryStore(store)}
	cached := storage.NewCachedStore(counter, 64)

	f := &storage.Filter{
		SiteID: siteA,
		From:   now.Add(-time.Hour),
		To:     now.Add(time.Hour),
	}

	// Hit the cache twice — second call must NOT roundtrip to ClickHouse.
	if _, err := cached.Overview(ctx, f); err != nil {
		t.Fatalf("overview 1: %v", err)
	}

	if _, err := cached.Overview(ctx, f); err != nil {
		t.Fatalf("overview 2: %v", err)
	}

	if got := counter.overviewCalls.Load(); got != 1 {
		t.Errorf("inner Overview called %d times, want exactly 1 (cache miss)", got)
	}

	// Different filter → cache miss → second roundtrip.
	f2 := *f
	f2.Path = "/different"

	if _, err := cached.Overview(ctx, &f2); err != nil {
		t.Fatalf("overview 3: %v", err)
	}

	if got := counter.overviewCalls.Load(); got != 2 {
		t.Errorf("inner Overview called %d times, want 2 (different filter hash)", got)
	}

	cancel()
}

// countingStore embeds the underlying Store + only overrides the
// methods we count. Embedding picks up the other 8 implementations as
// pass-throughs, no boilerplate.
type countingStore struct {
	storage.Store

	overviewCalls atomic.Int32
}

func (c *countingStore) Overview(ctx context.Context, f *storage.Filter) (*storage.OverviewResult, error) {
	c.overviewCalls.Add(1)

	return c.Store.Overview(ctx, f)
}
