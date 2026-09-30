package docker

import (
	"regexp"
	"strings"
)

// Format classifies the shape of a tag's version, so that only tags of the
// same shape are compared against each other. Ruby uses symbols; the dynamic
// "<version>-ea-<build_num>" form makes this a string rather than an enum.
type Format string

// The fixed tag formats.
const (
	FormatSHASuffixed  Format = "sha_suffixed"
	FormatYearMonth    Format = "year_month"
	FormatYearMonthDay Format = "year_month_day"
	FormatBuildNum     Format = "build_num"
	FormatNormal       Format = "normal"
)

// wordsWithBuild matches the "-ea-32" style build suffix. It stays
// case-sensitive even inside the case-insensitive version pattern, matching
// Ruby, where an interpolated Regexp keeps its own flags.
const wordsWithBuild = `(?:(?:-[a-z]+)+-[0-9]+)+`

const versionPattern = `v?(?P<version>[0-9]+(?:[_.][0-9]+)*` +
	`(?:\.[a-z0-9]+|(?-i:` + wordsWithBuild + `)|-(?:kb)?[0-9]+)*)`

// Ruby builds these into a single alternation, NAME_WITH_VERSION. Go's regexp
// rejects a pattern that reuses a capture-group name, so they stay separate
// and are tried in the same order the alternation would have.
var (
	versionWithPfx = regexp.MustCompile(
		`(?i)^(?P<prefix>[a-z][a-z0-9.\-_]*-)?` + versionPattern + `$`)
	versionWithSfx = regexp.MustCompile(
		`(?i)^(?P<operator>[~^<>=]*)` + versionPattern + `(?P<suffix>-[a-z][a-z0-9.\-]*)?$`)
	versionWithPfxAndSfx = regexp.MustCompile(
		`(?i)^(?P<prefix>[a-z\-_]+-)?` + versionPattern + `(?P<suffix>-[a-z\-]+)?$`)
)

var (
	digestRe          = regexp.MustCompile(`[0-9a-f]{64}`)
	shaSuffixRe       = regexp.MustCompile(`(^|-g?)[0-9a-f]{7,}$`)
	yearMonthRe       = regexp.MustCompile(`^[12]\d{3}(?:[.\-]|$)`)
	yearMonthDayRe    = regexp.MustCompile(`^[12](?:\d{5}|\d{7})(?:[.\-]|$)`)
	buildNumRe        = regexp.MustCompile(`^\d+$`)
	wordsWithBuildRe  = regexp.MustCompile(wordsWithBuild)
	buildNumInWordsRe = regexp.MustCompile(`-[0-9]+`)
	kbRe              = regexp.MustCompile(`(?i)kb`)
	wordSuffixRe      = regexp.MustCompile(`-[a-z]+`)
	segmentSplitRe    = regexp.MustCompile(`[.-]`)
)

// prereleasePatterns are the markers that make a tag a prerelease. The two
// unanchored-case patterns are case-sensitive in Ruby and stay that way here.
var prereleasePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)alpha`),
	regexp.MustCompile(`(?i)beta`),
	regexp.MustCompile(`(?i)rc\d*`),
	regexp.MustCompile(`(?i)dev`),
	regexp.MustCompile(`(?i)preview`),
	regexp.MustCompile(`(?i)\bpre\b`),
	regexp.MustCompile(`(?i)nightly`),
	regexp.MustCompile(`(?i)snapshot`),
	regexp.MustCompile(`(?i)canary`),
	regexp.MustCompile(`(?i)unstable`),
	regexp.MustCompile(`\d+[a-z]\d*`),
	regexp.MustCompile(`[a-z]+\d+$`),
	regexp.MustCompile(`(?i)\.post\d+`),
	regexp.MustCompile(`(?i)\.dev\d+`),
}

// Tag is a container image tag, e.g. "1.2.3-alpine" or "20240115".
type Tag struct {
	Name string

	parsed  bool
	prefix  string
	version string
	suffix  string
}

// NewTag builds a tag from its name.
func NewTag(name string) *Tag { return &Tag{Name: name} }

func (t *Tag) String() string { return t.Name }

// parse runs the first name pattern that matches, caching the result.
func (t *Tag) parse() {
	if t.parsed {
		return
	}
	t.parsed = true

	for _, re := range []*regexp.Regexp{versionWithPfx, versionWithSfx, versionWithPfxAndSfx} {
		m := re.FindStringSubmatch(t.Name)
		if m == nil {
			continue
		}
		for i, name := range re.SubexpNames() {
			switch name {
			case "prefix":
				t.prefix = m[i]
			case "version":
				t.version = m[i]
			case "suffix":
				t.suffix = m[i]
			}
		}
		return
	}
}

// Prefix is the non-version text before the version, e.g. "jdk-" in "jdk-17".
func (t *Tag) Prefix() string { t.parse(); return t.prefix }

// Version is the version part of the tag, e.g. "1.2.3" in "1.2.3-alpine".
func (t *Tag) Version() string { t.parse(); return t.version }

// Suffix is the variant text after the version, e.g. "-alpine".
func (t *Tag) Suffix() string { t.parse(); return t.suffix }

// Comparable reports whether the tag contains a version at all. Tags like
// "latest" or "bookworm" do not, and are never updated by version comparison.
func (t *Tag) Comparable() bool { t.parse(); return t.version != "" }

// Digest reports whether the tag is really a bare digest.
func (t *Tag) Digest() bool { return digestRe.MatchString(t.Name) }

// Format classifies the tag so only like-shaped tags are compared.
func (t *Tag) Format() Format {
	if shaSuffixRe.MatchString(t.Name) {
		return FormatSHASuffixed
	}
	version := t.Version()
	switch {
	case yearMonthRe.MatchString(version):
		return FormatYearMonth
	case yearMonthDayRe.MatchString(version):
		return FormatYearMonthDay
	case buildNumRe.MatchString(version):
		return FormatBuildNum
	}

	// "21-ea-32" and "22-ea-7" both reduce to "<version>-ea-<build_num>", so
	// they are comparable to each other but not to "22-ea-jdk-nanoserver-1809",
	// which reduces to "<version>-ea-jdk-nanoserver-<build_num>".
	if match := wordsWithBuildRe.FindString(version); match != "" {
		return Format("<version>" + buildNumInWordsRe.ReplaceAllString(match, "-<build_num>"))
	}
	return FormatNormal
}

// NumericVersion is the version with word suffixes stripped, which is what
// gets compared. It is empty for a non-comparable tag.
func (t *Tag) NumericVersion() string {
	if !t.Comparable() {
		return ""
	}
	result := kbRe.ReplaceAllString(t.Version(), "")
	result = wordSuffixRe.ReplaceAllString(result, "")
	return strings.ToLower(result)
}

// Segments are the numeric version's dot- and dash-separated parts.
func (t *Tag) Segments() []string {
	numeric := t.NumericVersion()
	if numeric == "" {
		return nil
	}
	parts := segmentSplitRe.Split(numeric, -1)
	// Ruby's String#split drops trailing empty fields.
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// Precision is how specific the tag is: "9" is 1, "9.0.17" is 3.
func (t *Tag) Precision() int { return len(t.Segments()) }

// SamePrecision reports whether two tags pin to the same depth.
func (t *Tag) SamePrecision(other *Tag) bool { return t.Precision() == other.Precision() }

// SameButLessPrecise reports whether t is a less precise form of other, e.g.
// "9.0" against "9.0.17".
func (t *Tag) SameButLessPrecise(other *Tag) bool {
	mine := t.Segments()
	theirs := other.Segments()
	for i, seg := range theirs {
		if i >= len(mine) {
			continue
		}
		if mine[i] != seg {
			return false
		}
	}
	return true
}

// Canonical reports whether the tag is the plain version, with none of the
// variant decoration that would tie it to a particular base image.
func (t *Tag) Canonical() bool {
	numeric := t.NumericVersion()
	if numeric == "" {
		return false
	}
	return t.Name == numeric ||
		// .NET tags are suffixed with -sdk.
		t.Name == numeric+"-sdk" ||
		t.Name == "jdk-"+numeric
}

// LooksLikePrerelease reports whether the tag names an unstable release.
func (t *Tag) LooksLikePrerelease() bool {
	if !t.Comparable() {
		return false
	}
	// A commit-suffixed tag such as "v3.10.0-169-gfe040d3" is a real release.
	if t.Format() == FormatSHASuffixed {
		return false
	}

	for _, pattern := range prereleasePatterns {
		if t.Version() != "" && pattern.MatchString(t.Version()) {
			return true
		}
		if t.Suffix() != "" && pattern.MatchString(t.Suffix()) {
			return true
		}
	}
	return false
}

// comparableFormats allows a bare build number to be compared against a dated
// tag, which is how images that switch between the two are still updated.
func (t *Tag) comparableFormats(otherFormat Format, otherPrefix, otherSuffix string) bool {
	if t.Prefix() != "" || t.Suffix() != "" || otherPrefix != "" || otherSuffix != "" {
		return false
	}
	dated := func(f Format) bool { return f == FormatYearMonth || f == FormatYearMonthDay }

	format := t.Format()
	return (format == FormatBuildNum && dated(otherFormat)) ||
		(dated(format) && otherFormat == FormatBuildNum)
}

// ComparableTo reports whether t is a candidate update for other: same prefix,
// same suffix and same format, so "1.2-alpine" is never offered for "1.2".
func (t *Tag) ComparableTo(other *Tag) bool {
	if !t.Comparable() {
		return false
	}

	otherPrefix := other.Prefix()
	otherSuffix := other.Suffix()
	otherFormat := other.Format()

	equalPrefix := t.Prefix() == otherPrefix
	equalFormat := t.Format() == otherFormat

	if otherFormat == FormatSHASuffixed {
		return equalPrefix && equalFormat
	}

	equalSuffix := t.Suffix() == otherSuffix
	return (equalPrefix && equalFormat && equalSuffix) ||
		t.comparableFormats(otherFormat, otherPrefix, otherSuffix)
}
