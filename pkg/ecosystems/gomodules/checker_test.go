package gomodules

import (
	"context"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

const testModule = "github.com/spf13/cobra"

func moduleDependency(version string) *dependabot.Dependency {
	return dependabot.MustNewDependency(dependabot.Dependency{
		Name:           testModule,
		Version:        version,
		PackageManager: EcosystemName,
		Requirements: []dependabot.Requirement{{
			Requirement: version, File: GoModFile,
			Source: &dependabot.Source{Type: "default", Extra: map[string]string{"source": testModule}},
		}},
	})
}

func newTestChecker(t *testing.T, published []string, cfg dependabot.CheckerConfig) *Checker {
	t.Helper()
	proxy := (&fakeProxy{versions: map[string][]string{testModule: published}}).start(t)
	return NewChecker(New(), cfg, proxy)
}

func TestCheckerLatestVersion(t *testing.T) {
	checker := newTestChecker(t,
		[]string{"v1.8.0", "v1.8.1", "v1.9.0", "v2.0.0-rc.1"},
		dependabot.CheckerConfig{Dependency: moduleDependency("v1.8.0")})

	latest, err := checker.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	// The release candidate is skipped: the project is on a stable version.
	if latest.String() != "v1.9.0" {
		t.Errorf("LatestVersion = %q, want v1.9.0", latest.String())
	}
}

// A project already on a prerelease has opted into them.
func TestCheckerAllowsPrereleasesWhenOnOne(t *testing.T) {
	checker := newTestChecker(t,
		[]string{"v1.9.0", "v2.0.0-rc.1", "v2.0.0-rc.2"},
		dependabot.CheckerConfig{Dependency: moduleDependency("v2.0.0-rc.1")})

	latest, err := checker.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if latest.String() != "v2.0.0-rc.2" {
		t.Errorf("LatestVersion = %q, want v2.0.0-rc.2", latest.String())
	}
}

func TestCheckerIgnoredVersions(t *testing.T) {
	checker := newTestChecker(t,
		[]string{"v1.8.0", "v1.8.1", "v1.9.0"},
		dependabot.CheckerConfig{
			Dependency:      moduleDependency("v1.8.0"),
			IgnoredVersions: []string{">= 1.9.0"},
		})

	latest, err := checker.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if latest.String() != "v1.8.1" {
		t.Errorf("LatestVersion = %q, want v1.8.1 (v1.9.0 is ignored)", latest.String())
	}
}

// The proxy never lists pseudo-versions, so one has to be folded back in or
// the "latest" would sort below where the project already is.
func TestCheckerKeepsCurrentPseudoVersion(t *testing.T) {
	current := "v1.8.2-0.20260320110106-0b84568fffcc"
	checker := newTestChecker(t,
		[]string{"v1.8.0", "v1.8.1"},
		dependabot.CheckerConfig{Dependency: moduleDependency(current)})

	latest, err := checker.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if latest.String() != current {
		t.Errorf("LatestVersion = %q, want the current pseudo-version", latest.String())
	}

	upToDate, err := checker.UpToDate(context.Background())
	if err != nil {
		t.Fatalf("UpToDate: %v", err)
	}
	if !upToDate {
		t.Error("a pseudo-version ahead of every tag should be up to date")
	}
}

func TestCheckerUpToDate(t *testing.T) {
	checker := newTestChecker(t,
		[]string{"v1.8.0", "v1.9.0"},
		dependabot.CheckerConfig{Dependency: moduleDependency("v1.9.0")})

	upToDate, err := checker.UpToDate(context.Background())
	if err != nil {
		t.Fatalf("UpToDate: %v", err)
	}
	if !upToDate {
		t.Error("v1.9.0 should be up to date when it is the highest version")
	}
}

func TestCheckerUpdatedRequirements(t *testing.T) {
	checker := newTestChecker(t,
		[]string{"v1.8.0", "v1.9.0"},
		dependabot.CheckerConfig{Dependency: moduleDependency("v1.8.0")})

	updated, err := checker.UpdatedRequirements(context.Background())
	if err != nil {
		t.Fatalf("UpdatedRequirements: %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("got %d requirements, want 1", len(updated))
	}
	// go.mod always wants the "v" prefix, whatever the input looked like.
	if updated[0].Requirement != "v1.9.0" {
		t.Errorf("requirement = %q, want v1.9.0", updated[0].Requirement)
	}
	if updated[0].Unfixable {
		t.Error("requirement should not be marked unfixable")
	}
}

// With no version to move to, the requirement is unfixable rather than
// silently unchanged.
func TestCheckerUpdatedRequirementsUnfixable(t *testing.T) {
	checker := newTestChecker(t,
		[]string{"v1.9.0"},
		dependabot.CheckerConfig{
			Dependency:      moduleDependency("v1.9.0"),
			IgnoredVersions: []string{">= 0"},
		})

	updated, err := checker.UpdatedRequirements(context.Background())
	if err != nil {
		t.Fatalf("UpdatedRequirements: %v", err)
	}
	if len(updated) != 1 || !updated[0].Unfixable {
		t.Errorf("requirement = %+v, want it marked unfixable", updated[0])
	}
}

func TestCheckerLowestSecurityFixVersion(t *testing.T) {
	advisory, err := dependabot.NewSecurityAdvisory(
		testModule, EcosystemName, []string{"< 1.8.2"}, nil)
	if err != nil {
		t.Fatalf("NewSecurityAdvisory: %v", err)
	}

	checker := newTestChecker(t,
		[]string{"v1.8.0", "v1.8.1", "v1.8.2", "v1.9.0"},
		dependabot.CheckerConfig{
			Dependency:         moduleDependency("v1.8.0"),
			SecurityAdvisories: []*dependabot.SecurityAdvisory{advisory},
		})

	// The lowest fix, not the latest release: a security update should be the
	// smallest move that resolves the advisory.
	lowest, err := checker.LowestSecurityFixVersion(context.Background())
	if err != nil {
		t.Fatalf("LowestSecurityFixVersion: %v", err)
	}
	if lowest == nil || lowest.String() != "v1.8.2" {
		t.Errorf("LowestSecurityFixVersion = %v, want v1.8.2", lowest)
	}
}

// An advisory's fix boundary can be a pseudo-version the proxy will never
// list, so it is pulled out of the advisory text to stay a candidate.
func TestCheckerSecurityFixFromPseudoVersion(t *testing.T) {
	fix := "v1.8.2-0.20260320110106-0b84568fffcc"
	advisory, err := dependabot.NewSecurityAdvisory(
		testModule, EcosystemName, []string{"< " + fix}, nil)
	if err != nil {
		t.Fatalf("NewSecurityAdvisory: %v", err)
	}

	checker := newTestChecker(t,
		[]string{"v1.8.0", "v1.8.1"},
		dependabot.CheckerConfig{
			Dependency:         moduleDependency("v1.8.0"),
			SecurityAdvisories: []*dependabot.SecurityAdvisory{advisory},
		})

	lowest, err := checker.LowestSecurityFixVersion(context.Background())
	if err != nil {
		t.Fatalf("LowestSecurityFixVersion: %v", err)
	}
	if lowest == nil || lowest.String() != fix {
		t.Errorf("LowestSecurityFixVersion = %v, want %s", lowest, fix)
	}
}

// The version list is fetched once, however many questions are asked of the
// checker.
func TestCheckerCachesVersions(t *testing.T) {
	fake := &fakeProxy{versions: map[string][]string{testModule: {"v1.8.0", "v1.9.0"}}}
	proxy := fake.start(t)
	checker := NewChecker(New(), dependabot.CheckerConfig{
		Dependency: moduleDependency("v1.8.0"),
	}, proxy)

	ctx := context.Background()
	if _, err := checker.LatestVersion(ctx); err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if _, err := checker.LatestResolvableVersion(ctx); err != nil {
		t.Fatalf("LatestResolvableVersion: %v", err)
	}
	if _, err := checker.UpdatedRequirements(ctx); err != nil {
		t.Fatalf("UpdatedRequirements: %v", err)
	}
	if fake.requests != 1 {
		t.Errorf("made %d proxy requests, want 1", fake.requests)
	}
}
