package ingest_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/metrics"
)

func TestValidateAndSanitize_AcceptsCleanEvent(t *testing.T) {
	t.Parallel()

	ev := ingest.RawEvent{
		Hostname:    "example.com",
		Pathname:    "/checkout",
		Title:       "Checkout — Example Store",
		Referrer:    "https://google.com/",
		UTMSource:   "newsletter",
		UTMMedium:   "email",
		UTMCampaign: "spring-sale-2026",
		EventType:   "custom",
		EventName:   "purchase.complete-v2",
		UserSegment: "returning",
		Props: map[string]string{
			"plan":  "pro",
			"price": "49.00",
		},
	}

	reason := ingest.ValidateAndSanitize(&ev)
	if reason != "" {
		t.Fatalf("clean event rejected: %q", reason)
	}

	if ev.EventName != "purchase.complete-v2" {
		t.Errorf("event_name mutated: %q", ev.EventName)
	}

	if ev.Props["plan"] != "pro" {
		t.Errorf("props mutated: %v", ev.Props)
	}
}

func TestValidateAndSanitize_HardReject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		ev     ingest.RawEvent
		reason string
	}{
		{
			name:   "script in event_name",
			ev:     ingest.RawEvent{Hostname: "x.com", EventName: "<script>"},
			reason: metrics.ReasonBadEventName,
		},
		{
			name:   "space in event_name",
			ev:     ingest.RawEvent{Hostname: "x.com", EventName: "page view"},
			reason: metrics.ReasonBadEventName,
		},
		{
			name:   "newline in event_name",
			ev:     ingest.RawEvent{Hostname: "x.com", EventName: "page\nview"},
			reason: metrics.ReasonBadEventName,
		},
		{
			name:   "space in event_type",
			ev:     ingest.RawEvent{Hostname: "x.com", EventType: "custom event"},
			reason: metrics.ReasonBadEventType,
		},
		{
			name:   "script as prop_key",
			ev:     ingest.RawEvent{Hostname: "x.com", Props: map[string]string{"<script>": "x"}},
			reason: metrics.ReasonBadPropKey,
		},
		{
			name:   "space in prop_key",
			ev:     ingest.RawEvent{Hostname: "x.com", Props: map[string]string{"bad key": "x"}},
			reason: metrics.ReasonBadPropKey,
		},
		{
			name:   "slash in prop_key (tighter than event_name regex)",
			ev:     ingest.RawEvent{Hostname: "x.com", Props: map[string]string{"a/b": "x"}},
			reason: metrics.ReasonBadPropKey,
		},
		{
			name: "props map exceeds entry cap",
			ev: ingest.RawEvent{
				Hostname: "x.com",
				Props:    fakeProps(100),
			},
			reason: metrics.ReasonTooManyProps,
		},
		{
			name:   "event_name exceeds 64 bytes",
			ev:     ingest.RawEvent{Hostname: "x.com", EventName: strings.Repeat("a", 65)},
			reason: metrics.ReasonBadEventName,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ev := tc.ev
			reason := ingest.ValidateAndSanitize(&ev)

			if reason != tc.reason {
				t.Errorf("reason = %q, want %q", reason, tc.reason)
			}
		})
	}
}

func TestValidateAndSanitize_AcceptedShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ev   ingest.RawEvent
	}{
		{"dotted event_name", ingest.RawEvent{Hostname: "x.com", EventName: "purchase.complete-v2"}},
		{"slash event_name", ingest.RawEvent{Hostname: "x.com", EventName: "checkout/step-1"}},
		{"colon event_name", ingest.RawEvent{Hostname: "x.com", EventName: "namespace:event"}},
		{"empty event_name", ingest.RawEvent{Hostname: "x.com"}},
		{"underscore prop_key", ingest.RawEvent{Hostname: "x.com", Props: map[string]string{"plan_tier": "pro"}}},
		{"dashed prop_key", ingest.RawEvent{Hostname: "x.com", Props: map[string]string{"plan-tier": "pro"}}},
		{"dotted prop_key", ingest.RawEvent{Hostname: "x.com", Props: map[string]string{"plan.tier": "pro"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ev := tc.ev
			reason := ingest.ValidateAndSanitize(&ev)

			if reason != "" {
				t.Errorf("rejected with %q", reason)
			}
		})
	}
}

func TestValidateAndSanitize_SoftSanitize_StripsControlChars(t *testing.T) {
	t.Parallel()

	ev := ingest.RawEvent{
		Hostname: "x.com",
		Title:    "A\x00\x01B\x07C\x7FD",
		Referrer: "https://x.com/?\x00",
	}

	if reason := ingest.ValidateAndSanitize(&ev); reason != "" {
		t.Fatalf("soft-sanitize should not reject: %q", reason)
	}

	if ev.Title != "ABCD" {
		t.Errorf("title = %q, want ABCD", ev.Title)
	}

	if ev.Referrer != "https://x.com/?" {
		t.Errorf("referrer = %q, want https://x.com/?", ev.Referrer)
	}
}

func TestValidateAndSanitize_SoftSanitize_PreservesTabAndUnicode(t *testing.T) {
	t.Parallel()

	ev := ingest.RawEvent{
		Hostname: "x.com",
		Title:    "درود جهان 你好\tworld",
	}

	if reason := ingest.ValidateAndSanitize(&ev); reason != "" {
		t.Fatalf("rejected: %q", reason)
	}

	if ev.Title != "درود جهان 你好\tworld" {
		t.Errorf("multibyte / tab mangled: %q", ev.Title)
	}
}

func TestValidateAndSanitize_SoftSanitize_TruncatesAtRuneBoundary(t *testing.T) {
	t.Parallel()

	// 3-byte rune × N puts the 512-byte cap mid-rune. Verify we never
	// split a rune.
	rune3 := "你" // U+4F60, 3 bytes in UTF-8
	ev := ingest.RawEvent{
		Hostname: "x.com",
		Title:    strings.Repeat(rune3, 200), // 600 bytes
	}

	if reason := ingest.ValidateAndSanitize(&ev); reason != "" {
		t.Fatalf("rejected: %q", reason)
	}

	if len(ev.Title) > 512 {
		t.Errorf("title length %d > maxTitleLen 512", len(ev.Title))
	}

	if !utf8.ValidString(ev.Title) {
		t.Errorf("truncated title is not valid UTF-8: %q", ev.Title)
	}
}

func TestValidateAndSanitize_SoftSanitize_TruncatesOversizePropVal(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("a", 10_000)
	ev := ingest.RawEvent{
		Hostname: "x.com",
		Props:    map[string]string{"plan": huge},
	}

	if reason := ingest.ValidateAndSanitize(&ev); reason != "" {
		t.Fatalf("rejected: %q", reason)
	}

	if ev.Props["plan"] != strings.Repeat("a", 256) {
		t.Errorf("prop val not truncated to 256 bytes; len=%d", len(ev.Props["plan"]))
	}
}

func TestValidateAndSanitize_SoftSanitize_KeepsHTMLCharsInFreeFields(t *testing.T) {
	t.Parallel()

	// `<` / `>` / `&` / quotes are NOT stripped — they are valid bytes
	// in URLs and titles. The dashboard layer is responsible for
	// HTML-escaping on render (OWASP A03 — escape on output, not input).
	ev := ingest.RawEvent{
		Hostname: "x.com",
		Title:    "<script>alert(1)</script>",
		Referrer: "https://example.com/?a=b&c=<script>",
	}

	if reason := ingest.ValidateAndSanitize(&ev); reason != "" {
		t.Fatalf("free-form sanitize must not reject HTML-ish bytes: %q", reason)
	}

	if ev.Title != "<script>alert(1)</script>" {
		t.Errorf("title mutated: %q", ev.Title)
	}

	if ev.Referrer != "https://example.com/?a=b&c=<script>" {
		t.Errorf("referrer mutated: %q", ev.Referrer)
	}
}

func TestValidateAndSanitize_TruncatesOversizeFreeFields(t *testing.T) {
	t.Parallel()

	ev := ingest.RawEvent{
		Hostname:    strings.Repeat("a", 1000), // > maxHostnameLen 253
		Pathname:    strings.Repeat("p", 2000), // > maxPathnameLen 1024
		Title:       strings.Repeat("t", 2000), // > maxTitleLen 512
		Referrer:    strings.Repeat("r", 2000), // > maxReferrerLen 1024
		UTMSource:   strings.Repeat("u", 1000), // > maxUTMFieldLen 256
		UserSegment: strings.Repeat("s", 1000), // > maxUserSegmentLen 64
	}

	if reason := ingest.ValidateAndSanitize(&ev); reason != "" {
		t.Fatalf("rejected: %q", reason)
	}

	checks := []struct {
		name  string
		value string
		want  int
	}{
		{"hostname", ev.Hostname, 253},
		{"pathname", ev.Pathname, 1024},
		{"title", ev.Title, 512},
		{"referrer", ev.Referrer, 1024},
		{"utm_source", ev.UTMSource, 256},
		{"user_segment", ev.UserSegment, 64},
	}

	for _, c := range checks {
		if len(c.value) != c.want {
			t.Errorf("%s len = %d, want %d", c.name, len(c.value), c.want)
		}
	}
}

func TestValidateAndSanitize_NilPropsSafe(t *testing.T) {
	t.Parallel()

	ev := ingest.RawEvent{Hostname: "x.com", EventName: "pageview"}

	if reason := ingest.ValidateAndSanitize(&ev); reason != "" {
		t.Errorf("nil Props rejected: %q", reason)
	}
}

func TestValidateAndSanitize_PreservesUnchangedPropMapIdentity(t *testing.T) {
	t.Parallel()

	// When no prop value needs truncation, the map should not be
	// rewritten — verifies the cleaned-vs-v guard in sanitizeFree.
	props := map[string]string{"plan": "pro"}

	ev := ingest.RawEvent{Hostname: "x.com", Props: props}

	if reason := ingest.ValidateAndSanitize(&ev); reason != "" {
		t.Fatalf("rejected: %q", reason)
	}

	if ev.Props["plan"] != "pro" {
		t.Errorf("prop value mutated: %q", ev.Props["plan"])
	}
}

func fakeProps(n int) map[string]string {
	m := make(map[string]string, n)
	for i := range n {
		m["k"+strconv.Itoa(i)] = "v"
	}

	return m
}
