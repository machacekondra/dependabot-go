package updater

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// recordingService captures what a run would have sent to the Dependabot
// service.
type recordingService struct {
	changes []*DependencyChange
	errs    map[string]error
	failPR  error
}

func (s *recordingService) CreatePullRequest(_ context.Context, change *DependencyChange) error {
	s.changes = append(s.changes, change)
	return s.failPR
}

func (s *recordingService) RecordUpdateError(_ context.Context, dep *dependabot.Dependency, err error) {
	if s.errs == nil {
		s.errs = map[string]error{}
	}
	s.errs[dep.Name] = err
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// manifest scripts a project with one file and the dependencies it declares.
func manifest(content string, deps ...*dependabot.Dependency) {
	s := resetState()
	s.files = []*dependabot.DependencyFile{dependabot.MustNewFile("manifest", content)}
	s.deps = deps
}

func runJob(t *testing.T, job *Job) (*Runner, *recordingService) {
	t.Helper()
	job.PackageManager = fakeEcosystemName
	service := &recordingService{}
	runner, err := NewRunner(job, service, quietLogger())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return runner, service
}

func TestNewRunnerUnknownPackageManager(t *testing.T) {
	if _, err := NewRunner(&Job{PackageManager: "nope"}, nil, quietLogger()); err == nil {
		t.Error("expected an error for an unregistered package manager")
	}
}

func TestRunProposesAnUpdate(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.0.0", "7.0.1", "7.1.0"}

	_, service := runJob(t, &Job{})

	if len(service.changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(service.changes))
	}
	change := service.changes[0]
	if len(change.Dependencies) != 1 || change.Dependencies[0].Version != "7.1.0" {
		t.Fatalf("change = %+v, want rails at 7.1.0", change.Dependencies)
	}
	if change.Dependencies[0].PreviousVersion != "7.0.0" {
		t.Errorf("previous version = %q, want 7.0.0", change.Dependencies[0].PreviousVersion)
	}
	if len(change.Files) != 1 || change.Files[0].ContentString() != "rails 7.1.0\n" {
		t.Errorf("files = %+v, want the manifest rewritten", change.Files)
	}
	if change.Directory != "/" {
		t.Errorf("directory = %q, want /", change.Directory)
	}
}

func TestRunSkipsUpToDateDependencies(t *testing.T) {
	manifest("rails 7.1.0\n", dependency("rails", "7.1.0"))
	state.available["rails"] = []string{"7.0.0", "7.1.0"}

	_, service := runJob(t, &Job{})

	if len(service.changes) != 0 {
		t.Errorf("got %d changes, want none", len(service.changes))
	}
}

func TestRunHonoursAllowedUpdates(t *testing.T) {
	manifest("rails 7.0.0\nrack 2.0.0\n",
		dependency("rails", "7.0.0"), dependency("rack", "2.0.0"))
	state.available["rails"] = []string{"7.1.0"}
	state.available["rack"] = []string{"2.1.0"}

	_, service := runJob(t, &Job{
		AllowedUpdates: []AllowedUpdate{{DependencyName: "rails"}},
	})

	if len(service.changes) != 1 {
		t.Fatalf("got %d changes, want only the allowed one", len(service.changes))
	}
	if service.changes[0].Dependencies[0].Name != "rails" {
		t.Errorf("updated %q, want rails", service.changes[0].Dependencies[0].Name)
	}
}

func TestRunHonoursIgnoreConditions(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.0.1", "8.0.0"}

	_, service := runJob(t, &Job{
		IgnoreConditions: []IgnoreCondition{
			{DependencyName: "rails", UpdateTypes: []string{IgnoreMajor}},
		},
	})

	if len(service.changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(service.changes))
	}
	// Ignoring major bumps should still allow the patch release through.
	if got := service.changes[0].Dependencies[0].Version; got != "7.0.1" {
		t.Errorf("updated to %q, want 7.0.1", got)
	}
}

// A dependency ignored outright leaves the run with nothing to do.
func TestRunWithEverythingIgnored(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.1.0"}

	_, service := runJob(t, &Job{
		IgnoreConditions: []IgnoreCondition{{DependencyName: "rails"}},
	})

	if len(service.changes) != 0 {
		t.Errorf("got %d changes, want none", len(service.changes))
	}
}

func TestRunSkipsExistingPullRequest(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.1.0"}

	_, service := runJob(t, &Job{
		ExistingPullRequests: []PullRequest{{
			Number:       4,
			Dependencies: []PullRequestDependency{{Name: "rails", Version: "7.1.0", Directory: "/"}},
		}},
	})

	if len(service.changes) != 0 {
		t.Errorf("got %d changes, want none: a pull request already proposes this", len(service.changes))
	}
}

// A security job takes the lowest version that resolves the advisory, not the
// latest release.
func TestRunSecurityUpdateTakesLowestFix(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.0.1", "7.1.0", "8.0.0"}

	advisory, err := dependabot.NewSecurityAdvisory(
		"rails", fakeEcosystemName, []string{"< 7.0.1"}, nil)
	if err != nil {
		t.Fatalf("NewSecurityAdvisory: %v", err)
	}

	_, service := runJob(t, &Job{
		SecurityUpdatesOnly: true,
		SecurityAdvisories:  []*dependabot.SecurityAdvisory{advisory},
		AllowedUpdates:      []AllowedUpdate{{DependencyType: DependencyTypeAll}},
	})

	if len(service.changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(service.changes))
	}
	if got := service.changes[0].Dependencies[0].Version; got != "7.0.1" {
		t.Errorf("updated to %q, want the lowest fix 7.0.1", got)
	}
}

// One unreachable registry should not abandon the rest of the project.
func TestRunContinuesAfterAFailure(t *testing.T) {
	manifest("rails 7.0.0\nrack 2.0.0\n",
		dependency("rails", "7.0.0"), dependency("rack", "2.0.0"))
	state.available["rails"] = []string{"7.1.0"}
	state.available["rack"] = []string{"2.1.0"}
	// The file updater fails for every dependency, so both are reported.
	state.updateErr = errors.New("registry is down")

	_, service := runJob(t, &Job{AllowedUpdates: []AllowedUpdate{{DependencyType: DependencyTypeAll}}})

	if len(service.errs) != 2 {
		t.Errorf("recorded %d errors, want one per dependency", len(service.errs))
	}
	if len(service.changes) != 0 {
		t.Errorf("got %d changes, want none", len(service.changes))
	}
}

// A service that rejects a pull request is a per-dependency failure, not a
// reason to abandon the run.
func TestRunReportsServiceFailure(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.1.0"}

	service := &recordingService{failPR: errors.New("the service is unavailable")}
	runner, err := NewRunner(&Job{PackageManager: fakeEcosystemName}, service, quietLogger())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if service.errs["rails"] == nil {
		t.Error("the rejected pull request should have been recorded as an error")
	}
}

func TestRunReportsFetchFailure(t *testing.T) {
	manifest("rails 7.0.0\n")
	state.fetchErr = errors.New("no such directory")

	runner, err := NewRunner(&Job{PackageManager: fakeEcosystemName}, &recordingService{}, quietLogger())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(context.Background()); err == nil {
		t.Error("expected the run to fail when files cannot be fetched")
	}
}

func TestRunReportsParseFailure(t *testing.T) {
	manifest("rails 7.0.0\n")
	state.parseErr = errors.New("manifest is malformed")

	runner, err := NewRunner(&Job{PackageManager: fakeEcosystemName}, &recordingService{}, quietLogger())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(context.Background()); err == nil {
		t.Error("expected the run to fail when the manifest cannot be parsed")
	}
}

// The run stops when its context is cancelled rather than working through the
// remaining dependencies.
func TestRunRespectsContextCancellation(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.1.0"}

	service := &recordingService{}
	runner, err := NewRunner(&Job{PackageManager: fakeEcosystemName}, service, quietLogger())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runner.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Run error = %v, want context.Canceled", err)
	}
	if len(service.changes) != 0 {
		t.Error("a cancelled run should propose nothing")
	}
}

// The same update must not be proposed twice within a single run.
func TestRunDeduplicatesWithinARun(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"), dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.1.0"}

	_, service := runJob(t, &Job{})

	if len(service.changes) != 1 {
		t.Errorf("got %d changes, want the duplicate suppressed", len(service.changes))
	}
}

func TestSnapshotCachesFiles(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))

	runner, err := NewRunner(&Job{PackageManager: fakeEcosystemName}, nil, quietLogger())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	deps, err := runner.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(deps) != 1 || deps[0].Name != "rails" {
		t.Errorf("Snapshot = %+v, want rails", deps)
	}
	if len(runner.Files()) != 1 {
		t.Errorf("Files() = %+v, want the fetched manifest", runner.Files())
	}
}

// A runner with no service still records what it would have proposed, which is
// how the dry-run CLI works.
func TestRunWithoutAService(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.1.0"}

	runner, err := NewRunner(&Job{PackageManager: fakeEcosystemName}, nil, quietLogger())
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.Changes()) != 1 {
		t.Errorf("Changes() = %+v, want one change", runner.Changes())
	}
}

func TestRunSetsDirectoryOnChanges(t *testing.T) {
	manifest("rails 7.0.0\n", dependency("rails", "7.0.0"))
	state.available["rails"] = []string{"7.1.0"}

	_, service := runJob(t, &Job{Source: &dependabot.RepoSource{Directory: "/app"}})

	if len(service.changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(service.changes))
	}
	if service.changes[0].Directory != "/app" {
		t.Errorf("directory = %q, want /app", service.changes[0].Directory)
	}
	if service.changes[0].Dependencies[0].Directory != "/app" {
		t.Errorf("dependency directory = %q, want /app",
			service.changes[0].Dependencies[0].Directory)
	}
}
