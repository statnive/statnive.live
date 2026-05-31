// Package main implements the Phase 7e load-gate event generator.
//
// Generates realistic /api/event payloads carrying the oracle tuple
// (test_run_id, generator_node_id, test_generator_seq, send_ts) so the
// canonical queries at test/perf/oracle_queries.sql can compute loss,
// duplicates, ordering inversions, and end-to-end latency from a single
// load run.
//
// Run with:
//
//	go run ./test/perf/generator/ \
//	    --url=http://127.0.0.1:8080 \
//	    --site-id=1 --eps=500 --duration=10m --nodes=4 \
//	    --profile=android-binge
//
// The generator prints its test_run_id at startup; pass that UUID to
// `make oracle-scan TEST_RUN_ID=<uuid>` after the run completes.
//
// Doc 29 §4 + doc 30 § session-length distribution.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

func main() {
	var (
		urlFlag      = flag.String("url", "http://127.0.0.1:8080", "binary base URL — POSTs go to <url>/api/event")
		siteID       = flag.Uint("site-id", 1, "site_id to send under (must already exist in statnive.sites)")
		hostname     = flag.String("hostname", "load-test.example.com", "Host header + payload hostname (must be in the site's allowlist)")
		eps          = flag.Float64("eps", 100, "events per second (aggregate across all generator nodes)")
		duration     = flag.Duration("duration", 60*time.Second, "how long to run before sending the final batch + exiting")
		nodes        = flag.Int("nodes", 1, "number of generator-node-ids to fan out across (1 = single-threaded sender)")
		concurrency  = flag.Int("concurrency", runtime.NumCPU(), "max in-flight HTTP requests across all nodes")
		profileFlag  = flag.String("profile", "android-short", "session profile: iphone-short | android-short | android-binge | mobile-web-power")
		runIDFlag    = flag.String("run-id", "", "explicit test_run_id UUID; default = generate a fresh one")
		timeoutFlag  = flag.Duration("timeout", 5*time.Second, "per-request HTTP timeout")
		logLevelFlag = flag.String("log-level", "info", "slog level: debug | info | warn | error")
	)
	flag.Parse()

	logger := newLogger(*logLevelFlag)

	runID, err := resolveRunID(*runIDFlag)
	if err != nil {
		logger.Error("invalid --run-id", "err", err)
		os.Exit(2)
	}

	profile, err := scenarioByName(*profileFlag)
	if err != nil {
		logger.Error("invalid --profile", "err", err)
		os.Exit(2)
	}

	if *nodes > maxGeneratorNodes {
		logger.Error("--nodes exceeds maximum",
			"nodes", *nodes, "max", maxGeneratorNodes)
		os.Exit(2)
	}

	cfg := genConfig{
		URL:         *urlFlag,
		Hostname:    *hostname,
		SiteID:      uint32(*siteID),
		EPS:         *eps,
		Duration:    *duration,
		Nodes:       *nodes,
		Concurrency: *concurrency,
		RunID:       runID,
		Profile:     profile,
		Timeout:     *timeoutFlag,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("generator start",
		"url", cfg.URL,
		"site_id", cfg.SiteID,
		"hostname", cfg.Hostname,
		"eps", cfg.EPS,
		"duration", cfg.Duration,
		"nodes", cfg.Nodes,
		"concurrency", cfg.Concurrency,
		"profile", *profileFlag,
		"test_run_id", cfg.RunID,
	)

	fmt.Printf("\n  TEST_RUN_ID=%s\n  (pass to: make oracle-scan TEST_RUN_ID=%s)\n\n",
		cfg.RunID, cfg.RunID)

	summary := run(ctx, logger, cfg)

	fmt.Printf("\n--- summary ---\n")
	fmt.Printf("sent_ok:       %d\n", summary.sentOK)
	fmt.Printf("sent_fail:     %d\n", summary.sentFail)
	fmt.Printf("ack_ratio:     %.4f%%\n", 100*float64(summary.sentOK)/float64(summary.sentOK+summary.sentFail))
	fmt.Printf("elapsed:       %s\n", summary.elapsed)
	fmt.Printf("achieved_eps:  %.1f\n", float64(summary.sentOK)/summary.elapsed.Seconds())
	fmt.Printf("test_run_id:   %s\n\n", cfg.RunID)
}

// resolveRunID returns the operator-supplied UUID or a fresh one.
func resolveRunID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Must(uuid.NewRandom()), nil
	}

	return uuid.Parse(s)
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level

	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lv}))
}

// maxGeneratorNodes pins the generator_node_id range. Migration 018's
// column is UInt16 (0..65535); we cap lower to keep oracle queries cheap.
const maxGeneratorNodes = 256

// genConfig is the per-run configuration. All fields immutable after
// flag parsing — workers pass it by value.
type genConfig struct {
	URL         string
	Hostname    string
	SiteID      uint32
	EPS         float64
	Duration    time.Duration
	Nodes       int
	Concurrency int
	RunID       uuid.UUID
	Profile     scenario
	Timeout     time.Duration
}

// genSummary aggregates per-run counters. Workers update atomically.
type genSummary struct {
	sentOK   uint64
	sentFail uint64
	elapsed  time.Duration
}

// run fans out workers, waits for them to drain, and returns the summary.
func run(ctx context.Context, logger *slog.Logger, cfg genConfig) genSummary {
	client := &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			MaxIdleConns:        cfg.Concurrency,
			MaxIdleConnsPerHost: cfg.Concurrency,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// runCtx ends after --duration even if SIGINT arrives later.
	runCtx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	// Per-node EPS share. nodes=1 → 100% EPS to node 1.
	perNodeEPS := cfg.EPS / float64(cfg.Nodes)

	// Semaphore bounds in-flight requests.
	sem := make(chan struct{}, cfg.Concurrency)

	var (
		wg       sync.WaitGroup
		summary  genSummary
		sentOK   atomic.Uint64
		sentFail atomic.Uint64
	)

	start := time.Now()

	for nodeID := 1; nodeID <= cfg.Nodes; nodeID++ {
		wg.Add(1)

		go func(nodeID uint16) {
			defer wg.Done()
			runNode(runCtx, logger, client, sem, cfg, nodeID, perNodeEPS, &sentOK, &sentFail)
		}(uint16(nodeID))
	}

	wg.Wait()

	summary.sentOK = sentOK.Load()
	summary.sentFail = sentFail.Load()
	summary.elapsed = time.Since(start)

	logger.Info("generator done",
		"sent_ok", summary.sentOK,
		"sent_fail", summary.sentFail,
		"elapsed", summary.elapsed,
	)

	return summary
}

// runNode is one generator-node. Drives session lifecycle + send rate.
func runNode(ctx context.Context, logger *slog.Logger, client *http.Client, sem chan struct{},
	cfg genConfig, nodeID uint16, perNodeEPS float64,
	sentOK, sentFail *atomic.Uint64,
) {
	// Per-node monotonic sequence — the oracle's loss + ordering primitive.
	var seq atomic.Uint64

	// rand.New + rand.NewPCG seeded from time + nodeID so parallel nodes
	// don't synchronize on the same session shapes.
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(nodeID)))

	tickInterval := time.Duration(float64(time.Second) / perNodeEPS)
	if tickInterval <= 0 {
		tickInterval = time.Millisecond
	}

	logger.Debug("node start", "node_id", nodeID, "eps", perNodeEPS, "tick", tickInterval)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// One event per tick. The session generator picks the
			// shape; we just hand it the next seq + timestamp.
			ev := nextEvent(rng, cfg, nodeID, seq.Add(1))

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}

			go func(ev rawEvent) {
				defer func() { <-sem }()

				if err := postEvent(ctx, client, cfg.URL, ev); err != nil {
					sentFail.Add(1)

					if rng.UintN(1000) == 0 { // ~0.1% sample
						logger.Debug("post failed", "err", err, "node_id", nodeID, "seq", ev.TestGeneratorSeq)
					}

					return
				}

				sentOK.Add(1)
			}(ev)
		}
	}
}
