package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"
)

// rawEvent mirrors ingest.RawEvent's JSON-relevant fields. We don't
// import the binary's type to keep the generator buildable standalone
// (you can run `go run ./test/perf/generator/` without compiling the
// rest of the tree); the wire format is the contract we're testing.
type rawEvent struct {
	Hostname      string `json:"hostname"`
	Pathname      string `json:"pathname"`
	Title         string `json:"title"`
	Referrer      string `json:"referrer"`
	UTMSource     string `json:"utm_source,omitempty"`
	UTMMedium     string `json:"utm_medium,omitempty"`
	UTMCampaign   string `json:"utm_campaign,omitempty"`
	ViewportWidth uint16 `json:"viewport_width"`
	EventType     string `json:"event_type"`
	EventName     string `json:"event_name"`
	UserID        string `json:"user_id,omitempty"`

	// Phase 7e oracle tuple.
	TestRunID        string `json:"test_run_id"`
	TestGeneratorSeq uint64 `json:"test_generator_seq"`
	GeneratorNodeID  uint16 `json:"generator_node_id"`
	SendTSMilli      int64  `json:"send_ts_ms"`
}

// landingPaths is a small fixed set that lets the channel-grouping and
// rollup paths exercise real cardinality without exploding the
// projection / hourly_visitors HLL. Real production traffic has ~50–
// 500 distinct paths per site; doc 30 shows the top-3 carry ~62% of
// pageviews, which this 4-entry list approximates.
var landingPaths = []string{
	"/",
	"/blog",
	"/pricing",
	"/checkout",
}

var pageTitles = []string{
	"Home",
	"Blog",
	"Pricing",
	"Checkout",
}

// uaAndroidChrome is a single representative UA. The bot-filter /
// device-detection pipeline already exercises UA cardinality via
// internal/enrich/ua_test.go fixtures — the load-gate just needs a
// shape that passes the bot-reject gate without triggering it.
const uaAndroidChrome = "Mozilla/5.0 (Linux; Android 14; SM-S921B) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"

// nextEvent generates one event payload. The scenario picks the shape;
// we pick the landing page + UA + cookie identity per the cohort's
// bingeBias to keep visitor_hash cardinality realistic.
func nextEvent(rng *rand.Rand, cfg genConfig, nodeID uint16, seq uint64) rawEvent {
	pathIdx := rng.IntN(len(landingPaths))

	ev := rawEvent{
		Hostname:         cfg.Hostname,
		Pathname:         landingPaths[pathIdx],
		Title:            pageTitles[pathIdx],
		Referrer:         "https://www.google.com/",
		UTMSource:        "google",
		UTMMedium:        "organic",
		ViewportWidth:    pickViewport(rng),
		EventType:        "pageview",
		EventName:        "pageview",
		UserID:           pickUserID(rng, cfg.Profile),
		TestRunID:        cfg.RunID.String(),
		TestGeneratorSeq: seq,
		GeneratorNodeID:  nodeID,
		SendTSMilli:      time.Now().UnixMilli(),
	}

	return ev
}

// pickUserID returns a user_id string drawn from a pool sized to honor
// the cohort's bingeBias — high bias = small pool = same UID returns
// often = long sessions; low bias = large pool = mostly-fresh sessions.
func pickUserID(rng *rand.Rand, sc scenario) string {
	const minPool = 32

	poolSize := minPool * (101 - int(sc.bingeBiasPercent))
	if poolSize < minPool {
		poolSize = minPool
	}

	return fmt.Sprintf("load-gate-uid-%d", rng.IntN(poolSize))
}

// pickViewport returns a plausible mobile viewport width. The bot
// filter rejects events with viewport_width = 0 or out of the
// (160..4096) range — staying inside avoids artificial loss.
func pickViewport(rng *rand.Rand) uint16 {
	common := []uint16{360, 375, 390, 414, 768, 820, 1024, 1280, 1440, 1920}
	return common[rng.IntN(len(common))]
}

// postEvent does one HTTP POST. Returns nil on 2xx, error otherwise.
// The Host header MUST be the cfg.Hostname (not the URL's host) because
// the binary's site-resolution looks at the Host header.
func postEvent(ctx context.Context, client *http.Client, baseURL string, ev rawEvent) error {
	body, err := json.Marshal(&ev)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/event", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("User-Agent", uaAndroidChrome)
	req.Host = ev.Hostname

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	return nil
}
