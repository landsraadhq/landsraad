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
`build` renders a static portal and `serve --watch` previews it. `build` also
fetches every remote repository named in `repos.yaml` over the GitHub and
GitLab APIs and merges their entities into one catalog — see "Multi-repository
catalogs" below.

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

## Tuning the standard — `standards.yaml`

Optional. Without it you are scored against the default matrix built into the
binary, which is what most teams should start with. `landsraad init` writes
that default into your repository so you can edit exactly what you were
already being scored against.

It is the only scoring knob: a matrix of check by tier, plus per-check
thresholds. Checks themselves are Go functions with stable ids, so there is
nothing to debug in YAML.

```yaml
apiVersion: landsraad/v1
kind: Standards
spec:
  staleAfterDays: 14            # ingested results older than this are stale, not pass
  checks:
    runbook-present:
      tiers: {1: required, 2: required, 3: warn}
    docs-fresh:
      params: {maxAgeDays: 180}
      tiers: {1: warn, 2: warn, 3: info}
    image-scanned:
      source: external          # reported in via .landsraad/checks/*.yaml
      appliesTo: [Service, Worker, Cron]
      tiers: {1: required, 2: required, 3: warn}
```

`required` fails `score --fail-on required`; `warn` counts toward the score and
gates only under `--fail-on warn`; `info` is reported and does not affect the
score; `skip` is not run. A tier with no entry is `skip`.

**`appliesTo` says which kinds a check is meaningful for.** Leave it out and
the check applies to every kind, which is what every file written before this
existed means. Use it for checks that cannot apply: an `API` contract
directory has no container image and no runtime, so asking it for
`image-scanned` or `otel-present` produces a score that is wrong in its
denominator, not a service that is behind. A check that does not apply is
reported as `not-applicable` and leaves the denominator, the same way an
in-force exemption does — it is not a silent skip.

Prefer it to an exemption for this. An exemption is time-bounded by design and
warns once it lapses, which is right for "we know, we're working on it" and
wrong for "an API is not a deployable" — that never expires. landsraad warns
(`exemption-not-applicable`) if you leave an exemption on a check your kind
already excludes.

**Rolling it out is a hard cut.** A landsraad older than `appliesTo` rejects a
`standards.yaml` that uses it, loudly, rather than ignoring the field and
scoring you against a standard you did not write. So upgrade the binary
everywhere — every CI runner and every developer — *before* the
`standards.yaml` change lands, or older runners fail immediately.

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
Every command exits `0` clean, `2` when a file you wrote has a problem a
diagnostic points at — a `service.yaml`, `teams.yaml`, `repos.yaml` or
`.landsraad/checks` file — or when `gen --check` finds a generated artifact
stale, and `1` when landsraad could not run: a bad flag, an unreadable
directory, a repository it could not fetch. `score` also exits `3` when the
catalog is valid and a service fails a check its tier requires. Script off
the exit code, not the output.

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

## Multi-repository catalogs

`build` reads every repository listed in `repos.yaml`, not only the one it
runs in. `url` and `paths` are the two keys shown above; four more are
optional:

```yaml
repos:
  - url: https://github.com/org/platform
    local: true                # the repository this command is running in
    paths: [services/*, workers/*, libs/*]
  - url: https://github.com/org/edge-gateway
    paths: [.]
  - url: https://gitlab.example.com/org/legacy-billing
    host: gitlab                # self-hosted instance; the hostname doesn't say
    name: billing                # this repo's identity in diagnostics and URLs
    ref: release                 # a branch or tag; omit to ask the host for its default
    paths: [.]
```

`local: true` marks the repository the command is standing in; at most one
entry may set it. `host` is needed only when the hostname does not already
say `github.com` or `gitlab.com` — a self-hosted instance. `name` is what
appears in every diagnostic and URL for that repository, in place of the
name `build` would otherwise derive from the URL. `ref` is a branch or tag;
left empty, landsraad asks the host for its default branch, which costs one
extra request but does not silently fetch nothing from a repository that
still uses `master`.

**Tokens.** Never put a token in `repos.yaml` — it is checked in like the
rest of the file, and a token there is a token in everyone's clone forever.
landsraad reads one from the environment instead, per repository:
`LANDSRAAD_TOKEN_<NAME>` first (the repository's `name`, or its derived
identity, uppercased, with every character that is not an ASCII letter or
digit replaced by `_`), then
`GITHUB_TOKEN` or `GITLAB_TOKEN` by host. No token is not an error — public
repositories work without one — but a **private** repository with no token
returns a 404 from both hosts, identical to a repository that does not
exist, so `build`'s failure message for that case names the variable to set.

**`validate --satellite`** is for a repository the platform's `repos.yaml`
fetches. It has no `teams.yaml` of its own — owners are defined once, in the
platform repository — so plain `validate` in its CI would fail on
`missing-teams`. `--satellite` checks everything else and leaves owners to
the platform build, with a note saying so. It refuses to run in a repository
that does have a `teams.yaml`.

**`build --allow-partial`** renders the portal from whichever repositories
could be read, instead of refusing outright, and stamps a banner naming the
ones that could not into every page — so a reader of the portal, not just
whoever ran the build, sees that it is incomplete and which services are
missing.

**`build --no-cache`** skips the fetched-blob cache. Fetched file contents are
otherwise cached under `.landsraad/cache/blobs/` in the repository `build`
runs in, keyed on the git blob SHA the host's tree listing returns —
content-addressed, so a hit can never be stale. The cache is safe to delete
at any time; nothing in v1 prunes it automatically, so it grows for as long
as the catalog changes.

If you scaffolded a repository with `landsraad init` before this cache
existed, its `.gitignore` will not have the `.landsraad/cache/` line —
`init` never overwrites a file that is already there. Add it yourself before
your first multi-repository build, or fetched file contents from other
repositories will stage into your git index.

**`serve --watch`** only watches the local repository. Remote repositories,
like `repos.yaml` itself, are fetched once at startup; picking up a change in
one needs a restart.

**Unsupported hosts.** Only GitHub and GitLab have adapters. Gitea, Forgejo,
Bitbucket and plain git remotes are not supported in v1 — an accepted
consequence of that decision, not an oversight, and `Fetcher` is a Go
interface so a third adapter is additive rather than a rewrite.

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
`--check` gate, a tier-aware scorecard with history, the static portal, and
multi-repository fetching over the GitHub and GitLab APIs.

Not supported: Gitea, Forgejo, Bitbucket and plain git remotes — see
"Multi-repository catalogs" above. Design rationale for all of the above is
in `docs/superpowers/specs/2026-09-08-landsraad-design.md`.

## License

Apache 2.0 — see [LICENSE](LICENSE).
