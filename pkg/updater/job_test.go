package updater

import (
	"strings"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

func dependency(name, version string, groups ...string) *dependabot.Dependency {
	return dependabot.MustNewDependency(dependabot.Dependency{
		Name: name, Version: version, PackageManager: fakeEcosystemName,
		Requirements: []dependabot.Requirement{{
			Requirement: version, File: "manifest", Groups: groups,
		}},
	})
}

func subdependency(name, version string) *dependabot.Dependency {
	return dependabot.MustNewDependency(dependabot.Dependency{
		Name: name, Version: version, PackageManager: fakeEcosystemName,
	})
}

func TestMatchesGlob(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"", "anything", true},
		{"*", "anything", true},
		{"rails", "rails", true},
		{"rails", "RAILS", true},
		{"rails", "rails-api", false},
		{"rails*", "rails-api", true},
		{"*-api", "rails-api", true},
		{"@types/*", "@types/node", true},
		{"@types/*", "typescript", false},
		// A dot is literal, not a wildcard.
		{"a.b", "axb", false},
	}
	for _, tc := range cases {
		if got := matchesGlob(tc.pattern, tc.name); got != tc.want {
			t.Errorf("matchesGlob(%q, %q) = %v, want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestJobDirectory(t *testing.T) {
	cases := map[string]string{
		"":        "/",
		"/":       "/",
		"sub":     "/sub",
		"/sub":    "/sub",
		"/sub/":   "/sub",
		"//a//b/": "/a/b",
	}
	for directory, want := range cases {
		job := &Job{Source: &dependabot.RepoSource{Directory: directory}}
		if got := job.Directory(); got != want {
			t.Errorf("Directory(%q) = %q, want %q", directory, got, want)
		}
	}
	if got := (&Job{}).Directory(); got != "/" {
		t.Errorf("Directory() with no source = %q, want /", got)
	}
}

// With no rules configured the service means "every direct dependency".
func TestAllowedUpdateDefault(t *testing.T) {
	job := &Job{}

	if !job.AllowedUpdate(dependency("rails", "7.0.0")) {
		t.Error("a direct dependency should be allowed by default")
	}
	if job.AllowedUpdate(subdependency("rack", "2.0.0")) {
		t.Error("an indirect dependency should not be allowed by default")
	}
}

func TestAllowedUpdateByDependencyType(t *testing.T) {
	direct := dependency("rails", "7.0.0")
	indirect := subdependency("rack", "2.0.0")
	dev := dependency("rspec", "3.0.0", "dev")

	cases := []struct {
		dependencyType string
		dep            *dependabot.Dependency
		want           bool
	}{
		{DependencyTypeAll, indirect, true},
		{DependencyTypeDirect, direct, true},
		{DependencyTypeDirect, indirect, false},
		{DependencyTypeIndirect, indirect, true},
		{DependencyTypeIndirect, direct, false},
		{DependencyTypeProduction, direct, true},
		{DependencyTypeProduction, dev, false},
		{DependencyTypeDevelopment, dev, true},
		{DependencyTypeDevelopment, direct, false},
		{"nonsense", direct, false},
	}
	for _, tc := range cases {
		job := &Job{AllowedUpdates: []AllowedUpdate{{DependencyType: tc.dependencyType}}}
		if got := job.AllowedUpdate(tc.dep); got != tc.want {
			t.Errorf("%s for %s = %v, want %v", tc.dependencyType, tc.dep.Name, got, tc.want)
		}
	}
}

func TestAllowedUpdateByName(t *testing.T) {
	job := &Job{AllowedUpdates: []AllowedUpdate{{DependencyName: "rails*"}}}

	if !job.AllowedUpdate(dependency("rails-api", "7.0.0")) {
		t.Error("a matching name should be allowed")
	}
	if job.AllowedUpdate(dependency("rack", "2.0.0")) {
		t.Error("a name outside the glob should not be allowed")
	}
}

// A job narrowed to named dependencies touches nothing else, whatever the
// allow rules say.
func TestAllowedUpdateNarrowedToNamedDependencies(t *testing.T) {
	job := &Job{
		Dependencies:   []string{"rails"},
		AllowedUpdates: []AllowedUpdate{{DependencyType: DependencyTypeAll}},
	}
	if !job.AllowedUpdate(dependency("rails", "7.0.0")) {
		t.Error("the named dependency should be allowed")
	}
	if job.AllowedUpdate(dependency("rack", "2.0.0")) {
		t.Error("an unnamed dependency should not be allowed")
	}
}

func TestAllowedUpdateSecurityOnly(t *testing.T) {
	vulnerable := dependency("rails", "7.0.0")
	healthy := dependency("rack", "2.0.0")

	advisory, err := dependabot.NewSecurityAdvisory(
		"rails", fakeEcosystemName, []string{"< 7.0.1"}, nil)
	if err != nil {
		t.Fatalf("NewSecurityAdvisory: %v", err)
	}

	job := &Job{
		SecurityUpdatesOnly: true,
		SecurityAdvisories:  []*dependabot.SecurityAdvisory{advisory},
		AllowedUpdates:      []AllowedUpdate{{DependencyType: DependencyTypeAll}},
	}
	if !job.AllowedUpdate(vulnerable) {
		t.Error("a vulnerable dependency should be allowed on a security job")
	}
	if job.AllowedUpdate(healthy) {
		t.Error("a healthy dependency should not be updated on a security job")
	}

	// The same holds for a rule that asks for security updates specifically.
	byRule := &Job{
		SecurityAdvisories: []*dependabot.SecurityAdvisory{advisory},
		AllowedUpdates:     []AllowedUpdate{{UpdateType: UpdateTypeSecurity}},
	}
	if !byRule.AllowedUpdate(vulnerable) {
		t.Error("a vulnerable dependency should match a security rule")
	}
	if byRule.AllowedUpdate(healthy) {
		t.Error("a healthy dependency should not match a security rule")
	}
}

func TestSecurityAdvisoriesFor(t *testing.T) {
	rails, err := dependabot.NewSecurityAdvisory("Rails", fakeEcosystemName, []string{"< 7.0.1"}, nil)
	if err != nil {
		t.Fatalf("NewSecurityAdvisory: %v", err)
	}
	rack, err := dependabot.NewSecurityAdvisory("rack", fakeEcosystemName, []string{"< 2.0.1"}, nil)
	if err != nil {
		t.Fatalf("NewSecurityAdvisory: %v", err)
	}

	job := &Job{SecurityAdvisories: []*dependabot.SecurityAdvisory{rails, rack}}
	got := job.SecurityAdvisoriesFor(dependency("rails", "7.0.0"))
	if len(got) != 1 || got[0] != rails {
		t.Errorf("got %d advisories, want the one naming rails (case-insensitively)", len(got))
	}
}

func TestIgnoreConditionsForVersionRequirement(t *testing.T) {
	job := &Job{IgnoreConditions: []IgnoreCondition{
		{DependencyName: "rails", VersionRequirement: ">= 8.0"},
		{DependencyName: "rack", VersionRequirement: ">= 3.0"},
	}}

	got := job.IgnoreConditionsFor(dependency("rails", "7.0.0"))
	if len(got) != 1 || got[0] != ">= 8.0" {
		t.Errorf("IgnoreConditionsFor = %v, want just the rails condition", got)
	}
}

// A condition naming neither a requirement nor an update type means "never
// update this dependency".
func TestIgnoreConditionsForWholeDependency(t *testing.T) {
	job := &Job{IgnoreConditions: []IgnoreCondition{{DependencyName: "rails"}}}

	got := job.IgnoreConditionsFor(dependency("rails", "7.0.0"))
	if len(got) != 1 || got[0] != ">= 0" {
		t.Errorf("IgnoreConditionsFor = %v, want [>= 0]", got)
	}
}

// Update types become requirement strings relative to the current version, so
// ignoring major bumps still lets a minor one through.
func TestIgnoreConditionsForUpdateTypes(t *testing.T) {
	cases := []struct {
		updateType string
		version    string
		want       string
	}{
		{IgnorePatch, "1.2.3", "> 1.2.3, < 1.3"},
		{IgnoreMinor, "1.2.3", ">= 1.3.a, < 2"},
		{IgnoreMajor, "1.2.3", ">= 2.a"},
		// A Go module version carries a "v" that is not part of the number.
		{IgnoreMajor, "v1.2.3", ">= 2.a"},
		// A prerelease suffix on the minor segment is not part of it either.
		{IgnoreMinor, "1.2-rc1", ">= 1.3.a, < 2"},
	}
	for _, tc := range cases {
		job := &Job{IgnoreConditions: []IgnoreCondition{
			{DependencyName: "rails", UpdateTypes: []string{tc.updateType}},
		}}
		got := job.IgnoreConditionsFor(dependency("rails", tc.version))
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s at %s gave %v, want [%s]", tc.updateType, tc.version, got, tc.want)
		}
	}
}

// Without a version there is nothing to compute a bump level against.
func TestIgnoreConditionsForUpdateTypesWithoutVersion(t *testing.T) {
	job := &Job{IgnoreConditions: []IgnoreCondition{
		{DependencyName: "rails", UpdateTypes: []string{IgnoreMajor}},
	}}
	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name: "rails", PackageManager: fakeEcosystemName,
		Requirements: []dependabot.Requirement{{Requirement: "^7.0", File: "manifest"}},
	})
	if got := job.IgnoreConditionsFor(dep); len(got) != 0 {
		t.Errorf("IgnoreConditionsFor = %v, want none", got)
	}
}

// A bump level is no reason to leave a vulnerability in place, so a security
// job honours only the explicit requirements.
func TestIgnoreConditionsForSecurityJob(t *testing.T) {
	job := &Job{
		SecurityUpdatesOnly: true,
		IgnoreConditions: []IgnoreCondition{
			{DependencyName: "rails", UpdateTypes: []string{IgnoreMajor}},
			{DependencyName: "rails", VersionRequirement: "= 8.0.1"},
		},
	}
	got := job.IgnoreConditionsFor(dependency("rails", "7.0.0"))
	if len(got) != 1 || got[0] != "= 8.0.1" {
		t.Errorf("IgnoreConditionsFor = %v, want just the explicit requirement", got)
	}
}

func TestIgnoreConditionsForDeduplicates(t *testing.T) {
	job := &Job{IgnoreConditions: []IgnoreCondition{
		{DependencyName: "rails", VersionRequirement: ">= 8.0"},
		{DependencyName: "*", VersionRequirement: ">= 8.0"},
	}}
	if got := job.IgnoreConditionsFor(dependency("rails", "7.0.0")); len(got) != 1 {
		t.Errorf("IgnoreConditionsFor = %v, want the duplicate collapsed", got)
	}
}

func TestExistingPullRequestForVersion(t *testing.T) {
	job := &Job{
		Source: &dependabot.RepoSource{Directory: "/app"},
		ExistingPullRequests: []PullRequest{
			{Number: 1, Dependencies: []PullRequestDependency{
				{Name: "rails", Version: "7.0.1", Directory: "/app"},
			}},
			// An older pull request may predate directory tracking.
			{Number: 2, Dependencies: []PullRequestDependency{
				{Name: "rack", Version: "2.0.1"},
			}},
			{Number: 3, Dependencies: []PullRequestDependency{
				{Name: "sinatra", Version: "3.0.0", Directory: "/other"},
			}},
		},
	}

	if pr := job.ExistingPullRequestFor(nil); pr != nil {
		t.Error("an empty dependency set should not match a pull request")
	}
	if pr := job.ExistingPullRequestForVersion("rails", "7.0.1"); pr == nil || pr.Number != 1 {
		t.Errorf("got %v, want pull request 1", pr)
	}
	// The name is matched the way a registry would.
	if pr := job.ExistingPullRequestForVersion("RAILS", "7.0.1"); pr == nil {
		t.Error("the dependency name should match case-insensitively")
	}
	if pr := job.ExistingPullRequestForVersion("rails", "7.0.2"); pr != nil {
		t.Error("a different version should not match")
	}
	if pr := job.ExistingPullRequestForVersion("rack", "2.0.1"); pr == nil || pr.Number != 2 {
		t.Errorf("got %v, want pull request 2", pr)
	}
	if pr := job.ExistingPullRequestForVersion("sinatra", "3.0.0"); pr != nil {
		t.Error("a pull request for another directory should not match")
	}
}

func TestExistingPullRequestFor(t *testing.T) {
	job := &Job{
		ExistingPullRequests: []PullRequest{
			{Number: 7, Dependencies: []PullRequestDependency{
				{Name: "rack", Version: "2.0.1", Directory: "/"},
				{Name: "rails", Version: "7.0.1", Directory: "/"},
			}},
		},
	}

	// Order does not matter: the set of dependencies identifies the change.
	match := []*dependabot.Dependency{dependency("rails", "7.0.1"), dependency("rack", "2.0.1")}
	if pr := job.ExistingPullRequestFor(match); pr == nil || pr.Number != 7 {
		t.Errorf("got %v, want pull request 7", pr)
	}

	// A subset is a different change.
	partial := []*dependabot.Dependency{dependency("rails", "7.0.1")}
	if pr := job.ExistingPullRequestFor(partial); pr != nil {
		t.Error("a subset of the dependencies should not match")
	}
}

// A removed dependency has no version, so "removed" stands in for one.
func TestExistingPullRequestForRemoval(t *testing.T) {
	job := &Job{
		ExistingPullRequests: []PullRequest{
			{Number: 9, Dependencies: []PullRequestDependency{
				{Name: "rails", Removed: true, Directory: "/"},
			}},
		},
	}
	removed := dependabot.MustNewDependency(dependabot.Dependency{
		Name: "rails", PackageManager: fakeEcosystemName, Removed: true,
		PreviousVersion: "7.0.0",
	})
	if pr := job.ExistingPullRequestFor([]*dependabot.Dependency{removed}); pr == nil {
		t.Error("a removal should match the pull request that proposes it")
	}
}

func TestSemverPrefix(t *testing.T) {
	cases := []struct {
		version      string
		major, minor int
		ok           bool
	}{
		{"1.2.3", 1, 2, true},
		{"v1.2.3", 1, 2, true},
		{"10", 10, 0, true},
		{"1.2-rc1", 1, 2, true},
		{"not-a-version", 0, 0, false},
		{"", 0, 0, false},
	}
	for _, tc := range cases {
		major, minor, ok := semverPrefix(tc.version)
		if ok != tc.ok || major != tc.major || minor != tc.minor {
			t.Errorf("semverPrefix(%q) = %d, %d, %v; want %d, %d, %v",
				tc.version, major, minor, ok, tc.major, tc.minor, tc.ok)
		}
	}
}

func TestJoinSorted(t *testing.T) {
	got := joinSorted([]string{"c", "a", "b"})
	if got != "a,b,c" {
		t.Errorf("joinSorted = %q, want a,b,c", got)
	}
	if strings.Contains(joinSorted(nil), ",") {
		t.Error("joinSorted(nil) should be empty")
	}
}
