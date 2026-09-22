# Monorepo defects — design

Branch: `fix/monorepo-defects-r50-r54`.

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
deliberately — but it is wider than this section first said.** The check lives
in `CODEOWNERS()`, and an earlier draft claimed "which only `gen` and `build`
call". `build` does not call it: `generate.CODEOWNERS` has exactly one
non-test caller, `cmd/landsraad/gen.go`, inside `artifacts()`, whose only
caller is `gen` itself, and `cmd/landsraad/build.go` names neither.

So a team whose CI runs `validate` on pull requests and `build` on main — the
shape this product is designed around — never sees `codeowners-duplicate-path`
at all. Their portal renders happily while the CODEOWNERS file in the
repository silently voids a team's review rights. The diagnostic fires only
when somebody runs `gen` or `gen --check`.

That does not change the decision to defer the validate-side twin, but it was
a decision taken against a false picture of how many commands are blind, and
the corrected picture belongs next to it. That is the same shape as R54 and is not fixed here: `validate` has its
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

### R55: front matter is not Markdown, and R51 put it on the busiest page

landsraad never stripped YAML or TOML front matter. A docs tree written for a
static site generator carries a metadata block the generator consumes and
Markdown does not, and goldmark renders it as body text. There is no
front-matter handling anywhere under `internal/render/md` — `extension.GFM`
and `extension.Footnote` are the whole extension set for this purpose. In the
monorepo that prompted R51, 203 of 203 files under the docs tree carry a
block.

**This is not R51's defect, and R51 is what makes it matter.** While
`_index.md` was not recognised as an index, the damage sat on secondary
documentation pages. R51 hoists the index onto the entity page, so the block
lands at the top of the most-read page landsraad produces. Verified against a
binary built from this spec's own merge commit, on a Hugo-shaped `_index.md`:

```
Documentation title: "Resort Service" description: "Details about the resort
service" lead: "" date: 2024-04-03T09:00:00+00:00 draft: false weight: 10020
The real overview prose starts here.
```

R51 as shipped therefore traded "no overview" for "front-matter dump", for
exactly the repositories it was written to serve. That is a regression in the
thing users look at, and it was found by a second pass from the session that
reported the original five defects — not by this one, and not by the
composition audit, both of which had the merge commit in hand.

**The ruling.** `md.Render` strips a leading front-matter block before the
parser sees the source. Every caller goes through `Render`, so documentation
pages, runbooks and the hoisted index are stripped alike — the same
one-definition reasoning as R51's own `catalog.DocsIndex`.

**The values are discarded, not used.** Feeding `title` into the page title
would suit a Hugo tree, whose `_index.md` often has no H1 at all because the
title lives in the block, so a stripped page falls back to the filename. But
that changes the title of every documented page and the row each contributes
to `search-index.json`. That is a visible change to the published artifact and
a separate decision from stopping the leak; this ruling does the second only.

**The delimiter rule, and the direction it errs in.** `---` alone on the first
line is also a valid thematic break, and `---` on the *second* line is a setext
H1 underline. So the opening delimiter must be the whole of line one, and a
closing delimiter (`---` or `...` for YAML, `+++` for TOML) must actually
appear. An unterminated `---` is a rule and is left alone.

That asymmetry is deliberate. Failing to strip a block renders ugly and is
obvious in the output; over-stripping silently eats a document's first
section, and the reader has no way to tell it is missing. Both directions are
pinned by tests.

### R56: a document that says nothing is not documentation

A second pass against the 75-entity catalog, run from the merge commit, found
that R51 buys that repository far less than either session assumed. All 18 of
its application `_index.md` files are front matter and nothing else — body
characters after the block: zero, eighteen times. They are Hugo **section
stubs**, which exist to give a section a title and a weight; the prose lives
in a sibling `overview.md`.

So the sequence there was: before, the entity page hoisted nothing because the
index was unrecognised; after, it hoists the recognised index, which is empty.
The overview is still blank. What changed is that the stubs left the
documentation link list and stopped being published, 65 pages to 48.

**Nothing in that is wrong.** R51 and R55 each do exactly what they say. What
it exposes is an assumption underneath **R18**: hoisting the index assumes the
index carries the overview prose. In Hugo's `_index.md` convention it
frequently carries none.

**The ruling has two halves, and the second is the one that matters.**

**First: landsraad does not guess which other file is the overview.** Falling
back to `overview.md`, or to `README.md`, hardcodes a second filename
convention — which is precisely the error R51 had just finished correcting.
One wrong guess about a filename is what this whole branch exists to fix;
adding a second guess as the remedy is not a remedy. A team that wants prose on
the entity page has two levers that are already landsraad's own rather than a
generator's: put prose in the index (Hugo permits it), or write
`metadata.description`, which is schema'd, validated and kind-independent. The
entity template already guards `{{- if .Index}}`, so an empty index renders no
empty block and no stray page — verified, and no change is needed there.

**Second: `docs-fresh` was certifying empty documentation, and now does not.**
It only `Stat`ed the index. A front-matter-only stub therefore counted as
documentation, and the check went on to score the *freshness* of a file with
nothing in it. `runbook-present` has refused exactly this shape since it was
written — "counting it as a pass is how a scorecard comes to certify a runbook
nobody wrote — the rot this product exists to make visible, certified by the
product" — and the docs index now gets the same rule and the same family of
message.

This is the finding the hoist question was standing in front of. The hoist
being empty is cosmetic; the scorecard calling an empty file documentation is
the product lying, and for this repository it was lying about 18 services.

**The same defect existed one file over, unnoticed.** `bodyIsEmpty` read every
front-matter line as content, so a runbook that was front matter and nothing
else passed `runbook-present`. Fixing `docs-fresh` alone would have left the
two checks using different definitions of "says nothing" — the two-answers
state R51 exists to eliminate, reintroduced inside one package.

**`internal/mdtext` is where the shared answer lives.** Two packages need it:
the renderer strips front matter so it is not printed as body text (R55), and
the scorecard cannot decide whether a document says anything without stripping
the same block first. The alternative was `internal/scorecard` importing
`internal/render/md`, which inverts the pipeline — stage 7 reaching into stage
8 — and drags goldmark into a stage with no use for it. No `internal/*`
package imports `internal/render` today and this ruling does not make one the
first. `mdtext` has no dependencies at all.

**Known cost.** A heading-only index now fails where it passed, which is the
intent, and it changed one existing test's fixture — `TestDocsFreshAsksPerRepository`
used `# Docs\n` as a stand-in while testing something else entirely. The
fixture was given prose rather than the rule being weakened.

## Correction: `_index.html` was never orphaned

An earlier account of R51's renderer half — in the message reporting it, not
in this document — said the un-hoisted `_index.md` was "published as an
orphaned `docs/_index.html`". It was not. The entity page links it from the
Documentation list all along; verified against a pre-R51 binary, whose output
carries `href="docs/_index.html"`. It was reachable and mislabelled, listed as
a document named `_index`.

The half that was right is the half that mattered: no overview is hoisted onto
the entity page. Recorded because the error has a pattern behind it — it is
the third time on this branch that a claim was made from the shape of a grep
result rather than from what the hits said.

## What the composition audit found

Run before merge, as `CLAUDE.md` requires. It produced one behavioural defect,
one false claim in a comment, and a set of accuracy failures. All are fixed on
this branch except the two at the end, which need a decision.

**R53 created a third way for an exemption to waive nothing, and
`exemptions()` knew two.** Its own doc comment enumerated "expired, or naming
a check that does not exist" and called such an exemption "worse than no
exemption at all: the author believes they are covered". An exemption naming a
check the entity's kind excludes is exactly that, and `exemptions()` had both
`e.Kind` and `std` in hand without asking. Worse, the expired branch still
fired: the tool told a team to renew a waiver for a check it had, three lines
earlier, declared can never apply to that kind. This is the population R53 was
written for — narrowing a check with `appliesTo` and leaving the old exemption
blocks in place is the natural migration order. Fixed with a distinct
`exemption-not-applicable`, ordered before the expiry check; a distinct id
because R43 is one condition one id, and "the kind excludes this" has a
different fix from "this id is misspelt".

**This is the second time R53 satisfied a property where it was noticed and
not where it was also true.** First `Fails` against the denominator, now
`exemptions` against both. The shape is worth naming: `StatusNotApplicable`
is consulted in three places that must agree, and the type checker enforces
none of them.

**R52's "the fallback should be unreachable" was false.** A YAML alias
(`dependsOn: *shared`) is an `AliasNode`, `seqItemLines` required a
`SequenceNode`, and the decoder resolves the alias — so `Spec.DependsOn` was
populated while its lines were not, and the diagnostic fell back to `NameLine`
for exactly the files that use an anchor. Harmless in effect, but the comment's
stated purpose was that the fallback "cannot quietly become load-bearing", and
it was. Fixed by following the alias, which yields a better citation than the
fallback; the comment now says "no parsed entity is *known* to reach it", with
the word doing real work.

**`DocsIndexNames` was exported ceremony and is deleted.** Its only callers
were two tests, one of which existed solely to assert the copy-on-read property
of the function justifying it. `internal/catalog` is the most-imported package
in the tree, so every exported symbol there is a promise — and this one
promised "the spellings are enumerable", which is how a second answer to
"which file is the index" gets reintroduced one ruling after R51 eliminated it.

**The `appliesTo` kind enum is now guarded against drift.**
`standards.schema.json` is a third copy of a list Go owns, and
`internal/schema` already had `TestSchemaKindsMatchGoKinds` for its copy. The
spec named this cost and left it unenforced when the enforcement was four
lines. Without it, adding a kind ships a landsraad that accepts it everywhere
in a catalog and rejects it in `standards.yaml`, with the same wording a
genuine typo produces — so the user cannot tell which it is.

**Smaller accuracy failures, all fixed:** two comments counting "six values,
not two" in a status vocabulary that now has seven — one of them falsified by
the very hunk that added the rule beneath it; eleven doc comments in
`internal/render` still asserting the index is `docs/index.md`, one of which
sent readers grepping for an "index.md special case" that no longer exists in
the function it names; `not-applicable` overflowing the CLI's 13-character
status column and appearing in the list a reader scans for things to fix, which
is the one list it is by definition not in; and `Entity.RefLines`, an exported
mutable map on a pointer type handed to three packages, in a codebase that
copies `allKinds` and `docsIndexNames` to avoid precisely that — now
unexported behind its existing accessor.

### Deferred, and needing a decision

**The portal describes the standard in one place and `appliesTo` is not in
it.** `internal/render/scorecard.go` builds each row from `Checks()`,
`IsExternal` and `Severity`, and nothing under `internal/render` calls
`AppliesTo`. So `/scorecard/` tells every reader that `image-scanned` is
`required` at tier 1, unqualified, while `/entity/api/payments/` renders the
same check as one row reading `image-scanned | not-applicable | required`. Two
pages of one portal disagreeing about one check, and one row contradicting
itself — the failure R51 exists to eliminate, reintroduced one ruling later.
The matrix is the published description of the standard and `appliesTo` is now
part of that standard, so the fix is to render it there rather than to blank
the severity cell. That is real design work on the published artifact and it
gets its own ruling.

**`appliesTo` ships with no user-facing documentation.** It appears in the
schema, the loader, the tests and this spec, and nowhere a user reads.
`README.md` does not document `standards.yaml`'s fields at all, and
`standards.default.yaml` — the file `landsraad init` writes into a user's
repository — carries no comments. A capability nobody can find has shipped the
cost of a one-way-door schema addition without the benefit. A `README.md` line
is uncontroversial; commenting `standards.default.yaml` changes bytes landing
in a user's repository, which is a one-way door in its own right.

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
3. **R51** — three coordinated sites, all under `internal/`, sharing one
   resolver in `internal/catalog`.
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
