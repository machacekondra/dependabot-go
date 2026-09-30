package dependabot

import "testing"

func TestNewDependencyValidation(t *testing.T) {
	cases := map[string]Dependency{
		"a blank name":       {PackageManager: "test"},
		"no package manager": {Name: "dep"},
		"a blank requirement": {Name: "dep", PackageManager: "test",
			Requirements: []Requirement{{Requirement: "  ", File: "manifest"}}},
		"a requirement with no file": {Name: "dep", PackageManager: "test",
			Requirements: []Requirement{{Requirement: "1.0.0"}}},
	}
	for name, dep := range cases {
		if _, err := NewDependency(dep); err == nil {
			t.Errorf("NewDependency with %s succeeded, want an error", name)
		}
	}
}

// Subdependency metadata describes where a lockfile-only dependency came from,
// so it is meaningless once the dependency has manifest requirements.
func TestNewDependencyDropsSubdependencyMetadata(t *testing.T) {
	production := true
	dep, err := NewDependency(Dependency{
		Name: "dep", PackageManager: "test",
		Requirements:          []Requirement{{Requirement: "1.0.0", File: "manifest"}},
		SubdependencyMetadata: []SubdependencyMetadata{{Production: &production}},
	})
	if err != nil {
		t.Fatalf("NewDependency: %v", err)
	}
	if dep.SubdependencyMetadata != nil {
		t.Error("a top-level dependency should not keep subdependency metadata")
	}
}

func TestDependencyTopLevel(t *testing.T) {
	top := MustNewDependency(Dependency{
		Name: "dep", PackageManager: "test",
		Requirements: []Requirement{{Requirement: "1.0.0", File: "manifest"}},
	})
	sub := MustNewDependency(Dependency{Name: "dep", Version: "1.0.0", PackageManager: "test"})

	if !top.TopLevel() {
		t.Error("a dependency with requirements is top level")
	}
	if sub.TopLevel() {
		t.Error("a dependency with no requirements is not top level")
	}
}

func TestDependencyHumanizedVersion(t *testing.T) {
	sha := "0b84568fffcc1cbbf2b3dcdd9e7b4a6c5d8e9f0a1b2c3d4e5f60718293a4b5c6"

	cases := []struct {
		name string
		dep  Dependency
		want string
	}{
		{"a plain version",
			Dependency{Name: "dep", PackageManager: "test", Version: "1.2.3"}, "1.2.3"},
		{"a removed dependency",
			Dependency{Name: "dep", PackageManager: "test", Version: "1.2.3", Removed: true}, "removed"},
		{"no version",
			Dependency{Name: "dep", PackageManager: "test"}, ""},
		// A commit is abbreviated the way git would show it.
		{"a commit",
			Dependency{Name: "dep", PackageManager: "test", Version: sha}, "`" + sha[:7] + "`"},
	}
	for _, tc := range cases {
		dep := MustNewDependency(tc.dep)
		if got := dep.HumanizedVersion(); got != tc.want {
			t.Errorf("%s: HumanizedVersion = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A ref move is the update, so the ref is what the title should name.
func TestDependencyHumanizedVersionPrefersRef(t *testing.T) {
	dep := MustNewDependency(Dependency{
		Name: "dep", PackageManager: "test",
		Version:         "0b84568fffcc1cbbf2b3dcdd9e7b4a6c5d8e9f0a1b2c3d4e5f60718293a4b5c6",
		PreviousVersion: "aaaaaaaaaaaacbbbf2b3dcdd9e7b4a6c5d8e9f0a1b2c3d4e5f60718293a4b5c6",
		Requirements: []Requirement{{
			File: "manifest", Source: &Source{Ref: "v2.0.0"},
		}},
		PreviousRequirements: []Requirement{{
			File: "manifest", Source: &Source{Ref: "v1.0.0"},
		}},
	})

	if got := dep.HumanizedVersion(); got != "v2.0.0" {
		t.Errorf("HumanizedVersion = %q, want v2.0.0", got)
	}
	if got := dep.HumanizedPreviousVersion(); got != "v1.0.0" {
		t.Errorf("HumanizedPreviousVersion = %q, want v1.0.0", got)
	}
	if !dep.RefChanged() {
		t.Error("RefChanged = false, want true")
	}
}

// Docker can update an image whose tag has not moved, by repointing the
// digest; the digest is then the only thing worth naming.
func TestDependencyHumanizedVersionDockerDigest(t *testing.T) {
	old := "1bcefdb2ab5edb0e0e7b9ba8cd27ba5bfdc6e8f4e2ca4a6b1f9a3a5e9e5b9c2d"
	updated := "2cd0a1e3b4f5061728394a5b6c7d8e9f0a1b2c3d4e5f60718293a4b5c6d7e8f9"

	dep := MustNewDependency(Dependency{
		Name: "ubuntu", PackageManager: "docker",
		Version: "22.04", PreviousVersion: "22.04",
		Requirements: []Requirement{{
			File: "Dockerfile", Source: &Source{Tag: "22.04", Digest: updated},
		}},
		PreviousRequirements: []Requirement{{
			File: "Dockerfile", Source: &Source{Tag: "22.04", Digest: old},
		}},
	})

	if got := dep.HumanizedVersion(); got == "22.04" {
		t.Error("an unmoved tag should be shown as its digest")
	}
	if dep.HumanizedVersion() == dep.HumanizedPreviousVersion() {
		t.Error("the two digests should render differently")
	}
}

func TestDependencyChangedRequirements(t *testing.T) {
	dep := MustNewDependency(Dependency{
		Name: "dep", PackageManager: "test",
		Requirements: []Requirement{
			{Requirement: "1.2.0", File: "manifest"},
			{Requirement: "1.2.0", File: "other"},
		},
		PreviousRequirements: []Requirement{
			{Requirement: "1.1.0", File: "manifest"},
			{Requirement: "1.2.0", File: "other"},
		},
	})

	changed := dep.ChangedRequirements()
	if len(changed) != 1 || changed[0].File != "manifest" {
		t.Errorf("ChangedRequirements = %+v, want only the manifest", changed)
	}
	if !dep.RequirementsChanged() {
		t.Error("RequirementsChanged = false, want true")
	}
}

func TestDependencyVersionIsSHA(t *testing.T) {
	full := "0b84568fffcc1cbbf2b3dcdd9e7b4a6c5d8e9f0a"
	cases := []struct {
		version       string
		isSHA, isFull bool
	}{
		{full, true, true},
		{"0b84568", true, false},
		{"1.2.3", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		dep := MustNewDependency(Dependency{
			Name: "dep", PackageManager: "test", Version: tc.version,
		})
		if got := dep.VersionIsSHA(); got != tc.isSHA {
			t.Errorf("VersionIsSHA(%q) = %v, want %v", tc.version, got, tc.isSHA)
		}
		if got := dep.VersionIsFullSHA(); got != tc.isFull {
			t.Errorf("VersionIsFullSHA(%q) = %v, want %v", tc.version, got, tc.isFull)
		}
	}
}

func TestDependencySourceDetails(t *testing.T) {
	agreeing := MustNewDependency(Dependency{
		Name: "dep", PackageManager: "test",
		Requirements: []Requirement{
			{Requirement: "1.0.0", File: "a", Source: &Source{Type: "git", URL: "https://example.com"}},
			{Requirement: "1.0.0", File: "b", Source: &Source{Type: "git", URL: "https://example.com"}},
		},
	})
	src, err := agreeing.SourceDetails()
	if err != nil {
		t.Fatalf("SourceDetails: %v", err)
	}
	if src == nil || src.URL != "https://example.com" {
		t.Errorf("SourceDetails = %+v, want the shared source", src)
	}
	if agreeing.SourceType() != "git" {
		t.Errorf("SourceType = %q, want git", agreeing.SourceType())
	}

	// Requirements that disagree give no single answer.
	disagreeing := MustNewDependency(Dependency{
		Name: "dep", PackageManager: "test",
		Requirements: []Requirement{
			{Requirement: "1.0.0", File: "a", Source: &Source{URL: "https://example.com"}},
			{Requirement: "1.0.0", File: "b", Source: &Source{URL: "https://other.example"}},
		},
	})
	if _, err := disagreeing.SourceDetails(); err == nil {
		t.Error("expected an error when requirements disagree on the source")
	}
	// The caller still needs an answer for a pull request body.
	if disagreeing.SourceType() != "default" {
		t.Errorf("SourceType = %q, want default", disagreeing.SourceType())
	}
}

func TestDependencyCloneIsDeep(t *testing.T) {
	dep := MustNewDependency(Dependency{
		Name: "dep", PackageManager: "test", Version: "1.0.0",
		Requirements: []Requirement{{
			Requirement: "1.0.0", File: "manifest",
			Groups:   []string{"prod"},
			Source:   &Source{Type: "git"},
			Metadata: map[string]string{"key": "value"},
		}},
		Metadata: map[string]any{"key": "value"},
	})

	clone := dep.Clone()
	clone.Requirements[0].Requirement = "2.0.0"
	clone.Requirements[0].Groups[0] = "dev"
	clone.Requirements[0].Source.Type = "registry"
	clone.Requirements[0].Metadata["key"] = "other"
	clone.Metadata["key"] = "other"

	if dep.Requirements[0].Requirement != "1.0.0" {
		t.Error("the requirement was shared with the clone")
	}
	if dep.Requirements[0].Groups[0] != "prod" {
		t.Error("the groups slice was shared with the clone")
	}
	if dep.Requirements[0].Source.Type != "git" {
		t.Error("the source was shared with the clone")
	}
	if dep.Requirements[0].Metadata["key"] != "value" {
		t.Error("the requirement metadata was shared with the clone")
	}
	if dep.Metadata["key"] != "value" {
		t.Error("the dependency metadata was shared with the clone")
	}
}
