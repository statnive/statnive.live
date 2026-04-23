package sites

import (
	"strings"
	"testing"
)

func TestGenerateSlug(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Example.com":       "example",
		"blog.acme.co":      "blog-acme",
		"foo_bar.io":        "foo-bar",
		"https://x.dev":     "https-x",
		"UPPER.IR":          "upper",
		"my-app.live":       "my-app",
		"":                  "",
		"   ":               "",
		"a.b.c.d.e.f.g.com": "a-b-c-d-e-f-g",
	}

	for in, want := range cases {
		got := GenerateSlug(in)
		if got != want {
			t.Errorf("GenerateSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGenerateSlug_Deterministic(t *testing.T) {
	t.Parallel()

	input := "Example.com"
	a := GenerateSlug(input)
	b := GenerateSlug(input)

	if a != b {
		t.Errorf("not deterministic: %q vs %q", a, b)
	}
}

func TestGenerateSlug_ClampsLength(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("abc", 40) + ".com" // ~120 chars

	got := GenerateSlug(long)
	if len(got) > 32 {
		t.Errorf("slug length = %d, want ≤ 32", len(got))
	}
}

func TestReservedSlugs_Contents(t *testing.T) {
	t.Parallel()

	list := ReservedSlugs()

	want := []string{"admin", "api", "app", "statnive"}
	have := make(map[string]bool, len(list))

	for _, s := range list {
		have[s] = true
	}

	for _, w := range want {
		if !have[w] {
			t.Errorf("reserved list missing %q", w)
		}
	}
}
