package updater

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// updated builds a dependency as an update checker would leave it: a new
// version with the old one recorded alongside.
func updated(name, from, to string) *dependabot.Dependency {
	return dependabot.MustNewDependency(dependabot.Dependency{
		Name: name, Version: to, PreviousVersion: from,
		PackageManager: fakeEcosystemName,
		Requirements: []dependabot.Requirement{
			{Requirement: to, File: "manifest"},
		},
		PreviousRequirements: []dependabot.Requirement{
			{Requirement: from, File: "manifest"},
		},
	})
}

func build(t *testing.T, change *DependencyChange) *Message {
	t.Helper()
	eco, err := dependabot.Lookup(fakeEcosystemName)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	return BuildMessage(context.Background(), eco, &Job{}, change)
}

func TestBuildMessageSingleBump(t *testing.T) {
	resetState()
	msg := build(t, &DependencyChange{
		Dependencies: []*dependabot.Dependency{updated("rails", "7.0.0", "7.1.0")},
		Directory:    "/",
	})

	if msg.PRName != "Bump rails from 7.0.0 to 7.1.0" {
		t.Errorf("PRName = %q", msg.PRName)
	}
	if want := "Bumps rails from 7.0.0 to 7.1.0.\n"; msg.PRMessage != want {
		t.Errorf("PRMessage = %q, want %q", msg.PRMessage, want)
	}
	if msg.CommitMessage != msg.PRName+"\n\n"+msg.PRMessage {
		t.Errorf("CommitMessage = %q, want the title and body joined", msg.CommitMessage)
	}
}

// A change outside the repository root says so, since one repository can have
// several manifests.
func TestBuildMessageNamesTheDirectory(t *testing.T) {
	resetState()
	msg := build(t, &DependencyChange{
		Dependencies: []*dependabot.Dependency{updated("rails", "7.0.0", "7.1.0")},
		Directory:    "/app",
	})

	if msg.PRName != "Bump rails from 7.0.0 to 7.1.0 in /app" {
		t.Errorf("PRName = %q", msg.PRName)
	}
}

func TestBuildMessageIncludesTheSourceURL(t *testing.T) {
	resetState().sourceURL = "https://github.com/rails/rails"

	msg := build(t, &DependencyChange{
		Dependencies: []*dependabot.Dependency{updated("rails", "7.0.0", "7.1.0")},
		Directory:    "/",
	})

	if !strings.Contains(msg.PRMessage, "\nSource: https://github.com/rails/rails\n") {
		t.Errorf("PRMessage = %q, want it to name the source", msg.PRMessage)
	}
}

// A metadata lookup that fails only makes the body less informative.
func TestBuildMessageSurvivesAMetadataFailure(t *testing.T) {
	s := resetState()
	s.sourceURL = "https://github.com/rails/rails"
	s.metadataErr = errors.New("the registry is unreachable")

	msg := build(t, &DependencyChange{
		Dependencies: []*dependabot.Dependency{updated("rails", "7.0.0", "7.1.0")},
	})

	if strings.Contains(msg.PRMessage, "Source:") {
		t.Errorf("PRMessage = %q, want no source after a failed lookup", msg.PRMessage)
	}
	if !strings.Contains(msg.PRMessage, "Bumps rails") {
		t.Errorf("PRMessage = %q, want the bump still described", msg.PRMessage)
	}
}

// Without a version on either side there is no bump to describe, only a
// requirement that moved.
func TestBuildMessageRequirementUpdate(t *testing.T) {
	resetState()
	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name: "rails", PackageManager: fakeEcosystemName,
		Requirements: []dependabot.Requirement{
			{Requirement: "~> 7.1", File: "manifest"},
		},
		PreviousRequirements: []dependabot.Requirement{
			{Requirement: "~> 7.0", File: "manifest"},
		},
	})

	msg := build(t, &DependencyChange{Dependencies: []*dependabot.Dependency{dep}})

	if msg.PRName != "Update rails requirement" {
		t.Errorf("PRName = %q", msg.PRName)
	}
	if !strings.Contains(msg.PRMessage, "Updates the requirements on rails.") {
		t.Errorf("PRMessage = %q", msg.PRMessage)
	}
}

func TestBuildMessageRemoval(t *testing.T) {
	resetState()
	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name: "rails", PackageManager: fakeEcosystemName,
		Removed: true, PreviousVersion: "7.0.0",
	})

	msg := build(t, &DependencyChange{Dependencies: []*dependabot.Dependency{dep}})

	if msg.PRName != "Remove rails" {
		t.Errorf("PRName = %q", msg.PRName)
	}
	if !strings.Contains(msg.PRMessage, "Removes rails.") {
		t.Errorf("PRMessage = %q", msg.PRMessage)
	}
}

// A change covering several dependencies names them all, in a stable order.
func TestBuildMessageMultipleDependencies(t *testing.T) {
	resetState()
	msg := build(t, &DependencyChange{
		Dependencies: []*dependabot.Dependency{
			updated("rails", "7.0.0", "7.1.0"),
			updated("rack", "2.0.0", "2.1.0"),
		},
	})

	if msg.PRName != "Bump rack and rails" {
		t.Errorf("PRName = %q, want the names sorted", msg.PRName)
	}
	for _, want := range []string{"Bumps rails from 7.0.0 to 7.1.0.", "Bumps rack from 2.0.0 to 2.1.0."} {
		if !strings.Contains(msg.PRMessage, want) {
			t.Errorf("PRMessage = %q, want it to contain %q", msg.PRMessage, want)
		}
	}
}

func TestBuildMessageEmptyChange(t *testing.T) {
	resetState()
	msg := build(t, &DependencyChange{})

	if msg.PRName != "" || msg.PRMessage != "" || msg.CommitMessage != "" {
		t.Errorf("BuildMessage = %+v, want an empty message", msg)
	}
}

func TestHumanizeList(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a and b"},
		{[]string{"a", "b", "c"}, "a, b and c"},
	}
	for _, tc := range cases {
		if got := humanizeList(tc.names); got != tc.want {
			t.Errorf("humanizeList(%v) = %q, want %q", tc.names, got, tc.want)
		}
	}
}
