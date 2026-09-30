package dependabot

import "testing"

func TestNewRepoSourceDefaults(t *testing.T) {
	cases := map[string]struct{ hostname, api string }{
		ProviderGitHub:     {"github.com", "https://api.github.com/"},
		ProviderGitLab:     {"gitlab.com", "https://gitlab.com/api/v4"},
		ProviderBitbucket:  {"bitbucket.org", "https://api.bitbucket.org/2.0/"},
		ProviderAzure:      {"dev.azure.com", "https://dev.azure.com/"},
		ProviderCodeCommit: {"us-east-1", ""},
	}
	for provider, want := range cases {
		src, err := NewRepoSource(provider, "owner/repo")
		if err != nil {
			t.Fatalf("NewRepoSource(%q): %v", provider, err)
		}
		if src.Hostname != want.hostname || src.APIEndpoint != want.api {
			t.Errorf("%s: hostname %q api %q, want %q and %q",
				provider, src.Hostname, src.APIEndpoint, want.hostname, want.api)
		}
	}

	if _, err := NewRepoSource("sourceforge", "owner/repo"); err == nil {
		t.Error("expected an error for an unknown provider")
	}
}

func TestRepoSourceURLWithDirectory(t *testing.T) {
	cases := []struct {
		provider, directory, branch, want string
	}{
		{ProviderGitHub, "/", "", "https://github.com/owner/repo"},
		{ProviderGitHub, "", "", "https://github.com/owner/repo"},
		{ProviderGitHub, ".", "", "https://github.com/owner/repo"},
		{ProviderGitHub, "/sub", "", "https://github.com/owner/repo/tree/HEAD/sub"},
		{ProviderGitHub, "/sub", "main", "https://github.com/owner/repo/tree/main/sub"},
		{ProviderGitLab, "/sub", "main", "https://gitlab.com/owner/repo/tree/main/sub"},
		{ProviderBitbucket, "/sub", "", "https://bitbucket.org/owner/repo/src/default/sub"},
		{ProviderAzure, "/sub", "", "https://dev.azure.com/owner/repo?path=/sub"},
	}
	for _, tc := range cases {
		src, err := NewRepoSource(tc.provider, "owner/repo")
		if err != nil {
			t.Fatalf("NewRepoSource: %v", err)
		}
		src.Directory, src.Branch = tc.directory, tc.branch

		got, err := src.URLWithDirectory()
		if err != nil {
			t.Fatalf("URLWithDirectory: %v", err)
		}
		if got != tc.want {
			t.Errorf("%s %q: URLWithDirectory = %q, want %q",
				tc.provider, tc.directory, got, tc.want)
		}
	}

	// CodeCommit repositories are addressed by ARN, not URL.
	src, err := NewRepoSource(ProviderCodeCommit, "repo")
	if err != nil {
		t.Fatalf("NewRepoSource: %v", err)
	}
	src.Directory = "/sub"
	if _, err := src.URLWithDirectory(); err == nil {
		t.Error("expected an error for codecommit")
	}
}

func TestRepoSourceOrganization(t *testing.T) {
	github, _ := NewRepoSource(ProviderGitHub, "dependabot/dependabot-core")
	if got := github.Organization(); got != "dependabot" {
		t.Errorf("Organization = %q, want dependabot", got)
	}
	if got := github.UnscopedRepo(); got != "dependabot-core" {
		t.Errorf("UnscopedRepo = %q, want dependabot-core", got)
	}

	// Azure repository paths carry an organisation, a project and "_git".
	azure, _ := NewRepoSource(ProviderAzure, "org/project/_git/repo")
	if got := azure.Organization(); got != "org" {
		t.Errorf("Azure Organization = %q, want org", got)
	}
	if got := azure.UnscopedRepo(); got != "repo" {
		t.Errorf("Azure UnscopedRepo = %q, want repo", got)
	}
}

func TestRepoSourceFromURL(t *testing.T) {
	cases := map[string]struct {
		provider, repo string
	}{
		"https://github.com/dependabot/dependabot-core":     {ProviderGitHub, "dependabot/dependabot-core"},
		"https://github.com/dependabot/dependabot-core.git": {ProviderGitHub, "dependabot/dependabot-core"},
		"git@github.com:dependabot/dependabot-core.git":     {ProviderGitHub, "dependabot/dependabot-core"},
		"https://gitlab.com/group/project":                  {ProviderGitLab, "group/project"},
		"https://bitbucket.org/owner/repo":                  {ProviderBitbucket, "owner/repo"},
		"https://dev.azure.com/org/_git/repo":               {ProviderAzure, "org/_git/repo"},
	}
	for rawURL, want := range cases {
		src := RepoSourceFromURL(rawURL)
		if src == nil {
			t.Errorf("RepoSourceFromURL(%q) = nil, want a source", rawURL)
			continue
		}
		if src.Provider != want.provider || src.Repo != want.repo {
			t.Errorf("RepoSourceFromURL(%q) = %s %q, want %s %q",
				rawURL, src.Provider, src.Repo, want.provider, want.repo)
		}
	}
}

// A URL pointing into a repository carries the branch and directory with it.
func TestRepoSourceFromURLWithPath(t *testing.T) {
	src := RepoSourceFromURL("https://github.com/owner/repo/tree/main/packages/app#")
	if src == nil {
		t.Fatal("RepoSourceFromURL = nil, want a source")
	}
	if src.Repo != "owner/repo" {
		t.Errorf("Repo = %q, want owner/repo", src.Repo)
	}
	if src.Branch != "main" {
		t.Errorf("Branch = %q, want main", src.Branch)
	}
	if src.Directory != "packages/app" {
		t.Errorf("Directory = %q, want packages/app", src.Directory)
	}
}

// Parsing makes no network calls, so a host it does not recognise gets no
// source rather than a guess.
func TestRepoSourceFromURLUnknownHost(t *testing.T) {
	for _, rawURL := range []string{
		"https://git.example.com/owner/repo",
		"https://gitbox.apache.org/repos/asf/thing.git",
		"not a url",
	} {
		if src := RepoSourceFromURL(rawURL); src != nil {
			t.Errorf("RepoSourceFromURL(%q) = %+v, want nil", rawURL, src)
		}
	}
}
