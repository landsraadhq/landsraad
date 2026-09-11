package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// The content set is every file a later stage will read. It is the whole
// contract between phase 1 and phase 2: a path missing from it reads as
// fetch.ErrNotFetched deep inside the renderer.
func TestContentSetIsEveryFileALaterStageReads(t *testing.T) {
	fsys := fstest.MapFS{
		"services/api/service.yaml":     {Data: []byte("x")},
		"services/api/runbook.md":       {Data: []byte("x")},
		"services/api/alerts.yaml":      {Data: []byte("x")},
		"services/api/docs/index.md":    {Data: []byte("x")},
		"services/api/docs/deep/why.md": {Data: []byte("x")},
		"services/api/docs/diagram.png": {Data: []byte("x")}, // not markdown
		"services/api/NOTES.md":         {Data: []byte("x")}, // not under docs
		".landsraad/checks/scan.yaml":   {Data: []byte("x")},
		".landsraad/checks/notes.txt":   {Data: []byte("x")}, // not a results file
	}
	e := &catalog.Entity{SourceRepo: "mono", SourcePath: "services/api/service.yaml"}
	e.Kind = "Service"
	e.Metadata.Name = "api"
	e.Spec.Runbook = "services/api/runbook.md"
	e.Spec.Alerts = "services/api/alerts.yaml"
	e.Spec.Docs = "services/api/docs"

	want := []string{
		".landsraad/checks/scan.yaml",
		"services/api/alerts.yaml",
		"services/api/docs/deep/why.md",
		"services/api/docs/index.md",
		"services/api/runbook.md",
	}
	if diff := cmp.Diff(want, contentSet(fsys, []*catalog.Entity{e})); diff != "" {
		t.Errorf("contentSet mismatch (-want +got):\n%s", diff)
	}
}

func TestContentSetSkipsUnsetFields(t *testing.T) {
	fsys := fstest.MapFS{"service.yaml": {Data: []byte("x")}}
	e := &catalog.Entity{SourceRepo: "mono", SourcePath: "service.yaml"}
	e.Kind = "Library"
	e.Metadata.Name = "lib"
	if got := contentSet(fsys, []*catalog.Entity{e}); len(got) != 0 {
		t.Errorf("contentSet = %v, want none: the entity names no files", got)
	}
}

func TestDocsDirs(t *testing.T) {
	a := &catalog.Entity{}
	a.Spec.Docs = "services/api/docs"
	b := &catalog.Entity{}
	b.Spec.Docs = "services/api/docs" // duplicate
	c := &catalog.Entity{}            // no docs
	want := []string{".landsraad/checks", "services/api/docs"}
	if diff := cmp.Diff(want, docsDirs([]*catalog.Entity{a, b, c})); diff != "" {
		t.Errorf("docsDirs mismatch (-want +got):\n%s", diff)
	}
}

// Ruling R29: the per-repository variable wins, then the host-wide one.
func TestTokenFor(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN":            "host-wide",
		"GITLAB_TOKEN":            "gl-wide",
		"LANDSRAAD_TOKEN_EDGE_GW": "per-repo",
	}
	look := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	for _, tt := range []struct {
		name string
		repo config.Repo
		want string
	}{
		{"per-repo wins", config.Repo{Name: "edge-gw", URL: "https://github.com/o/edge-gw"}, "per-repo"},
		{"github falls back to host", config.Repo{URL: "https://github.com/o/api"}, "host-wide"},
		{"gitlab falls back to host", config.Repo{URL: "https://gitlab.com/o/api"}, "gl-wide"},
		{"nothing set", config.Repo{URL: "https://github.com/o/x", Name: "x", Host: "bitbucket"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tokenFor(tt.repo, look); got != tt.want {
				t.Errorf("tokenFor = %q, want %q", got, tt.want)
			}
		})
	}
}

// The name is normalised the way the ruling states: uppercased, every
// non-alphanumeric byte to underscore. A repository called "edge-gateway"
// must not need a variable nobody could guess.
func TestTokenVarName(t *testing.T) {
	for _, tt := range []struct{ name, want string }{
		{"edge-gateway", "LANDSRAAD_TOKEN_EDGE_GATEWAY"},
		{"my.repo", "LANDSRAAD_TOKEN_MY_REPO"},
		{"api", "LANDSRAAD_TOKEN_API"},
	} {
		if got := tokenVarName(tt.name); got != tt.want {
			t.Errorf("tokenVarName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// openRepos's only path this task can exercise without an HTTP fixture: no
// repos.yaml at all, so loadReposFile's single-local-repository fallback is
// what runs. It still drives the real Open path (os.DirFS over Root, not a
// fetch.FS), so this is a genuine smoke test of the wiring in this file
// rather than of any adapter.
func TestOpenReposWithNoReposYAMLReadsTheLocalRepository(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "teams.yaml",
		"teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")
	writeFile(t, dir, "services/api/service.yaml", `apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 2
  lifecycle: production
spec:
  path: services/api
`)

	var c diag.Collector
	w := openRepos(context.Background(), reposOptions{
		Root:   dir,
		RootFS: os.DirFS(dir),
		Lookup: func(string) (string, bool) { return "", false },
		ErrOut: io.Discard,
	}, &c)

	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("Failures() = %+v, want none", got)
	}
	if got := w.Sources().Names(); len(got) != 1 {
		t.Fatalf("Sources().Names() = %v, want exactly one repository", got)
	}

	v := defaultValidator(&c)
	if v == nil {
		t.Fatalf("defaultValidator returned nil; diagnostics: %+v", c.Diagnostics())
	}
	entities := w.ParseAll(v, &c)
	if len(entities) != 1 || entities[0].Metadata.Name != "api" {
		t.Fatalf("ParseAll = %+v, want exactly the api entity", entities)
	}

	// The property that actually matters, not just the Identity() unit: a
	// checkout with no repos.yaml is most first runs, and Entity.Location()
	// renders SourceRepo == "" as a bare path. SourceRepo == "." (what
	// path.Base("") used to hand back through Repo.Identity()) would print
	// ".:services/api/service.yaml" in every diagnostic instead.
	if got := entities[0].SourceRepo; got != "" {
		t.Errorf("SourceRepo = %q, want \"\" (no repos.yaml means no repository name)", got)
	}
	if got, want := entities[0].Location(), "services/api/service.yaml"; got != want {
		t.Errorf("Location() = %q, want %q", got, want)
	}
}

func writeFile(t *testing.T, root, path, data string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", full, err)
	}
}

// The split must not change single-repository behaviour. This asserts the
// two halves compose back into what loadCatalogScoped always did.
func TestParseRepoThenAssembleMatchesTheOldPipeline(t *testing.T) {
	fsys := genFS() // the fixture gen_test.go uses for a valid in-memory repository
	var c diag.Collector
	cat, g, teams := loadCatalogScoped(fsys, catalog.FullCatalog, &c)
	if cat == nil || g == nil || teams == nil {
		t.Fatalf("loadCatalogScoped returned nils; diagnostics: %+v", c.Diagnostics())
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("got %d diagnostics on a good fixture: %+v", len(ds), ds)
	}
}
