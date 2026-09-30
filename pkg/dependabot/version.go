package dependabot

// Version is an ecosystem-specific version. Ruby models this as a subclass of
// Gem::Version per ecosystem (Dependabot::GoModules::Version and friends); Go
// has no inheritance, so ecosystems supply a type satisfying this interface and
// register a parser for it.
type Version interface {
	// String returns the version as it should be written back into a manifest.
	// Ecosystems that normalise on parse (Go strips a leading "v") are expected
	// to return the original spelling here.
	String() string

	// Compare returns -1, 0 or 1 as the receiver sorts before, equal to, or
	// after other. Comparing across ecosystems is undefined.
	Compare(other Version) int

	// Prerelease reports whether this is a pre-release version, which update
	// checkers use to avoid proposing an alpha to someone on a stable release.
	Prerelease() bool
}

// VersionConstraint is a constraint on a Version, e.g. ">= 1.0, < 2.0". Ruby
// calls this Dependabot::Requirement; the name is taken here by the requirement
// *entry* on a Dependency (see Requirement in dependency.go), so the constraint
// gets the longer name.
type VersionConstraint interface {
	String() string
	SatisfiedBy(v Version) bool
}

// LessThan reports whether a sorts before b.
func LessThan(a, b Version) bool { return a.Compare(b) < 0 }

// MaxVersion returns the highest version in vs, or nil when vs is empty.
func MaxVersion(vs []Version) Version {
	var max Version
	for _, v := range vs {
		if max == nil || v.Compare(max) > 0 {
			max = v
		}
	}
	return max
}

// MinVersion returns the lowest version in vs, or nil when vs is empty.
func MinVersion(vs []Version) Version {
	var min Version
	for _, v := range vs {
		if min == nil || v.Compare(min) < 0 {
			min = v
		}
	}
	return min
}
