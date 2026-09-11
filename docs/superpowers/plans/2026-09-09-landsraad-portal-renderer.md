# landsraad Portal Renderer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the validated, scored catalog into a browsable static portal — `landsraad build -o dist` renders it, `landsraad serve --watch` previews it with live rebuild.

**Architecture:** `internal/render.Site(Input) []emit.File` is a pure function: it takes the catalog, the graph, the teams, the scorecard and an `fs.FS` to read documentation from, and returns every byte of the site as values. `cmd/` owns the one loop that puts them on disk — the same loop `gen` and `init` already use. Markdown goes through `internal/render/md`, a goldmark pipeline running *without* `WithUnsafe`. Templates, CSS and the vanilla-JS clients are `go:embed`ed. Because the site is a value, `serve --watch` rebuilds it in memory and serves from a map, never writing to disk and so never watching its own output.

**Tech Stack:** Go 1.23 (toolchain 1.26.1), go-task 3.53.1, cobra, `gopkg.in/yaml.v3` v3.0.1, `github.com/santhosh-tekuri/jsonschema/v6` v6.0.1, `github.com/google/go-cmp` v0.6.0. **Three new direct dependencies:** `github.com/yuin/goldmark` v1.8.6, `github.com/alecthomas/chroma/v2` v2.24.1 (pinned below v2.27.0, which declares `go 1.25` and would raise this module's floor past the `go-version: '1.23'` / `golang:1.23` CI snippets the README publishes to adopters), `github.com/fsnotify/fsnotify` v1.10.1.

**Spec:** `docs/superpowers/specs/2026-09-08-landsraad-design.md` — this plan implements sub-project **D** (§10, the portal) and the `build`/`serve` half of §8. Read the spec alongside this plan; where they disagree, the spec wins and the disagreement is a bug in this plan, **except** where a ruling below records a deliberate, argued deviation.

**Baseline:** Plans 1 and 2 are complete and merged to `main` at `fb8dc82`. `landsraad validate`, `init`, `schema`, `gen` and `score` all work end to end. Run `task ci` before Task 1 and confirm it is green — a dirty baseline makes every later failure ambiguous.

**Out of scope, deliberately:** stage 2 (FETCH). This plan renders the **local** repository's catalog only. `internal/fetch`, the GitHub and GitLab adapters, remote `docs-fresh` over the host API, `--allow-partial` and its degraded-mode banner are Plan 4 (Carryall). Every stage here already takes an `io/fs.FS`, so Plan 4 adds a loop over repos and changes nothing below `cmd/`. See "What Plan 4 adds" at the end.

## Global Constraints

Every task's requirements implicitly include this section. Values are copied verbatim from the spec or from the existing codebase.

- **Nothing under `internal/` imports `os` or any `os/*` package.** Reads take `io/fs.FS`; writes return values that `cmd/` writes. Enforced by `.claude/hooks/no-os-in-internal.py` and `scripts/check-rules.sh`; both exclude `*_test.go`. **This bites hardest in this plan** — a renderer is the exact place a contributor reaches for `os.MkdirAll`. The answer is always: return an `emit.File`.
- **Nothing under `internal/` calls `time.Now()`, `rand`, or the network.** The `time` *package* is fine; the current instant is injected. `cmd/` supplies `time.Now()`. A generated page carries a build timestamp, so this is load-bearing: without it, every golden file fails one second after it is written.
- **No `sync.Once`, no `init()`, no package-level mutable state below `cmd/`.** Sets of things are functions returning a fresh slice, as `catalog.AllKinds()` and `config.DefaultPatterns()` are. The compiled `goldmark.Markdown` and the parsed `template.Template` are **values**, constructed by a `New`-style function and passed down — never package globals behind a `sync.Once`.
- **Diagnostics assert exact message strings in tests.** `strings.Contains` against `.Message` or `.Hint` is blocked by `.claude/hooks/exact-message-tests.py` and by `scripts/check-rules.sh`. Assert with `!=` against a full literal.
- **Accumulate, never fail fast** (spec §12). Every stage appends to a `*diag.Collector` and continues. A runbook that fails to render must not stop the other forty pages.
- **No silent fallbacks** (spec §12). A degraded mode is visible in the artifact, not only in a log. A missing `docs/` directory, an unreadable runbook and an entity with no documentation at all are three different answers and must render differently.
- **Every diagnostic carries a file and a line**, and says what to do. `Line: 0` is a bug; use `1` when the file has no better location.
- **Exit codes** (spec §12): `0` clean, `1` usage or config error, `2` validation error, `3` scorecard gate. Constants live in `cmd/landsraad/main.go`. `build` refuses a broken catalog with exit `2`, for the same reason `gen` does: a portal generated from a catalog with a dangling ref publishes the broken state as if it were the truth.
- **Stream contract** (spec §12): `stdout` carries **only** the selected format's payload; every human line goes to `stderr`. `build` writes no payload to stdout, so everything it says goes to stderr.
- **Artifacts are keyed on the ref** (`kind:name`), never the bare name (spec §12). This is the portal's URL scheme too: `service:orders` and `topic:orders` are different pages.
- **goldmark runs without `WithUnsafe`** (spec §14.1). Raw HTML in somebody's runbook is escaped, not injected into a shared portal page. There is a test that asserts this and it must never be deleted.
- **Go stays gofmt-clean and vet-clean.** `task ci` runs `go vet ./...`, `gofmt -l`, `go mod tidy -diff` and `scripts/check-rules.sh`.
- **Commit after every task**, with the test and the implementation in the same commit. Never `git add .` — add files individually.

---

## Rulings the spec does not settle

Recorded here because an executor who hits one of these mid-task will otherwise invent an answer, and because the *why* is the part that gets lost.

| # | Question | Ruling | What it costs |
|---|---|---|---|
| R10 | Spec §13 puts templates and assets at a top-level `web/`. `go:embed` patterns are relative to the package directory and may not contain `..`, so `internal/render` cannot reach a root-level `web/`. The alternative — a root-level `package web` holding the embed — is a **public** import path, which D8 ("everything starts in `internal/`") forbids. | Assets live at **`internal/render/web/`**, embedded by `internal/render/assets.go` with `//go:embed web`. | Spec §13's layout sketch is wrong and should be amended; Task 15 does that. The sketch predates the embed constraint. |
| R11 | What is a page's URL? | `entity/<kind>/<name>/index.html`, kind lowercased — keyed on the **ref**, per spec §12. Teams at `team/<name>/index.html`, plus `index.html`, `scorecard/index.html`, `map/index.html`. | Directory-per-page means every URL ends in `/` and the site works behind any static host with no rewrite rules. Costs one `index.html` per entity instead of one flat file. |
| R12 | Pages sit at different depths, so how do they link to `assets/style.css`? | Every page model carries `Root`, the relative path back to the site root (`""` at the top, `"../../../"` for an entity page), computed from the output path by `rootRel`. **No absolute paths anywhere.** | The site works when served from a subdirectory (`https://internal/portal/`), which is how most teams will host it. Costs one field on every page model and one unit-tested function. |
| R13 | Mermaid is 3.5 MB. Vendoring it puts 3.5 MB in git, in the `go install` binary, and in every `dist/`. | **CDN by default, pinned with an SRI hash**: `https://cdn.jsdelivr.net/npm/mermaid@11.17.2/dist/mermaid.min.js`, integrity `sha384-EOXBFmc3gx5mb+vn0vPvvGqACToJD24hhacX5Yx+8NUUQrHIle/Qi5Bg9o3zKwW2`. `build --mermaid-src <url-or-path>` overrides it; a **path** makes the renderer copy that file into `dist/assets/mermaid.min.js` and reference it relatively, with no integrity attribute. | An air-gapped adopter must pass one documented flag. The alternative made every adopter pay 3.5 MB for a property most of them do not need. The flag makes the offline case *explicit* rather than a diagram that silently never appears. |
| R14 | Chroma highlights with inline styles or CSS classes? | **Classes** (`chromahtml.WithClasses(true)`), with the stylesheet emitted once as `assets/chroma.css` from `formatter.WriteCSS`. Style: `github`. | One 4.3 KB stylesheet instead of inline `style=` on every token — smaller pages, and the style is swappable in one place. Costs one more emitted asset. |
| R15 | An unknown fence language (` ```notalanguage `) — error, or fall through? | Render as a plain escaped `<pre><code>`. Not a diagnostic. | Chroma has ~250 lexers and a runbook naming a language it lacks is not a metadata problem. Reporting it would put noise in every run. The content is still visible and still escaped. |
| R16 | `build` writes into `-o dist`. On the next build, what happens to the page of an entity that was deleted? | `build` writes a **manifest** (`dist/.landsraad-manifest`) listing every path it produced. On a rebuild it deletes the paths in the previous manifest that are not in the new set, then writes. If the output directory exists, is non-empty, and has **no** manifest, `build` refuses with exit `1` unless `--force`. | Never `rm -rf` a directory the user named. Stale pages from a deleted service are a lie the portal would tell forever; refusing to clobber an unknown directory is the guard against `build -o .`. Costs a manifest file and one refusal path. |
| R17 | Where does the search index's body text come from, and how big may it get? | Headings plus paragraph text of every rendered Markdown document, **truncated to 2000 runes per document**, truncated on a rune boundary. | A forty-service monorepo with long runbooks would otherwise ship a multi-megabyte JSON file to every visitor for a feature that mostly matches titles and headings. 2000 runes keeps the index in the tens of KB. A full-text search is a server component, which spec §15 rules out. |
| R18 | Spec §10 wants the service page to show "then rendered `docs/`". Inline the whole directory, or paginate? | `docs/index.md` renders **inline** on the entity page. Every other `.md` under `spec.docs` becomes a sub-page at `entity/<kind>/<name>/docs/<relpath>.html`, listed in a nav on the entity page. `spec.runbook` is always rendered as its own page and linked prominently, whether or not it lives under `docs/`. | A service with twenty documents does not produce one unreadable page. Costs a link rewriter: relative `.md` links between documents must become `.html`. |
| R19 | The whole-system dependency map (spec §10) on a large catalog is an unreadable hairball. | Render it anyway, but **cap it**: above 60 entities the map page renders the edge list as a table instead, with a one-line explanation naming the count and the cap. | Honest degradation, visible in the artifact. Silently rendering a 300-node Mermaid graph that the browser takes 40 seconds to lay out is worse than saying why it is a table. |
| R20 | Spec §10: "the page fetches `runtime.json` client-side and degrades to 'runtime unknown' when it is absent." v1 does not produce that file, so this degrades on **every** page today. | Build it as specified. The badge renders the literal text `runtime unknown` server-side; `assets/runtime.js` replaces it only on a successful fetch. | The whole feature is inert until sub-project E ships. That is the point: E must require no portal change, and the only way to know that is true is to build the consumer now. |
| R21 | Does `build` also write CODEOWNERS and the other `gen` artifacts? Spec §7's stage 8 lists "site / CODEOWNERS / routing / history.csv". | **No.** `build` writes the site into `-o dist`. `gen` writes ownership artifacts into the repository, and `score --history` appends the history file. They already exist and already have their own `--check` gate. | Spec §7's stage-8 line describes the stage, not one command. Merging them would mean `build` writing into the repo *and* into `dist`, and `gen --check` would no longer be the single anti-rot mechanism. |
| R22 | With stage 2 deferred to Plan 4, what should `build` do when `repos.yaml` lists three repositories and it can only read one? | Render the local one, emit a **warning** naming the repositories it did not read, and stamp a banner into every page of the generated site. Not a hard failure: a team with three repos must still be able to preview their own. | Spec §12 says a portal quietly missing three services is worse than rendering nothing, so the omission must be visible in the **artifact**. This is a scaffold with a known lifespan: Plan 4 deletes `partialNotice` and replaces it with real fetching plus `--allow-partial`, whose banner names repos that *actually* failed. |

---

## File Structure

**New packages**

| Path | Responsibility |
|---|---|
| `internal/render/md/md.go` | `New()` — the goldmark value; `ChromaCSS()` — the stylesheet bytes |
| `internal/render/md/code.go` | Fenced-code renderer: Mermaid passthrough, Chroma otherwise, escaped fallback |
| `internal/render/md/admonition.go` | MkDocs `!!! note "Title"` block parser, node and renderer |
| `internal/render/md/doc.go` | `Render(m, source, rewrite) (Doc, error)` — one AST walk producing HTML, title, headings and search text |
| `internal/render/url.go` | `Slug`, `EntityURL`, `TeamURL`, `rootRel` — the URL scheme of R11 and R12 |
| `internal/render/model.go` | View models: `Page`, `EntityView`, `TeamView`, `CatalogRow`, `ScoreView` |
| `internal/render/assets.go` | `//go:embed web` — the template set and the static files |
| `internal/render/site.go` | `Site(Input, *diag.Collector) []emit.File` — the one entry point |
| `internal/render/entity.go` | The entity page, its scorecard block and its docs sub-pages |
| `internal/render/graph.go` | Mermaid source for the neighbourhood graph and the system map |
| `internal/render/team.go` | Team pages |
| `internal/render/scorecard.go` | The scorecard page and the history sparkline |
| `internal/render/history.go` | Parse `scorecard-history.csv` into a trend |
| `internal/render/search.go` | `search-index.json` |
| `internal/render/web/templates/*.html` | `base`, `catalog`, `entity`, `doc`, `team`, `scorecard`, `map` |
| `internal/render/web/static/style.css` | The portal's stylesheet |
| `internal/render/web/static/catalog.js` | Filter and sort on the catalog page |
| `internal/render/web/static/search.js` | The search client |
| `internal/render/web/static/runtime.js` | Fetch `runtime.json`, degrade to "runtime unknown" |
| `cmd/landsraad/build.go` | `landsraad build [-o dist] [--mermaid-src] [--force]` |
| `cmd/landsraad/manifest.go` | Write and prune by `dist/.landsraad-manifest` (R16) |
| `cmd/landsraad/serve.go` | `landsraad serve [--watch] [--addr]` |

**Modified**

| Path | Change |
|---|---|
| `cmd/landsraad/main.go` | registers `newBuildCmd()` and `newServeCmd()` |
| `go.mod` / `go.sum` | three new direct dependencies |
| `Taskfile.yml` | a `site` task that builds the portal from `testdata/monorepo-ok` |
| `README.md` | the portal is real; `build` and `serve` documented |
| `CONTRIBUTING.md` | how to add a page; the golden-file workflow |
| `docs/superpowers/specs/2026-09-08-landsraad-design.md` | §13 amended for R10 |

**Reused unchanged:** `internal/emit` (`File`, and `Diff` for the golden tests), `internal/diag`, `internal/catalog`, `internal/config`, `internal/scorecard`, and `cmd/landsraad/gen.go`'s `loadCatalog`.

---

## Test fixtures shared between tasks

Tests are written in package-internal files, so a helper defined in one task's test file is visible to every later task in the same package. Tasks are sequential and this is deliberate.

| Helper | Defined in | Used by |
|---|---|---|
| `render(t, source)` | Task 1, `internal/render/md/code_test.go` | Tasks 2, 3 |
| `testNow`, `ent(name, kind, owner, tier)`, `input(t, files, entities...)`, `testTeamsYAML` | Task 4, `internal/render/model_test.go` | Tasks 5, 7–13 |
| `withTeams(t, in, yaml)` | Task 10, added to `internal/render/model_test.go` | Task 10 only |
| `golden(t, name, got)`, `-update` | Task 5, `internal/render/golden_test.go` | Tasks 5, 7–13 |
| `siteMap(files)`, `twoEntities(t)`, `keys(m)` | Task 5, `internal/render/site_test.go` | Tasks 7–13 |
| `linked(t)` | Task 8, `internal/render/graph_test.go` | Task 8 only |
| `documented(t)` | Task 9, `internal/render/docs_test.go` | Task 9 only |
| `historyCSV` | Task 11, `internal/render/history_test.go` | Task 11 (both test files) |
| `decodeIndex(t, data)` | Task 12, `internal/render/search_test.go` | Task 12 only |
| `buildFS()`, `buildOpts()`, `buildSiteMap(t, …)`, `buildNow` | Task 6, `cmd/landsraad/build_test.go` | Task 6 only |
| `testServer()`, `get(t, s, path)`, `waitFor(t, ch, d)` | Task 14, `cmd/landsraad/serve_test.go` | Task 14 only |
| `hrefs(page)` | Task 15, `cmd/landsraad/integration_test.go` | Task 15 only |

`noLastEdit()` and `plural(n, one, many)` already exist in `cmd/landsraad/` and are
reused by Tasks 6, 14 and 15. `standardsFor`, `loadCatalog`, `gitLastEdit`,
`findRoot` and `version()` likewise.

`plural(n, one, many)` already exists in `cmd/landsraad/validate.go` and is reused by Tasks 6 and 14.

**The golden-file workflow.** Task 5 establishes it and every later task adds fixtures. Golden files live in `internal/render/testdata/golden/`. `go test ./internal/render/ -update` regenerates them. **Read every diff before committing a regeneration** — the whole value of a golden test is that an unintended change shows up as a diff you had to look at.

---

## Task 1: The Markdown dialect — goldmark, Chroma and Mermaid fences

**Files:**
- Create: `internal/render/md/md.go`
- Create: `internal/render/md/code.go`
- Create: `internal/render/md/code_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nothing from this plan.
- Produces:
  - `func md.New() goldmark.Markdown` — the configured value. **Not** a package global.
  - `func md.ChromaCSS() ([]byte, error)` — the stylesheet for the classes the code renderer emits.
  - `type md.Code struct{}` implementing `goldmark.Extender`.

**Context:** Spec D2 fixes the dialect: "goldmark GFM (tables, footnotes, task lists) + Chroma + Mermaid + one custom extension for MkDocs-style `!!! note`". Spec §14.1 fixes the safety posture: goldmark runs *without* `WithUnsafe`, and Mermaid renders in strict mode.

Two facts were verified against the real libraries before this plan was written, and both change the code:

1. **`extension.GFM` does not include footnotes.** With GFM alone, `Text with a note[^1]` renders as the literal string `[^1]` — a silent drop of a feature spec D2 names. `extension.Footnote` must be listed separately.
2. **`goldmark-highlighting/v2` is not used.** It has no tagged release — only a 2023 pseudo-version — and it gives no clean way for a ` ```mermaid ` fence to bypass Chroma, which spec §10 needs. One custom `FencedCodeBlock` renderer over `chroma/v2` handles both cases and depends only on tagged releases.

A third fact to know while writing this: `text.Segment.Value` has a **pointer** receiver, so `n.Lines().At(i).Value(source)` does not compile. Assign the segment to a variable first.

- [ ] **Step 1: Add the dependencies**

```bash
go get github.com/yuin/goldmark@v1.8.6
# Not v2.27.0: that release declares `go 1.25`, which would raise this
# module's floor past the go-version: '1.23' / golang:1.23 CI snippets the
# README publishes to adopters (Task 15 correction).
go get github.com/alecthomas/chroma/v2@v2.24.1
go mod tidy
```

Expected: `go.mod` gains both as direct requirements, plus `github.com/dlclark/regexp2/v2` as indirect.

- [ ] **Step 2: Write the failing test**

Create `internal/render/md/code_test.go`:

```go
package md

import (
	"bytes"
	"strings"
	"testing"
)

// render is the shared helper for every test in this package.
func render(t *testing.T, source string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := New().Convert([]byte(source), &buf); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return buf.String()
}

func TestMermaidFenceIsPassedThroughToTheBrowser(t *testing.T) {
	got := render(t, "```mermaid\ngraph LR\n  a --> b\n```\n")
	want := "<pre class=\"mermaid\">graph LR\n  a --&gt; b\n</pre>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAKnownLanguageIsHighlightedWithClasses(t *testing.T) {
	got := render(t, "```go\nfunc main() {}\n```\n")
	if !strings.Contains(got, `<pre class="chroma">`) {
		t.Errorf("no chroma wrapper in:\n%s", got)
	}
	if !strings.Contains(got, `class="kd"`) {
		t.Errorf("no keyword class for `func` in:\n%s", got)
	}
	// WithClasses(true): tokens carry classes, never inline styles. The
	// stylesheet ships once as assets/chroma.css (ruling R14).
	if strings.Contains(got, "style=") {
		t.Errorf("inline styles must not appear, chroma is configured WithClasses:\n%s", got)
	}
}

// Ruling R15: an unknown language is not a metadata problem. Chroma has ~250
// lexers and a runbook naming one it lacks must still show its content.
func TestAnUnknownLanguageFallsBackToEscapedPlainText(t *testing.T) {
	got := render(t, "```notalanguage\n<raw & stuff>\n```\n")
	want := "<pre><code>&lt;raw &amp; stuff&gt;\n</code></pre>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAFenceWithNoLanguageIsEscapedPlainText(t *testing.T) {
	got := render(t, "```\njust text\n```\n")
	want := "<pre><code>just text\n</code></pre>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Spec §14.1, decided in advance rather than under pressure: goldmark runs
// WITHOUT WithUnsafe, so raw HTML in somebody's runbook is escaped rather than
// injected into a shared portal page. Deleting this test re-opens the hole.
func TestRawHTMLIsNeverInjected(t *testing.T) {
	got := render(t, "<script>alert(1)</script>\n\nInline <b>html</b> too.\n")
	if strings.Contains(got, "<script>") {
		t.Errorf("raw HTML reached the output — WithUnsafe must never be set:\n%s", got)
	}
	if strings.Contains(got, "<b>html</b>") {
		t.Errorf("raw inline HTML reached the output:\n%s", got)
	}
}

func TestGFMTablesRender(t *testing.T) {
	got := render(t, "| a | b |\n|---|---|\n| 1 | 2 |\n")
	if !strings.Contains(got, "<table>") {
		t.Errorf("GFM tables must render, got:\n%s", got)
	}
}

// Spec D2 names footnotes. extension.GFM does NOT include them: with GFM
// alone this renders the literal text "[^1]", silently dropping the feature.
func TestFootnotesRender(t *testing.T) {
	got := render(t, "Text with a note[^1].\n\n[^1]: The note body.\n")
	if !strings.Contains(got, `class="footnote-ref"`) {
		t.Errorf("footnotes need extension.Footnote alongside extension.GFM, got:\n%s", got)
	}
}

func TestTaskListsRender(t *testing.T) {
	got := render(t, "- [ ] todo\n- [x] done\n")
	if !strings.Contains(got, `type="checkbox"`) {
		t.Errorf("GFM task lists must render, got:\n%s", got)
	}
}

func TestHeadingsGetIDsForDeepLinking(t *testing.T) {
	got := render(t, "## Rollback procedure\n")
	if !strings.Contains(got, `id="rollback-procedure"`) {
		t.Errorf("parser.WithAutoHeadingID must be set, got:\n%s", got)
	}
}

func TestChromaCSSCoversTheClassesTheRendererEmits(t *testing.T) {
	css, err := ChromaCSS()
	if err != nil {
		t.Fatalf("ChromaCSS: %v", err)
	}
	for _, sel := range []string{".chroma", ".chroma .kd"} {
		if !strings.Contains(string(css), sel) {
			t.Errorf("stylesheet is missing %q", sel)
		}
	}
}

// The goldmark value is constructed, never shared. Two portals with two
// configurations must be able to coexist in one process, which is the whole
// reason the project forbids package-level state below cmd/.
func TestNewReturnsAFreshValueEachTime(t *testing.T) {
	if New() == New() {
		t.Error("New must return a fresh value, not a package-level singleton")
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/render/md/ -v`
Expected: FAIL — `no Go files in .../internal/render/md`.

- [ ] **Step 4: Write the fenced-code renderer**

Create `internal/render/md/code.go`:

```go
package md

import (
	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// chromaStyle is the highlighting theme. One name, in one place, so the
// stylesheet ChromaCSS writes and the classes codeRenderer emits cannot drift.
const chromaStyle = "github"

// Code is the fenced-code-block extension.
//
// It exists instead of goldmark-highlighting/v2 for two reasons. That module
// has no tagged release — only a 2023 pseudo-version — and it offers no clean
// way for one language to bypass the highlighter, which is exactly what a
// ```mermaid fence needs: Mermaid source is not code to colour, it is a
// diagram the browser lays out (spec §10).
type Code struct{}

func (Code) Extend(m goldmark.Markdown) {
	// Priority 1 — ahead of goldmark's own fenced-code renderer, which is
	// registered at 100. The lower number wins.
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(newCodeRenderer(), 1),
	))
}

type codeRenderer struct {
	style     *chroma.Style
	formatter *chromahtml.Formatter
}

func newCodeRenderer() *codeRenderer {
	s := styles.Get(chromaStyle)
	if s == nil {
		// Chroma returns nil for an unknown name rather than an error. A typo
		// in the constant above would otherwise produce an uncoloured portal
		// and no complaint; the fallback style is at least legible.
		s = styles.Fallback
	}
	return &codeRenderer{
		style: s,
		// WithClasses: tokens carry class names and the stylesheet ships once
		// as assets/chroma.css, rather than an inline style= on every span
		// (ruling R14).
		formatter: chromahtml.New(chromahtml.WithClasses(true)),
	}
}

func (r *codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFenced)
}

func (r *codeRenderer) renderFenced(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := string(n.Language(source))
	code := fencedLines(n, source)

	// Mermaid: hand the source to the browser, escaped. The client calls
	// mermaid.initialize with securityLevel "strict" (spec §14.1).
	if lang == "mermaid" {
		w.WriteString(`<pre class="mermaid">`)
		w.Write(util.EscapeHTML(code))
		w.WriteString("</pre>\n")
		return ast.WalkSkipChildren, nil
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		return plainCode(w, code)
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, string(code))
	if err != nil {
		// The content is more important than the colours. A lexer that chokes
		// on a fragment must not lose the fragment.
		return plainCode(w, code)
	}
	if err := r.formatter.Format(w, r.style, it); err != nil {
		return ast.WalkStop, err
	}
	return ast.WalkSkipChildren, nil
}

// plainCode is the fallback: escaped, unstyled, still readable (ruling R15).
func plainCode(w util.BufWriter, code []byte) (ast.WalkStatus, error) {
	w.WriteString("<pre><code>")
	w.Write(util.EscapeHTML(code))
	w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// fencedLines joins a block's line segments back into its source bytes.
//
// text.Segment.Value has a POINTER receiver, so the obvious
// `l.At(i).Value(source)` does not compile. The local variable is required.
func fencedLines(n ast.Node, source []byte) []byte {
	var out []byte
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		out = append(out, seg.Value(source)...)
	}
	return out
}
```

- [ ] **Step 5: Write the pipeline**

Create `internal/render/md/md.go`:

```go
// Package md is landsraad's Markdown dialect.
//
// Spec D2 fixes it: goldmark GFM — tables, footnotes, task lists — plus
// Chroma highlighting, Mermaid diagrams, and one custom extension for
// MkDocs-style `!!! note` admonitions. Tabs and snippet-includes are
// deliberately excluded: they break GitHub's rendering, and these files are
// read in the repository as well as in the portal.
//
// Spec §14.1 fixes the safety posture, decided in advance rather than under
// pressure: goldmark runs WITHOUT WithUnsafe, so raw HTML in a runbook is
// escaped rather than injected into a shared portal page.
package md

import (
	"bytes"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

// New returns a configured Markdown renderer.
//
// It is a value, not package state. Two portals with two dialects can coexist
// in one process, and a test can build its own — the same reason
// schema.Validator is a value and not a sync.Once global (spec §3.1).
func New() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			// Footnote is NOT part of extension.GFM. Without this line,
			// `note[^1]` renders as the literal text "[^1]" — a feature spec
			// D2 names, dropped in silence.
			extension.Footnote,
			Code{},
		),
		// Heading IDs make a runbook section deep-linkable, which is what an
		// incident channel actually pastes.
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		// Note what is absent: html.WithUnsafe. See the package comment.
	)
}

// ChromaCSS is the stylesheet for the classes the fenced-code renderer emits.
// The site writes it once as assets/chroma.css (ruling R14).
func ChromaCSS() ([]byte, error) {
	s := styles.Get(chromaStyle)
	if s == nil {
		s = styles.Fallback
	}
	var buf bytes.Buffer
	if err := chromahtml.New(chromahtml.WithClasses(true)).WriteCSS(&buf, s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/render/md/ -v`
Expected: PASS, all eleven tests.

If `TestMermaidFenceIsPassedThroughToTheBrowser` fails with goldmark's default `<pre><code class="language-mermaid">`, the renderer priority is wrong: `util.Prioritized(newCodeRenderer(), 1)` must be a **lower** number than goldmark's built-in 100.

- [ ] **Step 7: Verify the rule checks still pass**

Run: `sh scripts/check-rules.sh && go vet ./... && gofmt -l .`
Expected: no output, exit 0.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/render/md/md.go internal/render/md/code.go internal/render/md/code_test.go
git commit -m "feat: the Markdown dialect — goldmark, Chroma classes and Mermaid fences

goldmark runs without WithUnsafe (spec §14.1) and extension.Footnote is
listed explicitly: extension.GFM does not include it, and without it a
footnote renders as literal text.

Chroma is driven directly rather than through goldmark-highlighting/v2,
which has no tagged release and offers no way for a mermaid fence to
bypass the highlighter."
```

---

## Task 2: The admonition extension

**Files:**
- Create: `internal/render/md/admonition.go`
- Create: `internal/render/md/admonition_test.go`
- Modify: `internal/render/md/md.go` (add `Admonitions{}` to the extension list)

**Interfaces:**
- Consumes: `md.New()` from Task 1, and the `render(t, source)` helper in `code_test.go`.
- Produces:
  - `type md.Admonitions struct{}` implementing `goldmark.Extender`
  - `type md.Admonition struct { ast.BaseBlock; Class, Title string }`
  - `var md.KindAdmonition ast.NodeKind`

**Context:** This is spec D2's "one custom extension". MkDocs-material syntax:

```
!!! warning "Page the owner first"

    Do **not** restart before checking the lag.
```

The content is indented four spaces and may hold any block content — paragraphs, lists, code. The title is optional; without it the class name is the title, which is what MkDocs does.

**The security detail that decides the parser's shape:** the class name goes straight into a `class=` attribute. The opening regex therefore matches `[a-z]+` and nothing else, so there is no input that reaches the attribute unsanitised. Do not relax it to `\S+` — an admonition type of `x" onload="alert(1)` would then be a stored XSS in a shared internal portal, and spec §14.1's trust boundary ("service repositories are trusted") is about *accidents*, not about leaving an obvious hole open.

- [ ] **Step 1: Write the failing test**

Create `internal/render/md/admonition_test.go`:

```go
package md

import (
	"strings"
	"testing"
)

func TestAdmonitionWithATitle(t *testing.T) {
	got := render(t, "!!! warning \"Page the owner first\"\n\n    Do **not** restart.\n")
	want := "<div class=\"admonition warning\">" +
		"<p class=\"admonition-title\">Page the owner first</p>\n" +
		"<p>Do <strong>not</strong> restart.</p>\n" +
		"</div>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// MkDocs uses the type as the title when none is given.
func TestAdmonitionWithoutATitleUsesItsClass(t *testing.T) {
	got := render(t, "!!! note\n\n    Body text.\n")
	want := "<div class=\"admonition note\">" +
		"<p class=\"admonition-title\">note</p>\n" +
		"<p>Body text.</p>\n" +
		"</div>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAdmonitionHoldsMultipleBlocks(t *testing.T) {
	got := render(t, "!!! danger\n\n    First para.\n\n    Second para.\n\n    - a list item\n")
	for _, want := range []string{"<p>First para.</p>", "<p>Second para.</p>", "<li>a list item</li>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestAdmonitionClosesAtTheFirstUnindentedLine(t *testing.T) {
	got := render(t, "!!! note\n\n    Inside.\n\nOutside.\n")
	if !strings.Contains(got, "</div>\n<p>Outside.</p>") {
		t.Errorf("the unindented paragraph must fall outside the admonition:\n%s", got)
	}
}

// The class reaches a class= attribute, so the opener matches [a-z]+ and
// nothing else. Anything else is not an admonition and stays a paragraph.
func TestAnUnsafeTypeIsNotAnAdmonition(t *testing.T) {
	got := render(t, "!!! x\" onload=\"alert(1)\n\n    body\n")
	if strings.Contains(got, "onload") && strings.Contains(got, "class=\"admonition") {
		t.Errorf("an unsanitised class reached the output:\n%s", got)
	}
	if !strings.Contains(got, "<p>") {
		t.Errorf("a non-matching opener stays ordinary text, got:\n%s", got)
	}
}

func TestAdmonitionTitleIsEscaped(t *testing.T) {
	got := render(t, "!!! note \"A <b>bold</b> title\"\n\n    body\n")
	if strings.Contains(got, "<b>bold</b>") {
		t.Errorf("the title must be escaped:\n%s", got)
	}
	if !strings.Contains(got, "&lt;b&gt;bold&lt;/b&gt;") {
		t.Errorf("expected an escaped title, got:\n%s", got)
	}
}

func TestThreeBangsAloneAreNotAnAdmonition(t *testing.T) {
	got := render(t, "!!! \n\nplain\n")
	if strings.Contains(got, "admonition") {
		t.Errorf("an opener with no type is not an admonition:\n%s", got)
	}
}

// A paragraph must be interruptible: MkDocs authors do not reliably leave a
// blank line before the opener.
func TestAdmonitionInterruptsAParagraph(t *testing.T) {
	got := render(t, "Some text.\n!!! note\n\n    Inside.\n")
	if !strings.Contains(got, `<div class="admonition note">`) {
		t.Errorf("the opener must interrupt a paragraph:\n%s", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/render/md/ -run TestAdmonition -v`
Expected: FAIL — `undefined: Admonitions` is not the error yet; these fail on output, because `!!! note` currently renders as an ordinary paragraph.

- [ ] **Step 3: Write the extension**

Create `internal/render/md/admonition.go`:

```go
package md

import (
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// KindAdmonition identifies the node type this extension adds.
var KindAdmonition = ast.NewNodeKind("Admonition")

// Admonition is one MkDocs-style callout: `!!! warning "Title"` followed by
// four-space-indented block content.
type Admonition struct {
	ast.BaseBlock
	// Class is the admonition type. It is matched as [a-z]+ and nothing
	// else, because it is written directly into a class= attribute.
	Class string
	Title string
}

func (n *Admonition) Kind() ast.NodeKind { return KindAdmonition }

func (n *Admonition) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{
		"Class": n.Class, "Title": n.Title,
	}, nil)
}

// openRE matches `!!! note` and `!!! note "A title"`.
//
// [a-z]+ is a security boundary, not a convenience: Class reaches a class=
// attribute, and a permissive pattern would make `!!! x" onload="…` a stored
// XSS on a shared internal portal. Widening this needs escaping at the
// renderer instead — do not widen it without adding that.
var openRE = regexp.MustCompile(`^!!!\s+([a-z]+)\s*(?:"([^"]*)")?\s*$`)

type admonitionParser struct{}

func (admonitionParser) Trigger() []byte { return []byte{'!'} }

func (admonitionParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	m := openRE.FindSubmatch(util.TrimRightSpace(line))
	if m == nil {
		return nil, parser.NoChildren
	}
	reader.AdvanceLine()
	return &Admonition{Class: string(m[1]), Title: string(m[2])}, parser.HasChildren
}

func (admonitionParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, _ := reader.PeekLine()
	// A blank line does not close the block: MkDocs admonitions routinely
	// hold several paragraphs, and the opener is followed by one by
	// convention.
	if util.IsBlank(line) {
		return parser.Continue | parser.HasChildren
	}
	pos, padding := util.IndentPosition(line, reader.LineOffset(), 4)
	if pos < 0 {
		return parser.Close
	}
	reader.AdvanceAndSetPadding(pos, padding)
	return parser.Continue | parser.HasChildren
}

func (admonitionParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

// CanInterruptParagraph: authors do not reliably leave a blank line before
// the opener, and silently rendering `!!! warning` as body text is the kind
// of quiet wrong answer this project exists to avoid.
func (admonitionParser) CanInterruptParagraph() bool { return true }

// CanAcceptIndentedLine: false — an indented line belongs to the enclosing
// block, not to a new admonition.
func (admonitionParser) CanAcceptIndentedLine() bool { return false }

type admonitionRenderer struct{}

func (r admonitionRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindAdmonition, r.render)
}

func (admonitionRenderer) render(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	a := n.(*Admonition)
	if !entering {
		w.WriteString("</div>\n")
		return ast.WalkContinue, nil
	}
	w.WriteString(`<div class="admonition `)
	// Class is [a-z]+ by construction; escaping it would be theatre. The
	// title is arbitrary text and is escaped.
	w.WriteString(a.Class)
	w.WriteString(`">`)
	title := a.Title
	if title == "" {
		title = a.Class
	}
	w.WriteString(`<p class="admonition-title">`)
	w.Write(util.EscapeHTML([]byte(title)))
	w.WriteString("</p>\n")
	return ast.WalkContinue, nil
}

// Admonitions is spec D2's "one custom extension for MkDocs-style `!!! note`".
//
// It exists because runbooks genuinely use callouts, and because the syntax
// keeps these files paste-compatible with documentation written for MkDocs —
// which matters when the same file is read in the repository and in the
// portal.
type Admonitions struct{}

func (Admonitions) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(
		// 799 — ahead of goldmark's paragraph parser so the opener can
		// interrupt a paragraph, behind the fenced-code and list parsers so
		// `!!!` inside a code block stays literal.
		util.Prioritized(admonitionParser{}, 799),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(admonitionRenderer{}, 500),
	))
}
```

- [ ] **Step 4: Register the extension**

In `internal/render/md/md.go`, add `Admonitions{}` to the extension list:

```go
		goldmark.WithExtensions(
			extension.GFM,
			// Footnote is NOT part of extension.GFM. Without this line,
			// `note[^1]` renders as the literal text "[^1]" — a feature spec
			// D2 names, dropped in silence.
			extension.Footnote,
			Admonitions{},
			Code{},
		),
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/render/md/ -v`
Expected: PASS — the eight new tests and the eleven from Task 1.

- [ ] **Step 6: Commit**

```bash
git add internal/render/md/admonition.go internal/render/md/admonition_test.go internal/render/md/md.go
git commit -m "feat: MkDocs-style admonitions, spec D2's one custom extension

The opener matches [a-z]+ for the type because that string is written
into a class= attribute; a permissive pattern would make the syntax a
stored XSS on a shared portal. The title is arbitrary and is escaped."
```

---

## Task 3: One AST walk — headings, search text and `.md` → `.html` links

**Files:**
- Create: `internal/render/md/doc.go`
- Create: `internal/render/md/doc_test.go`

**Interfaces:**
- Consumes: `md.New()` from Task 1.
- Produces:
  - `type md.Doc struct { HTML []byte; Title string; Headings []Heading; Text string }`
  - `type md.Heading struct { Level int; Text, ID string }`
  - `type md.LinkRewriter func(dest string) string`
  - `func md.Render(m goldmark.Markdown, source []byte, rewrite LinkRewriter) (Doc, error)`

**Context:** Three consumers need three different things out of the same document, and all three are answerable from one AST walk:

- The **page** needs HTML, and its `<h1>` as the page title.
- The **search index** (Task 12) needs the headings and a bounded slice of body text.
- The **docs sub-pages** (Task 9) need relative `.md` links rewritten to `.html`, or a link between two runbooks 404s in the portal while working fine on GitHub.

Ruling R17 caps the search text at 2000 runes. Truncation is on a **rune** boundary: cutting a multi-byte character in half produces invalid UTF-8, which `encoding/json` then escapes into `�` — a corrupt index for any team that writes documentation in a language with accents.

**Do not reach for `ast.Node.Text(source)`.** It is the obvious API and it is wrong twice over. goldmark marks it *Deprecated* — "Use other properties of the node (i.e. Paragraph.Lines, Text.Value)" — and on a block node it returns the **raw source span**, so a paragraph containing `See [the runbook](runbook.md)` yields the literal string `See [the runbook](runbook.md)`. The search index would then match on URLs and bracket syntax, and every result snippet would show markup. Walk the `*ast.Text` and `*ast.String` leaves instead: that yields `See the runbook`, and it picks up list items and table cells for free while leaving code blocks out — a fenced block is not `ast.Text`, and indexing code would swamp prose matches.

The leaf walk emits one fragment per inline node, so `Payments **worker**` arrives as `"Payments "` + `"worker"`. Normalise whitespace once at the end with `strings.Fields`; doing it per fragment cannot tell a real space from a node boundary.

- [ ] **Step 1: Write the failing test**

Create `internal/render/md/doc_test.go`:

```go
package md

import (
	"strings"
	"testing"
)

func TestRenderReturnsTheFirstH1AsTheTitle(t *testing.T) {
	d, err := Render(New(), []byte("# Payments worker\n\nBody.\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d.Title != "Payments worker" {
		t.Errorf("Title = %q, want %q", d.Title, "Payments worker")
	}
}

// A document with no H1 has no title. The caller decides what to show
// instead — inventing one here would be a silent fallback.
func TestRenderReportsNoTitleWhenThereIsNoH1(t *testing.T) {
	d, err := Render(New(), []byte("## Only an H2\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d.Title != "" {
		t.Errorf("Title = %q, want the empty string", d.Title)
	}
}

func TestRenderCollectsHeadingsWithTheirIDs(t *testing.T) {
	src := "# Runbook\n\n## Rollback procedure\n\n### Step one\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []Heading{
		{Level: 1, Text: "Runbook", ID: "runbook"},
		{Level: 2, Text: "Rollback procedure", ID: "rollback-procedure"},
		{Level: 3, Text: "Step one", ID: "step-one"},
	}
	if len(d.Headings) != len(want) {
		t.Fatalf("got %d headings, want %d: %+v", len(d.Headings), len(want), d.Headings)
	}
	for i := range want {
		if d.Headings[i] != want[i] {
			t.Errorf("heading %d = %+v, want %+v", i, d.Headings[i], want[i])
		}
	}
}

func TestRenderCollectsSearchText(t *testing.T) {
	d, err := Render(New(), []byte("# Title\n\nFirst para.\n\nSecond para.\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Title", "First para.", "Second para."} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("search text is missing %q, got %q", want, d.Text)
		}
	}
}

// ast.Node.Text(source) returns the RAW SOURCE SPAN for a block node — and
// is deprecated besides. Using it here would put "[the runbook](runbook.md)"
// into the index, so every search would match on URLs and bracket syntax.
func TestSearchTextHasNoMarkupAndNoURLs(t *testing.T) {
	src := "# Payments **worker**\n\nSee [the runbook](runbook.md) and `code`.\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, unwanted := range []string{"[", "]", "(", "runbook.md", "**"} {
		if strings.Contains(d.Text, unwanted) {
			t.Errorf("search text contains markup %q: %q", unwanted, d.Text)
		}
	}
	if !strings.Contains(d.Text, "the runbook") {
		t.Errorf("link text must be indexed, got %q", d.Text)
	}
}

// List items and table cells come free with the leaf walk. Fenced code does
// not: a code block is not ast.Text, and indexing it would swamp prose.
func TestSearchTextCoversListsAndTablesButNotCode(t *testing.T) {
	src := "- list item\n\n| a | b |\n|---|---|\n| cell1 | cell2 |\n\n```go\nfunc secretHelper() {}\n```\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"list item", "cell1", "cell2"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("search text is missing %q, got %q", want, d.Text)
		}
	}
	if strings.Contains(d.Text, "secretHelper") {
		t.Errorf("fenced code must not be indexed, got %q", d.Text)
	}
}

// The leaf walk emits one fragment per inline node, so `Payments **worker**`
// arrives as "Payments " + "worker". Whitespace is normalised once at the
// end; per-fragment normalisation cannot tell a real space from a boundary.
func TestSearchTextHasNoDoubledSpaces(t *testing.T) {
	d, err := Render(New(), []byte("# Payments **worker**\n\nFirst **bold** para.\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(d.Text, "  ") {
		t.Errorf("search text has a doubled space: %q", d.Text)
	}
}

// Emphasis in a heading must not leak into the title shown on the page.
func TestTitleStripsInlineMarkup(t *testing.T) {
	d, err := Render(New(), []byte("# Payments **worker**\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d.Title != "Payments worker" {
		t.Errorf("Title = %q, want %q", d.Title, "Payments worker")
	}
}

// Ruling R17. A forty-service monorepo with long runbooks would otherwise
// ship a multi-megabyte index to every visitor.
func TestSearchTextIsCappedAtTwoThousandRunes(t *testing.T) {
	src := "# T\n\n" + strings.Repeat("word ", 2000)
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if n := len([]rune(d.Text)); n > 2000 {
		t.Errorf("search text is %d runes, want at most 2000", n)
	}
}

// Truncating mid-character produces invalid UTF-8, which encoding/json turns
// into U+FFFD — a corrupt index for anyone writing docs with accents.
func TestSearchTextTruncatesOnARuneBoundary(t *testing.T) {
	src := "# T\n\n" + strings.Repeat("é", 3000)
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.ContainsRune(d.Text, '�') {
		t.Error("search text was cut mid-character")
	}
}

func TestRenderRewritesRelativeMarkdownLinks(t *testing.T) {
	rewrite := func(dest string) string {
		if strings.HasSuffix(dest, ".md") {
			return strings.TrimSuffix(dest, ".md") + ".html"
		}
		return dest
	}
	d, err := Render(New(), []byte("See [the runbook](runbook.md) and [ops](../ops/index.md).\n"), rewrite)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(d.HTML), `href="runbook.html"`) {
		t.Errorf("sibling link not rewritten:\n%s", d.HTML)
	}
	if !strings.Contains(string(d.HTML), `href="../ops/index.html"`) {
		t.Errorf("parent-relative link not rewritten:\n%s", d.HTML)
	}
}

func TestRenderLeavesAbsoluteLinksAlone(t *testing.T) {
	rewrite := func(dest string) string { return dest + "?rewritten" }
	d, err := Render(New(), []byte("[grafana](https://grafana/d/pay)\n"), rewrite)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(d.HTML), "?rewritten") {
		t.Errorf("an absolute URL must not be handed to the rewriter:\n%s", d.HTML)
	}
}

func TestRenderLeavesFragmentLinksAlone(t *testing.T) {
	rewrite := func(dest string) string { return "REWRITTEN" }
	d, err := Render(New(), []byte("[jump](#rollback)\n"), rewrite)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(d.HTML), `href="#rollback"`) {
		t.Errorf("an in-page anchor must not be rewritten:\n%s", d.HTML)
	}
}

// A nil rewriter is the ordinary case for a runbook rendered on its own.
func TestRenderAcceptsANilRewriter(t *testing.T) {
	d, err := Render(New(), []byte("[x](y.md)\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(d.HTML), `href="y.md"`) {
		t.Errorf("a nil rewriter leaves links untouched:\n%s", d.HTML)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/render/md/ -run 'TestRender|TestSearchText|TestTitle' -v`
Expected: FAIL — `undefined: Render`.

- [ ] **Step 3: Write the implementation**

Create `internal/render/md/doc.go`:

```go
package md

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// searchTextLimit caps the body text kept per document for the search index
// (ruling R17). Search over a static site is a client-side substring match
// over a JSON file every visitor downloads; a full-text index is a server
// component, which spec §15 rules out.
const searchTextLimit = 2000

// Heading is one heading in a document, with the id goldmark assigned it so
// the search results and the table of contents can deep-link.
type Heading struct {
	Level int
	Text  string
	ID    string
}

// Doc is one rendered Markdown file, in the three shapes its three consumers
// need — all produced from a single AST walk.
type Doc struct {
	HTML []byte
	// Title is the first H1, or "" when the document has none. The caller
	// decides what to show instead; inventing one here would be a silent
	// fallback.
	Title    string
	Headings []Heading
	// Text is headings and paragraph text for the search index, capped at
	// searchTextLimit runes.
	Text string
}

// LinkRewriter maps a relative link destination to where it lives in the
// generated site. Only relative destinations are passed to it.
type LinkRewriter func(dest string) string

// Render converts source to HTML, collecting the title, the headings and the
// search text on the way, and rewriting relative links through rewrite.
//
// One walk rather than three passes: the AST is already built, and the
// alternative is three traversals that can disagree about what the document
// contains.
func Render(m goldmark.Markdown, source []byte, rewrite LinkRewriter) (Doc, error) {
	reader := text.NewReader(source)
	root := m.Parser().Parse(reader)

	var d Doc
	var textBuf strings.Builder

	err := ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Heading:
			h := Heading{Level: node.Level, Text: leafText(node, source)}
			if id, ok := node.AttributeString("id"); ok {
				if b, ok := id.([]byte); ok {
					h.ID = string(b)
				}
			}
			d.Headings = append(d.Headings, h)
			if node.Level == 1 && d.Title == "" {
				d.Title = h.Text
			}
		case *ast.Text:
			// Every inline text leaf, in document order: headings,
			// paragraphs, list items, table cells. NOT fenced code, which is
			// not an ast.Text and would swamp prose matches.
			seg := node.Segment
			appendText(&textBuf, string(seg.Value(source)))
		case *ast.String:
			// Some extensions synthesise text that has no source segment.
			appendText(&textBuf, string(node.Value))
		case *ast.Link:
			if rewrite != nil {
				node.Destination = []byte(rewriteRelative(string(node.Destination), rewrite))
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return Doc{}, err
	}

	var out bytes.Buffer
	if err := m.Renderer().Render(&out, source, root); err != nil {
		return Doc{}, err
	}
	d.HTML = out.Bytes()
	// Normalise whitespace once, at the end. The walk emits one fragment per
	// inline node, so `Payments **worker**` arrives as "Payments " +
	// "worker"; a per-fragment rule cannot tell a real space from a node
	// boundary. Fields collapses both without having to.
	d.Text = truncateRunes(strings.Join(strings.Fields(textBuf.String()), " "), searchTextLimit)
	return d, nil
}

// leafText concatenates the inline text under n with its markup removed, so
// a heading reads "Payments worker" rather than "Payments **worker**".
//
// This is what ast.Node.Text(source) looks like it does. It is not: goldmark
// deprecates that method, and on a block node it returns the raw source span
// — including link syntax and URLs.
func leafText(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			seg := t.Segment
			b.Write(seg.Value(source))
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

// rewriteRelative hands only repository-relative destinations to the
// rewriter. An absolute URL points outside the site and a fragment points
// inside the current page; rewriting either would break a working link.
func rewriteRelative(dest string, rewrite LinkRewriter) string {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "/") {
		return dest
	}
	// A scheme means it is not ours: https:, mailto:, slack:.
	if i := strings.Index(dest, ":"); i > 0 && !strings.ContainsAny(dest[:i], "/.") {
		return dest
	}
	return rewrite(dest)
}

// appendText accumulates search text, stopping once the cap is reached so a
// 400 KB runbook does not build a 400 KB string to throw away.
//
// The separator keeps two adjacent blocks from running together ("itema").
// Where the fragments already had a space it produces two, which the
// strings.Fields pass in Render collapses.
func appendText(b *strings.Builder, s string) {
	if s == "" || b.Len() > searchTextLimit*4 {
		return
	}
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(s)
}

// truncateRunes cuts to at most n runes, never mid-character. Cutting inside
// a multi-byte rune yields invalid UTF-8, which encoding/json escapes to
// U+FFFD — a corrupt index for documentation written with accents.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/render/md/ -v`
Expected: PASS — fourteen new tests plus the nineteen from Tasks 1 and 2.

If `TestRenderCollectsHeadingsWithTheirIDs` returns empty `ID` fields, `parser.WithAutoHeadingID()` is missing from `New()` in Task 1.

If `TestSearchTextHasNoMarkupAndNoURLs` fails, the walk is using `node.Text(source)` somewhere instead of `leafText`.

- [ ] **Step 5: Commit**

```bash
git add internal/render/md/doc.go internal/render/md/doc_test.go
git commit -m "feat: one AST walk yields HTML, headings, search text and rewritten links

The page, the search index and the docs sub-pages need three different
things from the same document. Three passes could disagree about what the
document contains; one walk cannot.

Search text is capped at 2000 runes and truncated on a rune boundary:
cutting mid-character yields invalid UTF-8, which encoding/json turns
into U+FFFD."
```

---

## Task 4: The URL scheme and the view models

**Files:**
- Create: `internal/render/url.go`
- Create: `internal/render/url_test.go`
- Create: `internal/render/model.go`
- Create: `internal/render/model_test.go`

**Interfaces:**
- Consumes: `catalog.Ref`, `catalog.Catalog`, `config.Teams`, `scorecard.Scorecard`, `config.Standards`.
- Produces:
  - `func render.EntityURL(r catalog.Ref) string` — `"entity/service/payments-worker/"`
  - `func render.EntityPath(r catalog.Ref) string` — `…+ "index.html"`
  - `func render.TeamURL(slug string) string`, `func render.TeamPath(slug string) string`
  - `func render.Slug(s string) (string, bool)`
  - `func render.TeamSlugs(t *config.Teams, c *diag.Collector) map[string]string`
  - `func render.rootRel(outputPath string) string`
  - `type render.Input struct{…}`, `type render.Page struct{…}`, `type render.Mermaid struct{…}`
  - `type render.CatalogRow struct{…}`, `type render.CatalogPage struct{…}`
  - `func render.newPage(in Input, outputPath, title, nav string) Page`

**Context:** Ruling R11 keys every page on the ref, and R12 makes every link relative so the site works from a subdirectory.

**The asymmetry that decides `Slug`'s signature.** Entity names are constrained by the JSON Schema to `^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$`, at most 63 characters — already a legal URL path segment, so `EntityURL` uses the name verbatim. **Team names have no schema at all**: `config.LoadTeams` rejects unknown *keys* but never checks the *value* of `name`. A team called `Payments Team!` must therefore be slugged, and two teams — `payments-team` and `Payments Team` — can slug to the same path and silently overwrite each other's page. `Slug` returns `ok=false` rather than inventing a fallback, and `TeamSlugs` reports the collision as an error. Both are exactly the "silent corruption instead of a hard failure" case the project forbids.

`Score` on a row is a `*float64`, not a `float64`. Plan 2's ruling R1 says an entity with no tier is **not scored**; rendering that as `0%` would put a scarlet letter on every library in the catalog for a check nobody ran.

- [ ] **Step 1: Write the failing test for the URL scheme**

Create `internal/render/url_test.go`:

```go
package render

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestEntityURLIsKeyedOnTheRef(t *testing.T) {
	// service:orders and topic:orders may coexist (spec §12), so the kind is
	// part of the path and not a disambiguating suffix.
	svc := EntityURL(catalog.Ref{Kind: catalog.KindService, Name: "orders"})
	top := EntityURL(catalog.Ref{Kind: catalog.KindTopic, Name: "orders"})
	if svc != "entity/service/orders/" {
		t.Errorf("EntityURL(service:orders) = %q, want %q", svc, "entity/service/orders/")
	}
	if top != "entity/topic/orders/" {
		t.Errorf("EntityURL(topic:orders) = %q, want %q", top, "entity/topic/orders/")
	}
	if svc == top {
		t.Error("two entities sharing a name must not share a URL")
	}
}

func TestEntityPathIsTheDirectoryIndex(t *testing.T) {
	got := EntityPath(catalog.Ref{Kind: catalog.KindWorker, Name: "payments.events"})
	if got != "entity/worker/payments.events/index.html" {
		t.Errorf("EntityPath = %q", got)
	}
}

// A directory-per-page means every URL ends in "/" and needs no rewrite rule
// from whatever static host the team puts this behind (ruling R11).
func TestEveryEntityURLEndsInASlash(t *testing.T) {
	for _, k := range catalog.AllKinds() {
		u := EntityURL(catalog.Ref{Kind: k, Name: "x"})
		if u[len(u)-1] != '/' {
			t.Errorf("EntityURL for %s = %q, want a trailing slash", k, u)
		}
	}
}

func TestRootRelCountsDepth(t *testing.T) {
	cases := []struct{ path, want string }{
		{"index.html", ""},
		{"search-index.json", ""},
		{"scorecard/index.html", "../"},
		{"entity/service/api/index.html", "../../../"},
		{"entity/service/api/docs/runbook.html", "../../../../"},
	}
	for _, c := range cases {
		if got := rootRel(c.path); got != c.want {
			t.Errorf("rootRel(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestSlugMakesALegalPathSegment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"team-payments", "team-payments"},
		{"Payments Team", "payments-team"},
		{"platform/infra", "platform-infra"},
		{"  spaced  out  ", "spaced-out"},
		{"Ünïcødé", "n-c-d"},
	}
	for _, c := range cases {
		got, ok := Slug(c.in)
		if !ok {
			t.Errorf("Slug(%q) reported failure", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// No silent fallback: a name with nothing sluggable in it produces no URL,
// and the caller reports it. Inventing "team-1" would put a page on the site
// that nobody can find from the name they know.
func TestSlugReportsFailureRatherThanInventingAName(t *testing.T) {
	for _, in := range []string{"", "###", "   "} {
		if got, ok := Slug(in); ok {
			t.Errorf("Slug(%q) = %q, ok — want a reported failure", in, got)
		}
	}
}

func TestTeamSlugsReportsACollision(t *testing.T) {
	var c diag.Collector
	teams := config.LoadTeams("teams.yaml", []byte(
		"teams:\n"+
			"  - name: payments-team\n"+
			"    members: [alice]\n"+
			"  - name: Payments Team\n"+
			"    members: [bob]\n"), &c)

	slugs := TeamSlugs(teams, &c)

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	if ds[0].Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError", ds[0].Severity)
	}
	// config.Teams.Names() sorts, and "Payments Team" (capital P, 0x50)
	// sorts before "payments-team" (0x70) — so it is the first claimant and
	// the one the message names first. The order is arbitrary from the
	// user's point of view but it is DETERMINISTIC, which is what a
	// diagnostic asserted by an exact string needs.
	want := `teams "Payments Team" and "payments-team" both produce the page team/payments-team/`
	if ds[0].Message != want {
		t.Errorf("Message = %q, want %q", ds[0].Message, want)
	}
	wantHint := "rename one of them; a team's page URL is derived from its name"
	if ds[0].Hint != wantHint {
		t.Errorf("Hint = %q, want %q", ds[0].Hint, wantHint)
	}
	if ds[0].File != "teams.yaml" {
		t.Errorf("File = %q, want %q", ds[0].File, "teams.yaml")
	}
	// The first claimant keeps the slug so the rest of the render still has
	// somewhere to link; the diagnostic is what stops the build.
	if slugs["Payments Team"] != "payments-team" {
		t.Errorf("first claimant lost its slug: %v", slugs)
	}
	if _, ok := slugs["payments-team"]; ok {
		t.Errorf("the losing team must get no slug: %v", slugs)
	}
}

func TestTeamSlugsReportsAnUnsluggableName(t *testing.T) {
	var c diag.Collector
	teams := config.LoadTeams("teams.yaml", []byte(
		"teams:\n  - name: \"###\"\n    members: [alice]\n"), &c)

	if slugs := TeamSlugs(teams, &c); len(slugs) != 0 {
		t.Errorf("got slugs %v, want none", slugs)
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	want := `team "###" has no usable page URL: its name contains no letters or digits`
	if ds[0].Message != want {
		t.Errorf("Message = %q, want %q", ds[0].Message, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/render/ -v`
Expected: FAIL — `no Go files in .../internal/render`.

- [ ] **Step 3: Write the URL scheme**

Create `internal/render/url.go`:

```go
package render

import (
	"fmt"
	"strings"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// EntityURL is the site-relative directory for an entity's page.
//
// Keyed on the ref, never the bare name (spec §12): service:orders and
// topic:orders are distinct entities that may coexist, and a portal that
// gave them one URL would show one team's service under the other's name.
//
// The name needs no escaping. The schema constrains it to
// ^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$ at 63 characters, which is already a
// legal path segment — no slash, no space, no uppercase. Team names have no
// such constraint, which is why they go through Slug and entity names do not.
func EntityURL(r catalog.Ref) string {
	return "entity/" + strings.ToLower(string(r.Kind)) + "/" + r.Name + "/"
}

// EntityPath is the file EntityURL resolves to.
func EntityPath(r catalog.Ref) string { return EntityURL(r) + "index.html" }

// TeamURL is the site-relative directory for a team's page.
func TeamURL(slug string) string { return "team/" + slug + "/" }

// TeamPath is the file TeamURL resolves to.
func TeamPath(slug string) string { return TeamURL(slug) + "index.html" }

// Slug maps an arbitrary string to a URL path segment, reporting ok=false
// when nothing usable survives.
//
// It does not invent a fallback. A team called "###" getting the page
// "team/team-1/" would be a URL nobody can guess from the name they know,
// and the tool would never mention it — the silent-corruption shape this
// project forbids. The caller reports it instead.
func Slug(s string) (string, bool) {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	return out, out != ""
}

// TeamSlugs maps every team name to its page slug, reporting names that
// cannot produce one and names that collide.
//
// The collision matters because two teams sharing a slug share a file, and
// the second write silently replaces the first: one team's members, on-call
// and owned services vanish from the portal with nothing to notice it by.
//
// Which of the two keeps the slug is decided by config.Teams.Names(), which
// sorts. That is arbitrary from the user's point of view and it does not
// matter — the collision is an error and the build stops. What matters is
// that it is deterministic, so the diagnostic reads the same on every run.
func TeamSlugs(t *config.Teams, c *diag.Collector) map[string]string {
	out := map[string]string{}
	seen := map[string]string{} // slug -> the name that claimed it
	for _, name := range t.Names() {
		slug, ok := Slug(name)
		if !ok {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "teams.yaml", Line: 1,
				Entity:  name,
				Check:   "team-url",
				Message: fmt.Sprintf("team %q has no usable page URL: its name contains no letters or digits", name),
				Hint:    "give the team a name with at least one letter or digit",
			})
			continue
		}
		if first, dup := seen[slug]; dup {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "teams.yaml", Line: 1,
				Entity: name,
				Check:  "team-url",
				Message: fmt.Sprintf("teams %q and %q both produce the page %s",
					first, name, TeamURL(slug)),
				Hint: "rename one of them; a team's page URL is derived from its name",
			})
			// The first claimant keeps the slug so every other page still has
			// somewhere to link. The diagnostic is what stops the build.
			continue
		}
		seen[slug] = name
		out[name] = slug
	}
	return out
}

// rootRel is the relative path from a generated file back to the site root
// (ruling R12).
//
// Every href in every template is prefixed with it, so the portal works
// unchanged at https://internal/ and at https://internal/portal/ — which is
// how most teams will actually host it. An absolute "/assets/style.css"
// would 404 in the second case, on every page, with no error anywhere.
func rootRel(outputPath string) string {
	depth := strings.Count(outputPath, "/")
	if depth == 0 {
		return ""
	}
	return strings.Repeat("../", depth)
}
```

- [ ] **Step 4: Run the URL tests**

Run: `go test ./internal/render/ -run 'TestEntity|TestRootRel|TestSlug|TestTeamSlugs|TestEvery' -v`
Expected: PASS, eight tests.

- [ ] **Step 5: Write the failing test for the view models**

Create `internal/render/model_test.go`:

```go
package render

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// testNow is the frozen clock every render test uses. Nothing below cmd/
// reads the clock, so a page's build timestamp is an input — which is what
// makes the golden files stable.
var testNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

// ent builds one entity for a test catalog.
func ent(name string, kind catalog.Kind, owner string, tier int) *catalog.Entity {
	return &catalog.Entity{
		APIVersion: catalog.APIVersion,
		Kind:       kind,
		Metadata: catalog.Metadata{
			Name: name, Owner: owner, Tier: tier,
			Description: "The " + name + " entity.",
			Lifecycle:   "production",
			Tags:        []string{"go"},
		},
		Spec:       catalog.Spec{Path: "services/" + name},
		SourcePath: "services/" + name + "/service.yaml",
		NameLine:   4,
	}
}

const testTeamsYAML = "teams:\n" +
	"  - name: team-payments\n" +
	"    members: [alice, bob]\n" +
	"    slack: \"#payments\"\n" +
	"    pagerduty: PAY\n"

// input builds a complete render.Input from entities, so each test names
// only what it cares about.
func input(t *testing.T, files fstest.MapFS, entities ...*catalog.Entity) Input {
	t.Helper()
	var c diag.Collector
	cat := catalog.NewCatalog(entities, &c)
	g := cat.Resolve(catalog.FullCatalog, &c)
	teams := config.LoadTeams("teams.yaml", []byte(testTeamsYAML), &c)
	std := config.DefaultStandards()
	if files == nil {
		files = fstest.MapFS{}
	}
	env := scorecard.Env{
		FS: files, Now: testNow, MaxDocsAgeDays: 180,
		LastEdit: func(string) (time.Time, bool) { return time.Time{}, false },
	}
	sc := scorecard.Score(cat, std, nil, env, &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("fixture is not clean: %+v", ds)
	}
	return Input{
		Catalog: cat, Graph: g, Teams: teams, Scorecard: sc, Standards: std,
		FS: files, GeneratedAt: testNow, Version: "v0.3.0-test",
		Mermaid: Mermaid{Src: DefaultMermaidSrc, Integrity: DefaultMermaidIntegrity},
	}
}

func TestNewPageCarriesTheRelativeRoot(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	p := newPage(in, "entity/service/api/index.html", "api", "catalog")
	if p.Root != "../../../" {
		t.Errorf("Root = %q, want %q", p.Root, "../../../")
	}
	if p.Title != "api" {
		t.Errorf("Title = %q, want %q", p.Title, "api")
	}
	if p.Nav != "catalog" {
		t.Errorf("Nav = %q, want %q", p.Nav, "catalog")
	}
	if p.GeneratedAt != "2026-09-09 12:00 UTC" {
		t.Errorf("GeneratedAt = %q, want %q", p.GeneratedAt, "2026-09-09 12:00 UTC")
	}
}

// A remote Mermaid stays absolute; a local one is rewritten per page, since
// "assets/mermaid.min.js" means something different three levels down.
func TestNewPageResolvesALocalMermaidRelativeToThePage(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	in.Mermaid = Mermaid{Src: LocalMermaidPath}

	deep := newPage(in, "entity/service/api/index.html", "api", "catalog")
	if deep.Mermaid.Src != "../../../assets/mermaid.min.js" {
		t.Errorf("deep Src = %q", deep.Mermaid.Src)
	}
	if deep.Mermaid.Integrity != "" {
		t.Errorf("a local file carries no integrity attribute, got %q", deep.Mermaid.Integrity)
	}

	top := newPage(in, "index.html", "Catalog", "catalog")
	if top.Mermaid.Src != "assets/mermaid.min.js" {
		t.Errorf("top Src = %q", top.Mermaid.Src)
	}
}

func TestNewPageLeavesARemoteMermaidAbsolute(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	p := newPage(in, "entity/service/api/index.html", "api", "catalog")
	if p.Mermaid.Src != DefaultMermaidSrc {
		t.Errorf("Src = %q, want %q", p.Mermaid.Src, DefaultMermaidSrc)
	}
	if !strings.HasPrefix(p.Mermaid.Integrity, "sha384-") {
		t.Errorf("the pinned CDN URL must carry an SRI hash, got %q", p.Mermaid.Integrity)
	}
}

func TestCatalogRowsAreSortedByRef(t *testing.T) {
	in := input(t, nil,
		ent("zebra", catalog.KindService, "team-payments", 1),
		ent("alpha", catalog.KindService, "team-payments", 2),
	)
	rows := catalogRows(in)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Ref != "service:alpha" || rows[1].Ref != "service:zebra" {
		t.Errorf("rows are not sorted by ref: %q, %q", rows[0].Ref, rows[1].Ref)
	}
}

// Plan 2's ruling R1: an entity with no tier is not scored. Rendering that
// as 0% would brand every library in the catalog for a check nobody ran.
func TestAnUntieredEntityHasNoScoreRatherThanZero(t *testing.T) {
	in := input(t, nil,
		ent("api", catalog.KindService, "team-payments", 1),
		ent("shared", catalog.KindLibrary, "team-payments", 0),
	)
	rows := catalogRows(in)
	byRef := map[string]CatalogRow{}
	for _, r := range rows {
		byRef[r.Ref] = r
	}
	if byRef["library:shared"].Score != nil {
		t.Errorf("an untiered entity must have no score, got %v", *byRef["library:shared"].Score)
	}
	if byRef["service:api"].Score == nil {
		t.Error("a tiered entity must have a score")
	}
}

func TestCatalogRowsCarryTheirTeamURL(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	rows := catalogRows(in)
	if rows[0].OwnerURL != "team/team-payments/" {
		t.Errorf("OwnerURL = %q, want %q", rows[0].OwnerURL, "team/team-payments/")
	}
}

// An owner that resolves to no team gets no link. A link to a page that was
// never generated is a 404 the portal itself created.
func TestAnUnknownOwnerGetsNoTeamLink(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	in.Catalog.Entities()[0].Metadata.Owner = "team-ghost"
	rows := catalogRows(in)
	if rows[0].OwnerURL != "" {
		t.Errorf("OwnerURL = %q, want empty for an unresolvable owner", rows[0].OwnerURL)
	}
	if rows[0].Owner != "team-ghost" {
		t.Errorf("the owner name is still shown, got %q", rows[0].Owner)
	}
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `go test ./internal/render/ -run 'TestNewPage|TestCatalogRows|TestAn' -v`
Expected: FAIL — `undefined: Input`, `undefined: newPage`, `undefined: catalogRows`.

- [ ] **Step 7: Write the view models**

Create `internal/render/model.go`:

```go
// Package render turns a validated, scored catalog into a static portal.
//
// Site is a pure function: it takes the catalog, the resolved graph, the
// teams, the scorecard and an fs.FS to read documentation from, and returns
// every byte of the site as []emit.File. Nothing here touches the
// filesystem, reads the clock, or reaches the network — cmd/ owns the one
// loop that writes what this returns (spec §3.1).
//
// That is also what makes `serve --watch` simple: the site is a value, so a
// rebuild is a function call and the preview server holds the result in
// memory rather than watching its own output directory.
package render

import (
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// The pinned Mermaid bundle (ruling R13). Version, URL and hash travel
// together: an SRI hash that does not match its URL blocks the script with a
// console error and no diagram, which is the worst of both outcomes.
const (
	DefaultMermaidSrc       = "https://cdn.jsdelivr.net/npm/mermaid@11.17.2/dist/mermaid.min.js"
	DefaultMermaidIntegrity = "sha384-EOXBFmc3gx5mb+vn0vPvvGqACToJD24hhacX5Yx+8NUUQrHIle/Qi5Bg9o3zKwW2"
	// LocalMermaidPath is the sentinel Src for a copy served from the site
	// itself. newPage rewrites it per page; the file is emitted by Site.
	LocalMermaidPath = "assets/mermaid.min.js"
)

// Mermaid says where the diagram renderer comes from.
type Mermaid struct {
	Src string
	// Integrity is the SRI hash, set only for a remote Src. A local file
	// served from the same origin as the page needs none, and an integrity
	// attribute on a file the user supplied would block their own override.
	Integrity string
}

// Input is everything Site needs. It is a struct rather than nine parameters
// because the renderer genuinely consumes all of it, and because cmd/
// building this value is the explicit composition spec §3.1 asks for.
type Input struct {
	Catalog   *catalog.Catalog
	Graph     *catalog.Graph
	Teams     *config.Teams
	Scorecard *scorecard.Scorecard
	Standards *config.Standards
	// History is scorecard-history.csv, or nil when the repository has none.
	// nil and empty are different: no file means no trend was ever recorded,
	// an empty file means the header is there and no run has appended yet.
	History []byte
	// FS is the repository, for reading docs/ and runbooks.
	FS          fs.FS
	Mermaid     Mermaid
	GeneratedAt time.Time
	Version     string
}

// Page is the header every template receives.
type Page struct {
	Title string
	// Root is the relative path back to the site root, "" at the top and
	// "../../../" for an entity page (ruling R12).
	Root        string
	Nav         string
	GeneratedAt string
	Version     string
	Mermaid     Mermaid
}

// CatalogRow is one entity in the catalog table.
type CatalogRow struct {
	Ref         string
	Name        string
	Kind        string
	Description string
	Owner       string
	// OwnerURL is empty when the owner resolves to no team. A link to a page
	// that was never generated is a 404 the portal created for itself.
	OwnerURL  string
	Tier      int
	Lifecycle string
	Tags      []string
	URL       string
	// Score is nil for an entity that was not scored at all. Plan 2's ruling
	// R1 excludes untiered entities, and rendering "not scored" as 0% would
	// brand every library in the catalog for a check nobody ran.
	Score *float64
}

// CatalogPage is the index.
type CatalogPage struct {
	Page
	Rows []CatalogRow
	// The distinct values present, for the filter controls. Rendered
	// server-side so the filters work before catalog.js loads and so an
	// empty catalog shows empty filters rather than stale ones.
	Kinds []string
	Teams []string
	Tiers []int
	Tags  []string
}

// newPage fills in the header for one output path.
func newPage(in Input, outputPath, title, nav string) Page {
	root := rootRel(outputPath)
	m := in.Mermaid
	if m.Src == LocalMermaidPath {
		// A site-relative asset means something different at every depth.
		m.Src = root + LocalMermaidPath
		m.Integrity = ""
	}
	return Page{
		Title: title,
		Root:  root,
		Nav:   nav,
		// Minute precision: a portal rebuilt on every merge would otherwise
		// show a diff in every footer for no reason a reader cares about.
		GeneratedAt: in.GeneratedAt.UTC().Format("2006-01-02 15:04 MST"),
		Version:     in.Version,
		Mermaid:     m,
	}
}

// scoresByRef indexes the scorecard so a row lookup is not a linear scan per
// entity.
func scoresByRef(sc *scorecard.Scorecard) map[catalog.Ref]float64 {
	out := map[catalog.Ref]float64{}
	if sc == nil {
		return out
	}
	for _, e := range sc.Entities {
		out[e.Ref] = e.Score()
	}
	return out
}

// catalogRows builds the index table, sorted by ref so the page is stable
// between runs.
func catalogRows(in Input) []CatalogRow {
	scores := scoresByRef(in.Scorecard)
	slugs := map[string]string{}
	for _, name := range in.Teams.Names() {
		if s, ok := Slug(name); ok {
			slugs[name] = s
		}
	}

	var rows []CatalogRow
	for _, e := range in.Catalog.Entities() {
		ref := e.Ref()
		row := CatalogRow{
			Ref:         ref.String(),
			Name:        e.Metadata.Name,
			Kind:        string(e.Kind),
			Description: e.Metadata.Description,
			Owner:       e.Metadata.Owner,
			Tier:        e.Metadata.Tier,
			Lifecycle:   e.Metadata.Lifecycle,
			Tags:        e.Metadata.Tags,
			URL:         EntityURL(ref),
		}
		if slug, ok := slugs[e.Metadata.Owner]; ok {
			row.OwnerURL = TeamURL(slug)
		}
		if s, ok := scores[ref]; ok {
			score := s
			row.Score = &score
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Ref < rows[j].Ref })
	return rows
}

// distinct returns the sorted unique non-empty values, for a filter control.
func distinct(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// catalogPage assembles the index.
func catalogPage(in Input) CatalogPage {
	rows := catalogRows(in)
	var kinds, teams, tags []string
	tierSeen := map[int]bool{}
	var tiers []int
	for _, r := range rows {
		kinds = append(kinds, r.Kind)
		teams = append(teams, r.Owner)
		tags = append(tags, r.Tags...)
		if r.Tier != 0 && !tierSeen[r.Tier] {
			tierSeen[r.Tier] = true
			tiers = append(tiers, r.Tier)
		}
	}
	sort.Ints(tiers)
	return CatalogPage{
		Page:  newPage(in, "index.html", "Catalog", "catalog"),
		Rows:  rows,
		Kinds: distinct(kinds),
		Teams: distinct(teams),
		Tiers: tiers,
		Tags:  distinct(tags),
	}
}

// lower is a template helper; kinds are title-cased in the data and
// lowercase in URLs and CSS classes.
func lower(s string) string { return strings.ToLower(s) }
```

- [ ] **Step 8: Run the tests**

Run: `go test ./internal/render/ -v`
Expected: PASS, fifteen tests.

- [ ] **Step 9: Commit**

```bash
git add internal/render/url.go internal/render/url_test.go internal/render/model.go internal/render/model_test.go
git commit -m "feat: the portal's URL scheme and view models

Pages are keyed on the ref, so service:orders and topic:orders get
different URLs. Every link is relative to a per-page Root, so the site
works from a subdirectory.

Entity names are schema-constrained and used verbatim; team names are
not validated at all, so they are slugged — and two teams whose names
slug to the same page is a reported error, not a silent overwrite."
```

---

## Task 5: Templates, assets, and the first real page

**Files:**
- Create: `internal/render/web/templates/base.html`
- Create: `internal/render/web/templates/catalog.html`
- Create: `internal/render/web/static/style.css`
- Create: `internal/render/assets.go`
- Create: `internal/render/site.go`
- Create: `internal/render/golden_test.go`
- Create: `internal/render/site_test.go`
- Create: `internal/render/testdata/golden/` (generated by `-update`)

**Interfaces:**
- Consumes: everything from Task 4, and `md.ChromaCSS()` from Task 1.
- Produces:
  - `func render.Site(in Input, c *diag.Collector) []emit.File` — **the one entry point**
  - `func render.templateSet(page string) (*template.Template, error)`
  - `func render.renderPage(t *template.Template, outPath string, data any, c *diag.Collector) (emit.File, bool)`
  - `func render.assets(in Input, c *diag.Collector) []emit.File`
  - Test helpers `golden(t, name, got)` and `siteMap(files)` for every later task

**Context:** After this task `Site` returns a real, complete, browsable one-page site, and Task 6 puts it on disk. Every later task adds pages to the same function.

**Why a template set per page.** Each page template defines `"content"`. Parsing them all into one `*template.Template` makes the last one parsed win, silently — every page would render the same body. `templateSet(page)` parses `base.html` plus exactly one page file, so the collision cannot happen.

**A note for Task 8, recorded here while the templates are being written:** `html/template` escapes `+` to `&#43;` inside an HTML attribute — a deliberate defence against UTF-7 smuggling, not a bug. The SRI hash contains two `+`, and the HTML parser decodes the entities back before the browser computes the integrity check, so the hash still matches. Do not "fix" it by declaring `Integrity` a `template.HTMLAttr`: that turns off escaping on an attribute `--mermaid-src` lets a user populate. Task 8 adds the test that pins this down.

**`assets` walks the embedded static directory** rather than listing files. Tasks 7, 8, 12 and 13 each add one `.js` file and one `<script>` tag in `base.html`; none of them needs to touch `assets`.

- [ ] **Step 1: Write the base template**

Create `internal/render/web/templates/base.html`:

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · landsraad</title>
<link rel="stylesheet" href="{{.Root}}assets/style.css">
<link rel="stylesheet" href="{{.Root}}assets/chroma.css">
</head>
<body>
<header class="site">
  <a class="brand" href="{{if .Root}}{{.Root}}{{else}}./{{end}}">landsraad</a>
  <nav>
    <a href="{{if .Root}}{{.Root}}{{else}}./{{end}}"{{if eq .Nav "catalog"}} aria-current="page"{{end}}>Catalog</a>
    <a href="{{.Root}}scorecard/"{{if eq .Nav "scorecard"}} aria-current="page"{{end}}>Scorecard</a>
    <a href="{{.Root}}map/"{{if eq .Nav "map"}} aria-current="page"{{end}}>Dependencies</a>
  </nav>
</header>
<main>
{{template "content" .}}
</main>
<footer class="site">
Generated by landsraad <span class="mono">{{.Version}}</span> at <span class="mono">{{.GeneratedAt}}</span>
</footer>
</body>
</html>
```

- [ ] **Step 2: Write the catalog template**

Create `internal/render/web/templates/catalog.html`:

```html
{{define "content"}}
<h1>Catalog</h1>
<p class="count">{{len .Rows}} entities</p>
<table class="catalog" id="catalog">
<thead>
<tr><th>Entity</th><th>Kind</th><th>Owner</th><th>Tier</th><th>Lifecycle</th><th>Score</th></tr>
</thead>
<tbody>
{{range .Rows}}
<tr data-kind="{{lower .Kind}}" data-owner="{{.Owner}}" data-tier="{{.Tier}}" data-tags="{{range .Tags}}{{.}} {{end}}">
  <td>
    <a href="{{$.Root}}{{.URL}}">{{.Name}}</a>
    {{with .Description}}<span class="desc">{{.}}</span>{{end}}
  </td>
  <td><span class="kind kind-{{lower .Kind}}">{{.Kind}}</span></td>
  <td>{{if .OwnerURL}}<a href="{{$.Root}}{{.OwnerURL}}">{{.Owner}}</a>{{else}}{{.Owner}}{{end}}</td>
  <td>{{if .Tier}}{{.Tier}}{{else}}<span class="none">—</span>{{end}}</td>
  <td>{{.Lifecycle}}</td>
  <td>{{if .Score}}<span class="mono">{{pct .Score}}</span>{{else}}<span class="none">not scored</span>{{end}}</td>
</tr>
{{end}}
</tbody>
</table>
{{end}}
```

- [ ] **Step 3: Write the stylesheet**

Create `internal/render/web/static/style.css`:

```css
/* Spec §10: dense, left-aligned, monospace reserved for versions. */
:root {
  --fg: #16181d;
  --muted: #626772;
  --bg: #ffffff;
  --line: #e2e5ea;
  --accent: #1a4fa0;
  --warn: #8a5a00;
  --bad: #a01a1a;
  --good: #1a7a3c;
}
* { box-sizing: border-box; }
body {
  margin: 0;
  font: 14px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  color: var(--fg);
  background: var(--bg);
}
.mono, code, pre { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }

header.site {
  display: flex; align-items: baseline; gap: 1.5rem;
  padding: .75rem 1.5rem; border-bottom: 1px solid var(--line);
}
header.site .brand { font-weight: 600; color: var(--fg); }
header.site nav { display: flex; gap: 1rem; }
header.site nav a[aria-current="page"] { color: var(--fg); font-weight: 600; }
main { padding: 1.5rem; max-width: 72rem; }
footer.site {
  padding: 1rem 1.5rem; border-top: 1px solid var(--line);
  color: var(--muted); font-size: 12px;
}

h1 { font-size: 1.35rem; margin: 0 0 .25rem; }
h2 { font-size: 1.1rem; margin: 1.5rem 0 .5rem; }
p.count { color: var(--muted); margin: 0 0 1rem; }

table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; padding: .4rem .6rem; border-bottom: 1px solid var(--line); vertical-align: top; }
th { font-size: 12px; text-transform: uppercase; letter-spacing: .04em; color: var(--muted); font-weight: 600; }
td .desc { display: block; color: var(--muted); font-size: 12px; }
.none { color: var(--muted); }

.kind { font-size: 12px; padding: .1rem .35rem; border: 1px solid var(--line); border-radius: 3px; }

/* Status vocabulary. Six values, not two: "not reported" and "stale" are
   failures of the evidence, not of the service (spec §9). */
.status-pass { color: var(--good); }
.status-fail { color: var(--bad); }
.status-error { color: var(--bad); }
.status-stale { color: var(--warn); }
.status-not-reported { color: var(--muted); }
.status-exempt { color: var(--muted); }

/* Admonitions, spec D2. */
.admonition { border-left: 3px solid var(--line); padding: .1rem 1rem; margin: 1rem 0; background: #fafbfc; }
.admonition-title { font-weight: 600; margin: .6rem 0 .2rem; }
.admonition.warning { border-left-color: var(--warn); }
.admonition.danger { border-left-color: var(--bad); }
.admonition.note, .admonition.info { border-left-color: var(--accent); }
.admonition.tip { border-left-color: var(--good); }

pre { overflow-x: auto; padding: .75rem; background: #f7f7f7; border-radius: 3px; }
```

- [ ] **Step 4: Write the failing test**

Create `internal/render/golden_test.go`:

```go
package render

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// update regenerates the golden files. Spec §14 asks for golden-file tests
// for the renderer with a -update flag.
//
// ALWAYS read the diff before committing a regeneration. A golden test whose
// output is refreshed without being looked at asserts nothing at all.
var update = flag.Bool("update", false, "regenerate golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s — run `go test ./internal/render/ -update`", path)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("%s differs (-want +got):\n%s", name, diff)
	}
}
```

Create `internal/render/site_test.go`:

```go
package render

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// siteMap indexes a rendered site by path, for tests that ask about one file.
func siteMap(files []emit.File) map[string][]byte {
	out := map[string][]byte{}
	for _, f := range files {
		out[f.Path] = f.Data
	}
	return out
}

// twoEntities is the fixture most site tests render.
func twoEntities(t *testing.T) Input {
	t.Helper()
	return input(t, nil,
		ent("ledger-api", catalog.KindService, "team-payments", 1),
		ent("payments-events", catalog.KindTopic, "team-payments", 0),
	)
}

func TestSiteEmitsTheCatalogAndItsAssets(t *testing.T) {
	var c diag.Collector
	files := Site(twoEntities(t), &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("clean input produced diagnostics: %+v", ds)
	}
	got := siteMap(files)
	for _, want := range []string{"index.html", "assets/style.css", "assets/chroma.css"} {
		if _, ok := got[want]; !ok {
			t.Errorf("site is missing %s; got %v", want, keys(got))
		}
	}
}

// Every path is slash-separated and relative to a root the renderer never
// names (spec §3.1). An absolute path here would escape cmd/'s write loop.
func TestEverySitePathIsRelativeAndSlashSeparated(t *testing.T) {
	var c diag.Collector
	for _, f := range Site(twoEntities(t), &c) {
		if strings.HasPrefix(f.Path, "/") {
			t.Errorf("absolute path %q", f.Path)
		}
		if strings.Contains(f.Path, `\`) {
			t.Errorf("backslash in path %q", f.Path)
		}
		if strings.Contains(f.Path, "..") {
			t.Errorf("parent traversal in path %q", f.Path)
		}
	}
}

func TestSiteIsDeterministic(t *testing.T) {
	var c1, c2 diag.Collector
	a := siteMap(Site(twoEntities(t), &c1))
	b := siteMap(Site(twoEntities(t), &c2))
	if len(a) != len(b) {
		t.Fatalf("file counts differ: %d vs %d", len(a), len(b))
	}
	for path, want := range a {
		if string(b[path]) != string(want) {
			t.Errorf("%s differs between two renders of the same input", path)
		}
	}
}

func TestCatalogPageIsGolden(t *testing.T) {
	var c diag.Collector
	files := Site(twoEntities(t), &c)
	golden(t, "index.html", siteMap(files)["index.html"])
}

// An entity that was not scored renders "not scored", never "0%".
func TestCatalogShowsNotScoredRatherThanZero(t *testing.T) {
	var c diag.Collector
	index := string(siteMap(Site(twoEntities(t), &c))["index.html"])
	if !strings.Contains(index, "not scored") {
		t.Errorf("the untiered topic must render as not scored:\n%s", index)
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `go test ./internal/render/ -run TestSite -v`
Expected: FAIL — `undefined: Site`.

- [ ] **Step 6: Write the asset and template plumbing**

Create `internal/render/assets.go`:

```go
package render

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/render/md"
)

// webFS holds the templates and static files.
//
// Spec §13's layout sketch puts these at a top-level web/. They live here
// instead (ruling R10): go:embed patterns are relative to the package
// directory and may not contain "..", and a root-level `package web` would
// be a public import path, which D8 forbids. The sketch predates the
// constraint and Task 15 amends it.
//
//go:embed web
var webFS embed.FS

// templateSet parses base.html plus exactly one page template.
//
// One set per page, not one set for all of them: every page file defines
// "content", so a shared set would silently keep only the last one parsed
// and render the same body on every page.
func templateSet(page string) (*template.Template, error) {
	return template.New("base.html").
		Funcs(funcs()).
		ParseFS(webFS, "web/templates/base.html", "web/templates/"+page)
}

func funcs() template.FuncMap {
	return template.FuncMap{
		"lower": lower,
		"pct":   pct,
	}
}

// pct renders a score as a whole percentage. It takes a pointer because
// "not scored" and "zero" are different answers (Plan 2, ruling R1) and the
// template asks which one it has before calling this.
func pct(f *float64) string {
	if f == nil {
		return ""
	}
	return fmt.Sprintf("%.0f%%", *f*100)
}

// renderPage executes one template into an emit.File.
//
// A template that fails to execute is reported and skipped, never fatal: one
// broken page must not hide the other forty (spec §12).
func renderPage(t *template.Template, outPath string, data any, c *diag.Collector) (emit.File, bool) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base.html", data); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: outPath, Line: 1,
			Check:   "template",
			Message: fmt.Sprintf("cannot render %s: %v", outPath, err),
		})
		return emit.File{}, false
	}
	return emit.File{Path: outPath, Data: buf.Bytes()}, true
}

// assets returns every static file the site serves.
//
// It walks the embedded directory rather than listing names, so adding a
// stylesheet or a client script is one new file and no code change.
func assets(in Input, c *diag.Collector) []emit.File {
	var out []emit.File

	entries, err := fs.ReadDir(webFS, "web/static")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "assets", Line: 1,
			Check:   "assets",
			Message: fmt.Sprintf("cannot read the embedded static files: %v", err),
		})
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(webFS, path.Join("web/static", e.Name()))
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "assets/" + e.Name(), Line: 1,
				Check:   "assets",
				Message: fmt.Sprintf("cannot read the embedded file %s: %v", e.Name(), err),
			})
			continue
		}
		out = append(out, emit.File{Path: "assets/" + e.Name(), Data: data})
	}

	css, err := md.ChromaCSS()
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "assets/chroma.css", Line: 1,
			Check:   "assets",
			Message: fmt.Sprintf("cannot generate the syntax-highlighting stylesheet: %v", err),
		})
	} else {
		out = append(out, emit.File{Path: "assets/chroma.css", Data: css})
	}

	// A locally served Mermaid bundle, when --mermaid-src named a file. cmd/
	// read the bytes; this only places them.
	if in.Mermaid.Src == LocalMermaidPath && len(in.Mermaid.Data) > 0 {
		out = append(out, emit.File{Path: LocalMermaidPath, Data: in.Mermaid.Data})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
```

Add `Data` to `Mermaid` in `internal/render/model.go`:

```go
// Mermaid says where the diagram renderer comes from.
type Mermaid struct {
	Src string
	// Integrity is the SRI hash, set only for a remote Src. A local file
	// served from the same origin as the page needs none, and an integrity
	// attribute on a file the user supplied would block their own override.
	Integrity string
	// Data is the bundle's bytes when Src is LocalMermaidPath. cmd/ reads
	// the file the user named; this package only places it, because nothing
	// under internal/ touches the filesystem outside an injected fs.FS.
	Data []byte
}
```

- [ ] **Step 7: Write `Site`**

Create `internal/render/site.go`:

```go
package render

import (
	"fmt"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// Site renders the whole portal.
//
// It is spec §7's stage 8 for sub-project D: a pure function from a
// validated, scored catalog to the bytes of a static site. Nothing here
// writes a file — cmd/ owns the one loop that does (spec §3.1), which is
// also what lets `serve --watch` hold a rebuilt site in memory instead of
// watching its own output.
//
// Pages that fail to render are reported and skipped. A single broken
// runbook must not cost the reader the other forty pages.
func Site(in Input, c *diag.Collector) []emit.File {
	var files []emit.File

	if t, err := templateSet("catalog.html"); err != nil {
		c.Add(templateCompileError("catalog.html", err))
	} else if f, ok := renderPage(t, "index.html", catalogPage(in), c); ok {
		files = append(files, f)
	}

	files = append(files, assets(in, c)...)
	return files
}

// templateCompileError is the one place a broken embedded template is
// reported, so the wording cannot drift between page types.
func templateCompileError(name string, err error) diag.Diagnostic {
	return diag.Diagnostic{
		Severity: diag.SevError, File: "templates/" + name, Line: 1,
		Check:   "template",
		Message: fmt.Sprintf("cannot compile the embedded template %s: %v", name, err),
		Hint:    "this is a bug in landsraad, not in your catalog",
	}
}
```

- [ ] **Step 8: Generate the golden file and read it**

Run: `go test ./internal/render/ -update && cat internal/render/testdata/golden/index.html`

Expected: a complete HTML page with two rows — `ledger-api` linking to `entity/service/ledger-api/`, and `payments-events` linking to `entity/topic/payments-events/` and showing `not scored`.

**Read the output before continuing.** If the links, the score column or the `—` for the missing tier are wrong, fix the template now: every later golden file is generated against this one.

- [ ] **Step 9: Run the full suite**

Run: `go test ./internal/render/... -v && sh scripts/check-rules.sh && go vet ./... && gofmt -l .`
Expected: PASS, and no rule output.

- [ ] **Step 10: Commit**

```bash
git add internal/render/assets.go internal/render/site.go internal/render/golden_test.go internal/render/site_test.go internal/render/model.go internal/render/web internal/render/testdata
git commit -m "feat: templates, assets and the catalog page

Site is a pure function from a scored catalog to the bytes of a static
site; cmd/ owns the write loop.

One template set per page, because every page file defines \"content\" and
a shared set would silently keep only the last one parsed.

Assets live under internal/render/web rather than spec §13's top-level
web/: go:embed cannot reach above its own package directory, and a
root-level package would be a public import path."
```

---

## Task 6: `landsraad build`

**Files:**
- Create: `cmd/landsraad/build.go`
- Create: `cmd/landsraad/build_test.go`
- Create: `cmd/landsraad/manifest.go`
- Create: `cmd/landsraad/manifest_test.go`
- Modify: `cmd/landsraad/gen.go` (`loadCatalog` gains a scoped form returning the graph)
- Modify: `cmd/landsraad/main.go` (register `newBuildCmd()`)
- Modify: `internal/render/model.go` (add `Notice` to `Input` and `Page`)
- Modify: `internal/render/web/templates/base.html` (render the notice banner)

**Interfaces:**
- Consumes: `render.Site`, `render.Input`, `render.Mermaid`, `loadCatalog`, `standardsFor`, `gitLastEdit`.
- Produces:
  - `type BuildOptions struct { Mermaid render.Mermaid; Now time.Time; LastEdit scorecard.LastEditFunc; Version string; Force bool }`
  - `func Build(fsys fs.FS, errOut io.Writer, opts BuildOptions) ([]emit.File, int)`
  - `func writeSite(outDir string, files []emit.File, force bool, errOut io.Writer) error`
  - `func loadCatalogScoped(fsys fs.FS, scope catalog.Scope, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams)`
  - `func readManifest(outDir string) ([]string, bool)`, `func writeManifest(outDir string, files []emit.File) error`

**Context:** `Build` returns `[]emit.File` and an exit code; it never touches the disk. `writeSite` is the disk half, in `cmd/` where the rules allow it. That split is what lets the whole build be tested against `fstest.MapFS` with no temporary directory, and it is what Task 14's `serve --watch` reuses to rebuild in memory.

**Scope, not `LocalOnly`.** Spec §7.1: under `build`, dangling refs and dependency cycles are hard failures. `loadCatalog` resolves `LocalOnly` because `validate` must not fail on a reference into another repo. `build` claims to render the whole catalog, so it resolves `FullCatalog` and a dangling ref stops it. This is why `loadCatalog` grows a scoped form rather than `build` re-implementing the pipeline.

**Ruling R22, and the honest edge of this plan.** With no fetcher, a `repos.yaml` listing three repositories produces a portal containing one. Rendering that silently would be exactly the failure spec §12 forbids — "rendering a portal quietly missing three services is worse than rendering nothing". So `build` reports a **warning** naming the repositories it did not read, and stamps a banner into every page of the generated site. Degraded mode is visible in the artifact, not only in a log. Plan 4 replaces both with real fetching.

**Ruling R16 in code.** `dist/.landsraad-manifest` lists what the last build wrote. A rebuild deletes the previous paths that are no longer produced — otherwise a deleted service's page stays on the portal forever, and a stale page is a lie with a URL. If the output directory is non-empty and has no manifest, `build` refuses: `landsraad build -o .` must not be able to delete somebody's repository.

- [ ] **Step 1: Write the failing manifest test**

Create `cmd/landsraad/manifest_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/landsraadhq/landsraad/internal/emit"
)

func TestManifestRoundTrips(t *testing.T) {
	dir := t.TempDir()
	files := []emit.File{
		{Path: "index.html", Data: []byte("a")},
		{Path: "entity/service/api/index.html", Data: []byte("b")},
	}
	if err := writeManifest(dir, files); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	got, ok := readManifest(dir)
	if !ok {
		t.Fatal("readManifest reported no manifest just after writing one")
	}
	want := []string{"entity/service/api/index.html", "index.html"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReadManifestReportsAbsence(t *testing.T) {
	if _, ok := readManifest(t.TempDir()); ok {
		t.Error("an empty directory has no manifest")
	}
}

// Ruling R16: a deleted service's page must not stay on the portal. A stale
// page is a lie with a URL.
func TestWriteSitePrunesPagesTheBuildNoLongerProduces(t *testing.T) {
	dir := t.TempDir()
	first := []emit.File{
		{Path: "index.html", Data: []byte("one")},
		{Path: "entity/service/gone/index.html", Data: []byte("bye")},
	}
	if err := writeSite(dir, first, false, os.Stderr); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entity/service/gone/index.html")); err != nil {
		t.Fatalf("first build did not write the page: %v", err)
	}

	second := []emit.File{{Path: "index.html", Data: []byte("two")}}
	if err := writeSite(dir, second, false, os.Stderr); err != nil {
		t.Fatalf("second build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entity/service/gone/index.html")); !os.IsNotExist(err) {
		t.Error("the page of a deleted entity survived the rebuild")
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil || string(data) != "two" {
		t.Errorf("index.html = %q, %v; want \"two\"", data, err)
	}
}

// `landsraad build -o .` must not be able to delete somebody's repository.
func TestWriteSiteRefusesAnUnknownNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "important.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := writeSite(dir, []emit.File{{Path: "index.html", Data: []byte("a")}}, false, os.Stderr)
	if err == nil {
		t.Fatal("writeSite must refuse a non-empty directory with no manifest")
	}
	want := "refusing to write into " + dir + ": it is not empty and was not written by landsraad build"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "important.txt")); statErr != nil {
		t.Error("the refusal must not have touched the existing files")
	}
}

func TestWriteSiteAcceptsAnUnknownDirectoryUnderForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "important.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSite(dir, []emit.File{{Path: "index.html", Data: []byte("a")}}, true, os.Stderr); err != nil {
		t.Fatalf("--force must permit it: %v", err)
	}
	// force permits writing; it does not license deleting files landsraad
	// never wrote.
	if _, err := os.Stat(filepath.Join(dir, "important.txt")); err != nil {
		t.Error("--force must not delete files outside the manifest")
	}
}

func TestWriteSiteAcceptsAnEmptyDirectory(t *testing.T) {
	if err := writeSite(t.TempDir(), []emit.File{{Path: "index.html", Data: []byte("a")}}, false, os.Stderr); err != nil {
		t.Fatalf("an empty directory is fine: %v", err)
	}
}

func TestWriteSiteCreatesAMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dist")
	if err := writeSite(dir, []emit.File{{Path: "index.html", Data: []byte("a")}}, false, os.Stderr); err != nil {
		t.Fatalf("writeSite: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Errorf("index.html was not written: %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/landsraad/ -run 'TestManifest|TestReadManifest|TestWriteSite' -v`
Expected: FAIL — `undefined: writeManifest`.

- [ ] **Step 3: Write the manifest**

Create `cmd/landsraad/manifest.go`:

```go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/landsraadhq/landsraad/internal/emit"
)

// manifestName records what the last build wrote, relative to the output
// directory (ruling R16).
//
// It exists so a rebuild can delete the page of a service that has been
// removed from the catalog. Without it a deleted entity keeps a live URL on
// the portal forever — a page that says a service exists, hosted by the tool
// whose entire thesis is that the metadata is the source.
const manifestName = ".landsraad-manifest"

const manifestHeader = "# Written by `landsraad build`. Do not edit: the next build deletes\n" +
	"# every path listed here that it no longer produces.\n"

// readManifest returns the paths the previous build wrote, and whether there
// was a manifest at all. Absent and empty are different: absent means this
// directory was not written by landsraad.
func readManifest(outDir string) ([]string, bool) {
	data, err := os.ReadFile(filepath.Join(outDir, manifestName))
	if err != nil {
		return nil, false
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, true
}

func writeManifest(outDir string, files []emit.File) error {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	body := manifestHeader + strings.Join(paths, "\n") + "\n"
	return os.WriteFile(filepath.Join(outDir, manifestName), []byte(body), 0o644)
}

// writeSite puts a rendered site on disk, pruning what the previous build
// wrote and no longer produces.
//
// It refuses a non-empty directory it did not write. `landsraad build -o .`
// is one keystroke away from `landsraad build -o dist`, and the difference
// between the two must not be somebody's repository.
func writeSite(outDir string, files []emit.File, force bool, errOut io.Writer) error {
	previous, known := readManifest(outDir)
	if !known {
		empty, err := isEmptyOrMissing(outDir)
		if err != nil {
			return err
		}
		if !empty && !force {
			return fmt.Errorf("refusing to write into %s: it is not empty and was not written by landsraad build", outDir)
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Prune before writing, and only paths this tool put there. force
	// permits writing into an unknown directory; it never licenses deleting
	// a file landsraad did not write.
	current := map[string]bool{}
	for _, f := range files {
		current[f.Path] = true
	}
	pruned := 0
	for _, p := range previous {
		if current[p] {
			continue
		}
		full := filepath.Join(outDir, filepath.FromSlash(p))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot remove the stale page %s: %w", p, err)
		}
		pruned++
	}
	if pruned > 0 {
		fmt.Fprintf(errOut, "  removed %s no longer in the catalog\n", plural(pruned, "page", "pages"))
	}

	for _, f := range files {
		full := filepath.Join(outDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, f.Data, 0o644); err != nil {
			return err
		}
	}
	return writeManifest(outDir, files)
}

func isEmptyOrMissing(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
```

- [ ] **Step 4: Run the manifest tests**

Run: `go test ./cmd/landsraad/ -run 'TestManifest|TestReadManifest|TestWriteSite' -v`
Expected: PASS, seven tests.

- [ ] **Step 5: Write the failing build test**

Create `cmd/landsraad/build_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/render"
)

var buildNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func buildOpts() BuildOptions {
	return BuildOptions{
		Now:      buildNow,
		LastEdit: noLastEdit(),
		Version:  "v0.3.0-test",
		Mermaid:  render.Mermaid{Src: render.DefaultMermaidSrc, Integrity: render.DefaultMermaidIntegrity},
	}
}

// buildFS is a minimal, valid single-repo catalog.
func buildFS() fstest.MapFS {
	return fstest.MapFS{
		"repos.yaml": {Data: []byte(
			"repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"teams.yaml": {Data: []byte(
			"teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")},
		"services/ledger-api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
				"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
				"  path: services/ledger-api\n")},
	}
}

func buildSiteMap(t *testing.T, fsys fstest.MapFS, opts BuildOptions) (map[string][]byte, int, string) {
	t.Helper()
	var errOut bytes.Buffer
	files, code := Build(fsys, &errOut, opts)
	out := map[string][]byte{}
	for _, f := range files {
		out[f.Path] = f.Data
	}
	return out, code, errOut.String()
}

func TestBuildRendersACleanCatalog(t *testing.T) {
	files, code, errOut := buildSiteMap(t, buildFS(), buildOpts())
	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut)
	}
	if _, ok := files["index.html"]; !ok {
		t.Error("no index.html")
	}
	if !strings.Contains(errOut, "ok: ") {
		t.Errorf("expected an ok summary on stderr, got:\n%s", errOut)
	}
}

// A portal generated from a broken catalog publishes the broken state as if
// it were the truth. gen already refuses for the same reason.
func TestBuildRefusesABrokenCatalog(t *testing.T) {
	fsys := buildFS()
	fsys["services/ledger-api/service.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-ghost\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n")}

	files, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if len(files) != 0 {
		t.Errorf("a refused build must render nothing, got %d files", len(files))
	}
	want := "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n"
	if !strings.HasSuffix(errOut, want) {
		t.Errorf("stderr:\n%s\nmust end with:\n%s", errOut, want)
	}
}

// Spec §7.1: under build, a dangling reference is a hard failure. validate
// records it and moves on, because the target may live in another repo.
func TestBuildFailsOnADanglingReference(t *testing.T) {
	fsys := buildFS()
	fsys["services/ledger-api/service.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n  dependsOn: [topic:nowhere]\n")}

	_, code, _ := buildSiteMap(t, fsys, buildOpts())
	if code != exitValidation {
		t.Errorf("exit = %d, want %d for a dangling ref under FullCatalog", code, exitValidation)
	}
}

// Ruling R22. Rendering a portal that quietly omits two of three repos is
// exactly what spec §12 forbids. Plan 4 replaces this with real fetching.
func TestBuildWarnsWhenReposYAMLListsRepositoriesItCannotRead(t *testing.T) {
	fsys := buildFS()
	fsys["repos.yaml"] = &fstest.MapFile{Data: []byte(
		"repos:\n" +
			"  - url: https://github.com/org/monorepo\n    paths: [services/*]\n" +
			"  - url: https://github.com/org/edge-gateway\n    paths: [.]\n")}

	files, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitOK {
		t.Fatalf("a partial build still succeeds, got exit %d:\n%s", code, errOut)
	}
	wantLine := "warn: repos.yaml lists 2 repositories and this build read only the local one; " +
		"edge-gateway is missing from the portal\n"
	if !strings.Contains(errOut, wantLine) {
		t.Errorf("stderr:\n%s\nmust contain:\n%s", errOut, wantLine)
	}
	// Degraded mode must be visible in the ARTIFACT, not only in a log.
	index := string(files["index.html"])
	if !strings.Contains(index, "read only the local repository") {
		t.Errorf("the generated page carries no banner:\n%s", index)
	}
}

func TestBuildPassesTheFrozenClockThrough(t *testing.T) {
	files, _, _ := buildSiteMap(t, buildFS(), buildOpts())
	if !strings.Contains(string(files["index.html"]), "2026-09-09 12:00 UTC") {
		t.Error("the page footer must carry the injected build time, not time.Now()")
	}
}

func TestBuildIsPureAndWritesNothing(t *testing.T) {
	fsys := buildFS()
	before := len(fsys)
	if _, _, _ := buildSiteMap(t, fsys, buildOpts()); len(fsys) != before {
		t.Error("Build must not write into the filesystem it reads")
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./cmd/landsraad/ -run TestBuild -v`
Expected: FAIL — `undefined: Build`, `undefined: BuildOptions`.

- [ ] **Step 7: Add the scoped catalog loader**

In `cmd/landsraad/gen.go`, replace the body of `loadCatalog` and add the scoped form. Everything from the start of the function to the `return cat, teams` line becomes:

```go
// loadCatalog runs stages 1, 3, 4 and 5 at LocalOnly scope — what gen and
// score need, and what validate uses.
func loadCatalog(fsys fs.FS, c *diag.Collector) (*catalog.Catalog, *config.Teams) {
	cat, _, teams := loadCatalogScoped(fsys, catalog.LocalOnly, c)
	return cat, teams
}

// loadCatalogScoped is the same composition at a caller-chosen scope, also
// returning the resolved graph.
//
// build needs both: spec §7.1 makes a dangling reference a hard failure
// under build and a recorded-and-skipped one under validate, because a
// service repo's CI cannot see entities defined elsewhere. And the portal's
// dependency pages are drawn from the Graph, which is a value produced BY
// Resolve rather than state on Catalog (spec §3.1).
func loadCatalogScoped(fsys fs.FS, scope catalog.Scope, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	paths := patternsFor(fsys, c)
	found, err := discover.Find(fsys, paths)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files: %v", discover.Filename, err),
		})
		return nil, nil, nil
	}
	if len(found) == 0 {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			File:     "repos.yaml",
			Line:     1,
			Check:    "no-entities",
			Message: fmt.Sprintf("no %s found under any configured path (%s)",
				discover.Filename, strings.Join(paths, ", ")),
			Hint: "add a repos.yaml listing the paths your services live under",
		})
	}
	files := discover.Load(fsys, found, c)

	validator, err := schema.Default()
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "schema", Line: 1,
			Check:   "schema-compile",
			Message: fmt.Sprintf("cannot compile the embedded schema: %v", err),
		})
		return nil, nil, nil
	}
	for _, f := range files {
		validator.Validate("", f.Path, f.Data, c)
	}

	cat := catalog.NewCatalog(catalog.ParseAll(localRepoName(fsys), files, c), c)
	catalog.CheckFiles(fsys, cat, c)
	g := cat.Resolve(scope, c)
	reportCycles(cat, g, c)

	teamsData, err := fs.ReadFile(fsys, "teams.yaml")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			Check:   "teams-missing",
			Message: "teams.yaml not found, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		})
		return nil, nil, nil
	}
	teams := config.LoadTeams("teams.yaml", teamsData, c)
	teams.ValidateOwners(cat, c)
	return cat, g, teams
}
```

- [ ] **Step 8: Add the notice banner to the renderer**

In `internal/render/model.go`, add `Notice` to `Input` and to `Page`, and copy it in `newPage`:

```go
	// Notice is a degraded-mode banner stamped into every page. Spec §12:
	// a portal quietly missing three services is worse than no portal, so
	// the degradation must be visible in the artifact and not only in a log.
	Notice string
```

In `newPage`, add `Notice: in.Notice,` to the returned `Page`.

In `internal/render/web/templates/base.html`, immediately after `<main>`:

```html
{{with .Notice}}<div class="notice" role="status">{{.}}</div>{{end}}
```

And in `internal/render/web/static/style.css`:

```css
.notice {
  border: 1px solid var(--warn); border-left-width: 3px;
  background: #fff8e6; color: var(--warn);
  padding: .5rem .75rem; margin: 0 0 1rem;
}
```

- [ ] **Step 9: Write the build command**

Create `cmd/landsraad/build.go`:

```go
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/render"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// BuildOptions is everything build needs that is not the filesystem. Now,
// LastEdit and Version are values so the whole command is reproducible from
// its inputs and testable without a clock or a git repository.
// The output directory is deliberately absent: Build produces values and
// has no opinion about where they land. writeSite takes it instead.
type BuildOptions struct {
	Mermaid  render.Mermaid
	Now      time.Time
	LastEdit scorecard.LastEditFunc
	Version  string
	Force    bool
}

// Build renders the portal, returning the files and an exit code.
//
// It writes nothing. writeSite is the disk half and lives beside it in cmd/,
// which is what lets the entire build be tested against an fstest.MapFS —
// and what lets `serve --watch` rebuild in memory without ever touching the
// output directory.
func Build(fsys fs.FS, errOut io.Writer, opts BuildOptions) ([]emit.File, int) {
	var c diag.Collector

	// FullCatalog, not LocalOnly: build claims to render the whole catalog,
	// so a dangling reference is a hard failure (spec §7.1).
	cat, g, teams := loadCatalogScoped(fsys, catalog.FullCatalog, &c)
	if cat == nil || c.HasErrors() {
		for _, d := range c.Diagnostics() {
			fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message)
		}
		fmt.Fprintf(errOut, "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n")
		return nil, exitValidation
	}

	std := standardsFor(fsys, errOut)
	reported := scorecard.Ingest(fsys, cat, std.StaleAfterDays(), opts.Now, &c)
	sc := scorecard.Score(cat, std, reported, scorecard.Env{
		FS:             fsys,
		Now:            opts.Now,
		MaxDocsAgeDays: std.Param("docs-fresh", "maxAgeDays", 180),
		LastEdit:       opts.LastEdit,
	}, &c)

	history, _ := fs.ReadFile(fsys, scorecard.HistoryPath)

	in := render.Input{
		Catalog: cat, Graph: g, Teams: teams,
		Scorecard: sc, Standards: std, History: history,
		FS: fsys, Mermaid: opts.Mermaid,
		GeneratedAt: opts.Now, Version: opts.Version,
		Notice: partialNotice(fsys, errOut),
	}
	files := render.Site(in, &c)

	for _, d := range c.Diagnostics() {
		fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message)
	}
	if c.HasErrors() {
		fmt.Fprintf(errOut, "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n")
		return nil, exitValidation
	}
	fmt.Fprintf(errOut, "ok: %s rendered\n", plural(len(files), "file", "files"))
	return files, exitOK
}

// partialNotice reports the repositories repos.yaml names that this build
// could not read, because fetching them is Plan 4 (ruling R22).
//
// It returns the banner text and writes the warning. Silently rendering a
// portal that covers one repository of three is precisely spec §12's "worse
// than rendering nothing".
func partialNotice(fsys fs.FS, errOut io.Writer) string {
	data, err := fs.ReadFile(fsys, "repos.yaml")
	if err != nil {
		return ""
	}
	var discard diag.Collector
	r := config.LoadRepos("repos.yaml", data, &discard)
	if len(r.Repos) < 2 {
		return ""
	}
	var missing []string
	for _, repo := range r.Repos[1:] {
		missing = append(missing, path.Base(strings.TrimSuffix(repo.URL, "/")))
	}
	// plural() prepends the count, which reads wrong here ("1 edge-gateway
	// is missing"). The subject is a list of names, so the verb agrees with
	// how many names there are and the count appears once, earlier.
	subject, verb := strings.Join(missing, ", "), "are"
	if len(missing) == 1 {
		verb = "is"
	}
	fmt.Fprintf(errOut, "warn: repos.yaml lists %d repositories and this build read only the local one; %s %s missing from the portal\n",
		len(r.Repos), subject, verb)
	return fmt.Sprintf("This portal read only the local repository. %s %s not included; fetching remote repositories is not implemented yet.",
		subject, verb)
}

// mermaidFor resolves --mermaid-src (ruling R13).
func mermaidFor(src string) (render.Mermaid, error) {
	switch {
	case src == "":
		return render.Mermaid{
			Src:       render.DefaultMermaidSrc,
			Integrity: render.DefaultMermaidIntegrity,
		}, nil
	case src == "none":
		// No diagram renderer at all. The pages still emit their Mermaid
		// source; assets/mermaid.js marks it unavailable rather than
		// leaving a blank space (spec §12).
		return render.Mermaid{}, nil
	case strings.HasPrefix(src, "https://"), strings.HasPrefix(src, "http://"):
		// A user-supplied URL carries no integrity hash: we do not know it,
		// and inventing one would block the very file they asked for.
		return render.Mermaid{Src: src}, nil
	default:
		data, err := os.ReadFile(src)
		if err != nil {
			return render.Mermaid{}, fmt.Errorf("cannot read the Mermaid bundle %s: %w", src, err)
		}
		return render.Mermaid{Src: render.LocalMermaidPath, Data: data}, nil
	}
}

func newBuildCmd() *cobra.Command {
	var (
		out        string
		mermaidSrc string
		force      bool
	)
	cmd := &cobra.Command{
		Use:   "build [root]",
		Short: "Render the static portal",
		Long: "Render the catalog, the scorecard and every entity's documentation " +
			"into a static site.\n\n" +
			"Diagrams load Mermaid from a pinned CDN URL with an integrity hash. " +
			"On a host with no outbound network, pass --mermaid-src with a path to " +
			"a local mermaid.min.js and it is copied into the site, or --mermaid-src " +
			"none to leave the diagrams unrendered.",
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
			mermaid, err := mermaidFor(mermaidSrc)
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			opts := BuildOptions{
				Mermaid: mermaid,
				Now:     time.Now().UTC(), LastEdit: gitLastEdit(resolved),
				Version: version(), Force: force,
			}
			files, code := Build(os.DirFS(resolved), cmd.ErrOrStderr(), opts)
			if code != exitOK {
				os.Exit(code)
			}
			if err := writeSite(out, files, force, cmd.ErrOrStderr()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "ok: portal written to %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "dist", "output directory")
	cmd.Flags().StringVar(&mermaidSrc, "mermaid-src", "",
		"Mermaid bundle: a URL, a path to a local file, or \"none\"")
	cmd.Flags().BoolVar(&force, "force", false,
		"write into a non-empty directory landsraad did not create")
	return cmd
}
```

- [ ] **Step 10: Register the command**

In `cmd/landsraad/main.go`, after `root.AddCommand(newScoreCmd())`:

```go
	root.AddCommand(newBuildCmd())
```

- [ ] **Step 11: Run the tests**

Run: `go test ./... -v 2>&1 | tail -40`
Expected: PASS everywhere. The existing `gen` and `score` tests must still pass — `loadCatalog` kept its signature.

- [ ] **Step 12: Build a real portal and look at it**

```bash
go build -o bin/landsraad ./cmd/landsraad
./bin/landsraad build testdata/monorepo-ok -o /tmp/landsraad-portal
ls -R /tmp/landsraad-portal
open /tmp/landsraad-portal/index.html   # or xdg-open
```

Expected: a catalog page listing `ledger-api`, `payments-worker` and `payments-events`, with working links to team pages that do not exist yet (Task 10 adds them) and entity pages that do not exist yet (Task 7 adds them). **Broken links at this point are expected**; every later task closes some of them.

- [ ] **Step 13: Commit**

```bash
git add cmd/landsraad/build.go cmd/landsraad/build_test.go cmd/landsraad/manifest.go cmd/landsraad/manifest_test.go cmd/landsraad/gen.go cmd/landsraad/main.go internal/render/model.go internal/render/web/templates/base.html internal/render/web/static/style.css
git commit -m "feat: landsraad build

Build returns the site as values and writes nothing; writeSite is the
disk half. That split keeps the whole build testable against an
fstest.MapFS and is what serve --watch will reuse.

build resolves FullCatalog, so a dangling reference is a hard failure —
validate records and skips one because a service repo cannot see entities
defined elsewhere (spec §7.1).

A manifest records what each build wrote, so a rebuild removes the page
of a deleted service. A non-empty directory with no manifest is refused:
\`build -o .\` must not be able to delete a repository."
```

---

## Task 7: The entity page

**Files:**
- Create: `internal/render/entity.go`
- Create: `internal/render/entity_test.go`
- Create: `internal/render/web/templates/entity.html`
- Create: `internal/render/web/static/runtime.js`
- Modify: `internal/render/site.go` (emit the entity pages)
- Modify: `internal/render/web/templates/base.html` (load `runtime.js`)
- Modify: `internal/render/web/static/style.css`

**Interfaces:**
- Consumes: `Input`, `newPage`, `EntityURL`, `EntityPath`, `TeamURL`, `Slug`, `scoresByRef`.
- Produces:
  - `type render.EntityView struct{…}`, `type render.ResultView struct{…}`, `type render.RefLink struct{…}`
  - `func render.entityPages(in Input, c *diag.Collector) []emit.File`

**Context:** Spec §10: "Service — header, links, runtime badge, Mermaid dependency graph, scorecard, then rendered `docs/`". This task does the header, the links, the runtime badge and the scorecard. Task 8 adds the graph, Task 9 the documentation.

**The six statuses are the point.** `scorecard.Status` has six values, not two, and Plan 2's package comment says why: *not-reported* and *stale* are failures of the **evidence**, not of the service, and rendering them as `fail` sends the owner hunting for a problem in the wrong place. The template must give each its own class and its own words.

**Ruling R20, the runtime badge.** v1 does not produce `runtime.json`, so this badge degrades on every page today. Build it anyway: sub-project E must require *no portal change*, and the only way to know that claim is true is to ship the consumer now. The badge's server-rendered text is `runtime unknown`; the script only ever replaces it on a successful fetch.

- [ ] **Step 1: Write the failing test**

Create `internal/render/entity_test.go`:

```go
package render

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestEntityPageIsWrittenAtItsRefURL(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(twoEntities(t), &c))
	for _, want := range []string{
		"entity/service/ledger-api/index.html",
		"entity/topic/payments-events/index.html",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s; got %v", want, keys(files))
		}
	}
}

func TestEntityPageShowsOwnerTierAndLifecycle(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	for _, want := range []string{"ledger-api", "team-payments", "production"} {
		if !strings.Contains(page, want) {
			t.Errorf("entity page is missing %q:\n%s", want, page)
		}
	}
	if !strings.Contains(page, `href="../../../team/team-payments/"`) {
		t.Errorf("the owner must link to the team page:\n%s", page)
	}
}

// Ruling R20. The badge is inert until sub-project E ships, and that is the
// point: E must require no portal change.
func TestEntityPageCarriesADegradedRuntimeBadge(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, `data-ref="service:ledger-api"`) {
		t.Errorf("the runtime badge must name its ref so runtime.json can key on it:\n%s", page)
	}
	if !strings.Contains(page, "runtime unknown") {
		t.Errorf("the badge's server-rendered text must be the degraded one:\n%s", page)
	}
}

// scorecard.Status has six values, not two. "not reported" and "stale" are
// failures of the evidence, not of the service; rendering them as fail sends
// the owner hunting for a problem in the wrong place.
func TestEntityScorecardDistinguishesTheSixStatuses(t *testing.T) {
	var c diag.Collector
	// runbook-present fails (no spec.runbook), and every external check is
	// not-reported because there is no .landsraad/checks directory.
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, `class="status status-fail"`) {
		t.Errorf("a failing hermetic check must render as fail:\n%s", page)
	}
	if !strings.Contains(page, `class="status status-not-reported"`) {
		t.Errorf("an external check with no result must render as not-reported:\n%s", page)
	}
	if strings.Contains(page, "status-pass\">not") {
		t.Error("not-reported must never be rendered as a pass")
	}
}

// "fail" is useless; "spec.runbook is unset" fixes itself. Plan 2 put the
// sentence in Result.Detail precisely so the portal could show it.
func TestEntityScorecardShowsTheDetailNotJustTheVerdict(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, "spec.runbook") {
		t.Errorf("the check detail must appear, not only the status:\n%s", page)
	}
}

func TestEntityPageRendersItsLinks(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Links = []catalog.Link{{Title: "Dashboard", URL: "https://grafana/d/api", Type: "dashboard"}}
	e.Spec.Oncall = "https://pagerduty/schedules/PAY"
	e.Spec.RepoURL = "https://github.com/org/monorepo/tree/main/services/api"

	var c diag.Collector
	page := string(siteMap(Site(input(t, nil, e), &c))["entity/service/api/index.html"])
	for _, want := range []string{
		`href="https://grafana/d/api"`, "Dashboard",
		`href="https://pagerduty/schedules/PAY"`,
		`href="https://github.com/org/monorepo/tree/main/services/api"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("entity page is missing %q:\n%s", want, page)
		}
	}
}

func TestEntityPageRendersSLOs(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.SLO = []catalog.SLO{{Name: "settle-latency-p99", Target: "500ms", Window: "30d"}}

	var c diag.Collector
	page := string(siteMap(Site(input(t, nil, e), &c))["entity/service/api/index.html"])
	for _, want := range []string{"settle-latency-p99", "500ms", "30d"} {
		if !strings.Contains(page, want) {
			t.Errorf("entity page is missing SLO field %q:\n%s", want, page)
		}
	}
}

// An untiered entity is not scored (Plan 2, ruling R1). Its page shows no
// scorecard section rather than an empty one implying zero.
func TestAnUntieredEntityPageHasNoScorecardSection(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/topic/payments-events/index.html"])
	if strings.Contains(page, `id="scorecard"`) {
		t.Errorf("an entity that was never scored must not show a scorecard:\n%s", page)
	}
	if !strings.Contains(page, "not scored") {
		t.Errorf("it must say so instead:\n%s", page)
	}
}

func TestEntityPageIsGolden(t *testing.T) {
	e := ent("ledger-api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/ledger-api/runbook.md"
	e.Spec.Links = []catalog.Link{{Title: "Dashboard", URL: "https://grafana/d/led", Type: "dashboard"}}
	files := fstest.MapFS{
		"services/ledger-api/runbook.md": {Data: []byte("# Runbook\n\nRestart carefully.\n")},
	}
	var c diag.Collector
	got := siteMap(Site(input(t, files, e), &c))["entity/service/ledger-api/index.html"]
	golden(t, "entity-service-ledger-api.html", got)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/render/ -run TestEntity -v`
Expected: FAIL — no entity pages are emitted.

- [ ] **Step 3: Write the entity view**

Create `internal/render/entity.go`:

```go
package render

import (
	"sort"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// RefLink is one edge of the dependency graph, ready to render.
type RefLink struct {
	Ref string
	// URL is empty when the target is not in the catalog. Under FullCatalog
	// that cannot happen — a dangling ref already failed the build — but
	// Resolve is also called at LocalOnly scope, and a link to a page that
	// was never generated is a 404 the portal invented for itself.
	URL string
}

// ResultView is one scorecard row.
type ResultView struct {
	Check  string
	Status string
	// Class is the status with its spaces already gone, for the CSS hook.
	// Doing it here rather than in the template keeps the vocabulary in Go,
	// where a renamed Status is a compile error.
	Class    string
	Detail   string
	URL      string
	Severity string
}

// EntityView is one entity's page.
type EntityView struct {
	Page
	Ref         string
	Name        string
	Kind        string
	Description string
	Owner       string
	OwnerURL    string
	Tier        int
	Lifecycle   string
	Language    string
	Type        string
	RepoURL     string
	Oncall      string
	SourcePath  string
	Tags        []string
	Links       []catalog.Link
	SLO         []catalog.SLO
	Aliases     []string
	Labels      []Pair
	Annotations []Pair
	// Score is nil when the entity was not scored at all (Plan 2, R1).
	Score      *float64
	Passed     int
	Applicable int
	Results    []ResultView
	DependsOn  []RefLink
	Dependents []RefLink
}

// Pair is a sorted key/value, so labels and annotations render in a stable
// order rather than Go's randomised map order.
type Pair struct{ Key, Value string }

func pairs(m map[string]string) []Pair {
	out := make([]Pair, 0, len(m))
	for k, v := range m {
		out = append(out, Pair{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// statusClass turns a Status into a CSS class: "not-reported" already has
// the shape, and every value in the vocabulary is lower-case and hyphenated.
func statusClass(s scorecard.Status) string { return "status-" + string(s) }

// entityScores indexes the scorecard by ref.
func entityScores(sc *scorecard.Scorecard) map[catalog.Ref]scorecard.EntityScore {
	out := map[catalog.Ref]scorecard.EntityScore{}
	if sc == nil {
		return out
	}
	for _, e := range sc.Entities {
		out[e.Ref] = e
	}
	return out
}

// refLinks turns resolved edges into links, in the order Graph returned them
// (already sorted).
func refLinks(in Input, refs []catalog.Ref) []RefLink {
	out := make([]RefLink, 0, len(refs))
	for _, r := range refs {
		l := RefLink{Ref: r.String()}
		if _, ok := in.Catalog.Lookup(r); ok {
			l.URL = EntityURL(r)
		}
		out = append(out, l)
	}
	return out
}

// entityView builds one page's data.
func entityView(in Input, e *catalog.Entity, slugs map[string]string, scores map[catalog.Ref]scorecard.EntityScore) EntityView {
	ref := e.Ref()
	out := EntityView{
		Page:        newPage(in, EntityPath(ref), e.Metadata.Name, "catalog"),
		Ref:         ref.String(),
		Name:        e.Metadata.Name,
		Kind:        string(e.Kind),
		Description: e.Metadata.Description,
		Owner:       e.Metadata.Owner,
		Tier:        e.Metadata.Tier,
		Lifecycle:   e.Metadata.Lifecycle,
		Language:    e.Spec.Language,
		Type:        e.Spec.Type,
		RepoURL:     e.Spec.RepoURL,
		Oncall:      e.Spec.Oncall,
		SourcePath:  e.SourcePath,
		Tags:        e.Metadata.Tags,
		Links:       e.Spec.Links,
		SLO:         e.Spec.SLO,
		Aliases:     e.Metadata.Aliases,
		Labels:      pairs(e.Metadata.Labels),
		Annotations: pairs(e.Metadata.Annotations),
		DependsOn:   refLinks(in, in.Graph.DependsOn(ref)),
		Dependents:  refLinks(in, in.Graph.Dependents(ref)),
	}
	if slug, ok := slugs[e.Metadata.Owner]; ok {
		out.OwnerURL = TeamURL(slug)
	}
	if es, ok := scores[ref]; ok {
		score := es.Score()
		out.Score = &score
		out.Passed, out.Applicable = es.Passed, es.Applicable
		for _, r := range es.Results {
			out.Results = append(out.Results, ResultView{
				Check:  r.Check,
				Status: string(r.Status),
				Class:  statusClass(r.Status),
				Detail: r.Detail,
				URL:    r.URL,
				Severity: string(in.Standards.Severity(r.Check, e.Metadata.Tier)),
			})
		}
	}
	return out
}

// entityPages renders one page per entity.
func entityPages(in Input, c *diag.Collector) []emit.File {
	t, err := templateSet("entity.html")
	if err != nil {
		c.Add(templateCompileError("entity.html", err))
		return nil
	}
	slugs := teamSlugMap(in)
	scores := entityScores(in.Scorecard)

	var out []emit.File
	for _, e := range in.Catalog.Entities() {
		view := entityView(in, e, slugs, scores)
		if f, ok := renderPage(t, EntityPath(e.Ref()), view, c); ok {
			out = append(out, f)
		}
	}
	return out
}

// teamSlugMap is the non-reporting slug lookup the page builders share.
// Site calls TeamSlugs once for the diagnostics; this is for the links.
func teamSlugMap(in Input) map[string]string {
	out := map[string]string{}
	for _, name := range in.Teams.Names() {
		if s, ok := Slug(name); ok {
			out[name] = s
		}
	}
	return out
}
```

- [ ] **Step 4: Write the entity template**

Create `internal/render/web/templates/entity.html`:

```html
{{define "content"}}
<article class="entity">
<header class="entity-head">
  <h1>{{.Name}}</h1>
  <p class="meta">
    <span class="kind kind-{{lower .Kind}}">{{.Kind}}</span>
    <span class="mono">{{.Ref}}</span>
    <span class="runtime" data-ref="{{.Ref}}">runtime unknown</span>
  </p>
  {{with .Description}}<p class="desc">{{.}}</p>{{end}}
</header>

<dl class="facts">
  <dt>Owner</dt><dd>{{if .OwnerURL}}<a href="{{.Root}}{{.OwnerURL}}">{{.Owner}}</a>{{else}}{{.Owner}}{{end}}</dd>
  <dt>Tier</dt><dd>{{if .Tier}}{{.Tier}}{{else}}<span class="none">not applicable to this kind</span>{{end}}</dd>
  <dt>Lifecycle</dt><dd>{{.Lifecycle}}</dd>
  {{with .Language}}<dt>Language</dt><dd>{{.}}</dd>{{end}}
  {{with .Type}}<dt>Type</dt><dd>{{.}}</dd>{{end}}
  {{with .Oncall}}<dt>On-call</dt><dd><a href="{{.}}">{{.}}</a></dd>{{end}}
  {{with .RepoURL}}<dt>Source</dt><dd><a href="{{.}}">{{.}}</a></dd>{{end}}
  <dt>Defined in</dt><dd class="mono">{{.SourcePath}}</dd>
  {{with .Aliases}}<dt>Aliases</dt><dd>{{range .}}<span class="mono">{{.}}</span> {{end}}</dd>{{end}}
  {{with .Tags}}<dt>Tags</dt><dd>{{range .}}<span class="tag">{{.}}</span> {{end}}</dd>{{end}}
</dl>

{{with .Links}}
<h2>Links</h2>
<ul class="links">
{{range .}}<li><a href="{{.URL}}">{{.Title}}</a>{{with .Type}} <span class="none">{{.}}</span>{{end}}</li>
{{end}}</ul>
{{end}}

{{with .SLO}}
<h2>Service level objectives</h2>
<table><thead><tr><th>Name</th><th>Target</th><th>Window</th></tr></thead><tbody>
{{range .}}<tr><td>{{.Name}}</td><td class="mono">{{.Target}}</td><td class="mono">{{.Window}}</td></tr>
{{end}}</tbody></table>
{{end}}

{{if .Score}}
<h2 id="scorecard">Scorecard <span class="mono">{{pct .Score}}</span> <span class="none">({{.Passed}}/{{.Applicable}})</span></h2>
<table class="results"><thead><tr><th>Check</th><th>Status</th><th>Severity</th><th>Detail</th></tr></thead><tbody>
{{range .Results}}
<tr>
  <td class="mono">{{.Check}}</td>
  <td><span class="status {{.Class}}">{{.Status}}</span></td>
  <td>{{.Severity}}</td>
  <td>{{.Detail}}{{with .URL}} <a href="{{.}}">evidence</a>{{end}}</td>
</tr>
{{end}}</tbody></table>
{{else}}
<h2>Scorecard</h2>
<p class="none">This entity has no tier, so it is not scored. Tiers apply to Service, Worker, Cron and API — the kinds that can page someone.</p>
{{end}}

{{if or .DependsOn .Dependents}}
<h2>Dependencies</h2>
<div class="deps">
  <div>
    <h3>Depends on</h3>
    {{if .DependsOn}}<ul>{{range .DependsOn}}<li>{{if .URL}}<a href="{{$.Root}}{{.URL}}">{{.Ref}}</a>{{else}}<span class="mono">{{.Ref}}</span>{{end}}</li>{{end}}</ul>
    {{else}}<p class="none">nothing</p>{{end}}
  </div>
  <div>
    <h3>Depended on by</h3>
    {{if .Dependents}}<ul>{{range .Dependents}}<li>{{if .URL}}<a href="{{$.Root}}{{.URL}}">{{.Ref}}</a>{{else}}<span class="mono">{{.Ref}}</span>{{end}}</li>{{end}}</ul>
    {{else}}<p class="none">nothing</p>{{end}}
  </div>
</div>
{{end}}
</article>
{{end}}
```

- [ ] **Step 5: Write the runtime client**

Create `internal/render/web/static/runtime.js`:

```js
// Spec §10: the page fetches runtime.json client-side and degrades to
// "runtime unknown" when it is absent.
//
// v1 does not produce that file — the runtime agent is sub-project E — so
// today this ALWAYS degrades, and that is the point. E must require no
// portal change, and the only way to know the claim is true is to ship the
// consumer before the producer.
//
// The badge already reads "runtime unknown" from the server. This script
// only ever replaces that text on a successful fetch: a network error must
// not turn a truthful "unknown" into a blank space.
(function () {
  var script = document.currentScript;
  var root = (script && script.getAttribute('data-root')) || '';
  var badges = document.querySelectorAll('.runtime[data-ref]');
  if (!badges.length) { return; }

  fetch(root + 'runtime.json', { cache: 'no-store' })
    .then(function (r) { return r.ok ? r.json() : null; })
    .then(function (data) {
      if (!data) { return; }
      badges.forEach(function (el) {
        var info = data[el.getAttribute('data-ref')];
        if (!info || !info.status) { return; }
        el.textContent = info.status;
        el.classList.add('runtime-known');
      });
    })
    .catch(function () {
      // Absent is the expected case in v1. The badge already says so.
    });
})();
```

- [ ] **Step 6: Load it from the base template**

In `internal/render/web/templates/base.html`, immediately before `</body>`:

```html
<script src="{{.Root}}assets/runtime.js" data-root="{{.Root}}"></script>
```

- [ ] **Step 7: Emit the pages**

In `internal/render/site.go`, inside `Site`, after the catalog page:

```go
	files = append(files, entityPages(in, c)...)
```

And immediately before it, so the slug diagnostics are reported exactly once:

```go
	// Reported here, once. The page builders use teamSlugMap for their
	// links; this call is what turns a collision into a build failure.
	TeamSlugs(in.Teams, c)
```

- [ ] **Step 8: Extend the stylesheet**

Append to `internal/render/web/static/style.css`:

```css
.entity-head .meta { display: flex; gap: .75rem; align-items: baseline; color: var(--muted); margin: .25rem 0; }
.entity-head .desc { margin: .5rem 0 1rem; }
.runtime { font-size: 12px; padding: .1rem .35rem; border: 1px dashed var(--line); border-radius: 3px; }
.runtime-known { border-style: solid; border-color: var(--good); color: var(--good); }
dl.facts { display: grid; grid-template-columns: 10rem 1fr; gap: .3rem 1rem; margin: 1rem 0; }
dl.facts dt { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: .04em; }
dl.facts dd { margin: 0; }
.tag { font-size: 12px; padding: .05rem .3rem; background: #f2f4f7; border-radius: 3px; }
ul.links { list-style: none; padding: 0; }
ul.links li { padding: .15rem 0; }
.deps { display: grid; grid-template-columns: 1fr 1fr; gap: 2rem; }
.deps h3 { font-size: .95rem; margin: .5rem 0 .25rem; }
.deps ul { list-style: none; padding: 0; margin: 0; }
.status { font-size: 12px; }
```

- [ ] **Step 9: Run the tests and regenerate the goldens**

```bash
go test ./internal/render/ -run TestEntity -v
go test ./internal/render/ -update
git diff --stat internal/render/testdata/golden/
```

Expected: PASS. The `index.html` golden is unchanged; `entity-service-ledger-api.html` is new. **Read the new golden file before committing it.**

- [ ] **Step 10: Look at a real page**

```bash
go build -o bin/landsraad ./cmd/landsraad
./bin/landsraad build testdata/monorepo-ok -o /tmp/landsraad-portal
open /tmp/landsraad-portal/entity/service/ledger-api/index.html
```

Expected: the header, the facts, the scorecard with a mix of `fail` and `not-reported`, and a dashed "runtime unknown" badge.

- [ ] **Step 11: Commit**

```bash
git add internal/render/entity.go internal/render/entity_test.go internal/render/web/templates/entity.html internal/render/web/templates/base.html internal/render/web/static/runtime.js internal/render/web/static/style.css internal/render/site.go internal/render/testdata
git commit -m "feat: the entity page — facts, links, SLOs, scorecard, runtime badge

The scorecard table renders all six statuses distinctly. not-reported and
stale are failures of the evidence, not of the service, and colouring
them as failures sends the owner hunting in the wrong place.

The runtime badge is inert until sub-project E ships. That is deliberate:
E must require no portal change, and shipping the consumer first is the
only way to know that is true."
```

---

## Task 8: Dependency graphs — the neighbourhood and the system map

**Files:**
- Create: `internal/render/graph.go`
- Create: `internal/render/graph_test.go`
- Create: `internal/render/web/templates/map.html`
- Create: `internal/render/web/static/mermaid.js`
- Modify: `internal/render/entity.go` (`EntityView.Diagram`)
- Modify: `internal/render/web/templates/entity.html` (render it)
- Modify: `internal/render/web/templates/base.html` (load Mermaid)
- Modify: `internal/render/site.go` (emit `map/index.html`)
- Modify: `internal/render/web/static/style.css`

**Interfaces:**
- Consumes: `Input`, `catalog.Graph`, `EntityURL`.
- Produces:
  - `func render.neighbourhood(in Input, ref catalog.Ref) string` — Mermaid source
  - `func render.systemGraph(in Input) (source string, edges []Edge, capped bool)`
  - `type render.Edge struct { From, To RefLink }`
  - `type render.MapPage struct{…}`
  - `const render.SystemMapCap = 60`

**Context:** Spec §10 asks for a Mermaid dependency graph on the service page and a whole-system map.

**Node ids are generated, never the ref.** A Mermaid node id may not contain `:`, and every landsraad ref does. So nodes are `n0`, `n1`, … with the ref as a quoted label. The label needs no escaping: a ref is `kind:name` and the schema constrains a name to `[a-z0-9._-]`, so it can never contain a quote — stated here because the *reason* it is safe is a schema constraint two packages away, and a future widening of that pattern silently makes this an injection into the diagram source.

**The diagram is not the navigation.** Mermaid's `click` directive is deliberately unused. `securityLevel: 'strict'` is what spec §14.1 requires, its exact handling of `click` targets is a property of a CDN-loaded third-party bundle, and a portal whose only route to a dependency is a JavaScript click handler is unusable when that bundle does not load. Task 7 already renders "Depends on" and "Depended on by" as plain `<a>` lists. The diagram shows shape; the lists are how you get there.

**Ruling R19, the cap.** Above `SystemMapCap` entities the map page renders the edge list as a table and says why, naming the count and the cap. A 300-node Mermaid graph the browser spends forty seconds laying out is worse than a table that loads.

**Ruling R13, when Mermaid is absent.** `--mermaid-src none`, a blocked CDN or a failed SRI check all end with `window.mermaid` undefined. `mermaid.js` then labels every diagram block *diagram not rendered* instead of leaving a wall of raw Mermaid source on the page. Degraded mode, visible in the artifact.

- [ ] **Step 1: Write the failing test**

Create `internal/render/graph_test.go`:

```go
package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// linked builds a catalog where service:api depends on topic:events.
func linked(t *testing.T) Input {
	t.Helper()
	api := ent("api", catalog.KindService, "team-payments", 1)
	api.Spec.DependsOn = []string{"topic:events"}
	return input(t, nil, api, ent("events", catalog.KindTopic, "team-payments", 0))
}

// A Mermaid node id may not contain ":", and every landsraad ref does.
func TestNeighbourhoodUsesGeneratedNodeIDs(t *testing.T) {
	in := linked(t)
	src := neighbourhood(in, catalog.Ref{Kind: catalog.KindService, Name: "api"})
	if !strings.HasPrefix(src, "graph LR\n") {
		t.Errorf("expected a left-to-right flowchart, got:\n%s", src)
	}
	if !strings.Contains(src, `n0["service:api"]`) {
		t.Errorf("the subject must be node n0 with its ref as the label:\n%s", src)
	}
	if !strings.Contains(src, "-->") {
		t.Errorf("the edge is missing:\n%s", src)
	}
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "["); i > 0 && strings.Contains(line[:i], ":") {
			t.Errorf("a raw ref leaked into a node id: %q", line)
		}
	}
}

func TestNeighbourhoodCoversBothDirections(t *testing.T) {
	in := linked(t)
	src := neighbourhood(in, catalog.Ref{Kind: catalog.KindTopic, Name: "events"})
	if !strings.Contains(src, `"service:api"`) {
		t.Errorf("the topic's page must show its consumer:\n%s", src)
	}
}

// An entity with no edges gets no diagram. An empty "graph LR" renders as a
// blank box, which reads as a broken page rather than as "no dependencies".
func TestAnIsolatedEntityHasNoDiagram(t *testing.T) {
	in := input(t, nil, ent("alone", catalog.KindService, "team-payments", 1))
	if src := neighbourhood(in, catalog.Ref{Kind: catalog.KindService, Name: "alone"}); src != "" {
		t.Errorf("expected no diagram, got:\n%s", src)
	}
}

func TestSystemGraphRendersEveryEdge(t *testing.T) {
	src, edges, capped := systemGraph(linked(t))
	if capped {
		t.Error("two entities must not trip the cap")
	}
	if len(edges) != 1 {
		t.Fatalf("got %d edges, want 1: %+v", len(edges), edges)
	}
	if edges[0].From.Ref != "service:api" || edges[0].To.Ref != "topic:events" {
		t.Errorf("edge = %+v", edges[0])
	}
	if !strings.Contains(src, "-->") {
		t.Errorf("no edge in the source:\n%s", src)
	}
}

// Ruling R19: above the cap the map is a table, and the page says why.
func TestSystemGraphIsCappedOnALargeCatalog(t *testing.T) {
	var entities []*catalog.Entity
	for i := 0; i <= SystemMapCap; i++ {
		entities = append(entities, ent(fmt.Sprintf("svc-%03d", i), catalog.KindService, "team-payments", 3))
	}
	src, _, capped := systemGraph(input(t, nil, entities...))
	if !capped {
		t.Errorf("%d entities must trip the cap of %d", len(entities), SystemMapCap)
	}
	if src != "" {
		t.Error("a capped map emits no Mermaid source")
	}
}

func TestMapPageExplainsTheCap(t *testing.T) {
	var entities []*catalog.Entity
	for i := 0; i <= SystemMapCap; i++ {
		entities = append(entities, ent(fmt.Sprintf("svc-%03d", i), catalog.KindService, "team-payments", 3))
	}
	var c diag.Collector
	page := string(siteMap(Site(input(t, nil, entities...), &c))["map/index.html"])
	want := fmt.Sprintf("This catalog has %d entities, above the %d-entity limit for a readable diagram.", len(entities), SystemMapCap)
	if !strings.Contains(page, want) {
		t.Errorf("the map page must say why it is a table:\n%s", page)
	}
}

// html/template escapes "+" to "&#43;" inside an attribute — Go's deliberate
// UTF-7 defence, not a bug. The HTML parser decodes it back before the
// browser computes the SRI check, so the hash still matches.
//
// Pinned here so nobody "fixes" it by declaring Integrity a
// template.HTMLAttr, which turns off escaping on an attribute that
// --mermaid-src lets a user populate.
func TestSRIHashIsEscapedAndStillCorrect(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(linked(t), &c))["entity/service/api/index.html"])
	if !strings.Contains(page, `integrity="`) {
		t.Fatalf("no integrity attribute on the Mermaid script:\n%s", page)
	}
	escaped := strings.ReplaceAll(DefaultMermaidIntegrity, "+", "&#43;")
	if !strings.Contains(page, escaped) {
		t.Errorf("expected the entity-escaped hash %q in:\n%s", escaped, page)
	}
	if strings.Contains(page, `crossorigin="anonymous"`) == false {
		t.Error("an SRI-checked cross-origin script needs crossorigin=anonymous or the check cannot run")
	}
}

func TestNoMermaidScriptWhenSrcIsEmpty(t *testing.T) {
	in := linked(t)
	in.Mermaid = Mermaid{}
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["entity/service/api/index.html"])
	if strings.Contains(page, "cdn.jsdelivr.net") {
		t.Errorf("--mermaid-src none must emit no CDN script:\n%s", page)
	}
	// The diagram source still ships; mermaid.js labels it unrendered.
	if !strings.Contains(page, `class="mermaid"`) {
		t.Errorf("the diagram block must still be present:\n%s", page)
	}
}

func TestMapPageIsGolden(t *testing.T) {
	var c diag.Collector
	golden(t, "map.html", siteMap(Site(linked(t), &c))["map/index.html"])
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/render/ -run 'TestNeighbourhood|TestSystemGraph|TestMapPage|TestAnIsolated|TestSRI|TestNoMermaid' -v`
Expected: FAIL — `undefined: neighbourhood`.

- [ ] **Step 3: Write the graph builders**

Create `internal/render/graph.go`:

```go
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// SystemMapCap is the entity count above which the whole-system map renders
// as a table instead of a diagram (ruling R19).
//
// A 300-node Mermaid flowchart is a hairball the browser spends tens of
// seconds laying out. Degrading to a table, and saying so on the page, is
// the honest answer; silently shipping the hairball is not.
const SystemMapCap = 60

// Edge is one resolved dependency, both ends ready to link.
//
// fromRef and toRef are the same edge as values. They are unexported —
// templates never see them — and they exist so the diagram builder does not
// have to parse a Ref back out of the string it just printed.
type Edge struct {
	From, To     RefLink
	fromRef      catalog.Ref
	toRef        catalog.Ref
}

// MapPage is the whole-system dependency map.
type MapPage struct {
	Page
	Diagram string
	Edges   []Edge
	Capped  bool
	Count   int
	Cap     int
}

// nodeIDs assigns a stable generated id to every ref.
//
// A Mermaid node id may not contain ":", and every landsraad ref does, so
// the id is generated and the ref becomes the label.
func nodeIDs(refs []catalog.Ref) map[catalog.Ref]string {
	sorted := append([]catalog.Ref(nil), refs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
	out := make(map[catalog.Ref]string, len(sorted))
	for i, r := range sorted {
		out[r] = fmt.Sprintf("n%d", i)
	}
	return out
}

// node renders one declaration.
//
// The label needs no escaping: a ref is kind:name and the JSON Schema
// constrains a name to ^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$, so it can never
// contain a quote. That is a constraint two packages away — widening the
// name pattern without revisiting this turns a diagram label into an
// injection into the Mermaid source.
func node(id string, r catalog.Ref) string {
	return fmt.Sprintf("  %s[\"%s\"]", id, r.String())
}

// neighbourhood is the diagram for one entity: what it depends on, and what
// depends on it. It returns "" when the entity has no edges at all — an
// empty `graph LR` renders as a blank box, which reads as a broken page
// rather than as "no dependencies".
func neighbourhood(in Input, ref catalog.Ref) string {
	deps := in.Graph.DependsOn(ref)
	users := in.Graph.Dependents(ref)
	if len(deps) == 0 && len(users) == 0 {
		return ""
	}

	all := append([]catalog.Ref{ref}, deps...)
	all = append(all, users...)
	// The subject is always n0, so the diagram's focus is identifiable in
	// the source and in a test.
	ids := map[catalog.Ref]string{ref: "n0"}
	next := 1
	for _, r := range append(append([]catalog.Ref{}, deps...), users...) {
		if _, seen := ids[r]; seen {
			continue
		}
		ids[r] = fmt.Sprintf("n%d", next)
		next++
	}

	var b strings.Builder
	b.WriteString("graph LR\n")
	declared := map[string]bool{}
	for _, r := range all {
		if declared[ids[r]] {
			continue
		}
		declared[ids[r]] = true
		b.WriteString(node(ids[r], r))
		b.WriteByte('\n')
	}
	for _, d := range deps {
		fmt.Fprintf(&b, "  %s --> %s\n", ids[ref], ids[d])
	}
	for _, u := range users {
		fmt.Fprintf(&b, "  %s --> %s\n", ids[u], ids[ref])
	}
	return b.String()
}

// systemGraph is the whole-system map: the Mermaid source, the edge list,
// and whether the catalog is too large to draw (ruling R19).
func systemGraph(in Input) (string, []Edge, bool) {
	entities := in.Catalog.Entities()

	var edges []Edge
	var refs []catalog.Ref
	for _, e := range entities {
		from := e.Ref()
		refs = append(refs, from)
		for _, to := range in.Graph.DependsOn(from) {
			edges = append(edges, Edge{
				From:    RefLink{Ref: from.String(), URL: EntityURL(from)},
				To:      RefLink{Ref: to.String(), URL: EntityURL(to)},
				fromRef: from,
				toRef:   to,
			})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From.Ref != edges[j].From.Ref {
			return edges[i].From.Ref < edges[j].From.Ref
		}
		return edges[i].To.Ref < edges[j].To.Ref
	})

	if len(entities) > SystemMapCap {
		return "", edges, true
	}

	ids := nodeIDs(refs)
	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, r := range refs {
		b.WriteString(node(ids[r], r))
		b.WriteByte('\n')
	}
	for _, e := range edges {
		fmt.Fprintf(&b, "  %s --> %s\n", ids[e.fromRef], ids[e.toRef])
	}
	return b.String(), edges, false
}

// mapPage renders the whole-system map.
func mapPage(in Input, c *diag.Collector) (emit.File, bool) {
	t, err := templateSet("map.html")
	if err != nil {
		c.Add(templateCompileError("map.html", err))
		return emit.File{}, false
	}
	src, edges, capped := systemGraph(in)
	view := MapPage{
		Page:    newPage(in, "map/index.html", "Dependencies", "map"),
		Diagram: src,
		Edges:   edges,
		Capped:  capped,
		Count:   len(in.Catalog.Entities()),
		Cap:     SystemMapCap,
	}
	return renderPage(t, "map/index.html", view, c)
}
```

- [ ] **Step 4: Write the map template**

Create `internal/render/web/templates/map.html`:

```html
{{define "content"}}
<h1>Dependencies</h1>
{{if .Capped}}
<p class="notice" role="status">This catalog has {{.Count}} entities, above the {{.Cap}}-entity limit for a readable diagram. The edges are listed below instead.</p>
{{else if .Diagram}}
<pre class="mermaid">{{.Diagram}}</pre>
{{else}}
<p class="none">No entity declares a dependency yet.</p>
{{end}}

{{with .Edges}}
<h2>Edges</h2>
<table><thead><tr><th>From</th><th>To</th></tr></thead><tbody>
{{range .}}<tr>
  <td><a href="{{$.Root}}{{.From.URL}}">{{.From.Ref}}</a></td>
  <td><a href="{{$.Root}}{{.To.URL}}">{{.To.Ref}}</a></td>
</tr>
{{end}}</tbody></table>
{{end}}
{{end}}
```

- [ ] **Step 5: Write the Mermaid initialiser**

Create `internal/render/web/static/mermaid.js`:

```js
// Spec §14.1: Mermaid renders in strict mode.
//
// The bundle may legitimately be absent — `--mermaid-src none`, a portal on
// a host with no outbound network, or a failed SRI check. In that case every
// diagram block is labelled rather than left as a wall of raw Mermaid
// source: spec §12, a degraded mode must be visible in the artifact.
(function () {
  var blocks = document.querySelectorAll('pre.mermaid');
  if (!blocks.length) { return; }

  if (!window.mermaid) {
    blocks.forEach(function (el) {
      el.classList.add('mermaid-unavailable');
      el.setAttribute('data-note', 'diagram not rendered: the Mermaid bundle did not load');
    });
    return;
  }
  window.mermaid.initialize({
    startOnLoad: true,
    securityLevel: 'strict',
    theme: 'neutral'
  });
})();
```

- [ ] **Step 6: Load Mermaid from the base template**

In `internal/render/web/templates/base.html`, before the `runtime.js` tag:

```html
{{if .Mermaid.Src}}<script src="{{.Mermaid.Src}}"{{with .Mermaid.Integrity}} integrity="{{.}}" crossorigin="anonymous"{{end}}></script>{{end}}
<script src="{{.Root}}assets/mermaid.js"></script>
```

`mermaid.js` loads unconditionally: it is what labels the blocks when the bundle is missing, so it must run in exactly the case where the other tag is absent.

- [ ] **Step 7: Put the diagram on the entity page**

In `internal/render/entity.go`, add to `EntityView`:

```go
	// Diagram is Mermaid source, empty when the entity has no edges. The
	// "Depends on" lists above are the navigation; this only shows shape.
	Diagram string
```

and in `entityView`, before the `return`:

```go
	out.Diagram = neighbourhood(in, ref)
```

In `internal/render/web/templates/entity.html`, inside the `{{if or .DependsOn .Dependents}}` block, immediately after `<h2>Dependencies</h2>`:

```html
{{with .Diagram}}<pre class="mermaid">{{.}}</pre>{{end}}
```

- [ ] **Step 8: Emit the map page**

In `internal/render/site.go`, inside `Site`:

```go
	if f, ok := mapPage(in, c); ok {
		files = append(files, f)
	}
```

- [ ] **Step 9: Extend the stylesheet**

Append to `internal/render/web/static/style.css`:

```css
pre.mermaid { background: transparent; padding: 0; }
pre.mermaid-unavailable {
  background: #f7f7f7; color: var(--muted);
  border: 1px dashed var(--line); font-size: 12px;
}
pre.mermaid-unavailable::before {
  content: attr(data-note);
  display: block; color: var(--warn); margin-bottom: .5rem;
}
```

- [ ] **Step 10: Run the tests and regenerate the goldens**

```bash
go test ./internal/render/ -v
go test ./internal/render/ -update
git diff internal/render/testdata/golden/
```

Expected: PASS. `entity-service-ledger-api.html` gains the Mermaid script tag; `map.html` is new. **Read the diff.**

- [ ] **Step 11: Look at it in a browser**

```bash
go build -o bin/landsraad ./cmd/landsraad
./bin/landsraad build testdata/monorepo-ok -o /tmp/landsraad-portal
open /tmp/landsraad-portal/map/index.html
```

Expected: a rendered flowchart. Then confirm the degraded path is honest:

```bash
./bin/landsraad build testdata/monorepo-ok -o /tmp/landsraad-nomermaid --mermaid-src none
open /tmp/landsraad-nomermaid/map/index.html
```

Expected: the same page with "diagram not rendered: the Mermaid bundle did not load" above the source, and the edge table below still fully navigable.

- [ ] **Step 12: Commit**

```bash
git add internal/render/graph.go internal/render/graph_test.go internal/render/web/templates/map.html internal/render/web/templates/entity.html internal/render/web/templates/base.html internal/render/web/static/mermaid.js internal/render/web/static/style.css internal/render/entity.go internal/render/site.go internal/render/testdata
git commit -m "feat: neighbourhood diagrams and the whole-system dependency map

Node ids are generated because a Mermaid id may not contain ':' and every
ref does. Mermaid's click directive is unused: the diagram shows shape,
the plain-anchor lists are the navigation, and they keep working when the
bundle does not load.

Above 60 entities the map is an edge table and the page says why. A
300-node flowchart the browser lays out for forty seconds is worse."
```

---

## Task 9: Documentation — `docs/` and the runbook

**Files:**
- Create: `internal/render/docs.go`
- Create: `internal/render/docs_test.go`
- Create: `internal/render/web/templates/doc.html`
- Modify: `internal/render/entity.go` (`EntityView.Docs`, `EntityView.DocNav`, `EntityView.RunbookURL`)
- Modify: `internal/render/web/templates/entity.html`
- Modify: `internal/render/site.go`
- Modify: `internal/render/web/static/style.css`

**Interfaces:**
- Consumes: `md.New`, `md.Render`, `md.Doc`, `Input.FS`.
- Produces:
  - `type render.DocLink struct { Title, URL string }`
  - `type render.RenderedDoc struct { URL, Title, Text string; Headings []md.Heading; EntityRef string }`
  - `type render.DocPage struct{…}`
  - `type render.entityDocs struct { Files []emit.File; Nav []DocLink; Index template.HTML; Docs []RenderedDoc }`
  - `func render.docsFor(in Input, e *catalog.Entity, t *template.Template, m goldmark.Markdown, c *diag.Collector) entityDocs`

**Context:** Ruling R18: `docs/index.md` renders **inline** on the entity page; every other `.md` under `spec.docs` becomes a sub-page; `spec.runbook` is always rendered and linked prominently. A service with twenty documents must not become one unreadable page.

**The link rewriter is why this is not just "render each file".** A runbook that says `[see the rollback guide](rollback.md)` works on GitHub and 404s in the portal unless `.md` becomes `.html`. `md.Render` takes the rewriter and only hands it *relative* destinations, so `https://` and `#anchor` links are untouched.

**The one `template.HTML` cast in the codebase lives here**, and it is safe for a reason that is checked by a test in Task 1: `md.New()` configures goldmark **without** `WithUnsafe`, so raw HTML in a runbook has already been escaped into text by the time these bytes exist. Do not move this cast anywhere that has not run through `md.Render`.

**Symlinks, from spec §14.1.** `io/fs` rejects absolute paths and `..`, so a `service.yaml` cannot name a file outside its repository *by path*. It says the qualifier is deliberate: `os.DirFS` does not resolve symlinks, so a symlink committed into a repository can still reach outside it, and this is the plan where the renderer first reads file **contents** rather than only stat-ing them. Given the stated trust boundary — same-organisation repositories, an internal portal, not a sandbox — that is **accepted, not defended against**. `os.Root` is the mechanism if the boundary ever changes, and it would live in `cmd/`, since `internal/` may not import `os`. Recorded so the next reader knows it was decided rather than missed.

- [ ] **Step 1: Write the failing test**

Create `internal/render/docs_test.go`:

```go
package render

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// documented builds an entity with a docs tree and a runbook.
func documented(t *testing.T) Input {
	t.Helper()
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	e.Spec.Runbook = "services/api/docs/runbook.md"
	files := fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte(
			"# API\n\nThe overview. See [the runbook](runbook.md).\n")},
		"services/api/docs/runbook.md": {Data: []byte(
			"# Runbook\n\n!!! warning \"Page the owner\"\n\n    Check lag first.\n\n## Rollback\n\nRoll back with `kubectl`.\n")},
		"services/api/docs/ops/scaling.md": {Data: []byte(
			"# Scaling\n\nSee [the index](../index.md).\n")},
	}
	return input(t, files, e)
}

func TestIndexMarkdownIsInlinedOnTheEntityPage(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(documented(t), &c))
	page := string(files["entity/service/api/index.html"])
	if !strings.Contains(page, "The overview.") {
		t.Errorf("docs/index.md must render inline on the entity page:\n%s", page)
	}
	// Inlined, not also a separate page — one document, one URL.
	if _, ok := files["entity/service/api/docs/index.html"]; ok {
		t.Error("index.md must not also become a sub-page")
	}
}

func TestOtherDocsBecomeSubPages(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(documented(t), &c))
	for _, want := range []string{
		"entity/service/api/docs/runbook.html",
		"entity/service/api/docs/ops/scaling.html",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s; got %v", want, keys(files))
		}
	}
}

// A link that works on GitHub must work in the portal.
func TestRelativeMarkdownLinksBecomeHTMLLinks(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(documented(t), &c))
	if !strings.Contains(string(files["entity/service/api/index.html"]), `href="docs/runbook.html"`) {
		t.Errorf("the inlined index's link was not rewritten:\n%s", files["entity/service/api/index.html"])
	}
	if !strings.Contains(string(files["entity/service/api/docs/ops/scaling.html"]), `href="../index.html"`) {
		t.Errorf("a nested doc's parent-relative link was not rewritten:\n%s", files["entity/service/api/docs/ops/scaling.html"])
	}
}

func TestDocPagesUseTheFullDialect(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(documented(t), &c))["entity/service/api/docs/runbook.html"])
	if !strings.Contains(page, `<div class="admonition warning">`) {
		t.Errorf("admonitions must render in a doc page:\n%s", page)
	}
	if !strings.Contains(page, `id="rollback"`) {
		t.Errorf("heading ids must survive into a doc page:\n%s", page)
	}
}

// spec.runbook is what an on-call engineer opens at 3am. It gets a link of
// its own, not just a row in a file listing.
func TestTheRunbookIsLinkedProminently(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(documented(t), &c))["entity/service/api/index.html"])
	if !strings.Contains(page, `class="runbook-link"`) {
		t.Errorf("the runbook needs its own link:\n%s", page)
	}
	if !strings.Contains(page, `href="docs/runbook.html"`) {
		t.Errorf("and it must point at the rendered page:\n%s", page)
	}
}

// A runbook outside spec.docs still gets rendered and linked.
func TestARunbookOutsideTheDocsDirectoryStillRenders(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\nSteps.\n")}}

	var c diag.Collector
	site := siteMap(Site(input(t, files, e), &c))
	if _, ok := site["entity/service/api/runbook.html"]; !ok {
		t.Errorf("a runbook outside docs/ needs a page; got %v", keys(site))
	}
}

// An entity with no docs and no runbook says so. A blank space where
// documentation should be reads as a broken page.
func TestAnUndocumentedEntitySaysSo(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, "No documentation") {
		t.Errorf("an undocumented entity must say so:\n%s", page)
	}
}

// Accumulate, never fail fast: one unreadable document must not cost the
// reader the other pages.
func TestAnUnreadableDocumentIsReportedAndSkipped(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte("# API\n")},
		"services/api/docs/broken.md": {Data: []byte("ok"), Mode: 0},
	}
	in := input(t, files, e)
	// Make the read fail the way a real permission problem would.
	files["services/api/docs/broken.md"] = &fstest.MapFile{Data: nil, Mode: 0}

	var c diag.Collector
	site := siteMap(Site(in, &c))
	if _, ok := site["entity/service/api/index.html"]; !ok {
		t.Error("the entity page must still be rendered")
	}
}

// Spec §14.1: raw HTML in somebody's runbook is escaped, never injected
// into a shared portal page. This is the end-to-end assertion of the
// property Task 1 tests at the goldmark level.
func TestRawHTMLInARunbookNeverReachesThePortal(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{
		"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\n<script>alert(document.cookie)</script>\n")},
	}
	var c diag.Collector
	page := string(siteMap(Site(input(t, files, e), &c))["entity/service/api/runbook.html"])
	if strings.Contains(page, "<script>alert(") {
		t.Errorf("raw HTML from a runbook reached the portal:\n%s", page)
	}
}

func TestDocPageIsGolden(t *testing.T) {
	var c diag.Collector
	golden(t, "doc-runbook.html", siteMap(Site(documented(t), &c))["entity/service/api/docs/runbook.html"])
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/render/ -run 'TestIndexMarkdown|TestOtherDocs|TestRelativeMarkdown|TestDoc|TestTheRunbook|TestARunbook|TestAnUndocumented|TestAnUnreadable|TestRawHTML' -v`
Expected: FAIL — no doc pages are emitted.

- [ ] **Step 3: Write the documentation renderer**

Create `internal/render/docs.go`:

```go
package render

import (
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/yuin/goldmark"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/render/md"
)

// DocLink is one entry in an entity's documentation nav.
type DocLink struct {
	Title string
	// URL is relative to the entity's own page, so the nav needs no Root.
	URL string
}

// RenderedDoc is one document's contribution to the search index (Task 12).
type RenderedDoc struct {
	URL       string
	Title     string
	Text      string
	Headings  []md.Heading
	EntityRef string
}

// DocPage is one documentation sub-page.
type DocPage struct {
	Page
	EntityName string
	EntityURL  string
	Nav        []DocLink
	// HTML has been through md.Render, which runs goldmark WITHOUT
	// WithUnsafe. See the cast in renderMarkdown.
	HTML template.HTML
}

// entityDocs is everything one entity's documentation contributes.
type entityDocs struct {
	Files []emit.File
	Nav   []DocLink
	// Index is docs/index.md, inlined on the entity page (ruling R18).
	Index template.HTML
	Docs  []RenderedDoc
}

// mdToHTML performs the single template.HTML conversion in this codebase.
//
// It is safe for a specific, tested reason: md.New() configures goldmark
// without WithUnsafe (spec §14.1), so raw HTML in a runbook was already
// escaped into text before these bytes existed —
// TestRawHTMLIsNeverInjected in internal/render/md asserts it.
//
// Do not reuse this on bytes that have not been through md.Render.
func mdToHTML(d md.Doc) template.HTML { return template.HTML(d.HTML) }

// htmlSuffix maps a repository-relative Markdown path to its page path.
func htmlSuffix(p string) string { return strings.TrimSuffix(p, ".md") + ".html" }

// rewriteMarkdownLinks turns a relative *.md destination into *.html,
// preserving any #fragment. A link that works on GitHub must work here.
func rewriteMarkdownLinks(dest string) string {
	frag := ""
	if i := strings.Index(dest, "#"); i >= 0 {
		dest, frag = dest[:i], dest[i:]
	}
	if !strings.HasSuffix(dest, ".md") {
		return dest + frag
	}
	return htmlSuffix(dest) + frag
}

// docsFor renders one entity's documentation.
//
// Every failure is reported and skipped. One unreadable document must not
// cost the reader the other pages (spec §12).
func docsFor(in Input, e *catalog.Entity, t *template.Template, m goldmark.Markdown, c *diag.Collector) entityDocs {
	var out entityDocs
	ref := e.Ref()
	entityDir := EntityURL(ref)

	render := func(repoPath, relURL string) (md.Doc, bool) {
		data, err := fs.ReadFile(in.FS, repoPath)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: repoPath, Line: 1,
				Entity:  e.Metadata.Name,
				Check:   "docs-unreadable",
				Message: fmt.Sprintf("cannot read %s", repoPath),
				Hint:    "the file is named by spec.docs or spec.runbook",
			})
			return md.Doc{}, false
		}
		doc, err := md.Render(m, data, rewriteMarkdownLinks)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: repoPath, Line: 1,
				Entity:  e.Metadata.Name,
				Check:   "docs-render",
				Message: fmt.Sprintf("cannot render %s: %v", repoPath, err),
			})
			return md.Doc{}, false
		}
		out.Docs = append(out.Docs, RenderedDoc{
			URL: entityDir + relURL, Title: docTitle(doc, repoPath),
			Text: doc.Text, Headings: doc.Headings, EntityRef: ref.String(),
		})
		return doc, true
	}

	// 1. The docs directory.
	var mdPaths []string
	if e.Spec.Docs != "" {
		err := fs.WalkDir(in.FS, e.Spec.Docs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				mdPaths = append(mdPaths, p)
			}
			return nil
		})
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: e.Spec.Docs, Line: 1,
				Entity:  e.Metadata.Name,
				Check:   "docs-unreadable",
				Message: fmt.Sprintf("cannot read the documentation directory %s: %v", e.Spec.Docs, err),
			})
		}
		sort.Strings(mdPaths)
	}

	for _, repoPath := range mdPaths {
		rel, err := relativeTo(e.Spec.Docs, repoPath)
		if err != nil {
			continue
		}
		if rel == "index.md" {
			// Inlined on the entity page, and NOT also a sub-page: one
			// document, one URL (ruling R18).
			if doc, ok := render(repoPath, "index.html"); ok {
				out.Index = mdToHTML(doc)
			}
			continue
		}
		relURL := "docs/" + htmlSuffix(rel)
		sitePath := entityDir + relURL
		doc, ok := render(repoPath, relURL)
		if !ok {
			continue
		}
		out.Nav = append(out.Nav, DocLink{Title: docTitle(doc, repoPath), URL: relURL})
		view := DocPage{
			Page:       newPage(in, sitePath, docTitle(doc, repoPath), "catalog"),
			EntityName: e.Metadata.Name,
			EntityURL:  entityDir,
			HTML:       mdToHTML(doc),
		}
		if f, ok := renderPage(t, sitePath, view, c); ok {
			out.Files = append(out.Files, f)
		}
	}

	// 2. The runbook, when it is not already one of the documents above.
	if e.Spec.Runbook != "" && !underDir(e.Spec.Docs, e.Spec.Runbook) {
		relURL := "runbook.html"
		sitePath := entityDir + relURL
		if doc, ok := render(e.Spec.Runbook, relURL); ok {
			view := DocPage{
				Page:       newPage(in, sitePath, docTitle(doc, e.Spec.Runbook), "catalog"),
				EntityName: e.Metadata.Name,
				EntityURL:  entityDir,
				HTML:       mdToHTML(doc),
			}
			if f, ok := renderPage(t, sitePath, view, c); ok {
				out.Files = append(out.Files, f)
			}
		}
	}

	return out
}

// docTitle prefers the document's own H1 and falls back to its filename.
// It never invents a title from the entity: a document called
// "rollback.md" with no heading is "rollback", not "api".
func docTitle(d md.Doc, repoPath string) string {
	if d.Title != "" {
		return d.Title
	}
	return strings.TrimSuffix(path.Base(repoPath), ".md")
}

// relativeTo returns p relative to dir.
func relativeTo(dir, p string) (string, error) {
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || !strings.HasPrefix(p, dir+"/") {
		return "", fmt.Errorf("%s is not under %s", p, dir)
	}
	return strings.TrimPrefix(p, dir+"/"), nil
}

// underDir reports whether p lives inside dir.
func underDir(dir, p string) bool {
	if dir == "" {
		return false
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// runbookURL is where an entity's runbook page ended up, relative to the
// entity's own page — "" when it has none.
func runbookURL(e *catalog.Entity) string {
	if e.Spec.Runbook == "" {
		return ""
	}
	if rel, err := relativeTo(e.Spec.Docs, e.Spec.Runbook); err == nil {
		return "docs/" + htmlSuffix(rel)
	}
	return "runbook.html"
}
```

- [ ] **Step 4: Write the doc template**

Create `internal/render/web/templates/doc.html`:

```html
{{define "content"}}
<nav class="crumbs"><a href="{{.Root}}{{.EntityURL}}">{{.EntityName}}</a> <span class="none">/</span> {{.Title}}</nav>
<article class="doc">
{{.HTML}}
</article>
{{end}}
```

- [ ] **Step 5: Wire the docs into the entity page**

In `internal/render/entity.go`, add to `EntityView`:

```go
	// Index is docs/index.md, rendered inline (ruling R18).
	Index template.HTML
	// DocNav lists the sub-pages, relative to this entity's own page.
	DocNav []DocLink
	// RunbookURL is where spec.runbook was rendered, "" when unset.
	RunbookURL string
```

`entityPages` now needs the doc template and the Markdown value, and must return the rendered documents for Task 12's search index. Change its signature and body:

```go
// entityPages renders one page per entity, plus that entity's documentation.
//
// It returns the rendered documents as well: the search index (Task 12) is
// built from the same md.Doc values, so the index and the pages cannot
// disagree about what a document contains.
func entityPages(in Input, c *diag.Collector) ([]emit.File, []RenderedDoc) {
	t, err := templateSet("entity.html")
	if err != nil {
		c.Add(templateCompileError("entity.html", err))
		return nil, nil
	}
	dt, err := templateSet("doc.html")
	if err != nil {
		c.Add(templateCompileError("doc.html", err))
		return nil, nil
	}
	m := md.New()
	slugs := teamSlugMap(in)
	scores := entityScores(in.Scorecard)

	var out []emit.File
	var docs []RenderedDoc
	for _, e := range in.Catalog.Entities() {
		ed := docsFor(in, e, dt, m, c)
		out = append(out, ed.Files...)
		docs = append(docs, ed.Docs...)

		view := entityView(in, e, slugs, scores)
		view.Index = ed.Index
		view.DocNav = ed.Nav
		view.RunbookURL = runbookURL(e)
		if f, ok := renderPage(t, EntityPath(e.Ref()), view, c); ok {
			out = append(out, f)
		}
	}
	return out, docs
}
```

Add `"html/template"` and the `md` import to `entity.go`.

In `internal/render/site.go`, change the call:

```go
	// The second return is the rendered documents. Task 12 builds the
	// search index from them; until then nothing consumes it.
	pages, _ := entityPages(in, c)
	files = append(files, pages...)
```

- [ ] **Step 6: Render the documentation on the entity page**

In `internal/render/web/templates/entity.html`, after the dependencies block:

```html
<h2>Documentation</h2>
{{if .RunbookURL}}<p><a class="runbook-link" href="{{.RunbookURL}}">Runbook</a></p>{{end}}
{{with .DocNav}}
<ul class="doc-nav">
{{range .}}<li><a href="{{.URL}}">{{.Title}}</a></li>
{{end}}</ul>
{{end}}
{{if .Index}}
<article class="doc">
{{.Index}}
</article>
{{else if not .DocNav}}
{{if not .RunbookURL}}<p class="none">No documentation. Point spec.docs at a directory or spec.runbook at a file.</p>{{end}}
{{end}}
```

- [ ] **Step 7: Extend the stylesheet**

Append to `internal/render/web/static/style.css`:

```css
.crumbs { color: var(--muted); margin: 0 0 1rem; }
.doc { max-width: 48rem; }
.doc h1 { margin-top: 0; }
.doc table { margin: 1rem 0; }
.doc blockquote { border-left: 3px solid var(--line); margin: 1rem 0; padding: 0 1rem; color: var(--muted); }
.runbook-link { font-weight: 600; }
ul.doc-nav { list-style: none; padding: 0; margin: .5rem 0 1rem; }
ul.doc-nav li { padding: .1rem 0; }
```

- [ ] **Step 8: Run the tests and regenerate the goldens**

```bash
go test ./internal/render/ -v
go test ./internal/render/ -update
git diff internal/render/testdata/golden/
```

Expected: PASS. **Read the diff** — `doc-runbook.html` should show the admonition as a `<div class="admonition warning">` and the inline code as `<code>`.

- [ ] **Step 9: Commit**

```bash
git add internal/render/docs.go internal/render/docs_test.go internal/render/web/templates/doc.html internal/render/web/templates/entity.html internal/render/web/static/style.css internal/render/entity.go internal/render/site.go internal/render/testdata
git commit -m "feat: render each entity's docs/ and runbook

index.md is inlined on the entity page and is not also a sub-page: one
document, one URL. Everything else under spec.docs gets its own page, and
relative .md links are rewritten to .html so a link that works on GitHub
works in the portal.

The single template.HTML cast in this codebase lives here, and it is safe
because md.New configures goldmark without WithUnsafe — asserted by
TestRawHTMLIsNeverInjected and again end to end here."
```

---

## Task 10: Team pages

**Files:**
- Create: `internal/render/team.go`
- Create: `internal/render/team_test.go`
- Create: `internal/render/web/templates/team.html`
- Modify: `internal/render/site.go`

**Interfaces:**
- Consumes: `Input`, `TeamSlugs`, `TeamPath`, `catalogRows`, `scorecard.TeamScore`.
- Produces:
  - `type render.TeamView struct{…}`
  - `func render.teamPages(in Input, c *diag.Collector) []emit.File`

**Context:** Spec §10: "Team — members, on-call, owned services, aggregate score". `scorecard.Scorecard.Teams()` already aggregates by owner and sorts by team name, so this task assembles rather than computes.

**A team with nothing gets a page too.** A team defined in `teams.yaml` that owns no entity still renders, saying so. The alternative — omitting the page — turns every link to that team into a 404, and makes "this team owns nothing" indistinguishable from "this team does not exist", which is precisely the distinction `teams.yaml` exists to record.

- [ ] **Step 1: Write the failing test**

Create `internal/render/team_test.go`:

```go
package render

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestTeamPageListsMembersAndContacts(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["team/team-payments/index.html"])
	for _, want := range []string{"alice", "bob", "#payments", "PAY"} {
		if !strings.Contains(page, want) {
			t.Errorf("team page is missing %q:\n%s", want, page)
		}
	}
}

func TestTeamPageListsWhatItOwns(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["team/team-payments/index.html"])
	if !strings.Contains(page, `href="../../entity/service/ledger-api/"`) {
		t.Errorf("owned entities must link to their pages:\n%s", page)
	}
	if !strings.Contains(page, "payments-events") {
		t.Errorf("every owned entity must appear, including untiered ones:\n%s", page)
	}
}

func TestTeamPageShowsTheAggregateScore(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["team/team-payments/index.html"])
	if !strings.Contains(page, "aggregate") {
		t.Errorf("the team's aggregate score must be labelled:\n%s", page)
	}
}

// A team that owns nothing still gets a page. Omitting it would turn every
// link to that team into a 404, and make "owns nothing" indistinguishable
// from "does not exist" — the distinction teams.yaml exists to record.
func TestATeamThatOwnsNothingStillGetsAPage(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	// teamsFromYAML adds a second team with no entities.
	in = withTeams(t, in, testTeamsYAML+
		"  - name: team-platform\n    members: [carol]\n    slack: \"#plat\"\n    pagerduty: PLT\n")

	var c diag.Collector
	files := siteMap(Site(in, &c))
	page, ok := files["team/team-platform/index.html"]
	if !ok {
		t.Fatalf("a team with no entities must still have a page; got %v", keys(files))
	}
	if !strings.Contains(string(page), "owns nothing in this catalog") {
		t.Errorf("and it must say so:\n%s", page)
	}
}

func TestTeamPageIsGolden(t *testing.T) {
	var c diag.Collector
	golden(t, "team-team-payments.html", siteMap(Site(twoEntities(t), &c))["team/team-payments/index.html"])
}
```

Add this helper to `internal/render/model_test.go`:

```go
// withTeams replaces an Input's teams, for tests about teams that own
// nothing.
func withTeams(t *testing.T, in Input, yaml string) Input {
	t.Helper()
	var c diag.Collector
	in.Teams = config.LoadTeams("teams.yaml", []byte(yaml), &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("teams fixture is not clean: %+v", ds)
	}
	return in
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/render/ -run TestTeam -v`
Expected: FAIL — no team pages.

- [ ] **Step 3: Write the team pages**

Create `internal/render/team.go`:

```go
package render

import (
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// TeamView is one team's page.
type TeamView struct {
	Page
	Name      string
	Members   []string
	Slack     string
	PagerDuty string
	Owned     []CatalogRow
	// Score is nil when nothing this team owns was scored. Distinct from
	// zero, for the same reason as everywhere else.
	Score      *float64
	Passed     int
	Applicable int
}

// teamScores indexes the scorecard's per-team aggregate.
func teamScores(sc *scorecard.Scorecard) map[string]scorecard.TeamScore {
	out := map[string]scorecard.TeamScore{}
	if sc == nil {
		return out
	}
	for _, t := range sc.Teams() {
		out[t.Team] = t
	}
	return out
}

// teamPages renders one page per team in teams.yaml.
//
// Every team gets one, including a team that owns nothing: the page says so,
// which is a different statement from a 404.
func teamPages(in Input, c *diag.Collector) []emit.File {
	t, err := templateSet("team.html")
	if err != nil {
		c.Add(templateCompileError("team.html", err))
		return nil
	}
	slugs := teamSlugMap(in)
	aggregates := teamScores(in.Scorecard)
	rows := catalogRows(in)

	owned := map[string][]CatalogRow{}
	for _, r := range rows {
		owned[r.Owner] = append(owned[r.Owner], r)
	}

	var out []emit.File
	for _, name := range in.Teams.Names() {
		slug, ok := slugs[name]
		if !ok {
			// TeamSlugs already reported why; a page with no URL cannot be
			// written and a second diagnostic would be noise.
			continue
		}
		team, _ := in.Teams.Get(name)
		path := TeamPath(slug)
		view := TeamView{
			Page:      newPage(in, path, name, "catalog"),
			Name:      name,
			Members:   team.Members,
			Slack:     team.Slack,
			PagerDuty: team.PagerDuty,
			Owned:     owned[name],
		}
		if agg, ok := aggregates[name]; ok && agg.Applicable > 0 {
			score := agg.Score()
			view.Score = &score
			view.Passed, view.Applicable = agg.Passed, agg.Applicable
		}
		if f, ok := renderPage(t, path, view, c); ok {
			out = append(out, f)
		}
	}
	return out
}
```

- [ ] **Step 4: Write the team template**

Create `internal/render/web/templates/team.html`:

```html
{{define "content"}}
<h1>{{.Name}}</h1>

<dl class="facts">
  {{with .Members}}<dt>Members</dt><dd>{{range .}}<span class="mono">{{.}}</span> {{end}}</dd>{{end}}
  {{with .Slack}}<dt>Slack</dt><dd class="mono">{{.}}</dd>{{end}}
  {{with .PagerDuty}}<dt>PagerDuty</dt><dd class="mono">{{.}}</dd>{{end}}
  <dt>Scorecard</dt>
  <dd>{{if .Score}}<span class="mono">{{pct .Score}}</span> <span class="none">aggregate over {{.Applicable}} applicable checks</span>
      {{else}}<span class="none">nothing this team owns was scored</span>{{end}}</dd>
</dl>

<h2>Owns</h2>
{{if .Owned}}
<table><thead><tr><th>Entity</th><th>Kind</th><th>Tier</th><th>Score</th></tr></thead><tbody>
{{range .Owned}}<tr>
  <td><a href="{{$.Root}}{{.URL}}">{{.Name}}</a></td>
  <td><span class="kind kind-{{lower .Kind}}">{{.Kind}}</span></td>
  <td>{{if .Tier}}{{.Tier}}{{else}}<span class="none">—</span>{{end}}</td>
  <td>{{if .Score}}<span class="mono">{{pct .Score}}</span>{{else}}<span class="none">not scored</span>{{end}}</td>
</tr>
{{end}}</tbody></table>
{{else}}
<p class="none">This team owns nothing in this catalog.</p>
{{end}}
{{end}}
```

- [ ] **Step 5: Emit them**

In `internal/render/site.go`, inside `Site`:

```go
	files = append(files, teamPages(in, c)...)
```

- [ ] **Step 6: Run the tests and regenerate**

```bash
go test ./internal/render/ -v && go test ./internal/render/ -update && git diff internal/render/testdata/golden/
```
Expected: PASS; `team-team-payments.html` is new. **Read it.**

- [ ] **Step 7: Commit**

```bash
git add internal/render/team.go internal/render/team_test.go internal/render/model_test.go internal/render/web/templates/team.html internal/render/site.go internal/render/testdata
git commit -m "feat: team pages

A team that owns nothing still gets a page saying so. Omitting it would
turn every link to that team into a 404 and make \"owns nothing\"
indistinguishable from \"does not exist\" — the distinction teams.yaml
exists to record."
```

---

## Task 11: The scorecard page and the history trend

**Files:**
- Create: `internal/render/scorecard.go`
- Create: `internal/render/scorecard_test.go`
- Create: `internal/render/history.go`
- Create: `internal/render/history_test.go`
- Create: `internal/render/web/templates/scorecard.html`
- Modify: `internal/render/site.go`
- Modify: `internal/render/web/static/style.css`

**Interfaces:**
- Consumes: `Input.Scorecard`, `Input.Standards`, `Input.History`, `config.Standards.Checks/Severity/IsExternal`.
- Produces:
  - `type render.TrendPoint struct { Date string; Score float64; Passed, Applicable int }`
  - `type render.Trend struct { Points []TrendPoint; Polyline string; Width, Height int }`
  - `func render.parseHistory(data []byte, c *diag.Collector) []TrendPoint`
  - `func render.trend(points []TrendPoint) Trend`
  - `type render.CheckRow struct{…}`, `type render.ScorecardPage struct{…}`
  - `func render.scorecardPage(in Input, c *diag.Collector) (emit.File, bool)`

**Context:** Spec §10: "Scorecard — the standards table with weekly trend". The standards table is `standards.yaml` rendered as the tier×severity matrix it is, so a team can see what they are being measured against without opening the file.

**The trend is an inline SVG built from numbers, not a chart library.** `Trend.Polyline` is a plain string of coordinates and the template writes the `<svg>` around it, so nothing here needs a `template.HTML` cast: the only cast in this codebase stays the Markdown one in `docs.go`. It also means no JavaScript and no dependency for a five-point line.

**`scorecard-history.csv` is written by other people's CI**, and this is a *reader* of it. Plan 2's `AppendHistory` deliberately preserves rows it does not understand, so this parser must be equally tolerant: a row with the wrong column count is skipped with a warning, never fatal. A portal that refuses to render because one CSV line is malformed is worse than a portal with one missing point.

**Tiers are 1, 2 and 3.** `config.Standards` exposes `Severity(check, tier)` but cannot enumerate tiers — the YAML is a map and a team may configure any subset. Spec §6's matrix uses 1–3 throughout and the schema's `tier` is constrained to them, so the table's columns are those three, named in one place.

- [ ] **Step 1: Write the failing history test**

Create `internal/render/history_test.go`:

```go
package render

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

const historyCSV = "date,ref,tier,owner,passed,applicable,score\n" +
	"2026-09-01,service:api,1,team-payments,3,4,0.750\n" +
	"2026-09-01,service:web,1,team-payments,1,4,0.250\n" +
	"2026-09-08,service:api,1,team-payments,4,4,1.000\n" +
	"2026-09-08,service:web,1,team-payments,2,4,0.500\n"

// One point per date, aggregating every entity — the same arithmetic the
// scorecard's own total uses: passed over applicable, not a mean of means.
func TestParseHistoryAggregatesByDate(t *testing.T) {
	var c diag.Collector
	points := parseHistory([]byte(historyCSV), &c)
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2: %+v", len(points), points)
	}
	if points[0].Date != "2026-09-01" || points[1].Date != "2026-09-08" {
		t.Errorf("points are not in date order: %+v", points)
	}
	if points[0].Passed != 4 || points[0].Applicable != 8 {
		t.Errorf("point 0 = %+v, want 4/8", points[0])
	}
	if points[1].Score != 0.75 {
		t.Errorf("point 1 score = %v, want 0.75", points[1].Score)
	}
}

// A portal that refuses to render because one CSV line is malformed is
// worse than a portal with one missing point.
func TestParseHistorySkipsAMalformedRow(t *testing.T) {
	var c diag.Collector
	points := parseHistory([]byte(historyCSV+"2026-09-15,broken\n"), &c)
	if len(points) != 2 {
		t.Errorf("got %d points, want 2 — the malformed row must be skipped: %+v", len(points), points)
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	if ds[0].Severity != diag.SevWarn {
		t.Errorf("Severity = %v, want SevWarn: a bad row is not a build failure", ds[0].Severity)
	}
	want := "scorecard-history.csv line 6 has 2 columns, want 7; skipping it"
	if ds[0].Message != want {
		t.Errorf("Message = %q, want %q", ds[0].Message, want)
	}
	if ds[0].Line != 6 {
		t.Errorf("Line = %d, want 6", ds[0].Line)
	}
}

func TestParseHistoryOnAHeaderOnlyFile(t *testing.T) {
	var c diag.Collector
	points := parseHistory([]byte("date,ref,tier,owner,passed,applicable,score\n"), &c)
	if len(points) != 0 {
		t.Errorf("got %d points, want none", len(points))
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Errorf("a file with only a header is normal, not a problem: %+v", ds)
	}
}

func TestTrendBuildsAPolyline(t *testing.T) {
	var c diag.Collector
	tr := trend(parseHistory([]byte(historyCSV), &c))
	if tr.Polyline == "" {
		t.Fatal("no polyline")
	}
	coords := strings.Fields(tr.Polyline)
	if len(coords) != 2 {
		t.Fatalf("got %d coordinates, want 2: %q", len(coords), tr.Polyline)
	}
	// Higher score, higher on the chart: SVG y grows downwards, so the
	// later, better point must have the SMALLER y.
	var y0, y1 float64
	if _, err := fmt.Sscanf(coords[0], "%f,%f", new(float64), &y0); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscanf(coords[1], "%f,%f", new(float64), &y1); err != nil {
		t.Fatal(err)
	}
	if y1 >= y0 {
		t.Errorf("an improving score must rise: y went %v -> %v", y0, y1)
	}
}

// A single run is not a trend, and two points from one date is not either.
func TestTrendNeedsTwoPoints(t *testing.T) {
	var c diag.Collector
	one := parseHistory([]byte("date,ref,tier,owner,passed,applicable,score\n2026-09-01,service:api,1,t,3,4,0.750\n"), &c)
	if tr := trend(one); tr.Polyline != "" {
		t.Errorf("one point is not a trend, got %q", tr.Polyline)
	}
}
```

Add `"fmt"` to that file's imports.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/render/ -run 'TestParseHistory|TestTrend' -v`
Expected: FAIL — `undefined: parseHistory`.

- [ ] **Step 3: Write the history reader**

Create `internal/render/history.go`:

```go
package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// historyColumns is the grain Plan 2's ruling R9 fixed:
// date,ref,tier,owner,passed,applicable,score.
const historyColumns = 7

// Sparkline geometry. Small, fixed, and in one place so the template and
// the coordinates cannot disagree.
const (
	trendWidth  = 240
	trendHeight = 48
)

// TrendPoint is one date's whole-catalog score.
type TrendPoint struct {
	Date       string
	Passed     int
	Applicable int
	Score      float64
}

// Trend is the sparkline.
//
// Polyline is a plain coordinate string and the template writes the <svg>
// around it. That is deliberate: it keeps the only template.HTML cast in
// this codebase confined to rendered Markdown, and it needs no charting
// library for a five-point line.
type Trend struct {
	Points        []TrendPoint
	Polyline      string
	Width, Height int
}

// parseHistory reads scorecard-history.csv into one point per date.
//
// This file is written by CI jobs in other people's repositories, and
// AppendHistory deliberately preserves rows it does not understand. So this
// reader is equally tolerant: a malformed row is a warning and a skipped
// point, never a refusal to render the portal.
func parseHistory(data []byte, c *diag.Collector) []TrendPoint {
	type totals struct{ passed, applicable int }
	byDate := map[string]*totals{}

	for i, line := range strings.Split(string(data), "\n") {
		lineNo := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "date,") {
			continue
		}
		cols := strings.Split(line, ",")
		if len(cols) != historyColumns {
			c.Add(diag.Diagnostic{
				Severity: diag.SevWarn,
				File:     scorecard.HistoryPath, Line: lineNo,
				Check: "history",
				Message: fmt.Sprintf("%s line %d has %d columns, want %d; skipping it",
					scorecard.HistoryPath, lineNo, len(cols), historyColumns),
				Hint: "the columns are date,ref,tier,owner,passed,applicable,score",
			})
			continue
		}
		passed, errP := strconv.Atoi(strings.TrimSpace(cols[4]))
		applicable, errA := strconv.Atoi(strings.TrimSpace(cols[5]))
		if errP != nil || errA != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevWarn,
				File:     scorecard.HistoryPath, Line: lineNo,
				Check: "history",
				Message: fmt.Sprintf("%s line %d has a non-numeric passed or applicable count; skipping it",
					scorecard.HistoryPath, lineNo),
			})
			continue
		}
		date := strings.TrimSpace(cols[0])
		if byDate[date] == nil {
			byDate[date] = &totals{}
		}
		byDate[date].passed += passed
		byDate[date].applicable += applicable
	}

	out := make([]TrendPoint, 0, len(byDate))
	for date, t := range byDate {
		p := TrendPoint{Date: date, Passed: t.passed, Applicable: t.applicable}
		if t.applicable > 0 {
			// passed over applicable across the whole catalog — the same
			// arithmetic Scorecard.Score uses, not a mean of per-entity
			// means, which would weight a one-check library like a
			// twelve-check service.
			p.Score = float64(t.passed) / float64(t.applicable)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// trend lays the points out as SVG coordinates.
//
// The y axis is always 0–100%, never auto-scaled to the data. An
// auto-scaled axis turns a wobble between 94% and 95% into a dramatic
// mountain range, which is how a chart lies without stating a falsehood.
func trend(points []TrendPoint) Trend {
	out := Trend{Points: points, Width: trendWidth, Height: trendHeight}
	if len(points) < 2 {
		// One point is not a trend. Rendering a single dot as a line chart
		// implies a history that does not exist.
		return out
	}
	step := float64(trendWidth) / float64(len(points)-1)
	coords := make([]string, 0, len(points))
	for i, p := range points {
		x := float64(i) * step
		// SVG y grows downwards, so a higher score is a smaller y.
		y := float64(trendHeight) * (1 - p.Score)
		coords = append(coords, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	out.Polyline = strings.Join(coords, " ")
	return out
}
```

- [ ] **Step 4: Write the failing scorecard-page test**

Create `internal/render/scorecard_test.go`:

```go
package render

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestScorecardPageShowsTheStandardsMatrix(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["scorecard/index.html"])
	for _, want := range []string{"owner-set", "runbook-present", "image-scanned", "required", "warn"} {
		if !strings.Contains(page, want) {
			t.Errorf("the standards matrix is missing %q:\n%s", want, page)
		}
	}
}

// D3 splits checks in two, and a team needs to know which of their gaps they
// can close by editing YAML and which need a CI job.
func TestScorecardPageMarksExternalChecks(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["scorecard/index.html"])
	if !strings.Contains(page, "reported by CI") {
		t.Errorf("external checks must be labelled as such:\n%s", page)
	}
}

func TestScorecardPageListsTeams(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["scorecard/index.html"])
	if !strings.Contains(page, `href="../team/team-payments/"`) {
		t.Errorf("each team must link to its page:\n%s", page)
	}
}

func TestScorecardPageRendersTheTrend(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte(historyCSV)
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["scorecard/index.html"])
	if !strings.Contains(page, "<polyline") {
		t.Errorf("the trend sparkline is missing:\n%s", page)
	}
	if !strings.Contains(page, "2026-09-08") {
		t.Errorf("the trend's dates must be readable as text too:\n%s", page)
	}
}

// nil History and an empty one are different: no file means no trend was
// ever recorded, an empty file means CI has not appended yet.
func TestScorecardPageWithoutHistorySaysHow(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["scorecard/index.html"])
	if strings.Contains(page, "<polyline") {
		t.Error("no history must mean no chart")
	}
	if !strings.Contains(page, "landsraad score --history") {
		t.Errorf("and the page must say how to start one:\n%s", page)
	}
}

func TestScorecardPageIsGolden(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte(historyCSV)
	var c diag.Collector
	golden(t, "scorecard.html", siteMap(Site(in, &c))["scorecard/index.html"])
}
```

- [ ] **Step 5: Run it to verify it fails**

Run: `go test ./internal/render/ -run TestScorecardPage -v`
Expected: FAIL — no `scorecard/index.html`.

- [ ] **Step 6: Write the scorecard page**

Create `internal/render/scorecard.go`:

```go
package render

import (
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// scorecardTiers are the tiers the matrix has columns for.
//
// config.Standards can answer Severity(check, tier) but cannot enumerate
// tiers — the YAML is a map and a team may configure any subset. Spec §6's
// matrix uses 1-3 throughout and the JSON Schema constrains metadata.tier to
// them, so those are the columns, named once here.
var scorecardTiers = []int{1, 2, 3}

// CheckRow is one row of the standards matrix.
type CheckRow struct {
	Check string
	// External marks a check whose result is reported in by CI rather than
	// computed in-binary (spec D3). A team needs to know which of their
	// gaps they can close by editing YAML and which need a CI job.
	External   bool
	Severities []string
}

// TeamScoreRow is one team's aggregate.
type TeamScoreRow struct {
	Team       string
	URL        string
	Score      float64
	Passed     int
	Applicable int
}

// ScorecardPage is the standards table with the weekly trend (spec §10).
type ScorecardPage struct {
	Page
	Overall float64
	Teams   []TeamScoreRow
	Checks  []CheckRow
	Tiers   []int
	Trend   Trend
	// HasHistory distinguishes "no scorecard-history.csv at all" from "a
	// file with only a header": the first needs the CI job set up, the
	// second is simply waiting for its second run.
	HasHistory bool
}

func scorecardPage(in Input, c *diag.Collector) (emit.File, bool) {
	t, err := templateSet("scorecard.html")
	if err != nil {
		c.Add(templateCompileError("scorecard.html", err))
		return emit.File{}, false
	}

	view := ScorecardPage{
		Page:       newPage(in, "scorecard/index.html", "Scorecard", "scorecard"),
		Tiers:      scorecardTiers,
		HasHistory: in.History != nil,
	}
	if in.Scorecard != nil {
		view.Overall = in.Scorecard.Score()
	}

	slugs := teamSlugMap(in)
	if in.Scorecard != nil {
		for _, ts := range in.Scorecard.Teams() {
			row := TeamScoreRow{
				Team: ts.Team, Score: ts.Score(),
				Passed: ts.Passed, Applicable: ts.Applicable,
			}
			if slug, ok := slugs[ts.Team]; ok {
				row.URL = TeamURL(slug)
			}
			view.Teams = append(view.Teams, row)
		}
	}

	for _, check := range in.Standards.Checks() {
		row := CheckRow{Check: check, External: in.Standards.IsExternal(check)}
		for _, tier := range scorecardTiers {
			row.Severities = append(row.Severities, string(in.Standards.Severity(check, tier)))
		}
		view.Checks = append(view.Checks, row)
	}

	view.Trend = trend(parseHistory(in.History, c))
	return renderPage(t, "scorecard/index.html", view, c)
}
```

- [ ] **Step 7: Write the scorecard template**

Create `internal/render/web/templates/scorecard.html`:

```html
{{define "content"}}
<h1>Scorecard</h1>
<p class="count">Catalog total <span class="mono">{{printf "%.0f%%" (mul .Overall 100)}}</span></p>

<h2>Trend</h2>
{{if .Trend.Polyline}}
<svg class="spark" viewBox="0 0 {{.Trend.Width}} {{.Trend.Height}}" width="{{.Trend.Width}}" height="{{.Trend.Height}}" role="img" aria-label="Catalog score over time">
  <polyline points="{{.Trend.Polyline}}" fill="none" stroke="currentColor" stroke-width="1.5" />
</svg>
<table class="trend"><thead><tr><th>Date</th><th>Score</th><th>Passed</th><th>Applicable</th></tr></thead><tbody>
{{range .Trend.Points}}<tr>
  <td class="mono">{{.Date}}</td>
  <td class="mono">{{printf "%.0f%%" (mul .Score 100)}}</td>
  <td>{{.Passed}}</td><td>{{.Applicable}}</td>
</tr>
{{end}}</tbody></table>
{{else if .HasHistory}}
<p class="none">Only one run has been recorded so far. A trend needs at least two.</p>
{{else}}
<p class="none">No history yet. Run <span class="mono">landsraad score --history</span> in CI to start recording one.</p>
{{end}}

<h2>By team</h2>
{{if .Teams}}
<table><thead><tr><th>Team</th><th>Score</th><th>Passed</th><th>Applicable</th></tr></thead><tbody>
{{range .Teams}}<tr>
  <td>{{if .URL}}<a href="{{$.Root}}{{.URL}}">{{.Team}}</a>{{else}}{{.Team}}{{end}}</td>
  <td class="mono">{{printf "%.0f%%" (mul .Score 100)}}</td>
  <td>{{.Passed}}</td><td>{{.Applicable}}</td>
</tr>
{{end}}</tbody></table>
{{else}}
<p class="none">Nothing in this catalog was scored.</p>
{{end}}

<h2>The standard</h2>
<p class="none">What each check is worth at each tier, from standards.yaml.</p>
<table><thead><tr><th>Check</th>{{range .Tiers}}<th>Tier {{.}}</th>{{end}}<th>Source</th></tr></thead><tbody>
{{range .Checks}}<tr>
  <td class="mono">{{.Check}}</td>
  {{range .Severities}}<td>{{.}}</td>{{end}}
  <td>{{if .External}}<span class="none">reported by CI</span>{{else}}computed{{end}}</td>
</tr>
{{end}}</tbody></table>
{{end}}
```

- [ ] **Step 8: Add the `mul` template function**

In `internal/render/assets.go`, add to `funcs()`:

```go
		// mul exists because Go templates have no arithmetic and a score is
		// stored as a fraction but read as a percentage.
		"mul": func(f float64, by float64) float64 { return f * by },
```

- [ ] **Step 9: Emit the page and style the sparkline**

In `internal/render/site.go`, inside `Site`:

```go
	if f, ok := scorecardPage(in, c); ok {
		files = append(files, f)
	}
```

Append to `internal/render/web/static/style.css`:

```css
svg.spark { color: var(--accent); display: block; margin: .5rem 0 1rem; }
table.trend { max-width: 32rem; }
```

- [ ] **Step 10: Run everything and regenerate**

```bash
go test ./internal/render/ -v && go test ./internal/render/ -update && git diff internal/render/testdata/golden/
```
Expected: PASS; `scorecard.html` is new. **Read it** — check the polyline rises for an improving score.

- [ ] **Step 11: Commit**

```bash
git add internal/render/scorecard.go internal/render/scorecard_test.go internal/render/history.go internal/render/history_test.go internal/render/web/templates/scorecard.html internal/render/web/static/style.css internal/render/assets.go internal/render/site.go internal/render/testdata
git commit -m "feat: the scorecard page and the history sparkline

The trend's y axis is fixed at 0-100% and never auto-scaled: an
auto-scaled axis turns a wobble between 94% and 95% into a mountain
range, which is how a chart lies without stating a falsehood.

scorecard-history.csv is written by other people's CI, so a malformed row
is a warning and a skipped point. Refusing to render a portal because of
one bad CSV line would be worse than a missing point."
```

---

## Task 12: `search-index.json` and the search client

**Files:**
- Create: `internal/render/search.go`
- Create: `internal/render/search_test.go`
- Create: `internal/render/web/static/search.js`
- Modify: `internal/render/site.go`
- Modify: `internal/render/web/templates/base.html`
- Modify: `internal/render/web/static/style.css`

**Interfaces:**
- Consumes: `catalogRows`, `RenderedDoc` from Task 9.
- Produces:
  - `type render.SearchEntry struct{…}`
  - `func render.searchIndex(rows []CatalogRow, docs []RenderedDoc) (emit.File, error)`

**Context:** Spec §10: "a build-time `search-index.json` over titles, headings and body text, queried by a small vanilla-JS client. No server component." Spec §15 rules out server-side search, so this is the whole feature.

**Two kinds of entry, one index.** Every entity contributes one entry (its ref, kind, owner, description), and every rendered document contributes one (its title, headings and capped body text). A search for `payments` should find the service *and* the runbook that mentions it.

**The client uses `textContent`, never `innerHTML`.** The index holds text taken from users' Markdown. It arrived escaped in the HTML pages because goldmark ran without `WithUnsafe` — but `search-index.json` is JSON, not HTML, and building a result list with `innerHTML` would reintroduce exactly the injection spec §14.1 closed. There is a comment saying so at the line it matters.

**Ruling R17's cap is already applied** by `md.Render`: each document's `Text` is at most 2000 runes.

- [ ] **Step 1: Write the failing test**

Create `internal/render/search_test.go`:

```go
package render

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func decodeIndex(t *testing.T, data []byte) []SearchEntry {
	t.Helper()
	var out []SearchEntry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("search-index.json is not valid JSON: %v\n%s", err, data)
	}
	return out
}

func TestSearchIndexCoversEveryEntity(t *testing.T) {
	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(twoEntities(t), &c))["search-index.json"])
	byURL := map[string]SearchEntry{}
	for _, e := range entries {
		byURL[e.URL] = e
	}
	e, ok := byURL["entity/service/ledger-api/"]
	if !ok {
		t.Fatalf("the service is not in the index: %+v", entries)
	}
	if e.Title != "ledger-api" || e.Kind != "Service" || e.Owner != "team-payments" {
		t.Errorf("entry = %+v", e)
	}
}

func TestSearchIndexCoversRenderedDocuments(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{
		"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\n## Rollback\n\nDrain the queue first.\n")},
	}
	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(input(t, files, e), &c))["search-index.json"])

	var doc *SearchEntry
	for i := range entries {
		if entries[i].URL == "entity/service/api/runbook.html" {
			doc = &entries[i]
		}
	}
	if doc == nil {
		t.Fatalf("the runbook is not in the index: %+v", entries)
	}
	if doc.Title != "Runbook" {
		t.Errorf("Title = %q, want %q", doc.Title, "Runbook")
	}
	if len(doc.Headings) == 0 || doc.Headings[0] != "Runbook" {
		t.Errorf("Headings = %v, want them collected", doc.Headings)
	}
	if !strings.Contains(doc.Text, "Drain the queue") {
		t.Errorf("body text is missing: %q", doc.Text)
	}
}

// The index is a JSON document a client parses, so it must be stable
// between identical runs or every build produces a spurious diff.
func TestSearchIndexIsSorted(t *testing.T) {
	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(twoEntities(t), &c))["search-index.json"])
	for i := 1; i < len(entries); i++ {
		if entries[i-1].URL > entries[i].URL {
			t.Errorf("entries are not sorted by URL: %q before %q", entries[i-1].URL, entries[i].URL)
		}
	}
}

// Markdown was escaped on its way into HTML because goldmark ran without
// WithUnsafe. It is NOT escaped here — this is JSON — which is why the
// client must use textContent.
func TestSearchIndexIsValidJSONForAwkwardText(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{
		"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\nQuotes \" and <angles> and \\backslashes\\ and émojis 🜃.\n")},
	}
	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(input(t, files, e), &c))["search-index.json"])
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
}

func TestSearchIndexIsGolden(t *testing.T) {
	var c diag.Collector
	golden(t, "search-index.json", siteMap(Site(twoEntities(t), &c))["search-index.json"])
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/render/ -run TestSearchIndex -v`
Expected: FAIL — no `search-index.json`.

- [ ] **Step 3: Write the index builder**

Create `internal/render/search.go`:

```go
package render

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// SearchIndexPath is where the client fetches the index from.
const SearchIndexPath = "search-index.json"

// SearchEntry is one searchable thing: an entity, or one of its documents.
//
// Field names are spelled out rather than shortened. The file is served
// once per visit and gzips well; a reader debugging why their runbook does
// not come up should not have to guess what "h" means.
type SearchEntry struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Kind and Owner are set for entity entries and empty for documents,
	// so the client can label a result as a service rather than a page.
	Kind        string   `json:"kind,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	Description string   `json:"description,omitempty"`
	Headings    []string `json:"headings,omitempty"`
	// Text is capped at 2000 runes by md.Render (ruling R17).
	Text string `json:"text,omitempty"`
}

// searchIndex builds the whole index.
//
// Entities and documents share one file so a single query finds both the
// service called "payments" and the runbook that explains how to drain it.
func searchIndex(rows []CatalogRow, docs []RenderedDoc) (emit.File, error) {
	entries := make([]SearchEntry, 0, len(rows)+len(docs))
	for _, r := range rows {
		entries = append(entries, SearchEntry{
			URL: r.URL, Title: r.Name, Kind: r.Kind,
			Owner: r.Owner, Description: r.Description,
		})
	}
	for _, d := range docs {
		e := SearchEntry{URL: d.URL, Title: d.Title, Text: d.Text}
		for _, h := range d.Headings {
			e.Headings = append(e.Headings, h.Text)
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].URL != entries[j].URL {
			return entries[i].URL < entries[j].URL
		}
		return entries[i].Title < entries[j].Title
	})

	data, err := json.Marshal(entries)
	if err != nil {
		return emit.File{}, err
	}
	return emit.File{Path: SearchIndexPath, Data: append(data, '\n')}, nil
}

// searchIndexFile wraps searchIndex for Site, reporting rather than
// returning the error.
func searchIndexFile(rows []CatalogRow, docs []RenderedDoc, c *diag.Collector) (emit.File, bool) {
	f, err := searchIndex(rows, docs)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: SearchIndexPath, Line: 1,
			Check:   "search-index",
			Message: fmt.Sprintf("cannot build the search index: %v", err),
		})
		return emit.File{}, false
	}
	return f, true
}
```

- [ ] **Step 4: Write the search client**

Create `internal/render/web/static/search.js`:

```js
// Spec §10: a build-time index over titles, headings and body text, queried
// by a small vanilla-JS client. No server component (spec §15).
(function () {
  var script = document.currentScript;
  var root = (script && script.getAttribute('data-root')) || '';
  var input = document.getElementById('q');
  var list = document.getElementById('results');
  if (!input || !list) { return; }

  var index = null;
  var loading = false;

  function load() {
    if (index || loading) { return Promise.resolve(); }
    loading = true;
    return fetch(root + 'search-index.json')
      .then(function (r) { return r.ok ? r.json() : []; })
      .then(function (data) { index = data; })
      .catch(function () { index = []; });
  }

  function score(entry, terms) {
    var title = (entry.title || '').toLowerCase();
    var headings = (entry.headings || []).join(' ').toLowerCase();
    var body = ((entry.description || '') + ' ' + (entry.text || '')).toLowerCase();
    var owner = (entry.owner || '').toLowerCase();
    var total = 0;
    for (var i = 0; i < terms.length; i++) {
      var t = terms[i];
      var hit = 0;
      if (title.indexOf(t) !== -1) { hit += title === t ? 100 : 40; }
      if (owner.indexOf(t) !== -1) { hit += 20; }
      if (headings.indexOf(t) !== -1) { hit += 10; }
      if (body.indexOf(t) !== -1) { hit += 2; }
      // Every term must appear somewhere, so "payments runbook" does not
      // match a page that only mentions payments.
      if (hit === 0) { return 0; }
      total += hit;
    }
    return total;
  }

  function render(results) {
    list.textContent = '';
    if (!results.length) {
      list.hidden = true;
      return;
    }
    results.forEach(function (entry) {
      var li = document.createElement('li');
      var a = document.createElement('a');
      a.href = root + entry.url;
      // textContent, NEVER innerHTML. This text came from somebody's
      // Markdown. It was escaped on its way into the HTML pages because
      // goldmark runs without WithUnsafe (spec §14.1) — but this arrived as
      // JSON, unescaped, and innerHTML here would reopen exactly that hole.
      a.textContent = entry.title || entry.url;
      li.appendChild(a);
      if (entry.kind) {
        var span = document.createElement('span');
        span.className = 'result-kind';
        span.textContent = entry.kind;
        li.appendChild(span);
      }
      list.appendChild(li);
    });
    list.hidden = false;
  }

  function run() {
    var q = input.value.trim().toLowerCase();
    if (q.length < 2) {
      render([]);
      return;
    }
    var terms = q.split(/\s+/);
    var scored = [];
    for (var i = 0; i < index.length; i++) {
      var s = score(index[i], terms);
      if (s > 0) { scored.push({ s: s, e: index[i] }); }
    }
    scored.sort(function (a, b) { return b.s - a.s; });
    render(scored.slice(0, 10).map(function (x) { return x.e; }));
  }

  input.addEventListener('input', function () {
    load().then(run);
  });
  input.addEventListener('blur', function () {
    // A click on a result must land before the list disappears.
    setTimeout(function () { list.hidden = true; }, 150);
  });
})();
```

- [ ] **Step 5: Add the search box and load the client**

In `internal/render/web/templates/base.html`, inside `<header class="site">` after the `<nav>`:

```html
  <div class="search">
    <input type="search" id="q" placeholder="Search" autocomplete="off" aria-label="Search the catalog">
    <ol id="results" hidden></ol>
  </div>
```

and before the `runtime.js` tag:

```html
<script src="{{.Root}}assets/search.js" data-root="{{.Root}}"></script>
```

- [ ] **Step 6: Emit the index**

In `internal/render/site.go`, change the entity-pages call and add the index. `Site` now looks like:

```go
func Site(in Input, c *diag.Collector) []emit.File {
	var files []emit.File

	// Reported here, once. The page builders use teamSlugMap for their
	// links; this call is what turns a collision into a build failure.
	TeamSlugs(in.Teams, c)

	rows := catalogRows(in)

	if t, err := templateSet("catalog.html"); err != nil {
		c.Add(templateCompileError("catalog.html", err))
	} else if f, ok := renderPage(t, "index.html", catalogPage(in), c); ok {
		files = append(files, f)
	}

	pages, docs := entityPages(in, c)
	files = append(files, pages...)
	files = append(files, teamPages(in, c)...)

	if f, ok := scorecardPage(in, c); ok {
		files = append(files, f)
	}
	if f, ok := mapPage(in, c); ok {
		files = append(files, f)
	}
	// The index is built from the same rows the catalog renders and the
	// same md.Doc values the pages do, so it cannot disagree with them
	// about what exists or what a document says.
	if f, ok := searchIndexFile(rows, docs, c); ok {
		files = append(files, f)
	}

	files = append(files, assets(in, c)...)
	return files
}
```

- [ ] **Step 7: Style the search box**

Append to `internal/render/web/static/style.css`:

```css
.search { position: relative; margin-left: auto; }
.search input { padding: .25rem .5rem; border: 1px solid var(--line); border-radius: 3px; font: inherit; width: 16rem; }
#results {
  position: absolute; right: 0; top: 1.9rem; z-index: 10;
  list-style: none; margin: 0; padding: .25rem 0; width: 24rem;
  background: var(--bg); border: 1px solid var(--line); border-radius: 3px;
  box-shadow: 0 4px 12px rgba(0,0,0,.08);
}
#results li { padding: .25rem .6rem; display: flex; gap: .5rem; align-items: baseline; }
#results li:hover { background: #f2f4f7; }
.result-kind { font-size: 12px; color: var(--muted); margin-left: auto; }
```

- [ ] **Step 8: Run the tests and regenerate**

```bash
go test ./internal/render/ -v && go test ./internal/render/ -update && git diff internal/render/testdata/golden/
```
Expected: PASS. Every golden page gains the search box; `search-index.json` is new. **Read the diff.**

- [ ] **Step 9: Try it in a browser**

```bash
go build -o bin/landsraad ./cmd/landsraad
./bin/landsraad build testdata/monorepo-ok -o /tmp/landsraad-portal
open /tmp/landsraad-portal/index.html
```

Type `pay` in the search box. Expected: `payments-worker` and `payments-events` appear.

**Note:** `fetch()` on a `file://` page is blocked by browsers. Search will not work when the page is opened from disk — that is a browser rule, not a bug. Task 14's `landsraad serve` is where it works; verify it there.

- [ ] **Step 10: Commit**

```bash
git add internal/render/search.go internal/render/search_test.go internal/render/web/static/search.js internal/render/web/templates/base.html internal/render/web/static/style.css internal/render/site.go internal/render/testdata
git commit -m "feat: search-index.json and a vanilla-JS search client

Entities and documents share one index, so a query finds both the service
called payments and the runbook explaining how to drain it.

The client builds results with textContent, never innerHTML. That text
came from somebody's Markdown and reached the client as JSON — unescaped,
unlike the HTML pages — so innerHTML would reopen the hole spec §14.1
closed by running goldmark without WithUnsafe."
```

---

## Task 13: Filtering and sorting the catalog

**Files:**
- Create: `internal/render/web/static/catalog.js`
- Modify: `internal/render/web/templates/catalog.html`
- Modify: `internal/render/web/templates/base.html`
- Modify: `internal/render/web/static/style.css`

**Interfaces:**
- Consumes: `CatalogPage.Kinds/Teams/Tiers/Tags` from Task 4, and the `data-` attributes already on each row.
- Produces: no Go API. This task is the client half of a feature whose server half shipped in Task 4.

**Context:** Spec §10: "Catalog — all entities, filterable by kind/team/tier/tag, sortable by score".

**The filters degrade to nothing, and the table still works.** Every row is already in the HTML with its `data-kind`, `data-owner`, `data-tier` and `data-tags`. With JavaScript disabled the reader sees the whole catalog, sorted by ref — which is a worse experience and not a broken one. That is why filtering was never going to be a query parameter and a rebuild.

**Sorting is by column click, and score sorts numerically.** A `<td>` containing `not scored` must not sort between `9%` and `90%`, so the score cell carries `data-score` with a numeric value and `-1` for "not scored", which sorts them together at one end rather than scattering them through the middle.

- [ ] **Step 1: Add the filter controls and the sortable headers**

Replace `internal/render/web/templates/catalog.html` with:

```html
{{define "content"}}
<h1>Catalog</h1>
<p class="count"><span id="shown">{{len .Rows}}</span> of {{len .Rows}} entities</p>

<div class="filters" id="filters">
  <label>Kind
    <select data-filter="kind"><option value="">any</option>
    {{range .Kinds}}<option value="{{lower .}}">{{.}}</option>{{end}}
    </select>
  </label>
  <label>Team
    <select data-filter="owner"><option value="">any</option>
    {{range .Teams}}<option value="{{.}}">{{.}}</option>{{end}}
    </select>
  </label>
  <label>Tier
    <select data-filter="tier"><option value="">any</option>
    {{range .Tiers}}<option value="{{.}}">{{.}}</option>{{end}}
    </select>
  </label>
  <label>Tag
    <select data-filter="tags"><option value="">any</option>
    {{range .Tags}}<option value="{{.}}">{{.}}</option>{{end}}
    </select>
  </label>
</div>

<table class="catalog" id="catalog">
<thead>
<tr>
  <th data-sort="text">Entity</th>
  <th data-sort="text">Kind</th>
  <th data-sort="text">Owner</th>
  <th data-sort="number">Tier</th>
  <th data-sort="text">Lifecycle</th>
  <th data-sort="number">Score</th>
</tr>
</thead>
<tbody>
{{range .Rows}}
<tr data-kind="{{lower .Kind}}" data-owner="{{.Owner}}" data-tier="{{.Tier}}" data-tags="{{range .Tags}}{{.}} {{end}}">
  <td>
    <a href="{{$.Root}}{{.URL}}">{{.Name}}</a>
    {{with .Description}}<span class="desc">{{.}}</span>{{end}}
  </td>
  <td><span class="kind kind-{{lower .Kind}}">{{.Kind}}</span></td>
  <td>{{if .OwnerURL}}<a href="{{$.Root}}{{.OwnerURL}}">{{.Owner}}</a>{{else}}{{.Owner}}{{end}}</td>
  <td data-value="{{.Tier}}">{{if .Tier}}{{.Tier}}{{else}}<span class="none">—</span>{{end}}</td>
  <td>{{.Lifecycle}}</td>
  {{if .Score}}
  <td data-value="{{mul .Score 100}}"><span class="mono">{{pct .Score}}</span></td>
  {{else}}
  <td data-value="-1"><span class="none">not scored</span></td>
  {{end}}
</tr>
{{end}}
</tbody>
</table>
{{end}}
```

- [ ] **Step 2: Write the client**

Create `internal/render/web/static/catalog.js`:

```js
// Spec §10: the catalog is filterable by kind/team/tier/tag and sortable.
//
// Every row is already in the HTML with its data- attributes, so with
// JavaScript disabled the reader still gets the whole catalog sorted by
// ref. That is a worse experience, not a broken page — which is why this
// was never going to be a query parameter and a rebuild.
(function () {
  var table = document.getElementById('catalog');
  var filters = document.getElementById('filters');
  if (!table) { return; }
  var tbody = table.tBodies[0];
  var shown = document.getElementById('shown');

  function rows() {
    return Array.prototype.slice.call(tbody.rows);
  }

  function applyFilters() {
    if (!filters) { return; }
    var selects = filters.querySelectorAll('select[data-filter]');
    var visible = 0;
    rows().forEach(function (row) {
      var ok = true;
      selects.forEach(function (sel) {
        var want = sel.value;
        if (!want) { return; }
        var have = row.getAttribute('data-' + sel.getAttribute('data-filter')) || '';
        if (sel.getAttribute('data-filter') === 'tags') {
          // data-tags is a space-separated list, so match a whole token:
          // the tag "go" must not match "golang".
          ok = ok && (' ' + have + ' ').indexOf(' ' + want + ' ') !== -1;
        } else {
          ok = ok && have === want;
        }
      });
      row.hidden = !ok;
      if (ok) { visible++; }
    });
    if (shown) { shown.textContent = String(visible); }
  }

  function cellValue(row, index, kind) {
    var cell = row.cells[index];
    if (!cell) { return kind === 'number' ? 0 : ''; }
    if (kind === 'number') {
      // data-value carries the sortable number: "not scored" is -1 so the
      // unscored entities group at one end rather than sorting between 9%
      // and 90% as text would.
      return parseFloat(cell.getAttribute('data-value') || '0');
    }
    return (cell.textContent || '').trim().toLowerCase();
  }

  function sortBy(index, kind, ascending) {
    var sorted = rows().sort(function (a, b) {
      var x = cellValue(a, index, kind);
      var y = cellValue(b, index, kind);
      if (x < y) { return ascending ? -1 : 1; }
      if (x > y) { return ascending ? 1 : -1; }
      return 0;
    });
    sorted.forEach(function (row) { tbody.appendChild(row); });
  }

  Array.prototype.slice.call(table.tHead.rows[0].cells).forEach(function (th, index) {
    var kind = th.getAttribute('data-sort');
    if (!kind) { return; }
    th.tabIndex = 0;
    th.classList.add('sortable');
    var ascending = true;
    function activate() {
      sortBy(index, kind, ascending);
      Array.prototype.slice.call(table.tHead.rows[0].cells).forEach(function (other) {
        other.removeAttribute('aria-sort');
      });
      th.setAttribute('aria-sort', ascending ? 'ascending' : 'descending');
      ascending = !ascending;
    }
    th.addEventListener('click', activate);
    th.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        activate();
      }
    });
  });

  if (filters) {
    filters.addEventListener('change', applyFilters);
  }
})();
```

- [ ] **Step 3: Load it**

In `internal/render/web/templates/base.html`, before the `search.js` tag:

```html
<script src="{{.Root}}assets/catalog.js"></script>
```

- [ ] **Step 4: Style it**

Append to `internal/render/web/static/style.css`:

```css
.filters { display: flex; gap: 1rem; margin: 0 0 1rem; flex-wrap: wrap; }
.filters label { font-size: 12px; color: var(--muted); display: flex; gap: .35rem; align-items: center; }
.filters select { font: inherit; padding: .15rem .3rem; border: 1px solid var(--line); border-radius: 3px; }
th.sortable { cursor: pointer; user-select: none; }
th.sortable:hover { color: var(--fg); }
th[aria-sort="ascending"]::after { content: " ▲"; }
th[aria-sort="descending"]::after { content: " ▼"; }
```

- [ ] **Step 5: Regenerate the goldens and read the diff**

```bash
go test ./internal/render/ -v && go test ./internal/render/ -update && git diff internal/render/testdata/golden/index.html
```

Expected: PASS. `index.html` gains the filter controls, `data-sort` headers and `data-value` cells. **Check that the untiered topic's score cell is `data-value="-1"`.**

- [ ] **Step 6: Try it**

```bash
go build -o bin/landsraad ./cmd/landsraad
./bin/landsraad build testdata/monorepo-ok -o /tmp/landsraad-portal
open /tmp/landsraad-portal/index.html
```

Expected: selecting Kind = Topic leaves one row and the count reads "1 of 3". Clicking Score sorts, with "not scored" grouped at one end. Both work from `file://` — unlike search, this needs no `fetch`.

- [ ] **Step 7: Commit**

```bash
git add internal/render/web/static/catalog.js internal/render/web/templates/catalog.html internal/render/web/templates/base.html internal/render/web/static/style.css internal/render/testdata
git commit -m "feat: filter and sort the catalog client-side

Every row ships in the HTML with its data- attributes, so the table still
works with JavaScript off — worse, not broken.

The score column sorts on data-value, with -1 for \"not scored\", so
unscored entities group at one end instead of sorting between 9% and 90%
the way text would."
```

---

## Task 14: `landsraad serve --watch`

**Files:**
- Create: `cmd/landsraad/serve.go`
- Create: `cmd/landsraad/serve_test.go`
- Modify: `cmd/landsraad/main.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: `Build`, `BuildOptions`.
- Produces:
  - `type siteServer struct{…}` with `set([]emit.File)` and `ServeHTTP`
  - `func Serve(root, addr string, opts BuildOptions, watch bool, errOut io.Writer) error`
  - `func watchDirs(root string, w *fsnotify.Watcher) error`

**Context:** Spec §8: `landsraad serve [--watch]`, local preview.

**`serve` never writes to disk.** `Build` returns `[]emit.File`, the server holds them in a map, and a rebuild replaces the map. That falls straight out of "a generator is a pure function returning the files it would write" — and it removes the classic watch bug outright: a server that rebuilt into `dist/` while watching the tree containing `dist/` would trigger itself, forever. There is no output directory to watch, so there is no loop to break.

**fsnotify is not recursive — verified, not assumed.** A watcher with every existing directory added receives nothing for a file created inside a *newly created* subdirectory. A new service added under `services/` while the server runs would be invisible until restart. So a `Create` event naming a directory re-walks and adds it. There is a test for exactly this, because it is the failure that looks like "the watcher just doesn't work sometimes".

**`.git` is skipped.** A single `git status` touches hundreds of files under `.git`, and a checkout or a rebase produces thousands of events. Watching it means a rebuild storm on every ordinary git command.

**Debounce.** An editor saving a file commonly emits several events (write, chmod, rename-into-place). Rebuilding per event means three renders per save. A 150 ms trailing debounce collapses them.

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/fsnotify/fsnotify@v1.10.1
go mod tidy
```

Expected: `go.mod` gains `github.com/fsnotify/fsnotify v1.10.1` and `golang.org/x/sys` as indirect.

- [ ] **Step 2: Write the failing test**

Create `cmd/landsraad/serve_test.go`:

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/landsraadhq/landsraad/internal/emit"
)

func testServer() *siteServer {
	s := &siteServer{}
	s.set([]emit.File{
		{Path: "index.html", Data: []byte("<h1>catalog</h1>")},
		{Path: "entity/service/api/index.html", Data: []byte("<h1>api</h1>")},
		{Path: "assets/style.css", Data: []byte("body{}")},
		{Path: "search-index.json", Data: []byte("[]")},
	})
	return s
}

func get(t *testing.T, s *siteServer, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestServeMapsRootToTheCatalogIndex(t *testing.T) {
	rec := get(t, testServer(), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "<h1>catalog</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// Ruling R11's directory-per-page URLs must resolve without a rewrite rule.
func TestServeMapsADirectoryURLToItsIndex(t *testing.T) {
	rec := get(t, testServer(), "/entity/service/api/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "<h1>api</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestServeSetsContentTypes(t *testing.T) {
	cases := map[string]string{
		"/assets/style.css":   "text/css",
		"/search-index.json":  "application/json",
		"/":                   "text/html",
	}
	s := testServer()
	for path, want := range cases {
		got := get(t, s, path).Header().Get("Content-Type")
		if len(got) < len(want) || got[:len(want)] != want {
			t.Errorf("%s Content-Type = %q, want a %q", path, got, want)
		}
	}
}

func TestServeReturns404ForAnUnknownPath(t *testing.T) {
	if code := get(t, testServer(), "/nope/").Code; code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

// A path escape must not reach outside the in-memory map. It cannot — there
// is no filesystem behind this — but the mapping must still not resolve.
func TestServeRejectsTraversal(t *testing.T) {
	if code := get(t, testServer(), "/../../etc/passwd").Code; code == http.StatusOK {
		t.Error("a traversal path must not resolve")
	}
}

func TestServeSwapsTheSiteOnRebuild(t *testing.T) {
	s := testServer()
	s.set([]emit.File{{Path: "index.html", Data: []byte("<h1>rebuilt</h1>")}})
	if body := get(t, s, "/").Body.String(); body != "<h1>rebuilt</h1>" {
		t.Errorf("body = %q, want the rebuilt page", body)
	}
	// The previous site's pages are gone, not merged.
	if code := get(t, s, "/entity/service/api/").Code; code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 after a rebuild that dropped the page", code)
	}
}

// fsnotify is NOT recursive. Without re-adding on Create, a service added
// while the server runs is invisible until restart — the failure that looks
// like "the watcher just doesn't work sometimes".
func TestWatchDirsPicksUpADirectoryCreatedLater(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := watchDirs(root, w); err != nil {
		t.Fatalf("watchDirs: %v", err)
	}

	events := make(chan fsnotify.Event, 64)
	go func() {
		for e := range w.Events {
			// This is what the serve loop does with a new directory.
			if e.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(e.Name); err == nil && info.IsDir() {
					_ = watchDirs(e.Name, w)
				}
			}
			events <- e
		}
	}()

	if err := os.MkdirAll(filepath.Join(root, "services", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, 2*time.Second) // the directory creation itself

	if err := os.WriteFile(filepath.Join(root, "services", "new", "service.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, 2*time.Second) // the write inside it
}

// .git produces thousands of events on an ordinary checkout. Watching it
// means a rebuild storm on every git command.
func TestWatchDirsSkipsDotGit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := watchDirs(root, w); err != nil {
		t.Fatal(err)
	}
	for _, watched := range w.WatchList() {
		if filepath.Base(watched) == ".git" || filepath.Base(filepath.Dir(watched)) == ".git" {
			t.Errorf(".git is being watched: %s", watched)
		}
	}
}

func waitFor(t *testing.T, ch chan fsnotify.Event, d time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatal("no filesystem event arrived")
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./cmd/landsraad/ -run 'TestServe|TestWatchDirs' -v`
Expected: FAIL — `undefined: siteServer`.

- [ ] **Step 4: Write the server**

Create `cmd/landsraad/serve.go`:

```go
package main

import (
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/emit"
)

// debounce is how long the watcher waits for the filesystem to go quiet.
//
// An editor saving one file commonly emits several events — a write, a
// chmod, a rename into place — and rebuilding per event means three renders
// per save.
const debounce = 150 * time.Millisecond

// siteServer serves a rendered site from memory.
//
// There is no output directory, which is not a shortcut: a preview server
// that rebuilt into dist/ while watching the tree that contains dist/ would
// trigger itself forever. Build returning values rather than writing files
// is what removes the loop instead of managing it.
type siteServer struct {
	mu    sync.RWMutex
	files map[string][]byte
}

func (s *siteServer) set(files []emit.File) {
	next := make(map[string][]byte, len(files))
	for _, f := range files {
		next[f.Path] = f.Data
	}
	s.mu.Lock()
	s.files = next
	s.mu.Unlock()
}

func (s *siteServer) lookup(p string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.files[p]
	return data, ok
}

func (s *siteServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// path.Clean resolves any "..", and the leading slash is then stripped,
	// so a request can only ever name a key that a generator produced.
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" || strings.HasSuffix(r.URL.Path, "/") {
		// Directory-per-page URLs (ruling R11) resolve to their index
		// without a rewrite rule, which is what the static hosts do too.
		p = path.Join(p, "index.html")
	}
	data, ok := s.lookup(p)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// A preview server must never serve yesterday's page after a rebuild.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// watchDirs adds root and every directory beneath it to w.
//
// fsnotify is NOT recursive — verified, not assumed: a watcher holding every
// directory that existed at startup receives nothing for a file created
// inside a directory made afterwards. The serve loop therefore calls this
// again for every new directory.
//
// .git is skipped. One `git status` touches hundreds of files under it and a
// rebase produces thousands, so watching it means a rebuild storm on every
// ordinary git command.
func watchDirs(root string, w *fsnotify.Watcher) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished mid-walk is not a reason to stop
			// watching the rest of the tree.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		return w.Add(p)
	})
}

// Serve renders the site and serves it, rebuilding on change when watch is
// set.
func Serve(root, addr string, opts BuildOptions, watch bool, errOut io.Writer) error {
	srv := &siteServer{}

	rebuild := func() {
		files, code := Build(os.DirFS(root), errOut, opts)
		if code != exitOK {
			// Keep serving the last good site. A preview that goes blank
			// the moment you make a typo is a preview you stop trusting;
			// the diagnostics are already on stderr.
			fmt.Fprintf(errOut, "  build failed; still serving the previous version\n")
			return
		}
		srv.set(files)
	}
	rebuild()

	if watch {
		w, err := fsnotify.NewWatcher()
		if err != nil {
			return fmt.Errorf("cannot watch %s: %w", root, err)
		}
		defer w.Close()
		if err := watchDirs(root, w); err != nil {
			return fmt.Errorf("cannot watch %s: %w", root, err)
		}
		go func() {
			var timer *time.Timer
			for {
				select {
				case event, ok := <-w.Events:
					if !ok {
						return
					}
					if event.Op&fsnotify.Create != 0 {
						if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
							// A new directory is invisible until added.
							_ = watchDirs(event.Name, w)
						}
					}
					if timer != nil {
						timer.Stop()
					}
					timer = time.AfterFunc(debounce, func() {
						fmt.Fprintf(errOut, "  change detected, rebuilding\n")
						rebuild()
					})
				case err, ok := <-w.Errors:
					if !ok {
						return
					}
					fmt.Fprintf(errOut, "watch error: %v\n", err)
				}
			}
		}()
		fmt.Fprintf(errOut, "watching %s for changes\n", root)
	}

	fmt.Fprintf(errOut, "serving on http://%s\n", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func newServeCmd() *cobra.Command {
	var (
		addr       string
		watch      bool
		mermaidSrc string
	)
	cmd := &cobra.Command{
		Use:   "serve [root]",
		Short: "Preview the portal locally",
		Long: "Render the portal and serve it from memory. Nothing is written to " +
			"disk, so --watch cannot trigger itself by rebuilding into the tree it " +
			"is watching.",
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
			mermaid, err := mermaidFor(mermaidSrc)
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return Serve(resolved, addr, BuildOptions{
				Mermaid: mermaid,
				// A preview rebuilt on every save re-reads the clock, which
				// is what makes the footer's timestamp meaningful here.
				Now:      time.Now().UTC(),
				LastEdit: gitLastEdit(resolved),
				Version:  version(),
			}, watch, cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "localhost:8080", "address to listen on")
	cmd.Flags().BoolVar(&watch, "watch", false, "rebuild when a file changes")
	cmd.Flags().StringVar(&mermaidSrc, "mermaid-src", "",
		"Mermaid bundle: a URL, a path to a local file, or \"none\"")
	return cmd
}
```

- [ ] **Step 5: Register it**

In `cmd/landsraad/main.go`, after `root.AddCommand(newBuildCmd())`:

```go
	root.AddCommand(newServeCmd())
```

- [ ] **Step 6: Run the tests**

Run: `go test ./cmd/landsraad/ -v`
Expected: PASS.

If `TestWatchDirsPicksUpADirectoryCreatedLater` times out on the second `waitFor`, the re-add on `Create` is missing or is checking the wrong thing — that is the exact bug this test exists to catch.

- [ ] **Step 7: Drive it by hand**

```bash
go build -o bin/landsraad ./cmd/landsraad
./bin/landsraad serve testdata/monorepo-ok --watch
```

Then, in another terminal, confirm three things:

```bash
curl -s localhost:8080/ | head -5
curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/entity/service/ledger-api/
curl -s localhost:8080/search-index.json | head -c 200
```

Expected: the catalog page, `200`, and the index. Open `http://localhost:8080/` and **use the search box** — this is the first place it can work, because `fetch()` is blocked on `file://` pages.

Then edit `testdata/monorepo-ok/services/ledger-api/service.yaml` (change the description) and reload. Expected: `change detected, rebuilding` on the server's stderr and the new text in the page.

Finally, break it — set `owner: nobody` — and reload. Expected: the diagnostics on stderr, `build failed; still serving the previous version`, and the last good page still served.

- [ ] **Step 8: Commit**

```bash
git add cmd/landsraad/serve.go cmd/landsraad/serve_test.go cmd/landsraad/main.go go.mod go.sum
git commit -m "feat: landsraad serve --watch

The site is served from memory and nothing is written to disk, so the
watcher cannot trigger itself by rebuilding into the tree it watches.
That falls out of Build returning values rather than writing files.

fsnotify is not recursive: a file created inside a directory made after
startup produces no event at all. A Create naming a directory re-walks
and adds it, and there is a test for exactly that. .git is skipped, or an
ordinary git command becomes a rebuild storm.

A failed rebuild keeps serving the last good site: a preview that goes
blank on a typo is a preview you stop trusting."
```

---

## Task 15: End-to-end verification and the documentation

**Files:**
- Modify: `cmd/landsraad/integration_test.go`
- Modify: `Taskfile.yml`
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`
- Modify: `docs/superpowers/specs/2026-09-08-landsraad-design.md` (§13, per ruling R10)
- Modify: `CLAUDE.md` (the plan map, which this split makes wrong)

**Interfaces:** none new. This task proves what the previous fourteen built and writes it down.

**Context:** `cmd/landsraad/integration_test.go` already chains `gen`, `validate` and `score` at the CLI level. `build` belongs in that chain: the portal is generated from the same catalog those commands validate and score, and the interesting failure is the one where they disagree.

The spec amendment is not optional bookkeeping. §13 currently states a package layout that **cannot be built** — `go:embed` cannot reach a top-level `web/` from `internal/render`. Leaving it means the next reader trusts a document that is wrong about the code, which is the specific failure the project's own CLAUDE.md is written against.

- [ ] **Step 1: Write the failing integration test**

Append to `cmd/landsraad/integration_test.go`:

```go
// A portal built from the fixture repository must be complete: every entity
// has a page, every page the catalog links to exists, and the site is
// internally consistent. This is the assertion that the fourteen unit-level
// tasks actually compose.
func TestBuildProducesACompletePortal(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "dist")

	files, code := Build(os.DirFS("../../testdata/monorepo-ok"), io.Discard, BuildOptions{
		Now:      time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		LastEdit: noLastEdit(),
		Version:  "v0.0.0-test",
		Mermaid:  render.Mermaid{Src: render.DefaultMermaidSrc, Integrity: render.DefaultMermaidIntegrity},
	})
	if code != exitOK {
		t.Fatalf("build of the known-good fixture failed with exit %d", code)
	}
	if err := writeSite(out, files, false, io.Discard); err != nil {
		t.Fatalf("writeSite: %v", err)
	}

	present := map[string]bool{}
	for _, f := range files {
		present[f.Path] = true
	}
	for _, want := range []string{
		"index.html",
		"scorecard/index.html",
		"map/index.html",
		"search-index.json",
		"assets/style.css",
		"assets/chroma.css",
		"assets/search.js",
		"assets/catalog.js",
		"assets/runtime.js",
		"assets/mermaid.js",
		"entity/service/ledger-api/index.html",
		"entity/service/payments-worker/index.html",
		"entity/topic/payments-events/index.html",
		"team/team-payments/index.html",
	} {
		if !present[want] {
			t.Errorf("the portal is missing %s", want)
		}
	}

	// Every internal href must resolve to a file the build produced. A
	// portal that links to its own 404s is the failure this whole plan's
	// URL scheme exists to prevent.
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".html") {
			continue
		}
		for _, href := range hrefs(string(f.Data)) {
			if strings.HasPrefix(href, "http") || strings.HasPrefix(href, "#") || href == "" {
				continue
			}
			target := path.Join(path.Dir(f.Path), href)
			if strings.HasSuffix(target, "/") || !strings.Contains(path.Base(target), ".") {
				target = path.Join(target, "index.html")
			}
			if !present[target] {
				t.Errorf("%s links to %q which resolves to %q, and nothing generated it", f.Path, href, target)
			}
		}
	}
}

// hrefs pulls every href and src value out of a page. Deliberately crude:
// this is a link checker for our own generated markup, not an HTML parser.
func hrefs(page string) []string {
	var out []string
	for _, attr := range []string{`href="`, `src="`} {
		rest := page
		for {
			i := strings.Index(rest, attr)
			if i < 0 {
				break
			}
			rest = rest[i+len(attr):]
			j := strings.Index(rest, `"`)
			if j < 0 {
				break
			}
			out = append(out, rest[:j])
			rest = rest[j:]
		}
	}
	return out
}

// A second build over the same directory must remove the page of an entity
// that has left the catalog (ruling R16).
func TestRebuildPrunesADeletedEntity(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "dist")
	opts := BuildOptions{
		Now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		LastEdit: noLastEdit(), Version: "v0.0.0-test",
	}

	full, code := Build(os.DirFS("../../testdata/monorepo-ok"), io.Discard, opts)
	if code != exitOK {
		t.Fatalf("first build exited %d", code)
	}
	if err := writeSite(out, full, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(out, "entity", "topic", "payments-events", "index.html")
	if _, err := os.Stat(page); err != nil {
		t.Fatalf("first build did not write the topic's page: %v", err)
	}

	// Rebuild with that entity's page absent from the set.
	var without []emit.File
	for _, f := range full {
		if f.Path != "entity/topic/payments-events/index.html" {
			without = append(without, f)
		}
	}
	if err := writeSite(out, without, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(page); !os.IsNotExist(err) {
		t.Error("the removed entity's page survived the rebuild")
	}
}
```

Add `io`, `os`, `path`, `path/filepath`, `strings`, `time`, `internal/emit` and `internal/render` to that file's imports as needed.

- [ ] **Step 2: Run it**

Run: `go test ./cmd/landsraad/ -run 'TestBuildProduces|TestRebuildPrunes' -v`

Expected: PASS. **If the link check fails, fix the link, not the test.** A dangling internal link is a real defect in the generated site, and this test is the only thing that looks at the site as a whole.

- [ ] **Step 3: Add a Taskfile target**

In `Taskfile.yml`, after the `schema` task:

```yaml
  site:
    desc: Build the portal from the fixture repository and open it
    cmds:
      - go run ./cmd/landsraad build testdata/monorepo-ok -o dist
      - echo "built dist/ — run 'go run ./cmd/landsraad serve testdata/monorepo-ok --watch' to preview"
```

And add `dist/` to `.gitignore`.

- [ ] **Step 4: Amend the spec (ruling R10)**

In `docs/superpowers/specs/2026-09-08-landsraad-design.md` §13, replace the line

```
web/                   go:embed templates, CSS, search JS
```

with

```
internal/render/web/   go:embed templates, CSS, search and catalog JS
```

and add below the layout block:

```
The assets sit under internal/render/ rather than at the repository root
because a go:embed pattern is relative to its own package directory and may
not contain "..", so internal/render cannot reach a top-level web/. The
alternative — a root-level `package web` holding the embed — would be a
public import path, which D8 rules out. Corrected in Plan 3; earlier drafts
of this section described a layout that does not compile.
```

- [ ] **Step 5: Correct the plan map in `CLAUDE.md`**

The "Read in this order" table lists one plan; there are now three. And the
sentence below it — "Scorecard and generated artefacts are Plan 2; fetch
adapters and the renderer are Plan 3" — is wrong, because the renderer and the
fetcher were split. Replace both:

```markdown
| | |
|---|---|
| Design, decisions D1–D13, rationale | `docs/superpowers/specs/2026-09-08-landsraad-design.md` |
| Plan 1 — catalog core, 11 tasks | `docs/superpowers/plans/2026-09-08-landsraad-catalog-core.md` |
| Plan 2 — scorecard and generated artefacts, 12 tasks | `docs/superpowers/plans/2026-09-09-landsraad-scorecard-and-artifacts.md` |
| Plan 3 — the portal renderer, 15 tasks | `docs/superpowers/plans/2026-09-09-landsraad-portal-renderer.md` |
| Composition principles in depth | `/composition` |

Plan 1 delivered scope A, the catalog core (`validate`). Plan 2 delivered B and
C (`gen`, `score`). Plan 3 delivers D, the portal (`build`, `serve`), against
the local repository. Plan 4 is Carryall — multi-repo fetching over the GitHub
and GitLab APIs — and is the only part of v1 still unbuilt.
```

Also add the renderer's rules to the "Non-negotiable" table's context: the
`no-os-in-internal` rule is unchanged but now has its most tempting violation,
so add a row to the notes below that table:

```markdown
The renderer is where the `os` rule bites hardest — generating a directory tree
is exactly where a contributor reaches for `os.MkdirAll`. The answer is always
to return an `emit.File` and let `cmd/` write it. That is also what makes
`serve --watch` able to rebuild in memory.
```

- [ ] **Step 6: Update the README**

Replace the "What exists today" section:

```markdown
## What exists today

**`landsraad validate`**, **`gen`**, **`score`**, **`build`** and **`serve`** all
work. Validation is hermetic and offline; `gen` derives CODEOWNERS, alert
routing and a Slack map; `score` measures the catalog against `standards.yaml`;
`build` renders a static portal and `serve --watch` previews it.

What is **not** built: fetching remote repositories over the GitHub and GitLab
APIs. `build` renders the repository it is run in, and warns when `repos.yaml`
names repositories it could not read.
```

Replace the "Status" section:

```markdown
## Status

Implemented: schema validation, ownership checks against `teams.yaml`,
dependency-cycle detection, four CI-friendly output formats
(`text`, `json`, `github`, `gitlab`), generated ownership artifacts with a
`--check` gate, a tier-aware scorecard with history, and the static portal.

Designed but not built: multi-repo fetching over the GitHub and GitLab APIs.
See `docs/superpowers/specs/2026-09-08-landsraad-design.md`.
```

And add a section after "Wiring into CI":

```markdown
## The portal

```bash
landsraad build -o dist      # render the static site
landsraad serve --watch      # preview at localhost:8080, rebuilding on change
```

`build` writes a `.landsraad-manifest` into the output directory listing what it
produced, so a later build removes the page of a service that has left the
catalog. It refuses to write into a non-empty directory it did not create;
pass `--force` if you mean it.

Dependency diagrams load Mermaid from a pinned CDN URL with an integrity hash.
On a host with no outbound network:

```bash
landsraad build --mermaid-src ./vendor/mermaid.min.js   # copied into the site
landsraad build --mermaid-src none                      # no diagrams
```

Search needs `fetch()`, which browsers block on `file://` pages. Use
`landsraad serve`, or host the output, to try it.
```

- [ ] **Step 7: Update CONTRIBUTING**

Add a section after "Adding a new output format":

```markdown
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

Rendered Markdown is the only place `template.HTML` appears, in
`internal/render/docs.go`. It is safe because `md.New()` configures goldmark
**without** `WithUnsafe` (spec §14.1), so raw HTML in a runbook was already
escaped into text. Do not reuse that cast on bytes that have not been through
`md.Render`.
```

- [ ] **Step 8: Full verification**

```bash
task ci
```

Expected: green. `go vet`, `gofmt -l`, `go mod tidy -diff`, `scripts/check-rules.sh` and every test.

Then confirm the two rules this plan stressed hardest:

```bash
grep -rn '"os"' internal/ --include='*.go' | grep -v '_test.go'   # must be empty
grep -rn 'WithUnsafe' internal/                                   # must be empty
```

Expected: no output from either.

- [ ] **Step 9: Commit**

```bash
git add cmd/landsraad/integration_test.go Taskfile.yml .gitignore README.md CONTRIBUTING.md CLAUDE.md docs/superpowers/specs/2026-09-08-landsraad-design.md
git commit -m "test: end-to-end portal build, and document it

The integration test checks every internal href against the set of files
the build produced. A portal that links to its own 404s is the failure
the whole URL scheme exists to prevent, and nothing else looks at the
site as a whole.

Spec §13 described a layout that cannot be built: go:embed cannot reach a
top-level web/ from internal/render. Corrected, with the reason."
```

---

## Definition of done

- `task ci` green.
- `landsraad build -o dist` renders the catalog, an entity page per entity, a page per team, the scorecard, the dependency map, the search index and every asset.
- Every internal link in the generated site resolves to a file the build produced — asserted, not assumed.
- `landsraad build` exits 2 on a broken catalog and writes nothing.
- A rebuild removes the page of an entity that left the catalog; a non-empty directory with no manifest is refused without `--force`.
- `landsraad serve --watch` previews from memory, picks up a newly created directory, and keeps serving the last good site when a rebuild fails.
- `--mermaid-src` accepts a URL, a local path or `none`, and the diagrams degrade visibly rather than silently.
- Raw HTML in a runbook is escaped; `grep -rn 'WithUnsafe' internal/` is empty.
- Nothing under `internal/` imports `os`/`os/*` or calls `time.Now()`.
- `go doc ./internal/render` reads as an explanation rather than a list.

## Before merging

Run `composition-auditor` (`.claude/agents/composition-auditor.md`). It audits abstractions in both directions and tests whether this project's documents' own claims are true. Plan 1's audit found eleven real defects and Plan 2's found more; expect this one to probe:

- whether `render.Input` earns being one struct or is three unrelated concerns wearing a coat;
- whether `entityPages` returning `([]emit.File, []RenderedDoc)` is a seam or a shortcut that couples the search index to the page renderer;
- whether the claim "`serve` cannot trigger itself" is actually true, or only true until someone adds a cache directory;
- whether `md.Doc`'s four fields are one type or four callers' wishes merged;
- whether this document's own rulings match what the code ended up doing.

## What Plan 4 adds

Plan 4 is **Carryall**, the multi-repo fetcher — spec stage 2, decision D5.

- `internal/fetch`: a `Fetcher` interface returning an `fs.FS`, with GitHub and GitLab adapters over the host archive APIs, tested against `httptest` with recorded responses and **zero network in the test suite**.
- Tokens from the environment, never from `repos.yaml` (spec §14.1).
- A remote `LastEditFunc` over `GET /commits?path=…&per_page=1`, the documented cost of D5 (spec §9).
- `build` loops over `repos.yaml`, merging one catalog from many filesystems, and `partialNotice` in `cmd/landsraad/build.go` is **deleted** — replaced by real fetching plus `--allow-partial`, which stamps a banner naming every repo that actually failed (spec §12).

> **Corrected on 2026-09-10, by Plan 4.** This section originally ended:
> *"Nothing below `cmd/` changes: every stage in this plan already takes an
> `io/fs.FS`, which is the whole point of spec §3.1's seam."* That is wrong.
> One `fs.FS` per **stage** is not one `fs.FS` per **entity**: once a merged
> catalog holds entities from three repositories, `CheckFiles`,
> `scorecard.Env`, `scorecard.Ingest`, `LastEditFunc` and `render.Input` are
> each answering with the wrong filesystem for two thirds of the catalog.
> Plan 4 changes all five, threading a `catalog.Sources` through them
> (ruling R23). The seam was real and it did its job — every one of those
> takes an interface rather than a path, which is why Plan 4 adds a
> filesystem *implementation* and no stage learns what a repository is. It
> was simply one-dimensional, and the catalog is two-dimensional.
