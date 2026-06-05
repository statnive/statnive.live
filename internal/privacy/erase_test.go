package privacy

import (
	"errors"
	"regexp"
	"testing"

	"github.com/google/uuid"
)

func TestEraseByCookieID_RejectsEmptyHash(t *testing.T) {
	t.Parallel()

	e := NewEraseEnumerator(nil, "statnive")

	_, err := e.EraseByCookieID(t.Context(), 1, "")
	if !errors.Is(err, errEraseEmptyHash) {
		t.Errorf("empty hash should return errEraseEmptyHash, got %v", err)
	}
}

// Gate 2 E1: the OAuth-grant eraser must reject a nil user_id so a malformed
// account-deletion call can never mass-delete every user's grants.
func TestEraseOAuthGrantsByUserID_RejectsNilUser(t *testing.T) {
	t.Parallel()

	e := NewEraseEnumerator(nil, "statnive")

	_, err := e.EraseOAuthGrantsByUserID(t.Context(), uuid.Nil)
	if !errors.Is(err, errEraseEmptyUserID) {
		t.Errorf("nil user_id should return errEraseEmptyUserID, got %v", err)
	}
}

// EraseByUserID is the single account-scoped erase entry point (tokens + oauth
// grants); a nil user_id must be rejected so a malformed account-deletion can't
// mass-delete every user's data.
func TestEraseByUserID_RejectsNilUser(t *testing.T) {
	t.Parallel()

	e := NewEraseEnumerator(nil, "statnive")

	_, err := e.EraseByUserID(t.Context(), uuid.Nil)
	if !errors.Is(err, errEraseEmptyUserID) {
		t.Errorf("nil user_id should return errEraseEmptyUserID, got %v", err)
	}
}

func TestEraseByCookieID_RejectsZeroSiteID(t *testing.T) {
	t.Parallel()

	e := NewEraseEnumerator(nil, "statnive")

	_, err := e.EraseByCookieID(t.Context(), 0, "h:deadbeef")
	if !errors.Is(err, errEraseEmptySiteID) {
		t.Errorf("siteID=0 should return errEraseEmptySiteID before touching ClickHouse, got %v", err)
	}
}

// SQL shape regression test — the WHERE clause MUST include `AND
// site_id = ?` so the mutation can't reach across tenants. Documented
// in audit/legal-vs-system-audit.md § FAIL-1 + LEARN.md Lesson 37.
func TestBuildEraseSQL_IncludesSiteIDFilter(t *testing.T) {
	t.Parallel()

	got := buildEraseSQL("statnive", "events_raw")

	wantPattern := regexp.MustCompile("^ALTER TABLE `statnive`\\.`events_raw` DELETE WHERE cookie_id = \\? AND site_id = \\?$")
	if !wantPattern.MatchString(got) {
		t.Errorf("SQL shape regressed.\n  got:  %q\n  want pattern: %q", got, wantPattern.String())
	}
}

func TestBuildEraseSQL_QuotesIdentifiersDefensively(t *testing.T) {
	t.Parallel()

	// Defence-in-depth: even though discovery only surfaces
	// system-validated names, the database name is operator-supplied
	// and the table name flows from a JOIN against system.columns.
	// Both must be backtick-quoted with interior backticks doubled.
	got := buildEraseSQL("weird`db", "table`with`ticks")

	want := "ALTER TABLE `weird``db`.`table``with``ticks` DELETE WHERE cookie_id = ? AND site_id = ?"
	if got != want {
		t.Errorf("identifier escaping broken.\n  got:  %q\n  want: %q", got, want)
	}
}

func TestQuoteIdent(t *testing.T) {
	t.Parallel()

	cases := []struct{ in, want string }{
		{"events_raw", "`events_raw`"},
		{"statnive", "`statnive`"},
		{"weird`name", "`weird``name`"},
	}
	for _, c := range cases {
		if got := quoteIdent(c.in); got != c.want {
			t.Errorf("quoteIdent(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
