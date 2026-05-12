package enrich

import "testing"

// EnrichedEvent.Referrer is written with only the host segment so query
// strings (potentially carrying session tokens, search terms, or reset
// tokens) never reach events_raw. This test pins that contract.
func TestExtractHostLower_StripsPathQueryFragment(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"https://example.com/search?q=secret&token=abc", "example.com"},
		{"https://example.com/foo/bar", "example.com"},
		{"https://example.com/#section", "example.com"},
		{"HTTPS://Example.COM/Path", "example.com"},
		{"https://user:pw@example.com/x", "example.com"},
		{"//example.com/x?y=z", "example.com"},
		{"example.com", "example.com"},
		{"example.com:8080", "example.com"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"", ""},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := extractHostLower(c.in); got != c.want {
				t.Errorf("extractHostLower(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
