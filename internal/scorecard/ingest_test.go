package scorecard

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
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

	got := Ingest(fsys, cat, 14, now, &c)

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

	got := Ingest(fsys, cat, 14, now, &c)

	r := got[catalog.Ref{Kind: catalog.KindService, Name: "api"}]["image-scanned"]
	if r.Result.Status != StatusStale {
		t.Errorf("Status = %q, want stale — 39 days old against a 14 day limit", r.Result.Status)
	}
	if r.Result.Detail != "reported pass 39 days ago by ci/test, older than the 14 day limit" {
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

	got := Ingest(fsys, cat, 14, now, &c)

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

	Ingest(fsys, cat, 14, now, &c)

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

	got := Ingest(fsys, cat, 14, now, &c)

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

	got := Ingest(fsys, cat, 14, now, &c)

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

	Ingest(fsys, cat, 14, now, &c)

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

	Ingest(fsys, cat, 14, now, &c)

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

	got := Ingest(fsys, cat, 14, now, &c)

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

	got := Ingest(fstest.MapFS{}, cat, 14, now, &c)

	if c.HasErrors() {
		t.Errorf("an absent .landsraad/checks is not an error: %+v", c.Diagnostics())
	}
	if len(got) != 0 {
		t.Errorf("nothing to ingest, got %+v", got)
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
