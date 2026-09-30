package docker

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// checkout writes a repository to a temporary directory, keyed by path.
func checkout(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return root
}

func fetch(t *testing.T, root, directory string) ([]string, error) {
	t.Helper()
	fetcher, err := NewFetcher(dependabot.FetcherConfig{
		RepoContentsPath: root,
		Source:           &dependabot.RepoSource{Directory: directory},
	})
	if err != nil {
		return nil, err
	}
	files, err := fetcher.FetchFiles(context.Background())
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names, nil
}

func TestFetchDockerfiles(t *testing.T) {
	root := checkout(t, map[string]string{
		"Dockerfile":     "FROM ubuntu:22.04\n",
		"Dockerfile.dev": "FROM ubuntu:22.04\n",
		"Containerfile":  "FROM alpine:3.19\n",
		// The name is matched case-insensitively, anywhere in the file name.
		"app.dockerfile": "FROM debian:12\n",
		"README.md":      "hello",
		"main.go":        "package main",
	})

	names, err := fetch(t, root, "/")
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	want := []string{"Containerfile", "Dockerfile", "Dockerfile.dev", "app.dockerfile"}
	if len(names) != len(want) {
		t.Fatalf("fetched %v, want %v", names, want)
	}
	for i, name := range want {
		if names[i] != name {
			t.Errorf("fetched %v, want %v", names, want)
			break
		}
	}
}

// A repository holds plenty of YAML that has nothing to do with containers;
// only Kubernetes resources and Helm values are ours.
func TestFetchFiltersYAML(t *testing.T) {
	root := checkout(t, map[string]string{
		"deployment.yaml":          "apiVersion: apps/v1\nkind: Deployment\nspec: {}\n",
		"values.yaml":              "image:\n  repository: nginx\n  tag: 1.25.3\n",
		"values-prod.yml":          "image:\n  repository: nginx\n  tag: 1.25.3\n",
		"config.yaml":              "log_level: debug\n",
		".github/workflows/ci.yml": "name: ci\non: push\n",
	})

	names, err := fetch(t, root, "/")
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	for _, name := range names {
		if name == "config.yaml" {
			t.Error("a plain config file should not be fetched")
		}
	}
	found := map[string]bool{}
	for _, name := range names {
		found[name] = true
	}
	for _, want := range []string{"deployment.yaml", "values.yaml", "values-prod.yml"} {
		if !found[want] {
			t.Errorf("%s was not fetched, got %v", want, names)
		}
	}
}

// Malformed YAML is skipped rather than failing the job.
func TestFetchSkipsUnparseableYAML(t *testing.T) {
	root := checkout(t, map[string]string{
		"Dockerfile":  "FROM ubuntu:22.04\n",
		"broken.yaml": "key: [unclosed\n  nope: :\n",
	})

	names, err := fetch(t, root, "/")
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	if len(names) != 1 || names[0] != "Dockerfile" {
		t.Errorf("fetched %v, want just the Dockerfile", names)
	}
}

// A BOM would otherwise fail the YAML parse and lose the file.
func TestFetchStripsBOMBeforeInspectingYAML(t *testing.T) {
	root := checkout(t, map[string]string{
		"deployment.yaml": "\ufeffapiVersion: apps/v1\nkind: Deployment\nspec: {}\n",
	})

	names, err := fetch(t, root, "/")
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	if len(names) != 1 || names[0] != "deployment.yaml" {
		t.Errorf("fetched %v, want the manifest despite its BOM", names)
	}
}

func TestFetchFromSubdirectory(t *testing.T) {
	root := checkout(t, map[string]string{
		"Dockerfile":         "FROM ubuntu:22.04\n",
		"service/Dockerfile": "FROM alpine:3.19\n",
	})

	files, err := (func() ([]*dependabot.DependencyFile, error) {
		fetcher, err := NewFetcher(dependabot.FetcherConfig{
			RepoContentsPath: root,
			Source:           &dependabot.RepoSource{Directory: "/service"},
		})
		if err != nil {
			return nil, err
		}
		return fetcher.FetchFiles(context.Background())
	})()
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("fetched %d files, want 1", len(files))
	}
	if files[0].ContentString() != "FROM alpine:3.19\n" {
		t.Errorf("fetched the wrong Dockerfile: %q", files[0].ContentString())
	}
	if files[0].Directory != "/service" {
		t.Errorf("directory = %q, want /service", files[0].Directory)
	}
}

func TestFetchNothingToUpdate(t *testing.T) {
	root := checkout(t, map[string]string{"README.md": "hello"})

	if _, err := fetch(t, root, "/"); err == nil {
		t.Fatal("expected an error when there is nothing to update")
	}
}

func TestNewFetcherRequiresRepoContents(t *testing.T) {
	if _, err := NewFetcher(dependabot.FetcherConfig{}); err == nil {
		t.Error("expected an error without a repo contents path")
	}
}
