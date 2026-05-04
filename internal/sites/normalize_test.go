package sites

import "testing"

func TestNormalizeHostname(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain hostname", "televika.com", "televika.com"},
		{"www prefix preserved", "www.televika.com", "www.televika.com"},
		{"https scheme stripped", "https://televika.com", "televika.com"},
		{"http scheme stripped", "http://televika.com", "televika.com"},
		{"protocol-relative stripped", "//televika.com", "televika.com"},
		{"https with www", "https://www.televika.com/", "www.televika.com"},
		{"trailing slash stripped", "televika.com/", "televika.com"},
		{"path stripped", "televika.com/some/page", "televika.com"},
		{"query stripped", "televika.com?utm=x", "televika.com"},
		{"fragment stripped", "televika.com#section", "televika.com"},
		{"port stripped", "televika.com:443", "televika.com"},
		{"http with port and path", "http://televika.com:8080/path?x=1", "televika.com"},
		{"https www port path query combo", "https://www.televika.com:8080/path?x=1", "www.televika.com"},
		{"uppercase coerced to lower", "TELEVIKA.COM", "televika.com"},
		{"surrounding whitespace", "  televika.com  ", "televika.com"},
		{"empty stays empty", "", ""},
		// IPv6 footgun — bare LastIndex(":") would have mangled these.
		{"ipv6 bracketed with port", "[::1]:8080", "::1"},
		{"ipv6 bracketed scheme path", "http://[2001:db8::1]:443/x", "2001:db8::1"},
		// userinfo leak from misconfigured trackers passing href instead of hostname.
		{"userinfo stripped", "user:pw@televika.com", "televika.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := NormalizeHostname(tc.in); got != tc.want {
				t.Fatalf("NormalizeHostname(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
