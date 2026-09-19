# landsraad — a lightweight developer portal for small teams

**Status:** approved design, pre-implementation
**Date:** 2026-09-08
**Scope:** v1 only. Deferred sub-projects are listed in §2.

---

## 1. Summary

`landsraad` is a single Go binary that reads service metadata colocated with code,
validates it, scores it against a team standard, and renders a static portal.

It answers the questions Backstage answers — who owns this, where are its docs,
runbook, dashboards and alerts, what depends on it — for a team of 5–15
engineers with one monorepo or a handful of repos, without a platform team and
without a framework to maintain.

The metadata is the *source* for CODEOWNERS and alert routing, never a copy, so
letting it rot breaks something visible.

### 1.1 Success metrics

Without these there is nothing to falsify, and no way to tell the "worked but
nobody cared" outcome from the intended one.

| Metric | Baseline | Target (6 months) | Measured by |
|---|---|---|---|
| Time for a new hire to find owner + runbook + dashboard for any service | minutes of asking | < 60 s, unassisted | onboarding observation |
| Services with complete metadata (owner, tier, runbook, dashboard, alerts, SLO) | unknown | 100 % | scorecard, enforced in CI |
| Scorecard average | — | ≥ 90 % tier-1, ≥ 75 % overall | portal |
| Incidents where on-call had to hunt for owner or runbook | count from postmortems | 0 | postmortem tag |
| Portal freshness | — | < 5 min after merge | build timestamps |
| Maintenance effort | — | < 2 h / month | time tracking |
| Adopting teams still running it after 90 days | — | ≥ 3 | ask them |
| Catalog size at which the ownership forcing-function bites | — | stated, not assumed | see below |

**The honest caveat on the forcing function.** Goal 2 claims that because
CODEOWNERS and alert routing are *generated* from the catalog, letting the
metadata rot breaks something visible. That is true only when a wrong owner
routes to a *different human*. At 5–15 engineers with one or two rotations, it
frequently does not: `gen --check` catches metadata that is **stale relative to
the catalog**, never metadata that is **wrong relative to reality**. The
mechanism bites somewhere above three rotations or roughly 25 services; below
that, the truth-rot checks (`owner-active`, `docs-fresh`) are doing the work,
not the generated artefacts. This is a known limit, not a solved problem.

---

## 2. Scope

The originating design doc described eight separable sub-projects. This spec
covers four; the rest get their own specs.

| | Sub-project | In v1 |
|---|---|---|
| A | Catalog core — schema, loader, validation, dep graph, multi-repo merge | yes |
| B | Generated artifacts — CODEOWNERS, alert routing, Slack map | yes |
| C | Scorecard — checks, tier severity, scoring, history | yes |
| D | Portal renderer — static site | yes |
| E | Runtime agent — `runtime.json` producer | no |
| F | Golden path — templates, `task new-service` | no |
| G | Mode runner — declarative cheap agents | no |
| H | Claude Code plugin — skills, routing hook | no |

A is the spine; B, C and D hang off it. G and H share nothing with A–F but a
catalog read, and have a different runtime and risk profile (provider keys, MCP
servers, token metering). They belong in a separate repo.

---

## 3. Decisions and rationale

Recorded because the *why* is the part that gets lost.

| # | Decision | Rationale |
|---|---|---|
| D1 | Go-native rendering (goldmark), not MkDocs | Portal pages are a generated app, not docs pages. Rendering them through MkDocs means Go *and* a pinned Python toolchain in every CI run and on every contributor's machine. `go install` beats `pip install mkdocs-material` for adoption. Backstage-migration risk is low: TechDocs needs `mkdocs.yml` + Markdown in `docs/`, our docs are already plain Markdown, and the yml is a generated file. |
| D2 | GFM + admonitions dialect | goldmark GFM (tables, footnotes, task lists) + Chroma + Mermaid + one custom extension for MkDocs-style `!!! note`. Runbooks genuinely use callouts, and it stays paste-compatible with docs written for MkDocs. Tabs and snippet-includes are excluded: they break GitHub rendering, and these files are read in the repo too. |
| D3 | Hermetic checks in-binary, expensive checks ingested | `landsraad score` must run offline in under a second with no Docker daemon and no network egress. Checks needing a build, a scanner or an HTTP probe are *reported into* the tool via `.landsraad/checks/*.yaml` written by the CI jobs that already know the answer. The extension point is YAML, not a plugin API — consistent with the non-goal of a plugin system. |
| D4 | Go checks + YAML severity matrix | Checks are Go funcs with stable ids; `standards.yaml` holds only the tier→severity matrix and thresholds. Type-safe, precise messages, nothing to debug in YAML. A CEL expression language was rejected as an API surface with no demonstrated demand. |
| D5 | GitHub + GitLab host API adapters | User decision, taken over a recommendation for a host-agnostic fetcher. Consequence accepted and recorded as a non-goal (§15): Gitea, Forgejo, Bitbucket and self-hosted git are unsupported in v1. Fetching sits behind a Go interface so a generic fetcher is additive later. |
| D6 | Flat entity names, unique per `(kind, name)` | `metadata.name` unique across the merged catalog; refs are `kind:name`. Repos, not teams, are the namespacing axis, and at a handful of repos collisions are a two-minute rename, not a migration. Decisive factor: flat → namespaced is a non-breaking additive change later (Backstage's default namespace is literally `default`), while namespaced → flat is breaking. On a one-way door, take the door that stays open. |
| D7 | Strict schema (`unevaluatedProperties: false`) | Unknown fields are rejected, not ignored. `unevaluatedProperties` rather than `additionalProperties` because the latter, at the root, makes a field introduced inside a conditional branch *unevaluated* and therefore rejected — foreclosing per-kind fields forever. Switching is free today and a behaviour change once files exist in the wild. |
| D12 | `metadata.aliases` from v1 | Names are not identities. Without aliases a rename breaks every `dependsOn` in every other repo, rewrites CODEOWNERS and alert routing, and silently restarts `scorecard-history.csv` — the one artefact with no way to notice it broke. Aliases make a rename additive. |
| D13 | `kind: Resource` plus free-string `spec.type` | A fixed seven-kind enum has no home for an S3 bucket, an SQS queue, a Redis cache or a Terraform module, so users model them as `Database` and the lie flows into the tier matrix, the dependency graph and the portal. For a product whose thesis is "the metadata is the source", users lying in the data is the worse failure. Backstage converged on the same shape. |
| D8 | Everything starts in `internal/` | `internal → pkg` is additive; `pkg → internal` is breaking. Catalog types get promoted when someone actually asks to import them. |
| D9 | Dune naming on user-facing surfaces only | Project, binary and deployed components carry Dune names; Go packages are literal (`catalog`, `scorecard`, `render`). Themed package names tax every future contributor with a glossary. |
| D10 | Named `landsraad`, not `sietch` | `sietch` was the first choice and failed an availability check on 2026-09-08: `danprince/sietch` is an existing **Go Markdown static site generator** — same language, same niche — alongside a 141-star storage project, three Go modules, and sietch.dev/.io/.sh/.org all registered. `landsraad` has zero Go modules and no namesake above one star, and is semantically closer: the assembly of the Great Houses is a federated register of who owns what. `apiVersion` needs no domain (k8s uses `apps/v1`), so no domain sits on the critical path. |
| D11 | Composition over a monolithic pipeline | File access goes through `io/fs.FS`, output formats through a `diag.Formatter` interface and a map literal, and each pipeline stage is a typed pure function that two commands compose differently. See §3.1. |

### 3.1 Composition principles

Three properties are required of this codebase, and each is bought by a
specific mechanism rather than by good intentions.

**Flexibility — parts are replaceable without rewriting their caller.**

- All file access is `io/fs.FS`, never `os` calls against a path string.
  `os.DirFS(root)` reads a local checkout, a tar reader reads a fetched
  remote repo, and `fstest.MapFS` reads a test fixture. Discovery, parsing
  and file checks are written once and work against all three.
- Schema validation is a `*schema.Validator` value, not a package-level
  function over `sync.Once` global state. Two schema versions can coexist
  during a migration, and a test can validate against its own schema.

**Loose coupling — a change in one part does not propagate.**

- Each pipeline stage (§7) is a typed pure function: `Discover` takes an
  `fs.FS` and returns paths; `ParseAll` takes paths and returns entities;
  `New` takes entities and returns a `Catalog`. A stage knows its input and
  its output type and nothing else — not the command that runs it, not the
  stage before it.
- `validate` and `build` are two explicit compositions of those same
  functions. Adding a stage to one does not touch the other, and Go's type
  checker refuses a composition that runs `New` before `ParseAll` — that
  ordering bug is a compile error, not a nil dereference at runtime.

  The claim holds where a stage consumes the *return type* of the one before
  it, and only there. It does not follow from a type being unexported-ish or
  from a comment asserting it: `Graph` has a usable zero value, so
  `(&Graph{}).Cycles()` compiles and answers "no cycles" — defensible, since
  a graph with no edges has none, but conventional rather than enforced. Where
  the wrong construction gives a *wrong* answer instead of an empty one, make
  it impossible: `Catalog`'s fields are unexported because a keyed literal
  used to build a catalog whose index was nil, in which entities could not
  find themselves and `Resolve` invented dangling references for entities that
  were right there.
- Nothing does its own IO or its own printing. Stages take an `fs.FS` and a
  `*diag.Collector`; rendering happens once, at the edge.

**Writes belong to the command layer.** `io/fs.FS` is read-only by design.
Plan 2's `gen` and Plan 3's `EMIT` produce files, and the command layer decides
where bytes land. Without stating this, the "no `os` below `cmd/`" rule gets
quietly weakened the first time someone needs to write a file — and an absolute
rule that has to be broken teaches contributors to ignore the rules.

An `io.Writer` is one stream, which covers a single artifact and not a tree.
`EMIT` produces `dist/`: a page per entity, a search index, CODEOWNERS, a
history file. A contributor writing `internal/render/` would reach for
`os.MkdirAll`, be blocked, and then either move the renderer into `cmd/` —
losing the pure-stage discipline — or weaken the rule. So:

> **A generator is a pure function returning the files it would write** —
> `[]File{Path, Data}`, paths slash-separated and relative to a root it never
> names — and `cmd/` owns the one loop that puts them on disk.

`internal/scaffold` is the first instance, backing `landsraad init`. Its
correctness property — what init writes already validates — is therefore
checked against an in-memory filesystem rather than a temporary directory.
`File` stays local to that package until Plan 2's generators give it a second
producer, which is when the type is promoted rather than guessed at.

**Reusability — one component serves unrelated callers.**

- `Discover`, `ParseAll` and `CheckFiles` are used by `validate` against a
  local checkout and by `build` against every fetched repo. One
  implementation, three filesystems, no branching on which.
- Output formats implement `diag.Formatter`. The four the spec requires (text,
  JSON, GitHub annotations, GitLab Code Quality) are four small types; a fifth
  is a new type plus one line in `diag.Formatters()`. There is deliberately no
  registry *type* — a map is enough, and an abstraction with a single instance
  whose only consumer is a test that invents its own subject is speculative
  generality.
- **The graph is a value produced by `Resolve`, not state on `Catalog`.**
  `Cycles`, `DependsOn` and `Dependents` are methods on `*Graph`, so asking for
  cycles before resolving does not compile. With the edges on `Catalog` the
  claim above was false for the one ordering constraint that is not obvious:
  `cat.Cycles()` compiled and silently returned zero cycles.

**What this deliberately is not.** "Replaceable at runtime" means the
composition is *selected* at startup from configuration — not that code is
loaded dynamically. Go's `plugin` package and subprocess plugins are both
out of scope, consistent with the no-plugin-system non-goal in §2.
Abstractions are added when a second implementation exists or is already
committed to in a later plan, never in anticipation of one.

---

## 4. Naming

| Component | Name | In v1 |
|---|---|---|
| Project and binary | **landsraad** (alias `lsr`) | yes |
| Validator | Truthsayer | yes |
| Scorecard engine | Mentat | yes |
| Multi-repo fetcher | Carryall | yes |
| Dependency resolver | Navigator | yes |
| CI gate | Sardaukar | yes |
| Templates | Missionaria | deferred (F) |
| Runtime agent | Suk | deferred (E) |
| Mode runner | Erasmus | deferred (G) |

Names appear in docs, subcommand help, and deployed component names. They do
not appear in package or type names.

**Verified 2026-09-08.** `landsraad`: zero Go modules on pkg.go.dev, 14 GitHub
repos of which none exceeds one star. Module path `github.com/<org>/landsraad`.
The nine-letter name carries an `lsr` alias for daily use.

`Heighliner` was rejected as the fetcher name: `manifoldco/heighliner` is a
281-star "continuous delivery to Kubernetes" tool with 18 Go modules — actively
confusing in a k8s-adjacent project. `Carryall` is the Dune aircraft that hauls
harvesters in and out: same metaphor, no collision.

---

## 5. Catalog schema

One `service.yaml` per deployable unit and per shared resource, colocated with
the code it describes.

```yaml
apiVersion: landsraad/v1
kind: Service            # Service | Worker | Cron | Library | Topic | Database | API
metadata:
  name: payments-worker  # (kind, name) unique across the merged catalog
  description: Consumes payment events and settles them.
  owner: team-payments   # must exist in teams.yaml
  aliases: []            # former names; references resolve through them
  tier: 1                # required for Service/Worker/Cron/API only
  labels: {}             # short key/value, for selection
  annotations: {}        # free key/value: Grafana folder, AWS account, cost centre
  lifecycle: production  # experimental | production | deprecated
  tags: [go, kafka]
spec:
  language: go
  path: services/payments-worker
  docs: services/payments-worker/docs
  runbook: services/payments-worker/docs/runbook.md
  oncall: https://pagerduty/schedules/PAY
  repoUrl: https://github.com/org/monorepo/tree/main/services/payments-worker
  links:
    - { title: Dashboard, url: https://grafana/d/pay-worker, type: dashboard }
  dependsOn: [topic:payments.events, database:payments-pg, service:ledger-api]
  providesApis: []
  type: ""                                      # free string, as Backstage's spec.type
  slo:
    - { name: settle-latency-p99, target: "500ms", window: 30d }
  exemptions:                                   # waive a check WITH a reason
    - { check: runbook-present, reason: "nightly backfill, no on-call path", until: 2027-01-01 }
  alerts: services/payments-worker/alerts.yaml
  runtime:
    selector: { app.kubernetes.io/name: payments-worker }
```

**Paths.** `spec.path`, `docs`, `runbook` and `alerts` are repository-relative
and slash-separated, and every one that is set must exist. Only `docs` must be
a directory.

`spec.path` is the anchor — it names the code the entity describes, and it is
the join key for generated CODEOWNERS (§11). That makes it the one path field
whose typo is silent rather than visible: a wrong `runbook` breaks a link
someone notices, while a wrong `path` emits a CODEOWNERS line for a directory
that does not exist, which git ignores without complaint, leaving the real
directory unowned. So it is checked for every kind that sets it, and checked
for existence only — a `Library` may legitimately name a single file, and
CODEOWNERS patterns match files as happily as directories.

An entity whose code does not live in the repository being validated — an
externally managed `Database`, a third-party `API` — **omits** `path` rather
than pointing it at something plausible. The field is optional precisely so
that "there is no code here" has an honest spelling.

**Backstage compatibility — what is actually true.** `kind`, `metadata`,
`dependsOn` and `providesApis` borrow Backstage's *field names*. The claim
stops there, and earlier drafts of this spec overstated it:

- landsraad puts `owner`, `tier` and `lifecycle` in **`metadata`**; Backstage
  requires `owner` and `lifecycle` in **`spec`**. The placement is the opposite.
- Backstage requires `spec.type` on Component and Resource. landsraad has the
  field (D13) but does not require it, and its eight kinds map many-to-one onto
  Backstage's kinds, so a converter must synthesise the value wherever it is
  absent.
- Backstage's `API` kind requires a non-empty `spec.definition`. landsraad has
  no field for it and `unevaluatedProperties: false` forbids adding one, so
  `kind: API` entities cannot be converted at all today.
- Backstage refs are `[<kind>:][<namespace>/]<name>`. Rewriting
  `topic:payments.events` into `resource:default/payments.events` requires
  knowing the *target's* kind — a catalog-wide transform, not a per-file one.

So: migration is a **catalog-wide converter**, not a 20-line per-file script,
and it is not lossless for `kind: API`. The shared field names still make it
tractable, and `/` is absent from the name pattern so the namespace slot stays
reserved. Closing the remaining gaps is a decision for a later revision, not
something this spec should claim is already done.

**Provenance** — `SourceRepo` and `SourcePath` are attached at parse time, not
present in the file. They exist so collision and dangling-ref errors can name
both sides.

`schema/service.schema.json` is `go:embed`ed for validation and published via
`landsraad schema` for editor autocomplete (yaml-language-server).

---

## 6. Supporting config

**`teams.yaml`** — the source for CODEOWNERS and alert routing. Plan 2's
generators consume exactly this shape, so it is a reviewed contract rather than
an implementation detail:

```yaml
teams:
  - name: team-payments      # what service.yaml `owner` refers to
    members: [alice, bob]    # CODEOWNERS entries
    slack: "#payments"       # deploy notifications
    pagerduty: PAY           # alert routing target
```

Unknown keys are rejected. A silently-ignored `pagerDuty:` typo would make
alert routing rot invisibly, which is the exact inverse of Goal 2.

**`repos.yaml`** — which repos and paths make up the catalog.

```yaml
repos:
  - url: https://github.com/org/monorepo
    paths: [services/*, workers/*, libs/*]
  - url: https://github.com/org/edge-gateway
    paths: [.]
```

**`standards.yaml`** — the tier×severity matrix, the only scoring knob.

```yaml
apiVersion: landsraad/v1
kind: Standards
spec:
  staleAfterDays: 14
  checks:
    owner-set:       { tiers: {1: required, 2: required, 3: required} }
    runbook-present: { tiers: {1: required, 2: required, 3: warn} }
    alerts-parse:    { tiers: {1: required, 2: required, 3: info} }
    slo-defined:     { tiers: {1: required, 2: warn, 3: info} }
    docs-fresh:      { params: {maxAgeDays: 180},
                       tiers: {1: warn, 2: warn, 3: info} }
    dashboard-resolves: { source: external, tiers: {1: required, 2: warn, 3: warn} }
    image-scanned:      { source: external, tiers: {1: required, 2: required, 3: warn} }
    otel-present:       { source: external, tiers: {1: required, 2: warn, 3: info} }
    deps-declared:      { source: external, tiers: {1: required, 2: required, 3: warn} }
```

Severities: `required` | `warn` | `info` | `skip`.

**`.landsraad/checks/*.yaml`** — results reported in by CI for `source: external`
checks.

```yaml
apiVersion: landsraad/v1
kind: CheckResults
producer: ci/image-scan
generatedAt: 2026-09-08T14:00:00Z
results:
  - { entity: service:payments-worker, check: image-scanned, status: pass,
      detail: "0 critical, 2 medium (trivy 0.55)", url: "https://ci/run/1234" }
```

This file is written by CI jobs **in other people's repositories**, which makes
it the most expensive contract here to change later. Three rules that are cheap
now and permanent support load if left undefined:

- **`entity` is a ref** (`kind:name`), not a bare name — every other reference
  in landsraad is a ref, and a bare name is genuinely ambiguous once
  `service:orders` and `topic:orders` may coexist. A bare name is accepted as a
  deprecated alias, resolved only when unambiguous.
- **`generatedAt` is RFC 3339 with an explicit offset.** The `staleAfterDays`
  arithmetic depends on it.
- **Precedence** when two producers report the same `(entity, check)`: newest
  `generatedAt` wins, and a tie is an error rather than a coin flip.

Staleness is wall-clock, not per-commit: a scan of an older commit looks
fresher than it is. Adding a `commit:` field later is additive.

`status` is `pass` | `fail` | `error`. A result older than `staleAfterDays`
renders as **stale**, not pass — an image scan from March is not evidence about
today.

Two distinct staleness clocks, deliberately: `spec.staleAfterDays` (days) ages
out *ingested check results*; `docs-fresh.params.maxAgeDays` ages out *service
documentation*. They answer different questions and are tuned independently.

---

## 7. Pipeline

```
1 DISCOVER  glob service.yaml from repos.yaml paths           → []FileRef
2 FETCH     pull remote repo trees (GitHub/GitLab adapter)    → local cache
            The cache is content-addressed on the git blob SHA, at
            .landsraad/cache/blobs/, with no automatic pruning in v1 (R27).
3 PARSE     YAML → Entity, strict schema, line numbers kept   → []Entity + diags
4 MERGE     one Catalog; detect name collisions               → Catalog
5 RESOLVE   resolve kind:name refs, build graph, find cycles  → Graph
6 INGEST    read .landsraad/checks/*.yaml, apply staleness       → []ExternalResult
7 SCORE     hermetic checks + ingested, apply standards.yaml  → Scorecard
8 EMIT      site / CODEOWNERS / routing / history.csv         → dist/
```

Stages are pure functions where practical, each independently testable.

### 7.1 The two entry points

| Command | Stages | Network | Run by |
|---|---|---|---|
| `landsraad validate` | 1, 3, 4, 5*, 6† | none | every service repo's PR CI |
| `landsraad build` | 1–8 | yes | platform repo, on merge to main |

Stage 6 (INGEST) is only **partly** part of `validate`, which is what the
table's `6†` says. Its *semantic* half is not: resolving entities, applying
precedence and ageing results need the merged catalog and a clock, and
`validate` is hermetic and offline so it can run in every service repo's PR CI
with no tokens.

Its *structural* half is. Plan 2 shipped the `CheckResults` schema, so
`validate` checks the shape of every `.landsraad/checks/*.yaml` — which is
hermetic, and closes the gap this section previously recorded: a malformed
results file is now caught by the PR that introduced it rather than by the
platform build days later.

`*` — refs pointing outside the current repo are **recorded, not resolved**.
Cross-repo references resolve at merge time only. A service repo's CI therefore
needs no tokens and no network, and cannot be broken by an unrelated team's
repo. Under `build`, dangling refs and dependency cycles are hard failures.

`†` — only stage 6's *structural* half runs: the shape of every
`.landsraad/checks/*.yaml` is checked, which is hermetic. Precedence and
ageing need the merged catalog and a clock, so they wait for `build`.

---

## 8. Commands

```
landsraad validate [--format]            local, hermetic, no network, no tokens
landsraad build [-o dist] [--allow-partial]
landsraad score  [--format] [--fail-on required|warn]
landsraad gen    [--check]               CODEOWNERS, alert routing, Slack map
landsraad serve  [--watch]               local preview
landsraad schema                         print JSON Schema for editor setup
landsraad version
```

`score --fail-on` defaults to `required`: only checks marked `required` for
that service's tier gate the build. `--fail-on warn` additionally gates on
`warn`, for a team that wants to ratchet.

`gen --check` regenerates to a temp dir and diffs against what is committed,
exiting non-zero when stale. This is the mechanism that makes metadata rot
break something visible.

Build, test, lint and release are driven by `Taskfile.yml` (go-task).

---

## 9. Scorecard

Score = passed / applicable, per service, per team, and appended weekly to
`scorecard-history.csv` by the CI job.

**Hermetic checks (computed in-binary):** `owner-set`, `runbook-present`,
`alerts-parse`, `slo-defined`, `docs-fresh`.

**External checks (ingested):** `dashboard-resolves`, `image-scanned`,
`otel-present`, `deps-declared`.

An external check with no reported result renders as **not reported**, which is
distinct from both pass and fail.

**Known cost of D5:** `docs-fresh` needs a last-edit date. In the local repo
that is `git log`, free. For repos fetched over a host API there is no git
history, so it costs one `GET /commits?path=…&per_page=1` per service. Tolerable
at a dozen services; documented in the call budget rather than silently dropped
for remote repos.

---

## 10. Portal

Static HTML generated at build time. No JS framework. Dense, left-aligned,
monospace reserved for versions.

- **Catalog** — all entities, filterable by kind/team/tier/tag, sortable by score
- **Service** — header, links, runtime badge, Mermaid dependency graph, scorecard, then rendered `docs/`
- **Team** — members, on-call, owned services, aggregate score
- **Scorecard** — the standards table with weekly trend
- **Dependency map** — whole-system Mermaid graph from `dependsOn`

**Search** — a build-time `search-index.json` over titles, headings and body
text, queried by a small vanilla-JS client. No server component.

**Runtime** — the page fetches `runtime.json` client-side and degrades to
"runtime unknown" when it is absent. v1 does not produce this file; building
the producer (E) later requires no portal change.

---

## 11. Generated artifacts

Derived from the catalog, never hand-edited, verified by `landsraad gen --check`:

- `CODEOWNERS`
- Alertmanager / PagerDuty routing, via `owner` → `teams.yaml`
- Slack channel map for deploy notifications

---

## 12. Error handling

**Accumulate, never fail fast.** Validating twelve services reports twelve
problems in one run.

```go
type Diagnostic struct {
    Severity      Severity // error | warn | info
    Repo, File    string
    Line          int      // from yaml.v3 Node
    Entity, Check string
    Message, Hint string
}
```

Every diagnostic carries a file and a line, and says what to do:

```
error: duplicate entity name "api"
  monorepo      services/api/service.yaml:4
  edge-gateway  service.yaml:4
  names must be unique across the merged catalog
```

**`tier` is required only for kinds that can page someone** — `Service`,
`Worker`, `Cron`, `API` — enforced by a conditional in the schema. A Kafka
topic's criticality is derived from its consumers; a library has none.
Requiring it everywhere means a team with forty libraries carries forty
permanent warnings, which makes `--fail-on warn` unusable on day one. Making a
required field optional later is non-breaking; the reverse is not.

**Exemptions exist so nobody has to lie.** A tier-1 nightly backfill genuinely
has no runbook. Without `spec.exemptions` the only available lever is to
misstate the tier — corrupting the dataset the whole product rests on. A waiver
needs a stated reason and may carry an expiry.

Uniqueness is on the **pair** `(kind, name)`, not on the name alone:
`service:orders` and `topic:orders` are distinct entities and may coexist.
Every generated artefact — CODEOWNERS, alert routing, the Slack map, and the
portal's per-entity URL — is therefore keyed on the **ref**, never the bare
name.

**Exit codes**

| Code | Meaning | Who fixes it |
|---|---|---|
| 0 | clean | — |
| 1 | landsraad could not run — a bad flag, an unreadable root, a repository it could not fetch | whoever ran it |
| 2 | a file you wrote has a problem a diagnostic points at — schema, collision, cycle, dangling ref, `repos.yaml` (ruling R36) | the YAML's author |
| 3 | scorecard gate — a tier-required check failed | the service owner |

2 and 3 are distinct because "your metadata is broken" and "your service does
not meet the standard" are different problems for different people.

**Stream contract.** `stdout` carries **only** the selected format's payload;
every human line — the `ok:` summary, usage errors, internal failures — goes to
`stderr`. Without this, `--format json` on a clean repo emits a JSON array
followed by `ok: 3 entities validated`, which no parser accepts, and the GitLab
Code Quality artefact is corrupt on the common path.

**Output** — `--format text|json`, plus auto-detected CI annotations:
`::error file=…,line=…::` under `GITHUB_ACTIONS`, Code Quality JSON under
`GITLAB_CI`.

**No silent fallbacks.** A fetch failure is a hard failure; rendering a portal
quietly missing three services is worse than rendering nothing. `--allow-partial`
exists but stamps a visible banner into the generated site naming every repo
that failed. Degraded mode must be visible in the artifact, not only in a log.

---

## 13. Package layout

```
cmd/landsraad/         cobra commands; each command is one explicit
                       composition of the stages below
internal/diag/         Diagnostic, Collector, Formatter interface + formats
internal/discover/     Find(fs.FS, patterns) — stage 1
internal/catalog/      Entity types, ParseAll, merge, refs, graph, CheckFiles
internal/schema/       embedded JSON Schema; *Validator value, no globals
internal/fetch/        fetch.go declares the Fetcher and Cache interfaces;
                       fs.go is the sparse filesystem; client.go the shared
                       HTTP half; github.go/githubwalk.go/gitlab.go the
                       adapters; blobs.go the parallel blob-fetch worker
                       pool and FetchError. The only package under
                       internal/ that speaks HTTP, and it makes every
                       request before a stage runs.
internal/scorecard/    checks, ingest, scoring, history
internal/render/       site generation, goldmark pipeline, search index
internal/render/md/    admonition extension
internal/generate/     CODEOWNERS, alert routing, Slack map
internal/config/       repos / teams / standards loading
internal/render/web/   go:embed templates, CSS, search and catalog JS
testdata/              fixture repos

The canonical JSON Schema lives at internal/schema/service.schema.json so
go:embed can reach it; schema/service.schema.json at the repo root is
generated from it by `task schema` for editor autocompletion.
```

The assets sit under internal/render/ rather than at the repository root
because a go:embed pattern is relative to its own package directory and may
not contain "..", so internal/render cannot reach a top-level web/. The
alternative — a root-level `package web` holding the embed — would be a
public import path, which D8 rules out. Corrected in Plan 3; earlier drafts
of this section described a layout that does not compile.

---

## 14. Testing

Test-first.

- **Table-driven per check** — one table entry per pass/fail/edge case.
- **Fixture repos in `testdata/`**: `monorepo-ok/`, `monorepo-broken/` (one
  fixture per diagnostic: collision, cycle, dangling ref, empty runbook,
  unknown owner), `multirepo/`.
- **Golden-file tests** for the renderer, with `-update` to regenerate.
- **Fetchers against `httptest`** with recorded responses. **Zero network in
  the test suite** — it must pass offline.
- **Every diagnostic type asserts its exact message string.** Error message
  quality is the product; a wording regression fails a test.

---

## 14.1 Security posture

**Trust boundary.** Service repositories are trusted, same-organisation
content; the portal is an internal site. landsraad is not a sandbox for
untrusted input and should not be described as one.

**Path handling.** All catalog file access goes through `io/fs.FS`, which
rejects absolute paths and any path containing `..`. A `service.yaml` cannot
name a file outside its own repository *by path*. That is a security property
of the seam in §3.1, not only a testing convenience.

The rejection is checked before the filesystem is touched, and it is reported
as what it is — `invalid-path`, naming the specific mistake — rather than as
`missing-file`. Telling someone a file they are looking at does not exist is
a false statement about their repository, and the distinction matters because
a shared runbook one directory up is an ordinary monorepo layout: the answer
is "move it or point at a copy", not "it is missing".

The qualifier *by path* is deliberate. `os.DirFS` does not resolve symlinks,
so a symlink committed into a repository can still reach outside it. Given the
trust boundary above — same-organisation content, not a sandbox — that is
accepted rather than defended against; `os.Root` (Go 1.24) is the mechanism if
the boundary ever changes. It is only material from Plan 3, where the renderer
reads runbook *contents*; `CheckFiles` today only stats.

**Rendering** (Plan 3, decided now rather than under pressure): goldmark runs
*without* `WithUnsafe`, so raw HTML in a runbook is escaped rather than injected
into a shared portal page, and Mermaid renders in strict mode.

**Supply chain.** Releases are tagged and checksummed; adopters pin a version in
CI rather than tracking `@latest`. `dependabot.yml` covers Go modules and
GitHub Actions.

**Secrets.** landsraad reads YAML and writes static files. It holds no
credentials in v1; the fetch adapters' tokens arrive with Plan 4 and belong in
CI secrets, never in `repos.yaml`. Resolution order, per repository (R29):
`LANDSRAAD_TOKEN_<NAME>` (the entry's identity, uppercased, every
non-alphanumeric byte replaced with `_`) first, then `GITHUB_TOKEN` or
`GITLAB_TOKEN` by host kind. No token is not an error — public repositories
still work — but it is a false economy for a private one: both hosts answer
a private repository with no token the same 404 they give a repository that
does not exist.

---

## 15. Non-goals for v1

- Producing `runtime.json` (the portal consumes it; the agent is sub-project E)
- Templates and `task new-service` (F)
- `mode-runner`, the Claude Code plugin, skills and modes catalog pages (G, H)
- **Git hosts other than GitHub and GitLab** — a consequence of D5, accepted
- Portal authentication, RBAC or SSO — whatever hosts the static site owns that
- Server-side search
- Provisioning infrastructure or running deployments
- Re-hosting anything Grafana, PagerDuty or Dynatrace already shows; the portal
  links out

---

## 16. Open items

1. **Tier-2 SLO severity at launch** — `warn` in the matrix above. Confirm with
   the first adopting team rather than by argument.
