package enrich_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/statnive/statnive.live/internal/enrich"
)

// BenchmarkChannel_Classify pins the hot-path budget for the 17-step
// decision tree. PLAN.md verification 28: must stay <50 ns/call.
// This benchmark is the regression gate — if a future PR's number
// climbs above 50 ns, the channel mapper has regressed.
func BenchmarkChannel_Classify(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "sources.yaml")

	// Minimal fixture — same shape as channel_test.go.
	const fixture = `
sources:
  - {name: Google,    channel: Organic Search, domains: [google.com, google.ir]}
  - {name: Facebook,  channel: Social,         domains: [facebook.com]}
  - {name: ChatGPT,   channel: AI,             domains: [chat.openai.com]}
`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		b.Fatalf("write fixture: %v", err)
	}

	m, err := enrich.NewChannelMapper(path)
	if err != nil {
		b.Fatalf("new: %v", err)
	}
	defer m.Close()

	b.ReportAllocs()

	for b.Loop() {
		_ = m.Classify("https://google.ir/search?q=foo", "", "", "", "")
	}
}

// BenchmarkBloom_CheckAndMark — bloom is on every event's hot path.
// 18MB bloom + sync.Mutex; expect ~200 ns/call.
func BenchmarkBloom_CheckAndMark(b *testing.B) {
	f := enrich.NewNewVisitorFilter(10_000_000, 0.001)

	b.ReportAllocs()

	var i int

	for b.Loop() {
		var h [16]byte

		h[0] = byte(i)
		h[1] = byte(i >> 8)
		h[2] = byte(i >> 16)
		f.CheckAndMark(h, h)

		i++
	}
}

// BenchmarkUA_Parse — medama-io singleton parser hot path.
func BenchmarkUA_Parse(b *testing.B) {
	p := enrich.NewUAParser()

	const ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120"

	b.ReportAllocs()

	for b.Loop() {
		_ = p.Parse(ua)
	}
}

// BenchmarkBot_IsBot — cheap-first matcher; literal substring check
// against ~50 patterns is the common case.
func BenchmarkBot_IsBot(b *testing.B) {
	logger := slog.New(slog.DiscardHandler)
	d := enrich.NewBotDetector(logger)

	const ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120"

	b.ReportAllocs()

	for b.Loop() {
		_, _ = d.IsBot(ua, "")
	}
}
