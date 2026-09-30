# dependabot-go

A Go port of [dependabot-core](https://github.com/dependabot/dependabot-core)'s
update engine.

It keeps the architecture that makes dependabot-core work — a small shared core
that knows nothing about any package manager, and one self-registering package
per ecosystem — and reimplements it in Go. Two ecosystems are ported end to
end: **Go modules** and **Docker**.

This is a port of the update engine, not of the hosted service. It reads a
repository from local disk and tells you what it would change. It does not talk
to a git host, open pull requests, or run the Dependabot job API.
[PORTING.md](PORTING.md) is the honest inventory: what is ported, what is not,
and every place the behaviour deliberately differs from Ruby.

## Quick start

```console
$ go build ./cmd/dependabot

$ ./dependabot ecosystems
docker
go_modules

$ ./dependabot update -package-manager go_modules -dir /path/to/repo

=== Bump golang.org/x/mod from v0.20.0 to v0.41.0 ===
Bumps golang.org/x/mod from v0.20.0 to v0.41.0.

Source: https://github.com/golang/mod

--- /go.mod ---
module example.com/app

go 1.26.3

require (
	golang.org/x/mod v0.41.0
	gopkg.in/yaml.v3 v3.0.1
)
```

By default the updated files are printed. Pass `-apply` to write them back to
the working tree instead.

### `dependabot update`

| Flag | Default | Meaning |
| --- | --- | --- |
| `-package-manager` | *(required)* | `go_modules` or `docker` |
| `-dir` | `.` | path to the checked-out repository |
| `-directory` | `/` | directory within the repository to update |
| `-repo` | `local/local` | repository name, used only in output |
| `-dependency` | | limit the run to this dependency (repeatable) |
| `-ignore` | | `name=requirement`, or a bare `name` to skip it entirely (repeatable) |
| `-apply` | `false` | write updated files to disk instead of printing them |
| `-v` | `false` | log at debug level |

Credentials come from the environment, so they stay off the command line:
`GITHUB_TOKEN`, and `DOCKER_REGISTRY` / `DOCKER_USERNAME` / `DOCKER_PASSWORD`.

## How it fits together

dependabot-core gives every ecosystem the same five-class contract. So does
this port, as five Go interfaces in `pkg/dependabot`:

| Interface | Question it answers |
| --- | --- |
| `FileFetcher` | which files in the repo describe dependencies? |
| `FileParser` | which dependencies do those files declare? |
| `UpdateChecker` | is there a newer version, and may we move to it? |
| `FileUpdater` | what do those files look like after the move? |
| `MetadataFinder` | where does this dependency live, for the pull request body? |

An `Ecosystem` is a factory for the five, plus the two value types every
ecosystem needs its own rules for: `Version` (how versions compare) and
`VersionConstraint` (what a requirement string allows).

```
pkg/dependabot/     the core: the interfaces, Dependency, DependencyFile,
                    RepoSource, SecurityAdvisory, the registry, and BaseChecker
pkg/updater/        the job: allow/ignore rules, the run loop, pull request text
pkg/ecosystems/     one package per package manager
internal/repofiles/ reading dependency files out of a local checkout
cmd/dependabot/     the CLI
```

Two pieces of Ruby dynamism needed a deliberate Go equivalent:

**Registration.** In Ruby, requiring `dependabot-go_modules` runs
`Dependabot::Utils.register_version_class(...)` as a side effect, and the
omnibus gem requires them all. Here each ecosystem package registers itself
from `init()`, and `cmd/dependabot/main.go` blank-imports the ones the binary
should support. Linking an ecosystem in is one import line; the core never
mentions it.

**Shared checker logic.** Ruby's `UpdateCheckers::Base` is a template method:
it implements `up_to_date?` and `can_update?` on top of abstract
`latest_version` and friends. Go has no inheritance, so `BaseChecker` holds a
`Finder VersionFinder` field that the concrete checker points back at itself:

```go
func (e Ecosystem) NewUpdateChecker(cfg dependabot.CheckerConfig) (dependabot.UpdateChecker, error) {
	c := &Checker{BaseChecker: dependabot.NewBaseChecker(e, cfg)}
	c.BaseChecker.Finder = c // the base calls back into the ecosystem
	return c, nil
}
```

An ecosystem that needs to replace one of the shared decisions implements an
optional interface (`VersionUpToDateOverrider`, `VersionCanUpdateOverrider`)
rather than overriding a method. Docker uses this, because an image pinned by
digest can be out of date while its tag has not moved.

## Ecosystems

**`go_modules`** parses `go.mod` with `golang.org/x/mod/modfile`, resolves
versions through the Go module proxy (`GOPROXY`, defaulting to
`https://proxy.golang.org`), and rewrites requirements while preserving
comments and formatting. Pseudo-versions, `+incompatible`, `replace`
directives and indirect requirements are all handled.

**`docker`** finds `FROM` lines in Dockerfiles and image references in
Kubernetes-style YAML, then queries the Docker Registry HTTP API v2 — including
the bearer-token challenge — for tags and digests. Tags are ordered by
RubyGems' `Gem::Version` rules, reimplemented here, because that is what
upstream does and `1.0.0.alpine` has to sort the same way. Tag "shapes"
(prefixes, suffixes, digit counts) are compared only against tags of the same
shape, so `1.2.3-alpine` never updates to `1.3.0-buster`.

## Development

```console
make test      # go test ./...
make lint      # gofmt -l and go vet
make build     # build ./cmd/dependabot
make cover     # test with a coverage summary
```

Everything is standard library plus `golang.org/x/mod` and `gopkg.in/yaml.v3`.
Registry access is tested against `httptest` servers, so the suite makes no
network calls.

## Licence

dependabot-core is dual-licensed; its non-commercial licence governs the
original code this port is derived from. See
[dependabot-core's LICENSE](https://github.com/dependabot/dependabot-core/blob/main/LICENSE)
before using this for anything.
