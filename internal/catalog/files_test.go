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
		"services/payments-worker/alerts.yaml":     {Data: []byte("groups: []\n")},
	}
}

func TestCheckFilesAcceptsExistingPaths(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Path = "services/payments-worker"
	e.Spec.Runbook = "services/payments-worker/docs/runbook.md"
	e.Spec.Docs = "services/payments-worker/docs"
	e.Spec.Alerts = "services/payments-worker/alerts.yaml"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

	if c.HasErrors() {
		t.Errorf("existing paths must pass: %+v", c.Diagnostics())
	}
}

func TestCheckFilesReportsMissingRunbook(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Runbook = "services/payments-worker/docs/nope.md"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

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
	wantHint := "paths are relative to the repository root, slash-separated"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

func TestCheckFilesReportsMissingAlerts(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Alerts = "services/payments-worker/alerts-nope.yaml"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

	if !c.HasErrors() {
		t.Fatal("an alerts path that does not exist must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "missing-file" {
		t.Errorf("Check = %q, want %q", d.Check, "missing-file")
	}
	if d.Line != 4 {
		t.Errorf("the diagnostic must point at the entity's line, got %d", d.Line)
	}
	want := `spec.alerts points at "services/payments-worker/alerts-nope.yaml", which does not exist`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "paths are relative to the repository root, slash-separated"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

func TestCheckFilesRejectsFileWhereDirectoryExpected(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Docs = "services/payments-worker/docs/index.md" // a file, not a dir
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

	if !c.HasErrors() {
		t.Fatal("spec.docs must be a directory")
	}
	d := c.Diagnostics()[0]
	if d.Check != "missing-file" {
		t.Errorf("Check = %q, want %q", d.Check, "missing-file")
	}
	want := `spec.docs points at "services/payments-worker/docs/index.md", which is a file but must be a directory`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if d.Hint != "" {
		t.Errorf("Hint = %q, want empty", d.Hint)
	}
}

func TestCheckFilesIgnoresEmptyPaths(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/ledger-api/service.yaml", "ledger-api", KindService, 4)
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

	if c.HasErrors() {
		t.Errorf("an unset optional path is not a missing file: %+v", c.Diagnostics())
	}
}

// spec.path is the anchor: it says which directory this entity *is*, and it is
// what CODEOWNERS generation joins against. A typo there does not break a link
// the way a bad spec.runbook does — it produces a CODEOWNERS line for a
// directory that does not exist, which git silently ignores, leaving the real
// directory unowned. Checked for every kind that sets it; an entity whose code
// is not in this repository omits the field.
func TestCheckFilesReportsMissingPath(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Path = "services/payments-workr" // the typo this check exists to catch
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

	if !c.HasErrors() {
		t.Fatal("a spec.path that does not exist must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "missing-file" {
		t.Errorf("Check = %q, want %q", d.Check, "missing-file")
	}
	if d.Line != 4 {
		t.Errorf("the diagnostic must point at the entity's line, got %d", d.Line)
	}
	want := `spec.path points at "services/payments-workr", which does not exist`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "paths are relative to the repository root, slash-separated"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// spec.path is checked for existence but deliberately not for directory-ness,
// unlike spec.docs. A Library may name a single file, and CODEOWNERS patterns
// match files as happily as directories. Requiring a directory stays available
// as a later tightening; loosening the rule once user repositories depend on
// it does not.
func TestCheckFilesAcceptsAPathThatNamesAFile(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "libs/kafkaclient/service.yaml", "kafkaclient", KindLibrary, 4)
	e.Spec.Path = "services/payments-worker/alerts.yaml"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

	if c.HasErrors() {
		t.Errorf("spec.path may name a file: %+v", c.Diagnostics())
	}
}

// Audit finding 4. fs.Stat rejects a path that is not a valid io/fs path
// before it ever looks at the filesystem, and CheckFiles reported that
// rejection as absence — telling the user a file is missing while they are
// looking straight at it. A shared runbook one directory up is an ordinary
// monorepo layout, and "/etc" demonstrably exists.
//
// The blocking is correct and is spec §14.1's security property. Only the
// diagnosis was wrong.
func TestCheckFilesNamesWhyAPathIsInvalid(t *testing.T) {
	for _, tc := range []struct {
		name  string
		set   func(*Entity)
		want  string
		check string
	}{
		{"absolute", func(e *Entity) { e.Spec.Runbook = "/etc/passwd" },
			`spec.runbook points at "/etc/passwd", which is an absolute path; write it relative to the repository root, for example "etc/passwd"`,
			"invalid-path"},
		{"escapes the repo", func(e *Entity) { e.Spec.Runbook = "../shared/runbook.md" },
			`spec.runbook points at "../shared/runbook.md", which escapes the repository root via ".."; a service.yaml can only point at files in its own repository`,
			"invalid-path"},
		{"trailing slash", func(e *Entity) { e.Spec.Docs = "services/payments-worker/docs/" },
			`spec.docs points at "services/payments-worker/docs/", which has a trailing slash; write "services/payments-worker/docs" instead`,
			"invalid-path"},
		{"redundant dot", func(e *Entity) { e.Spec.Docs = "./services/payments-worker/docs" },
			`spec.docs points at "./services/payments-worker/docs", which contains a redundant "." element; write it without that segment`,
			"invalid-path"},
		{"doubled slash", func(e *Entity) { e.Spec.Alerts = "services//alerts.yaml" },
			`spec.alerts points at "services//alerts.yaml", which contains an empty path element (a doubled "/"); remove it`,
			"invalid-path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c diag.Collector
			e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
			tc.set(e)
			cat := NewCatalog([]*Entity{e}, &c)

			CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

			if !c.HasErrors() {
				t.Fatal("an invalid path must be an error")
			}
			d := c.Diagnostics()[0]
			if d.Check != tc.check {
				t.Errorf("Check = %q, want %q", d.Check, tc.check)
			}
			if d.Line != 4 {
				t.Errorf("the diagnostic must point at the entity's line, got %d", d.Line)
			}
			if d.Message != tc.want {
				t.Errorf("Message\n got: %s\nwant: %s", d.Message, tc.want)
			}
		})
	}
}

// "." is a valid io/fs path: a single-service repository whose service.yaml
// sits at the root says `path: .`, and that must not be mistaken for a
// redundant element.
func TestCheckFilesAcceptsDotAsAWholePath(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "service.yaml", "root-service", KindService, 4)
	e.Spec.Path = "."
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(SingleSource("monorepo", repoFS()), cat, &c)

	if c.HasErrors() {
		t.Errorf("`path: .` is the single-service repo shape: %+v", c.Diagnostics())
	}
}

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
