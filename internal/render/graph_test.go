package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// linked builds a catalog where service:api depends on topic:events.
func linked(t *testing.T) Input {
	t.Helper()
	api := ent("api", catalog.KindService, "team-payments", 1)
	api.Spec.DependsOn = []string{"topic:events"}
	return input(t, nil, api, ent("events", catalog.KindTopic, "team-payments", 0))
}

// A Mermaid node id may not contain ":", and every landsraad ref does.
func TestNeighbourhoodUsesGeneratedNodeIDs(t *testing.T) {
	in := linked(t)
	src := neighbourhood(in, catalog.Ref{Kind: catalog.KindService, Name: "api"})
	if !strings.HasPrefix(src, "graph LR\n") {
		t.Errorf("expected a left-to-right flowchart, got:\n%s", src)
	}
	if !strings.Contains(src, `n0["service:api"]`) {
		t.Errorf("the subject must be node n0 with its ref as the label:\n%s", src)
	}
	if !strings.Contains(src, "-->") {
		t.Errorf("the edge is missing:\n%s", src)
	}
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "["); i > 0 && strings.Contains(line[:i], ":") {
			t.Errorf("a raw ref leaked into a node id: %q", line)
		}
	}
}

func TestNeighbourhoodCoversBothDirections(t *testing.T) {
	in := linked(t)
	src := neighbourhood(in, catalog.Ref{Kind: catalog.KindTopic, Name: "events"})
	if !strings.Contains(src, `"service:api"`) {
		t.Errorf("the topic's page must show its consumer:\n%s", src)
	}
}

// An entity with no edges gets no diagram. An empty "graph LR" renders as a
// blank box, which reads as a broken page rather than as "no dependencies".
func TestAnIsolatedEntityHasNoDiagram(t *testing.T) {
	in := input(t, nil, ent("alone", catalog.KindService, "team-payments", 1))
	if src := neighbourhood(in, catalog.Ref{Kind: catalog.KindService, Name: "alone"}); src != "" {
		t.Errorf("expected no diagram, got:\n%s", src)
	}
}

func TestSystemGraphRendersEveryEdge(t *testing.T) {
	src, edges, capped := systemGraph(linked(t))
	if capped {
		t.Error("two entities must not trip the cap")
	}
	if len(edges) != 1 {
		t.Fatalf("got %d edges, want 1: %+v", len(edges), edges)
	}
	if edges[0].From.Ref != "service:api" || edges[0].To.Ref != "topic:events" {
		t.Errorf("edge = %+v", edges[0])
	}
	if !strings.Contains(src, "-->") {
		t.Errorf("no edge in the source:\n%s", src)
	}
}

// Ruling R19: above the cap the map is a table, and the page says why.
func TestSystemGraphIsCappedOnALargeCatalog(t *testing.T) {
	var entities []*catalog.Entity
	for i := 0; i <= SystemMapCap; i++ {
		entities = append(entities, ent(fmt.Sprintf("svc-%03d", i), catalog.KindService, "team-payments", 3))
	}
	src, _, capped := systemGraph(input(t, nil, entities...))
	if !capped {
		t.Errorf("%d entities must trip the cap of %d", len(entities), SystemMapCap)
	}
	if src != "" {
		t.Error("a capped map emits no Mermaid source")
	}
}

func TestMapPageExplainsTheCap(t *testing.T) {
	var entities []*catalog.Entity
	for i := 0; i <= SystemMapCap; i++ {
		entities = append(entities, ent(fmt.Sprintf("svc-%03d", i), catalog.KindService, "team-payments", 3))
	}
	var c diag.Collector
	page := string(siteMap(Site(input(t, nil, entities...), &c))["map/index.html"])
	want := fmt.Sprintf("This catalog has %d entities, above the %d-entity limit for a readable diagram.", len(entities), SystemMapCap)
	if !strings.Contains(page, want) {
		t.Errorf("the map page must say why it is a table:\n%s", page)
	}
}

// html/template escapes "+" to "&#43;" inside an attribute — Go's deliberate
// UTF-7 defence, not a bug. The HTML parser decodes it back before the
// browser computes the SRI check, so the hash still matches.
//
// Pinned here so nobody "fixes" it by declaring Integrity a
// template.HTMLAttr, which turns off escaping on an attribute that
// --mermaid-src lets a user populate.
func TestSRIHashIsEscapedAndStillCorrect(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(linked(t), &c))["entity/service/api/index.html"])
	if !strings.Contains(page, `integrity="`) {
		t.Fatalf("no integrity attribute on the Mermaid script:\n%s", page)
	}
	escaped := strings.ReplaceAll(DefaultMermaidIntegrity, "+", "&#43;")
	if !strings.Contains(page, escaped) {
		t.Errorf("expected the entity-escaped hash %q in:\n%s", escaped, page)
	}
	if strings.Contains(page, `crossorigin="anonymous"`) == false {
		t.Error("an SRI-checked cross-origin script needs crossorigin=anonymous or the check cannot run")
	}
}

func TestNoMermaidScriptWhenSrcIsEmpty(t *testing.T) {
	in := linked(t)
	in.Mermaid = Mermaid{}
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["entity/service/api/index.html"])
	if strings.Contains(page, "cdn.jsdelivr.net") {
		t.Errorf("--mermaid-src none must emit no CDN script:\n%s", page)
	}
	// The diagram source still ships; mermaid.js labels it unrendered.
	if !strings.Contains(page, `class="mermaid"`) {
		t.Errorf("the diagram block must still be present:\n%s", page)
	}
}

func TestMapPageIsGolden(t *testing.T) {
	var c diag.Collector
	golden(t, "map.html", siteMap(Site(linked(t), &c))["map/index.html"])
}
