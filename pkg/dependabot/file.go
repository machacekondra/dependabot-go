package dependabot

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"path"
	"strings"
)

// Content encodings for DependencyFile.
const (
	EncodingUTF8   = "utf-8"
	EncodingBase64 = "base64"
)

// Operations describing what should happen to a file in the resulting commit.
const (
	OperationUpdate = "update"
	OperationCreate = "create"
	OperationDelete = "delete"
)

// Git file modes. See
// https://github.com/git/git/blob/a36e024e989f4d35f35987a60e3af8022cac3420/object.h#L144-L153
const (
	ModeFile       = "100644"
	ModeExecutable = "100755"
	ModeTree       = "040000"
	ModeSubmodule  = "160000"
	ModeSymlink    = "120000"
)

var validModes = map[string]bool{
	ModeFile:       true,
	ModeExecutable: true,
	ModeTree:       true,
	ModeSubmodule:  true,
	ModeSymlink:    true,
}

// DependencyFile is a manifest or lockfile, fetched from a repository and
// carried through parse → check → update.
type DependencyFile struct {
	// Name is the path relative to Directory, e.g. "go.mod" or "sub/go.sum".
	Name string
	// Content is nil for a file that was fetched but could not be read.
	Content *string
	// Directory is the directory of the *job*, not of the file. It always
	// starts with "/".
	Directory string
	// Type is used sparingly: the submodule updater uses it to mark a "file"
	// as a submodule, and the Go updaters use it to find main.go. Prefer
	// SupportFile for new cases.
	Type            string
	SupportFile     bool
	VendoredFile    bool
	SymlinkTarget   string
	ContentEncoding string
	Operation       string
	Mode            string
}

// FileOption configures a DependencyFile at construction.
type FileOption func(*DependencyFile)

// WithDirectory sets the job directory the file was fetched relative to.
func WithDirectory(dir string) FileOption {
	return func(f *DependencyFile) { f.Directory = cleanDirectory(dir) }
}

// WithFileType sets the file's type marker.
func WithFileType(t string) FileOption { return func(f *DependencyFile) { f.Type = t } }

// WithSupportFile marks the file as a support file: fetched for context, never
// itself updated.
func WithSupportFile() FileOption { return func(f *DependencyFile) { f.SupportFile = true } }

// WithVendoredFile marks the file as vendored.
func WithVendoredFile() FileOption { return func(f *DependencyFile) { f.VendoredFile = true } }

// WithSymlinkTarget marks the file as a symlink pointing at target.
func WithSymlinkTarget(target string) FileOption {
	return func(f *DependencyFile) {
		f.Type = "symlink"
		f.SymlinkTarget = target
	}
}

// WithBase64Content marks the file's content as base64-encoded.
func WithBase64Content() FileOption {
	return func(f *DependencyFile) { f.ContentEncoding = EncodingBase64 }
}

// WithOperation sets the operation to apply to the file.
func WithOperation(op string) FileOption { return func(f *DependencyFile) { f.Operation = op } }

// WithMode sets the git file mode.
func WithMode(mode string) FileOption { return func(f *DependencyFile) { f.Mode = mode } }

// NewFile builds a DependencyFile. It returns an error rather than panicking on
// an invalid mode or a mismatched symlink, which is where the Ruby constructor
// raises.
func NewFile(name, content string, opts ...FileOption) (*DependencyFile, error) {
	f := &DependencyFile{
		Name:            name,
		Content:         &content,
		Directory:       "/",
		Type:            "file",
		ContentEncoding: EncodingUTF8,
		Operation:       OperationUpdate,
	}
	for _, opt := range opts {
		opt(f)
	}

	if f.Mode != "" && !validModes[f.Mode] {
		return nil, fmt.Errorf("invalid git mode: %s", f.Mode)
	}
	if (f.Type == "symlink") != (f.SymlinkTarget != "") {
		if f.SymlinkTarget == "" {
			return nil, fmt.Errorf("symlinks must specify a target: %s", name)
		}
		return nil, fmt.Errorf("only symlinked files may specify a target: %s", name)
	}
	return f, nil
}

// MustNewFile is NewFile for cases where the arguments are known-good, such as
// literals in tests and fixtures.
func MustNewFile(name, content string, opts ...FileOption) *DependencyFile {
	f, err := NewFile(name, content, opts...)
	if err != nil {
		panic(err)
	}
	return f
}

// Path is the file's location within the repository.
func (f *DependencyFile) Path() string {
	return path.Clean(path.Join(f.Directory, f.Name))
}

// RealPath resolves a symlink to its target and strips the leading slash.
func (f *DependencyFile) RealPath() string {
	p := f.SymlinkTarget
	if p == "" {
		p = f.Path()
	}
	return strings.TrimPrefix(p, "/")
}

// Deleted reports whether the file is marked for deletion.
func (f *DependencyFile) Deleted() bool { return f.Operation == OperationDelete }

// Binary reports whether the content is base64-encoded.
func (f *DependencyFile) Binary() bool { return f.ContentEncoding == EncodingBase64 }

// DecodedContent returns the file's content, base64-decoding it if needed.
func (f *DependencyFile) DecodedContent() (string, error) {
	if f.Content == nil {
		return "", fmt.Errorf("file %s has no content", f.Name)
	}
	if !f.Binary() {
		return *f.Content, nil
	}
	raw, err := base64.StdEncoding.DecodeString(*f.Content)
	if err != nil {
		return "", fmt.Errorf("decoding %s: %w", f.Name, err)
	}
	return string(raw), nil
}

// ContentString returns the content, or "" when it is absent. Use
// DecodedContent when the file may be binary.
func (f *DependencyFile) ContentString() string {
	if f.Content == nil {
		return ""
	}
	return *f.Content
}

// WithContent returns a copy of the file carrying new content, which is how
// file updaters produce their output.
func (f *DependencyFile) WithContent(content string) *DependencyFile {
	updated := *f
	updated.Content = &content
	return &updated
}

// BlobOID returns the git blob OID for the file's content, matching the value
// GitHub uses for the same blob. algorithm is "sha1" (default) or "sha256".
func (f *DependencyFile) BlobOID(algorithm string) (string, error) {
	content, err := f.DecodedContent()
	if err != nil {
		return "", err
	}
	var h hash.Hash
	if algorithm == "sha256" {
		h = sha256.New()
	} else {
		h = sha1.New()
	}
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write([]byte(content))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FindFile returns the file with the given name, or nil.
func FindFile(files []*DependencyFile, name string) *DependencyFile {
	for _, f := range files {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func cleanDirectory(dir string) string {
	return "/" + strings.TrimLeft(dir, "/")
}
