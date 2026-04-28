// Phase 7e load-gate synthesizer (PLAN.md §283, doc 29 §6.1).
//
// Two modes:
//   - --mode=synth   curve-formula traffic mixed with the four oracle fields,
//     Iranian UAs + Persian paths + 1500-visitor pool.
//   - --mode=replay  NDJSON line-protocol replay of SamplePlatform anonymized
//     exports (chain-of-custody per docs/replay-attestation-template.md).
//
// generator_seq protocol — every emitted event carries
// (test_run_id, generator_node_id, test_generator_seq, send_ts) as headers.
// On generator restart within a run, the orchestrator must assign a fresh
// node_id (never resume a seq counter); see doc 29 §6.1.
//
// Locust is the primary tool for the Phase 7e gate (test/perf/gate/). This
// generator complements Locust for two cases that pure HTTP load doesn't
// cover:
//  1. Replay of SamplePlatform anonymized NDJSON for realistic load shape.
//  2. Curve-formula synth (match_spike, ramadan_diurnal, long_session) when
//     Locust's `wait_time` discipline is insufficient.
//
// Build:    go build -o bin/load-gate-generator ./test/perf/generator
// Tune:     ./test/perf/generator/sysctl.sh   (apply doc 29 §3.2 kernel tuning)
// Run:      bin/load-gate-generator --mode=synth --target http://127.0.0.1:8080 --eps 450 --duration 5m
package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const (
	headerTestRunID        = "X-Statnive-Test-Run-Id"
	headerGeneratorNodeID  = "X-Statnive-Generator-Node-Id"
	headerTestGeneratorSeq = "X-Statnive-Test-Generator-Seq"
	headerSendTs           = "X-Statnive-Send-Ts"
)

//go:embed shape/load-shape.json
var loadShapeJSON []byte

type loadShape struct {
	PersianPaths      []string `json:"persianPaths"`
	IranianUserAgents []string `json:"iranianUserAgents"`
}

var (
	persianPaths []string
	iranianUAs   []string
)

func init() {
	var s loadShape
	if err := json.Unmarshal(loadShapeJSON, &s); err != nil {
		panic("load-shape.json malformed: " + err.Error())
	}

	persianPaths = s.PersianPaths
	iranianUAs = s.IranianUserAgents
}

// Generator is doc 29 §6.1's per-node identity carrier.
type Generator struct {
	RunID  uuid.UUID
	NodeID uint16
	seq    atomic.Uint64
}

// Next returns the next dense monotonic sequence for this (run, node).
func (g *Generator) Next() uint64 { return g.seq.Add(1) }

type config struct {
	mode       string
	target     string
	runID      string
	nodeID     uint
	eps        int
	duration   time.Duration
	replayFile string
	hostname   string
	workers    int
	verbose    bool
}

func main() {
	os.Exit(run())
}

func run() int {
	cfg := parseFlags()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	runID, err := resolveRunID(cfg.runID)
	if err != nil {
		logger.Error("invalid run-id", "err", err)

		return 2
	}

	if cfg.nodeID == 0 || cfg.nodeID > 65535 {
		logger.Error("node-id out of range (1..65535)", "got", cfg.nodeID)

		return 2
	}

	gen := &Generator{RunID: runID, NodeID: uint16(cfg.nodeID)}

	logger.Info("generator starting",
		"mode", cfg.mode,
		"target", cfg.target,
		"test_run_id", gen.RunID.String(),
		"generator_node_id", gen.NodeID,
		"eps", cfg.eps,
		"duration", cfg.duration,
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if cfg.duration > 0 {
		ctx2, cancel2 := context.WithTimeout(ctx, cfg.duration)
		defer cancel2()

		ctx = ctx2
	}

	switch cfg.mode {
	case "synth":
		if err := runSynth(ctx, logger, cfg, gen); err != nil {
			logger.Error("synth run failed", "err", err)

			return 1
		}
	case "replay":
		if cfg.replayFile == "" {
			logger.Error("--replay-file required for --mode=replay")

			return 2
		}

		if err := runReplay(ctx, logger, cfg, gen); err != nil {
			logger.Error("replay run failed", "err", err)

			return 1
		}
	default:
		logger.Error("unknown mode", "mode", cfg.mode)

		return 2
	}

	return 0
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.mode, "mode", "synth", "synth | replay")
	flag.StringVar(&cfg.target, "target", "http://127.0.0.1:8080", "binary base URL")
	flag.StringVar(&cfg.runID, "run-id", "", "test_run_id (UUID); empty = mint a fresh one")
	flag.UintVar(&cfg.nodeID, "node-id", 1, "generator_node_id (1..65535); fresh per generator host")
	flag.IntVar(&cfg.eps, "eps", 450, "target events/sec (P1=450, P5=40000)")
	flag.DurationVar(&cfg.duration, "duration", 5*time.Minute, "run duration (0 = until SIGINT)")
	flag.StringVar(&cfg.replayFile, "replay-file", "", "NDJSON line-protocol replay file")
	flag.StringVar(&cfg.hostname, "hostname", "load-test.example.com", "events_raw.hostname filter for oracle scan")
	flag.IntVar(&cfg.workers, "workers", 32, "concurrent HTTP senders")
	flag.BoolVar(&cfg.verbose, "verbose", false, "log every emit (noisy)")
	flag.Parse()

	return cfg
}

func resolveRunID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.New(), nil
	}

	return uuid.Parse(s)
}

// runSynth drives target EPS via a token-bucket-ish rate gate. Phase 7e
// scaffolds the curve-formula stubs (match_spike, ramadan_diurnal,
// long_session) as no-ops on top of a flat baseline; doc 29 §2.5 / doc 30
// §6 wire-up lands in the Phase 10 P3+ gate when production replay arrives.
func runSynth(ctx context.Context, logger *slog.Logger, cfg *config, gen *Generator) error {
	if cfg.eps <= 0 {
		return errors.New("eps must be > 0")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	tickInterval := time.Second / time.Duration(cfg.eps)

	var (
		emitted atomic.Uint64
		failed  atomic.Uint64
	)

	jobs := make(chan struct{}, cfg.workers*2)

	var wg sync.WaitGroup
	for range cfg.workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range jobs {
				if err := emitSynthEvent(ctx, client, cfg, gen); err != nil {
					failed.Add(1)

					if cfg.verbose {
						logger.Warn("emit failed", "err", err)
					}

					continue
				}

				emitted.Add(1)
			}
		}()
	}

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	progress := time.NewTicker(10 * time.Second)
	defer progress.Stop()

	started := time.Now()

ProduceLoop:
	for {
		select {
		case <-ctx.Done():
			break ProduceLoop
		case <-progress.C:
			logger.Info("progress",
				"elapsed_s", int(time.Since(started).Seconds()),
				"emitted", emitted.Load(),
				"failed", failed.Load(),
			)
		case <-ticker.C:
			select {
			case jobs <- struct{}{}:
			default:
				// queue full — back-pressure means we're below configured EPS;
				// counted as failed rather than logged (would be too noisy).
				failed.Add(1)
			}
		}
	}

	close(jobs)
	wg.Wait()

	logger.Info("synth complete",
		"test_run_id", gen.RunID.String(),
		"emitted", emitted.Load(),
		"failed", failed.Load(),
		"elapsed_s", int(time.Since(started).Seconds()),
	)

	return nil
}

func runReplay(ctx context.Context, logger *slog.Logger, cfg *config, gen *Generator) error {
	f, err := os.Open(cfg.replayFile)
	if err != nil {
		return fmt.Errorf("open replay: %w", err)
	}

	defer func() { _ = f.Close() }()

	client := &http.Client{Timeout: 5 * time.Second}
	dec := json.NewDecoder(f)

	var emitted, failed uint64

	for dec.More() {
		if err := ctx.Err(); err != nil {
			break
		}

		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("decode: %w", err)
		}

		if err := emitReplayEvent(ctx, client, cfg, gen, raw); err != nil {
			failed++

			if cfg.verbose {
				logger.Warn("replay emit failed", "err", err)
			}

			continue
		}

		emitted++
	}

	logger.Info("replay complete", "emitted", emitted, "failed", failed)

	return nil
}

func emitSynthEvent(ctx context.Context, client *http.Client, cfg *config, gen *Generator) error {
	visitorIdx := randIntN(1500)
	pathIdx := randIntN(len(persianPaths))
	uaIdx := randIntN(len(iranianUAs))
	xff := fmt.Sprintf("192.0.2.%d", randIntN(254)+1)

	body := fmt.Sprintf(
		`{"hostname":%q,"pathname":%q,"event_type":"pageview","event_name":"pageview"}`,
		cfg.hostname, persianPaths[pathIdx],
	)

	return emit(ctx, client, cfg, gen, []byte(body), iranianUAs[uaIdx],
		fmt.Sprintf("v-%08x", visitorIdx), xff)
}

func emitReplayEvent(ctx context.Context, client *http.Client, cfg *config, gen *Generator, body []byte) error {
	// Replay UAs are baked into the anonymized export; we still pin XFF to
	// the documentation range so no live IP ever enters the test pipeline.
	xff := fmt.Sprintf("192.0.2.%d", randIntN(254)+1)

	return emit(ctx, client, cfg, gen, body, iranianUAs[0], "", xff)
}

// randIntN wraps math/rand/v2.IntN in a single nolint-suppressed call. This
// generator is a load-test fixture surface (synthetic Persian paths,
// documentation-range XFFs, sample UAs); cryptographic randomness would
// slow synth without changing oracle outcomes.
func randIntN(n int) int {
	//nolint:gosec // load-test fixture; not a security surface.
	return rand.IntN(n)
}

func emit(ctx context.Context, client *http.Client, cfg *config, gen *Generator,
	body []byte, ua, cookie, xff string,
) error {
	seq := gen.Next()
	sendTsMs := time.Now().UnixMilli()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.target+"/api/event", bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", ua)

	if cookie != "" {
		req.Header.Set("Cookie", "_statnive="+cookie)
	}

	req.Header.Set("X-Forwarded-For", xff)
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set(headerTestRunID, gen.RunID.String())
	req.Header.Set(headerGeneratorNodeID, strconv.FormatUint(uint64(gen.NodeID), 10))
	req.Header.Set(headerTestGeneratorSeq, strconv.FormatUint(seq, 10))
	req.Header.Set(headerSendTs, strconv.FormatInt(sendTsMs, 10))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	return nil
}
