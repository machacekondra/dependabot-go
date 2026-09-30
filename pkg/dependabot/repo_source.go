package dependabot

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Git hosting providers Dependabot can open pull requests against.
const (
	ProviderGitHub     = "github"
	ProviderGitLab     = "gitlab"
	ProviderBitbucket  = "bitbucket"
	ProviderAzure      = "azure"
	ProviderCodeCommit = "codecommit"
)

// RepoSource identifies the repository, directory and branch an update job runs
// against. Ruby calls this Dependabot::Source; the name Source is taken here by
// a requirement's package source.
type RepoSource struct {
	Provider string
	Repo     string
	// Directory is the single directory to update. Directories is the
	// multi-directory form; a job uses one or the other.
	Directory   string
	Directories []string
	Branch      string
	Commit      string
	Hostname    string
	APIEndpoint string
}

// NewRepoSource builds a RepoSource, filling in the provider's default hostname
// and API endpoint.
func NewRepoSource(provider, repo string) (*RepoSource, error) {
	hostname, err := defaultHostname(provider)
	if err != nil {
		return nil, err
	}
	return &RepoSource{
		Provider:    provider,
		Repo:        repo,
		Hostname:    hostname,
		APIEndpoint: defaultAPIEndpoint(provider),
	}, nil
}

// URL is the repository's web URL.
func (s *RepoSource) URL() string {
	return "https://" + s.Hostname + "/" + s.Repo
}

// URLWithDirectory is the web URL of the directory being updated.
func (s *RepoSource) URLWithDirectory() (string, error) {
	if s.Directory == "" || s.Directory == "." || s.Directory == "/" {
		return s.URL(), nil
	}
	switch s.Provider {
	case ProviderGitHub, ProviderGitLab:
		branch := s.Branch
		if branch == "" {
			branch = "HEAD"
		}
		return s.URL() + "/" + path.Clean(path.Join("tree", branch, s.Directory)), nil
	case ProviderBitbucket:
		branch := s.Branch
		if branch == "" {
			branch = "default"
		}
		return s.URL() + "/" + path.Clean(path.Join("src", branch, s.Directory)), nil
	case ProviderAzure:
		return s.URL() + "?path=" + s.Directory, nil
	case ProviderCodeCommit:
		return "", fmt.Errorf("the codecommit provider does not use URLs")
	default:
		return "", fmt.Errorf("unexpected repo provider %q", s.Provider)
	}
}

// Organization is the owning org or user.
func (s *RepoSource) Organization() string {
	if s.Provider == ProviderAzure {
		parts := strings.Split(s.Repo, "/_git/")
		segments := strings.Split(parts[0], "/")
		if len(segments) >= 2 {
			return segments[len(segments)-2]
		}
		return segments[0]
	}
	return strings.SplitN(s.Repo, "/", 2)[0]
}

// UnscopedRepo is the repository name without its owner.
func (s *RepoSource) UnscopedRepo() string {
	parts := strings.Split(s.Repo, "/")
	return parts[len(parts)-1]
}

var (
	githubSourceRe = regexp.MustCompile(
		`(?i)(github)(?:\.com)[/:]([\w.-]+/[\w.-]+?)(?:\.git)?(?:(?:/tree|/blob)/([^/]+)/(.*)[#/])?$`)
	gitlabSourceRe = regexp.MustCompile(
		`(?i)(gitlab)(?:\.com)[/:]([^/]+/[^/]+?)(?:\.git)?(?:(?:/tree|/blob)/([^/]+)/(.*)[#/])?$`)
	bitbucketSourceRe = regexp.MustCompile(
		`(?i)(bitbucket)(?:\.org)[/:]([\w.-]+/[\w.-]+?)(?:\.git)?(?:(?:/src)/([^/]+)/(.*)[#/])?$`)
	azureSourceRe = regexp.MustCompile(
		`(?i)(azure)(?:\.com)[/:]([\w.-]+/(?:[\w.-]+/)?_git/[\w.-]+)`)
)

// RepoSourceFromURL parses a repository URL into a RepoSource, returning nil
// when the URL does not name a recognised host.
//
// Ruby additionally probes unknown hosts over HTTP to detect GitHub Enterprise.
// That is deliberately omitted: parsing should not make network calls. Callers
// that need GHES support construct a RepoSource directly with an explicit
// hostname and API endpoint. Ruby's IGNORED_PROVIDER_HOSTS list exists only to
// skip that probe, so it has no equivalent here.
func RepoSourceFromURL(rawURL string) *RepoSource {
	for provider, re := range map[string]*regexp.Regexp{
		ProviderGitHub:    githubSourceRe,
		ProviderGitLab:    gitlabSourceRe,
		ProviderBitbucket: bitbucketSourceRe,
	} {
		if m := re.FindStringSubmatch(rawURL); m != nil {
			src, err := NewRepoSource(provider, strings.TrimSuffix(m[2], "."))
			if err != nil {
				return nil
			}
			src.Branch = m[3]
			src.Directory = m[4]
			return src
		}
	}
	if m := azureSourceRe.FindStringSubmatch(rawURL); m != nil {
		src, err := NewRepoSource(ProviderAzure, m[2])
		if err != nil {
			return nil
		}
		return src
	}
	return nil
}

func defaultHostname(provider string) (string, error) {
	switch provider {
	case ProviderGitHub:
		return "github.com", nil
	case ProviderBitbucket:
		return "bitbucket.org", nil
	case ProviderGitLab:
		return "gitlab.com", nil
	case ProviderAzure:
		return "dev.azure.com", nil
	case ProviderCodeCommit:
		return "us-east-1", nil
	default:
		return "", fmt.Errorf("unexpected provider %q", provider)
	}
}

func defaultAPIEndpoint(provider string) string {
	switch provider {
	case ProviderGitHub:
		return "https://api.github.com/"
	case ProviderBitbucket:
		return "https://api.bitbucket.org/2.0/"
	case ProviderGitLab:
		return "https://gitlab.com/api/v4"
	case ProviderAzure:
		return "https://dev.azure.com/"
	default:
		return ""
	}
}
