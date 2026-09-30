// Package repofiles reads dependency files from a repository checked out on
// local disk.
//
// Ruby's FileFetchers::Base talks to the GitHub/GitLab/Azure/Bitbucket
// contents APIs and optionally clones. Only the local-checkout half is ported
// here: the Dependabot CLI and the dry-run path both hand the updater a
// directory, and a host-API fetcher can be added behind the same
// dependabot.FileFetcher interface later.
package repofiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// Dir is a directory within a checked-out repository.
type Dir struct {
	// RepoContentsPath is the root of the checkout.
	RepoContentsPath string
	// Directory is the job's directory within the repo, e.g. "/" or "/sub".
	Directory string
}

// NewDir builds a Dir, defaulting the directory to the repository root.
func NewDir(repoContentsPath, directory string) (*Dir, error) {
	if repoContentsPath == "" {
		return nil, fmt.Errorf("a repo contents path is required to fetch files from disk")
	}
	if directory == "" {
		directory = "/"
	}
	return &Dir{RepoContentsPath: repoContentsPath, Directory: directory}, nil
}

// Abs is the absolute path of a file within the job directory.
func (d *Dir) Abs(name string) string {
	return filepath.Join(d.RepoContentsPath, filepath.FromSlash(strings.TrimPrefix(d.Directory, "/")), filepath.FromSlash(name))
}

// Fetch reads one file. A missing file yields a FileNotFoundError, which
// callers distinguish from a read failure.
func (d *Dir) Fetch(name string, opts ...dependabot.FileOption) (*dependabot.DependencyFile, error) {
	content, err := os.ReadFile(d.Abs(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &dependabot.FileNotFoundError{FilePath: filepath.Join(d.Directory, name)}
		}
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	opts = append([]dependabot.FileOption{dependabot.WithDirectory(d.Directory)}, opts...)
	if !utf8.Valid(content) {
		return nil, &dependabot.FileNotParseableError{
			FilePath: filepath.Join(d.Directory, name),
			Detail:   "file is not valid UTF-8",
		}
	}
	return dependabot.NewFile(name, string(content), opts...)
}

// FetchIfPresent reads a file, returning nil when it does not exist.
func (d *Dir) FetchIfPresent(name string, opts ...dependabot.FileOption) (*dependabot.DependencyFile, error) {
	f, err := d.Fetch(name, opts...)
	var notFound *dependabot.FileNotFoundError
	if err != nil {
		if ok := asFileNotFound(err, &notFound); ok {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// Entries lists the names of the regular files directly inside the job
// directory, sorted for deterministic output.
func (d *Dir) Entries() ([]string, error) {
	root := filepath.Join(d.RepoContentsPath, filepath.FromSlash(strings.TrimPrefix(d.Directory, "/")))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &dependabot.FileNotFoundError{FilePath: d.Directory, Detail: "directory not found"}
		}
		return nil, fmt.Errorf("listing %s: %w", d.Directory, err)
	}

	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// WalkNames lists file paths under the job directory, relative to it, skipping
// directories the walk function rejects. Used by ecosystems whose files are
// spread through a tree, such as Kubernetes manifests.
func (d *Dir) WalkNames(skipDir func(name string) bool) ([]string, error) {
	root := filepath.Join(d.RepoContentsPath, filepath.FromSlash(strings.TrimPrefix(d.Directory, "/")))

	var names []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if rel != "." && skipDir != nil && skipDir(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if entry.Type().IsRegular() {
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

func asFileNotFound(err error, target **dependabot.FileNotFoundError) bool {
	if e, ok := err.(*dependabot.FileNotFoundError); ok {
		*target = e
		return true
	}
	return false
}
