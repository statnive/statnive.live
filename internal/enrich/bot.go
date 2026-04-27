package enrich

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"
)

// fallbackPatterns is the inline bot list that ships with the binary.
// Used when the embedded crawler-user-agents.json is empty or invalid —
// e.g., a fresh checkout before `make refresh-bot-patterns` runs.
//
// Substring-match (cheap-first per doc 24 §Sec 1.3); regex patterns get
// added only when the embedded JSON file is populated.
var fallbackPatterns = []string{
	"Googlebot", "AdsBot-Google", "Mediapartners-Google", "APIs-Google",
	"Google-InspectionTool", "Storebot-Google", "GoogleOther", "Feedfetcher-Google",
	"bingbot", "BingPreview", "adidxbot",
	"Slurp", "DuckDuckBot", "YandexBot", "YandexImages", "YandexMobile",
	"Baiduspider", "Sogou", "SeznamBot", "Applebot",
	"facebookexternalhit", "Facebot", "meta-externalagent",
	"Twitterbot", "LinkedInBot", "Pinterestbot", "redditbot",
	"Discordbot", "TelegramBot", "Slackbot", "WhatsApp",
	"AhrefsBot", "SemrushBot", "MJ12bot", "DotBot", "BLEXBot",
	"PetalBot", "Exabot", "Nutch", "HTTrack", "ia_archiver",
	"ClaudeBot", "GPTBot", "ChatGPT-User", "OAI-SearchBot", "PerplexityBot",
	"Amazonbot", "Bytespider", "ImagesiftBot", "DataForSeoBot",
	"Python-urllib", "python-requests", "aiohttp", "httpx", "Go-http-client",
	"curl/", "Wget", "libwww-perl", "Java/", "Apache-HttpClient",
}

//go:embed crawler-user-agents.json
var crawlerJSON []byte

type crawlerEntry struct {
	Pattern string `json:"pattern"`
}

// BotDetector layers UA-string checks cheap-first per doc 24 §Sec 1.3:
// (1) plain substring match, (2) regex match, (3) optional datacenter
// CIDR match. Each tier short-circuits on first hit so steady-state cost
// for legitimate traffic stays at one substring-loop pass.
type BotDetector struct {
	literal  []string
	compiled []*regexp.Regexp
	dcNets   []netip.Prefix
	logger   *slog.Logger
}

// NewBotDetector loads the embedded crawler JSON if present, otherwise
// falls back to the inline list. Either way the detector is non-nil.
func NewBotDetector(logger *slog.Logger) *BotDetector {
	b := &BotDetector{logger: logger}

	if len(crawlerJSON) > 0 {
		var entries []crawlerEntry
		if err := json.Unmarshal(crawlerJSON, &entries); err == nil && len(entries) > 0 {
			for _, e := range entries {
				b.addPattern(e.Pattern)
			}

			logger.Info("bot detector loaded from embedded JSON", "patterns", len(entries))

			return b
		}

		// Embed unmarshalled but produced zero entries — JSON shape changed.
		logger.Error("embedded crawler JSON empty or invalid; using fallback patterns",
			"embed_bytes", len(crawlerJSON), "see", "LEARN.md Lesson 23")
	} else {
		// Empty embed happens when the binary was built before
		// `make refresh-bot-patterns` ran (fresh checkouts) — that path is
		// intentional graceful degradation. But on a release build the
		// embed should be ~254 KB / 647 patterns; an empty embed there is
		// the regression class captured in LEARN.md Lesson 23. Surface the
		// byte count loudly so operators (and CI's --check-embed-sizes
		// flag) can catch it without depending on log-grep.
		logger.Error("embedded crawler JSON is empty; using fallback patterns",
			"embed_bytes", 0, "expected_min_bytes", crawlerJSONMinBytes,
			"see", "LEARN.md Lesson 23")
	}

	for _, p := range fallbackPatterns {
		b.addPattern(p)
	}

	logger.Info("bot detector loaded from fallback list", "patterns", len(b.literal))

	return b
}

// crawlerJSONMinBytes is the size floor for a release-build embed. Below
// this, the binary either was built before `make refresh-bot-patterns` ran
// (fresh checkout — fine) or hit the LEARN.md Lesson 23 //go:embed
// regression (release build — bug). The CLI flag --check-embed-sizes
// uses this threshold to fail CI loudly without changing runtime behavior.
const crawlerJSONMinBytes = 100 * 1024

// CrawlerEmbedBytes returns the size of the embedded crawler JSON. Used
// by `statnive-live --check-embed-sizes` and tests to assert the release
// build embedded the full upstream pattern set.
func CrawlerEmbedBytes() int { return len(crawlerJSON) }

// CrawlerEmbedMinBytes returns the size floor used by the --check-embed-sizes
// CI gate.
func CrawlerEmbedMinBytes() int { return crawlerJSONMinBytes }

// SetDatacenterCIDRs lets operators plug in an ASN/datacenter prefix list.
// Lookup is linear — fine for the tiny lists you'd ship by hand. v1.1
// will swap to a radix tree if a real ASN feed lands.
func (b *BotDetector) SetDatacenterCIDRs(prefixes []netip.Prefix) {
	b.dcNets = prefixes
}

// IsBot reports whether the given (UA, IP) pair looks like a bot.
// Empty UA is treated as bot — legitimate browsers always send one.
//
// Returns (isBot, reason) — reason is the layer that fired, useful for
// the bot_reason column landing in v1.1.
func (b *BotDetector) IsBot(userAgent, ip string) (bool, string) {
	if userAgent == "" {
		return true, "empty_ua"
	}

	for _, lit := range b.literal {
		if strings.Contains(userAgent, lit) {
			return true, "ua_literal"
		}
	}

	for _, re := range b.compiled {
		if re.MatchString(userAgent) {
			return true, "ua_regex"
		}
	}

	if len(b.dcNets) > 0 && ip != "" {
		if addr, err := netip.ParseAddr(ip); err == nil {
			for _, p := range b.dcNets {
				if p.Contains(addr) {
					return true, "datacenter_cidr"
				}
			}
		}
	}

	return false, ""
}

func (b *BotDetector) addPattern(p string) {
	if p == "" {
		return
	}

	if !strings.ContainsAny(p, `\^$*+?()[]{}|`) {
		b.literal = append(b.literal, p)

		return
	}

	re, err := regexp.Compile(p)
	if err != nil {
		b.logger.Debug("bad crawler regex", "pattern", p, "err", err)

		return
	}

	b.compiled = append(b.compiled, re)
}
