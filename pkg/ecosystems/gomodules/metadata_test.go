package gomodules

import (
	"context"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

func TestRepoURLForModule(t *testing.T) {
	cases := map[string]string{
		// Hosts whose module path is already a repository path.
		"github.com/spf13/cobra":           "https://github.com/spf13/cobra",
		"github.com/aws/aws-sdk-go-v2/s3":  "https://github.com/aws/aws-sdk-go-v2",
		"gitlab.com/group/project":         "https://gitlab.com/group/project",
		"bitbucket.org/owner/repo/sub/pkg": "https://bitbucket.org/owner/repo",
		// A major-version suffix is part of the import path, not the repository.
		"github.com/go-redis/redis/v8": "https://github.com/go-redis/redis",
		// gopkg.in, in both of its forms.
		"gopkg.in/yaml.v3":     "https://github.com/go-yaml/yaml",
		"gopkg.in/check.v1":    "https://github.com/go-check/check",
		"gopkg.in/user/pkg.v2": "https://github.com/user/pkg",
		// Vanity domains, where only the first segment names a repository.
		"golang.org/x/mod":               "https://github.com/golang/mod",
		"golang.org/x/tools/go/packages": "https://github.com/golang/tools",
		"google.golang.org/grpc":         "https://github.com/googleapis/grpc",
		"go.uber.org/zap":                "https://github.com/uber-go/zap",
		"k8s.io/client-go":               "https://github.com/kubernetes/client-go",
		// The longer prefix has to win over "k8s.io".
		"sigs.k8s.io/yaml": "https://github.com/kubernetes-sigs/yaml",
		"go.etcd.io/bbolt": "https://github.com/etcd-io/bbolt",
		// A vanity prefix that is itself a full repository path.
		"gotest.tools/v3":        "https://github.com/gotestyourself/gotest.tools",
		"gotest.tools/v3/assert": "https://github.com/gotestyourself/gotest.tools",
		// Paths we cannot map without a network lookup.
		"example.com/private/thing": "",
		"":                          "",
	}
	for modulePath, want := range cases {
		if got := repoURLForModule(modulePath); got != want {
			t.Errorf("repoURLForModule(%q) = %q, want %q", modulePath, got, want)
		}
	}
}

func TestMetadataFinderSourceURL(t *testing.T) {
	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:           "golang.org/x/mod",
		Version:        "v0.41.0",
		PackageManager: EcosystemName,
		Requirements: []dependabot.Requirement{{
			Requirement: "v0.41.0", File: GoModFile,
			Source: &dependabot.Source{Extra: map[string]string{"source": "golang.org/x/mod"}},
		}},
	})

	finder, err := NewMetadataFinder(dependabot.MetadataConfig{Dependency: dep})
	if err != nil {
		t.Fatalf("NewMetadataFinder: %v", err)
	}
	url, err := finder.SourceURL(context.Background())
	if err != nil {
		t.Fatalf("SourceURL: %v", err)
	}
	if url != "https://github.com/golang/mod" {
		t.Errorf("SourceURL = %q, want https://github.com/golang/mod", url)
	}
}

// An unmappable path is not an error: the pull request just goes out without
// a link to the source.
func TestMetadataFinderUnknownHost(t *testing.T) {
	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:           "example.com/private/thing",
		Version:        "v1.0.0",
		PackageManager: EcosystemName,
	})
	finder, err := NewMetadataFinder(dependabot.MetadataConfig{Dependency: dep})
	if err != nil {
		t.Fatalf("NewMetadataFinder: %v", err)
	}
	url, err := finder.SourceURL(context.Background())
	if err != nil {
		t.Fatalf("SourceURL: %v", err)
	}
	if url != "" {
		t.Errorf("SourceURL = %q, want an empty string", url)
	}
}

// A replace directive changes where the module actually comes from, and the
// requirement's source records that.
func TestMetadataFinderPrefersRequirementSource(t *testing.T) {
	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:           "example.com/aliased",
		Version:        "v1.0.0",
		PackageManager: EcosystemName,
		Requirements: []dependabot.Requirement{{
			Requirement: "v1.0.0", File: GoModFile,
			Source: &dependabot.Source{Extra: map[string]string{"source": "github.com/real/repo"}},
		}},
	})
	finder, err := NewMetadataFinder(dependabot.MetadataConfig{Dependency: dep})
	if err != nil {
		t.Fatalf("NewMetadataFinder: %v", err)
	}
	url, err := finder.SourceURL(context.Background())
	if err != nil {
		t.Fatalf("SourceURL: %v", err)
	}
	if url != "https://github.com/real/repo" {
		t.Errorf("SourceURL = %q, want the source recorded on the requirement", url)
	}
}
