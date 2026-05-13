package privacy

import "testing"

func TestEraseByCookieID_RejectsEmptyHash(t *testing.T) {
	t.Parallel()

	e := NewEraseEnumerator(nil, "statnive")
	if _, err := e.EraseByCookieID(t.Context(), ""); err == nil {
		t.Errorf("empty hash should error before touching ClickHouse")
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
