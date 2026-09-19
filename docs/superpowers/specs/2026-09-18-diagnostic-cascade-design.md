# Diagnostic cascade — design

Branch: `fix/diagnostic-cascade-r42`.

## What this is

`d8148f1` fixed a real defect — `no-entities` fired off the back of a
`repos-parse` error, against guessed fallback globs, sorted ahead of its own
cause, hinting "add a repos.yaml" at a repository whose `repos.yaml` is sitting
right there. The fix is correct in `validate`.

A composition audit of that commit then found two things it got wrong and two
documents that had drifted. This spec rules on all four. Nothing here changes
the schema, `.landsraad/checks`, or any exit code.

## Corrections to `d8148f1`

**The `gen` give-up path hides a second, unrelated file's mistakes.**
`cmd/landsraad/gen.go:60-68` returns before `assemble`, and `assemble`'s first
action is the `teams.yaml` read that ruling R42 exists to guarantee. Verified
against binaries built from `d8148f1^` and `d8148f1`, on a repository whose
`service.yaml` sits *inside* the fallback globs so `no-entities` was never in
play:

| | before | after |
|---|---|---|
| `repos-parse` | reported | reported |
| `missing-teams` | reported | **gone** |

`teams-parse` is lost the same way, and `score` behaves identically — both
reach `loadCatalog`. `validate` still reports them, because it calls
`checkOwners` directly, so `gen` and `score` now disagree with `validate`
about the same directory. That is what R43 ("one id for a missing
`teams.yaml`") exists to prevent.

The loss is wider than `teams.yaml`, and this section originally understated
it. Returning before `parseRepo` and `assemble` drops every diagnostic that
needs the catalog those two build. Measured the same way, on a repository with
a broken `repos.yaml`, a valid `teams.yaml` and one `service.yaml` *inside*
the fallback globs carrying a bad `tier` and an undefined field:

| | before | after |
|---|---|---|
| `repos-parse` | reported | reported |
| `schema` at `/metadata/tier` | reported | **gone** |
| `schema` at `/nonsense` | reported | **gone** |
| `routing-no-pagerduty` | reported | **gone** |

`owners-skipped` belongs in this second table, not the first: it comes from
`Teams.ValidateOwners` (`internal/config/teams.go:151`), which `assemble`
calls with the catalog in hand. Reading `teams.yaml` earlier cannot restore
it, and the fix that does is in R46 below.

The reasoning error is worth naming, because it is the same error in both
directions: `repos-parse` and `missing-teams` are not two diagnostics for one
cause. They are independent facts about two different files. Suppressing the
second is not deduplication, it is hiding.

**The claim that the multi-repository path could not have this defect is
false.** It was the stated justification for not threading the flag through
`parseRepo`. `repoDefaultPatternsNote` is indeed unreachable on a parse
failure, but one call up, `loadReposFile` has every element of the defect
`d8148f1` describes — live in `build` and `serve` today. See R47.

## Rulings

### R46: a give-up path reports the other files' mistakes

`teams.yaml` is a different file from `repos.yaml` and from the embedded
schema. No failure to read either of those may suppress its diagnostics. R42
established this for the empty-catalog return; it holds for every return in
the single-repository load composition, and for every return in `Build` that
is reached with a catalog to read. It does not reach above `Build`'s
fetch-failure refusals (`reportFetchFailures` at `build.go:51`, and the
all-repositories-failed branch below it): a fetch failure is neither of R46's
two causes, nothing has been read yet when either fires, and hoisting the read
above them would change what a network failure prints.

`loadCatalogScoped` has two give-up paths before `assemble` — `v == nil` (a
schema that will not compile, which is a landsraad bug) and `!patternsKnown`
(added by `d8148f1`). Both currently skip the read. The second is the
regression; the first is a pre-existing hole the second was modelled on.

**Part 1: hoist the read.** The mechanism is not a helper called on each
return path, because that requires every future give-up path to remember.
`assemble` uses its `cfg fs.FS` parameter for exactly one thing —
`fs.ReadFile(cfg, "teams.yaml")` at `gen.go:163` — so `cfg` becomes
`teams *config.Teams` and the read moves above both give-up paths. `assemble`
can then no longer read `teams.yaml` itself, so it cannot run against an
unread one. That is weaker than ordering-as-a-compile-error and must be
described as what it is: `*config.Teams`'s zero value is valid and
load-bearing — `assemble` treats `teams == nil` as "already reported" and
returns all-nil — so `assemble(p, src, scope, nil, c)` still compiles and
still bypasses `loadTeamsFor`. The type stops a caller passing a filesystem;
it does not stop a caller passing a literal `nil`. Which is exactly why no
give-up path may return above the `loadTeamsFor` call, and why that
arrangement is held by tests rather than by the compiler.

Part 1 restores what `loadTeamsFor` itself reports — `missing-teams`,
`teams-parse` — and nothing more. It does not restore `owners-skipped`, the
`schema` diagnostics on the files that were found, or the generators' checks,
because all of those need the catalog `parseRepo` and `assemble` build, and
the `!patternsKnown` return is above both.

**Part 2: suppress the diagnostic, not the load.** `!patternsKnown` stops
being a give-up path at all. It becomes a parameter on `parseRepo`, gating
only the zero-found diagnostic — which is what `validate.go` already does,
one condition on one `if`. `loadCatalogScoped` is then left with a single
give-up path (`v == nil`), `gen` and `score` agree with `validate` on the
same filesystem again (R43), and `no-entities` is still suppressed off a
guessed pattern set.

The multi-repository callers in `repos.go` pass `true` unconditionally. That
is sound rather than lucky, and R47 is why: a `repos.yaml` that did not parse
yields no repositories, so `ParseAll` and `openRepos`' phase-1 loop never
iterate on that path. R47's "no flag need be threaded anywhere" was a claim
about the multi-repository path, and it still holds there.

Consequence accepted: a nil validator now also reports the user's `teams.yaml`
problems. That is correct — the exit code is unchanged, and a landsraad bug is
no reason to withhold a fact about the user's repository.

### R47: `Loaded()`, not `len(Repos) == 0`, decides whether `repos.yaml` spoke

`config.LoadRepos` returns `&Repos{}` on a parse error
(`internal/config/repos.go:230-235`), so `len(r.Repos) == 0` at
`cmd/landsraad/repos.go:640` is true both when the file parsed and named
nothing and when it did not parse at all. Those are different facts, and
`Repos.Loaded()` exists to carry the distinction. `patternsFor` already uses
it; `loadReposFile`, the other consumer of `LoadRepos`, re-derives a worse
version.

Verified — `build` and `serve` against a genuine parse failure print:

```
info: repos.yaml:1 [default-patterns]
  repos.yaml lists no repositories; using default paths (., services/*, ...)
  hint: add repos.yaml if your services live elsewhere
error: repos.yaml:2 [repos-parse]
  cannot parse repos file: yaml: line 2: mapping values are not allowed ...
```

Two diagnostics for one cause; consequence before cause; a hint naming a file
that exists; and a message that is simply false, since the file names one
repository and merely failed to parse.

**A `repos.yaml` that did not parse yields no repositories.** Not one
synthesised local entry carrying `DefaultPatterns()`. `repos-parse` already
carries the cause, and the parsed-but-empty note at `repos.yaml:640-646` stays
exactly as it is — that case is real and deserves its announcement.

This also converts the `parseRepo` claim from plausible into true. Today a
parse failure produces one synthesised repo, `ParseAll` computes
`solo := len(w.sources) <= 1` as true, and `parseRepo`'s solo branch fires
`no-entities` — reached in a probe, and prevented from surfacing only by the
`c.HasErrors()` gate in `newBuildCmd`'s and `newServeCmd`'s `RunE`. That gate
is behavioural, not structural, and `Build` builds a *fresh* collector, so if
it were ever relaxed `no-entities` would surface alone with its cause nowhere
in the output — strictly worse than the defect `d8148f1` fixed. With no
repositories returned there is nothing to iterate, and no flag need be threaded
anywhere.

### R48: a claim's mechanism is part of the claim

Two design-spec bullets are true in their conclusion and wrong in their
mechanism, which sends the next contributor looking for something that is not
there.

`additionalProperties: false` appears nowhere in `schema/service.schema.json`
as a constraint — it occurs three times, each a schema for map values under
`labels` and `annotations`. The keyword actually relied on is
`unevaluatedProperties: false`, at seven sites. The conclusion holds: adding
`spec.definition` to a `kind: API` entity is rejected with
`at '/spec/definition': unknown field 'definition'`. Only the named mechanism
is wrong, and the two keywords are not interchangeable — a contributor who
"restores" `additionalProperties: false` while composing subschemas gets
different semantics from what the file relies on.

Decision D11 says output formats go through a `Formatter` **registry**. §3.1 of
the same document says there is deliberately no registry type, and
`internal/diag/format.go:21-36` returns a fresh map literal per call with the
deleted abstraction's postmortem in its comment. D11 is a decision *record*,
and CLAUDE.md's reading order sends contributors to the decision table first,
so as written it invites re-adding precisely what this project removed.

## Hygiene: no decision needed

- `internal/render/scorecard.go:16` — `var scorecardTiers = []int{1, 2, 3}` is a
  package-level mutable slice. `internal/catalog/entity.go:30-48` documents the
  exact cost of that shape and uses `[...]Kind{}` with a copying accessor
  instead, as do `config.defaultPatterns` and `config.hostKinds`. This is the
  one table that did not get the lesson. Unexported, so the blast radius is one
  package; the fix is one character.
- CLAUDE.md's rule 2 forbids package-level mutable state and names `sync.Once`
  and `init()` as its mechanisms. Five `//go:embed` vars are package-level
  mutable state that the rule's prose forbids and its regex cannot see, and
  `//go:embed` *requires* a package-level var, so it is a forced exception.
  `schema.Raw` has real cross-package readers. Name the exception the way the
  `os` row already names its two test exceptions.
- §7.1's table gives `validate` stages "1, 3, 4, 5\*", while the prose six
  lines below correctly says it also runs stage 6's structural half, which
  `validateCheckResults` confirms.
- ~~`cmd/landsraad/validate.go:372` says "eleven call sites"; there are ten.~~
  **Withdrawn.** The audit counted only non-test files. `plural` is also called
  from `cmd/landsraad/validate_test.go:632`, which is in the same `package
  main`, so ten production call sites plus one test call site is eleven and the
  comment is correct. Left here rather than deleted, because "the audit was
  wrong about one row" is the useful record — a claim about a count has to be
  checked against the same scope the claim uses.

## What the audit confirmed, and this spec does not touch

The `os` rule including both named test exceptions; the network rule and the
exact agreement between its scope and its enforcement regex; clock-at-the-edges;
the exact-message rule (the 194 `strings.Contains` calls are presence checks at
the integration layer, with wording pinned at the unit layer);
`writeSite`'s path backstops; stage 7's second ordering break genuinely held by
a behavioural test; and `bf3016e` itself. The `([]string, bool)` return added by
`d8148f1` is proportionate and stays — two production consumers one file apart,
each using it in a single `if`, with the flag asserted from three distinct true
states so "false in exactly one case" is pinned as a property.

## Order

1. R46 — the regression, first, because it is committed and it hides facts.
2. R47 — retires the twin defect and makes R46's neighbour claim true.
3. R48 and hygiene — documentation and one-character changes, last.

## What every task inherits

- `task ci` green before every commit: `go test -race ./...`, `go vet`,
  `gofmt -l` empty, `go mod tidy -diff`, `scripts/check-rules.sh`.
- Diagnostics assert **exact** message strings. A substring check passes
  against a badly worded message.
- No non-test file under `internal/` imports `os` or any `os/*` package.
- Exit codes are a one-way door. Every task that touches a diagnostic set pins
  the exit code in its test.
- Commit messages carry no AI attribution.
