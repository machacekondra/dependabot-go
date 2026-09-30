package gomodules

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

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

func fetch(t *testing.T, root, directory string) ([]*dependabot.DependencyFile, error) {
	t.Helper()
	cfg := dependabot.FetcherConfig{RepoContentsPath: root}
	if directory != "" {
		cfg.Source = &dependabot.RepoSource{Directory: directory}
	}
	fetcher, err := NewFetcher(cfg)
	if err != nil {
		return nil, err
	}
	return fetcher.FetchFiles(context.Background())
}

func TestFetchGoModAndGoSum(t *testing.T) {
	root := checkout(t, map[string]string{
		"go.mod":  "module example.com/app\n",
		"go.sum":  "golang.org/x/mod v0.40.0 h1:abc=\n",
		"main.go": "package main\n",
	})

	files, err := fetch(t, root, "")
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("fetched %d files, want go.mod and go.sum", len(files))
	}
	if files[0].Name != GoModFile || files[1].Name != GoSumFile {
		t.Errorf("fetched %q and %q", files[0].Name, files[1].Name)
	}
	if files[0].ContentString() != "module example.com/app\n" {
		t.Errorf("go.mod content = %q", files[0].ContentString())
	}
}

// go.sum is optional: a module with no dependencies has none.
func TestFetchWithoutGoSum(t *testing.T) {
	root := checkout(t, map[string]string{"go.mod": "module example.com/app\n"})

	files, err := fetch(t, root, "")
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	if len(files) != 1 || files[0].Name != GoModFile {
		t.Errorf("fetched %+v, want go.mod alone", files)
	}
}

func TestFetchFromSubdirectory(t *testing.T) {
	root := checkout(t, map[string]string{
		"go.mod":         "module example.com/root\n",
		"service/go.mod": "module example.com/service\n",
	})

	files, err := fetch(t, root, "/service")
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("fetched %d files, want 1", len(files))
	}
	if files[0].Directory != "/service" {
		t.Errorf("Directory = %q, want /service", files[0].Directory)
	}
	if files[0].ContentString() != "module example.com/service\n" {
		t.Errorf("fetched the wrong go.mod: %q", files[0].ContentString())
	}
}

// Without a go.mod there is no Go module to update, which is an error rather
// than an empty result.
func TestFetchRequiresGoMod(t *testing.T) {
	root := checkout(t, map[string]string{"main.go": "package main\n"})

	_, err := fetch(t, root, "")
	var notFound *dependabot.FileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("FetchFiles error = %v, want a FileNotFoundError", err)
	}
}

func TestNewFetcherRequiresRepoContents(t *testing.T) {
	if _, err := NewFetcher(dependabot.FetcherConfig{}); err == nil {
		t.Error("expected an error without a repo contents path")
	}
}
