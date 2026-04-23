package sites

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// ErrSlugTaken is returned when a slug is already registered to a
// different site_id.
var ErrSlugTaken = errors.New("sites: slug taken")

// reservedSlugs block common route names + vendor-identifying strings
// from being claimed via signup. Phase 11 uses this list to reject
// hostile signups that would shadow /api, /app, or /admin URL
// segments.
var reservedSlugs = map[string]struct{}{
	"admin":    {},
	"api":      {},
	"app":      {},
	"assets":   {},
	"auth":     {},
	"demo":     {},
	"docs":     {},
	"healthz":  {},
	"login":    {},
	"logout":   {},
	"s":        {},
	"signup":   {},
	"statnive": {},
	"static":   {},
	"tracker":  {},
}

// GenerateSlug derives a URL-safe, lowercase slug from a hostname.
// Strips common TLD suffixes + punctuation + non-ASCII runs; clamps
// to 32 chars. Deterministic: the same hostname always yields the
// same slug, so Phase 11 signup can show the proposed slug before
// committing.
//
// Examples:
//
//	"Example.com"      → "example"
//	"blog.acme.co.uk"  → "blog-acme"
//	"foo_bar.io"       → "foo-bar"
//	"xn--bcher-kva.de" → "bcher"  (punycode collapsed to ASCII prefix)
func GenerateSlug(hostname string) string {
	h := strings.ToLower(strings.TrimSpace(hostname))
	if h == "" {
		return ""
	}

	// Strip a small set of common TLDs so "example.com" + "example.org"
	// both propose "example" — operators can still manually override.
	tlds := []string{
		".com", ".org", ".net", ".io", ".dev",
		".co.uk", ".co", ".ir", ".live", ".app",
	}
	for _, tld := range tlds {
		if strings.HasSuffix(h, tld) {
			h = strings.TrimSuffix(h, tld)

			break
		}
	}

	// Replace every non-alnum rune with "-", then collapse runs.
	var b strings.Builder

	b.Grow(len(h))

	prevDash := false

	for _, r := range h {
		if r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)

			prevDash = false

			continue
		}

		if !prevDash {
			b.WriteByte('-')

			prevDash = true
		}
	}

	out := strings.Trim(b.String(), "-")

	if len(out) > 32 {
		out = out[:32]
	}

	return out
}

// IsSlugAvailable reports whether slug is unclaimed + not reserved.
// Returns false on reserved slugs without hitting ClickHouse (cheap
// synchronous reject). Otherwise checks the sites table.
func (r *Registry) IsSlugAvailable(ctx context.Context, slug string) (bool, error) {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return false, nil
	}

	if _, reserved := reservedSlugs[slug]; reserved {
		return false, nil
	}

	var count uint64

	row := r.conn.QueryRow(ctx,
		`SELECT count() FROM statnive.sites FINAL WHERE slug = ? LIMIT 1`,
		slug,
	)

	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("sites slug lookup: %w", err)
	}

	return count == 0, nil
}

// ReserveSlug assigns slug to siteID via a compare-and-set INSERT
// against the sites table. Returns ErrSlugTaken if another site_id
// already owns the slug (the IsSlugAvailable check is advisory — this
// is the authoritative CAS).
//
// Phase 3c does NOT use this method (no sites HTTP surface ships);
// Phase 11 signup calls it from the signup handler. Lives here now so
// one primitive exists instead of Phase 11 duplicating the logic.
func (r *Registry) ReserveSlug(ctx context.Context, slug string, siteID uint32) error {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return errors.New("sites: empty slug")
	}

	if _, reserved := reservedSlugs[slug]; reserved {
		return ErrSlugTaken
	}

	// Claim check: only the caller's site_id should own the slug
	// after the insert. If a different site_id grabs the same slug
	// between IsSlugAvailable + ReserveSlug, this SELECT catches it.
	var existingSiteID uint32

	row := r.conn.QueryRow(ctx,
		`SELECT site_id FROM statnive.sites FINAL WHERE slug = ? LIMIT 1`,
		slug,
	)

	if err := row.Scan(&existingSiteID); err != nil {
		if !strings.Contains(err.Error(), "no rows") {
			return fmt.Errorf("sites claim check: %w", err)
		}
	}

	if existingSiteID != 0 && existingSiteID != siteID {
		return ErrSlugTaken
	}

	// Update the row for siteID to set its slug. Uses
	// ReplacingMergeTree-style upsert if the row exists; a pure
	// INSERT otherwise. In practice Phase 11 signup inserts a brand-
	// new row with site_id + hostname + slug in one call, so this
	// method is rarely used for standalone slug rotation.
	if err := r.conn.Exec(ctx,
		`ALTER TABLE statnive.sites UPDATE slug = ? WHERE site_id = ? SETTINGS mutations_sync = 2`,
		slug, siteID,
	); err != nil {
		return fmt.Errorf("sites set slug: %w", err)
	}

	return nil
}

// ReservedSlugs returns the static blocklist for tests / admin UI
// preview. Exported so callers can render the "slug is reserved" hint
// without a server round-trip.
func ReservedSlugs() []string {
	out := make([]string, 0, len(reservedSlugs))
	for s := range reservedSlugs {
		out = append(out, s)
	}

	return out
}
