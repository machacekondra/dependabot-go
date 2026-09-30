package docker

import "testing"

// The cases below are taken from docker/spec/dependabot/docker/tag_spec.rb so
// that the port can be checked against the behaviour it is copying.

func TestTagSameButLessPrecise(t *testing.T) {
	if !NewTag("2.4").SameButLessPrecise(NewTag("2.4.2")) {
		t.Error("2.4 should be a less precise form of 2.4.2")
	}
	if NewTag("2.4").SameButLessPrecise(NewTag("2.42")) {
		t.Error("2.4 should not be a less precise form of 2.42")
	}
}

func TestTagLooksLikePrerelease(t *testing.T) {
	prereleases := []string{
		"3.15.0a2", "1.0.0-alpha", "1.0.0-alpha.1", "2.0-ALPHA",
		"3.5.0b3", "1.0.0-beta", "1.0.0-beta.1", "2.0-BETA",
		"3.6.0rc1", "1.0.0-rc", "1.0.0-rc.1", "2.0-RC",
		"1.0-dev", "1.0.0-DEV",
		"1.0-preview", "1.0.0-PREVIEW",
		"1.0-pre", "1.0.0-PRE",
		"1.0-nightly", "1.0.0-NIGHTLY",
		"1.0-snapshot", "1.0.0-SNAPSHOT",
		"1.0-canary", "1.0.0-CANARY",
		"1.0-unstable", "1.0.0-UNSTABLE",
		"1.0.0a1", "2.1.0b2", "3.2.0rc3",
		"1.0.0.post1", "2.1.0.POST2",
		"1.0.0.dev0", "2.1.0.DEV1",
	}
	for _, name := range prereleases {
		if !NewTag(name).LooksLikePrerelease() {
			t.Errorf("%q should look like a prerelease", name)
		}
	}

	releases := []string{
		"3.14.1", "1.0.0", "2.4.2",
		"3.14.1-slim-trixie", "3.6.3-alpine", "2.7.14-stretch",
	}
	for _, name := range releases {
		if NewTag(name).LooksLikePrerelease() {
			t.Errorf("%q should not look like a prerelease", name)
		}
	}
}

func TestTagNumericVersion(t *testing.T) {
	cases := map[string]string{
		"4.8-20250909-windowsservercore-ltsc2022":   "4.8-20250909",
		"4.8.1-20251014-windowsservercore-ltsc2022": "4.8.1-20251014",
		"4.8.1-windowsservercore-ltsc2022":          "4.8.1",
		"4.8-windowsservercore-ltsc2022":            "4.8",
		"17.10":                                     "17.10",
		"2.4.0-slim":                                "2.4.0",
		"latest":                                    "",
	}
	for name, want := range cases {
		if got := NewTag(name).NumericVersion(); got != want {
			t.Errorf("NewTag(%q).NumericVersion() = %q, want %q", name, got, want)
		}
	}
}

func TestTagComparableTo(t *testing.T) {
	pairs := [][2]string{
		{"4.8-20250909-windowsservercore-ltsc2022", "4.8.1-windowsservercore-ltsc2022"},
		{"4.8-20250909-windowsservercore-ltsc2022", "4.8.1-20251014-windowsservercore-ltsc2022"},
		{"4.8-windowsservercore-ltsc2022", "4.8.1-windowsservercore-ltsc2022"},
	}
	for _, pair := range pairs {
		a, b := NewTag(pair[0]), NewTag(pair[1])
		if !a.ComparableTo(b) || !b.ComparableTo(a) {
			t.Errorf("%q and %q should be comparable in both directions", pair[0], pair[1])
		}
	}

	// A variant suffix has to survive an update: alpine is not a candidate
	// replacement for slim.
	if NewTag("1.2.3-alpine").ComparableTo(NewTag("1.2.0-slim")) {
		t.Error("tags with different suffixes should not be comparable")
	}
	// A tag with no version at all is never a candidate.
	if NewTag("latest").ComparableTo(NewTag("1.2.0")) {
		t.Error("latest should not be comparable to a version")
	}
}

func TestTagFormat(t *testing.T) {
	cases := map[string]Format{
		"1.2.3":                  FormatNormal,
		"2024":                   FormatYearMonth,
		"202401":                 FormatYearMonthDay,
		"42":                     FormatBuildNum,
		"v3.10.0-169-gfe040d3":   FormatSHASuffixed,
		"22-ea-7":                Format("<version>-ea-<build_num>"),
		"22-ea-jdk-nanoserver-1": Format("<version>-ea-jdk-nanoserver-<build_num>"),

		// The sha_suffixed test runs first, so any tag ending in seven or more
		// hex characters is classified as commit-suffixed even when it reads
		// like a date. This matches Ruby.
		"20240115": FormatSHASuffixed,
		// Likewise year_month is tried before build_num, so a four-digit
		// Windows build number is classified as a date.
		"1803": FormatYearMonth,
	}
	for name, want := range cases {
		if got := NewTag(name).Format(); got != want {
			t.Errorf("NewTag(%q).Format() = %q, want %q", name, got, want)
		}
	}
}

func TestTagPrecisionAndCanonical(t *testing.T) {
	if got := NewTag("9").Precision(); got != 1 {
		t.Errorf("precision of 9 = %d, want 1", got)
	}
	if got := NewTag("9.0.17").Precision(); got != 3 {
		t.Errorf("precision of 9.0.17 = %d, want 3", got)
	}

	if !NewTag("1.2.3").Canonical() {
		t.Error("1.2.3 should be canonical")
	}
	if NewTag("1.2.3-alpine").Canonical() {
		t.Error("1.2.3-alpine should not be canonical")
	}
	if !NewTag("jdk-17").Canonical() {
		t.Error("jdk-17 should be canonical")
	}
}

func TestTagDigest(t *testing.T) {
	digest := "1bcefdb2ab5edb0e0e7b9ba8cd27ba5bfdc6e8f4e2ca4a6b1f9a3a5e9e5b9c2d"
	if !NewTag(digest).Digest() {
		t.Error("a bare digest should be recognised")
	}
	if NewTag("1.2.3").Digest() {
		t.Error("a version tag is not a digest")
	}
}
