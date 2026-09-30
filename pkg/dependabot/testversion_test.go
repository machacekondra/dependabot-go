package dependabot

import (
	"fmt"
	"strconv"
	"strings"
)

// testVersion is a minimal dotted-numeric version with an optional prerelease
// suffix, standing in for a real ecosystem's version type in the tests for the
// ecosystem-independent code.
type testVersion struct {
	original string
	segments []int
	pre      string
}

var _ Version = (*testVersion)(nil)

func parseTestVersion(s string) (Version, error) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if trimmed == "" {
		return nil, fmt.Errorf("empty version")
	}

	release, pre, _ := strings.Cut(trimmed, "-")
	v := &testVersion{original: s, pre: pre}
	for _, segment := range strings.Split(release, ".") {
		n, err := strconv.Atoi(segment)
		if err != nil {
			return nil, fmt.Errorf("invalid version %q", s)
		}
		v.segments = append(v.segments, n)
	}
	return v, nil
}

func mustTestVersion(s string) Version {
	v, err := parseTestVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}

func (v *testVersion) String() string { return v.original }

func (v *testVersion) Prerelease() bool { return v.pre != "" }

func (v *testVersion) Compare(other Version) int {
	o, ok := other.(*testVersion)
	if !ok {
		return strings.Compare(v.original, other.String())
	}
	for i := 0; i < len(v.segments) || i < len(o.segments); i++ {
		if c := compareInt(segmentAt(v.segments, i), segmentAt(o.segments, i)); c != 0 {
			return c
		}
	}
	switch {
	case v.pre == o.pre:
		return 0
	case v.pre == "": // a release sorts above its prereleases
		return 1
	case o.pre == "":
		return -1
	default:
		return strings.Compare(v.pre, o.pre)
	}
}

func segmentAt(segments []int, i int) int {
	if i < len(segments) {
		return segments[i]
	}
	return 0
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
