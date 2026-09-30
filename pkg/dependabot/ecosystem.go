package dependabot

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// UnlockLevel says how much a checker is allowed to change when looking for an
// update: nothing but the lockfile, the dependency's own requirement, or the
// requirements of other dependencies too.
type UnlockLevel string

// Unlock levels, matching Ruby's :none / :own / :all symbols.
const (
	UnlockNone UnlockLevel = "none"
	UnlockOwn  UnlockLevel = "own"
	UnlockAll  UnlockLevel = "all"
)

// RequirementsUpdateStrategy is how an ecosystem should rewrite a manifest
// requirement when the resolved version moves.
type RequirementsUpdateStrategy string

// Requirements update strategies.
const (
	StrategyBumpVersions            RequirementsUpdateStrategy = "bump_versions"
	StrategyBumpVersionsIfNecessary RequirementsUpdateStrategy = "bump_versions_if_necessary"
	StrategyLockfileOnly            RequirementsUpdateStrategy = "lockfile_only"
	StrategyWidenRanges             RequirementsUpdateStrategy = "widen_ranges"
)

// LockfileOnly reports whether the strategy forbids touching manifests.
func (s RequirementsUpdateStrategy) LockfileOnly() bool { return s == StrategyLockfileOnly }

// FetcherConfig is the input to an ecosystem's FileFetcher.
type FetcherConfig struct {
	Source           *RepoSource
	Credentials      []*Credential
	RepoContentsPath string
	Options          map[string]any
}

// ParserConfig is the input to an ecosystem's FileParser.
type ParserConfig struct {
	Files              []*DependencyFile
	Source             *RepoSource
	RepoContentsPath   string
	Credentials        []*Credential
	RejectExternalCode bool
	Options            map[string]any
}

// CheckerConfig is the input to an ecosystem's UpdateChecker.
type CheckerConfig struct {
	Dependency         *Dependency
	Files              []*DependencyFile
	Credentials        []*Credential
	RepoContentsPath   string
	IgnoredVersions    []string
	SecurityAdvisories []*SecurityAdvisory
	Strategy           RequirementsUpdateStrategy
	Options            map[string]any
}

// UpdaterConfig is the input to an ecosystem's FileUpdater.
type UpdaterConfig struct {
	Dependencies     []*Dependency
	Files            []*DependencyFile
	Credentials      []*Credential
	RepoContentsPath string
	Options          map[string]any
}

// MetadataConfig is the input to an ecosystem's MetadataFinder.
type MetadataConfig struct {
	Dependency  *Dependency
	Credentials []*Credential
}

// FileFetcher fetches the files an ecosystem needs from a repository.
type FileFetcher interface {
	FetchFiles(ctx context.Context) ([]*DependencyFile, error)
}

// FileParser turns fetched files into dependencies.
type FileParser interface {
	Parse(ctx context.Context) ([]*Dependency, error)
}

// UpdateChecker decides whether a dependency is out of date and what it should
// become. Ecosystems implement the version-finding methods; the generic
// UpToDate / CanUpdate / UpdatedDependencies logic is supplied by embedding
// BaseChecker.
type UpdateChecker interface {
	// LatestVersion is the newest version that exists, ignoring whether the
	// project could actually resolve it.
	LatestVersion(ctx context.Context) (Version, error)
	// LatestResolvableVersion is the newest version resolvable given the
	// project's other dependencies.
	LatestResolvableVersion(ctx context.Context) (Version, error)
	// LatestResolvableVersionWithNoUnlock is the newest version reachable
	// without changing any manifest requirement.
	LatestResolvableVersionWithNoUnlock(ctx context.Context) (Version, error)
	// LowestSecurityFixVersion is the lowest version that fixes every advisory
	// affecting the dependency. Returns nil when there is none.
	LowestSecurityFixVersion(ctx context.Context) (Version, error)
	// UpdatedRequirements is the dependency's requirements rewritten for the
	// version this checker proposes.
	UpdatedRequirements(ctx context.Context) ([]Requirement, error)

	UpToDate(ctx context.Context) (bool, error)
	CanUpdate(ctx context.Context, unlock UnlockLevel) (bool, error)
	UpdatedDependencies(ctx context.Context, unlock UnlockLevel) ([]*Dependency, error)

	// RequirementsUnlockedOrCanBe reports whether manifest requirements may be
	// rewritten, which decides the unlock levels worth attempting.
	RequirementsUnlockedOrCanBe() bool
}

// FileUpdater writes updated dependencies back into manifests and lockfiles.
type FileUpdater interface {
	UpdatedDependencyFiles(ctx context.Context) ([]*DependencyFile, error)
}

// MetadataFinder locates a dependency's source repository, which the pull
// request builder uses to find changelogs and release notes.
type MetadataFinder interface {
	SourceURL(ctx context.Context) (string, error)
}

// Ecosystem is the contract every package manager implements. It mirrors the
// set of classes each dependabot-core gem defines.
type Ecosystem interface {
	// Name is the package-manager identifier, e.g. "go_modules".
	Name() string

	// ParseVersion parses an ecosystem version string.
	ParseVersion(s string) (Version, error)
	// ValidVersion reports whether ParseVersion would succeed.
	ValidVersion(s string) bool
	// ParseConstraints parses a requirement string such as ">= 1.0, < 2.0"
	// into the constraints it is made of.
	ParseConstraints(s string) ([]VersionConstraint, error)

	NewFileFetcher(cfg FetcherConfig) (FileFetcher, error)
	NewFileParser(cfg ParserConfig) (FileParser, error)
	NewUpdateChecker(cfg CheckerConfig) (UpdateChecker, error)
	NewFileUpdater(cfg UpdaterConfig) (FileUpdater, error)
	NewMetadataFinder(cfg MetadataConfig) (MetadataFinder, error)
}

// ProductionCheck decides whether a set of dependency groups means the
// dependency is used in production.
type ProductionCheck func(groups []string) bool

var (
	registryMu          sync.RWMutex
	ecosystems          = map[string]Ecosystem{}
	productionChecks    = map[string]ProductionCheck{}
	displayNameBuilders = map[string]func(string) string{}
)

// Register makes an ecosystem available by name. Ecosystem packages call this
// from an init function, the way each Ruby gem's top-level file registers its
// classes on require.
func Register(eco Ecosystem) {
	registryMu.Lock()
	defer registryMu.Unlock()
	ecosystems[eco.Name()] = eco
}

// RegisterProductionCheck records how an ecosystem decides that a dependency is
// a production dependency.
func RegisterProductionCheck(packageManager string, check ProductionCheck) {
	registryMu.Lock()
	defer registryMu.Unlock()
	productionChecks[packageManager] = check
}

// RegisterDisplayNameBuilder records how an ecosystem renders a dependency name
// for humans.
func RegisterDisplayNameBuilder(packageManager string, builder func(string) string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	displayNameBuilders[packageManager] = builder
}

// Lookup returns the ecosystem registered under name.
func Lookup(name string) (Ecosystem, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	eco, ok := ecosystems[name]
	if !ok {
		return nil, fmt.Errorf("unregistered package manager %q (is its package imported?)", name)
	}
	return eco, nil
}

// Registered lists the names of all registered ecosystems, sorted.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(ecosystems))
	for name := range ecosystems {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func productionCheckFor(packageManager string) ProductionCheck {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return productionChecks[packageManager]
}

func displayNameBuilderFor(packageManager string) func(string) string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return displayNameBuilders[packageManager]
}
