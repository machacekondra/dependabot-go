package docker

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// dockerVersionRe splits a tag into leading words and the version-bearing rest.
// The bounded repetitions come from Ruby, where an unbounded one trips static
// analysis; a tag cannot exceed 255 characters anyway.
var dockerVersionRe = regexp.MustCompile(`^(?P<prefix>[a-z._\-]{0,255})[_\-v]?(?P<version>.{1,255})$`)

// Version is a container image version.
//
// Ruby's Docker::Version stringifies to the numeric release part, while the
// update checker separately passes tag names around as plain strings. This
// port keeps one type: the version prints as the tag it came from, which is
// what gets written back into a Dockerfile, and compares on the numeric part.
type Version struct {
	tag *Tag
	// release is the numeric version, e.g. "8.0.1" from "8.0.1_3-jdk". It is
	// nil for a tag that carries no version at all, such as "latest".
	release *gemVersion
	// update is Java's update number, the part after "_".
	update *gemVersion
}

var _ dependabot.Version = (*Version)(nil)

// ParseVersion parses a tag name into a comparable version, failing for a tag
// that has no version in it.
func ParseVersion(s string) (*Version, error) {
	release, update, err := releaseAndUpdate(s)
	if err != nil {
		return nil, err
	}
	return &Version{tag: NewTag(s), release: release, update: update}, nil
}

// TagVersion wraps any tag, including one with no version in it. The update
// checker deals in tags throughout and has to be able to carry "latest"
// around; such a version only ever compares equal to itself.
func TagVersion(tag *Tag) *Version {
	release, update, err := releaseAndUpdate(tag.Name)
	if err != nil {
		return &Version{tag: tag}
	}
	return &Version{tag: tag, release: release, update: update}
}

// ValidVersion reports whether a tag can be compared as a version.
func ValidVersion(s string) bool {
	_, err := ParseVersion(s)
	return err == nil
}

func releaseAndUpdate(s string) (*gemVersion, *gemVersion, error) {
	m := dockerVersionRe.FindStringSubmatch(s)
	if m == nil {
		return nil, nil, fmt.Errorf("malformed docker version %q", s)
	}
	rest := m[dockerVersionRe.SubexpIndex("version")]

	releaseText, updateText, _ := strings.Cut(rest, "_")

	// Run the release text back through the tag rules so that a decorated tag
	// such as "20.9.0-alpine3.18" reduces to the "20.9.0" that gets compared.
	releaseText = chomp(chomp(chomp(releaseText, "."), "-"), "_")
	numeric := NewTag(releaseText).NumericVersion()
	if numeric == "" {
		return nil, nil, fmt.Errorf("no comparable version in %q", s)
	}

	release, err := newGemVersion(strings.ReplaceAll(numeric, "-", "."))
	if err != nil {
		return nil, nil, err
	}

	// A non-numeric update part is not an update number at all, so it sorts as
	// if absent.
	if updateText == "" || !startsWithDigit(updateText) {
		updateText = "0"
	}
	update, err := newGemVersion(updateText)
	if err != nil {
		return nil, nil, err
	}
	return release, update, nil
}

func chomp(s, suffix string) string { return strings.TrimSuffix(s, suffix) }

func startsWithDigit(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// String returns the tag the version was parsed from.
func (v *Version) String() string { return v.tag.Name }

// Tag returns the underlying tag.
func (v *Version) Tag() *Tag { return v.tag }

// Compare orders by numeric release, then by Java update number. A tag with
// no version in it has no meaningful order, so it falls back to comparing the
// tag names, which at least keeps sorting total and stable.
func (v *Version) Compare(other dependabot.Version) int {
	o, ok := other.(*Version)
	if !ok || v.release == nil || o.release == nil {
		return strings.Compare(v.String(), other.String())
	}
	if cmp := compareGemVersions(v.release, o.release); cmp != 0 {
		return cmp
	}
	return compareGemVersions(v.update, o.update)
}

// Prerelease reports whether the tag names an unstable release.
func (v *Version) Prerelease() bool { return v.tag.LooksLikePrerelease() }

func parseVersionIface(s string) (dependabot.Version, error) {
	v, err := ParseVersion(s)
	if err != nil {
		return nil, err
	}
	return v, nil
}
