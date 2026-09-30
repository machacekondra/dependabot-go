package gomodules

import (
	"context"
	"regexp"
	"strings"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// MetadataFinder derives a dependency's source repository from its module path.
//
// Ruby resolves vanity import paths by fetching the module page and reading its
// go-import meta tag. This port maps the paths that cover almost all real
// dependencies and returns no source for the rest, rather than making a network
// call on a path the pull request builder treats as best-effort anyway.
type MetadataFinder struct {
	cfg dependabot.MetadataConfig
}

var _ dependabot.MetadataFinder = (*MetadataFinder)(nil)

// NewMetadataFinder builds a metadata finder for one dependency.
func NewMetadataFinder(cfg dependabot.MetadataConfig) (*MetadataFinder, error) {
	return &MetadataFinder{cfg: cfg}, nil
}

// SourceURL is the dependency's repository URL, or "" when it cannot be
// determined without a network lookup.
func (f *MetadataFinder) SourceURL(_ context.Context) (string, error) {
	if f.cfg.Dependency == nil {
		return "", nil
	}
	repoURL := repoURLForModule(modulePathOf(f.cfg.Dependency))
	if repoURL == "" {
		return "", nil
	}
	src := dependabot.RepoSourceFromURL(repoURL)
	if src == nil {
		return "", nil
	}
	return src.URL(), nil
}

// modulePathOf prefers the path recorded on the requirement's source, which is
// what a replace directive would have changed, and falls back to the name.
func modulePathOf(dep *dependabot.Dependency) string {
	for _, req := range dep.Requirements {
		if req.Source != nil {
			if p := req.Source.Get("source"); p != "" {
				return p
			}
		}
	}
	return dep.Name
}

// majorVersionSuffixRe matches the "/v2" a module path carries from v2 onwards.
var majorVersionSuffixRe = regexp.MustCompile(`/v[1-9][0-9]*$`)

// gopkgInRe matches gopkg.in paths, in both their "pkg.v3" and "user/pkg.v3"
// forms.
var gopkgInRe = regexp.MustCompile(`^gopkg\.in/(?:([^/]+)/)?([^/.]+)\.v[0-9]+$`)

// vanityHosts maps the module-path prefix of a vanity domain to the GitHub
// organisation it redirects to.
var vanityHosts = map[string]string{
	"golang.org/x":      "github.com/golang",
	"google.golang.org": "github.com/googleapis",
	"go.uber.org":       "github.com/uber-go",
	"k8s.io":            "github.com/kubernetes",
	"sigs.k8s.io":       "github.com/kubernetes-sigs",
	"go.etcd.io":        "github.com/etcd-io",
	"gotest.tools":      "github.com/gotestyourself/gotest.tools",
}

// directHosts are hosts whose module paths are already repository paths, of the
// form host/owner/repo.
var directHosts = []string{"github.com", "gitlab.com", "bitbucket.org"}

// repoURLForModule turns a module path into a repository URL, or "" when the
// path is not one we can map without a lookup.
func repoURLForModule(modulePath string) string {
	if modulePath == "" {
		return ""
	}
	modulePath = majorVersionSuffixRe.ReplaceAllString(modulePath, "")

	if m := gopkgInRe.FindStringSubmatch(modulePath); m != nil {
		owner, repo := m[1], m[2]
		if owner == "" {
			// gopkg.in/yaml.v3 is github.com/go-yaml/yaml.
			owner = "go-" + repo
		}
		return "https://github.com/" + owner + "/" + repo
	}

	for _, host := range directHosts {
		if !strings.HasPrefix(modulePath, host+"/") {
			continue
		}
		segments := strings.Split(modulePath, "/")
		if len(segments) < 3 {
			return ""
		}
		return "https://" + strings.Join(segments[:3], "/")
	}

	// Longest prefix wins, so "sigs.k8s.io" is not shadowed by a shorter entry.
	bestPrefix, bestTarget := "", ""
	for prefix, target := range vanityHosts {
		if modulePath != prefix && !strings.HasPrefix(modulePath, prefix+"/") {
			continue
		}
		if len(prefix) > len(bestPrefix) {
			bestPrefix, bestTarget = prefix, target
		}
	}
	if bestPrefix == "" {
		return ""
	}

	rest := strings.TrimPrefix(strings.TrimPrefix(modulePath, bestPrefix), "/")
	if rest == "" {
		// The vanity prefix is the whole module, e.g. gotest.tools.
		if strings.Count(bestTarget, "/") == 2 {
			return "https://" + bestTarget
		}
		return ""
	}
	// Only the first segment after the prefix names a repository; anything
	// further is a package within it.
	repo := strings.SplitN(rest, "/", 2)[0]
	if strings.Count(bestTarget, "/") == 2 {
		return "https://" + bestTarget
	}
	return "https://" + bestTarget + "/" + repo
}
