package dependabot

import (
	"context"
	"fmt"
	"strings"
)

// VersionFinder is the ecosystem-specific half of an update checker: the
// methods that have to know how a particular registry works.
type VersionFinder interface {
	LatestVersion(ctx context.Context) (Version, error)
	LatestResolvableVersion(ctx context.Context) (Version, error)
	LatestResolvableVersionWithNoUnlock(ctx context.Context) (Version, error)
	LowestSecurityFixVersion(ctx context.Context) (Version, error)
	UpdatedRequirements(ctx context.Context) ([]Requirement, error)
}

// VersionUpToDateOverrider lets an ecosystem replace the generic "is the
// current version the latest one" test. Docker implements it because an image
// pinned by digest can be out of date while its tag has not moved.
type VersionUpToDateOverrider interface {
	VersionUpToDate(ctx context.Context) (bool, error)
}

// VersionCanUpdateOverrider lets an ecosystem replace the generic test for
// whether an update is available at a given unlock level.
type VersionCanUpdateOverrider interface {
	VersionCanUpdate(ctx context.Context, unlock UnlockLevel) (bool, error)
}

// BaseChecker supplies the ecosystem-independent half of UpdateChecker: the
// rules for deciding whether a dependency is out of date and what the updated
// dependency should look like. Ruby gets this from inheriting
// UpdateCheckers::Base; here an ecosystem checker embeds BaseChecker and
// points Finder at itself.
type BaseChecker struct {
	// Finder must be set to the embedding checker.
	Finder VersionFinder
	Cfg    CheckerConfig
	Eco    Ecosystem
}

// NewBaseChecker builds the shared half of a checker. Finder must be assigned
// by the caller once the concrete checker exists.
func NewBaseChecker(eco Ecosystem, cfg CheckerConfig) *BaseChecker {
	return &BaseChecker{Cfg: cfg, Eco: eco}
}

// Dependency is the dependency under consideration.
func (b *BaseChecker) Dependency() *Dependency { return b.Cfg.Dependency }

// CurrentVersion parses the dependency's current version, returning nil when it
// has none or it is not a valid version (a git SHA, for instance).
func (b *BaseChecker) CurrentVersion() Version {
	if b.Cfg.Dependency.Version == "" || !b.Eco.ValidVersion(b.Cfg.Dependency.Version) {
		return nil
	}
	v, err := b.Eco.ParseVersion(b.Cfg.Dependency.Version)
	if err != nil {
		return nil
	}
	return v
}

// IgnoreConstraints parses the job's ignore conditions for this dependency.
func (b *BaseChecker) IgnoreConstraints() []VersionConstraint {
	var out []VersionConstraint
	for _, raw := range b.Cfg.IgnoredVersions {
		constraints, err := b.Eco.ParseConstraints(raw)
		if err != nil {
			continue
		}
		out = append(out, constraints...)
	}
	return out
}

// AllVersionsIgnored reports whether the job ignores every version, which makes
// any update impossible.
func (b *BaseChecker) AllVersionsIgnored() bool {
	for _, raw := range b.Cfg.IgnoredVersions {
		if strings.ReplaceAll(raw, " ", "") == ">=0" {
			return true
		}
	}
	return false
}

// RequirementsUnlockedOrCanBe reports whether the checker is allowed to rewrite
// the dependency's manifest requirement, which a lockfile-only job forbids.
func (b *BaseChecker) RequirementsUnlockedOrCanBe() bool {
	return !b.Cfg.Strategy.LockfileOnly()
}

// Ignored reports whether a version is excluded by the job's ignore conditions.
func (b *BaseChecker) Ignored(v Version) bool {
	for _, c := range b.IgnoreConstraints() {
		if c.SatisfiedBy(v) {
			return true
		}
	}
	return false
}

// Vulnerable reports whether the dependency's current version is affected by
// any of the job's advisories.
func (b *BaseChecker) Vulnerable() bool {
	if len(b.Cfg.SecurityAdvisories) == 0 {
		return false
	}
	// Versionless dependencies (no lockfile) and git dependencies can't be
	// checked against advisory ranges.
	if b.Cfg.Dependency.Version == "" || b.ExistingVersionIsSHA() {
		return false
	}
	current := b.CurrentVersion()
	if current == nil {
		return false
	}
	return AnyAffect(b.Cfg.SecurityAdvisories, current)
}

// ExistingVersionIsSHA reports whether the dependency is pinned to a commit
// rather than a released version.
func (b *BaseChecker) ExistingVersionIsSHA() bool {
	if b.Eco.ValidVersion(b.Cfg.Dependency.Version) {
		return false
	}
	return b.Cfg.Dependency.VersionIsSHA()
}

// PreferredResolvableVersion is the version the checker wants to move to: the
// lowest version that fixes an advisory when the dependency is vulnerable,
// otherwise the latest resolvable version.
func (b *BaseChecker) PreferredResolvableVersion(ctx context.Context) (Version, error) {
	if b.Vulnerable() {
		v, err := b.Finder.LowestSecurityFixVersion(ctx)
		if err != nil {
			return nil, err
		}
		if v != nil {
			return v, nil
		}
	}
	return b.Finder.LatestResolvableVersion(ctx)
}

// UpToDate reports whether the dependency needs no update at all.
func (b *BaseChecker) UpToDate(ctx context.Context) (bool, error) {
	if b.Cfg.Dependency.Version != "" {
		return b.versionUpToDate(ctx)
	}
	return b.requirementsUpToDate(ctx)
}

func (b *BaseChecker) versionUpToDate(ctx context.Context) (bool, error) {
	if o, ok := b.Finder.(VersionUpToDateOverrider); ok {
		return o.VersionUpToDate(ctx)
	}

	latest, err := b.Finder.LatestVersion(ctx)
	if err != nil {
		return false, err
	}
	if latest == nil {
		return true, nil
	}
	if b.ExistingVersionIsSHA() {
		return strings.HasPrefix(latest.String(), b.Cfg.Dependency.Version), nil
	}
	// A lockfile that is up to date but has switched the dependency to a git
	// source shows up as a numeric version becoming a SHA. Treat that as up to
	// date so the dependency is left alone.
	if fortyHexRegexp.MatchString(latest.String()) {
		return true, nil
	}
	current := b.CurrentVersion()
	if current == nil {
		return false, nil
	}
	return latest.Compare(current) <= 0, nil
}

func (b *BaseChecker) requirementsUpToDate(ctx context.Context) (bool, error) {
	updated, err := b.Finder.UpdatedRequirements(ctx)
	if err != nil {
		return false, err
	}
	return !requirementsDiffer(updated, b.Cfg.Dependency.Requirements), nil
}

// CanUpdate reports whether an update is possible at the given unlock level.
func (b *BaseChecker) CanUpdate(ctx context.Context, unlock UnlockLevel) (bool, error) {
	if b.AllVersionsIgnored() {
		return false, nil
	}
	if b.Cfg.Dependency.Version == "" {
		// Full unlock for dependencies without a lockfile is not supported.
		if unlock == UnlockNone {
			return false, nil
		}
		return b.requirementsCanUpdate(ctx)
	}
	return b.versionCanUpdate(ctx, unlock)
}

func (b *BaseChecker) versionCanUpdate(ctx context.Context, unlock UnlockLevel) (bool, error) {
	if o, ok := b.Finder.(VersionCanUpdateOverrider); ok {
		return o.VersionCanUpdate(ctx, unlock)
	}

	upToDate, err := b.versionUpToDate(ctx)
	if err != nil || upToDate {
		return false, err
	}

	switch unlock {
	case UnlockNone:
		newVersion, err := b.Finder.LatestResolvableVersionWithNoUnlock(ctx)
		if err != nil || newVersion == nil {
			return false, err
		}
		if b.ExistingVersionIsSHA() {
			return !strings.HasPrefix(newVersion.String(), b.Cfg.Dependency.Version), nil
		}
		current := b.CurrentVersion()
		if current == nil {
			return false, nil
		}
		return newVersion.Compare(current) > 0, nil
	case UnlockOwn:
		return b.preferredVersionResolvableWithUnlock(ctx)
	case UnlockAll:
		// Full unlock is ecosystem-specific; neither ported ecosystem supports
		// it, matching go_modules and docker upstream.
		return false, nil
	default:
		return false, fmt.Errorf("unknown unlock level %q", unlock)
	}
}

func (b *BaseChecker) preferredVersionResolvableWithUnlock(ctx context.Context) (bool, error) {
	newVersion, err := b.PreferredResolvableVersion(ctx)
	if err != nil || newVersion == nil {
		return false, err
	}
	if b.ExistingVersionIsSHA() {
		if strings.HasPrefix(newVersion.String(), b.Cfg.Dependency.Version) {
			return false, nil
		}
	} else {
		current := b.CurrentVersion()
		if current != nil && newVersion.Compare(current) <= 0 {
			return false, nil
		}
	}
	updated, err := b.Finder.UpdatedRequirements(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range updated {
		if r.Unfixable {
			return false, nil
		}
	}
	return true, nil
}

func (b *BaseChecker) requirementsCanUpdate(ctx context.Context) (bool, error) {
	updated, err := b.Finder.UpdatedRequirements(ctx)
	if err != nil {
		return false, err
	}
	changed := false
	for _, r := range updated {
		if r.Unfixable {
			return false, nil
		}
		if !containsRequirement(b.Cfg.Dependency.Requirements, r) {
			changed = true
		}
	}
	return changed, nil
}

// UpdatedDependencies returns the dependency as it should look after the
// update, or an empty slice when no update is possible.
func (b *BaseChecker) UpdatedDependencies(ctx context.Context, unlock UnlockLevel) ([]*Dependency, error) {
	can, err := b.CanUpdate(ctx, unlock)
	if err != nil || !can {
		return nil, err
	}

	switch unlock {
	case UnlockNone:
		version, err := b.Finder.LatestResolvableVersionWithNoUnlock(ctx)
		if err != nil {
			return nil, err
		}
		return []*Dependency{b.updatedDependency(version, b.Cfg.Dependency.Requirements)}, nil
	case UnlockOwn:
		version, err := b.PreferredResolvableVersion(ctx)
		if err != nil {
			return nil, err
		}
		requirements, err := b.Finder.UpdatedRequirements(ctx)
		if err != nil {
			return nil, err
		}
		return []*Dependency{b.updatedDependency(version, requirements)}, nil
	case UnlockAll:
		return nil, fmt.Errorf("full unlock is not supported for %s", b.Cfg.Dependency.PackageManager)
	default:
		return nil, fmt.Errorf("unknown unlock level %q", unlock)
	}
}

func (b *BaseChecker) updatedDependency(version Version, requirements []Requirement) *Dependency {
	dep := b.Cfg.Dependency
	newVersion := ""
	if version != nil {
		newVersion = version.String()
	}
	return &Dependency{
		Name:                  dep.Name,
		Version:               newVersion,
		Requirements:          requirements,
		PreviousVersion:       dep.Version,
		PreviousRequirements:  dep.Requirements,
		PackageManager:        dep.PackageManager,
		Directory:             dep.Directory,
		Metadata:              dep.Metadata,
		SubdependencyMetadata: dep.SubdependencyMetadata,
	}
}

func requirementsDiffer(a, b []Requirement) bool {
	for _, r := range a {
		if !containsRequirement(b, r) {
			return true
		}
	}
	return false
}

func containsRequirement(reqs []Requirement, target Requirement) bool {
	for _, r := range reqs {
		if r.Equal(target) {
			return true
		}
	}
	return false
}
