package docker

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// Updater rewrites image references in Dockerfiles and YAML manifests.
//
// It edits the text rather than re-serialising, exactly as Ruby does: a YAML
// round trip would reflow comments, quoting and key order across the whole
// file, turning a one-line version bump into an unreviewable diff.
type Updater struct {
	cfg dependabot.UpdaterConfig
}

var _ dependabot.FileUpdater = (*Updater)(nil)

// NewUpdater builds a Docker file updater.
func NewUpdater(cfg dependabot.UpdaterConfig) (*Updater, error) {
	if len(cfg.Files) == 0 {
		return nil, &dependabot.FileNotFoundError{FilePath: "Dockerfile", Detail: "no files to update"}
	}
	if len(cfg.Dependencies) == 0 {
		return nil, fmt.Errorf("no dependencies to update")
	}
	return &Updater{cfg: cfg}, nil
}

// dependency is the single image being updated. A Docker update never covers
// more than one, as in Ruby.
func (u *Updater) dependency() *dependabot.Dependency { return u.cfg.Dependencies[0] }

// UpdatedDependencyFiles returns the files whose contents changed.
func (u *Updater) UpdatedDependencyFiles(_ context.Context) ([]*dependabot.DependencyFile, error) {
	var updated []*dependabot.DependencyFile

	for _, file := range u.cfg.Files {
		if !u.requirementChanged(file) {
			continue
		}

		var content string
		var err error
		if isYAMLFile(file) {
			content, err = u.updatedYAMLContent(file)
		} else {
			content, err = u.updatedDockerfileContent(file)
		}
		if err != nil {
			return nil, err
		}
		if content == file.ContentString() {
			continue
		}
		updated = append(updated, file.WithContent(content))
	}

	if len(updated) == 0 {
		return nil, fmt.Errorf("no files changed")
	}
	return updated, nil
}

// requirementChanged reports whether this file's requirement was rewritten.
func (u *Updater) requirementChanged(file *dependabot.DependencyFile) bool {
	dep := u.dependency()
	for _, req := range dep.Requirements {
		if req.File != file.Name {
			continue
		}
		if !containsRequirement(dep.PreviousRequirements, req) {
			return true
		}
	}
	return false
}

func (u *Updater) requirementsFor(file *dependabot.DependencyFile) []dependabot.Requirement {
	return requirementsForFile(u.dependency().Requirements, file.Name)
}

func (u *Updater) previousRequirementsFor(file *dependabot.DependencyFile) []dependabot.Requirement {
	return requirementsForFile(u.dependency().PreviousRequirements, file.Name)
}

func requirementsForFile(reqs []dependabot.Requirement, name string) []dependabot.Requirement {
	var out []dependabot.Requirement
	for _, req := range reqs {
		if req.File == name {
			out = append(out, req)
		}
	}
	return out
}

// ------------------------------------------------------------- Dockerfiles --

func (u *Updater) updatedDockerfileContent(file *dependabot.DependencyFile) (string, error) {
	oldReqs := u.previousRequirementsFor(file)
	newReqs := u.requirementsFor(file)

	content := file.ContentString()
	for i, oldReq := range oldReqs {
		if i >= len(newReqs) {
			break
		}
		content = u.updateDigestAndTag(content, oldReq.Source, newReqs[i].Source)
	}

	if content == file.ContentString() {
		return "", fmt.Errorf("expected %s to change, but it did not", file.Path())
	}
	return content, nil
}

// updateDigestAndTag rewrites one FROM instruction in place, leaving the rest
// of the line — platform flags, stage names, comments — untouched.
func (u *Updater) updateDigestAndTag(content string, oldSource, newSource *dependabot.Source) string {
	oldTag := oldSource.Get("tag")
	newTag := newSource.Get("tag")
	oldDigest := strings.TrimPrefix(oldSource.Get("digest"), "sha256:")
	newDigest := strings.TrimPrefix(newSource.Get("digest"), "sha256:")

	declaration := u.dependency().Name
	if registry := oldSource.Get("registry"); registry != "" {
		declaration = registry + "/" + declaration
	}
	if oldTag != "" {
		declaration += ":" + oldTag
	}
	if oldDigest != "" {
		declaration += "@sha256:" + oldDigest
	}

	// Only the FROM keyword and its platform flag are case-insensitive; the
	// image reference itself is matched exactly.
	re := regexp.MustCompile(
		`(?m)^((?i:FROM(?:\s+--platform\=\S+)?)\s+(?:docker\.io/)?` +
			regexp.QuoteMeta(declaration) + `)(\s|$)`)

	return re.ReplaceAllStringFunc(content, func(match string) string {
		groups := re.FindStringSubmatch(match)
		found, trailing := groups[1], groups[2]

		if oldDigest != "" {
			found = strings.ReplaceAll(found, "@sha256:"+oldDigest, "@sha256:"+newDigest)
			found = strings.ReplaceAll(found, "@"+oldDigest, "@"+newDigest)
		}
		if oldTag != "" {
			found = strings.ReplaceAll(found, ":"+oldTag, ":"+newTag)
		}
		// Pinning a digest onto a previously tag-only image.
		if oldDigest == "" && newDigest != "" {
			found += "@sha256:" + newDigest
		}
		return found + trailing
	})
}

// -------------------------------------------------------------------- YAML --

func (u *Updater) updatedYAMLContent(file *dependabot.DependencyFile) (string, error) {
	content := file.ContentString()
	if helmValuesRe.MatchString(file.Name) {
		content = u.updateHelmTags(file, content)
	}
	content = u.updateYAMLImages(file, content)

	if content == file.ContentString() {
		return "", fmt.Errorf("expected %s to change, but it did not", file.Path())
	}
	return content, nil
}

// updateHelmTags rewrites the `tag:` or `version:` key of a Helm image block,
// which names the tag separately from the repository.
func (u *Updater) updateHelmTags(file *dependabot.DependencyFile, content string) string {
	oldTags := uniqueStrings(helmTagStrings(u.previousRequirementsFor(file)))
	if len(oldTags) == 0 {
		return content
	}
	replacement := ""
	if newTags := helmTagStrings(u.requirementsFor(file)); len(newTags) > 0 {
		replacement = newTags[0]
	}

	for _, oldTag := range oldTags {
		re := regexp.MustCompile(
			`(?m)^(\s*(?:-\s)?(?:tag|version):\s+["']?` + regexp.QuoteMeta(oldTag) + `["']?)(\s|$)`)
		content = re.ReplaceAllStringFunc(content, func(match string) string {
			groups := re.FindStringSubmatch(match)
			return strings.ReplaceAll(groups[1], oldTag, replacement) + groups[2]
		})
	}
	return content
}

// updateYAMLImages rewrites `image:` values, the form Kubernetes manifests use.
func (u *Updater) updateYAMLImages(file *dependabot.DependencyFile, content string) string {
	oldImages := uniqueStrings(u.imageStrings(u.previousRequirementsFor(file)))
	if len(oldImages) == 0 {
		return content
	}
	replacement := ""
	if newImages := u.imageStrings(u.requirementsFor(file)); len(newImages) > 0 {
		replacement = newImages[0]
	}

	for _, oldImage := range oldImages {
		re := regexp.MustCompile(
			`(?m)^(\s*(?:-\s)?image:\s+(?:docker\.io/)?` + regexp.QuoteMeta(oldImage) + `)(\s|$)`)
		content = re.ReplaceAllStringFunc(content, func(match string) string {
			groups := re.FindStringSubmatch(match)
			return strings.ReplaceAll(groups[1], oldImage, replacement) + groups[2]
		})
	}
	return content
}

// helmTagStrings renders each requirement as the value of a Helm `tag:` key.
func helmTagStrings(reqs []dependabot.Requirement) []string {
	out := make([]string, 0, len(reqs))
	for _, req := range reqs {
		value := req.Source.Get("tag")
		if digest := req.Source.Get("digest"); digest != "" {
			value += "@sha256:" + digest
		}
		out = append(out, value)
	}
	return out
}

// imageStrings renders each requirement as a full image reference.
func (u *Updater) imageStrings(reqs []dependabot.Requirement) []string {
	out := make([]string, 0, len(reqs))
	for _, req := range reqs {
		image := u.dependency().Name
		if registry := req.Source.Get("registry"); registry != "" {
			image = registry + "/" + image
		}
		if tag := req.Source.Get("tag"); tag != "" {
			image += ":" + tag
		}
		if digest := req.Source.Get("digest"); digest != "" {
			image += "@sha256:" + digest
		}
		out = append(out, image)
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := values[:0:0]
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
