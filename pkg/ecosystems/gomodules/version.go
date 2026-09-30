package gomodules

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// PseudoVersionRegexp matches Go's pseudo-versions, e.g.
// "v0.0.0-20260320110106-0b84568fffcc". The module proxy does not list these,
// so the update checker has to handle them specially.
var PseudoVersionRegexp = regexp.MustCompile(`\b\d{14}-[0-9a-f]{12}$`)

// Version is a Go module version.
//
// Ruby reimplements Go's prerelease ordering on top of Gem::Version; here
// golang.org/x/mod/semver is the same code the Go toolchain uses, so ordering
// is correct by construction. The version as written is preserved so an update
// writes back "v1.2.3" rather than a normalised form.
type Version struct {
	original  string
	canonical string
}

var _ dependabot.Version = (*Version)(nil)

// ParseVersion parses a Go module version, with or without its leading "v".
func ParseVersion(s string) (*Version, error) {
	canonical := canonicalise(s)
	if !semver.IsValid(canonical) {
		return nil, fmt.Errorf("invalid go module version %q", s)
	}
	return &Version{original: s, canonical: canonical}, nil
}

// ValidVersion reports whether s is a usable Go module version.
func ValidVersion(s string) bool {
	return s != "" && semver.IsValid(canonicalise(s))
}

func canonicalise(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s
}

// String returns the version as it was written.
func (v *Version) String() string { return v.original }

// Canonical returns the version in the "v"-prefixed form Go tools expect.
func (v *Version) Canonical() string { return v.canonical }

// Compare orders two versions using Go's own semver rules. Comparing against a
// version from another ecosystem falls back to a string comparison of the
// canonical forms, which keeps sorting total rather than panicking.
func (v *Version) Compare(other dependabot.Version) int {
	o, ok := other.(*Version)
	if !ok {
		return strings.Compare(v.canonical, canonicalise(other.String()))
	}
	return semver.Compare(v.canonical, o.canonical)
}

// Prerelease reports whether the version carries a prerelease suffix.
//
// Pseudo-versions are semver prereleases ("v0.0.0-2026...") but represent a
// real commit rather than an unstable release, so they are not reported as
// prereleases; otherwise a project pinned to a pseudo-version would never be
// offered an update.
func (v *Version) Prerelease() bool {
	pre := semver.Prerelease(v.canonical)
	if pre == "" {
		return false
	}
	return !PseudoVersionRegexp.MatchString(v.canonical)
}

// IsPseudoVersion reports whether the version names a commit rather than a tag.
func (v *Version) IsPseudoVersion() bool { return PseudoVersionRegexp.MatchString(v.canonical) }

// Incompatible reports whether the version carries Go's "+incompatible" build
// tag, meaning the module has no go.mod at that major version.
func (v *Version) Incompatible() bool { return strings.HasSuffix(v.original, "+incompatible") }

// Major returns the version's major-version prefix, e.g. "v2".
func (v *Version) Major() string { return semver.Major(v.canonical) }

func parseVersionIface(s string) (dependabot.Version, error) {
	v, err := ParseVersion(s)
	if err != nil {
		return nil, err
	}
	return v, nil
}
