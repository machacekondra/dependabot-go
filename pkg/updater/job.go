// Package updater orchestrates a single update run: fetch the project's files,
// parse them into dependencies, check each one and turn the ones that moved
// into dependency changes.
//
// This is the Go equivalent of dependabot-core's updater/ service, reduced to
// the UpdateAllVersions operation. Grouped updates, security-only runs against
// an existing pull request, refresh operations and the API client that reports
// back to the Dependabot service are not ported; see PORTING.md.
package updater

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// Dependency types an allowed_updates rule can name.
const (
	DependencyTypeAll         = "all"
	DependencyTypeDirect      = "direct"
	DependencyTypeIndirect    = "indirect"
	DependencyTypeProduction  = "production"
	DependencyTypeDevelopment = "development"
)

// Update types an allowed_updates rule can name.
const (
	UpdateTypeAll      = "all"
	UpdateTypeSecurity = "security"
)

// Update types an ignore condition can name.
const (
	IgnoreMajor = "version-update:semver-major"
	IgnoreMinor = "version-update:semver-minor"
	IgnorePatch = "version-update:semver-patch"
)

// AllowedUpdate is one allowed_updates rule from the job definition.
type AllowedUpdate struct {
	// DependencyName is a glob; empty matches every dependency.
	DependencyName string
	DependencyType string
	UpdateType     string
}

// IgnoreCondition is one ignore rule from the job definition.
type IgnoreCondition struct {
	// DependencyName is a glob matched case-insensitively.
	DependencyName string
	// VersionRequirement is an ecosystem requirement string such as "> 1.2".
	VersionRequirement string
	// UpdateTypes names semver bump levels to ignore wholesale.
	UpdateTypes []string
}

// PullRequest is an open or already-created pull request, used to avoid
// proposing the same update twice.
type PullRequest struct {
	Number       int
	Dependencies []PullRequestDependency
}

// PullRequestDependency is one dependency a pull request updates.
type PullRequestDependency struct {
	Name      string
	Version   string
	Removed   bool
	Directory string
}

// Job is one update run's configuration, the Go form of Dependabot::Job.
type Job struct {
	PackageManager   string
	Source           *dependabot.RepoSource
	RepoContentsPath string
	Credentials      []*dependabot.Credential

	// Dependencies narrows the run to named dependencies. Empty means all.
	Dependencies []string

	AllowedUpdates   []AllowedUpdate
	IgnoreConditions []IgnoreCondition

	SecurityAdvisories  []*dependabot.SecurityAdvisory
	SecurityUpdatesOnly bool

	RequirementsUpdateStrategy dependabot.RequirementsUpdateStrategy
	ExistingPullRequests       []PullRequest

	// Options carries experiment flags through to the ecosystem classes.
	Options map[string]any
}

// Directory is the directory the job updates, defaulting to the repo root.
func (j *Job) Directory() string {
	if j.Source == nil || j.Source.Directory == "" {
		return "/"
	}
	return path.Clean("/" + strings.TrimLeft(j.Source.Directory, "/"))
}

// AllowedUpdate reports whether the job's allowed_updates rules cover a
// dependency.
//
// A job with no rules at all allows every direct dependency, which is what the
// service sends for a default configuration.
func (j *Job) AllowedUpdate(dep *dependabot.Dependency) bool {
	if len(j.Dependencies) > 0 && !containsFold(j.Dependencies, dep.Name) {
		return false
	}

	rules := j.AllowedUpdates
	if len(rules) == 0 {
		rules = []AllowedUpdate{{DependencyType: DependencyTypeDirect, UpdateType: UpdateTypeAll}}
	}

	for _, rule := range rules {
		if !matchesGlob(rule.DependencyName, dep.Name) {
			continue
		}
		if !j.matchesDependencyType(rule.DependencyType, dep) {
			continue
		}
		if rule.UpdateType == UpdateTypeSecurity && !j.vulnerable(dep) {
			continue
		}
		// A security-only job never proposes a routine version bump, whatever
		// the rule says.
		if j.SecurityUpdatesOnly && !j.vulnerable(dep) {
			continue
		}
		return true
	}
	return false
}

func (j *Job) matchesDependencyType(dependencyType string, dep *dependabot.Dependency) bool {
	switch dependencyType {
	case "", DependencyTypeAll:
		return true
	case DependencyTypeDirect:
		return dep.TopLevel()
	case DependencyTypeIndirect:
		return !dep.TopLevel()
	case DependencyTypeProduction:
		return dep.Production()
	case DependencyTypeDevelopment:
		return !dep.Production()
	default:
		return false
	}
}

func (j *Job) vulnerable(dep *dependabot.Dependency) bool {
	for _, advisory := range j.SecurityAdvisoriesFor(dep) {
		if advisory.AffectsVersionString(dep.Version) {
			return true
		}
	}
	return false
}

// SecurityAdvisoriesFor returns the advisories that name a dependency.
func (j *Job) SecurityAdvisoriesFor(dep *dependabot.Dependency) []*dependabot.SecurityAdvisory {
	var out []*dependabot.SecurityAdvisory
	for _, advisory := range j.SecurityAdvisories {
		if strings.EqualFold(advisory.DependencyName, dep.Name) {
			out = append(out, advisory)
		}
	}
	return out
}

// IgnoreConditionsFor returns the version requirements to ignore for a
// dependency, in the form the ecosystem's constraint parser expects.
//
// Semver update types are translated into requirement strings against the
// dependency's current version, so that ignoring major bumps still lets a minor
// one through rather than blocking the dependency outright.
func (j *Job) IgnoreConditionsFor(dep *dependabot.Dependency) []string {
	var out []string
	for _, condition := range j.conditionsFor(dep) {
		var requirements []string
		if condition.VersionRequirement != "" {
			requirements = []string{condition.VersionRequirement}
		}

		switch {
		case j.SecurityUpdatesOnly:
			// A security job ignores only what was named explicitly: a bump
			// level is no reason to leave a vulnerability in place.
		case condition.VersionRequirement == "" && len(condition.UpdateTypes) == 0:
			// A condition naming neither means "ignore this dependency".
			requirements = []string{">= 0"}
		default:
			requirements = append(
				ignoredVersionsForUpdateTypes(dep.Version, condition.UpdateTypes), requirements...)
		}

		for _, requirement := range requirements {
			if !containsFold(out, requirement) {
				out = append(out, requirement)
			}
		}
	}
	return out
}

// ignoredVersionsForUpdateTypes turns semver bump levels into the requirement
// strings that exclude them, relative to the version in use.
func ignoredVersionsForUpdateTypes(version string, updateTypes []string) []string {
	if version == "" || len(updateTypes) == 0 {
		return nil
	}
	major, minor, ok := semverPrefix(version)
	if !ok {
		return nil
	}

	var out []string
	for _, updateType := range updateTypes {
		switch strings.ToLower(strings.TrimSpace(updateType)) {
		case IgnorePatch:
			out = append(out, fmt.Sprintf("> %s, < %d.%d", version, major, minor+1))
		case IgnoreMinor:
			out = append(out, fmt.Sprintf(">= %d.%d.a, < %d", major, minor+1, major+1))
		case IgnoreMajor:
			// The "a" suffix sorts below every prerelease, so the bound also
			// catches the next major's release candidates.
			out = append(out, fmt.Sprintf(">= %d.a", major+1))
		}
	}
	return out
}

// semverPrefix reads the leading major and minor numbers of a version, treating
// a missing minor as zero. A leading "v", which Go module versions carry, is
// not part of the number.
func semverPrefix(version string) (int, int, bool) {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor := 0
	if len(parts) > 1 {
		// A minor segment may carry a prerelease suffix, e.g. "2-rc1".
		if n, err := strconv.Atoi(leadingDigits(parts[1])); err == nil {
			minor = n
		}
	}
	return major, minor, true
}

func leadingDigits(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}

func (j *Job) conditionsFor(dep *dependabot.Dependency) []IgnoreCondition {
	var out []IgnoreCondition
	for _, condition := range j.IgnoreConditions {
		if matchesGlob(condition.DependencyName, dep.Name) {
			out = append(out, condition)
		}
	}
	return out
}

// ExistingPullRequestFor returns an open pull request that already proposes the
// given set of updated dependencies, or nil.
func (j *Job) ExistingPullRequestFor(deps []*dependabot.Dependency) *PullRequest {
	want := pullRequestKey(deps, j.Directory())
	for i := range j.ExistingPullRequests {
		if pullRequestKeyOf(&j.ExistingPullRequests[i]) == want {
			return &j.ExistingPullRequests[i]
		}
	}
	return nil
}

// ExistingPullRequestForVersion returns an open pull request that already
// updates a dependency to a version, or nil.
func (j *Job) ExistingPullRequestForVersion(name, version string) *PullRequest {
	for i := range j.ExistingPullRequests {
		for _, dep := range j.ExistingPullRequests[i].Dependencies {
			if !strings.EqualFold(dep.Name, name) || dep.Version != version {
				continue
			}
			// An older pull request may predate directory tracking, in which
			// case the name and version alone identify it.
			if dep.Directory == "" || dep.Directory == j.Directory() {
				return &j.ExistingPullRequests[i]
			}
		}
	}
	return nil
}

func pullRequestKey(deps []*dependabot.Dependency, directory string) string {
	entries := make([]string, 0, len(deps))
	for _, dep := range deps {
		version := dep.Version
		if dep.Removed {
			version = "removed"
		}
		entries = append(entries, strings.ToLower(dep.Name)+"@"+version+"@"+directory)
	}
	return joinSorted(entries)
}

func pullRequestKeyOf(pr *PullRequest) string {
	entries := make([]string, 0, len(pr.Dependencies))
	for _, dep := range pr.Dependencies {
		version := dep.Version
		if dep.Removed {
			version = "removed"
		}
		entries = append(entries, strings.ToLower(dep.Name)+"@"+version+"@"+dep.Directory)
	}
	return joinSorted(entries)
}

func joinSorted(entries []string) string {
	sorted := append([]string(nil), entries...)
	for i := 1; i < len(sorted); i++ {
		for k := i; k > 0 && sorted[k] < sorted[k-1]; k-- {
			sorted[k], sorted[k-1] = sorted[k-1], sorted[k]
		}
	}
	return strings.Join(sorted, ",")
}

// matchesGlob matches a dependency name against a job glob, where "*" stands
// for any run of characters. An empty pattern matches everything.
func matchesGlob(pattern, name string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return strings.EqualFold(pattern, name)
	}
	parts := strings.Split(pattern, "*")
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	re, err := regexp.Compile(`(?i)^` + strings.Join(parts, `.*`) + `$`)
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

func containsFold(values []string, target string) bool {
	for _, v := range values {
		if strings.EqualFold(v, target) {
			return true
		}
	}
	return false
}
