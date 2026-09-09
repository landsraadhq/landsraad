# landsraad Scorecard and Generated Artifacts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the validated catalog into the two things that make metadata rot break something visible — generated ownership artifacts (`landsraad gen`) and a tier-aware scorecard (`landsraad score`).

**Architecture:** Both halves are pure functions over the `*catalog.Catalog` that Plan 1 already builds. A generator returns `[]emit.File` and `cmd/` owns the only loop that writes them (spec §3.1). Scoring is a fold over checks — Go funcs with stable ids, severity supplied by `standards.yaml` — producing a `Scorecard` value that the command layer renders and gates on. Nothing below `cmd/` reads the clock, shells out, or touches the filesystem except through an injected `io/fs.FS`.

**Tech Stack:** Go 1.23 (toolchain 1.26.1), go-task 3.53.1, cobra, `gopkg.in/yaml.v3` v3.0.1, `github.com/santhosh-tekuri/jsonschema/v6` v6.0.1, `github.com/google/go-cmp` v0.6.0. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-08-landsraad-design.md` — this plan implements sub-project **B** (§11, generated artifacts) and sub-project **C** (§9, scorecard). Read the spec alongside this plan; where they disagree, the spec wins and the disagreement is a bug in this plan.

**Baseline:** Plan 1 is complete and merged to `main` at `f9d7979`. `landsraad validate` works end to end. Run `task ci` before Task 1 and confirm it is green — a dirty baseline makes every later failure ambiguous.

## Global Constraints

Every task's requirements implicitly include this section. Values are copied verbatim from the spec or from the existing codebase.

- **`apiVersion: landsraad/v1`** on every catalog-adjacent file, including `standards.yaml` and `.landsraad/checks/*.yaml`. No domain, matching Kubernetes `apps/v1`.
- **Nothing under `internal/` imports `os` or any `os/*` package.** Reads take `io/fs.FS`, writes return values that `cmd/` writes. Enforced by `.claude/hooks/no-os-in-internal.py` and `scripts/check-rules.sh`; both exclude `*_test.go`.
- **Nothing under `internal/` calls `time.Now()`, `rand`, or the network.** The `time` *package* is fine — `time.Time`, `time.Duration`, `time.Parse` — but the current instant is injected as a value. A stage that reads the clock is not a pure function and its tests become flaky at midnight. `cmd/` supplies `time.Now()`.
- **No `sync.Once`, no `init()`, no package-level mutable state below `cmd/`.** Sets of things are functions returning a fresh slice, as `catalog.AllKinds()` and `config.DefaultPatterns()` are.
- **Diagnostics assert exact message strings in tests.** `strings.Contains` against `.Message` or `.Hint` is blocked by `.claude/hooks/exact-message-tests.py` and by `scripts/check-rules.sh`. Assert with `!=` against a full literal.
- **Accumulate, never fail fast** (spec §12). Every stage appends to a `*diag.Collector` and continues. Scoring twelve services reports twelve problems in one run.
- **No silent fallbacks** (spec §12). A degraded mode is visible in the artifact, not only in a log. "Zero results" and "no results file" are different answers and must render differently.
- **Every diagnostic carries a file and a line**, and says what to do. `Line: 0` is a bug; use `1` when the file has no better location.
- **Exit codes** (spec §12): `0` clean, `1` usage or config error, `2` validation error, `3` scorecard gate — a tier-required check failed. `2` and `3` are distinct because "your metadata is broken" and "your service does not meet the standard" are different problems for different people. Constants live in `cmd/landsraad/main.go`.
- **Stream contract** (spec §12): `stdout` carries **only** the selected format's payload; every human line — the `ok:` summary, usage errors, internal failures — goes to `stderr`.
- **Artifacts are keyed on the ref** (`kind:name`), never the bare name (spec §12). `service:orders` and `topic:orders` may coexist.
- **Go stays gofmt-clean and vet-clean.** `task ci` runs `go vet ./...`, `gofmt -l`, `go mod tidy -diff` and `scripts/check-rules.sh`.
- **Commit after every task**, with the test and the implementation in the same commit. Never `git add .` — add files individually.

## What ships when

The plan is ordered so that **sub-project B is complete and shippable after Task 4**. If work stops there, `landsraad gen` and `landsraad gen --check` are working features and the scorecard is simply absent. Tasks 5–12 add sub-project C. Reviewers should treat Task 4 as a natural release point.

## File Structure

**New packages**

| Path | Responsibility |
|---|---|
| `internal/emit/emit.go` | `File{Path, Data}` — the one type every generator returns — and `Diff`, which compares a generated set against what is on disk. This is the type `internal/scaffold` deliberately kept local in Plan 1 pending a second producer; Task 1 promotes it. |
| `internal/generate/codeowners.go` | `CODEOWNERS(cat, teams) emit.File` |
| `internal/generate/routing.go` | `AlertRoutes(cat, teams) emit.File`, `SlackMap(cat, teams) emit.File` |
| `internal/config/standards.go` | `LoadStandards` — the tier×severity matrix, the only scoring knob (spec §6) |
| `internal/config/standards.schema.json` | JSON Schema for `standards.yaml`, embedded |
| `internal/scorecard/check.go` | `Check`, `Env`, `Status`, `Result` — the vocabulary |
| `internal/scorecard/hermetic.go` | `HermeticChecks() []Check` — `owner-set`, `runbook-present`, `alerts-parse`, `slo-defined`, `docs-fresh` |
| `internal/scorecard/ingest.go` | `Ingest` — read `.landsraad/checks/*.yaml`, apply precedence and staleness |
| `internal/scorecard/ingest.schema.json` | JSON Schema for `CheckResults`, embedded |
| `internal/scorecard/score.go` | `Score` — fold checks over entities, apply standards and exemptions |
| `internal/scorecard/history.go` | `AppendHistory` — one row per entity per run, returned as an `emit.File` |
| `cmd/landsraad/gen.go` | `landsraad gen [--check]` |
| `cmd/landsraad/score.go` | `landsraad score [--format] [--fail-on]` |

**Modified**

| Path | Change |
|---|---|
| `internal/scaffold/scaffold.go` | `Files()` returns `[]emit.File`; the local `File` type is deleted |
| `cmd/landsraad/init.go` | ranges over `[]emit.File` |
| `cmd/landsraad/root.go` | registers `newGenCmd()` and `newScoreCmd()` |
| `cmd/landsraad/main.go` | adds `exitScorecard = 3` |
| `cmd/landsraad/validate.go` | validates `.landsraad/checks/*.yaml` structurally (Task 12) |
| `Taskfile.yml` | `schema` task also exports the two new schemas |

**Why `internal/emit` rather than one more type per package:** Plan 1's finding 9 settled that a generator returns files and `cmd/` writes them, and recorded that `scaffold.File` stays local "until Plan 2's generators give it a second producer, which is when the type is promoted rather than guessed at." Task 1 is that moment: `scaffold`, `generate` and `scorecard`'s history writer are three producers. `emit` is named for spec §7's stage 8.

---

## Test fixtures shared between tasks

Tests are written in package-internal files, so a helper defined in one task's
test file is visible to every later task in the same package. Tasks are
sequential and this is deliberate — redefining `svc()` five times would be
worse — but it means running a task out of order will not compile.

| Helper | Defined in | Used by |
|---|---|---|
| `ent(name, kind, owner, path)`, `teamsFrom`, `twoTeams` | Task 2, `internal/generate/codeowners_test.go` | Task 3 |
| `contains`, `indexOf` | Task 3, `internal/generate/routing_test.go` | Task 3 only |
| `svc(name)`, `env(files)`, `run(t, id, e, env)` | Task 6, `internal/scorecard/hermetic_test.go` | Tasks 7, 8, 9 |
| `catalogOf(t, entities...)`, `topic(name)`, `now` | Task 8, `internal/scorecard/ingest_test.go` | Tasks 9, 10 |
| `stdOf(t, yaml)`, `ownerOnly`, `hasWarn` | Task 9, `internal/scorecard/score_test.go` | Task 9 only |
| `genFS()` | Task 4, `cmd/landsraad/gen_test.go` | Task 4 only |
| `scoreFS()`, `scoreOpts()`, `testNow` | Task 11, `cmd/landsraad/score_test.go` | Task 11 only |

`plural(n, one, many)` already exists in `cmd/landsraad/validate.go` and is
reused by Tasks 4 and 11. `internal/generate` defines its own copy in Task 3 —
a different package, and two copies of six lines is not worth a shared package
(the same two-examples-not-three rule Plan 1 applied to `lineFromYAMLError`).

---

## Rulings the spec does not settle

The spec is the binding authority. Where it is silent, this plan decides, and each decision is recorded here with what it costs if wrong. An executor who disagrees should raise it rather than quietly implement something else.

| # | Question | Ruling | Cost if wrong |
|---|---|---|---|
| R1 | What severity applies to an entity with no `tier`? `tier` is required only for `Service`, `Worker`, `Cron`, `API` (spec §12), so a `Library` or `Topic` has `tier: 0`. | Entities without a tier are **not scored** — they appear in no scorecard and no history row. | A team gets no visibility into library documentation. Reversible: adding a `tiers.0` row to `standards.yaml` later is additive. |
| R2 | What counts as *applicable* in "Score = passed / applicable" (spec §9)? | Severity `required` and `warn` are applicable; `info` and `skip` are not. | An `info` check silently drags every score down, and teams respond by setting it to `skip`, losing the signal entirely. |
| R3 | Do `not-reported` and `stale` count as passes? | No. They are **not passed** but remain **applicable**. | If they were excluded from the denominator, deleting your CI job would *raise* your score. That is the one incentive this product must never create. |
| R4 | Does an exemption remove a check from the denominator? | Yes — an exempted check is `exempt` and applicable-minus-one. An exemption whose `until` has passed does not waive anything and produces a `warn` diagnostic naming the expiry. | An expired exemption that silently keeps waiving is a permanent lie in the dataset. |
| R5 | Where do CODEOWNERS owners come from? `config.Team` has `Name`, `Members`, `Slack`, `PagerDuty` — no host team handle. | From `Members`, each written with a leading `@` if it lacks one. | A team with an org-level GitHub team (`@org/payments`) has to list individuals. Additive fix: a `github:` field on `Team`, which is why this plan does not invent one now. |
| R6 | What path does a CODEOWNERS line use? | `spec.path`, with a trailing `/` when it names a directory and none when it names a file. An entity with no `spec.path` produces no line. | An entity outside the repo would otherwise emit a rule for a path git ignores. |
| R7 | Exact output format for alert routing and the Slack map — the spec names the artifacts but not their shape. | Alertmanager `routes:`/`receivers:` keyed on a `team` label; Slack map is `channels: {<ref>: <channel>}`. Both are `landsraad/v1` documents with a generated-file banner. | These land in users' repos, so the shape is a one-way door. Kept minimal and boring for that reason. |
| R8 | Should `validate` structurally check `.landsraad/checks/*.yaml`? Spec §7.1 excludes stage 6 from `validate` "in v1", but says the exclusion holds "until Plan 2 ships" because the shape was unspecified. | Yes, Task 12 adds structural validation only — no scoring, no network, still hermetic. | If §7.1's "in v1" was meant absolutely, `validate` does slightly more work than intended. The alternative leaves a malformed results file first caught by the platform build rather than the PR that introduced it, which is the exit-0-on-something-unexamined failure this project exists to prevent. |
| R9 | What is the history CSV's grain? | One row per (date, entity ref) per run, appended. Columns: `date,ref,tier,owner,passed,applicable,score`. | Weekly-only grain would lose data if CI runs daily; this grain is a superset and can be down-sampled. |

---

### Task 1: `internal/emit` — the file type every generator returns

**Files:**
- Create: `internal/emit/emit.go`
- Create: `internal/emit/emit_test.go`
- Modify: `internal/scaffold/scaffold.go` (delete the local `File` type, return `[]emit.File`)
- Modify: `internal/scaffold/scaffold_test.go` (the `asFS` helper and `TestFilesIsFreshOnEachCall`)
- Modify: `cmd/landsraad/init.go` (range over `[]emit.File`)

**Interfaces:**
- Consumes: nothing from this plan.
- Produces:
  - `type emit.File struct { Path string; Data []byte }`
  - `func emit.Diff(want []File, got fs.FS) []Difference`
  - `type emit.Difference struct { Path string; Kind DiffKind; }`
  - `type emit.DiffKind int` with `DiffMissing`, `DiffStale` constants and a `String()` method
  - `func (f File) String() string` — `"<path> (<n> bytes)"`, for test failure output

**Context:** Plan 1's audit finding 9 settled the write story: a generator is a pure function returning the files it would write, and `cmd/` owns the one loop that puts them on disk. `internal/scaffold` was the first producer and deliberately kept `File` local, with a comment saying the type gets promoted when a second producer appears. Tasks 2, 3 and 10 are that second, third and fourth producer.

`Diff` exists for `gen --check` (spec §8): "regenerates to a temp dir and diffs against what is committed, exiting non-zero when stale." Diffing in memory against an `fs.FS` is the same check without the temp dir, and it is testable without a filesystem.

- [ ] **Step 1: Write the failing test**

Create `internal/emit/emit_test.go`:

```go
package emit

import (
	"testing"
	"testing/fstest"
)

func TestDiffReportsAMissingFile(t *testing.T) {
	want := []File{{Path: "CODEOWNERS", Data: []byte("* @team\n")}}
	got := fstest.MapFS{}

	diffs := Diff(want, got)

	if len(diffs) != 1 {
		t.Fatalf("got %d differences, want 1: %+v", len(diffs), diffs)
	}
	if diffs[0].Path != "CODEOWNERS" {
		t.Errorf("Path = %q, want %q", diffs[0].Path, "CODEOWNERS")
	}
	if diffs[0].Kind != DiffMissing {
		t.Errorf("Kind = %v, want DiffMissing", diffs[0].Kind)
	}
}

func TestDiffReportsAStaleFile(t *testing.T) {
	want := []File{{Path: "CODEOWNERS", Data: []byte("* @team-payments\n")}}
	got := fstest.MapFS{"CODEOWNERS": {Data: []byte("* @team-old\n")}}

	diffs := Diff(want, got)

	if len(diffs) != 1 {
		t.Fatalf("got %d differences, want 1: %+v", len(diffs), diffs)
	}
	if diffs[0].Kind != DiffStale {
		t.Errorf("Kind = %v, want DiffStale", diffs[0].Kind)
	}
}

func TestDiffIsEmptyWhenEverythingMatches(t *testing.T) {
	want := []File{
		{Path: "CODEOWNERS", Data: []byte("* @team\n")},
		{Path: "a/b.yaml", Data: []byte("k: v\n")},
	}
	got := fstest.MapFS{
		"CODEOWNERS": {Data: []byte("* @team\n")},
		"a/b.yaml":   {Data: []byte("k: v\n")},
	}

	if diffs := Diff(want, got); len(diffs) != 0 {
		t.Errorf("identical content must produce no differences, got %+v", diffs)
	}
}

// A file on disk that the generator does not produce is NOT a difference:
// landsraad generates a named set of artifacts and has no opinion about the
// rest of the repository. Reporting them would make `gen --check` fail on
// every repository that has any other file.
func TestDiffIgnoresFilesTheGeneratorDoesNotProduce(t *testing.T) {
	want := []File{{Path: "CODEOWNERS", Data: []byte("* @team\n")}}
	got := fstest.MapFS{
		"CODEOWNERS": {Data: []byte("* @team\n")},
		"README.md":  {Data: []byte("# hello\n")},
	}

	if diffs := Diff(want, got); len(diffs) != 0 {
		t.Errorf("unrelated files are not differences, got %+v", diffs)
	}
}

func TestDiffKindStringsAreHumanReadable(t *testing.T) {
	if got := DiffMissing.String(); got != "not generated yet" {
		t.Errorf("DiffMissing.String() = %q, want %q", got, "not generated yet")
	}
	if got := DiffStale.String(); got != "out of date" {
		t.Errorf("DiffStale.String() = %q, want %q", got, "out of date")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/emit/ -run TestDiff -v`
Expected: FAIL — the package does not exist yet (`no Go files in .../internal/emit`).

- [ ] **Step 3: Write the implementation**

Create `internal/emit/emit.go`:

```go
// Package emit holds the type every landsraad generator returns.
//
// Spec §7's stage 8 is EMIT. Spec §3.1 puts writes at the command layer, and
// Plan 1 settled the shape: a generator is a pure function returning the files
// it would write, and cmd/ owns the single loop that puts them on disk. An
// io.Writer is one stream and every generator here produces a set.
//
// Keeping the type here rather than in any one producer's package is what lets
// scaffold, generate and scorecard's history writer share cmd/'s write loop
// without importing each other.
package emit

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
)

// File is one file to write: a slash-separated path relative to a root the
// generator never names, and its contents.
type File struct {
	Path string
	Data []byte
}

func (f File) String() string {
	return fmt.Sprintf("%s (%d bytes)", f.Path, len(f.Data))
}

// DiffKind says how a generated file differs from what is on disk.
type DiffKind int

const (
	// DiffMissing: the generator produces this file and it is not committed.
	DiffMissing DiffKind = iota
	// DiffStale: it is committed, with different content.
	DiffStale
)

func (k DiffKind) String() string {
	switch k {
	case DiffMissing:
		return "not generated yet"
	case DiffStale:
		return "out of date"
	}
	return "unknown"
}

// Difference is one generated file that does not match the repository.
type Difference struct {
	Path string
	Kind DiffKind
}

// Diff compares what a generator would write against what is in fsys,
// returning one Difference per mismatch, sorted by path.
//
// This is the mechanism behind `gen --check` (spec §8) and therefore the
// mechanism that makes metadata rot break something visible. It runs entirely
// in memory: the spec describes regenerating to a temp dir, but the comparison
// is the point and a temp dir is only one way to hold the bytes.
//
// Files present in fsys that no generator produces are deliberately not
// reported. landsraad generates a named set of artifacts and has no opinion
// about the rest of the repository; reporting them would make --check fail on
// every repository that contains anything else.
func Diff(want []File, fsys fs.FS) []Difference {
	var out []Difference
	for _, f := range want {
		got, err := fs.ReadFile(fsys, f.Path)
		if err != nil {
			out = append(out, Difference{Path: f.Path, Kind: DiffMissing})
			continue
		}
		if !bytes.Equal(got, f.Data) {
			out = append(out, Difference{Path: f.Path, Kind: DiffStale})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/emit/ -v`
Expected: PASS, five tests.

- [ ] **Step 5: Migrate `internal/scaffold` onto the shared type**

In `internal/scaffold/scaffold.go`, delete the local `File` type and its doc paragraph, import `internal/emit`, and change the signature. The package doc paragraph beginning "File is deliberately local." is now false and must be replaced — a stale comment that contradicts the code is worse than no comment.

Replace the package doc's final paragraph with:

```go
// File was local to this package in Plan 1, pending a second producer. Plan 2's
// generators are that producer, so the type now lives in internal/emit and
// cmd/ runs one write loop for all of them.
```

Change the type of `Files`:

```go
// Files returns the starter catalog, in the order it should be created.
//
// Every file is valid on the first run: the acceptance test for init is that
// `landsraad validate` passes immediately afterwards. Pure, so that property
// is checked in memory rather than against a temporary directory.
func Files() []emit.File {
	return []emit.File{
		{Path: "teams.yaml", Data: []byte(teamsYAML)},
		{Path: "repos.yaml", Data: []byte(reposYAML)},
		// The modeline in service.yaml below points here. Writing it is what
		// makes editor autocompletion work in a fresh repository without a
		// second manual step; `landsraad schema` regenerates it after an
		// upgrade.
		{Path: "schema/service.schema.json", Data: schema.Raw},
		{Path: "services/example/service.yaml", Data: []byte(exampleService)},
	}
}
```

Import block becomes:

```go
import (
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/schema"
)
```

- [ ] **Step 6: Run the whole suite**

Run: `go test ./...`
Expected: PASS. `cmd/landsraad/init.go` compiles unchanged — it ranges over `f.Path` and `f.Data`, which are the same field names. `internal/scaffold/scaffold_test.go` compiles unchanged for the same reason.

If anything fails to compile, the field names diverged; fix the call site rather than reintroducing a second type.

- [ ] **Step 7: Commit**

```bash
git add internal/emit/emit.go internal/emit/emit_test.go \
        internal/scaffold/scaffold.go
git commit -m "feat: promote the generated-file type to internal/emit

Plan 1 settled that a generator returns files and cmd/ writes them, and
kept scaffold.File local pending a second producer. Plan 2's CODEOWNERS,
routing and history generators are that producer, so the type moves to
internal/emit — named for spec §7's stage 8 — and cmd/ keeps one write
loop for all of them.

emit.Diff comes with it: it is what gen --check compares, and doing the
comparison in memory rather than against a temp dir makes it testable
without a filesystem."
```

---

### Task 2: CODEOWNERS generation

**Files:**
- Create: `internal/generate/codeowners.go`
- Create: `internal/generate/codeowners_test.go`

**Interfaces:**
- Consumes: `emit.File` (Task 1); `*catalog.Catalog`, `catalog.Ref`, `(*Entity).Ref()`, `(*Catalog).Entities()`; `*config.Teams`, `(*Teams).Get(name) (*Team, bool)`, `config.Team{Name, Members, Slack, PagerDuty}`; `*diag.Collector`.
- Produces:
  - `const generate.CodeownersPath = "CODEOWNERS"`
  - `func generate.CODEOWNERS(cat *catalog.Catalog, teams *config.Teams, c *diag.Collector) emit.File`
  - `func generate.Banner(what string) string` — the do-not-edit header shared by every generated artifact

**Context:** Spec §11 lists CODEOWNERS first among "derived from the catalog, never hand-edited, verified by `landsraad gen --check`". This is the artifact that makes ownership metadata load-bearing: UC3 in the originating design doc is "a team member leaves, one PR, no drift."

Ruling R5 applies: owners come from `Team.Members`, each prefixed with `@` if it lacks one, because `config.Team` has no host team-handle field and inventing one now is a schema change nobody has asked for. Ruling R6 applies: the path is `spec.path`, and an entity without one produces no line.

Ordering must be deterministic — a generated file that reorders itself between runs makes `--check` fail at random. Sort by path, because CODEOWNERS is evaluated **last-match-wins** by git: a shorter path must come before a longer one that overrides it, and lexical sort gives that for free (`services/` sorts before `services/api/`).

- [ ] **Step 1: Write the failing test**

Create `internal/generate/codeowners_test.go`:

```go
package generate

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// ent builds an entity the way the catalog would after parsing.
func ent(name string, kind catalog.Kind, owner, path string) *catalog.Entity {
	e := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: kind}
	e.Metadata.Name = name
	e.Metadata.Owner = owner
	e.Metadata.Tier = 2
	e.Spec.Path = path
	e.SourcePath = path + "/service.yaml"
	e.NameLine = 4
	return e
}

func teamsFrom(t *testing.T, yaml string) *config.Teams {
	t.Helper()
	var c diag.Collector
	teams := config.LoadTeams("teams.yaml", []byte(yaml), &c)
	if c.HasErrors() {
		t.Fatalf("fixture teams.yaml must load: %+v", c.Diagnostics())
	}
	return teams
}

const twoTeams = `teams:
  - name: team-payments
    members: [alice, "@bob"]
    slack: "#payments"
    pagerduty: PAY
  - name: team-sre
    members: [carol]
    slack: "#sre"
    pagerduty: SRE
`

func TestCODEOWNERSRendersOneLinePerEntity(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("api", catalog.KindService, "team-payments", "services/api"),
		ent("worker", catalog.KindWorker, "team-sre", "workers/worker"),
	}, &c)

	f := CODEOWNERS(cat, teamsFrom(t, twoTeams), &c)

	if c.HasErrors() {
		t.Fatalf("a complete catalog must generate cleanly: %+v", c.Diagnostics())
	}
	if f.Path != CodeownersPath {
		t.Errorf("Path = %q, want %q", f.Path, CodeownersPath)
	}
	want := `# Generated by landsraad. Do not edit.
# Source: the owner field of each service.yaml, resolved through teams.yaml.
# Run `+ "`landsraad gen`" + ` to regenerate; ` + "`landsraad gen --check`" + ` fails when this is stale.

services/api/ @alice @bob
workers/worker/ @carol
`
	if string(f.Data) != want {
		t.Errorf("CODEOWNERS\n got:\n%s\nwant:\n%s", f.Data, want)
	}
}

// git evaluates CODEOWNERS last-match-wins, so a more specific path must come
// after the general one it overrides. Lexical order gives that, and it also
// makes the file stable between runs — a generated file that reorders itself
// makes --check fail at random.
func TestCODEOWNERSIsSortedByPath(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("z", catalog.KindService, "team-sre", "services/z"),
		ent("a", catalog.KindService, "team-payments", "services/a"),
	}, &c)

	f := CODEOWNERS(cat, teamsFrom(t, twoTeams), &c)

	want := "services/a/ @alice @bob\nservices/z/ @carol\n"
	got := string(f.Data)
	if len(got) < len(want) || got[len(got)-len(want):] != want {
		t.Errorf("entries must be sorted by path, got:\n%s", got)
	}
}

// An entity that describes something outside this repository — an externally
// managed Database, a third-party API — omits spec.path (spec §5). Emitting a
// rule for a path that does not exist would be an ownership line git silently
// ignores.
func TestCODEOWNERSSkipsEntitiesWithNoPath(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("api", catalog.KindService, "team-payments", "services/api"),
		ent("rds", catalog.KindDatabase, "team-sre", ""),
	}, &c)

	f := CODEOWNERS(cat, teamsFrom(t, twoTeams), &c)

	want := "services/api/ @alice @bob\n"
	got := string(f.Data)
	if got[len(got)-len(want):] != want {
		t.Errorf("expected exactly one entry, got:\n%s", got)
	}
	if c.HasErrors() {
		t.Errorf("an entity without a path is not an error: %+v", c.Diagnostics())
	}
}

// A team with no members produces no owners, and a CODEOWNERS line with a path
// and no owner means "nobody owns this" to git — which silently removes the
// review requirement the file exists to create. Say so instead.
func TestCODEOWNERSReportsATeamWithNoMembers(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("api", catalog.KindService, "team-empty", "services/api"),
	}, &c)
	teams := teamsFrom(t, "teams:\n  - name: team-empty\n    slack: \"#e\"\n")

	CODEOWNERS(cat, teams, &c)

	if !c.HasErrors() {
		t.Fatal("a team with no members cannot own anything; that must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "codeowners-no-members" {
		t.Errorf("Check = %q, want %q", d.Check, "codeowners-no-members")
	}
	if d.Line != 4 {
		t.Errorf("Line = %d, want 4 — the entity's name line", d.Line)
	}
	want := `team "team-empty" owns service:api but lists no members, so CODEOWNERS would leave it unowned`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "add members to team-empty in teams.yaml"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// An owner that is not in teams.yaml is already an error from validate's
// unknown-owner check. Generating a line naming a team that does not exist
// would put a broken rule in the repository, so it is skipped and reported —
// two diagnostics for one cause is noise, but a silently wrong artifact is
// worse, and the checks run in different commands.
func TestCODEOWNERSReportsAnUnknownOwner(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("api", catalog.KindService, "team-nope", "services/api"),
	}, &c)

	CODEOWNERS(cat, teamsFrom(t, twoTeams), &c)

	if !c.HasErrors() {
		t.Fatal("an owner missing from teams.yaml must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "codeowners-unknown-owner" {
		t.Errorf("Check = %q, want %q", d.Check, "codeowners-unknown-owner")
	}
	want := `owner "team-nope" of service:api is not defined in teams.yaml`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// The banner is what tells a human who opens the file not to edit it, and
// tells them the command that regenerates it.
func TestBannerNamesTheCommandThatRegenerates(t *testing.T) {
	want := "# Generated by landsraad. Do not edit.\n" +
		"# Source: the owner field of each service.yaml, resolved through teams.yaml.\n" +
		"# Run `landsraad gen` to regenerate; `landsraad gen --check` fails when this is stale.\n"
	if got := Banner("the owner field of each service.yaml, resolved through teams.yaml"); got != want {
		t.Errorf("Banner\n got:\n%s\nwant:\n%s", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/generate/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

Create `internal/generate/codeowners.go`:

```go
// Package generate turns the catalog into the artifacts that make ownership
// metadata load-bearing: CODEOWNERS, alert routing, and the Slack channel map
// (spec §11). Every one is derived, never hand-edited, and verified by
// `landsraad gen --check`.
//
// Each generator is a pure function returning an emit.File. Nothing here
// touches the filesystem; cmd/ writes what these return.
package generate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// CodeownersPath is where CODEOWNERS is written, relative to the repository
// root. GitHub also accepts .github/CODEOWNERS and docs/CODEOWNERS; the root
// is the one every host reads.
const CodeownersPath = "CODEOWNERS"

// Banner is the header every generated artifact carries. A generated file that
// does not say it is generated gets hand-edited, and the edit is silently
// destroyed on the next run.
func Banner(source string) string {
	return "# Generated by landsraad. Do not edit.\n" +
		"# Source: " + source + ".\n" +
		"# Run `landsraad gen` to regenerate; `landsraad gen --check` fails when this is stale.\n"
}

// CODEOWNERS renders the ownership rules implied by the catalog.
//
// Entries are sorted by path. git evaluates CODEOWNERS last-match-wins, so a
// more specific path must appear after the general one it overrides, and
// lexical order gives that. It also makes the output stable between runs: a
// generated file that reorders itself makes `gen --check` fail at random.
//
// Owners come from the team's members (spec §11 routes owner through
// teams.yaml). config.Team carries no host team handle, so members it is; a
// handle already written with a leading @ is left alone.
func CODEOWNERS(cat *catalog.Catalog, teams *config.Teams, c *diag.Collector) emit.File {
	type entry struct{ path, owners string }
	var entries []entry

	for _, e := range cat.Entities() {
		// An entity that describes something outside this repository omits
		// spec.path (spec §5). A CODEOWNERS rule for a path that does not
		// exist is one git silently ignores.
		if e.Spec.Path == "" {
			continue
		}
		team, ok := teams.Get(e.Metadata.Owner)
		if !ok {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError,
				Repo:     e.SourceRepo,
				File:     e.SourcePath,
				Line:     e.NameLine,
				Entity:   e.Metadata.Name,
				Check:    "codeowners-unknown-owner",
				Message: fmt.Sprintf("owner %q of %s is not defined in teams.yaml",
					e.Metadata.Owner, e.Ref()),
				Hint: "every owner must be a team in teams.yaml",
			})
			continue
		}
		owners := handles(team.Members)
		if len(owners) == 0 {
			// A CODEOWNERS line with a path and no owner means "nobody owns
			// this" to git, which silently removes the review requirement the
			// file exists to create.
			c.Add(diag.Diagnostic{
				Severity: diag.SevError,
				Repo:     e.SourceRepo,
				File:     e.SourcePath,
				Line:     e.NameLine,
				Entity:   e.Metadata.Name,
				Check:    "codeowners-no-members",
				Message: fmt.Sprintf("team %q owns %s but lists no members, so CODEOWNERS would leave it unowned",
					team.Name, e.Ref()),
				Hint: fmt.Sprintf("add members to %s in teams.yaml", team.Name),
			})
			continue
		}
		entries = append(entries, entry{path: ownedPath(e), owners: strings.Join(owners, " ")})
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	var b strings.Builder
	b.WriteString(Banner("the owner field of each service.yaml, resolved through teams.yaml"))
	b.WriteString("\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "%s %s\n", e.path, e.owners)
	}
	return emit.File{Path: CodeownersPath, Data: []byte(b.String())}
}

// ownedPath renders spec.path as a CODEOWNERS pattern.
//
// A trailing slash restricts the rule to a directory's contents, which is what
// is wanted whenever spec.path names a directory. spec.path is checked for
// existence but deliberately not for directory-ness (spec §5: a Library may
// name a single file), so a path that looks like a file is emitted bare.
func ownedPath(e *catalog.Entity) string {
	p := e.Spec.Path
	if p == "." {
		// A single-service repository: the rule covers everything.
		return "*"
	}
	if strings.Contains(lastSegment(p), ".") {
		return p
	}
	return p + "/"
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// handles renders team members as host handles, adding the leading @ that
// CODEOWNERS requires when the member is written without one. Both spellings
// appear in real teams.yaml files and neither is wrong.
func handles(members []string) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if !strings.HasPrefix(m, "@") {
			m = "@" + m
		}
		out = append(out, m)
	}
	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/generate/ -v`
Expected: PASS, six tests.

- [ ] **Step 5: Run the whole suite and the linters**

Run: `task ci`
Expected: green.

- [ ] **Step 6: Commit**

```bash
git add internal/generate/codeowners.go internal/generate/codeowners_test.go
git commit -m "feat: generate CODEOWNERS from the catalog

Spec §11's first derived artifact, and the one that makes ownership
metadata load-bearing: change the owner in service.yaml, regenerate, and
review routing follows in the same PR.

Sorted by path because git evaluates CODEOWNERS last-match-wins, so a
specific path must follow the general one it overrides — and because a
generated file that reorders itself makes gen --check fail at random.

Two things are errors rather than silent omissions: an owner missing
from teams.yaml, and a team with no members. The second matters more
than it looks. A CODEOWNERS line with a path and no owner reads to git
as 'nobody owns this', which silently removes the review requirement the
file exists to create."
```

---

### Task 3: Alert routing and the Slack channel map

**Files:**
- Create: `internal/generate/routing.go`
- Create: `internal/generate/routing_test.go`

**Interfaces:**
- Consumes: `emit.File` (Task 1); `generate.Banner` (Task 2); `*catalog.Catalog`, `*config.Teams`, `*diag.Collector`.
- Produces:
  - `const generate.AlertRoutesPath = "alertmanager-routes.yaml"`
  - `const generate.SlackMapPath = "slack-channels.yaml"`
  - `func generate.AlertRoutes(cat *catalog.Catalog, teams *config.Teams, c *diag.Collector) emit.File`
  - `func generate.SlackMap(cat *catalog.Catalog, teams *config.Teams, c *diag.Collector) emit.File`

**Context:** Spec §11's second and third artifacts: "Alertmanager / PagerDuty routing, via `owner` → `teams.yaml`" and "Slack channel map for deploy notifications".

Ruling R7 applies: the spec names these artifacts but not their shape, and their shape is a one-way door because they land in users' repositories and get referenced by Alertmanager configs. Both are kept deliberately minimal.

**Alertmanager routing** is emitted as a `routes:`/`receivers:` fragment matched on a `team` label. That is the shape Alertmanager expects, and matching on a label rather than on a service name is what makes the file stable when services are added: a new service owned by an existing team changes nothing here. Only teams that actually own something appear — a receiver nothing routes to is dead config.

**The Slack map** is keyed on the **ref** (`service:api`), per the Global Constraints and spec §12. A deploy pipeline looks up the thing it just deployed, and `service:orders` and `topic:orders` are different things.

Both artifacts skip an entity whose owner is unknown, without re-reporting it — `CODEOWNERS` already emitted `codeowners-unknown-owner` for the same entity in the same run, and three diagnostics for one typo is noise. This is a deliberate asymmetry with Task 2 and is commented as such.

- [ ] **Step 1: Write the failing test**

Create `internal/generate/routing_test.go`:

```go
package generate

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestAlertRoutesEmitsOneReceiverPerOwningTeam(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("api", catalog.KindService, "team-payments", "services/api"),
		ent("ledger", catalog.KindService, "team-payments", "services/ledger"),
		ent("worker", catalog.KindWorker, "team-sre", "workers/worker"),
	}, &c)

	f := AlertRoutes(cat, teamsFrom(t, twoTeams), &c)

	if f.Path != AlertRoutesPath {
		t.Errorf("Path = %q, want %q", f.Path, AlertRoutesPath)
	}
	want := Banner("the owner field of each service.yaml, resolved through teams.yaml") + `apiVersion: landsraad/v1
kind: AlertRouting
spec:
  routes:
    - match:
        team: team-payments
      receiver: pagerduty-team-payments
    - match:
        team: team-sre
      receiver: pagerduty-team-sre
  receivers:
    - name: pagerduty-team-payments
      pagerduty_configs:
        - service_key: PAY
    - name: pagerduty-team-sre
      pagerduty_configs:
        - service_key: SRE
`
	if string(f.Data) != want {
		t.Errorf("AlertRoutes\n got:\n%s\nwant:\n%s", f.Data, want)
	}
}

// A team that owns nothing gets no receiver. A receiver nothing routes to is
// dead config, and Alertmanager will not tell anyone it is there.
func TestAlertRoutesOmitsTeamsThatOwnNothing(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("api", catalog.KindService, "team-payments", "services/api"),
	}, &c)

	f := AlertRoutes(cat, teamsFrom(t, twoTeams), &c)

	for _, line := range []string{"team-sre", "SRE"} {
		if contains(string(f.Data), line) {
			t.Errorf("team-sre owns nothing and must not appear:\n%s", f.Data)
		}
	}
}

// A team with no PagerDuty key cannot page anyone. Emitting a receiver with an
// empty service_key produces an Alertmanager config that loads fine and pages
// nobody — the failure this whole product exists to prevent, generated by the
// product itself.
func TestAlertRoutesReportsATeamWithNoPagerDuty(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("api", catalog.KindService, "team-nopd", "services/api"),
	}, &c)
	teams := teamsFrom(t, "teams:\n  - name: team-nopd\n    members: [a]\n    slack: \"#x\"\n")

	AlertRoutes(cat, teams, &c)

	if !c.HasErrors() {
		t.Fatal("a team that owns something but has no pagerduty key must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "routing-no-pagerduty" {
		t.Errorf("Check = %q, want %q", d.Check, "routing-no-pagerduty")
	}
	if d.File != "teams.yaml" {
		t.Errorf("File = %q, want %q — the file the fix goes in", d.File, "teams.yaml")
	}
	want := `team "team-nopd" owns 1 entity but has no pagerduty key, so its alerts would page nobody`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "add `pagerduty: <service key>` to team-nopd in teams.yaml"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

func TestSlackMapIsKeyedOnTheRef(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("orders", catalog.KindService, "team-payments", "services/orders"),
		ent("orders", catalog.KindTopic, "team-sre", "topics/orders"),
	}, &c)

	f := SlackMap(cat, teamsFrom(t, twoTeams), &c)

	if f.Path != SlackMapPath {
		t.Errorf("Path = %q, want %q", f.Path, SlackMapPath)
	}
	want := Banner("the owner field of each service.yaml, resolved through teams.yaml") + `apiVersion: landsraad/v1
kind: SlackChannels
spec:
  channels:
    service:orders: "#payments"
    topic:orders: "#sre"
`
	if string(f.Data) != want {
		t.Errorf("SlackMap\n got:\n%s\nwant:\n%s", f.Data, want)
	}
}

// The two entities above share the bare name "orders". Keying on the name
// would collapse them and route one team's deploys to the other's channel.
func TestSlackMapDoesNotCollapseEntitiesSharingAName(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("orders", catalog.KindService, "team-payments", "services/orders"),
		ent("orders", catalog.KindTopic, "team-sre", "topics/orders"),
	}, &c)

	f := SlackMap(cat, teamsFrom(t, twoTeams), &c)

	if !contains(string(f.Data), "service:orders") || !contains(string(f.Data), "topic:orders") {
		t.Errorf("both refs must appear:\n%s", f.Data)
	}
}

func TestSlackMapReportsATeamWithNoChannel(t *testing.T) {
	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{
		ent("api", catalog.KindService, "team-noslack", "services/api"),
	}, &c)
	teams := teamsFrom(t, "teams:\n  - name: team-noslack\n    members: [a]\n    pagerduty: X\n")

	SlackMap(cat, teams, &c)

	if !c.HasErrors() {
		t.Fatal("a team that owns something but has no slack channel must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "routing-no-slack" {
		t.Errorf("Check = %q, want %q", d.Check, "routing-no-slack")
	}
	want := `team "team-noslack" owns 1 entity but has no slack channel, so its deploy notifications would go nowhere`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// contains is a local helper: the exact-message rule forbids strings.Contains
// against .Message and .Hint, and this is neither — it inspects generated file
// content, where a substring check is the right tool.
func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/generate/ -run 'TestAlertRoutes|TestSlackMap' -v`
Expected: FAIL — `undefined: AlertRoutes`.

- [ ] **Step 3: Write the implementation**

Create `internal/generate/routing.go`:

```go
package generate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

const (
	// AlertRoutesPath is an Alertmanager routing fragment, included from the
	// operator's own alertmanager.yml rather than replacing it: landsraad owns
	// who gets paged, not the rest of the alerting configuration.
	AlertRoutesPath = "alertmanager-routes.yaml"
	// SlackMapPath maps an entity ref to the channel its deploys announce in.
	SlackMapPath = "slack-channels.yaml"
)

// routingSource is the one-line provenance both artifacts carry.
const routingSource = "the owner field of each service.yaml, resolved through teams.yaml"

// owningTeams returns the teams that own at least one entity, sorted by name,
// with the number of entities each owns.
//
// An entity whose owner is not in teams.yaml is skipped without a diagnostic:
// CODEOWNERS already reported codeowners-unknown-owner for that same entity in
// the same run, and three diagnostics for one typo is noise. The asymmetry with
// CODEOWNERS is deliberate — one artifact reports the problem, the rest stay
// quiet about it.
func owningTeams(cat *catalog.Catalog, teams *config.Teams) ([]*config.Team, map[string]int) {
	counts := map[string]int{}
	for _, e := range cat.Entities() {
		if _, ok := teams.Get(e.Metadata.Owner); !ok {
			continue
		}
		counts[e.Metadata.Owner]++
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]*config.Team, 0, len(names))
	for _, n := range names {
		team, _ := teams.Get(n)
		out = append(out, team)
	}
	return out, counts
}

// AlertRoutes renders Alertmanager routes and receivers for every team that
// owns something.
//
// Routes match on a `team` label rather than on a service name, which is what
// keeps this file stable as services are added: a new service owned by an
// existing team changes nothing here. It is the alerting rules' job to attach
// that label, and spec.alerts is where those rules live.
func AlertRoutes(cat *catalog.Catalog, teams *config.Teams, c *diag.Collector) emit.File {
	owners, counts := owningTeams(cat, teams)

	var b strings.Builder
	b.WriteString(Banner(routingSource))
	b.WriteString("apiVersion: landsraad/v1\n")
	b.WriteString("kind: AlertRouting\n")
	b.WriteString("spec:\n")

	b.WriteString("  routes:\n")
	for _, team := range owners {
		if team.PagerDuty == "" {
			// A receiver with an empty service_key loads without complaint and
			// pages nobody. That is the failure this product exists to
			// prevent, generated by the product itself.
			c.Add(diag.Diagnostic{
				Severity: diag.SevError,
				File:     "teams.yaml",
				Line:     1,
				Check:    "routing-no-pagerduty",
				Message: fmt.Sprintf("team %q owns %s but has no pagerduty key, so its alerts would page nobody",
					team.Name, plural(counts[team.Name], "entity", "entities")),
				Hint: fmt.Sprintf("add `pagerduty: <service key>` to %s in teams.yaml", team.Name),
			})
			continue
		}
		fmt.Fprintf(&b, "    - match:\n        team: %s\n      receiver: pagerduty-%s\n", team.Name, team.Name)
	}

	b.WriteString("  receivers:\n")
	for _, team := range owners {
		if team.PagerDuty == "" {
			continue
		}
		fmt.Fprintf(&b, "    - name: pagerduty-%s\n      pagerduty_configs:\n        - service_key: %s\n",
			team.Name, team.PagerDuty)
	}

	return emit.File{Path: AlertRoutesPath, Data: []byte(b.String())}
}

// SlackMap renders ref → channel for every entity with a resolvable owner.
//
// Keyed on the ref, never the bare name (spec §12): service:orders and
// topic:orders are distinct entities that may coexist, and a deploy pipeline
// looking up "orders" would get one team's channel for the other's deploy.
func SlackMap(cat *catalog.Catalog, teams *config.Teams, c *diag.Collector) emit.File {
	_, counts := owningTeams(cat, teams)

	type line struct{ ref, channel string }
	var lines []line
	reported := map[string]bool{}

	for _, e := range cat.Entities() {
		team, ok := teams.Get(e.Metadata.Owner)
		if !ok {
			continue
		}
		if team.Slack == "" {
			// Reported once per team, not once per entity: a team owning
			// twelve services should produce one diagnostic, not twelve.
			if !reported[team.Name] {
				reported[team.Name] = true
				c.Add(diag.Diagnostic{
					Severity: diag.SevError,
					File:     "teams.yaml",
					Line:     1,
					Check:    "routing-no-slack",
					Message: fmt.Sprintf("team %q owns %s but has no slack channel, so its deploy notifications would go nowhere",
						team.Name, plural(counts[team.Name], "entity", "entities")),
					Hint: fmt.Sprintf("add `slack: \"#channel\"` to %s in teams.yaml", team.Name),
				})
			}
			continue
		}
		lines = append(lines, line{ref: e.Ref().String(), channel: team.Slack})
	}

	sort.Slice(lines, func(i, j int) bool { return lines[i].ref < lines[j].ref })

	var b strings.Builder
	b.WriteString(Banner(routingSource))
	b.WriteString("apiVersion: landsraad/v1\n")
	b.WriteString("kind: SlackChannels\n")
	b.WriteString("spec:\n")
	b.WriteString("  channels:\n")
	for _, l := range lines {
		// Quoted because a channel begins with '#', which YAML would otherwise
		// read as a comment.
		fmt.Fprintf(&b, "    %s: %q\n", l.ref, l.channel)
	}
	return emit.File{Path: SlackMapPath, Data: []byte(b.String())}
}

// plural renders a count with the right noun, as cmd/landsraad does for the
// validate summary. "1 entities" in a diagnostic is the same defect as "1
// entities validated" on stdout.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/generate/ -v`
Expected: PASS, twelve tests across both files.

- [ ] **Step 5: Commit**

```bash
git add internal/generate/routing.go internal/generate/routing_test.go
git commit -m "feat: generate Alertmanager routing and the Slack channel map

Spec §11's remaining two artifacts. Routes match on a team label rather
than a service name, so adding a service owned by an existing team
changes nothing in this file — and a team that owns nothing gets no
receiver, because a receiver nothing routes to is dead config.

A team that owns something with no pagerduty key is an error. A receiver
with an empty service_key loads without complaint and pages nobody,
which is the exact failure this product exists to prevent, produced by
the product itself.

The Slack map is keyed on the ref. service:orders and topic:orders may
coexist (spec §12), and a deploy pipeline looking up the bare name would
announce one team's deploy in the other team's channel."
```

---

### Task 4: `landsraad gen` and `gen --check`

**Files:**
- Create: `cmd/landsraad/gen.go`
- Create: `cmd/landsraad/gen_test.go`
- Modify: `cmd/landsraad/root.go` (register the command)

**Interfaces:**
- Consumes: `generate.CODEOWNERS`, `generate.AlertRoutes`, `generate.SlackMap` (Tasks 2–3); `emit.File`, `emit.Diff`, `emit.Difference` (Task 1); the catalog-loading composition already in `cmd/landsraad/validate.go`.
- Produces:
  - `func Gen(fsys fs.FS, root string, out, errOut io.Writer, f diag.Formatter, check bool) int` — `root` is the directory `fsys` was opened at, used only by the write loop; tests pass `""` with `check: true`
  - `func artifacts(fsys fs.FS, c *diag.Collector) []emit.File`
  - `func newGenCmd() *cobra.Command`

**Context:** Spec §8: "`gen --check` regenerates to a temp dir and diffs against what is committed, exiting non-zero when stale. This is the mechanism that makes metadata rot break something visible."

The command is an **explicit composition**, matching `Validate` in `validate.go`: it calls the stages in order and the type checker enforces that order. Read `cmd/landsraad/validate.go` before writing this — it is the shape to follow, including how it resolves the root, builds the formatter, and honours the stream contract.

`gen` must refuse to write anything when the catalog has errors. Generating CODEOWNERS from a catalog with a dangling ref or a duplicate name produces an artifact that encodes the broken state, and `--check` then passes forever against it. Validation problems exit `2`; a stale artifact under `--check` also exits `2` — it is the YAML author's problem in both cases, and `3` is reserved for the scorecard gate.

- [ ] **Step 1: Write the failing test**

Create `cmd/landsraad/gen_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
)

// genFS is a minimal repository: one team, one service, and the repos.yaml
// that points at it.
func genFS() fstest.MapFS {
	return fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")},
		"repos.yaml": {Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"services/api/service.yaml": {Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 2
  lifecycle: production
spec:
  path: services/api
`)},
	}
}

func TestGenWritesNothingToStdoutWhenClean(t *testing.T) {
	var out, errOut bytes.Buffer

	code := Gen(genFS(), "", &out, &errOut, diagText(), false)

	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	// Stream contract (spec §12): the human summary is stderr, and gen's
	// payload is files on disk, not stdout.
	if out.Len() != 0 {
		t.Errorf("stdout must be empty, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "3 artifacts") {
		t.Errorf("stderr must summarise what was generated, got %q", errOut.String())
	}
}

// --check against a repository with no generated files must fail: the
// artifacts have never been committed, so ownership routing does not exist.
func TestGenCheckFailsWhenArtifactsAreMissing(t *testing.T) {
	var out, errOut bytes.Buffer

	code := Gen(genFS(), "", &out, &errOut, diagText(), true)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if !strings.Contains(errOut.String(), "CODEOWNERS") {
		t.Errorf("the failure must name the stale file, got %q", errOut.String())
	}
}

// --check against a repository holding exactly what gen would produce passes.
// This is the CI gate: it is what makes metadata rot break something visible.
func TestGenCheckPassesWhenArtifactsAreCurrent(t *testing.T) {
	fsys := genFS()
	var c diagCollectorForTest
	for _, f := range artifacts(fsys, c.collector()) {
		fsys[f.Path] = &fstest.MapFile{Data: f.Data}
	}

	var out, errOut bytes.Buffer
	code := Gen(fsys, "", &out, &errOut, diagText(), true)

	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
}

// A hand-edited artifact is the case --check exists for.
func TestGenCheckFailsOnAHandEditedArtifact(t *testing.T) {
	fsys := genFS()
	var c diagCollectorForTest
	for _, f := range artifacts(fsys, c.collector()) {
		fsys[f.Path] = &fstest.MapFile{Data: f.Data}
	}
	fsys["CODEOWNERS"] = &fstest.MapFile{Data: []byte("services/api/ @someone-else\n")}

	var out, errOut bytes.Buffer
	code := Gen(fsys, "", &out, &errOut, diagText(), true)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if !strings.Contains(errOut.String(), "out of date") {
		t.Errorf("stderr must say the file is out of date, got %q", errOut.String())
	}
}

// Generating from a catalog with validation errors would encode the broken
// state into the artifact, and --check would then pass against it forever.
func TestGenRefusesToGenerateFromABrokenCatalog(t *testing.T) {
	fsys := genFS()
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-does-not-exist
  tier: 2
  lifecycle: production
spec:
  path: services/api
`)}

	var out, errOut bytes.Buffer
	code := Gen(fsys, "", &out, &errOut, diagText(), false)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if strings.Contains(errOut.String(), "artifacts") {
		t.Errorf("nothing must be reported as generated, got %q", errOut.String())
	}
}
```

Add this helper to the same file — the tests need a collector without importing the diag package's internals into every case:

```go
// diagCollectorForTest hands out a collector whose diagnostics the test does
// not care about: artifacts() reports through it, and these cases assert on
// exit codes and file content instead.
type diagCollectorForTest struct{ c diag.Collector }

func (d *diagCollectorForTest) collector() *diag.Collector { return &d.c }
```

and add `"github.com/landsraadhq/landsraad/internal/diag"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/landsraad/ -run TestGen -v`
Expected: FAIL — `undefined: Gen`.

- [ ] **Step 3: Write the implementation**

Create `cmd/landsraad/gen.go`. Read `cmd/landsraad/validate.go` first: `loadCatalog` below must reuse the same stages in the same order, and if `validate.go` has since changed, follow it rather than this snippet.

```go
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/generate"
	"github.com/landsraadhq/landsraad/internal/schema"
)

// artifacts is stage 8 (EMIT) for the generated-ownership half of the spec:
// the pure part, returning what should be on disk without touching it.
//
// Exported artifacts are ordered as CODEOWNERS, routing, Slack map — the order
// spec §11 lists them, which is also the order of decreasing blast radius.
func artifacts(fsys fs.FS, c *diag.Collector) []emit.File {
	cat, teams := loadCatalog(fsys, c)
	if cat == nil {
		return nil
	}
	return []emit.File{
		generate.CODEOWNERS(cat, teams, c),
		generate.AlertRoutes(cat, teams, c),
		generate.SlackMap(cat, teams, c),
	}
}

// loadCatalog runs stages 1, 3, 4 and 5 and loads teams.yaml — the same
// composition Validate uses, minus the reporting. It returns nil when the
// repository is unusable rather than when it merely has problems; callers
// check c.HasErrors() for the latter.
func loadCatalog(fsys fs.FS, c *diag.Collector) (*catalog.Catalog, *config.Teams) {
	paths := patternsFor(fsys, c)
	found, err := discover.Find(fsys, paths)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files: %v", discover.Filename, err),
		})
		return nil, nil
	}
	files := discover.Load(fsys, found, c)

	validator, err := schema.Default()
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "schema", Line: 1,
			Check:   "schema-compile",
			Message: fmt.Sprintf("cannot compile the embedded schema: %v", err),
		})
		return nil, nil
	}
	for _, f := range files {
		validator.Validate("", f.Path, f.Data, c)
	}

	cat := catalog.NewCatalog(catalog.ParseAll(localRepoName(fsys), files, c), c)
	catalog.CheckFiles(fsys, cat, c)
	g := cat.Resolve(catalog.LocalOnly, c)
	reportCycles(cat, g, c)

	teamsData, err := fs.ReadFile(fsys, "teams.yaml")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			Check:   "teams-missing",
			Message: "teams.yaml not found, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		})
		return nil, nil
	}
	teams := config.LoadTeams("teams.yaml", teamsData, c)
	teams.ValidateOwners(cat, c)
	return cat, teams
}

// Gen writes the derived artifacts, or under check reports which are stale.
//
// It refuses to generate from a catalog with errors. An artifact derived from
// a broken catalog encodes the broken state, and `--check` then passes against
// it forever — metadata rot that the anti-rot mechanism certifies as fine.
func Gen(fsys fs.FS, root string, out, errOut io.Writer, f diag.Formatter, check bool) int {
	var c diag.Collector
	files := artifacts(fsys, &c)

	if c.HasErrors() {
		if err := f.Write(out, c.Diagnostics()); err != nil {
			fmt.Fprintf(errOut, "error: cannot write diagnostics: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(errOut, "refusing to generate from a catalog with errors; fix them and rerun\n")
		return exitValidation
	}
	if err := f.Write(out, c.Diagnostics()); err != nil {
		fmt.Fprintf(errOut, "error: cannot write diagnostics: %v\n", err)
		return exitUsage
	}

	if check {
		diffs := emit.Diff(files, fsys)
		if len(diffs) == 0 {
			fmt.Fprintf(errOut, "ok: %s up to date\n", plural(len(files), "artifact", "artifacts"))
			return exitOK
		}
		for _, d := range diffs {
			fmt.Fprintf(errOut, "stale: %s is %s\n", d.Path, d.Kind)
		}
		fmt.Fprintf(errOut, "\nrun `landsraad gen` and commit the result\n")
		return exitValidation
	}

	// The one write loop. Spec §3.1: only the command layer touches the
	// filesystem, and every generator above returned values.
	//
	// root travels beside fsys rather than being recovered from it: an fs.FS
	// does not know where it came from, and a package-level variable holding
	// the answer would be exactly the hidden global the project forbids one
	// directory down.
	for _, file := range files {
		full := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return exitUsage
		}
		if err := os.WriteFile(full, file.Data, 0o644); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(errOut, "  wrote %s\n", file.Path)
	}
	fmt.Fprintf(errOut, "ok: %s generated\n", plural(len(files), "artifact", "artifacts"))
	return exitOK
}

func newGenCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "gen [root]",
		Short: "Bene Gesserit — regenerate CODEOWNERS, alert routing and the Slack map",
		Long: "Derive the ownership artifacts from the catalog. These files are " +
			"never hand-edited: `--check` regenerates them in memory and fails when " +
			"what is committed differs, which is what makes metadata rot break " +
			"something visible.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start := "."
			if len(args) == 1 {
				start = args[0]
			}
			resolved, err := findRoot(start)
			if err != nil {
				return err
			}
			if code := Gen(os.DirFS(resolved), resolved, cmd.OutOrStdout(), cmd.ErrOrStderr(), diagText(), check); code != exitOK {
				return exitWith(code)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "fail if the committed artifacts differ from what would be generated")
	return cmd
}
```

**Check before you write:** `exitWith`, `findRoot`, `patternsFor`, `localRepoName` and `reportCycles` are all expected to exist already in `cmd/landsraad/`. Confirm their names against the current source; if the helper that turns an exit code into a cobra error is called something else, use the existing one rather than adding a second.

Note that `Gen` with `root: ""` never reaches the write loop in these tests, because every case that would write passes `check: true`. A test that exercised the write path would need a real directory, which is why the write loop is the thinnest part of the command and everything above it is a pure function.

- [ ] **Step 4: Register the command**

In `cmd/landsraad/root.go`, add `newGenCmd()` alongside the existing `AddCommand` calls.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./cmd/landsraad/ -v -run TestGen`
Expected: PASS, five tests.

- [ ] **Step 6: Verify end to end against the real fixture**

```bash
go build -o /tmp/landsraad ./cmd/landsraad
cd "$(mktemp -d)" && /tmp/landsraad init . && /tmp/landsraad gen . && cat CODEOWNERS && /tmp/landsraad gen --check .; echo "exit=$?"
```

Expected: `init` scaffolds, `gen` writes three files, `CODEOWNERS` contains `services/example/ @you`, and `--check` exits 0. Then hand-edit `CODEOWNERS` and confirm `--check` exits 2 naming it.

- [ ] **Step 7: Run `task ci` and commit**

```bash
git add cmd/landsraad/gen.go cmd/landsraad/gen_test.go cmd/landsraad/root.go
git commit -m "feat: landsraad gen, with --check as the anti-rot gate

Spec §8: gen --check regenerates and fails when what is committed
differs. That is the mechanism that makes metadata rot break something
visible, so it is a CI gate rather than a report.

gen refuses to run on a catalog with errors. An artifact derived from a
broken catalog encodes the broken state, and --check then passes against
it forever — the anti-rot mechanism certifying the rot as fine.

Diffing happens in memory rather than against a temp dir. The comparison
is the point; a temp dir is one way to hold bytes, and the in-memory
version is testable without a filesystem."
```

**Sub-project B is complete here.** `landsraad gen` and `gen --check` work. Everything below adds the scorecard.

---

### Task 5: `standards.yaml` — the tier×severity matrix

**Files:**
- Create: `internal/config/standards.go`
- Create: `internal/config/standards_test.go`
- Create: `internal/config/standards.schema.json`

**Interfaces:**
- Consumes: `*diag.Collector`; `yamlerr.Nouns` and `yamlerr.Problems` (the translator Plan 1 built); `schema.New` for schema compilation.
- Produces:
  - `type config.Severity string` with `SevRequired = "required"`, `SevWarn = "warn"`, `SevInfo = "info"`, `SevSkip = "skip"`
  - `type config.CheckStandard struct { Source string; Params map[string]int; Tiers map[int]Severity }`
  - `type config.Standards struct{ ... }` with methods:
    - `func (s *Standards) Severity(check string, tier int) Severity`
    - `func (s *Standards) Checks() []string` — sorted check ids
    - `func (s *Standards) IsExternal(check string) bool`
    - `func (s *Standards) Param(check, name string, fallback int) int`
    - `func (s *Standards) StaleAfterDays() int`
    - `func (s *Standards) Loaded() bool`
  - `func config.LoadStandards(path string, data []byte, c *diag.Collector) *Standards`
  - `func config.DefaultStandards() *Standards` — the matrix from spec §6, used when `standards.yaml` is absent
  - `var config.StandardsSchema []byte` — the embedded JSON Schema

**Context:** Spec §6 gives the exact document, and D4 explains why it holds only the severity matrix and thresholds: "Checks are Go funcs with stable ids; `standards.yaml` holds only the tier→severity matrix and thresholds. Type-safe, precise messages, nothing to debug in YAML."

Copy the spec's example verbatim as `DefaultStandards()`. A repository with no `standards.yaml` must still score, and it must score against the published defaults rather than against nothing — otherwise `landsraad score` on a fresh repo reports a perfect score having checked nothing, which is this project's signature failure.

The absence of `standards.yaml` is **announced**, exactly as the absence of `repos.yaml` is (see `patternsFor` in `cmd/landsraad/validate.go`). Degraded mode is visible in the artifact.

Two staleness clocks exist and must not be conflated (spec §6): `spec.staleAfterDays` ages out ingested check results; `docs-fresh.params.maxAgeDays` ages out service documentation. They are tuned independently.

- [ ] **Step 1: Write the schema**

Create `internal/config/standards.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "standards.schema.json",
  "title": "landsraad standards",
  "description": "The tier by severity matrix: the only scoring knob.",
  "type": "object",
  "required": ["apiVersion", "kind", "spec"],
  "properties": {
    "apiVersion": { "const": "landsraad/v1" },
    "kind": { "const": "Standards" },
    "spec": {
      "type": "object",
      "required": ["checks"],
      "properties": {
        "staleAfterDays": {
          "type": "integer",
          "minimum": 1,
          "description": "Ingested check results older than this render as stale, not pass."
        },
        "checks": {
          "type": "object",
          "minProperties": 1,
          "additionalProperties": {
            "type": "object",
            "required": ["tiers"],
            "properties": {
              "source": {
                "enum": ["hermetic", "external"],
                "description": "hermetic checks are computed in-binary; external results are reported in via .landsraad/checks/*.yaml"
              },
              "params": {
                "type": "object",
                "additionalProperties": { "type": "integer" }
              },
              "tiers": {
                "type": "object",
                "minProperties": 1,
                "propertyNames": { "pattern": "^[123]$" },
                "additionalProperties": { "enum": ["required", "warn", "info", "skip"] }
              }
            },
            "unevaluatedProperties": false
          }
        }
      },
      "unevaluatedProperties": false
    }
  },
  "unevaluatedProperties": false
}
```

Note `unevaluatedProperties: false` rather than `additionalProperties: false`, matching decision D7 in the spec — and note that `checks` uses `additionalProperties` in its *value-schema* sense, which is a different keyword meaning and is correct there.

- [ ] **Step 2: Write the failing test**

Create `internal/config/standards_test.go`:

```go
package config

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

const minimalStandards = `apiVersion: landsraad/v1
kind: Standards
spec:
  staleAfterDays: 7
  checks:
    owner-set: { tiers: {1: required, 2: required, 3: required} }
    runbook-present: { tiers: {1: required, 2: required, 3: warn} }
    docs-fresh: { params: {maxAgeDays: 90}, tiers: {1: warn, 2: warn, 3: info} }
    image-scanned: { source: external, tiers: {1: required, 2: required, 3: warn} }
`

func TestLoadStandardsReadsTheMatrix(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(minimalStandards), &c)
	if c.HasErrors() {
		t.Fatalf("a valid standards.yaml must load: %+v", c.Diagnostics())
	}
	if !s.Loaded() {
		t.Error("Loaded() = false after a clean load")
	}
	if got := s.Severity("runbook-present", 3); got != SevWarn {
		t.Errorf("Severity(runbook-present, 3) = %q, want %q", got, SevWarn)
	}
	if got := s.Severity("owner-set", 1); got != SevRequired {
		t.Errorf("Severity(owner-set, 1) = %q, want %q", got, SevRequired)
	}
	if got := s.StaleAfterDays(); got != 7 {
		t.Errorf("StaleAfterDays() = %d, want 7", got)
	}
	if !s.IsExternal("image-scanned") {
		t.Error("image-scanned declares source: external")
	}
	if s.IsExternal("owner-set") {
		t.Error("owner-set has no source, so it is hermetic")
	}
	if got := s.Param("docs-fresh", "maxAgeDays", 180); got != 90 {
		t.Errorf("Param(docs-fresh, maxAgeDays) = %d, want 90", got)
	}
	if got := s.Param("docs-fresh", "nope", 42); got != 42 {
		t.Errorf("an absent param must fall back, got %d", got)
	}
}

// Checks() is sorted so the scorecard's column order, the JSON output and the
// history CSV header are all stable between runs.
func TestStandardsChecksAreSorted(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(minimalStandards), &c)
	got := s.Checks()
	want := []string{"docs-fresh", "image-scanned", "owner-set", "runbook-present"}
	if len(got) != len(want) {
		t.Fatalf("Checks() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Checks()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A tier with no row in the matrix is skip, not required. Defaulting the other
// way would fail every entity for a check its team never configured.
func TestSeverityOfAnUnconfiguredTierIsSkip(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(
		"apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    owner-set: { tiers: {1: required} }\n"), &c)
	if got := s.Severity("owner-set", 2); got != SevSkip {
		t.Errorf("Severity(owner-set, 2) = %q, want %q", got, SevSkip)
	}
	if got := s.Severity("not-a-check", 1); got != SevSkip {
		t.Errorf("an unknown check is skip, got %q", got)
	}
}

// Ruling R1: an entity with no tier is not scored, so tier 0 has no row.
func TestSeverityOfTierZeroIsSkip(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(minimalStandards), &c)
	if got := s.Severity("owner-set", 0); got != SevSkip {
		t.Errorf("Severity(owner-set, 0) = %q, want %q — a Library is not scored", got, SevSkip)
	}
}

func TestLoadStandardsRejectsAnUnknownSeverity(t *testing.T) {
	var c diag.Collector
	LoadStandards("standards.yaml", []byte(
		"apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    owner-set: { tiers: {1: mandatory} }\n"), &c)
	if !c.HasErrors() {
		t.Fatal("an unknown severity must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "standards-schema" {
		t.Errorf("Check = %q, want %q", d.Check, "standards-schema")
	}
}

func TestLoadStandardsRejectsAnUnknownKey(t *testing.T) {
	var c diag.Collector
	LoadStandards("standards.yaml", []byte(
		"apiVersion: landsraad/v1\nkind: Standards\nspec:\n  stale_after_days: 7\n  checks:\n    owner-set: { tiers: {1: required} }\n"), &c)
	if !c.HasErrors() {
		t.Fatal("an unknown key must be rejected, as it is in teams.yaml and repos.yaml")
	}
}

// DefaultStandards is the matrix in spec §6, verbatim. A repository with no
// standards.yaml scores against the published defaults; scoring against
// nothing would report a perfect score having checked nothing.
func TestDefaultStandardsMatchesTheSpec(t *testing.T) {
	s := DefaultStandards()
	if !s.Loaded() {
		t.Error("the defaults are loaded by definition")
	}
	for _, tc := range []struct {
		check string
		tier  int
		want  Severity
	}{
		{"owner-set", 1, SevRequired},
		{"owner-set", 3, SevRequired},
		{"runbook-present", 3, SevWarn},
		{"alerts-parse", 3, SevInfo},
		{"slo-defined", 2, SevWarn},
		{"docs-fresh", 1, SevWarn},
		{"dashboard-resolves", 1, SevRequired},
		{"image-scanned", 3, SevWarn},
		{"otel-present", 3, SevInfo},
		{"deps-declared", 2, SevRequired},
	} {
		if got := s.Severity(tc.check, tc.tier); got != tc.want {
			t.Errorf("Severity(%s, %d) = %q, want %q", tc.check, tc.tier, got, tc.want)
		}
	}
	if got := s.StaleAfterDays(); got != 14 {
		t.Errorf("StaleAfterDays() = %d, want 14 (spec §6)", got)
	}
	if got := s.Param("docs-fresh", "maxAgeDays", 0); got != 180 {
		t.Errorf("docs-fresh maxAgeDays = %d, want 180 (spec §6)", got)
	}
	for _, ext := range []string{"dashboard-resolves", "image-scanned", "otel-present", "deps-declared"} {
		if !s.IsExternal(ext) {
			t.Errorf("%s is source: external in spec §6", ext)
		}
	}
	for _, herm := range []string{"owner-set", "runbook-present", "alerts-parse", "slo-defined", "docs-fresh"} {
		if s.IsExternal(herm) {
			t.Errorf("%s is hermetic in spec §9", herm)
		}
	}
}

// The embedded default must itself satisfy the published schema. A default
// that would be rejected if a user wrote it is a schema bug either way.
func TestDefaultStandardsValidatesAgainstTheSchema(t *testing.T) {
	var c diag.Collector
	LoadStandards("standards.yaml", DefaultStandardsYAML, &c)
	if c.HasErrors() {
		t.Errorf("the shipped defaults must validate: %+v", c.Diagnostics())
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/config/ -run Standards -v`
Expected: FAIL — `undefined: LoadStandards`.

- [ ] **Step 4: Write the implementation**

Create `internal/config/standards.go`:

```go
package config

import (
	_ "embed"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/schema"
)

//go:embed standards.schema.json
var StandardsSchema []byte

// DefaultStandardsYAML is spec §6's matrix, verbatim. It is the document a
// repository is scored against when it has no standards.yaml of its own, and
// it is kept as YAML rather than as a Go literal so that `landsraad init` can
// write it out and a team can start editing from exactly what they were
// already being scored against.
//
//go:embed standards.default.yaml
var DefaultStandardsYAML []byte

// Severity is what one check is worth for one tier.
type Severity string

const (
	// SevRequired fails the build under `score --fail-on required`.
	SevRequired Severity = "required"
	// SevWarn is applicable to the score but gates only under --fail-on warn.
	SevWarn Severity = "warn"
	// SevInfo is reported and does not affect the score (ruling R2).
	SevInfo Severity = "info"
	// SevSkip is not run and not counted.
	SevSkip Severity = "skip"
)

// CheckStandard is one row of the matrix.
type CheckStandard struct {
	// Source is "hermetic" (default) or "external". External checks are not
	// computed in-binary; their results are reported in via
	// .landsraad/checks/*.yaml (spec D3).
	Source string `yaml:"source"`
	// Params are per-check thresholds. Only docs-fresh uses one today.
	Params map[string]int `yaml:"params"`
	// Tiers maps tier to severity. A tier with no entry is SevSkip.
	Tiers map[int]Severity `yaml:"tiers"`
}

type standardsFile struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Spec       struct {
		StaleAfterDays int                      `yaml:"staleAfterDays"`
		Checks         map[string]CheckStandard `yaml:"checks"`
	} `yaml:"spec"`
}

// Standards is the loaded standards.yaml: the tier by severity matrix and the
// thresholds, and nothing else (spec D4). Checks themselves are Go functions
// with stable ids, so there is nothing to debug in YAML.
type Standards struct {
	checks         map[string]CheckStandard
	staleAfterDays int
	loaded         bool
}

// defaultStaleAfterDays is spec §6's value, applied when the document omits it.
const defaultStaleAfterDays = 14

// Loaded reports whether standards.yaml parsed. A Standards that did not parse
// is empty, which scores nothing — callers must not treat that as a clean run.
func (s *Standards) Loaded() bool { return s.loaded }

// Severity returns what check is worth at tier. An unknown check, an
// unconfigured tier, and tier 0 all return SevSkip.
//
// Skip rather than required is the safe default in both directions: defaulting
// to required would fail every entity for a check nobody configured, and teams
// would respond by deleting the check.
//
// Tier 0 means the entity has no tier, which the schema allows for kinds that
// cannot page anyone — a Library, a Topic (spec §12). Ruling R1: those are not
// scored.
func (s *Standards) Severity(check string, tier int) Severity {
	cs, ok := s.checks[check]
	if !ok {
		return SevSkip
	}
	sev, ok := cs.Tiers[tier]
	if !ok {
		return SevSkip
	}
	return sev
}

// Checks returns every configured check id, sorted, so the scorecard's column
// order, the JSON payload and the history CSV header are stable between runs.
func (s *Standards) Checks() []string {
	out := make([]string, 0, len(s.checks))
	for id := range s.checks {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// IsExternal reports whether results for this check are reported in rather
// than computed (spec D3).
func (s *Standards) IsExternal(check string) bool {
	return s.checks[check].Source == "external"
}

// Param returns a per-check threshold, or fallback when it is not configured.
func (s *Standards) Param(check, name string, fallback int) int {
	cs, ok := s.checks[check]
	if !ok {
		return fallback
	}
	v, ok := cs.Params[name]
	if !ok {
		return fallback
	}
	return v
}

// StaleAfterDays is how old an ingested result may be before it renders as
// stale rather than pass.
//
// This is one of two deliberately distinct staleness clocks (spec §6). The
// other is docs-fresh.params.maxAgeDays, which ages out service documentation.
// They answer different questions and are tuned independently.
func (s *Standards) StaleAfterDays() int { return s.staleAfterDays }

// DefaultStandards is the matrix a repository is scored against when it has no
// standards.yaml. Parsing the embedded document rather than building a Go
// literal is what keeps the default and the published schema from drifting.
func DefaultStandards() *Standards {
	var discard diag.Collector
	s := LoadStandards("standards.yaml", DefaultStandardsYAML, &discard)
	if !s.loaded {
		// Unreachable in a working binary: TestDefaultStandardsValidatesAgainstTheSchema
		// fails first. Panicking rather than returning an empty Standards is
		// deliberate — an empty matrix scores nothing and reports a perfect
		// score, which is the failure mode this project exists to prevent.
		panic("embedded default standards do not parse: " + fmt.Sprint(discard.Diagnostics()))
	}
	return s
}

// LoadStandards reads standards.yaml, always returning a usable value.
func LoadStandards(path string, data []byte, c *diag.Collector) *Standards {
	s := &Standards{checks: map[string]CheckStandard{}, staleAfterDays: defaultStaleAfterDays}

	// Schema first, for the precise messages, exactly as catalog files are
	// handled: the schema is the single source of truth for structure.
	v, err := schema.New(StandardsSchema)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: 1,
			Check:   "standards-schema",
			Message: fmt.Sprintf("cannot compile the standards schema: %v", err),
		})
		return s
	}
	if !v.Validate("", path, data, c) {
		return s
	}

	var f standardsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		for _, d := range yamlDiagnostics(path, "standards-parse", "standards file", standardsParseHint, err) {
			c.Add(d)
		}
		return s
	}
	s.checks = f.Spec.Checks
	if f.Spec.StaleAfterDays > 0 {
		s.staleAfterDays = f.Spec.StaleAfterDays
	}
	s.loaded = true
	return s
}

const standardsParseHint = "standards.yaml is a `checks:` mapping under `spec:`, each check with a `tiers:` map of tier to required|warn|info|skip"
```

**Also required, in the same step:** add the new struct type to `configNouns` in `internal/config/yamlerr.go`:

```go
"config.CheckStandard": {Singular: "check standard", Plural: "check standards"},
```

`TestNounsCoverEveryFieldType` in `internal/config/yamlerr_test.go` walks `teamsFile` and `Repos`; extend its loop to include `reflect.TypeOf(standardsFile{})`. If you skip this, the test will not fail — it does not know about the new struct — and a decode error will silently read "expected a value". That is exactly the defect Plan 1's audit finding 3 closed; do not reopen it.

- [ ] **Step 5: Create the default standards document**

Create `internal/config/standards.default.yaml`, copied verbatim from spec §6:

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

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/config/ -v`
Expected: PASS, including `TestNounsCoverEveryFieldType` after you extend it.

- [ ] **Step 7: Export the schema from `task schema`**

In `Taskfile.yml`, extend the `schema` task so editors can autocomplete `standards.yaml` too. Add a `--kind` flag to the existing `schema` command in `cmd/landsraad/` if one does not exist; if adding a flag is more than a few lines, write the file directly instead and note it:

```yaml
  schema:
    desc: Export the JSON Schemas to schema/ for editor autocompletion
    cmds:
      - mkdir -p schema
      - go run ./cmd/landsraad schema > schema/service.schema.json
      - go run ./cmd/landsraad schema --kind standards > schema/standards.schema.json
```

- [ ] **Step 8: Commit**

```bash
git add internal/config/standards.go internal/config/standards_test.go \
        internal/config/standards.schema.json internal/config/standards.default.yaml \
        internal/config/yamlerr.go internal/config/yamlerr_test.go Taskfile.yml cmd/landsraad
git commit -m "feat: load standards.yaml, the tier x severity matrix

Spec D4: checks are Go funcs with stable ids and standards.yaml holds
only the matrix and thresholds, so there is nothing to debug in YAML.

A repository with no standards.yaml is scored against spec §6's
published defaults, parsed from an embedded copy of that exact document
rather than a Go literal, so the default and the schema cannot drift.
Scoring against an empty matrix would report a perfect score having
checked nothing — this project's signature failure.

An unconfigured tier is skip rather than required in both directions:
defaulting to required fails every entity for a check nobody configured,
and teams respond by deleting the check."
```

---

### Task 6: The check vocabulary and the four cheap hermetic checks

**Files:**
- Create: `internal/scorecard/check.go`
- Create: `internal/scorecard/hermetic.go`
- Create: `internal/scorecard/hermetic_test.go`

**Interfaces:**
- Consumes: `*catalog.Entity`, `catalog.Ref`; `io/fs.FS`.
- Produces:
  - `type scorecard.Status string` with `StatusPass`, `StatusFail`, `StatusError`, `StatusNotReported`, `StatusStale`, `StatusExempt`
  - `type scorecard.Result struct { Check string; Status Status; Detail string; URL string }`
  - `type scorecard.Env struct { FS fs.FS; Now time.Time; MaxDocsAgeDays int; LastEdit LastEditFunc }`
  - `type scorecard.LastEditFunc func(path string) (time.Time, bool)`
  - `type scorecard.Check struct { ID string; Run func(*catalog.Entity, Env) Result }`
  - `func scorecard.HermeticChecks() []Check`

**Context:** Spec §9 names five hermetic checks: `owner-set`, `runbook-present`, `alerts-parse`, `slo-defined`, `docs-fresh`. This task implements the first four; `docs-fresh` needs a last-edit date and gets Task 7 to itself.

Spec D3: "`landsraad score` must run offline in under a second with no Docker daemon and no network egress." Every check here reads only the entity and the injected `fs.FS`.

`HermeticChecks()` returns a fresh slice, not a package-level variable — the same shape as `catalog.AllKinds()` and for the same reason.

`Detail` is what the scorecard shows next to a failure, and it is the difference between a scorecard people act on and one they ignore. "fail" is useless; "no runbook: spec.runbook is unset" fixes itself.

- [ ] **Step 1: Write the failing test**

Create `internal/scorecard/hermetic_test.go`:

```go
package scorecard

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
)

func svc(name string) *catalog.Entity {
	e := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindService}
	e.Metadata.Name = name
	e.Metadata.Owner = "team-payments"
	e.Metadata.Tier = 1
	e.SourcePath = "services/" + name + "/service.yaml"
	e.NameLine = 4
	return e
}

func env(files fstest.MapFS) Env {
	return Env{
		FS:             files,
		Now:            time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		MaxDocsAgeDays: 180,
		LastEdit:       func(string) (time.Time, bool) { return time.Time{}, false },
	}
}

// run finds a check by id and runs it, failing the test when the id is not
// registered — a typo in a check id would otherwise silently test nothing.
func run(t *testing.T, id string, e *catalog.Entity, en Env) Result {
	t.Helper()
	for _, c := range HermeticChecks() {
		if c.ID == id {
			return c.Run(e, en)
		}
	}
	t.Fatalf("no hermetic check with id %q; have %v", id, checkIDs())
	return Result{}
}

func checkIDs() []string {
	var out []string
	for _, c := range HermeticChecks() {
		out = append(out, c.ID)
	}
	return out
}

func TestHermeticChecksHaveTheIDsTheSpecNames(t *testing.T) {
	want := map[string]bool{
		"owner-set": false, "runbook-present": false,
		"alerts-parse": false, "slo-defined": false, "docs-fresh": false,
	}
	for _, c := range HermeticChecks() {
		if _, known := want[c.ID]; !known {
			t.Errorf("unexpected hermetic check %q — spec §9 names five", c.ID)
			continue
		}
		want[c.ID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("spec §9 names %q as hermetic, but it is not registered", id)
		}
	}
}

// A check id is a stable contract: it appears in standards.yaml, in
// .landsraad/checks/*.yaml written by other people's CI, and in the history
// CSV. Renaming one silently rewrites history.
func TestHermeticChecksReturnsAFreshSlice(t *testing.T) {
	first := HermeticChecks()
	first[0].ID = "clobbered"
	if HermeticChecks()[0].ID == "clobbered" {
		t.Error("HermeticChecks() handed out state a caller could rewrite")
	}
}

func TestOwnerSet(t *testing.T) {
	e := svc("api")
	if got := run(t, "owner-set", e, env(nil)); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got.Status)
	}
	e.Metadata.Owner = ""
	got := run(t, "owner-set", e, env(nil))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	if got.Detail != "metadata.owner is unset" {
		t.Errorf("Detail = %q, want %q", got.Detail, "metadata.owner is unset")
	}
}

func TestRunbookPresentRequiresANonEmptyFile(t *testing.T) {
	e := svc("api")
	e.Spec.Runbook = "services/api/docs/runbook.md"

	files := fstest.MapFS{"services/api/docs/runbook.md": {Data: []byte("# Runbook\n\nRestart it.\n")}}
	if got := run(t, "runbook-present", e, env(files)); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got.Status)
	}

	// Spec §5.3 says "runbook exists and non-empty". A file holding only a
	// heading is the shape a scaffold leaves behind, and treating it as a pass
	// is how a scorecard comes to certify a runbook nobody wrote.
	empty := fstest.MapFS{"services/api/docs/runbook.md": {Data: []byte("# Runbook\n")}}
	got := run(t, "runbook-present", e, env(empty))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail for a heading-only runbook", got.Status)
	}
	if got.Detail != "services/api/docs/runbook.md has a heading and no content" {
		t.Errorf("Detail = %q", got.Detail)
	}

	e.Spec.Runbook = ""
	if got := run(t, "runbook-present", e, env(files)); got.Detail != "spec.runbook is unset" {
		t.Errorf("Detail = %q, want %q", got.Detail, "spec.runbook is unset")
	}
}

func TestAlertsParse(t *testing.T) {
	e := svc("api")
	e.Spec.Alerts = "services/api/alerts.yaml"

	good := fstest.MapFS{"services/api/alerts.yaml": {Data: []byte(
		"groups:\n  - name: api\n    rules:\n      - alert: HighErrorRate\n        expr: rate(errors[5m]) > 0.05\n")}}
	if got := run(t, "alerts-parse", e, env(good)); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got.Status)
	}

	// A rules file that parses as YAML but declares no groups is a file that
	// alerts on nothing. Prometheus loads it without complaint.
	emptyGroups := fstest.MapFS{"services/api/alerts.yaml": {Data: []byte("groups: []\n")}}
	got := run(t, "alerts-parse", e, env(emptyGroups))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail for zero groups", got.Status)
	}
	if got.Detail != "services/api/alerts.yaml declares no alert groups" {
		t.Errorf("Detail = %q", got.Detail)
	}

	broken := fstest.MapFS{"services/api/alerts.yaml": {Data: []byte("groups:\n  - name: api\n   rules: []\n")}}
	if got := run(t, "alerts-parse", e, env(broken)); got.Status != StatusError {
		t.Errorf("Status = %q, want error for unparseable YAML", got.Status)
	}
}

func TestSLODefined(t *testing.T) {
	e := svc("api")
	if got := run(t, "slo-defined", e, env(nil)); got.Status != StatusFail {
		t.Errorf("Status = %q, want fail with no SLO", got.Status)
	}

	e.Spec.SLO = []catalog.SLO{{Name: "availability", Target: "99.9%", Window: "30d"}}
	if got := run(t, "slo-defined", e, env(nil)); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got.Status)
	}

	// An SLO with no target is a name, not an objective.
	e.Spec.SLO = []catalog.SLO{{Name: "availability"}}
	got := run(t, "slo-defined", e, env(nil))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	if got.Detail != `SLO "availability" has no target` {
		t.Errorf("Detail = %q", got.Detail)
	}
}

// A check must never report pass for a file it could not read. That is the
// exit-0-on-something-unexamined failure, inside a single check.
func TestChecksReportErrorRatherThanPassOnAnUnreadableFile(t *testing.T) {
	e := svc("api")
	e.Spec.Runbook = "services/api/docs/runbook.md"
	e.Spec.Alerts = "services/api/alerts.yaml"

	for _, id := range []string{"runbook-present", "alerts-parse"} {
		got := run(t, id, e, env(fstest.MapFS{}))
		if got.Status == StatusPass {
			t.Errorf("%s returned pass for a file that does not exist", id)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/scorecard/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the vocabulary**

Create `internal/scorecard/check.go`:

```go
// Package scorecard measures entities against the team's standard.
//
// Spec D3 splits checks in two. Hermetic checks are computed in-binary and
// must run offline in under a second with no Docker daemon and no network
// egress. Expensive checks — a build, a scanner, an HTTP probe — are reported
// *into* the tool via .landsraad/checks/*.yaml, written by the CI jobs that
// already know the answer. The extension point is YAML, not a plugin API.
//
// Nothing here touches the filesystem directly, reads the clock, or reaches
// the network: everything outside a check comes in through Env.
package scorecard

import (
	"io/fs"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
)

// Status is the outcome of one check for one entity.
//
// Six values, not two, because the difference between them is what makes a
// scorecard trustworthy. "Not reported" and "stale" are specifically not
// failures of the service — they are failures of the evidence — and rendering
// them as fail would send owners hunting for a problem in the wrong place.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	// StatusError: the check could not reach a verdict — an unreadable file, a
	// results file that does not parse. Distinct from fail: the service may be
	// perfectly fine and the tool cannot tell.
	StatusError Status = "error"
	// StatusNotReported: an external check with no result at all (spec §9,
	// "distinct from both pass and fail").
	StatusNotReported Status = "not-reported"
	// StatusStale: an external result older than staleAfterDays. An image scan
	// from March is not evidence about today (spec §6).
	StatusStale Status = "stale"
	// StatusExempt: waived by spec.exemptions with a stated reason.
	StatusExempt Status = "exempt"
)

// Passed reports whether this status counts towards the numerator.
//
// Only pass does. In particular not-reported and stale do not, while remaining
// applicable (ruling R3): if they were excluded from the denominator instead,
// deleting a CI job would raise a team's score, which is the one incentive
// this product must never create.
func (s Status) Passed() bool { return s == StatusPass }

// Result is one check's verdict for one entity.
type Result struct {
	Check  string
	Status Status
	// Detail is what the scorecard shows next to the verdict. "fail" is
	// useless; "spec.runbook is unset" fixes itself. It is the difference
	// between a scorecard people act on and one they ignore.
	Detail string
	// URL points at the evidence, for ingested results that carry one.
	URL string
}

// LastEditFunc reports when a path was last changed, and whether that is
// known at all.
//
// It is injected because the answer comes from git, and nothing under
// internal/ may shell out or touch os. In the local repository cmd/ supplies
// it from `git log`; for a repository fetched over a host API there is no git
// history and it costs one API call per service (spec §9, "known cost of D5").
// A fetcher that cannot answer returns false, and docs-fresh reports
// not-reported rather than inventing a date.
type LastEditFunc func(path string) (time.Time, bool)

// Env is everything a check needs from outside itself.
//
// Now is a value rather than a call to time.Now() so scoring is a pure
// function of its inputs: a check that reads the clock has tests that fail at
// midnight and a result that cannot be reproduced from a commit.
type Env struct {
	FS             fs.FS
	Now            time.Time
	MaxDocsAgeDays int
	LastEdit       LastEditFunc
}

// Check is one measurable property, with a stable id.
//
// The id is a contract: it appears in standards.yaml, in .landsraad/checks
// files written by CI in other people's repositories, and in every row of
// scorecard-history.csv. Renaming one silently rewrites history.
type Check struct {
	ID  string
	Run func(e *catalog.Entity, env Env) Result
}
```

- [ ] **Step 4: Write the four checks**

Create `internal/scorecard/hermetic.go`:

```go
package scorecard

import (
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/catalog"
)

// HermeticChecks returns the checks computed in-binary (spec §9), fresh on
// each call so a caller cannot rewrite what the package believes.
func HermeticChecks() []Check {
	return []Check{
		{ID: "owner-set", Run: ownerSet},
		{ID: "runbook-present", Run: runbookPresent},
		{ID: "alerts-parse", Run: alertsParse},
		{ID: "slo-defined", Run: sloDefined},
		{ID: "docs-fresh", Run: docsFresh},
	}
}

func ownerSet(e *catalog.Entity, _ Env) Result {
	if strings.TrimSpace(e.Metadata.Owner) == "" {
		return Result{Check: "owner-set", Status: StatusFail, Detail: "metadata.owner is unset"}
	}
	return Result{Check: "owner-set", Status: StatusPass, Detail: e.Metadata.Owner}
}

// runbookPresent requires a runbook that exists and says something.
//
// Spec §5.3 words it as "runbook exists and non-empty". A file holding only a
// heading is what a scaffold leaves behind, and counting it as a pass is how a
// scorecard comes to certify a runbook nobody wrote — the rot this product
// exists to make visible, certified by the product.
func runbookPresent(e *catalog.Entity, env Env) Result {
	const id = "runbook-present"
	if e.Spec.Runbook == "" {
		return Result{Check: id, Status: StatusFail, Detail: "spec.runbook is unset"}
	}
	data, err := fs.ReadFile(env.FS, e.Spec.Runbook)
	if err != nil {
		// Never pass for a file that could not be read: that is the
		// exit-0-on-something-unexamined failure inside a single check.
		return Result{Check: id, Status: StatusError,
			Detail: fmt.Sprintf("cannot read %s", e.Spec.Runbook)}
	}
	if bodyIsEmpty(data) {
		return Result{Check: id, Status: StatusFail,
			Detail: fmt.Sprintf("%s has a heading and no content", e.Spec.Runbook)}
	}
	return Result{Check: id, Status: StatusPass, Detail: e.Spec.Runbook}
}

// bodyIsEmpty reports whether a Markdown document has no content beyond
// headings, blank lines and HTML comments.
func bodyIsEmpty(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#"):
		case strings.HasPrefix(t, "<!--"):
		default:
			return false
		}
	}
	return true
}

// alertRules is the subset of the Prometheus rules format landsraad reads. It
// is deliberately shallow: this check answers "does this file define alerts",
// not "are these good alerts", and promtool is the right tool for the latter.
type alertRules struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert  string `yaml:"alert"`
			Record string `yaml:"record"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

func alertsParse(e *catalog.Entity, env Env) Result {
	const id = "alerts-parse"
	if e.Spec.Alerts == "" {
		return Result{Check: id, Status: StatusFail, Detail: "spec.alerts is unset"}
	}
	data, err := fs.ReadFile(env.FS, e.Spec.Alerts)
	if err != nil {
		return Result{Check: id, Status: StatusError,
			Detail: fmt.Sprintf("cannot read %s", e.Spec.Alerts)}
	}
	var rules alertRules
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return Result{Check: id, Status: StatusError,
			Detail: fmt.Sprintf("%s does not parse as Prometheus rules", e.Spec.Alerts)}
	}
	if len(rules.Groups) == 0 {
		// Prometheus loads a file with no groups without complaint, and it
		// alerts on nothing.
		return Result{Check: id, Status: StatusFail,
			Detail: fmt.Sprintf("%s declares no alert groups", e.Spec.Alerts)}
	}
	alerts := 0
	for _, g := range rules.Groups {
		for _, r := range g.Rules {
			if r.Alert != "" {
				alerts++
			}
		}
	}
	if alerts == 0 {
		return Result{Check: id, Status: StatusFail,
			Detail: fmt.Sprintf("%s defines only recording rules, no alerts", e.Spec.Alerts)}
	}
	return Result{Check: id, Status: StatusPass,
		Detail: fmt.Sprintf("%d alert rules", alerts)}
}

func sloDefined(e *catalog.Entity, _ Env) Result {
	const id = "slo-defined"
	if len(e.Spec.SLO) == 0 {
		return Result{Check: id, Status: StatusFail, Detail: "spec.slo is empty"}
	}
	for _, s := range e.Spec.SLO {
		// A name with no target is a label, not an objective.
		if strings.TrimSpace(s.Target) == "" {
			return Result{Check: id, Status: StatusFail,
				Detail: fmt.Sprintf("SLO %q has no target", s.Name)}
		}
	}
	return Result{Check: id, Status: StatusPass,
		Detail: fmt.Sprintf("%d defined", len(e.Spec.SLO))}
}
```

`docsFresh` does not exist yet; Task 7 adds it. To keep this task's tests green, add a temporary stub at the bottom of `hermetic.go` **and delete it in Task 7**:

```go
// docsFresh is implemented in Task 7, which adds the last-edit plumbing.
func docsFresh(_ *catalog.Entity, _ Env) Result {
	return Result{Check: "docs-fresh", Status: StatusNotReported, Detail: "not implemented"}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/scorecard/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scorecard/check.go internal/scorecard/hermetic.go internal/scorecard/hermetic_test.go
git commit -m "feat: the check vocabulary and four hermetic checks

Spec D3: hermetic checks run offline in under a second with no Docker
daemon and no network egress, so every one of these reads only the
entity and an injected fs.FS. Now is a value, not a call to time.Now():
a check that reads the clock has tests that fail at midnight and a
verdict that cannot be reproduced from a commit.

Status has six values rather than two because the distinctions are what
make a scorecard trustworthy. not-reported and stale are failures of the
evidence, not of the service, and rendering them as fail sends owners
hunting in the wrong place.

Two checks are deliberately stricter than they look. A runbook holding
only a heading fails: that is what a scaffold leaves behind, and passing
it is how a scorecard certifies a runbook nobody wrote. An alerts file
with zero groups fails: Prometheus loads it without complaint and it
alerts on nothing.

No check returns pass for a file it could not read — that is the
exit-0-on-something-unexamined failure, inside a single check."
```

---

### Task 7: `docs-fresh` and the injected last-edit clock

**Files:**
- Modify: `internal/scorecard/hermetic.go` (replace the Task 6 stub)
- Modify: `internal/scorecard/hermetic_test.go` (add cases)
- Create: `cmd/landsraad/lastedit.go`
- Create: `cmd/landsraad/lastedit_test.go`

**Interfaces:**
- Consumes: `scorecard.Env`, `scorecard.LastEditFunc`, `scorecard.Result` (Task 6).
- Produces:
  - `func gitLastEdit(root string) scorecard.LastEditFunc` — in `cmd/`, shells to `git log`
  - `func noLastEdit() scorecard.LastEditFunc` — always `(time.Time{}, false)`, for a repository with no git history

**Context:** Spec §9's "Known cost of D5": "`docs-fresh` needs a last-edit date. In the local repo that is `git log`, free. For repos fetched over a host API there is no git history, so it costs one `GET /commits?path=…&per_page=1` per service. Tolerable at a dozen services; documented in the call budget rather than silently dropped for remote repos."

The date therefore comes from outside the package, injected as `Env.LastEdit`. `internal/` may not import `os/exec` — the hook blocks it and `scripts/check-rules.sh` blocks it in CI — so the git invocation lives in `cmd/`.

**When the date is unknown, `docs-fresh` reports `not-reported`, never `pass`.** A fetched repository with no git history would otherwise silently score full marks for documentation freshness, which is the failure this project keeps finding in new corners.

The threshold is `docs-fresh.params.maxAgeDays`, default 180 (spec §6). It is a different clock from `spec.staleAfterDays`, which ages out ingested results; conflating them is the mistake this comment exists to prevent.

- [ ] **Step 1: Write the failing test**

Append to `internal/scorecard/hermetic_test.go`:

```go
func TestDocsFreshUsesTheInjectedLastEditDate(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/index.md": {Data: []byte("# Docs\n\nreal content\n")}}

	base := env(files)
	recent := base
	recent.LastEdit = func(string) (time.Time, bool) {
		return base.Now.AddDate(0, 0, -10), true
	}
	if got := docsFreshFor(t, e, recent); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass for docs edited 10 days ago", got.Status)
	}

	old := base
	old.LastEdit = func(string) (time.Time, bool) {
		return base.Now.AddDate(0, 0, -365), true
	}
	got := docsFreshFor(t, e, old)
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail for docs edited 365 days ago", got.Status)
	}
	if got.Detail != "services/api/docs last edited 365 days ago, limit is 180" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

// The boundary: exactly maxAgeDays old is still fresh. An off-by-one here
// flips a whole tier of services on the day the threshold changes.
func TestDocsFreshBoundaryIsInclusive(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/index.md": {Data: []byte("# Docs\n\nc\n")}}

	en := env(files)
	base := en.Now
	en.LastEdit = func(string) (time.Time, bool) { return base.AddDate(0, 0, -180), true }
	if got := docsFreshFor(t, e, en); got.Status != StatusPass {
		t.Errorf("exactly at the limit must pass, got %q", got.Status)
	}
	en.LastEdit = func(string) (time.Time, bool) { return base.AddDate(0, 0, -181), true }
	if got := docsFreshFor(t, e, en); got.Status != StatusFail {
		t.Errorf("one day past the limit must fail, got %q", got.Status)
	}
}

// A repository fetched over a host API has no git history. Reporting pass
// would silently give every such service full marks for documentation
// freshness — the failure this project keeps finding in new corners.
func TestDocsFreshReportsNotReportedWhenTheDateIsUnknown(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/index.md": {Data: []byte("# Docs\n\nc\n")}}

	got := docsFreshFor(t, e, env(files))
	if got.Status != StatusNotReported {
		t.Errorf("Status = %q, want not-reported when no last-edit date is available", got.Status)
	}
	if got.Detail != "no last-edit date available for services/api/docs" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

// Spec §5.3 pairs freshness with an index: "Docs index and last edit < 180
// days". Docs with no index page is a directory, not documentation.
func TestDocsFreshRequiresAnIndex(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	en := env(fstest.MapFS{"services/api/docs/other.md": {Data: []byte("x\n")}})
	en.LastEdit = func(string) (time.Time, bool) { return en.Now, true }

	got := docsFreshFor(t, e, en)
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	if got.Detail != "services/api/docs has no index.md" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

func TestDocsFreshFailsWhenDocsAreUnset(t *testing.T) {
	got := docsFreshFor(t, svc("api"), env(nil))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	if got.Detail != "spec.docs is unset" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

func docsFreshFor(t *testing.T, e *catalog.Entity, en Env) Result {
	t.Helper()
	return run(t, "docs-fresh", e, en)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/scorecard/ -run TestDocsFresh -v`
Expected: FAIL — the stub returns `not-reported` with detail `"not implemented"`, so the pass/fail cases fail.

- [ ] **Step 3: Replace the stub**

In `internal/scorecard/hermetic.go`, delete the Task 6 stub and add:

```go
// docsFresh answers spec §5.3's pairing: a docs index exists, and it was
// edited recently enough to be believable.
//
// The date is injected (Env.LastEdit) rather than read here, because it comes
// from git and nothing under internal/ may shell out. Spec §9 records the
// consequence: free in a local repository, one API call per service for a
// fetched one.
//
// The threshold is docs-fresh.params.maxAgeDays, default 180. It is NOT
// spec.staleAfterDays — that clock ages out ingested check results. Two
// clocks, deliberately, answering different questions (spec §6).
func docsFresh(e *catalog.Entity, env Env) Result {
	const id = "docs-fresh"
	if e.Spec.Docs == "" {
		return Result{Check: id, Status: StatusFail, Detail: "spec.docs is unset"}
	}

	index := pathpkg.Join(e.Spec.Docs, "index.md")
	if _, err := fs.Stat(env.FS, index); err != nil {
		// Docs with no index page is a directory, not documentation.
		return Result{Check: id, Status: StatusFail,
			Detail: fmt.Sprintf("%s has no index.md", e.Spec.Docs)}
	}

	if env.LastEdit == nil {
		return Result{Check: id, Status: StatusNotReported,
			Detail: fmt.Sprintf("no last-edit date available for %s", e.Spec.Docs)}
	}
	edited, ok := env.LastEdit(e.Spec.Docs)
	if !ok {
		// A repository fetched over a host API has no git history. Passing
		// here would give every such service full marks for freshness.
		return Result{Check: id, Status: StatusNotReported,
			Detail: fmt.Sprintf("no last-edit date available for %s", e.Spec.Docs)}
	}

	age := int(env.Now.Sub(edited).Hours() / 24)
	limit := env.MaxDocsAgeDays
	if limit <= 0 {
		limit = 180
	}
	if age > limit {
		return Result{Check: id, Status: StatusFail,
			Detail: fmt.Sprintf("%s last edited %d days ago, limit is %d", e.Spec.Docs, age, limit)}
	}
	return Result{Check: id, Status: StatusPass,
		Detail: fmt.Sprintf("edited %d days ago", age)}
}
```

Add `pathpkg "path"` to the import block. Use `path`, never `path/filepath`: `fs.FS` paths are always slash-separated, and `filepath` would produce backslashes on Windows and silently fail every lookup.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/scorecard/ -v`
Expected: PASS.

- [ ] **Step 5: Write the git side, in `cmd/`**

Create `cmd/landsraad/lastedit.go`:

```go
package main

import (
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// gitLastEdit answers "when was this path last changed" from git history.
//
// It lives in cmd/ because internal/ may not import os/exec — that rule is
// what keeps every stage below here testable in memory, and spec §9 records
// this as the one place the answer has to come from outside.
//
// The result is cached per path: `git log` is cheap but a repository with
// forty entities would otherwise fork forty processes for the same handful of
// directories.
func gitLastEdit(root string) scorecard.LastEditFunc {
	cache := map[string]struct {
		t  time.Time
		ok bool
	}{}
	return func(p string) (time.Time, bool) {
		if hit, seen := cache[p]; seen {
			return hit.t, hit.ok
		}
		t, ok := gitLastEditUncached(root, p)
		cache[p] = struct {
			t  time.Time
			ok bool
		}{t, ok}
		return t, ok
	}
}

func gitLastEditUncached(root, p string) (time.Time, bool) {
	// %ct is the committer date as a Unix timestamp: no locale, no timezone
	// parsing, no ambiguity.
	cmd := exec.Command("git", "-C", root, "log", "-1", "--format=%ct", "--", p)
	out, err := cmd.Output()
	if err != nil {
		// Not a git repository, git not installed, or the path has no
		// history. All three mean the same thing to the caller: unknown.
		// Reporting a date we do not have would silently pass docs-fresh.
		return time.Time{}, false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, false
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(secs, 0).UTC(), true
}

// noLastEdit is the answer for a repository with no git history: a fetched
// tree, or a directory that was never a repository. docs-fresh renders
// not-reported for every entity, which is the honest answer.
func noLastEdit() scorecard.LastEditFunc {
	return func(string) (time.Time, bool) { return time.Time{}, false }
}
```

- [ ] **Step 6: Test the git side against a real repository**

Create `cmd/landsraad/lastedit_test.go`:

```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This test needs a real git repository, which is why it lives in cmd/ — the
// rule that keeps tests off disk applies to internal/, and this code exists
// precisely to be the boundary where that stops being possible.
func TestGitLastEditReadsRealHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "index.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "docs/index.md")
	runGit("commit", "-qm", "add docs")

	got, ok := gitLastEdit(root)("docs")
	if !ok {
		t.Fatal("a committed path must have a last-edit date")
	}
	if time.Since(got) > time.Hour {
		t.Errorf("last edit = %v, want approximately now", got)
	}
}

// The honest answer for a path git knows nothing about is "unknown", never a
// date. A date we do not have silently passes docs-fresh.
func TestGitLastEditReportsUnknownOutsideARepository(t *testing.T) {
	if _, ok := gitLastEdit(t.TempDir())("docs"); ok {
		t.Error("a directory that is not a git repository has no history")
	}
}

func TestNoLastEditAlwaysReportsUnknown(t *testing.T) {
	if _, ok := noLastEdit()("anything"); ok {
		t.Error("noLastEdit must never claim to know a date")
	}
}
```

- [ ] **Step 7: Run everything and commit**

Run: `task ci`

```bash
git add internal/scorecard/hermetic.go internal/scorecard/hermetic_test.go \
        cmd/landsraad/lastedit.go cmd/landsraad/lastedit_test.go
git commit -m "feat: docs-fresh, with the last-edit date injected

Spec §9's known cost of D5: the date comes from git log locally and from
one API call per service for a fetched repository. Either way it comes
from outside internal/, which may not shell out, so it arrives as
Env.LastEdit.

When the date is unknown, docs-fresh reports not-reported rather than
pass. A fetched repository with no git history would otherwise give
every service full marks for documentation freshness — the same
exit-0-over-something-unexamined this project has now found five times.

The threshold is docs-fresh.params.maxAgeDays. It is not
spec.staleAfterDays, which ages out ingested results; two clocks
answering different questions, per spec §6, and the comment says so
because conflating them is the obvious mistake."
```

---

### Task 8: INGEST — `.landsraad/checks/*.yaml`

**Files:**
- Create: `internal/scorecard/ingest.go`
- Create: `internal/scorecard/ingest_test.go`
- Create: `internal/scorecard/ingest.schema.json`

**Interfaces:**
- Consumes: `Status`, `Result` (Task 6); `catalog.Ref`, `catalog.ParseRef`, `*catalog.Catalog`, `(*Catalog).Lookup`; `*diag.Collector`; `schema.New`.
- Produces:
  - `const scorecard.ChecksDir = ".landsraad/checks"`
  - `type scorecard.Reported struct { Result Result; GeneratedAt time.Time; Producer string; SourceFile string }`
  - `func scorecard.Ingest(fsys fs.FS, cat *catalog.Catalog, staleAfterDays int, now time.Time, c *diag.Collector) map[catalog.Ref]map[string]Reported`
  - `var scorecard.CheckResultsSchema []byte`

**Context:** Spec §6 defines the file and states three rules "that are cheap now and permanent support load if left undefined", because this file is written by CI jobs **in other people's repositories** — the most expensive contract in the product to change later:

1. **`entity` is a ref** (`kind:name`), not a bare name. A bare name is accepted as a deprecated alias, **resolved only when unambiguous**.
2. **`generatedAt` is RFC 3339 with an explicit offset.** The `staleAfterDays` arithmetic depends on it.
3. **Precedence** when two producers report the same `(entity, check)`: newest `generatedAt` wins, **and a tie is an error rather than a coin flip**.

Also: `status` is `pass | fail | error`; a result older than `staleAfterDays` renders as **stale**, not pass. Staleness is wall-clock, not per-commit.

Every one of those is a test below. The deprecated bare-name alias in particular has a sharp edge: `orders` is unambiguous until someone adds `topic:orders`, at which point a previously-working CI job starts erroring. That is the correct behaviour — silently picking one would route evidence to the wrong entity — and the diagnostic must say what to change.

- [ ] **Step 1: Write the schema**

Create `internal/scorecard/ingest.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "check-results.schema.json",
  "title": "landsraad check results",
  "description": "Results reported in by CI for checks declared source: external.",
  "type": "object",
  "required": ["apiVersion", "kind", "generatedAt", "results"],
  "properties": {
    "apiVersion": { "const": "landsraad/v1" },
    "kind": { "const": "CheckResults" },
    "producer": {
      "type": "string",
      "minLength": 1,
      "description": "What wrote this file, e.g. ci/image-scan. Named in the diagnostic when two producers tie."
    },
    "generatedAt": {
      "type": "string",
      "format": "date-time",
      "description": "RFC 3339 with an explicit offset. The staleAfterDays arithmetic depends on it."
    },
    "results": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "required": ["entity", "check", "status"],
        "properties": {
          "entity": {
            "type": "string",
            "minLength": 1,
            "description": "A ref: kind:name. A bare name is a deprecated alias, resolved only when unambiguous."
          },
          "check": { "type": "string", "minLength": 1 },
          "status": { "enum": ["pass", "fail", "error"] },
          "detail": { "type": "string" },
          "url": { "type": "string" }
        },
        "unevaluatedProperties": false
      }
    }
  },
  "unevaluatedProperties": false
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/scorecard/ingest_test.go`:

```go
package scorecard

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

var now = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func catalogOf(t *testing.T, entities ...*catalog.Entity) *catalog.Catalog {
	t.Helper()
	var c diag.Collector
	cat := catalog.NewCatalog(entities, &c)
	if c.HasErrors() {
		t.Fatalf("fixture catalog must build cleanly: %+v", c.Diagnostics())
	}
	return cat
}

func resultsFile(generatedAt, body string) []byte {
	return []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/test\ngeneratedAt: " +
		generatedAt + "\nresults:\n" + body)
}

func TestIngestReadsAResultKeyedByRef(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: service:api, check: image-scanned, status: pass, detail: \"0 critical\", url: \"https://ci/1\" }\n")},
	}
	var c diag.Collector

	got := Ingest(fsys, cat, 14, now, &c)

	if c.HasErrors() {
		t.Fatalf("a well-formed results file must ingest cleanly: %+v", c.Diagnostics())
	}
	ref := catalog.Ref{Kind: catalog.KindService, Name: "api"}
	r, ok := got[ref]["image-scanned"]
	if !ok {
		t.Fatalf("no result for %s/image-scanned; got %+v", ref, got)
	}
	if r.Result.Status != StatusPass {
		t.Errorf("Status = %q, want pass", r.Result.Status)
	}
	if r.Result.Detail != "0 critical" {
		t.Errorf("Detail = %q", r.Result.Detail)
	}
	if r.Result.URL != "https://ci/1" {
		t.Errorf("URL = %q", r.Result.URL)
	}
	if r.Producer != "ci/test" {
		t.Errorf("Producer = %q", r.Producer)
	}
}

// Spec §6: a result older than staleAfterDays renders as stale, not pass. An
// image scan from March is not evidence about today.
func TestIngestMarksAnOldResultStale(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-08-01T00:00:00Z",
			"  - { entity: service:api, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(fsys, cat, 14, now, &c)

	r := got[catalog.Ref{Kind: catalog.KindService, Name: "api"}]["image-scanned"]
	if r.Result.Status != StatusStale {
		t.Errorf("Status = %q, want stale — 39 days old against a 14 day limit", r.Result.Status)
	}
	if r.Result.Detail != "reported pass 39 days ago by ci/test, older than the 14 day limit" {
		t.Errorf("Detail = %q", r.Result.Detail)
	}
}

// Spec §6 precedence: newest generatedAt wins.
func TestIngestNewestResultWins(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/old.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/old\ngeneratedAt: 2026-09-07T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: fail }\n")},
		".landsraad/checks/new.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/new\ngeneratedAt: 2026-09-08T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(fsys, cat, 14, now, &c)

	r := got[catalog.Ref{Kind: catalog.KindService, Name: "api"}]["image-scanned"]
	if r.Result.Status != StatusPass {
		t.Errorf("Status = %q, want pass — the newer file wins", r.Result.Status)
	}
	if r.Producer != "ci/new" {
		t.Errorf("Producer = %q, want ci/new", r.Producer)
	}
}

// Spec §6: "a tie is an error rather than a coin flip".
func TestIngestReportsATieBetweenProducers(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/a.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/a\ngeneratedAt: 2026-09-08T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")},
		".landsraad/checks/b.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/b\ngeneratedAt: 2026-09-08T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: fail }\n")},
	}
	var c diag.Collector

	Ingest(fsys, cat, 14, now, &c)

	if !c.HasErrors() {
		t.Fatal("two producers reporting the same (entity, check) at the same instant must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "checks-tie" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-tie")
	}
	want := `producers "ci/a" and "ci/b" both report image-scanned for service:api at 2026-09-08T00:00:00Z, so neither can win`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "give the producers different generatedAt values, or have only one report this check"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// Spec §6: a bare name is a deprecated alias, resolved only when unambiguous.
func TestIngestResolvesAnUnambiguousBareName(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: api, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(fsys, cat, 14, now, &c)

	if _, ok := got[catalog.Ref{Kind: catalog.KindService, Name: "api"}]["image-scanned"]; !ok {
		t.Fatal("an unambiguous bare name must resolve")
	}
	// Deprecated, so it warns — but it does not fail, because existing CI jobs
	// in other people's repositories are already writing this shape.
	if len(c.Diagnostics()) != 1 {
		t.Fatalf("expected exactly one deprecation warning, got %+v", c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if d.Severity != diag.SevWarn {
		t.Errorf("Severity = %v, want SevWarn", d.Severity)
	}
	if d.Check != "checks-bare-name" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-bare-name")
	}
	want := `entity "api" is a bare name; write it as the ref "service:api"`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// The sharp edge of the deprecated alias: "orders" is unambiguous until
// someone adds topic:orders, at which point a working CI job starts erroring.
// That is correct — picking one silently would route evidence to the wrong
// entity — and the message has to say exactly what to change.
func TestIngestRefusesAnAmbiguousBareName(t *testing.T) {
	cat := catalogOf(t, svc("orders"), topic("orders"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: orders, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(fsys, cat, 14, now, &c)

	if len(got) != 0 {
		t.Errorf("an ambiguous name must resolve to nothing, got %+v", got)
	}
	if !c.HasErrors() {
		t.Fatal("an ambiguous bare name must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "checks-ambiguous-name" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-ambiguous-name")
	}
	want := `entity "orders" is ambiguous: it could be service:orders or topic:orders`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "write the full ref, for example service:orders"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

func TestIngestReportsAResultForAnEntityThatDoesNotExist(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: service:ghost, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	Ingest(fsys, cat, 14, now, &c)

	if !c.HasErrors() {
		t.Fatal("a result for an entity not in the catalog must be reported")
	}
	d := c.Diagnostics()[0]
	if d.Check != "checks-unknown-entity" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-unknown-entity")
	}
	want := `result reported for service:ghost, which is not in the catalog`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// A malformed results file must be loud. Skipping it silently means the
// scorecard reports "not reported" for checks that were, in fact, reported —
// and the CI job that wrote the file goes on believing it works.
func TestIngestReportsAMalformedFile(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\ngeneratedAt: not-a-date\nresults: []\n")},
	}
	var c diag.Collector

	Ingest(fsys, cat, 14, now, &c)

	if !c.HasErrors() {
		t.Fatal("a results file that does not validate must be an error")
	}
	if c.Diagnostics()[0].File != ".landsraad/checks/scan.yaml" {
		t.Errorf("File = %q, want the results file", c.Diagnostics()[0].File)
	}
}

// No directory at all is not an error — a repository that reports no external
// results is a normal repository. Every external check renders not-reported,
// which is the honest answer and is visible in the scorecard.
func TestIngestWithNoChecksDirectoryIsNotAnError(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	var c diag.Collector

	got := Ingest(fstest.MapFS{}, cat, 14, now, &c)

	if c.HasErrors() {
		t.Errorf("an absent .landsraad/checks is not an error: %+v", c.Diagnostics())
	}
	if len(got) != 0 {
		t.Errorf("nothing to ingest, got %+v", got)
	}
}

func topic(name string) *catalog.Entity {
	e := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindTopic}
	e.Metadata.Name = name
	e.Metadata.Owner = "team-payments"
	e.SourcePath = "topics/" + name + "/service.yaml"
	e.NameLine = 4
	return e
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/scorecard/ -run TestIngest -v`
Expected: FAIL — `undefined: Ingest`.

- [ ] **Step 4: Write the implementation**

Create `internal/scorecard/ingest.go`:

```go
package scorecard

import (
	_ "embed"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/schema"
)

//go:embed ingest.schema.json
var CheckResultsSchema []byte

// ChecksDir is where CI jobs write their results.
const ChecksDir = ".landsraad/checks"

// Reported is one ingested result with the provenance needed to resolve
// precedence and to name a producer in a diagnostic.
type Reported struct {
	Result      Result
	GeneratedAt time.Time
	Producer    string
	SourceFile  string
}

type checkResultsFile struct {
	APIVersion  string `yaml:"apiVersion"`
	Kind        string `yaml:"kind"`
	Producer    string `yaml:"producer"`
	GeneratedAt string `yaml:"generatedAt"`
	Results     []struct {
		Entity string `yaml:"entity"`
		Check  string `yaml:"check"`
		Status string `yaml:"status"`
		Detail string `yaml:"detail"`
		URL    string `yaml:"url"`
	} `yaml:"results"`
}

// Ingest is pipeline stage 6: read every results file, resolve entities,
// apply precedence, and age out results older than staleAfterDays.
//
// Spec §6 fixes three rules here, because this file is written by CI jobs in
// other people's repositories and is the most expensive contract in the
// product to change later:
//
//   - entity is a ref; a bare name is a deprecated alias resolved only when
//     unambiguous
//   - generatedAt is RFC 3339 with an explicit offset
//   - newest generatedAt wins, and a tie is an error rather than a coin flip
//
// An absent directory is not an error. A repository reporting no external
// results is normal, and every external check then renders not-reported —
// which is visible in the scorecard rather than hidden.
func Ingest(fsys fs.FS, cat *catalog.Catalog, staleAfterDays int, now time.Time, c *diag.Collector) map[catalog.Ref]map[string]Reported {
	out := map[catalog.Ref]map[string]Reported{}

	entries, err := fs.ReadDir(fsys, ChecksDir)
	if err != nil {
		return out
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if len(n) < 6 || (n[len(n)-5:] != ".yaml" && n[len(n)-4:] != ".yml") {
			continue
		}
		names = append(names, n)
	}
	// Sorted so a tie's diagnostic names producers in a stable order and the
	// message is reproducible between runs.
	sort.Strings(names)

	v, err := schema.New(CheckResultsSchema)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: ChecksDir, Line: 1,
			Check:   "checks-schema",
			Message: fmt.Sprintf("cannot compile the check-results schema: %v", err),
		})
		return out
	}

	for _, name := range names {
		path := ChecksDir + "/" + name
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Check:   "checks-unreadable",
				Message: fmt.Sprintf("cannot read %s", path),
			})
			continue
		}
		if !v.Validate("", path, data, c) {
			continue
		}
		var f checkResultsFile
		if err := yaml.Unmarshal(data, &f); err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Check:   "checks-parse",
				Message: fmt.Sprintf("cannot read %s as check results", path),
				Hint:    "the file must be a landsraad/v1 CheckResults document",
			})
			continue
		}
		// The schema asserts format: date-time, so this parses — but an
		// unparseable value must never be silently treated as the zero time,
		// which is 1 January year 1 and would render every result stale.
		generatedAt, err := time.Parse(time.RFC3339, f.GeneratedAt)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Check:   "checks-generated-at",
				Message: fmt.Sprintf("generatedAt %q is not RFC 3339 with an offset", f.GeneratedAt),
				Hint:    "for example 2026-09-08T14:00:00Z; the staleness arithmetic depends on it",
			})
			continue
		}

		for _, r := range f.Results {
			ref, ok := resolveEntity(r.Entity, cat, path, c)
			if !ok {
				continue
			}
			cand := Reported{
				Result: Result{
					Check:  r.Check,
					Status: Status(r.Status),
					Detail: r.Detail,
					URL:    r.URL,
				},
				GeneratedAt: generatedAt,
				Producer:    f.Producer,
				SourceFile:  path,
			}
			if out[ref] == nil {
				out[ref] = map[string]Reported{}
			}
			prev, exists := out[ref][r.Check]
			switch {
			case !exists || cand.GeneratedAt.After(prev.GeneratedAt):
				out[ref][r.Check] = cand
			case cand.GeneratedAt.Equal(prev.GeneratedAt) && prev.Producer != cand.Producer:
				// Spec §6: a tie is an error rather than a coin flip.
				c.Add(diag.Diagnostic{
					Severity: diag.SevError, File: path, Line: 1,
					Entity: ref.Name,
					Check:  "checks-tie",
					Message: fmt.Sprintf("producers %q and %q both report %s for %s at %s, so neither can win",
						prev.Producer, cand.Producer, r.Check, ref, f.GeneratedAt),
					Hint: "give the producers different generatedAt values, or have only one report this check",
				})
			}
		}
	}

	// Staleness is applied last, once precedence has picked a winner: ageing
	// out a loser would be wasted work, and ageing out before precedence could
	// let a stale-but-newer result lose to a fresh-but-older one.
	limit := time.Duration(staleAfterDays) * 24 * time.Hour
	for ref, byCheck := range out {
		for id, rep := range byCheck {
			age := now.Sub(rep.GeneratedAt)
			if age > limit {
				days := int(age.Hours() / 24)
				rep.Result.Status = StatusStale
				rep.Result.Detail = fmt.Sprintf("reported %s %d days ago by %s, older than the %d day limit",
					rep.Result.Status, days, rep.Producer, staleAfterDays)
				byCheck[id] = rep
			}
		}
		out[ref] = byCheck
	}
	return out
}

// resolveEntity turns a results file's entity field into a ref.
//
// A bare name is a deprecated alias (spec §6) and is resolved only when
// exactly one entity has that name. The ambiguity is not hypothetical:
// "orders" resolves fine until someone adds topic:orders, and at that moment a
// working CI job starts erroring. That is correct — picking one silently would
// attach evidence to the wrong entity — so the diagnostic says what to write
// instead.
func resolveEntity(s string, cat *catalog.Catalog, path string, c *diag.Collector) (catalog.Ref, bool) {
	if ref, err := catalog.ParseRef(s); err == nil {
		if _, ok := cat.Lookup(ref); !ok {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Entity: ref.Name,
				Check:  "checks-unknown-entity",
				Message: fmt.Sprintf("result reported for %s, which is not in the catalog", ref),
				Hint:   "the entity may have been renamed; metadata.aliases makes a rename additive",
			})
			return catalog.Ref{}, false
		}
		return ref, true
	}

	var matches []catalog.Ref
	for _, e := range cat.Entities() {
		if e.Metadata.Name == s {
			matches = append(matches, e.Ref())
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].String() < matches[j].String() })

	switch len(matches) {
	case 0:
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: 1,
			Check:   "checks-unknown-entity",
			Message: fmt.Sprintf("result reported for %q, which is not in the catalog", s),
			Hint:    "entity is a ref, for example service:payments-worker",
		})
		return catalog.Ref{}, false
	case 1:
		c.Add(diag.Diagnostic{
			Severity: diag.SevWarn, File: path, Line: 1,
			Entity:  matches[0].Name,
			Check:   "checks-bare-name",
			Message: fmt.Sprintf("entity %q is a bare name; write it as the ref %q", s, matches[0].String()),
			Hint:    "bare names are accepted for now and resolved only while unambiguous",
		})
		return matches[0], true
	default:
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: 1,
			Check: "checks-ambiguous-name",
			Message: fmt.Sprintf("entity %q is ambiguous: it could be %s or %s",
				s, matches[0], matches[1]),
			Hint: fmt.Sprintf("write the full ref, for example %s", matches[0]),
		})
		return catalog.Ref{}, false
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/scorecard/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scorecard/ingest.go internal/scorecard/ingest_test.go internal/scorecard/ingest.schema.json
git commit -m "feat: ingest .landsraad/checks/*.yaml

Pipeline stage 6. Spec §6 fixes three rules here because this file is
written by CI jobs in other people's repositories, which makes it the
most expensive contract in the product to change later, and all three
are tested:

entity is a ref; a bare name is a deprecated alias resolved only while
unambiguous. generatedAt is RFC 3339 with an offset. Newest wins, and a
tie between two producers is an error rather than a coin flip.

The ambiguous bare name has a sharp edge worth naming: 'orders' resolves
until someone adds topic:orders, at which point a working CI job starts
erroring. That is the correct behaviour — resolving it silently would
attach one team's evidence to another team's entity — so the message
says exactly what to write instead.

An absent .landsraad/checks is not an error. A repository reporting no
external results is normal, and every external check then renders
not-reported, which is visible rather than hidden."
```

---

### Task 9: SCORE — fold the checks, apply standards and exemptions

**Files:**
- Create: `internal/scorecard/score.go`
- Create: `internal/scorecard/score_test.go`

**Interfaces:**
- Consumes: everything from Tasks 5–8.
- Produces:
  - `type scorecard.EntityScore struct { Ref catalog.Ref; Tier int; Owner string; Results []Result; Passed, Applicable int }`
  - `func (s EntityScore) Score() float64` — `0` when `Applicable == 0`
  - `func (s EntityScore) Fails(gate config.Severity, std *config.Standards) []Result`
  - `type scorecard.TeamScore struct { Team string; Passed, Applicable int }`
  - `type scorecard.Scorecard struct { Entities []EntityScore }`
  - `func (s *Scorecard) Teams() []TeamScore` — sorted by team name
  - `func (s *Scorecard) Score() float64`
  - `func scorecard.Score(cat *catalog.Catalog, std *config.Standards, reported map[catalog.Ref]map[string]Reported, env Env, c *diag.Collector) *Scorecard`

**Context:** Spec §9: "Score = passed / applicable, per service, per team". Four rulings govern the arithmetic and every one has a test:

- **R1** — an entity with no tier is not scored. `Standards.Severity` already returns `SevSkip` for tier 0, so this falls out; the test pins it.
- **R2** — applicable is `required` + `warn`. `info` is reported and does not move the number.
- **R3** — `not-reported` and `stale` are **not passed but still applicable**. If they were excluded from the denominator, deleting a CI job would *raise* a team's score.
- **R4** — an exemption removes a check from the denominator; an **expired** exemption does not, and warns.

Exemptions come from `spec.exemptions` (`catalog.Exemption{Check, Reason, Until}`). Spec §12: "Exemptions exist so nobody has to lie. A tier-1 nightly backfill genuinely has no runbook. Without `spec.exemptions` the only available lever is to misstate the tier — corrupting the dataset the whole product rests on."

- [ ] **Step 1: Write the failing test**

Create `internal/scorecard/score_test.go`:

```go
package scorecard

import (
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// stdOf builds a Standards from YAML, failing the test if it does not load.
func stdOf(t *testing.T, yaml string) *config.Standards {
	t.Helper()
	var c diag.Collector
	s := config.LoadStandards("standards.yaml", []byte(yaml), &c)
	if c.HasErrors() {
		t.Fatalf("fixture standards must load: %+v", c.Diagnostics())
	}
	return s
}

const ownerOnly = `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set: { tiers: {1: required, 2: required, 3: required} }
`

func TestScoreIsPassedOverApplicable(t *testing.T) {
	e := svc("api") // tier 1, owner set
	cat := catalogOf(t, e)
	var c diag.Collector

	sc := Score(cat, stdOf(t, ownerOnly), nil, env(nil), &c)

	if len(sc.Entities) != 1 {
		t.Fatalf("got %d entity scores, want 1", len(sc.Entities))
	}
	es := sc.Entities[0]
	if es.Applicable != 1 || es.Passed != 1 {
		t.Errorf("Passed/Applicable = %d/%d, want 1/1", es.Passed, es.Applicable)
	}
	if got := es.Score(); got != 1.0 {
		t.Errorf("Score() = %v, want 1.0", got)
	}
}

// Ruling R1: an entity with no tier is not scored at all.
func TestScoreSkipsEntitiesWithNoTier(t *testing.T) {
	lib := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindLibrary}
	lib.Metadata.Name = "kafkaclient"
	lib.Metadata.Owner = "team-payments"
	lib.SourcePath = "libs/kafkaclient/service.yaml"
	lib.NameLine = 4
	cat := catalogOf(t, svc("api"), lib)
	var c diag.Collector

	sc := Score(cat, stdOf(t, ownerOnly), nil, env(nil), &c)

	if len(sc.Entities) != 1 {
		t.Fatalf("got %d entity scores, want 1 — a Library has no tier and is not scored", len(sc.Entities))
	}
	if sc.Entities[0].Ref.Kind != catalog.KindService {
		t.Errorf("scored %s, want the Service", sc.Entities[0].Ref)
	}
}

// Ruling R2: info is reported and does not move the number.
func TestScoreExcludesInfoChecksFromTheDenominator(t *testing.T) {
	e := svc("api")
	e.Metadata.Tier = 3
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:   { tiers: {3: required} }
    slo-defined: { tiers: {3: info} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	es := sc.Entities[0]
	if es.Applicable != 1 {
		t.Errorf("Applicable = %d, want 1 — the info check does not count", es.Applicable)
	}
	// It is still reported: a check nobody can see is a check nobody fixes.
	if len(es.Results) != 2 {
		t.Errorf("got %d results, want 2 — info checks are reported, just not counted", len(es.Results))
	}
}

// Ruling R3, and the most important test in this file. If not-reported were
// excluded from the denominator, deleting a CI job would raise the score.
func TestNotReportedCountsAgainstTheScore(t *testing.T) {
	e := svc("api")
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:     { tiers: {1: required} }
    image-scanned: { source: external, tiers: {1: required} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c) // nothing reported

	es := sc.Entities[0]
	if es.Applicable != 2 {
		t.Errorf("Applicable = %d, want 2 — an unreported external check stays applicable", es.Applicable)
	}
	if es.Passed != 1 {
		t.Errorf("Passed = %d, want 1", es.Passed)
	}
	if got := es.Score(); got != 0.5 {
		t.Errorf("Score() = %v, want 0.5; deleting the CI job must not raise the score", got)
	}
	var found bool
	for _, r := range es.Results {
		if r.Check == "image-scanned" {
			found = true
			if r.Status != StatusNotReported {
				t.Errorf("Status = %q, want not-reported", r.Status)
			}
			if r.Detail != "no result reported in .landsraad/checks" {
				t.Errorf("Detail = %q", r.Detail)
			}
		}
	}
	if !found {
		t.Error("an external check with no result must still appear in the scorecard")
	}
}

// Ruling R4: an exemption removes a check from the denominator.
func TestAnExemptionRemovesACheckFromTheDenominator(t *testing.T) {
	e := svc("api")
	e.Spec.Exemptions = []catalog.Exemption{
		{Check: "runbook-present", Reason: "nightly backfill, no on-call path", Until: "2027-01-01"},
	}
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:       { tiers: {1: required} }
    runbook-present: { tiers: {1: required} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	es := sc.Entities[0]
	if es.Applicable != 1 {
		t.Errorf("Applicable = %d, want 1 — the exempt check is not counted", es.Applicable)
	}
	if got := es.Score(); got != 1.0 {
		t.Errorf("Score() = %v, want 1.0", got)
	}
	for _, r := range es.Results {
		if r.Check == "runbook-present" {
			if r.Status != StatusExempt {
				t.Errorf("Status = %q, want exempt", r.Status)
			}
			if r.Detail != "exempt until 2027-01-01: nightly backfill, no on-call path" {
				t.Errorf("Detail = %q", r.Detail)
			}
		}
	}
}

// Ruling R4's other half: an expired exemption waives nothing and says so. An
// expired waiver that keeps waiving is a permanent lie in the dataset.
func TestAnExpiredExemptionDoesNotWaive(t *testing.T) {
	e := svc("api")
	e.Spec.Exemptions = []catalog.Exemption{
		{Check: "runbook-present", Reason: "temporary", Until: "2026-01-01"},
	}
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    runbook-present: { tiers: {1: required} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	if sc.Entities[0].Applicable != 1 {
		t.Errorf("Applicable = %d, want 1 — an expired exemption waives nothing", sc.Entities[0].Applicable)
	}
	if !hasWarn(c.Diagnostics()) {
		t.Fatal("an expired exemption must warn")
	}
	d := c.Diagnostics()[0]
	if d.Check != "exemption-expired" {
		t.Errorf("Check = %q, want %q", d.Check, "exemption-expired")
	}
	want := `exemption for runbook-present on service:api expired on 2026-01-01 and no longer waives anything`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "renew it with a new `until`, or fix the check and remove the exemption"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// An exemption with no `until` never expires, which is allowed — some
// waivers are permanent facts about a service — but it must still carry a
// reason, and the schema already requires one.
func TestAnExemptionWithNoUntilNeverExpires(t *testing.T) {
	e := svc("api")
	e.Spec.Exemptions = []catalog.Exemption{{Check: "runbook-present", Reason: "no on-call path"}}
	cat := catalogOf(t, e)
	std := stdOf(t, "apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    runbook-present: { tiers: {1: required} }\n")
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	if sc.Entities[0].Applicable != 0 {
		t.Errorf("Applicable = %d, want 0", sc.Entities[0].Applicable)
	}
	for _, r := range sc.Entities[0].Results {
		if r.Detail != "exempt: no on-call path" {
			t.Errorf("Detail = %q", r.Detail)
		}
	}
}

// An exemption naming a check that does not exist is a typo that silently
// waives nothing. Say so — the author believes they are covered.
func TestAnExemptionForAnUnknownCheckIsReported(t *testing.T) {
	e := svc("api")
	e.Spec.Exemptions = []catalog.Exemption{{Check: "runbook-presnt", Reason: "typo"}}
	cat := catalogOf(t, e)
	var c diag.Collector

	Score(cat, stdOf(t, ownerOnly), nil, env(nil), &c)

	if !hasWarn(c.Diagnostics()) {
		t.Fatal("an exemption for an unknown check must be reported")
	}
	d := c.Diagnostics()[0]
	if d.Check != "exemption-unknown-check" {
		t.Errorf("Check = %q, want %q", d.Check, "exemption-unknown-check")
	}
	want := `exemption on service:api names check "runbook-presnt", which is not in standards.yaml, so it waives nothing`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// An entity with every check exempt or skipped has no score. Zero is the
// honest rendering: it has passed nothing because nothing was asked.
func TestScoreIsZeroWhenNothingIsApplicable(t *testing.T) {
	e := svc("api")
	cat := catalogOf(t, e)
	std := stdOf(t, "apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    owner-set: { tiers: {1: skip} }\n")
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	if got := sc.Entities[0].Score(); got != 0 {
		t.Errorf("Score() = %v, want 0 for an entity with nothing applicable", got)
	}
	if sc.Entities[0].Applicable != 0 {
		t.Errorf("Applicable = %d, want 0", sc.Entities[0].Applicable)
	}
}

// Results are sorted by check id so the JSON payload, the rendered table and
// the history CSV are stable between runs.
func TestResultsAreSortedByCheckID(t *testing.T) {
	e := svc("api")
	e.Spec.Runbook = "r.md"
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    slo-defined:     { tiers: {1: warn} }
    owner-set:       { tiers: {1: required} }
    runbook-present: { tiers: {1: required} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(fstest.MapFS{}), &c)

	got := sc.Entities[0].Results
	want := []string{"owner-set", "runbook-present", "slo-defined"}
	for i, id := range want {
		if got[i].Check != id {
			t.Errorf("Results[%d].Check = %q, want %q", i, got[i].Check, id)
		}
	}
}

// Fails is what the gate reads: only checks at or above the gate severity that
// did not pass.
func TestFailsHonoursTheGate(t *testing.T) {
	e := svc("api")
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:       { tiers: {1: required} }
    runbook-present: { tiers: {1: required} }
    slo-defined:     { tiers: {1: warn} }
`)
	var c diag.Collector

	es := Score(cat, std, nil, env(fstest.MapFS{}), &c).Entities[0]

	// runbook-present fails (unset) and slo-defined fails (none). owner-set passes.
	if got := len(es.Fails(config.SevRequired, std)); got != 1 {
		t.Errorf("Fails(required) = %d, want 1 — only runbook-present gates", got)
	}
	if got := len(es.Fails(config.SevWarn, std)); got != 2 {
		t.Errorf("Fails(warn) = %d, want 2 — a ratcheting team gates on both", got)
	}
}

func TestTeamScoresAggregate(t *testing.T) {
	a, b := svc("a"), svc("b")
	b.Metadata.Owner = "team-sre"
	cat := catalogOf(t, a, b)
	var c diag.Collector

	teams := Score(cat, stdOf(t, ownerOnly), nil, env(nil), &c).Teams()

	if len(teams) != 2 {
		t.Fatalf("got %d teams, want 2: %+v", len(teams), teams)
	}
	// Sorted by name, so the rendered table does not reshuffle between runs.
	if teams[0].Team != "team-payments" || teams[1].Team != "team-sre" {
		t.Errorf("teams must be sorted by name, got %+v", teams)
	}
}

func hasWarn(ds []diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.SevWarn {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/scorecard/ -run 'TestScore|TestNotReported|TestAnExe|TestResults|TestFails|TestTeam' -v`
Expected: FAIL — `undefined: Score`.

- [ ] **Step 3: Write the implementation**

Create `internal/scorecard/score.go`:

```go
package scorecard

import (
	"fmt"
	"sort"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// EntityScore is one entity measured against the standard.
type EntityScore struct {
	Ref     catalog.Ref
	Tier    int
	Owner   string
	Results []Result
	// Passed and Applicable are the fraction spec §9 defines. Applicable
	// counts required and warn checks that are not exempt; info and skip are
	// reported but not counted (ruling R2).
	Passed     int
	Applicable int
}

// Score is passed over applicable, or 0 when nothing is applicable.
//
// Zero rather than one for the empty case: an entity nothing was asked of has
// not demonstrated anything, and rendering it as 100% would make "exempt
// everything" the cheapest way to a perfect scorecard.
func (s EntityScore) Score() float64 {
	if s.Applicable == 0 {
		return 0
	}
	return float64(s.Passed) / float64(s.Applicable)
}

// Fails returns the results that gate the build at the given severity: checks
// whose severity is at least gate and whose status is not pass.
func (s EntityScore) Fails(gate config.Severity, std *config.Standards) []Result {
	var out []Result
	for _, r := range s.Results {
		if r.Status.Passed() || r.Status == StatusExempt {
			continue
		}
		if atLeast(std.Severity(r.Check, s.Tier), gate) {
			out = append(out, r)
		}
	}
	return out
}

// atLeast orders the severities required > warn > info > skip.
func atLeast(have, gate config.Severity) bool {
	return rank(have) >= rank(gate) && rank(have) > 0
}

func rank(s config.Severity) int {
	switch s {
	case config.SevRequired:
		return 3
	case config.SevWarn:
		return 2
	case config.SevInfo:
		return 1
	}
	return 0
}

// TeamScore aggregates every entity a team owns.
type TeamScore struct {
	Team       string
	Passed     int
	Applicable int
}

func (t TeamScore) Score() float64 {
	if t.Applicable == 0 {
		return 0
	}
	return float64(t.Passed) / float64(t.Applicable)
}

// Scorecard is the whole run.
type Scorecard struct {
	Entities []EntityScore
}

// Teams aggregates by owner, sorted by team name so the rendered table does
// not reshuffle between runs.
func (s *Scorecard) Teams() []TeamScore {
	byTeam := map[string]*TeamScore{}
	for _, e := range s.Entities {
		t, ok := byTeam[e.Owner]
		if !ok {
			t = &TeamScore{Team: e.Owner}
			byTeam[e.Owner] = t
		}
		t.Passed += e.Passed
		t.Applicable += e.Applicable
	}
	out := make([]TeamScore, 0, len(byTeam))
	for _, t := range byTeam {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Team < out[j].Team })
	return out
}

// Score is the whole-catalog total.
func (s *Scorecard) Score() float64 {
	passed, applicable := 0, 0
	for _, e := range s.Entities {
		passed += e.Passed
		applicable += e.Applicable
	}
	if applicable == 0 {
		return 0
	}
	return float64(passed) / float64(applicable)
}

// Score is pipeline stage 7: hermetic checks plus ingested results, resolved
// through standards.yaml.
//
// Entities are scored in catalog order, which NewCatalog already sorted by
// source repo and path, so the output is stable.
func Score(cat *catalog.Catalog, std *config.Standards, reported map[catalog.Ref]map[string]Reported, env Env, c *diag.Collector) *Scorecard {
	hermetic := map[string]Check{}
	for _, ch := range HermeticChecks() {
		hermetic[ch.ID] = ch
	}

	sc := &Scorecard{}
	for _, e := range cat.Entities() {
		// Ruling R1: tier is required only for kinds that can page someone
		// (spec §12), and an entity without one is not scored. Standards
		// returns skip for tier 0, so this is belt and braces — but an entity
		// with no applicable checks would otherwise appear in the scorecard
		// and in the history CSV as a permanent 0%.
		if e.Metadata.Tier == 0 {
			continue
		}

		es := EntityScore{Ref: e.Ref(), Tier: e.Metadata.Tier, Owner: e.Metadata.Owner}
		exempt := exemptions(e, std, env.Now, c)

		for _, id := range std.Checks() {
			sev := std.Severity(id, e.Metadata.Tier)
			if sev == config.SevSkip {
				continue
			}

			var r Result
			switch {
			case exempt[id] != "":
				r = Result{Check: id, Status: StatusExempt, Detail: exempt[id]}
			case std.IsExternal(id):
				r = externalResult(id, e.Ref(), reported)
			default:
				ch, ok := hermetic[id]
				if !ok {
					// standards.yaml names a hermetic check the binary does
					// not implement. Silently skipping it would make the
					// scorecard report on fewer checks than the team believes.
					c.Add(diag.Diagnostic{
						Severity: diag.SevWarn, File: "standards.yaml", Line: 1,
						Check: "standards-unknown-check",
						Message: fmt.Sprintf("check %q is not implemented by this version of landsraad and is not marked `source: external`", id),
						Hint: "check the spelling, add `source: external`, or upgrade landsraad",
					})
					continue
				}
				r = ch.Run(e, env)
			}

			es.Results = append(es.Results, r)

			// Ruling R2: only required and warn are applicable.
			// Ruling R3: not-reported and stale are not passes, and are NOT
			// removed from the denominator — otherwise deleting a CI job
			// raises the score, which is the one incentive this product must
			// never create.
			// Ruling R4: exempt is removed from the denominator.
			if r.Status == StatusExempt || rank(sev) < rank(config.SevWarn) {
				continue
			}
			es.Applicable++
			if r.Status.Passed() {
				es.Passed++
			}
		}

		sort.Slice(es.Results, func(i, j int) bool { return es.Results[i].Check < es.Results[j].Check })
		sc.Entities = append(sc.Entities, es)
	}
	return sc
}

// externalResult looks up an ingested result, rendering its absence as
// not-reported rather than as a failure of the service (spec §9: "distinct
// from both pass and fail").
func externalResult(id string, ref catalog.Ref, reported map[catalog.Ref]map[string]Reported) Result {
	rep, ok := reported[ref][id]
	if !ok {
		return Result{
			Check:  id,
			Status: StatusNotReported,
			Detail: "no result reported in " + ChecksDir,
		}
	}
	return rep.Result
}

// exemptions returns check id to rendered detail for every exemption that is
// currently in force, reporting the ones that are not.
//
// Spec §12: exemptions exist so nobody has to lie. Without them the only lever
// for a tier-1 nightly backfill with no runbook is to misstate its tier, which
// corrupts the dataset the whole product rests on. That makes an exemption
// that silently does nothing — expired, or naming a check that does not
// exist — worse than no exemption at all: the author believes they are
// covered.
func exemptions(e *catalog.Entity, std *config.Standards, now time.Time, c *diag.Collector) map[string]string {
	known := map[string]bool{}
	for _, id := range std.Checks() {
		known[id] = true
	}

	out := map[string]string{}
	for _, x := range e.Spec.Exemptions {
		if !known[x.Check] {
			c.Add(diag.Diagnostic{
				Severity: diag.SevWarn,
				Repo:     e.SourceRepo,
				File:     e.SourcePath,
				Line:     e.NameLine,
				Entity:   e.Metadata.Name,
				Check:    "exemption-unknown-check",
				Message: fmt.Sprintf("exemption on %s names check %q, which is not in standards.yaml, so it waives nothing",
					e.Ref(), x.Check),
				Hint: "check the spelling against the checks listed in standards.yaml",
			})
			continue
		}
		if x.Until != "" {
			// The schema asserts format: date on `until`, so this parses.
			until, err := time.Parse("2006-01-02", x.Until)
			if err == nil && until.Before(now) {
				c.Add(diag.Diagnostic{
					Severity: diag.SevWarn,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "exemption-expired",
					Message: fmt.Sprintf("exemption for %s on %s expired on %s and no longer waives anything",
						x.Check, e.Ref(), x.Until),
					Hint: "renew it with a new `until`, or fix the check and remove the exemption",
				})
				continue
			}
			out[x.Check] = fmt.Sprintf("exempt until %s: %s", x.Until, x.Reason)
			continue
		}
		out[x.Check] = "exempt: " + x.Reason
	}
	return out
}
```

**Why `exemptions` takes `now` rather than reading the clock:** expiry needs the current date, and the Global Constraints forbid `time.Now()` below `cmd/`. Threading `env.Now` through is what keeps `TestAnExpiredExemptionDoesNotWaive` deterministic — a clock read here makes that test pass or fail depending on the day it runs.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/scorecard/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scorecard/score.go internal/scorecard/score_test.go
git commit -m "feat: score entities against the standard

Spec §9: score = passed / applicable, per service and per team. Four
rulings govern the arithmetic and each has a test.

The load-bearing one is that not-reported and stale count against the
score rather than being removed from the denominator. The alternative
means deleting your CI job raises your score, which is the one incentive
this product must never create.

An exemption removes a check from the denominator; an expired one does
not, and warns. Spec §12 says exemptions exist so nobody has to lie —
which makes an exemption that silently waives nothing worse than none at
all, because the author believes they are covered. Same for one naming a
check that is not in standards.yaml.

An entity with no applicable checks scores 0, not 100%. Otherwise
exempting everything is the cheapest route to a perfect scorecard."
```

---

### Task 10: `scorecard-history.csv`

**Files:**
- Create: `internal/scorecard/history.go`
- Create: `internal/scorecard/history_test.go`

**Interfaces:**
- Consumes: `*Scorecard`, `EntityScore` (Task 9); `emit.File` (Task 1).
- Produces:
  - `const scorecard.HistoryPath = "scorecard-history.csv"`
  - `func scorecard.AppendHistory(existing []byte, sc *Scorecard, now time.Time) emit.File`

**Context:** Spec §9: "Score = passed / applicable, per service, per team, and appended weekly to `scorecard-history.csv` by the CI job." Spec D12 names this as the artifact with the sharpest failure mode: a rename "silently restarts `scorecard-history.csv` — the one artefact with no way to notice it broke."

Ruling R9 fixes the grain: one row per (date, ref) per run. Columns `date,ref,tier,owner,passed,applicable,score`.

Appending rather than rewriting means the function takes the existing bytes and returns the whole new file — still a pure function, still an `emit.File`, and `cmd/` still owns the write. A run on the same date **replaces** that date's rows rather than duplicating them, so re-running CI on the same day does not double every row.

- [ ] **Step 1: Write the failing test**

Create `internal/scorecard/history_test.go`:

```go
package scorecard

import (
	"strings"
	"testing"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
)

func scorecardOf(rows ...EntityScore) *Scorecard { return &Scorecard{Entities: rows} }

func row(name string, tier, passed, applicable int) EntityScore {
	return EntityScore{
		Ref:        catalog.Ref{Kind: catalog.KindService, Name: name},
		Tier:       tier,
		Owner:      "team-payments",
		Passed:     passed,
		Applicable: applicable,
	}
}

func TestAppendHistoryWritesAHeaderAndRows(t *testing.T) {
	f := AppendHistory(nil, scorecardOf(row("api", 1, 3, 4)), now)

	if f.Path != HistoryPath {
		t.Errorf("Path = %q, want %q", f.Path, HistoryPath)
	}
	want := "date,ref,tier,owner,passed,applicable,score\n" +
		"2026-09-09,service:api,1,team-payments,3,4,0.750\n"
	if string(f.Data) != want {
		t.Errorf("history\n got:\n%s\nwant:\n%s", f.Data, want)
	}
}

func TestAppendHistoryKeepsEarlierRows(t *testing.T) {
	existing := []byte("date,ref,tier,owner,passed,applicable,score\n" +
		"2026-09-02,service:api,1,team-payments,2,4,0.500\n")

	f := AppendHistory(existing, scorecardOf(row("api", 1, 3, 4)), now)

	got := string(f.Data)
	if !strings.Contains(got, "2026-09-02,service:api,1,team-payments,2,4,0.500") {
		t.Errorf("the earlier row must survive:\n%s", got)
	}
	if !strings.Contains(got, "2026-09-09,service:api,1,team-payments,3,4,0.750") {
		t.Errorf("the new row must be appended:\n%s", got)
	}
}

// CI runs more than once a day. Re-running on the same date must replace that
// date's rows, not double them — a trend line with two points per day for
// every re-run is a trend line nobody trusts.
func TestAppendHistoryReplacesRowsForTheSameDate(t *testing.T) {
	existing := []byte("date,ref,tier,owner,passed,applicable,score\n" +
		"2026-09-09,service:api,1,team-payments,1,4,0.250\n")

	f := AppendHistory(existing, scorecardOf(row("api", 1, 3, 4)), now)

	got := string(f.Data)
	if strings.Count(got, "2026-09-09,service:api") != 1 {
		t.Errorf("expected exactly one row for today:\n%s", got)
	}
	if !strings.Contains(got, "2026-09-09,service:api,1,team-payments,3,4,0.750") {
		t.Errorf("today's row must be the new one:\n%s", got)
	}
}

// Rows are sorted by date then ref, so a diff of this file between runs shows
// only the rows that actually changed.
func TestAppendHistoryIsSorted(t *testing.T) {
	f := AppendHistory(nil, scorecardOf(row("z", 1, 1, 1), row("a", 2, 1, 1)), now)

	lines := strings.Split(strings.TrimSpace(string(f.Data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header plus two rows, got %d lines", len(lines))
	}
	if !strings.HasPrefix(lines[1], "2026-09-09,service:a,") {
		t.Errorf("rows must be sorted by ref, got %q", lines[1])
	}
}

// Score is written to three decimals so a one-check improvement in a
// twelve-check service is visible in the trend rather than rounded away.
func TestScoreIsWrittenToThreeDecimals(t *testing.T) {
	f := AppendHistory(nil, scorecardOf(row("api", 1, 1, 3)), now)
	if !strings.Contains(string(f.Data), ",1,3,0.333\n") {
		t.Errorf("want three decimal places:\n%s", f.Data)
	}
}

// An existing file with a different header is not silently reformatted: the
// columns are a contract with whatever reads the trend, and rewriting them
// would discard data the tool did not write.
func TestAppendHistoryPreservesAnUnknownHeader(t *testing.T) {
	existing := []byte("date,ref,score\n2026-09-02,service:api,0.500\n")

	f := AppendHistory(existing, scorecardOf(row("api", 1, 3, 4)), now)

	if !strings.Contains(string(f.Data), "2026-09-02,service:api,0.500") {
		t.Errorf("unrecognised earlier rows must survive verbatim:\n%s", f.Data)
	}
}

func TestAppendHistoryUsesTheInjectedDate(t *testing.T) {
	other := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	f := AppendHistory(nil, scorecardOf(row("api", 1, 1, 1)), other)
	if !strings.Contains(string(f.Data), "2027-03-01,") {
		t.Errorf("the injected date must be used, got:\n%s", f.Data)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/scorecard/ -run TestAppendHistory -v`
Expected: FAIL — `undefined: AppendHistory`.

- [ ] **Step 3: Write the implementation**

Create `internal/scorecard/history.go`:

```go
package scorecard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/landsraadhq/landsraad/internal/emit"
)

// HistoryPath is the trend file spec §9 describes, appended by the CI job.
const HistoryPath = "scorecard-history.csv"

// historyHeader is the column contract. Spec D12 names this file as the one
// artifact with no way to notice it broke, which is why the grain is explicit
// and why an unrecognised earlier row is preserved rather than reformatted.
const historyHeader = "date,ref,tier,owner,passed,applicable,score"

// AppendHistory returns the whole history file with this run's rows in place.
//
// It takes the existing bytes rather than a file handle so it stays a pure
// function: cmd/ reads, this decides, cmd/ writes. Ruling R9 fixes the grain
// at one row per (date, ref) per run — a superset of spec §9's "weekly", which
// can be down-sampled later, where a weekly grain would lose data from a
// project that runs CI daily.
//
// A run on a date that already has rows replaces them. CI runs more than once
// a day, and a trend with several points per day per re-run is a trend nobody
// trusts.
func AppendHistory(existing []byte, sc *Scorecard, now time.Time) emit.File {
	date := now.UTC().Format("2006-01-02")

	var kept []string
	for _, line := range strings.Split(string(existing), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || line == historyHeader {
			continue
		}
		// Rows for today are replaced by this run's. Everything else survives
		// verbatim, including rows written by a different version with
		// different columns: they are somebody's trend data and this tool did
		// not write them.
		if strings.HasPrefix(line, date+",") {
			continue
		}
		kept = append(kept, line)
	}

	for _, e := range sc.Entities {
		kept = append(kept, fmt.Sprintf("%s,%s,%d,%s,%d,%d,%.3f",
			date, e.Ref, e.Tier, e.Owner, e.Passed, e.Applicable, e.Score()))
	}

	// Sorted by the whole line, which orders by date then ref, so a diff
	// between runs shows only the rows that actually changed.
	sort.Strings(kept)

	var b strings.Builder
	b.WriteString(historyHeader)
	b.WriteString("\n")
	for _, line := range kept {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return emit.File{Path: HistoryPath, Data: []byte(b.String())}
}
```

- [ ] **Step 4: Run the tests and commit**

Run: `go test ./internal/scorecard/ -v` — expected PASS.

```bash
git add internal/scorecard/history.go internal/scorecard/history_test.go
git commit -m "feat: append scorecard-history.csv

Spec §9's trend file. Spec D12 calls it the one artifact with no way to
notice it broke, which shapes two decisions here.

The grain is one row per (date, ref) per run — a superset of the spec's
'weekly', down-samplable later, where a weekly grain loses data from a
project running CI daily. A second run on the same date replaces that
date's rows rather than doubling them.

Rows this tool did not write are preserved verbatim, including ones with
different columns. They are somebody's trend data; reformatting them to
match today's header would discard it silently."
```

---

### Task 11: `landsraad score`

**Files:**
- Create: `cmd/landsraad/score.go`
- Create: `cmd/landsraad/score_test.go`
- Modify: `cmd/landsraad/main.go` (add `exitScorecard = 3`)
- Modify: `cmd/landsraad/root.go` (register the command)

**Interfaces:**
- Consumes: everything above; `loadCatalog` from Task 4.
- Produces:
  - `const exitScorecard = 3`
  - `func Score(fsys fs.FS, out, errOut io.Writer, opts ScoreOptions) int`
  - `type ScoreOptions struct { Format diag.Formatter; FailOn config.Severity; Now time.Time; LastEdit scorecard.LastEditFunc; History bool }`
  - `func newScoreCmd() *cobra.Command`

**Context:** Spec §8: "`score --fail-on` defaults to `required`: only checks marked `required` for that service's tier gate the build. `--fail-on warn` additionally gates on `warn`, for a team that wants to ratchet."

Exit `3`, not `2` (spec §12): "2 and 3 are distinct because 'your metadata is broken' and 'your service does not meet the standard' are different problems for different people."

The stream contract is load-bearing here. `--format json` must emit **only** JSON on stdout; the human summary is stderr. Plan 1 established this because "`--format json` on a clean repo emits a JSON array followed by `ok: 3 entities validated`, which no parser accepts."

`Now` and `LastEdit` are `ScoreOptions` fields rather than values read inside, so the whole command is testable without a clock or a git repository.

- [ ] **Step 1: Write the failing test**

Create `cmd/landsraad/score_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

var testNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func scoreFS() fstest.MapFS {
	return fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")},
		"repos.yaml": {Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"services/api/service.yaml": {Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  path: services/api
`)},
	}
}

func scoreOpts() ScoreOptions {
	return ScoreOptions{
		Format:   diagText(),
		FailOn:   config.SevRequired,
		Now:      testNow,
		LastEdit: func(string) (time.Time, bool) { return time.Time{}, false },
	}
}

// A tier-1 service with no runbook, no SLO and no alerts fails required
// checks, so the gate trips with exit 3 — not 2, which means broken metadata.
func TestScoreExitsThreeWhenARequiredCheckFails(t *testing.T) {
	var out, errOut bytes.Buffer

	code := Score(scoreFS(), &out, &errOut, scoreOpts())

	if code != exitScorecard {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitScorecard, errOut.String())
	}
	if code == exitValidation {
		t.Error("a failing check is not a validation error; those are different problems for different people")
	}
}

// The metadata is fine, so validation must not be what fails.
func TestScoreDoesNotReportValidationErrorsForACleanCatalog(t *testing.T) {
	var out, errOut bytes.Buffer
	Score(scoreFS(), &out, &errOut, scoreOpts())
	if strings.Contains(errOut.String(), "refusing") {
		t.Errorf("the catalog is valid; stderr should not refuse:\n%s", errOut.String())
	}
}

// Stream contract (spec §12): --format json puts only JSON on stdout. Plan 1
// established this because a JSON array followed by "ok: ..." parses nowhere.
func TestScoreJSONOutputIsParseable(t *testing.T) {
	var out, errOut bytes.Buffer
	opts := scoreOpts()
	opts.JSON = true

	Score(scoreFS(), &out, &errOut, opts)

	var payload struct {
		Entities []struct {
			Ref        string  `json:"ref"`
			Tier       int     `json:"tier"`
			Owner      string  `json:"owner"`
			Score      float64 `json:"score"`
			Passed     int     `json:"passed"`
			Applicable int     `json:"applicable"`
			Results    []struct {
				Check  string `json:"check"`
				Status string `json:"status"`
				Detail string `json:"detail"`
			} `json:"results"`
		} `json:"entities"`
		Teams []struct {
			Team  string  `json:"team"`
			Score float64 `json:"score"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout must be parseable JSON: %v\n%s", err, out.String())
	}
	if len(payload.Entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(payload.Entities))
	}
	if payload.Entities[0].Ref != "service:api" {
		t.Errorf("ref = %q, want service:api", payload.Entities[0].Ref)
	}
}

// --fail-on warn is the ratchet: a team that has cleared the required checks
// opts into gating on warn too.
func TestScoreFailOnWarnGatesMore(t *testing.T) {
	fsys := scoreFS()
	// A tier-3 service: runbook-present is warn, not required.
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 3
  lifecycle: production
spec:
  path: services/api
  slo:
    - { name: availability, target: "99.9%", window: 30d }
  alerts: services/api/alerts.yaml
  docs: services/api/docs
`)}
	fsys["services/api/alerts.yaml"] = &fstest.MapFile{Data: []byte(
		"groups:\n  - name: api\n    rules:\n      - alert: Down\n        expr: up == 0\n")}
	fsys["services/api/docs/index.md"] = &fstest.MapFile{Data: []byte("# Docs\n\ncontent\n")}

	var out, errOut bytes.Buffer
	if code := Score(fsys, &out, &errOut, scoreOpts()); code != exitOK {
		t.Fatalf("at tier 3 with --fail-on required, exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}

	opts := scoreOpts()
	opts.FailOn = config.SevWarn
	out.Reset()
	errOut.Reset()
	if code := Score(fsys, &out, &errOut, opts); code != exitScorecard {
		t.Fatalf("--fail-on warn must gate on runbook-present, exit = %d, want %d", code, exitScorecard)
	}
}

// A repository with no standards.yaml is scored against spec §6's defaults,
// and told so. Scoring against nothing would report a perfect score having
// checked nothing.
func TestScoreAnnouncesTheDefaultStandards(t *testing.T) {
	var out, errOut bytes.Buffer
	Score(scoreFS(), &out, &errOut, scoreOpts())
	if !strings.Contains(errOut.String(), "no standards.yaml found") {
		t.Errorf("the default matrix must be announced, got:\n%s", errOut.String())
	}
}

// Broken metadata is exit 2, and scoring does not happen: a score computed
// from a catalog with a dangling ref is a number nobody should act on.
func TestScoreExitsTwoOnABrokenCatalog(t *testing.T) {
	fsys := scoreFS()
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-nope
  tier: 1
  lifecycle: production
spec:
  path: services/api
`)}

	var out, errOut bytes.Buffer
	if code := Score(fsys, &out, &errOut, scoreOpts()); code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
}

// --history writes the trend row. Without it, score is read-only.
func TestScoreWritesHistoryOnlyWhenAsked(t *testing.T) {
	var out, errOut bytes.Buffer
	opts := scoreOpts()
	opts.History = true

	files := scoreHistoryFiles(scoreFS(), &out, &errOut, opts)

	if len(files) != 1 || files[0].Path != scorecard.HistoryPath {
		t.Fatalf("want exactly one history file, got %+v", files)
	}
	if !strings.Contains(string(files[0].Data), "2026-09-09,service:api,1,team-payments,") {
		t.Errorf("history row missing:\n%s", files[0].Data)
	}
}
```

**Note for the implementer:** the last test calls `scoreHistoryFiles`, a helper that runs the scoring pipeline and returns the history `emit.File` without writing it — extract it from `Score` so both the command and the test use the same path. `ScoreOptions` also needs a `JSON bool` field used by `TestScoreJSONOutputIsParseable`; the scorecard payload is its own JSON shape, not `diag.Formatter`'s, because diagnostics and scores are different documents.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/landsraad/ -run TestScore -v`
Expected: FAIL — `undefined: Score`, `undefined: exitScorecard`.

- [ ] **Step 3: Add the exit code**

In `cmd/landsraad/main.go`:

```go
const (
	exitOK         = 0
	exitUsage      = 1
	exitValidation = 2
	// exitScorecard: the metadata is valid and the service does not meet the
	// standard. Distinct from exitValidation because they are different
	// problems for different people (spec §12) — a broken service.yaml is the
	// YAML author's, a failing check is the service owner's.
	exitScorecard = 3
)
```

- [ ] **Step 4: Write the command**

Create `cmd/landsraad/score.go`. It is an explicit composition, like `Validate` and `Gen`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// ScoreOptions is everything the score command needs that is not the
// filesystem. Now and LastEdit are values so the whole command is testable
// without a clock or a git repository.
type ScoreOptions struct {
	Format   diag.Formatter
	FailOn   config.Severity
	Now      time.Time
	LastEdit scorecard.LastEditFunc
	JSON     bool
	History  bool
}

// standardsFor loads standards.yaml, announcing the fallback to spec §6's
// published defaults the way patternsFor announces a missing repos.yaml.
// Degraded mode is visible in the artifact, not only in a log.
func standardsFor(fsys fs.FS, errOut io.Writer) *config.Standards {
	data, err := fs.ReadFile(fsys, "standards.yaml")
	if err != nil {
		fmt.Fprintf(errOut, "no standards.yaml found; scoring against the published defaults\n")
		return config.DefaultStandards()
	}
	var c diag.Collector
	std := config.LoadStandards("standards.yaml", data, &c)
	for _, d := range c.Diagnostics() {
		fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message)
	}
	if !std.Loaded() {
		fmt.Fprintf(errOut, "standards.yaml did not parse; scoring against the published defaults\n")
		return config.DefaultStandards()
	}
	return std
}

// scoreHistoryFiles runs the pipeline and returns the history file it would
// write. Extracted so the command and its test take the same path.
func scoreHistoryFiles(fsys fs.FS, out, errOut io.Writer, opts ScoreOptions) []emit.File {
	sc, _, _, ok := computeScore(fsys, errOut, opts)
	if !ok || !opts.History {
		return nil
	}
	existing, _ := fs.ReadFile(fsys, scorecard.HistoryPath)
	return []emit.File{scorecard.AppendHistory(existing, sc, opts.Now)}
}

// computeScore runs stages 1, 3, 4, 5, 6 and 7. It returns ok=false when the
// catalog has errors: a score computed from a catalog with a dangling ref or a
// duplicate name is a number nobody should act on.
func computeScore(fsys fs.FS, errOut io.Writer, opts ScoreOptions) (*scorecard.Scorecard, *config.Standards, *diag.Collector, bool) {
	var c diag.Collector
	cat, _ := loadCatalog(fsys, &c)
	if cat == nil || c.HasErrors() {
		return nil, nil, &c, false
	}
	std := standardsFor(fsys, errOut)
	reported := scorecard.Ingest(fsys, cat, std.StaleAfterDays(), opts.Now, &c)
	env := scorecard.Env{
		FS:             fsys,
		Now:            opts.Now,
		MaxDocsAgeDays: std.Param("docs-fresh", "maxAgeDays", 180),
		LastEdit:       opts.LastEdit,
	}
	return scorecard.Score(cat, std, reported, env, &c), std, &c, true
}

// Score measures the catalog against the standard.
//
// Exit 3 when a check at or above --fail-on does not pass. Exit 2 when the
// metadata itself is broken, because those are different problems for
// different people (spec §12).
func Score(fsys fs.FS, out, errOut io.Writer, opts ScoreOptions) int {
	sc, std, c, ok := computeScore(fsys, errOut, opts)
	if !ok {
		if err := opts.Format.Write(out, c.Diagnostics()); err != nil {
			fmt.Fprintf(errOut, "error: cannot write diagnostics: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(errOut, "refusing to score a catalog with errors; a score computed from broken metadata is a number nobody should act on\n")
		return exitValidation
	}

	if opts.JSON {
		if err := writeScoreJSON(out, sc); err != nil {
			fmt.Fprintf(errOut, "error: cannot write scorecard: %v\n", err)
			return exitUsage
		}
	} else {
		writeScoreText(errOut, sc)
	}

	// Diagnostics from ingest and exemptions go to stderr in text mode and are
	// part of the payload's siblings in JSON mode; either way they are never
	// interleaved with a JSON document on stdout.
	for _, d := range c.Diagnostics() {
		fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message)
	}

	gated := 0
	for _, e := range sc.Entities {
		gated += len(e.Fails(opts.FailOn, std))
	}
	if gated > 0 {
		fmt.Fprintf(errOut, "\n%s failing at or above %q\n",
			plural(gated, "check", "checks"), opts.FailOn)
		return exitScorecard
	}
	fmt.Fprintf(errOut, "\nok: %s meet the standard\n",
		plural(len(sc.Entities), "entity", "entities"))
	return exitOK
}

func writeScoreJSON(w io.Writer, sc *scorecard.Scorecard) error {
	type result struct {
		Check  string `json:"check"`
		Status string `json:"status"`
		Detail string `json:"detail,omitempty"`
		URL    string `json:"url,omitempty"`
	}
	type entity struct {
		Ref        string   `json:"ref"`
		Tier       int      `json:"tier"`
		Owner      string   `json:"owner"`
		Score      float64  `json:"score"`
		Passed     int      `json:"passed"`
		Applicable int      `json:"applicable"`
		Results    []result `json:"results"`
	}
	type team struct {
		Team       string  `json:"team"`
		Score      float64 `json:"score"`
		Passed     int     `json:"passed"`
		Applicable int     `json:"applicable"`
	}
	payload := struct {
		Entities []entity `json:"entities"`
		Teams    []team   `json:"teams"`
		Score    float64  `json:"score"`
	}{Score: sc.Score()}

	for _, e := range sc.Entities {
		ent := entity{
			Ref: e.Ref.String(), Tier: e.Tier, Owner: e.Owner,
			Score: e.Score(), Passed: e.Passed, Applicable: e.Applicable,
		}
		for _, r := range e.Results {
			ent.Results = append(ent.Results, result{
				Check: r.Check, Status: string(r.Status), Detail: r.Detail, URL: r.URL,
			})
		}
		payload.Entities = append(payload.Entities, ent)
	}
	for _, t := range sc.Teams() {
		payload.Teams = append(payload.Teams, team{
			Team: t.Team, Score: t.Score(), Passed: t.Passed, Applicable: t.Applicable,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// writeScoreText renders to stderr: the scorecard's human form is a summary,
// and stdout is reserved for the selected format's payload (spec §12).
func writeScoreText(w io.Writer, sc *scorecard.Scorecard) {
	for _, e := range sc.Entities {
		fmt.Fprintf(w, "\n%s  tier %d  %s  %.0f%% (%d/%d)\n",
			e.Ref, e.Tier, e.Owner, e.Score()*100, e.Passed, e.Applicable)
		for _, r := range e.Results {
			if r.Status.Passed() {
				continue
			}
			fmt.Fprintf(w, "  %-16s %-13s %s\n", r.Check, r.Status, r.Detail)
		}
	}
	fmt.Fprintf(w, "\nby team:\n")
	for _, t := range sc.Teams() {
		fmt.Fprintf(w, "  %-20s %.0f%% (%d/%d)\n", t.Team, t.Score()*100, t.Passed, t.Applicable)
	}
}

func newScoreCmd() *cobra.Command {
	var (
		format  string
		failOn  string
		history bool
	)
	cmd := &cobra.Command{
		Use:   "score [root]",
		Short: "Landsraad Council — measure services against the team standard",
		Long: "Score every entity against standards.yaml. Exit 3 when a check at or " +
			"above --fail-on does not pass; exit 2 when the metadata itself is broken, " +
			"because those are different problems for different people.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start := "."
			if len(args) == 1 {
				start = args[0]
			}
			resolved, err := findRoot(start)
			if err != nil {
				return err
			}
			gate := config.Severity(failOn)
			if gate != config.SevRequired && gate != config.SevWarn {
				return fmt.Errorf("--fail-on must be required or warn, got %q", failOn)
			}
			opts := ScoreOptions{
				Format:   diagText(),
				FailOn:   gate,
				Now:      time.Now().UTC(),
				LastEdit: gitLastEdit(resolved),
				JSON:     format == "json",
				History:  history,
			}
			fsys := os.DirFS(resolved)

			if history {
				for _, f := range scoreHistoryFiles(fsys, cmd.OutOrStdout(), cmd.ErrOrStderr(), opts) {
					full := filepath.Join(resolved, filepath.FromSlash(f.Path))
					if err := os.WriteFile(full, f.Data, 0o644); err != nil {
						return err
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "  wrote %s\n", f.Path)
				}
			}
			if code := Score(fsys, cmd.OutOrStdout(), cmd.ErrOrStderr(), opts); code != exitOK {
				return exitWith(code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "text or json")
	cmd.Flags().StringVar(&failOn, "fail-on", "required", "gate on required, or additionally on warn")
	cmd.Flags().BoolVar(&history, "history", false, "append this run to "+scorecard.HistoryPath)
	return cmd
}
```

**Why `computeScore` returns the `*config.Standards`:** the gate needs it to look up each check's severity, and loading it a second time would re-announce the "no standards.yaml found" line — printing the same degraded-mode warning twice, which trains people to stop reading it.

**On `scoreHistoryFiles` and `Score` both calling `computeScore`:** `--history` runs the pipeline twice. That is acceptable — scoring is hermetic and sub-second by D3, and the alternative is threading a computed scorecard through the cobra layer for one flag. If you find it offends, compute once in `newScoreCmd` and pass the result to both; do not cache it in a package variable.

- [ ] **Step 5: Register the command**

In `cmd/landsraad/root.go`, add `newScoreCmd()`.

- [ ] **Step 6: Run the tests**

Run: `go test ./cmd/landsraad/ -v`
Expected: PASS.

- [ ] **Step 7: Verify end to end**

```bash
go build -o /tmp/landsraad ./cmd/landsraad
cd "$(mktemp -d)" && git init -q && /tmp/landsraad init . >/dev/null && /tmp/landsraad score .; echo "exit=$?"
/tmp/landsraad score --format json . | head -20
```

Expected: the scaffolded example service is tier 3, so `--fail-on required` reports what it fails and the exit code is 3 or 0 depending on which checks are required at tier 3. `--format json` emits only JSON on stdout — pipe it through `jq .` to confirm.

- [ ] **Step 8: `task ci` and commit**

```bash
git add cmd/landsraad/score.go cmd/landsraad/score_test.go cmd/landsraad/main.go cmd/landsraad/root.go
git commit -m "feat: landsraad score, gated on --fail-on

Spec §8: --fail-on defaults to required, and warn is the ratchet a team
opts into once they have cleared it.

Exit 3, not 2. Spec §12 keeps them distinct because a broken
service.yaml is the YAML author's problem and a failing check is the
service owner's, and collapsing them sends the wrong person looking.

score refuses to run on a catalog with errors: a score computed from
metadata with a dangling ref is a number nobody should act on.

A repository with no standards.yaml is scored against spec §6's
published defaults and told so on stderr. Scoring against an empty
matrix would report a perfect score having checked nothing."
```

---

### Task 12: `validate` structurally checks `.landsraad/checks/*.yaml`

**Files:**
- Modify: `cmd/landsraad/validate.go`
- Modify: `cmd/landsraad/validate_test.go`
- Modify: `docs/superpowers/specs/2026-09-08-landsraad-design.md` (§7.1, record that the gap is closed)

**Interfaces:**
- Consumes: `scorecard.CheckResultsSchema`, `scorecard.ChecksDir` (Task 8); `schema.New`.
- Produces: `func validateCheckResults(fsys fs.FS, c *diag.Collector)`

**Context:** Ruling R8. Spec §7.1 says stage 6 is deliberately not part of `validate`, and explains why: "the schema defining that file's shape arrives with the scorecard in Plan 2, and validating a shape that is not yet specified is premature. The consequence is recorded rather than hidden: **until Plan 2 ships**, a malformed check-results file is first caught by the platform build, not by the PR that introduced it."

Plan 2 has now shipped that schema. This task closes the gap the spec itself flagged: `validate` gains **structural validation only** — no scoring, no ingest precedence, no network, no clock. It stays hermetic, which is what makes it safe to run in every service repo's PR CI with no tokens.

It must not duplicate the ingest diagnostics: this validates the *document*, and `score` resolves entities and precedence.

- [ ] **Step 1: Write the failing test**

Append to `cmd/landsraad/validate_test.go`:

```go
// Ruling R8, closing the gap spec §7.1 flagged: until Plan 2 shipped the
// CheckResults schema, a malformed results file was first caught by the
// platform build rather than by the PR that introduced it. That is the
// exit-0-over-something-unexamined shape this project keeps finding.
func TestValidateRejectsAMalformedCheckResultsFile(t *testing.T) {
	fsys := goodRepo()
	fsys[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\ngeneratedAt: not-a-date\nresults:\n  - { entity: service:api, check: x, status: pass }\n")}

	var out, errOut bytes.Buffer
	if code := Validate(fsys, &out, &errOut, diagText()); code != exitValidation {
		t.Fatalf("exit = %d, want %d — a malformed results file must fail the PR", code, exitValidation)
	}
}

// validate stays hermetic: it checks the document's shape and says nothing
// about whether the entities exist or which producer wins. Those need the
// merged catalog and a clock, and belong to score.
func TestValidateDoesNotResolveCheckResultEntities(t *testing.T) {
	fsys := goodRepo()
	fsys[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/x\ngeneratedAt: 2026-09-08T14:00:00Z\nresults:\n  - { entity: service:ghost, check: x, status: pass }\n")}

	var out, errOut bytes.Buffer
	if code := Validate(fsys, &out, &errOut, diagText()); code != exitOK {
		t.Fatalf("exit = %d, want %d — a well-formed file naming an unknown entity is score's problem, not validate's; stderr:\n%s",
			code, exitOK, errOut.String())
	}
}

func TestValidateAcceptsAWellFormedCheckResultsFile(t *testing.T) {
	fsys := goodRepo()
	fsys[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/x\ngeneratedAt: 2026-09-08T14:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")}

	var out, errOut bytes.Buffer
	if code := Validate(fsys, &out, &errOut, diagText()); code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
}
```

**Note for the implementer:** `goodRepo()` may not exist under that name — `cmd/landsraad/validate_test.go` already has a fixture helper for a valid in-memory repository. Use whatever it is called, and if the existing fixture is a `testdata` directory rather than a `MapFS`, add a small `MapFS` fixture rather than putting `.landsraad/checks` into `testdata/monorepo-ok`, which other tests assert on.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/landsraad/ -run TestValidate.*Check -v`
Expected: FAIL — the malformed file is currently ignored, so the first test gets exit 0.

- [ ] **Step 3: Write the implementation**

In `cmd/landsraad/validate.go`, add a stage after the existing schema loop and call it from `Validate`:

```go
// validateCheckResults structurally validates every .landsraad/checks/*.yaml.
//
// Spec §7.1 excluded this while the shape was unspecified, and said so: "until
// Plan 2 ships, a malformed check-results file is first caught by the platform
// build, not by the PR that introduced it." Plan 2 shipped the schema, so the
// PR catches it now.
//
// Structure only. Resolving entities, applying precedence and ageing results
// need the merged catalog and a clock, which would make validate neither
// hermetic nor offline — and being both is what lets it run in every service
// repo's PR CI with no tokens and no network.
func validateCheckResults(fsys fs.FS, c *diag.Collector) {
	entries, err := fs.ReadDir(fsys, scorecard.ChecksDir)
	if err != nil {
		// No directory is not a problem: most repositories report no external
		// results.
		return
	}
	v, err := schema.New(scorecard.CheckResultsSchema)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: scorecard.ChecksDir, Line: 1,
			Check:   "checks-schema",
			Message: fmt.Sprintf("cannot compile the check-results schema: %v", err),
		})
		return
	}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		path := scorecard.ChecksDir + "/" + e.Name()
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Check:   "checks-unreadable",
				Message: fmt.Sprintf("cannot read %s", path),
			})
			continue
		}
		v.Validate("", path, data, c)
	}
}
```

Call it from `Validate` immediately after the catalog-file schema loop, before `catalog.NewCatalog`. Add `"strings"` and the `scorecard` import if they are not already present.

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/landsraad/ -v`
Expected: PASS.

- [ ] **Step 5: Update the spec**

In `docs/superpowers/specs/2026-09-08-landsraad-design.md` §7.1, replace the paragraph beginning "Stage 6 (INGEST) is deliberately **not** part of `validate` in v1." with:

```markdown
Stage 6 (INGEST) is **not** part of `validate`: resolving entities, applying
precedence and ageing results need the merged catalog and a clock, and
`validate` is hermetic and offline so it can run in every service repo's PR CI
with no tokens.

Its *structural* half is. Plan 2 shipped the `CheckResults` schema, so
`validate` checks the shape of every `.landsraad/checks/*.yaml` — which is
hermetic, and closes the gap this section previously recorded: a malformed
results file is now caught by the PR that introduced it rather than by the
platform build days later.
```

- [ ] **Step 6: Full verification**

Run: `task ci` — expected green.

Then end to end:

```bash
go build -o /tmp/landsraad ./cmd/landsraad
cd "$(mktemp -d)" && git init -q && /tmp/landsraad init . >/dev/null
mkdir -p .landsraad/checks && printf 'apiVersion: landsraad/v1\nkind: CheckResults\ngeneratedAt: nope\nresults: []\n' > .landsraad/checks/x.yaml
/tmp/landsraad validate .; echo "exit=$? (want 2)"
```

- [ ] **Step 7: Commit**

```bash
git add cmd/landsraad/validate.go cmd/landsraad/validate_test.go \
        docs/superpowers/specs/2026-09-08-landsraad-design.md
git commit -m "feat: validate the shape of .landsraad/checks files

Spec §7.1 excluded this while the shape was unspecified and recorded the
consequence: 'until Plan 2 ships, a malformed check-results file is
first caught by the platform build, not by the PR that introduced it.'
Plan 2 shipped the schema, so the PR catches it now.

Structure only. Resolving entities, applying precedence and ageing
results need the merged catalog and a clock, and validate stays hermetic
and offline so it can run in every service repo's PR CI with no tokens.
A well-formed file naming an entity that does not exist is score's
problem, and there is a test pinning that it is not validate's.

Closes the last instance of this project's signature defect that the
spec itself had flagged: something the tool never looked at, reported as
fine."
```

---

## Definition of done

- `task ci` green.
- `landsraad gen` writes CODEOWNERS, `alertmanager-routes.yaml` and `slack-channels.yaml`; `landsraad gen --check` exits 0 when they are current and 2 when they are not.
- `landsraad score` reports per-entity and per-team scores, exits 3 when a check at or above `--fail-on` fails, 2 when the metadata is broken, 0 otherwise.
- `landsraad score --format json` emits only JSON on stdout.
- `landsraad score --history` appends to `scorecard-history.csv`.
- `landsraad validate` rejects a malformed `.landsraad/checks/*.yaml`.
- Nothing under `internal/` imports `os`/`os/*` or calls `time.Now()`.
- `scripts/check-rules.sh` passes, and `go doc ./internal/scorecard` reads as an explanation rather than a list.

## Before merging

Run `composition-auditor` (`.claude/agents/composition-auditor.md`). It audits abstractions in both directions and tests whether this project's documents' own claims are true. Plan 1's audit found eleven real defects, four of them repeats of the same failure this plan is full of guards against; expect it to find more here, particularly around `Env`, `ScoreOptions`, and whether `internal/emit` earns its keep now that it has three producers.
