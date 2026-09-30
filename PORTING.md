# Porting notes

What was ported from [dependabot-core](https://github.com/dependabot/dependabot-core),
what was not, and every place this port deliberately behaves differently from
the Ruby it is derived from.

Read this before trusting any output. The gaps below are not bugs to be filed;
they are the boundary of the port.

## Scope

dependabot-core is a shared `common/` gem, ~35 ecosystem gems, an `omnibus/`
gem that requires them all, and an `updater/` service that runs jobs. This port
covers the shared core, the job/run layer, and two ecosystems.

| Upstream | Here | Status |
| --- | --- | --- |
| `common/lib/dependabot/*` | `pkg/dependabot/` | ported, minus the host API clients |
| `common/lib/dependabot/file_fetchers/base.rb` | `internal/repofiles/` | local checkouts only |
| `updater/lib/dependabot/updater/` | `pkg/updater/` | the version-update operation only |
| `omnibus/` | blank imports in `cmd/dependabot/main.go` | ported |
| `go_modules/` | `pkg/ecosystems/gomodules/` | ported end to end |
| `docker/` | `pkg/ecosystems/docker/` | ported end to end, minus the items below |
| 33 other ecosystems | — | not ported |

### Class-by-class

| Ruby | Go |
| --- | --- |
| `Dependabot::Dependency` | `dependabot.Dependency` |
| `Dependabot::DependencyFile` | `dependabot.DependencyFile` |
| `Dependabot::Source` | `dependabot.RepoSource` |
| `Dependabot::SecurityAdvisory` | `dependabot.SecurityAdvisory` |
| `Dependabot::Credential` | `dependabot.Credential` |
| `Dependabot::Version` (per ecosystem) | `dependabot.Version` interface |
| `Gem::Requirement` / `Dependabot::Requirement` | `dependabot.VersionConstraint` |
| `FileFetchers::Base` … `MetadataFinders::Base` | the five interfaces in `pkg/dependabot/ecosystem.go` |
| `UpdateCheckers::Base` template methods | `dependabot.BaseChecker` + `VersionFinder` |
| `Dependabot::Utils.register_*` | `dependabot.Register` and friends, called from `init()` |
| `Updater::Operations::UpdateAllVersions` | `updater.Runner` |
| `Dependabot::Job` | `updater.Job` |
| `PullRequestCreator::MessageBuilder` | `updater.BuildMessage` |

## Not ported

### The service side

- **The Dependabot job API client.** Upstream the updater is driven by a job
  description fetched from an API and reports progress back to it
  (`Dependabot::ApiClient`). Here a `Job` is a plain struct you construct, and
  results go to a `updater.Service` interface. The CLI's implementation prints.
- **Git host access.** No branch creation, no commits, no pull requests, no
  `PullRequestCreator`/`PullRequestUpdater`. `updater.BuildMessage` renders the
  title and body; delivering them is left to the caller.
- **Host-API file fetching.** Ruby's `FileFetchers::Base` reads files through
  the GitHub/GitLab/Azure/Bitbucket contents APIs, with cloning as a fallback.
  Only the local-checkout path is ported (`internal/repofiles`). A host-API
  fetcher fits behind the same `FileFetcher` interface.
- **Changelog, release note, commit and upgrade-guide fetching.** Upstream's
  message builder pulls these from the dependency's source repository to build
  the pull request body. That needs a host API client, so the body here is the
  bump description plus the source URL from the `MetadataFinder`.
- **Commit message preferences.** Upstream reads the repository's existing
  commit style (prefix, casing) and matches it. Not ported.

### Run modes

Only "update all versions" is ported. Missing:

- **Grouped updates** (`Updater::Operations::GroupUpdateAllVersions`,
  `DependencyGroup`, `group-rules`). Each dependency here gets its own change.
- **Security-only refresh operations** (`RefreshSecurityUpdatePullRequest`,
  `CreateSecurityUpdatePullRequest`). Security advisories *are* honoured —
  `Job.SecurityUpdatesOnly`, `SecurityAdvisoriesFor`, and
  `BaseChecker.PreferredResolvableVersion` picking the lowest fix — but the
  refresh/rebase operations that update an existing PR are not.
- **Vendoring** (`Dependabot::FileUpdaters` vendor support), **subdependency
  updates**, and the **dependency submission / snapshot** operations.

### Behaviour inside what is ported

- **`raise_on_ignored`.** Ruby raises `AllVersionsIgnored` so the service can
  tell the user their ignore rules blocked everything.
  `BaseChecker.AllVersionsIgnored()` exists and makes `CanUpdate` return false,
  but nothing raises: the run simply reports no update.
- **Full unlock (`UnlockAll`).** `BaseChecker.CanUpdate(ctx, UnlockAll)` always
  returns false. That matches upstream for both ported ecosystems —
  `go_modules` and `docker` do not implement `updated_dependencies_after_full_unlock`
  — but an ecosystem that needs it would have to add it.
- **Release cooldown** (`Dependabot::Package::ReleaseCooldownOptions`, holding
  back versions published in the last N days). Not ported for either ecosystem.
- **GitHub Enterprise detection.** `Source.from_url` probes unknown hosts over
  HTTP to see whether they are GHES. `dependabot.RepoSourceFromURL` never makes
  a network call and returns nil for hosts it does not recognise; construct a
  `RepoSource` directly with an explicit hostname and API endpoint instead.
  Ruby's `IGNORED_PROVIDER_HOSTS` list exists only to skip that probe, so it has
  no equivalent here.

## Docker divergences

The tag selection pipeline — shape matching, common-component detection,
prerelease handling, digest comparison, `latest` resolution — is ported in
full. These are the parts that are not:

- **`MetadataFinder`.** Ruby reads the `org.opencontainers.image.source`
  annotation off the image manifest by shelling out to `regctl`. This port has
  no external binary dependency, so the Docker metadata finder returns no
  source URL. Pull request bodies for Docker updates therefore have no
  `Source:` line.
- **`docker_created_timestamp_validation`.** An experiment flag upstream that
  cross-checks a candidate tag's created timestamp per platform before
  accepting it. Not ported.
- **`docker_pin_digests`.** A job setting that rewrites a tag reference to
  `tag@sha256:…`. Not ported; digests are updated in place when the reference
  already has one.
- **`docker_digest_only_update_suppression`.** Upstream can suppress an update
  that only moves the digest. Not ported, so a digest-only move is proposed.
- **Multi-arch no-op suppression.** Upstream skips updates where the new digest
  resolves to the same image content across platforms. Not ported.
- **`legacyVersionRelatedPatterns`.** Ruby uses
  `/^(?=.*\d)(?=.*[a-z])[a-z\d]+$/i` — "alphanumeric containing at least one
  digit and at least one letter" — to spot version-bearing tag components like
  `rc1` or `ltsc2022`. Go's RE2 has no lookahead, so this port approximates it
  with `(?i)^(?:\d+[a-z]|[a-z]+\d)[a-z\d]*$`. The two agree on every tag shape
  in the upstream test suite. They differ on components where the digit and the
  letter are separated by another run of the same class — Ruby matches
  `a1b2c3`, this does not.
- **Registry access.** Ruby uses the `docker_registry2` gem. `registry.go` is a
  direct implementation of the Docker Registry HTTP API v2 against the standard
  library, including the `WWW-Authenticate` bearer-token challenge and `Link`
  header pagination.

## Go modules divergences

- **No Go helper binary.** Upstream ships a compiled Go helper
  (`go_modules/helpers/`) and calls it over a JSON protocol to resolve
  versions. This port speaks the module proxy HTTP protocol directly
  (`proxy.go`) and rewrites `go.mod` with `golang.org/x/mod/modfile`, which
  preserves comments and formatting.
- **`go.sum` needs a toolchain.** Module hashes cannot be computed without
  downloading the module, so `go.sum` is refreshed by running `go mod tidy`
  against a scratch copy of the repository with the new `go.mod` in place.
  That needs `go` on `PATH` and a `RepoContentsPath`; without either, the
  updater errors rather than emitting a stale `go.sum`. Set
  `SkipGoSumOption` in `UpdaterConfig.Options` to update `go.mod` alone. Note
  that `go mod tidy` is driven by the module's *source*, so pointing the
  updater at a directory holding only a `go.mod` and `go.sum` will legitimately
  empty the `go.sum`.
- **No `replace` resolution.** Modules with a `replace` directive are skipped
  by the parser, as upstream does, rather than resolved through the
  replacement.
- **Vendored dependencies** (`vendor/modules.txt`) are not updated.

## Version semantics

Each ecosystem implements `dependabot.Version` itself, exactly as upstream
subclasses `Gem::Version`:

- **`gomodules.Version`** wraps `golang.org/x/mod/semver`, preserving the
  original string so `+incompatible` and pseudo-versions round-trip. A
  pseudo-version is *not* treated as a prerelease, matching upstream.
- **`docker.Version`** compares tags by RubyGems' rules. `gemversion.go` is a
  reimplementation of `Gem::Version` — RubyGems' `ANCHORED_VERSION_PATTERN`,
  segment splitting, and the numeric-beats-alphabetic ordering — because Docker
  tags are not semver and `1.0.0.alpine` has to sort where upstream puts it.

`dependabot.ParseOperatorConstraints` implements the requirement operators
(`=`, `!=`, `>`, `>=`, `<`, `<=`, `~>`) over any `Version`, so an ecosystem
gets `Gem::Requirement` semantics by supplying only a version parser.

## Ignore conditions

`version-update:semver-{patch,minor,major}` is translated into requirement
strings relative to the current version, as Ruby does:

| Update type | Current version | Ignored |
| --- | --- | --- |
| patch | `1.2.3` | `> 1.2.3, < 1.3` |
| minor | `1.2.3` | `>= 1.3.a, < 2` |
| major | `1.2.3` | `>= 2.a` |

The `.a` suffix is RubyGems' idiom for "including prereleases of that series".
An ecosystem's version parser has to accept it for the translation to bite;
both ported ecosystems do. A security job ignores update types entirely and
honours only explicit version requirements — a bump level is no reason to leave
a vulnerability in place.

## Testing

`go test ./...` makes no network calls. Registry access is exercised against
`httptest` servers: a fake Go module proxy (`gomodules/proxy_test.go`) and a
TLS `httptest` server for the Docker registry, since registry URLs are always
built as `https://`. Each of `pkg/dependabot` and `pkg/updater` registers its
own fake ecosystem so the registry-driven code paths run for real rather than
being bypassed.
