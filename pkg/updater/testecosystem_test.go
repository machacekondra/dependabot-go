package updater

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// fakeEcosystemName is the package manager the updater tests register. The
// runner reaches everything through the registry, so a fake registered under
// its own name exercises the real wiring.
const fakeEcosystemName = "fake"

// fakeState is the scripted behaviour of the fake ecosystem for one test.
// Tests assign to it before building a Runner.
type fakeState struct {
	files []*dependabot.DependencyFile
	deps  []*dependabot.Dependency

	// available maps a dependency name to the versions its registry offers.
	available map[string][]string

	sourceURL string

	fetchErr    error
	parseErr    error
	updateErr   error
	metadataErr error
}

var state *fakeState

func resetState() *fakeState {
	state = &fakeState{available: map[string][]string{}}
	return state
}

type fakeEcosystem struct{}

var _ dependabot.Ecosystem = fakeEcosystem{}

func (fakeEcosystem) Name() string { return fakeEcosystemName }

func (fakeEcosystem) ParseVersion(s string) (dependabot.Version, error) { return parseFakeVersion(s) }

func (fakeEcosystem) ValidVersion(s string) bool {
	_, err := parseFakeVersion(s)
	return err == nil
}

func (fakeEcosystem) ParseConstraints(s string) ([]dependabot.VersionConstraint, error) {
	return dependabot.ParseOperatorConstraints(s, parseFakeVersion)
}

func (fakeEcosystem) NewFileFetcher(dependabot.FetcherConfig) (dependabot.FileFetcher, error) {
	return fakeFetcher{}, nil
}

func (fakeEcosystem) NewFileParser(dependabot.ParserConfig) (dependabot.FileParser, error) {
	return fakeParser{}, nil
}

func (e fakeEcosystem) NewUpdateChecker(cfg dependabot.CheckerConfig) (dependabot.UpdateChecker, error) {
	c := &fakeChecker{BaseChecker: dependabot.NewBaseChecker(e, cfg)}
	c.BaseChecker.Finder = c
	return c, nil
}

func (fakeEcosystem) NewFileUpdater(cfg dependabot.UpdaterConfig) (dependabot.FileUpdater, error) {
	return fakeUpdater{cfg: cfg}, nil
}

func (fakeEcosystem) NewMetadataFinder(dependabot.MetadataConfig) (dependabot.MetadataFinder, error) {
	return fakeMetadataFinder{}, nil
}

func init() {
	dependabot.Register(fakeEcosystem{})
	dependabot.RegisterProductionCheck(fakeEcosystemName, func(groups []string) bool {
		for _, g := range groups {
			if g == "dev" {
				return false
			}
		}
		return true
	})
}

type fakeFetcher struct{}

func (fakeFetcher) FetchFiles(context.Context) ([]*dependabot.DependencyFile, error) {
	if state.fetchErr != nil {
		return nil, state.fetchErr
	}
	return state.files, nil
}

type fakeParser struct{}

func (fakeParser) Parse(context.Context) ([]*dependabot.Dependency, error) {
	if state.parseErr != nil {
		return nil, state.parseErr
	}
	out := make([]*dependabot.Dependency, 0, len(state.deps))
	for _, dep := range state.deps {
		out = append(out, dep.Clone())
	}
	return out, nil
}

// fakeChecker offers the highest scripted version that is not ignored.
type fakeChecker struct {
	*dependabot.BaseChecker
}

var _ dependabot.UpdateChecker = (*fakeChecker)(nil)

func (c *fakeChecker) candidates() []dependabot.Version {
	var out []dependabot.Version
	for _, raw := range state.available[c.Dependency().Name] {
		v, err := parseFakeVersion(raw)
		if err != nil || c.Ignored(v) {
			continue
		}
		out = append(out, v)
	}
	return out
}

func (c *fakeChecker) LatestVersion(context.Context) (dependabot.Version, error) {
	var highest dependabot.Version
	for _, v := range c.candidates() {
		if highest == nil || v.Compare(highest) > 0 {
			highest = v
		}
	}
	return highest, nil
}

func (c *fakeChecker) LatestResolvableVersion(ctx context.Context) (dependabot.Version, error) {
	return c.LatestVersion(ctx)
}

func (c *fakeChecker) LatestResolvableVersionWithNoUnlock(ctx context.Context) (dependabot.Version, error) {
	return c.LatestVersion(ctx)
}

func (c *fakeChecker) LowestSecurityFixVersion(ctx context.Context) (dependabot.Version, error) {
	var lowest dependabot.Version
	current := c.CurrentVersion()
	for _, v := range c.candidates() {
		if current != nil && v.Compare(current) <= 0 {
			continue
		}
		if dependabot.AnyAffect(c.Cfg.SecurityAdvisories, v) {
			continue
		}
		if lowest == nil || v.Compare(lowest) < 0 {
			lowest = v
		}
	}
	return lowest, nil
}

func (c *fakeChecker) UpdatedRequirements(ctx context.Context) ([]dependabot.Requirement, error) {
	target, err := c.PreferredResolvableVersion(ctx)
	if err != nil {
		return nil, err
	}
	updated := make([]dependabot.Requirement, 0, len(c.Dependency().Requirements))
	for _, req := range c.Dependency().Requirements {
		clone := req.Clone()
		if target == nil {
			clone.Unfixable = true
		} else {
			clone.Requirement = target.String()
		}
		updated = append(updated, clone)
	}
	return updated, nil
}

// fakeUpdater rewrites each file by replacing the previous version with the
// new one, which is enough to tell a real change from a no-op.
type fakeUpdater struct {
	cfg dependabot.UpdaterConfig
}

func (u fakeUpdater) UpdatedDependencyFiles(context.Context) ([]*dependabot.DependencyFile, error) {
	if state.updateErr != nil {
		return nil, state.updateErr
	}

	var updated []*dependabot.DependencyFile
	for _, file := range u.cfg.Files {
		content := file.ContentString()
		for _, dep := range u.cfg.Dependencies {
			if dep.PreviousVersion == "" || dep.Version == "" {
				continue
			}
			content = strings.ReplaceAll(content,
				dep.Name+" "+dep.PreviousVersion, dep.Name+" "+dep.Version)
		}
		if content != file.ContentString() {
			updated = append(updated, file.WithContent(content))
		}
	}
	if len(updated) == 0 {
		return nil, fmt.Errorf("no file changed")
	}
	return updated, nil
}

type fakeMetadataFinder struct{}

func (fakeMetadataFinder) SourceURL(context.Context) (string, error) {
	return state.sourceURL, state.metadataErr
}

// fakeVersion is a dotted-numeric version, enough to order the versions these
// tests script.
type fakeVersion struct {
	original string
	segments []int
	pre      string
}

var _ dependabot.Version = (*fakeVersion)(nil)

func parseFakeVersion(s string) (dependabot.Version, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, fmt.Errorf("empty version")
	}
	release, pre, _ := strings.Cut(trimmed, "-")
	v := &fakeVersion{original: s, pre: pre}
	for i, segment := range strings.Split(release, ".") {
		n, err := strconv.Atoi(segment)
		if err != nil {
			// A trailing non-numeric segment marks a prerelease, the way Ruby's
			// "1.3.a" does: it sorts below every release of 1.3. Ignore
			// conditions built from update types rely on that form.
			if i == 0 || v.pre != "" {
				return nil, fmt.Errorf("invalid version %q", s)
			}
			v.pre = segment
			break
		}
		v.segments = append(v.segments, n)
	}
	return v, nil
}

func (v *fakeVersion) String() string   { return v.original }
func (v *fakeVersion) Prerelease() bool { return v.pre != "" }

func (v *fakeVersion) Compare(other dependabot.Version) int {
	o, ok := other.(*fakeVersion)
	if !ok {
		return strings.Compare(v.original, other.String())
	}
	for i := 0; i < len(v.segments) || i < len(o.segments); i++ {
		a, b := segmentAt(v.segments, i), segmentAt(o.segments, i)
		if a != b {
			if a < b {
				return -1
			}
			return 1
		}
	}
	switch {
	case v.pre == o.pre:
		return 0
	case v.pre == "":
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
