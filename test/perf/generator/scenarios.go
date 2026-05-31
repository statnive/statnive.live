package main

import (
	"fmt"
	"strings"
)

// scenario picks the session-length distribution + per-session payload
// shape the generator uses. Names are the four behaviour cohorts from
// doc 30's GA4 calibration (post-2026-04-20 anonymised observation
// of SamplePlatform-class traffic):
//
//	iphone-short        15% — 1–3 pageviews per session, ≤30s
//	android-short       40% — 1–3 pageviews per session, ≤60s
//	android-binge       30% — 5–20 pageviews per session, 5–30 min
//	mobile-web-power    15% — 20–80 pageviews per session, 1–8h
//
// The default ramp is constant EPS; load-gate phases P1–P5 add ramp
// profiles on top via the --eps flag schedule (driven externally by
// the locust/k6 harness in B.3 — the generator itself is steady).
type scenario struct {
	name             string
	minPageviews     int
	maxPageviews     int
	minSessionMS     int
	maxSessionMS     int
	bingeBiasPercent uint8 // probability the next event is in the same session
}

var allScenarios = []scenario{
	{
		name:             "iphone-short",
		minPageviews:     1,
		maxPageviews:     3,
		minSessionMS:     1500,
		maxSessionMS:     30000,
		bingeBiasPercent: 50,
	},
	{
		name:             "android-short",
		minPageviews:     1,
		maxPageviews:     3,
		minSessionMS:     2000,
		maxSessionMS:     60000,
		bingeBiasPercent: 55,
	},
	{
		name:             "android-binge",
		minPageviews:     5,
		maxPageviews:     20,
		minSessionMS:     5 * 60 * 1000,
		maxSessionMS:     30 * 60 * 1000,
		bingeBiasPercent: 85,
	},
	{
		name:             "mobile-web-power",
		minPageviews:     20,
		maxPageviews:     80,
		minSessionMS:     60 * 60 * 1000,
		maxSessionMS:     8 * 60 * 60 * 1000,
		bingeBiasPercent: 95,
	},
}

// scenarioByName resolves a CLI flag value to a scenario.
func scenarioByName(name string) (scenario, error) {
	name = strings.ToLower(strings.TrimSpace(name))

	for _, s := range allScenarios {
		if s.name == name {
			return s, nil
		}
	}

	return scenario{}, fmt.Errorf("unknown profile %q (known: %s)", name, knownScenarioNames())
}

func knownScenarioNames() string {
	names := make([]string, 0, len(allScenarios))

	for _, s := range allScenarios {
		names = append(names, s.name)
	}

	return strings.Join(names, ", ")
}
