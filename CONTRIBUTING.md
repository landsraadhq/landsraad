# Contributing to landsraad

## Before you open a PR

```sh
task ci
```

runs everything CI runs — `go vet`, a `gofmt` check, `go mod tidy -diff`,
the project rules below (`scripts/check-rules.sh`), and the full test suite.
It must pass from a clean checkout before a PR is reviewed.

## How this codebase is organised

Read `docs/superpowers/specs/2026-09-08-landsraad-design.md` first — the
decisions and their rationale (§3) explain *why* the code is shaped the way
it is, not just what it does. A few of those decisions are enforced
mechanically, not just documented:

- **All file access goes through `io/fs.FS`, never `os` calls against a path
  string** (spec §3.1). `os.DirFS` reads a local checkout; a test uses
  `fstest.MapFS`; a fetched remote repo will use a third implementation later
  — all three run the same discovery, parsing and file-check code unchanged.
  As a consequence, **nothing under `internal/` may import `"os"`** — reads
  take an `fs.FS`, writes take an `io.Writer`, and only `cmd/` touches the
  real filesystem. `task lint` fails on it; it isn't a style preference.
- **No `sync.Once`, no `func init()` below `cmd/`.** Package-level mutable
  state means two configurations can't coexist in the same process and
  initialisation failure can't be tested. `task lint` fails on this too.
- **Diagnostic wording is asserted exactly** — see Tests below. `task lint`
  fails on a substring assertion against `.Message` or `.Hint`.
- **New checks are Go functions with stable ids, not a plugin system** (D4).
  When the scorecard lands, a new check is a typed function registered by id
  and a line in `standards.yaml`'s severity matrix — never a YAML rule
  language or a dynamically loaded plugin. That's a deliberate non-goal, not
  a gap.

The first three are grep rules in `scripts/check-rules.sh`, which `task lint`
runs, so they fail in your shell rather than in review. `.claude/hooks/`
carries the same three patterns as editor-time hooks for Claude Code
sessions; those run in that workflow only, and are a convenience, not the
enforcement point.

## Tests

Most tests are one function per case, named for the behaviour they pin down
(`TestParseFileRejectsASecondDocument`), so a failure names the property that
broke before you read a line of it. A table with subtests is fine where the
cases really are the same assertion over different inputs — prefer whichever
makes the failure output say more.

For anything that produces a diagnostic, the test asserts the **exact**
message string (and hint, where the diagnostic sets one) — never a substring
match. Error message quality is the product here; a wording regression that a
`strings.Contains` check would let through is a regression a user has to
puzzle out in their own CI log. `task lint` fails on `strings.Contains`
against `.Message` or `.Hint` in a test file for this reason — assert with
`==` and show both strings on failure:

```go
if got.Message != want {
    t.Errorf("\n got: %s\nwant: %s", got.Message, want)
}
```

Fixture repos live in `testdata/`. The test suite reaches zero network — if
a test needs an HTTP call, it goes through `httptest` with a recorded
response, not a live request.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/) style
(`feat:`, `fix:`, `test:`, `docs:`, …) for the summary line. Commits and PR
descriptions carry no AI attribution of any kind, regardless of what wrote
the change.

## Adding a new output format

Implement `diag.Formatter` and add one line to `diag.Formatters()` — that's
the whole extension point, by design. There's deliberately no registry type
or `init()`-based self-registration to wire up.
