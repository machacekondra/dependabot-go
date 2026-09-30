package dependabot

import (
	"fmt"
	"strings"
)

// SecurityAdvisory is a vulnerability affecting a range of a dependency's
// versions, used to drive security updates and to check that a proposed update
// actually fixes the problem.
type SecurityAdvisory struct {
	DependencyName string
	PackageManager string

	// VulnerableVersionStrings keeps the advisory's ranges as written, which
	// ecosystems inspect for boundaries their registry won't list (Go's
	// pseudo-versions, for instance).
	VulnerableVersionStrings []string

	vulnerable []VersionConstraint
	safe       []VersionConstraint
	ecosystem  Ecosystem
}

// NewSecurityAdvisory parses an advisory's version ranges using the
// ecosystem's constraint syntax.
func NewSecurityAdvisory(dependencyName, packageManager string, vulnerableVersions, safeVersions []string) (*SecurityAdvisory, error) {
	eco, err := Lookup(packageManager)
	if err != nil {
		return nil, err
	}
	a := &SecurityAdvisory{
		DependencyName:           dependencyName,
		PackageManager:           packageManager,
		VulnerableVersionStrings: vulnerableVersions,
		ecosystem:                eco,
	}
	if a.vulnerable, err = parseAll(eco, vulnerableVersions); err != nil {
		return nil, fmt.Errorf("advisory for %s: vulnerable versions: %w", dependencyName, err)
	}
	if a.safe, err = parseAll(eco, safeVersions); err != nil {
		return nil, fmt.Errorf("advisory for %s: safe versions: %w", dependencyName, err)
	}
	return a, nil
}

func parseAll(eco Ecosystem, ranges []string) ([]VersionConstraint, error) {
	var out []VersionConstraint
	for _, r := range ranges {
		constraints, err := eco.ParseConstraints(r)
		if err != nil {
			return nil, err
		}
		out = append(out, constraints...)
	}
	return out, nil
}

// AffectsVersion reports whether the advisory applies to a version.
//
// The precedence is: a version in a safe range is never vulnerable; otherwise a
// version in a vulnerable range is; otherwise, if vulnerable ranges were given
// and none matched, it is not; and finally, if only safe ranges were given and
// none matched, it is.
func (a *SecurityAdvisory) AffectsVersion(v Version) bool {
	if v == nil {
		return false
	}
	if len(a.safe) == 0 && len(a.vulnerable) == 0 {
		return false
	}
	for _, c := range a.safe {
		if c.SatisfiedBy(v) {
			return false
		}
	}
	for _, c := range a.vulnerable {
		if c.SatisfiedBy(v) {
			return true
		}
	}
	if len(a.vulnerable) > 0 {
		return false
	}
	return len(a.safe) > 0
}

// AffectsVersionString parses a version string and reports whether the advisory
// applies to it. An unparseable version is treated as unaffected.
func (a *SecurityAdvisory) AffectsVersionString(s string) bool {
	if s == "" || !a.ecosystem.ValidVersion(s) {
		return false
	}
	v, err := a.ecosystem.ParseVersion(s)
	if err != nil {
		return false
	}
	return a.AffectsVersion(v)
}

// FixedBy reports whether an updated dependency resolves the advisory.
func (a *SecurityAdvisory) FixedBy(dep *Dependency) bool {
	if !strings.EqualFold(a.DependencyName, dep.Name) {
		return false
	}
	if a.PackageManager != dep.PackageManager {
		return false
	}
	// Without a previous version we cannot tell whether the dependency was
	// vulnerable to begin with, so we do not claim to have fixed it.
	if dep.PreviousVersion == "" {
		return false
	}
	if !a.AffectsVersionString(dep.PreviousVersion) {
		return false
	}
	// Removing a dependency is a way of fixing the vulnerability.
	if dep.Removed {
		return true
	}
	return !a.AffectsVersionString(dep.Version)
}

// AnyAffect reports whether any advisory in the set applies to a version.
func AnyAffect(advisories []*SecurityAdvisory, v Version) bool {
	for _, a := range advisories {
		if a.AffectsVersion(v) {
			return true
		}
	}
	return false
}
