# Carryall follow-ups

What Plan 4 left behind, written down on the day it merged. Everything here
was found by a review or an audit, verified against the code, and
deliberately not fixed — either because it needs a decision, or because it
was not worth widening a finished branch for.

Nothing in this file blocks anything. It exists so the next person does not
have to rediscover it.

## Two decisions

Both change what landsraad requires of a user's repository, which this
project treats as a one-way door. Each has a test pinning today's behaviour
so the current answer is visible rather than accidental.

### 1. `build` exits 1 where `validate` and `serve` exit 2

For the same malformed `repos.yaml`, `build` exits `exitUsage` (1) while
`validate` and `serve` exit `exitValidation` (2). `repos-url` is somebody's
YAML being wrong, which is what 2 means everywhere else in the tool, so
`build` looks like the odd one out.

Pinned by `TestBuildExitsWhenReposYAMLIsMalformed`
(`cmd/landsraad/build_test.go`), whose comment says the same thing at
length. Changing it changes a documented exit code.

### 2. `validate` fails inside a satellite repository

Ruling R34 puts `teams.yaml` and `standards.yaml` in the root repository
only. So `landsraad validate`, run inside a fetched satellite's own
checkout, fails with `missing-teams` — a repository's own team gets an
error in their CI about a file they were never meant to have.

`TestValidateToleratesTheFixturesCrossRepoRef`
(`cmd/landsraad/validate_test.go`) asserts this against
`testdata/multirepo/edge-gateway`, and reasons that "a real user only ever
validates it as part of a checkout that has one." That reasoning is the
open question, not the settled answer: a satellite's own CI has no such
checkout, and the only workarounds available to that team are duplicating
`teams.yaml` into every satellite — defeating the point of having one — or
not running `validate` at all.

## One root, three symptoms

`NewFS` marks `"."` listed at construction without proof that anything
enumerated the root. Three known issues are that one fact:

- `GitLab.Open` on non-root prefixes leaves a root marked listed whose
  contents nobody fetched, so `fs.Stat` answers `fs.ErrNotExist` for a file
  sitting in the repository — the false statement `ErrNotListed` exists to
  prevent. A test currently pins that `ErrNotExist` answer.
- `FS.lookup`'s climb inherits the same imprecision at `"."`. It does not
  introduce it — the immediate-parent rule already gave the same confident
  answer one level down — and the code says so at `internal/fetch/fs.go`.
- `docsDirs`' comment (`cmd/landsraad/repos.go`) justifies never returning
  `"."` with "both adapters already list the repository root", which is
  true of GitHub and false of GitLab.

Fixing the flag honestly reaches `fs.Glob` inside `discover.Find`, which is
why it was not a finish-line change. Treat it as one item.

## Deferred

### Missing tests, behaviour believed correct

- A cache hit whose bytes fail `verifyBlob` falls through to a real fetch
  (`internal/fetch/blobs.go`). Correct by inspection, untested.
- No test asserts the retry *count* for a rate-limited 403.
- No coverage of a trailing-slash `BaseURL`, or of a populated query.
- `literalPrefixes`' edge cases have no direct unit test; `ancestorsOf`'s
  multi-level reversal is correct by inspection only.
- `blobCache.Put`'s exact rejection message is unasserted.
- `workspace.FetcherFor` is exercised only through `multiLastEdit`.
- `assemble`'s `len(src) > 1` gate also changes the case where files match
  but every one fails to parse. That is closer to pre-Plan-4 behaviour than
  what it replaced, and has no test either way.

### Latent correctness

- `summarise` truncates at `s[:200]` (`internal/fetch/client.go`), which can
  split a multi-byte rune and put invalid UTF-8 in a diagnostic.
- `firstErr` in `fetchBlobs` is whichever worker loses the race, so two
  failures in one repository can print differently across runs. `Paths` is
  sorted for reproducibility; this is not.
- A response over 64 MB is silently truncated by the `io.LimitReader`. A
  blob would fail SHA verification; a tree listing would fail to parse — so
  it surfaces as a confusing error rather than "response too large".
- `ingest.go` collapses `ErrNotListed` into "no results" when reading
  `.landsraad/checks`. Not currently reachable: `docsDirs` always seeds the
  directory and `Expand` runs first.
- `diag.Text` never prints `Repo`, so a satellite's `missing-file` says
  `service.yaml:4` without naming the repository it came from.
- `Expand`-before-`contentSet` ordering is enforced by comments, not types.
  Running them out of order now makes `CheckFiles` report a file that is
  really there as missing.

### Cosmetic

- `blobcache.go`'s `var _ fetch.Cache` comment says nothing else references
  `*blobCache` as a `fetch.Cache`. `cacheFor` does, and the compiler checks
  the same thing.
- `repoFailure.URL` and `.Line` are written and never read, though
  `config.Repo.Line`'s doc comment promises a fetch failure will point at
  the line that named the repository.
- `serve.go` shadows its `w *workspace` parameter with the fsnotify
  watcher. Compiles only because the workspace is unused after `newRebuild`.
- `ClientOptions.MaxAttempts` has no production consumer; only tests set it.
- `fetch.Cache` documents no concurrency contract although `fetchBlobs`
  calls it from parallel workers, while `*FS` next door documents a careful
  single-goroutine one. Both shipped implementations are safe.
- `README` says `tokenVarName` replaces every non-alphanumeric *byte*; it
  iterates runes.
- A `local: true` entry with a `name:` and no `url:` is rejected for a URL
  nothing ever reads.
- `internal/render/docs.go`'s `docsFor` comment reads as universal against a
  deliberate early return a few lines below.
- `github.go`'s `ref string // resolved on first use` predates the mutex
  that now guards every access to it.
- `blobcache.go` defers `os.Remove` on the temp file even after a successful
  rename — one failed syscall, harmless.
- The malformed-`repos.yaml` fixture and its expected diagnostic are spelled
  out in three tests. A fourth would justify extracting it.

## Deliberately not done

The `GitHub` and `GitLab` adapters duplicate roughly 25–30 lines across
`resolveRef` and `LastEdit` — a memo-plus-mutex pattern in which a data race
was already found once and fixed in both copies by hand. Both a review and
an audit raised extracting it, and both agreed waiting is defensible: this
project requires three examples for an abstraction, and there are two. A
third adapter is the trigger; extract then, and extract only the lock
discipline, not the endpoints or JSON shapes, which differ honestly.
