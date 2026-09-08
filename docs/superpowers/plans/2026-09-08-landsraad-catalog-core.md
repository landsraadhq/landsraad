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
- **Strict schema:** `additionalProperties: false` everywhere. Unknown fields are rejected, never ignored.
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
| `internal/diag/format.go` | text, JSON and CI-annotation output |
| `internal/catalog/entity.go` | `Kind`, `Entity`, `Metadata`, `Spec` |
| `internal/catalog/parse.go` | YAML → `Entity`, preserving line numbers |
| `internal/catalog/ref.go` | `Ref` parse and format |
| `internal/catalog/merge.go` | `Catalog`, merge, collision detection |
| `internal/catalog/graph.go` | reference resolution, cycle detection |
| `internal/catalog/files.go` | referenced-file existence checks |
| `internal/schema/schema.go` | embedded JSON Schema, structural validation |
| `internal/schema/service.schema.json` | the schema — canonical, embedded in the binary |
| `schema/service.schema.json` | generated copy for editors, written by `task schema` |
| `internal/config/teams.go` | `teams.yaml` |
| `internal/config/repos.go` | `repos.yaml` |
| `internal/discover/discover.go` | glob `service.yaml` from configured paths |
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

- [ ] **Step 3: Write `.gitignore`**

```
bin/
dist/
```

- [ ] **Step 4: Write the failing test**

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

- [ ] **Step 5: Run it to verify it fails**

Run: `task test`
Expected: FAIL — `undefined: Version`

- [ ] **Step 6: Write the minimal implementation**

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

- [ ] **Step 7: Run it to verify it passes**

Run: `task test`
Expected: PASS

- [ ] **Step 8: Write the CI workflow**

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

- [ ] **Step 9: Commit**

```bash
git add go.mod Taskfile.yml .gitignore .github/workflows/ci.yml cmd/landsraad/main.go cmd/landsraad/main_test.go
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

- [ ] **Step 1: Write the failing test**

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

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/diag/ -v`
Expected: FAIL — `undefined: Collector`

- [ ] **Step 3: Write the implementation**

Create `internal/diag/diag.go`:

```go
// Package diag collects validation problems so a single run can report all
// of them. Nothing in landsraad fails fast: every stage appends here and
// continues, so validating twelve broken services reports twelve problems.
package diag

import "sort"

// Severity ranks a diagnostic. Only SevError affects the exit code.
type Severity int

const (
	SevInfo Severity = iota
	SevWarn
	SevError
)

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

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/diag/ -v`
Expected: PASS — all three tests

- [ ] **Step 5: Commit**

```bash
git add internal/diag/
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
)

// AllKinds is the complete set, used by validation and by the JSON Schema test.
var AllKinds = []Kind{
	KindService, KindWorker, KindCron, KindLibrary,
	KindTopic, KindDatabase, KindAPI,
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
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Owner       string   `yaml:"owner"`
	Tier        int      `yaml:"tier"`
	Lifecycle   string   `yaml:"lifecycle"`
	Tags        []string `yaml:"tags"`
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

// Spec is the operational block.
type Spec struct {
	Language     string   `yaml:"language"`
	Path         string   `yaml:"path"`
	Docs         string   `yaml:"docs"`
	Runbook      string   `yaml:"runbook"`
	Oncall       string   `yaml:"oncall"`
	RepoURL      string   `yaml:"repoUrl"`
	Links        []Link   `yaml:"links"`
	DependsOn    []string `yaml:"dependsOn"`
	ProvidesApis []string `yaml:"providesApis"`
	SLO          []SLO    `yaml:"slo"`
	Alerts       string   `yaml:"alerts"`
	Runtime      *Runtime `yaml:"runtime"`
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
	_, ok := ParseFile("monorepo", "broken/service.yaml", []byte("kind: Service\n  bad indent\n"), &c)
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
	"strings"
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
	msg := c.Diagnostics()[0].Message
	// The message must name BOTH sides — that is the whole point of provenance.
	for _, want := range []string{"api", "monorepo", "edge-gateway"} {
		if !strings.Contains(msg, want) {
			t.Errorf("collision message %q must mention %q", msg, want)
		}
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
					"duplicate entity name %q: already defined as %s in %s:%s (line %d), redefined in %s:%s",
					e.Metadata.Name, ref, prev.SourceRepo, prev.SourcePath, prev.NameLine,
					e.SourceRepo, e.SourcePath),
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
  - `func (c *Catalog) Resolve(local bool, col *diag.Collector)` — resolves every `dependsOn`; when `local` is true, references to entities absent from the catalog are recorded rather than reported (a service repo cannot see other repos)
  - `func (c *Catalog) Dependents(r Ref) []Ref` — reverse edges, sorted
  - `type Cycle []Ref`; `func (c *Catalog) Cycles() []Cycle`

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/graph_test.go`:

```go
package catalog

import (
	"strings"
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
	cat.Resolve(false, &c)

	if !c.HasErrors() {
		t.Fatal("a dangling reference must be an error when resolving the full catalog")
	}
	msg := c.Diagnostics()[0].Message
	if !strings.Contains(msg, "service:nowhere") {
		t.Errorf("message %q must name the unresolved reference", msg)
	}
}

func TestResolveToleratesDanglingRefsWhenLocal(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:in-another-repo"),
	}, &c)
	cat.Resolve(true, &c)

	if c.HasErrors() {
		t.Errorf("`landsraad validate` runs on one repo and cannot see the others; "+
			"cross-repo refs must not be errors there. Got: %+v", c.Diagnostics())
	}
}

func TestResolveReportsMalformedRefs(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{entDeps("a", KindService, "noprefix")}, &c)
	cat.Resolve(true, &c)

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
	cat.Resolve(false, &c)

	cycles := cat.Cycles()
	if len(cycles) == 0 {
		t.Fatal("a <-> b is a cycle and must be detected")
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
	cat.Resolve(false, &c)

	if cycles := cat.Cycles(); len(cycles) != 0 {
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
	cat.Resolve(false, &c)

	got := cat.Dependents(Ref{Kind: KindTopic, Name: "t"})
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

// Resolve walks every dependsOn entry and builds the forward and reverse edge
// maps.
//
// When local is true the caller is validating a single repo and cannot see
// entities defined elsewhere, so an unresolvable reference is recorded and
// skipped rather than reported. Malformed references are always errors: they
// could never resolve in any repo.
func (c *Catalog) Resolve(local bool, col *diag.Collector) {
	c.edges = make(map[Ref][]Ref, len(c.Entities))
	c.reverse = make(map[Ref][]Ref, len(c.Entities))

	for _, e := range c.Entities {
		from := e.Ref()
		for _, raw := range e.Spec.DependsOn {
			to, err := ParseRef(raw)
			if err != nil {
				col.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "malformed-ref",
					Message:  err.Error(),
					Hint:     "references look like service:ledger-api or topic:payments.events",
				})
				continue
			}
			if _, found := c.Lookup(to); !found {
				if !local {
					col.Add(diag.Diagnostic{
						Severity: diag.SevError,
						Repo:     e.SourceRepo,
						File:     e.SourcePath,
						Line:     e.NameLine,
						Entity:   e.Metadata.Name,
						Check:    "dangling-ref",
						Message: fmt.Sprintf("%s depends on %s, which is not in the catalog",
							from, to),
						Hint: "check the spelling, or add the missing entity",
					})
				}
				// In local mode the target lives in another repo. Record no
				// edge: the platform build resolves it.
				continue
			}
			c.edges[from] = append(c.edges[from], to)
			c.reverse[to] = append(c.reverse[to], from)
		}
	}
	for k := range c.edges {
		sortRefs(c.edges[k])
	}
	for k := range c.reverse {
		sortRefs(c.reverse[k])
	}
}

func sortRefs(rs []Ref) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].String() < rs[j].String() })
}

// DependsOn returns the resolved outgoing edges, sorted.
func (c *Catalog) DependsOn(r Ref) []Ref { return c.edges[r] }

// Dependents returns everything that depends on r, sorted. This is what
// answers "who consumes this topic?" on an entity page.
func (c *Catalog) Dependents(r Ref) []Ref { return c.reverse[r] }

// Cycles returns every dependency loop, using an iterative depth-first search
// with a recursion stack. Each cycle is reported once.
func (c *Catalog) Cycles() []Cycle {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // finished
	)
	state := make(map[Ref]int, len(c.Entities))
	var path []Ref
	var found []Cycle
	seen := make(map[string]bool)

	var visit func(Ref)
	visit = func(r Ref) {
		state[r] = grey
		path = append(path, r)
		for _, next := range c.edges[r] {
			switch state[next] {
			case white:
				visit(next)
			case grey:
				// Found a loop: take the path back to where next appears.
				for i, p := range path {
					if p == next {
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
	for _, e := range c.Entities {
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

- [ ] **Step 4: Add the edge maps to `Catalog`**

In `internal/catalog/merge.go`, extend the struct and the constructor:

```go
type Catalog struct {
	Entities []*Entity
	byRef    map[Ref]*Entity
	edges    map[Ref][]Ref
	reverse  map[Ref][]Ref
}
```

In `NewCatalog`, initialise them alongside `byRef`:

```go
	cat := &Catalog{
		Entities: sorted,
		byRef:    make(map[Ref]*Entity, len(sorted)),
		edges:    make(map[Ref][]Ref),
		reverse:  make(map[Ref][]Ref),
	}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/catalog/ -v`
Expected: PASS — every test, including the six new graph tests

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/
git commit -m "feat: dependency graph resolution and cycle detection"
```

---

## Task 7: JSON Schema

The schema is the single source of truth for structure, and it ships to editors. Validating against it — rather than hand-writing field checks in Go — is what stops the schema and the behaviour from drifting apart.

**Files:**
- Create: `internal/schema/service.schema.json`, `internal/schema/schema.go`
- Test: `internal/schema/schema_test.go`

**Interfaces:**
- Consumes: `diag.Collector`, `catalog.AllKinds`.
- Produces:
  - `func Validate(repo, path string, data []byte, c *diag.Collector) bool`
  - `var Raw []byte` — the embedded schema, printed by `landsraad schema`

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/santhosh-tekuri/jsonschema/v6@v6.0.1
```

- [ ] **Step 2: Write `internal/schema/service.schema.json`**

The canonical schema lives beside the Go file that embeds it, because `go:embed`
cannot reference a parent directory. The root `schema/service.schema.json` that
editors point at is generated from it by `task schema`.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://landsraad.dev/schema/service.schema.json",
  "title": "landsraad catalog entity",
  "type": "object",
  "additionalProperties": false,
  "required": ["apiVersion", "kind", "metadata"],
  "properties": {
    "apiVersion": { "const": "landsraad/v1" },
    "kind": {
      "enum": ["Service", "Worker", "Cron", "Library", "Topic", "Database", "API"]
    },
    "metadata": {
      "type": "object",
      "additionalProperties": false,
      "required": ["name", "owner", "tier", "lifecycle"],
      "properties": {
        "name": { "type": "string", "pattern": "^[a-z0-9][a-z0-9._-]*$", "maxLength": 63 },
        "description": { "type": "string" },
        "owner": { "type": "string", "minLength": 1 },
        "tier": { "type": "integer", "enum": [1, 2, 3] },
        "lifecycle": { "enum": ["experimental", "production", "deprecated"] },
        "tags": { "type": "array", "items": { "type": "string" } }
      }
    },
    "spec": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
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
            "additionalProperties": false,
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
            "additionalProperties": false,
            "required": ["name", "target"],
            "properties": {
              "name": { "type": "string" },
              "target": { "type": "string" },
              "window": { "type": "string" }
            }
          }
        },
        "alerts": { "type": "string" },
        "runtime": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "selector": {
              "type": "object",
              "additionalProperties": { "type": "string" }
            }
          }
        }
      }
    }
  }
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

func TestValidateAcceptsAGoodFile(t *testing.T) {
	var c diag.Collector
	if !Validate("monorepo", "a/service.yaml", []byte(good), &c) {
		t.Fatalf("a valid file must pass: %+v", c.Diagnostics())
	}
}

func TestValidateRejectsUnknownFields(t *testing.T) {
	in := strings.Replace(good, "  language: go\n", "  language: go\n  nonsense: yes\n", 1)
	var c diag.Collector
	if Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("additionalProperties is false — an unknown field must be rejected, not ignored")
	}
	if !c.HasErrors() {
		t.Fatal("rejection must produce an error diagnostic")
	}
	if c.Diagnostics()[0].Line == 0 {
		t.Error("a schema violation must carry a line number")
	}
}

func TestValidateRejectsBadTier(t *testing.T) {
	in := strings.Replace(good, "tier: 1", "tier: 9", 1)
	var c diag.Collector
	if Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("tier 9 is not one of 1, 2, 3")
	}
}

func TestValidateRejectsMissingOwner(t *testing.T) {
	in := strings.Replace(good, "  owner: team-payments\n", "", 1)
	var c diag.Collector
	if Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("owner is required")
	}
}

func TestValidateRejectsWrongAPIVersion(t *testing.T) {
	in := strings.Replace(good, "landsraad/v1", "platform/v1", 1)
	var c diag.Collector
	if Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("only landsraad/v1 is accepted")
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
Expected: FAIL — `undefined: Validate`

- [ ] **Step 5: Write the implementation**

Create `internal/schema/schema.go`:

```go
// Package schema validates catalog files against the JSON Schema that ships
// with the binary. The schema is the single source of truth for structure:
// validating against it, rather than hand-written Go checks, is what keeps
// the shipped schema and the tool's behaviour from drifting apart.
package schema

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
)

//go:embed service.schema.json
var Raw []byte

var (
	compileOnce sync.Once
	compiled    *jsonschema.Schema
	compileErr  error
)

func schema() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		var doc any
		if err := json.Unmarshal(Raw, &doc); err != nil {
			compileErr = fmt.Errorf("embedded schema is not valid JSON: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("service.schema.json", doc); err != nil {
			compileErr = fmt.Errorf("cannot add embedded schema: %w", err)
			return
		}
		compiled, compileErr = c.Compile("service.schema.json")
	})
	return compiled, compileErr
}

// Validate checks one file's bytes against the schema. It returns true when
// the file is structurally valid, and adds one diagnostic per violation.
func Validate(repo, path string, data []byte, c *diag.Collector) bool {
	sch, err := schema()
	if err != nil {
		// A broken embedded schema is a build defect, not user error.
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: repo, File: path, Line: 1,
			Check: "schema-internal", Message: err.Error(),
		})
		return false
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		// Parsing is reported by catalog.ParseFile; stay silent here.
		return false
	}
	var inst any
	if err := root.Decode(&inst); err != nil {
		return false
	}
	// yaml.v3 produces map[string]any for string keys, which the validator
	// accepts directly.
	if err := sch.Validate(inst); err != nil {
		var ve *jsonschema.ValidationError
		if ok := asValidationError(err, &ve); ok {
			for _, leaf := range leaves(ve) {
				loc := strings.TrimPrefix(leaf.InstanceLocation, "/")
				keys := splitLocation(loc)
				line := fieldLine(&root, keys...)
				if line == 0 {
					line = 1
				}
				c.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     repo,
					File:     path,
					Line:     line,
					Check:    "schema",
					Message:  describe(loc, leaf),
				})
			}
		} else {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, Repo: repo, File: path, Line: 1,
				Check: "schema", Message: err.Error(),
			})
		}
		return false
	}
	return true
}

func describe(loc string, ve *jsonschema.ValidationError) string {
	where := loc
	if where == "" {
		where = "document root"
	}
	return fmt.Sprintf("%s: %s", where, ve.ErrorKind.LocalizedString(nil))
}

// leaves returns the most specific errors in the tree. The root error is a
// generic "doesn't validate"; the causes carry the useful detail.
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

func splitLocation(loc string) []string {
	if loc == "" {
		return nil
	}
	return strings.Split(loc, "/")
}

// fieldLine walks a document node down a key path. Numeric path segments are
// array indices. Returns 0 when the path is not found.
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

Add `asValidationError` in the same file:

```go
func asValidationError(err error, target **jsonschema.ValidationError) bool {
	ve, ok := err.(*jsonschema.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}
```

- [ ] **Step 6: Run it to verify it passes**

Run: `go test ./internal/schema/ -v`
Expected: PASS — all six tests

If the `jsonschema/v6` API differs from the calls above (`AddResource`, `Compile`, `ValidationError.ErrorKind`, `InstanceLocation`), fix the calls against the pinned version's godoc before moving on. Do not change the tests — they encode the required behaviour, not the library's shape.

- [ ] **Step 7: Commit**

```bash
git add internal/schema/ go.mod go.sum
git commit -m "feat: JSON Schema validation with line-accurate diagnostics"
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
	"strings"
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
	if !strings.Contains(d.Hint, "team-payments") {
		t.Errorf("a near-miss owner must suggest the real team; hint was %q", d.Hint)
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
	"fmt"
	"sort"

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

	var f teamsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: 1,
			Check: "teams-parse", Message: fmt.Sprintf("cannot parse teams file: %v", err),
		})
		return t
	}
	for _, team := range f.Teams {
		if team.Name == "" {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Check: "teams-parse", Message: "a team entry has no name",
			})
			continue
		}
		if _, dup := t.byName[team.Name]; dup {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Check:   "teams-duplicate",
				Message: fmt.Sprintf("team %q is defined twice", team.Name),
			})
			continue
		}
		t.byName[team.Name] = team
	}
	return t
}

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

## Task 9: Discovery and referenced-file checks

**Files:**
- Create: `internal/discover/discover.go`, `internal/catalog/files.go`
- Test: `internal/discover/discover_test.go`, `internal/catalog/files_test.go`
- Create: `testdata/monorepo-ok/` fixtures

**Interfaces:**
- Consumes: `diag.Collector`, `Catalog`.
- Produces:
  - `func Find(root string, patterns []string) ([]string, error)` — repo-relative paths of every `service.yaml` under the given glob patterns, sorted
  - `func CheckFiles(root string, cat *Catalog, c *diag.Collector)` — verifies `spec.runbook`, `spec.docs`, `spec.alerts` exist on disk

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

import "testing"

func TestFindMatchesGlobPatterns(t *testing.T) {
	got, err := Find("../../testdata/monorepo-ok", []string{"services/*", "topics/*"})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	want := []string{
		"services/ledger-api/service.yaml",
		"services/payments-worker/service.yaml",
		"topics/payments-events/service.yaml",
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

func TestFindDotMeansRepoRoot(t *testing.T) {
	got, err := Find("../../testdata/monorepo-ok/services/ledger-api", []string{"."})
	if err != nil {
		t.Fatalf("Find returned error: %v", err)
	}
	if len(got) != 1 || got[0] != "service.yaml" {
		t.Errorf(`Find with pattern "." must find the root service.yaml, got %v`, got)
	}
}

func TestFindIgnoresDirectoriesWithoutServiceYAML(t *testing.T) {
	got, err := Find("../../testdata/monorepo-ok", []string{"nonexistent/*"})
	if err != nil {
		t.Fatalf("a pattern matching nothing is not an error, got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want no results", got)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/discover/ -v`
Expected: FAIL — `undefined: Find`

- [ ] **Step 4: Write `discover.go`**

```go
// Package discover locates service.yaml files under a repository root.
package discover

import (
	"os"
	"path/filepath"
	"sort"
)

// Filename is the fixed name of a catalog file.
const Filename = "service.yaml"

// Find returns repo-relative paths of every service.yaml matching the given
// glob patterns, sorted. A pattern of "." means the repository root itself.
// A pattern that matches nothing is not an error: a repo may legitimately
// have no services under some configured path.
func Find(root string, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	for _, pattern := range patterns {
		var dirs []string
		if pattern == "." {
			dirs = []string{root}
		} else {
			matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
			if err != nil {
				// Only ErrBadPattern is possible here, and that is a config bug.
				return nil, err
			}
			dirs = matches
		}
		for _, dir := range dirs {
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue
			}
			candidate := filepath.Join(dir, Filename)
			if _, err := os.Stat(candidate); err != nil {
				continue
			}
			rel, err := filepath.Rel(root, candidate)
			if err != nil {
				continue
			}
			seen[filepath.ToSlash(rel)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/discover/ -v`
Expected: PASS — all three tests

- [ ] **Step 6: Write the failing file-check test**

Create `internal/catalog/files_test.go`:

```go
package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestCheckFilesAcceptsExistingPaths(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Runbook = "services/payments-worker/docs/runbook.md"
	e.Spec.Docs = "services/payments-worker/docs"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles("../../testdata/monorepo-ok", cat, &c)

	if c.HasErrors() {
		t.Errorf("existing paths must pass: %+v", c.Diagnostics())
	}
}

func TestCheckFilesReportsMissingRunbook(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Runbook = "services/payments-worker/docs/nope.md"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles("../../testdata/monorepo-ok", cat, &c)

	if !c.HasErrors() {
		t.Fatal("a runbook path that does not exist must be an error")
	}
	if got := c.Diagnostics()[0].Check; got != "missing-file" {
		t.Errorf("Check = %q, want %q", got, "missing-file")
	}
}

func TestCheckFilesIgnoresEmptyPaths(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/ledger-api/service.yaml", "ledger-api", KindService, 4)
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles("../../testdata/monorepo-ok", cat, &c)

	if c.HasErrors() {
		t.Errorf("an unset optional path is not a missing file: %+v", c.Diagnostics())
	}
}
```

- [ ] **Step 7: Run it to verify it fails**

Run: `go test ./internal/catalog/ -run TestCheckFiles -v`
Expected: FAIL — `undefined: CheckFiles`

- [ ] **Step 8: Write `files.go`**

Create `internal/catalog/files.go`:

```go
package catalog

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// CheckFiles verifies that every path an entity points at actually exists.
// Empty paths are skipped: these fields are optional, and "not set" is a
// scorecard question, not a validation error.
func CheckFiles(root string, cat *Catalog, c *diag.Collector) {
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
			full := filepath.Join(root, filepath.FromSlash(f.path))
			info, err := os.Stat(full)
			if err != nil {
				c.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "missing-file",
					Message:  fmt.Sprintf("%s points at %q, which does not exist", f.field, f.path),
					Hint:     "paths are relative to the repository root",
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

- [ ] **Step 9: Run everything to verify it passes**

Run: `task test`
Expected: PASS — every package

- [ ] **Step 10: Commit**

```bash
git add internal/discover/ internal/catalog/files.go internal/catalog/files_test.go testdata/
git commit -m "feat: service.yaml discovery and referenced-file checks"
```

---

## Task 10: The `validate` command

Wires the pipeline together behind a CLI, with the output formats and exit codes from the spec.

**Files:**
- Create: `internal/diag/format.go`, `internal/config/repos.go`, `cmd/landsraad/validate.go`
- Modify: `cmd/landsraad/main.go`
- Test: `internal/diag/format_test.go`, `cmd/landsraad/validate_test.go`
- Create: `testdata/monorepo-broken/` fixtures

**Interfaces:**
- Consumes: everything above.
- Produces:
  - `func diag.WriteText(w io.Writer, ds []Diagnostic) error`
  - `func diag.WriteJSON(w io.Writer, ds []Diagnostic) error`
  - `func diag.WriteGitHub(w io.Writer, ds []Diagnostic) error`
  - `func config.LoadRepos(path string, data []byte, c *diag.Collector) *Repos` with `func (*Repos) LocalPatterns() []string`
  - `func runValidate(root string, out io.Writer, format string) int` — returns the exit code

- [ ] **Step 1: Add cobra**

```bash
go get github.com/spf13/cobra@v1.8.1
```

- [ ] **Step 2: Write the failing format test**

Create `internal/diag/format_test.go`:

```go
package diag

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

var sample = []Diagnostic{
	{Severity: SevError, File: "services/a/service.yaml", Line: 4, Entity: "a",
		Check: "unknown-owner", Message: `owner "team-payment" is not defined in teams.yaml`,
		Hint: `did you mean "team-payments"?`},
	{Severity: SevWarn, File: "services/b/service.yaml", Line: 9, Entity: "b",
		Check: "missing-file", Message: `spec.runbook points at "nope.md", which does not exist`},
}

func TestWriteTextIncludesFileLineAndHint(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, sample); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"services/a/service.yaml:4",
		"error",
		"unknown-owner",
		`did you mean "team-payments"?`,
		"services/b/service.yaml:9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteJSONRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sample); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var back []Diagnostic
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(back) != 2 || back[0].Check != "unknown-owner" {
		t.Errorf("round trip lost data: %+v", back)
	}
}

func TestWriteGitHubUsesAnnotationSyntax(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteGitHub(&buf, sample); err != nil {
		t.Fatalf("WriteGitHub: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "::error file=services/a/service.yaml,line=4::") {
		t.Errorf("missing GitHub error annotation:\n%s", out)
	}
	if !strings.Contains(out, "::warning file=services/b/service.yaml,line=9::") {
		t.Errorf("missing GitHub warning annotation:\n%s", out)
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/diag/ -run TestWrite -v`
Expected: FAIL — `undefined: WriteText`

- [ ] **Step 4: Write `format.go`**

Create `internal/diag/format.go`:

```go
package diag

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// WriteText renders diagnostics for a human terminal, one block each.
func WriteText(w io.Writer, ds []Diagnostic) error {
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

// WriteJSON renders diagnostics as a JSON array for machine consumption.
func WriteJSON(w io.Writer, ds []Diagnostic) error {
	if ds == nil {
		ds = []Diagnostic{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ds)
}

// WriteGitHub renders GitHub Actions workflow commands so problems appear as
// inline annotations on the pull request.
func WriteGitHub(w io.Writer, ds []Diagnostic) error {
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
		// Newlines and commas would break the annotation syntax.
		msg = strings.ReplaceAll(msg, "\n", " ")
		if _, err := fmt.Fprintf(w, "::%s file=%s,line=%d::%s\n", level, d.File, d.Line, msg); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/diag/ -v`
Expected: PASS

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

// LocalPatterns returns the glob patterns for the first repo entry, which is
// by convention the repository the command is running in. `landsraad validate`
// is hermetic and never fetches the others; that is the platform build's job.
func (r *Repos) LocalPatterns() []string {
	if len(r.Repos) == 0 {
		return []string{"services/*", "workers/*", "libs/*"}
	}
	return r.Repos[0].Paths
}
```

- [ ] **Step 7: Create the broken fixture**

```bash
mkdir -p testdata/monorepo-broken/services/api
mkdir -p testdata/monorepo-broken/services/dup
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
	"strings"
	"testing"
)

func TestRunValidateOnGoodRepoExitsZero(t *testing.T) {
	var out bytes.Buffer
	code := runValidate("../../testdata/monorepo-ok", &out, "text")
	if code != exitOK {
		t.Errorf("exit code = %d, want %d\noutput:\n%s", code, exitOK, out.String())
	}
}

func TestRunValidateOnBrokenRepoExitsTwo(t *testing.T) {
	var out bytes.Buffer
	code := runValidate("../../testdata/monorepo-broken", &out, "text")
	if code != exitValidation {
		t.Errorf("exit code = %d, want %d", code, exitValidation)
	}
}

// The point of the collector: one run reports every problem, not the first.
func TestRunValidateReportsAllProblemsAtOnce(t *testing.T) {
	var out bytes.Buffer
	runValidate("../../testdata/monorepo-broken", &out, "text")
	got := out.String()

	for _, want := range []string{
		"unknown-owner",     // owner: team-payment
		"duplicate-name",    // two entities named api
		"missing-file",      // runbook does not exist
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a single run must report %q; output was:\n%s", want, got)
		}
	}
}

// validate is hermetic: a reference to another repo is not an error here.
func TestRunValidateToleratesCrossRepoRefs(t *testing.T) {
	var out bytes.Buffer
	runValidate("../../testdata/monorepo-ok", &out, "text")
	if strings.Contains(out.String(), "dangling-ref") {
		t.Errorf("validate runs on one repo and must not report cross-repo refs:\n%s", out.String())
	}
}

func TestRunValidateMissingRootExitsUsage(t *testing.T) {
	var out bytes.Buffer
	if code := runValidate("../../testdata/does-not-exist", &out, "text"); code != exitUsage {
		t.Errorf("exit code = %d, want %d for a missing root", code, exitUsage)
	}
}
```

- [ ] **Step 9: Run it to verify it fails**

Run: `go test ./cmd/landsraad/ -v`
Expected: FAIL — `undefined: runValidate`

- [ ] **Step 10: Write `validate.go`**

Create `cmd/landsraad/validate.go`:

```go
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/schema"
)

func newValidateCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "validate [root]",
		Short: "Validate this repository's catalog files",
		Long: "Validate runs offline against a single repository. It needs no network " +
			"access and no tokens, and it does not resolve references to entities in " +
			"other repositories — the platform build does that.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			code := runValidate(root, cmd.OutOrStdout(), format)
			if code != exitOK {
				// Suppress cobra's usage dump; the diagnostics are the message.
				cmd.SilenceUsage = true
				cmd.SilenceErrors = true
				os.Exit(code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "auto", "output format: auto, text, json, github")
	return cmd
}

// runValidate executes the hermetic pipeline and returns the process exit code.
func runValidate(root string, out io.Writer, format string) int {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		fmt.Fprintf(out, "error: %s is not a directory\n", root)
		return exitUsage
	}

	var c diag.Collector

	// repos.yaml is optional; without it the conventional paths are used.
	patterns := (&config.Repos{}).LocalPatterns()
	if data, err := os.ReadFile(filepath.Join(root, "repos.yaml")); err == nil {
		patterns = config.LoadRepos("repos.yaml", data, &c).LocalPatterns()
	}

	paths, err := discover.Find(root, patterns)
	if err != nil {
		fmt.Fprintf(out, "error: cannot search for %s files: %v\n", discover.Filename, err)
		return exitUsage
	}

	var entities []*catalog.Entity
	for _, rel := range paths {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: rel, Line: 1,
				Check: "unreadable", Message: fmt.Sprintf("cannot read file: %v", err),
			})
			continue
		}
		// Schema first: it produces the precise structural messages.
		schema.Validate("", rel, data, &c)
		if e, ok := catalog.ParseFile("", rel, data, &c); ok {
			entities = append(entities, e)
		}
	}

	cat := catalog.NewCatalog(entities, &c)
	// local=true: this repo cannot see entities defined in other repos.
	cat.Resolve(true, &c)

	for _, cyc := range cat.Cycles() {
		e, _ := cat.Lookup(cyc[0])
		d := diag.Diagnostic{
			Severity: diag.SevError, Line: 1,
			Check: "dependency-cycle", Message: fmt.Sprintf("dependency cycle: %s", joinRefs(cyc)),
			Hint: "break the loop, or model one direction as a shared library",
		}
		if e != nil {
			d.File, d.Line, d.Entity = e.SourcePath, e.NameLine, e.Metadata.Name
		}
		c.Add(d)
	}

	if data, err := os.ReadFile(filepath.Join(root, "teams.yaml")); err == nil {
		config.LoadTeams("teams.yaml", data, &c).ValidateOwners(cat, &c)
	} else {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root",
			Hint:    "every entity's owner must resolve to a team defined there",
		})
	}

	catalog.CheckFiles(root, cat, &c)

	ds := c.Diagnostics()
	if err := write(out, ds, format); err != nil {
		fmt.Fprintf(out, "error: cannot write output: %v\n", err)
		return exitUsage
	}

	if c.HasErrors() {
		return exitValidation
	}
	fmt.Fprintf(out, "ok: %d entities validated, no problems found\n", len(cat.Entities))
	return exitOK
}

func write(out io.Writer, ds []diag.Diagnostic, format string) error {
	if format == "auto" {
		if os.Getenv("GITHUB_ACTIONS") == "true" {
			format = "github"
		} else {
			format = "text"
		}
	}
	switch format {
	case "json":
		return diag.WriteJSON(out, ds)
	case "github":
		return diag.WriteGitHub(out, ds)
	default:
		return diag.WriteText(out, ds)
	}
}

func joinRefs(rs []catalog.Ref) string {
	out := ""
	for i, r := range rs {
		if i > 0 {
			out += " -> "
		}
		out += r.String()
	}
	if len(rs) > 0 {
		out += " -> " + rs[0].String()
	}
	return out
}
```

- [ ] **Step 11: Rewrite `main.go` to use cobra**

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
		Use:           "landsraad",
		Short:         "A lightweight developer portal for small teams",
		SilenceUsage:  false,
		SilenceErrors: false,
	}
	root.AddCommand(newValidateCmd())
	root.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for service.yaml",
		Long: "Print the embedded JSON Schema. Redirect it to schema/service.schema.json " +
			"and point yaml-language-server at that file for editor autocompletion.",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.OutOrStdout().Write(schema.Raw)
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

- [ ] **Step 12: Run everything to verify it passes**

Run: `task ci`
Expected: PASS — lint clean, every test green including the five command tests

- [ ] **Step 13: Verify by hand**

```bash
task build
./bin/landsraad validate testdata/monorepo-ok
./bin/landsraad validate testdata/monorepo-broken; echo "exit=$?"
```

Expected: the first prints `ok: 3 entities validated, no problems found`. The second lists the unknown owner with a `did you mean "team-payments"?` hint, the duplicate name naming both files, and the missing runbook — then `exit=2`.

- [ ] **Step 14: Commit**

```bash
git add cmd/landsraad/ internal/diag/format.go internal/diag/format_test.go internal/config/repos.go testdata/
git commit -m "feat: landsraad validate command with text, json and CI output"
```

---

## Definition of done

- [ ] `task ci` passes from a clean checkout
- [ ] `landsraad validate testdata/monorepo-ok` exits 0
- [ ] `landsraad validate testdata/monorepo-broken` exits 2 and reports **all three** seeded problems in one run
- [ ] Every diagnostic in that output carries a file and a non-zero line
- [ ] `task schema` leaves `schema/service.schema.json` byte-identical to the committed copy
- [ ] No test reaches the network
- [ ] `go.mod` requires exactly: `yaml.v3`, `jsonschema/v6`, `cobra`

## What this plan deliberately leaves out

Handled by later plans, not gaps:

- **Scorecard, `standards.yaml`, `.landsraad/checks/`** — Plan 2
- **CODEOWNERS, alert routing, Slack map, `gen --check`** — Plan 2
- **Fetching remote repos, GitHub and GitLab adapters** — Plan 3
- **Rendering, goldmark, admonitions, search, `build`, `serve`** — Plan 3
- **Exit code 3** — arrives with the scorecard gate in Plan 2
