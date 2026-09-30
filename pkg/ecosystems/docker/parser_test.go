package docker

import (
	"context"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

func parse(t *testing.T, files ...*dependabot.DependencyFile) []*dependabot.Dependency {
	t.Helper()
	parser, err := NewParser(dependabot.ParserConfig{Files: files})
	if err != nil {
		t.Fatalf("NewParser: %v", err)
	}
	deps, err := parser.Parse(context.Background())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return deps
}

func TestParseDockerfile(t *testing.T) {
	content := `# a comment
FROM --platform=linux/amd64 ubuntu:22.04 AS base
RUN apt-get update
FROM myregistry.example.com:5000/team/app:1.2.3
FROM docker.io/library/alpine:3.19
FROM scratch
`
	deps := parse(t, dependabot.MustNewFile("Dockerfile", content))

	if len(deps) != 3 {
		t.Fatalf("parsed %d dependencies, want 3: %+v", len(deps), deps)
	}

	if deps[0].Name != "ubuntu" || deps[0].Version != "22.04" {
		t.Errorf("first dependency = %s@%s, want ubuntu@22.04", deps[0].Name, deps[0].Version)
	}
	if got := deps[0].Requirements[0].Source.Platform; got != "linux/amd64" {
		t.Errorf("platform = %q, want linux/amd64", got)
	}

	if deps[1].Name != "team/app" {
		t.Errorf("second dependency name = %q, want team/app", deps[1].Name)
	}
	if got := deps[1].Requirements[0].Source.Registry; got != "myregistry.example.com:5000" {
		t.Errorf("registry = %q, want myregistry.example.com:5000", got)
	}

	// docker.io is implicit, so recording it would make the image look like it
	// came from a private registry.
	if got := deps[2].Requirements[0].Source.Registry; got != "" {
		t.Errorf("docker.io registry = %q, want it to be dropped", got)
	}
}

func TestParseDockerfileWithDigest(t *testing.T) {
	digest := "1bcefdb2ab5edb0e0e7b9ba8cd27ba5bfdc6e8f4e2ca4a6b1f9a3a5e9e5b9c2d"
	content := "FROM ubuntu:22.04@sha256:" + digest + "\n"

	deps := parse(t, dependabot.MustNewFile("Dockerfile", content))
	if len(deps) != 1 {
		t.Fatalf("parsed %d dependencies, want 1", len(deps))
	}
	source := deps[0].Requirements[0].Source
	if source.Tag != "22.04" || source.Digest != digest {
		t.Errorf("source = tag %q digest %q, want 22.04 and the digest", source.Tag, source.Digest)
	}
	// The tag, not the digest, is the version when both are present.
	if deps[0].Version != "22.04" {
		t.Errorf("version = %q, want 22.04", deps[0].Version)
	}
}

func TestParseKubernetesManifest(t *testing.T) {
	content := `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      initContainers:
        - name: init
          image: busybox:1.36
      containers:
        - name: app
          image: nginx:1.25.3
`
	deps := parse(t, dependabot.MustNewFile("deployment.yaml", content))

	names := map[string]string{}
	for _, dep := range deps {
		names[dep.Name] = dep.Version
	}
	if names["nginx"] != "1.25.3" || names["busybox"] != "1.36" {
		t.Errorf("parsed %v, want nginx@1.25.3 and busybox@1.36", names)
	}
}

func TestParseHelmValues(t *testing.T) {
	content := `image:
  registry: quay.io
  repository: prometheus/prometheus
  tag: v2.48.0
sidecar:
  image:
    repository: busybox
    tag: 1.36
templated:
  image:
    repository: nope
    tag: "{{ .Chart.AppVersion }}"
`
	deps := parse(t, dependabot.MustNewFile("values.yaml", content))

	found := map[string]*dependabot.Dependency{}
	for _, dep := range deps {
		found[dep.Name] = dep
	}

	prometheus, ok := found["prometheus/prometheus"]
	if !ok {
		t.Fatalf("prometheus not parsed, got %v", found)
	}
	if prometheus.Version != "v2.48.0" {
		t.Errorf("prometheus version = %q, want v2.48.0", prometheus.Version)
	}
	if got := prometheus.Requirements[0].Source.Registry; got != "quay.io" {
		t.Errorf("prometheus registry = %q, want quay.io", got)
	}

	// A numeric tag is a YAML number, not a string.
	if busybox, ok := found["busybox"]; !ok || busybox.Version != "1.36" {
		t.Errorf("busybox = %v, want version 1.36", busybox)
	}

	// A tag that is a chart template cannot be resolved without rendering.
	if _, ok := found["nope"]; ok {
		t.Error("a templated tag should not produce a dependency")
	}
}

func TestParseMergesRequirementsAcrossFiles(t *testing.T) {
	deps := parse(t,
		dependabot.MustNewFile("Dockerfile", "FROM ubuntu:22.04\n"),
		dependabot.MustNewFile("Dockerfile.dev", "FROM ubuntu:22.04\n"),
	)
	if len(deps) != 1 {
		t.Fatalf("parsed %d dependencies, want 1", len(deps))
	}
	if len(deps[0].Requirements) != 2 {
		t.Errorf("got %d requirements, want one per file", len(deps[0].Requirements))
	}
}
