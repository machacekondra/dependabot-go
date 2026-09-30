package gomodules

import "testing"

func TestValidVersion(t *testing.T) {
	valid := []string{
		"v1.2.3", "1.2.3", "v2.0.0-rc.1", "v1.0.0+incompatible",
		"v0.0.0-20260320110106-0b84568fffcc",
	}
	for _, s := range valid {
		if !ValidVersion(s) {
			t.Errorf("ValidVersion(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "latest", "master", "v1.2.3.4.5-"} {
		if ValidVersion(s) {
			t.Errorf("ValidVersion(%q) = true, want false", s)
		}
	}
}

func TestVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.4", -1},
		{"v1.2.3", "v1.2.3", 0},
		{"v1.10.0", "v1.9.0", 1},
		// A prerelease sorts below its release, which a plain string compare
		// would get backwards.
		{"v1.2.3-rc.1", "v1.2.3", -1},
		{"v1.2.3-rc.2", "v1.2.3-rc.10", -1},
		// The leading "v" is optional on input.
		{"1.2.3", "v1.2.3", 0},
	}
	for _, tc := range cases {
		a, err := ParseVersion(tc.a)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.a, err)
		}
		b, err := ParseVersion(tc.b)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", tc.b, err)
		}
		if got := sign(a.Compare(b)); got != tc.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// The version is written back into go.mod, so it has to survive a round trip
// unchanged rather than being normalised.
func TestVersionPreservesInput(t *testing.T) {
	v, err := ParseVersion("v1.2.3+incompatible")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if v.String() != "v1.2.3+incompatible" {
		t.Errorf("String() = %q, want the version as written", v.String())
	}
	if !v.Incompatible() {
		t.Error("Incompatible() = false, want true")
	}
}

func TestVersionPrerelease(t *testing.T) {
	cases := map[string]bool{
		"v1.2.3":                             false,
		"v1.2.3-rc.1":                        true,
		"v2.0.0-beta":                        true,
		"v0.0.0-20260320110106-0b84568fffcc": false, // a commit, not an unstable release
	}
	for s, want := range cases {
		v, err := ParseVersion(s)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", s, err)
		}
		if got := v.Prerelease(); got != want {
			t.Errorf("ParseVersion(%q).Prerelease() = %v, want %v", s, got, want)
		}
	}
}

func TestVersionMajor(t *testing.T) {
	v, err := ParseVersion("v2.3.4")
	if err != nil {
		t.Fatalf("ParseVersion: %v", err)
	}
	if v.Major() != "v2" {
		t.Errorf("Major() = %q, want v2", v.Major())
	}
}

func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}
