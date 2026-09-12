# Carryall follow-ups

What Plan 4 left behind, written down on the day it merged. Everything here
was found by a review or an audit, verified against the code, and
deliberately not fixed at the time — either because it needed a decision,
or because it was not worth widening a finished branch for.

**Closed, on the `carryall-followups` branch.** Every item was re-verified
before anything was decided, and the decisions are recorded in
`docs/superpowers/specs/2026-09-11-carryall-followups-design.md`. That pass
found this file wrong in four places, each corrected below where it
occurs, and found eight problems it did not mention (the spec's N1–N8).
Each item now says where it went: a ruling, R36–R45, described in the spec,
or a task in `docs/superpowers/plans/2026-09-11-carryall-followups-hygiene.md`.

## Two decisions

Both change what landsraad requires of a user's repository, which this
project treats as a one-way door.

### 1. `build` exits 1 where `validate` and `serve` exit 2

For the same malformed `repos.yaml`, `build` exited `exitUsage` (1) while
`validate` and `serve` exited `exitValidation` (2).

**Resolved by R36.** The inconsistency was wider than this: `validate`
itself exited 1 for a bad `paths:` and 2 for a bad `url:`, and the README
documented 1 for "a config error". There is now one rule: 2 when a file
you wrote has a problem a diagnostic can point at, 1 when landsraad could
not run. `build` exits 2 here.

### 2. `validate` fails inside a satellite repository

Ruling R34 puts `teams.yaml` and `standards.yaml` in the root repository
only, so `landsraad validate`, run inside a satellite's own checkout,
failed with `missing-teams` about a file that team was never meant to have.

**Resolved by R37:** `landsraad validate --satellite` skips owner
resolution and says so. Without the flag nothing changes.

## One root, three symptoms

`NewFS` marked `"."` listed at construction without proof that anything
enumerated the root. Three known issues were that one fact:

- `GitLab.Open` on non-root prefixes left a root marked listed whose
  contents nobody fetched, so `fs.Stat` answered `fs.ErrNotExist` for a
  file sitting in the repository.
- `FS.lookup`'s climb inherited the same imprecision at `"."`.
- `docsDirs`' comment was said to claim "both adapters already list the
  repository root". **Correction:** that was already out of date when this
  file was written; the comment had been corrected to say GitLab's root is
  not known.

**Resolved by R45**, and not by `NewFS` alone. Listing the GitLab root and
nothing else would have made a missing `docs/runbooks` under an existing
`docs/` read as "never looked", and a 404 cannot stand in for a listing:
GitLab answers one for a missing path only from 17.7, and also when Gitaly
is down. A directory is now marked listed only by a listing that covered
it, which is the discipline GitHub's descent already followed. `fs.Glob`
did not need to change: `discover.Find` treats both answers the same.

## Deferred

### Missing tests, behaviour believed correct

- A cache hit whose bytes fail `verifyBlob` falls through to a real fetch.
  **Hygiene Task 6**, with a valid hit alongside it: no test took any cache
  hit at all.
- No test asserted the retry *count* for a rate-limited 403. **R44** for a
  spent quota, which is no longer retried when the retry would fire before
  the reset; **hygiene Task 4** for a 403 with quota left and a 429's
  `Retry-After`.
- No coverage of a trailing-slash `BaseURL`, or of a populated query.
  **Hygiene Task 4.**
- `literalPrefixes`' edge cases and `ancestorsOf`'s multi-level reversal.
  **Hygiene Task 7.**
- `blobCache.Put`'s exact rejection message. **Hygiene Task 8.**
- `workspace.FetcherFor` was exercised only through `multiLastEdit`.
  **Hygiene Task 9**, which covers the three routes through `multiLastEdit`
  that had no test.
- `assemble`'s `len(src) > 1` gate. **R42.** It was a regression, not a
  neutral change: since Plan 4, a run where every `service.yaml` failed to
  parse also hid every `teams.yaml` diagnostic.

### Latent correctness

- `summarise` could split a multi-byte rune. **Hygiene Task 1.**
- `firstErr` in `fetchBlobs` was whichever worker lost the race. **Hygiene
  Task 6.** Worse than stated: the first sorted path was printed beside
  another path's error.
- A response over 64 MB was silently truncated. **Hygiene Task 3**, which
  also keeps the refusal from being retried.
- `ingest.go` collapsed `ErrNotListed` into "no results". **R40**, which
  also reports a `.landsraad/checks` that is a file, or cannot be read, on
  any filesystem.
- `diag.Text` never printed `Repo`. **R41.** `Repo` meant two things; it now
  always names the repository that holds `File`, and multi-repository
  `build` and `serve` print it.
- `Expand`-before-`contentSet` ordering was enforced by comments, not
  types. **Correction:** out of order, the result is `docs-unreadable`,
  labelled a landsraad bug, not a false `missing-file`, because
  `CheckFiles` stats after `Expand` has listed the directory. **Hygiene
  Task 13** makes the order a type.

### Cosmetic

- `blobcache.go`'s `var _ fetch.Cache` comment. **Hygiene Task 8.**
- `repoFailure.URL` and `.Line` were written and never read. **R38:** a
  fetch failure now cites the line that named the repository; `URL` is
  gone.
- `serve.go` shadowed its `w *workspace` parameter with the fsnotify
  watcher. **Correction:** the shadow would compile even if the workspace
  were used, so this was readability only. **Hygiene Task 11.**
- `ClientOptions.MaxAttempts` had no production consumer. **Hygiene Task 2:**
  a constant.
- `fetch.Cache` documented no concurrency contract. **Hygiene Task 11.**
- `README` said `tokenVarName` replaces every non-alphanumeric *byte*.
  **Hygiene Task 10.**
- A `local: true` entry with a `name:` and no `url:` is rejected. **R39:**
  the rejection stays, because loosening `repos.yaml` cannot be undone.
  `localRepoName` now picks the local entry by `local:` and `name:`.
- `docsFor`'s comment read as universal. **Hygiene Task 11.**
- `github.go`'s `ref` comment predated the mutex. **Hygiene Task 11.**
- `blobcache.go` deferred `os.Remove` even after a successful rename.
  **Hygiene Task 8:** commented, not restructured.
- The malformed-`repos.yaml` fixture was spelled out in three tests.
  **Correction:** four. **Hygiene Task 12.**

## Deliberately not done

The `GitHub` and `GitLab` adapters duplicate roughly 25–30 lines across
`resolveRef` and `LastEdit` — a memo-plus-mutex pattern in which a data race
was already found once and fixed in both copies by hand. Both a review and
an audit raised extracting it, and both agreed waiting is defensible: this
project requires three examples for an abstraction, and there are two —
still two when this file was closed. A third adapter is the trigger;
extract then, and extract only the lock discipline, not the endpoints or
JSON shapes, which differ honestly.
