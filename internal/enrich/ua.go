package enrich

import "github.com/medama-io/go-useragent"

// UAInfo is the subset of UA parser output the pipeline writes onto the
// EnrichedEvent. Browser/OS/Device come from medama-io's parser; IsBot
// is a coarse signal that the pipeline's BotDetector layers on top.
type UAInfo struct {
	Browser string
	OS      string
	Device  string // desktop | mobile | tablet | tv | bot | unknown
	IsBot   bool
}

// UAParser wraps medama-io/go-useragent. The constructor allocates a trie
// (~10 MB) so the parser MUST be a singleton — see docs/tech-docs/go-useragent.md.
type UAParser struct {
	p *useragent.Parser
}

// NewUAParser allocates the singleton parser.
func NewUAParser() *UAParser {
	return &UAParser{p: useragent.NewParser()}
}

// Parse is safe for concurrent use. Empty input short-circuits to a known
// "unknown" UAInfo so the pipeline never has to nil-check.
func (u *UAParser) Parse(s string) UAInfo {
	if s == "" {
		return UAInfo{Device: "unknown"}
	}

	a := u.p.Parse(s)

	dev := "unknown"

	switch {
	case a.IsBot():
		dev = "bot"
	case a.IsTablet():
		dev = "tablet"
	case a.IsMobile():
		dev = "mobile"
	case a.IsTV():
		dev = "tv"
	case a.IsDesktop():
		dev = "desktop"
	}

	return UAInfo{
		Browser: string(a.Browser()),
		OS:      string(a.OS()),
		Device:  dev,
		IsBot:   a.IsBot(),
	}
}
