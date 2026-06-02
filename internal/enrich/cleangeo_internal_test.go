package enrich

import "testing"

func TestCleanGeoField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"trim", "  Tehran  ", "Tehran"},
		{"dash sentinel", "-", ""},
		{"dash with whitespace", "  -  ", ""},
		{"valid value passes through", "Comcast Cable", "Comcast Cable"},
		{
			"lite-db sentinel",
			"This parameter is unavailable for selected data file. Please upgrade the data file.",
			"",
		},
		{
			"lite-db sentinel with leading whitespace",
			"  This parameter is unavailable for selected data file. Please upgrade the data file.",
			"",
		},
		// Anchored full-equality match: a value starting with "This" must
		// pass through, not be stripped by an over-broad prefix check.
		{"prose starting with This is preserved", "This Mobile Co", "This Mobile Co"},
		{
			"ipv4-only-bin ipv6 sentinel",
			"IPv6 address missing in IPv4 BIN.",
			"",
		},
		{
			"ipv4-only-bin ipv6 sentinel with trailing whitespace",
			"IPv6 address missing in IPv4 BIN.   ",
			"",
		},
		// Anchored full-equality on the IPv6 sentinel too — a real ISP
		// name happening to mention "IPv6" must pass through.
		{"prose containing IPv6 is preserved", "IPv6 Datacenter LLC", "IPv6 Datacenter LLC"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := cleanGeoField(tc.in); got != tc.want {
				t.Fatalf("cleanGeoField(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
