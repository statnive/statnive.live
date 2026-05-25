package sites

import "testing"

// TestWwwBareToggleHost pins the pure-string www. ↔ bare toggle used by
// LookupSitePolicy (ingest path). Single-level toggle — multi-www. spoofing
// must be an explicit allowlist entry, not a recursive shortcut.
func TestWwwBareToggleHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "bare to www", in: "foo.com", want: "www.foo.com", ok: true},
		{name: "www to bare", in: "www.foo.com", want: "foo.com", ok: true},
		{name: "double www strips one level", in: "www.www.foo.com", want: "www.foo.com", ok: true},
		{name: "empty rejected", in: "", ok: false},
		{name: "subdomain prefix preserved", in: "api.foo.com", want: "www.api.foo.com", ok: true},
		{name: "www-foo not stripped (no dot after www)", in: "www-foo.com", want: "www.www-foo.com", ok: true},
		{name: "ipv6-ish (no realistic match) still toggles", in: "::1", want: "www.::1", ok: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, ok := wwwBareToggleHost(c.in)

			if ok != c.ok {
				t.Fatalf("wwwBareToggleHost(%q) ok = %v, want %v", c.in, ok, c.ok)
			}

			if ok && got != c.want {
				t.Errorf("wwwBareToggleHost(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestWwwBareToggleOrigin pins the URL-aware www. ↔ bare toggle used by
// OriginIndex.Lookup (CORS path). Preserves scheme + port; bails on
// malformed origins so a parse failure never silently widens the
// allowlist.
func TestWwwBareToggleOrigin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "https bare to www", in: "https://foo.com", want: "https://www.foo.com", ok: true},
		{name: "https www to bare", in: "https://www.foo.com", want: "https://foo.com", ok: true},
		{name: "port preserved bare to www", in: "https://foo.com:8443", want: "https://www.foo.com:8443", ok: true},
		{name: "port preserved www to bare", in: "https://www.foo.com:8443", want: "https://foo.com:8443", ok: true},
		{name: "double www strips one level", in: "https://www.www.foo.com", want: "https://www.foo.com", ok: true},
		{name: "subdomain prefix preserved", in: "https://api.foo.com", want: "https://www.api.foo.com", ok: true},
		{name: "scheme preserved when http", in: "http://foo.com", want: "http://www.foo.com", ok: true},
		// Bailout: malformed origins must not produce a toggle. A silent
		// pass-through here would let an attacker register `garbage` and
		// match `www.garbage`.
		{name: "no scheme rejected", in: "foo.com", ok: false},
		{name: "no host rejected", in: "https://", ok: false},
		{name: "wildcard rejected", in: "*", ok: false},
		{name: "empty rejected", in: "", ok: false},
		{name: "trailing-path rejected by url.Parse host check", in: "https:///", ok: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got, ok := wwwBareToggleOrigin(c.in)

			if ok != c.ok {
				t.Fatalf("wwwBareToggleOrigin(%q) ok = %v, want %v", c.in, ok, c.ok)
			}

			if ok && got != c.want {
				t.Errorf("wwwBareToggleOrigin(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
