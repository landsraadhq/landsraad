package render

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func decodeIndex(t *testing.T, data []byte) []SearchEntry {
	t.Helper()
	var out []SearchEntry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("search-index.json is not valid JSON: %v\n%s", err, data)
	}
	return out
}

func TestSearchIndexCoversEveryEntity(t *testing.T) {
	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(twoEntities(t), &c))["search-index.json"])
	byURL := map[string]SearchEntry{}
	for _, e := range entries {
		byURL[e.URL] = e
	}
	e, ok := byURL["entity/service/ledger-api/"]
	if !ok {
		t.Fatalf("the service is not in the index: %+v", entries)
	}
	if e.Title != "ledger-api" || e.Kind != "Service" || e.Owner != "team-payments" {
		t.Errorf("entry = %+v", e)
	}
}

// Ruling R18 hoists docs/index.md onto the entity page, so the document and
// the entity are ONE page. Recording that document at entityDir+"index.html"
// spelled the same page a second way — CatalogRow.URL has no "index.html" —
// and searchIndex could not tell they were the same, so every documented
// entity got two rows: one labelled "Service" carrying no body text, one
// carrying the body and labelled nothing. A search for a documented service
// returned it twice.
func TestADocumentedEntityHasExactlyOneSearchEntryCarryingItsDocumentation(t *testing.T) {
	e := ent("ledger-api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/ledger-api/docs"
	files := fstest.MapFS{
		"services/ledger-api/docs/index.md": {Data: []byte(
			"# ledger-api\n\nSettles the daily ledger against the clearing file.\n")},
	}

	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(input(t, files, e), &c))["search-index.json"])

	var found []SearchEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.URL, "entity/service/ledger-api") {
			found = append(found, entry)
		}
	}
	if len(found) != 1 {
		t.Fatalf("a documented entity must have exactly one search entry, got %d: %+v", len(found), found)
	}
	got := found[0]
	if got.URL != "entity/service/ledger-api/" {
		t.Errorf("URL = %q, want the entity's own URL %q", got.URL, "entity/service/ledger-api/")
	}
	// The surviving row must be the labelled one: a reader recognises
	// "ledger-api, Service", not the H1 of its index.md.
	if got.Kind != "Service" {
		t.Errorf("Kind = %q, want %q", got.Kind, "Service")
	}
	if got.Title != "ledger-api" {
		t.Errorf("Title = %q, want %q", got.Title, "ledger-api")
	}
	// Folding rather than dropping is what makes the entity findable by the
	// words in its own documentation, which it was not before: the entity row
	// carried no text at all.
	wantText := "ledger-api Settles the daily ledger against the clearing file."
	if got.Text != wantText {
		t.Errorf("Text = %q, want %q", got.Text, wantText)
	}
	if len(got.Headings) != 1 || got.Headings[0] != "ledger-api" {
		t.Errorf("Headings = %+v, want [ledger-api]", got.Headings)
	}
}

func TestSearchIndexCoversRenderedDocuments(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{
		"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\n## Rollback\n\nDrain the queue first.\n")},
	}
	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(input(t, files, e), &c))["search-index.json"])

	var doc *SearchEntry
	for i := range entries {
		if entries[i].URL == "entity/service/api/runbook.html" {
			doc = &entries[i]
		}
	}
	if doc == nil {
		t.Fatalf("the runbook is not in the index: %+v", entries)
	}
	if doc.Title != "Runbook" {
		t.Errorf("Title = %q, want %q", doc.Title, "Runbook")
	}
	if len(doc.Headings) == 0 || doc.Headings[0] != "Runbook" {
		t.Errorf("Headings = %v, want them collected", doc.Headings)
	}
	if !strings.Contains(doc.Text, "Drain the queue") {
		t.Errorf("body text is missing: %q", doc.Text)
	}
}

// The index is a JSON document a client parses, so it must be stable
// between identical runs or every build produces a spurious diff.
func TestSearchIndexIsSorted(t *testing.T) {
	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(twoEntities(t), &c))["search-index.json"])
	for i := 1; i < len(entries); i++ {
		if entries[i-1].URL > entries[i].URL {
			t.Errorf("entries are not sorted by URL: %q before %q", entries[i-1].URL, entries[i].URL)
		}
	}
}

// Markdown was escaped on its way into HTML because goldmark ran without
// WithUnsafe. It is NOT escaped here — this is JSON — which is why the
// client must use textContent.
func TestSearchIndexIsValidJSONForAwkwardText(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{
		"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\nQuotes \" and <angles> and \\backslashes\\ and émojis 🜃.\n")},
	}
	var c diag.Collector
	entries := decodeIndex(t, siteMap(Site(input(t, files, e), &c))["search-index.json"])
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
}

func TestSearchIndexIsGolden(t *testing.T) {
	var c diag.Collector
	golden(t, "search-index.json", siteMap(Site(twoEntities(t), &c))["search-index.json"])
}

// Task 9's nonNilHeadings exists so a document with no headings marshals to
// `[]`, never `null`: a browser client doing `entry.headings.length` on
// `null` throws. This asserts the raw bytes directly, not the decoded Go
// value, because json.Unmarshal cannot tell `[]`, `null` and an absent key
// apart — all three decode to a nil []string, so a round-trip check would
// pass even if the encoder emitted the wrong one.
func TestSearchIndexEmptyHeadingsMarshalToEmptyArray(t *testing.T) {
	var c diag.Collector
	data := siteMap(Site(twoEntities(t), &c))["search-index.json"]
	if strings.Contains(string(data), `"headings":null`) {
		t.Fatalf("an empty Headings slice marshalled to null, not []: %s", data)
	}
	if !strings.Contains(string(data), `"headings":[]`) {
		t.Errorf("expected at least one entry with no headings to carry an explicit []: %s", data)
	}
}
