# Monorepo defects — design

Branch: `fix/codeowners-duplicate-path-r50`.

## What this is

Five defects found by running `main` @ `9d56bbd` against a real ~75-entity Go
monorepo catalog — roughly 20 services, kinds derived from which Helm chart
deploys each one, `dependsOn` from `go list -deps` plus the `EVENT_*_TOPIC__`
config. The full pipeline ran: `validate` 0, `gen --check` 0, `score` 3. The
catalog was a scratch mirror; the source repository was never written to.

This spec rules on all five. Two of them — R51 and R53 — change what landsraad
requires of, or reads from, a user's repository, and are flagged as such.

**What was verified here, and what was not.** R50's defect was reproduced
first-hand against a binary built from `9d56bbd`, and the transcript below is
that run, not the report's. R51's, R52's and R53's mechanisms were confirmed
by reading the code at the cited lines. R51's *consequence* — that one real
service's documentation is 391 days stale behind a false "has no index.md" —
is the reporter's measurement, reproduced here as a claim about their data and
not re-run. The distinction matters because R51's argument does not depend on
it: the hardcoded filename is visible in the source.

## Correction to the report as received

**The report scopes the docs-index defect to one file. It is three, in two
packages.** It cites `internal/scorecard/hermetic.go:225` and stops. `index.md`
is also hardcoded at `internal/render/docs.go:168` (the link-rewrite special
case) and `:338` (the ruling R18 hoist onto the entity page).

Fixing only the scorecard would leave a Hugo repository passing `docs-fresh`
while the portal still refuses to hoist its `_index.md` onto the entity page.
That is the scorecard and the renderer disagreeing about what a docs index is,
which is strictly worse than today's state — today they are both wrong in the
same direction, and the failure is at least honest.

**This section first said four sites, and named `internal/fetch` as the
fourth. That was wrong, and it is corrected here rather than quietly
narrowed.** The two `index.md` occurrences in `internal/fetch/fs.go` are
illustrative prose inside doc comments, not logic. The content planner is
`cmd/landsraad/repos.go`, and it does not name index files at all: it
`fs.WalkDir`s `spec.docs` and adds every `*.md` it finds, then `addDir`s the
directory. `_index.md` has therefore always been fetched for a remote
repository, and the claim that fixing the scorecard alone would produce a
landsraad-bug diagnostic about remote repositories was false.

The error is worth keeping because of how it was made. The `fetch` claim came
from `grep`ping for `index.md` and reading the hit count, not the hits — the
same shortcut the report's own author owned up to when they "confirmed" the
CODEOWNERS defect off a stale file. A citation is a claim about code, and a
grep hit is not yet one.

**One thing the report got right that is worth keeping.** It flagged
`sort.Slice`'s instability in `codeowners.go` as a suspected source of
nondeterministic output for equal paths, then tested it — 15 consecutive `gen`
runs, identical md5 — and withdrew it. It holds because catalog order is
already deterministic (`NewCatalog` sorts by repo then source path, and
`Entities()` returns that slice). R50 does not change it, and depends on
exactly that ordering property.

## Rulings

### R50: a file keyed on paths says when two entities claim one path

The catalog's uniqueness invariant is `(kind, name)`. That is deliberate, and
it is why `service:event-router` and `worker:event-router` legally coexist —
the monorepo run exercised it and it worked. `CODEOWNERS()` then projects that
entity space onto **path** space through `ownedPath(e)` and assumes the
projection is injective. Nothing constrains `spec.path` to be unique, so it is
not. `entries` is a flat slice and the only invariant enforced on it is sort
order.

Reproduced against a binary built from `9d56bbd`, two entities differing in
kind and owner and sharing `spec.path: internal/app/event-router`:

```
$ landsraad validate .
ok: 2 entities validated, no problems found          exit 0
$ landsraad gen .
ok: 3 artifacts generated                            exit 0
$ tail -2 CODEOWNERS
internal/app/event-router/ @vredp/event-processing
internal/app/event-router/ @vredp/unified-analytics
```

git evaluates CODEOWNERS **last-match-wins**. `event-processing` therefore
holds no review rights whatsoever on a directory this catalog records it as
owning, and nothing in `validate` or `gen` said so. At 20 entities on one path
the reporter measured 20 lines and 19 silently voided owners.

This is not a regression — the output is identical on `5f0b35c` and `9d56bbd`,
and `git log -S"seen" -- internal/generate/codeowners.go` is empty. No dedup
has ever existed.

**The project already ruled on this class and guarded it one level up.**
`docs/superpowers/plans/2026-09-09-landsraad-portal-renderer.md:1272` says two
teams slugging to one path "silently overwrite each other's page" and calls it
"exactly the 'silent corruption instead of a hard failure' case the project
forbids"; `TeamSlugs` reports it as an error. R50 is that same guard, missing
at the level below.

**The ruling.** `CODEOWNERS()` groups entries by `ownedPath(e)`. When two
entities resolve to one path:

- **The owners render identically** — emit one line, no diagnostic. Nothing is
  lost. The second line was a byte-for-byte repeat of the first and git's
  last-match-wins resolves it to the same owner either way.
- **The owners differ** — `codeowners-duplicate-path`, severity error. `gen`
  refuses, as it already does for `codeowners-unknown-owner`.

**Dedup keys on the rendered owners string, not on the team name.** Two
distinct teams with identical member lists produce a byte-identical line, and
there is nothing to warn about: the file git reads is the same file. The
question this check asks is "would these two entities emit different owners for
one path", and that question is about the emitted text.

**The diagnostic mirrors `duplicate-name`, and does not invent a second
convention.** `internal/catalog/merge.go:54-68` already answers "two things
collide, which one do I cite": it anchors on the second entity encountered,
names the first's location and line inside the message, and `continue`s so the
first wins the slot. R50 does the same, over `cat.Entities()`, which is already
sorted by repo then source path — so which entity is "first" is deterministic
and does not depend on filesystem walk order.

Message and hint, exact:

```
error: workers/event-router/service.yaml:4 [codeowners-duplicate-path]
  internal/app/event-router is claimed by both service:event-router
  (event-processing, services/event-router/service.yaml line 4) and
  worker:event-router (unified-analytics), whose teams differ, so CODEOWNERS
  would silently give one of them no review rights
  hint: CODEOWNERS is last-match-wins; give them distinct spec.path values, or
        one owner
```

That is the implemented output, not a sketch. "Duplicate path" alone would send
someone looking for a typo; this names both entities, both teams and both
files, so one line of output reaches every side of the problem.

**An earlier draft of this ruling named the winning team, and that was wrong.**
It read "would give the directory to `unified-analytics` alone". The winner is
the entity whose line sorts last, and `sort.Slice` is not stable — for two
entries with equal keys the resulting order is not defined. The correction
survives the fix that makes it moot: because differing owners are now refused,
the message would have been describing a file landsraad no longer writes.
Naming a consequence the code does not produce is the failure mode R48 is
about. It says "one of them" because that is what is true.

**Nested paths are deliberately untouched.** `services/` owned by A and
`services/api/` owned by B is last-match-wins working *correctly* — B's rule is
a deliberate override, and the existing sort exists to make it land in the
right order. R50 is about **identical** paths, where last-match-wins is not an
override anybody expressed.

**`validate` still exits 0 on this catalog, and that gap is left open
deliberately.** The check lives in `CODEOWNERS()`, which only `gen` and `build`
call, so the PR gate still reports "no problems found" for a catalog `gen` will
refuse. That is the same shape as R54 and is not fixed here: `validate` has its
own `unknown-owner` check mirroring the generator's `codeowners-unknown-owner`,
so the precedent for a validate-side twin exists, and R43 ("one condition, one
check id") is the ruling that would govern it. It is a separate change with its
own decision to make, and folding it into this one would widen a settled fix
into an unsettled one.

**This changes exit codes for catalogs that pass today.** A repository with two
differently-owned entities on one path goes from `gen` exit 0 to exit 2. That
is the point — the exit 0 was wrong — but it is a real break for anyone whose
catalog is in that state, and it is the reason this ruling was confirmed before
being written rather than after.

### R51: one spelling of "docs index", defined in one place

`docsFresh` joins a literal `index.md` and returns `Fail` on a failed `Stat`
**before** it ever calls `env.LastEdit`:

```go
index := pathpkg.Join(e.Spec.Docs, "index.md")     // hermetic.go:225
if _, err := fs.Stat(fsys, index); err != nil {
	return Result{Check: id, Status: StatusFail,
		Detail: fmt.Sprintf("%s has no index.md", e.Spec.Docs)}
}
```

Any Hugo docs tree uses `_index.md`. In the monorepo measured, 1 file is named
`index.md` and 53 are named `_index.md`, so all 18 documented services reported
"has no index.md" and the freshness machinery below that `Stat` never ran once.
The reporter renamed two services' files, preserving their real git commit
dates, and got `worker:analytics-ddo-out` → pass "edited 33 days ago" and
`service:customer` → fail "last edited 391 days ago, limit is 180 days", with
an untouched control unchanged. The machinery is correct. The filename is the
sole blocker, and it masked a genuine 391-day-stale finding behind a false
"you have no docs".

**The spec never named this file.** `docs/superpowers/specs/2026-09-08-landsraad-design.md`
words §5.3 as a docs index existing and being recent; `index.md` is convention
that hardened into three hardcoded literals, not a ruling anybody made.

**The ruling.** A docs index is `index.md` **or** `_index.md`, and when both
exist `index.md` wins — deterministic, and it leaves every repository that
passes today passing with the same file. That definition lives in exactly one
place and is consumed by all three sites:

| site | today | after |
|---|---|---|
| `internal/scorecard/hermetic.go:225` | `Stat` of a literal | asks the shared resolver |
| `internal/render/docs.go:338` | `rel == "index.md"` selects the R18 hoist | the resolver selects it |
| `internal/render/docs.go:168` | `src == l.docsDir+"/index.md"` rewrites links to it | the resolved name |

**One definition, in `internal/catalog`.** `DocsIndex(fsys, docsDir)` answers
"which file is this entity's documentation index" once, and both the scorecard
and the renderer ask it. Two answers is exactly how a service comes to pass
`docs-fresh` while the portal refuses to hoist the very file that passed it,
and this ruling exists because there were already two.

**The diagnostic wording changes.** "has no index.md" becomes a sentence that
names both spellings, because a message naming one file is what sent 18
services' owners looking for the wrong thing:

```
docs/ has no index.md or _index.md
```

**This changes scorecard outcomes for existing users**, which is the whole
point — entities that falsely failed now get their real verdict, and some of
those real verdicts are failures with a different reason, as `service:customer`
shows. It is a one-way-ish door in the weak sense: no user file changes and no
exit code is redefined, but a published score can move in either direction on
the next run. It needs to land with a line in the release notes, not silently.

### R52: a diagnostic about a reference cites the reference

`resolveRefs` passes `Line: e.NameLine` for both `malformed-ref`
(`internal/catalog/graph.go:84`) and `dangling-ref` (`:98`). A bad reference
written on line 12 reports as `svc/service.yaml:4`. The monorepo's first real
run produced 17 errors on one file, all pointing at line 4.

**The type's own doc comment already promises otherwise.**
`internal/catalog/entity.go:126-128`: "SourceRepo, SourcePath and NameLine are
provenance ... They exist so a collision or **a dangling reference** can name
both sides with a file and a line." `NameLine` is the line of `metadata.name`.
For a collision that is exactly right — `duplicate-name` is a claim about the
name. For a dangling reference it is the wrong line, and has been since the
comment was written.

**The ruling.** `malformed-ref` and `dangling-ref` cite the line of the
offending list item. Mechanically:

- `parse.go` gains a sequence-aware sibling to `fieldLine`, returning the
  1-indexed line of each item under a key path. The mapping walk `fieldLine`
  already performs is extracted to a shared `nodeAt` so both use one traversal;
  the new `seqItemLines` requires a `yaml.SequenceNode` and reads
  `Content[i].Line`. These are named structurally and not by line number,
  because this ruling's own change moves them — which is what R48 is about.
- `Entity` carries those lines the same way it carries `NameLine` — attached at
  parse time, `yaml:"-"`, for exactly the two fields `resolveRefs` walks:
  `dependsOn` and `providesApis`. No other field holds a reference.
- Those two field names become the exported constants `FieldDependsOn` and
  `FieldProvidesApis`, because one string is now doing three jobs: the key
  `RefLines` records under, the label `resolveRefs` prints, and the YAML key
  the parser walks to. Three uses of a bare literal is how one of them drifts.
- `resolveRefs` already has the item's index — it is the loop variable over
  `raws` — so it asks for the line by field and index.

**When the line is unavailable it falls back to `NameLine`, and that is stated
rather than silent.** `fieldLine` returns 0 for a path it cannot walk, and
`internal/diag/format.go:69` renders `File:Line` unconditionally, so a 0 would
print `service.yaml:0` — a location that does not exist, in a product whose
thesis is that diagnostic quality is the deliverable. The floor here is
today's behaviour, not corruption: `NameLine` points at the right *file* and
the right *entity*, merely not the right line. A reference list that parsed
into `raws` came from a sequence node that exists, so the fallback should be
unreachable; it is there because a panic on the diagnostic path would turn a
helpful message into a crash, and it is documented at the call site so it
cannot quietly become load-bearing.

Exit codes, check ids and severities are unchanged. This ruling moves a
number.

### R53: a kind-level truth needs a kind-level home — decision required

`internal/scorecard/score.go:149` calls `std.Severity(id, e.Metadata.Tier)`, and
`internal/config/standards.go:93` is `Severity(check string, tier int)`. There
is no kind parameter anywhere on the path, and `standards.default.yaml` has
only a `tiers:` map per check. So a tier-1 entity of kind `API` is **required**
to pass `image-scanned`, `runbook-present` and `otel-present`. A proto contract
directory has no container image and no runtime. All 11 API entities in the
monorepo scored 11% — 1 of 9.

**Exemptions are not the answer, and the reason is structural.** They work
correctly — R4 was confirmed in this run, denominator 9 → 7 for an in-force
exemption, and an expired one warns and waives nothing. But an exemption is
time-bounded by design: it means "we know, we are working on it", and R4's
second half exists to make sure it stops waiving when the date passes. "An API
is not a deployable" is not a temporary condition and must never expire.
Encoding it as ~4 exemption blocks × 11 entities, each carrying an expiry date
somebody has to keep renewing, uses the expiry machinery against its purpose
and produces 44 blocks that all say the same thing.

**Recommended shape: `appliesTo` on the check, not a `kinds:` severity map.**

```yaml
image-scanned: { source: external, appliesTo: [Service, Worker],
                 tiers: {1: required, 2: required, 3: warn} }
```

Three reasons to prefer it over a second severity axis:

- It states the actual proposition. The check is *meaningless* for an API, not
  "waived at a lower severity". A `kinds:` map would need a fourth severity
  value meaning "not counted", overloading a required/warn/info scale that
  currently means one thing.
- **It must leave the denominator**, exactly as R4's in-force exemption does.
  A score of 1/9 where two of the nine cannot apply is a lie in the
  denominator, and the fix that leaves them in it is not a fix.
- Absent, it means every kind, so every existing `standards.yaml` keeps its
  current meaning. The change is additive.

The result carries `StatusNotApplicable`, joining `StatusStale` and
`StatusExempt` (`internal/scorecard/check.go:39,41`) — the vocabulary for "a
check that ran no verdict" already exists. It stays **visible** on the portal,
rendered as not applicable, so a short check list is explained rather than
mysterious.

**This is a one-way door, and it was not taken unilaterally.** `standards.yaml`
lives in the user's repository and is validated by the embedded
`config.StandardsSchema`. Adding a property is additive, but the vocabulary of
kinds becomes load-bearing the day anyone writes one. Ratified after an
`/autonomy-check`, in its own change, separate from R50.

`Severity`'s signature is untouched. An earlier draft assumed the kind would
have to be threaded through it; it does not. `AppliesTo` is a second question
asked of the same `Standards`, which leaves three call sites alone and keeps
"what is this check worth" and "is this check meaningful here" as two
propositions rather than one overloaded one.

**A typo in `appliesTo` is a load error, not a check that quietly evaluates
against nothing.** The kind list is an enum in `standards.schema.json`, so
`appliesTo: [Srvice]` fails at load with the schema's own wording. The failure
it prevents is `standards-unknown-check`'s, reached from the other side: a
scorecard reporting on fewer checks than the team believes. The cost is that
the kind vocabulary is now enumerated in a third JSON schema —
`service.schema.json` already spells it twice — and nothing makes those three
agree. A kind added to `catalog.AllKinds` without touching them is a silent
gap in all three.

**The denominator and the gate must agree, and for one commit they did not.**
`Score` excluded not-applicable from `Applicable` while `EntityScore.Fails` —
what `--fail-on` consults — did not, so an API entity scored 100% and the build
still failed it on `image-scanned`. The two lines appeared in one run's output,
one under the other. `Fails` already excludes `StatusExempt` for exactly this
reason under R4; not-applicable is the same claim about a kind rather than a
date, and the first implementation applied R4's precedent to one of the two
places that had to change. That is the R46/R49 shape again — a property
satisfied in the place it was noticed and not in the place it was also true.
It was found by running the binary, which is the method this repository keeps
having to relearn.

**What this ruling does NOT do: the default standards are unchanged.**
`standards.default.yaml` still asks every kind for `image-scanned`,
`runbook-present` and `otel-present`. Shipping the capability and changing the
default are two decisions, and the second moves the score of every repository
that has not written its own `standards.yaml` — including in the direction of
looking better without anything having improved, which is the one incentive
spec §6 says this product must never create. A team can adopt `appliesTo`
today in their own file, and `landsraad init` writes the default out for them
to edit. Deciding which checks are deployable-only in the shipped default is
its own change, with its own ruling.

### R54: ambiguity is decidable locally; absence is not

`validateCheckResults` (`cmd/landsraad/validate.go:148`) is structure-only by
design. The consequence: an ambiguous bare `entity:` name in
`.landsraad/checks` passes the PR gate and fails later at `score` or `build`.
`score` handles it well — "entity \"event-router\" is ambiguous: it could be
service:event-router or worker:event-router", exit 2 — but the gate that was
supposed to catch it is the one that let it through.

**The objection that this breaks hermeticity does not survive inspection, and
this project has a ruling about exactly that error.** "Structure-only" is a
*mechanism*. The property is that `validate` runs offline and touches no
socket. Resolving a bare name against the catalog already in memory makes no
request. R49 is the precedent: a fix that satisfies a property's mechanism
rather than the property itself, with nothing checking the difference.

**The ruling, and it is already encoded one file over.** `resolveRefs` gates
`dangling-ref` behind `if scope == FullCatalog` (`internal/catalog/graph.go:93`)
because under `LocalOnly` the target legitimately lives in another repository.
Ambiguity has the opposite polarity, and that asymmetry is the whole ruling:

| claim | adding repositories can... | sound locally? |
|---|---|---|
| "`event-router` is ambiguous" | only ever make it *more* ambiguous | **yes, under any scope** |
| "`event-router` is not in the catalog" | resolve it | no — `FullCatalog` only |

So `validate` reports ambiguity at any scope, including `--satellite`, and
continues never to report absence. No new scope concept is invented; the
existing one is applied to a second call site.

In a monorepo the whole catalog is local, which is where this pays: the case
the reporter hit is one `validate` had every fact needed to catch.

**The severity is `warn`, and `validate` keeps exiting 0.** The first draft of
this ruling never said, which made the door question invisible. `score` already
errors and already refuses the artifact, so nothing incorrect ships either way;
what an error here would add is turning a green PR gate red for a condition
that was green yesterday and that was already being caught downstream. A
warning can be escalated to an error in a later release. An error cannot be
walked back without having broken people for nothing. Take the door that stays
open.

R43 is not violated by the two severities differing. Its claim is one
condition, one check id, and both commands now emit `checks-ambiguous-name`
with the same wording from one constructor —
`scorecard.AmbiguousNameDiagnostic` — precisely so they cannot drift apart
describing one file. The severity is the caller's.

**A bare name matching exactly one entity gets nothing from `validate`**, even
though `score` warns `checks-bare-name` and says to write the full ref. That
advice is scope-dependent in the direction this ruling forbids: the name
resolves to `service:api` locally, and a second repository adding
`worker:api` makes the suggested ref the wrong one. Ambiguity is sound locally
because it only grows; a resolution is not, because another repository can
undo it.

**The comment that justified the old boundary was wrong and is corrected.**
`validateCheckResults` said resolving entities "need the merged catalog and a
clock, which would make validate neither hermetic nor offline". Precedence and
ageing need a clock; resolving a bare name needs neither a clock nor a socket,
only the catalog `validate` builds three lines below that call. "Structure
only" was the mechanism, and the property was that `validate` stays offline —
the same mechanism-for-property substitution R49 is about.

## Hygiene: no decision needed

- `internal/generate/codeowners.go`'s doc comment reasons about last-match-wins
  only for nested paths. R50 adds the identical-path case to it, because the
  comment is what the next reader will trust.
- `hermetic.go:229`'s `"%s has no index.md"` is the message R51 rewords. It is
  listed separately because the exact-message hook will require the test
  updated in the same change.

## What the run confirmed, and this spec does not touch

`spec.path` existence checking caught a fabricated path immediately.
Kind-scoped entity URLs let `service:x` and `worker:x` coexist cleanly — the
property R50's root cause analysis turns on. External check ingestion and the
stale clock read well in anger ("reported pass 52 days ago by
ci/dynatrace-dashboard-check, older than the 14-day limit"), and R3 held: stale
stayed in the denominator. R19's diagram cap fired at 75 entities with a clear
explanation.

`sort.Slice`'s instability in `codeowners.go` was suspected and disproved — 15
consecutive runs, identical md5 — and is left alone. See the correction above.

## Order

1. **R50** — the highest-priority defect, and the only one whose fix is fully
   specified and settled. Ships alone.
2. **R52** — contained, mechanical, no user-visible contract changes.
3. **R51** — four coordinated sites; needs `fetch`'s planner in the same change
   or it trades a wrong answer for a landsraad-bug diagnostic.
4. **R54** — small, and reuses an existing scope distinction.
5. **R53** — `/autonomy-check` first. Schema change to a user's file.

## What every task inherits

The non-negotiables in `CLAUDE.md`: no `os` under `internal/`, no package state
below `cmd/`, exact message assertions, no network outside `internal/fetch`,
gofmt- and vet-clean.

One addition specific to this branch, from R49's transferable rule: **a test
for a diagnostic that should have been added asserts the complete output**, not
the presence of a named check. R50's defect is precisely "a diagnostic that
should exist and does not", and a subset assertion is structurally incapable of
detecting it. Every test written for R50 compares the whole rendered
diagnostic stream.
