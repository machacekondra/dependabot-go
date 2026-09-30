package repofiles

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// checkout writes a set of files into a temporary directory and returns its
// root, standing in for a repository cloned to disk.
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

func TestNewDir(t *testing.T) {
	if _, err := NewDir("", "/"); err == nil {
		t.Error("expected an error without a repo contents path")
	}

	dir, err := NewDir("/checkout", "")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}
	if dir.Directory != "/" {
		t.Errorf("Directory = %q, want /", dir.Directory)
	}
}

func TestAbs(t *testing.T) {
	cases := []struct {
		directory, name, want string
	}{
		{"/", "go.mod", "/checkout/go.mod"},
		{"/sub", "go.mod", "/checkout/sub/go.mod"},
		// A job directory is repo-relative even though it starts with a slash.
		{"/sub", "nested/go.mod", "/checkout/sub/nested/go.mod"},
	}
	for _, tc := range cases {
		dir, err := NewDir("/checkout", tc.directory)
		if err != nil {
			t.Fatalf("NewDir: %v", err)
		}
		if got := dir.Abs(tc.name); got != filepath.FromSlash(tc.want) {
			t.Errorf("Abs(%q) in %q = %q, want %q", tc.name, tc.directory, got, tc.want)
		}
	}
}

func TestFetch(t *testing.T) {
	root := checkout(t, map[string]string{"sub/go.mod": "module example.com/app\n"})
	dir, err := NewDir(root, "/sub")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	file, err := dir.Fetch("go.mod")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if file.ContentString() != "module example.com/app\n" {
		t.Errorf("content = %q", file.ContentString())
	}
	// The file carries the job directory so its path in the repo is known.
	if file.Directory != "/sub" || file.Path() != "/sub/go.mod" {
		t.Errorf("directory = %q, path = %q, want /sub and /sub/go.mod", file.Directory, file.Path())
	}
}

// A missing file is a distinct condition: a fetcher may treat it as fatal or
// simply skip the file.
func TestFetchMissingFile(t *testing.T) {
	dir, err := NewDir(t.TempDir(), "/")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	_, err = dir.Fetch("go.mod")
	var notFound *dependabot.FileNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Fetch error = %v, want a FileNotFoundError", err)
	}
	if notFound.FilePath != filepath.Join("/", "go.mod") {
		t.Errorf("FilePath = %q, want /go.mod", notFound.FilePath)
	}
}

// Binary content cannot round-trip through a UTF-8 dependency file, so it is
// rejected rather than silently mangled.
func TestFetchRejectsNonUTF8(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte{0xff, 0xfe, 0x00}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	dir, err := NewDir(root, "/")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	var unparseable *dependabot.FileNotParseableError
	if _, err := dir.Fetch("go.mod"); !errors.As(err, &unparseable) {
		t.Fatalf("Fetch error = %v, want a FileNotParseableError", err)
	}
}

func TestFetchOptions(t *testing.T) {
	root := checkout(t, map[string]string{"go.sum": "hashes\n"})
	dir, err := NewDir(root, "/")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	file, err := dir.Fetch("go.sum", dependabot.WithSupportFile())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !file.SupportFile {
		t.Error("SupportFile = false, want true")
	}
}

func TestFetchIfPresent(t *testing.T) {
	root := checkout(t, map[string]string{"go.mod": "module example.com/app\n"})
	dir, err := NewDir(root, "/")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	file, err := dir.FetchIfPresent("go.mod")
	if err != nil || file == nil {
		t.Fatalf("FetchIfPresent = %v, %v; want the file", file, err)
	}

	file, err = dir.FetchIfPresent("go.sum")
	if err != nil {
		t.Fatalf("FetchIfPresent: %v", err)
	}
	if file != nil {
		t.Errorf("FetchIfPresent = %v, want nil for a missing file", file)
	}
}

// A read failure that is not "missing" still has to surface.
func TestFetchIfPresentPropagatesOtherErrors(t *testing.T) {
	root := checkout(t, map[string]string{"go.mod/nested": ""})
	dir, err := NewDir(root, "/")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	// go.mod is a directory here, so reading it fails for a reason other than
	// the file being absent.
	if _, err := dir.FetchIfPresent("go.mod"); err == nil {
		t.Error("expected reading a directory as a file to fail")
	}
}

func TestEntries(t *testing.T) {
	root := checkout(t, map[string]string{
		"sub/go.mod":        "",
		"sub/go.sum":        "",
		"sub/vendor/thing":  "",
		"outside-the-job":   "",
		"sub/.golangci.yml": "",
	})
	dir, err := NewDir(root, "/sub")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	names, err := dir.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	// Directories are not entries, and the listing does not recurse.
	want := []string{".golangci.yml", "go.mod", "go.sum"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("Entries = %v, want %v", names, want)
	}
}

func TestEntriesMissingDirectory(t *testing.T) {
	dir, err := NewDir(t.TempDir(), "/nope")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	var notFound *dependabot.FileNotFoundError
	if _, err := dir.Entries(); !errors.As(err, &notFound) {
		t.Fatalf("Entries error = %v, want a FileNotFoundError", err)
	}
}

func TestWalkNames(t *testing.T) {
	root := checkout(t, map[string]string{
		"k8s/deployment.yaml":         "",
		"k8s/base/service.yaml":       "",
		"k8s/.git/config":             "",
		"k8s/vendor/dep/manifest.yml": "",
	})
	dir, err := NewDir(root, "/k8s")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	names, err := dir.WalkNames(func(name string) bool {
		return name == ".git" || name == "vendor"
	})
	if err != nil {
		t.Fatalf("WalkNames: %v", err)
	}
	// Paths are relative to the job directory and use forward slashes whatever
	// the host filesystem does.
	want := []string{"base/service.yaml", "deployment.yaml"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("WalkNames = %v, want %v", names, want)
	}
}

// Without a skip function the whole tree is walked.
func TestWalkNamesWithoutSkip(t *testing.T) {
	root := checkout(t, map[string]string{"a.yaml": "", "nested/b.yaml": ""})
	dir, err := NewDir(root, "/")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}

	names, err := dir.WalkNames(nil)
	if err != nil {
		t.Fatalf("WalkNames: %v", err)
	}
	want := []string{"a.yaml", "nested/b.yaml"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("WalkNames = %v, want %v", names, want)
	}
}

func TestWalkNamesMissingDirectory(t *testing.T) {
	dir, err := NewDir(t.TempDir(), "/nope")
	if err != nil {
		t.Fatalf("NewDir: %v", err)
	}
	if _, err := dir.WalkNames(nil); err == nil {
		t.Error("expected walking a missing directory to fail")
	}
}
