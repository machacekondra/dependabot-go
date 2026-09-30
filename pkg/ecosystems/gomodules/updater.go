package gomodules

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// Updater writes updated versions back into go.mod, and refreshes go.sum when
// a Go toolchain is available.
//
// Ruby drives the whole update through `go get`/`go mod tidy` in a scratch
// copy of the repo. Rewriting go.mod with modfile is deterministic, needs no
// toolchain, and preserves comments and formatting. go.sum still needs the
// toolchain, because its entries are cryptographic hashes of module content
// that can only be produced by downloading and verifying the modules.
type Updater struct {
	cfg dependabot.UpdaterConfig
}

var _ dependabot.FileUpdater = (*Updater)(nil)

// SkipGoSumOption, when set to true in UpdaterConfig.Options, leaves go.sum
// untouched instead of erroring when it cannot be refreshed.
const SkipGoSumOption = "skip_go_sum"

// NewUpdater builds a go.mod updater.
func NewUpdater(cfg dependabot.UpdaterConfig) (*Updater, error) {
	if dependabot.FindFile(cfg.Files, GoModFile) == nil {
		return nil, &dependabot.FileNotFoundError{FilePath: GoModFile, Detail: "no go.mod"}
	}
	if len(cfg.Dependencies) == 0 {
		return nil, fmt.Errorf("no dependencies to update")
	}
	return &Updater{cfg: cfg}, nil
}

// UpdatedDependencyFiles returns the files whose contents changed.
func (u *Updater) UpdatedDependencyFiles(ctx context.Context) ([]*dependabot.DependencyFile, error) {
	goMod := dependabot.FindFile(u.cfg.Files, GoModFile)

	updatedGoMod, err := u.updatedGoModContent(goMod)
	if err != nil {
		return nil, err
	}
	if updatedGoMod == goMod.ContentString() {
		return nil, fmt.Errorf("expected go.mod to change, but it did not")
	}
	updated := []*dependabot.DependencyFile{goMod.WithContent(updatedGoMod)}

	goSum := dependabot.FindFile(u.cfg.Files, GoSumFile)
	if goSum == nil {
		return updated, nil
	}

	updatedGoSum, err := u.updatedGoSumContent(ctx, updatedGoMod, goSum)
	if err != nil {
		if skip, _ := u.cfg.Options[SkipGoSumOption].(bool); skip {
			return updated, nil
		}
		return nil, err
	}
	if updatedGoSum != goSum.ContentString() {
		updated = append(updated, goSum.WithContent(updatedGoSum))
	}
	return updated, nil
}

func (u *Updater) updatedGoModContent(goMod *dependabot.DependencyFile) (string, error) {
	parsed, err := modfile.Parse(goMod.Name, []byte(goMod.ContentString()), nil)
	if err != nil {
		return "", &dependabot.FileNotParseableError{FilePath: goMod.Path(), Detail: err.Error()}
	}

	for _, dep := range u.cfg.Dependencies {
		version, err := targetVersion(dep)
		if err != nil {
			return "", err
		}
		if err := parsed.AddRequire(dep.Name, version); err != nil {
			return "", fmt.Errorf("setting %s to %s in go.mod: %w", dep.Name, version, err)
		}
	}

	parsed.Cleanup()
	out, err := parsed.Format()
	if err != nil {
		return "", fmt.Errorf("formatting go.mod: %w", err)
	}
	return string(out), nil
}

// targetVersion is the version to write for a dependency: the requirement the
// checker produced for go.mod, falling back to the dependency's version.
func targetVersion(dep *dependabot.Dependency) (string, error) {
	for _, req := range dep.Requirements {
		if req.Unfixable {
			return "", fmt.Errorf("no valid updated requirement for %s", dep.Name)
		}
		if req.File == GoModFile && req.Requirement != "" {
			return canonicalise(req.Requirement), nil
		}
	}
	if dep.Version == "" {
		return "", fmt.Errorf("dependency %s has no version to write", dep.Name)
	}
	return canonicalise(dep.Version), nil
}

// updatedGoSumContent refreshes go.sum by running the Go toolchain over a copy
// of the repository with the new go.mod in place.
func (u *Updater) updatedGoSumContent(ctx context.Context, goModContent string, goSum *dependabot.DependencyFile) (string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("go.sum is present but no Go toolchain is available to refresh it: %w", err)
	}
	if u.cfg.RepoContentsPath == "" {
		return "", fmt.Errorf("go.sum is present but no repo contents path was given to refresh it in")
	}

	workDir, err := os.MkdirTemp("", "dependabot-gomod-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workDir)

	if err := copyTree(u.cfg.RepoContentsPath, workDir); err != nil {
		return "", fmt.Errorf("copying repo to scratch directory: %w", err)
	}

	subdir := strings.TrimPrefix(goSum.Directory, "/")
	moduleDir := filepath.Join(workDir, filepath.FromSlash(subdir))
	if err := os.WriteFile(filepath.Join(moduleDir, GoModFile), []byte(goModContent), 0o644); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, goBin, "mod", "tidy")
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", &dependabot.FileNotResolvableError{
			Detail: fmt.Sprintf("go mod tidy failed: %s", strings.TrimSpace(string(out))),
		}
	}

	content, err := os.ReadFile(filepath.Join(moduleDir, GoSumFile))
	if err != nil {
		return "", fmt.Errorf("reading refreshed go.sum: %w", err)
	}
	return string(content), nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			if info.Name() == ".git" && rel != "." {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
