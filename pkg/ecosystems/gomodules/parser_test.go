package gomodules

import (
	"context"
	"errors"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

func parse(t *testing.T, content string) []*dependabot.Dependency {
	t.Helper()
	parser, err := NewParser(dependabot.ParserConfig{
		Files: []*dependabot.DependencyFile{dependabot.MustNewFile(GoModFile, content)},
	})
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	deps, err := parser.Parse(context.Background())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return deps
}

func byName(deps []*dependabot.Dependency) map[string]*dependabot.Dependency {
	found := map[string]*dependabot.Dependency{}
	for _, dep := range deps {
		found[dep.Name] = dep
	}
	return found
}

func TestParseGoMod(t *testing.T) {
	deps := parse(t, `module example.com/app

go 1.24

require (
	github.com/spf13/cobra v1.8.1
	golang.org/x/mod v0.41.0
)

require github.com/inconshreveable/mousetrap v1.1.0 // indirect
`)

	found := byName(deps)
	if len(found) != 3 {
		t.Fatalf("parsed %d dependencies, want 3: %v", len(found), found)
	}

	cobra := found["github.com/spf13/cobra"]
	if cobra == nil || cobra.Version != "v1.8.1" {
		t.Fatalf("cobra = %v, want v1.8.1", cobra)
	}
	if len(cobra.Requirements) != 1 {
		t.Fatalf("cobra has %d requirements, want 1", len(cobra.Requirements))
	}
	req := cobra.Requirements[0]
	if req.Requirement != "v1.8.1" || req.File != GoModFile {
		t.Errorf("requirement = %q in %q, want v1.8.1 in go.mod", req.Requirement, req.File)
	}
	if got := req.Source.Get("source"); got != "github.com/spf13/cobra" {
		t.Errorf("source = %q, want the module path", got)
	}
}

// An indirect requirement has no manifest-level constraint of its own, so it
// carries no requirements and is treated as a subdependency.
func TestParseIndirectHasNoRequirements(t *testing.T) {
	deps := parse(t, `module example.com/app

go 1.24

require github.com/inconshreveable/mousetrap v1.1.0 // indirect
`)
	if len(deps) != 1 {
		t.Fatalf("parsed %d dependencies, want 1", len(deps))
	}
	if len(deps[0].Requirements) != 0 {
		t.Errorf("indirect dependency has %d requirements, want 0", len(deps[0].Requirements))
	}
	if deps[0].TopLevel() {
		t.Error("indirect dependency should not be top level")
	}
}

// Updating a replaced module would have no effect, so it is not offered.
func TestParseSkipsReplacedModules(t *testing.T) {
	deps := parse(t, `module example.com/app

go 1.24

require (
	github.com/spf13/cobra v1.8.1
	example.com/forked v1.0.0
)

replace example.com/forked => ../forked
`)
	if _, ok := byName(deps)["example.com/forked"]; ok {
		t.Error("a replaced module should not be parsed as a dependency")
	}
	if _, ok := byName(deps)["github.com/spf13/cobra"]; !ok {
		t.Error("unreplaced modules should still be parsed")
	}
}

// A first path segment with no dot is not a fetchable host.
func TestParseSkipsNonDomainPaths(t *testing.T) {
	deps := parse(t, `module example.com/app

go 1.24

require (
	internal/thing v1.0.0
	github.com/spf13/cobra v1.8.1
)
`)
	if len(deps) != 1 || deps[0].Name != "github.com/spf13/cobra" {
		t.Errorf("parsed %v, want only the fetchable module", byName(deps))
	}
}

func TestParserRequiresGoMod(t *testing.T) {
	_, err := NewParser(dependabot.ParserConfig{
		Files: []*dependabot.DependencyFile{dependabot.MustNewFile("README.md", "hi")},
	})
	if err == nil {
		t.Fatal("expected an error without a go.mod")
	}
	var notFound *dependabot.FileNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("error = %T, want FileNotFoundError", err)
	}
}

func TestParseRejectsUnparseableGoMod(t *testing.T) {
	parser, err := NewParser(dependabot.ParserConfig{
		Files: []*dependabot.DependencyFile{dependabot.MustNewFile(GoModFile, "this is not a go.mod")},
	})
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	if _, err := parser.Parse(context.Background()); err == nil {
		t.Fatal("expected an error parsing a malformed go.mod")
	}
}
