package docker

import (
	"context"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/omachace/dependabot-go/internal/repofiles"
	"github.com/omachace/dependabot-go/pkg/dependabot"
)

var (
	// dockerfileNameRe matches the files a FROM instruction can live in.
	dockerfileNameRe = regexp.MustCompile(`(?i)dockerfile|containerfile`)
	// helmValuesRe matches a Helm chart's values file.
	helmValuesRe = regexp.MustCompile(`(?i)values[\-a-zA-Z_0-9.]*\.ya?ml$`)
)

// Fetcher collects Dockerfiles and container-bearing YAML from a directory.
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

// FetchFiles returns every Dockerfile in the directory, plus the YAML files
// that look like Kubernetes resources or Helm values.
func (f *Fetcher) FetchFiles(_ context.Context) ([]*dependabot.DependencyFile, error) {
	names, err := f.dir.Entries()
	if err != nil {
		return nil, err
	}

	var files []*dependabot.DependencyFile
	for _, name := range names {
		switch {
		case dockerfileNameRe.MatchString(name):
		case yamlNameRe.MatchString(name):
		default:
			continue
		}

		file, err := f.dir.Fetch(name)
		if err != nil {
			// A file we cannot decode is skipped rather than failing the job;
			// a repository often holds YAML that has nothing to do with us.
			continue
		}
		if yamlNameRe.MatchString(name) && !containerBearingYAML(file) {
			continue
		}
		files = append(files, file)
	}

	if len(files) == 0 {
		return nil, &dependabot.FileNotFoundError{
			FilePath: "Dockerfile",
			Detail:   "no Dockerfile, Containerfile or Kubernetes YAML found",
		}
	}
	return files, nil
}

// containerBearingYAML reports whether a YAML file is one we should parse: a
// Helm values file, or a document that looks like a Kubernetes resource.
func containerBearingYAML(file *dependabot.DependencyFile) bool {
	if helmValuesRe.MatchString(file.Name) {
		return true
	}

	// A multi-document file had better lead with a valid resource, so only the
	// first document is inspected.
	content := yamlDocSplitRe.Split(stripBOM(file.ContentString()), -1)
	for _, doc := range content {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var parsed map[string]any
		if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
			return false
		}
		_, hasAPIVersion := parsed["apiVersion"]
		_, hasKind := parsed["kind"]
		return hasAPIVersion && hasKind
	}
	return false
}

// stripBOM removes a leading byte-order mark, which the YAML parser does not
// handle and which otherwise fails the parse in a confusing way.
func stripBOM(content string) string {
	return strings.TrimPrefix(content, "\ufeff")
}
