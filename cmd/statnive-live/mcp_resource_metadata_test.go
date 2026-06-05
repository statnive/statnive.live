package main

import "testing"

// Gate 2 H2: an unset resource_metadata_url must be derived from the audience
// origin so the chatgpt-app 401 always carries an RFC 9728 discovery hint.
func TestResourceMetadataURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		configured string
		audience   string
		want       string
	}{
		{"derived from audience origin", "", "https://app.statnive.live/mcp", "https://app.statnive.live/.well-known/oauth-protected-resource"},
		{"configured value wins", "https://x/meta", "https://app.statnive.live/mcp", "https://x/meta"},
		{"unparseable audience falls back to configured (empty)", "", "::not a url", ""},
		{"audience without scheme falls back", "", "app.statnive.live/mcp", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := resourceMetadataURL(tc.configured, tc.audience); got != tc.want {
				t.Errorf("resourceMetadataURL(%q, %q) = %q, want %q", tc.configured, tc.audience, got, tc.want)
			}
		})
	}
}
