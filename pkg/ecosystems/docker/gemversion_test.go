package docker

import "testing"

// Docker tags are ordered by RubyGems' comparison rules, so these cases check
// the reimplementation against Gem::Version's documented behaviour.

func TestGemVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		// Trailing zeros are not significant.
		{"1.0", "1.0.0", 0},
		{"1", "1.0.0.0", 0},
		{"1.1", "1.0", 1},
		{"1.0.1", "1.0", 1},
		{"2", "10", -1},
		// A word segment sorts below the numeric release it decorates.
		{"1.0.a", "1.0", -1},
		{"1.0.a", "1.0.b", -1},
		{"1.0.beta", "1.0.0", -1},
		// Splitting happens at every digit/letter boundary.
		{"1.0.0a1", "1.0.0a2", -1},
		{"1.0.0a2", "1.0.0", -1},
		// An empty version is zero.
		{"", "0", 0},
	}
	for _, tc := range cases {
		a, err := newGemVersion(tc.a)
		if err != nil {
			t.Fatalf("newGemVersion(%q): %v", tc.a, err)
		}
		b, err := newGemVersion(tc.b)
		if err != nil {
			t.Fatalf("newGemVersion(%q): %v", tc.b, err)
		}
		if got := sign(compareGemVersions(a, b)); got != tc.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestGemVersionRejectsMalformed(t *testing.T) {
	for _, s := range []string{"not a version", "1.0 2.0", "@"} {
		if _, err := newGemVersion(s); err == nil {
			t.Errorf("newGemVersion(%q) succeeded, want an error", s)
		}
	}
}
