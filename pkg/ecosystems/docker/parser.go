package docker

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// The image-reference grammar, ported from
// https://github.com/docker/distribution/blob/master/reference/regexp.go via
// Ruby's SharedFileParser. Everything but the FROM keyword is case-sensitive,
// as it is in Ruby.
const (
	domainComponent = `(?:[[:alnum:]]|[[:alnum:]][[:alnum:]-]*[[:alnum:]])`
	domain          = `(?:` + domainComponent + `(?:\.` + domainComponent + `)+)`
	registryGroup   = `(?P<registry>` + domain + `(?::\d+)?)`
	nameComponent   = `(?:[a-z\d]+(?:(?:[._]|__|[-]*)[a-z\d]+)*)`
	imageGroup      = `(?P<image>` + nameComponent + `(?:/` + nameComponent + `)*)`
	tagGroup        = `:(?P<tag>[\w][\w.-]{0,127})`
	tagNoPrefix     = `(?P<tag>[\w][\w.-]{0,127})`
	digestGroup     = `(?P<digest>[0-9a-f]{64})`
	stageNameGroup  = `\s+AS\s+(?P<name>[\w-]+)`
	platformGroup   = `--platform\=(?P<platform>\S+)`
)

var (
	// yamlNameRe matches YAML files, excluding dotfiles.
	yamlNameRe = regexp.MustCompile(`(?i)^[^.].*\.ya?ml$`)

	// fromLineRe matches a Dockerfile FROM instruction.
	fromLineRe = regexp.MustCompile(
		`^(?i:FROM)\s+(?:` + platformGroup + `\s+)?(?:` + registryGroup + `/)?` +
			imageGroup + `(?:` + tagGroup + `)?(?:@sha256:` + digestGroup + `)?(?:` + stageNameGroup + `)?`)

	// imageSpecRe matches a bare image reference, as found in a YAML manifest.
	imageSpecRe = regexp.MustCompile(
		`^(?:` + registryGroup + `/)?` + imageGroup +
			`(?:` + tagGroup + `)?(?:@sha256:` + digestGroup + `)?(?:` + stageNameGroup + `)?`)

	// tagWithDigestRe matches a Helm chart's tag value.
	tagWithDigestRe = regexp.MustCompile(`^` + tagNoPrefix + `(?:@sha256:` + digestGroup + `)?`)

	// yamlDocSplitRe splits a multi-document YAML file.
	yamlDocSplitRe = regexp.MustCompile(`(?m)^---$`)
)

// imageRef is one parsed image reference.
type imageRef struct {
	registry string
	image    string
	tag      string
	digest   string
	platform string
}

// version is the tag, or the digest when the image is pinned by digest alone.
func (r imageRef) version() string {
	if r.tag != "" {
		return r.tag
	}
	return r.digest
}

// Parser reads image references out of Dockerfiles and Kubernetes or Helm YAML.
type Parser struct {
	cfg dependabot.ParserConfig
}

var _ dependabot.FileParser = (*Parser)(nil)

// NewParser builds a Docker parser.
func NewParser(cfg dependabot.ParserConfig) (*Parser, error) {
	if len(cfg.Files) == 0 {
		return nil, &dependabot.FileNotFoundError{FilePath: "Dockerfile", Detail: "no Dockerfile"}
	}
	return &Parser{cfg: cfg}, nil
}

// Parse returns every image the fetched files reference.
func (p *Parser) Parse(_ context.Context) ([]*dependabot.Dependency, error) {
	set := newDependencySet()

	for _, file := range p.cfg.Files {
		if isYAMLFile(file) {
			refs, err := imagesFromYAML(file)
			if err != nil {
				// A manifest we cannot read is skipped rather than failing the
				// job: a repo often holds YAML that is not a k8s resource.
				continue
			}
			for _, ref := range refs {
				set.add(buildDependency(file, ref))
			}
			continue
		}

		for _, ref := range imagesFromDockerfile(file) {
			set.add(buildDependency(file, ref))
		}
	}

	return set.dependencies(), nil
}

func isYAMLFile(file *dependabot.DependencyFile) bool {
	return yamlNameRe.MatchString(file.Name)
}

func imagesFromDockerfile(file *dependabot.DependencyFile) []imageRef {
	var refs []imageRef
	for _, line := range strings.Split(file.ContentString(), "\n") {
		ref, ok := matchImageRef(fromLineRe, strings.TrimSuffix(line, "\r"))
		if !ok || ref.version() == "" {
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

// matchImageRef runs one of the image-reference patterns and reads its groups.
func matchImageRef(re *regexp.Regexp, s string) (imageRef, bool) {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return imageRef{}, false
	}

	var ref imageRef
	for i, name := range re.SubexpNames() {
		switch name {
		case "registry":
			ref.registry = m[i]
		case "image":
			ref.image = m[i]
		case "tag":
			ref.tag = m[i]
		case "digest":
			ref.digest = m[i]
		case "platform":
			ref.platform = m[i]
		}
	}
	// docker.io is the implicit default, so recording it would make the
	// requirement look like a private registry pin.
	if ref.registry == "docker.io" {
		ref.registry = ""
	}
	if ref.image == "" {
		return imageRef{}, false
	}
	return ref, true
}

// imagesFromYAML walks every document in a YAML file for "image" keys, which
// is how both Kubernetes manifests and Helm values files name images.
func imagesFromYAML(file *dependabot.DependencyFile) ([]imageRef, error) {
	var refs []imageRef
	seen := map[string]bool{}

	for _, doc := range yamlDocSplitRe.Split(file.ContentString(), -1) {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var parsed any
		if err := yaml.Unmarshal([]byte(doc), &parsed); err != nil {
			return nil, &dependabot.FileNotParseableError{FilePath: file.Path(), Detail: err.Error()}
		}

		for _, image := range deepFetchImages(parsed) {
			if seen[image] {
				continue
			}
			seen[image] = true

			ref, ok := matchImageRef(imageSpecRe, image)
			if !ok || ref.version() == "" {
				continue
			}
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// deepFetchImages collects every "image" value in a decoded YAML document,
// including the Helm form where "image" is a map of repository and tag.
func deepFetchImages(node any) []string {
	switch value := node.(type) {
	case map[string]any:
		var images []string
		switch image := value["image"].(type) {
		case string:
			if image != "" {
				images = append(images, image)
			}
		case map[string]any:
			images = append(images, parseHelmImage(image)...)
		}
		// Iterate the map's values in a stable order so the parsed dependency
		// list does not change between runs.
		for _, key := range sortedKeys(value) {
			images = append(images, deepFetchImages(value[key])...)
		}
		return images
	case []any:
		var images []string
		for _, item := range value {
			images = append(images, deepFetchImages(item)...)
		}
		return images
	default:
		return nil
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort: these maps are small and this avoids pulling in
	// sort just for key ordering.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// parseHelmImage turns a Helm image map into an image reference string.
func parseHelmImage(image map[string]any) []string {
	tagValue, ok := image["tag"]
	if !ok {
		tagValue = image["version"]
	}
	tag := scalarString(tagValue)
	if tag == "" {
		return nil
	}
	// A template expression cannot be resolved without rendering the chart.
	if strings.Contains(tag, "{{") {
		return nil
	}

	repo, ok := image["repository"].(string)
	if !ok || repo == "" {
		return nil
	}

	m := tagWithDigestRe.FindStringSubmatch(tag)
	if m == nil {
		return nil
	}
	tagPart := m[tagWithDigestRe.SubexpIndex("tag")]
	digest := m[tagWithDigestRe.SubexpIndex("digest")]
	if tagPart == "" {
		return []string{repo}
	}

	out := repo + ":" + tagPart
	if registry := scalarString(image["registry"]); registry != "" {
		out = registry + "/" + out
	}
	if digest != "" {
		out += "@sha256:" + digest
	}
	return []string{out}
}

// scalarString renders a YAML scalar as a string, since a chart may write a
// numeric tag such as `tag: 1.21`.
func scalarString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

func buildDependency(file *dependabot.DependencyFile, ref imageRef) *dependabot.Dependency {
	return dependabot.MustNewDependency(dependabot.Dependency{
		Name:           ref.image,
		Version:        ref.version(),
		PackageManager: EcosystemName,
		Requirements: []dependabot.Requirement{{
			File:   file.Name,
			Groups: []string{},
			Source: &dependabot.Source{
				Registry: ref.registry,
				Tag:      ref.tag,
				Digest:   ref.digest,
				Platform: ref.platform,
			},
		}},
	})
}

// dependencySet merges dependencies parsed from different files, the way
// Ruby's FileParsers::Base::DependencySet does: one entry per name and
// version, with the requirements unioned.
type dependencySet struct {
	order []string
	byKey map[string]*dependabot.Dependency
}

func newDependencySet() *dependencySet {
	return &dependencySet{byKey: map[string]*dependabot.Dependency{}}
}

func (s *dependencySet) add(dep *dependabot.Dependency) {
	key := strings.ToLower(dep.Name) + "\x00" + dep.Version

	existing, ok := s.byKey[key]
	if !ok {
		s.byKey[key] = dep
		s.order = append(s.order, key)
		return
	}

	for _, req := range dep.Requirements {
		if !containsRequirement(existing.Requirements, req) {
			existing.Requirements = append(existing.Requirements, req)
		}
	}
}

func (s *dependencySet) dependencies() []*dependabot.Dependency {
	out := make([]*dependabot.Dependency, 0, len(s.order))
	for _, key := range s.order {
		out = append(out, s.byKey[key])
	}
	return out
}

func containsRequirement(reqs []dependabot.Requirement, target dependabot.Requirement) bool {
	for _, req := range reqs {
		if req.Equal(target) {
			return true
		}
	}
	return false
}
