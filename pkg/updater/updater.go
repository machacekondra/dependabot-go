package updater

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// DependencyChange is one proposed pull request: the dependencies that moved
// and the files that have to change to move them.
type DependencyChange struct {
	Dependencies []*dependabot.Dependency
	Files        []*dependabot.DependencyFile
	Directory    string
}

// Service receives the outcome of an update run. The real Dependabot updater
// posts to an internal API; a dry run prints instead.
type Service interface {
	CreatePullRequest(ctx context.Context, change *DependencyChange) error
	RecordUpdateError(ctx context.Context, dep *dependabot.Dependency, err error)
}

// Runner performs one update job, the equivalent of Ruby's
// Updater::Operations::UpdateAllVersions.
type Runner struct {
	Job     *Job
	Service Service
	Logger  *slog.Logger

	eco     dependabot.Ecosystem
	files   []*dependabot.DependencyFile
	created []*DependencyChange
}

// NewRunner builds a runner for a job, failing when the job's package manager
// has no registered ecosystem.
func NewRunner(job *Job, service Service, logger *slog.Logger) (*Runner, error) {
	eco, err := dependabot.Lookup(job.PackageManager)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{Job: job, Service: service, Logger: logger, eco: eco}, nil
}

// Run fetches, parses and checks every dependency, proposing a change for each
// one that can be updated.
//
// A failure on one dependency is reported and the run continues, as in Ruby: a
// single unreachable registry should not abandon the rest of the project.
func (r *Runner) Run(ctx context.Context) error {
	deps, err := r.Snapshot(ctx)
	if err != nil {
		return err
	}

	var allowed []*dependabot.Dependency
	for _, dep := range deps {
		if r.Job.AllowedUpdate(dep) {
			allowed = append(allowed, dep)
		}
	}
	if len(deps) > 0 && len(allowed) == 0 {
		r.Logger.Info("found no dependencies to update after filtering allowed updates")
		return nil
	}

	for _, dep := range allowed {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.checkAndCreate(ctx, dep); err != nil {
			r.Logger.Error("update failed", "dependency", dep.Name, "error", err)
			if r.Service != nil {
				r.Service.RecordUpdateError(ctx, dep, err)
			}
		}
	}
	return nil
}

// Snapshot fetches the job's files and parses them into dependencies, caching
// the files for the update phase.
func (r *Runner) Snapshot(ctx context.Context) ([]*dependabot.Dependency, error) {
	fetcher, err := r.eco.NewFileFetcher(dependabot.FetcherConfig{
		Source:           r.Job.Source,
		Credentials:      r.Job.Credentials,
		RepoContentsPath: r.Job.RepoContentsPath,
		Options:          r.Job.Options,
	})
	if err != nil {
		return nil, err
	}
	files, err := fetcher.FetchFiles(ctx)
	if err != nil {
		return nil, err
	}
	r.files = files

	parser, err := r.eco.NewFileParser(dependabot.ParserConfig{
		Files:            files,
		Source:           r.Job.Source,
		RepoContentsPath: r.Job.RepoContentsPath,
		Credentials:      r.Job.Credentials,
		Options:          r.Job.Options,
	})
	if err != nil {
		return nil, err
	}
	return parser.Parse(ctx)
}

// Files returns the files the last Snapshot fetched.
func (r *Runner) Files() []*dependabot.DependencyFile { return r.files }

// Changes returns the changes proposed so far.
func (r *Runner) Changes() []*DependencyChange { return r.created }

func (r *Runner) checkAndCreate(ctx context.Context, dep *dependabot.Dependency) error {
	checker, err := r.checkerFor(dep)
	if err != nil {
		return err
	}
	r.Logger.Info("checking for update", "dependency", dep.Name, "version", dep.Version)

	upToDate, err := checker.UpToDate(ctx)
	if err != nil {
		return err
	}
	if upToDate {
		r.Logger.Info("no update needed", "dependency", dep.Name, "version", dep.Version)
		return nil
	}

	latest, err := checker.LatestVersion(ctx)
	if err != nil {
		return err
	}
	if latest != nil && r.pullRequestExistsForVersion(dep.Name, latest.String()) {
		r.Logger.Info("pull request already exists",
			"dependency", dep.Name, "version", latest.String())
		return nil
	}

	unlock, err := r.requirementsToUnlock(ctx, checker)
	if err != nil {
		return err
	}
	if unlock == "" {
		r.Logger.Info("no update possible", "dependency", dep.Name, "version", dep.Version)
		return nil
	}
	r.Logger.Info("update possible", "dependency", dep.Name, "unlock", string(unlock))

	updatedDeps, err := checker.UpdatedDependencies(ctx, unlock)
	if err != nil {
		return err
	}
	if len(updatedDeps) == 0 {
		return fmt.Errorf(
			"%s: requirements were unlockable but no dependency was updated", dep.Name)
	}

	if existing := r.existingPullRequest(updatedDeps); existing != nil {
		r.Logger.Info("pull request already exists", "dependency", dep.Name)
		return nil
	}

	change, err := r.buildChange(ctx, dep, updatedDeps)
	if err != nil {
		return err
	}

	r.created = append(r.created, change)
	if r.Service == nil {
		return nil
	}
	return r.Service.CreatePullRequest(ctx, change)
}

// requirementsToUnlock finds the least invasive unlock level that yields an
// update, returning "" when none does.
func (r *Runner) requirementsToUnlock(ctx context.Context, checker dependabot.UpdateChecker) (dependabot.UnlockLevel, error) {
	if !checker.RequirementsUnlockedOrCanBe() {
		can, err := checker.CanUpdate(ctx, dependabot.UnlockNone)
		if err != nil || !can {
			return "", err
		}
		return dependabot.UnlockNone, nil
	}

	for _, unlock := range []dependabot.UnlockLevel{dependabot.UnlockOwn, dependabot.UnlockAll} {
		can, err := checker.CanUpdate(ctx, unlock)
		if err != nil {
			return "", err
		}
		if can {
			return unlock, nil
		}
	}
	return "", nil
}

// buildChange runs the file updater and drops dependencies that did not
// actually move, mirroring DependencyChangeBuilder.
func (r *Runner) buildChange(ctx context.Context, source *dependabot.Dependency, updatedDeps []*dependabot.Dependency) (*DependencyChange, error) {
	updater, err := r.eco.NewFileUpdater(dependabot.UpdaterConfig{
		Dependencies:     updatedDeps,
		Files:            r.files,
		Credentials:      r.Job.Credentials,
		RepoContentsPath: r.Job.RepoContentsPath,
		Options:          r.Job.Options,
	})
	if err != nil {
		return nil, err
	}
	updatedFiles, err := updater.UpdatedDependencyFiles(ctx)
	if err != nil {
		return nil, err
	}
	if len(updatedFiles) == 0 {
		return nil, fmt.Errorf(
			"%s: an update was available but no file could be updated", source.Name)
	}

	// Support files are fetched for context and are not the change itself,
	// unless they are all the updater touched.
	primary := updatedFiles[:0:0]
	for _, file := range updatedFiles {
		if !file.SupportFile {
			primary = append(primary, file)
		}
	}
	if len(primary) == 0 {
		primary = updatedFiles
	}

	changed := make([]*dependabot.Dependency, 0, len(updatedDeps))
	for _, dep := range updatedDeps {
		if dep.Name != source.Name && dep.Version == dep.PreviousVersion && !dep.RequirementsChanged() {
			continue
		}
		dep.Directory = r.Job.Directory()
		changed = append(changed, dep)
	}

	return &DependencyChange{
		Dependencies: changed,
		Files:        primary,
		Directory:    r.Job.Directory(),
	}, nil
}

func (r *Runner) checkerFor(dep *dependabot.Dependency) (dependabot.UpdateChecker, error) {
	return r.eco.NewUpdateChecker(dependabot.CheckerConfig{
		Dependency:         dep,
		Files:              r.files,
		Credentials:        r.Job.Credentials,
		RepoContentsPath:   r.Job.RepoContentsPath,
		IgnoredVersions:    r.Job.IgnoreConditionsFor(dep),
		SecurityAdvisories: r.Job.SecurityAdvisoriesFor(dep),
		Strategy:           r.Job.RequirementsUpdateStrategy,
		Options:            r.Job.Options,
	})
}

func (r *Runner) pullRequestExistsForVersion(name, version string) bool {
	if r.Job.ExistingPullRequestForVersion(name, version) != nil {
		return true
	}
	for _, change := range r.created {
		for _, dep := range change.Dependencies {
			if dep.Name == name && dep.Version == version {
				return true
			}
		}
	}
	return false
}

func (r *Runner) existingPullRequest(deps []*dependabot.Dependency) *PullRequest {
	if pr := r.Job.ExistingPullRequestFor(deps); pr != nil {
		return pr
	}
	want := pullRequestKey(deps, r.Job.Directory())
	for _, change := range r.created {
		if pullRequestKey(change.Dependencies, change.Directory) == want {
			return &PullRequest{}
		}
	}
	return nil
}
