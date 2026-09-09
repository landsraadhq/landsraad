# landsraad

landsraad is a single Go binary that reads service metadata colocated with
your code and validates it — schema, ownership, dependency cycles — reporting
every problem it finds in one run with a file, a line, and what to do about
it. It's the small-team alternative to Backstage: no plugin framework, no
database, no platform team, and it works against a repo you already have.

## What exists today

**`landsraad validate`** is the whole product right now, alongside `init` to
scaffold a catalog and `schema` for editor support. Everything past that —
`score`, `gen`, `build`/`serve`, the static portal, fetching remote repos over
the GitHub/GitLab APIs — is designed but **not implemented**. If you came here
looking for a browsable service catalog site or a scorecard, that's the plan,
not the current state; see [Status](#status) below for what's real.

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

The source for who owns what. A future generator (Plan 2) will produce
CODEOWNERS and alert routing from exactly this shape, so it's worth getting
right early:

```yaml
teams:
  - name: team-payments      # what service.yaml `owner` refers to
    members: [alice, bob]    # CODEOWNERS entries, eventually
    slack: "#payments"       # deploy notifications, eventually
    pagerduty: PAY           # alert routing target, eventually
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
dependency-cycle detection, and four CI-friendly output formats
(`text`, `json`, `github`, `gitlab`) — all hermetic, all offline.

Designed but not built: the scorecard (`score`), generated artifacts
(`gen` — CODEOWNERS, alert routing, a Slack map), and the static portal
(`build`/`serve`), along with fetching remote repos over the GitHub and
GitLab APIs. See `docs/superpowers/specs/2026-09-08-landsraad-design.md` for
the full design and what's coming.

## License

Apache 2.0 — see [LICENSE](LICENSE).
