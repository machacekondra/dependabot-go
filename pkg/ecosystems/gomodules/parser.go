package gomodules

import (
	"context"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// GoModFile is the manifest name every Go module has.
const GoModFile = "go.mod"

// GoSumFile is the lockfile name.
const GoSumFile = "go.sum"

// Parser reads dependencies out of a go.mod.
//
// Ruby runs `go mod edit -json` in a scratch copy of the repo and reads the
// JSON. golang.org/x/mod/modfile is the same parser the Go tool uses, so this
// gets identical results without a toolchain or a subprocess.
type Parser struct {
	cfg dependabot.ParserConfig
}

var _ dependabot.FileParser = (*Parser)(nil)

// NewParser builds a go.mod parser.
func NewParser(cfg dependabot.ParserConfig) (*Parser, error) {
	if dependabot.FindFile(cfg.Files, GoModFile) == nil {
		return nil, &dependabot.FileNotFoundError{FilePath: GoModFile, Detail: "no go.mod"}
	}
	return &Parser{cfg: cfg}, nil
}

// Parse returns the module's requirements.
func (p *Parser) Parse(_ context.Context) ([]*dependabot.Dependency, error) {
	goMod := dependabot.FindFile(p.cfg.Files, GoModFile)

	parsed, err := modfile.Parse(goMod.Name, []byte(goMod.ContentString()), nil)
	if err != nil {
		return nil, &dependabot.FileNotParseableError{FilePath: goMod.Path(), Detail: err.Error()}
	}

	replaced := replacedPaths(parsed)

	var deps []*dependabot.Dependency
	for _, req := range parsed.Require {
		if skipDependency(req.Mod.Path, replaced) {
			continue
		}
		dep, err := dependencyFrom(req, goMod.Name)
		if err != nil {
			return nil, err
		}
		deps = append(deps, dep)
	}
	return deps, nil
}

// replacedPaths collects modules with a replace directive. Ruby skips these
// because an update to a replaced module would have no effect.
func replacedPaths(f *modfile.File) map[string]bool {
	replaced := map[string]bool{}
	for _, r := range f.Replace {
		replaced[r.Old.Path] = true
	}
	return replaced
}

// skipDependency drops modules we cannot or should not update: replaced
// modules, and paths whose first segment is not a domain (the standard library
// and other non-fetchable paths).
func skipDependency(path string, replaced map[string]bool) bool {
	if replaced[path] {
		return true
	}
	host := path
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	return !strings.Contains(host, ".")
}

func dependencyFrom(req *modfile.Require, fileName string) (*dependabot.Dependency, error) {
	// Ruby stores the version with its "v" stripped and the requirement with
	// the "v" kept. This port keeps the "v" in both: the updater writes go.mod
	// directly rather than shelling out to `go get`, so the version it carries
	// has to be the form go.mod uses. The visible effect is that PR titles say
	// "v1.2.3" where Ruby says "1.2.3".
	version := req.Mod.Version

	requirements := []dependabot.Requirement{{
		Requirement: req.Mod.Version,
		File:        fileName,
		Groups:      []string{},
		Source:      &dependabot.Source{Type: "default", Extra: map[string]string{"source": req.Mod.Path}},
	}}
	// An indirect requirement is a transitive dependency: it has no
	// manifest-level constraint of its own, so it gets no requirements and is
	// treated as a subdependency.
	if req.Indirect {
		requirements = nil
	}

	return dependabot.NewDependency(dependabot.Dependency{
		Name:           req.Mod.Path,
		Version:        version,
		Requirements:   requirements,
		PackageManager: EcosystemName,
	})
}
