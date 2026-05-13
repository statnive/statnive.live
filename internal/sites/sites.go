// Package sites is the DAO for the statnive.sites table. Hostname → site_id
// resolution happens on every incoming event in the hot path; callers are
// expected to cache.
//
// v1 slice: lookup + list. Phase 6-polish added CreateSite / ListAdmin /
// UpdateSiteEnabled for the operator first-run UX. Phase 11 SaaS signup
// reuses GenerateSlug / IsSlugAvailable / ReserveSlug and a future signup-
// specific CreateSite variant.
package sites

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// ErrUnknownHostname is returned when no row in statnive.sites matches the
// request hostname. Callers map this to HTTP 400.
var ErrUnknownHostname = errors.New("unknown hostname")

// ErrHostnameTaken is returned when a CreateSite tries to claim a hostname
// already present in statnive.sites. The API maps this to HTTP 409.
var ErrHostnameTaken = errors.New("sites: hostname taken")

// ErrInvalidHostname is returned when a CreateSite rejects malformed
// input — empty, too long, or containing characters that the hostname
// validator on /api/event would itself reject. Maps to HTTP 400.
var ErrInvalidHostname = errors.New("sites: invalid hostname")

// ErrInvalidCurrency is returned when a CreateSite or UpdateSiteAttributes
// receives a currency code outside the allow-list in currencies.go.
// Maps to HTTP 400.
var ErrInvalidCurrency = errors.New("sites: invalid currency")

// ErrInvalidTimezone is returned when a CreateSite or UpdateSiteAttributes
// receives a timezone outside the allow-list in timezones.go (or one
// that fails time.LoadLocation). Maps to HTTP 400.
var ErrInvalidTimezone = errors.New("sites: invalid timezone")

// Site is the JSON-serialized row the dashboard's site-switcher consumes.
// TZ is an IANA zone name — the dashboard's date picker renders midnight
// boundaries in this zone (default Europe/Berlin for SaaS, operator-set
// per tenant). Currency is an ISO 4217 alpha-3 code used as a display
// label by the SPA's Intl.NumberFormat call (default EUR).
type Site struct {
	ID       uint32 `json:"id"`
	Hostname string `json:"hostname"`
	Enabled  bool   `json:"enabled"`
	TZ       string `json:"tz"`
	Currency string `json:"currency"`
}

// SitePolicy is the per-site privacy + bot-tracking + jurisdiction
// posture, written to statnive.sites by migrations 006 + 013.
//
// Defaults (operator unset): RespectDNT=false, RespectGPC=false,
// TrackBots=true, Jurisdiction=OTHER-NON-EU, ConsentMode=permissive —
// preserves the post-PR-#78 SaaS posture (count every visit, suppress
// identity only on operator-flipped opt-in).
//
// The Jurisdiction + ConsentMode fields are the Stage 3 cross-cutting
// knobs that drive privacy.PolicyToMode. Defaults are backfilled by
// migration 013 so existing operators keep byte-for-byte identical
// tracking behaviour until they consciously flip.
type SitePolicy struct {
	RespectDNT     bool     `json:"respect_dnt"`
	RespectGPC     bool     `json:"respect_gpc"`
	TrackBots      bool     `json:"track_bots"`
	Jurisdiction   string   `json:"jurisdiction"`
	ConsentMode    string   `json:"consent_mode"`
	EventAllowlist []string `json:"event_allowlist"`
}

// Jurisdiction enum values. Kept as string constants (not a typed
// alias) so the admin POST/PATCH JSON decode stays simple — Validate
// catches the bad strings.
const (
	JurisdictionDE          = "DE"
	JurisdictionFR          = "FR"
	JurisdictionIT          = "IT"
	JurisdictionES          = "ES"
	JurisdictionNL          = "NL"
	JurisdictionBE          = "BE"
	JurisdictionIE          = "IE"
	JurisdictionUK          = "UK"
	JurisdictionOtherEU     = "OTHER-EU"
	JurisdictionOtherNonEU  = "OTHER-NON-EU"
	JurisdictionIR          = "IR"
)

// ConsentMode enum values. Same string-not-typed-alias rationale.
const (
	ConsentModePermissive      = "permissive"
	ConsentModeConsentFree     = "consent-free"
	ConsentModeConsentRequired = "consent-required"
	ConsentModeHybrid          = "hybrid"
)

// validJurisdictions is the closed set of accepted jurisdiction codes.
// New entries require a Validate() update and an admin-UI dropdown
// extension — keep it deliberate.
var validJurisdictions = map[string]struct{}{
	JurisdictionDE: {}, JurisdictionFR: {}, JurisdictionIT: {},
	JurisdictionES: {}, JurisdictionNL: {}, JurisdictionBE: {},
	JurisdictionIE: {}, JurisdictionUK: {},
	JurisdictionOtherEU: {}, JurisdictionOtherNonEU: {}, JurisdictionIR: {},
}

var validConsentModes = map[string]struct{}{
	ConsentModePermissive:      {},
	ConsentModeConsentFree:     {},
	ConsentModeConsentRequired: {},
	ConsentModeHybrid:          {},
}

// euJurisdictions lists the codes that recognise the GDPR + the
// CNIL-style audience-measurement exemption. UK is included (post-
// Brexit it still follows UK GDPR — for our purposes the same
// hybrid/consent-free options apply).
var euJurisdictions = map[string]struct{}{
	JurisdictionDE: {}, JurisdictionFR: {}, JurisdictionIT: {},
	JurisdictionES: {}, JurisdictionNL: {}, JurisdictionBE: {},
	JurisdictionIE: {}, JurisdictionUK: {}, JurisdictionOtherEU: {},
}

// Validate enforces the cross-field invariants the admin handler and
// the migration backfill MUST hold. Reasoning is encoded in the
// returned error so the admin UI can surface it verbatim.
func (p SitePolicy) Validate() error {
	if p.Jurisdiction == "" {
		// Empty is treated as the backfill default. Other layers
		// (CreateSite, the admin PATCH decoder) refuse empty
		// upfront; Validate just leaves it alone for migrated rows.
		return nil
	}

	if _, ok := validJurisdictions[p.Jurisdiction]; !ok {
		return fmt.Errorf("sites: invalid jurisdiction %q", p.Jurisdiction)
	}

	if p.ConsentMode != "" {
		if _, ok := validConsentModes[p.ConsentMode]; !ok {
			return fmt.Errorf("sites: invalid consent_mode %q", p.ConsentMode)
		}
	}

	// Germany under TDDDG § 25: no consent-free path that uses
	// client-side storage. consent-free (server-only) or hybrid
	// (consent-gated upgrade) are the only safe defaults; explicit
	// permissive on a DE site is a hard reject.
	if p.Jurisdiction == JurisdictionDE && p.ConsentMode == ConsentModePermissive {
		return fmt.Errorf("sites: jurisdiction=DE requires consent_mode in {consent-free, hybrid, consent-required}, got permissive")
	}

	// Hybrid only makes sense in jurisdictions where consent has a
	// legal effect (the EU GDPR set). Outside the set, the
	// pre-consent vs post-consent split is theatre without a legal
	// hook to anchor it.
	if p.ConsentMode == ConsentModeHybrid {
		if _, ok := euJurisdictions[p.Jurisdiction]; !ok {
			return fmt.Errorf("sites: consent_mode=hybrid only valid in EU jurisdictions, got %s", p.Jurisdiction)
		}

		if n := len(p.EventAllowlist); n < 1 || n > 3 {
			return fmt.Errorf("sites: consent_mode=hybrid requires 1-3 event_allowlist entries (CNIL cap), got %d", n)
		}
	}

	// consent-free also lives under the CNIL 3-event cap.
	if p.ConsentMode == ConsentModeConsentFree && len(p.EventAllowlist) > 3 {
		return fmt.Errorf("sites: consent_mode=consent-free caps event_allowlist at 3 entries (CNIL), got %d", len(p.EventAllowlist))
	}

	return nil
}

// DerivedConsentMode returns the recommended consent_mode for a fresh
// site based on the operator's chosen jurisdiction. Used by
// sites.CreateSite when the operator doesn't specify a mode explicitly.
// Never auto-applies hybrid (per the plan's Stage-3 contract: hybrid
// is opt-in).
func DerivedConsentMode(jurisdiction string) string {
	switch jurisdiction {
	case JurisdictionDE:
		return ConsentModeConsentFree
	case JurisdictionFR, JurisdictionIT, JurisdictionES, JurisdictionNL,
		JurisdictionBE, JurisdictionIE, JurisdictionUK, JurisdictionOtherEU:
		return ConsentModeConsentFree
	default:
		return ConsentModePermissive
	}
}

// SiteAdmin is the richer projection returned to /api/admin/sites. Adds
// slug + plan + created_at + per-site privacy/bot policy on top of Site
// for the Admin UI. The policy fields drive the three Site Settings
// checkboxes (CLAUDE.md Privacy Rule 6 + migration 006).
type SiteAdmin struct {
	Site
	SitePolicy

	Slug      string `json:"slug"`
	Plan      string `json:"plan"`
	CreatedAt int64  `json:"created_at"`
}

// Registry resolves hostnames to site_id. Backed by a live ClickHouse
// connection; the in-memory cache is deliberately deferred until we have
// benchmarks that show it matters.
type Registry struct {
	conn driver.Conn
}

// New constructs a Registry on top of an existing ClickHouse connection.
func New(conn driver.Conn) *Registry {
	return &Registry{conn: conn}
}

// LookupSiteIDByHostname returns the site_id for a given hostname, or
// ErrUnknownHostname if none is registered / enabled. Kept for callers
// that don't yet need the per-site policy (admin paths, tests). The
// hot ingest path uses LookupSitePolicy instead.
func (r *Registry) LookupSiteIDByHostname(ctx context.Context, hostname string) (uint32, error) {
	siteID, _, err := r.LookupSitePolicy(ctx, hostname)

	return siteID, err
}

// LookupSitePolicy returns the site_id AND the per-site privacy +
// bot-tracking policy in a single round-trip. The hot ingest path
// (internal/ingest/handler.go) calls this so the consent gate +
// burst-vs-bot decision happen against per-site flags rather than the
// (now-removed) global cfg.consent.respect_* surface.
//
// Returns ErrUnknownHostname if no enabled row matches. Sites without
// migration 006 columns yet (very old deployments mid-upgrade) get the
// default policy (count every visit, no DNT/GPC suppression, bots
// flagged-not-dropped).
func (r *Registry) LookupSitePolicy(ctx context.Context, hostname string) (uint32, SitePolicy, error) {
	host := NormalizeHostname(hostname)
	if host == "" {
		return 0, SitePolicy{}, ErrUnknownHostname
	}

	siteID, policy, err := r.lookupExactPolicy(ctx, host)
	// Retry once with the leading "www." stripped so a customer who
	// registers televika.com is still resolved when the tracker payload
	// reports www.televika.com (CF-fronted bare→www redirects are common).
	// Try the literal first so an explicitly-seeded "www.foo.com" row
	// still wins over its bare counterpart.
	if errors.Is(err, ErrUnknownHostname) && strings.HasPrefix(host, "www.") {
		siteID, policy, err = r.lookupExactPolicy(ctx, strings.TrimPrefix(host, "www."))
	}

	return siteID, policy, err
}

func (r *Registry) lookupExactPolicy(ctx context.Context, host string) (uint32, SitePolicy, error) {
	var (
		siteID                            uint32
		respectDNT, respectGPC, trackBots uint8
		jurisdiction, consentMode         string
		eventAllowlistJSON                string
	)

	row := r.conn.QueryRow(ctx,
		`SELECT site_id, respect_dnt, respect_gpc, track_bots,
		        jurisdiction, consent_mode, event_allowlist
		 FROM statnive.sites
		 WHERE hostname = ? AND enabled = 1 LIMIT 1`,
		host,
	)

	if err := row.Scan(&siteID, &respectDNT, &respectGPC, &trackBots,
		&jurisdiction, &consentMode, &eventAllowlistJSON); err != nil {
		// ClickHouse driver returns a generic error on no-rows; treat any
		// scan failure as ErrUnknownHostname. The handler then 204s the
		// event silently. Real connection failures bubble through ping/health.
		return 0, SitePolicy{}, ErrUnknownHostname
	}

	if siteID == 0 {
		return 0, SitePolicy{}, ErrUnknownHostname
	}

	return siteID, SitePolicy{
		RespectDNT:     respectDNT != 0,
		RespectGPC:     respectGPC != 0,
		TrackBots:      trackBots != 0,
		Jurisdiction:   jurisdiction,
		ConsentMode:    consentMode,
		EventAllowlist: parseEventAllowlist(eventAllowlistJSON),
	}, nil
}

// parseEventAllowlist decodes the JSON-encoded list stored in
// statnive.sites.event_allowlist. Malformed JSON or empty input
// returns a nil slice (no enforcement) — Privacy Rule 9 style:
// failures default toward more-permissive ingest, not silent drop.
func parseEventAllowlist(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}

	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}

	return out
}

// NormalizeHostname coerces a tracker-supplied hostname into the registry
// shape (lowercase, no scheme, no path/port). Tracker payloads occasionally
// leak "https://", trailing slashes, or :port suffixes from misconfigured
// integrations; without normalization those events drop silently as
// hostname_unknown. Mirrors `extractHostLower` in internal/enrich/channel.go;
// kept separate to avoid an inverted layering dependency (sites → enrich).
func NormalizeHostname(raw string) string {
	h := strings.TrimSpace(raw)

	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	} else {
		h = strings.TrimPrefix(h, "//")
	}

	if cut := strings.IndexAny(h, "/?#"); cut >= 0 {
		h = h[:cut]
	}

	// userinfo@ leaks from misconfigured trackers passing href instead of hostname.
	if at := strings.LastIndexByte(h, '@'); at >= 0 {
		h = h[at+1:]
	}

	// IPv6 literal: keep the address between the brackets and drop the :port.
	// Bare colons in non-bracketed input mean :port (FQDNs never contain ':').
	if rb := strings.IndexByte(h, ']'); rb >= 0 {
		if lb := strings.IndexByte(h, '['); lb >= 0 && lb < rb {
			h = h[lb+1 : rb]
		}
	} else if c := strings.LastIndexByte(h, ':'); c >= 0 {
		h = h[:c]
	}

	return strings.ToLower(h)
}

// ValidateHostname runs the same cheap shape checks the /api/event
// handler applies at ingest. Exported so fakeSitesStore + admin
// handler tests validate identically to the production Registry.
func ValidateHostname(h string) error {
	h = strings.TrimSpace(h)
	if h == "" || len(h) > 253 {
		return ErrInvalidHostname
	}

	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-':
		default:
			return ErrInvalidHostname
		}
	}

	return nil
}

// normalizeAttribute resolves a TZ or currency to a validated value or
// returns the matching sentinel error. Empty input falls back to
// fallback; non-empty input is checked against valid(...).
func normalizeAttribute(raw, fallback string, valid func(string) bool, invalid error) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return fallback, nil
	}

	if !valid(v) {
		return "", invalid
	}

	return v, nil
}

// CreateSite inserts a new row in statnive.sites. Returns ErrHostnameTaken
// if the hostname already exists (any enabled flag), ErrSlugTaken if the
// proposed slug collides with another site's row, ErrInvalidHostname
// for shape-invalid input, ErrInvalidCurrency for unknown currency codes,
// or ErrInvalidTimezone for unknown IANA zones. Empty tz/currency fall
// back to DefaultTimezone / DefaultCurrency. Site ID is allocated
// server-side via a max(site_id)+1 select — safe on single-node;
// Phase 11 SaaS will revisit once multi-writer contention matters.
func (r *Registry) CreateSite(ctx context.Context, hostname, slug, tz, currency string) (uint32, error) {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if err := ValidateHostname(hostname); err != nil {
		return 0, err
	}

	if _, err := r.LookupSiteIDByHostname(ctx, hostname); err == nil {
		return 0, ErrHostnameTaken
	}

	slug, err := r.resolveSlug(ctx, hostname, slug)
	if err != nil {
		return 0, err
	}

	tz, err = normalizeAttribute(tz, DefaultTimezone, IsValidTimezone, ErrInvalidTimezone)
	if err != nil {
		return 0, err
	}

	currency, err = normalizeAttribute(currency, DefaultCurrency, IsValidCurrency, ErrInvalidCurrency)
	if err != nil {
		return 0, err
	}

	var maxID uint32

	if err := r.conn.QueryRow(ctx,
		`SELECT coalesce(max(site_id), 0) FROM statnive.sites`,
	).Scan(&maxID); err != nil {
		return 0, fmt.Errorf("sites max id: %w", err)
	}

	siteID := maxID + 1

	if err := r.conn.Exec(ctx,
		`INSERT INTO statnive.sites (site_id, hostname, slug, plan, enabled, created_at, tz, currency)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		siteID, hostname, slug, "free", uint8(1), time.Now().UTC(), tz, currency,
	); err != nil {
		return 0, fmt.Errorf("sites insert: %w", err)
	}

	return siteID, nil
}

// resolveSlug normalizes the operator-supplied slug (or auto-generates
// from hostname when empty) and asserts uniqueness against the
// reserved set + the existing rows.
func (r *Registry) resolveSlug(ctx context.Context, hostname, slug string) (string, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		slug = GenerateSlug(hostname)
	}

	if slug == "" {
		return "", ErrInvalidHostname
	}

	if _, reserved := reservedSlugs[slug]; reserved {
		return "", ErrSlugTaken
	}

	ok, err := r.IsSlugAvailable(ctx, slug)
	if err != nil {
		return "", err
	}

	if !ok {
		return "", ErrSlugTaken
	}

	return slug, nil
}

// UpdateSiteAttributes mutates the per-site display attributes
// (currency + tz). Either field can be nil to leave it unchanged. Both
// are validated against the allow-lists before the ALTER UPDATE runs;
// invalid input returns ErrInvalidCurrency / ErrInvalidTimezone before
// ClickHouse is touched. ErrUnknownHostname semantics for non-existent
// site_id, mirroring UpdateSitePolicy / UpdateSiteEnabled.
func (r *Registry) UpdateSiteAttributes(ctx context.Context, siteID uint32, currency, tz *string) error {
	if siteID == 0 {
		return ErrUnknownHostname
	}

	if currency == nil && tz == nil {
		return nil
	}

	if currency != nil && !IsValidCurrency(*currency) {
		return ErrInvalidCurrency
	}

	if tz != nil && !IsValidTimezone(*tz) {
		return ErrInvalidTimezone
	}

	var exists uint64

	if err := r.conn.QueryRow(ctx,
		`SELECT count() FROM statnive.sites WHERE site_id = ?`,
		siteID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("sites existence: %w", err)
	}

	if exists == 0 {
		return ErrUnknownHostname
	}

	sets := make([]string, 0, 2)
	args := make([]any, 0, 3)

	if currency != nil {
		sets = append(sets, "currency = ?")
		args = append(args, *currency)
	}

	if tz != nil {
		sets = append(sets, "tz = ?")
		args = append(args, *tz)
	}

	args = append(args, siteID)

	stmt := fmt.Sprintf(
		`ALTER TABLE statnive.sites UPDATE %s WHERE site_id = ? SETTINGS mutations_sync = 2`,
		strings.Join(sets, ", "),
	)

	if err := r.conn.Exec(ctx, stmt, args...); err != nil {
		return fmt.Errorf("sites update attributes: %w", err)
	}

	return nil
}

// UpdateSitePolicy mutates the per-site privacy + bot flags
// (statnive.sites.respect_dnt / respect_gpc / track_bots — migration
// 006). Uses synchronous ALTER UPDATE so the change is visible to the
// next LookupSitePolicy call. ErrUnknownHostname semantics for
// non-existent site_id.
func (r *Registry) UpdateSitePolicy(ctx context.Context, siteID uint32, policy SitePolicy) error {
	if siteID == 0 {
		return ErrUnknownHostname
	}

	var exists uint64

	if err := r.conn.QueryRow(ctx,
		`SELECT count() FROM statnive.sites WHERE site_id = ?`,
		siteID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("sites existence: %w", err)
	}

	if exists == 0 {
		return ErrUnknownHostname
	}

	sets := []string{"respect_dnt = ?", "respect_gpc = ?", "track_bots = ?"}
	args := []any{
		boolU8(policy.RespectDNT), boolU8(policy.RespectGPC), boolU8(policy.TrackBots),
	}

	// Stage-3 columns. Empty strings are treated as "no change" so
	// existing callers that only know about the three privacy bools
	// (PR D2 / migration 006) keep working unchanged. The admin
	// handler explicitly populates these fields when the operator
	// patches them.
	if policy.Jurisdiction != "" {
		sets = append(sets, "jurisdiction = ?")
		args = append(args, policy.Jurisdiction)
	}

	if policy.ConsentMode != "" {
		sets = append(sets, "consent_mode = ?")
		args = append(args, policy.ConsentMode)
	}

	if policy.EventAllowlist != nil {
		buf, err := json.Marshal(policy.EventAllowlist)
		if err != nil {
			return fmt.Errorf("sites marshal event_allowlist: %w", err)
		}

		sets = append(sets, "event_allowlist = ?")
		args = append(args, string(buf))
	}

	args = append(args, siteID)

	stmt := fmt.Sprintf(
		`ALTER TABLE statnive.sites UPDATE %s WHERE site_id = ? SETTINGS mutations_sync = 2`,
		strings.Join(sets, ", "),
	)

	if err := r.conn.Exec(ctx, stmt, args...); err != nil {
		return fmt.Errorf("sites update policy: %w", err)
	}

	return nil
}

func boolU8(b bool) uint8 {
	if b {
		return 1
	}

	return 0
}

// UpdateSiteEnabled toggles the enabled flag for an existing row. Uses a
// synchronous ALTER UPDATE mutation so the change is visible to the next
// LookupSiteIDByHostname call. Returns ErrUnknownHostname semantics
// (site not found) when site_id doesn't exist.
func (r *Registry) UpdateSiteEnabled(ctx context.Context, siteID uint32, enabled bool) error {
	if siteID == 0 {
		return ErrUnknownHostname
	}

	var exists uint64

	if err := r.conn.QueryRow(ctx,
		`SELECT count() FROM statnive.sites WHERE site_id = ?`,
		siteID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("sites existence: %w", err)
	}

	if exists == 0 {
		return ErrUnknownHostname
	}

	flag := uint8(0)
	if enabled {
		flag = 1
	}

	if err := r.conn.Exec(ctx,
		`ALTER TABLE statnive.sites UPDATE enabled = ? WHERE site_id = ? SETTINGS mutations_sync = 2`,
		flag, siteID,
	); err != nil {
		return fmt.Errorf("sites update enabled: %w", err)
	}

	return nil
}

// LookupSiteByID returns a single SiteAdmin row keyed by site_id, the
// efficient single-row complement to ListAdmin used by admin PATCH
// flows that need to read-modify-write a specific site without
// scanning the whole table. Returns ErrUnknownHostname (semantic
// site-not-found) when site_id doesn't exist.
func (r *Registry) LookupSiteByID(ctx context.Context, siteID uint32) (SiteAdmin, error) {
	if siteID == 0 {
		return SiteAdmin{}, ErrUnknownHostname
	}

	var (
		sa                                         SiteAdmin
		enabled, respectDNT, respectGPC, trackBots uint8
		jurisdiction, consentMode                  string
		eventAllowlistJSON                         string
	)

	row := r.conn.QueryRow(ctx,
		`SELECT site_id, hostname, slug, plan, enabled, tz, currency,
		        toInt64(toUnixTimestamp(created_at)),
		        respect_dnt, respect_gpc, track_bots,
		        jurisdiction, consent_mode, event_allowlist
		 FROM statnive.sites WHERE site_id = ? LIMIT 1`,
		siteID,
	)

	if err := row.Scan(
		&sa.ID, &sa.Hostname, &sa.Slug, &sa.Plan, &enabled, &sa.TZ, &sa.Currency, &sa.CreatedAt,
		&respectDNT, &respectGPC, &trackBots,
		&jurisdiction, &consentMode, &eventAllowlistJSON,
	); err != nil {
		return SiteAdmin{}, ErrUnknownHostname
	}

	if sa.ID == 0 {
		return SiteAdmin{}, ErrUnknownHostname
	}

	sa.Enabled = enabled != 0
	sa.RespectDNT = respectDNT != 0
	sa.RespectGPC = respectGPC != 0
	sa.TrackBots = trackBots != 0
	sa.Jurisdiction = jurisdiction
	sa.ConsentMode = consentMode
	sa.EventAllowlist = parseEventAllowlist(eventAllowlistJSON)

	return sa, nil
}

// ListAdmin returns every site with the richer SiteAdmin projection —
// adds slug, plan, created_at, and the per-site privacy + bot policy
// (migration 006) on top of List() for /api/admin/sites.
func (r *Registry) ListAdmin(ctx context.Context) ([]SiteAdmin, error) {
	// statnive.sites is plain MergeTree — FINAL is rejected. Duplicate
	// rows can only appear if migration 001 changes engines; the
	// integration test pins that invariant.
	rows, err := r.conn.Query(ctx,
		`SELECT site_id, hostname, slug, plan, enabled, tz, currency,
		        toInt64(toUnixTimestamp(created_at)),
		        respect_dnt, respect_gpc, track_bots,
		        jurisdiction, consent_mode, event_allowlist
		 FROM statnive.sites ORDER BY site_id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("sites list admin: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []SiteAdmin

	for rows.Next() {
		var (
			sa                                         SiteAdmin
			enabled, respectDNT, respectGPC, trackBots uint8
			jurisdiction, consentMode                  string
			eventAllowlistJSON                         string
		)

		if scanErr := rows.Scan(
			&sa.ID, &sa.Hostname, &sa.Slug, &sa.Plan, &enabled, &sa.TZ, &sa.Currency, &sa.CreatedAt,
			&respectDNT, &respectGPC, &trackBots,
			&jurisdiction, &consentMode, &eventAllowlistJSON,
		); scanErr != nil {
			return nil, fmt.Errorf("sites scan admin: %w", scanErr)
		}

		sa.Enabled = enabled != 0
		sa.RespectDNT = respectDNT != 0
		sa.RespectGPC = respectGPC != 0
		sa.TrackBots = trackBots != 0
		sa.Jurisdiction = jurisdiction
		sa.ConsentMode = consentMode
		sa.EventAllowlist = parseEventAllowlist(eventAllowlistJSON)

		out = append(out, sa)
	}

	return out, rows.Err()
}

// List returns every registered site, ordered by site_id for stable UI
// render. Includes disabled rows — the dashboard renders them greyed out
// so the operator can see the full tenancy picture.
func (r *Registry) List(ctx context.Context) ([]Site, error) {
	rows, err := r.conn.Query(ctx,
		`SELECT site_id, hostname, enabled, tz, currency FROM statnive.sites ORDER BY site_id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("sites list: %w", err)
	}

	defer func() { _ = rows.Close() }()

	var out []Site

	for rows.Next() {
		var s Site

		var enabled uint8

		if scanErr := rows.Scan(&s.ID, &s.Hostname, &enabled, &s.TZ, &s.Currency); scanErr != nil {
			return nil, fmt.Errorf("sites scan: %w", scanErr)
		}

		s.Enabled = enabled != 0

		out = append(out, s)
	}

	return out, rows.Err()
}
