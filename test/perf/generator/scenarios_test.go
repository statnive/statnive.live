package main

import (
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestScenarioByName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"iphone-short", "android-short", "android-binge", "mobile-web-power"} {
		sc, err := scenarioByName(name)
		if err != nil {
			t.Errorf("scenarioByName(%q): unexpected error %v", name, err)
			continue
		}

		if sc.name != name {
			t.Errorf("scenarioByName(%q) returned name %q", name, sc.name)
		}

		if sc.minPageviews <= 0 || sc.maxPageviews < sc.minPageviews {
			t.Errorf("scenarioByName(%q) bad pageview range: %d..%d", name, sc.minPageviews, sc.maxPageviews)
		}

		if sc.minSessionMS <= 0 || sc.maxSessionMS < sc.minSessionMS {
			t.Errorf("scenarioByName(%q) bad session ms range: %d..%d", name, sc.minSessionMS, sc.maxSessionMS)
		}
	}

	// Bad name → error, no panic.
	if _, err := scenarioByName("not-a-real-profile"); err == nil {
		t.Error("scenarioByName(bogus): want error, got nil")
	}

	// Whitespace + case normalized.
	sc, err := scenarioByName("  IPHONE-SHORT  ")
	if err != nil {
		t.Fatalf("scenarioByName whitespace: %v", err)
	}

	if sc.name != "iphone-short" {
		t.Errorf("whitespace normalize: got %q", sc.name)
	}
}

func TestNextEvent_OracleTuplePresent(t *testing.T) {
	t.Parallel()

	cfg := genConfig{
		URL:      "http://127.0.0.1:8080",
		Hostname: "load-test.example.com",
		SiteID:   1,
		RunID:    uuid.Must(uuid.NewRandom()),
		Profile:  allScenarios[0],
	}

	rng := rand.New(rand.NewPCG(1, 2))
	ev := nextEvent(rng, cfg, 7, 42)

	if ev.TestRunID != cfg.RunID.String() {
		t.Errorf("TestRunID: got %q want %q", ev.TestRunID, cfg.RunID.String())
	}

	if ev.GeneratorNodeID != 7 {
		t.Errorf("GeneratorNodeID: got %d want 7", ev.GeneratorNodeID)
	}

	if ev.TestGeneratorSeq != 42 {
		t.Errorf("TestGeneratorSeq: got %d want 42", ev.TestGeneratorSeq)
	}

	if ev.SendTSMilli <= 0 {
		t.Errorf("SendTSMilli must be set; got %d", ev.SendTSMilli)
	}

	if ev.Hostname != "load-test.example.com" {
		t.Errorf("Hostname: got %q", ev.Hostname)
	}
}

// TestPostEvent_RoundTrip is the smallest possible Tier-3 — a real
// HTTP server that captures the payload, parses it back as rawEvent,
// and asserts the oracle tuple round-tripped intact.
func TestPostEvent_RoundTrip(t *testing.T) {
	t.Parallel()

	var captured rawEvent

	var hits atomic.Uint32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}

		if jsonErr := json.Unmarshal(body, &captured); jsonErr != nil {
			http.Error(w, "json", http.StatusBadRequest)
			return
		}

		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := genConfig{
		URL:      srv.URL,
		Hostname: "load-test.example.com",
		SiteID:   1,
		RunID:    uuid.Must(uuid.NewRandom()),
		Profile:  allScenarios[0],
		Timeout:  2 * time.Second,
	}

	rng := rand.New(rand.NewPCG(99, 100))
	ev := nextEvent(rng, cfg, 3, 99)

	client := &http.Client{Timeout: cfg.Timeout}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := postEvent(ctx, client, srv.URL, ev); err != nil {
		t.Fatalf("postEvent: %v", err)
	}

	if hits.Load() != 1 {
		t.Fatalf("server hits: got %d want 1", hits.Load())
	}

	if captured.TestRunID != cfg.RunID.String() {
		t.Errorf("captured TestRunID: got %q want %q", captured.TestRunID, cfg.RunID.String())
	}

	if captured.GeneratorNodeID != 3 {
		t.Errorf("captured GeneratorNodeID: got %d want 3", captured.GeneratorNodeID)
	}

	if captured.TestGeneratorSeq != 99 {
		t.Errorf("captured TestGeneratorSeq: got %d want 99", captured.TestGeneratorSeq)
	}
}
