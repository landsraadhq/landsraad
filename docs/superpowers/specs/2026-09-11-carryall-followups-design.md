# Carryall follow-ups — design

Resolves `docs/superpowers/plans/2026-09-11-carryall-followups.md`. The
decisions were approved by Q on 2026-09-11; this file is their record.
Rulings continue Plan 4's numbering, where R35 was the last.

## What this is

The follow-ups file listed what Plan 4 left behind. Before anything here was
decided, every item in it was re-verified against `10acd0c`. That meant
reading the code, running coverage profiles, and, where reading was not
enough, running the code itself, including binaries built from before,
during and after Plan 4. The pass found the file wrong in four places and
found eight problems it does not mention. Both are recorded below: a
follow-ups file trusted without being checked is how the next one gets
written.

Everything in the file is covered:

- ten rulings, R36–R45, for what needed a decision;
- a hygiene list for what did not;
- one item left undone for the reason the file already gave.

## Corrections to the follow-ups file

| The file says | What is true |
|---|---|
| `docsDirs`' comment justifies never returning `"."` with "both adapters already list the repository root" | Already corrected. `cmd/landsraad/repos.go:490–498` says GitLab's root is not known. |
| Running `Expand` and `contentSet` out of order makes `CheckFiles` report a present file as missing | It produces `docs-unreadable`, labelled a landsraad bug, because `CheckFiles` stats after `Expand` has listed the directory. `missing-file` needs `Expand` dropped entirely, and only on GitLab. |
| `serve.go`'s shadowed `w` "compiles only because the workspace is unused" | The shadow would compile even if the workspace were used; using it later would be a loud type error. This is a readability issue only. |
| The malformed-`repos.yaml` fixture is spelled out in three tests | Four: three in `serve_test.go`, one in `build_test.go`. The file's own trigger for extracting it is met. |

Decision 2 in the file names `missing-teams`. That is `validate`'s id; the
other commands say `teams-missing` (R43).

## Found by the triage, not in the file

| | Finding | Resolved by |
|---|---|---|
| N1 | `GitLab.Open` returns a 404 from a prefix listing as a failure of the whole repository. GitLab 17.7 and later answer 404 for a missing path, while GitHub and `discover.Find` both treat a pattern that matches nothing as not an error. | R45 |
| N2 | `localRepoName` (`cmd/landsraad/validate.go:149–160`) names the repository after `Repos[0].URL`'s basename, ignoring `local:` and `name:`. | R39 |
| N3 | One condition has two check ids, `missing-teams` (`cmd/landsraad/validate.go:218`) and `teams-missing` (`cmd/landsraad/gen.go:165`), with different messages and hints. | R43 |
| N4 | A `repos.yaml` mistake exits 1 or 2 depending on which key is wrong and which command reads it. | R36 |
| N5 | Since Plan 4, a run where every `service.yaml` fails to parse drops every `teams.yaml` diagnostic. A multi-repository run also claims no `service.yaml` was found. | R42 |
| N6 | `backoff` ignores `RateReset`, so an exhausted quota is retried at 1 s and 2 s while it is still spent. | R44 |
| N7 | A fetch failure names its repository twice: `cannot read edge-gateway: edge-gateway: cannot fetch …` | Hygiene |
| N8 | A `.landsraad/checks` that is a file, or cannot be read, silently counts as "no results", in `ingest.go` and in `cmd/landsraad/validate.go:113–117`. | R40 |

## Rulings

### R36: a mistake in a file you wrote exits 2

**Problem.** The same kind of mistake gets different exit codes:

| `repos.yaml` mistake | `validate` | `build` | `serve` |
|---|---|---|---|
| bad `url:` | 2 | 1 (`cmd/landsraad/build.go:413–415`) | 2 (`cmd/landsraad/serve.go:344–346`) |
| bad `paths:` (absolute, `..`, bad glob) | 1 (`cmd/landsraad/validate.go:32–36`) | 2 for the local repository. For a remote one it is reported as a fetch failure, exits 1, and `--allow-partial` downgrades it to a warning. | not traced |

`README.md:129` documents exit 1 for "a usage or config error". The
follow-ups file assumed 2 already meant "somebody's YAML is wrong"
everywhere, but the README did not say that either. The exit code depended
on the call site, not on any rule.

**Ruling.** One rule, stated in the README:

> **2** when a file you wrote has a problem a diagnostic can point at:
> `service.yaml`, `teams.yaml`, `repos.yaml`, `.landsraad/checks`.
> **1** when landsraad could not run: a bad flag, an unreadable root, a
> repository it could not fetch.

- `paths:` patterns are validated where `repos.yaml` is loaded
  (`validateRepos`), as check `repos-path` at the entry's line. It uses the
  same check `discover.Find` applies, so the two cannot disagree. A rejected
  pattern is reported once, and nothing downstream reports its absence
  again.
- `build` exits `exitValidation` for `repos.yaml` diagnostics.
- `validate` turns a `discover.Find` error into a `discover` diagnostic, as
  `parseRepo` already does (`cmd/landsraad/gen.go:82–90`), instead of
  exiting 1.
- A bad pattern on a remote repository is no longer a fetch failure, so
  `--allow-partial` can no longer downgrade a configuration mistake.
- `README.md:129` states the rule for every command, not only `validate`.
  So does the design spec's §12 exit-code table, whose row 1 read "usage
  or config error". The `score.go` comment that quotes that row changes
  with it.

**Door.** Every change moves a YAML mistake from 1 to 2. A script that
tests `== 1` for one of those cases breaks. `README.md:129–130` tells
scripts to key off the exit code, and the codes it documented were not
applied consistently.

### R37: `validate --satellite`

**Problem.** Ruling R34 keeps `teams.yaml` in the root repository only. So
`validate` in a satellite's own CI fails with `missing-teams`, about a file
that team was never meant to have. The team's only workarounds are copying
`teams.yaml` into every satellite, or not running `validate`.

**Ruling.** `landsraad validate --satellite` skips owner resolution and says
so.

- It emits SevInfo, check `owners-deferred`, File `teams.yaml`, Line 1:
  `owners are not checked in a satellite repository; the platform build
  resolves them against its teams.yaml`.
- `--satellite` in a repository that *has* a `teams.yaml` at its root,
  the one place `checkOwners` looks, is a usage error, exit 1: `--satellite skips owner checks, but this repository has a
  teams.yaml; drop the flag, or delete the file if the platform
  repository's teams.yaml is the real one`.
- Without the flag, nothing changes.
  `TestValidateToleratesTheFixturesCrossRepoRef` keeps asserting
  `missing-teams`, and a sibling test asserts the flag's exact output.

**Doors.**

- Rejected: downgrading `missing-teams` whenever there is no `repos.yaml`. A
  single-repository user has no `repos.yaml` either, and would lose a real
  error.
- The flag names the situation, not the mechanism. If another root-only
  file ever needs the same treatment, it can join additively, as a
  documented change and never a silent one.
- Rejecting the flag when a `teams.yaml` is present is the reversible
  choice: it can be relaxed later, whereas accepting it now could never be
  tightened. It also stops a root repository from switching off its own
  owner checks by copying a satellite's CI configuration.

### R38: a fetch failure cites the line that named the repository

**Problem.** `config.Repo.Line`'s doc comment and Plan 4 both promise that a
fetch failure points at its `repos.yaml` entry. `repoFailure` carries `URL`
and `Line`, and nothing reads either.

**Ruling.**

- Whenever the line is known, a fetch failure reads
  `error: repos.yaml:4: cannot read edge-gateway: …`, or `warn:` under
  `--allow-partial`.
- `lastEditDiagnostic` (`cmd/landsraad/build.go:230–240`) uses the entry's
  line instead of 1, for the same reason.
- `repoFailure.URL` is deleted.

### R39: a url-less local entry stays rejected, and the local repository has one name

**Problem.**

- `- name: platform` / `local: true` with no `url:` is rejected by
  `repos-url`.
- Separately, `localRepoName` names the local repository after the *first*
  entry's url, whatever `local:` and `name:` say.

**Ruling.**

- The rejection stays. Loosening `repos.yaml` cannot be undone, and nobody
  has asked for it; tightening it later would break the users it let in.
- `localRepoName` uses the entry `LocalPatterns` already selects
  (`LocalRepo()`, otherwise the first entry) and takes that entry's
  `Identity()`. `validate`, `gen` and `score` then name the repository they
  are standing in the same way `build` does.

**Visible.** The `repo` value in `validate` and `score --format json`
changes for repositories whose local entry is not first, or has a `name:`.
That change is the correction.

### R40: `.landsraad/checks` errors are reported, not swallowed

**Problem.** `internal/scorecard/ingest.go:112–115` and
`cmd/landsraad/validate.go:113–117` treat any `ReadDir` error as "no
results".

- A `.landsraad/checks` that is a file, or cannot be read, silently drops
  that repository's reported checks.
- The `ReadFile` branches print `cannot read %s` without the error.
  `unreadable` in `internal/scorecard/hermetic.go:57–68` already calls that
  a mistake, and fixes it for the hermetic checks.
- Once R45 makes the root flag honest, a planner bug here would show up as
  exactly this silence.

**Ruling.**

- `fs.ErrNotExist` stays "no results".
- `ErrNotListed` and `ErrNotFetched` become errors, worded as the planner
  bugs they are. `unreadable` does this for `ErrNotFetched` today but not
  for `ErrNotListed`, which falls through to `cannot read X: <err>`; it
  gains that branch. `ingest.go` shares a package with `unreadable` and
  uses it. `cmd/landsraad/validate.go` needs the same wording.
- Any other error becomes SevError `checks-unreadable`, with the message
  `cannot read .landsraad/checks: <err>`.
- Every `cannot read` message carries the error.

**Door.** A repository with a broken `.landsraad/checks` goes from exit 0
to 2. Today its reported results are silently ignored, which is worse. This
ruling lands first, because it is the tripwire for R45.

### R41: `Repo` names the repository that holds `File`

**Problem.** `diag.Text` never prints `Repo`. In a ten-repository build, a
satellite's `missing-file` reads `service.yaml:4` and never says which
repository it is in.

Printing `Repo` as it stands would be wrong, because it currently means two
things. In a multi-repository run, four sites set it to the repository a
diagnostic is *about*, while `File` is the root's `repos.yaml`:

- `parseRepo`'s search error (`cmd/landsraad/gen.go:84–88`);
- `parseRepo`'s per-repository warning (`cmd/landsraad/gen.go:113–119`);
- `repoDefaultPatternsNote` (`cmd/landsraad/repos.go:572–583`);
- `lastEditDiagnostic` (`cmd/landsraad/build.go:230–240`).

`parseRepo`'s single-repository error (`cmd/landsraad/gen.go:98–104`) is
already consistent: there, the one repository does hold `repos.yaml`.

Printed, those would read `edge-gateway:repos.yaml:1`, a file edge-gateway
does not have.

**Ruling.**

- `Repo` always names the repository that holds `File`, and empty means
  the repository the command is standing in. The four sites leave it
  empty, as every diagnostic about the root's own configuration already
  does (`repos-url`, `missing-teams`, `default-patterns`). Their messages
  name the repository they are about through `repoLabel`, as
  `repoDefaultPatternsNote` already does. Naming the root explicitly
  instead would thread its name through `parseRepo` and `openRepos` only
  to print `platform:repos.yaml:4`.
- `diag.Text` prints `repo:file:line` when told to. `build` and `serve`
  tell it to when there is more than one source, so single-repository
  output does not change.
- No JSON field is added, renamed or removed
  (`internal/diag/diag.go:58`). `repo` already means this for every
  diagnostic `--format json` can emit, because only single-repository
  `validate` and `score` take `--format`.

**Door.** This changes terminal text only, and `README.md:130` tells
scripts not to parse that.

### R42: an all-parse failure keeps its other diagnostics

**Problem.** `assemble` returns before reading `teams.yaml` when no entity
parsed (`cmd/landsraad/gen.go:136–155`). So since Plan 4, a run where every
`service.yaml` fails to parse drops every `teams.yaml` diagnostic, and the
user only finds out on the next run. In a multi-repository run, the same
case also reports `no service.yaml found in any configured repository`
about files that were found.

**Ruling.**

- `teams.yaml` is read, and its diagnostics are reported, whether or not
  any entity parsed.
- The multi-repository "no service.yaml found" fires only when no
  repository found a file. It never fires when files were found and failed
  to parse, because `yaml-parse` has already said why.
- The exit code is unchanged (2).

### R43: one id for a missing `teams.yaml`

**Ruling.** Every command uses the check id `missing-teams` and the message
`teams.yaml not found at the repository root, so no owner can be resolved`.
The hint differs by command, because the remedy does:

- `validate`: ``run `landsraad init` to create one, or pass --satellite if
  this repository's owners are defined in the platform repository's
  teams.yaml``
- `gen`, `score`, `build`: ``run `landsraad init` to create one``

**Why this id.** It is the one PR annotations already show, since
`validate` is what runs in PR CI, and it matches `missing-file`.

**Door.** `score --format json` is the only machine-readable output that
ever carried `teams-missing`.

**Depends on** R37, whose flag the `validate` hint names.

### R44: no retry that cannot succeed

**Problem.** A 403 for a spent quota is retried at 1 s and 2 s. `backoff`
(`internal/fetch/client.go:265`) ignores `RateReset`, so both retries fire
while the quota is still spent.

**Ruling.**

- A rate-limited response is not retried when `RateReset` is known and
  the retry would fire before it. `failureMessage` already names the reset
  time. When `RateReset` is unknown, retries work as they do today.
- `Retry-After` handling is unchanged.
- `ClientOptions` gains `Now func() time.Time`, defaulting to `time.Now`
  the same way `Sleep` defaults to `time.Sleep`
  (`internal/fetch/client.go:27–29`), so a test controls both.

### R45: only a listing marks a directory listed

**Problem.** `NewFS` marks `"."` listed as soon as it is constructed
(`internal/fetch/fs.go:93–100`).

- GitHub earns that flag. `FromEntries` covers the whole repository, and
  the truncated-tree descent lists the root before anything else
  (`internal/fetch/githubwalk.go:118–128`).
- `GitLab.Open` at non-root prefixes never lists the root
  (`internal/fetch/gitlab.go:150–162`). `lookup` then answers
  `ErrNotExist` for every root-level path. So a GitLab satellite with
  `paths: [services/*]` and `runbook: RUNBOOK.md` gets `missing-file` for a
  runbook that is sitting in the repository.

The obvious fix, listing the root as well, makes one case worse. Take a
`docs/` that has no `runbooks/`:

- Today, `docs/runbooks/api.md` answers `ErrNotExist`, and is right only by
  accident.
- With the root listed, `docs` becomes a known entry that nobody has
  listed. The lookup climbs to it and answers `ErrNotListed`. `CheckFiles`
  then tells the user to widen `paths:`, for a runbook that really is
  missing.

A 404 cannot fill the gap:

- GitLab has answered a missing tree path with 404 only since 17.7. Before
  that, it answered `200 []`.
- It also answers 404 when Gitaly is offline, and for a repository with no
  commits.

Treating a 404 as proof of absence would turn an outage into "your runbook
is missing".

**Ruling.** Only a listing proves anything, and a directory is marked
listed only when a listing covered it. That is the discipline GitHub's
descent already follows.

- `NewFS` marks nothing. `FromEntries` marks `"."`, because a complete
  listing did enumerate the root. A successful listing of a directory
  marks it listed even when it returns no rows.
- `GitLab` gains a non-recursive `listDir`, paginated like `listPath`
  (`internal/fetch/gitlab.go:97–140`).
- `GitLab.Open`, unless a prefix is `"."`, does three things:
  1. It lists the root, non-recursively.
  2. For each prefix, it lists the prefix's ancestors non-recursively,
     from the root down to the parent (`ancestorsOf`,
     `internal/fetch/githubwalk.go:238–247`), skipping any already listed.
  3. It lists the prefix recursively, but only if the parent's listing
     shows it and it is not already listed. A prefix its parent does not
     show is absent, and costs no request.
- When a prefix is `"."`, nothing changes: the single recursive listing
  `Open` makes today already lists the root.
- `GitLab.Expand` uses the same descent for each directory.
- **landsraad asks only for a directory a listing has shown.** Any error
  for such a directory is therefore unexpected, a 404 included, and it
  fails the repository. The root listing is no exception: a repository
  with commits always has a root. `Expand`'s tolerance of a 404
  (`internal/fetch/gitlab.go:209–214`) goes. It existed for a user naming
  a directory that does not exist, and that case no longer makes a
  request.
- GitHub's behaviour does not change.
- The `lookup` comment (`internal/fetch/fs.go:254–261`) and `docsDirs`'
  comment (`cmd/landsraad/repos.go:490–498`) are rewritten. Both adapters
  now list the root, so `docsDirs` never needing `"."` becomes true of
  both.

**Falls out.**

- N1: a prefix that does not exist is skipped without a request, so it can
  no longer fail the repository.
- The `literalPrefixes` redundancy, where `services/*` plus
  `services/api/*` lists `services/api` twice: a prefix that is already
  listed is skipped.

**Cost.** For a GitLab repository opened at non-root prefixes: one request
for the root, plus one for each unlisted ancestor of a nested prefix or of
an expanded directory. `paths: [services/*]` goes from one request to two.

**Rejected.**

- Recording absence proven by a 404 in `*FS`. It makes the fewest
  requests, but it trusts the 404 described above. It also adds a fourth
  state to a type whose three-answer design the code defends at length.
- Listing the root and nothing else. It makes the `docs/runbooks` case
  worse, as shown above.
- Making `discover.Find` surface `ErrNotListed`. That would mean replacing
  `fs.Glob`, which ignores `ReadDir` errors by design. `Find` already
  treats both answers the same, so an honest root flag changes nothing it
  returns. This is optional hardening, not part of this work.

**Tests.**

- The GitLab fakes serve the root listing.
- `TestGitLabExpandTreatsA404AsAbsent`
  (`internal/fetch/gitlab_test.go:298–325`) is replaced by a test that a
  directory its parent does not show makes no request and reads
  `ErrNotExist`.
- New tests in `internal/fetch`:
  - a root-level file can be statted after `Open(["services/*"])`;
  - `docs/runbooks`, absent under a present `docs`, reads `ErrNotExist`;
  - a missing prefix makes no request;
  - a 404 for a directory a listing showed is an error;
  - a 404 on the root listing is an error;
  - request counts are pinned.
- End to end: `cmd/`'s fake host speaks only GitHub today
  (`cmd/landsraad/fakehost_test.go`). It gains enough GitLab for one test:
  a satellite with `runbook: RUNBOOK.md` builds without `missing-file`.

## Hygiene: no decision needed

Each item is small. Any item that touches a message gets an exact-string
test.

**Tests for behaviour believed correct**

- `fetchBlobs` cache hits (`internal/fetch/blobs.go:80`). A corrupt hit is
  fetched again and overwritten; a valid hit makes no request. No test
  takes any cache hit today, corrupt or not.
- Rate limits. A 403 for a spent quota follows R44. A 403 with quota left
  is one call. A 429's `Retry-After` is honoured, a branch no test covers
  today.
- `Client`: a trailing-slash `BaseURL`; an encoded query; a `StatusError`
  whose text leaves out the query (`GET /x: HTTP 404: (empty response
  body)`).
- `literalPrefixes` and `ancestorsOf`: table tests, including
  `ancestorsOf("a/b/c/d")` returning `a`, `a/b`, `a/b/c`.
- `blobCache.Put`: its exact rejection message, `refusing to cache under
  "../../../etc/passwd": not a git object id`, added to the existing loop.
- `multiLastEdit`: its local-git, no-fetcher and remote-success branches,
  none covered today. The test uses a fixed-answer fetcher beside
  `errFetcher`, and a temporary git repository.

**Latent correctness**

- `summarise` (`internal/fetch/client.go:243–244`) backs off to the start
  of a character before truncating.
- `fetchBlobs` (`internal/fetch/blobs.go:110–138`) keeps each path's error
  and reports the error that belongs to the first path after sorting.
  Today the first path can be printed with another path's error, so
  `failureMessage`'s advice can change between runs.
- `internal/fetch/client.go:161` reads one byte past 64 MB and fails with
  `response larger than 64 MB`, without retrying. The limit is a const, and
  the test streams limit + 1 bytes.
- `Expand` before `contentSet`. An `expanded` value in `cmd/`, built only
  by the function that runs `Expand`, becomes what `contentSet` takes. This
  item is last and optional, because a wrong order already fails loudly,
  with the right label.
- N7: `failureMessage`'s default branch stops repeating the repository
  name.

**Cosmetic**

- `blobcache.go`: drop the out-of-date comment on `var _ fetch.Cache`, and
  note above the deferred remove that it does nothing after a successful
  `Rename`.
- `repoFailure.URL` is deleted, under R38.
- `serve.go`: the watcher is named `watcher`, not a second `w`.
- `ClientOptions.MaxAttempts` becomes `const maxAttempts = 3` beside the
  backoff schedule, since nothing in production set it. Tests that set it
  to 1 must still pass without real sleeps, so check that each one injects
  `Sleep`.
- `fetch.Cache` documents two things. `Get` and `Put` run concurrently,
  including two `Put`s for the same sha. And `fetchBlobs` discards a `Put`
  error, because a cache that cannot write makes a slower build, not a
  wrong one.
- `tokenVarName`: the README and its doc comment say *character*, not
  byte, and a test case pins `café` → `LANDSRAAD_TOKEN_CAF_`. The code
  does not change, because the variable name is what users set in CI.
- `render/docs.go`: `docsFor`'s comment mentions its one early return.
- `github.go`: `ref` moves below `mu`, and `mu`'s comment records why the
  lock exists. `gitlab.go` points at that reason today, and nothing states
  it.
- The malformed-`repos.yaml` fixture and its expected stderr become one
  helper and one const, still compared exactly.

## Not done

- **Extracting the shared adapter code.** There are still two adapters,
  and this project wants three examples before an abstraction. A third
  adapter is the trigger. When it comes, extract the lock discipline only.
- **`verifyBlob` skipping SHA-256 ids.** Found by the triage, not listed in
  the follow-ups file; the code already documents it. Revisit it when a
  host serves SHA-256 object ids.

## Order

1. R40, the tripwire.
2. R45, the root fix.
3. R36, R37, R43, R42, R41, R38, R39, R44. R43's hint names R37's flag, and
   R43 and R42 touch the same diagnostic.
4. Hygiene.
5. Documentation: the README (exit codes, `--satellite`, `tokenVarName`),
   any claim in `CLAUDE.md` these changes make false, and the follow-ups
   file, with each item marked with where it went and the four corrections
   applied. Then run `composition-auditor` before merging.

There are two implementation plans: steps 1–3, which carry the behaviour
changes and the review that matters, and step 4. README and `CLAUDE.md`
changes ship with the ruling that causes them. The follow-ups file is
closed at the end of the second plan. The plans run one after the other,
not in parallel, because both touch `internal/fetch/client.go`.

## What every task inherits

- The hooks: no `os` imports in non-test files under `internal/`; no
  package state; exact message strings in tests; `net/http` only in
  `internal/fetch`; gofmt and vet clean.
- `task ci` runs under `-race`, so a fake shared by `fetchBlobs`' workers
  needs a lock.
- The failing test lands before the fix.
