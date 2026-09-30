package dependabot

import (
	"context"
	"errors"
	"testing"
)

func checkerFor(cfg CheckerConfig, finder *stubFinder) *testChecker {
	return newTestChecker(testEcosystem{}, cfg, finder)
}

func versioned(version string, requirement string) *Dependency {
	dep := Dependency{Name: "dep", PackageManager: testEcosystemName, Version: version}
	if requirement != "" {
		dep.Requirements = []Requirement{{Requirement: requirement, File: "manifest"}}
	}
	return MustNewDependency(dep)
}

func TestCurrentVersion(t *testing.T) {
	c := checkerFor(CheckerConfig{Dependency: versioned("1.2.3", "1.2.3")}, &stubFinder{})
	if v := c.CurrentVersion(); v == nil || v.String() != "1.2.3" {
		t.Errorf("CurrentVersion = %v, want 1.2.3", v)
	}

	// A version the ecosystem cannot parse is no version at all.
	sha := checkerFor(CheckerConfig{
		Dependency: versioned("0b84568fffcc1cbbf2b3dcdd9e7b4a6c5d8e9f0a", ""),
	}, &stubFinder{})
	if v := sha.CurrentVersion(); v != nil {
		t.Errorf("CurrentVersion = %v, want nil for a commit", v)
	}
	if !sha.ExistingVersionIsSHA() {
		t.Error("ExistingVersionIsSHA = false, want true")
	}
}

func TestIgnored(t *testing.T) {
	c := checkerFor(CheckerConfig{
		Dependency:      versioned("1.0.0", "1.0.0"),
		IgnoredVersions: []string{">= 2.0.0, < 3.0.0", "1.5.0"},
	}, &stubFinder{})

	cases := map[string]bool{
		"1.4.0": false,
		"1.5.0": true,
		"2.5.0": true,
		"3.0.0": false,
	}
	for version, want := range cases {
		if got := c.Ignored(mustTestVersion(version)); got != want {
			t.Errorf("Ignored(%q) = %v, want %v", version, got, want)
		}
	}
}

// An unparseable ignore condition is skipped rather than failing the run; the
// other conditions still apply.
func TestIgnoreConstraintsSkipsUnparseable(t *testing.T) {
	c := checkerFor(CheckerConfig{
		Dependency:      versioned("1.0.0", "1.0.0"),
		IgnoredVersions: []string{">= nonsense", ">= 2.0.0"},
	}, &stubFinder{})

	if len(c.IgnoreConstraints()) != 1 {
		t.Errorf("got %d constraints, want the one that parsed", len(c.IgnoreConstraints()))
	}
	if !c.Ignored(mustTestVersion("2.1.0")) {
		t.Error("the parseable condition should still apply")
	}
}

func TestAllVersionsIgnored(t *testing.T) {
	cases := map[string]bool{
		">= 0":     true,
		">=0":      true,
		">=  0":    true,
		">= 1.0.0": false,
	}
	for condition, want := range cases {
		c := checkerFor(CheckerConfig{
			Dependency:      versioned("1.0.0", "1.0.0"),
			IgnoredVersions: []string{condition},
		}, &stubFinder{})
		if got := c.AllVersionsIgnored(); got != want {
			t.Errorf("AllVersionsIgnored with %q = %v, want %v", condition, got, want)
		}
	}
}

func TestRequirementsUnlockedOrCanBe(t *testing.T) {
	unlocked := checkerFor(CheckerConfig{Dependency: versioned("1.0.0", "1.0.0")}, &stubFinder{})
	if !unlocked.RequirementsUnlockedOrCanBe() {
		t.Error("requirements should be rewritable by default")
	}

	locked := checkerFor(CheckerConfig{
		Dependency: versioned("1.0.0", "1.0.0"),
		Strategy:   StrategyLockfileOnly,
	}, &stubFinder{})
	if locked.RequirementsUnlockedOrCanBe() {
		t.Error("a lockfile-only job must not rewrite manifest requirements")
	}
}

func TestVulnerable(t *testing.T) {
	a := advisory(t, []string{"< 1.2.0"}, nil)

	affected := checkerFor(CheckerConfig{
		Dependency:         versioned("1.1.0", "1.1.0"),
		SecurityAdvisories: []*SecurityAdvisory{a},
	}, &stubFinder{})
	if !affected.Vulnerable() {
		t.Error("Vulnerable = false, want true")
	}

	safe := checkerFor(CheckerConfig{
		Dependency:         versioned("1.2.0", "1.2.0"),
		SecurityAdvisories: []*SecurityAdvisory{a},
	}, &stubFinder{})
	if safe.Vulnerable() {
		t.Error("Vulnerable = true, want false")
	}

	// A dependency pinned to a commit cannot be placed in a version range.
	pinned := checkerFor(CheckerConfig{
		Dependency:         versioned("0b84568fffcc1cbbf2b3dcdd9e7b4a6c5d8e9f0a", ""),
		SecurityAdvisories: []*SecurityAdvisory{a},
	}, &stubFinder{})
	if pinned.Vulnerable() {
		t.Error("a commit-pinned dependency cannot be judged vulnerable")
	}
}

// A vulnerable dependency takes the smallest fix, not the latest release.
func TestPreferredResolvableVersion(t *testing.T) {
	a := advisory(t, []string{"< 1.2.0"}, nil)
	finder := &stubFinder{
		resolvable:  mustTestVersion("2.0.0"),
		securityFix: mustTestVersion("1.2.0"),
	}

	vulnerable := checkerFor(CheckerConfig{
		Dependency:         versioned("1.1.0", "1.1.0"),
		SecurityAdvisories: []*SecurityAdvisory{a},
	}, finder)
	got, err := vulnerable.PreferredResolvableVersion(context.Background())
	if err != nil {
		t.Fatalf("PreferredResolvableVersion: %v", err)
	}
	if got.String() != "1.2.0" {
		t.Errorf("PreferredResolvableVersion = %q, want the lowest fix", got.String())
	}

	// Without an advisory the latest resolvable version is the target.
	healthy := checkerFor(CheckerConfig{Dependency: versioned("1.1.0", "1.1.0")}, finder)
	got, err = healthy.PreferredResolvableVersion(context.Background())
	if err != nil {
		t.Fatalf("PreferredResolvableVersion: %v", err)
	}
	if got.String() != "2.0.0" {
		t.Errorf("PreferredResolvableVersion = %q, want 2.0.0", got.String())
	}
}

// When no version fixes the advisory, the latest resolvable one is still worth
// proposing.
func TestPreferredResolvableVersionFallsBack(t *testing.T) {
	a := advisory(t, []string{"< 1.2.0"}, nil)
	c := checkerFor(CheckerConfig{
		Dependency:         versioned("1.1.0", "1.1.0"),
		SecurityAdvisories: []*SecurityAdvisory{a},
	}, &stubFinder{resolvable: mustTestVersion("1.1.5")})

	got, err := c.PreferredResolvableVersion(context.Background())
	if err != nil {
		t.Fatalf("PreferredResolvableVersion: %v", err)
	}
	if got.String() != "1.1.5" {
		t.Errorf("PreferredResolvableVersion = %q, want 1.1.5", got.String())
	}
}

func TestUpToDateByVersion(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"1.0.0", "1.1.0", false},
		{"1.1.0", "1.1.0", true},
		{"1.2.0", "1.1.0", true},
	}
	for _, tc := range cases {
		c := checkerFor(CheckerConfig{Dependency: versioned(tc.current, tc.current)},
			&stubFinder{latest: mustTestVersion(tc.latest)})
		got, err := c.UpToDate(context.Background())
		if err != nil {
			t.Fatalf("UpToDate: %v", err)
		}
		if got != tc.want {
			t.Errorf("UpToDate(current %s, latest %s) = %v, want %v",
				tc.current, tc.latest, got, tc.want)
		}
	}
}

// Nothing to compare against means there is nothing to do.
func TestUpToDateWithNoLatestVersion(t *testing.T) {
	c := checkerFor(CheckerConfig{Dependency: versioned("1.0.0", "1.0.0")}, &stubFinder{})
	got, err := c.UpToDate(context.Background())
	if err != nil {
		t.Fatalf("UpToDate: %v", err)
	}
	if !got {
		t.Error("UpToDate = false, want true when there is no latest version")
	}
}

// Without a lockfile there is no version to compare, so the requirements
// decide.
func TestUpToDateByRequirements(t *testing.T) {
	dep := versioned("", "^1.0.0")

	unchanged := checkerFor(CheckerConfig{Dependency: dep},
		&stubFinder{requirements: []Requirement{{Requirement: "^1.0.0", File: "manifest"}}})
	got, err := unchanged.UpToDate(context.Background())
	if err != nil {
		t.Fatalf("UpToDate: %v", err)
	}
	if !got {
		t.Error("UpToDate = false, want true when the requirements are unchanged")
	}

	changed := checkerFor(CheckerConfig{Dependency: dep},
		&stubFinder{requirements: []Requirement{{Requirement: "^2.0.0", File: "manifest"}}})
	got, err = changed.UpToDate(context.Background())
	if err != nil {
		t.Fatalf("UpToDate: %v", err)
	}
	if got {
		t.Error("UpToDate = true, want false when the requirement would change")
	}
}

func TestCanUpdate(t *testing.T) {
	dep := versioned("1.0.0", "1.0.0")
	finder := &stubFinder{
		latest:       mustTestVersion("2.0.0"),
		resolvable:   mustTestVersion("2.0.0"),
		noUnlock:     mustTestVersion("1.5.0"),
		requirements: []Requirement{{Requirement: "2.0.0", File: "manifest"}},
	}

	cases := map[UnlockLevel]bool{
		UnlockNone: true,
		UnlockOwn:  true,
		// Neither ported ecosystem supports unlocking other dependencies.
		UnlockAll: false,
	}
	for unlock, want := range cases {
		c := checkerFor(CheckerConfig{Dependency: dep}, finder)
		got, err := c.CanUpdate(context.Background(), unlock)
		if err != nil {
			t.Fatalf("CanUpdate(%s): %v", unlock, err)
		}
		if got != want {
			t.Errorf("CanUpdate(%s) = %v, want %v", unlock, got, want)
		}
	}

	if _, err := checkerFor(CheckerConfig{Dependency: dep}, finder).
		CanUpdate(context.Background(), UnlockLevel("sideways")); err == nil {
		t.Error("expected an error for an unknown unlock level")
	}
}

// Ignoring every version leaves nothing to update to, whatever the registry
// says.
func TestCanUpdateWithEverythingIgnored(t *testing.T) {
	c := checkerFor(CheckerConfig{
		Dependency:      versioned("1.0.0", "1.0.0"),
		IgnoredVersions: []string{">= 0"},
	}, &stubFinder{latest: mustTestVersion("2.0.0"), resolvable: mustTestVersion("2.0.0")})

	got, err := c.CanUpdate(context.Background(), UnlockOwn)
	if err != nil {
		t.Fatalf("CanUpdate: %v", err)
	}
	if got {
		t.Error("CanUpdate = true, want false when every version is ignored")
	}
}

// An unfixable requirement means the checker could not produce something to
// write, so there is no update to make.
func TestCanUpdateWithUnfixableRequirement(t *testing.T) {
	c := checkerFor(CheckerConfig{Dependency: versioned("1.0.0", "1.0.0")}, &stubFinder{
		latest:       mustTestVersion("2.0.0"),
		resolvable:   mustTestVersion("2.0.0"),
		requirements: []Requirement{{File: "manifest", Unfixable: true}},
	})

	got, err := c.CanUpdate(context.Background(), UnlockOwn)
	if err != nil {
		t.Fatalf("CanUpdate: %v", err)
	}
	if got {
		t.Error("CanUpdate = true, want false for an unfixable requirement")
	}
}

func TestUpdatedDependencies(t *testing.T) {
	dep := versioned("1.0.0", "1.0.0")
	c := checkerFor(CheckerConfig{Dependency: dep}, &stubFinder{
		latest:       mustTestVersion("2.0.0"),
		resolvable:   mustTestVersion("2.0.0"),
		requirements: []Requirement{{Requirement: "2.0.0", File: "manifest"}},
	})

	updated, err := c.UpdatedDependencies(context.Background(), UnlockOwn)
	if err != nil {
		t.Fatalf("UpdatedDependencies: %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("got %d dependencies, want 1", len(updated))
	}
	got := updated[0]
	if got.Version != "2.0.0" || got.PreviousVersion != "1.0.0" {
		t.Errorf("version %q from %q, want 2.0.0 from 1.0.0", got.Version, got.PreviousVersion)
	}
	if len(got.Requirements) != 1 || got.Requirements[0].Requirement != "2.0.0" {
		t.Errorf("requirements = %+v, want the updated one", got.Requirements)
	}
	if len(got.PreviousRequirements) != 1 || got.PreviousRequirements[0].Requirement != "1.0.0" {
		t.Errorf("previous requirements = %+v, want the original", got.PreviousRequirements)
	}
	// The original must not have been mutated.
	if dep.Version != "1.0.0" {
		t.Error("the checker mutated the dependency it was given")
	}
}

// A no-unlock update moves the version but leaves the manifest alone.
func TestUpdatedDependenciesWithNoUnlock(t *testing.T) {
	c := checkerFor(CheckerConfig{Dependency: versioned("1.0.0", "1.0.0")}, &stubFinder{
		latest:   mustTestVersion("2.0.0"),
		noUnlock: mustTestVersion("1.5.0"),
	})

	updated, err := c.UpdatedDependencies(context.Background(), UnlockNone)
	if err != nil {
		t.Fatalf("UpdatedDependencies: %v", err)
	}
	if len(updated) != 1 || updated[0].Version != "1.5.0" {
		t.Fatalf("got %+v, want a move to 1.5.0", updated)
	}
	if updated[0].Requirements[0].Requirement != "1.0.0" {
		t.Error("a no-unlock update must leave the requirement alone")
	}
}

func TestUpdatedDependenciesWhenUpToDate(t *testing.T) {
	c := checkerFor(CheckerConfig{Dependency: versioned("2.0.0", "2.0.0")},
		&stubFinder{latest: mustTestVersion("2.0.0")})

	updated, err := c.UpdatedDependencies(context.Background(), UnlockOwn)
	if err != nil {
		t.Fatalf("UpdatedDependencies: %v", err)
	}
	if len(updated) != 0 {
		t.Errorf("got %d dependencies, want none", len(updated))
	}
}

func TestCheckerPropagatesFinderErrors(t *testing.T) {
	wantErr := errors.New("registry is down")
	c := checkerFor(CheckerConfig{Dependency: versioned("1.0.0", "1.0.0")},
		&stubFinder{err: wantErr})

	if _, err := c.UpToDate(context.Background()); !errors.Is(err, wantErr) {
		t.Errorf("UpToDate error = %v, want %v", err, wantErr)
	}
	if _, err := c.CanUpdate(context.Background(), UnlockOwn); !errors.Is(err, wantErr) {
		t.Errorf("CanUpdate error = %v, want %v", err, wantErr)
	}
}
