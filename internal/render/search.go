package render

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// SearchIndexPath is where the client fetches the index from.
const SearchIndexPath = "search-index.json"

// SearchEntry is one searchable thing: an entity, or one of its documents.
//
// Field names are spelled out rather than shortened. The file is served
// once per visit and gzips well; a reader debugging why their runbook does
// not come up should not have to guess what "h" means.
type SearchEntry struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Kind and Owner are set for entity entries and empty for documents,
	// so the client can label a result as a service rather than a page.
	Kind        string `json:"kind,omitempty"`
	Owner       string `json:"owner,omitempty"`
	Description string `json:"description,omitempty"`
	// Headings deliberately has no omitempty. json's "omitempty" drops a
	// slice field whenever it is empty regardless of nil-ness, which would
	// undo RenderedDoc's nonNilHeadings guarantee at the last possible
	// moment: the field would vanish from the JSON instead of becoming
	// `null`, but a client reading `entry.headings.length` unguarded would
	// still crash on the resulting `undefined`. Every entry always carries
	// at least `[]`.
	Headings []string `json:"headings"`
	// Text is capped at 2000 runes by md.Render (ruling R17).
	Text string `json:"text,omitempty"`
}

// searchIndex builds the whole index.
//
// Entities and documents share one file so a single query finds both the
// service called "payments" and the runbook that explains how to drain it.
func searchIndex(rows []CatalogRow, docs []RenderedDoc) (emit.File, error) {
	entries := make([]SearchEntry, 0, len(rows)+len(docs))
	// at maps a URL to the entry already describing it. One URL is one page,
	// so it is one row: ruling R18 hoists docs/index.md onto the entity page,
	// which means that document and that entity ARE the same page, and the
	// index must not claim otherwise.
	at := make(map[string]int, len(rows)+len(docs))
	for _, r := range rows {
		at[r.URL] = len(entries)
		entries = append(entries, SearchEntry{
			URL: r.URL, Title: r.Name, Kind: r.Kind,
			Owner: r.Owner, Description: r.Description,
			// Entities have no headings, but the field must still be `[]`
			// rather than `null` — see the comment on Headings above.
			Headings: []string{},
		})
	}
	for _, d := range docs {
		headings := make([]string, 0, len(d.Headings))
		for _, h := range d.Headings {
			headings = append(headings, h.Text)
		}
		if i, ok := at[d.URL]; ok {
			// A document rendered at a URL an entry already covers folds into
			// it rather than adding a second row. Without this, a search for a
			// documented service returned it twice — once labelled "Service"
			// with no body text, once labelled nothing with the body — and the
			// entity itself was unfindable by the words in its own
			// documentation, because the entity row carried no text at all.
			// The entity's own Title, Kind and Owner win: the label a reader
			// recognises is "ledger-api, Service", not the H1 of its index.md.
			entries[i].Text = d.Text
			entries[i].Headings = append(entries[i].Headings, headings...)
			continue
		}
		at[d.URL] = len(entries)
		entries = append(entries, SearchEntry{
			URL: d.URL, Title: d.Title, Text: d.Text, Headings: headings,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].URL != entries[j].URL {
			return entries[i].URL < entries[j].URL
		}
		return entries[i].Title < entries[j].Title
	})

	data, err := json.Marshal(entries)
	if err != nil {
		return emit.File{}, err
	}
	return emit.File{Path: SearchIndexPath, Data: append(data, '\n')}, nil
}

// searchIndexFile wraps searchIndex for Site, reporting rather than
// returning the error.
func searchIndexFile(rows []CatalogRow, docs []RenderedDoc, c *diag.Collector) (emit.File, bool) {
	f, err := searchIndex(rows, docs)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: SearchIndexPath, Line: 1,
			Check:   "search-index",
			Message: fmt.Sprintf("cannot build the search index: %v", err),
		})
		return emit.File{}, false
	}
	return f, true
}
