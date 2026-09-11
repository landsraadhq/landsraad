package scorecard

import (
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/fetch"
)

var now = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func catalogOf(t *testing.T, entities ...*catalog.Entity) *catalog.Catalog {
	t.Helper()
	var c diag.Collector
	cat := catalog.NewCatalog(entities, &c)
	if c.HasErrors() {
		t.Fatalf("fixture catalog must build cleanly: %+v", c.Diagnostics())
	}
	return cat
}

func resultsFile(generatedAt, body string) []byte {
	return []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/test\ngeneratedAt: " +
		generatedAt + "\nresults:\n" + body)
}

func TestIngestReadsAResultKeyedByRef(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: service:api, check: image-scanned, status: pass, detail: \"0 critical\", url: \"https://ci/1\" }\n")},
	}
	var c diag.Collector

	got := Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	if c.HasErrors() {
		t.Fatalf("a well-formed results file must ingest cleanly: %+v", c.Diagnostics())
	}
	ref := catalog.Ref{Kind: catalog.KindService, Name: "api"}
	r, ok := got[ref]["image-scanned"]
	if !ok {
		t.Fatalf("no result for %s/image-scanned; got %+v", ref, got)
	}
	if r.Result.Status != StatusPass {
		t.Errorf("Status = %q, want pass", r.Result.Status)
	}
	if r.Result.Detail != "0 critical" {
		t.Errorf("Detail = %q", r.Result.Detail)
	}
	if r.Result.URL != "https://ci/1" {
		t.Errorf("URL = %q", r.Result.URL)
	}
	if r.Producer != "ci/test" {
		t.Errorf("Producer = %q", r.Producer)
	}
}

// Spec §6: a result older than staleAfterDays renders as stale, not pass. An
// image scan from March is not evidence about today.
func TestIngestMarksAnOldResultStale(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-08-01T00:00:00Z",
			"  - { entity: service:api, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	r := got[catalog.Ref{Kind: catalog.KindService, Name: "api"}]["image-scanned"]
	if r.Result.Status != StatusStale {
		t.Errorf("Status = %q, want stale — 39 days old against a 14 day limit", r.Result.Status)
	}
	if r.Result.Detail != "reported pass 39 days ago by ci/test, older than the 14-day limit" {
		t.Errorf("Detail = %q", r.Result.Detail)
	}
}

// Spec §6 precedence: newest generatedAt wins.
func TestIngestNewestResultWins(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/old.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/old\ngeneratedAt: 2026-09-07T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: fail }\n")},
		".landsraad/checks/new.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/new\ngeneratedAt: 2026-09-08T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	r := got[catalog.Ref{Kind: catalog.KindService, Name: "api"}]["image-scanned"]
	if r.Result.Status != StatusPass {
		t.Errorf("Status = %q, want pass — the newer file wins", r.Result.Status)
	}
	if r.Producer != "ci/new" {
		t.Errorf("Producer = %q, want ci/new", r.Producer)
	}
}

// Spec §6: "a tie is an error rather than a coin flip".
func TestIngestReportsATieBetweenProducers(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/a.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/a\ngeneratedAt: 2026-09-08T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")},
		".landsraad/checks/b.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/b\ngeneratedAt: 2026-09-08T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: fail }\n")},
	}
	var c diag.Collector

	Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	if !c.HasErrors() {
		t.Fatal("two producers reporting the same (entity, check) at the same instant must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "checks-tie" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-tie")
	}
	want := `producers "ci/a" and "ci/b" both report image-scanned for service:api at 2026-09-08T00:00:00Z, so neither can win`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "give the producers different generatedAt values, or have only one report this check"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// Spec §6: a bare name is a deprecated alias, resolved only when unambiguous.
func TestIngestResolvesAnUnambiguousBareName(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: api, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	if _, ok := got[catalog.Ref{Kind: catalog.KindService, Name: "api"}]["image-scanned"]; !ok {
		t.Fatal("an unambiguous bare name must resolve")
	}
	// Deprecated, so it warns — but it does not fail, because existing CI jobs
	// in other people's repositories are already writing this shape.
	if len(c.Diagnostics()) != 1 {
		t.Fatalf("expected exactly one deprecation warning, got %+v", c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if d.Severity != diag.SevWarn {
		t.Errorf("Severity = %v, want SevWarn", d.Severity)
	}
	if d.Check != "checks-bare-name" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-bare-name")
	}
	want := `entity "api" is a bare name; write it as the ref "service:api"`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// The sharp edge of the deprecated alias: "orders" is unambiguous until
// someone adds topic:orders, at which point a working CI job starts erroring.
// That is correct — picking one silently would route evidence to the wrong
// entity — and the message has to say exactly what to change.
func TestIngestRefusesAnAmbiguousBareName(t *testing.T) {
	cat := catalogOf(t, svc("orders"), topic("orders"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: orders, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	if len(got) != 0 {
		t.Errorf("an ambiguous name must resolve to nothing, got %+v", got)
	}
	if !c.HasErrors() {
		t.Fatal("an ambiguous bare name must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "checks-ambiguous-name" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-ambiguous-name")
	}
	want := `entity "orders" is ambiguous: it could be service:orders or topic:orders`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "write the full ref, for example service:orders"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

func TestIngestReportsAResultForAnEntityThatDoesNotExist(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: service:ghost, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	if !c.HasErrors() {
		t.Fatal("a result for an entity not in the catalog must be reported")
	}
	d := c.Diagnostics()[0]
	if d.Check != "checks-unknown-entity" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-unknown-entity")
	}
	want := `result reported for service:ghost, which is not in the catalog`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// A malformed results file must be loud. Skipping it silently means the
// scorecard reports "not reported" for checks that were, in fact, reported —
// and the CI job that wrote the file goes on believing it works.
func TestIngestReportsAMalformedFile(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: CheckResults\ngeneratedAt: not-a-date\nresults: []\n")},
	}
	var c diag.Collector

	Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	if !c.HasErrors() {
		t.Fatal("a results file that does not validate must be an error")
	}
	if c.Diagnostics()[0].File != ".landsraad/checks/scan.yaml" {
		t.Errorf("File = %q, want the results file", c.Diagnostics()[0].File)
	}
}

// A filename 5 characters or shorter with a .yml extension (e.g. "a.yml")
// must still be ingested. validate.go and Ingest used to disagree on this via
// two independently hand-rolled predicates; IsCheckResultsFile is now the one
// place both check.
func TestIngestReadsAShortDotYmlFilename(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	fsys := fstest.MapFS{
		".landsraad/checks/a.yml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: service:api, check: image-scanned, status: pass }\n")},
	}
	var c diag.Collector

	got := Ingest(catalog.SingleSource("", fsys), cat, 14, now, &c)

	if c.HasErrors() {
		t.Fatalf("a well-formed short .yml results file must ingest cleanly: %+v", c.Diagnostics())
	}
	ref := catalog.Ref{Kind: catalog.KindService, Name: "api"}
	if _, ok := got[ref]["image-scanned"]; !ok {
		t.Fatalf("a.yml must be ingested; got %+v", got)
	}
}

// No directory at all is not an error — a repository that reports no external
// results is a normal repository. Every external check renders not-reported,
// which is the honest answer and is visible in the scorecard.
func TestIngestWithNoChecksDirectoryIsNotAnError(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	var c diag.Collector

	got := Ingest(catalog.SingleSource("", fstest.MapFS{}), cat, 14, now, &c)

	if c.HasErrors() {
		t.Errorf("an absent .landsraad/checks is not an error: %+v", c.Diagnostics())
	}
	if len(got) != 0 {
		t.Errorf("nothing to ingest, got %+v", got)
	}
}

// failPathFS is a MapFS on which one path cannot be read. ReadDir and
// ReadFile of it fail with a permission error, the shape os.DirFS gives for
// a path the build user cannot read, without depending on the test
// process's own permissions. Compare render's failFS.
type failPathFS struct {
	fstest.MapFS
	path string
}

func (f failPathFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadDir(name)
}

func (f failPathFS) ReadFile(name string) ([]byte, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadFile(name)
}

// A .landsraad/checks that exists but cannot be listed is a different fact
// from no directory at all. ingestRepo used to treat both as "no results",
// so every result the repository reported vanished from the scorecard
// without a word.
func TestIngestReportsAnUnreadableChecksDirectory(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	var c diag.Collector

	Ingest(catalog.SingleSource("platform", failPathFS{MapFS: fstest.MapFS{}, path: ChecksDir}), cat, 14, now, &c)

	want := []diag.Diagnostic{{
		Severity: diag.SevError, Repo: "platform", File: ".landsraad/checks", Line: 1,
		Check:   "checks-unreadable",
		Message: "cannot read .landsraad/checks: readdir .landsraad/checks: permission denied",
	}}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// ErrNotListed here means cmd/'s planner never listed a directory it always
// expands (docsDirs seeds ChecksDir), so it is a landsraad bug. It has to be
// loud: once every listed flag is earned (R45), a planner mistake would
// otherwise surface as exactly the silence above.
func TestIngestReportsAnUnlistedChecksDirectoryAsALandsraadBug(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	remote := fetch.NewFS()
	// The root listing saw .landsraad, and nothing ever listed inside it.
	remote.AddDir(".", []fetch.Entry{{Path: ".landsraad", Dir: true}})
	var c diag.Collector

	Ingest(catalog.SingleSource("edge-gateway", remote), cat, 14, now, &c)

	want := []diag.Diagnostic{{
		Severity: diag.SevError, Repo: "edge-gateway", File: ".landsraad/checks", Line: 1,
		Check: "checks-unreadable",
		Message: ".landsraad/checks was never listed, so landsraad cannot read it; " +
			"this is a landsraad bug, not a problem with your catalog",
	}}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// A results file that was listed but whose bytes were never fetched. The
// ReadFile branch used to say "cannot read .landsraad/checks/scan.yaml" and
// stop there, which sends somebody to look at a file that is fine.
func TestIngestReportsAnUnfetchedResultsFileAsALandsraadBug(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	remote := fetch.NewFS()
	remote.AddDir(".", []fetch.Entry{{Path: ".landsraad", Dir: true}})
	remote.AddDir(".landsraad", []fetch.Entry{{Path: ".landsraad/checks", Dir: true}})
	remote.AddDir(".landsraad/checks", []fetch.Entry{
		{Path: ".landsraad/checks/scan.yaml", SHA: "0123456789abcdef0123456789abcdef01234567", Size: 40},
	})
	var c diag.Collector

	Ingest(catalog.SingleSource("edge-gateway", remote), cat, 14, now, &c)

	want := []diag.Diagnostic{{
		Severity: diag.SevError, Repo: "edge-gateway", File: ".landsraad/checks/scan.yaml", Line: 1,
		Check: "checks-unreadable",
		Message: ".landsraad/checks/scan.yaml is in the repository but its content was never fetched; " +
			"this is a landsraad bug, not a problem with your catalog",
	}}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

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

// Under R31 a ChecksDir filename is no longer unique across the catalog:
// two repositories can each have .landsraad/checks/scan.yaml. A
// resolveEntity diagnostic's File alone cannot say which repository's file
// named the unknown entity — only Repo can.
func TestIngestUnknownEntityDiagnosticNamesItsRepository(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	alpha := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: service:api, check: image-scanned, status: pass }\n")},
	}
	beta := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: service:ghost, check: image-scanned, status: pass }\n")},
	}
	src := catalog.Sources{"alpha": alpha, "beta": beta}
	var c diag.Collector

	Ingest(src, cat, 14, now, &c)

	if !c.HasErrors() {
		t.Fatal("a result for an entity not in the catalog must be reported")
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	d := ds[0]
	if d.Check != "checks-unknown-entity" {
		t.Errorf("Check = %q, want %q", d.Check, "checks-unknown-entity")
	}
	if d.File != ".landsraad/checks/scan.yaml" {
		t.Errorf("File = %q, want %q — both repositories have a file at this path", d.File, ".landsraad/checks/scan.yaml")
	}
	if d.Repo != "beta" {
		t.Errorf("Repo = %q, want %q — File alone cannot tell alpha's scan.yaml from beta's", d.Repo, "beta")
	}
	want := `result reported for service:ghost, which is not in the catalog`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "the entity may have been renamed; metadata.aliases makes a rename additive"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// Same gap, one step earlier in the pipeline: schema.Validator.Validate also
// takes a repo, and a CheckResults file that fails schema validation before
// it is even parsed must still say which repository it came from — File
// alone is ambiguous under R31 the same way it is for resolveEntity.
func TestIngestSchemaDiagnosticNamesItsRepository(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	alpha := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: resultsFile("2026-09-08T14:00:00Z",
			"  - { entity: service:api, check: image-scanned, status: pass }\n")},
	}
	beta := fstest.MapFS{
		".landsraad/checks/scan.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/test\ngeneratedAt: not-a-date\n" +
				"results:\n  - { entity: service:api, check: image-scanned, status: pass }\n")},
	}
	src := catalog.Sources{"alpha": alpha, "beta": beta}
	var c diag.Collector

	Ingest(src, cat, 14, now, &c)

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	d := ds[0]
	if d.Check != "schema" {
		t.Errorf("Check = %q, want %q", d.Check, "schema")
	}
	if d.File != ".landsraad/checks/scan.yaml" {
		t.Errorf("File = %q, want %q — both repositories have a file at this path", d.File, ".landsraad/checks/scan.yaml")
	}
	if d.Repo != "beta" {
		t.Errorf("Repo = %q, want %q — File alone cannot tell alpha's scan.yaml from beta's", d.Repo, "beta")
	}
	want := "at '/generatedAt': 'not-a-date' is not a valid date-time"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

func topic(name string) *catalog.Entity {
	e := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindTopic}
	e.Metadata.Name = name
	e.Metadata.Owner = "team-payments"
	e.SourcePath = "topics/" + name + "/service.yaml"
	e.NameLine = 4
	return e
}
