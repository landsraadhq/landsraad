# landsraad

A single Go binary that reads `service.yaml` metadata colocated with code,
validates it, scores it against a team standard, and renders a static portal.
The small-team alternative to Backstage: no plugin framework, no database, no
platform team.

## Read in this order

| | |
|---|---|
| Design, decisions D1–D13, rationale | `docs/superpowers/specs/2026-09-08-landsraad-design.md` |
| Plan 1 — catalog core, 11 tasks | `docs/superpowers/plans/2026-09-08-landsraad-catalog-core.md` |
| Plan 2 — scorecard and generated artefacts, 12 tasks | `docs/superpowers/plans/2026-09-09-landsraad-scorecard-and-artifacts.md` |
| Plan 3 — the portal renderer, 15 tasks | `docs/superpowers/plans/2026-09-09-landsraad-portal-renderer.md` |
| Plan 4 — Carryall, multi-repo fetching, 16 tasks | `docs/superpowers/plans/2026-09-10-landsraad-carryall.md` |
| Composition principles in depth | `/composition` |

Plan 1 delivered scope A, the catalog core (`validate`). Plan 2 delivered B and
C (`gen`, `score`). Plan 3 delivered D, the portal (`build`, `serve`), against
the local repository. Plan 4 delivered Carryall — multi-repo fetching over the
GitHub and GitLab APIs. v1 is complete.

## Non-negotiable, and enforced by hooks

These are not style preferences. **`scripts/check-rules.sh`, run by
`task lint`, is the enforcement point** — it applies to everyone, including
contributors who do not use Claude Code. The first four rules additionally
have a hook in `.claude/hooks/` that blocks the edit before it lands, because
this project has already demonstrated that writing a principle down is not the
same as following it; the hooks are the fast feedback, not the gate. The
fifth formats and reports but never blocks — `task ci` is its gate.

| Rule | Why | Hook |
|---|---|---|
| No non-test file under `internal/` imports `os` or any `os/*` package | Reads take `io/fs.FS`, writes take `io.Writer`, only `cmd/` touches the filesystem. Buys Plan 4's fetched-repo support for free, keeps tests off disk, and makes `..` traversal structurally impossible *inside `internal/`*. It does not extend to `cmd/`: `writeSite` re-reads `dist/.landsraad-manifest` from disk on a rebuild, and until `safeManifestPath` was added a `../` line in that file deleted the sibling it named. Where `cmd/` joins a path it did not produce itself, it checks it: `safeManifestPath` for a manifest read back off disk, `safeBlobPath` for a sha a remote host supplied, and `writeSite` backstops every generator path with `emit.ValidPath`. The joins in `gen.go`, `score.go` and `init.go` are not checked, and do not need to be today — every path reaching them is a constant this program wrote — but nothing enforces that, so a generator that ever derives a path from user data has to be checked at its join too. `os/*` counts: a fetch adapter shelling out to `git clone` via `os/exec` breaks the rule without importing `os`. Two deliberate exceptions: `discover_test.go` checks the seam against a real directory, and `render/golden_test.go` reads its golden files off disk on the ordinary test path, not only under `-update` — both enforcement points exclude `*_test.go`. | `no-os-in-internal.py` |
| No `sync.Once`, no `init()` below `cmd/` | Package-level mutable state means two configurations cannot coexist and initialisation failure cannot be tested. | `no-package-state.py` |
| Diagnostics assert **exact** message strings | Error message quality is the product. A substring check passes against a badly worded message — the first draft was 0/10 on this while looking thoroughly tested. | `exact-message-tests.py` |
| Only `internal/fetch` imports `net/http` — or bare `net` | Every other package under `internal/` is a pipeline stage. A stage that blocks on a socket cannot run in a service repo's PR CI, cannot be tested offline, and makes "`score` runs offline in under a second" a claim about which filesystem you passed it. `internal/fetch` makes every **content** request before stage 1 or between stages 1 and 3. Exactly one **check** makes requests from inside a stage, and it is named rather than hidden: `docs-fresh` asks a host when a path last changed, during `Score`, which is stage 7 (ruling R35) — one request per entity path it is asked about, plus a possible ref resolution, so the count is per-entity and not one. It still passes this rule because it reaches the socket through `scorecard.LastEditFunc`, an injected function type rather than an import — which is also why a score against a local filesystem makes no request at all. Both enforcement points ban bare `"net"` alongside `"net/http"`. | `no-network-in-stages.py` |
| Go stays gofmt-clean and vet-clean | `task ci` gates on both. This hook applies `gofmt -w` and *reports* vet findings without blocking: the plan is test-first, so a package that does not compile is the expected state between the failing test and the fix. | `gofmt-vet.py` (advisory) |

The renderer is where the `os` rule bites hardest — generating a directory tree
is exactly where a contributor reaches for `os.MkdirAll`. The answer is always
to return an `emit.File` and let `cmd/` write it. That is also what makes
`serve --watch` able to rebuild in memory.

## Project shape

- Build, test and lint through **go-task**, never `make`.
- Pipeline stages are typed pure functions **where practical** (spec §7);
  commands are explicit compositions. The type checker enforces stage
  ordering. Stage 7 is the exception, and it is the same one as the `net/http`
  rule's: `scorecard.Env.LastEdit` is an effectful callback, so `Score` is
  pure in everything except when a host says a path last changed.
- `apiVersion: landsraad/v1` — no domain, matching Kubernetes `apps/v1`.
- Entity names are flat, unique per `(kind, name)`, referenced as `kind:name`.
- Anything landing in a user's repository — the schema, `.landsraad/checks`,
  exit codes — is a one-way door. `/autonomy-check` before changing one.

## Before merging

Run `composition-auditor`. It audits abstractions in both directions and, more
importantly, tests whether this document's own claims are actually true.
