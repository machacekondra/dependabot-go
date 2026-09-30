package docker

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// Checker decides whether a container image reference should move to a newer
// tag or digest.
//
// Ruby's version of this class also implements release cooldown, per-platform
// created-timestamp validation and multi-arch no-op suppression, all of them
// behind experiment flags or job settings this port does not carry. See
// PORTING.md; the tag selection pipeline itself is ported in full.
type Checker struct {
	*dependabot.BaseChecker
	registry *Registry

	tags       []*Tag
	tagsErr    error
	tagsLoaded bool

	digests      map[string]string
	latestPerTag map[string]*Tag

	latestTagResolved bool
	latestTagValue    *Tag
}

var (
	_ dependabot.UpdateChecker             = (*Checker)(nil)
	_ dependabot.VersionUpToDateOverrider  = (*Checker)(nil)
	_ dependabot.VersionCanUpdateOverrider = (*Checker)(nil)
)

// NewChecker builds an update checker for one image reference.
func NewChecker(eco dependabot.Ecosystem, cfg dependabot.CheckerConfig) *Checker {
	c := &Checker{
		BaseChecker:  dependabot.NewBaseChecker(eco, cfg),
		digests:      map[string]string{},
		latestPerTag: map[string]*Tag{},
	}
	c.BaseChecker.Finder = c
	c.registry = NewRegistry(c.registryHostname(), cfg.Credentials)
	return c
}

// registryHostname is the registry the image is pulled from.
func (c *Checker) registryHostname() string {
	for _, req := range c.Dependency().Requirements {
		if registry := req.Source.Get("registry"); registry != "" {
			return registry
		}
	}
	return DefaultRegistry
}

func (c *Checker) usingDockerHub() bool { return c.registry.Host == DefaultRegistry }

// repoName is the path the registry knows the image by. Docker Hub keeps
// official images under the implicit "library" namespace.
func (c *Checker) repoName() string {
	name := c.Dependency().Name
	if c.usingDockerHub() && !strings.Contains(name, "/") {
		return "library/" + name
	}
	return name
}

// versionTag is the tag the project currently pins.
func (c *Checker) versionTag() *Tag { return NewTag(c.Dependency().Version) }

// ---------------------------------------------------------------- registry --

func (c *Checker) tagsFromRegistry(ctx context.Context) ([]*Tag, error) {
	if c.tagsLoaded {
		return c.tags, c.tagsErr
	}
	c.tagsLoaded = true

	names, err := c.registry.Tags(ctx, c.repoName())
	if err != nil {
		c.tagsErr = err
		return nil, err
	}
	for _, name := range names {
		c.tags = append(c.tags, NewTag(name))
	}
	return c.tags, nil
}

// digestOf resolves a tag to the digest the registry currently serves for it.
func (c *Checker) digestOf(ctx context.Context, tagName string) string {
	if digest, ok := c.digests[tagName]; ok {
		return digest
	}
	// A digest we cannot fetch is cached as empty: callers treat an unknown
	// digest as "no opinion" rather than failing the whole check.
	digest, err := c.registry.ManifestDigest(ctx, c.repoName(), tagName)
	if err != nil {
		digest = ""
	}
	c.digests[tagName] = digest
	return digest
}

// latestDigest is the digest the "latest" tag points at, when the image has one.
func (c *Checker) latestDigest(ctx context.Context) string {
	tags, err := c.tagsFromRegistry(ctx)
	if err != nil {
		return ""
	}
	for _, tag := range tags {
		if tag.Name == "latest" {
			return c.digestOf(ctx, "latest")
		}
	}
	return ""
}

// latestTag is the highest canonical tag that "latest" resolves to, which is
// what tells us whether a higher-numbered tag is really released yet.
func (c *Checker) latestTag(ctx context.Context) *Tag {
	if c.latestTagResolved {
		return c.latestTagValue
	}
	c.latestTagResolved = true

	digest := c.latestDigest(ctx)
	if digest == "" {
		return nil
	}
	tags, err := c.tagsFromRegistry(ctx)
	if err != nil {
		return nil
	}

	var canonical []*Tag
	for _, tag := range tags {
		if tag.Canonical() {
			canonical = append(canonical, tag)
		}
	}
	sort.SliceStable(canonical, func(i, j int) bool {
		return TagVersion(canonical[i]).Compare(TagVersion(canonical[j])) < 0
	})

	for i := len(canonical) - 1; i >= 0; i-- {
		if c.digestOf(ctx, canonical[i].Name) == digest {
			c.latestTagValue = canonical[i]
			break
		}
	}
	return c.latestTagValue
}

// ------------------------------------------------------------- tag pipeline --

// latestTagFrom is the tag the given tag should be updated to, cached because
// the same tag is resolved once per requirement and once per digest check.
func (c *Checker) latestTagFrom(ctx context.Context, version string) *Tag {
	if tag, ok := c.latestPerTag[version]; ok {
		return tag
	}
	tag := c.fetchLatestTag(ctx, NewTag(version))
	c.latestPerTag[version] = tag
	return tag
}

// fetchLatestTag always returns a tag, falling back to the current one, since
// the caller goes on to check that tag's digest.
func (c *Checker) fetchLatestTag(ctx context.Context, versionTag *Tag) *Tag {
	if versionTag.Digest() {
		if digest := c.latestDigest(ctx); digest != "" {
			return NewTag(digest)
		}
	}
	if !versionTag.Comparable() {
		return versionTag
	}

	// Downgrades are pruned before the prerelease check, which needs a
	// registry round trip per tag and so is the expensive step.
	candidates := c.comparableTagsFromRegistry(ctx, versionTag)
	candidates = removeVersionDowngrades(candidates, versionTag)
	candidates = removeMorePreciseTags(candidates, versionTag)
	candidates = c.removePrereleases(ctx, candidates, versionTag)
	candidates = c.filterIgnored(candidates)
	candidates = sortTags(candidates, versionTag)

	return c.selectBestCandidate(ctx, candidates, versionTag)
}

// comparableTagsFromRegistry narrows the registry's tags to those that are
// shaped like the current tag and carry the same variant components.
func (c *Checker) comparableTagsFromRegistry(ctx context.Context, original *Tag) []*Tag {
	tags, err := c.tagsFromRegistry(ctx)
	if err != nil {
		return nil
	}

	common := identifyCommonComponents(tags)
	originalComponents := extractTagComponents(original.Name, common)

	var out []*Tag
	for _, tag := range tags {
		if !tag.ComparableTo(original) {
			continue
		}
		if len(originalComponents) > 0 &&
			!compatibleComponents(extractTagComponents(tag.Name, common), originalComponents) {
			continue
		}
		out = append(out, tag)
	}
	return out
}

// legacyVersionRelatedPatterns identify parts of a tag name that carry version
// information rather than naming a base image or variant. Everything else is
// treated as a variant component that an update has to preserve.
var legacyVersionRelatedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\d+$`),                               // "123", "8"
	regexp.MustCompile(`^\d+\.\d+$`),                          // "1.2"
	regexp.MustCompile(`^v\d+`),                               // "v2", "v10"
	regexp.MustCompile(`(?i)^(?:\d+[a-z]|[a-z]+\d)[a-z\d]*$`), // "rc1", "alpine3", "ltsc2022"
	regexp.MustCompile(`^(?:rc|jre)$`),                        // version-bearing tag words
	regexp.MustCompile(`(?i)^kb\d+$`),                         // Microsoft KB numbers
	regexp.MustCompile(`^g[0-9a-f]{5,}$`),                     // git SHAs
	regexp.MustCompile(`^\d{8,14}$`),                          // timestamps
	regexp.MustCompile(`\d+_\d+`),                             // "12_8"
}

var (
	versionPlaceholderRe = regexp.MustCompile(`\d+\.\d+\.\d+_\d+`)
	componentSplitRe     = regexp.MustCompile(`[-./]`)
)

// identifyCommonComponents lists the variant words that appear across a
// repository's tags, e.g. "alpine", "bookworm", "windowsservercore".
func identifyCommonComponents(tags []*Tag) []string {
	var components []string
	seen := map[string]bool{}

	for _, tag := range tags {
		normalised := versionPlaceholderRe.ReplaceAllString(tag.Name, "VERSION")
		for _, part := range componentSplitRe.Split(normalised, -1) {
			if part == "" || len(part) <= 1 || part == "VERSION" || seen[part] {
				continue
			}
			if versionRelatedPattern(part) {
				continue
			}
			seen[part] = true
			components = append(components, part)
		}
	}
	return components
}

func versionRelatedPattern(part string) bool {
	for _, pattern := range legacyVersionRelatedPatterns {
		if pattern.MatchString(part) {
			return true
		}
	}
	return false
}

// extractTagComponents lists the variant components a tag name contains.
func extractTagComponents(tagName string, common []string) []string {
	var out []string
	for _, component := range common {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(component) + `\b`).MatchString(tagName) {
			out = append(out, component)
		}
	}
	return out
}

// compatibleComponents reports whether two tags carry exactly the same variant
// components, so "1.2-alpine" is never offered as an update for "1.2-slim".
func compatibleComponents(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func removeVersionDowngrades(candidates []*Tag, versionTag *Tag) []*Tag {
	current := TagVersion(versionTag)

	out := candidates[:0:0]
	for _, tag := range candidates {
		if TagVersion(tag).Compare(current) >= 0 {
			out = append(out, tag)
		}
	}
	return out
}

// removeMorePreciseTags keeps a rolling pin rolling: a project on "9.0" has
// opted into the minor-version tag, so "9.0.17" is not an update it wants.
// Only plain semver tags are filtered; dated and build-number formats have
// their own comparability rules.
func removeMorePreciseTags(candidates []*Tag, versionTag *Tag) []*Tag {
	if versionTag.Format() != FormatNormal || versionTag.Precision() > 2 {
		return candidates
	}

	out := candidates[:0:0]
	for _, tag := range candidates {
		if tag.Precision() <= versionTag.Precision() {
			out = append(out, tag)
		}
	}
	return out
}

func (c *Checker) removePrereleases(ctx context.Context, candidates []*Tag, versionTag *Tag) []*Tag {
	if c.prerelease(ctx, versionTag) {
		return candidates
	}

	out := candidates[:0:0]
	for _, tag := range candidates {
		if !c.prerelease(ctx, tag) {
			out = append(out, tag)
		}
	}
	return out
}

// prerelease treats anything above the version "latest" points at as unreleased,
// as well as tags whose names mark them as prereleases.
func (c *Checker) prerelease(ctx context.Context, tag *Tag) bool {
	if tag.LooksLikePrerelease() {
		return true
	}
	latest := c.latestTag(ctx)
	if latest == nil {
		return false
	}
	return TagVersion(tag).Compare(TagVersion(latest)) > 0
}

func (c *Checker) filterIgnored(candidates []*Tag) []*Tag {
	out := candidates[:0:0]
	for _, tag := range candidates {
		if !c.Ignored(TagVersion(tag)) {
			out = append(out, tag)
		}
	}
	return out
}

// sortTags orders candidates lowest first, preferring a tag of the same
// precision as the current one when versions tie.
func sortTags(candidates []*Tag, versionTag *Tag) []*Tag {
	out := append([]*Tag(nil), candidates...)

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]

		if cmp := TagVersion(a).Compare(TagVersion(b)); cmp != 0 {
			return cmp < 0
		}
		if cmp := comparePrecision(a, b, versionTag); cmp != 0 {
			return cmp < 0
		}
		// Dated tags that share a base version tie on everything above, so the
		// raw version string breaks the tie and sorts the newer date higher.
		return a.Version() < b.Version()
	})
	return out
}

func comparePrecision(a, b, versionTag *Tag) int {
	aMatch := a.SamePrecision(versionTag)
	bMatch := b.SamePrecision(versionTag)
	switch {
	case aMatch && !bMatch:
		return 1
	case bMatch && !aMatch:
		return -1
	default:
		return 0
	}
}

// selectBestCandidate picks the highest candidate, preferring an equally
// precise tag that resolves to the same image.
func (c *Checker) selectBestCandidate(ctx context.Context, candidates []*Tag, versionTag *Tag) *Tag {
	if len(candidates) == 0 {
		return versionTag
	}
	samePrecision := removePrecisionChanges(candidates, versionTag)
	return c.selectTagWithPrecision(ctx, candidates[len(candidates)-1], samePrecision, versionTag)
}

func removePrecisionChanges(candidates []*Tag, versionTag *Tag) []*Tag {
	out := candidates[:0:0]
	for _, tag := range candidates {
		if tag.SamePrecision(versionTag) {
			out = append(out, tag)
		}
	}
	return out
}

// selectTagWithPrecision keeps the project's pinning style: if a tag of the
// current precision points at the same image as the more precise candidate,
// the less precise one is the update to make.
func (c *Checker) selectTagWithPrecision(ctx context.Context, candidate *Tag, samePrecision []*Tag, versionTag *Tag) *Tag {
	if candidate.SamePrecision(versionTag) {
		return candidate
	}

	var best *Tag
	for i := len(samePrecision) - 1; i >= 0; i-- {
		if TagVersion(samePrecision[i]).Compare(TagVersion(candidate)) <= 0 {
			best = samePrecision[i]
			break
		}
	}
	if best == nil {
		return candidate
	}

	if c.digestOf(ctx, best.Name) == c.digestOf(ctx, candidate.Name) && best.SameButLessPrecise(candidate) {
		return best
	}
	return candidate
}

// ------------------------------------------------------------ update checks --

// digestRequirements are the requirements that pin a digest as well as a tag.
func (c *Checker) digestRequirements() []dependabot.Requirement {
	var out []dependabot.Requirement
	for _, req := range c.Dependency().Requirements {
		if req.Source.Get("digest") != "" {
			out = append(out, req)
		}
	}
	return out
}

// VersionUpToDate overrides the generic check: an image can be behind on its
// digest even when its tag has not moved.
func (c *Checker) VersionUpToDate(ctx context.Context) (bool, error) {
	if len(c.digestRequirements()) > 0 {
		return c.versionTagUpToDate(ctx) && c.digestUpToDate(ctx), nil
	}
	return c.versionTagUpToDate(ctx), nil
}

// VersionCanUpdate overrides the generic check. Docker has no notion of
// unlocking, so the unlock level is ignored.
func (c *Checker) VersionCanUpdate(ctx context.Context, _ dependabot.UnlockLevel) (bool, error) {
	if len(c.digestRequirements()) > 0 {
		return !c.digestUpToDate(ctx), nil
	}
	upToDate, err := c.VersionUpToDate(ctx)
	return !upToDate, err
}

func (c *Checker) versionTagUpToDate(ctx context.Context) bool {
	version := c.Dependency().Version
	if version == "" {
		return false
	}
	current := c.versionTag()
	if !current.Comparable() {
		return true
	}
	latest := c.latestTagFrom(ctx, version)
	return TagVersion(latest).Compare(TagVersion(current)) <= 0
}

func (c *Checker) digestUpToDate(ctx context.Context) bool {
	for _, req := range c.digestRequirements() {
		if !c.digestRequirementUpToDate(ctx, req) {
			return false
		}
	}
	return true
}

func (c *Checker) digestRequirementUpToDate(ctx context.Context, req dependabot.Requirement) bool {
	sourceDigest := req.Source.Get("digest")
	sourceTag := req.Source.Get("tag")

	var expected string
	if sourceTag != "" {
		expected = c.digestOf(ctx, c.latestTagFrom(ctx, sourceTag).Name)
	} else {
		expected = c.updatedDigest(ctx)
	}

	// A registry that will not tell us the digest gives us no grounds to
	// propose a change.
	if expected == "" {
		return true
	}
	return sourceDigest == expected
}

// updatedDigest is the digest a digest-only pin should move to.
func (c *Checker) updatedDigest(ctx context.Context) string {
	latest := c.latestTagFrom(ctx, c.Dependency().Version)
	if latest.Digest() {
		return c.latestDigest(ctx)
	}
	return c.digestOf(ctx, latest.Name)
}

// ------------------------------------------------------- VersionFinder half --

// LatestVersion is the tag the image should move to.
func (c *Checker) LatestVersion(ctx context.Context) (dependabot.Version, error) {
	if _, err := c.tagsFromRegistry(ctx); err != nil {
		return nil, err
	}
	return TagVersion(c.latestTagFrom(ctx, c.Dependency().Version)), nil
}

// LatestResolvableVersion aliases LatestVersion: resolvability is not a
// concern for a container image.
func (c *Checker) LatestResolvableVersion(ctx context.Context) (dependabot.Version, error) {
	return c.LatestVersion(ctx)
}

// LatestResolvableVersionWithNoUnlock returns the current version, since there
// is nothing to unlock.
func (c *Checker) LatestResolvableVersionWithNoUnlock(_ context.Context) (dependabot.Version, error) {
	return TagVersion(c.versionTag()), nil
}

// LowestSecurityFixVersion has no Docker equivalent: advisories are published
// against the software in the image, not the image tag.
func (c *Checker) LowestSecurityFixVersion(_ context.Context) (dependabot.Version, error) {
	return nil, nil
}

// UpdatedRequirements rewrites each requirement's tag and digest.
func (c *Checker) UpdatedRequirements(ctx context.Context) ([]dependabot.Requirement, error) {
	if _, err := c.tagsFromRegistry(ctx); err != nil {
		return nil, err
	}

	updated := make([]dependabot.Requirement, 0, len(c.Dependency().Requirements))
	for _, req := range c.Dependency().Requirements {
		clone := req.Clone()
		if clone.Source == nil {
			clone.Source = &dependabot.Source{}
		}

		switch {
		case clone.Source.Tag != "":
			newTag := c.latestTagFrom(ctx, clone.Source.Tag).Name
			clone.Source.Tag = newTag
			if clone.Source.Digest != "" {
				clone.Source.Digest = c.digestOf(ctx, newTag)
			}
		case clone.Source.Digest != "":
			clone.Source.Digest = c.digestOf(ctx, "latest")
		}
		updated = append(updated, clone)
	}
	return updated, nil
}
