package storage

import "testing"

func TestOrderClause(t *testing.T) {
	t.Parallel()

	allowed := map[string]string{
		"views":   "views",
		"revenue": "revenue, visitors",
		"rpv":     "if(visitors > 0, revenue / visitors, 0)",
	}

	cases := []struct {
		name     string
		sort     string
		dir      string
		fallback string
		want     string
	}{
		{
			name:     "empty sort falls back",
			fallback: "views DESC",
			want:     "ORDER BY views DESC",
		},
		{
			name:     "unknown sort falls back (rejects sql injection)",
			sort:     "; DROP TABLE events --",
			fallback: "views DESC",
			want:     "ORDER BY views DESC",
		},
		{
			name: "known sort default desc",
			sort: "views",
			want: "ORDER BY views DESC",
		},
		{
			name: "known sort asc",
			sort: "views",
			dir:  "asc",
			want: "ORDER BY views ASC",
		},
		{
			name: "compound expression both terms get dir desc",
			sort: "revenue",
			want: "ORDER BY revenue DESC, visitors DESC",
		},
		{
			name: "compound expression both terms get dir asc",
			sort: "revenue",
			dir:  "asc",
			want: "ORDER BY revenue ASC, visitors ASC",
		},
		{
			name: "expression with parens stays single term (rpv regression)",
			sort: "rpv",
			want: "ORDER BY if(visitors > 0, revenue / visitors, 0) DESC",
		},
		{
			name: "expression with parens asc",
			sort: "rpv",
			dir:  "asc",
			want: "ORDER BY if(visitors > 0, revenue / visitors, 0) ASC",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &Filter{Sort: tc.sort, Dir: tc.dir}
			got := orderClause(f, allowed, tc.fallback)

			if got != tc.want {
				t.Errorf("orderClause = %q, want %q", got, tc.want)
			}
		})
	}
}
