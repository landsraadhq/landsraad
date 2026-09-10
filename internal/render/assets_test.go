package render

import (
	"html/template"
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// renderPage is the one place a broken page is reported and skipped rather
// than aborting the whole site (spec §12). This exercises the failure
// branch directly — renderPage takes a *template.Template, so a template
// that fails to execute needs no embedded FS to construct.
func TestRenderPageReportsAndSkipsATemplateThatFailsToExecute(t *testing.T) {
	tpl := template.Must(template.New("base.html").Parse(`{{.NoSuchField}}`))
	var c diag.Collector
	f, ok := renderPage(tpl, "index.html", struct{}{}, &c)
	if ok {
		t.Fatalf("renderPage must report ok=false for a template that fails to execute, got %+v", f)
	}
	if f.Path != "" || f.Data != nil {
		t.Errorf("a failed render must emit no file, got %+v", f)
	}

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("want exactly one diagnostic, got %d: %+v", len(ds), ds)
	}
	d := ds[0]
	wantMsg := `cannot render index.html: template: base.html:1:2: executing "base.html" at <.NoSuchField>: can't evaluate field NoSuchField in type struct {}`
	if d.Message != wantMsg {
		t.Errorf("Message =\n%q\nwant\n%q", d.Message, wantMsg)
	}
	if d.File != "index.html" {
		t.Errorf("File = %q, want %q", d.File, "index.html")
	}
	if d.Line != 1 {
		t.Errorf("Line = %d, want 1", d.Line)
	}
}

// A local Mermaid bundle with its bytes present is emitted verbatim.
func TestAssetsEmitsTheLocalMermaidBundleWhenDataIsPresent(t *testing.T) {
	in := twoEntities(t)
	in.Mermaid = Mermaid{Src: LocalMermaidPath, Data: []byte("console.log('mermaid')")}
	var c diag.Collector
	got := siteMap(assets(in, &c))
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("a populated local bundle must not report a diagnostic: %+v", ds)
	}
	data, ok := got[LocalMermaidPath]
	if !ok {
		t.Fatalf("assets() did not emit %s; got %v", LocalMermaidPath, keys(got))
	}
	if string(data) != "console.log('mermaid')" {
		t.Errorf("bundle bytes = %q, want the bytes passed in Mermaid.Data", data)
	}
}

// Src naming the local bundle with no bytes behind it is not a user-data
// problem — cmd/ is supposed to have read the file before building Input —
// so this must surface as a diagnostic, not a silently missing script tag
// (spec §12: no silent fallbacks). Every other asset must still be emitted.
func TestAssetsReportsWhenMermaidDataIsEmptyForTheLocalSrc(t *testing.T) {
	in := twoEntities(t)
	in.Mermaid = Mermaid{Src: LocalMermaidPath}
	var c diag.Collector
	got := siteMap(assets(in, &c))

	if _, ok := got[LocalMermaidPath]; ok {
		t.Errorf("must not emit %s with no bytes behind it", LocalMermaidPath)
	}
	for _, want := range []string{"assets/style.css", "assets/chroma.css"} {
		if _, ok := got[want]; !ok {
			t.Errorf("other assets must still be emitted despite the Mermaid diagnostic; missing %s", want)
		}
	}

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("want exactly one diagnostic, got %d: %+v", len(ds), ds)
	}
	d := ds[0]
	wantMsg := "cannot emit assets/mermaid.min.js: Mermaid.Data is empty"
	if d.Message != wantMsg {
		t.Errorf("Message = %q, want %q", d.Message, wantMsg)
	}
	if d.File != LocalMermaidPath {
		t.Errorf("File = %q, want %q", d.File, LocalMermaidPath)
	}
	if d.Line != 1 {
		t.Errorf("Line = %d, want 1", d.Line)
	}
	wantHint := "this is a landsraad bug, not a problem with your catalog: cmd/ must read the file named by --mermaid-src before calling Site"
	if d.Hint != wantHint {
		t.Errorf("Hint = %q, want %q", d.Hint, wantHint)
	}
}
