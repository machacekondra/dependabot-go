// Package gomodules implements the Dependabot ecosystem contract for Go
// modules.
//
// It replaces three shell-outs the Ruby gem makes with native Go libraries,
// each noted where it happens: `go mod edit -json` becomes
// golang.org/x/mod/modfile (parser.go, updater.go), `go list -m -versions`
// becomes the module proxy's HTTP protocol (proxy.go), and Ruby's
// reimplementation of Go prerelease ordering becomes golang.org/x/mod/semver
// (version.go).
package gomodules

import (
	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// EcosystemName is the package-manager identifier used in job configuration
// and on every parsed dependency.
const EcosystemName = "go_modules"

// Ecosystem is the Go modules implementation of dependabot.Ecosystem.
type Ecosystem struct {
	// Proxy is the module proxy update checks read from. Nil means the default
	// proxy, resolved from GOPROXY.
	Proxy *Proxy
}

var _ dependabot.Ecosystem = (*Ecosystem)(nil)

// New builds the Go modules ecosystem.
func New() *Ecosystem { return &Ecosystem{} }

// Name is "go_modules".
func (e *Ecosystem) Name() string { return EcosystemName }

// ParseVersion parses a Go module version.
func (e *Ecosystem) ParseVersion(s string) (dependabot.Version, error) {
	return parseVersionIface(s)
}

// ValidVersion reports whether s is a usable Go module version.
func (e *Ecosystem) ValidVersion(s string) bool { return ValidVersion(s) }

// ParseConstraints parses a comma-separated requirement string.
//
// go.mod requirements are exact versions, not ranges, so this exists for the
// one caller that needs ranges: security advisories, whose vulnerable and safe
// ranges are written with comparison operators.
func (e *Ecosystem) ParseConstraints(s string) ([]dependabot.VersionConstraint, error) {
	return dependabot.ParseOperatorConstraints(s, parseVersionIface)
}

// NewFileFetcher returns a fetcher for go.mod and go.sum.
func (e *Ecosystem) NewFileFetcher(cfg dependabot.FetcherConfig) (dependabot.FileFetcher, error) {
	return NewFetcher(cfg)
}

// NewFileParser returns a go.mod parser.
func (e *Ecosystem) NewFileParser(cfg dependabot.ParserConfig) (dependabot.FileParser, error) {
	return NewParser(cfg)
}

// NewUpdateChecker returns an update checker backed by the module proxy.
func (e *Ecosystem) NewUpdateChecker(cfg dependabot.CheckerConfig) (dependabot.UpdateChecker, error) {
	return NewChecker(e, cfg, e.proxy()), nil
}

// NewFileUpdater returns an updater that rewrites go.mod.
func (e *Ecosystem) NewFileUpdater(cfg dependabot.UpdaterConfig) (dependabot.FileUpdater, error) {
	return NewUpdater(cfg)
}

// NewMetadataFinder returns a finder for the dependency's source repository.
func (e *Ecosystem) NewMetadataFinder(cfg dependabot.MetadataConfig) (dependabot.MetadataFinder, error) {
	return NewMetadataFinder(cfg)
}

func (e *Ecosystem) proxy() *Proxy {
	if e.Proxy != nil {
		return e.Proxy
	}
	e.Proxy = NewProxy()
	return e.Proxy
}

// init registers the ecosystem, mirroring how requiring a Ruby gem's top-level
// file populates Dependabot's lookup tables.
func init() {
	dependabot.Register(New())

	// go.mod has no dev-dependency concept: every requirement in the main
	// module's build list ships. Ruby registers the same always-true check.
	dependabot.RegisterProductionCheck(EcosystemName, func(_ []string) bool { return true })
}
