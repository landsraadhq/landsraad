package main

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/render"
)

var buildNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func buildOpts() BuildOptions {
	return BuildOptions{
		Now:      buildNow,
		LastEdit: noLastEdit(),
		Version:  "v0.3.0-test",
		Mermaid:  render.Mermaid{Src: render.DefaultMermaidSrc, Integrity: render.DefaultMermaidIntegrity},
	}
}

// buildFS is a minimal, valid single-repo catalog.
func buildFS() fstest.MapFS {
	return fstest.MapFS{
		"repos.yaml": {Data: []byte(
			"repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"teams.yaml": {Data: []byte(
			"teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")},
		"services/ledger-api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
				"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
				"  path: services/ledger-api\n")},
	}
}

func buildSiteMap(t *testing.T, fsys fstest.MapFS, opts BuildOptions) (map[string][]byte, int, string) {
	t.Helper()
	var errOut bytes.Buffer
	files, code := Build(fsys, &errOut, opts)
	out := map[string][]byte{}
	for _, f := range files {
		out[f.Path] = f.Data
	}
	return out, code, errOut.String()
}

func TestBuildRendersACleanCatalog(t *testing.T) {
	files, code, errOut := buildSiteMap(t, buildFS(), buildOpts())
	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut)
	}
	if _, ok := files["index.html"]; !ok {
		t.Error("no index.html")
	}
	if !strings.Contains(errOut, "ok: ") {
		t.Errorf("expected an ok summary on stderr, got:\n%s", errOut)
	}
}

// A portal generated from a broken catalog publishes the broken state as if
// it were the truth. gen already refuses for the same reason.
func TestBuildRefusesABrokenCatalog(t *testing.T) {
	fsys := buildFS()
	fsys["services/ledger-api/service.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-ghost\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n")}

	files, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if len(files) != 0 {
		t.Errorf("a refused build must render nothing, got %d files", len(files))
	}
	want := "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n"
	if !strings.HasSuffix(errOut, want) {
		t.Errorf("stderr:\n%s\nmust end with:\n%s", errOut, want)
	}
}

// Spec §7.1: under build, a dangling reference is a hard failure. validate
// records it and moves on, because the target may live in another repo.
func TestBuildFailsOnADanglingReference(t *testing.T) {
	fsys := buildFS()
	fsys["services/ledger-api/service.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n  dependsOn: [topic:nowhere]\n")}

	_, code, _ := buildSiteMap(t, fsys, buildOpts())
	if code != exitValidation {
		t.Errorf("exit = %d, want %d for a dangling ref under FullCatalog", code, exitValidation)
	}
}

// Ruling R22. Rendering a portal that quietly omits two of three repos is
// exactly what spec §12 forbids. Plan 4 replaces this with real fetching.
func TestBuildWarnsWhenReposYAMLListsRepositoriesItCannotRead(t *testing.T) {
	fsys := buildFS()
	fsys["repos.yaml"] = &fstest.MapFile{Data: []byte(
		"repos:\n" +
			"  - url: https://github.com/org/monorepo\n    paths: [services/*]\n" +
			"  - url: https://github.com/org/edge-gateway\n    paths: [.]\n")}

	files, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitOK {
		t.Fatalf("a partial build still succeeds, got exit %d:\n%s", code, errOut)
	}
	wantLine := "warn: repos.yaml lists 2 repositories and this build read only the local one; " +
		"edge-gateway is missing from the portal\n"
	if !strings.Contains(errOut, wantLine) {
		t.Errorf("stderr:\n%s\nmust contain:\n%s", errOut, wantLine)
	}
	// Degraded mode must be visible in the ARTIFACT, not only in a log.
	index := string(files["index.html"])
	if !strings.Contains(index, "read only the local repository") {
		t.Errorf("the generated page carries no banner:\n%s", index)
	}
}

func TestBuildPassesTheFrozenClockThrough(t *testing.T) {
	files, _, _ := buildSiteMap(t, buildFS(), buildOpts())
	if !strings.Contains(string(files["index.html"]), "2026-09-09 12:00 UTC") {
		t.Error("the page footer must carry the injected build time, not time.Now()")
	}
}

func TestBuildIsPureAndWritesNothing(t *testing.T) {
	fsys := buildFS()
	before := len(fsys)
	buildSiteMap(t, fsys, buildOpts())
	if len(fsys) != before {
		t.Error("Build must not write into the filesystem it reads")
	}
}
