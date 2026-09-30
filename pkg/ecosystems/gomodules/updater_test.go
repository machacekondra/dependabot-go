package gomodules

import (
	"context"
	"strings"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

func updatedGoMod(t *testing.T, content string, deps ...*dependabot.Dependency) string {
	t.Helper()
	updater, err := NewUpdater(dependabot.UpdaterConfig{
		Dependencies: deps,
		Files:        []*dependabot.DependencyFile{dependabot.MustNewFile(GoModFile, content)},
	})
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	updated, err := updater.UpdatedDependencyFiles(context.Background())
	if err != nil {
		t.Fatalf("UpdatedDependencyFiles: %v", err)
	}
	if len(updated) != 1 || updated[0].Name != GoModFile {
		t.Fatalf("got %d updated files, want just go.mod", len(updated))
	}
	return updated[0].ContentString()
}

func requirementDependency(name, version, previous string) *dependabot.Dependency {
	return dependabot.MustNewDependency(dependabot.Dependency{
		Name:            name,
		Version:         version,
		PreviousVersion: previous,
		PackageManager:  EcosystemName,
		Requirements: []dependabot.Requirement{{
			Requirement: version, File: GoModFile,
			Source: &dependabot.Source{Type: "default", Extra: map[string]string{"source": name}},
		}},
		PreviousRequirements: []dependabot.Requirement{{
			Requirement: previous, File: GoModFile,
			Source: &dependabot.Source{Type: "default", Extra: map[string]string{"source": name}},
		}},
	})
}

const baseGoMod = `module example.com/app

go 1.24

require (
	github.com/spf13/cobra v1.8.1 // pinned for the CLI
	golang.org/x/mod v0.40.0
)
`

func TestUpdateGoModRewritesRequirement(t *testing.T) {
	got := updatedGoMod(t, baseGoMod,
		requirementDependency("golang.org/x/mod", "v0.41.0", "v0.40.0"))

	if !strings.Contains(got, "golang.org/x/mod v0.41.0") {
		t.Errorf("version was not updated:\n%s", got)
	}
	// Only the named module moves, and its neighbour keeps its comment.
	if !strings.Contains(got, "github.com/spf13/cobra v1.8.1 // pinned for the CLI") {
		t.Errorf("an unrelated requirement was disturbed:\n%s", got)
	}
}

func TestUpdateGoModMultipleDependencies(t *testing.T) {
	got := updatedGoMod(t, baseGoMod,
		requirementDependency("golang.org/x/mod", "v0.41.0", "v0.40.0"),
		requirementDependency("github.com/spf13/cobra", "v1.9.0", "v1.8.1"))

	for _, want := range []string{"golang.org/x/mod v0.41.0", "github.com/spf13/cobra v1.9.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// A subdependency has no go.mod requirement of its own, so the version it
// carries is what gets written.
func TestUpdateGoModFallsBackToDependencyVersion(t *testing.T) {
	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:            "golang.org/x/mod",
		Version:         "v0.41.0",
		PreviousVersion: "v0.40.0",
		PackageManager:  EcosystemName,
	})
	if got := updatedGoMod(t, baseGoMod, dep); !strings.Contains(got, "golang.org/x/mod v0.41.0") {
		t.Errorf("version was not updated:\n%s", got)
	}
}

// The requirement may be written without its "v"; go.mod always needs one.
func TestUpdateGoModAddsMissingVPrefix(t *testing.T) {
	got := updatedGoMod(t, baseGoMod,
		requirementDependency("golang.org/x/mod", "0.41.0", "0.40.0"))
	if !strings.Contains(got, "golang.org/x/mod v0.41.0") {
		t.Errorf("the v prefix was not added:\n%s", got)
	}
}

func TestUpdateGoModErrorsWhenNothingChanges(t *testing.T) {
	updater, err := NewUpdater(dependabot.UpdaterConfig{
		Dependencies: []*dependabot.Dependency{
			requirementDependency("golang.org/x/mod", "v0.40.0", "v0.40.0"),
		},
		Files: []*dependabot.DependencyFile{dependabot.MustNewFile(GoModFile, baseGoMod)},
	})
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	if _, err := updater.UpdatedDependencyFiles(context.Background()); err == nil {
		t.Error("expected an error when go.mod did not change")
	}
}

// An unfixable requirement means the checker could not produce a version to
// write, which must fail loudly rather than write the old one back.
func TestUpdateGoModRejectsUnfixableRequirement(t *testing.T) {
	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:           "golang.org/x/mod",
		Version:        "v0.41.0",
		PackageManager: EcosystemName,
		Requirements: []dependabot.Requirement{{
			File: GoModFile, Unfixable: true,
		}},
	})
	updater, err := NewUpdater(dependabot.UpdaterConfig{
		Dependencies: []*dependabot.Dependency{dep},
		Files:        []*dependabot.DependencyFile{dependabot.MustNewFile(GoModFile, baseGoMod)},
	})
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	if _, err := updater.UpdatedDependencyFiles(context.Background()); err == nil {
		t.Error("expected an error for an unfixable requirement")
	}
}

func TestNewUpdaterValidatesInput(t *testing.T) {
	goMod := dependabot.MustNewFile(GoModFile, baseGoMod)
	dep := requirementDependency("golang.org/x/mod", "v0.41.0", "v0.40.0")

	if _, err := NewUpdater(dependabot.UpdaterConfig{
		Dependencies: []*dependabot.Dependency{dep},
		Files:        []*dependabot.DependencyFile{dependabot.MustNewFile("README.md", "hi")},
	}); err == nil {
		t.Error("expected an error without a go.mod")
	}

	if _, err := NewUpdater(dependabot.UpdaterConfig{
		Files: []*dependabot.DependencyFile{goMod},
	}); err == nil {
		t.Error("expected an error with no dependencies")
	}
}

// go.sum can only be regenerated by the toolchain against a real checkout.
// Without one, the option decides between failing and leaving it alone.
func TestUpdateGoSumSkipOption(t *testing.T) {
	files := []*dependabot.DependencyFile{
		dependabot.MustNewFile(GoModFile, baseGoMod),
		dependabot.MustNewFile(GoSumFile, "golang.org/x/mod v0.40.0 h1:abc=\n"),
	}
	dep := requirementDependency("golang.org/x/mod", "v0.41.0", "v0.40.0")

	updater, err := NewUpdater(dependabot.UpdaterConfig{
		Dependencies: []*dependabot.Dependency{dep},
		Files:        files,
		Options:      map[string]any{SkipGoSumOption: true},
	})
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	updated, err := updater.UpdatedDependencyFiles(context.Background())
	if err != nil {
		t.Fatalf("UpdatedDependencyFiles: %v", err)
	}
	if len(updated) != 1 || updated[0].Name != GoModFile {
		t.Errorf("got %d files, want go.mod alone when go.sum is skipped", len(updated))
	}

	// Without the option, the missing repo contents path is an error rather
	// than a silently stale go.sum.
	strict, err := NewUpdater(dependabot.UpdaterConfig{
		Dependencies: []*dependabot.Dependency{dep},
		Files:        files,
	})
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	if _, err := strict.UpdatedDependencyFiles(context.Background()); err == nil {
		t.Error("expected an error refreshing go.sum with no repo contents path")
	}
}
