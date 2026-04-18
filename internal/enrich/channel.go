package enrich

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// Decision is what the pipeline writes onto EnrichedEvent for the
// referrer/channel pair. Channel labels follow GA4's bucket vocabulary
// so reports port cleanly to/from external dashboards.
type Decision struct {
	ReferrerName string
	Channel      string
}

// SourceEntry is one entry in config/sources.yaml.
type SourceEntry struct {
	Name    string   `yaml:"name"`
	Channel string   `yaml:"channel"` // referrer-bucket label, e.g. "Organic Search"
	Domains []string `yaml:"domains"`
}

type sourceDatabase struct {
	Sources []SourceEntry `yaml:"sources"`
}

// ChannelMapper classifies a (referrer, utm*, clickID) tuple into one of
// the 17 buckets in the doc 24 §3.1 decision tree. Reload is atomic via
// SIGHUP; lookups are lock-free (atomic.Pointer swap).
type ChannelMapper struct {
	path string
	db   atomic.Pointer[compiledDB]

	stopCh chan struct{}
}

type compiledDB struct {
	exact      map[string]SourceEntry
	suffixIdx  []suffixEntry
	searchSet  map[string]struct{} // hostnames classified as Organic Search
	socialSet  map[string]struct{} // hostnames classified as Social
	videoSet   map[string]struct{} // hostnames classified as Video
	shopSet    map[string]struct{} // hostnames classified as Shopping
	emailSet   map[string]struct{} // hostnames classified as Email
	aiSet      map[string]struct{} // hostnames classified as AI
	referralSet map[string]struct{} // hostnames classified as Referral
}

type suffixEntry struct {
	suffix string
	entry  SourceEntry
}

// Pre-compiled regexes from doc 24 §3.1. Inputs are lowercased once in
// step0Normalize, so the regexes themselves are case-sensitive — saves
// ~10–20 ns per match by skipping the (?i) flag's runtime case folding.
var (
	// `^(.*(([^a-df-z]|^)shop|shopping).*)$` — leading negative class
	// prevents `/workshop/` or `/bookshop/` from false-positiving.
	shoppingCampaignRegex = regexp.MustCompile(`(^|[^a-df-z])shop|shopping`)
	// `^(.*cp.*|ppc|retargeting|paid.*)$` — covers cpc/cpm/cpv/cpi/cpl/ppc.
	paidMediumRegex = regexp.MustCompile(`cp.*|ppc|retargeting|paid.*`)
)

// Display-medium tokens (Step 7).
var displayMediums = map[string]struct{}{
	"display": {}, "banner": {}, "expandable": {}, "interstitial": {}, "cpm": {},
}

// Social-medium tokens (Step 10) — Twitter/X, Mastodon, HubSpot, etc.
var socialMediums = map[string]struct{}{
	"social": {}, "social-network": {}, "social-media": {}, "sm": {},
	"social network": {}, "social media": {},
}

// Email tokens (Step 14) — checked across referrer, utm_source, utm_medium.
var emailTokens = map[string]struct{}{
	"email": {}, "e-mail": {}, "e_mail": {}, "e mail": {}, "gmail": {},
}

// Referral tokens (Step 13).
var referralMediums = map[string]struct{}{
	"referral": {}, "app": {}, "link": {},
}

// Push tokens (Step 15.4 — partial; full check uses suffix+contains too).
var pushSources = map[string]struct{}{
	"firebase": {},
}

// NewChannelMapper loads sources.yaml. Reload is driven by main.go's
// central SIGHUP listener (cmd/statnive-live/main.go:runSIGHUP) — having
// each subsystem install its own signal.Notify caused duplicate reloads
// and racy tests, so the mapper exposes Reload() for the caller to wire.
func NewChannelMapper(path string) (*ChannelMapper, error) {
	m := &ChannelMapper{path: path, stopCh: make(chan struct{})}
	if err := m.Reload(); err != nil {
		return nil, err
	}

	return m, nil
}

// Close is a no-op kept for API compatibility — the per-mapper SIGHUP
// listener was removed in Phase 2a. Safe to call multiple times.
func (m *ChannelMapper) Close() {
	select {
	case <-m.stopCh:
		// already closed
	default:
		close(m.stopCh)
	}
}

// Reload re-parses sources.yaml and atom-swaps the compiled DB.
func (m *ChannelMapper) Reload() error {
	raw, err := os.ReadFile(m.path)
	if err != nil {
		return fmt.Errorf("read sources: %w", err)
	}

	var sdb sourceDatabase
	if err := yaml.Unmarshal(raw, &sdb); err != nil {
		return fmt.Errorf("parse sources: %w", err)
	}

	cd := &compiledDB{
		exact:       make(map[string]SourceEntry, len(sdb.Sources)*2),
		searchSet:   make(map[string]struct{}),
		socialSet:   make(map[string]struct{}),
		videoSet:    make(map[string]struct{}),
		shopSet:     make(map[string]struct{}),
		emailSet:    make(map[string]struct{}),
		aiSet:       make(map[string]struct{}),
		referralSet: make(map[string]struct{}),
	}

	for _, s := range sdb.Sources {
		bucketSet := bucketSetFor(cd, s.Channel)

		for _, d := range s.Domains {
			d = strings.ToLower(strings.TrimSpace(d))
			if d == "" {
				continue
			}

			cd.exact[d] = s
			cd.suffixIdx = append(cd.suffixIdx, suffixEntry{suffix: "." + d, entry: s})

			if bucketSet != nil {
				bucketSet[d] = struct{}{}
			}
		}
	}

	m.db.Store(cd)

	return nil
}

func bucketSetFor(cd *compiledDB, channel string) map[string]struct{} {
	switch channel {
	case "Organic Search":
		return cd.searchSet
	case "Social":
		return cd.socialSet
	case "Video":
		return cd.videoSet
	case "Shopping":
		return cd.shopSet
	case "Email":
		return cd.emailSet
	case "AI":
		return cd.aiSet
	case "Referral":
		return cd.referralSet
	}

	return nil
}

// classCtx carries normalized inputs through the 17-step ladder. Lower-cased
// once at Step 0 so each subsequent map-lookup / strings.Contains is cheap.
type classCtx struct {
	db           *compiledDB
	referrer     string // stripped-www hostname
	referrerName string // normalized source name (e.g. "google" for any google.*)
	utmSource    string
	utmMedium    string
	utmCampaign  string
	clickID      string
	isPaidMedium bool
	isShopping   bool
}

// Classify runs the 17-step decision tree from doc 24 §3.1 and returns the
// resulting (referrer-name, channel) pair. The order of the steps is
// load-bearing — reordering breaks attribution.
func (m *ChannelMapper) Classify(referrer, utmSource, utmMedium, utmCampaign, clickID string) Decision {
	cd := m.db.Load()
	if cd == nil {
		return Decision{Channel: "Direct"}
	}

	ctx := step0Normalize(cd, referrer, utmSource, utmMedium, utmCampaign, clickID)

	for _, step := range steps {
		if label, matched := step(&ctx); matched {
			return Decision{ReferrerName: ctx.referrerName, Channel: label}
		}
	}

	return Decision{ReferrerName: ctx.referrerName, Channel: "Direct"}
}

// steps is the table-driven ladder. Adding a new branch is an insert at
// the correct index; the existing branches don't move.
var steps = []func(*classCtx) (string, bool){
	step1CrossNetwork,
	step2ComputePredicates,
	step3PaidShopping,
	step4PaidSearch,
	step5PaidSocial,
	step6PaidVideo,
	step7Display,
	step8PaidOther,
	step9OrganicShopping,
	step10OrganicSocial,
	step11OrganicVideo,
	step12OrganicSearch,
	step13Referral,
	step14Email,
	step15Misc,
}

// Step 0 — Normalize.
func step0Normalize(cd *compiledDB, referrer, utmSource, utmMedium, utmCampaign, clickID string) classCtx {
	c := classCtx{
		db:          cd,
		utmSource:   strings.ToLower(strings.TrimSpace(utmSource)),
		utmMedium:   strings.ToLower(strings.TrimSpace(utmMedium)),
		utmCampaign: strings.ToLower(strings.TrimSpace(utmCampaign)),
		clickID:     strings.TrimSpace(clickID),
	}

	if referrer != "" {
		c.referrer = strings.TrimPrefix(extractHostLower(referrer), "www.")
	}

	if c.referrer != "" {
		if entry, ok := cd.exact[c.referrer]; ok {
			c.referrerName = strings.ToLower(entry.Name)
		} else {
			for _, se := range cd.suffixIdx {
				if strings.HasSuffix(c.referrer, se.suffix) {
					c.referrerName = strings.ToLower(se.entry.Name)
					break
				}
			}
		}
	}

	return c
}

// Step 1 — Cross-network wins over everything (Performance Max).
func step1CrossNetwork(c *classCtx) (string, bool) {
	if strings.Contains(c.utmCampaign, "cross-network") {
		return "Cross-network", true
	}

	return "", false
}

// Step 2 — Compute shared predicates once.
func step2ComputePredicates(c *classCtx) (string, bool) {
	c.isPaidMedium = paidMediumRegex.MatchString(c.utmMedium)
	c.isShopping = inSet(c.db.shopSet, c.referrer) || shoppingCampaignRegex.MatchString(c.utmCampaign)

	return "", false
}

// Step 3 — Paid Shopping.
func step3PaidShopping(c *classCtx) (string, bool) {
	if c.isShopping && c.isPaidMedium {
		return "Paid Shopping", true
	}

	return "", false
}

// Step 4 — Paid Search.
func step4PaidSearch(c *classCtx) (string, bool) {
	if inSet(c.db.searchSet, c.referrer) && c.isPaidMedium {
		return "Paid Search", true
	}

	if c.referrerName == "google" && c.clickID == "(gclid)" {
		return "Paid Search", true
	}

	if c.referrerName == "bing" && c.clickID == "(msclkid)" {
		return "Paid Search", true
	}

	return "", false
}

// Step 5 — Paid Social.
func step5PaidSocial(c *classCtx) (string, bool) {
	if inSet(c.db.socialSet, c.referrer) && c.isPaidMedium {
		return "Paid Social", true
	}

	return "", false
}

// Step 6 — Paid Video.
func step6PaidVideo(c *classCtx) (string, bool) {
	if inSet(c.db.videoSet, c.referrer) && c.isPaidMedium {
		return "Paid Video", true
	}

	return "", false
}

// Step 7 — Display catch-all.
func step7Display(c *classCtx) (string, bool) {
	if _, ok := displayMediums[c.utmMedium]; ok {
		return "Display", true
	}

	return "", false
}

// Step 8 — Paid Other (any paid medium with no sub-channel match).
func step8PaidOther(c *classCtx) (string, bool) {
	if c.isPaidMedium {
		return "Paid Other", true
	}

	return "", false
}

// Step 9 — Organic Shopping.
func step9OrganicShopping(c *classCtx) (string, bool) {
	if c.isShopping {
		return "Organic Shopping", true
	}

	return "", false
}

// Step 10 — Organic Social.
func step10OrganicSocial(c *classCtx) (string, bool) {
	if inSet(c.db.socialSet, c.referrer) {
		return "Organic Social", true
	}

	if _, ok := socialMediums[c.utmMedium]; ok {
		return "Organic Social", true
	}

	return "", false
}

// Step 11 — Organic Video.
func step11OrganicVideo(c *classCtx) (string, bool) {
	if inSet(c.db.videoSet, c.referrer) {
		return "Organic Video", true
	}

	if strings.Contains(c.utmMedium, "video") {
		return "Organic Video", true
	}

	return "", false
}

// Step 12 — Organic Search.
func step12OrganicSearch(c *classCtx) (string, bool) {
	if inSet(c.db.searchSet, c.referrer) {
		return "Organic Search", true
	}

	if c.utmMedium == "organic" {
		return "Organic Search", true
	}

	return "", false
}

// Step 13 — Referral.
func step13Referral(c *classCtx) (string, bool) {
	if _, ok := referralMediums[c.utmMedium]; ok {
		return "Referral", true
	}

	if c.referrer != "" && inSet(c.db.referralSet, c.referrer) {
		return "Referral", true
	}

	return "", false
}

// Step 14 — Email.
func step14Email(c *classCtx) (string, bool) {
	if _, ok := emailTokens[c.referrer]; ok {
		return "Email", true
	}

	if _, ok := emailTokens[c.utmSource]; ok {
		return "Email", true
	}

	if _, ok := emailTokens[c.utmMedium]; ok {
		return "Email", true
	}

	if inSet(c.db.emailSet, c.referrer) {
		return "Email", true
	}

	return "", false
}

// Step 15 — Affiliates / Audio / SMS / Push / AI.
// Single-token branches in this exact order — see doc 24 §3.1 Step 15.
func step15Misc(c *classCtx) (string, bool) {
	switch c.utmMedium {
	case "affiliate":
		return "Affiliates", true
	case "audio":
		return "Audio", true
	}

	if c.referrer == "sms" || c.utmSource == "sms" || c.utmMedium == "sms" {
		return "SMS", true
	}

	if strings.HasSuffix(c.utmMedium, "push") ||
		strings.Contains(c.utmMedium, "mobile") ||
		strings.Contains(c.utmMedium, "notification") {
		return "Mobile Push Notifications", true
	}

	if _, ok := pushSources[c.referrer]; ok {
		return "Mobile Push Notifications", true
	}

	if _, ok := pushSources[c.utmSource]; ok {
		return "Mobile Push Notifications", true
	}

	if inSet(c.db.aiSet, c.referrer) {
		return "AI", true
	}

	if _, ok := c.db.aiSet[c.utmSource]; ok {
		return "AI", true
	}

	return "", false
}

// extractHostLower pulls the hostname out of a URL-shaped string without
// allocating the full url.URL net/url.Parse builds. Handles
// "scheme://host/path", "//host/path", "host/path", "host", "host:port",
// "[ipv6]:port", and "user@host". Returns lowercase. Never panics.
func extractHostLower(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else if strings.HasPrefix(s, "//") {
		s = s[2:]
	}

	if cut := strings.IndexAny(s, "/?#"); cut >= 0 {
		s = s[:cut]
	}

	if at := strings.LastIndexByte(s, '@'); at >= 0 {
		s = s[at+1:]
	}

	if rb := strings.IndexByte(s, ']'); rb >= 0 {
		// IPv6: keep what's between the brackets.
		if lb := strings.IndexByte(s, '['); lb >= 0 && lb < rb {
			return strings.ToLower(s[lb+1 : rb])
		}
	} else if c := strings.LastIndexByte(s, ':'); c >= 0 {
		s = s[:c]
	}

	return strings.ToLower(s)
}

func inSet(set map[string]struct{}, key string) bool {
	if key == "" {
		return false
	}

	_, ok := set[key]

	return ok
}
