# landsraad

landsraad is a single Go binary that reads service metadata colocated with
your code and validates it — schema, ownership, dependency cycles — reporting
every problem it finds in one run with a file, a line, and what to do about
it. It's the small-team alternative to Backstage: no plugin framework, no
database, no platform team, and it works against a repo you already have.

## What exists today

**`landsraad validate`**, **`gen`**, **`score`**, **`build`** and **`serve`** all
work. Validation is hermetic and offline; `gen` derives CODEOWNERS, alert
routing and a Slack map; `score` measures the catalog against `standards.yaml`;
`build` renders a static portal and `serve --watch` previews it.

What is **not** built: fetching remote repositories over the GitHub and GitLab
APIs. `build` renders the repository it is run in, and warns when `repos.yaml`
names repositories it could not read.

## Try it in under a minute

```sh
go install github.com/landsraadhq/landsraad/cmd/landsraad@latest
cd your-repo
landsraad init
landsraad validate
```

`landsraad init` writes a minimal `teams.yaml`, `repos.yaml`, and one
`services/example/service.yaml` — it never overwrites a file that's already
there, and what it writes passes `landsraad validate` immediately. Edit those
three files to describe your actual services, delete `services/example`, and
you have a catalog.

`landsraad validate` works from any subdirectory of the repo, not only the
root: it walks up looking for `repos.yaml`, `teams.yaml` or `.git`, the way
any linter would.

## The three files

### `teams.yaml`

The source for who owns what. `landsraad gen` produces CODEOWNERS, alert
routing and a Slack map from exactly this shape, so it's worth getting right
early:

```yaml
teams:
  - name: team-payments      # what service.yaml `owner` refers to
    members: [alice, bob]    # CODEOWNERS entries
    slack: "#payments"       # the Slack map
    pagerduty: PAY           # alert routing target
```

Unknown keys are rejected: a silently ignored `pagerDuty:` typo would make
alert routing rot invisibly, and that's the exact failure this tool exists to
prevent.

### `repos.yaml`

Which repos and paths make up the catalog:

```yaml
repos:
  - url: https://github.com/org/monorepo
    paths: [services/*, workers/*, libs/*]
  - url: https://github.com/org/edge-gateway
    paths: [.]
```

If it's absent, `validate` falls back to `., services/*, workers/*, libs/*,
topics/*` — including `.` itself, so a root-level `service.yaml` is still
found — and says so in its output rather than staying silent about it.

### `service.yaml`

One per deployable unit or shared resource, colocated with the code it
describes:

```yaml
apiVersion: landsraad/v1
kind: Service            # Service | Worker | Cron | Library | Topic | Database | API | Resource
metadata:
  name: payments-worker  # unique per (kind, name) across the merged catalog
  description: Consumes payment events and settles them.
  owner: team-payments    # must exist in teams.yaml
  tier: 1                 # required for Service/Worker/Cron/API only
  lifecycle: production   # experimental | production | deprecated
  tags: [go, kafka]
spec:
  language: go
  path: services/payments-worker
  docs: services/payments-worker/docs
  runbook: services/payments-worker/docs/runbook.md
  dependsOn: [topic:payments.events, service:ledger-api]
```

`tier` is required only for kinds that can page someone — `Service`, `Worker`,
`Cron`, `API`. A library or topic doesn't need one.

## Wiring into CI

### GitHub Actions

```yaml
- uses: actions/setup-go@v5
  with:
    go-version: '1.23'
- run: go install github.com/landsraadhq/landsraad/cmd/landsraad@latest
- run: landsraad validate
```

Running under `GITHUB_ACTIONS=true` auto-selects `--format github`, so
problems show up as inline PR annotations.

### GitLab CI

```yaml
validate:
  image: golang:1.23
  script:
    - go install github.com/landsraadhq/landsraad/cmd/landsraad@latest
    - landsraad validate
```

Under `GITLAB_CI=true`, `--format gitlab` is auto-selected and writes a Code
Quality report GitLab renders on the MR.

Both can be forced explicitly with `--format text|json|github|gitlab`.
`validate` exits `0` clean, `1` on a usage or config error, `2` when it found
a problem in the catalog — script off the exit code, not the output.

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
landsraad build --mermaid-src none                      # diagrams degrade to a visible "not rendered" note
```

Search needs `fetch()`, which browsers block on `file://` pages. Use
`landsraad serve`, or host the output, to try it.

## Editor autocompletion

`landsraad init` writes `schema/service.schema.json` — the schema the binary
validates against — so this works in a fresh repository with no extra step.
After upgrading landsraad, refresh it:

```sh
landsraad schema > schema/service.schema.json
```

Each file `init` generates already carries:

```yaml
# yaml-language-server: $schema=../../schema/service.schema.json
```

so any editor running the yaml-language-server extension gets inline
validation and completion as you type.

## Why not Backstage / Cortex / OpsLevel / Port / a README table

Backstage, Cortex, OpsLevel and Port are real developer portals with real
plugin ecosystems. If you have a platform team and dozens of services, one of
those is probably the right answer — they do far more than landsraad ever
will. landsraad exists for the team that has neither: 5–15 engineers, one
monorepo or a handful of repos, and nobody whose job is running a portal. A
hosted platform is cheap to feed — plugins do the discovery for you — and
expensive to run: it wants a database, an ingestion pipeline, and someone on
call for it. landsraad takes the opposite trade. It's cheaper to run — one
static binary, no database, no server to keep up — and more expensive to
feed, because nothing discovers your services for you; you write the YAML
yourself, in the same PR as the code it describes.

A README table of services and owners is the other end of the spectrum: free
to start, and silently wrong within a month, because nothing enforces that it
stays in sync with reality. landsraad's bet is that metadata reviewed in the
same PR as the code it describes, and checked in the same CI run, is the only
kind that survives.

## Status

Implemented: schema validation, ownership checks against `teams.yaml`,
dependency-cycle detection, four CI-friendly output formats
(`text`, `json`, `github`, `gitlab`), generated ownership artifacts with a
`--check` gate, a tier-aware scorecard with history, and the static portal.

Designed but not built: multi-repo fetching over the GitHub and GitLab APIs.
See `docs/superpowers/specs/2026-09-08-landsraad-design.md`.

## License

Apache 2.0 — see [LICENSE](LICENSE).
