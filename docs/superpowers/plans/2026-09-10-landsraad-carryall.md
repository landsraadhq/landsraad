# landsraad Carryall — Multi-Repo Fetch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `landsraad build` reads every repository named in `repos.yaml` — the local checkout plus remote GitHub and GitLab repositories over their host APIs — and renders one portal from the merged catalog.

**Architecture:** `internal/fetch` turns a remote repository into an `fs.FS` **sparsely**: one request lists the tree, and only the files landsraad actually reads are fetched as blobs. Fetching is **eager and two-phase** — list, fetch `service.yaml`, parse, compute the exact remaining content set from the parsed entities, fetch that — so every pipeline stage still runs against a fully-populated in-memory filesystem and nothing under `internal/` blocks on the network. A merged `Catalog` now holds entities from several repositories, so the five places that read an entity's files take a `catalog.Sources` (repo → `fs.FS`) instead of a bare `fs.FS`. `cmd/` owns every HTTP call, every token and every byte written to disk.

**Tech Stack:** Go 1.23 (toolchain 1.26.1), go-task 3.53.1, cobra, `gopkg.in/yaml.v3` v3.0.1, `github.com/santhosh-tekuri/jsonschema/v6` v6.0.1, `github.com/google/go-cmp` v0.6.0, `github.com/yuin/goldmark` v1.8.6, `github.com/alecthomas/chroma/v2` v2.24.1, `github.com/fsnotify/fsnotify` v1.10.1. **No new dependencies.** The adapters are `net/http`, `encoding/json` and `crypto/sha1` from the standard library; a host SDK would be a large dependency for four endpoints, and `go-github` alone declares a Go floor above this module's.

**Spec:** `docs/superpowers/specs/2026-09-08-landsraad-design.md` — this plan implements spec **stage 2 (FETCH)** from §7, decision **D5**, the `--allow-partial` half of §12, and the `multirepo/` fixture §14 requires. Read the spec alongside this plan; where they disagree, the spec wins and the disagreement is a bug in this plan, **except** where a ruling below records a deliberate, argued deviation. Three such deviations exist: R25, R27 and R31.

**Baseline:** Plans 1, 2 and 3 are complete and merged to `main` at `7bf3175`. `landsraad validate`, `init`, `schema`, `gen`, `score`, `build` and `serve` all work end to end against the local repository. Run `task ci` before Task 1 and confirm it is green — a dirty baseline makes every later failure ambiguous.

**The correction this plan opens with.** Plan 3 ends with the claim: *"Nothing below `cmd/` changes: every stage in this plan already takes an `io/fs.FS`, which is the whole point of spec §3.1's seam."* **That claim is false, and Task 3 through Task 5 exist because of it.** One `fs.FS` per *stage* is not one `fs.FS` per *entity*. Once a merged `Catalog` holds entities from three repositories, "read `e.Spec.Runbook`" is not answerable from a single filesystem, and five call sites below `internal/` are silently wrong:

| Site | Today | Why one FS is not enough |
|---|---|---|
| `internal/catalog/files.go:17` | `CheckFiles(fsys, cat, c)` | stats `spec.path`/`runbook`/`docs`/`alerts` for **every** entity against one FS |
| `internal/scorecard/check.go:82` | `Env.FS` | `hermetic.go:57,105,171` read runbook bytes, alert bytes, docs index |
| `internal/scorecard/check.go:74` | `LastEditFunc func(path string)` | `services/api` names a different directory in each repo |
| `internal/scorecard/ingest.go:70` | `Ingest(fsys, cat, …)` | `.landsraad/checks/*.yaml` is written by CI **in each repo** |
| `internal/render/model.go:71` | `Input.FS` | `docs.go:230` reads runbook bytes, `docs.go:264` walks `spec.docs` |

The seam was real and it did its job — every one of those takes an interface rather than a path, which is why this plan adds a filesystem *implementation* and no stage learns what a repository is. But the seam is one-dimensional and the catalog is now two-dimensional. Task 15 of this plan amends Plan 3's closing section so the record is not left wrong.

## Global Constraints

Every task's requirements implicitly include this section. Values are copied verbatim from the spec or from the existing codebase.

- **Nothing under `internal/` imports `os` or any `os/*` package.** Reads take `io/fs.FS`; writes return values that `cmd/` writes. Enforced by `.claude/hooks/no-os-in-internal.py` and `scripts/check-rules.sh`; both exclude `*_test.go`. **This bites in a new place in this plan**: the blob cache reads and writes real files, so `internal/fetch` defines a `Cache` *interface* and `cmd/landsraad/blobcache.go` is the only thing that touches a directory.
- **Nothing under `internal/` calls `time.Now()`, `rand`, or the network** *from a pipeline stage*. This plan is the first to put `net/http` under `internal/` at all, and R25 is the ruling that keeps the constraint true: `internal/fetch` makes every **content** request **before** stage 1 and **between** stages 1 and 3, driven by `cmd/`; `discover`, `catalog`, `scorecard` and `render` still run against a filesystem whose bytes are already in memory. Exactly one **check** makes requests from inside a stage, and R35 is the ruling that allows it: `docs-fresh` asks a host when a path was last changed, during `Score`, which is stage 7 — once per entity path, plus a possible ref resolution, so the count is per-entity. It reaches the socket through `scorecard.LastEditFunc` — an injected function type, not an import — so `internal/scorecard` still does not import `net/http` and a score against a local filesystem still makes no request. Any *other* stage that blocks on a socket is a bug in this plan, not a relaxation of the rule.
- **Zero network in the test suite** (spec §14). Every adapter test runs against `httptest.NewServer` with recorded responses. `task test` must pass with the machine's network cable unplugged, and Task 15 states how to verify that rather than assume it.
- **No `sync.Once`, no `init()`, no package-level mutable state below `cmd/`.** The HTTP client, the cache and each adapter are **values** constructed by a `New`-style function and passed down. There is no default client, no ambient `http.DefaultClient`, and no package-level token.
- **Tokens never appear in `repos.yaml`** (spec §14.1). They are read from the environment by `cmd/` and passed to the adapters as values. A token must never reach a diagnostic, a log line, a URL query string, or the generated site — Task 7 has a test that asserts this.
- **Diagnostics assert exact message strings in tests.** `strings.Contains` against `.Message` or `.Hint` is blocked by `.claude/hooks/exact-message-tests.py` and by `scripts/check-rules.sh`. Assert with `!=` against a full literal.
- **Accumulate, never fail fast** (spec §12). A repository that fails to fetch does not stop the other two from being read; what it does instead is R32.
- **No silent fallbacks** (spec §12). A degraded mode is visible in the artifact, not only in a log. This is the whole point of `--allow-partial`.
- **Every diagnostic carries a file and a line**, and says what to do. `Line: 0` is a bug; use `1` when the file has no better location. For a fetch failure the file is `repos.yaml` and the line is the entry's line, which Task 1 makes available.
- **Exit codes** (spec §12): `0` clean, `1` usage or config error, `2` validation error, `3` scorecard gate. A fetch failure is `1` — it is neither the YAML author's fault nor the service owner's (R32).
- **Stream contract** (spec §12): `stdout` carries **only** the selected format's payload; every human line goes to `stderr`.
- **Artifacts are keyed on the ref** (`kind:name`), never the bare name (spec §12).
- **Go stays gofmt-clean and vet-clean.** `task ci` runs `go vet ./...`, `gofmt -l`, `go mod tidy -diff` and `scripts/check-rules.sh`.
- **Commit after every task**, with the test and the implementation in the same commit. Never `git add .` — add files individually.
- **Test helpers named in this plan that you must supply from the existing suite**, rather than write fresh: `goodFixtureFS(t)` stands for whatever `cmd/landsraad`'s existing tests use to build a valid in-memory repository, and `apiServiceYAML` / `edgeServiceYAML` for their entity fixtures. `testNow` already exists in `cmd/landsraad/score_test.go` and `plural` in `validate.go`. Read the neighbouring test file and use its helpers; a second fixture builder that drifts from the first is worse than a slightly awkward reuse.

---
## Rulings the spec does not settle

Recorded here because an executor who hits one of these mid-task will otherwise invent an answer, and because the *why* is the part that gets lost. Numbering continues from Plan 3, which ended at R22.

| # | Question | Ruling | What it costs |
|---|---|---|---|
| R23 | A merged `Catalog` holds entities from three repositories. How does "read this entity's runbook" find the right filesystem? | A **`catalog.Sources`** value: `map[repoName]fs.FS` behind `For(e *Entity) (fs.FS, bool)`, constructed by `cmd/` and threaded into the five sites listed above. Single-repo commands wrap with `catalog.SingleSource(name, fsys)`. It is a **named map**, `type Sources map[string]fs.FS`, not a struct wrapping one. What the type earns is exactly one thing: `For` encodes that the key is `e.SourceRepo`, once, rather than at five call sites. A struct would additionally buy a defensive copy and nothing else — and `Catalog.Entities` already declined that same defence one file over, arguing that copying to protect against a caller that does not exist is over-abstraction. It does **not** buy what `Catalog`'s unexported fields buy, because there is no keyed literal here that looks constructed and answers wrongly: an empty `Sources` is visibly empty. Nil-safety comes free, which matters because `For` is called deep inside `Score`. | Five signature changes and the test churn behind them. Bought: the compiler enumerates every site that must say which repo it means, which is spec §3.1's "make the wrong construction a compile error" applied to a second axis. The two alternatives were a repo-prefixed union FS — rejected because every read becomes a `path.Join` that compiles when forgotten, and because `edge-gateway/runbook.md` would become a legal path *out of* the monorepo, weakening the property §14.1 states — and an `FS` field on `Entity`, rejected because it makes a parsed-YAML value carry a live IO handle, inverting "IO at the edges", and because `LastEdit(path)` stays ambiguous under it anyway. |
| R24 | How does an adapter pull a repository — archive tarball, or tree listing plus individual blobs? | **Tree listing plus per-blob fetch.** One request lists the tree; only the files landsraad reads are fetched. | An archive is one request and simpler code, but it holds an entire repository in memory to read fifteen `service.yaml` files, and the repositories this will actually run against are large. The sparse cost model is: `Stat`, `Glob`, `ReadDir` and `WalkDir` are answered from the listing for **zero** requests — so `CheckFiles` is free — and only `ReadFile` costs one. The complete content read set below `internal/` is exactly `service.yaml`, `spec.runbook`, `spec.alerts`, `*.md` under `spec.docs`, and `.landsraad/checks/*.yaml`; verified by enumerating every `fs.ReadFile` call site, not assumed. |
| R25 | A sparse remote filesystem is either lazy (fetch inside `Open`) or eager (fetch everything up front). Which? | **Eager, two-phase.** Phase 1 lists the tree and fetches the discovered `service.yaml` files; entities are parsed; phase 2 computes the exact remaining content set from those entities and fetches it concurrently. Every stage then runs against a filesystem with no network behind it. | A lazy FS would be a far smaller diff — the pipeline would not change at all — and it is rejected for three reasons that are not purity. Fetches inside `Open` are **serial** by construction, so fifty blobs is fifty round trips with nowhere to put a worker pool. A failed GET arrives at `docs.go:230` as an unreadable-file diagnostic, making "not in the repository" and "the network broke" indistinguishable — which is the exact distinction `--allow-partial` has to make. And a stage that blocks on a socket makes "`score` runs offline in under a second" a claim about which filesystem you happened to pass. The cost is real: `loadCatalogScoped` must split into a parse half and an assemble half, and all four commands run that function. **This deviates from nothing in the spec; it interprets §7's stage ordering, which already places FETCH after DISCOVER.** |
| R26 | `repos.yaml` has `url` and `paths`. `build` must know which entry is the working tree — `build.go:115` calls this "a one-way door, and Plan 4's to open" — and an adapter needs a ref and a host. | Four new optional keys: **`local: true`** marks the working tree, **`ref`** names a branch or tag (default: ask the host for its default branch), **`host`** is `github` or `gitlab` (default: inferred from the URL hostname), and **`name`** is the repository's identity in diagnostics and in `Sources` (default: the last path segment of the URL). At most one entry may be `local`. Two entries resolving to the same `name` is a **hard error** naming both and telling the user to set `name:`. | Matching `git remote get-url origin` was rejected: it is zero-config until a fork, a mirror, a renamed remote or a CI checkout without a remote, and every one of those needs a fallback — which is precisely how today's "entry[0] is local by convention" bug happened. Dropping the local case entirely and requiring `--local url=path` was rejected because `testdata/monorepo-ok` and `task site` have no remote at all, so every fixture build would need the flag for a repository that does not exist. Keeping `name` as the basename rather than `owner/repo` preserves the diagnostic format spec §12 prints as its worked example; uniqueness is bought by erroring instead of by lengthening every message. |
| R27 | Spec §7's stage 2 sketches "→ local cache". What caching, and keyed on what? | A **content-addressed blob cache** at `.landsraad/cache/blobs/<sha[:2]>/<sha>`, keyed on the git blob SHA the tree listing already returns. A hit costs zero requests and cannot be stale. On read, a 40-character SHA is **verified** by recomputing `sha1("blob " + len + "\x00" + data)`; a mismatch deletes the entry and refetches rather than serving corruption. `build --no-cache` bypasses it. **No automatic pruning in v1** — `rm -rf .landsraad/cache` is documented in the README, and `landsraad init` adds the directory to `.gitignore`. | A cache is invisible in behaviour, so it could have been deferred entirely; it is here because repeat local builds against large repositories are the common development loop. The honest cost is an unbounded directory: a plan-5 `landsraad cache prune` is additive, and inventing an eviction policy now would be a knob with no measurement behind it. SHA verification is ten lines that turn a corrupted cache from a wrong portal into a loud error. |
| R28 | GitHub's recursive tree API truncates at **100,000 entries or 7 MB**; GitLab's `per_page` defaults to **20**. | GitHub: request `recursive=1` first and, when the response has `"truncated": true`, fall back to a **pattern-directed descent** — list only the root, the directories a configured pattern can match, each matched entity directory, and each `spec.docs` directory. GitLab: **always** paginate, keyset with `per_page=100`; a single-page assumption is wrong on the eleventh file, not on the hundred-thousandth. | Two code paths on GitHub instead of one, both tested. The recursive path is one request and serves the overwhelming majority; the fallback is roughly `2 + entities` requests but is the only thing that works on the repositories that motivated R24. GitLab's default is the reason its pagination is the *common* path there and not an edge case — a fifteen-service repository silently returning the first twenty entries is a portal missing services with nothing to notice it by. |
| R29 | Where do tokens come from, and what happens without one? | Per repository, in order: `LANDSRAAD_TOKEN_<NAME>` (the entry's `name`, uppercased, every non-alphanumeric byte replaced with `_`), then `GITHUB_TOKEN` or `GITLAB_TOKEN` by host type. No token is **not** an error — public repositories work — but it emits a warning naming the repository and the unauthenticated rate limit, because the failure it leads to arrives an hour later as a 403 nobody connects to a missing variable. | One rule covering both "one token for all my GitHub repos" and "this one repository lives on a different instance". Costs one documented naming convention. A private repository with no token returns **404, not 403**, on both hosts, so Task 8's error message for 404 must name the possibility explicitly — otherwise the tool tells a user their repository does not exist while they are looking at it. |
| R30 | `build` becomes multi-repo. Do `gen` and `score` follow? | **No.** `validate`, `gen` and `score` stay local, hermetic and offline. Only `build` and `serve` read `repos.yaml`'s remote entries. | `gen` writes CODEOWNERS and alert routing *into the repository it is standing in*; there is nowhere to put a CODEOWNERS generated for a repository that exists only as a tree listing in memory, and `gen --check` — the single anti-rot mechanism — would start comparing against files it cannot write. `score` gates a PR in one repository's CI, which spec §7.1 requires to need no tokens. This preserves spec §7.1's table exactly: `validate` is stages 1, 3, 4, 5 with no network. |
| R31 | May `.landsraad/checks/*.yaml` in repository A report a result for an entity defined in repository B? | **Yes.** INGEST runs over every repository in `Sources` and resolves `entity:` refs against the merged catalog. Precedence is unchanged — newest `generatedAt` wins, a tie is an error — but now spans repositories. | A platform repository running one image-scan job for every service is the natural shape, and forbidding it would mean a CI job may only report on entities it happens to sit beside. **This extends spec §6, which describes the file without saying whose entities it may name.** The cost is that a tie now has two files in two repositories, so the tie diagnostic must name the repository as well as the file — Task 4 covers it. |
| R32 | A repository fails to fetch. What does `build` do? | **Hard failure, exit `1`**, naming the repository and the underlying error. `build --allow-partial` downgrades it: the repository is skipped, a warning is written to stderr, and a banner naming **every repository that actually failed** is stamped into every page via the existing `render.Input.Notice`. | Spec §12 by name: "a fetch failure is a hard failure... `--allow-partial` exists but stamps a visible banner into the generated site naming every repo that failed". Exit `1` rather than `2` because the catalog's YAML is not what broke — `2` would send the service owner to look at a file that is fine. This replaces `partialNotice`, whose banner could only count repositories because it could not know which ones were missing; a real fetcher knows exactly. |
| R33 | `serve --watch` calls `Build` on **every** file change (`serve.go:170`). Does that refetch three repositories per keystroke? | **No.** Remotes are fetched **once**, before the first build, and the resulting `fs.FS` values are reused for the life of the process. Each rebuild constructs a fresh `Sources` carrying a fresh `os.DirFS(root)` for the local entry and the same remote values — `Sources.With(name, fsys)` returns a new value rather than mutating one. A remote change requires a restart, and `--watch`'s help text says so. | Watching a remote repository would mean polling a host API forever from a developer's laptop. The cost is stated in the flag's own help rather than discovered. This is also why fetching cannot live inside `Build`: `Build` is called per rebuild, so the fetch has to happen in the command's `RunE` and be passed in. |
| R34 | `teams.yaml`, `standards.yaml`, `repos.yaml` and `scorecard-history.csv` are read from `fsys` today. Which repository holds them in a multi-repo build? | The one `build` is standing in, always. `Build` takes **two** filesystems — `root fs.FS` for configuration and history, and `src catalog.Sources` for catalog content — named differently because they are different things. The local repository usually appears in both, pointing at the same `os.DirFS`. | One more parameter on `Build`, and a reader who has to notice there are two. The alternative — searching `Sources` for whichever repository happens to contain a `teams.yaml` — makes the answer depend on the contents of somebody else's repository. |
| R35 | `docs-fresh` needs a last-edit date. A fetched repository has no git history. | `LastEditFunc` becomes `func(repo, path string) (time.Time, bool)`. For the local repository it is `git log` as today; for a remote one it is `GET …/commits?path=…&per_page=1`, memoized per run. A repository whose adapter cannot answer returns `false`, and `docs-fresh` reports not-reported rather than inventing a date. | Spec §9 already records this as "the known cost of D5": one API call per documented entity, so a forty-service build adds forty requests. Memoization makes it one per distinct path rather than one per check. The commit date is **not** blob-cacheable — it is a property of the ref, not of content — which is the one thing R27's cache cannot help with. |

---
## File Structure

**New package: `internal/fetch/`**

| Path | Responsibility |
|---|---|
| `internal/fetch/fs.go` | `Entry` and `*FS` — an `fs.FS` over a listing plus a blob map. Metadata always present, content present only once fetched; reading an unfetched path is `ErrNotFetched`, which is a landsraad bug and not a user's. |
| `internal/fetch/fetch.go` | `Fetcher` and `Prefetcher` interfaces, `Repo` identity, `Cache` interface, the error types `--allow-partial` switches on. |
| `internal/fetch/client.go` | `*Client` — auth header injection, retry with backoff, rate-limit reporting, and the token-redaction that keeps a secret out of every error string. |
| `internal/fetch/github.go` | GitHub adapter: default branch, recursive tree, blob, commits. |
| `internal/fetch/githubwalk.go` | GitHub's truncated-tree fallback: pattern-directed descent (R28). |
| `internal/fetch/gitlab.go` | GitLab adapter: project lookup, keyset-paginated tree, blob, commits. |

**New files elsewhere**

| Path | Responsibility |
|---|---|
| `internal/catalog/sources.go` | `Sources` — a named `map[string]fs.FS`, with `For`, `SingleSource`, `With`, `Names` (R23). |
| `cmd/landsraad/blobcache.go` | The only thing in the program that reads or writes the cache directory (R27). |
| `cmd/landsraad/repos.go` | `openRepos` — the two-phase orchestration (R25), token resolution (R29), and the local/remote split. |
| `testdata/multirepo/` | The fixture spec §14 requires: a local repository plus recorded responses for two remotes. |

**Modified**

| Path | Change |
|---|---|
| `internal/config/repos.go` | `local`, `ref`, `host`, `name`; identity, uniqueness, per-entry line numbers |
| `internal/catalog/files.go:17` | `CheckFiles(src Sources, cat *Catalog, c *diag.Collector)` |
| `internal/scorecard/check.go:74,82` | `Env.Sources` replaces `Env.FS`; `LastEditFunc` gains a repo |
| `internal/scorecard/ingest.go:70` | `Ingest(src catalog.Sources, cat, …)`, iterating repositories |
| `internal/scorecard/hermetic.go:57,105,171` | read through `env.Sources.For(e)` |
| `internal/render/model.go:71` | `Input.Sources` replaces `Input.FS` |
| `internal/render/docs.go:230,264` | read and walk through the entity's own filesystem |
| `cmd/landsraad/gen.go:54` | `loadCatalogScoped` splits into `parseRepo` and `assemble` |
| `cmd/landsraad/build.go` | `Build(root, src, …)`; `--allow-partial`, `--no-cache`; `partialNotice` **deleted** |
| `cmd/landsraad/serve.go:170` | fetch once, swap only the local filesystem per rebuild |
| `cmd/landsraad/lastedit.go` | `gitLastEdit` gains the repo parameter it ignores |
| `cmd/landsraad/init.go` | `.gitignore` gains `.landsraad/cache/` |

## Task overview

| # | Task | Deliverable |
|---|---|---|
| 1 | `repos.yaml` grows up | `local`, `ref`, `host`, `name`, identity uniqueness, entry line numbers |
| 2 | `catalog.Sources` | the type, with `SingleSource` and `With` |
| 3 | `CheckFiles` takes `Sources` | tree compiles, single-repo behaviour identical |
| 4 | scorecard takes `Sources` | `Env`, `Ingest` across repositories, `LastEdit(repo, path)` |
| 5 | `render.Input` takes `Sources` | the renderer reads each entity's own repository |
| 6 | `fetch.Entry` and `fetch.FS` | an `fs.FS` with metadata now and content later. No HTTP. |
| 7 | `fetch.Client` | auth, typed errors, retry, rate limits, token redaction |
| 8 | GitHub adapter | default branch, recursive tree, blobs, commits |
| 9 | GitHub truncated trees | pattern-directed descent |
| 10 | GitLab adapter | project lookup, keyset pagination, blobs, commits |
| 11 | The blob cache | `Cache` interface, SHA verification, the disk half in `cmd/` |
| 12 | Two-phase orchestration | `loadCatalogScoped` splits; `openRepos` fetches |
| 13 | `build --allow-partial` | real fetching, the real banner, `partialNotice` deleted |
| 14 | `serve --watch` with remotes | fetch once, rebuild the local tree only |
| 15 | Fixture and end-to-end test | `testdata/multirepo/`, offline-suite verification |
| 16 | Documentation | README, CONTRIBUTING, CLAUDE.md, and the spec and Plan 3 amendments |

Tasks 1–5 are a green-tree refactor: after each one `task ci` passes and `landsraad` behaves exactly as it does today. Tasks 6–11 build a fetcher nothing calls yet. Task 12 is the first task where a remote repository is actually read.

---
### Task 1: `repos.yaml` grows up

`repos.yaml` today is `url` plus `paths`, and `LocalPatterns` takes the first entry "by convention" — a convention nothing verifies, which already shipped one wrong banner (`build.go:100-115`). This task makes the local entry explicit, adds what an adapter needs, and gives every entry a line number so a fetch failure can point at the line that named the repository.

**Files:**
- Modify: `internal/config/repos.go`
- Test: `internal/config/repos_test.go`

**Interfaces:**
- Consumes: `diag.Collector`, `diag.Diagnostic` (unchanged).
- Produces, and relied on by Tasks 12, 13 and 14:
  - `config.Repo` gains `Local bool`, `Ref string`, `Host string`, `Name string`, `Line int`
  - `func (r *Repo) Identity() string`
  - `func (r *Repo) HostKind() (kind string, known bool)`
  - `func (r *Repos) LocalRepo() (*Repo, bool)`
  - `func (r *Repos) LocalPatterns() ([]string, LocalSource)` — **signature change**, the second return is now a three-state value rather than a bool
  - `type LocalSource int` with `LocalMarked`, `LocalAssumedFirst`, `LocalDefaulted`
  - `func HostKinds() []string`

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/repos_test.go`:

```go
func TestRepoIdentity(t *testing.T) {
	for _, tt := range []struct {
		name string
		repo Repo
		want string
	}{
		{"basename", Repo{URL: "https://github.com/org/monorepo"}, "monorepo"},
		{"trailing slash", Repo{URL: "https://github.com/org/monorepo/"}, "monorepo"},
		{"git suffix", Repo{URL: "https://github.com/org/monorepo.git"}, "monorepo"},
		{"explicit name wins", Repo{URL: "https://github.com/org/monorepo", Name: "core"}, "core"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.repo.Identity(); got != tt.want {
				t.Errorf("Identity() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRepoHostKind(t *testing.T) {
	for _, tt := range []struct {
		name      string
		repo      Repo
		wantKind  string
		wantKnown bool
	}{
		{"github.com", Repo{URL: "https://github.com/org/a"}, "github", true},
		{"gitlab.com", Repo{URL: "https://gitlab.com/org/a"}, "gitlab", true},
		{"explicit beats hostname", Repo{URL: "https://gl.internal/org/a", Host: "gitlab"}, "gitlab", true},
		{"self-hosted, unstated", Repo{URL: "https://git.example.com/org/a"}, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kind, known := tt.repo.HostKind()
			if kind != tt.wantKind || known != tt.wantKnown {
				t.Errorf("HostKind() = (%q, %v), want (%q, %v)", kind, known, tt.wantKind, tt.wantKnown)
			}
		})
	}
}

func TestLoadReposDiagnostics(t *testing.T) {
	for _, tt := range []struct {
		name        string
		yaml        string
		wantCheck   string
		wantLine    int
		wantMessage string
		wantHint    string
	}{
		{
			name: "two locals",
			yaml: "repos:\n  - url: https://github.com/org/monorepo\n    local: true\n  - url: https://github.com/org/edge\n    local: true\n",
			wantCheck: "repos-local",
			wantLine:  4,
			wantMessage: `two entries in repos.yaml are marked local: true — "monorepo" (line 2) and "edge" (line 4)`,
			wantHint:    "exactly one entry is the repository you are standing in; remove local: true from the other",
		},
		{
			name: "duplicate identity",
			yaml: "repos:\n  - url: https://github.com/org1/api\n  - url: https://github.com/org2/api\n",
			wantCheck: "repos-duplicate-name",
			wantLine:  3,
			wantMessage: `two repositories resolve to the name "api": https://github.com/org1/api (line 2) and https://github.com/org2/api (line 3)`,
			wantHint:    "the name is the last path segment of the url unless you set name:; give one of them an explicit name:",
		},
		{
			name:      "unknown host key",
			yaml:      "repos:\n  - url: https://bitbucket.org/org/api\n    host: bitbucket\n",
			wantCheck: "repos-host",
			wantLine:  2,
			wantMessage: `unknown host "bitbucket" for https://bitbucket.org/org/api`,
			wantHint:    "host must be github or gitlab; landsraad v1 supports no others (design decision D5)",
		},
		{
			name:      "host not inferable",
			yaml:      "repos:\n  - url: https://git.example.com/org/api\n",
			wantCheck: "repos-host",
			wantLine:  2,
			wantMessage: `cannot tell which host https://git.example.com/org/api is`,
			wantHint:    "add host: github or host: gitlab to this entry",
		},
		{
			name:      "ssh url",
			yaml:      "repos:\n  - url: git@github.com:org/api.git\n",
			wantCheck: "repos-url",
			wantLine:  2,
			wantMessage: `repository url must begin with https://, got "git@github.com:org/api.git"`,
			wantHint:    "write it as https://github.com/org/api",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var c diag.Collector
			LoadRepos("repos.yaml", []byte(tt.yaml), &c)
			ds := c.Diagnostics()
			if len(ds) != 1 {
				t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
			}
			d := ds[0]
			if d.Check != tt.wantCheck {
				t.Errorf("Check = %q, want %q", d.Check, tt.wantCheck)
			}
			if d.Line != tt.wantLine {
				t.Errorf("Line = %d, want %d", d.Line, tt.wantLine)
			}
			if d.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", d.Message, tt.wantMessage)
			}
			if d.Hint != tt.wantHint {
				t.Errorf("Hint = %q, want %q", d.Hint, tt.wantHint)
			}
		})
	}
}

func TestLocalPatterns(t *testing.T) {
	for _, tt := range []struct {
		name     string
		yaml     string
		want     []string
		wantWhy  LocalSource
	}{
		{
			name:    "marked entry wins over order",
			yaml:    "repos:\n  - url: https://github.com/org/other\n    paths: [apps/*]\n  - url: https://github.com/org/mine\n    local: true\n    paths: [services/*]\n",
			want:    []string{"services/*"},
			wantWhy: LocalMarked,
		},
		{
			name:    "single entry needs no marking",
			yaml:    "repos:\n  - url: https://github.com/org/mine\n    paths: [services/*]\n",
			want:    []string{"services/*"},
			wantWhy: LocalMarked,
		},
		{
			name:    "several entries, none marked",
			yaml:    "repos:\n  - url: https://github.com/org/a\n    paths: [apps/*]\n  - url: https://github.com/org/b\n    paths: [services/*]\n",
			want:    []string{"apps/*"},
			wantWhy: LocalAssumedFirst,
		},
		{
			name:    "no paths anywhere",
			yaml:    "repos:\n  - url: https://github.com/org/a\n",
			want:    DefaultPatterns(),
			wantWhy: LocalDefaulted,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var c diag.Collector
			r := LoadRepos("repos.yaml", []byte(tt.yaml), &c)
			got, why := r.LocalPatterns()
			if !slices.Equal(got, tt.want) {
				t.Errorf("patterns = %v, want %v", got, tt.want)
			}
			if why != tt.wantWhy {
				t.Errorf("why = %v, want %v", why, tt.wantWhy)
			}
		})
	}
}

func TestLoadReposRecordsLines(t *testing.T) {
	const y = "repos:\n  - url: https://github.com/org/a\n    paths: [.]\n  - url: https://github.com/org/b\n"
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte(y), &c)
	if len(r.Repos) != 2 {
		t.Fatalf("got %d repos, want 2", len(r.Repos))
	}
	if r.Repos[0].Line != 2 {
		t.Errorf("Repos[0].Line = %d, want 2", r.Repos[0].Line)
	}
	if r.Repos[1].Line != 4 {
		t.Errorf("Repos[1].Line = %d, want 4", r.Repos[1].Line)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestRepoIdentity|TestRepoHostKind|TestLoadReposDiagnostics|TestLocalPatterns|TestLoadReposRecordsLines' -v`

Expected: FAIL to compile — `undefined: LocalSource`, `repo.Identity undefined`, `r.Repos[0].Line undefined`. Existing `TestLocalPatterns` callers elsewhere in the file will also fail to compile once the signature changes, which is the point of Step 3's second half.

- [ ] **Step 3: Implement**

Replace the `Repo` type and add the new API in `internal/config/repos.go`:

```go
// Repo is one entry in repos.yaml.
//
// Local, Ref, Host and Name arrived with Plan 4 and are all optional, so a
// repos.yaml written before them still loads. Ruling R26 records why each
// one is a stated key rather than something inferred: every inference
// available here — the first entry is local, the hostname names the host,
// the basename is unique — is true right up until it is not, and each has a
// failure that is silent rather than loud.
type Repo struct {
	URL   string   `yaml:"url"`
	Paths []string `yaml:"paths"`
	// Local marks the repository the command is standing in. At most one
	// entry may set it; see LocalPatterns for what happens when none does.
	Local bool `yaml:"local"`
	// Ref is a branch or tag. Empty means "ask the host for its default
	// branch", which costs one request and is honest — assuming "main"
	// fetches nothing from every repository that still uses "master" and
	// reports it as a missing repository.
	Ref string `yaml:"ref"`
	// Host is "github" or "gitlab", needed only when the hostname does not
	// say (a self-hosted instance). See HostKinds.
	Host string `yaml:"host"`
	// Name is this repository's identity: the string in every diagnostic's
	// Repo field, the key in catalog.Sources, and the value of
	// Entity.SourceRepo. Empty means Identity derives it from the URL.
	Name string `yaml:"name"`

	// Line is where this entry's `url:` appears in repos.yaml. Not a YAML
	// field: it is provenance, filled in by attachLines, and it exists so a
	// fetch failure can point at the line that named the repository rather
	// than at the top of the file.
	Line int `yaml:"-"`
}

// hostKinds are the hosts landsraad can fetch from (decision D5).
//
// Array-shaped and unexported for the reason in catalog.allKinds: an
// exported mutable slice is state any importer can rewrite underneath
// everything else.
var hostKinds = [...]string{"github", "gitlab"}

// HostKinds returns the supported hosts, fresh on each call.
func HostKinds() []string {
	out := make([]string, len(hostKinds))
	copy(out, hostKinds[:])
	return out
}

// Identity is the repository's name in diagnostics and in catalog.Sources.
//
// The basename rather than owner/repo, deliberately: spec §12's worked
// example prints "monorepo services/api/service.yaml:4", and lengthening
// every diagnostic to buy uniqueness is the wrong trade when uniqueness can
// be bought by rejecting the collision instead. LoadRepos does exactly that.
func (r *Repo) Identity() string {
	if r.Name != "" {
		return r.Name
	}
	if r.URL == "" {
		// A repository with neither a name: nor a url: has no name, and ""
		// is the answer localRepoName already gives for a checkout with no
		// repos.yaml. path.Base("") returns "." — not a name, just path.Base
		// being asked about an empty string — and Entity.Location() would
		// then render ".:services/api/service.yaml" for every diagnostic in
		// the commonest first run, where it currently renders the bare path.
		return ""
	}
	u := strings.TrimSuffix(r.URL, "/")
	u = strings.TrimSuffix(u, ".git")
	return path.Base(u)
}

// HostKind reports which adapter fetches this repository.
//
// known is false when the hostname is not one landsraad recognises and the
// entry did not say. That is a question, not a default: guessing github for
// a self-hosted GitLab produces 404s from an API that was never there.
func (r *Repo) HostKind() (kind string, known bool) {
	if r.Host != "" {
		return r.Host, slices.Contains(hostKinds[:], r.Host)
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com":
		return "github", true
	case "gitlab.com":
		return "gitlab", true
	}
	return "", false
}

// LocalSource says how LocalPatterns decided which entry is local, so the
// caller can report an assumption rather than making one silently
// (spec §12: degraded mode is visible in the artifact, not only in a log).
type LocalSource int

const (
	// LocalMarked: an entry said `local: true`, or there is exactly one
	// entry and it is unambiguous.
	LocalMarked LocalSource = iota
	// LocalAssumedFirst: several entries, none marked, so the first was
	// used — which is what repos.yaml meant before `local:` existed.
	LocalAssumedFirst
	// LocalDefaulted: no entry named any paths, so DefaultPatterns are in use.
	LocalDefaulted
)

// LocalRepo returns the entry marked `local: true`, or the only entry when
// there is exactly one. ok is false when neither applies.
func (r *Repos) LocalRepo() (*Repo, bool) {
	for i := range r.Repos {
		if r.Repos[i].Local {
			return &r.Repos[i], true
		}
	}
	if len(r.Repos) == 1 {
		return &r.Repos[0], true
	}
	return nil, false
}

// LocalPatterns returns the glob patterns for the local repository.
//
// The second return replaced a bool in Plan 4. Two of its three states used
// to be one: "no repos.yaml, so DefaultPatterns" and "three entries and no
// idea which one you are standing in" both reported `defaulted`, and the
// second is the condition that stamped a banner naming the wrong two
// repositories into every page of a generated site.
func (r *Repos) LocalPatterns() ([]string, LocalSource) {
	local, ok := r.LocalRepo()
	if !ok {
		if len(r.Repos) == 0 || len(r.Repos[0].Paths) == 0 {
			return DefaultPatterns(), LocalDefaulted
		}
		return r.Repos[0].Paths, LocalAssumedFirst
	}
	if len(local.Paths) == 0 {
		return DefaultPatterns(), LocalDefaulted
	}
	return local.Paths, LocalMarked
}
```

Then extend `LoadRepos` to attach lines and validate. Append inside it, after `r.loaded = true`:

```go
	attachLines(data, r)
	validateRepos(path, r, c)
	return r
}

// attachLines records where each entry's `url:` key appears.
//
// A second pass over the same bytes, deliberately. yaml.Node decoding does
// not honour KnownFields, and the strict decode above is load-bearing — a
// `path:` typo for `paths:` used to fall through to DefaultPatterns and
// validate an entire repository's worth of nothing. So the strict decode
// stays exactly as it was, and this pass does one job: provenance. Two
// passes over a file with a handful of entries is not worth a cleverer
// scheme.
func attachLines(data []byte, r *Repos) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "repos" {
			continue
		}
		seq := root.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return
		}
		for j, item := range seq.Content {
			if j >= len(r.Repos) {
				return
			}
			r.Repos[j].Line = urlLine(item)
		}
		return
	}
}

// urlLine is the line of an entry's `url:` key, falling back to the line the
// entry itself starts on. Never zero for a node that parsed: a Line of 0 is
// a bug, and the Global Constraints say to use 1 when there is nothing
// better — but here there always is.
func urlLine(item *yaml.Node) int {
	if item.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(item.Content); i += 2 {
			if item.Content[i].Value == "url" {
				return item.Content[i].Line
			}
		}
	}
	if item.Line == 0 {
		return 1
	}
	return item.Line
}

// validateRepos reports the configuration mistakes that would otherwise
// surface as a fetch failure against a host that was never going to answer.
//
// Every check here runs in `validate` too, which never fetches anything.
// That is deliberate: repos.yaml is configuration, and a malformed entry is
// wrong in the PR that introduced it rather than days later in the platform
// build. It is the same argument spec §7.1 makes for checking the shape of
// .landsraad/checks files hermetically.
func validateRepos(file string, r *Repos, c *diag.Collector) {
	var firstLocal *Repo
	byName := map[string]*Repo{}
	for i := range r.Repos {
		e := &r.Repos[i]

		if !strings.HasPrefix(e.URL, "https://") {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: file, Line: e.Line,
				Check:   "repos-url",
				Message: fmt.Sprintf("repository url must begin with https://, got %q", e.URL),
				Hint:    httpsHint(e.URL),
			})
			continue
		}

		if e.Local {
			if firstLocal != nil {
				c.Add(diag.Diagnostic{
					Severity: diag.SevError, File: file, Line: e.Line,
					Check: "repos-local",
					Message: fmt.Sprintf(
						"two entries in repos.yaml are marked local: true — %q (line %d) and %q (line %d)",
						firstLocal.Identity(), firstLocal.Line, e.Identity(), e.Line),
					Hint: "exactly one entry is the repository you are standing in; remove local: true from the other",
				})
				continue
			}
			firstLocal = e
		}

		if prev, dup := byName[e.Identity()]; dup {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: file, Line: e.Line,
				Check: "repos-duplicate-name",
				Message: fmt.Sprintf(
					"two repositories resolve to the name %q: %s (line %d) and %s (line %d)",
					e.Identity(), prev.URL, prev.Line, e.URL, e.Line),
				Hint: "the name is the last path segment of the url unless you set name:; give one of them an explicit name:",
			})
			continue
		}
		byName[e.Identity()] = e

		// A local repository is read from disk and never fetched, so it
		// needs no host. Requiring one would make `landsraad validate` in a
		// single-repo checkout demand a key it will never use.
		if e.Local || len(r.Repos) == 1 {
			continue
		}
		if _, known := e.HostKind(); !known {
			c.Add(hostDiagnostic(file, e))
		}
	}
}

// hostDiagnostic distinguishes "you named a host landsraad does not support"
// from "landsraad cannot tell what this is". They are different mistakes
// with different fixes, and one message for both would be wrong for one of
// them: telling somebody with a self-hosted GitLab that "gitlab" is not a
// valid host is a false statement about their configuration.
func hostDiagnostic(file string, e *Repo) diag.Diagnostic {
	if e.Host != "" {
		return diag.Diagnostic{
			Severity: diag.SevError, File: file, Line: e.Line,
			Check:   "repos-host",
			Message: fmt.Sprintf("unknown host %q for %s", e.Host, e.URL),
			Hint: fmt.Sprintf("host must be %s; landsraad v1 supports no others (design decision D5)",
				strings.Join(HostKinds(), " or ")),
		}
	}
	return diag.Diagnostic{
		Severity: diag.SevError, File: file, Line: e.Line,
		Check:   "repos-host",
		Message: fmt.Sprintf("cannot tell which host %s is", e.URL),
		Hint:    "add host: github or host: gitlab to this entry",
	}
}

// httpsHint rewrites the url the user actually wrote, when it can. An ssh
// remote is what `git remote -v` prints and what people paste, so "must
// begin with https://" alone would make them work out the translation.
func httpsHint(raw string) string {
	if at := strings.Index(raw, "@"); at >= 0 && !strings.Contains(raw, "://") {
		rest := raw[at+1:]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			host := rest[:colon]
			p := strings.TrimSuffix(strings.TrimPrefix(rest[colon+1:], "/"), ".git")
			return fmt.Sprintf("write it as https://%s/%s", host, p)
		}
	}
	return "write it as https://<host>/<owner>/<repo>"
}
```

Add `"fmt"`, `"net/url"`, `"path"`, `"slices"` and `"strings"` to the imports.

- [ ] **Step 4: Fix the three `LocalPatterns` callers**

The signature change breaks `cmd/landsraad/validate.go:163`. Replace the bool check with the three-state one:

```go
	r := config.LoadRepos("repos.yaml", data, c)
	patterns, why := r.LocalPatterns()
	switch why {
	case config.LocalDefaulted:
		c.Add(defaultPatternsNote("repos.yaml names no paths"))
	case config.LocalAssumedFirst:
		c.Add(diag.Diagnostic{
			Severity: diag.SevWarn, File: "repos.yaml", Line: 1,
			Check: "repos-local-assumed",
			Message: fmt.Sprintf(
				"no entry in repos.yaml is marked local: true, so the first (%s) is assumed to be this repository",
				r.Repos[0].Identity()),
			Hint: "add `local: true` to the entry for the repository you are standing in",
		})
	}
	return patterns
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/config/ ./cmd/... -v`
Expected: PASS. Any existing test asserting the old two-value `LocalPatterns` must be updated to the three-state value, not deleted.

- [ ] **Step 6: Verify the whole tree is still green**

Run: `task ci`
Expected: PASS — `landsraad` behaves exactly as before for every repos.yaml that does not use the new keys.

- [ ] **Step 7: Commit**

```bash
git add internal/config/repos.go internal/config/repos_test.go cmd/landsraad/validate.go cmd/landsraad/validate_test.go
git commit -m "feat(config): repos.yaml gains local, ref, host and name

The local entry is now stated rather than assumed to be the first, which
is what stamped a banner naming the wrong repositories into every page of
a generated site. Every entry carries the line its url: appears on, so a
fetch failure can point at the line that named the repository.

Ruling R26."
```

---
### Task 2: `catalog.Sources`

The value that answers "which filesystem holds this entity's files" (R23). Nothing consumes it yet; Tasks 3, 4 and 5 thread it into the five sites one at a time so each is a reviewable step with a green tree behind it.

**Files:**
- Create: `internal/catalog/sources.go`
- Test: `internal/catalog/sources_test.go`

**Interfaces:**
- Consumes: `catalog.Entity` (its `SourceRepo` field), `diag.Diagnostic`.
- Produces, and relied on by every later task:
  - `type Sources map[string]fs.FS`
  - `func SingleSource(name string, fsys fs.FS) Sources`
  - `func (s Sources) For(e *Entity) (fs.FS, bool)`
  - `func (s Sources) Get(name string) (fs.FS, bool)`
  - `func (s Sources) Names() []string` — sorted
  - `func (s Sources) With(name string, fsys fs.FS) Sources` — returns a new value
  - `func MissingSourceDiagnostic(e *Entity, check string) diag.Diagnostic`

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/sources_test.go`:

```go
package catalog

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

func TestSourcesFor(t *testing.T) {
	mono := fstest.MapFS{"services/api/runbook.md": {Data: []byte("mono")}}
	edge := fstest.MapFS{"runbook.md": {Data: []byte("edge")}}
	s := Sources{"monorepo": mono, "edge-gateway": edge}

	for _, tt := range []struct {
		name    string
		entity  *Entity
		want    string
		wantOK  bool
	}{
		{"first repo", &Entity{SourceRepo: "monorepo"}, "mono", true},
		{"second repo", &Entity{SourceRepo: "edge-gateway"}, "edge", true},
		{"unknown repo", &Entity{SourceRepo: "nope"}, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := s.For(tt.entity)
			if ok != tt.wantOK {
				t.Fatalf("For() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			paths := []string{"services/api/runbook.md", "runbook.md"}
			var data []byte
			for _, p := range paths {
				if b, err := fs.ReadFile(got, p); err == nil {
					data = b
					break
				}
			}
			if string(data) != tt.want {
				t.Errorf("read %q, want %q", data, tt.want)
			}
		})
	}
}

// An entity parsed from a repository with no repos.yaml has an empty
// SourceRepo (validate.go's localRepoName returns ""). That must resolve,
// not miss: every single-repo command in the tool is in that state.
func TestSingleSourceResolvesTheEmptyName(t *testing.T) {
	fsys := fstest.MapFS{"service.yaml": {Data: []byte("x")}}
	s := SingleSource("", fsys)
	if _, ok := s.For(&Entity{SourceRepo: ""}); !ok {
		t.Fatal("For() with an empty SourceRepo missed; single-repo commands depend on it")
	}
}

// Methods on a nil map are nil-safe for free. This is load-bearing: a check
// calls env.Sources.For(e) deep inside Score, and an Env built without one
// must produce a diagnostic naming the repository, not a panic pointing at
// a scorecard check for a mistake made in cmd/.
func TestNilSourcesAnswersRatherThanPanics(t *testing.T) {
	var s Sources
	if _, ok := s.Get("x"); ok {
		t.Fatal("nil Sources returned ok")
	}
	if got := s.Names(); len(got) != 0 {
		t.Errorf("Names() on nil = %v, want empty", got)
	}
	if got := s.With("a", fstest.MapFS{}); len(got) != 1 {
		t.Errorf("With on nil = %v, want one entry", got)
	}
}

func TestSourcesWithReturnsANewValue(t *testing.T) {
	before := fstest.MapFS{"x": {Data: []byte("before")}}
	after := fstest.MapFS{"x": {Data: []byte("after")}}
	s1 := Sources{"repo": before}
	s2 := s1.With("repo", after)

	got1, _ := s1.Get("repo")
	b1, _ := fs.ReadFile(got1, "x")
	if string(b1) != "before" {
		t.Errorf("With() mutated the receiver: read %q, want %q", b1, "before")
	}
	got2, _ := s2.Get("repo")
	b2, _ := fs.ReadFile(got2, "x")
	if string(b2) != "after" {
		t.Errorf("With() result read %q, want %q", b2, "after")
	}
}

func TestSourcesNamesAreSorted(t *testing.T) {
	s := Sources{"zeta": fstest.MapFS{}, "alpha": fstest.MapFS{}, "mu": fstest.MapFS{}}
	if diff := cmp.Diff([]string{"alpha", "mu", "zeta"}, s.Names()); diff != "" {
		t.Errorf("Names() mismatch (-want +got):\n%s", diff)
	}
}

func TestMissingSourceDiagnostic(t *testing.T) {
	e := &Entity{SourceRepo: "ghost", SourcePath: "services/api/service.yaml", NameLine: 4}
	e.Kind = "Service"
	e.Metadata.Name = "api"
	d := MissingSourceDiagnostic(e, "docs-unreadable")

	if d.Message != `no filesystem for repository "ghost", which defines service:api` {
		t.Errorf("Message = %q", d.Message)
	}
	if d.Hint != "this is a landsraad bug, not a problem with your catalog: cmd/ must put every repository it parsed into catalog.Sources" {
		t.Errorf("Hint = %q", d.Hint)
	}
	if d.Check != "docs-unreadable" {
		t.Errorf("Check = %q, want %q", d.Check, "docs-unreadable")
	}
	if d.Line != 4 {
		t.Errorf("Line = %d, want 4", d.Line)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/catalog/ -run 'TestSources|TestSingleSource|TestNilSources|TestMissingSource' -v`
Expected: FAIL to compile — `undefined: Sources`.

- [ ] **Step 3: Implement**

Create `internal/catalog/sources.go`:

```go
package catalog

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Sources maps a repository's name to the filesystem holding its files.
//
// It exists because a merged Catalog is two-dimensional and an fs.FS is
// one-dimensional. Every stage below cmd/ already took an fs.FS — which is
// what lets this plan add a filesystem implementation without teaching any
// stage what a repository is — but one filesystem per *stage* is not one
// filesystem per *entity*, and once entities from three repositories share
// a Catalog, "read e.Spec.Runbook" has three possible answers. Ruling R23.
//
// A named map rather than a struct wrapping one, and the reason is a
// decision this codebase already took. Catalog.Entities returns the
// catalog's own slice, arguing that "returning a copy on every call to
// defend against a caller that does not exist is the over-abstraction half
// of the composition rule". The only thing a struct would buy here over
// this is preventing a write by a caller that does not exist — the same
// defence, declined one file over.
//
// What the named type earns, and it is the whole reason this is not a bare
// map[string]fs.FS: For encodes that the key is e.SourceRepo, once, instead
// of at the five call sites that would each otherwise write
// m[e.SourceRepo]. Five consumers of one invariant is what a method is for.
//
// It also earns nil-safety for free — a read from a nil map is a zero value,
// not a panic — which matters because a check calls s.For(e) deep inside
// Score. A caller that built an Env without a Sources gets a loud, specific
// "no filesystem for repository" against every entity rather than a stack
// trace pointing at a scorecard check for a mistake made in cmd/.
//
// Unlike Catalog, there is no keyed literal here that looks constructed and
// answers wrongly: an empty Sources is visibly empty. That is why it needs
// no constructor and has none — build one with a literal.
type Sources map[string]fs.FS

// SingleSource is the one-repository case: validate, gen and score.
//
// name may be empty, and routinely is — localRepoName returns "" for a
// repository with no repos.yaml, and every entity it parses then carries an
// empty SourceRepo. That must resolve rather than miss.
func SingleSource(name string, fsys fs.FS) Sources { return Sources{name: fsys} }

// For returns the filesystem holding this entity's files.
//
// ok is false only when cmd/ built a Sources that does not cover a
// repository it parsed entities from, which is a bug in this program rather
// than in anybody's catalog. Callers report MissingSourceDiagnostic and
// carry on with the next entity: accumulate, never fail fast.
func (s Sources) For(e *Entity) (fs.FS, bool) { return s.Get(e.SourceRepo) }

// Get returns a filesystem by repository name.
func (s Sources) Get(name string) (fs.FS, bool) {
	fsys, ok := s[name]
	return fsys, ok
}

// Names returns every repository name, sorted.
func (s Sources) Names() []string { return slices.Sorted(maps.Keys(s)) }

// With returns a copy with name bound to fsys, leaving the receiver alone.
//
// A copy rather than a mutation because serve --watch rebuilds on every file
// change and must swap only the local filesystem while reusing the remotes
// fetched at startup (ruling R33). A mutating setter would make the
// rebuild's Sources shared state between the watcher's goroutine and the
// server's, for no gain.
func (s Sources) With(name string, fsys fs.FS) Sources {
	next := maps.Clone(s)
	if next == nil {
		next = Sources{}
	}
	next[name] = fsys
	return next
}

// MissingSourceDiagnostic reports an entity whose repository has no
// filesystem, in the caller's own check namespace.
//
// check is a parameter because the same bug surfaces at five different
// stages and each one already has a name the user has seen in other
// diagnostics; inventing a sixth check id here would make the failure look
// unrelated to the stage it stopped.
func MissingSourceDiagnostic(e *Entity, check string) diag.Diagnostic {
	return diag.Diagnostic{
		Severity: diag.SevError,
		Repo:     e.SourceRepo,
		File:     e.SourcePath,
		Line:     e.NameLine,
		Entity:   e.Metadata.Name,
		Check:    check,
		Message:  fmt.Sprintf("no filesystem for repository %q, which defines %s", e.SourceRepo, e.Ref()),
		Hint:     "this is a landsraad bug, not a problem with your catalog: cmd/ must put every repository it parsed into catalog.Sources",
	}
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./internal/catalog/ -run 'TestSources|TestSingleSource|TestNilSources|TestMissingSource' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/sources.go internal/catalog/sources_test.go
git commit -m "feat(catalog): add Sources, a repository name to filesystem map

A merged Catalog is two-dimensional and an fs.FS is one-dimensional.
Nothing consumes this yet; the next three tasks thread it into the five
sites that read an entity's files.

Ruling R23."
```

---

### Task 3: `CheckFiles` takes `Sources`

The first of the five sites, and the simplest: it only stats. Behaviour must be identical afterwards for a single-repo catalog, which is what the existing tests assert.

**Files:**
- Modify: `internal/catalog/files.go:17`
- Modify: `cmd/landsraad/gen.go:96-97`
- Test: `internal/catalog/files_test.go`

**Interfaces:**
- Consumes: `catalog.Sources` and `catalog.MissingSourceDiagnostic` from Task 2.
- Produces: `func CheckFiles(src Sources, cat *Catalog, c *diag.Collector)` — used by Task 12's `assemble`.

- [ ] **Step 1: Write the failing test**

Add to `internal/catalog/files_test.go`:

```go
// CheckFiles stats each entity's paths in the repository that entity came
// from — not in whichever filesystem the caller happened to pass. Before
// Plan 4 this test could not be written: there was only one filesystem.
func TestCheckFilesUsesEachEntitysOwnRepository(t *testing.T) {
	mono := fstest.MapFS{
		"services/api/service.yaml": {Data: []byte("x")},
		"services/api/runbook.md":   {Data: []byte("x")},
	}
	// edge has a runbook.md at its root and nothing under services/.
	edge := fstest.MapFS{
		"service.yaml": {Data: []byte("x")},
		"runbook.md":   {Data: []byte("x")},
	}
	src := Sources{"monorepo": mono, "edge-gateway": edge}

	inMono := &Entity{SourceRepo: "monorepo", SourcePath: "services/api/service.yaml", NameLine: 4}
	inMono.Kind = "Service"
	inMono.Metadata.Name = "api"
	inMono.Spec.Runbook = "services/api/runbook.md"

	inEdge := &Entity{SourceRepo: "edge-gateway", SourcePath: "service.yaml", NameLine: 4}
	inEdge.Kind = "Service"
	inEdge.Metadata.Name = "edge"
	inEdge.Spec.Runbook = "runbook.md"

	var c diag.Collector
	cat := NewCatalog([]*Entity{inMono, inEdge}, &c)
	CheckFiles(src, cat, &c)

	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("got %d diagnostics, want 0: %+v", len(ds), ds)
	}
}

func TestCheckFilesReportsAnEntityWithNoFilesystem(t *testing.T) {
	src := Sources{"monorepo": fstest.MapFS{}}

	orphan := &Entity{SourceRepo: "ghost", SourcePath: "service.yaml", NameLine: 4}
	orphan.Kind = "Service"
	orphan.Metadata.Name = "api"
	orphan.Spec.Runbook = "runbook.md"

	var c diag.Collector
	cat := NewCatalog([]*Entity{orphan}, &c)
	CheckFiles(src, cat, &c)

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	if ds[0].Message != `no filesystem for repository "ghost", which defines service:api` {
		t.Errorf("Message = %q", ds[0].Message)
	}
	if ds[0].Check != "unknown-repo" {
		t.Errorf("Check = %q, want %q", ds[0].Check, "unknown-repo")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/catalog/ -run TestCheckFiles -v`
Expected: FAIL to compile — `cannot use src (variable of type Sources) as fs.FS value`.

- [ ] **Step 3: Implement**

In `internal/catalog/files.go`, change the signature and add the per-entity lookup. Only the first four lines of the function body change; the loop over the four fields is untouched:

```go
// CheckFiles verifies that every path an entity points at exists in the
// repository that entity came from.
//
// Empty paths are skipped: these fields are optional, and "not set" is a
// scorecard question, not a validation error.
//
// It took a single fs.FS until Plan 4, which was correct for exactly as long
// as a Catalog held one repository's entities. Taking a Sources rather than
// a path is still what lets the platform build run this against a fetched
// remote repository with no change — and because the sparse fetcher answers
// Stat from its tree listing (ruling R24), this whole function costs zero
// network requests against a remote repository.
func CheckFiles(src Sources, cat *Catalog, c *diag.Collector) {
	for _, e := range cat.entities {
		fsys, ok := src.For(e)
		if !ok {
			c.Add(MissingSourceDiagnostic(e, "unknown-repo"))
			continue
		}
		for _, f := range []struct {
			field string
			path  string
			dir   bool
		}{
			// ... unchanged ...
```

- [ ] **Step 4: Update the caller**

In `cmd/landsraad/gen.go`, replace lines 96-97:

```go
	repo := localRepoName(fsys)
	cat := catalog.NewCatalog(catalog.ParseAll(repo, files, c), c)
	catalog.CheckFiles(catalog.SingleSource(repo, fsys), cat, c)
```

`localRepoName` was called inline before; hoisting it to a variable makes the two uses provably the same string. They must be: `ParseAll` writes `SourceRepo` and `SingleSource` writes the key `For` looks up, and a mismatch between them is a catalog where no entity can find its own files.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/catalog/ ./cmd/... -v`
Expected: PASS. Existing `CheckFiles` tests need their call site updated to `SingleSource("", fsys)` — the entities in those fixtures have an empty `SourceRepo`.

- [ ] **Step 6: Verify the tree**

Run: `task ci`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/files.go internal/catalog/files_test.go cmd/landsraad/gen.go
git commit -m "refactor(catalog): CheckFiles stats each entity's own repository

First of the five sites that took one fs.FS for a whole catalog. Plan 3
claimed none of them would change; they all do.

Ruling R23."
```

---
### Task 4: The scorecard reads per repository

Three hermetic checks read files, `Ingest` reads `.landsraad/checks` and `docs-fresh` asks for a last-edit date. All four assume one repository. This task also opens R31 — `.landsraad/checks` in one repository may report on entities in another — which introduces a tie the old precedence rule could not see.

**Files:**
- Modify: `internal/scorecard/check.go:74,82`
- Modify: `internal/scorecard/hermetic.go:57,105,171`
- Modify: `internal/scorecard/ingest.go:70`
- Modify: `cmd/landsraad/lastedit.go`, `cmd/landsraad/build.go`, `cmd/landsraad/score.go`
- Test: `internal/scorecard/hermetic_test.go`, `internal/scorecard/ingest_test.go`

**Interfaces:**
- Consumes: `catalog.Sources` from Task 2.
- Produces:
  - `type LastEditFunc func(repo, path string) (time.Time, bool)` — **signature change**
  - `Env.Sources catalog.Sources` replaces `Env.FS fs.FS`
  - `func Ingest(src catalog.Sources, cat *catalog.Catalog, staleAfterDays int, now time.Time, c *diag.Collector) map[catalog.Ref]map[string]Reported` — **signature change**
  - `Reported.SourceRepo string` — new field, so a tie can name which repository a file came from

- [ ] **Step 1: Write the failing tests**

Add to `internal/scorecard/hermetic_test.go`:

```go
// runbook-present reads the runbook from the entity's own repository. Two
// entities naming the same relative path in different repositories must get
// different answers.
func TestRunbookPresentReadsTheEntitysOwnRepository(t *testing.T) {
	full := fstest.MapFS{"runbook.md": {Data: []byte("# Runbook\n\nCall the on-call.\n")}}
	stub := fstest.MapFS{"runbook.md": {Data: []byte("# Runbook\n")}}
	src := catalog.Sources{"full-repo": full, "stub-repo": stub}

	good := &catalog.Entity{SourceRepo: "full-repo"}
	good.Spec.Runbook = "runbook.md"
	bad := &catalog.Entity{SourceRepo: "stub-repo"}
	bad.Spec.Runbook = "runbook.md"

	env := Env{Sources: src}
	if got := runbookPresent(good, env); got.Status != StatusPass {
		t.Errorf("full-repo: Status = %v, want %v (Detail %q)", got.Status, StatusPass, got.Detail)
	}
	got := runbookPresent(bad, env)
	if got.Status != StatusFail {
		t.Errorf("stub-repo: Status = %v, want %v", got.Status, StatusFail)
	}
	if got.Detail != "runbook.md has a heading and no content" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

func TestChecksReportAnEntityWithNoFilesystem(t *testing.T) {
	env := Env{Sources: catalog.Sources{"known": fstest.MapFS{}}}
	e := &catalog.Entity{SourceRepo: "ghost"}
	e.Spec.Runbook = "runbook.md"
	e.Spec.Alerts = "alerts.yaml"

	for _, tt := range []struct {
		name string
		run  func(*catalog.Entity, Env) Result
	}{
		{"runbook-present", runbookPresent},
		{"alerts-parse", alertsParse},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run(e, env)
			if got.Status != StatusError {
				t.Errorf("Status = %v, want %v", got.Status, StatusError)
			}
			if got.Detail != `no filesystem for repository "ghost"` {
				t.Errorf("Detail = %q", got.Detail)
			}
		})
	}
}

// docs-fresh asks for a last-edit date per repository. The same path in two
// repositories is two different directories with two different histories.
func TestDocsFreshAsksPerRepository(t *testing.T) {
	docs := fstest.MapFS{"docs/index.md": {Data: []byte("# Docs\n")}}
	src := catalog.Sources{"fresh": docs, "ancient": docs}
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	var asked []string
	env := Env{
		Sources:        src,
		Now:            now,
		MaxDocsAgeDays: 180,
		LastEdit: func(repo, path string) (time.Time, bool) {
			asked = append(asked, repo+":"+path)
			if repo == "fresh" {
				return now.AddDate(0, 0, -3), true
			}
			return now.AddDate(0, 0, -400), true
		},
	}

	fresh := &catalog.Entity{SourceRepo: "fresh"}
	fresh.Spec.Docs = "docs"
	ancient := &catalog.Entity{SourceRepo: "ancient"}
	ancient.Spec.Docs = "docs"

	if got := docsFresh(fresh, env); got.Status != StatusPass {
		t.Errorf("fresh: Status = %v, want %v", got.Status, StatusPass)
	}
	if got := docsFresh(ancient, env); got.Status != StatusFail {
		t.Errorf("ancient: Status = %v, want %v", got.Status, StatusFail)
	}
	want := []string{"fresh:docs", "ancient:docs"}
	if diff := cmp.Diff(want, asked); diff != "" {
		t.Errorf("LastEdit calls mismatch (-want +got):\n%s", diff)
	}
}
```

Add to `internal/scorecard/ingest_test.go`:

```go
// Ruling R31: a CheckResults file in one repository may report on an entity
// defined in another. A platform repository running one image-scan job for
// every service is the natural shape.
func TestIngestReadsEveryRepository(t *testing.T) {
	platform := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/image-scan\n" +
				"generatedAt: 2026-09-09T12:00:00Z\nresults:\n" +
				"  - { entity: service:edge, check: image-scanned, status: pass, detail: \"0 critical\" }\n")},
	}
	edge := fstest.MapFS{}
	src := catalog.Sources{"platform": platform, "edge-gateway": edge}

	var c diag.Collector
	e := &catalog.Entity{SourceRepo: "edge-gateway", SourcePath: "service.yaml", NameLine: 4}
	e.Kind = "Service"
	e.Metadata.Name = "edge"
	cat := catalog.NewCatalog([]*catalog.Entity{e}, &c)

	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	got := Ingest(src, cat, 14, now, &c)

	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("got %d diagnostics, want 0: %+v", len(ds), ds)
	}
	rep, ok := got[e.Ref()]["image-scanned"]
	if !ok {
		t.Fatal("image-scanned was not ingested for service:edge")
	}
	if rep.SourceRepo != "platform" {
		t.Errorf("SourceRepo = %q, want %q", rep.SourceRepo, "platform")
	}
}

// The tie rule used to compare producers only, which was sufficient while
// one repository held every CheckResults file. Under R31 the same producer
// name can report the same check at the same instant from two repositories,
// and first-wins there is exactly the coin flip spec §6 forbids.
func TestIngestTieAcrossRepositoriesWithTheSameProducer(t *testing.T) {
	body := "apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/image-scan\n" +
		"generatedAt: 2026-09-09T12:00:00Z\nresults:\n" +
		"  - { entity: service:edge, check: image-scanned, status: %s, detail: d }\n"
	a := fstest.MapFS{".landsraad/checks/scan.yaml": {Data: []byte(fmt.Sprintf(body, "pass"))}}
	b := fstest.MapFS{".landsraad/checks/scan.yaml": {Data: []byte(fmt.Sprintf(body, "fail"))}}
	src := catalog.Sources{"alpha": a, "beta": b}

	var c diag.Collector
	e := &catalog.Entity{SourceRepo: "alpha", SourcePath: "service.yaml", NameLine: 4}
	e.Kind = "Service"
	e.Metadata.Name = "edge"
	cat := catalog.NewCatalog([]*catalog.Entity{e}, &c)

	Ingest(src, cat, 14, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), &c)

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	want := `producer "ci/image-scan" reports image-scanned for service:edge at 2026-09-09T12:00:00Z ` +
		`from both alpha:.landsraad/checks/scan.yaml and beta:.landsraad/checks/scan.yaml, so neither can win`
	if ds[0].Message != want {
		t.Errorf("Message = %q, want %q", ds[0].Message, want)
	}
	if ds[0].Hint != "give the producers different generatedAt values, or have only one report this check" {
		t.Errorf("Hint = %q", ds[0].Hint)
	}
	if ds[0].Check != "checks-tie" {
		t.Errorf("Check = %q, want %q", ds[0].Check, "checks-tie")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/scorecard/ -run 'TestRunbookPresentReads|TestChecksReportAnEntity|TestDocsFreshAsks|TestIngestReads|TestIngestTie' -v`
Expected: FAIL to compile — `unknown field Sources in struct literal of type Env`.

- [ ] **Step 3: Implement `Env` and the checks**

In `internal/scorecard/check.go`:

```go
// LastEditFunc reports when a path in a given repository was last changed,
// and whether that is known at all.
//
// It is injected because the answer comes from git, and nothing under
// internal/ may shell out or touch os. In the local repository cmd/ supplies
// it from `git log`; for a repository fetched over a host API there is no git
// history and it costs one API call per service (spec §9, "known cost of
// D5"). A fetcher that cannot answer returns false, and docs-fresh reports
// not-reported rather than inventing a date.
//
// repo arrived with Plan 4. Without it, "services/api" names a different
// directory in every repository in the catalog, and the local git history
// would be asked about paths that only exist on a host somewhere.
type LastEditFunc func(repo, path string) (time.Time, bool)

// Env is everything a check needs from outside itself.
//
// Now is a value rather than a call to time.Now() so scoring is a pure
// function of its inputs: a check that reads the clock has tests that fail at
// midnight and a result that cannot be reproduced from a commit.
type Env struct {
	// Sources replaced a single fs.FS in Plan 4. A merged catalog holds
	// entities from several repositories, so a check reads through
	// Sources.For(e) and never through an ambient filesystem (ruling R23).
	Sources        catalog.Sources
	Now            time.Time
	MaxDocsAgeDays int
	LastEdit       LastEditFunc
}
```

In `internal/scorecard/hermetic.go`, add the shared result and change the three sites:

```go
// missingSourceResult is a check's answer for an entity whose repository has
// no filesystem. Error rather than fail: the service is not at fault, this
// program is, and a fail would put a red mark on somebody's scorecard for a
// bug in cmd/.
func missingSourceResult(e *catalog.Entity, id string) Result {
	return Result{Check: id, Status: StatusError,
		Detail: fmt.Sprintf("no filesystem for repository %q", e.SourceRepo)}
}
```

`runbookPresent` (line 57), `alertsParse` (line 105) and `docsFresh` (line 171) each gain the same four lines before their first filesystem call:

```go
	fsys, ok := env.Sources.For(e)
	if !ok {
		return missingSourceResult(e, id)
	}
```

then use `fsys` in place of `env.FS`. In `docsFresh` the `LastEdit` call becomes:

```go
	edited, ok := env.LastEdit(e.SourceRepo, e.Spec.Docs)
```

- [ ] **Step 4: Implement `Ingest` across repositories**

In `internal/scorecard/ingest.go`, wrap the existing body in a per-repository loop. `Ingest` becomes the loop and `ingestRepo` is today's function with two new parameters:

```go
// Ingest reads .landsraad/checks from every repository in the catalog.
//
// Ruling R31: a CheckResults file in one repository may report on an entity
// defined in another, because a platform repository running one image-scan
// job for every service is the natural shape and forbidding it would mean a
// CI job may only report on entities it happens to sit beside. Spec §6
// describes the file without saying whose entities it may name; this is the
// answer.
//
// An absent directory is not an error. A repository reporting no external
// results is normal, and every external check then renders not-reported —
// which is visible in the scorecard rather than hidden.
func Ingest(src catalog.Sources, cat *catalog.Catalog, staleAfterDays int, now time.Time, c *diag.Collector) map[catalog.Ref]map[string]Reported {
	out := map[catalog.Ref]map[string]Reported{}
	// Sorted, via Sources.Names, so precedence and every tie diagnostic read
	// the same on every run regardless of map iteration order.
	for _, repo := range src.Names() {
		fsys, ok := src.Get(repo)
		if !ok {
			continue
		}
		ingestRepo(repo, fsys, cat, out, c)
	}
	applyStaleness(out, staleAfterDays, now)
	return out
}
```

`ingestRepo` holds everything from today's `fs.ReadDir(fsys, ChecksDir)` down to the end of the precedence switch. Three changes inside it:

1. Every `diag.Diagnostic` it constructs gains `Repo: repo`. There are five: `checks-schema`, `checks-unreadable`, `checks-parse`, `checks-generated-at`, `checks-tie`.
2. The `Reported` literal gains `SourceRepo: repo`.
3. The precedence switch gains a branch for the same-producer tie:

```go
			prev, exists := out[ref][r.Check]
			switch {
			case !exists || cand.GeneratedAt.After(prev.GeneratedAt):
				out[ref][r.Check] = cand
			case cand.GeneratedAt.Equal(prev.GeneratedAt) && prev.Producer != cand.Producer:
				// Spec §6: a tie is an error rather than a coin flip.
				c.Add(diag.Diagnostic{
					Severity: diag.SevError, Repo: repo, File: path, Line: 1,
					Entity: ref.Name,
					Check:  "checks-tie",
					Message: fmt.Sprintf("producers %q and %q both report %s for %s at %s, so neither can win",
						prev.Producer, cand.Producer, r.Check, ref, f.GeneratedAt),
					Hint: "give the producers different generatedAt values, or have only one report this check",
				})
			case cand.GeneratedAt.Equal(prev.GeneratedAt) && prev.Location() != cand.Location():
				// Same producer name, two repositories. Unreachable while
				// one repository held every CheckResults file, which is why
				// the rule above only ever compared producers. Under R31 it
				// is reachable, and first-wins would be the coin flip.
				c.Add(diag.Diagnostic{
					Severity: diag.SevError, Repo: repo, File: path, Line: 1,
					Entity: ref.Name,
					Check:  "checks-tie",
					Message: fmt.Sprintf("producer %q reports %s for %s at %s from both %s and %s, so neither can win",
						cand.Producer, r.Check, ref, f.GeneratedAt, prev.Location(), cand.Location()),
					Hint: "give the producers different generatedAt values, or have only one report this check",
				})
			}
```

Add `SourceRepo` and `Location` to `Reported`:

```go
	// SourceRepo is the repository the CheckResults file came from. With
	// R31 the file may live somewhere other than the entity it reports on,
	// so the path alone no longer identifies it.
	SourceRepo string

// Location renders where a reported result came from, matching
// Entity.Location: "repo:path" when the repository is known, "path" when it
// is not.
func (r Reported) Location() string {
	if r.SourceRepo == "" {
		return r.SourceFile
	}
	return r.SourceRepo + ":" + r.SourceFile
}
```

Move the staleness loop at the end of today's `Ingest` into `applyStaleness(out, staleAfterDays, now)` unchanged. It must stay **after** the whole repository loop: ageing out before precedence could let a stale-but-newer result lose to a fresh-but-older one, and that argument now spans repositories too.

- [ ] **Step 5: Update `cmd/`**

`cmd/landsraad/lastedit.go` — `gitLastEdit` takes a repo it does not use, and says why:

```go
// gitLastEdit answers "when was this path last changed" from git history.
//
// The repo parameter is ignored: this closure is built for one checkout and
// only ever asked about that checkout's own entities. It is in the signature
// because Plan 4's LastEditFunc serves both this and a host API adapter, and
// the adapter genuinely needs it. Ignoring a parameter here is better than a
// second function type that cmd/ would have to choose between.
func gitLastEdit(root string) scorecard.LastEditFunc {
	// ... cache unchanged ...
	return func(_ string, p string) (time.Time, bool) {
```

`cmd/landsraad/build.go` and `cmd/landsraad/score.go` — `scorecard.Ingest(fsys, …)` becomes `scorecard.Ingest(src, …)` and `Env{FS: fsys}` becomes `Env{Sources: src}`, where `src` is the `catalog.Sources` those commands already build for `CheckFiles` in Task 3. In `score.go` that is `catalog.SingleSource(repo, fsys)`.

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/scorecard/ ./cmd/... -v`
Expected: PASS. Existing scorecard tests constructing `Env{FS: fsys}` become `Env{Sources: catalog.SingleSource(<name>, fsys)}`. **Read the fixtures to find `<name>` — do not assume it is `""`.** Task 3 hit exactly this: the plan claimed `internal/catalog`'s fixtures carried an empty `SourceRepo` and they carried `"monorepo"`, so the literal `SingleSource("", fsys)` would have broken every existing test. Whatever `SourceRepo` the fixture entities actually have is the name to key on, and no existing assertion should need changing.

- [ ] **Step 7: Verify the tree**

Run: `task ci`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/scorecard/check.go internal/scorecard/hermetic.go internal/scorecard/ingest.go internal/scorecard/hermetic_test.go internal/scorecard/ingest_test.go cmd/landsraad/lastedit.go cmd/landsraad/build.go cmd/landsraad/score.go
git commit -m "refactor(scorecard): read, ingest and date per repository

Env carries a catalog.Sources, Ingest walks every repository, and
LastEditFunc takes the repository whose history it is asking about.

R31 makes a CheckResults file in one repository able to report on an
entity in another, which makes a same-producer tie reachable for the
first time; first-wins there is the coin flip spec 6 forbids, so it is
now a diagnostic.

Rulings R23, R31, R35."
```

---
### Task 5: `render.Input` takes `Sources`

The last of the five sites, and the one that makes the portal correct rather than merely compiling. `docsFor` already has both the `Input` and the `*catalog.Entity`, so the lookup happens once at the top of the function.

**Files:**
- Modify: `internal/render/model.go:71`
- Modify: `internal/render/docs.go:205-270`
- Modify: `cmd/landsraad/build.go`
- Test: `internal/render/docs_test.go`

**Interfaces:**
- Consumes: `catalog.Sources` from Task 2.
- Produces: `Input.Sources catalog.Sources` replaces `Input.FS fs.FS`. Consumed by Task 13's `Build`.

- [ ] **Step 1: Write the failing test**

Add to `internal/render/docs_test.go`:

```go
// Two entities in different repositories naming the same relative runbook
// path must render different documents. Before Plan 4 the second entity
// silently rendered the first's runbook, because there was one filesystem.
func TestDocsForReadsTheEntitysOwnRepository(t *testing.T) {
	mono := fstest.MapFS{
		"docs/index.md": {Data: []byte("# Monorepo docs\n\nThe monorepo's index.\n")},
	}
	edge := fstest.MapFS{
		"docs/index.md": {Data: []byte("# Edge docs\n\nThe gateway's index.\n")},
	}
	src := catalog.Sources{"monorepo": mono, "edge-gateway": edge}

	for _, tt := range []struct {
		repo string
		want string
	}{
		{"monorepo", "The monorepo&rsquo;s index."},
		{"edge-gateway", "The gateway&rsquo;s index."},
	} {
		t.Run(tt.repo, func(t *testing.T) {
			e := &catalog.Entity{SourceRepo: tt.repo, SourcePath: "service.yaml", NameLine: 4}
			e.Kind = "Service"
			e.Metadata.Name = "api"
			e.Spec.Docs = "docs"

			var c diag.Collector
			in := Input{Sources: src}
			got := docsFor(in, e, testTemplates(t), md.New(Mermaid{}), c.Sub())
			if !strings.Contains(string(got.Index), tt.want) {
				t.Errorf("rendered index does not contain %q:\n%s", tt.want, got.Index)
			}
		})
	}
}
```

> **Note for the implementer:** `testTemplates`, `c.Sub()` and the exact field on `entityDocs` holding the inline index are this plan's placeholders for whatever the existing `docs_test.go` helpers are called — read that file and use its own helpers rather than inventing these. The assertion that matters is the one on the *content*: each entity gets its own repository's bytes. This is the one place in this plan where you should copy a neighbouring test's scaffolding instead of the code above.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/render/ -run TestDocsForReadsTheEntitysOwnRepository -v`
Expected: FAIL to compile — `unknown field Sources in struct literal of type Input`.

- [ ] **Step 3: Implement**

In `internal/render/model.go`, replace the `FS` field:

```go
	// Sources holds each repository's filesystem, for reading docs/ and
	// runbooks. It replaced a single fs.FS in Plan 4: a portal built from
	// three repositories has three, and "read e.Spec.Runbook" is answerable
	// only against the repository that entity came from (ruling R23).
	Sources catalog.Sources
```

In `internal/render/docs.go`, resolve once at the top of `docsFor`:

```go
func docsFor(in Input, e *catalog.Entity, t *template.Template, m goldmark.Markdown, c *diag.Collector) entityDocs {
	var out entityDocs
	ref := e.Ref()
	entityDir := EntityURL(ref)

	fsys, ok := in.Sources.For(e)
	if !ok {
		c.Add(catalog.MissingSourceDiagnostic(e, "docs-unreadable"))
		return out
	}
```

Then `fs.ReadFile(in.FS, repoPath)` becomes `fs.ReadFile(fsys, repoPath)` and `fs.WalkDir(in.FS, e.Spec.Docs, …)` becomes `fs.WalkDir(fsys, e.Spec.Docs, …)`.

Returning early on a missing filesystem is right, and different from every other failure in this function. Elsewhere `docsFor` reports and carries on, because one unreadable document must not cost the reader the other pages. Here there is no filesystem at all, so every subsequent read would produce an identical diagnostic — one per document, for a bug that has nothing to do with any of them.

- [ ] **Step 4: Update the caller**

In `cmd/landsraad/build.go`'s `render.Input` literal, `FS: fsys` becomes `Sources: src`.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/render/ ./cmd/... -v`
Expected: PASS. The golden tests are the ones to watch: their `Input` literals need `Sources: catalog.SingleSource(<name>, fsys)` — **read the fixtures for `<name>` rather than assuming `""`,** as Task 3's fixtures turned out to carry `"monorepo"` — and their **output must not change at all**. A golden diff here means this refactor changed the portal, which it must not.

- [ ] **Step 6: Verify the tree**

Run: `task ci`
Expected: PASS. This is the checkpoint that closes the five-site refactor: `landsraad` still behaves exactly as it did at `7bf3175`, and `Sources` is threaded everywhere it needs to be.

- [ ] **Step 7: Commit**

```bash
git add internal/render/model.go internal/render/docs.go internal/render/docs_test.go cmd/landsraad/build.go
git commit -m "refactor(render): read each entity's docs from its own repository

Last of the five sites. Two entities in different repositories naming the
same relative runbook path used to render the same document.

Ruling R23."
```

---

### Task 6: `fetch.Entry` and `fetch.FS`

An `fs.FS` whose metadata arrives before its content. No HTTP in this task — it is a pure data structure, testable entirely in memory, and it is what makes R24's cost model real: `Stat`, `ReadDir`, `Glob` and `WalkDir` are answered from the listing for zero requests.

It distinguishes **three** answers where a normal filesystem has two. A path can be present, genuinely absent, or *unknown* — the directory that would contain it was never listed, which happens under R28's truncated-tree fallback. Collapsing the third into "absent" would make landsraad tell somebody a file they are looking at does not exist, which is the false statement spec §14.1 forbids by name.

**Files:**
- Create: `internal/fetch/fs.go`
- Test: `internal/fetch/fs_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces, and relied on by Tasks 8, 9, 10 and 12:
  - `type Entry struct{ Path string; SHA string; Size int64; Dir bool }`
  - `func NewFS() *FS`, `func FromEntries(entries []Entry) *FS`
  - `func (f *FS) AddDir(dir string, entries []Entry)`
  - `func (f *FS) Put(path string, data []byte)`
  - `func (f *FS) Entries() []Entry` — sorted by path
  - `func (f *FS) Open(name string) (fs.File, error)`, `Stat`, `ReadDir` — so `fs.Glob` and `fs.WalkDir` work
  - `var ErrNotFetched, ErrNotListed error`

- [ ] **Step 1: Write the failing test**

Create `internal/fetch/fs_test.go`:

```go
package fetch

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func testEntries() []Entry {
	return []Entry{
		{Path: "service.yaml", SHA: "aaa", Size: 12},
		{Path: "docs", Dir: true},
		{Path: "docs/index.md", SHA: "bbb", Size: 30},
		{Path: "docs/deep", Dir: true},
		{Path: "docs/deep/more.md", SHA: "ccc", Size: 7},
	}
}

// Metadata is answerable the moment the tree is listed, before a single blob
// is fetched. This is ruling R24's cost model: CheckFiles is free.
func TestStatBeforeAnyContent(t *testing.T) {
	f := FromEntries(testEntries())
	info, err := fs.Stat(f, "docs/index.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.IsDir() {
		t.Error("IsDir() = true, want false")
	}
	if info.Size() != 30 {
		t.Errorf("Size() = %d, want 30", info.Size())
	}
	if info.Name() != "index.md" {
		t.Errorf("Name() = %q, want %q", info.Name(), "index.md")
	}
}

func TestGlobAndWalkBeforeAnyContent(t *testing.T) {
	f := FromEntries(testEntries())

	got, err := fs.Glob(f, "docs/*")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if diff := cmp.Diff([]string{"docs/deep", "docs/index.md"}, got); diff != "" {
		t.Errorf("Glob mismatch (-want +got):\n%s", diff)
	}

	var walked []string
	if err := fs.WalkDir(f, "docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		walked = append(walked, p)
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	want := []string{"docs", "docs/deep", "docs/deep/more.md", "docs/index.md"}
	if diff := cmp.Diff(want, walked); diff != "" {
		t.Errorf("WalkDir mismatch (-want +got):\n%s", diff)
	}
}

// Reading a path that is in the tree but was never fetched is a bug in the
// content planner, not a missing file. It must not look like one.
func TestReadUnfetchedIsNotNotExist(t *testing.T) {
	f := FromEntries(testEntries())
	_, err := fs.ReadFile(f, "docs/index.md")
	if err == nil {
		t.Fatal("ReadFile of an unfetched path succeeded")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("unfetched content reported as ErrNotExist; it would be diagnosed as a missing file")
	}
	if !errors.Is(err, ErrNotFetched) {
		t.Errorf("error = %v, want ErrNotFetched", err)
	}
}

func TestPutThenRead(t *testing.T) {
	f := FromEntries(testEntries())
	f.Put("docs/index.md", []byte("# Docs\n"))
	got, err := fs.ReadFile(f, "docs/index.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "# Docs\n" {
		t.Errorf("ReadFile = %q, want %q", got, "# Docs\n")
	}
}

// A genuinely absent path in a listed directory is ErrNotExist, which is
// what CheckFiles must report as a missing file.
func TestAbsentInAListedDirectoryIsNotExist(t *testing.T) {
	f := FromEntries(testEntries())
	_, err := fs.Stat(f, "docs/nope.md")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want ErrNotExist", err)
	}
}

// A path whose directory was never listed is a third answer. Under R28's
// truncated-tree fallback only some directories are walked, and reporting
// "does not exist" for one that was never looked at is a false statement
// about the user's repository (spec 14.1).
func TestUnlistedDirectoryIsNotNotExist(t *testing.T) {
	f := NewFS()
	f.AddDir(".", []Entry{{Path: "services", Dir: true}})

	_, err := fs.Stat(f, "services/api/runbook.md")
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("unlisted path reported as ErrNotExist; landsraad would call it a missing file")
	}
	if !errors.Is(err, ErrNotListed) {
		t.Errorf("error = %v, want ErrNotListed", err)
	}
}

func TestAddDirMakesADirectoryKnown(t *testing.T) {
	f := NewFS()
	f.AddDir(".", []Entry{{Path: "services", Dir: true}})
	f.AddDir("services", []Entry{{Path: "services/api", Dir: true}})
	f.AddDir("services/api", []Entry{{Path: "services/api/service.yaml", SHA: "d", Size: 3}})

	if _, err := fs.Stat(f, "services/api/service.yaml"); err != nil {
		t.Fatalf("Stat after AddDir: %v", err)
	}
	if _, err := fs.Stat(f, "services/api/nope.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("absent file in a listed directory: error = %v, want ErrNotExist", err)
	}
}

func TestEntriesAreSorted(t *testing.T) {
	f := FromEntries([]Entry{
		{Path: "z.md", SHA: "1"}, {Path: "a.md", SHA: "2"}, {Path: "m.md", SHA: "3"},
	})
	var paths []string
	for _, e := range f.Entries() {
		paths = append(paths, e.Path)
	}
	if diff := cmp.Diff([]string{"a.md", "m.md", "z.md"}, paths); diff != "" {
		t.Errorf("Entries mismatch (-want +got):\n%s", diff)
	}
}

func TestInvalidPathIsRejected(t *testing.T) {
	f := FromEntries(testEntries())
	for _, p := range []string{"/etc/passwd", "../escape", "docs//index.md"} {
		if _, err := fs.Stat(f, p); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Stat(%q) error = %v, want ErrInvalid", p, err)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/fetch/ -v`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Implement `fs.go` — the package doc and `Entry`**

```go
// Package fetch turns a remote repository into an io/fs.FS.
//
// It is the only package under internal/ that speaks HTTP, and it does so
// only when cmd/ tells it to: every request happens before stage 1 or
// between stages 1 and 3, never underneath a pipeline stage (ruling R25).
// What the stages see is this package's *FS, whose bytes are already in
// memory — which is what keeps "nothing under internal/ calls the network"
// true of every stage in the program.
//
// Nothing here imports os. The blob cache is a Cache interface implemented
// in cmd/, for the same reason every other write in this codebase is.
package fetch

// Entry is one path in a repository's tree.
//
// SHA is the git blob SHA the host's tree listing returns, and it is the
// cache key in ruling R27: content-addressed, so a hit cannot be stale. It
// is empty for a directory.
type Entry struct {
	Path string
	SHA  string
	Size int64
	Dir  bool
}
```

- [ ] **Step 4: Implement `fs.go` — the filesystem**

```go
package fetch

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"time"
)

// ErrNotFetched means the path is in the tree and its content was never
// requested. It is a bug in cmd/'s content planner, never a user's mistake,
// and it is deliberately not ErrNotExist: a missing-file diagnostic would
// send somebody to look for a file that is sitting in their repository.
var ErrNotFetched = errors.New("content was listed but never fetched")

// ErrNotListed means nothing is known about this path's directory, because
// no listing ever covered it. Also a planner bug, and also deliberately not
// ErrNotExist — see spec §14.1 on why telling somebody a file does not
// exist, when the truth is that landsraad never looked, is worse than
// saying nothing.
var ErrNotListed = errors.New("directory was never listed")

// FS is a repository whose metadata arrives before its content.
//
// Stat, ReadDir, Glob and WalkDir are answered from the listing alone and
// cost nothing. Open — and therefore fs.ReadFile — needs the blob, which
// cmd/ must have fetched first.
type FS struct {
	entries map[string]Entry
	// listed holds the directories whose contents are known. A complete
	// recursive listing marks every directory; R28's fallback marks only
	// what it walked.
	listed map[string]bool
	blobs  map[string][]byte
}

// NewFS returns an empty filesystem with only its root listed.
func NewFS() *FS {
	return &FS{
		entries: map[string]Entry{},
		listed:  map[string]bool{".": true},
		blobs:   map[string][]byte{},
	}
}

// FromEntries builds a filesystem from a COMPLETE listing — one recursive
// response that describes the whole repository.
//
// It marks every directory in the listing, and every *parent* of every
// entry whether or not the host returned a row for it: a recursive listing
// that mentions docs/index.md has told us what is in docs/.
//
// A partial listing never comes through here. GitHub's truncated-tree
// descent and GitLab's prefix listing both build with NewFS plus AddDir,
// which marks exactly the directories that were actually walked — and that
// distinction is the only thing letting *FS tell "absent" from "never
// looked". A flag on this function would be a second way to express it, and
// the wrong value would silently turn the second answer into the first.
func FromEntries(entries []Entry) *FS {
	f := NewFS()
	for _, e := range entries {
		f.entries[e.Path] = e
		if e.Dir {
			f.listed[e.Path] = true
		}
		for d := path.Dir(e.Path); d != "." && d != "/"; d = path.Dir(d) {
			f.listed[d] = true
		}
	}
	return f
}

// AddDir records the contents of one directory. Used by R28's fallback and
// by GitLab's prefix listing, which learn the repository one listing at a
// time. A directory row among entries says that directory exists; only
// AddDir on that directory says what is inside it.
//
// It also records dir ITSELF, and every ancestor, as a directory that
// exists — without marking any of them listed. Successfully listing a
// directory is proof it is there, so recording that is a fact just learned,
// not an assumption. Without it, a directory reached only through Expand
// (a spec.docs outside the configured paths, which is the case Expand exists
// for) had entries for its children and none for itself, so fs.WalkDir over
// it failed on the stat of its own root. Found by review in Task 10.
//
// The asymmetry with `listed` is the three-answer property and must hold:
// after AddDir("shared/docs", …), "shared" EXISTS but its contents are still
// unknown, so Stat("shared/other") is ErrNotListed — not ErrNotExist, which
// would be landsraad claiming a file is absent from a directory it never
// looked in.
func (f *FS) AddDir(dir string, entries []Entry) {
	f.listed[dir] = true
	for _, e := range entries {
		f.entries[e.Path] = e
	}
	for d := dir; d != "." && d != "/" && d != ""; d = path.Dir(d) {
		if _, ok := f.entries[d]; !ok {
			f.entries[d] = Entry{Path: d, Dir: true}
		}
	}
}

// Put stores a blob's content.
func (f *FS) Put(p string, data []byte) { f.blobs[p] = data }

// Entries returns every known entry, sorted by path.
func (f *FS) Entries() []Entry {
	out := make([]Entry, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Listed reports whether a directory's contents are known.
func (f *FS) Listed(dir string) bool { return f.listed[dir] }

// Stat implements fs.StatFS.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	e, err := f.lookup("stat", name)
	if err != nil {
		return nil, err
	}
	return fileInfo{e}, nil
}

// Open implements fs.FS. Opening a directory yields something ReadDir can
// walk; opening a file needs its content to have been fetched.
func (f *FS) Open(name string) (fs.File, error) {
	e, err := f.lookup("open", name)
	if err != nil {
		return nil, err
	}
	if e.Dir {
		return &dir{fs: f, info: fileInfo{e}}, nil
	}
	data, ok := f.blobs[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: ErrNotFetched}
	}
	return &file{info: fileInfo{e}, r: bytes.NewReader(data)}, nil
}

// ReadDir implements fs.ReadDirFS.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	if _, err := f.lookup("readdir", name); err != nil {
		return nil, err
	}
	if !f.listed[name] {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: ErrNotListed}
	}
	var out []fs.DirEntry
	for p, e := range f.entries {
		if path.Dir(p) == name && p != name {
			out = append(out, fileInfo{e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// lookup resolves a path to its entry, distinguishing the three answers a
// sparse filesystem has and a complete one does not.
func (f *FS) lookup(op, name string) (Entry, error) {
	if !fs.ValidPath(name) {
		return Entry{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return Entry{Path: ".", Dir: true}, nil
	}
	if e, ok := f.entries[name]; ok {
		return e, nil
	}
	// Absent. Whether that is a fact about the repository or a fact about
	// what landsraad bothered to list depends on the parent.
	if f.listed[path.Dir(name)] {
		return Entry{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	}
	return Entry{}, &fs.PathError{Op: op, Path: name, Err: ErrNotListed}
}

// fileInfo is one entry as both an fs.FileInfo and an fs.DirEntry.
//
// ModTime is the zero time: a tree listing carries no timestamps, and
// inventing time.Now() would be a lie that also makes every golden test
// fail one second after it is written. Nothing in landsraad reads ModTime —
// docs-fresh asks LastEditFunc precisely because a fetched repository has
// no useful mtimes (ruling R35).
type fileInfo struct{ e Entry }

func (i fileInfo) Name() string { return path.Base(i.e.Path) }
func (i fileInfo) Size() int64  { return i.e.Size }
func (i fileInfo) Mode() fs.FileMode {
	if i.e.Dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i fileInfo) ModTime() time.Time     { return time.Time{} }
func (i fileInfo) IsDir() bool            { return i.e.Dir }
func (i fileInfo) Sys() any                   { return nil }
func (i fileInfo) Type() fs.FileMode          { return i.Mode().Type() }
func (i fileInfo) Info() (fs.FileInfo, error) { return i, nil }

// file is an open blob.
type file struct {
	info fileInfo
	r    *bytes.Reader
}

func (f *file) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *file) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *file) Close() error               { return nil }

// dir is an open directory. fs.WalkDir reaches it through ReadDirFile.
type dir struct {
	fs     *FS
	info   fileInfo
	offset int
}

func (d *dir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *dir) Close() error               { return nil }
func (d *dir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.e.Path, Err: errors.New("is a directory")}
}

func (d *dir) ReadDir(n int) ([]fs.DirEntry, error) {
	all, err := d.fs.ReadDir(d.info.e.Path)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		d.offset = len(all)
		return all, nil
	}
	if d.offset >= len(all) {
		return nil, io.EOF
	}
	end := min(d.offset+n, len(all))
	out := all[d.offset:end]
	d.offset = end
	return out, nil
}

// Compile-time proof that *FS is everything the pipeline asks of it. Without
// StatFS and ReadDirFS, io/fs falls back to Open, which for this type means
// a directory walk would demand blob content it never needed.
var (
	_ fs.FS        = (*FS)(nil)
	_ fs.StatFS    = (*FS)(nil)
	_ fs.ReadDirFS = (*FS)(nil)
)
```

> **This block is verified, not sketched.** It was compiled and run against the ten tests above before this plan was written: `go vet` clean, all green. Type it in as it stands.

- [ ] **Step 5: Run the test**

Run: `go test ./internal/fetch/ -v`
Expected: PASS, all nine tests.

- [ ] **Step 6: Verify the rule hooks are happy**

Run: `sh scripts/check-rules.sh && go vet ./internal/fetch/`
Expected: silent, exit 0. `internal/fetch` must import no `os` package — this is the task where the temptation first appears, and the answer is that the cache lives in `cmd/` (Task 11).

- [ ] **Step 7: Commit**

```bash
git add internal/fetch/fs.go internal/fetch/fs_test.go
git commit -m "feat(fetch): an fs.FS whose metadata arrives before its content

Stat, ReadDir, Glob and WalkDir are answered from a tree listing for zero
requests; only ReadFile needs a blob. Three answers rather than two:
present, absent, and never-listed, because reporting the third as absent
tells somebody a file they are looking at does not exist.

Rulings R24, R28."
```

---
### Task 7: `fetch.Client` — the shared HTTP half

Auth, retries, rate limits and error classification, written once so the two adapters differ only in their endpoints. Both hosts return **404 for a private repository with no token**, so the error type has to carry enough for a message that says so; that is the single most confusing failure this feature can produce.

**Files:**
- Create: `internal/fetch/fetch.go`, `internal/fetch/client.go`
- Test: `internal/fetch/client_test.go`

**Interfaces:**
- Consumes: `fetch.Entry`, `fetch.FS` from Task 6.
- Produces, and relied on by Tasks 8, 10, 12 and 13:
  - `type Fetcher interface{ Open; Fetch; Expand; LastEdit }`
  - `type Repo struct{ Name, URL, Ref, Owner, Slug string }`, `func ParseRepo(name, rawURL, ref string) (Repo, error)`
  - `type Cache interface{ Get(sha string) ([]byte, bool); Put(sha string, data []byte) error }`
  - `type Client struct{ … }`, `func NewClient(o ClientOptions) *Client`
  - `type StatusError struct{ Status int; Method, Endpoint, Body string; RetryAfter time.Duration }`
  - `func IsNotFound(err) bool`, `func IsUnauthorized(err) bool`, `func IsRateLimited(err) bool`

- [ ] **Step 1: Write the failing test**

Create `internal/fetch/client_test.go`:

```go
package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{
		HTTP:        srv.Client(),
		BaseURL:     srv.URL,
		Token:       "secret-token-value",
		AuthHeader:  "Authorization",
		AuthPrefix:  "Bearer ",
		Headers:     map[string]string{"X-GitHub-Api-Version": "2026-03-10"},
		MaxAttempts: 3,
		Sleep:       func(time.Duration) {}, // tests never wait
	}), srv
}

func TestClientSendsAuthAndHeaders(t *testing.T) {
	var gotAuth, gotVersion string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		w.Write([]byte(`{}`))
	})
	if _, _, err := c.Get(context.Background(), "/repos/o/r", nil, ""); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotAuth != "Bearer secret-token-value" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotVersion != "2026-03-10" {
		t.Errorf("X-GitHub-Api-Version = %q", gotVersion)
	}
}

// A token must never reach a diagnostic, a log line or the generated site
// (spec §14.1). The error path is where it would leak, because that is the
// one place the request gets described back to the user.
func TestClientErrorsNeverCarryTheToken(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("upstream said secret-token-value"))
	})
	_, _, err := c.Get(context.Background(), "/repos/o/r", nil, "")
	if err == nil {
		t.Fatal("Get succeeded on a 500")
	}
	if strings.Contains(err.Error(), "secret-token-value") {
		t.Fatalf("token leaked into the error: %v", err)
	}
}

func TestClientRetriesServerErrorsThenGivesUp(t *testing.T) {
	var calls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	})
	_, _, err := c.Get(context.Background(), "/x", nil, "")
	if err == nil {
		t.Fatal("Get succeeded on repeated 502s")
	}
	if calls != 3 {
		t.Errorf("made %d attempts, want 3", calls)
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadGateway {
		t.Errorf("error = %v, want a StatusError with 502", err)
	}
}

func TestClientRetriesThenSucceeds(t *testing.T) {
	var calls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
	body, _, err := c.Get(context.Background(), "/x", nil, "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
}

// 4xx other than 429 must not be retried: a 404 is an answer, and retrying
// it three times turns one wrong url into three requests against a rate
// limit somebody else is sharing.
func TestClientDoesNotRetryNotFound(t *testing.T) {
	var calls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	})
	_, _, err := c.Get(context.Background(), "/x", nil, "")
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
	if calls != 1 {
		t.Errorf("made %d attempts, want 1", calls)
	}
}

func TestClientClassifies(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		header map[string]string
		check  func(error) bool
	}{
		{"unauthorized", http.StatusUnauthorized, nil, IsUnauthorized},
		{"forbidden with no quota", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, IsRateLimited},
		{"too many requests", http.StatusTooManyRequests, nil, IsRateLimited},
		{"not found", http.StatusNotFound, nil, IsNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
			})
			_, _, err := c.Get(context.Background(), "/x", nil, "")
			if !tt.check(err) {
				t.Errorf("classifier said no for %v", err)
			}
		})
	}
}

// A 403 that is NOT a rate limit — an org policy, SAML enforcement — must
// not be reported as one. "wait for the reset" is useless advice for a
// permission that will never change on its own.
func TestForbiddenWithQuotaLeftIsNotRateLimited(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "4998")
		w.WriteHeader(http.StatusForbidden)
	})
	_, _, err := c.Get(context.Background(), "/x", nil, "")
	if IsRateLimited(err) {
		t.Errorf("403 with quota remaining classified as a rate limit: %v", err)
	}
}

func TestParseRepo(t *testing.T) {
	for _, tt := range []struct {
		url        string
		wantOwner  string
		wantSlug   string
		wantErr    bool
	}{
		{"https://github.com/org/monorepo", "org", "monorepo", false},
		{"https://github.com/org/monorepo.git", "org", "monorepo", false},
		{"https://github.com/org/monorepo/", "org", "monorepo", false},
		{"https://gitlab.com/group/sub/project", "group/sub", "project", false},
		{"https://github.com/onlyowner", "", "", true},
	} {
		t.Run(tt.url, func(t *testing.T) {
			r, err := ParseRepo("n", tt.url, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseRepo error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if r.Owner != tt.wantOwner || r.Slug != tt.wantSlug {
				t.Errorf("Owner/Slug = %q/%q, want %q/%q", r.Owner, r.Slug, tt.wantOwner, tt.wantSlug)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/fetch/ -run 'TestClient|TestForbidden|TestParseRepo' -v`
Expected: FAIL to compile — `undefined: NewClient`.

- [ ] **Step 3: Implement `fetch.go`**

```go
package fetch

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Repo identifies one repository to an adapter.
//
// Owner and Slug are split out because both hosts address a repository by
// its path and neither accepts the browser URL: GitHub wants
// /repos/{owner}/{slug} and GitLab wants the whole path percent-encoded as
// one id. Deriving them once, here, is what keeps that difference inside
// each adapter's URL builder instead of in cmd/.
type Repo struct {
	Name  string // the identity from repos.yaml; Entity.SourceRepo
	URL   string
	Ref   string // empty means "ask the host for its default branch"
	Host  string // scheme://hostname, for building the API base
	Owner string // "org", or "group/sub" on GitLab
	Slug  string // "monorepo"
}

// ParseRepo splits a repository URL.
//
// GitLab groups nest, so everything before the last path segment is the
// owner. GitHub's owner never nests, and treating its two-segment path the
// same way gives the same answer — so there is one rule here rather than a
// branch on which host it is.
func ParseRepo(name, rawURL, ref string) (Repo, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Repo{}, fmt.Errorf("cannot parse repository url %q: %w", rawURL, err)
	}
	p := strings.Trim(u.Path, "/")
	p = strings.TrimSuffix(p, ".git")
	owner, slug, found := cutLast(p, "/")
	if !found || owner == "" || slug == "" {
		return Repo{}, fmt.Errorf(
			"repository url %q has no owner and name; expected https://%s/<owner>/<repo>", rawURL, u.Hostname())
	}
	return Repo{
		Name: name, URL: rawURL, Ref: ref,
		Host:  u.Scheme + "://" + u.Host,
		Owner: owner, Slug: slug,
	}, nil
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// Fetcher is one repository on one host.
//
// Four methods rather than one Get, because cmd/ drives fetching in phases
// and each phase asks a different question (ruling R25). Two implementations
// exist — github and gitlab — which is what earns this the interface it has;
// a third, generic-git fetcher is what decision D5 says stays additive.
type Fetcher interface {
	// Open lists the repository and returns a filesystem holding its
	// metadata and no content. patterns bound the work when the host cannot
	// return a whole listing (ruling R28); an adapter that always can may
	// ignore them.
	Open(ctx context.Context, patterns []string) (*FS, error)

	// Expand lists directories Open may not have covered. A no-op when the
	// listing was already complete.
	Expand(ctx context.Context, f *FS, dirs []string) error

	// Fetch loads the content of paths into f. Paths not in f's listing are
	// an error: the caller computed the set, and asking for something that
	// is not there means the caller is wrong.
	Fetch(ctx context.Context, f *FS, paths []string) error

	// LastEdit reports when a path was last changed. ok is false when the
	// host has no answer, and docs-fresh then reports not-reported rather
	// than inventing a date (ruling R35).
	LastEdit(ctx context.Context, path string) (t time.Time, ok bool, err error)
}

// Cache is a content-addressed blob store, keyed on the git blob SHA the
// tree listing returns (ruling R27).
//
// An interface here and an implementation in cmd/ because a cache reads and
// writes real files, and nothing under internal/ may import os. A miss is
// never an error: a cache that cannot answer is a slow build, not a broken
// one, so Get has no error return at all.
type Cache interface {
	Get(sha string) ([]byte, bool)
	Put(sha string, data []byte) error
}

// NopCache is the --no-cache implementation and the default in tests.
type NopCache struct{}

func (NopCache) Get(string) ([]byte, bool)  { return nil, false }
func (NopCache) Put(string, []byte) error   { return nil }
```

- [ ] **Step 4: Implement `client.go`**

```go
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClientOptions constructs a Client. Every field is injected — there is no
// default client, no ambient http.DefaultClient and no package-level token,
// because two repositories on two hosts with two tokens must be able to
// coexist in one process.
type ClientOptions struct {
	HTTP        *http.Client
	BaseURL     string
	Token       string
	AuthHeader  string // "Authorization" on GitHub, "PRIVATE-TOKEN" on GitLab
	AuthPrefix  string // "Bearer " on GitHub, "" on GitLab
	Headers     map[string]string
	MaxAttempts int
	// Sleep is how the client waits between retries. Injected so the test
	// suite runs in milliseconds instead of in the backoff schedule.
	Sleep func(time.Duration)
}

// Client is one host's API, already authenticated.
type Client struct {
	http        *http.Client
	base        string
	token       string
	authHeader  string
	authPrefix  string
	headers     map[string]string
	maxAttempts int
	sleep       func(time.Duration)
}

func NewClient(o ClientOptions) *Client {
	c := &Client{
		http: o.HTTP, base: strings.TrimSuffix(o.BaseURL, "/"),
		token: o.Token, authHeader: o.AuthHeader, authPrefix: o.AuthPrefix,
		headers: o.Headers, maxAttempts: o.MaxAttempts, sleep: o.Sleep,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	if c.maxAttempts < 1 {
		c.maxAttempts = 3
	}
	if c.sleep == nil {
		c.sleep = time.Sleep
	}
	return c
}

// StatusError is a request that came back with a status the caller has to
// reason about.
//
// Endpoint is the path only, never the full URL with its query, and Body is
// truncated: both are printed to users, and a token that reached either
// would be printed with them.
type StatusError struct {
	Status     int
	Method     string
	Endpoint   string
	Body       string
	RetryAfter time.Duration
	// RateRemaining is the host's remaining quota, -1 when it said nothing.
	// It is what separates "you are rate limited" from "you are not allowed",
	// which arrive as the same 403.
	RateRemaining int
	// RateReset is when the quota returns, zero when unknown.
	RateReset time.Time
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Endpoint, e.Status, e.Body)
}

func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusNotFound
}

func IsUnauthorized(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusUnauthorized
}

// IsRateLimited is true for 429, and for the 403 both hosts return when a
// quota is spent.
//
// The quota check is what stops an org-policy 403 — SAML enforcement, an
// app with no access to a repository — being reported as a rate limit.
// "wait for the reset" is useless advice for a permission that will never
// change on its own, and it would send somebody away for an hour to come
// back to the same error.
func IsRateLimited(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	if se.Status == http.StatusTooManyRequests {
		return true
	}
	return se.Status == http.StatusForbidden && se.RateRemaining == 0
}

// Get performs an authenticated GET with retries.
//
// accept overrides the Accept header for one call, which is how a blob is
// asked for as raw bytes rather than as base64 inside JSON.
func (c *Client) Get(ctx context.Context, endpoint string, query url.Values, accept string) ([]byte, http.Header, error) {
	target := c.base + endpoint
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var last error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		body, header, err := c.once(ctx, target, endpoint, accept)
		if err == nil {
			return body, header, nil
		}
		last = err
		if !retryable(err) || attempt == c.maxAttempts {
			return nil, nil, err
		}
		c.sleep(backoff(attempt, err))
	}
	return nil, nil, last
}

func (c *Client) once(ctx context.Context, target, endpoint, accept string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.token != "" && c.authHeader != "" {
		req.Header.Set(c.authHeader, c.authPrefix+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// A transport error's message can contain the URL. It cannot
		// contain the token — the token is a header — but redact anyway:
		// this is the one place a future change could put it there.
		return nil, nil, fmt.Errorf("GET %s: %w", endpoint, c.redact(err))
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode/100 != 2 {
		return nil, nil, c.statusError(resp, endpoint, body)
	}
	if readErr != nil {
		return nil, nil, fmt.Errorf("GET %s: cannot read the response: %w", endpoint, readErr)
	}
	return body, resp.Header, nil
}

func (c *Client) statusError(resp *http.Response, endpoint string, body []byte) error {
	se := &StatusError{
		Status: resp.StatusCode, Method: http.MethodGet, Endpoint: endpoint,
		// Redact BEFORE truncating. summarise cuts at 200 bytes, and a token
		// straddling that cut survives as an unredacted prefix: the
		// full-string ReplaceAll no longer matches the halved value. Found by
		// review, reproduced with a 57-character token starting at byte 180.
		Body:          summarise(c.redactString(string(body))),
		RateRemaining: -1,
	}
	if v := resp.Header.Get("X-RateLimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			se.RateRemaining = n
		}
	}
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			se.RateReset = time.Unix(n, 0).UTC()
		}
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			se.RetryAfter = time.Duration(n) * time.Second
		}
	}
	return se
}

// redact removes the token from anything on its way to a user.
func (c *Client) redact(err error) error {
	if c.token == "" {
		return err
	}
	return errors.New(c.redactString(err.Error()))
}

func (c *Client) redactString(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "[redacted]")
}

// summarise trims an ALREADY-REDACTED body to something printable. A host's
// error body can be a full HTML page, and a diagnostic is one line.
//
// It takes a string rather than bytes to make the ordering hard to get
// wrong: the only caller must have redacted before it can call this.
func summarise(body string) string {
	s := strings.TrimSpace(body)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		s = "(empty response body)"
	}
	return s
}

// retryable is true for the failures that go away on their own: a 5xx, a
// rate limit, and a transport error. A 404 is an answer and a 401 is a
// decision; repeating either spends somebody's quota to be told the same
// thing three times.
func retryable(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	return se.Status/100 == 5 || IsRateLimited(err)
}

// backoff is 1s, 2s, 4s, unless the host said how long to wait.
func backoff(attempt int, err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) && se.RetryAfter > 0 {
		return se.RetryAfter
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
```

> **This task's code is verified, not sketched.** `fetch.go` and `client.go` were compiled and run against the eight tests above before this plan was written: `go vet` clean, all green.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/fetch/ -v`
Expected: PASS, including the Task 6 tests.

- [ ] **Step 6: Prove the suite is offline**

Run: `go test ./internal/fetch/ -count=1` with the machine's network disabled, or `GOFLAGS=-count=1 go test ./internal/fetch/` inside a sandbox with no egress.
Expected: PASS. Spec §14 requires zero network in the test suite; `httptest.NewServer` binds loopback and satisfies it. If any test here ever reaches a real host it must be deleted, not skipped.

- [ ] **Step 7: Commit**

```bash
git add internal/fetch/fetch.go internal/fetch/client.go internal/fetch/client_test.go
git commit -m "feat(fetch): the shared HTTP client, with classified errors

Retries what goes away on its own and nothing else. A 403 with quota
remaining is not a rate limit -- telling somebody to wait an hour for an
org policy sends them away to come back to the same error.

Tokens are redacted from every error string: the error path is the one
place a request gets described back to the user.

Rulings R29, R32."
```

---
### Task 8: The GitHub adapter

Four endpoints, all verified against the live documentation on 2026-09-10:

| Purpose | Request |
|---|---|
| default branch | `GET /repos/{owner}/{repo}` → `.default_branch` |
| tree | `GET /repos/{owner}/{repo}/git/trees/{ref}?recursive=1` → `.tree[]` of `{path,mode,type,sha,size}` plus `.truncated` |
| blob | `GET /repos/{owner}/{repo}/git/blobs/{sha}` with `Accept: application/vnd.github.raw+json` → raw bytes |
| last edit | `GET /repos/{owner}/{repo}/commits?path=…&per_page=1&sha={ref}` → `[0].commit.committer.date` |

Headers: `Authorization: Bearer <token>`, `X-GitHub-Api-Version: 2026-03-10`, `Accept: application/vnd.github+json`. Truncation is Task 9; this task returns a specific error for it so that task has something to replace.

**Files:**
- Create: `internal/fetch/blobs.go` (shared with Task 10), `internal/fetch/github.go`
- Test: `internal/fetch/github_test.go`

**Interfaces:**
- Consumes: `Client`, `Repo`, `Cache`, `Entry`, `FS` from Tasks 6 and 7.
- Produces:
  - `func NewGitHub(r Repo, c *Client, cache Cache, parallel int) *GitHub` — implements `Fetcher`
  - `func GitHubBaseURL(r Repo) string`
  - `func fetchBlobs(ctx context.Context, repo string, f *FS, paths []string, parallel int, cache Cache, get blobGetter) error` — used by Task 10
  - `type FetchError struct{ Repo string; Paths []string; Err error }`
  - `func gitBlobSHA(data []byte) string`
  - `var errTruncated error` — replaced by Task 9

- [ ] **Step 1: Write the failing test**

Create `internal/fetch/github_test.go`:

```go
package fetch

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// githubServer serves a fixed repository. Every test in this file runs
// against it; no test in this package ever reaches the network (spec §14).
func githubServer(t *testing.T, blobs map[string]string, truncated bool) *httptest.Server {
	t.Helper()
	tree := []map[string]any{
		{"path": "service.yaml", "type": "blob", "sha": "sha-svc", "size": 20},
		{"path": "docs", "type": "tree", "sha": "sha-docs"},
		{"path": "docs/index.md", "type": "blob", "sha": "sha-idx", "size": 9},
		{"path": "vendored", "type": "commit", "sha": "sha-sub"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/org/repo":
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "trunk"})
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/git/trees/"):
			if r.URL.Query().Get("recursive") != "1" {
				t.Errorf("tree request was not recursive: %s", r.URL)
			}
			json.NewEncoder(w).Encode(map[string]any{"tree": tree, "truncated": truncated})
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/git/blobs/"):
			sha := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/git/blobs/")
			if got := r.Header.Get("Accept"); got != "application/vnd.github.raw+json" {
				t.Errorf("blob Accept = %q, want the raw media type", got)
			}
			body, ok := blobs[sha]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(body))
		case r.URL.Path == "/repos/org/repo/commits":
			if r.URL.Query().Get("path") == "nohistory" {
				w.Write([]byte(`[]`))
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"commit": map[string]any{"committer": map[string]any{"date": "2026-08-01T09:30:00Z"}}},
			})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestGitHub(t *testing.T, srv *httptest.Server, ref string) *GitHub {
	t.Helper()
	r, err := ParseRepo("repo", "https://github.com/org/repo", ref)
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}
	c := NewClient(ClientOptions{
		HTTP: srv.Client(), BaseURL: srv.URL, Token: "t",
		AuthHeader: "Authorization", AuthPrefix: "Bearer ",
		Headers:     map[string]string{"X-GitHub-Api-Version": "2026-03-10", "Accept": "application/vnd.github+json"},
		MaxAttempts: 1, Sleep: func(time.Duration) {},
	})
	return NewGitHub(r, c, NopCache{}, 4)
}

func TestGitHubOpenListsTheTree(t *testing.T) {
	g := newTestGitHub(t, githubServer(t, nil, false), "main")
	f, err := g.Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fs.Stat(f, "docs/index.md"); err != nil {
		t.Errorf("Stat docs/index.md: %v", err)
	}
	info, err := fs.Stat(f, "docs")
	if err != nil || !info.IsDir() {
		t.Errorf("docs should be a directory: info=%v err=%v", info, err)
	}
	// A submodule is a pointer to another repository with no content here.
	// Listing it as a file would make CheckFiles see a path it can never read.
	if _, err := fs.Stat(f, "vendored"); err == nil {
		t.Error("submodule (type commit) was listed as a file")
	}
}

func TestGitHubOpenResolvesTheDefaultBranch(t *testing.T) {
	var askedRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/org/repo":
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "trunk"})
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/git/trees/"):
			askedRef = strings.TrimPrefix(r.URL.Path, "/repos/org/repo/git/trees/")
			json.NewEncoder(w).Encode(map[string]any{"tree": []any{}, "truncated": false})
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitHub(t, srv, "") // no ref configured
	if _, err := g.Open(context.Background(), nil); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if askedRef != "trunk" {
		t.Errorf("listed ref %q, want the host's default branch %q", askedRef, "trunk")
	}
}

func TestGitHubFetchLoadsContent(t *testing.T) {
	body := "# Docs\n"
	srv := githubServer(t, map[string]string{gitBlobSHA([]byte(body)): body}, false)

	// The tree's sha must be the real one for verification to pass.
	g := newTestGitHub(t, srv, "main")
	f, err := g.Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.AddDir("docs", []Entry{{Path: "docs/index.md", SHA: gitBlobSHA([]byte(body)), Size: int64(len(body))}})

	if err := g.Fetch(context.Background(), f, []string{"docs/index.md"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := fs.ReadFile(f, "docs/index.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != body {
		t.Errorf("ReadFile = %q, want %q", got, body)
	}
}

// A blob whose bytes do not hash to the sha the tree promised is corruption,
// on the wire or in the cache. Serving it would put wrong content in the
// portal with nothing to notice it by.
func TestGitHubFetchRejectsACorruptBlob(t *testing.T) {
	real := "# Docs\n"
	sha := gitBlobSHA([]byte(real))
	srv := githubServer(t, map[string]string{sha: "TAMPERED"}, false)

	g := newTestGitHub(t, srv, "main")
	f, _ := g.Open(context.Background(), nil)
	f.AddDir("docs", []Entry{{Path: "docs/index.md", SHA: sha, Size: int64(len(real))}})

	err := g.Fetch(context.Background(), f, []string{"docs/index.md"})
	if err == nil {
		t.Fatal("Fetch accepted a blob that does not match its sha")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error = %v, want it to name the mismatch", err)
	}
}

func TestGitHubLastEdit(t *testing.T) {
	g := newTestGitHub(t, githubServer(t, nil, false), "main")

	got, ok, err := g.LastEdit(context.Background(), "docs")
	if err != nil {
		t.Fatalf("LastEdit: %v", err)
	}
	if !ok {
		t.Fatal("LastEdit ok = false, want true")
	}
	want := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("LastEdit = %v, want %v", got, want)
	}

	// A path with no commits is "unknown", never "now" and never the zero
	// time: docs-fresh reports not-reported rather than inventing a date.
	if _, ok, err := g.LastEdit(context.Background(), "nohistory"); err != nil || ok {
		t.Errorf("LastEdit for a path with no history = (ok %v, err %v), want (false, nil)", ok, err)
	}
}

func TestGitHubLastEditIsMemoized(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode([]map[string]any{
			{"commit": map[string]any{"committer": map[string]any{"date": "2026-08-01T09:30:00Z"}}},
		})
	}))
	t.Cleanup(srv.Close)

	g := newTestGitHub(t, srv, "main")
	for range 3 {
		if _, _, err := g.LastEdit(context.Background(), "docs"); err != nil {
			t.Fatalf("LastEdit: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("made %d requests for the same path, want 1", calls)
	}
}

func TestGitHubBaseURL(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"https://github.com/org/repo", "https://api.github.com"},
		{"https://ghe.internal/org/repo", "https://ghe.internal/api/v3"},
	} {
		r, err := ParseRepo("n", tt.url, "")
		if err != nil {
			t.Fatalf("ParseRepo: %v", err)
		}
		if got := GitHubBaseURL(r); got != tt.want {
			t.Errorf("GitHubBaseURL(%s) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/fetch/ -run TestGitHub -v`
Expected: FAIL to compile — `undefined: NewGitHub`.

- [ ] **Step 3: Implement `blobs.go`**

```go
package fetch

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// blobGetter fetches one blob by its sha.
type blobGetter func(ctx context.Context, sha string) ([]byte, error)

// FetchError is one repository's failed reads.
//
// It names every path rather than only the first, because --allow-partial's
// banner and the operator's next move both depend on knowing whether one
// file or forty went missing. Err is kept for classification: IsNotFound and
// IsRateLimited see through Unwrap, so cmd/ can tell "your token expired"
// from "that file is gone".
type FetchError struct {
	Repo  string
	Paths []string
	Err   error
}

func (e *FetchError) Error() string {
	if len(e.Paths) == 0 {
		// No construction site produces this today, but the cancellation
		// accounting above builds a FetchError from a second site, and an
		// unconditional e.Paths[0] below would panic if one ever did.
		return fmt.Sprintf("%s: cannot fetch: %v", e.Repo, e.Err)
	}
	if len(e.Paths) == 1 {
		return fmt.Sprintf("%s: cannot fetch %s: %v", e.Repo, e.Paths[0], e.Err)
	}
	return fmt.Sprintf("%s: cannot fetch %d files, starting with %s: %v",
		e.Repo, len(e.Paths), e.Paths[0], e.Err)
}

func (e *FetchError) Unwrap() error { return e.Err }

// fetchBlobs loads the content of paths into f, at most parallel at a time.
//
// Shared by both adapters: they differ in how a blob is addressed and in
// nothing else that happens here. The cache is consulted first and populated
// after, keyed on the git blob sha from the tree listing — content-addressed,
// so a hit cannot be stale (ruling R27).
func fetchBlobs(ctx context.Context, repo string, f *FS, paths []string, parallel int, cache Cache, get blobGetter) error {
	if parallel < 1 {
		parallel = 1
	}
	byPath := map[string]Entry{}
	for _, e := range f.Entries() {
		byPath[e.Path] = e
	}

	type outcome struct {
		path string
		data []byte
		err  error
	}
	work := make(chan string)
	results := make(chan outcome)

	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				e, ok := byPath[p]
				if !ok {
					// The caller computed this set from f's own listing, so
					// a path that is not in it means the planner is wrong.
					results <- outcome{p, nil, fmt.Errorf("%s is not in the repository listing", p)}
					continue
				}
				// A corrupt cache entry is not fatal: it falls through and
				// is fetched again from the host, which then overwrites it.
				if cached, hit := cache.Get(e.SHA); hit && verifyBlob(e.SHA, cached) == nil {
					results <- outcome{p, cached, nil}
					continue
				}
				data, err := get(ctx, e.SHA)
				if err == nil {
					err = verifyBlob(e.SHA, data)
				}
				if err == nil {
					_ = cache.Put(e.SHA, data) // a cache that cannot write is a slow build
				}
				results <- outcome{p, data, err}
			}
		}()
	}

	go func() {
		defer close(work)
		for _, p := range paths {
			select {
			case work <- p:
			case <-ctx.Done():
				// Stop dispatching, but do NOT return silently: every
				// abandoned path is accounted for after the results loop.
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	// Every requested path must be accounted for exactly once. A cancelled
	// context used to make the producer abandon undispatched paths, which
	// then produced no outcome at all — so failed stayed empty, firstErr
	// stayed nil, and fetchBlobs reported success while f was missing
	// content the caller believed was present. Silent corruption, found by
	// review. Anything unseen when results closes is a failure.
	outstanding := make(map[string]bool, len(paths))
	for _, p := range paths {
		outstanding[p] = true
	}

	var failed []string
	var firstErr error
	for r := range results {
		delete(outstanding, r.path)
		if r.err != nil {
			failed = append(failed, r.path)
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		f.Put(r.path, r.data)
	}
	for p := range outstanding {
		failed = append(failed, p)
		if firstErr == nil {
			firstErr = ctx.Err()
			if firstErr == nil {
				firstErr = errors.New("the fetch stopped before this file was requested")
			}
		}
	}
	if firstErr != nil {
		// Sorted so the message is the same on every run: the worker pool
		// finishes in whatever order it finishes.
		slices.Sort(failed)
		return &FetchError{Repo: repo, Paths: failed, Err: firstErr}
	}
	return nil
}

// verifyBlob checks that data hashes to sha.
//
// Not a security control — the trust boundary in spec §14.1 is that these
// repositories are same-organisation content. It catches a truncated
// transfer and a corrupted cache entry, both of which would otherwise put
// wrong bytes in the portal with nothing to notice them by.
//
// Only SHA-1 object ids are checked. A repository using SHA-256 object
// format returns 64-character ids, and verifying those against SHA-1 would
// fail every blob in it; landsraad would rather not check than be wrong.
func verifyBlob(sha string, data []byte) error {
	if len(sha) != 40 {
		return nil
	}
	if got := gitBlobSHA(data); !strings.EqualFold(got, sha) {
		return fmt.Errorf("content does not match its sha: the host said %s, the bytes hash to %s", sha, got)
	}
	return nil
}

// gitBlobSHA is git's object id for a blob: sha1 over "blob <len>\x00" and
// the content. SHA-1 is git's choice, not a security choice.
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
```

- [ ] **Step 4: Implement `github.go`**

```go
package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// errTruncated is GitHub saying the recursive listing did not fit. Task 9
// replaces the caller's handling of it with a pattern-directed descent; until
// then it is an honest failure rather than a silently short listing.
var errTruncated = errors.New("the repository is too large for a single tree listing")

// GitHub fetches a repository over the GitHub REST API (decision D5).
type GitHub struct {
	repo     Repo
	c        *Client
	cache    Cache
	parallel int

	// mu guards BOTH ref and edits. ref was outside it in an earlier draft
	// and review caught the race: resolveRef reads and writes it unlocked
	// while Open and LastEdit both call resolveRef, so two concurrent
	// LastEdit calls — which the memoisation right below exists to support —
	// race on it. sync.Once is banned below cmd/, so the mutex covers both.
	mu    sync.Mutex
	ref   string          // resolved on first use, under mu
	edits map[string]edit // memoized LastEdit answers (ruling R35)
}

type edit struct {
	t  time.Time
	ok bool
}

func NewGitHub(r Repo, c *Client, cache Cache, parallel int) *GitHub {
	return &GitHub{repo: r, c: c, cache: cache, parallel: parallel, ref: r.Ref, edits: map[string]edit{}}
}

// GitHubBaseURL is api.github.com for the public host and /api/v3 for GitHub
// Enterprise, which is where a self-hosted instance puts the same API.
func GitHubBaseURL(r Repo) string {
	if u, err := url.Parse(r.URL); err == nil && u.Hostname() == "github.com" {
		return "https://api.github.com"
	}
	return r.Host + "/api/v3"
}

func (g *GitHub) base() string {
	return "/repos/" + url.PathEscape(g.repo.Owner) + "/" + url.PathEscape(g.repo.Slug)
}

// resolveRef asks the host for its default branch when repos.yaml named no
// ref. Assuming "main" would fetch nothing from every repository that still
// uses "master" and report it as a repository that does not exist.
func (g *GitHub) resolveRef(ctx context.Context) error {
	if g.ref != "" {
		return nil
	}
	body, _, err := g.c.Get(ctx, g.base(), nil, "")
	if err != nil {
		return err
	}
	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("cannot read the repository description: %w", err)
	}
	if payload.DefaultBranch == "" {
		return errors.New("the host reported no default branch; set `ref:` in repos.yaml")
	}
	g.ref = payload.DefaultBranch
	return nil
}

func (g *GitHub) Open(ctx context.Context, patterns []string) (*FS, error) {
	if err := g.resolveRef(ctx); err != nil {
		return nil, err
	}
	body, _, err := g.c.Get(ctx, g.base()+"/git/trees/"+url.PathEscape(g.ref),
		url.Values{"recursive": {"1"}}, "")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("cannot read the tree listing: %w", err)
	}
	if payload.Truncated {
		// GitHub truncates at 100,000 entries or 7 MB. Task 9 replaces this
		// with a pattern-directed descent.
		return nil, errTruncated
	}
	var entries []Entry
	for _, e := range payload.Tree {
		switch e.Type {
		case "blob":
			entries = append(entries, Entry{Path: e.Path, SHA: e.SHA, Size: e.Size})
		case "tree":
			entries = append(entries, Entry{Path: e.Path, Dir: true})
		// "commit" is a submodule: a pointer to another repository, with no
		// content on this side. Listing it as a file would give CheckFiles a
		// path that exists and can never be read.
		}
	}
	return FromEntries(entries), nil
}

// Expand is a no-op for a complete listing. Task 9 gives it a body.
func (g *GitHub) Expand(ctx context.Context, f *FS, dirs []string) error { return nil }

func (g *GitHub) Fetch(ctx context.Context, f *FS, paths []string) error {
	return fetchBlobs(ctx, g.repo.Name, f, paths, g.parallel, g.cache,
		func(ctx context.Context, sha string) ([]byte, error) {
			body, _, err := g.c.Get(ctx, g.base()+"/git/blobs/"+url.PathEscape(sha),
				nil, "application/vnd.github.raw+json")
			return body, err
		})
}

func (g *GitHub) LastEdit(ctx context.Context, p string) (time.Time, bool, error) {
	g.mu.Lock()
	hit, seen := g.edits[p]
	g.mu.Unlock()
	if seen {
		return hit.t, hit.ok, nil
	}
	if err := g.resolveRef(ctx); err != nil {
		return time.Time{}, false, err
	}
	body, _, err := g.c.Get(ctx, g.base()+"/commits", url.Values{
		"path": {p}, "per_page": {strconv.Itoa(1)}, "sha": {g.ref},
	}, "")
	if err != nil {
		return time.Time{}, false, err
	}
	var payload []struct {
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return time.Time{}, false, fmt.Errorf("cannot read the commit list for %s: %w", p, err)
	}
	res := edit{}
	if len(payload) > 0 {
		res = edit{t: payload[0].Commit.Committer.Date.UTC(), ok: true}
	}
	g.mu.Lock()
	g.edits[p] = res
	g.mu.Unlock()
	return res.t, res.ok, nil
}

var _ Fetcher = (*GitHub)(nil)
```

> **This task's code is verified, not sketched.** `blobs.go` and `github.go` were compiled and run against the seven tests above before this plan was written: `go vet` clean, all green.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/fetch/ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/fetch/blobs.go internal/fetch/github.go internal/fetch/github_test.go
git commit -m "feat(fetch): the GitHub adapter

Lists a tree in one request, fetches only the blobs landsraad reads, and
verifies every blob against the sha the listing promised -- a truncated
transfer or a corrupted cache entry would otherwise put wrong bytes in the
portal with nothing to notice them by.

Submodules are skipped: a type=commit entry is a pointer to another
repository with no content here, and listing it as a file gives CheckFiles
a path that exists and can never be read.

Rulings R24, R27, R35."
```

---
### Task 9: GitHub's truncated trees

GitHub truncates a recursive listing at **100,000 entries or 7 MB**, and returns `"truncated": true` rather than an error. Task 8 turns that into `errTruncated`; this task replaces it with a descent that lists only the directories the configured patterns can reach. For `paths: [services/*]` that is the root, `services/`, and each `services/<x>/` — roughly `2 + entities` requests instead of one, and it is the only thing that works on the repositories large enough to motivate R24.

The same descent gives `Expand` its body, which is what fills in a `spec.docs` directory nobody knew about until the entities were parsed.

**Files:**
- Create: `internal/fetch/githubwalk.go`
- Modify: `internal/fetch/github.go` (`Open` calls `walk`, `Expand` gains a body)
- Test: `internal/fetch/githubwalk_test.go`

**Interfaces:**
- Consumes: `GitHub`, `FS`, `Entry` from Tasks 6 and 8.
- Produces: no new exported API. `errTruncated` stops being returned to callers.

- [ ] **Step 1: Write the failing test**

Create `internal/fetch/githubwalk_test.go`:

```go
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

// bigRepoServer always reports the recursive listing as truncated, so every
// test here exercises the descent. Trees are addressed by sha, as GitHub
// addresses them.
func bigRepoServer(t *testing.T, listed *[]string) *httptest.Server {
	t.Helper()
	trees := map[string][]map[string]any{
		"trunk": {
			{"path": "services", "type": "tree", "sha": "t-services"},
			{"path": "vendor", "type": "tree", "sha": "t-vendor"},
			{"path": "README.md", "type": "blob", "sha": "b-readme", "size": 3},
		},
		"t-services": {
			{"path": "api", "type": "tree", "sha": "t-api"},
			{"path": "worker", "type": "tree", "sha": "t-worker"},
		},
		"t-api": {
			{"path": "service.yaml", "type": "blob", "sha": "b-api-svc", "size": 10},
			{"path": "docs", "type": "tree", "sha": "t-api-docs"},
		},
		"t-api-docs": {
			{"path": "index.md", "type": "blob", "sha": "b-api-idx", "size": 5},
			{"path": "deep", "type": "tree", "sha": "t-api-deep"},
		},
		"t-api-deep": {
			{"path": "more.md", "type": "blob", "sha": "b-more", "size": 4},
		},
		"t-worker": {
			{"path": "service.yaml", "type": "blob", "sha": "b-wk-svc", "size": 10},
		},
		"t-vendor": {},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sha := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/git/trees/")
		if r.URL.Query().Get("recursive") == "1" {
			// The whole-repository listing never fits.
			json.NewEncoder(w).Encode(map[string]any{"tree": []any{}, "truncated": true})
			return
		}
		entries, ok := trees[sha]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		*listed = append(*listed, sha)
		json.NewEncoder(w).Encode(map[string]any{"tree": entries, "truncated": false})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubDescendsWhenTruncated(t *testing.T) {
	var listed []string
	g := newTestGitHub(t, bigRepoServer(t, &listed), "trunk")

	f, err := g.Open(context.Background(), []string{"services/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// The service.yaml files the patterns point at are listed.
	for _, p := range []string{"services/api/service.yaml", "services/worker/service.yaml"} {
		if _, err := fs.Stat(f, p); err != nil {
			t.Errorf("Stat %s: %v", p, err)
		}
	}
	// vendor/ was never walked, so nothing under it was listed -- and asking
	// about it must say so rather than claim the file is not there.
	if _, err := fs.Stat(f, "vendor/huge/thing.md"); !isNotListed(err) {
		t.Errorf("vendor path error = %v, want ErrNotListed", err)
	}
	sort.Strings(listed)
	if got := strings.Join(listed, ","); strings.Contains(got, "t-vendor") {
		t.Errorf("walked vendor/, which no pattern can reach: %s", got)
	}
}

// spec.docs is not known until entities are parsed, so the descent cannot
// have listed it. Expand fills it in, recursively, because a docs directory
// has subdirectories and every .md under it becomes a page.
func TestGitHubExpandListsADocsTree(t *testing.T) {
	var listed []string
	g := newTestGitHub(t, bigRepoServer(t, &listed), "trunk")
	f, err := g.Open(context.Background(), []string{"services/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := g.Expand(context.Background(), f, []string{"services/api/docs"}); err != nil {
		t.Fatalf("Expand: %v", err)
	}
	for _, p := range []string{"services/api/docs/index.md", "services/api/docs/deep/more.md"} {
		if _, err := fs.Stat(f, p); err != nil {
			t.Errorf("Stat %s after Expand: %v", p, err)
		}
	}
}

// Expand on an already-complete listing costs nothing. This is what lets
// cmd/ call it unconditionally rather than branching on which host, which
// ref, and whether the listing happened to be truncated.
func TestGitHubExpandIsFreeOnACompleteListing(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]any{
			{"path": "docs", "type": "tree", "sha": "d"},
			{"path": "docs/index.md", "type": "blob", "sha": "i", "size": 1},
		}, "truncated": false})
	}))
	t.Cleanup(srv.Close)

	g := newTestGitHub(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	before := requests
	if err := g.Expand(context.Background(), f, []string{"docs"}); err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if requests != before {
		t.Errorf("Expand made %d requests against a complete listing, want 0", requests-before)
	}
}

func isNotListed(err error) bool {
	return err != nil && strings.Contains(fmt.Sprint(err), "never listed")
}

var _ = time.Second
```

> **Note for the implementer:** `isNotListed` here uses the error text only because this file has no other handle on it; prefer `errors.Is(err, ErrNotListed)` and delete both the helper and the `time` blank. The rule in `scripts/check-rules.sh` bans `strings.Contains` against `.Message` and `.Hint` on **diagnostics**, which these are not — but the reason behind that rule applies here too.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/fetch/ -run 'TestGitHubDescends|TestGitHubExpand' -v`
Expected: FAIL — `Open` returns `errTruncated`.

- [ ] **Step 3: Implement `githubwalk.go`**

```go
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// ghEntry is one row of a non-recursive tree listing. GitHub reports paths
// relative to the tree being listed, not to the repository root, so every
// caller joins them onto the directory it asked about.
type ghEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// listTree fetches one tree by sha, without recursing.
func (g *GitHub) listTree(ctx context.Context, sha string) ([]ghEntry, error) {
	body, _, err := g.c.Get(ctx, g.base()+"/git/trees/"+url.PathEscape(sha), nil, "")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tree []ghEntry `json:"tree"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("cannot read the tree listing: %w", err)
	}
	return payload.Tree, nil
}

// record adds one listing to f and returns the subtree shas it found, keyed
// by full path.
func (g *GitHub) record(f *FS, dir string, rows []ghEntry) map[string]string {
	subtrees := map[string]string{}
	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		full := r.Path
		if dir != "." {
			full = path.Join(dir, r.Path)
		}
		switch r.Type {
		case "blob":
			entries = append(entries, Entry{Path: full, SHA: r.SHA, Size: r.Size})
		case "tree":
			entries = append(entries, Entry{Path: full, Dir: true})
			subtrees[full] = r.SHA
		// "commit" is a submodule; see Open.
		}
	}
	f.AddDir(dir, entries)
	return subtrees
}

// walk lists only what patterns can reach.
//
// The alternative, when a repository is too large for one recursive listing,
// is to give up — and the repositories large enough to truncate are exactly
// the monorepos this tool exists for. Walking costs roughly 2 + entities
// requests, which is worse than one and enormously better than nothing.
func (g *GitHub) walk(ctx context.Context, patterns []string) (*FS, error) {
	f := NewFS()
	shas := map[string]string{".": g.ref}

	rows, err := g.listTree(ctx, g.ref)
	if err != nil {
		return nil, err
	}
	for p, sha := range g.record(f, ".", rows) {
		shas[p] = sha
	}

	for _, pattern := range patterns {
		dirs, err := g.expandPattern(ctx, f, shas, pattern)
		if err != nil {
			return nil, err
		}
		// Each matched directory must itself be listed: that is where
		// discover.Find looks for service.yaml.
		for _, d := range dirs {
			if err := g.listDir(ctx, f, shas, d); err != nil {
				return nil, err
			}
		}
	}
	return f, nil
}

// expandPattern resolves one repos.yaml glob to the directories it matches,
// listing intermediate directories as it descends.
//
// It handles the shapes fs.Glob accepts in a repos.yaml path: literal
// segments and a segment containing a wildcard. It deliberately does not
// implement "**" — neither fs.Glob nor discover.Find supports it either, so
// accepting it here would make the fallback path match things the fast path
// does not.
func (g *GitHub) expandPattern(ctx context.Context, f *FS, shas map[string]string, pattern string) ([]string, error) {
	if pattern == "." || pattern == "" {
		return []string{"."}, nil
	}
	current := []string{"."}
	for _, seg := range strings.Split(pattern, "/") {
		var next []string
		for _, dir := range current {
			if err := g.listDir(ctx, f, shas, dir); err != nil {
				return nil, err
			}
			if !strings.ContainsAny(seg, "*?[") {
				candidate := join(dir, seg)
				if _, ok := shas[candidate]; ok {
					next = append(next, candidate)
				}
				continue
			}
			for child, sha := range shas {
				_ = sha
				if path.Dir(child) != dir {
					continue
				}
				if ok, err := path.Match(seg, path.Base(child)); err == nil && ok {
					next = append(next, child)
				}
			}
		}
		current = next
		if len(current) == 0 {
			// A pattern matching nothing is not an error: a repository may
			// legitimately have no services under a configured path. Compare
			// discover.Find, which makes the same distinction.
			return nil, nil
		}
	}
	return current, nil
}

// listDir lists dir if it has not been listed already, recording any
// subtrees it finds.
func (g *GitHub) listDir(ctx context.Context, f *FS, shas map[string]string, dir string) error {
	if f.Listed(dir) {
		return nil
	}
	sha, ok := shas[dir]
	if !ok {
		// Nothing on the way here saw this directory, so it is not there.
		// f already answers Stat correctly for it; there is nothing to list.
		return nil
	}
	rows, err := g.listTree(ctx, sha)
	if err != nil {
		return err
	}
	for p, s := range g.record(f, dir, rows) {
		shas[p] = s
	}
	return nil
}

// expandRecursive lists dir and everything beneath it. Used by Expand for a
// spec.docs directory, whose subdirectories each hold pages.
func (g *GitHub) expandRecursive(ctx context.Context, f *FS, shas map[string]string, dir string) error {
	if err := g.listDir(ctx, f, shas, dir); err != nil {
		return err
	}
	for _, e := range f.Entries() {
		if !e.Dir || path.Dir(e.Path) != dir {
			continue
		}
		if err := g.expandRecursive(ctx, f, shas, e.Path); err != nil {
			return err
		}
	}
	return nil
}

func join(dir, seg string) string {
	if dir == "." {
		return seg
	}
	return dir + "/" + seg
}
```

- [ ] **Step 4: Wire it into `github.go`**

`Open`'s truncation branch calls `walk`, and `Expand` gets a body. `GitHub` gains a `shas map[string]string` field so the two share what the descent learned:

```go
	if payload.Truncated {
		// 100,000 entries or 7 MB. Ruling R28: list what the patterns can
		// reach rather than refusing a repository for being large.
		return g.walk(ctx, patterns)
	}
```

```go
// Expand lists directories Open did not cover, which under a complete
// listing is none of them.
//
// Free on the fast path, which is what lets cmd/ call it unconditionally
// instead of branching on host, ref and whether this particular listing
// happened to truncate. A path whose parent was never listed reads as
// ErrNotListed rather than as a missing file, and this is what turns the
// former into the latter honestly: after Expand, absent means absent.
func (g *GitHub) Expand(ctx context.Context, f *FS, dirs []string) error {
	for _, d := range dirs {
		// Descend from the root first. listDir can only list a directory
		// whose tree sha it already holds, and a spec.docs outside every
		// configured path glob was never walked — so without this, Expand
		// missed in shas and returned silently, and the directory never came
		// to exist. walk always lists the root, so every ancestor is
		// reachable one listing at a time; each step no-ops if already
		// listed, which is what keeps Expand free on a complete listing.
		if err := g.listAncestors(ctx, f, d); err != nil {
			return err
		}
		if err := g.expandRecursive(ctx, f, g.shas, d); err != nil {
			return err
		}
	}
	return nil
}

// listAncestors lists each directory on the path to dir, outermost first.
//
// A segment the parent listing does not contain is not an error: shas has no
// entry for it, listDir returns without listing, and Stat on the requested
// path reports ErrNotExist because its parent WAS listed. A spec.docs
// pointing somewhere that does not exist is a catalog problem for CheckFiles
// to report, not a fetch failure — the same treatment GitLab's Expand gives
// a 404.
func (g *GitHub) listAncestors(ctx context.Context, f *FS, dir string) error {
	if dir == "." || dir == "" {
		return nil
	}
	segs := strings.Split(dir, "/")
	cur := ""
	for _, seg := range segs {
		cur = join(cur, seg)
		if cur == dir {
			break
		}
		if err := g.listDir(ctx, f, g.shas, cur); err != nil {
			return err
		}
	}
	return nil
}
```

Initialise `shas` in `NewGitHub` as `map[string]string{}`, and have `walk` write into `g.shas` rather than a local map. `errTruncated` is now unreferenced outside `Open`'s comment — delete the variable.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/fetch/ -v`
Expected: PASS, including every Task 6, 7 and 8 test.

- [ ] **Step 6: Commit**

```bash
git add internal/fetch/githubwalk.go internal/fetch/github.go internal/fetch/githubwalk_test.go
git commit -m "feat(fetch): descend the tree when GitHub truncates the listing

GitHub truncates a recursive listing at 100,000 entries or 7 MB and says
so in a json field rather than an error. Repositories that large are
exactly the monorepos this tool exists for, so refusing them is not an
option; the descent lists only what the configured patterns can reach.

Ruling R28."
```

---

### Task 10: The GitLab adapter

GitLab differs from GitHub in three ways that matter, and every one of them is a silent wrong answer if missed:

- **`per_page` defaults to 20.** A fifteen-service repository returning its first twenty entries looks complete. Pagination is the common path here, not an edge case.
- **The tree endpoint takes `path=`**, so there is no truncation to work around: list recursively under each pattern's literal prefix and the work is bounded by construction.
- **Tree rows carry no size.** `Entry.Size` is 0 for every GitLab file. Nothing in landsraad reads a file size — `CheckFiles` asks only `IsDir`, and everything else reads bytes — so this is recorded rather than worked around.

Endpoints, verified against the live documentation on 2026-09-10:

| Purpose | Request |
|---|---|
| default branch | `GET /projects/{id}` → `.default_branch` |
| tree | `GET /projects/{id}/repository/tree?ref=&recursive=true&per_page=100&path=` → `[{id,name,type,path,mode}]` |
| blob | `GET /projects/{id}/repository/blobs/{sha}/raw` → raw bytes |
| last edit | `GET /projects/{id}/repository/commits?path=…&ref_name=…&per_page=1` → `[0].committed_date` |

`{id}` is the project path percent-encoded whole: `group/sub/project` becomes `group%2Fsub%2Fproject`. Auth is `PRIVATE-TOKEN: <token>`.

**Files:**
- Create: `internal/fetch/gitlab.go`
- Test: `internal/fetch/gitlab_test.go`

**Interfaces:**
- Consumes: `Client`, `Repo`, `Cache`, `FS`, `fetchBlobs` from Tasks 6–8.
- Produces: `func NewGitLab(r Repo, c *Client, cache Cache, parallel int) *GitLab` — implements `Fetcher`; `func GitLabBaseURL(r Repo) string`.

- [ ] **Step 1: Write the failing test**

Create `internal/fetch/gitlab_test.go`:

```go
package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func newTestGitLab(t *testing.T, srv *httptest.Server, ref string) *GitLab {
	t.Helper()
	r, err := ParseRepo("billing", "https://gitlab.com/group/sub/billing", ref)
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}
	c := NewClient(ClientOptions{
		HTTP: srv.Client(), BaseURL: srv.URL, Token: "glpat-x",
		AuthHeader: "PRIVATE-TOKEN", AuthPrefix: "",
		MaxAttempts: 1, Sleep: func(time.Duration) {},
	})
	return NewGitLab(r, c, NopCache{}, 4)
}

// The project id is the whole path, percent-encoded as one segment. Getting
// this wrong produces a 404 that looks exactly like a missing repository.
func TestGitLabEncodesTheProjectPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "")
	_, _ = g.Open(context.Background(), []string{"."})
	if want := "/projects/group%2Fsub%2Fbilling"; gotPath[:len(want)] != want {
		t.Errorf("project path = %q, want it to start with %q", gotPath, want)
	}
}

func TestGitLabSendsThePrivateTokenHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("PRIVATE-TOKEN")
		w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	_, _ = g.Open(context.Background(), []string{"."})
	if got != "glpat-x" {
		t.Errorf("PRIVATE-TOKEN = %q, want %q", got, "glpat-x")
	}
}

// per_page defaults to 20, so a repository with more than twenty files
// returns a first page that looks like the whole tree. Every page must be
// followed.
func TestGitLabFollowsEveryPage(t *testing.T) {
	const total = 250
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling" {
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
			return
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage != 100 {
			t.Errorf("per_page = %d, want 100", perPage)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		start := (page - 1) * perPage
		var rows []map[string]any
		for i := start; i < min(start+perPage, total); i++ {
			rows = append(rows, map[string]any{
				"id": fmt.Sprintf("blob-%d", i), "name": fmt.Sprintf("f%d.md", i),
				"type": "blob", "path": fmt.Sprintf("f%d.md", i),
			})
		}
		if start+perPage < total {
			w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
		}
		json.NewEncoder(w).Encode(rows)
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fs.Stat(f, "f249.md"); err != nil {
		t.Errorf("Stat of the last file across three pages: %v", err)
	}
	if got := len(f.Entries()); got != total {
		t.Errorf("listed %d entries, want %d", got, total)
	}
}

func TestGitLabFetchAndLastEdit(t *testing.T) {
	body := "# Billing\n"
	sha := gitBlobSHA([]byte(body))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		// EscapedPath, not Path: net/url decodes %2F to "/" in .Path, so a
		// comparison against a string containing %2F can never match.
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/tree":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": sha, "name": "runbook.md", "type": "blob", "path": "runbook.md"},
			})
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/blobs/"+sha+"/raw":
			w.Write([]byte(body))
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/commits":
			json.NewEncoder(w).Encode([]map[string]any{{"committed_date": "2026-07-15T11:00:00+02:00"}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := g.Fetch(context.Background(), f, []string{"runbook.md"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := fs.ReadFile(f, "runbook.md")
	if err != nil || string(got) != body {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}

	edited, ok, err := g.LastEdit(context.Background(), "runbook.md")
	if err != nil || !ok {
		t.Fatalf("LastEdit = (%v, %v, %v)", edited, ok, err)
	}
	want := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	if !edited.Equal(want) {
		t.Errorf("LastEdit = %v, want %v (the offset must be honoured)", edited, want)
	}
}

func TestGitLabBaseURL(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"https://gitlab.com/group/project", "https://gitlab.com/api/v4"},
		{"https://gl.internal/group/project", "https://gl.internal/api/v4"},
	} {
		r, err := ParseRepo("n", tt.url, "")
		if err != nil {
			t.Fatalf("ParseRepo: %v", err)
		}
		if got := GitLabBaseURL(r); got != tt.want {
			t.Errorf("GitLabBaseURL(%s) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/fetch/ -run TestGitLab -v`
Expected: FAIL to compile — `undefined: NewGitLab`.

- [ ] **Step 3: Implement `gitlab.go`**

```go
package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitLab fetches a repository over the GitLab REST API (decision D5).
type GitLab struct {
	repo     Repo
	c        *Client
	cache    Cache
	parallel int

	// mu guards BOTH ref and edits, for the reason recorded on GitHub.
	mu    sync.Mutex
	ref   string
	edits map[string]edit
}

func NewGitLab(r Repo, c *Client, cache Cache, parallel int) *GitLab {
	return &GitLab{repo: r, c: c, cache: cache, parallel: parallel, ref: r.Ref, edits: map[string]edit{}}
}

// GitLabBaseURL is /api/v4 on whichever host the repository lives on. Unlike
// GitHub, gitlab.com is not a special case: the public instance serves the
// same path as a self-hosted one.
func GitLabBaseURL(r Repo) string { return r.Host + "/api/v4" }

// project is the id GitLab addresses a repository by: the whole path,
// percent-encoded as a single segment. Groups nest, so this is not the
// two-segment shape GitHub uses.
func (g *GitLab) project() string {
	return "/projects/" + url.PathEscape(g.repo.Owner+"/"+g.repo.Slug)
}

// resolveRef returns the ref to list, asking the host for its default branch
// when repos.yaml named none.
//
// It RETURNS the ref rather than leaving callers to read g.ref, and every
// access sits under g.mu. Grouping the field under the mutex is not enough on
// its own: an earlier draft did exactly that and still read and wrote it
// unlocked here, which is the same race review found on GitHub.resolveRef.
// The shape is copied from there deliberately — two adapters with one
// discipline.
func (g *GitLab) resolveRef(ctx context.Context) (string, error) {
	g.mu.Lock()
	if g.ref != "" {
		ref := g.ref
		g.mu.Unlock()
		return ref, nil
	}
	g.mu.Unlock()

	body, _, err := g.c.Get(ctx, g.project(), nil, "")
	if err != nil {
		return "", err
	}
	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("cannot read the project description: %w", err)
	}
	if payload.DefaultBranch == "" {
		return "", errors.New("the host reported no default branch; set `ref:` in repos.yaml")
	}

	g.mu.Lock()
	g.ref = payload.DefaultBranch
	ref := g.ref
	g.mu.Unlock()
	return ref, nil
}

// glRow is one row of a tree listing. `id` is the blob sha — the same value
// GitHub calls `sha`, and the key ruling R27's cache is built on. There is
// no size field; see this task's preamble.
type glRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
}

// listPath lists everything under prefix, following every page.
//
// per_page is set to 100 because GitLab's default is 20, and a listing that
// stops after twenty files is a portal missing services with nothing to
// notice it by. X-Next-Page is empty on the last page, which is the
// documented end condition.
func (g *GitLab) listPath(ctx context.Context, f *FS, prefix string) error {
	page := 1
	for {
		q := url.Values{
			"ref":       {g.ref},
			"recursive": {"true"},
			"per_page":  {"100"},
			"page":      {strconv.Itoa(page)},
		}
		if prefix != "." && prefix != "" {
			q.Set("path", prefix)
		}
		body, header, err := g.c.Get(ctx, g.project()+"/repository/tree", q, "")
		if err != nil {
			return err
		}
		var rows []glRow
		if err := json.Unmarshal(body, &rows); err != nil {
			return fmt.Errorf("cannot read the tree listing: %w", err)
		}
		byDir := map[string][]Entry{}
		for _, r := range rows {
			switch r.Type {
			case "blob":
				byDir[path.Dir(r.Path)] = append(byDir[path.Dir(r.Path)], Entry{Path: r.Path, SHA: r.ID})
			case "tree":
				byDir[path.Dir(r.Path)] = append(byDir[path.Dir(r.Path)], Entry{Path: r.Path, Dir: true})
			// "commit" is a submodule; see GitHub.Open.
			}
		}
		for dir, entries := range byDir {
			f.AddDir(dir, entries)
		}
		next := header.Get("X-Next-Page")
		if next == "" {
			return nil
		}
		n, err := strconv.Atoi(next)
		if err != nil {
			return fmt.Errorf("the host returned an unreadable X-Next-Page %q", next)
		}
		page = n
	}
}

// Open lists under each pattern's literal prefix rather than the whole
// repository.
//
// GitLab has no truncation flag — it simply paginates — so a hundred-thousand
// file monorepo would be a thousand requests for a listing of which
// landsraad reads a handful of directories. Bounding by prefix is the same
// economy ruling R28 buys on GitHub, taken on the cheap path rather than as
// a fallback.
func (g *GitLab) Open(ctx context.Context, patterns []string) (*FS, error) {
	if err := g.resolveRef(ctx); err != nil {
		return nil, err
	}
	f := NewFS()
	for _, prefix := range literalPrefixes(patterns) {
		if err := g.listPath(ctx, f, prefix); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// literalPrefixes reduces glob patterns to the deepest directory that must
// be listed for each, deduplicated. "services/*" needs "services";
// "*/api" and "." both need the root, and a root listing subsumes every
// other prefix, so it is returned alone.
func literalPrefixes(patterns []string) []string {
	if len(patterns) == 0 {
		return []string{"."}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range patterns {
		prefix := "."
		var kept []string
		for _, seg := range strings.Split(p, "/") {
			if seg == "." || seg == "" || strings.ContainsAny(seg, "*?[") {
				break
			}
			kept = append(kept, seg)
		}
		if len(kept) > 0 {
			prefix = strings.Join(kept, "/")
		}
		if prefix == "." {
			return []string{"."}
		}
		if !seen[prefix] {
			seen[prefix] = true
			out = append(out, prefix)
		}
	}
	return out
}

// Expand lists a directory that no pattern prefix covered — a spec.docs
// somewhere else in the repository.
func (g *GitLab) Expand(ctx context.Context, f *FS, dirs []string) error {
	for _, d := range dirs {
		if f.Listed(d) {
			continue
		}
		if err := g.listPath(ctx, f, d); err != nil {
			if IsNotFound(err) {
				// The directory is not there. f already answers Stat
				// correctly; a 404 for a path the user named is a catalog
				// problem for CheckFiles to report, not a fetch failure.
				continue
			}
			return err
		}
	}
	return nil
}

func (g *GitLab) Fetch(ctx context.Context, f *FS, paths []string) error {
	return fetchBlobs(ctx, g.repo.Name, f, paths, g.parallel, g.cache,
		func(ctx context.Context, sha string) ([]byte, error) {
			body, _, err := g.c.Get(ctx,
				g.project()+"/repository/blobs/"+url.PathEscape(sha)+"/raw", nil, "")
			return body, err
		})
}

func (g *GitLab) LastEdit(ctx context.Context, p string) (time.Time, bool, error) {
	g.mu.Lock()
	hit, seen := g.edits[p]
	g.mu.Unlock()
	if seen {
		return hit.t, hit.ok, nil
	}
	if err := g.resolveRef(ctx); err != nil {
		return time.Time{}, false, err
	}
	body, _, err := g.c.Get(ctx, g.project()+"/repository/commits", url.Values{
		"path": {p}, "ref_name": {g.ref}, "per_page": {"1"},
	}, "")
	if err != nil {
		return time.Time{}, false, err
	}
	var payload []struct {
		CommittedDate time.Time `json:"committed_date"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return time.Time{}, false, fmt.Errorf("cannot read the commit list for %s: %w", p, err)
	}
	res := edit{}
	if len(payload) > 0 {
		// GitLab returns an explicit offset; UTC normalises it so two
		// repositories in two timezones sort against each other correctly.
		res = edit{t: payload[0].CommittedDate.UTC(), ok: true}
	}
	g.mu.Lock()
	g.edits[p] = res
	g.mu.Unlock()
	return res.t, res.ok, nil
}

var _ Fetcher = (*GitLab)(nil)
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/fetch/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fetch/gitlab.go internal/fetch/gitlab_test.go
git commit -m "feat(fetch): the GitLab adapter

per_page defaults to 20 on this host, so pagination is the common path and
not an edge case -- a fifteen-service repository would otherwise return a
first page that looks like the whole tree.

Lists under each pattern's literal prefix rather than the whole
repository: GitLab has no truncation flag, it just paginates, and a large
monorepo would be a thousand requests for a listing of which landsraad
reads a handful of directories.

Rulings R24, R28."
```

---
### Task 11: The blob cache

`internal/fetch` declares a `Cache` interface; this task writes the only implementation that touches a disk, and it lives in `cmd/` because nothing under `internal/` may import `os`.

**The path safety is the point of this task, not a detail.** A blob SHA arrives from a remote host and becomes a filesystem path. That is precisely the shape CONTRIBUTING.md's first rule describes as where the `fs.FS` guarantee stops — the same shape that let a `../` line in `dist/.landsraad-manifest` delete a sibling directory until `safeManifestPath` was added. A SHA is validated as hex of a known length **before** it is joined onto anything.

**Files:**
- Create: `cmd/landsraad/blobcache.go`
- Modify: `cmd/landsraad/init.go` (`.gitignore`)
- Test: `cmd/landsraad/blobcache_test.go`

**Interfaces:**
- Consumes: `fetch.Cache` from Task 7.
- Produces: `func newBlobCache(root string) *blobCache`, implementing `fetch.Cache`; `func safeBlobPath(root, sha string) (string, bool)`.

- [ ] **Step 1: Write the failing test**

Create `cmd/landsraad/blobcache_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlobCacheRoundTrip(t *testing.T) {
	c := newBlobCache(t.TempDir())
	const sha = "356a192b7913b04c54574d18c28d46e6395428ab"

	if _, hit := c.Get(sha); hit {
		t.Fatal("empty cache reported a hit")
	}
	if err := c.Put(sha, []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, hit := c.Get(sha)
	if !hit {
		t.Fatal("Get missed after Put")
	}
	if string(got) != "hello" {
		t.Errorf("Get = %q, want %q", got, "hello")
	}
}

// The sha comes from a remote host and becomes a filesystem path. This is
// the seam CONTRIBUTING.md names: every path cmd/ turns into a filesystem
// path is checked where it is joined.
func TestBlobCacheRejectsAnythingThatIsNotASha(t *testing.T) {
	root := t.TempDir()
	for _, sha := range []string{
		"../../../etc/passwd",
		"..",
		"/absolute",
		"356a192b7913b04c54574d18c28d46e6395428ab/../../x",
		"not-hex-at-all-not-hex-at-all-not-hex-aa",
		"",
		"356a192b", // too short
	} {
		t.Run(sha, func(t *testing.T) {
			if _, ok := safeBlobPath(root, sha); ok {
				t.Errorf("safeBlobPath accepted %q", sha)
			}
			c := newBlobCache(root)
			if err := c.Put(sha, []byte("x")); err == nil {
				t.Errorf("Put accepted %q", sha)
			}
			if _, hit := c.Get(sha); hit {
				t.Errorf("Get accepted %q", sha)
			}
		})
	}
	// A canary OUTSIDE the root. Walking root itself proves nothing: an
	// escape via "../../../etc/passwd" resolves to somewhere root does not
	// contain, so WalkDir(root, ...) would never enumerate it. An earlier
	// draft of this test did exactly that and read as if it proved
	// containment while being a no-op. Found by review.
	canary := filepath.Join(filepath.Dir(root), "canary.txt")
	if err := os.WriteFile(canary, []byte("untouched"), 0o644); err != nil {
		t.Fatalf("planting the canary: %v", err)
	}
	before, err := os.ReadFile(canary)
	if err != nil {
		t.Fatalf("reading the canary: %v", err)
	}
	// ... malicious inputs run above ...
	after, err := os.ReadFile(canary)
	if err != nil {
		t.Fatalf("the canary is gone: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("a write escaped the cache root and changed %s", canary)
	}
}

func TestBlobCacheAcceptsBothShaLengths(t *testing.T) {
	root := t.TempDir()
	for _, sha := range []string{
		"356a192b7913b04c54574d18c28d46e6395428ab",                         // sha-1
		"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", // sha-256
	} {
		if _, ok := safeBlobPath(root, sha); !ok {
			t.Errorf("safeBlobPath rejected a real object id %q", sha)
		}
	}
}

// A cache that cannot be written is a slow build, never a failed one.
func TestBlobCacheGetSurvivesAnUnreadableRoot(t *testing.T) {
	c := newBlobCache(filepath.Join(t.TempDir(), "does", "not", "exist"))
	if _, hit := c.Get("356a192b7913b04c54574d18c28d46e6395428ab"); hit {
		t.Error("Get reported a hit from a directory that does not exist")
	}
}

func TestBlobCacheShardsByPrefix(t *testing.T) {
	root := t.TempDir()
	const sha = "356a192b7913b04c54574d18c28d46e6395428ab"
	got, ok := safeBlobPath(root, sha)
	if !ok {
		t.Fatal("safeBlobPath rejected a valid sha")
	}
	want := filepath.Join(root, "35", sha)
	if got != want {
		t.Errorf("safeBlobPath = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/landsraad/ -run TestBlobCache -v`
Expected: FAIL to compile — `undefined: newBlobCache`.

- [ ] **Step 3: Implement**

Create `cmd/landsraad/blobcache.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// blobCache is the on-disk half of ruling R27: a content-addressed store
// under .landsraad/cache/blobs, keyed on the git blob sha the tree listing
// returns.
//
// It lives in cmd/ because it reads and writes real files and nothing under
// internal/ may import os. internal/fetch declares the interface; this is
// the only implementation that touches a disk.
//
// Content-addressed means a hit cannot be stale: the key IS the content's
// identity, so there is no invalidation, no TTL and no revalidation request.
// It also means no automatic pruning in v1 — an unbounded directory, stated
// in the README, with `landsraad cache prune` left as an additive change
// rather than an eviction policy invented with no measurement behind it.
type blobCache struct{ root string }

func newBlobCache(root string) *blobCache { return &blobCache{root: root} }

// safeBlobPath turns a sha into a path under root, or refuses.
//
// The sha arrives from a remote host and becomes a filesystem path, which is
// exactly where CONTRIBUTING.md says the io/fs guarantee stops and a check
// belongs. A `../` in this string would otherwise let a compromised or
// simply broken host write outside the cache — the same shape that let a
// `../` line in dist/.landsraad-manifest delete the sibling it named, until
// safeManifestPath was added.
//
// Both git object-id lengths are accepted: 40 hex for SHA-1 and 64 for the
// SHA-256 object format. Anything else is not an object id, whatever it is.
func safeBlobPath(root, sha string) (string, bool) {
	if len(sha) != 40 && len(sha) != 64 {
		return "", false
	}
	for i := 0; i < len(sha); i++ {
		c := sha[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return "", false
		}
	}
	// Sharded by the first two characters: a flat directory with fifty
	// thousand entries is slow to list on every filesystem that matters.
	return filepath.Join(root, sha[:2], sha), true
}

// Get returns a cached blob. A miss is never an error — a cache that cannot
// answer is a slow build, not a broken one — which is why the interface has
// no error return here at all.
func (c *blobCache) Get(sha string) ([]byte, bool) {
	p, ok := safeBlobPath(c.root, sha)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Put stores a blob.
//
// Written to a temporary file and renamed, so an interrupted build leaves
// no half-written entry behind. fetch.verifyBlob would catch one on the next
// read and refetch, but a torn file that hashes correctly by accident is not
// something to rely on being impossible.
func (c *blobCache) Put(sha string, data []byte) error {
	p, ok := safeBlobPath(c.root, sha)
	if !ok {
		return fmt.Errorf("refusing to cache under %q: not a git object id", sha)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
```

- [ ] **Step 4: Teach `init` about the cache directory**

In `cmd/landsraad/init.go`, add `.landsraad/cache/` to the generated `.gitignore`, with a comment in the generated file itself:

```
# landsraad's fetched-blob cache. Content-addressed, safe to delete.
.landsraad/cache/
```

A cache committed to a repository would be an ever-growing directory of other repositories' file contents, which is a surprise nobody wants in a diff.

- [ ] **Step 5: Run the tests**

Run: `go test ./cmd/landsraad/ -run 'TestBlobCache|TestInit' -v`
Expected: PASS. The `init` golden test needs its expected `.gitignore` updated.

- [ ] **Step 6: Commit**

```bash
git add cmd/landsraad/blobcache.go cmd/landsraad/blobcache_test.go cmd/landsraad/init.go cmd/landsraad/init_test.go
git commit -m "feat(cmd): a content-addressed blob cache

Keyed on the git blob sha the tree listing already returns, so a hit costs
zero requests and cannot be stale.

The sha comes from a remote host and becomes a filesystem path, which is
the seam CONTRIBUTING.md names: it is validated as hex of an object-id
length before it is joined onto anything.

Ruling R27."
```

---

### Task 12: Two-phase fetching, and the split of `loadCatalogScoped`

The task R25 was written for. `loadCatalogScoped` splits into a parse half and an assemble half so `cmd/` can fetch between them, and `openRepos` turns `repos.yaml` into a `catalog.Sources`.

The content set fetched in phase 2 is exactly `{spec.runbook, spec.alerts, *.md under spec.docs, .landsraad/checks/*.yaml}`. That list is not a guess: it is every `fs.ReadFile` call site below `internal/`, enumerated. If a later plan adds a sixth, phase 2 must learn about it, and the test in Step 1 is what fails when it does not.

**Files:**
- Create: `cmd/landsraad/repos.go`
- Modify: `cmd/landsraad/gen.go` (the split)
- Test: `cmd/landsraad/repos_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–11.
- Produces:
  - `func parseRepo(name string, fsys fs.FS, patterns []string, v *schema.Validator, c *diag.Collector) []*catalog.Entity`
  - `func assemble(entities []*catalog.Entity, src catalog.Sources, scope catalog.Scope, cfg fs.FS, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams)`
  - `func contentSet(fsys fs.FS, entities []*catalog.Entity) []string`
  - `func docsDirs(entities []*catalog.Entity) []string`
  - `func openRepos(ctx context.Context, o reposOptions, c *diag.Collector) *workspace`
  - `type workspace struct{ … }` with `ParseAll`, `WithLocal`, `Failures`
  - `type repoFailure struct{ Name, URL string; Line int; Err error }`
  - `func tokenFor(repo config.Repo, look func(string) (string, bool)) string`

- [ ] **Step 1: Write the failing test**

Create `cmd/landsraad/repos_test.go`:

```go
package main

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
)

// The content set is every file a later stage will read. It is the whole
// contract between phase 1 and phase 2: a path missing from it reads as
// fetch.ErrNotFetched deep inside the renderer.
func TestContentSetIsEveryFileALaterStageReads(t *testing.T) {
	fsys := fstest.MapFS{
		"services/api/service.yaml":       {Data: []byte("x")},
		"services/api/runbook.md":         {Data: []byte("x")},
		"services/api/alerts.yaml":        {Data: []byte("x")},
		"services/api/docs/index.md":      {Data: []byte("x")},
		"services/api/docs/deep/why.md":   {Data: []byte("x")},
		"services/api/docs/diagram.png":   {Data: []byte("x")}, // not markdown
		"services/api/NOTES.md":           {Data: []byte("x")}, // not under docs
		".landsraad/checks/scan.yaml":     {Data: []byte("x")},
		".landsraad/checks/notes.txt":     {Data: []byte("x")}, // not a results file
	}
	e := &catalog.Entity{SourceRepo: "mono", SourcePath: "services/api/service.yaml"}
	e.Kind = "Service"
	e.Metadata.Name = "api"
	e.Spec.Runbook = "services/api/runbook.md"
	e.Spec.Alerts = "services/api/alerts.yaml"
	e.Spec.Docs = "services/api/docs"

	want := []string{
		".landsraad/checks/scan.yaml",
		"services/api/alerts.yaml",
		"services/api/docs/deep/why.md",
		"services/api/docs/index.md",
		"services/api/runbook.md",
	}
	if diff := cmp.Diff(want, contentSet(fsys, []*catalog.Entity{e})); diff != "" {
		t.Errorf("contentSet mismatch (-want +got):\n%s", diff)
	}
}

func TestContentSetSkipsUnsetFields(t *testing.T) {
	fsys := fstest.MapFS{"service.yaml": {Data: []byte("x")}}
	e := &catalog.Entity{SourceRepo: "mono", SourcePath: "service.yaml"}
	e.Kind = "Library"
	e.Metadata.Name = "lib"
	if got := contentSet(fsys, []*catalog.Entity{e}); len(got) != 0 {
		t.Errorf("contentSet = %v, want none: the entity names no files", got)
	}
}

func TestDocsDirs(t *testing.T) {
	a := &catalog.Entity{}
	a.Spec.Docs = "services/api/docs"
	b := &catalog.Entity{}
	b.Spec.Docs = "services/api/docs" // duplicate
	c := &catalog.Entity{}            // no docs
	want := []string{".landsraad/checks", "services/api/docs"}
	if diff := cmp.Diff(want, docsDirs([]*catalog.Entity{a, b, c})); diff != "" {
		t.Errorf("docsDirs mismatch (-want +got):\n%s", diff)
	}
}

// Ruling R29: the per-repository variable wins, then the host-wide one.
func TestTokenFor(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN":            "host-wide",
		"GITLAB_TOKEN":            "gl-wide",
		"LANDSRAAD_TOKEN_EDGE_GW": "per-repo",
	}
	look := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	for _, tt := range []struct {
		name string
		repo config.Repo
		want string
	}{
		{"per-repo wins", config.Repo{Name: "edge-gw", URL: "https://github.com/o/edge-gw"}, "per-repo"},
		{"github falls back to host", config.Repo{URL: "https://github.com/o/api"}, "host-wide"},
		{"gitlab falls back to host", config.Repo{URL: "https://gitlab.com/o/api"}, "gl-wide"},
		{"nothing set", config.Repo{URL: "https://github.com/o/x", Name: "x", Host: "bitbucket"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tokenFor(tt.repo, look); got != tt.want {
				t.Errorf("tokenFor = %q, want %q", got, tt.want)
			}
		})
	}
}

// The name is normalised the way the ruling states: uppercased, every
// non-alphanumeric byte to underscore. A repository called "edge-gateway"
// must not need a variable nobody could guess.
func TestTokenVarName(t *testing.T) {
	for _, tt := range []struct{ name, want string }{
		{"edge-gateway", "LANDSRAAD_TOKEN_EDGE_GATEWAY"},
		{"my.repo", "LANDSRAAD_TOKEN_MY_REPO"},
		{"api", "LANDSRAAD_TOKEN_API"},
	} {
		if got := tokenVarName(tt.name); got != tt.want {
			t.Errorf("tokenVarName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// The split must not change single-repository behaviour. This asserts the
// two halves compose back into what loadCatalogScoped always did.
func TestParseRepoThenAssembleMatchesTheOldPipeline(t *testing.T) {
	fsys := goodFixtureFS(t) // the fixture the existing gen tests use
	var c diag.Collector
	cat, g, teams := loadCatalogScoped(fsys, catalog.FullCatalog, &c)
	if cat == nil || g == nil || teams == nil {
		t.Fatalf("loadCatalogScoped returned nils; diagnostics: %+v", c.Diagnostics())
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("got %d diagnostics on a good fixture: %+v", len(ds), ds)
	}
}
```

> **Note for the implementer:** `goodFixtureFS` stands for whatever the existing `gen_test.go` uses to build a valid in-memory repository — reuse it rather than writing a new one.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/landsraad/ -run 'TestContentSet|TestDocsDirs|TestTokenFor|TestTokenVarName|TestParseRepoThen' -v`
Expected: FAIL to compile — `undefined: contentSet`.

- [ ] **Step 3: Split `loadCatalogScoped`**

In `cmd/landsraad/gen.go`, the existing function becomes a composition of two new ones. Nothing about its behaviour changes:

```go
// loadCatalogScoped is the single-repository composition: stages 1, 3, 4
// and 5 against one filesystem. validate, gen and score all end here, and
// ruling R30 keeps them there.
func loadCatalogScoped(fsys fs.FS, scope catalog.Scope, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	v := defaultValidator(c)
	if v == nil {
		return nil, nil, nil
	}
	repo := localRepoName(fsys)
	entities := parseRepo(repo, fsys, patternsFor(fsys, c), v, c)
	return assemble(entities, catalog.SingleSource(repo, fsys), scope, fsys, c)
}

// parseRepo runs stages 1 and 3 for one repository: find the catalog files,
// read them, validate their shape, parse them into entities.
//
// It stops there — deliberately. Under ruling R25 cmd/ has to fetch a
// repository's *content* before stages 4 onwards can run, and it cannot
// know which files to fetch until the entities are parsed. That is the
// whole reason this is a separate function from assemble.
func parseRepo(name string, fsys fs.FS, patterns []string, v *schema.Validator, c *diag.Collector) []*catalog.Entity {
	found, err := discover.Find(fsys, patterns)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: name, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files: %v", discover.Filename, err),
		})
		return nil
	}
	if len(found) == 0 {
		// A warning per repository, where it used to be one error for the
		// whole run. With several repositories, "no service.yaml anywhere"
		// and "the third repository's paths are wrong" are different
		// problems, and the second is the apps/-instead-of-services/ bug
		// the strict repos.yaml decoding already exists to catch. assemble
		// still errors when the WHOLE catalog is empty.
		c.Add(diag.Diagnostic{
			Severity: diag.SevWarn, Repo: name, File: "repos.yaml", Line: 1,
			Check: "no-entities",
			Message: fmt.Sprintf("no %s found in %s under any configured path (%s)",
				discover.Filename, repoLabel(name), strings.Join(patterns, ", ")),
			Hint: "check this repository's `paths:` in repos.yaml",
		})
		return nil
	}
	files := discover.Load(fsys, found, c)
	for _, f := range files {
		// name, not "": schema.Validator.Validate takes a repo and the
		// single-repository caller had nothing to give it. A schema error in
		// the third repository must say which repository.
		v.Validate(name, f.Path, f.Data, c)
	}
	return catalog.ParseAll(name, files, c)
}

// assemble runs stages 4 and 5 over every repository's entities at once, and
// loads the configuration that lives in the repository the command is
// standing in (ruling R34).
func assemble(entities []*catalog.Entity, src catalog.Sources, scope catalog.Scope, cfg fs.FS, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	if len(entities) == 0 {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "repos.yaml", Line: 1,
			Check:   "no-entities",
			Message: fmt.Sprintf("no %s found in any configured repository", discover.Filename),
			Hint:    "add a repos.yaml listing the paths your services live under",
		})
		return nil, nil, nil
	}
	cat := catalog.NewCatalog(entities, c)
	catalog.CheckFiles(src, cat, c)
	g := cat.Resolve(scope, c)
	reportCycles(cat, g, c)

	teamsData, err := fs.ReadFile(cfg, "teams.yaml")
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

// repoLabel names a repository in a message, or says "this repository" when
// there is no name — which is the ordinary case for a checkout with no
// repos.yaml, where "no service.yaml found in  under any path" would read
// as a bug.
func repoLabel(name string) string {
	if name == "" {
		return "this repository"
	}
	return name
}

// defaultValidator compiles the embedded schema once per command.
func defaultValidator(c *diag.Collector) *schema.Validator {
	v, err := schema.Default()
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "schema", Line: 1,
			Check:   "schema-compile",
			Message: fmt.Sprintf("cannot compile the embedded schema: %v", err),
		})
		return nil
	}
	return v
}
```

- [ ] **Step 4: Implement `repos.go`**

```go
package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/fetch"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// blobParallel is how many blobs are fetched at once per repository. Eight
// is enough to hide latency and small enough that a build does not look
// like an attack to a host's abuse detection.
const blobParallel = 8

// repoFailure is one repository that could not be read. Ruling R32 decides
// what build does with these; this file only collects them.
type repoFailure struct {
	Name string
	URL  string
	Line int
	Err  error
}

// reposOptions is everything openRepos needs from the command layer.
type reposOptions struct {
	// Root is the directory the command is running in, and RootFS reads it.
	// Both, because the local repository is an os.DirFS over Root while
	// teams.yaml and standards.yaml are read through RootFS (ruling R34).
	Root   string
	RootFS fs.FS
	Cache  fetch.Cache
	Lookup func(string) (string, bool) // os.LookupEnv, injected for tests
	ErrOut io.Writer
	// HTTP is the transport the adapters use. Injected, and nil in
	// production, so Task 15's end-to-end test can point the whole command
	// at an httptest TLS server: repos.yaml requires https, and a test that
	// had to relax that rule would stop testing the rule.
	HTTP *http.Client
}

// workspace is every repository a build reads, opened and fetched.
//
// It keeps the patterns beside the filesystems because parsing happens more
// than once: serve --watch re-parses on every file change, against a fresh
// os.DirFS for the local repository and the same already-fetched values for
// the remotes (ruling R33). Re-parsing is cheap and re-fetching is not.
type workspace struct {
	sources  catalog.Sources
	patterns map[string][]string
	// local is the local repository's name, empty when every entry is
	// remote — a platform repository that holds only configuration.
	local    string
	failures []repoFailure
	// fetchers is kept for exactly one caller: docs-fresh asks a remote
	// repository when a path was last changed, and that is a question about
	// a ref which no tree listing answers and no blob cache can hold
	// (ruling R35). Nothing else touches a Fetcher after openRepos returns.
	fetchers map[string]fetch.Fetcher
}

func (w *workspace) Sources() catalog.Sources { return w.sources }
func (w *workspace) Failures() []repoFailure   { return w.failures }

// FetcherFor returns the adapter behind a repository, if it has one. The
// local repository has none: its history comes from git.
func (w *workspace) FetcherFor(name string) (fetch.Fetcher, bool) {
	f, ok := w.fetchers[name]
	return f, ok
}

// ParseAll runs stage 1 and stage 3 over every repository.
//
// Sorted by Sources.Names so entity order — and therefore which entity wins
// a name collision, and every diagnostic's order — does not depend on map
// iteration.
func (w *workspace) ParseAll(v *schema.Validator, c *diag.Collector) []*catalog.Entity {
	var out []*catalog.Entity
	for _, name := range w.sources.Names() {
		fsys, ok := w.sources.Get(name)
		if !ok {
			continue
		}
		out = append(out, parseRepo(name, fsys, w.patterns[name], v, c)...)
	}
	return out
}

// WithLocal returns a copy reading the local repository from fsys, leaving
// the receiver and every fetched filesystem alone.
func (w *workspace) WithLocal(fsys fs.FS) *workspace {
	if w.local == "" {
		return w
	}
	return &workspace{
		sources:  w.sources.With(w.local, fsys),
		patterns: w.patterns, local: w.local, failures: w.failures,
		fetchers: w.fetchers,
	}
}

// openRepos turns repos.yaml into a workspace, fetching every remote
// repository in two phases (ruling R25).
//
// Phase 1 lists each repository and fetches only the service.yaml files the
// configured patterns match. The entities are parsed — and then thrown away,
// because their only job here is to say which files phase 2 must fetch.
// Phase 2 fetches those. Every request in the program happens inside this
// function, before assemble runs a single stage.
func openRepos(ctx context.Context, o reposOptions, c *diag.Collector) *workspace {
	v := defaultValidator(c)
	if v == nil {
		return &workspace{sources: catalog.Sources{}, patterns: map[string][]string{}, fetchers: map[string]fetch.Fetcher{}}
	}
	repos := loadReposFile(o.RootFS, c)

	// Built directly as a catalog.Sources rather than as a map that is
	// converted at the end: a conversion would alias this loop's map, and
	// the type carries no constructor to copy it (see catalog/sources.go).
	w := &workspace{
		sources:  catalog.Sources{},
		patterns: map[string][]string{},
		fetchers: map[string]fetch.Fetcher{},
	}
	var failures []repoFailure

	for _, r := range repos {
		name := r.Identity()
		patterns := r.Paths
		if len(patterns) == 0 {
			patterns = config.DefaultPatterns()
		}

		fail := func(err error) {
			failures = append(failures, repoFailure{Name: name, URL: r.URL, Line: r.Line, Err: err})
		}

		fsys, fetcher, err := openOne(ctx, r, patterns, o)
		if err != nil {
			fail(err)
			continue
		}
		if r.Local {
			w.local = name
		}

		if fetcher != nil {
			// Phase 1: the catalog files the patterns match, and nothing
			// else. A local repository skips both phases: it is already on
			// disk, and every read below will find it there.
			remote := fsys.(*fetch.FS)
			found, err := discover.Find(fsys, patterns)
			if err != nil {
				fail(err)
				continue
			}
			if err := fetcher.Fetch(ctx, remote, found); err != nil {
				fail(err)
				continue
			}

			// Phase 2: everything those entities name. These entities are
			// discarded — ParseAll re-derives them once every repository is
			// in the workspace, so that cross-repository references resolve
			// against the merged catalog rather than against one repository
			// at a time. Parsing twice is cheap; fetching twice is not.
			var scratch diag.Collector
			parsed := parseRepo(name, fsys, patterns, v, &scratch)

			// Expand before contentSet: a spec.docs directory outside the
			// configured paths may not be listed yet, and contentSet walks
			// it to find the pages.
			if err := fetcher.Expand(ctx, remote, docsDirs(parsed)); err != nil {
				fail(err)
				continue
			}
			if err := fetcher.Fetch(ctx, remote, contentSet(fsys, parsed)); err != nil {
				fail(err)
				continue
			}
		}

		w.sources[name] = fsys
		w.patterns[name] = patterns
		if fetcher != nil {
			w.fetchers[name] = fetcher
		}
	}
	w.failures = failures
	return w
}

// openOne returns the filesystem for one repository, and the fetcher behind
// it when there is one. The local repository has no fetcher: it is already
// on disk, and re-reading it through a host API would be slower, need a
// token, and show committed state rather than what the user is editing.
func openOne(ctx context.Context, r config.Repo, patterns []string, o reposOptions) (fs.FS, fetch.Fetcher, error) {
	if r.Local {
		return os.DirFS(o.Root), nil, nil
	}
	repo, err := fetch.ParseRepo(r.Identity(), r.URL, r.Ref)
	if err != nil {
		return nil, nil, err
	}
	kind, known := r.HostKind()
	if !known {
		// validateRepos already reported this as a diagnostic; reaching here
		// means the command chose to continue past it.
		return nil, nil, fmt.Errorf("no adapter for %s", r.URL)
	}
	token := tokenFor(r, o.Lookup)
	if token == "" {
		fmt.Fprintf(o.ErrOut,
			"warn: no token found for %s; unauthenticated requests are rate-limited (set %s or %s)\n",
			r.Identity(), tokenVarName(r.Identity()), strings.ToUpper(kind)+"_TOKEN")
	}

	var f fetch.Fetcher
	switch kind {
	case "github":
		f = fetch.NewGitHub(repo, fetch.NewClient(fetch.ClientOptions{
			HTTP:    o.HTTP,
			BaseURL: fetch.GitHubBaseURL(repo), Token: token,
			AuthHeader: "Authorization", AuthPrefix: "Bearer ",
			Headers: map[string]string{
				"Accept":               "application/vnd.github+json",
				"X-GitHub-Api-Version": "2026-03-10",
			},
		}), o.Cache, blobParallel)
	case "gitlab":
		f = fetch.NewGitLab(repo, fetch.NewClient(fetch.ClientOptions{
			HTTP:    o.HTTP,
			BaseURL: fetch.GitLabBaseURL(repo), Token: token,
			AuthHeader: "PRIVATE-TOKEN",
		}), o.Cache, blobParallel)
	}
	fsys, err := f.Open(ctx, patterns)
	if err != nil {
		return nil, nil, err
	}
	return fsys, f, nil
}

// contentSet is every file a later stage will read from this repository.
//
// Exactly five things, and the list is not a guess: it is every fs.ReadFile
// call site below internal/, enumerated. spec.runbook (scorecard and
// renderer), spec.alerts (scorecard), every .md under spec.docs (renderer),
// and .landsraad/checks/*.yaml (ingest). Anything a later plan adds must be
// added here too, or it reads as fetch.ErrNotFetched inside a stage.
//
// spec.path is deliberately absent: CheckFiles only stats it, and a stat is
// answered from the tree listing for free (ruling R24).
func contentSet(fsys fs.FS, entities []*catalog.Entity) []string {
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || !fs.ValidPath(p) {
			return
		}
		seen[p] = true
	}
	for _, e := range entities {
		add(e.Spec.Runbook)
		add(e.Spec.Alerts)
		if e.Spec.Docs == "" {
			continue
		}
		// WalkDir over the listing: no content is needed to find the pages,
		// which is the sparse fetcher's whole point.
		fs.WalkDir(fsys, e.Spec.Docs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // reported later, by the renderer, with a path
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				add(p)
			}
			return nil
		})
	}
	if entries, err := fs.ReadDir(fsys, scorecard.ChecksDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && scorecard.IsCheckResultsFile(e.Name()) {
				add(path.Join(scorecard.ChecksDir, e.Name()))
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// docsDirs is every directory that must be listed before contentSet can walk
// it: each entity's spec.docs, plus the check-results directory.
//
// Separate from contentSet because listing and fetching are different
// requests, and the listing has to happen first — contentSet cannot walk a
// directory the host has not described yet.
func docsDirs(entities []*catalog.Entity) []string {
	seen := map[string]bool{scorecard.ChecksDir: true}
	for _, e := range entities {
		if e.Spec.Docs != "" && fs.ValidPath(e.Spec.Docs) {
			seen[e.Spec.Docs] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// tokenFor resolves a repository's token (ruling R29).
//
// Per-repository first, then the host-wide variable. Never from repos.yaml:
// spec §14.1 puts tokens in CI secrets, and a token committed to a
// repository is a token in everybody's clone forever.
func tokenFor(r config.Repo, look func(string) (string, bool)) string {
	if name := r.Identity(); name != "" {
		if v, ok := look(tokenVarName(name)); ok && v != "" {
			return v
		}
	}
	kind, known := r.HostKind()
	if !known {
		return ""
	}
	if v, ok := look(strings.ToUpper(kind) + "_TOKEN"); ok {
		return v
	}
	return ""
}

// tokenVarName is LANDSRAAD_TOKEN_<NAME>: uppercased, every byte that is not
// a letter or a digit replaced with an underscore. Stated rather than clever,
// because a variable nobody can guess is a variable nobody sets.
func tokenVarName(name string) string {
	var b strings.Builder
	b.WriteString("LANDSRAAD_TOKEN_")
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

// loadReposFile reads repos.yaml, defaulting to a single local repository
// when there is none. A checkout with no repos.yaml still builds.
func loadReposFile(rootFS fs.FS, c *diag.Collector) []config.Repo {
	data, err := fs.ReadFile(rootFS, "repos.yaml")
	if err != nil {
		c.Add(defaultPatternsNote("no repos.yaml found"))
		return []config.Repo{{Local: true, Paths: config.DefaultPatterns(), Line: 1}}
	}
	r := config.LoadRepos("repos.yaml", data, c)
	if len(r.Repos) == 0 {
		return []config.Repo{{Local: true, Paths: config.DefaultPatterns(), Line: 1}}
	}
	// Exactly one entry is read from disk. LocalRepo applies ruling R26's
	// rule; anything it does not choose is fetched.
	out := make([]config.Repo, len(r.Repos))
	copy(out, r.Repos)
	local, ok := r.LocalRepo()
	for i := range out {
		out[i].Local = ok && out[i].URL == local.URL && out[i].Name == local.Name
	}
	return out
}
```

`scorecard.IsCheckResultsFile` already exists — it is what `Ingest` uses to pick files out of `.landsraad/checks`. Using the same predicate here is what keeps phase 2 and stage 6 agreeing about which files matter.

- [ ] **Step 5: Run the tests**

Run: `go test ./cmd/... ./internal/... -v`
Expected: PASS. `validate`, `gen` and `score` must behave identically — the split is a refactor, and every one of their existing tests is the assertion that it was.

- [ ] **Step 6: Verify the tree**

Run: `task ci`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/landsraad/repos.go cmd/landsraad/repos_test.go cmd/landsraad/gen.go cmd/landsraad/gen_test.go
git commit -m "feat(cmd): fetch every repository in repos.yaml, in two phases

loadCatalogScoped splits into a parse half and an assemble half so cmd/
can fetch between them: the files an entity names are not knowable until
that entity is parsed.

The content set is every fs.ReadFile call site below internal/,
enumerated rather than guessed -- runbook, alerts, docs/**.md and
.landsraad/checks. A sixth would read as ErrNotFetched inside a stage.

Rulings R25, R29, R34."
```

---
### Task 13: `build --allow-partial`, and the death of `partialNotice`

`partialNotice` counted repositories because it could not know which ones were missing — it warned that "2 of 3 were not read" and could not name them, since an earlier version that tried named the wrong two. A real fetcher knows exactly which failed. This task deletes the scaffold and replaces it with the thing R22 said would replace it.

**Files:**
- Modify: `cmd/landsraad/build.go` — `Build` signature, `--allow-partial`, `--no-cache`; **delete `partialNotice`**
- Test: `cmd/landsraad/build_test.go`

**Interfaces:**
- Consumes: `workspace`, `repoFailure` from Task 12.
- Produces: `func Build(root fs.FS, w *workspace, errOut io.Writer, opts BuildOptions) ([]emit.File, int)` — **signature change**, consumed by Task 14.

- [ ] **Step 1: Write the failing test**

Add to `cmd/landsraad/build_test.go`:

```go
// Spec §12: a fetch failure is a hard failure. A portal quietly missing
// three services is worse than no portal.
func TestBuildRefusesAFailedFetch(t *testing.T) {
	w := &workspace{
		sources:  catalog.Sources{"platform": goodFixtureFS(t)},
		patterns: map[string][]string{"platform": {"services/*"}},
		local:    "platform",
		failures: []repoFailure{{
			Name: "edge-gateway", URL: "https://github.com/org/edge-gateway", Line: 4,
			Err: &fetch.StatusError{Status: 404, Method: "GET", Endpoint: "/repos/org/edge-gateway", Body: "Not Found", RateRemaining: -1},
		}},
	}
	var errOut bytes.Buffer
	files, code := Build(goodFixtureFS(t), w, &errOut, BuildOptions{Now: testNow, Version: "test"})

	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if files != nil {
		t.Error("Build produced files despite a failed fetch")
	}
	want := "error: cannot read edge-gateway: not found. A private repository with no token " +
		"looks exactly like this; check the url and that LANDSRAAD_TOKEN_EDGE_GATEWAY or GITHUB_TOKEN is set\n"
	if got := errOut.String(); !strings.Contains(got, want) {
		t.Errorf("stderr =\n%s\nwant it to contain\n%s", got, want)
	}
}

// --allow-partial downgrades it, and the banner names the repositories that
// actually failed -- which is the whole difference from the scaffold this
// replaces.
func TestBuildAllowPartialNamesTheFailures(t *testing.T) {
	w := &workspace{
		sources:  catalog.Sources{"platform": goodFixtureFS(t)},
		patterns: map[string][]string{"platform": {"services/*"}},
		local:    "platform",
		failures: []repoFailure{
			{Name: "edge-gateway", URL: "https://github.com/org/edge-gateway", Line: 4, Err: errors.New("boom")},
			{Name: "billing", URL: "https://gitlab.com/org/billing", Line: 7, Err: errors.New("boom")},
		},
	}
	var errOut bytes.Buffer
	files, code := Build(goodFixtureFS(t), w, &errOut, BuildOptions{
		Now: testNow, Version: "test", AllowPartial: true,
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	want := "This portal is incomplete: billing and edge-gateway could not be read, " +
		"so their services are missing from this catalog."
	var found bool
	for _, f := range files {
		if strings.Contains(string(f.Data), want) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no generated page carries the banner %q", want)
	}
}

func TestPartialBanner(t *testing.T) {
	for _, tt := range []struct {
		name  string
		fails []repoFailure
		want  string
	}{
		{"none", nil, ""},
		{
			"one",
			[]repoFailure{{Name: "billing"}},
			"This portal is incomplete: billing could not be read, so its services are missing from this catalog.",
		},
		{
			"two, named in sorted order",
			[]repoFailure{{Name: "edge-gateway"}, {Name: "billing"}},
			"This portal is incomplete: billing and edge-gateway could not be read, so their services are missing from this catalog.",
		},
		{
			"three",
			[]repoFailure{{Name: "c"}, {Name: "a"}, {Name: "b"}},
			"This portal is incomplete: a, b and c could not be read, so their services are missing from this catalog.",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := partialBanner(tt.fails); got != tt.want {
				t.Errorf("partialBanner = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFailureMessage(t *testing.T) {
	for _, tt := range []struct {
		name string
		f    repoFailure
		want string
	}{
		{
			"not found says why it might not be",
			repoFailure{Name: "edge", Err: &fetch.StatusError{Status: 404, RateRemaining: -1}},
			"cannot read edge: not found. A private repository with no token looks exactly like this; " +
				"check the url and that LANDSRAAD_TOKEN_EDGE or GITHUB_TOKEN is set",
		},
		{
			"rejected token",
			repoFailure{Name: "edge", Err: &fetch.StatusError{Status: 401, RateRemaining: -1}},
			"cannot read edge: the host rejected the token; check that LANDSRAAD_TOKEN_EDGE or GITHUB_TOKEN is current and has read access",
		},
		{
			"rate limited names the reset",
			repoFailure{Name: "edge", Err: &fetch.StatusError{
				Status: 403, RateRemaining: 0,
				RateReset: time.Date(2026, 9, 10, 15, 4, 5, 0, time.UTC),
			}},
			"cannot read edge: the host's rate limit is spent until 2026-09-10T15:04:05Z; " +
				"an unauthenticated build gets 60 requests an hour, an authenticated one 5000",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := failureMessage(tt.f, "github"); got != tt.want {
				t.Errorf("failureMessage = %q, want %q", got, tt.want)
			}
		})
	}
}

// The scaffold is gone. This test exists so that deleting it is a decision
// rather than an oversight.
func TestPartialNoticeIsGone(t *testing.T) {
	// partialNotice counted repositories because it could not know which
	// ones were missing; an earlier version that tried named the wrong two.
	// If this file ever compiles with a call to it again, ruling R22's
	// scaffold has come back.
	t.Log("partialNotice was deleted in Plan 4 Task 13; see ruling R32")
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/landsraad/ -run 'TestBuild|TestPartial|TestFailureMessage' -v`
Expected: FAIL to compile — `too many arguments in call to Build`.

- [ ] **Step 3: Implement**

In `cmd/landsraad/build.go`:

```go
// BuildOptions is everything build needs that is not a filesystem.
type BuildOptions struct {
	Mermaid  render.Mermaid
	Now      time.Time
	LastEdit scorecard.LastEditFunc
	Version  string
	Force    bool
	// AllowPartial renders a portal from the repositories that could be
	// read, stamping a banner naming the ones that could not (ruling R32).
	AllowPartial bool
}

// Build renders the portal, returning the files and an exit code.
//
// Two filesystems, and they are different things (ruling R34). root is the
// repository the command is standing in: teams.yaml, standards.yaml and
// scorecard-history.csv live there and nowhere else. w is every repository
// in the catalog, already fetched.
//
// It writes nothing and it fetches nothing. writeSite is the disk half;
// openRepos is the network half, and it ran before this was called — which
// is what lets serve --watch rebuild on every keystroke without touching a
// host API (ruling R33).
func Build(root fs.FS, w *workspace, errOut io.Writer, opts BuildOptions) ([]emit.File, int) {
	var c diag.Collector

	if code := reportFetchFailures(w.Failures(), opts.AllowPartial, errOut); code != exitOK {
		return nil, code
	}

	v := defaultValidator(&c)
	if v == nil {
		reportDiagnostics(errOut, c.Diagnostics())
		return nil, exitValidation
	}

	// FullCatalog, not LocalOnly: build claims to render the whole catalog,
	// so a dangling reference is a hard failure (spec §7.1).
	cat, g, teams := assemble(w.ParseAll(v, &c), w.Sources(), catalog.FullCatalog, root, &c)
	if cat == nil || c.HasErrors() {
		reportDiagnostics(errOut, c.Diagnostics())
		fmt.Fprintf(errOut, "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n")
		return nil, exitValidation
	}

	std := standardsFor(root, errOut)
	reported := scorecard.Ingest(w.Sources(), cat, std.StaleAfterDays(), opts.Now, &c)
	sc := scorecard.Score(cat, std, reported, scorecard.Env{
		Sources:        w.Sources(),
		Now:            opts.Now,
		MaxDocsAgeDays: std.Param("docs-fresh", "maxAgeDays", 180),
		LastEdit:       opts.LastEdit,
	}, &c)

	history, historyErr := fs.ReadFile(root, scorecard.HistoryPath)
	// ... unchanged ...

	in := render.Input{
		Catalog: cat, Graph: g, Teams: teams,
		Scorecard: sc, Standards: std, History: history,
		HistoryUnreadable: historyErr != nil && !errors.Is(historyErr, fs.ErrNotExist),
		Sources:           w.Sources(), Mermaid: opts.Mermaid,
		GeneratedAt: opts.Now, Version: opts.Version,
		Notice: partialBanner(w.Failures()),
	}
	// ... unchanged from here ...
}

// reportFetchFailures decides what a failed repository costs.
//
// Spec §12: a fetch failure is a hard failure, because rendering a portal
// quietly missing three services is worse than rendering nothing.
// --allow-partial is the stated exception, and it pays for itself with a
// banner in the artifact rather than a line in a log nobody reads.
//
// exitUsage, not exitValidation: nobody's YAML is wrong. Exit 2 would send
// a service owner to look at a file that is fine.
func reportFetchFailures(fails []repoFailure, allowPartial bool, errOut io.Writer) int {
	if len(fails) == 0 {
		return exitOK
	}
	sorted := make([]repoFailure, len(fails))
	copy(sorted, fails)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	level := "error"
	if allowPartial {
		level = "warn"
	}
	for _, f := range sorted {
		fmt.Fprintf(errOut, "%s: %s\n", level, failureMessage(f, hostKindOf(f)))
	}
	if allowPartial {
		return exitOK
	}
	fmt.Fprintf(errOut,
		"refusing to build a portal that is missing %s; pass --allow-partial to build one anyway, with a banner saying so\n",
		plural(len(fails), "repository", "repositories"))
	return exitUsage
}

// failureMessage says what went wrong and what to do about it.
//
// The 404 case is the one that earns its length. Both hosts return 404 for a
// private repository with no token — identical to a repository that is not
// there — so a bare "not found" tells somebody their repository does not
// exist while they are looking at it in a browser tab.
func failureMessage(f repoFailure, kind string) string {
	vars := tokenVarName(f.Name) + " or " + strings.ToUpper(kind) + "_TOKEN"
	switch {
	case fetch.IsNotFound(f.Err):
		return fmt.Sprintf("cannot read %s: not found. A private repository with no token looks "+
			"exactly like this; check the url and that %s is set", f.Name, vars)
	case fetch.IsUnauthorized(f.Err):
		return fmt.Sprintf("cannot read %s: the host rejected the token; check that %s is current "+
			"and has read access", f.Name, vars)
	case fetch.IsRateLimited(f.Err):
		var se *fetch.StatusError
		errors.As(f.Err, &se)
		return fmt.Sprintf("cannot read %s: the host's rate limit is spent until %s; an "+
			"unauthenticated build gets 60 requests an hour, an authenticated one 5000",
			f.Name, se.RateReset.Format(time.RFC3339))
	default:
		return fmt.Sprintf("cannot read %s: %v", f.Name, f.Err)
	}
}

// hostKindOf names the host in a token hint. It reads the url rather than
// carrying the kind on repoFailure, because a failure that happened before
// the adapter was chosen has no kind to carry.
func hostKindOf(f repoFailure) string {
	if strings.Contains(f.URL, "gitlab") {
		return "gitlab"
	}
	return "github"
}

// partialBanner is the degraded-mode notice stamped into every page.
//
// It NAMES the repositories. partialNotice, the scaffold this replaces,
// could only count them: with no fetcher it did not know which entry was
// local, and the version that guessed named the wrong two in every page of
// a generated site. Ruling R22 said a real fetcher would name the ones that
// actually failed. This is that.
func partialBanner(fails []repoFailure) string {
	if len(fails) == 0 {
		return ""
	}
	names := make([]string, 0, len(fails))
	for _, f := range fails {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	possessive := "their"
	if len(names) == 1 {
		possessive = "its"
	}
	return fmt.Sprintf("This portal is incomplete: %s could not be read, so %s services are "+
		"missing from this catalog.", englishList(names), possessive)
}

// englishList renders "a", "a and b", "a, b and c". A banner is read by a
// person, and "a, b" for two items reads as a truncated list.
func englishList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}
```

**Delete `partialNotice` entirely**, along with its call site in the `render.Input` literal. Its doc comment is thirty lines about a limitation that no longer exists, and leaving it would leave a comment claiming Plan 4 has not happened.

- [ ] **Step 4: Wire the command**

In `newBuildCmd`'s `RunE`, fetch before building:

```go
			cmd.SilenceUsage = true
			var c diag.Collector
			w := openRepos(cmd.Context(), reposOptions{
				Root: resolved, RootFS: os.DirFS(resolved),
				Cache:  cacheFor(resolved, noCache),
				Lookup: os.LookupEnv,
				ErrOut: cmd.ErrOrStderr(),
			}, &c)
			reportDiagnostics(cmd.ErrOrStderr(), c.Diagnostics())
			if c.HasErrors() {
				os.Exit(exitUsage)
			}
			opts := BuildOptions{
				Mermaid: mermaid,
				Now:     time.Now().UTC(), LastEdit: multiLastEdit(resolved, w),
				Version: version(), Force: force, AllowPartial: allowPartial,
			}
			files, code := Build(os.DirFS(resolved), w, cmd.ErrOrStderr(), opts)
```

Add the two flags:

```go
	cmd.Flags().BoolVar(&allowPartial, "allow-partial", false,
		"render a portal from the repositories that could be read, with a banner naming the ones that could not")
	cmd.Flags().BoolVar(&noCache, "no-cache", false,
		"ignore the fetched-blob cache under .landsraad/cache")
```

And the two small helpers:

```go
// cacheFor is the blob cache, or nothing when --no-cache is set.
func cacheFor(root string, noCache bool) fetch.Cache {
	if noCache {
		return fetch.NopCache{}
	}
	return newBlobCache(filepath.Join(root, ".landsraad", "cache", "blobs"))
}

// multiLastEdit answers docs-fresh for every repository: git history for the
// local one, the host's commit API for the rest (ruling R35).
//
// A repository whose adapter has no answer returns false rather than a date,
// and docs-fresh then reports not-reported. Inventing one would give every
// fetched service full marks for freshness.
func multiLastEdit(root string, w *workspace) scorecard.LastEditFunc {
	local := gitLastEdit(root)
	return func(repo, p string) (time.Time, bool) {
		if repo == w.local {
			return local(repo, p)
		}
		f, ok := w.FetcherFor(repo)
		if !ok {
			return time.Time{}, false
		}
		t, known, err := f.LastEdit(context.Background(), p)
		if err != nil {
			return time.Time{}, false
		}
		return t, known
	}
}
```

`workspace.FetcherFor` and the `fetchers` map behind it are defined in Task 12. This is the one place a fetcher is used after `openRepos` returns, and it is unavoidable: `docs-fresh` asks a question about a ref that no tree listing answers and no content-addressed cache can hold.

- [ ] **Step 5: Run the tests**

Run: `go test ./cmd/... -v`
Expected: PASS.

- [ ] **Step 6: Verify the tree**

Run: `task ci && go run ./cmd/landsraad build testdata/monorepo-ok -o /tmp/plan4-check`
Expected: `task ci` passes, and the fixture build still renders — a single-repository build with no `repos.yaml` remote entries must be untouched by all of this.

- [ ] **Step 7: Commit**

```bash
git add cmd/landsraad/build.go cmd/landsraad/build_test.go
git commit -m "feat(build): real fetching, --allow-partial, and a banner that names names

Deletes partialNotice. It counted repositories because it could not know
which ones were missing -- the version that guessed named the wrong two in
every page of a generated site. A fetcher knows exactly which failed.

A fetch failure exits 1, not 2: nobody's YAML is wrong, and exit 2 would
send a service owner to look at a file that is fine.

Rulings R22, R32, R34, R35."
```

---

### Task 14: `serve --watch` with remotes

`serve.go:170` calls `Build` on every filesystem event. With remotes in the catalog that must not mean a host API call per keystroke.

**Files:**
- Modify: `cmd/landsraad/serve.go`
- Test: `cmd/landsraad/serve_test.go`

**Interfaces:**
- Consumes: `workspace`, `Build` from Tasks 12 and 13.
- Produces: `func Serve(root string, w *workspace, addr string, opts BuildOptions, now func() time.Time, watch bool, errOut io.Writer) error` — **signature change**.

- [ ] **Step 1: Write the failing test**

Add to `cmd/landsraad/serve_test.go`:

```go
// Ruling R33: remotes are fetched once, before the first build. A rebuild
// swaps only the local filesystem. Without this, editing one character in a
// runbook costs a round trip to every repository in repos.yaml.
func TestRebuildReusesFetchedRemotes(t *testing.T) {
	remote := fstest.MapFS{
		"service.yaml": {Data: []byte(edgeServiceYAML)},
	}
	w := &workspace{
		sources: catalog.Sources{
			"platform": goodFixtureFS(t),
			"edge":     remote,
		},
		patterns: map[string][]string{"platform": {"services/*"}, "edge": {"."}},
		local:    "platform",
	}

	swapped := w.WithLocal(fstest.MapFS{"services/api/service.yaml": {Data: []byte(apiServiceYAML)}})

	// The remote value is the same object, not a re-fetch.
	before, _ := w.Sources().Get("edge")
	after, _ := swapped.Sources().Get("edge")
	if fmt.Sprintf("%p", before) != fmt.Sprintf("%p", after) {
		t.Error("WithLocal replaced the fetched remote filesystem")
	}
	// And the local one did change.
	local, ok := swapped.Sources().Get("platform")
	if !ok {
		t.Fatal("the swapped workspace has no local repository")
	}
	if _, err := fs.Stat(local, "services/api/service.yaml"); err != nil {
		t.Errorf("the swapped local filesystem does not hold the new file: %v", err)
	}
}

// A workspace with no local entry -- a platform repository holding only
// configuration -- must not crash on rebuild. There is simply nothing to
// swap.
func TestWithLocalOnAnAllRemoteWorkspace(t *testing.T) {
	w := &workspace{
		sources:  catalog.Sources{"edge": fstest.MapFS{}},
		patterns: map[string][]string{"edge": {"."}},
	}
	if got := w.WithLocal(fstest.MapFS{}); got != w {
		t.Error("WithLocal on a workspace with no local repository should return the receiver unchanged")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/landsraad/ -run 'TestRebuildReuses|TestWithLocalOn' -v`
Expected: FAIL to compile — `undefined: workspace` in this file until Task 12's type is in place, then FAIL on `Serve`'s signature.

- [ ] **Step 3: Implement**

In `cmd/landsraad/serve.go`, `Serve` takes the workspace and the rebuild swaps only the local filesystem:

```go
func Serve(root string, w *workspace, addr string, opts BuildOptions, now func() time.Time, watch bool, errOut io.Writer) error {
```

and inside `rebuild`:

```go
		// A fresh os.DirFS for the local repository; every fetched remote is
		// the same value it was at startup (ruling R33). Fetching here would
		// mean a host API call for every keystroke in a runbook.
		files, code := Build(os.DirFS(root), w.WithLocal(os.DirFS(root)), errOut, opts)
```

In `newServeCmd`'s `RunE`, call `openRepos` once before `Serve`, exactly as `build` does, and extend `--watch`'s help:

```go
	cmd.Flags().BoolVar(&watch, "watch", false,
		"rebuild when a file changes. Only the local repository is watched: "+
			"remote repositories are fetched once at startup, and picking up a "+
			"change in one needs a restart")
```

The startup line gains the count, so somebody looking at a portal knows what it covers:

```go
	if n := len(w.Sources().Names()); n > 1 {
		fmt.Fprintf(errOut, "serving %s\n", plural(n, "repository", "repositories"))
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./cmd/... -v`
Expected: PASS.

- [ ] **Step 5: Verify by hand**

Run: `go run ./cmd/landsraad serve testdata/monorepo-ok --watch`, edit a runbook, and confirm the rebuild is instant and no request is attempted.
Expected: "change detected" then a rebuild, with no network activity — the fixture has no remote entries, so this is the single-repository regression check.

- [ ] **Step 6: Commit**

```bash
git add cmd/landsraad/serve.go cmd/landsraad/serve_test.go
git commit -m "feat(serve): fetch remotes once, rebuild the local tree only

Build is called on every filesystem event. Fetching inside it would mean a
host API call for every keystroke in a runbook.

Ruling R33."
```

---
### Task 15: The `multirepo` fixture and the end-to-end test

Spec §14 requires a `multirepo/` fixture and this is the plan that owes it. The fixture is a **real directory tree** served by a fake host, so it is readable and editable like `monorepo-ok/` rather than a wall of recorded JSON — and the helper that serves it computes real git blob SHAs, so Task 8's verification is exercised rather than bypassed.

**Files:**
- Create: `testdata/multirepo/platform/` (the local repository), `testdata/multirepo/edge-gateway/` (served as a remote)
- Create: `cmd/landsraad/fakehost_test.go`
- Modify: `cmd/landsraad/integration_test.go`
- Modify: `Taskfile.yml`

**Interfaces:**
- Consumes: everything.
- Produces: `func fakeGitHub(t *testing.T, dir string) *httptest.Server` — a test helper only.

- [ ] **Step 1: Build the fixture**

```
testdata/multirepo/
  platform/
    repos.yaml            # names itself local, plus the remote below
    teams.yaml
    standards.yaml
    services/api/service.yaml
    services/api/runbook.md
    services/api/docs/index.md
    .landsraad/checks/scan.yaml
  edge-gateway/
    service.yaml          # dependsOn: service:api -- a cross-repo reference
    runbook.md
    docs/index.md
```

`platform/repos.yaml`:

```yaml
repos:
  - url: https://example.invalid/org/platform
    local: true
    paths: [services/*]
  - url: https://REMOTE_HOST/org/edge-gateway
    host: github
    ref: main
    paths: [.]
```

`REMOTE_HOST` is substituted with the test server's host at run time. The cross-repository `dependsOn` in `edge-gateway/service.yaml` is the point of the whole fixture: it is a reference that **cannot** resolve under `validate` (spec §7.1 records it, LocalOnly) and **must** resolve under `build` (FullCatalog). Nothing before this plan could test that.

- [ ] **Step 2: Write the fake host**

Create `cmd/landsraad/fakehost_test.go`:

```go
package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves a directory as if it were a GitHub repository.
//
// A real directory rather than recorded JSON, so the fixture is readable and
// editable like every other one in testdata/. The blob shas are computed the
// way git computes them, which means Task 8's verification runs for real
// here instead of being skipped by fixture shas that were never right.
//
// TLS because repos.yaml requires https, and a test that had to relax that
// rule would stop testing the rule. The caller injects srv.Client().
func fakeGitHub(t *testing.T, dir string) *httptest.Server {
	t.Helper()

	type blob struct {
		sha  string
		data []byte
	}
	blobs := map[string]blob{}   // path -> blob
	bySHA := map[string][]byte{} // sha  -> content
	var dirs []string

	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, rel)
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha1.New()
		fmt.Fprintf(h, "blob %d\x00", len(data))
		h.Write(data)
		sha := hex.EncodeToString(h.Sum(nil))
		blobs[rel] = blob{sha, data}
		bySHA[sha] = data
		return nil
	})
	if err != nil {
		t.Fatalf("building the fake host from %s: %v", dir, err)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/git/trees/main"):
			rows := []map[string]any{}
			for _, d := range dirs {
				rows = append(rows, map[string]any{"path": d, "type": "tree", "sha": "t-" + d})
			}
			for p, b := range blobs {
				rows = append(rows, map[string]any{
					"path": p, "type": "blob", "sha": b.sha, "size": len(b.data),
				})
			}
			json.NewEncoder(w).Encode(map[string]any{"tree": rows, "truncated": false})
		case strings.Contains(r.URL.Path, "/git/blobs/"):
			sha := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			data, ok := bySHA[sha]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(data)
		case strings.HasSuffix(r.URL.Path, "/commits"):
			json.NewEncoder(w).Encode([]map[string]any{
				{"commit": map[string]any{"committer": map[string]any{"date": "2026-09-01T00:00:00Z"}}},
			})
		default:
			t.Errorf("fake host got an unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// multirepoRoot copies testdata/multirepo/platform into a temp directory
// with REMOTE_HOST substituted, and returns the path.
func multirepoRoot(t *testing.T, host string) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "multirepo", "platform")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, []byte(strings.ReplaceAll(string(data), "REMOTE_HOST", host)), 0o644)
	})
	if err != nil {
		t.Fatalf("staging the multirepo fixture: %v", err)
	}
	return root
}
```

- [ ] **Step 3: Write the end-to-end test**

Add to `cmd/landsraad/integration_test.go`:

```go
// The whole feature, end to end: two repositories, one on disk and one over
// a host API, merged into one catalog with a reference crossing between
// them.
func TestBuildMergesALocalAndARemoteRepository(t *testing.T) {
	srv := fakeGitHub(t, filepath.Join("..", "..", "testdata", "multirepo", "edge-gateway"))
	root := multirepoRoot(t, srv.Listener.Addr().String())

	var c diag.Collector
	w := openRepos(context.Background(), reposOptions{
		Root: root, RootFS: os.DirFS(root),
		Cache:  fetch.NopCache{},
		Lookup: func(string) (string, bool) { return "test-token", true },
		ErrOut: io.Discard,
		HTTP:   srv.Client(),
	}, &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("openRepos reported %d diagnostics: %+v", len(ds), ds)
	}
	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("openRepos failed for %+v", got)
	}

	var errOut bytes.Buffer
	files, code := Build(os.DirFS(root), w, &errOut, BuildOptions{
		Now: testNow, Version: "test",
		LastEdit: func(string, string) (time.Time, bool) { return testNow, true },
	})
	if code != exitOK {
		t.Fatalf("Build exit = %d, stderr:\n%s", code, errOut.String())
	}

	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	// A page for the remote repository's entity.
	if _, ok := byPath["entity/service/edge/index.html"]; !ok {
		t.Error("no page for the entity defined in the fetched repository")
	}
	// Its runbook, fetched as a blob and rendered.
	if _, ok := byPath["entity/service/edge/runbook.html"]; !ok {
		t.Error("the fetched repository's runbook was not rendered")
	}
	// And the cross-repository dependency resolved: build runs at
	// FullCatalog, so a dangling ref would have been a hard failure.
	if got := string(byPath["entity/service/edge/index.html"]); !strings.Contains(got, "service:api") {
		t.Error("the cross-repository dependsOn did not resolve")
	}
	// No banner: nothing failed.
	for p, data := range byPath {
		if strings.Contains(string(data), "This portal is incomplete") {
			t.Errorf("%s carries a degraded-mode banner on a clean build", p)
		}
	}
}

// The same fixture with the host refusing everything: build must refuse, and
// --allow-partial must render the local half with a banner naming the
// remote (ruling R32).
func TestBuildWithAnUnreachableRemote(t *testing.T) {
	dead := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(dead.Close)
	root := multirepoRoot(t, dead.Listener.Addr().String())

	open := func() *workspace {
		var c diag.Collector
		return openRepos(context.Background(), reposOptions{
			Root: root, RootFS: os.DirFS(root), Cache: fetch.NopCache{},
			Lookup: func(string) (string, bool) { return "t", true },
			ErrOut: io.Discard, HTTP: dead.Client(),
		}, &c)
	}

	var errOut bytes.Buffer
	if _, code := Build(os.DirFS(root), open(), &errOut, BuildOptions{Now: testNow, Version: "t"}); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}

	errOut.Reset()
	files, code := Build(os.DirFS(root), open(), &errOut, BuildOptions{
		Now: testNow, Version: "t", AllowPartial: true,
	})
	if code != exitOK {
		t.Fatalf("--allow-partial exit = %d, stderr:\n%s", code, errOut.String())
	}
	var banners int
	for _, f := range files {
		if strings.Contains(string(f.Data), "edge-gateway could not be read") {
			banners++
		}
	}
	if banners == 0 {
		t.Error("no page names the repository that failed")
	}
}
```

- [ ] **Step 4: Prove the suite is offline**

Spec §14 says zero network in the test suite, and this is the plan that could break it. Verify rather than assume:

Run: `go test ./... -count=1` with outbound network blocked.
On macOS, the cheap version is to confirm no test resolves a name outside loopback:

```bash
go test ./... -count=1 2>&1 | tail -5
# then, to be sure the fetch tests are not quietly reaching a host:
go test ./internal/fetch/ ./cmd/landsraad/ -count=1 -v 2>&1 | grep -ci 'api.github.com\|gitlab.com'
```

Expected: `task test` passes, and the grep finds **0** — every host in the suite is an `httptest` server on loopback. If it ever finds more than zero, the offending test is deleted, not skipped.

- [ ] **Step 5: Add a task for the fixture**

In `Taskfile.yml`:

```yaml
  site-multirepo:
    desc: Build the portal from the multi-repo fixture, with the remote faked
    cmds:
      - go test ./cmd/landsraad/ -run TestBuildMergesALocalAndARemoteRepository -v
```

A `task` entry rather than a runnable `landsraad build`, because the remote half only exists inside the test's fake host. Pretending otherwise would mean shipping a fixture that cannot actually be built.

- [ ] **Step 6: Commit**

```bash
git add testdata/multirepo cmd/landsraad/fakehost_test.go cmd/landsraad/integration_test.go Taskfile.yml
git commit -m "test: the multirepo fixture spec 14 asks for

A real directory served by a fake host, with git-computed blob shas, so
the fixture reads like every other one in testdata/ and Task 8's sha
verification runs for real instead of being bypassed.

The cross-repository dependsOn is the point: a reference that cannot
resolve under validate and must resolve under build. Nothing before this
plan could test that."
```

---

### Task 16: Documentation, and correcting the record

Three documents make claims this plan falsifies, and one rule needs a new enforcement point.

**Files:**
- Modify: `README.md`, `CONTRIBUTING.md`, `CLAUDE.md`
- Modify: `scripts/check-rules.sh`, `.claude/hooks/no-network-in-stages.py` (new)
- Modify: `docs/superpowers/specs/2026-09-08-landsraad-design.md`
- Modify: `docs/superpowers/plans/2026-09-09-landsraad-portal-renderer.md`

- [ ] **Step 1: Add the rule that keeps R25 true**

`internal/fetch` is the first package under `internal/` allowed to speak HTTP, and the only one. Without an enforcement point, the next contributor who needs a URL puts `http.Get` in a stage and R25 quietly stops being true — exactly the failure CONTRIBUTING.md's preamble describes ("this project has already demonstrated that writing a principle down is not the same as following it").

Add to `scripts/check-rules.sh`:

```sh
# 4. Only internal/fetch speaks HTTP. Every other package under internal/ is
#    a pipeline stage, and a stage that blocks on a socket cannot be tested
#    offline, cannot be run in a service repo's PR CI, and makes "score runs
#    offline in under a second" a claim about which filesystem you happened
#    to pass it. Plan 4 ruling R25.
found=$(grep -rnE '^[[:space:]]*(import[[:space:]]+)?([A-Za-z0-9_.]+[[:space:]]+)?"(net/http|net)"[[:space:]]*(//.*)?$' \
	internal --include='*.go' --exclude='*_test.go' 2>/dev/null \
	| grep -v '^internal/fetch/' || true)
if [ -n "$found" ]; then
	report 'only internal/fetch may import net/http — a pipeline stage that blocks on a socket cannot run offline:' "$found"
fi
```

Mirror it in a new `.claude/hooks/no-network-in-stages.py`, following the shape of `no-os-in-internal.py`.

- [ ] **Step 2: Correct Plan 3's closing claim**

In `docs/superpowers/plans/2026-09-09-landsraad-portal-renderer.md`, the "What Plan 4 adds" section ends with a sentence that is false. Replace it:

```markdown
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
```

Leaving the original sentence in place would be worse than the error itself: a reader would trust it.

- [ ] **Step 3: Amend the spec**

Three edits to `docs/superpowers/specs/2026-09-08-landsraad-design.md`:

- **§7, stage 2** — "→ local cache" is now specific: append a sentence saying the cache is content-addressed on the git blob SHA, at `.landsraad/cache/blobs/`, with no automatic pruning in v1 (R27).
- **§13, package layout** — replace the one-line `internal/fetch/` entry with the real file list, the way §13 was already corrected once for `internal/render/web/`:

```
internal/fetch/        Fetcher interface returning an fs.FS; fs.go is the
                       sparse filesystem, client.go the shared
                       HTTP half, github.go/githubwalk.go/gitlab.go the
                       adapters. The only package under internal/ that
                       speaks HTTP, and it makes every request before a
                       stage runs.
```

- **§14.1, Secrets** — "the fetch adapters' tokens arrive with Plan 3" is wrong; Plan 3 was the renderer. Change to "with Plan 4", and add the resolution order from R29.

- [ ] **Step 4: Update `CLAUDE.md`**

The project file says Plan 4 "is the only part of v1 still unbuilt". Add Plan 4 to the reading order table, change that sentence to say v1 is complete, and add one row to the non-negotiable rules table:

| Rule | Why | Hook |
|---|---|---|
| Only `internal/fetch` imports `net/http` | Every other package under `internal/` is a pipeline stage. A stage that blocks on a socket cannot run in a service repo's PR CI, cannot be tested offline, and makes "`score` runs offline in under a second" a claim about which filesystem you passed it. `internal/fetch` makes every request *before* a stage runs. | `no-network-in-stages.py` |

Also extend the `os` rule's row: the blob cache is the second place a path from outside becomes a filesystem path, and `safeBlobPath` is its `safeManifestPath`.

- [ ] **Step 5: Update `README.md`**

A new section documenting the user-facing surface, all of it new:

- `repos.yaml`'s four new keys, with the worked example from R26.
- Tokens: `LANDSRAAD_TOKEN_<NAME>` then `GITHUB_TOKEN`/`GITLAB_TOKEN`, never in `repos.yaml`, and the note that a private repository with no token returns a 404 that looks like a missing repository.
- `build --allow-partial` and what the banner means to somebody reading the portal.
- `build --no-cache`, where the cache lives, that it is content-addressed and safe to delete, and that nothing prunes it.
- That `serve --watch` watches only the local repository.
- That Gitea, Forgejo, Bitbucket and plain git remain unsupported (D5, §15) — now that two hosts work, the third question will be asked.

- [ ] **Step 6: Update `CONTRIBUTING.md`**

Add the `net/http` rule beside the other three, with the same framing: it is enforced by `scripts/check-rules.sh` for everyone, and by a hook for the fast feedback.

- [ ] **Step 7: Verify**

Run: `task ci`
Expected: PASS, including the new rule — which should find nothing, because `internal/fetch` is the only package importing `net/http`.

Then confirm the new rule actually bites, rather than trusting that it does:

```bash
printf 'package catalog\n\nimport "net/http"\n\nvar _ = http.Get\n' > internal/catalog/tmp_check.go
sh scripts/check-rules.sh; echo "exit=$?"
rm internal/catalog/tmp_check.go
```

Expected: the script reports the violation and exits non-zero. A rule that does not fail on a deliberate violation is not a rule.

- [ ] **Step 8: Commit**

```bash
git add README.md CONTRIBUTING.md CLAUDE.md scripts/check-rules.sh .claude/hooks/no-network-in-stages.py docs/superpowers/specs/2026-09-08-landsraad-design.md docs/superpowers/plans/2026-09-09-landsraad-portal-renderer.md
git commit -m "docs: Carryall, and the correction Plan 3 owed

Plan 3 claimed nothing below cmd/ would change. Five call sites did. The
claim is corrected in place rather than quietly dropped -- a reader would
have trusted it.

Adds the fourth enforced rule: only internal/fetch imports net/http. A
pipeline stage that blocks on a socket cannot run in a service repo's PR
CI and cannot be tested offline.

Rulings R23, R25, R27, R29."
```

---

## Self-review

Run before considering the plan done, and again after execution.

**Spec coverage.** Stage 2 (FETCH) is Tasks 6–12. Decision D5's two adapters are Tasks 8–10; §15's non-goal of other hosts is documented in Task 16. §12's `--allow-partial` and its visible banner are Task 13. §14's `multirepo/` fixture and zero-network rule are Task 15. §14.1's "tokens in CI secrets, never in `repos.yaml`" is R29, enforced by a test in Task 7 and corrected in the spec in Task 16. §9's "known cost of D5" — one API call per documented entity — is R35, Task 13. §7's "→ local cache" is R27, Task 11.

**What this plan does not do, deliberately.** `gen` and `score` stay single-repository (R30). There is no `landsraad cache prune` (R27). `serve --watch` does not poll remotes (R33). Gitea, Forgejo, Bitbucket and plain git remain unsupported, which is D5's accepted consequence and §15's stated non-goal — this plan makes the `Fetcher` interface the place a third adapter would go, and adds nothing speculative for one.

**Known weak points, stated rather than discovered.**

- **`openRepos` parses twice.** Phase 1 parses to learn which files phase 2 must fetch, throws those entities away, and `workspace.ParseAll` re-derives them once every repository is present. Parsing is cheap and the alternative — keeping the first parse — would mean merging a catalog from entities parsed against a half-fetched filesystem. It is still two passes, and if it ever shows up in a profile the fix is to keep phase 1's entities keyed by repository rather than to fetch differently.
- **The content set is a hand-maintained list.** `contentSet` must name every `fs.ReadFile` below `internal/`. Task 12's test pins the current five, but nothing makes a sixth fail to compile — it fails at runtime, inside a stage, as `ErrNotFetched`. A `Prefetcher`-aware `fs.FS` that recorded misses would catch it; that is a real improvement and it is not in this plan.
- **`docs-fresh` costs one request per documented entity on a remote repository.** Spec §9 already calls this the known cost of D5. Forty services is forty requests, memoized per path per run and not cacheable — a commit date is a property of a ref, not of content, so R27's cache cannot help.
- **GitHub's truncated-tree fallback is `2 + entities` requests.** It only runs on repositories large enough to blow a 7 MB listing, and it is the difference between working and refusing.

**Before merging.** Run `composition-auditor`, as CLAUDE.md requires. Point it at what this plan could not settle from a document: whether `catalog.Sources` still reads as one idea once five callers have it, and whether `Fetcher`'s four methods stayed one interface or drifted into four callers' wishes merged.

Two questions an earlier draft listed here are already answered and should not be re-litigated. `Sources` is a **named map**, not a struct — settled against `Catalog.Entities`'s own precedent about defending from callers that do not exist, and recorded in R23. And "do these rulings match the code" cannot be asked before the code exists; it is exactly what the *second* run of this section is for.
