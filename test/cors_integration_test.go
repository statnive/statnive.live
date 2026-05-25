//go:build integration

// Integration: CORS middleware wired to /api/event through the full
// router chain. Covers OPTIONS preflight (204 + ACAO across the www.
// toggle, 403 unseeded), POST JSON across the 4-combo hostname×Origin
// matrix, and the defence-in-depth path where an unseeded Origin POST
// still reaches the handler (sendBeacon+text/plain transports must
// keep working without an allowlist entry).
//
// Requires `make ch-up`. Run with: make test-integration
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/statnive/statnive.live/internal/enrich"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/ingest"
	statnivemiddleware "github.com/statnive/statnive.live/internal/middleware"
	"github.com/statnive/statnive.live/internal/sites"
	"github.com/statnive/statnive.live/internal/storage"
)

// Disjoint site_ids + hostnames so this test never collides with the
// other integration tests in this package. The 70-range is unused by
// integration_test.go (42), multitenant_isolation_test.go (varies),
// and the rest.
const (
	corsTestSiteIDBare   = 71
	corsTestSiteIDWww    = 72
	corsTestSiteIDBoth   = 73
	corsTestHostBare     = "bare-only.cors-integration.example"
	corsTestHostWww      = "www.www-only.cors-integration.example"
	corsTestHostBoth     = "both.cors-integration.example"
	corsTestPathPrefix   = "/cors-integration"
	corsTestFlushTimeout = 15 * time.Second
)

// TestCORSIntegration_EventEndpoint exercises the post-fix wiring:
// /api/event registered for both POST and OPTIONS, wrapped with
// corsMW backed by a real OriginIndex built from seeded sites.
func TestCORSIntegration_EventEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs:    []string{clickhouseAddr()},
		Database: testDatabase,
		Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	migrator := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{
		Database: testDatabase,
	}, logger)
	if migErr := migrator.Run(ctx); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	// Clean slate for our test sites only.
	for _, id := range []uint32{corsTestSiteIDBare, corsTestSiteIDWww, corsTestSiteIDBoth} {
		if cleanErr := store.Conn().Exec(ctx,
			`ALTER TABLE statnive.events_raw DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`,
			id,
		); cleanErr != nil {
			t.Logf("delete-existing warning (ok on first run): %v", cleanErr)
		}

		if cleanErr := store.Conn().Exec(ctx,
			`ALTER TABLE statnive.sites DELETE WHERE site_id = ? SETTINGS mutations_sync = 2`,
			id,
		); cleanErr != nil {
			t.Logf("delete-existing sites warning (ok on first run): %v", cleanErr)
		}
	}

	// Seed three test sites covering the three allowlist topologies a
	// real tenant might pick.
	seeds := []struct {
		id       uint32
		hostname string
		origins  string
	}{
		{corsTestSiteIDBare, corsTestHostBare, `["https://` + corsTestHostBare + `"]`},
		{corsTestSiteIDWww, corsTestHostWww, `["https://` + corsTestHostWww + `"]`},
		{corsTestSiteIDBoth, corsTestHostBoth,
			`["https://` + corsTestHostBoth + `","https://www.` + corsTestHostBoth + `"]`},
	}

	for _, s := range seeds {
		if seedErr := store.Conn().Exec(ctx,
			`INSERT INTO statnive.sites (site_id, hostname, slug, enabled, allowed_origins)
			 VALUES (?, ?, ?, 1, ?)`,
			s.id, s.hostname, fmt.Sprintf("cors-test-%d", s.id), s.origins,
		); seedErr != nil {
			t.Fatalf("seed site %d: %v", s.id, seedErr)
		}
	}

	// Build the router using the same wiring main.go uses post-fix:
	// originIndex → corsMW → ingestHandler, with OPTIONS + POST
	// registered on /api/event.
	registry := sites.New(store.Conn())

	originIndex := sites.NewOriginIndex()
	if _, oErr := originIndex.Rebuild(ctx, registry); oErr != nil {
		t.Fatalf("origin index rebuild: %v", oErr)
	}

	corsMW := statnivemiddleware.CORS(originIndex.Resolver())

	walDir := t.TempDir()

	wal, err := ingest.NewWALWriter(ingest.WALConfig{Dir: filepath.Join(walDir, "wal")}, logger)
	if err != nil {
		t.Fatalf("wal: %v", err)
	}
	t.Cleanup(func() { _ = wal.Close() })

	saltMgr, err := identity.NewSaltManager([]byte("cors-integration-test-master-32b"))
	if err != nil {
		t.Fatalf("salt manager: %v", err)
	}

	geoIP, err := enrich.NewGeoIPEnricher("", logger)
	if err != nil {
		t.Fatalf("geoip: %v", err)
	}
	t.Cleanup(func() { _ = geoIP.Close() })

	channelMapper, err := enrich.NewChannelMapper("../config/sources.yaml")
	if err != nil {
		t.Fatalf("channel mapper: %v", err)
	}
	t.Cleanup(channelMapper.Close)

	pipeline := enrich.NewPipeline(enrich.Deps{
		Salt:    saltMgr,
		Bloom:   enrich.NewNewVisitorFilter(10000, 0.001),
		GeoIP:   geoIP,
		UA:      enrich.NewUAParser(),
		Bot:     enrich.NewBotDetector(logger),
		Channel: channelMapper,
		Logger:  logger,
	})

	groupSyncer := ingest.NewGroupSyncer(wal, ingest.GroupConfig{}, nil, logger)
	t.Cleanup(groupSyncer.Close)

	consumer := ingest.NewConsumer(groupSyncer.Out(), wal, store, ingest.ConsumerConfig{
		BatchRows:     50,
		BatchInterval: 100 * time.Millisecond,
		DrainSettle:   100 * time.Millisecond,
	}, nil, logger)

	consumerDone := make(chan struct{})
	go func() {
		consumer.Run(ctx)
		close(consumerDone)
	}()

	t.Cleanup(func() {
		cancel()
		<-consumerDone
	})

	ingestHandler := ingest.NewHandler(ingest.HandlerConfig{
		Pipeline: pipeline,
		WAL:      groupSyncer,
		Sites:    registry,
		Logger:   logger,
	})

	// Mirror main.go's chain order: corsMW BEFORE FastReject so OPTIONS
	// preflight short-circuits at the CORS layer (preflight returns 204
	// with ACAO; FastReject's POST-only check would otherwise 405 the
	// preflight before chi dispatched).
	router := chi.NewRouter()
	router.Group(func(r chi.Router) {
		r.Use(corsMW)
		r.Use(ingest.FastRejectMiddleware(nil, nil))
		r.Method(http.MethodPost, "/api/event", ingestHandler)
		r.Method(http.MethodOptions, "/api/event", ingestHandler)
	})

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	client := &http.Client{Timeout: testHTTPTimeout}

	// ===== Preflight tests =====

	preflightCases := []struct {
		name       string
		origin     string
		wantStatus int
		wantACAO   string // empty means "no ACAO expected"
	}{
		{
			name:       "preflight_bare_origin_against_bare_only_allowlist",
			origin:     "https://" + corsTestHostBare,
			wantStatus: http.StatusNoContent,
			wantACAO:   "https://" + corsTestHostBare,
		},
		{
			name:       "preflight_www_origin_against_bare_only_allowlist_via_fallback",
			origin:     "https://www." + corsTestHostBare,
			wantStatus: http.StatusNoContent,
			wantACAO:   "https://www." + corsTestHostBare, // echoes REQUEST origin, not seeded
		},
		{
			name:       "preflight_www_origin_against_www_only_allowlist",
			origin:     "https://" + corsTestHostWww,
			wantStatus: http.StatusNoContent,
			wantACAO:   "https://" + corsTestHostWww,
		},
		{
			name:       "preflight_bare_origin_against_www_only_allowlist_via_fallback",
			origin:     "https://" + strings.TrimPrefix(corsTestHostWww, "www."),
			wantStatus: http.StatusNoContent,
			wantACAO:   "https://" + strings.TrimPrefix(corsTestHostWww, "www."),
		},
		{
			name:       "preflight_unseeded_origin_returns_403",
			origin:     "https://attacker.example",
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tc := range preflightCases {
		t.Run(tc.name, func(t *testing.T) {
			req, reqErr := http.NewRequestWithContext(ctx, http.MethodOptions, srv.URL+"/api/event", nil)
			if reqErr != nil {
				t.Fatalf("build request: %v", reqErr)
			}

			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "content-type")

			resp, doErr := client.Do(req)
			if doErr != nil {
				t.Fatalf("OPTIONS: %v", doErr)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}

			got := resp.Header.Get("Access-Control-Allow-Origin")
			if tc.wantACAO == "" {
				if got != "" {
					t.Errorf("ACAO leaked = %q, want empty", got)
				}
			} else {
				if got != tc.wantACAO {
					t.Errorf("ACAO = %q, want %q (must echo request Origin)", got, tc.wantACAO)
				}

				if vary := resp.Header.Get("Vary"); !strings.Contains(vary, "Origin") {
					t.Errorf("Vary header missing Origin: %q", vary)
				}

				if acac := resp.Header.Get("Access-Control-Allow-Credentials"); acac != "true" {
					t.Errorf("ACAC = %q, want true", acac)
				}
			}
		})
	}

	// ===== POST tests: 4-combo hostname × Origin matrix =====

	// Each case uses a distinct pathname so we can correlate the row
	// back to the case in CH without depending on insertion order.
	postCases := []struct {
		name     string
		hostname string
		origin   string
		siteID   uint32 // expected resolved site_id (for CH query)
	}{
		{
			name:     "post_bare_hostname_bare_origin_against_bare_allowlist",
			hostname: corsTestHostBare,
			origin:   "https://" + corsTestHostBare,
			siteID:   corsTestSiteIDBare,
		},
		{
			name:     "post_bare_hostname_www_origin_against_bare_allowlist",
			hostname: corsTestHostBare,
			origin:   "https://www." + corsTestHostBare,
			siteID:   corsTestSiteIDBare,
		},
		{
			name:     "post_www_hostname_bare_origin_against_bare_allowlist",
			hostname: "www." + corsTestHostBare, // ingest www→bare fallback
			origin:   "https://" + corsTestHostBare,
			siteID:   corsTestSiteIDBare,
		},
		{
			name:     "post_www_hostname_www_origin_against_bare_allowlist",
			hostname: "www." + corsTestHostBare,
			origin:   "https://www." + corsTestHostBare,
			siteID:   corsTestSiteIDBare,
		},
	}

	for _, tc := range postCases {
		t.Run(tc.name, func(t *testing.T) {
			path := corsTestPathPrefix + "/" + tc.name
			body, _ := json.Marshal(ingest.RawEvent{
				Hostname:  tc.hostname,
				Pathname:  path,
				EventType: "custom",
				EventName: "purchase",
			})

			req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/event", bytes.NewReader(body))
			if reqErr != nil {
				t.Fatalf("build request: %v", reqErr)
			}

			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "Mozilla/5.0 (CORSIntegrationTest/1.0) BrowserLike")

			resp, doErr := client.Do(req)
			if doErr != nil {
				t.Fatalf("POST: %v", doErr)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusAccepted {
				bb, _ := io.ReadAll(resp.Body)
				t.Fatalf("POST status = %d, want 202; body=%s", resp.StatusCode, string(bb))
			}

			// CORS headers must echo on the POST response too (not just preflight)
			// so a browser allows the JS caller to read the response.
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != tc.origin {
				t.Errorf("POST response ACAO = %q, want %q", got, tc.origin)
			}

			// ClickHouse-oracle (Tier 1) — assert the row landed on the
			// correct site_id, addressed by the unique pathname.
			waitForPathnameOnSite(t, ctx, store, tc.siteID, path, corsTestFlushTimeout)
		})
	}

	// ===== Defence-in-depth: POST from unseeded Origin still reaches handler =====

	t.Run("post_from_unseeded_origin_still_reaches_handler", func(t *testing.T) {
		// wp-slimstat keeps working: hostname-from-body resolves, CORS
		// chains through without ACAO, browser blocks response but
		// server still records the event. Asserts the fix didn't
		// regress sendBeacon transports that don't trigger preflight.
		path := corsTestPathPrefix + "/defence-in-depth"
		body, _ := json.Marshal(ingest.RawEvent{
			Hostname:  corsTestHostBare,
			Pathname:  path,
			EventType: "pageview",
			EventName: "pageview",
		})

		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/event", bytes.NewReader(body))
		if reqErr != nil {
			t.Fatalf("build request: %v", reqErr)
		}

		req.Header.Set("Origin", "https://attacker.example") // unseeded
		req.Header.Set("Content-Type", "text/plain")        // CORS-safe content type
		req.Header.Set("User-Agent", "Mozilla/5.0 (CORSIntegrationTest/1.0) BrowserLike")

		resp, doErr := client.Do(req)
		if doErr != nil {
			t.Fatalf("POST: %v", doErr)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST status = %d, want 202 (handler must run regardless of Origin)", resp.StatusCode)
		}

		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("ACAO must NOT echo for unseeded Origin; got %q", got)
		}

		waitForPathnameOnSite(t, ctx, store, corsTestSiteIDBare, path, corsTestFlushTimeout)
	})
}

// waitForPathnameOnSite polls events_raw for a row at (site_id, pathname)
// — the Tier-1 ClickHouse-oracle pattern. Distinct from waitForCount in
// integration_test.go because we address rows by unique pathname rather
// than total count, so 4 concurrent subtests don't race each other.
func waitForPathnameOnSite(t *testing.T, parent context.Context, store *storage.ClickHouseStore, siteID uint32, pathname string, timeout time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		var got uint64

		row := store.Conn().QueryRow(ctx,
			`SELECT count() FROM statnive.events_raw WHERE site_id = ? AND pathname = ?`,
			siteID, pathname,
		)

		if scanErr := row.Scan(&got); scanErr == nil && got > 0 {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for row at (site_id=%d, pathname=%q)", siteID, pathname)
		case <-ticker.C:
		}
	}
}
