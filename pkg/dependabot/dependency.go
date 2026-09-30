package dependabot

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// Source describes where a requirement's dependency comes from.
//
// Ruby carries this as an untyped hash whose keys vary by ecosystem. Modelling
// it as a struct costs a field per ecosystem-specific key but makes the
// well-known ones (type, url, ref, registry, tag, digest) checkable; Extra is
// the escape hatch for the rest. This follows the direction dependabot-core is
// already moving in with its typed record work.
type Source struct {
	Type     string
	URL      string
	Ref      string
	Branch   string
	Registry string
	Tag      string
	Digest   string
	Platform string
	Extra    map[string]string
}

// Get reads a source field by its Ruby key name, falling back to Extra.
func (s *Source) Get(key string) string {
	if s == nil {
		return ""
	}
	switch key {
	case "type":
		return s.Type
	case "url":
		return s.URL
	case "ref":
		return s.Ref
	case "branch":
		return s.Branch
	case "registry":
		return s.Registry
	case "tag":
		return s.Tag
	case "digest":
		return s.Digest
	case "platform":
		return s.Platform
	default:
		return s.Extra[key]
	}
}

// Clone returns a deep copy, so an update checker can amend a source without
// mutating the requirement it was read from.
func (s *Source) Clone() *Source {
	if s == nil {
		return nil
	}
	c := *s
	if s.Extra != nil {
		c.Extra = make(map[string]string, len(s.Extra))
		for k, v := range s.Extra {
			c.Extra[k] = v
		}
	}
	return &c
}

// Requirement is a single requirement entry on a Dependency: the constraint as
// written, the file it was written in, the groups it belongs to, and where the
// dependency is fetched from.
type Requirement struct {
	// Requirement is the constraint string, e.g. ">= 1.0, < 2.0". Empty when
	// the dependency is pinned by a lockfile rather than a manifest.
	Requirement string
	// Unfixable marks a requirement for which no valid update can be
	// generated. Ruby uses the :unfixable symbol in the Requirement field.
	Unfixable bool
	File      string
	Groups    []string
	Source    *Source
	Metadata  map[string]string
}

// Equal reports whether two requirement entries are identical. Used to work out
// whether an update actually changed anything.
func (r Requirement) Equal(other Requirement) bool {
	return r.Requirement == other.Requirement &&
		r.Unfixable == other.Unfixable &&
		r.File == other.File &&
		reflect.DeepEqual(r.Groups, other.Groups) &&
		reflect.DeepEqual(r.Source, other.Source) &&
		reflect.DeepEqual(r.Metadata, other.Metadata)
}

// Clone returns a deep copy of the requirement.
func (r Requirement) Clone() Requirement {
	c := r
	if r.Groups != nil {
		c.Groups = append([]string(nil), r.Groups...)
	}
	c.Source = r.Source.Clone()
	if r.Metadata != nil {
		c.Metadata = make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			c.Metadata[k] = v
		}
	}
	return c
}

// Dependency is a single dependency of a project, as parsed from its manifests
// and then amended by an update check.
type Dependency struct {
	Name string
	// Version is the currently resolved version, empty when the project has no
	// lockfile.
	Version              string
	Requirements         []Requirement
	PackageManager       string
	PreviousVersion      string
	PreviousRequirements []Requirement
	Directory            string
	// SubdependencyMetadata carries per-source detail for dependencies that
	// appear only in a lockfile.
	SubdependencyMetadata []SubdependencyMetadata
	Removed               bool
	Metadata              map[string]any
}

// SubdependencyMetadata describes a transitive dependency's origin.
type SubdependencyMetadata struct {
	Production *bool
	Source     *Source
}

var (
	shaRegexp       = regexp.MustCompile(`^[0-9a-f]{40}`)
	shortSHARegexp  = regexp.MustCompile(`^[0-9a-f]{6,}$`)
	fortyHexRegexp  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	blankRequiredRe = regexp.MustCompile(`^\s*$`)
)

// NewDependency validates and builds a Dependency, mirroring the checks Ruby's
// constructor performs.
func NewDependency(d Dependency) (*Dependency, error) {
	if d.Name == "" {
		return nil, fmt.Errorf("dependency name must not be blank")
	}
	if d.PackageManager == "" {
		return nil, fmt.Errorf("dependency %s must specify a package manager", d.Name)
	}
	for _, reqs := range [][]Requirement{d.Requirements, d.PreviousRequirements} {
		for _, r := range reqs {
			if r.Requirement != "" && blankRequiredRe.MatchString(r.Requirement) {
				return nil, fmt.Errorf("dependency %s: blank strings must not be provided as requirements", d.Name)
			}
			if r.File == "" {
				return nil, fmt.Errorf("dependency %s: each requirement must name a file", d.Name)
			}
		}
	}
	// A top-level dependency's subdependency metadata is meaningless.
	if len(d.Requirements) > 0 {
		d.SubdependencyMetadata = nil
	}
	return &d, nil
}

// MustNewDependency is NewDependency for known-good arguments.
func MustNewDependency(d Dependency) *Dependency {
	dep, err := NewDependency(d)
	if err != nil {
		panic(err)
	}
	return dep
}

// TopLevel reports whether the dependency is declared in a manifest, as opposed
// to appearing only in a lockfile.
func (d *Dependency) TopLevel() bool { return len(d.Requirements) > 0 }

// AppearsInLockfile reports whether the dependency is pinned by a lockfile.
func (d *Dependency) AppearsInLockfile() bool {
	return d.PreviousVersion != "" || (d.Version != "" && d.PreviousRequirements == nil)
}

// Production reports whether the dependency is used in production, using the
// production check the ecosystem registered.
func (d *Dependency) Production() bool {
	if !d.TopLevel() {
		return d.subdependencyProduction()
	}
	var groups []string
	for _, r := range d.Requirements {
		groups = append(groups, r.Groups...)
	}
	check := productionCheckFor(d.PackageManager)
	if check == nil {
		// Ruby raises here. Defaulting to production is the safer read: it
		// keeps a dependency visible rather than silently dropping it from
		// production-only update runs.
		return true
	}
	return check(groups)
}

func (d *Dependency) subdependencyProduction() bool {
	if len(d.SubdependencyMetadata) == 0 {
		return true
	}
	for _, m := range d.SubdependencyMetadata {
		if m.Production == nil || *m.Production {
			return true
		}
	}
	return false
}

// DisplayName is the name to show in a pull request, which some ecosystems
// rewrite (Maven joins group and artifact, for instance).
func (d *Dependency) DisplayName() string {
	if builder := displayNameBuilderFor(d.PackageManager); builder != nil {
		return builder(d.Name)
	}
	return d.Name
}

// HumanizedVersion renders the new version for a pull request title.
func (d *Dependency) HumanizedVersion() string {
	if d.Removed {
		return "removed"
	}
	if d.Version == "" {
		return ""
	}
	if shaRegexp.MatchString(d.Version) {
		if d.RefChanged() && d.NewRef() != "" {
			return d.NewRef()
		}
		return "`" + d.Version[:7] + "`"
	}
	if d.Version == d.PreviousVersion && d.PackageManager == "docker" {
		if short := shortDigest(digestFromRequirements(d.Requirements)); short != "" {
			return "`" + short + "`"
		}
	}
	return d.Version
}

// HumanizedPreviousVersion renders the old version for a pull request title.
func (d *Dependency) HumanizedPreviousVersion() string {
	if d.PreviousVersion == "" {
		// Without a previous version we may still be able to name one, if a ref
		// was provided and changed — the previous ref was in effect the version.
		if d.RefChanged() {
			return d.PreviousRef()
		}
		return ""
	}
	if shaRegexp.MatchString(d.PreviousVersion) {
		if d.RefChanged() && d.PreviousRef() != "" {
			return d.PreviousRef()
		}
		return "`" + d.PreviousVersion[:7] + "`"
	}
	if d.Version == d.PreviousVersion && d.PackageManager == "docker" {
		if short := shortDigest(digestFromRequirements(d.PreviousRequirements)); short != "" {
			return "`" + short + "`"
		}
	}
	return d.PreviousVersion
}

// PreviousRef returns the git ref the dependency was pinned to, when all
// previous requirements agree on one.
func (d *Dependency) PreviousRef() string {
	if d.PreviousRequirements == nil {
		return ""
	}
	return uniqueRef(d.PreviousRequirements)
}

// NewRef returns the git ref the dependency is now pinned to, when all
// requirements agree on one.
func (d *Dependency) NewRef() string { return uniqueRef(d.Requirements) }

// RefChanged reports whether the git ref moved.
func (d *Dependency) RefChanged() bool { return d.PreviousRef() != d.NewRef() }

// RequirementsChanged reports whether the update altered any requirement.
func (d *Dependency) RequirementsChanged() bool {
	return len(d.ChangedRequirements()) > 0
}

// ChangedRequirements returns the requirements that differ from the previous
// ones.
func (d *Dependency) ChangedRequirements() []Requirement {
	var changed []Requirement
	for _, r := range d.Requirements {
		found := false
		for _, prev := range d.PreviousRequirements {
			if r.Equal(prev) {
				found = true
				break
			}
		}
		if !found {
			changed = append(changed, r)
		}
	}
	return changed
}

// VersionIsSHA reports whether the dependency is pinned to a commit rather than
// a released version.
func (d *Dependency) VersionIsSHA() bool {
	return d.Version != "" && shortSHARegexp.MatchString(d.Version)
}

// VersionIsFullSHA reports whether the version is a full 40-character SHA.
func (d *Dependency) VersionIsFullSHA() bool {
	return fortyHexRegexp.MatchString(d.Version)
}

// SourceDetails returns the single source shared by the dependency's
// requirements, erroring when they disagree.
func (d *Dependency) SourceDetails() (*Source, error) {
	var sources []*Source
	if d.TopLevel() {
		for _, r := range d.Requirements {
			if r.Source != nil {
				sources = append(sources, r.Source)
			}
		}
	} else {
		for _, m := range d.SubdependencyMetadata {
			if m.Source != nil {
				sources = append(sources, m.Source)
			}
		}
	}
	var unique []*Source
	for _, s := range sources {
		dup := false
		for _, u := range unique {
			if reflect.DeepEqual(s, u) {
				dup = true
				break
			}
		}
		if !dup {
			unique = append(unique, s)
		}
	}
	if len(unique) > 1 {
		return nil, fmt.Errorf("dependency %s has multiple sources", d.Name)
	}
	if len(unique) == 0 {
		return nil, nil
	}
	return unique[0], nil
}

// SourceType names the kind of source backing the dependency, "default" when it
// comes from the ecosystem's usual registry.
func (d *Dependency) SourceType() string {
	src, err := d.SourceDetails()
	if err != nil || src == nil {
		return "default"
	}
	if src.Type == "" {
		return "default"
	}
	return src.Type
}

// Clone returns a deep copy of the dependency.
func (d *Dependency) Clone() *Dependency {
	c := *d
	c.Requirements = cloneRequirements(d.Requirements)
	c.PreviousRequirements = cloneRequirements(d.PreviousRequirements)
	if d.Metadata != nil {
		c.Metadata = make(map[string]any, len(d.Metadata))
		for k, v := range d.Metadata {
			c.Metadata[k] = v
		}
	}
	return &c
}

func cloneRequirements(reqs []Requirement) []Requirement {
	if reqs == nil {
		return nil
	}
	out := make([]Requirement, len(reqs))
	for i, r := range reqs {
		out[i] = r.Clone()
	}
	return out
}

func uniqueRef(reqs []Requirement) string {
	var ref string
	for _, r := range reqs {
		if r.Source == nil || r.Source.Ref == "" {
			continue
		}
		if ref != "" && ref != r.Source.Ref {
			return ""
		}
		ref = r.Source.Ref
	}
	return ref
}

func digestFromRequirements(reqs []Requirement) string {
	for _, r := range reqs {
		if r.Source != nil && r.Source.Digest != "" {
			return r.Source.Digest
		}
	}
	return ""
}

func shortDigest(digest string) string {
	if digest == "" {
		return ""
	}
	if i := strings.LastIndex(digest, ":"); i >= 0 {
		digest = digest[i+1:]
	}
	if len(digest) > 7 {
		digest = digest[:7]
	}
	return digest
}
