package dependabot

import (
	"encoding/base64"
	"testing"
)

func TestNewFileDefaults(t *testing.T) {
	f, err := NewFile("go.mod", "module example.com/app\n")
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}
	if f.Directory != "/" {
		t.Errorf("Directory = %q, want /", f.Directory)
	}
	if f.Type != "file" || f.ContentEncoding != EncodingUTF8 || f.Operation != OperationUpdate {
		t.Errorf("defaults = %q %q %q, want file, utf-8 and update",
			f.Type, f.ContentEncoding, f.Operation)
	}
	if f.Deleted() || f.Binary() {
		t.Error("a plain file is neither deleted nor binary")
	}
}

func TestNewFileValidation(t *testing.T) {
	if _, err := NewFile("go.mod", "", WithMode("777")); err == nil {
		t.Error("expected an error for an invalid git mode")
	}
	if _, err := NewFile("go.mod", "", WithMode(ModeExecutable)); err != nil {
		t.Errorf("a valid mode was rejected: %v", err)
	}
	// A symlink must point somewhere, and only a symlink may have a target.
	if _, err := NewFile("link", "", WithFileType("symlink")); err == nil {
		t.Error("expected an error for a symlink with no target")
	}
	if _, err := NewFile("link", "", WithSymlinkTarget("/go.mod")); err != nil {
		t.Errorf("a valid symlink was rejected: %v", err)
	}
}

func TestFilePath(t *testing.T) {
	cases := []struct {
		directory, name, want string
	}{
		{"/", "go.mod", "/go.mod"},
		{"/sub", "go.mod", "/sub/go.mod"},
		// The directory is normalised to start with exactly one slash.
		{"sub", "go.mod", "/sub/go.mod"},
		{"//sub", "go.mod", "/sub/go.mod"},
		{"/sub", "nested/go.mod", "/sub/nested/go.mod"},
	}
	for _, tc := range cases {
		f := MustNewFile(tc.name, "", WithDirectory(tc.directory))
		if got := f.Path(); got != tc.want {
			t.Errorf("Path() for %q + %q = %q, want %q", tc.directory, tc.name, got, tc.want)
		}
	}
}

// A symlink is updated at its target, and paths are relative to the repo root.
func TestFileRealPath(t *testing.T) {
	plain := MustNewFile("go.mod", "", WithDirectory("/sub"))
	if got := plain.RealPath(); got != "sub/go.mod" {
		t.Errorf("RealPath = %q, want sub/go.mod", got)
	}

	link := MustNewFile("link", "", WithSymlinkTarget("/real/go.mod"))
	if got := link.RealPath(); got != "real/go.mod" {
		t.Errorf("RealPath = %q, want real/go.mod", got)
	}
}

func TestFileDecodedContent(t *testing.T) {
	plain := MustNewFile("go.mod", "content")
	got, err := plain.DecodedContent()
	if err != nil {
		t.Fatalf("DecodedContent: %v", err)
	}
	if got != "content" {
		t.Errorf("DecodedContent = %q, want content", got)
	}

	encoded := MustNewFile("blob", base64.StdEncoding.EncodeToString([]byte("content")),
		WithBase64Content())
	if !encoded.Binary() {
		t.Error("Binary = false, want true")
	}
	got, err = encoded.DecodedContent()
	if err != nil {
		t.Fatalf("DecodedContent: %v", err)
	}
	if got != "content" {
		t.Errorf("DecodedContent = %q, want content", got)
	}

	broken := MustNewFile("blob", "not base64!!", WithBase64Content())
	if _, err := broken.DecodedContent(); err == nil {
		t.Error("expected an error decoding invalid base64")
	}

	// A file that was fetched but could not be read has no content at all.
	unreadable := &DependencyFile{Name: "go.mod"}
	if _, err := unreadable.DecodedContent(); err == nil {
		t.Error("expected an error for a file with no content")
	}
	if got := unreadable.ContentString(); got != "" {
		t.Errorf("ContentString = %q, want an empty string", got)
	}
}

// WithContent is how a file updater produces its output, so it must not touch
// the file it was given.
func TestFileWithContent(t *testing.T) {
	original := MustNewFile("go.mod", "before", WithDirectory("/sub"))
	updated := original.WithContent("after")

	if original.ContentString() != "before" {
		t.Error("the original file was mutated")
	}
	if updated.ContentString() != "after" {
		t.Errorf("ContentString = %q, want after", updated.ContentString())
	}
	if updated.Name != "go.mod" || updated.Directory != "/sub" {
		t.Error("the copy lost the file's identity")
	}
}

// The OID has to match what git would compute for the same blob, since it is
// compared against the value the host API returns.
func TestFileBlobOID(t *testing.T) {
	f := MustNewFile("hello", "hello\n")

	sha1OID, err := f.BlobOID("sha1")
	if err != nil {
		t.Fatalf("BlobOID: %v", err)
	}
	// `printf 'hello\n' | git hash-object --stdin`
	if sha1OID != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("sha1 BlobOID = %q, want git's value", sha1OID)
	}

	sha256OID, err := f.BlobOID("sha256")
	if err != nil {
		t.Fatalf("BlobOID: %v", err)
	}
	if len(sha256OID) != 64 {
		t.Errorf("sha256 BlobOID = %q, want 64 hex characters", sha256OID)
	}
}

func TestFileOperations(t *testing.T) {
	deleted := MustNewFile("go.sum", "", WithOperation(OperationDelete))
	if !deleted.Deleted() {
		t.Error("Deleted = false, want true")
	}
	created := MustNewFile("go.sum", "", WithOperation(OperationCreate))
	if created.Deleted() {
		t.Error("Deleted = true, want false")
	}
}

func TestFindFile(t *testing.T) {
	files := []*DependencyFile{
		MustNewFile("go.mod", ""),
		MustNewFile("go.sum", ""),
	}
	if f := FindFile(files, "go.sum"); f == nil || f.Name != "go.sum" {
		t.Errorf("FindFile = %v, want go.sum", f)
	}
	if f := FindFile(files, "Gemfile"); f != nil {
		t.Errorf("FindFile = %v, want nil", f)
	}
}

func TestMustNewFilePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustNewFile did not panic on invalid arguments")
		}
	}()
	MustNewFile("go.mod", "", WithMode("777"))
}
