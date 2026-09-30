package docker

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Docker tags are not semver, so Ruby compares them as Gem::Version — segments
// split on runs of digits and letters, with a letter segment sorting below a
// numeric one. gemVersion reimplements that ordering, because no Go semver
// library gets "1.0.0.alpine" vs "1.0.0" the same way RubyGems does.

// gemAnchoredVersion is RubyGems' ANCHORED_VERSION_PATTERN. Dependabot::Version
// uses it unchanged to decide whether a string is a version at all.
var gemAnchoredVersion = regexp.MustCompile(
	`^\s*([0-9]+(\.[0-9a-zA-Z]+)*(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?)?\s*$`)

// gemSegmentRe splits a version into its digit and letter runs.
var gemSegmentRe = regexp.MustCompile(`(?i)[0-9]+|[a-z]+`)

// gemSegment is one segment of a version: either a number or a word. Exactly
// one of the two fields is meaningful, selected by isNum.
type gemSegment struct {
	num   int
	word  string
	isNum bool
}

func (s gemSegment) String() string {
	if s.isNum {
		return strconv.Itoa(s.num)
	}
	return s.word
}

// gemVersion is a version compared by RubyGems' rules.
type gemVersion struct {
	original string
	// canonical is the segment list with trailing zeros dropped from the
	// numeric and the alphabetic run, which is what RubyGems compares.
	canonical []gemSegment
}

// newGemVersion parses a version string, rejecting anything RubyGems would
// reject. An empty string is version 0, as in Ruby.
func newGemVersion(s string) (*gemVersion, error) {
	if !gemAnchoredVersion.MatchString(s) {
		return nil, fmt.Errorf("malformed version number string %q", s)
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		trimmed = "0"
	}
	return &gemVersion{original: trimmed, canonical: canonicalSegments(trimmed)}, nil
}

func (v *gemVersion) String() string { return v.original }

// segments returns the version's raw segments, before canonicalisation.
func (v *gemVersion) segments() []gemSegment { return splitSegments(v.original) }

func splitSegments(s string) []gemSegment {
	var out []gemSegment
	for _, match := range gemSegmentRe.FindAllString(s, -1) {
		if n, err := strconv.Atoi(match); err == nil {
			out = append(out, gemSegment{num: n, isNum: true})
			continue
		}
		out = append(out, gemSegment{word: match})
	}
	return out
}

// canonicalSegments splits the segments at the first word segment and drops
// each run's trailing zeros, so "1.2.0" and "1.2" compare equal.
func canonicalSegments(s string) []gemSegment {
	all := splitSegments(s)

	firstWord := len(all)
	for i, seg := range all {
		if !seg.isNum {
			firstWord = i
			break
		}
	}

	numeric := dropTrailingZeros(all[:firstWord])
	words := dropTrailingZeros(all[firstWord:])
	return append(numeric, words...)
}

func dropTrailingZeros(segs []gemSegment) []gemSegment {
	end := len(segs)
	for end > 0 {
		s := segs[end-1]
		if !s.isNum || s.num != 0 {
			break
		}
		end--
	}
	out := make([]gemSegment, end)
	copy(out, segs[:end])
	return out
}

// compareGemVersions orders two versions the way Gem::Version#<=> does: pad the
// shorter with zeros, and rank a word segment below a numeric one.
func compareGemVersions(a, b *gemVersion) int {
	lhs, rhs := a.canonical, b.canonical

	limit := len(lhs)
	if len(rhs) > limit {
		limit = len(rhs)
	}

	for i := 0; i < limit; i++ {
		l := segmentAt(lhs, i)
		r := segmentAt(rhs, i)

		switch {
		case l.isNum && r.isNum:
			if l.num != r.num {
				return compareInts(l.num, r.num)
			}
		case !l.isNum && !r.isNum:
			if l.word != r.word {
				return strings.Compare(l.word, r.word)
			}
		case !l.isNum && r.isNum:
			return -1
		default:
			return 1
		}
	}
	return 0
}

// segmentAt returns the segment at i, or the implicit zero past the end.
func segmentAt(segs []gemSegment, i int) gemSegment {
	if i < len(segs) {
		return segs[i]
	}
	return gemSegment{num: 0, isNum: true}
}

func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
