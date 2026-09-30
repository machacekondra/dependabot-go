package gomodules

import (
	"context"
	"strings"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// Checker decides whether a Go module dependency is out of date.
type Checker struct {
	*dependabot.BaseChecker
	proxy *Proxy

	cachedVersions []*Version
	versionsErr    error
	versionsLoaded bool
}

var _ dependabot.UpdateChecker = (*Checker)(nil)

// NewChecker builds an update checker for one dependency.
func NewChecker(eco dependabot.Ecosystem, cfg dependabot.CheckerConfig, proxy *Proxy) *Checker {
	c := &Checker{BaseChecker: dependabot.NewBaseChecker(eco, cfg), proxy: proxy}
	c.BaseChecker.Finder = c
	return c
}

// availableVersions lists the module's parseable tagged versions, fetched once
// per checker.
func (c *Checker) availableVersions(ctx context.Context) ([]*Version, error) {
	if c.versionsLoaded {
		return c.cachedVersions, c.versionsErr
	}
	c.versionsLoaded = true

	raw, err := c.proxy.Versions(ctx, c.Dependency().Name)
	if err != nil {
		c.versionsErr = err
		return nil, err
	}
	for _, s := range raw {
		if v, err := ParseVersion(s); err == nil {
			c.cachedVersions = append(c.cachedVersions, v)
		}
	}
	return c.cachedVersions, nil
}

// LatestVersion is the highest version the module has published, after
// prerelease and ignore filtering.
//
// Go resolution is cheap enough that Ruby aliases latest_resolvable_version to
// this rather than doing a separate resolvability check; the same holds here.
func (c *Checker) LatestVersion(ctx context.Context) (dependabot.Version, error) {
	candidates, err := c.availableVersions(ctx)
	if err != nil {
		return nil, err
	}

	candidates = c.filterPrereleases(candidates)
	candidates = c.filterIgnored(candidates)

	// A dependency pinned to a pseudo-version is not in the proxy's list.
	// Include it so the "latest" never sorts below where we already are.
	if current := c.currentVersion(); current != nil && current.IsPseudoVersion() {
		candidates = append(candidates, current)
	}

	latest := maxVersion(candidates)
	if latest == nil {
		return nil, nil
	}
	return latest, nil
}

// LatestResolvableVersion aliases LatestVersion, as Ruby does.
func (c *Checker) LatestResolvableVersion(ctx context.Context) (dependabot.Version, error) {
	return c.LatestVersion(ctx)
}

// LatestResolvableVersionWithNoUnlock is meaningless for Go modules, which have
// a single manifest and no separate lock constraint to respect.
func (c *Checker) LatestResolvableVersionWithNoUnlock(_ context.Context) (dependabot.Version, error) {
	return nil, nil
}

// LowestSecurityFixVersion is the lowest published version that is not
// affected by any of the job's advisories and is newer than the current one.
func (c *Checker) LowestSecurityFixVersion(ctx context.Context) (dependabot.Version, error) {
	candidates, err := c.availableVersions(ctx)
	if err != nil {
		return nil, err
	}

	candidates = c.filterPrereleases(candidates)

	// An advisory's fix boundary can be a pseudo-version, which the proxy will
	// never list. Pull those out of the advisory text so they stay candidates.
	// They are added after the prerelease filter because a pseudo-version looks
	// like a prerelease to semver but is a legitimate fix target.
	candidates = append(candidates, c.pseudoVersionsFromAdvisories()...)

	candidates = c.filterVulnerable(candidates)
	candidates = c.filterIgnored(candidates)
	candidates = c.filterLowerOrEqual(candidates)

	lowest := minVersion(candidates)
	if lowest == nil {
		return nil, nil
	}
	return lowest, nil
}

// UpdatedRequirements rewrites each requirement to name the proposed version.
func (c *Checker) UpdatedRequirements(ctx context.Context) ([]dependabot.Requirement, error) {
	target, err := c.PreferredResolvableVersion(ctx)
	if err != nil {
		return nil, err
	}

	updated := make([]dependabot.Requirement, 0, len(c.Dependency().Requirements))
	for _, req := range c.Dependency().Requirements {
		clone := req.Clone()
		if target == nil {
			clone.Unfixable = true
		} else if v, ok := target.(*Version); ok {
			// go.mod requires the "v" prefix, so write the canonical form.
			clone.Requirement = v.Canonical()
		} else {
			clone.Requirement = target.String()
		}
		updated = append(updated, clone)
	}
	return updated, nil
}

func (c *Checker) currentVersion() *Version {
	if v, ok := c.CurrentVersion().(*Version); ok {
		return v
	}
	return nil
}

// filterPrereleases drops prereleases unless the project is already on one.
func (c *Checker) filterPrereleases(versions []*Version) []*Version {
	if current := c.currentVersion(); current != nil && current.Prerelease() {
		return versions
	}
	out := versions[:0:0]
	for _, v := range versions {
		if !v.Prerelease() {
			out = append(out, v)
		}
	}
	return out
}

func (c *Checker) filterIgnored(versions []*Version) []*Version {
	out := versions[:0:0]
	for _, v := range versions {
		if !c.Ignored(v) {
			out = append(out, v)
		}
	}
	return out
}

// filterVulnerable drops versions that any advisory still affects.
func (c *Checker) filterVulnerable(versions []*Version) []*Version {
	out := versions[:0:0]
	for _, v := range versions {
		if !dependabot.AnyAffect(c.Cfg.SecurityAdvisories, v) {
			out = append(out, v)
		}
	}
	return out
}

// filterLowerOrEqual drops versions that would not move the dependency forward.
func (c *Checker) filterLowerOrEqual(versions []*Version) []*Version {
	current := c.currentVersion()
	if current == nil {
		return versions
	}
	out := versions[:0:0]
	for _, v := range versions {
		if v.Compare(current) > 0 {
			out = append(out, v)
		}
	}
	return out
}

// pseudoVersionsFromAdvisories extracts pseudo-version fix boundaries from the
// job's advisories, e.g. the "< 2.3.1-0.20260320110106-0b84568fffcc" in a
// vulnerable range.
func (c *Checker) pseudoVersionsFromAdvisories() []*Version {
	var out []*Version
	seen := map[string]bool{}

	for _, advisory := range c.Cfg.SecurityAdvisories {
		for _, rangeStr := range advisory.VulnerableVersionStrings {
			for _, constraint := range strings.Split(rangeStr, ",") {
				text := strings.TrimLeft(strings.TrimSpace(constraint), "<>=!~^ ")
				if !PseudoVersionRegexp.MatchString(text) || seen[text] {
					continue
				}
				v, err := ParseVersion(text)
				if err != nil {
					continue
				}
				seen[text] = true
				out = append(out, v)
			}
		}
	}
	return out
}

func maxVersion(versions []*Version) *Version {
	var max *Version
	for _, v := range versions {
		if max == nil || v.Compare(max) > 0 {
			max = v
		}
	}
	return max
}

func minVersion(versions []*Version) *Version {
	var min *Version
	for _, v := range versions {
		if min == nil || v.Compare(min) < 0 {
			min = v
		}
	}
	return min
}
