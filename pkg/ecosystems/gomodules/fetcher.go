package gomodules

import (
	"context"

	"github.com/omachace/dependabot-go/internal/repofiles"
	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// Fetcher collects the files the Go parser and updater need.
type Fetcher struct {
	dir *repofiles.Dir
}

var _ dependabot.FileFetcher = (*Fetcher)(nil)

// NewFetcher builds a fetcher for a checked-out repository.
func NewFetcher(cfg dependabot.FetcherConfig) (*Fetcher, error) {
	directory := "/"
	if cfg.Source != nil && cfg.Source.Directory != "" {
		directory = cfg.Source.Directory
	}
	dir, err := repofiles.NewDir(cfg.RepoContentsPath, directory)
	if err != nil {
		return nil, err
	}
	return &Fetcher{dir: dir}, nil
}

// FetchFiles returns go.mod and, when present, go.sum.
func (f *Fetcher) FetchFiles(_ context.Context) ([]*dependabot.DependencyFile, error) {
	goMod, err := f.dir.Fetch(GoModFile)
	if err != nil {
		return nil, err
	}
	files := []*dependabot.DependencyFile{goMod}

	goSum, err := f.dir.FetchIfPresent(GoSumFile)
	if err != nil {
		return nil, err
	}
	if goSum != nil {
		files = append(files, goSum)
	}
	return files, nil
}
