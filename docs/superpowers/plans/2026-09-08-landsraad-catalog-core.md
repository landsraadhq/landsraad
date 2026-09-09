# landsraad Catalog Core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `landsraad validate` — a hermetic, offline command that loads every `service.yaml` in a repo, validates it, resolves the dependency graph, and reports every problem at once with a file and a line number.

**Architecture:** A linear pipeline of pure functions. Discovery globs files; parse turns YAML into `Entity` while keeping line numbers; schema validation checks structure; merge builds a `Catalog` and catches name collisions; the graph resolves `kind:name` references and finds cycles. Nothing fails fast — every stage writes into a shared `diag.Collector`, so one run reports every problem in the repo.

**Tech Stack:** Go 1.23, go-task, `gopkg.in/yaml.v3` (line numbers via `yaml.Node`), `github.com/santhosh-tekuri/jsonschema/v6`, `github.com/spf13/cobra`, `github.com/google/go-cmp`.

**Spec:** `docs/superpowers/specs/2026-09-08-landsraad-design.md`

## Global Constraints

- **Module path:** `github.com/landsraadhq/landsraad` — every import in this plan uses it verbatim.
- **Go version:** 1.23. Set `go 1.23` in `go.mod`.
- **Binary name:** `landsraad`. Built to `bin/landsraad`.
- **`apiVersion` value:** `landsraad/v1` — no domain, matching Kubernetes `apps/v1`.
- **This plan is scope A only.** `validate` is hermetic: **no network calls, no tokens, no Docker**. Fetching remote repos is Plan 3; the scorecard is Plan 2. If a task seems to need the network, it belongs in another plan.
- **Zero network in the test suite.** The suite must pass on a plane.
- **Never fail fast.** Every validation stage appends to `diag.Collector` and continues. A run of twelve broken services reports twelve problems.
- **Every diagnostic carries `File` and `Line`.** A diagnostic without a line number is a bug.
- **Exit codes:** `0` clean, `1` usage/config error, `2` validation error. (`3`, the scorecard gate, arrives in Plan 2.)
- **Entity names are flat and globally unique.** References are `kind:name`. A collision is a hard error naming both file paths.
- **Strict schema:** `unevaluatedProperties: false` everywhere unknown keys are
  guarded (spec D7 — `additionalProperties` at the root would reject any field
  introduced inside an `if`/`then` branch). Unknown fields are rejected, never
  ignored. The separate `"additionalProperties": {"type": "string"}` form used for
  `labels`, `annotations` and `runtime.selector` constrains map *values* and is
  unrelated.
- **Composition, per spec §3.1 — these are hard rules, not preferences:**
  - All file access goes through `io/fs.FS`. No `os.Stat`, `os.ReadFile` or
    `filepath.Glob` against a path string anywhere below `cmd/`. The command
    layer builds the `fs.FS` (`os.DirFS(root)`); everything else receives one.
  - Every pipeline stage is a **typed pure function** — it takes its input
    type and a `*diag.Collector`, and returns its output type. A stage never
    knows which command runs it or what ran before it.
  - Commands are **explicit compositions** of those functions. Ordering is
    enforced by the type checker, not by convention.
  - **No package-level mutable state.** No `sync.Once` singletons, no `init()`
    registration. Things that need configuring are values you construct.
  - Output formats implement `diag.Formatter`; `Formatters()` returns the
    built-in ones. Adding one is a new type, never an edit to a `switch`.
- **Task boundaries are commit boundaries.** Every task ends with a passing `task test` and a commit.

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod`, `Taskfile.yml` | module, build/test/lint tasks |
| `.github/workflows/ci.yml` | run `task ci` on push and PR |
| `cmd/landsraad/main.go` | entry point, exit codes |
| `cmd/landsraad/validate.go` | the `validate` cobra command |
| `internal/diag/diag.go` | `Severity`, `Diagnostic`, `Collector` |
| `internal/diag/format.go` | `Formatter` interface, `Formatters()`, and the four formats |
| `internal/catalog/entity.go` | `Kind`, `Entity`, `Metadata`, `Spec` |
| `internal/catalog/parse.go` | YAML → `Entity`, preserving line numbers |
| `internal/catalog/ref.go` | `Ref` parse and format |
| `internal/catalog/merge.go` | `Catalog`, merge, collision detection |
| `internal/catalog/graph.go` | reference resolution, cycle detection |
| `internal/catalog/files.go` | referenced-file existence checks, over an `fs.FS` |
| `internal/catalog/load.go` | `ParseAll` — read and parse every discovered file from an `fs.FS` |
| `internal/schema/schema.go` | embedded schema; `*Validator` value, no globals |
| `internal/schema/service.schema.json` | the schema — canonical, embedded in the binary |
| `schema/service.schema.json` | generated copy for editors, written by `task schema` |
| `internal/config/teams.go` | `teams.yaml` |
| `internal/config/repos.go` | `repos.yaml` |
| `internal/discover/discover.go` | `Find(fs.FS, patterns)` — stage 1, no `os` |
| `testdata/` | fixture repos: one valid, one per diagnostic |

---

## Task 1: Project scaffold and the test cycle

**Files:**
- Create: `go.mod`, `Taskfile.yml`, `.gitignore`, `.github/workflows/ci.yml`
- Create: `cmd/landsraad/main.go`
- Test: `cmd/landsraad/main_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: a working `task test` cycle and `Version` constant, used by every later task.

- [ ] **Step 1: Initialise the module**

```bash
cd /path/to/repo
go mod init github.com/landsraadhq/landsraad
```

- [ ] **Step 2: Write `Taskfile.yml`**

```yaml
version: '3'

tasks:
  build:
    desc: Build the landsraad binary
    cmds:
      - go build -o bin/landsraad ./cmd/landsraad

  test:
    desc: Run all tests
    cmds:
      - go test ./...

  lint:
    desc: Vet and check formatting
    cmds:
      - go vet ./...
      - test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)

  release:
    desc: Build tagged, checksummed binaries for every platform
    cmds:
      - goreleaser release --clean

  install:
    desc: Build and symlink the lsr alias alongside the binary
    deps: [build]
    cmds:
      - ln -sf landsraad bin/lsr

  schema:
    desc: Export the JSON Schema to schema/ for editor autocompletion
    cmds:
      - mkdir -p schema
      - go run ./cmd/landsraad schema > schema/service.schema.json

  ci:
    desc: Everything CI runs
    deps: [lint, test]

  default:
    cmds:
      - task: ci
```

- [ ] **Step 3: Add the licence**

Unlicensed code is all-rights-reserved by default: an adopter with any standard
OSS policy is blocked before `go install`, and pkg.go.dev hides documentation
for modules with no detected licence. This is the cheapest fix in the project
and the one with the widest blast radius.

```bash
curl -sSL https://www.apache.org/licenses/LICENSE-2.0.txt -o LICENSE
```

Apache-2.0 rather than MIT: it carries an explicit patent grant, which is what
corporate legal review looks for in a tool that will be adopted at work.

- [ ] **Step 4: Write `.gitignore`**

```
bin/
dist/
```

- [ ] **Step 5: Write the failing test**

Create `cmd/landsraad/main_test.go`:

```go
package main

import "testing"

func TestVersionIsSet(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `task test`
Expected: FAIL — `undefined: Version`

- [ ] **Step 7: Write the minimal implementation**

Create `cmd/landsraad/main.go`:

```go
package main

import (
	"fmt"
	"os"
)

// Version is the binary version, overridden at release time with -ldflags.
var Version = "dev"

// Exit codes. See the spec, §12.
const (
	exitOK         = 0
	exitUsage      = 1
	exitValidation = 2
)

func main() {
	fmt.Fprintf(os.Stdout, "landsraad %s\n", Version)
	os.Exit(exitOK)
}
```

- [ ] **Step 8: Run it to verify it passes**

Run: `task test`
Expected: PASS

- [ ] **Step 9: Write the CI workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: ci
on:
  push:
    branches: [main]
  pull_request:

jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      - uses: arduino/setup-task@v2
        with:
          version: 3.x
          repo-token: ${{ secrets.GITHUB_TOKEN }}
      - run: task ci
```

- [ ] **Step 10: Add dependency updates**

Create `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: /
    schedule:
      interval: weekly
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
```

- [ ] **Step 11: Commit**

```bash
git add go.mod Taskfile.yml .gitignore LICENSE .github/workflows/ci.yml .github/dependabot.yml cmd/landsraad/main.go cmd/landsraad/main_test.go
git commit -m "feat: project scaffold with go-task build and test cycle"
```

---

## Task 2: Diagnostics

Everything downstream reports through this. Build it first so no later task invents its own error style.

**Files:**
- Create: `internal/diag/diag.go`
- Test: `internal/diag/diag_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Severity int` with `SevInfo`, `SevWarn`, `SevError`; `func (Severity) String() string`
  - `type Diagnostic struct { Severity Severity; Repo, File string; Line int; Entity, Check, Message, Hint string }`
  - `type Collector struct{ ... }` with `func (*Collector) Add(Diagnostic)`, `func (*Collector) Diagnostics() []Diagnostic` (sorted, deterministic), `func (*Collector) HasErrors() bool`, `func (*Collector) Len() int`

- [ ] **Step 1: Add go-cmp**

Full-struct comparison is what stops a formatter from silently dropping fields.

```bash
go get github.com/google/go-cmp@v0.6.0
```

- [ ] **Step 2: Write the failing test**

Create `internal/diag/diag_test.go`:

```go
package diag

import "testing"

func TestCollectorSortsDeterministically(t *testing.T) {
	var c Collector
	c.Add(Diagnostic{File: "b.yaml", Line: 1, Check: "z", Message: "second"})
	c.Add(Diagnostic{File: "a.yaml", Line: 9, Check: "a", Message: "first"})
	c.Add(Diagnostic{File: "a.yaml", Line: 2, Check: "a", Message: "zeroth"})

	got := c.Diagnostics()
	want := []string{"zeroth", "first", "second"}
	if len(got) != len(want) {
		t.Fatalf("got %d diagnostics, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Message != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i].Message, want[i])
		}
	}
}

func TestHasErrorsOnlyCountsErrors(t *testing.T) {
	var c Collector
	c.Add(Diagnostic{Severity: SevWarn, File: "a.yaml", Line: 1, Message: "just a warning"})
	if c.HasErrors() {
		t.Error("a warning alone must not report HasErrors")
	}
	c.Add(Diagnostic{Severity: SevError, File: "a.yaml", Line: 2, Message: "a real error"})
	if !c.HasErrors() {
		t.Error("an error must report HasErrors")
	}
}

func TestSeverityString(t *testing.T) {
	cases := map[Severity]string{SevInfo: "info", SevWarn: "warn", SevError: "error"}
	for sev, want := range cases {
		if got := sev.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", sev, got, want)
		}
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/diag/ -v`
Expected: FAIL — `undefined: Collector`

- [ ] **Step 4: Write the implementation**

Create `internal/diag/diag.go`:

```go
// Package diag collects validation problems so a single run can report all
// of them. Nothing in landsraad fails fast: every stage appends here and
// continues, so validating twelve broken services reports twelve problems.
package diag

import (
	"fmt"
	"sort"
)

// Severity ranks a diagnostic. Only SevError affects the exit code.
type Severity int

const (
	SevInfo Severity = iota
	SevWarn
	SevError
)

// MarshalJSON writes the severity name. Severity is an iota, so serialising
// the integer would mean that inserting a new severity silently changes what
// every existing consumer's filter matches.
func (s Severity) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

func (s *Severity) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case `"info"`:
		*s = SevInfo
	case `"warn"`:
		*s = SevWarn
	case `"error"`:
		*s = SevError
	default:
		return fmt.Errorf("unknown severity %s", b)
	}
	return nil
}

func (s Severity) String() string {
	switch s {
	case SevInfo:
		return "info"
	case SevWarn:
		return "warn"
	case SevError:
		return "error"
	default:
		return "unknown"
	}
}

// Diagnostic is one problem found in one place. File and Line are required:
// a diagnostic the reader cannot navigate to is a bug.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Repo     string   `json:"repo,omitempty"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Entity   string   `json:"entity,omitempty"`
	Check    string   `json:"check,omitempty"`
	Message  string   `json:"message"`
	// Hint is an optional suggested fix, e.g. "did you mean 'team-payments'?".
	Hint string `json:"hint,omitempty"`
}

// Collector accumulates diagnostics. The zero value is ready to use.
// It is not safe for concurrent use; the pipeline is single-goroutine.
type Collector struct {
	diags []Diagnostic
}

func (c *Collector) Add(d Diagnostic) { c.diags = append(c.diags, d) }

func (c *Collector) Len() int { return len(c.diags) }

// Diagnostics returns the collected diagnostics sorted by file, then line,
// then check. Output order must not depend on filesystem walk order, or
// tests and CI logs become unstable.
func (c *Collector) Diagnostics() []Diagnostic {
	out := make([]Diagnostic, len(c.diags))
	copy(out, c.diags)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Check < b.Check
	})
	return out
}

func (c *Collector) HasErrors() bool {
	for _, d := range c.diags {
		if d.Severity == SevError {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/diag/ -v`
Expected: PASS — all three tests

- [ ] **Step 6: Commit**

```bash
git add internal/diag/ go.mod go.sum
git commit -m "feat: diagnostic collector with deterministic ordering"
```

---

## Task 3: Entity types

**Files:**
- Create: `internal/catalog/entity.go`
- Test: `internal/catalog/entity_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Kind string` with constants `KindService`, `KindWorker`, `KindCron`, `KindLibrary`, `KindTopic`, `KindDatabase`, `KindAPI`; `func (Kind) Valid() bool`; `var AllKinds []Kind`
  - `type Entity struct` with fields `APIVersion string`, `Kind Kind`, `Metadata Metadata`, `Spec Spec`, and non-YAML provenance `SourceRepo, SourcePath string`, `NameLine int`
  - `type Metadata struct { Name, Description, Owner string; Tier int; Lifecycle string; Tags []string }`
  - `type Spec struct` — see code
  - `const APIVersion = "landsraad/v1"`

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/entity_test.go`:

```go
package catalog

import "testing"

func TestKindValid(t *testing.T) {
	for _, k := range AllKinds {
		if !k.Valid() {
			t.Errorf("%q is in AllKinds but reports invalid", k)
		}
	}
	for _, bad := range []Kind{"", "service", "SERVICE", "Widget"} {
		if bad.Valid() {
			t.Errorf("%q must be invalid — kinds are case-sensitive", bad)
		}
	}
}

func TestAPIVersionConstant(t *testing.T) {
	if APIVersion != "landsraad/v1" {
		t.Errorf("APIVersion = %q, want %q", APIVersion, "landsraad/v1")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/catalog/ -v`
Expected: FAIL — `undefined: AllKinds`

- [ ] **Step 3: Write the implementation**

Create `internal/catalog/entity.go`:

```go
// Package catalog holds the entity model and the pipeline stages that turn
// service.yaml files into a validated, resolved catalog.
package catalog

// APIVersion is the only apiVersion this release accepts. It carries no
// domain, matching Kubernetes core groups such as apps/v1.
const APIVersion = "landsraad/v1"

// Kind is the type of a catalog entity. Kinds are case-sensitive.
type Kind string

const (
	KindService  Kind = "Service"
	KindWorker   Kind = "Worker"
	KindCron     Kind = "Cron"
	KindLibrary  Kind = "Library"
	KindTopic    Kind = "Topic"
	KindDatabase Kind = "Database"
	KindAPI      Kind = "API"
	// KindResource is the escape hatch: an S3 bucket, an SQS queue, a Redis
	// cache, a Terraform module. Without it users model those as Database or
	// Topic — a lie that then flows into the tier matrix, the dependency
	// graph and the portal. Backstage converged on the same answer: a small
	// kind set plus a free-string spec.type.
	KindResource Kind = "Resource"
)

// AllKinds is the complete set, used by validation and by the JSON Schema test.
var AllKinds = []Kind{
	KindService, KindWorker, KindCron, KindLibrary,
	KindTopic, KindDatabase, KindAPI, KindResource,
}

func (k Kind) Valid() bool {
	for _, known := range AllKinds {
		if k == known {
			return true
		}
	}
	return false
}

// Metadata is the identity and ownership block.
type Metadata struct {
	Name string `yaml:"name"`
	// Aliases are former names. References resolve through them, so renaming
	// an entity is additive rather than a break for every repo that depends
	// on it — and the scorecard time series survives the rename.
	Aliases     []string          `yaml:"aliases"`
	Description string            `yaml:"description"`
	Owner       string            `yaml:"owner"`
	Tier        int               `yaml:"tier"`
	Lifecycle   string            `yaml:"lifecycle"`
	Tags        []string          `yaml:"tags"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

// Link is one external destination: dashboard, deploy, traces, and so on.
type Link struct {
	Title string `yaml:"title"`
	URL   string `yaml:"url"`
	Type  string `yaml:"type"`
}

// SLO is one service level objective.
type SLO struct {
	Name   string `yaml:"name"`
	Target string `yaml:"target"`
	Window string `yaml:"window"`
}

// Runtime carries the selector used to match live workloads. It is unused in
// this plan; the runtime agent is a later sub-project.
type Runtime struct {
	Selector map[string]string `yaml:"selector"`
}

// Exemption waives one scorecard check with a stated reason. Without this a
// tier-1 nightly backfill that genuinely has no runbook fails forever, and the
// team's only lever is to lie about its tier — corrupting the dataset the
// whole product is built on.
type Exemption struct {
	Check  string `yaml:"check"`
	Reason string `yaml:"reason"`
	Until  string `yaml:"until"`
}

// Spec is the operational block.
type Spec struct {
	// Type is a free string, deliberately unconstrained, matching Backstage's
	// spec.type. It is how a Resource says what kind of resource it is.
	Type         string      `yaml:"type"`
	Language     string      `yaml:"language"`
	Path         string      `yaml:"path"`
	Docs         string      `yaml:"docs"`
	Runbook      string      `yaml:"runbook"`
	Oncall       string      `yaml:"oncall"`
	RepoURL      string      `yaml:"repoUrl"`
	Links        []Link      `yaml:"links"`
	DependsOn    []string    `yaml:"dependsOn"`
	ProvidesApis []string    `yaml:"providesApis"`
	SLO          []SLO       `yaml:"slo"`
	Exemptions   []Exemption `yaml:"exemptions"`
	Alerts       string      `yaml:"alerts"`
	Runtime      *Runtime    `yaml:"runtime"`
}

// Entity is one parsed service.yaml.
//
// SourceRepo, SourcePath and NameLine are provenance: they are not present in
// the file, they are attached at parse time. They exist so a collision or a
// dangling reference can name both sides with a file and a line.
type Entity struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       Kind     `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`

	SourceRepo string `yaml:"-"`
	SourcePath string `yaml:"-"`
	NameLine   int    `yaml:"-"`
}

// Ref returns this entity's canonical reference, e.g. "service:ledger-api".
func (e *Entity) Ref() Ref {
	return Ref{Kind: e.Kind, Name: e.Metadata.Name}
}

// Location renders where this entity came from: "repo:path" when the repo is
// known, "path" when it is not. `landsraad validate` runs inside one repo and
// often has no name for it, and "…in :services/api/service.yaml" is worse
// than no prefix at all.
func (e *Entity) Location() string {
	if e.SourceRepo == "" {
		return e.SourcePath
	}
	return e.SourceRepo + ":" + e.SourcePath
}
```

- [ ] **Step 4: Run it to verify it fails differently**

Run: `go test ./internal/catalog/ -v`
Expected: FAIL — `undefined: Ref`. This is expected; `Ref` arrives in Task 5. Temporarily comment out the `Ref()` method to get a green bar, and uncomment it in Task 5 Step 3.

- [ ] **Step 5: Comment out `Ref()`, run, verify it passes**

Run: `go test ./internal/catalog/ -v`
Expected: PASS — both tests

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/entity.go internal/catalog/entity_test.go
git commit -m "feat: catalog entity types"
```

---

## Task 4: Parse YAML while keeping line numbers

The whole diagnostic design depends on this. `yaml.Unmarshal` into a struct throws line numbers away, so parse into a `yaml.Node` first, then decode.

**Files:**
- Create: `internal/catalog/parse.go`
- Test: `internal/catalog/parse_test.go`

**Interfaces:**
- Consumes: `diag.Collector`, `Entity` (Task 3).
- Produces:
  - `func ParseFile(repo, path string, data []byte, c *diag.Collector) (*Entity, bool)` — returns `nil, false` when the file cannot be parsed at all
  - `func fieldLine(root *yaml.Node, path ...string) int` — unexported, returns 0 when not found

- [ ] **Step 1: Add the dependency**

```bash
go get gopkg.in/yaml.v3@v3.0.1
```

- [ ] **Step 2: Write the failing test**

Create `internal/catalog/parse_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

const validYAML = `apiVersion: landsraad/v1
kind: Service
metadata:
  name: payments-worker
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  language: go
  path: services/payments-worker
  dependsOn:
    - topic:payments.events
`

func TestParseFileReadsFields(t *testing.T) {
	var c diag.Collector
	e, ok := ParseFile("monorepo", "services/payments-worker/service.yaml", []byte(validYAML), &c)
	if !ok {
		t.Fatalf("ParseFile failed, diagnostics: %+v", c.Diagnostics())
	}
	if e.Metadata.Name != "payments-worker" {
		t.Errorf("Name = %q, want %q", e.Metadata.Name, "payments-worker")
	}
	if e.Kind != KindService {
		t.Errorf("Kind = %q, want %q", e.Kind, KindService)
	}
	if e.Metadata.Tier != 1 {
		t.Errorf("Tier = %d, want 1", e.Metadata.Tier)
	}
	if len(e.Spec.DependsOn) != 1 || e.Spec.DependsOn[0] != "topic:payments.events" {
		t.Errorf("DependsOn = %v, want [topic:payments.events]", e.Spec.DependsOn)
	}
}

func TestParseFileAttachesProvenance(t *testing.T) {
	var c diag.Collector
	e, ok := ParseFile("monorepo", "services/payments-worker/service.yaml", []byte(validYAML), &c)
	if !ok {
		t.Fatal("ParseFile failed")
	}
	if e.SourceRepo != "monorepo" {
		t.Errorf("SourceRepo = %q, want %q", e.SourceRepo, "monorepo")
	}
	if e.SourcePath != "services/payments-worker/service.yaml" {
		t.Errorf("SourcePath = %q", e.SourcePath)
	}
	// metadata.name is on line 4 of validYAML (1-indexed).
	if e.NameLine != 4 {
		t.Errorf("NameLine = %d, want 4 — line numbers are the point of this task", e.NameLine)
	}
}

func TestParseFileReportsMalformedYAML(t *testing.T) {
	var c diag.Collector
	_, ok := ParseFile("monorepo", "broken/service.yaml", []byte("kind: Service\n  bad: indent\n"), &c)
	if ok {
		t.Fatal("malformed YAML must not parse successfully")
	}
	if !c.HasErrors() {
		t.Fatal("malformed YAML must produce an error diagnostic")
	}
	got := c.Diagnostics()[0]
	if got.File != "broken/service.yaml" {
		t.Errorf("File = %q, want %q", got.File, "broken/service.yaml")
	}
	if got.Line == 0 {
		t.Error("a YAML syntax error must carry a line number")
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/catalog/ -run TestParseFile -v`
Expected: FAIL — `undefined: ParseFile`

- [ ] **Step 4: Write the implementation**

Create `internal/catalog/parse.go`:

```go
package catalog

import (
	"fmt"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// yamlLineRE pulls the line number out of a yaml.v3 error string, which has
// the form "yaml: line 3: mapping values are not allowed in this context".
var yamlLineRE = regexp.MustCompile(`line (\d+):`)

// ParseFile turns one service.yaml into an Entity, attaching provenance and
// the line number of metadata.name. It reports at most one diagnostic and
// returns ok=false only when the bytes are not YAML at all — structural
// validation is a separate stage (see internal/schema).
func ParseFile(repo, path string, data []byte, c *diag.Collector) (*Entity, bool) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     lineFromYAMLError(err),
			Check:    "yaml-parse",
			Message:  fmt.Sprintf("cannot parse YAML: %v", err),
		})
		return nil, false
	}

	// An empty file unmarshals into a zero Node with no content.
	if root.Kind == 0 || len(root.Content) == 0 {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     1,
			Check:    "yaml-parse",
			Message:  "file is empty",
			Hint:     "a service.yaml needs at least apiVersion, kind and metadata.name",
		})
		return nil, false
	}

	var e Entity
	if err := root.Decode(&e); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     lineFromYAMLError(err),
			Check:    "yaml-decode",
			Message:  fmt.Sprintf("cannot read as a catalog entity: %v", err),
		})
		return nil, false
	}

	e.SourceRepo = repo
	e.SourcePath = path
	e.NameLine = fieldLine(&root, "metadata", "name")
	if e.NameLine == 0 {
		e.NameLine = 1
	}
	return &e, true
}

// lineFromYAMLError extracts a 1-indexed line from a yaml.v3 error, falling
// back to 1 so no diagnostic is ever emitted without a line.
func lineFromYAMLError(err error) int {
	if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			return n
		}
	}
	return 1
}

// fieldLine walks a document node down the given key path and returns the
// 1-indexed line of that key's value, or 0 when the path does not exist.
func fieldLine(root *yaml.Node, path ...string) int {
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, key := range path {
		if node.Kind != yaml.MappingNode {
			return 0
		}
		found := false
		// Mapping content alternates key, value, key, value.
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				node = node.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return 0
		}
	}
	return node.Line
}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/catalog/ -run TestParseFile -v`
Expected: PASS — all three tests

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/parse.go internal/catalog/parse_test.go go.mod go.sum
git commit -m "feat: parse service.yaml preserving line numbers"
```

---

## Task 5: References, catalog, and collision detection

**Files:**
- Create: `internal/catalog/ref.go`, `internal/catalog/merge.go`
- Modify: `internal/catalog/entity.go` — uncomment `Ref()` from Task 3 Step 4
- Test: `internal/catalog/ref_test.go`, `internal/catalog/merge_test.go`

**Interfaces:**
- Consumes: `Entity` (Task 3), `diag.Collector` (Task 2).
- Produces:
  - `type Ref struct { Kind Kind; Name string }`; `func ParseRef(string) (Ref, error)`; `func (Ref) String() string`
  - `type Catalog struct { Entities []*Entity; byRef map[Ref]*Entity }`
  - `func NewCatalog(entities []*Entity, c *diag.Collector) *Catalog` — detects collisions
  - `func (*Catalog) Lookup(Ref) (*Entity, bool)`

- [ ] **Step 1: Write the failing ref test**

Create `internal/catalog/ref_test.go`:

```go
package catalog

import "testing"

func TestParseRef(t *testing.T) {
	cases := []struct {
		in      string
		want    Ref
		wantErr bool
	}{
		{in: "service:ledger-api", want: Ref{Kind: KindService, Name: "ledger-api"}},
		{in: "topic:payments.events", want: Ref{Kind: KindTopic, Name: "payments.events"}},
		{in: "database:payments-pg", want: Ref{Kind: KindDatabase, Name: "payments-pg"}},
		{in: "api:billing", want: Ref{Kind: KindAPI, Name: "billing"}},
		{in: "Service:ledger-api", want: Ref{Kind: KindService, Name: "ledger-api"}},
		{in: "noprefix", wantErr: true},
		{in: "widget:thing", wantErr: true},
		{in: "service:", wantErr: true},
		{in: ":ledger-api", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := ParseRef(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseRef(%q) = %v, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseRef(%q) returned error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseRef(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestRefStringIsLowercasePrefixed(t *testing.T) {
	r := Ref{Kind: KindService, Name: "ledger-api"}
	if got := r.String(); got != "service:ledger-api" {
		t.Errorf("String() = %q, want %q", got, "service:ledger-api")
	}
}

func TestRefRoundTrips(t *testing.T) {
	for _, k := range AllKinds {
		in := Ref{Kind: k, Name: "thing"}
		out, err := ParseRef(in.String())
		if err != nil {
			t.Errorf("round trip of %v failed: %v", in, err)
			continue
		}
		if out != in {
			t.Errorf("round trip of %v gave %v", in, out)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/catalog/ -run TestParseRef -v`
Expected: FAIL — `undefined: ParseRef`

- [ ] **Step 3: Write `ref.go` and uncomment `Entity.Ref()`**

Create `internal/catalog/ref.go`:

```go
package catalog

import (
	"fmt"
	"strings"
)

// Ref identifies one catalog entity. Names are flat and globally unique, so a
// Ref is a kind plus a name and nothing more. Written as "service:ledger-api".
type Ref struct {
	Kind Kind
	Name string
}

// String renders the canonical form, with a lowercase kind prefix.
func (r Ref) String() string {
	return strings.ToLower(string(r.Kind)) + ":" + r.Name
}

// ParseRef reads "kind:name". The kind prefix is case-insensitive so both
// "service:x" and "Service:x" work, but the name is taken verbatim.
func ParseRef(s string) (Ref, error) {
	kindStr, name, found := strings.Cut(s, ":")
	if !found {
		return Ref{}, fmt.Errorf("reference %q has no kind prefix, want the form kind:name (for example service:ledger-api)", s)
	}
	if name == "" {
		return Ref{}, fmt.Errorf("reference %q has an empty name", s)
	}
	if kindStr == "" {
		return Ref{}, fmt.Errorf("reference %q has an empty kind", s)
	}
	for _, k := range AllKinds {
		if strings.EqualFold(kindStr, string(k)) {
			return Ref{Kind: k, Name: name}, nil
		}
	}
	return Ref{}, fmt.Errorf("reference %q has unknown kind %q", s, kindStr)
}
```

Then in `internal/catalog/entity.go`, uncomment the `Ref()` method added in Task 3.

- [ ] **Step 4: Run the ref tests to verify they pass**

Run: `go test ./internal/catalog/ -run 'TestParseRef|TestRef' -v`
Expected: PASS

- [ ] **Step 5: Write the failing merge test**

Create `internal/catalog/merge_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func ent(repo, path, name string, kind Kind, line int) *Entity {
	e := &Entity{Kind: kind}
	e.Metadata.Name = name
	e.SourceRepo = repo
	e.SourcePath = path
	e.NameLine = line
	return e
}

func TestNewCatalogIndexesByRef(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		ent("monorepo", "services/a/service.yaml", "a", KindService, 4),
		ent("monorepo", "topics/t/service.yaml", "payments.events", KindTopic, 4),
	}, &c)

	if c.HasErrors() {
		t.Fatalf("unexpected errors: %+v", c.Diagnostics())
	}
	if _, ok := cat.Lookup(Ref{Kind: KindService, Name: "a"}); !ok {
		t.Error("service:a must be found")
	}
	if _, ok := cat.Lookup(Ref{Kind: KindTopic, Name: "payments.events"}); !ok {
		t.Error("topic:payments.events must be found")
	}
	if _, ok := cat.Lookup(Ref{Kind: KindService, Name: "missing"}); ok {
		t.Error("service:missing must not be found")
	}
}

func TestNewCatalogRejectsDuplicateNames(t *testing.T) {
	var c diag.Collector
	NewCatalog([]*Entity{
		ent("monorepo", "services/api/service.yaml", "api", KindService, 4),
		ent("edge-gateway", "service.yaml", "api", KindService, 4),
	}, &c)

	if !c.HasErrors() {
		t.Fatal("a duplicate name must be an error")
	}
	// Spec §14: assert the EXACT string. Error message quality is the product,
	// so a wording regression must fail a test rather than slip through a
	// substring check.
	// NewCatalog sorts by (repo, path) so the winner of a collision is
	// deterministic and does not depend on filesystem walk order.
	// "edge-gateway" < "monorepo", so edge-gateway holds the index slot.
	got := c.Diagnostics()[0].Message
	want := `duplicate entity name "api": already defined as service:api in ` +
		`edge-gateway:service.yaml (line 4), redefined in ` +
		`monorepo:services/api/service.yaml`
	if got != want {
		t.Errorf("collision message\n got: %s\nwant: %s", got, want)
	}
}

func TestNewCatalogAllowsSameNameDifferentKind(t *testing.T) {
	var c diag.Collector
	NewCatalog([]*Entity{
		ent("monorepo", "services/orders/service.yaml", "orders", KindService, 4),
		ent("monorepo", "topics/orders/service.yaml", "orders", KindTopic, 4),
	}, &c)

	if c.HasErrors() {
		t.Errorf("service:orders and topic:orders are distinct refs and must coexist: %+v", c.Diagnostics())
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/catalog/ -run TestNewCatalog -v`
Expected: FAIL — `undefined: NewCatalog`

- [ ] **Step 7: Write `merge.go`**

Create `internal/catalog/merge.go`:

```go
package catalog

import (
	"fmt"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Catalog is the merged set of entities from every source repo.
type Catalog struct {
	Entities []*Entity
	byRef    map[Ref]*Entity
}

// NewCatalog merges entities into one catalog, reporting a collision when two
// entities share a kind and a name. Names are flat and globally unique, so a
// collision is a hard error rather than something to resolve silently. The
// first entity encountered wins the index slot so later stages have something
// to resolve against; the diagnostic is what stops the build.
func NewCatalog(entities []*Entity, c *diag.Collector) *Catalog {
	// Sort by file path so which entity "wins" a collision is deterministic
	// and does not depend on filesystem walk order.
	sorted := make([]*Entity, len(entities))
	copy(sorted, entities)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].SourceRepo != sorted[j].SourceRepo {
			return sorted[i].SourceRepo < sorted[j].SourceRepo
		}
		return sorted[i].SourcePath < sorted[j].SourcePath
	})

	cat := &Catalog{
		Entities: sorted,
		byRef:    make(map[Ref]*Entity, len(sorted)),
	}
	for _, e := range sorted {
		ref := e.Ref()
		if prev, exists := cat.byRef[ref]; exists {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError,
				Repo:     e.SourceRepo,
				File:     e.SourcePath,
				Line:     e.NameLine,
				Entity:   e.Metadata.Name,
				Check:    "duplicate-name",
				Message: fmt.Sprintf(
					"duplicate entity name %q: already defined as %s in %s (line %d), redefined in %s",
					e.Metadata.Name, ref, prev.Location(), prev.NameLine, e.Location()),
				Hint: "names must be unique across the merged catalog; rename one of them",
			})
			continue
		}
		cat.byRef[ref] = e
	}
	return cat
}

// Lookup finds an entity by reference.
func (c *Catalog) Lookup(r Ref) (*Entity, bool) {
	e, ok := c.byRef[r]
	return e, ok
}
```

- [ ] **Step 8: Run the whole package to verify it passes**

Run: `go test ./internal/catalog/ -v`
Expected: PASS — every test in the package

- [ ] **Step 9: Commit**

```bash
git add internal/catalog/
git commit -m "feat: entity references and catalog merge with collision detection"
```

---

## Task 6: Dependency graph and cycle detection

**Files:**
- Create: `internal/catalog/graph.go`
- Test: `internal/catalog/graph_test.go`

**Interfaces:**
- Consumes: `Catalog`, `Ref`, `diag.Collector`.
- Produces:
  - `type Scope int` with `FullCatalog` and `LocalOnly`
  - `type Graph`; `func (c *Catalog) Resolve(scope Scope, col *diag.Collector) *Graph` — resolves `dependsOn` and `providesApis` and returns the graph. Under `LocalOnly`, references to entities absent from the catalog are recorded rather than reported (a service repo cannot see other repos)
  - `func (g *Graph) DependsOn(r Ref) []Ref` — forward edges, sorted
  - `func (g *Graph) Dependents(r Ref) []Ref` — reverse edges, sorted
  - `type Cycle []Ref`; `func (g *Graph) Cycles() []Cycle`

`Cycles` lives on `Graph`, not `Catalog`, so it cannot be called before
`Resolve`. Spec §3.1 claims an ordering bug is a compile error rather than a
runtime nil; with the edges on `Catalog` that claim was false for this exact
pair — `cat.Cycles()` would have compiled and silently returned zero cycles.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/graph_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func entDeps(name string, kind Kind, deps ...string) *Entity {
	e := ent("monorepo", "services/"+name+"/service.yaml", name, kind, 4)
	e.Spec.DependsOn = deps
	return e
}

func TestResolveReportsDanglingRefs(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:nowhere"),
	}, &c)
	cat.Resolve(FullCatalog, &c)

	if !c.HasErrors() {
		t.Fatal("a dangling reference must be an error when resolving the full catalog")
	}
	got := c.Diagnostics()[0].Message
	if want := "service:a dependsOn service:nowhere, which is not in the catalog"; got != want {
		t.Errorf("dangling-ref message\n got: %s\nwant: %s", got, want)
	}
}

func TestResolveToleratesDanglingRefsWhenLocal(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:in-another-repo"),
	}, &c)
	cat.Resolve(LocalOnly, &c)

	if c.HasErrors() {
		t.Errorf("`landsraad validate` runs on one repo and cannot see the others; "+
			"cross-repo refs must not be errors there. Got: %+v", c.Diagnostics())
	}
}

func TestResolveReportsMalformedRefs(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{entDeps("a", KindService, "noprefix")}, &c)
	cat.Resolve(LocalOnly, &c)

	if !c.HasErrors() {
		t.Fatal("a malformed reference is an error even in local mode — it can never resolve")
	}
}

func TestCyclesDetectsASimpleCycle(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:b"),
		entDeps("b", KindService, "service:a"),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	cycles := g.Cycles()
	if len(cycles) != 1 {
		t.Fatalf("a <-> b is one cycle, got %d: %v", len(cycles), cycles)
	}
	// Assert the members, not just the count: a stub that returns any
	// non-empty slice would satisfy a length check.
	members := map[string]bool{}
	for _, r := range cycles[0] {
		members[r.String()] = true
	}
	if len(members) != 2 || !members["service:a"] || !members["service:b"] {
		t.Errorf("cycle members = %v, want service:a and service:b", cycles[0])
	}
}

func TestResolveValidatesProvidesApis(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/a/service.yaml", "a", KindService, 4)
	e.Spec.ProvidesApis = []string{"api:typo"}
	cat := NewCatalog([]*Entity{e}, &c)
	cat.Resolve(FullCatalog, &c)

	if !c.HasErrors() {
		t.Fatal("a providesApis typo must be caught now, not silently accepted " +
			"until a later release starts resolving the field")
	}
	if got := c.Diagnostics()[0].Message; got != "service:a providesApis api:typo, which is not in the catalog" {
		t.Errorf("message = %q", got)
	}
}

// providesApis is validated but must NOT become a dependency edge.
func TestProvidesApisIsNotADependencyEdge(t *testing.T) {
	var c diag.Collector
	svc := ent("monorepo", "services/a/service.yaml", "a", KindService, 4)
	svc.Spec.ProvidesApis = []string{"api:billing"}
	api := ent("monorepo", "apis/billing/service.yaml", "billing", KindAPI, 4)
	cat := NewCatalog([]*Entity{svc, api}, &c)
	g := cat.Resolve(FullCatalog, &c)

	if c.HasErrors() {
		t.Fatalf("both entities exist: %+v", c.Diagnostics())
	}
	if got := g.DependsOn(Ref{Kind: KindService, Name: "a"}); len(got) != 0 {
		t.Errorf("providesApis must not create a dependency edge, got %v", got)
	}
}

func TestCyclesDetectsASelfDependency(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{entDeps("a", KindService, "service:a")}, &c)
	g := cat.Resolve(FullCatalog, &c)

	cycles := g.Cycles()
	if len(cycles) != 1 || len(cycles[0]) != 1 || cycles[0][0].String() != "service:a" {
		t.Errorf("a service depending on itself is a cycle, got %v", cycles)
	}
}

func TestCyclesDetectsALongerChain(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:b"),
		entDeps("b", KindService, "service:c"),
		entDeps("c", KindService, "service:a"),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	cycles := g.Cycles()
	if len(cycles) != 1 || len(cycles[0]) != 3 {
		t.Errorf("a -> b -> c -> a is one 3-node cycle, got %v", cycles)
	}
}

// DependsOn is the forward-edge accessor the portal's dependency graph needs.
func TestDependsOnReturnsForwardEdges(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:b", "service:c"),
		entDeps("b", KindService),
		entDeps("c", KindService),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	got := g.DependsOn(Ref{Kind: KindService, Name: "a"})
	if len(got) != 2 || got[0].String() != "service:b" || got[1].String() != "service:c" {
		t.Errorf("DependsOn must return sorted forward edges, got %v", got)
	}
}

func TestCyclesIgnoresADiamond(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:b", "service:c"),
		entDeps("b", KindService, "service:d"),
		entDeps("c", KindService, "service:d"),
		entDeps("d", KindService),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	if cycles := g.Cycles(); len(cycles) != 0 {
		t.Errorf("a diamond is not a cycle, got %v", cycles)
	}
}

func TestDependentsReturnsReverseEdges(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "topic:t"),
		entDeps("b", KindService, "topic:t"),
		ent("monorepo", "topics/t/service.yaml", "t", KindTopic, 4),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	got := g.Dependents(Ref{Kind: KindTopic, Name: "t"})
	if len(got) != 2 {
		t.Fatalf("topic:t has two consumers, got %d: %v", len(got), got)
	}
	if got[0].String() != "service:a" || got[1].String() != "service:b" {
		t.Errorf("Dependents must be sorted, got %v", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/catalog/ -run 'TestResolve|TestCycles|TestDependents' -v`
Expected: FAIL — `cat.Resolve undefined`

- [ ] **Step 3: Write the implementation**

Create `internal/catalog/graph.go`:

```go
package catalog

import (
	"fmt"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Cycle is a dependency loop, listed in traversal order.
type Cycle []Ref

// Scope says how much of the world the caller can see.
type Scope int

const (
	// FullCatalog means every entity is present, so an unresolvable
	// reference is a real error. The platform build uses this.
	FullCatalog Scope = iota
	// LocalOnly means the caller is validating a single repo and cannot see
	// entities defined elsewhere. `landsraad validate` uses this.
	LocalOnly
)

// Navigator — route-finding over the dependency graph.
//
// Graph is produced BY Resolve and holds the resolved edges. Keeping the edges
// here rather than on Catalog is what makes the ordering constraint a compile
// error: there is no cat.Cycles() to call before resolving, so the silent
// "zero cycles" answer from an unresolved catalog cannot happen.
type Graph struct {
	order   []*Entity
	edges   map[Ref][]Ref
	reverse map[Ref][]Ref
}

// Resolve walks every reference and returns the resolved graph.
//
// Under LocalOnly an unresolvable reference is recorded and skipped rather
// than reported, because the target may simply live in another repo.
// Malformed references are errors under either scope: they could never
// resolve anywhere.
func (c *Catalog) Resolve(scope Scope, col *diag.Collector) *Graph {
	g := &Graph{
		order:   c.Entities,
		edges:   make(map[Ref][]Ref, len(c.Entities)),
		reverse: make(map[Ref][]Ref, len(c.Entities)),
	}
	for _, e := range c.Entities {
		from := e.Ref()
		c.resolveRefs(g, e, from, e.Spec.DependsOn, "dependsOn", scope, col)
		c.resolveRefs(g, e, from, e.Spec.ProvidesApis, "providesApis", scope, col)
	}
	for k := range g.edges {
		sortRefs(g.edges[k])
	}
	for k := range g.reverse {
		sortRefs(g.reverse[k])
	}
	return g
}

// resolveRefs validates one reference list and records its edges.
//
// providesApis goes through the same path as dependsOn: it was previously
// accepted and never resolved, so `providesApis: [api:bling]` validated green
// today and would have become a hard failure the day a later release started
// resolving it — breaking repos that had been green for months.
func (c *Catalog) resolveRefs(g *Graph, e *Entity, from Ref, raws []string, field string, scope Scope, col *diag.Collector) {
	for _, raw := range raws {
		to, err := ParseRef(raw)
		if err != nil {
			col.Add(diag.Diagnostic{
				Severity: diag.SevError,
				Repo:     e.SourceRepo,
				File:     e.SourcePath,
				Line:     e.NameLine,
				Entity:   e.Metadata.Name,
				Check:    "malformed-ref",
				Message:  fmt.Sprintf("%s: %v", field, err),
				Hint:     "references look like service:ledger-api or topic:payments.events",
			})
			continue
		}
		if _, found := c.Lookup(to); !found {
			if scope == FullCatalog {
				col.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "dangling-ref",
					Message: fmt.Sprintf("%s %s %s, which is not in the catalog",
						from, field, to),
					Hint: "check the spelling, or add the missing entity",
				})
			}
			// Under LocalOnly the target lives in another repo. Record no
			// edge: the platform build resolves it.
			continue
		}
		// providesApis is validated but is not a dependency: recording it as
		// one would put a false edge in the graph the portal renders.
		if field == "dependsOn" {
			g.edges[from] = append(g.edges[from], to)
			g.reverse[to] = append(g.reverse[to], from)
		}
	}
}

func sortRefs(rs []Ref) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].String() < rs[j].String() })
}

// DependsOn returns the resolved outgoing edges, sorted.
func (g *Graph) DependsOn(r Ref) []Ref { return g.edges[r] }

// Dependents returns everything that depends on r, sorted. This is what
// answers "who consumes this topic?" on an entity page.
func (g *Graph) Dependents(r Ref) []Ref { return g.reverse[r] }

// Cycles returns every dependency loop, using a depth-first search with a
// recursion stack. Each cycle is reported once.
func (g *Graph) Cycles() []Cycle {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // finished
	)
	state := make(map[Ref]int, len(g.order))
	var path []Ref
	var found []Cycle
	seen := make(map[string]bool)

	var visit func(Ref)
	visit = func(r Ref) {
		state[r] = grey
		path = append(path, r)
		for _, next := range g.edges[r] {
			switch state[next] {
			case white:
				visit(next)
			case grey:
				// Found a loop: take the path back to where next appears.
				for i, pr := range path {
					if pr == next {
						cyc := append(Cycle{}, path[i:]...)
						if key := cycleKey(cyc); !seen[key] {
							seen[key] = true
							found = append(found, cyc)
						}
						break
					}
				}
			}
		}
		path = path[:len(path)-1]
		state[r] = black
	}

	// Iterate entities in their sorted order so output is deterministic.
	for _, e := range g.order {
		if state[e.Ref()] == white {
			visit(e.Ref())
		}
	}
	return found
}

// cycleKey builds an order-independent identity for a cycle so the same loop
// discovered from two entry points is only reported once.
func cycleKey(cyc Cycle) string {
	parts := make([]string, len(cyc))
	for i, r := range cyc {
		parts[i] = r.String()
	}
	sort.Strings(parts)
	key := ""
	for _, p := range parts {
		key += p + "|"
	}
	return key
}
```


- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/catalog/ -v`
Expected: PASS — every test, including the graph tests

- [ ] **Step 5: Commit**

```bash
git add internal/catalog/
git commit -m "feat: dependency graph resolution and cycle detection"
```

---

## Task 7: JSON Schema validation

The schema is the single source of truth for structure, and it ships to editors.
Validating against it — rather than hand-writing field checks in Go — is what
stops the schema and the tool's behaviour from drifting apart.

Per spec §3.1, validation is a method on a `*Validator` **value**, not a package
function over `sync.Once` state. Two schema versions can coexist during a
migration, a test can validate against a schema of its own, and there is no
process-wide state to reason about.

**Files:**
- Create: `internal/schema/service.schema.json`, `internal/schema/schema.go`
- Test: `internal/schema/schema_test.go`

**Interfaces:**
- Consumes: `diag.Collector` (Task 2), `catalog.AllKinds` (Task 3).
- Produces:
  - `var Raw []byte` — the embedded schema, printed by `landsraad schema`
  - `type Validator struct{ ... }`
  - `func New(schemaJSON []byte) (*Validator, error)` — compile any schema
  - `func Default() (*Validator, error)` — compile the embedded schema
  - `func (v *Validator) Validate(repo, path string, data []byte, c *diag.Collector) bool`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/santhosh-tekuri/jsonschema/v6@v6.0.1
```

- [ ] **Step 2: Create `internal/schema/service.schema.json`**

The canonical schema lives beside the Go file that embeds it, because `go:embed`
cannot reference a parent directory. The root `schema/service.schema.json` that
editors point at is generated from it by `task schema`.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/landsraadhq/landsraad/main/schema/service.schema.json",
  "title": "landsraad catalog entity",
  "type": "object",
  "unevaluatedProperties": false,
  "required": ["apiVersion", "kind", "metadata"],
  "properties": {
    "apiVersion": { "const": "landsraad/v1" },
    "kind": {
      "enum": ["Service", "Worker", "Cron", "Library", "Topic", "Database", "API", "Resource"]
    },
    "metadata": {
      "type": "object",
      "unevaluatedProperties": false,
      "required": ["name", "owner", "lifecycle"],
      "properties": {
        "name": {
          "type": "string",
          "pattern": "^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$",
          "maxLength": 63
        },
        "aliases": {
          "type": "array",
          "items": { "type": "string", "pattern": "^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$" }
        },
        "description": { "type": "string" },
        "owner": { "type": "string", "minLength": 1 },
        "tier": { "type": "integer", "enum": [1, 2, 3] },
        "lifecycle": { "enum": ["experimental", "production", "deprecated"] },
        "tags": { "type": "array", "items": { "type": "string" } },
        "labels": { "type": "object", "additionalProperties": { "type": "string" } },
        "annotations": { "type": "object", "additionalProperties": { "type": "string" } }
      }
    },
    "spec": {
      "type": "object",
      "unevaluatedProperties": false,
      "properties": {
        "type": { "type": "string" },
        "language": { "type": "string" },
        "path": { "type": "string" },
        "docs": { "type": "string" },
        "runbook": { "type": "string" },
        "oncall": { "type": "string" },
        "repoUrl": { "type": "string" },
        "links": {
          "type": "array",
          "items": {
            "type": "object",
            "unevaluatedProperties": false,
            "required": ["title", "url"],
            "properties": {
              "title": { "type": "string" },
              "url": { "type": "string" },
              "type": { "type": "string" }
            }
          }
        },
        "dependsOn": { "type": "array", "items": { "type": "string" } },
        "providesApis": { "type": "array", "items": { "type": "string" } },
        "slo": {
          "type": "array",
          "items": {
            "type": "object",
            "unevaluatedProperties": false,
            "required": ["name", "target"],
            "properties": {
              "name": { "type": "string" },
              "target": { "type": "string" },
              "window": { "type": "string" }
            }
          }
        },
        "exemptions": {
          "type": "array",
          "items": {
            "type": "object",
            "unevaluatedProperties": false,
            "required": ["check", "reason"],
            "properties": {
              "check": { "type": "string" },
              "reason": { "type": "string", "minLength": 1 },
              "until": { "type": "string", "format": "date" }
            }
          }
        },
        "alerts": { "type": "string" },
        "runtime": {
          "type": "object",
          "unevaluatedProperties": false,
          "properties": {
            "selector": {
              "type": "object",
              "additionalProperties": { "type": "string" }
            }
          }
        }
      }
    }
  },
  "allOf": [
    {
      "$comment": "tier is required only for things that can page someone. A Kafka topic's criticality is derived from its consumers; a library has none.",
      "if": {
        "required": ["kind"],
        "properties": { "kind": { "enum": ["Service", "Worker", "Cron", "API"] } }
      },
      "then": {
        "properties": { "metadata": { "required": ["tier"] } }
      }
    }
  ]
}
```

- [ ] **Step 3: Write the failing test**

Create `internal/schema/schema_test.go`:

```go
package schema

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

const good = `apiVersion: landsraad/v1
kind: Service
metadata:
  name: payments-worker
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  language: go
`

func mustDefault(t *testing.T) *Validator {
	t.Helper()
	v, err := Default()
	if err != nil {
		t.Fatalf("the embedded schema must compile: %v", err)
	}
	return v
}

func TestValidateAcceptsAGoodFile(t *testing.T) {
	var c diag.Collector
	if !mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(good), &c) {
		t.Fatalf("a valid file must pass: %+v", c.Diagnostics())
	}
}

func TestValidateRejectsUnknownFields(t *testing.T) {
	in := strings.Replace(good, "  language: go\n", "  language: go\n  nonsense: yes\n", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("unevaluatedProperties is false — an unknown field must be rejected, not ignored")
	}
	if !c.HasErrors() {
		t.Fatal("rejection must produce an error diagnostic")
	}
	if c.Diagnostics()[0].Line == 0 {
		t.Error("a schema violation must carry a line number")
	}
	// Without a message assertion, a Validate that emitted one hardcoded
	// "invalid" for every violation would pass all five rejection tests.
	if msg := c.Diagnostics()[0].Message; !strings.Contains(msg, "nonsense") {
		t.Errorf("the message must name the offending property, got %q", msg)
	}
}

func TestValidateRejectsBadTier(t *testing.T) {
	in := strings.Replace(good, "tier: 1", "tier: 9", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("tier 9 is not one of 1, 2, 3")
	}
	if msg := c.Diagnostics()[0].Message; !strings.Contains(msg, "tier") {
		t.Errorf("the message must name the offending field, got %q", msg)
	}
}

func TestValidateRejectsMissingOwner(t *testing.T) {
	in := strings.Replace(good, "  owner: team-payments\n", "", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("owner is required")
	}
	if msg := c.Diagnostics()[0].Message; !strings.Contains(msg, "owner") {
		t.Errorf("the message must name the missing field, got %q", msg)
	}
}

// tier is required for things that page, optional for things that do not.
func TestTierIsRequiredOnlyForPageableKinds(t *testing.T) {
	lib := `apiVersion: landsraad/v1
kind: Library
metadata:
  name: kafkaclient
  owner: team-payments
  lifecycle: production
`
	var c diag.Collector
	if !mustDefault(t).Validate("", "libs/kafkaclient/service.yaml", []byte(lib), &c) {
		t.Errorf("a Library needs no tier: %+v", c.Diagnostics())
	}

	svc := strings.Replace(good, "  tier: 1\n", "", 1)
	var c2 diag.Collector
	if mustDefault(t).Validate("", "a/service.yaml", []byte(svc), &c2) {
		t.Error("a Service without a tier must be rejected")
	}
}

func TestValidateAcceptsAnnotationsAndAliases(t *testing.T) {
	in := strings.Replace(good, "  tier: 1\n",
		"  tier: 1\n  aliases: [payments-svc]\n  annotations:\n    grafana-folder: abc123\n", 1)
	var c diag.Collector
	if !mustDefault(t).Validate("", "a/service.yaml", []byte(in), &c) {
		t.Errorf("annotations and aliases must be accepted: %+v", c.Diagnostics())
	}
}

func TestNameMustNotEndInASeparator(t *testing.T) {
	for _, bad := range []string{"payments.", "foo-", "bar_"} {
		in := strings.Replace(good, "name: payments-worker", "name: "+bad, 1)
		var c diag.Collector
		if mustDefault(t).Validate("", "a/service.yaml", []byte(in), &c) {
			t.Errorf("%q ends in a separator and must be rejected (Backstage requires "+
				"names to end alphanumeric; loosening later is free, tightening is not)", bad)
		}
	}
}

func TestValidateRejectsWrongAPIVersion(t *testing.T) {
	in := strings.Replace(good, "landsraad/v1", "platform/v1", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("only landsraad/v1 is accepted")
	}
}

// A Validator is a value, so a caller can supply its own schema. This is the
// flexibility requirement in spec §3.1, asserted rather than assumed.
func TestNewAcceptsACallerSuppliedSchema(t *testing.T) {
	v, err := New([]byte(`{"type":"object","required":["kind"]}`))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var c diag.Collector
	if !v.Validate("", "a.yaml", []byte("kind: Anything\n"), &c) {
		t.Errorf("the caller's schema must be the one applied: %+v", c.Diagnostics())
	}
}

func TestNewRejectsBrokenSchema(t *testing.T) {
	if _, err := New([]byte("{not json")); err == nil {
		t.Fatal("a schema that is not JSON must return an error, not panic")
	}
}

// The schema and the Go Kind constants must never drift apart.
func TestSchemaKindsMatchGoKinds(t *testing.T) {
	raw := string(Raw)
	for _, k := range catalog.AllKinds {
		if !strings.Contains(raw, `"`+string(k)+`"`) {
			t.Errorf("kind %q exists in Go but not in service.schema.json", k)
		}
	}
}
```

- [ ] **Step 4: Run it to verify it fails**

Run: `go test ./internal/schema/ -v`
Expected: FAIL — `undefined: Validator`

- [ ] **Step 5: Write the implementation**

Create `internal/schema/schema.go`:

```go
// Package schema validates catalog files against a JSON Schema. The schema
// shipped with the binary is the single source of truth for structure:
// validating against it, rather than with hand-written Go checks, is what
// keeps the published schema and the tool's behaviour from drifting apart.
package schema

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
)

//go:embed service.schema.json
var Raw []byte

// Validator validates catalog files against one compiled schema.
//
// It is a value rather than package state so that two schema versions can
// coexist during a migration, and so a test can supply its own schema. See
// spec §3.1.
type Validator struct {
	sch *jsonschema.Schema
}

const schemaID = "service.schema.json"

// New compiles the given JSON Schema document.
func New(schemaJSON []byte) (*Validator, error) {
	var doc any
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		return nil, fmt.Errorf("schema is not valid JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(schemaID, doc); err != nil {
		return nil, fmt.Errorf("cannot add schema resource: %w", err)
	}
	sch, err := c.Compile(schemaID)
	if err != nil {
		return nil, fmt.Errorf("cannot compile schema: %w", err)
	}
	return &Validator{sch: sch}, nil
}

// Default compiles the schema embedded in the binary.
func Default() (*Validator, error) { return New(Raw) }

// Validate checks one file's bytes. It returns true when the file is
// structurally valid, and adds one diagnostic per violation.
//
// It reports nothing for bytes that are not YAML at all: that is
// catalog.ParseFile's diagnostic to make, and two errors for one cause is
// noise.
func (v *Validator) Validate(repo, path string, data []byte, c *diag.Collector) bool {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false
	}
	var inst any
	if err := root.Decode(&inst); err != nil {
		return false
	}

	err := v.sch.Validate(inst)
	if err == nil {
		return true
	}

	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: repo, File: path, Line: 1,
			Check: "schema", Message: err.Error(),
		})
		return false
	}
	for _, leaf := range leaves(ve) {
		// InstanceLocation is []string in jsonschema/v6 — one element per
		// path segment — which is exactly what fieldLine wants.
		line := fieldLine(&root, leaf.InstanceLocation...)
		if line == 0 {
			line = 1
		}
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     line,
			Check:    "schema",
			// leaf.Error() already names the instance location, so do not
			// prefix it again.
			Message: leaf.Error(),
		})
	}
	return false
}

// leaves returns the most specific errors in the tree. The root error is a
// generic "does not validate"; the causes carry the useful detail.
func leaves(ve *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return []*jsonschema.ValidationError{ve}
	}
	var out []*jsonschema.ValidationError
	for _, c := range ve.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

// fieldLine walks a document node down a key path and returns the 1-indexed
// line of the value it lands on. Numeric segments are array indices. It
// returns the nearest enclosing node's line when the path runs out, so a
// diagnostic always points somewhere useful.
func fieldLine(root *yaml.Node, path ...string) int {
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, key := range path {
		switch node.Kind {
		case yaml.MappingNode:
			found := false
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == key {
					node = node.Content[i+1]
					found = true
					break
				}
			}
			if !found {
				return node.Line
			}
		case yaml.SequenceNode:
			idx := -1
			if _, err := fmt.Sscanf(key, "%d", &idx); err != nil || idx < 0 || idx >= len(node.Content) {
				return node.Line
			}
			node = node.Content[idx]
		default:
			return node.Line
		}
	}
	return node.Line
}
```

- [ ] **Step 6: Run it to verify it passes**

Run: `go test ./internal/schema/ -v`
Expected: PASS — all eight tests

If the `jsonschema/v6` API differs from the calls above (`NewCompiler`,
`AddResource`, `Compile`, `ValidationError.InstanceLocation`, `.Causes`), fix
the calls against the pinned version's godoc. **Do not change the tests** —
they encode the required behaviour, not the library's shape.

- [ ] **Step 7: Generate the editor-facing copy**

The definition of done requires `schema/service.schema.json` to exist and match
the embedded one. Generate it rather than hand-maintaining a second copy:

```bash
task schema
git diff --exit-code schema/service.schema.json || true
```

- [ ] **Step 8: Commit**

```bash
git add internal/schema/ schema/ go.mod go.sum
git commit -m "feat: JSON Schema validation as a Validator value"
```

---

## Task 8: teams.yaml and owner validation

**Files:**
- Create: `internal/config/teams.go`
- Test: `internal/config/teams_test.go`

**Interfaces:**
- Consumes: `diag.Collector`, `catalog.Catalog`.
- Produces:
  - `type Team struct { Name string; Members []string; Slack, PagerDuty string }`
  - `type Teams struct { byName map[string]*Team }`
  - `func LoadTeams(path string, data []byte, c *diag.Collector) *Teams`
  - `func (*Teams) Get(name string) (*Team, bool)`
  - `func (*Teams) Names() []string` — sorted
  - `func (*Teams) ValidateOwners(cat *catalog.Catalog, c *diag.Collector)` — adds a `did you mean` hint using edit distance

- [ ] **Step 1: Write the failing test**

Create `internal/config/teams_test.go`:

```go
package config

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

const teamsYAML = `teams:
  - name: team-payments
    members: [alice, bob]
    slack: "#payments"
    pagerduty: PAY
  - name: team-sre
    members: [carol]
    slack: "#sre"
    pagerduty: SRE
`

func TestLoadTeams(t *testing.T) {
	var c diag.Collector
	teams := LoadTeams("teams.yaml", []byte(teamsYAML), &c)
	if c.HasErrors() {
		t.Fatalf("valid teams.yaml must load: %+v", c.Diagnostics())
	}
	got, ok := teams.Get("team-payments")
	if !ok {
		t.Fatal("team-payments must be found")
	}
	if len(got.Members) != 2 || got.Slack != "#payments" {
		t.Errorf("team fields not read: %+v", got)
	}
	if names := teams.Names(); len(names) != 2 || names[0] != "team-payments" {
		t.Errorf("Names() must be sorted, got %v", names)
	}
}

func TestLoadTeamsRejectsUnknownFields(t *testing.T) {
	var c diag.Collector
	LoadTeams("teams.yaml", []byte("teams:\n  - name: team-a\n    pagerDuty: PAY\n"), &c)
	if !c.HasErrors() {
		t.Fatal("a misspelled key in the file that routes alerts must be rejected, not ignored")
	}
}

func TestLoadTeamsReportsRealLineNumbers(t *testing.T) {
	in := "teams:\n  - name: team-a\n  - name: team-a\n"
	var c diag.Collector
	LoadTeams("teams.yaml", []byte(in), &c)
	if !c.HasErrors() {
		t.Fatal("a duplicate team must be an error")
	}
	if got := c.Diagnostics()[0].Line; got != 3 {
		t.Errorf("the duplicate is on line 3, diagnostic points at line %d", got)
	}
}

func TestValidateOwnersRejectsUnknownTeam(t *testing.T) {
	var c diag.Collector
	teams := LoadTeams("teams.yaml", []byte(teamsYAML), &c)

	e := &catalog.Entity{Kind: catalog.KindService}
	e.Metadata.Name = "api"
	e.Metadata.Owner = "team-payment" // typo: missing trailing s
	e.SourcePath = "services/api/service.yaml"
	e.NameLine = 4
	cat := catalog.NewCatalog([]*catalog.Entity{e}, &c)

	teams.ValidateOwners(cat, &c)

	if !c.HasErrors() {
		t.Fatal("an unknown owner must be an error")
	}
	d := c.Diagnostics()[0]
	if want := `owner "team-payment" is not defined in teams.yaml`; d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if want := `did you mean "team-payments"?`; d.Hint != want {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, want)
	}
}

func TestValidateOwnersAcceptsKnownTeam(t *testing.T) {
	var c diag.Collector
	teams := LoadTeams("teams.yaml", []byte(teamsYAML), &c)

	e := &catalog.Entity{Kind: catalog.KindService}
	e.Metadata.Name = "api"
	e.Metadata.Owner = "team-sre"
	e.SourcePath = "services/api/service.yaml"
	e.NameLine = 4
	cat := catalog.NewCatalog([]*catalog.Entity{e}, &c)

	teams.ValidateOwners(cat, &c)

	if c.HasErrors() {
		t.Errorf("a known owner must pass: %+v", c.Diagnostics())
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: LoadTeams`

- [ ] **Step 3: Write the implementation**

Create `internal/config/teams.go`:

```go
// Package config loads the repository-level YAML files: teams.yaml and
// repos.yaml.
package config

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// Team is one owning team. It is the source for CODEOWNERS and alert routing.
type Team struct {
	Name      string   `yaml:"name"`
	Members   []string `yaml:"members"`
	Slack     string   `yaml:"slack"`
	PagerDuty string   `yaml:"pagerduty"`
}

// Teams is the loaded teams.yaml.
type Teams struct {
	byName map[string]*Team
}

type teamsFile struct {
	Teams []*Team `yaml:"teams"`
}

// LoadTeams reads teams.yaml. It always returns a usable (possibly empty)
// Teams so callers need no nil checks; problems are reported as diagnostics.
func LoadTeams(path string, data []byte, c *diag.Collector) *Teams {
	t := &Teams{byName: map[string]*Team{}}

	// KnownFields(true) rejects unknown keys. teams.yaml is the source for
	// alert routing, so a silently-ignored `pagerDuty:` typo would make
	// routing rot invisibly — the exact inverse of this product's thesis.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f teamsFile
	if err := dec.Decode(&f); err != nil && err != io.EOF {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: lineFromYAMLError(err),
			Check:   "teams-parse",
			Message: fmt.Sprintf("cannot parse teams file: %v", err),
			Hint:    "teams.yaml is a list under `teams:` with name, members, slack and pagerduty",
		})
		return t
	}

	lines := teamLines(data, len(f.Teams))
	for i, team := range f.Teams {
		line := lines[i]
		if team.Name == "" {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: line,
				Check: "teams-parse", Message: "a team entry has no name",
				Hint: "every team needs a name; it is what service.yaml owner fields refer to",
			})
			continue
		}
		if prev, dup := t.byName[team.Name]; dup {
			_ = prev
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: line,
				Check:   "teams-duplicate",
				Message: fmt.Sprintf("team %q is defined twice", team.Name),
				Hint:    "merge the two entries",
			})
			continue
		}
		t.byName[team.Name] = team
	}
	return t
}

// teamLines returns the 1-indexed line of each entry under `teams:`, so a
// duplicate or nameless team points at itself rather than at line 1. The
// yaml.Node technique is the same one catalog.ParseFile uses.
func teamLines(data []byte, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 1
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) == 0 {
		return out
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "teams" {
			continue
		}
		seq := doc.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return out
		}
		for j := 0; j < len(seq.Content) && j < n; j++ {
			out[j] = seq.Content[j].Line
		}
	}
	return out
}

// lineFromYAMLError pulls a line out of a yaml.v3 error string.
func lineFromYAMLError(err error) int {
	if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			return n
		}
	}
	return 1
}

var yamlLineRE = regexp.MustCompile(`line (\d+):`)

func (t *Teams) Get(name string) (*Team, bool) {
	team, ok := t.byName[name]
	return team, ok
}

// Names returns every team name, sorted.
func (t *Teams) Names() []string {
	out := make([]string, 0, len(t.byName))
	for n := range t.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ValidateOwners checks that every entity's owner exists, suggesting the
// closest real team when the owner looks like a typo.
func (t *Teams) ValidateOwners(cat *catalog.Catalog, c *diag.Collector) {
	for _, e := range cat.Entities {
		if _, ok := t.Get(e.Metadata.Owner); ok {
			continue
		}
		d := diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     e.SourceRepo,
			File:     e.SourcePath,
			Line:     e.NameLine,
			Entity:   e.Metadata.Name,
			Check:    "unknown-owner",
			Message:  fmt.Sprintf("owner %q is not defined in teams.yaml", e.Metadata.Owner),
		}
		if best, ok := closest(e.Metadata.Owner, t.Names()); ok {
			d.Hint = fmt.Sprintf("did you mean %q?", best)
		} else {
			d.Hint = fmt.Sprintf("known teams: %v", t.Names())
		}
		c.Add(d)
	}
}

// closest returns the nearest candidate within an edit distance of 3, which
// catches typos without inventing wild suggestions.
func closest(s string, candidates []string) (string, bool) {
	best, bestDist := "", 4
	for _, cand := range candidates {
		if d := levenshtein(s, cand); d < bestDist {
			best, bestDist = cand, d
		}
	}
	return best, best != ""
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS — all three tests

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: teams.yaml loading and owner validation with typo hints"
```

---

## Task 9: Discovery, loading, and file checks — the `fs.FS` seam

This is where spec §3.1 becomes code. Nothing below `cmd/` touches `os`;
everything reads through an `io/fs.FS`. That buys three things at once:

1. Plan 3 points these same functions at a **fetched remote repo** — no second
   implementation, no refactor of this plan.
2. Tests run against `fstest.MapFS`, so the suite touches neither the network
   nor the disk.
3. `catalog` becomes **pure** — `ParseAll` takes bytes and returns entities,
   with no IO anywhere in the package except `CheckFiles`, which is inherently
   a filesystem question.

**Files:**
- Create: `internal/discover/discover.go`, `internal/catalog/load.go`, `internal/catalog/files.go`
- Test: `internal/discover/discover_test.go`, `internal/catalog/load_test.go`, `internal/catalog/files_test.go`
- Create: `testdata/monorepo-ok/` fixtures

**Interfaces:**
- Consumes: `diag.Collector` (Task 2), `Entity` (Task 3), `ParseFile` (Task 4), `Catalog` (Task 5).
- Produces:
  - `type discover.File struct { Path string; Data []byte }`
  - `func discover.Find(fsys fs.FS, patterns []string) ([]string, error)` — sorted, slash-separated
  - `func discover.Load(fsys fs.FS, paths []string, c *diag.Collector) []File`
  - `func catalog.ParseAll(repo string, files []discover.File, c *diag.Collector) []*Entity` — pure, no IO
  - `func (e *Entity) Location() string` — `repo:path` when the repo is known, else `path`
  - `func catalog.CheckFiles(fsys fs.FS, cat *Catalog, c *diag.Collector)`

- [ ] **Step 1: Create the fixture repo**

```bash
mkdir -p testdata/monorepo-ok/services/payments-worker/docs
mkdir -p testdata/monorepo-ok/services/ledger-api/docs
mkdir -p testdata/monorepo-ok/topics/payments-events
```

Create `testdata/monorepo-ok/services/payments-worker/service.yaml`:

```yaml
apiVersion: landsraad/v1
kind: Service
metadata:
  name: payments-worker
  description: Consumes payment events and settles them.
  owner: team-payments
  tier: 1
  lifecycle: production
  tags: [go, kafka]
spec:
  language: go
  path: services/payments-worker
  docs: services/payments-worker/docs
  runbook: services/payments-worker/docs/runbook.md
  dependsOn:
    - topic:payments.events
    - service:ledger-api
```

Create `testdata/monorepo-ok/services/payments-worker/docs/runbook.md`:

```markdown
# payments-worker runbook

## When it pages

Consumer lag above threshold.
```

Create `testdata/monorepo-ok/services/ledger-api/service.yaml`:

```yaml
apiVersion: landsraad/v1
kind: Service
metadata:
  name: ledger-api
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  language: go
  path: services/ledger-api
  docs: services/ledger-api/docs
```

Create `testdata/monorepo-ok/services/ledger-api/docs/index.md`:

```markdown
# ledger-api
```

Create `testdata/monorepo-ok/topics/payments-events/service.yaml`:

```yaml
apiVersion: landsraad/v1
kind: Topic
metadata:
  name: payments.events
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  path: topics/payments-events
```

Create `testdata/monorepo-ok/teams.yaml`:

```yaml
teams:
  - name: team-payments
    members: [alice, bob]
    slack: "#payments"
    pagerduty: PAY
```

- [ ] **Step 2: Write the failing discovery test**

Create `internal/discover/discover_test.go`:

```go
package discover

import (
	"os"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// The whole point of the fs.FS seam: these tests touch no disk at all.
func mem() fstest.MapFS {
	return fstest.MapFS{
		"services/a/service.yaml":  {Data: []byte("kind: Service\n")},
		"services/b/service.yaml":  {Data: []byte("kind: Service\n")},
		"services/b/docs/index.md": {Data: []byte("# b\n")},
		"topics/t/service.yaml":    {Data: []byte("kind: Topic\n")},
		"libs/nocatalog/README.md": {Data: []byte("no service.yaml here\n")},
	}
}

func TestFindMatchesGlobPatterns(t *testing.T) {
	got, err := Find(mem(), []string{"services/*", "topics/*"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want := []string{
		"services/a/service.yaml",
		"services/b/service.yaml",
		"topics/t/service.yaml",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q (results must be sorted)", i, got[i], want[i])
		}
	}
}

func TestFindSkipsDirectoriesWithoutServiceYAML(t *testing.T) {
	got, err := Find(mem(), []string{"libs/*"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a directory with no service.yaml must be skipped, got %v", got)
	}
}

func TestFindPatternMatchingNothingIsNotAnError(t *testing.T) {
	got, err := Find(mem(), []string{"nonexistent/*"})
	if err != nil {
		t.Fatalf("a pattern matching nothing must not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestFindDotMeansRoot(t *testing.T) {
	root := fstest.MapFS{"service.yaml": {Data: []byte("kind: Service\n")}}
	got, err := Find(root, []string{"."})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 || got[0] != "service.yaml" {
		t.Errorf(`pattern "." must find the root service.yaml, got %v`, got)
	}
}

func TestFindDeduplicatesOverlappingPatterns(t *testing.T) {
	got, err := Find(mem(), []string{"services/*", "services/a"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("overlapping patterns must not yield duplicates, got %v", got)
	}
}

func TestLoadReadsBytes(t *testing.T) {
	var c diag.Collector
	files := Load(mem(), []string{"services/a/service.yaml"}, &c)
	if c.HasErrors() {
		t.Fatalf("unexpected errors: %+v", c.Diagnostics())
	}
	if len(files) != 1 || string(files[0].Data) != "kind: Service\n" {
		t.Errorf("Load did not return the file bytes: %+v", files)
	}
}

func TestLoadReportsUnreadableFile(t *testing.T) {
	var c diag.Collector
	files := Load(mem(), []string{"services/gone/service.yaml"}, &c)
	if len(files) != 0 {
		t.Errorf("a missing file must not be returned, got %+v", files)
	}
	if !c.HasErrors() {
		t.Fatal("a missing file must produce an error diagnostic")
	}
	if c.Diagnostics()[0].Line == 0 {
		t.Error("every diagnostic carries a line, even this one")
	}
}

// The same functions must work against a real directory, because that is what
// the command does. os.DirFS is the only place `os` appears.
func TestFindWorksOverOsDirFS(t *testing.T) {
	got, err := Find(os.DirFS("../../testdata/monorepo-ok"), []string{"services/*", "topics/*"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d entries from the fixture repo, want 3: %v", len(got), got)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/discover/ -v`
Expected: FAIL — `undefined: Find`

- [ ] **Step 4: Write `discover.go`**

Create `internal/discover/discover.go`:

```go
// Package discover locates and reads the catalog files in a repository.
//
// It works against any io/fs.FS: os.DirFS for a local checkout, a tar reader
// for a fetched remote repo, fstest.MapFS in tests. Nothing here calls os.
package discover

import (
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Filename is the fixed name of a catalog file.
const Filename = "service.yaml"

// File is one catalog file's bytes with the path it came from. Parsing and
// validation take these, so neither of those stages does any IO.
type File struct {
	Path string
	Data []byte
}

// Find returns the paths of every service.yaml matching the given glob
// patterns, sorted and deduplicated. Paths are slash-separated and relative to
// the filesystem root, as io/fs requires.
//
// A pattern of "." means the root itself. A pattern matching nothing is not an
// error: a repo may legitimately have no services under a configured path.
func Find(fsys fs.FS, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	for _, pattern := range patterns {
		var dirs []string
		if pattern == "." || pattern == "" {
			dirs = []string{"."}
		} else {
			matches, err := fs.Glob(fsys, pattern)
			if err != nil {
				// Only ErrBadPattern is possible, and that is a config bug.
				return nil, fmt.Errorf("bad path pattern %q: %w", pattern, err)
			}
			dirs = matches
		}
		for _, dir := range dirs {
			info, err := fs.Stat(fsys, dir)
			if err != nil || !info.IsDir() {
				continue
			}
			candidate := Filename
			if dir != "." {
				candidate = path.Join(dir, Filename)
			}
			if _, err := fs.Stat(fsys, candidate); err != nil {
				continue
			}
			seen[candidate] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// Load reads each path, reporting a diagnostic for any it cannot read and
// omitting it from the result. Never fails fast: one unreadable file does not
// hide problems in the rest.
func Load(fsys fs.FS, paths []string, c *diag.Collector) []File {
	out := make([]File, 0, len(paths))
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError,
				File:     p,
				Line:     1,
				Check:    "unreadable",
				Message:  fmt.Sprintf("cannot read file: %v", err),
			})
			continue
		}
		out = append(out, File{Path: p, Data: data})
	}
	return out
}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/discover/ -v`
Expected: PASS — all eight tests

- [ ] **Step 6: Write the failing `ParseAll` test**

Create `internal/catalog/load_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
)

func TestParseAllIsPureAndOrdered(t *testing.T) {
	var c diag.Collector
	files := []discover.File{
		{Path: "services/b/service.yaml", Data: []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: b\n  owner: t\n  tier: 1\n  lifecycle: production\n")},
		{Path: "services/a/service.yaml", Data: []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: a\n  owner: t\n  tier: 1\n  lifecycle: production\n")},
	}
	got := ParseAll("monorepo", files, &c)
	if c.HasErrors() {
		t.Fatalf("valid files must parse: %+v", c.Diagnostics())
	}
	if len(got) != 2 {
		t.Fatalf("got %d entities, want 2", len(got))
	}
	// ParseAll preserves input order; sorting is NewCatalog's job.
	if got[0].Metadata.Name != "b" || got[1].Metadata.Name != "a" {
		t.Errorf("ParseAll must preserve input order, got %q then %q",
			got[0].Metadata.Name, got[1].Metadata.Name)
	}
	if got[0].SourcePath != "services/b/service.yaml" {
		t.Errorf("provenance not attached: %q", got[0].SourcePath)
	}
	if got[0].SourceRepo != "monorepo" {
		t.Errorf("repo provenance not attached: %q", got[0].SourceRepo)
	}
	if want := "monorepo:services/b/service.yaml"; got[0].Location() != want {
		t.Errorf("Location() = %q, want %q", got[0].Location(), want)
	}
}

func TestParseAllSkipsBrokenFilesAndKeepsGoing(t *testing.T) {
	var c diag.Collector
	files := []discover.File{
		{Path: "broken/service.yaml", Data: []byte("kind: Service\n  bad: indent\n")},
		{Path: "services/a/service.yaml", Data: []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: a\n  owner: t\n  tier: 1\n  lifecycle: production\n")},
	}
	got := ParseAll("monorepo", files, &c)
	if len(got) != 1 {
		t.Fatalf("the good file must still be parsed, got %d entities", len(got))
	}
	if !c.HasErrors() {
		t.Error("the broken file must still be reported")
	}
}
```

- [ ] **Step 7: Run it to verify it fails**

Run: `go test ./internal/catalog/ -run TestParseAll -v`
Expected: FAIL — `undefined: ParseAll`

- [ ] **Step 8: Write `load.go`**

Create `internal/catalog/load.go`:

```go
package catalog

import (
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
)

// ParseAll turns loaded bytes into entities. It is pure: no filesystem, no
// network, no globals — which is what makes it reusable across a local
// checkout, a fetched remote repo and a test fixture without changing.
//
// Files that cannot be parsed are reported and skipped; the rest still parse,
// so one broken file never hides problems in the others.
func ParseAll(repo string, files []discover.File, c *diag.Collector) []*Entity {
	out := make([]*Entity, 0, len(files))
	for _, f := range files {
		if e, ok := ParseFile(repo, f.Path, f.Data, c); ok {
			out = append(out, e)
		}
	}
	return out
}
```

- [ ] **Step 9: Run it to verify it passes**

Run: `go test ./internal/catalog/ -run TestParseAll -v`
Expected: PASS — both tests

- [ ] **Step 10: Write the failing file-check test**

Create `internal/catalog/files_test.go`:

```go
package catalog

import (
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func repoFS() fstest.MapFS {
	return fstest.MapFS{
		"services/payments-worker/docs/runbook.md": {Data: []byte("# runbook\n")},
		"services/payments-worker/docs/index.md":   {Data: []byte("# docs\n")},
	}
}

func TestCheckFilesAcceptsExistingPaths(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Runbook = "services/payments-worker/docs/runbook.md"
	e.Spec.Docs = "services/payments-worker/docs"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if c.HasErrors() {
		t.Errorf("existing paths must pass: %+v", c.Diagnostics())
	}
}

func TestCheckFilesReportsMissingRunbook(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Runbook = "services/payments-worker/docs/nope.md"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if !c.HasErrors() {
		t.Fatal("a runbook path that does not exist must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "missing-file" {
		t.Errorf("Check = %q, want %q", d.Check, "missing-file")
	}
	if d.Line != 4 {
		t.Errorf("the diagnostic must point at the entity's line, got %d", d.Line)
	}
	want := `spec.runbook points at "services/payments-worker/docs/nope.md", which does not exist`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

func TestCheckFilesRejectsFileWhereDirectoryExpected(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Docs = "services/payments-worker/docs/index.md" // a file, not a dir
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if !c.HasErrors() {
		t.Fatal("spec.docs must be a directory")
	}
}

func TestCheckFilesIgnoresEmptyPaths(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/ledger-api/service.yaml", "ledger-api", KindService, 4)
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if c.HasErrors() {
		t.Errorf("an unset optional path is not a missing file: %+v", c.Diagnostics())
	}
}
```

- [ ] **Step 11: Run it to verify it fails**

Run: `go test ./internal/catalog/ -run TestCheckFiles -v`
Expected: FAIL — `undefined: CheckFiles`

- [ ] **Step 12: Write `files.go`**

Create `internal/catalog/files.go`:

```go
package catalog

import (
	"fmt"
	"io/fs"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// CheckFiles verifies that every path an entity points at exists in the given
// filesystem. Empty paths are skipped: these fields are optional, and "not
// set" is a scorecard question, not a validation error.
//
// Taking an fs.FS rather than a root path is what lets the platform build run
// this against a fetched remote repo with no change.
func CheckFiles(fsys fs.FS, cat *Catalog, c *diag.Collector) {
	for _, e := range cat.Entities {
		for _, f := range []struct {
			field string
			path  string
			dir   bool
		}{
			{"spec.runbook", e.Spec.Runbook, false},
			{"spec.docs", e.Spec.Docs, true},
			{"spec.alerts", e.Spec.Alerts, false},
		} {
			if f.path == "" {
				continue
			}
			info, err := fs.Stat(fsys, f.path)
			if err != nil {
				c.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "missing-file",
					Message:  fmt.Sprintf("%s points at %q, which does not exist", f.field, f.path),
					Hint:     "paths are relative to the repository root, slash-separated",
				})
				continue
			}
			if f.dir && !info.IsDir() {
				c.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "missing-file",
					Message:  fmt.Sprintf("%s points at %q, which is a file but must be a directory", f.field, f.path),
				})
			}
		}
	}
}
```

- [ ] **Step 13: Run everything to verify it passes**

Run: `task test`
Expected: PASS — every package

- [ ] **Step 14: Commit**

```bash
git add internal/discover/ internal/catalog/load.go internal/catalog/load_test.go internal/catalog/files.go internal/catalog/files_test.go testdata/
git commit -m "feat: fs.FS-based discovery and loading, pure ParseAll"
```

---

## Task 10: The `validate` command

The payoff task. `Validate` is an **explicit composition** of the typed stages
built above — you can read the whole pipeline in twelve lines, the compiler
refuses an ordering that makes no sense, and `build` in Plan 3 reuses every one
of these stages against a fetched repo rather than reimplementing them.

`os` appears exactly once in the codebase, in the cobra layer, where
`os.DirFS(root)` turns a path into the `fs.FS` everything else consumes.

**Files:**
- Create: `internal/diag/format.go`, `internal/config/repos.go`, `cmd/landsraad/validate.go`
- Modify: `cmd/landsraad/main.go` — replace with the cobra root
- Test: `internal/diag/format_test.go`, `cmd/landsraad/validate_test.go`
- Create: `testdata/monorepo-broken/` fixtures

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `type diag.Formatter interface { Name() string; Write(io.Writer, []Diagnostic) error }`
  - `func diag.Formatters() map[string]Formatter`; `func diag.Lookup(string) (Formatter, bool)`; `func diag.FormatNames() []string`
  - `type diag.Text`, `diag.JSON`, `diag.GitHub`, `diag.GitLab` — the four formats
  - `func config.LoadRepos(path string, data []byte, c *diag.Collector) *Repos`; `(*Repos).LocalPatterns() []string`
  - `func Validate(fsys fs.FS, out, errOut io.Writer, f diag.Formatter) int` — `out` carries only the format payload

- [ ] **Step 1: Add cobra**

```bash
go get github.com/spf13/cobra@v1.8.1
```

- [ ] **Step 2: Write the failing formatter test**

Create `internal/diag/format_test.go`:

```go
package diag

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var sample = []Diagnostic{
	{Severity: SevError, File: "services/a/service.yaml", Line: 4, Entity: "a",
		Check: "unknown-owner", Message: `owner "team-payment" is not defined in teams.yaml`,
		Hint: `did you mean "team-payments"?`},
	{Severity: SevWarn, File: "services/b/service.yaml", Line: 9, Entity: "b",
		Check: "missing-file", Message: `spec.runbook points at "nope.md", which does not exist`},
}

func TestTextIncludesFileLineAndHint(t *testing.T) {
	var buf bytes.Buffer
	if err := (Text{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"services/a/service.yaml:4", "error", "unknown-owner",
		`did you mean "team-payments"?`, "services/b/service.yaml:9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestJSONRoundTripsEveryField(t *testing.T) {
	var buf bytes.Buffer
	if err := (JSON{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var back []Diagnostic
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	// Compare the WHOLE struct. Checking one field lets a formatter silently
	// drop File, Line, Message, Hint and Severity and still pass.
	if diff := cmp.Diff(sample, back); diff != "" {
		t.Errorf("round trip lost data (-want +got):\n%s", diff)
	}
}

// Severity must cross the wire as a name. It is an iota, so if it serialised
// as an integer, inserting a new severity later would silently change the
// meaning of every user's jq filter.
func TestSeverityMarshalsAsAName(t *testing.T) {
	var buf bytes.Buffer
	if err := (JSON{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if raw[0]["severity"] != "error" || raw[1]["severity"] != "warn" {
		t.Errorf("severity must serialise as a name, got %v and %v",
			raw[0]["severity"], raw[1]["severity"])
	}
}

func TestGitHubUsesAnnotationSyntax(t *testing.T) {
	var buf bytes.Buffer
	if err := (GitHub{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "::error file=services/a/service.yaml,line=4::") {
		t.Errorf("missing GitHub error annotation:\n%s", out)
	}
	if !strings.Contains(out, "::warning file=services/b/service.yaml,line=9::") {
		t.Errorf("missing GitHub warning annotation:\n%s", out)
	}
}

func TestGitLabEmitsCodeQualityShape(t *testing.T) {
	var buf bytes.Buffer
	if err := (GitLab{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d issues, want 2", len(out))
	}
	if out[0]["severity"] != "major" {
		t.Errorf("an error must map to GitLab severity major, got %v", out[0]["severity"])
	}
	if out[1]["severity"] != "minor" {
		t.Errorf("a warning must map to minor, got %v", out[1]["severity"])
	}
	if out[0]["fingerprint"] == "" || out[0]["fingerprint"] == nil {
		t.Error("GitLab requires a fingerprint per issue")
	}
	loc, _ := out[0]["location"].(map[string]any)
	if loc == nil || loc["path"] != "services/a/service.yaml" {
		t.Errorf("location.path missing or wrong: %v", out[0]["location"])
	}
	// Without this, every issue could be pinned to line 0 and the test would
	// still pass — defeating the point of a line-scoped report.
	lines, _ := loc["lines"].(map[string]any)
	if lines == nil || lines["begin"] != float64(4) {
		t.Errorf("location.lines.begin = %v, want 4", lines["begin"])
	}
	if out[0]["description"] == "" || out[0]["check_name"] != "unknown-owner" {
		t.Errorf("description/check_name not carried: %v", out[0])
	}
}

// Every built-in format resolves, and each one's Name matches its key.
func TestFormattersAreConsistent(t *testing.T) {
	for _, name := range []string{"text", "json", "github", "gitlab"} {
		f, ok := Lookup(name)
		if !ok {
			t.Errorf("built-in format %q does not resolve", name)
			continue
		}
		if f.Name() != name {
			t.Errorf("format keyed %q reports Name() = %q", name, f.Name())
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("an unknown name must not resolve")
	}
	if got := FormatNames(); len(got) != 4 || got[0] != "github" {
		t.Errorf("FormatNames must be sorted, got %v", got)
	}
}

// A caller can supply its own Formatter: the interface is the extension
// point, which is why the Registry type was unnecessary.
func TestAnyFormatterSatisfiesTheInterface(t *testing.T) {
	var f Formatter = quietFormat{}
	var buf bytes.Buffer
	if err := f.Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("quiet formatter wrote %q", buf.String())
	}
}

type quietFormat struct{}

func (quietFormat) Name() string                        { return "quiet" }
func (quietFormat) Write(io.Writer, []Diagnostic) error { return nil }
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/diag/ -run 'TestText|TestJSON|TestGitHub|TestGitLab|TestFormatters|TestAnyFormatter' -v`
Expected: FAIL — `undefined: Text`

- [ ] **Step 4: Write `format.go`**

Create `internal/diag/format.go`:

```go
package diag

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strings"
)

// Formatter renders diagnostics for one destination. Adding a destination is
// a new type implementing this interface — never an edit to a switch
// statement (spec §3.1).
type Formatter interface {
	Name() string
	Write(w io.Writer, ds []Diagnostic) error
}

// Formatters returns the built-in formats by name. A new format is a new type
// implementing Formatter plus one line here — no switch to edit, and no init()
// self-registration, which would be package-level mutable state.
//
// This was a Registry type with a constructor and a Names method. It was
// deleted: there was never a second registry, its only consumer was a test
// that invented its own subject to justify it, and the interface alone gives
// every property the extra type claimed.
func Formatters() map[string]Formatter {
	return map[string]Formatter{
		"text":   Text{},
		"json":   JSON{},
		"github": GitHub{},
		"gitlab": GitLab{},
	}
}

// Lookup finds a formatter by name.
func Lookup(name string) (Formatter, bool) {
	f, ok := Formatters()[name]
	return f, ok
}

// FormatNames returns the built-in names, sorted, for help text.
func FormatNames() []string {
	out := make([]string, 0, 4)
	for n := range Formatters() {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Text renders diagnostics for a human terminal.
type Text struct{}

func (Text) Name() string { return "text" }

func (Text) Write(w io.Writer, ds []Diagnostic) error {
	for _, d := range ds {
		if _, err := fmt.Fprintf(w, "%s: %s:%d", d.Severity, d.File, d.Line); err != nil {
			return err
		}
		if d.Check != "" {
			if _, err := fmt.Fprintf(w, " [%s]", d.Check); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n  %s\n", d.Message); err != nil {
			return err
		}
		if d.Hint != "" {
			if _, err := fmt.Fprintf(w, "  hint: %s\n", d.Hint); err != nil {
				return err
			}
		}
	}
	return nil
}

// JSON renders diagnostics as an array for machine consumption.
type JSON struct{}

func (JSON) Name() string { return "json" }

func (JSON) Write(w io.Writer, ds []Diagnostic) error {
	if ds == nil {
		ds = []Diagnostic{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ds)
}

// GitHub renders GitHub Actions workflow commands, so problems appear as
// inline annotations on the pull request.
type GitHub struct{}

func (GitHub) Name() string { return "github" }

func (GitHub) Write(w io.Writer, ds []Diagnostic) error {
	for _, d := range ds {
		level := "notice"
		switch d.Severity {
		case SevError:
			level = "error"
		case SevWarn:
			level = "warning"
		}
		msg := d.Message
		if d.Hint != "" {
			msg += " (" + d.Hint + ")"
		}
		// A newline would terminate the workflow command early.
		msg = strings.ReplaceAll(msg, "\n", " ")
		if _, err := fmt.Fprintf(w, "::%s file=%s,line=%d::%s\n", level, d.File, d.Line, msg); err != nil {
			return err
		}
	}
	return nil
}

// GitLab renders GitLab Code Quality report JSON.
type GitLab struct{}

func (GitLab) Name() string { return "gitlab" }

func (GitLab) Write(w io.Writer, ds []Diagnostic) error {
	type qlines struct {
		Begin int `json:"begin"`
	}
	type qlocation struct {
		Path  string `json:"path"`
		Lines qlines `json:"lines"`
	}
	type qissue struct {
		Description string    `json:"description"`
		CheckName   string    `json:"check_name"`
		Fingerprint string    `json:"fingerprint"`
		Severity    string    `json:"severity"`
		Location    qlocation `json:"location"`
	}

	out := make([]qissue, 0, len(ds))
	for _, d := range ds {
		severity := "info"
		switch d.Severity {
		case SevError:
			severity = "major"
		case SevWarn:
			severity = "minor"
		}
		// GitLab dedupes by fingerprint across pipeline runs, so it must be
		// stable for the same problem in the same place.
		h := fnv.New64a()
		fmt.Fprintf(h, "%s:%d:%s:%s", d.File, d.Line, d.Check, d.Message)
		out = append(out, qissue{
			Description: d.Message,
			CheckName:   d.Check,
			Fingerprint: hex.EncodeToString(h.Sum(nil)),
			Severity:    severity,
			Location:    qlocation{Path: d.File, Lines: qlines{Begin: d.Line}},
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/diag/ -v`
Expected: PASS — every test in the package

- [ ] **Step 6: Write `repos.go`**

Create `internal/config/repos.go`:

```go
package config

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Repo is one entry in repos.yaml.
type Repo struct {
	URL   string   `yaml:"url"`
	Paths []string `yaml:"paths"`
}

// Repos is the loaded repos.yaml.
type Repos struct {
	Repos []Repo `yaml:"repos"`
}

// DefaultPatterns is where entities live when repos.yaml says nothing.
//
// "." matches a service.yaml at the repository root — the single-service repo
// shape in spec §5.1. Without it such a repo validates zero entities and
// exits 0, which is the worst possible first run.
var DefaultPatterns = []string{".", "services/*", "workers/*", "libs/*", "topics/*"}

// LoadRepos reads repos.yaml, always returning a usable value.
func LoadRepos(path string, data []byte, c *diag.Collector) *Repos {
	r := &Repos{}
	if err := yaml.Unmarshal(data, r); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: 1,
			Check: "repos-parse", Message: fmt.Sprintf("cannot parse repos file: %v", err),
		})
		return &Repos{}
	}
	return r
}

// LocalPatterns returns the glob patterns for the first repo entry, which by
// convention is the repository the command is running in. `landsraad validate`
// is hermetic and never fetches the others; that is the platform build's job.
func (r *Repos) LocalPatterns() []string {
	if len(r.Repos) == 0 || len(r.Repos[0].Paths) == 0 {
		return DefaultPatterns
	}
	return r.Repos[0].Paths
}
```

- [ ] **Step 7: Create the broken fixture**

```bash
mkdir -p testdata/monorepo-broken/services/api
mkdir -p testdata/monorepo-broken/services/dup
mkdir -p testdata/monorepo-broken/services/loop-a
mkdir -p testdata/monorepo-broken/services/loop-b
```

Create `testdata/monorepo-broken/teams.yaml`:

```yaml
teams:
  - name: team-payments
    members: [alice]
    slack: "#payments"
    pagerduty: PAY
```

Create `testdata/monorepo-broken/services/api/service.yaml` — unknown owner, dangling ref, missing runbook:

```yaml
apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  owner: team-payment
  tier: 1
  lifecycle: production
spec:
  language: go
  path: services/api
  runbook: services/api/docs/runbook.md
  dependsOn:
    - service:nowhere
```

Create `testdata/monorepo-broken/services/loop-a/service.yaml` and
`testdata/monorepo-broken/services/loop-b/service.yaml` — a dependency cycle,
so `reportCycles` is exercised end to end and not only in unit tests:

```yaml
apiVersion: landsraad/v1
kind: Service
metadata:
  name: loop-a
  owner: team-payments
  tier: 3
  lifecycle: production
spec:
  path: services/loop-a
  dependsOn:
    - service:loop-b
```

```yaml
apiVersion: landsraad/v1
kind: Service
metadata:
  name: loop-b
  owner: team-payments
  tier: 3
  lifecycle: production
spec:
  path: services/loop-b
  dependsOn:
    - service:loop-a
```

Create `testdata/monorepo-broken/services/dup/service.yaml` — collides with `api`:

```yaml
apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  owner: team-payments
  tier: 2
  lifecycle: production
spec:
  language: go
  path: services/dup
```

- [ ] **Step 8: Write the failing command test**

Create `cmd/landsraad/validate_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// validate is the shared harness. out carries only the format payload;
// errOut carries the human lines. Keeping them separate in the tests is what
// stops the two being conflated in the implementation.
func validate(fsys fstest.MapFS) (code int, out, errOut string) {
	var o, e bytes.Buffer
	c := Validate(fsys, &o, &e, diag.Text{})
	return c, o.String(), e.String()
}

func TestValidateOnGoodRepoExitsZero(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(os.DirFS("../../testdata/monorepo-ok"), &out, &errOut, diag.Text{})
	if code != exitOK {
		t.Errorf("exit code = %d, want %d\nout:\n%s\nerr:\n%s", code, exitOK, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "ok: 3 entities validated") {
		t.Errorf("expected 3 entities (2 services + 1 topic), got:\n%s", errOut.String())
	}
}

func TestValidateOnBrokenRepoExitsTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{}); code != exitValidation {
		t.Errorf("exit code = %d, want %d", code, exitValidation)
	}
}

// The point of the collector: one run reports every problem, not the first.
func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	var out, errOut bytes.Buffer
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{})
	got := out.String()
	for _, want := range []string{
		"unknown-owner",    // owner: team-payment
		"duplicate-name",   // two entities named api
		"missing-file",     // runbook does not exist
		"dependency-cycle", // loop-a <-> loop-b
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a single run must report %q; output was:\n%s", want, got)
		}
	}
}

// reportCycles has no other coverage: assert its rendered message exactly.
func TestValidateRendersTheCycleMessage(t *testing.T) {
	var out, errOut bytes.Buffer
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{})
	if !strings.Contains(out.String(), "dependency cycle: service:loop-a -> service:loop-b -> service:loop-a") {
		t.Errorf("cycle message missing or malformed:\n%s", out.String())
	}
}

// validate is hermetic: a reference to another repo is not an error here.
func TestValidateToleratesCrossRepoRefs(t *testing.T) {
	repo := fstest.MapFS{
		"repos.yaml": {Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\nspec:\n" +
				"  dependsOn:\n    - service:lives-in-another-repo\n")},
	}
	code, out, _ := validate(repo)
	if code != exitOK {
		t.Errorf("a cross-repo ref must not fail validate, got exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "dangling-ref") {
		t.Errorf("validate sees one repo and must not report cross-repo refs:\n%s", out)
	}
}

// The whole pipeline runs against an in-memory filesystem with no disk at all.
func TestValidateRunsEntirelyInMemory(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n    members: [alice]\n")},
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\nspec:\n  language: go\n")},
	}
	if code, out, _ := validate(repo); code != exitOK {
		t.Errorf("exit code = %d, want %d\n%s", code, exitOK, out)
	}
}

func TestValidateRequiresTeamsFile(t *testing.T) {
	repo := fstest.MapFS{
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	code, out, _ := validate(repo)
	if code != exitValidation {
		t.Errorf("a missing teams.yaml must fail validation, got exit %d", code)
	}
	if !strings.Contains(out, "teams.yaml") {
		t.Errorf("the diagnostic must name teams.yaml:\n%s", out)
	}
}

// A repo whose services live somewhere the patterns do not reach must FAIL.
// Exiting 0 here is the silently-inert-linter bug: a team wires landsraad into
// CI, gets a green check forever, and validates nothing.
func TestValidateFailsWhenNothingMatches(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"apps/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	code, out, _ := validate(repo)
	if code != exitValidation {
		t.Errorf("matching zero files must fail, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "no-entities") {
		t.Errorf("the diagnostic must explain that nothing matched:\n%s", out)
	}
}

// A single-service repo with service.yaml at the root must be discovered.
func TestValidateFindsARootLevelService(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: edge-gateway\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	if code, out, _ := validate(repo); code != exitOK {
		t.Errorf("a root-level service.yaml must be found, got exit %d:\n%s", code, out)
	}
}

// A missing repos.yaml is tolerated but must not be silent (spec §12).
func TestValidateAnnouncesDefaultPatterns(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	_, out, _ := validate(repo)
	if !strings.Contains(out, "default-patterns") {
		t.Errorf("defaulting must be visible in the output, not only in a log:\n%s", out)
	}
}

// --format json must be pipeable to jq on SUCCESS, which is the common case.
// The ok-line belongs on stderr; if it lands on stdout the payload is corrupt.
func TestJSONOutputIsParseableOnSuccess(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(os.DirFS("../../testdata/monorepo-ok"), &out, &errOut, diag.JSON{})
	if code != exitOK {
		t.Fatalf("fixture must be clean, got exit %d:\n%s", code, out.String())
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Errorf("stdout must be valid JSON on success, got %q: %v", out.String(), err)
	}
	if strings.Contains(out.String(), "ok:") {
		t.Error("the ok-line must go to stderr, never into the format payload")
	}
}

func TestJSONOutputIsParseableOnFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.JSON{})
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Errorf("stdout must be valid JSON on failure too: %v", err)
	}
	if len(ds) == 0 {
		t.Error("the broken fixture must produce diagnostics")
	}
}
```

- [ ] **Step 9: Run it to verify it fails**

Run: `go test ./cmd/landsraad/ -v`
Expected: FAIL — `undefined: Validate`

- [ ] **Step 10: Write `validate.go`**

Create `cmd/landsraad/validate.go`:

```go
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/schema"
)

// Validate is the hermetic pipeline: an explicit composition of the typed
// stages, in the only order the types permit. Compare spec §7 — this function
// is that table, and nothing more.
//
// It takes an fs.FS rather than a path, so the same pipeline runs against a
// local checkout, a fetched remote repo, or a test fixture in memory.
func Validate(fsys fs.FS, out, errOut io.Writer, f diag.Formatter) int {
	var c diag.Collector

	// 1. discover — which files are we looking at
	patterns := patternsFor(fsys, &c)
	paths, err := discover.Find(fsys, patterns)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return exitUsage
	}
	// Matching nothing at all is the single most likely way a first run goes
	// wrong: a team whose code lives under apps/* would otherwise get a green
	// check forever on a repo the tool never looked at.
	if len(paths) == 0 {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			File:     "repos.yaml",
			Line:     1,
			Check:    "no-entities",
			Message: fmt.Sprintf("no %s found under any configured path (%s)",
				discover.Filename, strings.Join(patterns, ", ")),
			Hint: "add a repos.yaml listing the paths your services live under",
		})
	}
	files := discover.Load(fsys, paths, &c)

	// 2. schema — structural validation, the precise messages
	validator, err := schema.Default()
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return exitUsage
	}
	for _, file := range files {
		validator.Validate("", file.Path, file.Data, &c)
	}

	// 3. parse and merge — pure, no IO
	cat := catalog.NewCatalog(catalog.ParseAll(localRepoName(fsys), files, &c), &c)

	// 4. resolve — LocalOnly: this repo cannot see entities defined elsewhere
	g := cat.Resolve(catalog.LocalOnly, &c)
	reportCycles(cat, g, &c)

	// 5. semantic checks
	checkOwners(fsys, cat, &c)
	catalog.CheckFiles(fsys, cat, &c)

	// 6. report
	// out carries ONLY the selected format's payload, so `--format json` stays
	// pipeable to jq and the GitLab report stays a valid artifact. Everything
	// human goes to errOut.
	if err := f.Write(out, c.Diagnostics()); err != nil {
		fmt.Fprintf(errOut, "error: cannot write output: %v\n", err)
		return exitUsage
	}
	if c.HasErrors() {
		return exitValidation
	}
	fmt.Fprintf(errOut, "ok: %d entities validated, no problems found\n", len(cat.Entities))
	return exitOK
}

// localRepoName names the repo being validated, for provenance in diagnostics.
// It is empty when repos.yaml is absent, and Entity.Location() renders that
// case without a dangling prefix.
func localRepoName(fsys fs.FS) string {
	data, err := fs.ReadFile(fsys, "repos.yaml")
	if err != nil {
		return ""
	}
	var discard diag.Collector
	r := config.LoadRepos("repos.yaml", data, &discard)
	if len(r.Repos) == 0 {
		return ""
	}
	return path.Base(strings.TrimSuffix(r.Repos[0].URL, "/"))
}

// patternsFor reads repos.yaml if present, falling back to the conventional
// layout. A repo without repos.yaml still works out of the box.
func patternsFor(fsys fs.FS, c *diag.Collector) []string {
	data, err := fs.ReadFile(fsys, "repos.yaml")
	if err != nil {
		// Not an error — but not silent either. Spec §12: degraded mode must
		// be visible in the artifact, not only in a log. Compare checkOwners,
		// which errors loudly for a missing teams.yaml.
		c.Add(diag.Diagnostic{
			Severity: diag.SevInfo,
			File:     "repos.yaml",
			Line:     1,
			Check:    "default-patterns",
			Message: fmt.Sprintf("no repos.yaml found; using default paths (%s)",
				strings.Join(config.DefaultPatterns, ", ")),
			Hint: "add repos.yaml if your services live elsewhere",
		})
		return config.DefaultPatterns
	}
	return config.LoadRepos("repos.yaml", data, c).LocalPatterns()
}

func checkOwners(fsys fs.FS, cat *catalog.Catalog, c *diag.Collector) {
	data, err := fs.ReadFile(fsys, "teams.yaml")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root",
			Hint:    "every entity's owner must resolve to a team defined there",
		})
		return
	}
	config.LoadTeams("teams.yaml", data, c).ValidateOwners(cat, c)
}

func reportCycles(cat *catalog.Catalog, g *catalog.Graph, c *diag.Collector) {
	for _, cyc := range g.Cycles() {
		d := diag.Diagnostic{
			Severity: diag.SevError, Line: 1,
			Check:   "dependency-cycle",
			Message: fmt.Sprintf("dependency cycle: %s", joinRefs(cyc)),
			Hint:    "break the loop, or model one direction as a shared library",
		}
		if e, ok := cat.Lookup(cyc[0]); ok {
			d.File, d.Line, d.Entity = e.SourcePath, e.NameLine, e.Metadata.Name
		}
		c.Add(d)
	}
}

func joinRefs(rs []catalog.Ref) string {
	if len(rs) == 0 {
		return ""
	}
	out := ""
	for _, r := range rs {
		out += r.String() + " -> "
	}
	return out + rs[0].String()
}

func newValidateCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "validate [root]",
		Short: "Truthsayer — validate this repository's catalog files",
		Long: "Truthsayer detects metadata that lies about reality.\n\n" +
			"It runs offline against a single repository: no network access, no " +
			"tokens, and no resolution of references to entities in other " +
			"repositories — the platform build does that.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			info, err := os.Stat(root)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("%s is not a directory", root)
			}
			name := format
			if name == "auto" {
				name = "text"
				if os.Getenv("GITHUB_ACTIONS") == "true" {
					name = "github"
				} else if os.Getenv("GITLAB_CI") == "true" {
					name = "gitlab"
				}
			}
			f, ok := diag.Lookup(name)
			if !ok {
				return fmt.Errorf("unknown format %q, want one of %v", name, diag.FormatNames())
			}
			cmd.SilenceUsage = true
			// os.DirFS is the single place this program touches os for reading.
			if code := Validate(os.DirFS(root), cmd.OutOrStdout(), cmd.ErrOrStderr(), f); code != exitOK {
				os.Exit(code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "auto",
		"output format: auto, "+strings.Join(diag.FormatNames(), ", "))
	return cmd
}
```

- [ ] **Step 11: Rewrite `main.go`**

Replace `cmd/landsraad/main.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/schema"
)

// Version is the binary version, overridden at release time with -ldflags.
var Version = "dev"

// Exit codes. See the spec, §12.
const (
	exitOK         = 0
	exitUsage      = 1
	exitValidation = 2
)

func main() {
	root := &cobra.Command{
		Use:   "landsraad",
		Short: "A lightweight developer portal for small teams",
	}
	root.AddCommand(newValidateCmd())
	root.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for service.yaml",
		Long: "Print the embedded JSON Schema. Redirect it to schema/service.schema.json " +
			"and point yaml-language-server at that file for editor autocompletion.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := cmd.OutOrStdout().Write(schema.Raw)
			return err
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "landsraad %s\n", Version)
		},
	})
	if err := root.Execute(); err != nil {
		os.Exit(exitUsage)
	}
}
```

- [ ] **Step 12: Run everything**

Run: `task ci`
Expected: PASS — lint clean, every test green

- [ ] **Step 13: Prove the `os` boundary holds**

```bash
grep -rn '"os"' internal/ | grep -v _test.go
```

Expected: **no output.** `os` appears only in `cmd/` and in tests. If any
`internal/` package imports `os`, the fs.FS seam is already leaking and Plan 3
will pay for it.

- [ ] **Step 14: Verify by hand**

```bash
task build
./bin/landsraad validate testdata/monorepo-ok
./bin/landsraad validate testdata/monorepo-broken; echo "exit=$?"
./bin/landsraad validate testdata/monorepo-broken --format gitlab | head -20
```

Expected: the first prints `ok: 3 entities validated, no problems found`. The
second lists the unknown owner with a `did you mean "team-payments"?` hint, the
duplicate name naming both files, and the missing runbook — then `exit=2`. The
third prints Code Quality JSON.

- [ ] **Step 15: Commit**

```bash
git add cmd/landsraad/ internal/diag/format.go internal/diag/format_test.go internal/config/repos.go testdata/
git commit -m "feat: validate command composed from typed stages, four output formats"
```

---

## Task 11: The on-ramp

The review's pre-mortem walked a six-person team through their first hour and
they quit inside it: no licence, no README, no idea what `teams.yaml` contains,
`landsraad validate` from a subdirectory failing with "not found at the
repository root", and `landsraad version` printing `dev` in every bug report.
None of that is a code defect and all of it is fatal to adoption.

**Files:**
- Create: `cmd/landsraad/init.go`, `cmd/landsraad/root.go`, `README.md`, `CONTRIBUTING.md`
- Modify: `cmd/landsraad/main.go` — real version, register `init`
- Test: `cmd/landsraad/init_test.go`, `cmd/landsraad/root_test.go`

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `func findRoot(start string) (string, error)` — nearest ancestor holding `repos.yaml`, `teams.yaml` or `.git`
  - `func version() string` — module version from the build, falling back to `Version`
  - `func newInitCmd() *cobra.Command`

- [ ] **Step 1: Write the failing root-discovery test**

Create `cmd/landsraad/root_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRootWalksUp(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "services", "payments-worker", "docs")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "teams.yaml"), []byte("teams: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findRoot(deep)
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	want, _ := filepath.EvalSymlinks(base)
	gotEval, _ := filepath.EvalSymlinks(got)
	if gotEval != want {
		t.Errorf("findRoot(%q) = %q, want %q — running from a subdirectory is "+
			"the normal case and must work", deep, gotEval, want)
	}
}

func TestFindRootFailsWithAClearMessage(t *testing.T) {
	_, err := findRoot(t.TempDir())
	if err == nil {
		t.Fatal("a directory with no markers above it must be an error")
	}
	if !strings.Contains(err.Error(), "repos.yaml") {
		t.Errorf("the error must say what it looked for, got %q", err)
	}
}

func TestVersionFallsBackToTheConstant(t *testing.T) {
	if version() == "" {
		t.Error("version() must never be empty — every bug report quotes it")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/landsraad/ -run 'TestFindRoot|TestVersion' -v`
Expected: FAIL — `undefined: findRoot`

- [ ] **Step 3: Write `root.go`**

Create `cmd/landsraad/root.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
)

// rootMarkers identify a repository root, most specific first.
var rootMarkers = []string{"repos.yaml", "teams.yaml", ".git"}

// findRoot walks up from start looking for a repository root, the way every
// linter does. Without it, running `landsraad validate` from inside
// services/foo/ reports "teams.yaml not found at the repository root" while
// standing in a subdirectory of a perfectly valid repo.
func findRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		for _, marker := range rootMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no repository root above %s: looked for %v "+
				"(run `landsraad init` to create them)", start, rootMarkers)
		}
		dir = parent
	}
}

// version reports the module version recorded at build time, so a bug report
// quotes something useful. `go install ...@latest` applies no -ldflags, which
// is why every user would otherwise report "dev".
func version() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return Version
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./cmd/landsraad/ -run 'TestFindRoot|TestVersion' -v`
Expected: PASS — all three tests

- [ ] **Step 5: Write the failing `init` test**

Create `cmd/landsraad/init_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInitWritesAWorkingCatalog(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runInit(dir, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	for _, f := range []string{"teams.yaml", "repos.yaml", "services/example/service.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("init did not create %s", f)
		}
	}

	// The whole point: what init writes must pass validate immediately.
	var vout, verr bytes.Buffer
	if code := Validate(os.DirFS(dir), &vout, &verr, diagText()); code != exitOK {
		t.Errorf("a freshly initialised repo must validate, got exit %d\n%s", code, vout.String())
	}
}

func TestInitDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	keep := []byte("teams:\n  - name: mine\n")
	if err := os.WriteFile(filepath.Join(dir, "teams.yaml"), keep, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runInit(dir, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "teams.yaml"))
	if string(got) != string(keep) {
		t.Error("init must never overwrite a file that already exists")
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./cmd/landsraad/ -run TestInit -v`
Expected: FAIL — `undefined: runInit`

- [ ] **Step 7: Write `init.go`**

Create `cmd/landsraad/init.go`:

```go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// diagText exists so tests can name the default formatter without importing
// the whole registry surface.
func diagText() diag.Formatter { return diag.Text{} }

// scaffold is what `landsraad init` writes. Every file is valid on the first
// run: the acceptance test for init is that validate passes immediately after.
var scaffold = []struct {
	path string
	body string
}{
	{"teams.yaml", `teams:
  - name: team-example
    members: [you]
    slack: "#team-example"
    pagerduty: EXAMPLE
`},
	{"repos.yaml", `# The first entry is the repository you are in.
repos:
  - url: https://github.com/your-org/your-repo
    paths: [services/*, workers/*, libs/*]
`},
	{"services/example/service.yaml", `# yaml-language-server: $schema=../../schema/service.schema.json
apiVersion: landsraad/v1
kind: Service
metadata:
  name: example
  description: Replace me with a real service.
  owner: team-example
  tier: 3
  lifecycle: experimental
spec:
  language: go
  path: services/example
`},
}

func runInit(root string, out io.Writer) error {
	for _, f := range scaffold {
		full := filepath.Join(root, filepath.FromSlash(f.path))
		if _, err := os.Stat(full); err == nil {
			fmt.Fprintf(out, "  skip    %s (already exists)\n", f.path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(f.body), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "  created %s\n", f.path)
	}
	fmt.Fprintf(out, "\nNext: run `landsraad schema > schema/service.schema.json` for editor\n"+
		"autocompletion, then `landsraad validate`.\n")
	return nil
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [root]",
		Short: "Missionaria — scaffold teams.yaml, repos.yaml and a first service",
		Long: "Write a minimal, valid catalog into this repository. Existing files " +
			"are never overwritten. What init produces passes `landsraad validate` " +
			"on the first run.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			return runInit(root, cmd.OutOrStdout())
		},
	}
}
```

- [ ] **Step 8: Register `init` and use the real version and root**

In `cmd/landsraad/main.go`, add `root.AddCommand(newInitCmd())` beside the
other commands, and change the version command to print `version()` rather
than `Version`.

In `cmd/landsraad/validate.go`, replace the `os.Stat(root)` check inside
`newValidateCmd`'s `RunE` with root discovery:

```go
			resolved, err := findRoot(root)
			if err != nil {
				return err
			}
```

and pass `os.DirFS(resolved)` to `Validate`.

- [ ] **Step 9: Run everything**

Run: `task ci`
Expected: PASS

- [ ] **Step 10: Write `README.md`**

The first hour is the product. Cover, in this order: what it is in two
sentences; `go install github.com/landsraadhq/landsraad/cmd/landsraad@latest`;
`landsraad init`; what `teams.yaml`, `repos.yaml` and `service.yaml` each
contain, with the worked examples from §5 and §6 of the spec; wiring
`landsraad validate` into CI with a copyable GitHub Actions and GitLab CI
snippet; the editor-autocompletion setup; and a "why not Backstage / Cortex /
OpsLevel / Port / a README table" section stating plainly that landsraad is
cheaper to run and more expensive to feed.

- [ ] **Step 11: Write `CONTRIBUTING.md`**

Cover: `task ci` must pass; tests are table-driven and assert **exact**
diagnostic messages (spec §14), because the wording is the product; new
scorecard checks are Go functions with stable ids, not a plugin system (D4);
all file access goes through `io/fs.FS` and nothing under `internal/` may
import `os` (spec §3.1); commits are conventional; no AI attribution.

- [ ] **Step 12: Commit**

```bash
git add cmd/landsraad/ README.md CONTRIBUTING.md
git commit -m "feat: init command, root discovery, real version, README"
```

---

## Definition of done

**Composition (spec §3.1) — these are pass/fail, not aspirations:**

- [ ] `grep -rn '"os"' internal/ | grep -v _test.go` returns **nothing**
- [ ] `grep -rn 'sync.Once\|^func init(' internal/` returns **nothing** — no hidden initialisation. (`AllKinds`, `DefaultPatterns` and `schema.Raw` are exported package-level slices, therefore technically mutable by an importer; they are read-only by convention and never written after init.)
- [ ] Every stage function takes its input type and a `*diag.Collector`, and returns its output type. None takes a root path string.
- [ ] `Validate` reads top to bottom as the stage list in spec §7, with no branching on where the files came from
- [ ] `grep -rn 'diag.Registry\|NewRegistry' .` returns **nothing** — the interface is the extension point
- [ ] `cat.Cycles()` does not compile; cycles are reachable only through the `*Graph` that `Resolve` returns
- [ ] `catalog.ParseAll` does no IO — the package imports `io/fs` only in `files.go`
- [ ] A new output format is a new type implementing `Formatter` plus one line in `Formatters()` — no `switch` to edit and no `init()` registration

**Function:**

- [ ] `task ci` passes from a clean checkout
- [ ] `landsraad validate testdata/monorepo-ok` exits 0
- [ ] `landsraad validate testdata/monorepo-broken` exits 2 and reports **all three** seeded problems in one run
- [ ] Every diagnostic in that output carries a file and a non-zero line
- [ ] `task schema` leaves `schema/service.schema.json` byte-identical to the committed copy
- [ ] No test reaches the network
- [ ] `go.mod` requires exactly: `yaml.v3`, `jsonschema/v6`, `cobra`, `go-cmp`
- [ ] The full pipeline runs against `fstest.MapFS` with no disk access (`TestValidateRunsEntirelyInMemory`)
- [ ] All four output formats work: text, json, github, gitlab

**One-way doors (they land in other people's repositories):**

- [ ] A name ending in `-`, `.` or `_` is rejected
- [ ] `tier` is required for `Service`/`Worker`/`Cron`/`API` and optional for the rest
- [ ] `metadata.annotations`, `metadata.labels`, `metadata.aliases` and `spec.exemptions` all validate
- [ ] `kind: Resource` with a free-string `spec.type` validates
- [ ] A `providesApis` typo is an error, not silently accepted
- [ ] `teams.yaml` rejects an unknown key, and its diagnostics carry real line numbers

**The first hour:**

- [ ] `LICENSE` exists and is Apache-2.0
- [ ] `landsraad init` in an empty directory produces a repo that `landsraad validate` passes
- [ ] `landsraad validate` works from a subdirectory, not only the repo root
- [ ] `landsraad version` reports the module version for a `go install` build, not `dev`
- [ ] `README.md` and `CONTRIBUTING.md` exist

## What this plan deliberately leaves out

Handled by later plans, not gaps:

- **Scorecard, `standards.yaml`, `.landsraad/checks/`** — Plan 2
- **CODEOWNERS, alert routing, Slack map, `gen --check`** — Plan 2
- **Fetching remote repos, GitHub and GitLab adapters** — Plan 3
- **Rendering, goldmark, admonitions, search, `build`, `serve`** — Plan 3
- **Exit code 3** — arrives with the scorecard gate in Plan 2
- **Pipeline stage 6 (ingesting `.landsraad/checks/*.yaml`)** — deferred with the
  schema that defines the file's shape; spec §7.1 records the consequence
- **Resolving `metadata.aliases` when matching references** — the field is
  accepted and validated in v1 so it can be populated; resolution through it
  lands with the merged catalog in Plan 3
