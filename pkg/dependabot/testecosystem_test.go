package dependabot

import "context"

// testEcosystemName is the package manager the core tests register, so that
// the pieces which look an ecosystem up by name have one to find.
const testEcosystemName = "test"

type testEcosystem struct{}

var _ Ecosystem = (*testEcosystem)(nil)

func (testEcosystem) Name() string { return testEcosystemName }

func (testEcosystem) ParseVersion(s string) (Version, error) { return parseTestVersion(s) }

func (testEcosystem) ValidVersion(s string) bool {
	_, err := parseTestVersion(s)
	return err == nil
}

func (testEcosystem) ParseConstraints(s string) ([]VersionConstraint, error) {
	return ParseOperatorConstraints(s, parseTestVersion)
}

func (testEcosystem) NewFileFetcher(FetcherConfig) (FileFetcher, error)        { return nil, nil }
func (testEcosystem) NewFileParser(ParserConfig) (FileParser, error)           { return nil, nil }
func (testEcosystem) NewFileUpdater(UpdaterConfig) (FileUpdater, error)        { return nil, nil }
func (testEcosystem) NewMetadataFinder(MetadataConfig) (MetadataFinder, error) { return nil, nil }

func (e testEcosystem) NewUpdateChecker(cfg CheckerConfig) (UpdateChecker, error) {
	return newTestChecker(e, cfg, &stubFinder{}), nil
}

func init() {
	Register(testEcosystem{})
	// Anything in the "dev" group is not a production dependency.
	RegisterProductionCheck(testEcosystemName, func(groups []string) bool {
		for _, g := range groups {
			if g == "dev" {
				return false
			}
		}
		return true
	})
}

// stubFinder is a VersionFinder whose answers are set by the test.
type stubFinder struct {
	latest       Version
	resolvable   Version
	noUnlock     Version
	securityFix  Version
	requirements []Requirement
	err          error
}

var _ VersionFinder = (*stubFinder)(nil)

func (f *stubFinder) LatestVersion(context.Context) (Version, error) { return f.latest, f.err }

func (f *stubFinder) LatestResolvableVersion(context.Context) (Version, error) {
	return f.resolvable, f.err
}

func (f *stubFinder) LatestResolvableVersionWithNoUnlock(context.Context) (Version, error) {
	return f.noUnlock, f.err
}

func (f *stubFinder) LowestSecurityFixVersion(context.Context) (Version, error) {
	return f.securityFix, f.err
}

func (f *stubFinder) UpdatedRequirements(context.Context) ([]Requirement, error) {
	return f.requirements, f.err
}

// testChecker is a concrete UpdateChecker built from BaseChecker and a stub,
// which is how a real ecosystem assembles one.
type testChecker struct {
	*BaseChecker
	*stubFinder
}

var _ UpdateChecker = (*testChecker)(nil)

func newTestChecker(eco Ecosystem, cfg CheckerConfig, finder *stubFinder) *testChecker {
	c := &testChecker{BaseChecker: NewBaseChecker(eco, cfg), stubFinder: finder}
	c.BaseChecker.Finder = finder
	return c
}
