// Command dependabot runs a Dependabot update against a local checkout and
// prints what it would change.
//
// It is the equivalent of dependabot-core's `bin/dependabot update` dry run,
// minus the parts that talk to a git host: nothing is pushed and no pull
// request is opened.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/omachace/dependabot-go/pkg/dependabot"
	"github.com/omachace/dependabot-go/pkg/updater"

	// Ecosystems register themselves on import, the way requiring a Ruby gem
	// populates Dependabot's lookup tables. This import list is the Go
	// equivalent of the omnibus gem.
	_ "github.com/omachace/dependabot-go/pkg/ecosystems/docker"
	_ "github.com/omachace/dependabot-go/pkg/ecosystems/gomodules"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("a command is required")
	}

	switch args[0] {
	case "update":
		return runUpdate(args[1:])
	case "ecosystems":
		for _, name := range dependabot.Registered() {
			fmt.Println(name)
		}
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: dependabot <command> [flags]

commands:
  update       check a local checkout for dependency updates
  ecosystems   list the package managers this binary supports
  help         show this message

run "dependabot update -h" for the update flags
`)
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	packageManager := fs.String("package-manager", "", "ecosystem to run, e.g. go_modules or docker")
	repoPath := fs.String("dir", ".", "path to the checked-out repository")
	directory := fs.String("directory", "/", "directory within the repository to update")
	repo := fs.String("repo", "local/local", "repository name, used only in output")
	applyChanges := fs.Bool("apply", false, "write the updated files back to disk instead of printing them")
	verbose := fs.Bool("v", false, "log at debug level")

	var only stringList
	var ignores stringList
	fs.Var(&only, "dependency", "limit the run to this dependency (repeatable)")
	fs.Var(&ignores, "ignore", "ignore versions matching a requirement, as name=requirement (repeatable)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if *packageManager == "" {
		return fmt.Errorf("-package-manager is required (one of: %s)",
			strings.Join(dependabot.Registered(), ", "))
	}

	absPath, err := filepath.Abs(*repoPath)
	if err != nil {
		return err
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	source, err := dependabot.NewRepoSource(dependabot.ProviderGitHub, *repo)
	if err != nil {
		return err
	}
	source.Directory = *directory

	conditions, err := parseIgnores(ignores)
	if err != nil {
		return err
	}

	job := &updater.Job{
		PackageManager:   *packageManager,
		Source:           source,
		RepoContentsPath: absPath,
		Credentials:      credentialsFromEnv(),
		Dependencies:     only,
		IgnoreConditions: conditions,
	}

	eco, err := dependabot.Lookup(*packageManager)
	if err != nil {
		return err
	}

	service := &printService{
		eco:   eco,
		job:   job,
		apply: *applyChanges,
		root:  absPath,
	}
	runner, err := updater.NewRunner(job, service, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runner.Run(ctx); err != nil {
		return err
	}
	if service.failures > 0 {
		return fmt.Errorf("%d dependencies could not be checked", service.failures)
	}
	if len(runner.Changes()) == 0 {
		fmt.Println("No updates available.")
	}
	return nil
}

// parseIgnores reads "name=requirement" pairs. A bare name ignores the
// dependency entirely.
func parseIgnores(values []string) ([]updater.IgnoreCondition, error) {
	var out []updater.IgnoreCondition
	for _, value := range values {
		name, requirement, found := strings.Cut(value, "=")
		if name == "" {
			return nil, fmt.Errorf("invalid -ignore %q: expected name=requirement", value)
		}
		if !found {
			out = append(out, updater.IgnoreCondition{DependencyName: name})
			continue
		}
		out = append(out, updater.IgnoreCondition{
			DependencyName:     name,
			VersionRequirement: requirement,
		})
	}
	return out, nil
}

// credentialsFromEnv builds registry credentials from the environment, so a
// private registry can be used without putting secrets on the command line.
func credentialsFromEnv() []*dependabot.Credential {
	var out []*dependabot.Credential

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		out = append(out, dependabot.NewCredential(map[string]string{
			"type":     "git_source",
			"host":     "github.com",
			"username": "x-access-token",
			"password": token,
		}))
	}

	host := os.Getenv("DOCKER_REGISTRY")
	if host == "" {
		host = "registry.hub.docker.com"
	}
	if user, pass := os.Getenv("DOCKER_USERNAME"), os.Getenv("DOCKER_PASSWORD"); user != "" || pass != "" {
		out = append(out, dependabot.NewCredential(map[string]string{
			"type":     "docker_registry",
			"registry": host,
			"host":     host,
			"username": user,
			"password": pass,
		}))
	}
	return out
}

// printService reports each proposed change on stdout, and optionally writes
// the updated files back to the working tree.
type printService struct {
	eco      dependabot.Ecosystem
	job      *updater.Job
	apply    bool
	root     string
	failures int
}

func (s *printService) CreatePullRequest(ctx context.Context, change *updater.DependencyChange) error {
	message := updater.BuildMessage(ctx, s.eco, s.job, change)

	fmt.Printf("\n=== %s ===\n", message.PRName)
	if message.PRMessage != "" {
		fmt.Println(message.PRMessage)
	}
	for _, file := range change.Files {
		fmt.Printf("--- %s ---\n", file.Path())
		if !s.apply {
			fmt.Println(file.ContentString())
			continue
		}
		if err := s.write(file); err != nil {
			return err
		}
		fmt.Println("written")
	}
	return nil
}

func (s *printService) write(file *dependabot.DependencyFile) error {
	content, err := file.DecodedContent()
	if err != nil {
		return err
	}
	// Path() is repository-relative and always absolute-looking; join it onto
	// the checkout rather than treating it as a filesystem path.
	target := filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(file.Path(), "/")))
	return os.WriteFile(target, []byte(content), 0o644)
}

func (s *printService) RecordUpdateError(_ context.Context, dep *dependabot.Dependency, err error) {
	s.failures++
	fmt.Fprintf(os.Stderr, "could not check %s: %v\n", dep.Name, err)
}
