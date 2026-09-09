# landsraad

A single Go binary that reads `service.yaml` metadata colocated with code,
validates it, scores it against a team standard, and renders a static portal.
The small-team alternative to Backstage: no plugin framework, no database, no
platform team.

## Read in this order

| | |
|---|---|
| Design, decisions D1–D13, rationale | `docs/superpowers/specs/2026-09-08-landsraad-design.md` |
| Implementation, 11 tasks, 125 steps | `docs/superpowers/plans/2026-09-08-landsraad-catalog-core.md` |
| Composition principles in depth | `/composition` |

Plan 1 covers scope A only: catalog core, delivering `landsraad validate`.
Scorecard and generated artefacts are Plan 2; fetch adapters and the renderer
are Plan 3.

## Non-negotiable, and enforced by hooks

These are not style preferences. The first three have a hook in
`.claude/hooks/` that **blocks** the edit, because this project has already
demonstrated that writing a principle down is not the same as following it.
The fourth formats and reports but never blocks — `task ci` is its gate.

| Rule | Why | Hook |
|---|---|---|
| Nothing under `internal/` imports `os` | Reads take `io/fs.FS`, writes take `io.Writer`, only `cmd/` touches the filesystem. Buys Plan 3's fetched-repo support for free, keeps tests off disk, and makes `..` traversal structurally impossible. | `no-os-in-internal.py` |
| No `sync.Once`, no `init()` below `cmd/` | Package-level mutable state means two configurations cannot coexist and initialisation failure cannot be tested. | `no-package-state.py` |
| Diagnostics assert **exact** message strings | Error message quality is the product. A substring check passes against a badly worded message — the first draft was 0/10 on this while looking thoroughly tested. | `exact-message-tests.py` |
| Go stays gofmt-clean and vet-clean | `task ci` gates on both. This hook applies `gofmt -w` and *reports* vet findings without blocking: the plan is test-first, so a package that does not compile is the expected state between the failing test and the fix. | `gofmt-vet.py` (advisory) |

## Project shape

- Build, test and lint through **go-task**, never `make`.
- Pipeline stages are typed pure functions; commands are explicit compositions.
  The type checker enforces stage ordering.
- `apiVersion: landsraad/v1` — no domain, matching Kubernetes `apps/v1`.
- Entity names are flat, unique per `(kind, name)`, referenced as `kind:name`.
- Anything landing in a user's repository — the schema, `.landsraad/checks`,
  exit codes — is a one-way door. `/autonomy-check` before changing one.

## Before merging

Run `composition-auditor`. It audits abstractions in both directions and, more
importantly, tests whether this document's own claims are actually true.
