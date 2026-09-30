package docker

import (
	"context"
	"strings"
	"testing"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

const (
	oldDigest = "1bcefdb2ab5edb0e0e7b9ba8cd27ba5bfdc6e8f4e2ca4a6b1f9a3a5e9e5b9c2d"
	newDigest = "2cd0a1e3b4f5061728394a5b6c7d8e9f0a1b2c3d4e5f60718293a4b5c6d7e8f9"
)

func update(t *testing.T, file *dependabot.DependencyFile, dep *dependabot.Dependency) string {
	t.Helper()
	updater, err := NewUpdater(dependabot.UpdaterConfig{
		Dependencies: []*dependabot.Dependency{dep},
		Files:        []*dependabot.DependencyFile{file},
	})
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	updated, err := updater.UpdatedDependencyFiles(context.Background())
	if err != nil {
		t.Fatalf("UpdatedDependencyFiles: %v", err)
	}
	if len(updated) != 1 {
		t.Fatalf("got %d updated files, want 1", len(updated))
	}
	return updated[0].ContentString()
}

func TestUpdateDockerfileTag(t *testing.T) {
	file := dependabot.MustNewFile("Dockerfile",
		"FROM --platform=linux/amd64 ubuntu:22.04 AS base\nRUN echo ubuntu:22.04\n")

	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:            "ubuntu",
		Version:         "24.04",
		PreviousVersion: "22.04",
		PackageManager:  EcosystemName,
		Requirements: []dependabot.Requirement{{
			File: "Dockerfile", Source: &dependabot.Source{Tag: "24.04"},
		}},
		PreviousRequirements: []dependabot.Requirement{{
			File: "Dockerfile", Source: &dependabot.Source{Tag: "22.04"},
		}},
	})

	got := update(t, file, dep)
	want := "FROM --platform=linux/amd64 ubuntu:24.04 AS base\nRUN echo ubuntu:22.04\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUpdateDockerfileDigest(t *testing.T) {
	file := dependabot.MustNewFile("Dockerfile",
		"FROM ubuntu:22.04@sha256:"+oldDigest+"\n")

	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:            "ubuntu",
		Version:         "24.04",
		PreviousVersion: "22.04",
		PackageManager:  EcosystemName,
		Requirements: []dependabot.Requirement{{
			File: "Dockerfile", Source: &dependabot.Source{Tag: "24.04", Digest: newDigest},
		}},
		PreviousRequirements: []dependabot.Requirement{{
			File: "Dockerfile", Source: &dependabot.Source{Tag: "22.04", Digest: oldDigest},
		}},
	})

	got := update(t, file, dep)
	want := "FROM ubuntu:24.04@sha256:" + newDigest + "\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUpdateDockerfilePinsNewDigest(t *testing.T) {
	file := dependabot.MustNewFile("Dockerfile", "FROM ubuntu:22.04\n")

	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:            "ubuntu",
		Version:         "24.04",
		PreviousVersion: "22.04",
		PackageManager:  EcosystemName,
		Requirements: []dependabot.Requirement{{
			File: "Dockerfile", Source: &dependabot.Source{Tag: "24.04", Digest: newDigest},
		}},
		PreviousRequirements: []dependabot.Requirement{{
			File: "Dockerfile", Source: &dependabot.Source{Tag: "22.04"},
		}},
	})

	if got := update(t, file, dep); !strings.Contains(got, "ubuntu:24.04@sha256:"+newDigest) {
		t.Errorf("digest was not pinned onto the updated tag: %s", got)
	}
}

func TestUpdateDockerfileWithRegistry(t *testing.T) {
	file := dependabot.MustNewFile("Dockerfile",
		"FROM myregistry.example.com:5000/team/app:1.2.3\n")

	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:            "team/app",
		Version:         "1.3.0",
		PreviousVersion: "1.2.3",
		PackageManager:  EcosystemName,
		Requirements: []dependabot.Requirement{{
			File: "Dockerfile",
			Source: &dependabot.Source{
				Registry: "myregistry.example.com:5000", Tag: "1.3.0",
			},
		}},
		PreviousRequirements: []dependabot.Requirement{{
			File: "Dockerfile",
			Source: &dependabot.Source{
				Registry: "myregistry.example.com:5000", Tag: "1.2.3",
			},
		}},
	})

	got := update(t, file, dep)
	want := "FROM myregistry.example.com:5000/team/app:1.3.0\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUpdateKubernetesManifest(t *testing.T) {
	file := dependabot.MustNewFile("deployment.yaml", `apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: app
          image: nginx:1.25.3
`)

	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:            "nginx",
		Version:         "1.27.0",
		PreviousVersion: "1.25.3",
		PackageManager:  EcosystemName,
		Requirements: []dependabot.Requirement{{
			File: "deployment.yaml", Source: &dependabot.Source{Tag: "1.27.0"},
		}},
		PreviousRequirements: []dependabot.Requirement{{
			File: "deployment.yaml", Source: &dependabot.Source{Tag: "1.25.3"},
		}},
	})

	got := update(t, file, dep)
	if !strings.Contains(got, "image: nginx:1.27.0") {
		t.Errorf("image was not updated: %s", got)
	}
	// Rewriting text rather than re-serialising keeps the rest of the file
	// byte-for-byte identical.
	if !strings.HasPrefix(got, "apiVersion: apps/v1\nkind: Deployment\n") {
		t.Errorf("the rest of the document was disturbed: %s", got)
	}
}

func TestUpdateHelmValues(t *testing.T) {
	file := dependabot.MustNewFile("values.yaml", `image:
  repository: nginx
  tag: 1.25.3
`)

	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:            "nginx",
		Version:         "1.27.0",
		PreviousVersion: "1.25.3",
		PackageManager:  EcosystemName,
		Requirements: []dependabot.Requirement{{
			File: "values.yaml", Source: &dependabot.Source{Tag: "1.27.0"},
		}},
		PreviousRequirements: []dependabot.Requirement{{
			File: "values.yaml", Source: &dependabot.Source{Tag: "1.25.3"},
		}},
	})

	got := update(t, file, dep)
	if !strings.Contains(got, "tag: 1.27.0") {
		t.Errorf("tag was not updated: %s", got)
	}
	if !strings.Contains(got, "repository: nginx") {
		t.Errorf("repository was disturbed: %s", got)
	}
}

func TestUpdateLeavesUnchangedFilesAlone(t *testing.T) {
	file := dependabot.MustNewFile("Dockerfile", "FROM ubuntu:22.04\n")

	dep := dependabot.MustNewDependency(dependabot.Dependency{
		Name:           "ubuntu",
		Version:        "22.04",
		PackageManager: EcosystemName,
		Requirements: []dependabot.Requirement{{
			File: "Dockerfile", Source: &dependabot.Source{Tag: "22.04"},
		}},
		PreviousRequirements: []dependabot.Requirement{{
			File: "Dockerfile", Source: &dependabot.Source{Tag: "22.04"},
		}},
	})

	updater, err := NewUpdater(dependabot.UpdaterConfig{
		Dependencies: []*dependabot.Dependency{dep},
		Files:        []*dependabot.DependencyFile{file},
	})
	if err != nil {
		t.Fatalf("NewUpdater: %v", err)
	}
	if _, err := updater.UpdatedDependencyFiles(context.Background()); err == nil {
		t.Error("expected an error when nothing changed")
	}
}
