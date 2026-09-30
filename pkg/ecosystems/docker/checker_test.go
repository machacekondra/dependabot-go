package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// fakeRegistry serves the two endpoints the checker uses, behind the bearer
// token handshake a real registry requires.
type fakeRegistry struct {
	tags    []string
	digests map[string]string

	tokenIssued int
	tagRequests int
}

func (f *fakeRegistry) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenIssued++
		json.NewEncoder(w).Encode(map[string]string{"token": "a-token"})
	})

	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer a-token" {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer realm="https://%s/token",service="registry",scope="repository:myimage:pull"`, r.Host))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case strings.HasSuffix(r.URL.Path, "/tags/list"):
			f.tagRequests++
			// Serve the tags in two pages so the Link header is exercised.
			if r.URL.Query().Get("last") == "" && len(f.tags) > 1 {
				w.Header().Set("Link",
					fmt.Sprintf(`<%s?n=100&last=%s>; rel="next"`, r.URL.Path, f.tags[0]))
				json.NewEncoder(w).Encode(tagsResponse{Tags: f.tags[:1]})
				return
			}
			json.NewEncoder(w).Encode(tagsResponse{Tags: f.tags[1:]})

		case strings.Contains(r.URL.Path, "/manifests/"):
			reference := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			digest, ok := f.digests[reference]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Docker-Content-Digest", "sha256:"+digest)
			w.WriteHeader(http.StatusOK)

		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return mux
}

// newTestChecker wires a checker to a fake registry served over TLS, which is
// the only scheme the registry client speaks.
func newTestChecker(t *testing.T, fake *fakeRegistry, dep *dependabot.Dependency) *Checker {
	t.Helper()

	server := httptest.NewTLSServer(fake.handler(t))
	t.Cleanup(server.Close)

	host := strings.TrimPrefix(server.URL, "https://")
	for i, req := range dep.Requirements {
		if req.Source == nil {
			dep.Requirements[i].Source = &dependabot.Source{}
		}
		dep.Requirements[i].Source.Registry = host
	}

	checker := NewChecker(New(), dependabot.CheckerConfig{Dependency: dep})
	checker.registry.Client = server.Client()
	return checker
}

func imageDependency(name, version string) *dependabot.Dependency {
	return dependabot.MustNewDependency(dependabot.Dependency{
		Name:           name,
		Version:        version,
		PackageManager: EcosystemName,
		Requirements: []dependabot.Requirement{{
			File:   "Dockerfile",
			Source: &dependabot.Source{Tag: version},
		}},
	})
}

func TestCheckerLatestVersion(t *testing.T) {
	fake := &fakeRegistry{tags: []string{
		"1.2.3", "1.2.4", "1.3.0",
		"2.0.0rc1",     // a prerelease, so not an update from a stable tag
		"1.3.0-alpine", // a different variant, so not comparable
		"20240101",     // a differently shaped tag
	}}
	checker := newTestChecker(t, fake, imageDependency("myimage", "1.2.3"))

	latest, err := checker.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if latest.String() != "1.3.0" {
		t.Errorf("LatestVersion = %q, want 1.3.0", latest.String())
	}

	upToDate, err := checker.UpToDate(context.Background())
	if err != nil {
		t.Fatalf("UpToDate: %v", err)
	}
	if upToDate {
		t.Error("1.2.3 should not be up to date when 1.3.0 exists")
	}

	// Both pages were requested and the token was fetched only once.
	if fake.tagRequests != 2 {
		t.Errorf("made %d tag requests, want 2 (one per page)", fake.tagRequests)
	}
	if fake.tokenIssued != 1 {
		t.Errorf("fetched %d tokens, want 1", fake.tokenIssued)
	}
}

func TestCheckerKeepsVariantSuffix(t *testing.T) {
	fake := &fakeRegistry{tags: []string{
		"1.2.3-alpine", "1.3.0-alpine", "1.4.0", "1.3.0-slim",
	}}
	checker := newTestChecker(t, fake, imageDependency("myimage", "1.2.3-alpine"))

	latest, err := checker.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if latest.String() != "1.3.0-alpine" {
		t.Errorf("LatestVersion = %q, want 1.3.0-alpine", latest.String())
	}
}

// A project pinned to a rolling minor tag has opted into that precision, so a
// patch-level tag is not the update it wants.
func TestCheckerKeepsPrecision(t *testing.T) {
	fake := &fakeRegistry{tags: []string{"1.2", "1.3", "1.3.1", "1.4.0"}}
	checker := newTestChecker(t, fake, imageDependency("myimage", "1.2"))

	latest, err := checker.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if latest.String() != "1.3" {
		t.Errorf("LatestVersion = %q, want 1.3", latest.String())
	}
}

func TestCheckerUpToDate(t *testing.T) {
	fake := &fakeRegistry{tags: []string{"1.2.3", "1.2.2"}}
	checker := newTestChecker(t, fake, imageDependency("myimage", "1.2.3"))

	upToDate, err := checker.UpToDate(context.Background())
	if err != nil {
		t.Fatalf("UpToDate: %v", err)
	}
	if !upToDate {
		t.Error("1.2.3 should be up to date when it is the highest tag")
	}
}

func TestCheckerUpdatedRequirements(t *testing.T) {
	fake := &fakeRegistry{
		tags:    []string{"1.2.3", "1.3.0"},
		digests: map[string]string{"1.3.0": newDigest},
	}
	dep := imageDependency("myimage", "1.2.3")
	dep.Requirements[0].Source.Digest = oldDigest

	checker := newTestChecker(t, fake, dep)

	updated, err := checker.UpdatedRequirements(context.Background())
	if err != nil {
		t.Fatalf("UpdatedRequirements: %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("got %d requirements, want 1", len(updated))
	}
	if updated[0].Source.Tag != "1.3.0" {
		t.Errorf("tag = %q, want 1.3.0", updated[0].Source.Tag)
	}
	if updated[0].Source.Digest != newDigest {
		t.Errorf("digest = %q, want the digest of the new tag", updated[0].Source.Digest)
	}
	// The original requirement must not have been mutated in place.
	if dep.Requirements[0].Source.Tag != "1.2.3" {
		t.Error("the checker mutated the dependency it was given")
	}
}

func TestCheckerIgnoredVersions(t *testing.T) {
	fake := &fakeRegistry{tags: []string{"1.2.3", "1.3.0", "1.4.0"}}
	dep := imageDependency("myimage", "1.2.3")

	server := httptest.NewTLSServer(fake.handler(t))
	t.Cleanup(server.Close)
	dep.Requirements[0].Source.Registry = strings.TrimPrefix(server.URL, "https://")

	checker := NewChecker(New(), dependabot.CheckerConfig{
		Dependency:      dep,
		IgnoredVersions: []string{">= 1.4.0"},
	})
	checker.registry.Client = server.Client()

	latest, err := checker.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if latest.String() != "1.3.0" {
		t.Errorf("LatestVersion = %q, want 1.3.0 (1.4.0 is ignored)", latest.String())
	}
}
