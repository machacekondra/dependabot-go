// Package docker implements the Dependabot ecosystem contract for container
// images referenced from Dockerfiles, Kubernetes manifests and Helm values.
//
// The two Ruby dependencies it replaces are noted where they happen: the
// docker_registry2 gem becomes a direct Registry HTTP API v2 client
// (registry.go), and Gem::Version's comparison rules, which Docker tags are
// ordered by, are reimplemented in gemversion.go.
package docker

import (
	"context"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// EcosystemName is the package-manager identifier used in job configuration
// and on every parsed dependency.
const EcosystemName = "docker"

// Ecosystem is the Docker implementation of dependabot.Ecosystem.
type Ecosystem struct{}

var _ dependabot.Ecosystem = (*Ecosystem)(nil)

// New builds the Docker ecosystem.
func New() *Ecosystem { return &Ecosystem{} }

// Name is "docker".
func (e *Ecosystem) Name() string { return EcosystemName }

// ParseVersion parses a tag into a comparable version.
func (e *Ecosystem) ParseVersion(s string) (dependabot.Version, error) {
	return parseVersionIface(s)
}

// ValidVersion reports whether a tag can be compared as a version.
func (e *Ecosystem) ValidVersion(s string) bool { return ValidVersion(s) }

// ParseConstraints parses a comma-separated requirement string, which for
// Docker only ever comes from a job's ignore conditions.
func (e *Ecosystem) ParseConstraints(s string) ([]dependabot.VersionConstraint, error) {
	return dependabot.ParseOperatorConstraints(s, parseVersionIface)
}

// NewFileFetcher returns a fetcher for Dockerfiles and container YAML.
func (e *Ecosystem) NewFileFetcher(cfg dependabot.FetcherConfig) (dependabot.FileFetcher, error) {
	return NewFetcher(cfg)
}

// NewFileParser returns a parser for image references.
func (e *Ecosystem) NewFileParser(cfg dependabot.ParserConfig) (dependabot.FileParser, error) {
	return NewParser(cfg)
}

// NewUpdateChecker returns a checker backed by the image's registry.
func (e *Ecosystem) NewUpdateChecker(cfg dependabot.CheckerConfig) (dependabot.UpdateChecker, error) {
	return NewChecker(e, cfg), nil
}

// NewFileUpdater returns an updater that rewrites image references in place.
func (e *Ecosystem) NewFileUpdater(cfg dependabot.UpdaterConfig) (dependabot.FileUpdater, error) {
	return NewUpdater(cfg)
}

// NewMetadataFinder returns a finder for the image's source repository.
func (e *Ecosystem) NewMetadataFinder(cfg dependabot.MetadataConfig) (dependabot.MetadataFinder, error) {
	return NewMetadataFinder(cfg)
}

// MetadataFinder locates the repository an image was built from.
//
// Ruby reads the org.opencontainers.image.source annotation by shelling out to
// regctl. That is not ported: it needs an external binary, and the source URL
// only enriches a pull request body. The result is that Docker pull requests
// carry no changelog links.
type MetadataFinder struct{}

var _ dependabot.MetadataFinder = (*MetadataFinder)(nil)

// NewMetadataFinder builds a metadata finder.
func NewMetadataFinder(_ dependabot.MetadataConfig) (*MetadataFinder, error) {
	return &MetadataFinder{}, nil
}

// SourceURL is always empty; see MetadataFinder.
func (f *MetadataFinder) SourceURL(_ context.Context) (string, error) { return "", nil }

// init registers the ecosystem, mirroring how requiring a Ruby gem's top-level
// file populates Dependabot's lookup tables.
func init() {
	dependabot.Register(New())

	// An image referenced from a Dockerfile always ships, so there is no
	// development-only classification to make.
	dependabot.RegisterProductionCheck(EcosystemName, func(_ []string) bool { return true })
}
