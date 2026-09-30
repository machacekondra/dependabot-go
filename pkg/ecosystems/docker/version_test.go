package docker

import "testing"

// Cases from docker/spec/dependabot/docker/version_spec.rb.

func TestValidVersion(t *testing.T) {
	valid := []string{
		"3.7.7-slim-buster", "20.9.0-alpine3.18",
		"img_20230915.3", "artful-20170826",
		"2.4.2", "20.9.0.alpine3.18",
		"11.0.16_8", "v11.0.16_8", "11.0.16.1",
	}
	for _, s := range valid {
		if !ValidVersion(s) {
			t.Errorf("ValidVersion(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"python", "latest", ""} {
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
		{"2.4.2", "2.1.0", 1},
		{"2.4.2", "2.4.3", -1},
		{"2.4.2", "2.4.2", 0},
		// The part after the underscore is Java's update number, which sorts
		// below a fourth version segment.
		{"11.0.16_8", "11.0.16.1", -1},
		{"17.0.2_8", "17.0.1_12", 1},
		{"artful_10", "artful_15", -1},
		{"artful-10", "artful-15", -1},
		{"img_20230915.3", "img_20231011.1", -1},
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

// TagVersion has to accept a tag with no version so the update checker can
// carry "latest" through its pipeline.
func TestTagVersionWithoutAVersion(t *testing.T) {
	latest := TagVersion(NewTag("latest"))
	if latest.String() != "latest" {
		t.Errorf("String() = %q, want %q", latest.String(), "latest")
	}
	if latest.Compare(TagVersion(NewTag("latest"))) != 0 {
		t.Error("latest should compare equal to itself")
	}
	if latest.Compare(TagVersion(NewTag("1.2.3"))) == 0 {
		t.Error("latest should not compare equal to a version")
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
