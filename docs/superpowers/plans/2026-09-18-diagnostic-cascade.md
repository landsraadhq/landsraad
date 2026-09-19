# Diagnostic cascade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the regression `d8148f1` introduced, fix the twin defect it was wrongly reasoned safe against, and correct four documentation claims the audit proved false.

**Architecture:** Three independent changes, in dependency order. Task 1 hoists the `teams.yaml` read above every give-up path in the single-repository load composition and makes that ordering a compile error by giving `assemble` a `*config.Teams` parameter in place of the `fs.FS` it only used for that read. Task 2 makes `loadReposFile` distinguish "parsed and named nothing" from "did not parse" via `Repos.Loaded()`, which retires the twin defect and makes Task 1's neighbouring claim about `parseRepo` true. Task 3 is documentation and one-character fixes.

**Tech Stack:** Go, go-task (`task ci`, `task build`), `testing/fstest.MapFS`, `gopkg.in/yaml.v3`.

**Spec:** `docs/superpowers/specs/2026-09-18-diagnostic-cascade-design.md` — rulings R46, R47, R48 and the "Hygiene: no decision needed" section.

## Global Constraints

- `task ci` green before every commit: `go test -race ./...`, `go vet ./...`, `gofmt -l .` empty, `go mod tidy -diff`, `sh scripts/check-rules.sh`.
- Diagnostics assert **exact** message strings. A substring check at the integration layer is acceptable only when the full message text is the substring, and only where the unit layer already pins the wording.
- No non-test file under `internal/` imports `os` or any `os/*` package. All work here is in `cmd/` and documentation; `internal/render/scorecard.go` is touched but gains no import.
- Exit codes are a one-way door. Every task touching a diagnostic set pins the exit code in its test.
- Commit messages carry no AI attribution — no `Co-Authored-By`, no "Generated with".
- Branch: `fix/diagnostic-cascade-r42`. Already holds `d8148f1` and `bf3016e`.

## Files

| File | Responsibility after this plan |
|---|---|
| `cmd/landsraad/gen.go` | Gains `loadTeamsFor`; `assemble` takes `teams *config.Teams` instead of `cfg fs.FS`; `loadCatalogScoped` reads teams before both give-up paths |
| `cmd/landsraad/build.go` | Reads teams before its own `v == nil` return, passes it to `assemble` |
| `cmd/landsraad/repos.go` | `loadReposFile` returns no repositories when `repos.yaml` did not parse |
| `cmd/landsraad/gen_test.go` | New: `gen` reports `teams.yaml` problems despite a `repos-parse` error |
| `cmd/landsraad/repos_test.go` | Four `assemble` call sites updated; new `loadReposFile` parse-failure test |
| `cmd/landsraad/score_test.go` | New: `score` matches `gen` and `validate` on the same repository |
| `internal/render/scorecard.go` | `scorecardTiers` becomes a fixed-size array |
| `CLAUDE.md` | Rule 2 names `//go:embed` as its forced exception |
| `docs/superpowers/specs/2026-09-08-landsraad-design.md` | D11, the `unevaluatedProperties` bullet and §7.1's stage table corrected |

---

### Task 1: `teams.yaml` is read before every give-up path (R46)

**Files:**
- Modify: `cmd/landsraad/gen.go:39-70` (`loadCatalogScoped`), `cmd/landsraad/gen.go:153-210` (`assemble`)
- Modify: `cmd/landsraad/build.go:72-79`
- Modify: `cmd/landsraad/repos_test.go:409,424,470,552` (four `assemble` call sites)
- Test: `cmd/landsraad/gen_test.go`, `cmd/landsraad/score_test.go`

**Interfaces:**
- Produces: `func loadTeamsFor(cfg fs.FS, c *diag.Collector) *config.Teams` — returns nil and adds the `missing-teams` error when `teams.yaml` is absent; otherwise returns `config.LoadTeams`'s result, which is non-nil even when the file failed to parse.
- Changes: `func assemble(p parseResult, src catalog.Sources, scope catalog.Scope, teams *config.Teams, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams)` — fourth parameter was `cfg fs.FS`.

- [ ] **Step 1: Write the failing test**

Add to `cmd/landsraad/gen_test.go`:

```go
// Ruling R46: teams.yaml is a different file from repos.yaml, so a
// repos-parse error must not hide missing-teams. service.yaml sits INSIDE
// the fallback globs deliberately, so no-entities is never in play and the
// only thing under test is whether teams.yaml was read at all.
func TestGenReportsTeamsProblemsWhenReposYAMLFailedToParse(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml":                {Data: []byte("kind: Service\n  bad: indent\n")},
		"services/api/service.yaml": genFS()["services/api/service.yaml"],
	}
	var out, errOut bytes.Buffer

	code := Gen(fsys, t.TempDir(), &out, &errOut, diagText(), false)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
	}
	got := errOut.String()
	for _, want := range []string{
		"cannot parse repos file: yaml: line 2: mapping values are not allowed in this context",
		"teams.yaml not found at the repository root, so no owner can be resolved",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr must report both files' problems in one run (R46); missing:\n  %s\ngot:\n%s", want, got)
		}
	}
}
```

Add to `cmd/landsraad/score_test.go` — `gen`, `score` and `validate` must agree about the same directory (R43):

```go
// Ruling R46 for score, which reaches the same loadCatalog as gen.
func TestScoreReportsTeamsProblemsWhenReposYAMLFailedToParse(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml":                {Data: []byte("kind: Service\n  bad: indent\n")},
		"services/api/service.yaml": genFS()["services/api/service.yaml"],
	}
	var out, errOut bytes.Buffer

	code := Score(fsys, &out, &errOut, diagText(), ScoreOptions{Now: time.Now()})

	if code == exitOK {
		t.Fatalf("a malformed repos.yaml must not exit clean, got %d", code)
	}
	if want := "teams.yaml not found at the repository root, so no owner can be resolved"; !strings.Contains(errOut.String(), want) {
		t.Errorf("score must report teams.yaml problems alongside repos-parse (R46); missing %q:\n%s", want, errOut.String())
	}
}
```

If `Score`'s signature or `ScoreOptions` differ, match the existing calls in `score_test.go` rather than this sketch — only the fixture and the two assertions matter.

- [ ] **Step 2: Run them and watch them fail**

```
go test ./cmd/landsraad/ -run 'ReportsTeamsProblemsWhenReposYAMLFailedToParse' -v
```

Expected: both FAIL, reporting that `teams.yaml not found at the repository root...` is missing from stderr. The `repos-parse` line is present. If `missing-teams` is already there, stop — the regression is not where this plan says it is.

- [ ] **Step 3: Extract the read**

In `cmd/landsraad/gen.go`, delete lines 157-175 from `assemble` (the `R42` comment, `var teams *config.Teams`, and the whole `if data, err := fs.ReadFile(cfg, "teams.yaml")` block), and add this new function immediately above `assemble`:

```go
// loadTeamsFor reads teams.yaml and reports its own mistakes.
//
// Called before every give-up path in the load composition, because
// teams.yaml is a different file from repos.yaml and from the embedded
// schema, and a failure to read either of those is no reason to withhold a
// fact about this one. R42 established that for the empty-catalog return;
// ruling R46 makes it hold for all of them. assemble takes the result rather
// than the filesystem, so it cannot run before this (ordering as a compile
// error) — which is what the two give-up paths in loadCatalogScoped used to
// do.
func loadTeamsFor(cfg fs.FS, c *diag.Collector) *config.Teams {
	data, err := fs.ReadFile(cfg, "teams.yaml")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			// validate's id and message for the same absent file (ruling R43).
			// The hint is this command's own: gen, score and build run only in
			// the platform repository, where --satellite is not an answer.
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		})
		return nil
	}
	return config.LoadTeams("teams.yaml", data, c)
}
```

Change `assemble`'s signature and its doc comment:

```go
// assemble runs stages 4 and 5 over every repository's entities at once,
// against the configuration the command is standing in (ruling R34). teams is
// loadTeamsFor's result, taken as a value rather than read here, so no caller
// can reach stage 4 without teams.yaml's own diagnostics already collected
// (ruling R46).
func assemble(p parseResult, src catalog.Sources, scope catalog.Scope, teams *config.Teams, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	if len(p.entities) == 0 {
```

The rest of `assemble` — the `len(src) > 1 && p.found == 0` block, `NewCatalog`, `CheckFiles`, `Resolve`, `reportCycles`, the `teams == nil` return and `ValidateOwners` — is unchanged.

- [ ] **Step 4: Hoist the call above both give-up paths**

In `cmd/landsraad/gen.go`, `loadCatalogScoped` becomes:

```go
func loadCatalogScoped(fsys fs.FS, scope catalog.Scope, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	// R46: before both give-up paths below. A schema that will not compile is
	// a landsraad bug and an unparseable repos.yaml is the user's, and neither
	// is a reason to hide what is wrong with teams.yaml.
	teams := loadTeamsFor(fsys, c)

	v := defaultValidator(c)
	if v == nil {
		return nil, nil, nil
	}
	repo := localRepoName(fsys)
	// solo: true. loadCatalogScoped is exclusively the single-repository
	// composition (validate, gen, score — ruling R30), so the "no entities"
	// diagnostic below must be the one error main always produced, not the
	// per-repository warning that exists for the multi-repository case.
	patterns, patternsKnown := patternsFor(fsys, c)
	if !patternsKnown {
		// repos.yaml is present but did not parse, so patterns is a guess.
		// parseRepo's no-entities would be a second diagnostic for the one
		// cause repos-parse already reports (defect 4). teams.yaml's problems
		// are already collected above, and are not a second diagnostic for
		// that cause (ruling R46).
		return nil, nil, nil
	}
	p := parseRepo(repo, fsys, patterns, true, v, c)
	return assemble(p, catalog.SingleSource(repo, fsys), scope, teams, c)
}
```

In `cmd/landsraad/build.go`, move the read above the `v == nil` return at line 72 and pass it through. The block from line 72 becomes:

```go
	teams := loadTeamsFor(root, &c) // R46: above the give-up path below
	if v == nil {
		reportDiagnostics(errOut, c.Diagnostics(), len(w.Sources()) > 1)
		return nil, exitValidation
	}

	// FullCatalog, not LocalOnly: build claims to render the whole catalog,
	// so a dangling reference is a hard failure (spec §7.1).
	cat, g, teams := assemble(w.ParseAll(v, &c), w.Sources(), catalog.FullCatalog, teams, &c)
```

`cat` and `g` are new, so `:=` assigns rather than redeclares `teams`, and everything downstream of line 79 is unchanged.

In `cmd/landsraad/repos_test.go`, replace `teamsOnly()` with `loadTeamsFor(teamsOnly(), &c)` at each of the four `assemble` calls (lines 409, 424, 470, 552). Each already declares `var c diag.Collector` above the call; if a call site does not, add one.

- [ ] **Step 5: Run the new tests, then everything**

```
go test ./cmd/landsraad/ -run 'ReportsTeamsProblemsWhenReposYAMLFailedToParse' -v
task ci
```

Expected: both new tests PASS; `task ci` fully green. `TestLoadCatalogReportsMissingTeamsLikeValidate` (`validate_test.go:302`) must still pass — it pins R43 and is the test that could not see this regression.

- [ ] **Step 6: Prove the fix is what makes them pass**

```
git stash push cmd/landsraad/gen.go cmd/landsraad/build.go cmd/landsraad/repos_test.go
go test ./cmd/landsraad/ -run 'ReportsTeamsProblemsWhenReposYAMLFailedToParse'
git stash pop
```

Expected: FAIL while stashed. A test that passes without the fix is not testing the fix.

- [ ] **Step 7: Commit**

```bash
git add cmd/landsraad/gen.go cmd/landsraad/build.go cmd/landsraad/gen_test.go cmd/landsraad/score_test.go cmd/landsraad/repos_test.go
git commit -m "fix: teams.yaml is read before every give-up path (R46)"
```

---

### Task 2: a `repos.yaml` that did not parse yields no repositories (R47)

**Files:**
- Modify: `cmd/landsraad/repos.go:639-647`
- Test: `cmd/landsraad/repos_test.go`

**Interfaces:**
- Consumes: nothing from Task 1.
- Changes: `loadReposFile` returns `nil` on a parse failure, where it previously returned one synthesised `{Local: true, Paths: config.DefaultPatterns(), Line: 1}`.

- [ ] **Step 1: Write the failing test**

Add to `cmd/landsraad/repos_test.go`:

```go
// Ruling R47: len(r.Repos) == 0 is true both for a file that parsed and named
// nothing and for a file that did not parse. Repos.Loaded() carries the
// distinction, and patternsFor already uses it. Announcing default-patterns
// for a parse failure is two diagnostics for one cause, sorted ahead of its
// own cause, with a hint naming a file that exists — and a message that is
// false, since the file names one repository.
func TestLoadReposFileReturnsNoReposWhenParseFailed(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml": {Data: []byte("kind: Service\n  bad: indent\n")},
	}
	var c diag.Collector

	got := loadReposFile(fsys, &c)

	if len(got) != 0 {
		t.Fatalf("loadReposFile = %+v, want no repositories: a file that did not parse configures nothing", got)
	}
	var parse bool
	for _, d := range c.Diagnostics() {
		switch d.Check {
		case "repos-parse":
			parse = true
		case "default-patterns":
			t.Errorf("default-patterns must not be announced for a file that did not parse; got message %q", d.Message)
		}
	}
	if !parse {
		t.Fatalf("the cause must still be reported; no repos-parse in %+v", c.Diagnostics())
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

```
go test ./cmd/landsraad/ -run TestLoadReposFileReturnsNoReposWhenParseFailed -v
```

Expected: FAIL twice over — one synthesised repository returned, and a `default-patterns` diagnostic whose message is `repos.yaml lists no repositories; using default paths (...)`.

- [ ] **Step 3: Guard on `Loaded()`**

In `cmd/landsraad/repos.go`, insert the new branch between `LoadRepos` and the existing `len(r.Repos) == 0` check:

```go
	r := config.LoadRepos("repos.yaml", data, c)
	if !r.Loaded() {
		// The file exists and did not parse. repos-parse already carries the
		// cause, and defaulting here would present a guess as configuration —
		// the same rule patternsFor applies to default-patterns (ruling R47).
		// Returning no repositories is also what makes the multi-repository
		// path structurally unable to reach parseRepo's solo no-entities off
		// a guessed pattern set: there is nothing for ParseAll to iterate.
		return nil
	}
	if len(r.Repos) == 0 {
		// Same degraded mode as an absent file — repos.yaml exists but names
		// no repositories at all — and it deserves the same announcement:
		// silent here would mean this specific case is invisible while its
		// sibling three lines up is not.
		c.Add(defaultPatternsNote("repos.yaml lists no repositories"))
		return []config.Repo{{Local: true, Paths: config.DefaultPatterns(), Line: 1}}
	}
```

- [ ] **Step 4: Run it, then prove `build` and `serve` changed**

```
go test ./cmd/landsraad/ -run TestLoadReposFile -v
task ci
```

Expected: the new test PASSES; `TestLoadReposFileWithEmptyReposListAnnouncesDefaultPatterns` (`repos_test.go:168`) still passes — it covers the parsed-but-empty half, which must keep its note. `task ci` green.

Then confirm the user-visible change, which is the point of the task:

```
task build
mkdir -p /tmp/r47 && cd /tmp/r47
printf 'repos:\n  - url: https://example.com/x\n    paths: [a, *broken]\n' > repos.yaml
/path/to/landsraad/bin/landsraad build --out ./dist ; echo "EXIT=$?"
```

Expected: `repos-parse` alone. No `default-patterns`, and no `no-entities`. Exit code unchanged from before the task — check it against `git stash` if in doubt.

- [ ] **Step 5: Commit**

```bash
git add cmd/landsraad/repos.go cmd/landsraad/repos_test.go
git commit -m "fix: a repos.yaml that did not parse configures no repositories (R47)"
```

---

### Task 3: claims match the code (R48 and hygiene)

**Files:**
- Modify: `docs/superpowers/specs/2026-09-08-landsraad-design.md` (D11 at line 90, the `unevaluatedProperties` bullet at 275-277, §7.1's table at 405)
- Modify: `CLAUDE.md` (rule 2 row)
- Modify: `internal/render/scorecard.go:16`
- Modify: `cmd/landsraad/validate.go:372`

**Interfaces:** none. No production behaviour changes; `scorecardTiers` keeps the same values and the same read sites.

- [ ] **Step 1: Verify each claim before changing it**

```
grep -c 'additionalProperties' schema/service.schema.json    # expect 3, all map-value schemas
grep -c 'unevaluatedProperties' schema/service.schema.json    # expect 7
grep -n 'Registry' internal/diag/format.go                    # expect the postmortem comment only
grep -rn 'scorecardTiers' internal/render/                    # expect the var at :16 plus reads at :75 and :109
```

Do not change a claim whose grep disagrees with the spec — report instead. The point of this task is that claims and code agree, so a surprise here means the spec is wrong about itself again.

**Do not touch `cmd/landsraad/validate.go`.** The audit reported its "eleven call sites" comment as drift, counting ten. The pre-flight scan found the audit had excluded test files, and `plural` is called from `cmd/landsraad/validate_test.go:632`, which is in the same `package main`. Ten production call sites plus one test call site is eleven, so the comment is correct and stays. Recorded as a pre-flight ruling.

- [ ] **Step 2: Correct the four documentation claims**

In `docs/superpowers/specs/2026-09-08-landsraad-design.md`, the `kind: API` bullet — replace `additionalProperties: false` with `` `unevaluatedProperties: false` `` (the conclusion is correct and stays; only the keyword is wrong).

In the same file, D11's row: replace "output formats through a `Formatter` registry" with "output formats through a `diag.Formatter` interface and a map literal", so the decision record agrees with §3.1 and with `internal/diag/format.go`, which records that the `Registry` type was deleted as speculative generality.

In the same file, §7.1's table: change `validate`'s stages from `1, 3, 4, 5*` to `1, 3, 4, 5*, 6*`, matching the prose six lines below and `validateCheckResults`.

In `CLAUDE.md`, rule 2's row: after "no `init()` below `cmd/`", name the forced exception — `//go:embed` requires a package-level var, so `schema.Raw`, `config.StandardsSchema`, `config.DefaultStandardsYAML`, `scorecard.CheckResultsSchema` and `render.webFS` are exempt; nothing mutates them, and `schema.Raw` has real cross-package readers (`internal/scaffold/scaffold.go:41` writes it into a user's repository at `landsraad init`). Phrase it the way the `os` row already names its two test exceptions.

- [ ] **Step 3: Give `scorecardTiers` the lesson `entity.go` already learned**

In `internal/render/scorecard.go:16`:

```go
var scorecardTiers = [...]int{1, 2, 3}
```

`internal/catalog/entity.go:30-48` documents why: tests in a package share a process, so one test mutating an exported-or-not package-level slice without a `t.Cleanup` poisons every test after it and the failure surfaces somewhere else entirely. A fixed-size array cannot be reassigned element-wise through a shared backing store.

Two read sites, and they differ. `internal/render/scorecard.go:109` ranges over the value, which works on an array unchanged. `internal/render/scorecard.go:75` assigns it to a `Tiers []int` field (declared at `internal/render/model.go:126` and `internal/render/scorecard.go:54`), so that site becomes:

```go
		Tiers:             scorecardTiers[:],
```

Do not widen the field to an array or revert the var to a slice — the point is that the package-level value cannot be mutated through a shared backing store, and `[:]` at the one assignment keeps the field's type untouched.

- [ ] **Step 4: Verify and commit**

```
task ci
```

Expected: green. `scorecardTiers` is the only code change and it is type-only, so any failure is a read site needing `[:]`.

```bash
git add docs/superpowers/specs/2026-09-08-landsraad-design.md CLAUDE.md internal/render/scorecard.go
git commit -m "docs: claims match the code (R48), and scorecardTiers stops being a mutable slice"
```

---

## Self-review

**Spec coverage.** R46 → Task 1. R47 → Task 2. R48 → Task 3 Step 2 (both bullets). Hygiene's four items → three tasks and one ruling: `//go:embed` and §7.1's table in Task 3 Step 2, `scorecardTiers` in Step 3, and the call-site count **withdrawn** — the pre-flight scan proved the comment correct and the audit's count wrong (it excluded `validate_test.go:632`, which is in the same package). The spec's "Hygiene" section should be read with that ruling attached. The spec's "Order" section is 1, 2, 3 as numbered. The spec's "What the audit confirmed" section is out of scope and has no task, correctly.

**Placeholders.** None. Every code step carries the code. The one hedge — `Score`'s signature in Task 1 Step 1 — names the fallback explicitly ("match the existing calls in `score_test.go`") rather than leaving it open, because the two assertions are what matter and the harness around them is local convention.

**Type consistency.** `loadTeamsFor(cfg fs.FS, c *diag.Collector) *config.Teams` is defined in Task 1 Step 3 and used in Task 1 Step 4 at three sites (`loadCatalogScoped`, `build.go`, `repos_test.go`) with that exact signature. `assemble`'s fourth parameter is `teams *config.Teams` in the signature, in `loadCatalogScoped`, in `build.go` and in the four test call sites. `loadReposFile`'s return type is unchanged (`[]config.Repo`); only the parse-failure value changes, from a one-element slice to `nil`.
