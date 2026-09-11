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
- **Only `internal/fetch` imports `net/http`, or bare `net`** (Plan 4 ruling
  R25). Every other package under `internal/` is a pipeline stage, and a
  stage that blocks on a socket cannot be tested offline, cannot run in a
  service repo's PR CI, and makes "`score` runs offline in under a second" a
  claim about which filesystem you happened to pass it. `task lint` fails on
  this too. One check makes requests from inside a stage:
  `docs-fresh` asks a host when a path last changed, during `Score`, once
  per entity path. It reaches the socket
  through `scorecard.LastEditFunc`, an injected function type rather than an
  import, so the rule holds and a local score still makes no request.
- **New checks are Go functions with stable ids, not a plugin system** (D4).
  When the scorecard lands, a new check is a typed function registered by id
  and a line in `standards.yaml`'s severity matrix — never a YAML rule
  language or a dynamically loaded plugin. That's a deliberate non-goal, not
  a gap.

The first four are grep rules in `scripts/check-rules.sh`, which `task lint`
runs, so they fail in your shell rather than in review. `.claude/hooks/`
carries the same four patterns as editor-time hooks for Claude Code
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

To check that claim rather than trust it, point every proxy variable at a
dead port and run the suite:

```sh
HTTP_PROXY=http://127.0.0.1:1 HTTPS_PROXY=http://127.0.0.1:1 ALL_PROXY=http://127.0.0.1:1 \
  go test ./... -count=1
```

This works because Go's `http.ProxyFromEnvironment` never proxies loopback:
every `httptest` server listens on `127.0.0.1`, so requests to it still go
straight through, while any request to a real host gets routed at the dead
port and fails instantly instead of hanging or, worse, succeeding. A `-v`
grep for a hostname like `api.github.com` proves nothing by itself — it can
only find a hostname that happens to appear in test *output*, which includes
subtest names built from table-driven inputs (`TestParseRepo`'s cases name
URLs; `ParseRepo` is pure string parsing that touches no socket) and says
nothing about whether a request was actually made.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/) style
(`feat:`, `fix:`, `test:`, `docs:`, …) for the summary line. Commits and PR
descriptions carry no AI attribution of any kind, regardless of what wrote
the change.

## Adding a new output format

Implement `diag.Formatter` and add one line to `diag.Formatters()` — that's
the whole extension point, by design. There's deliberately no registry type
or `init()`-based self-registration to wire up.

## Adding a page to the portal

1. A view model in `internal/render/`, and a function returning `[]emit.File`
   or `(emit.File, bool)`. **Never write a file** — `cmd/` owns the one loop
   that does, and `internal/` may not import `os`.
2. A template in `internal/render/web/templates/`. Every page template defines
   `"content"`; `templateSet` parses `base.html` plus exactly one of them,
   because a shared set would silently keep only the last one parsed.
3. Every link is `{{.Root}}` plus a site-relative path. `Root` is the relative
   path back to the site root, so the portal works when hosted in a
   subdirectory. An absolute `/assets/style.css` would 404 there, on every
   page, with nothing to report it.
4. A golden test. `go test ./internal/render/ -update` regenerates the fixtures
   — **read the diff before committing it.** A golden file refreshed without
   being looked at asserts nothing.
5. Static assets go in `internal/render/web/static/` and are picked up
   automatically; add the `<script>` or `<link>` tag to `base.html`.

### Browser support: modern/evergreen only

The portal's client scripts target **current versions of Chrome, Firefox, Safari
and Edge**. There is no IE11 support, no transpiler and no polyfill anywhere in
the tree, and none is going to be added.

The scripts are already written past that line and cannot be walked back: they
use `fetch`, `Promise`, `document.currentScript` and `Element.replaceWith`
(`mermaid.js`), none of which exist in IE11. So the ES5-looking style you will
find in `internal/render/web/static/` — `var`, `function` expressions,
`Array.prototype.slice.call` on a `NodeList` — is **house style, not a
compatibility contract**. Match it for consistency if you like; do not pay an
ES5 tax believing it buys a guarantee, because it does not, and do not "fix" a
script to ES5 on compatibility grounds.

Every script must still degrade without JavaScript at all. That is a real
requirement and a separate one: the catalog table ships fully rendered and
sorted in the HTML, and `mermaid.js` replaces an unrendered diagram with a
visible note rather than a blank space.

Rendered Markdown is the only place `template.HTML` appears, in
`internal/render/docs.go`. It is safe because `md.New()` configures goldmark
**without** `WithUnsafe` (spec §14.1), so raw HTML in a runbook was already
escaped into text. Do not reuse that cast on bytes that have not been through
`md.Render`.
