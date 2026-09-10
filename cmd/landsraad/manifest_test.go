package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/landsraadhq/landsraad/internal/emit"
)

func TestManifestRoundTrips(t *testing.T) {
	dir := t.TempDir()
	files := []emit.File{
		{Path: "index.html", Data: []byte("a")},
		{Path: "entity/service/api/index.html", Data: []byte("b")},
	}
	if err := writeManifest(dir, files); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}
	m := readManifest(dir)
	if !m.Found {
		t.Fatal("readManifest reported no manifest just after writing one")
	}
	if len(m.Rejected) != 0 {
		t.Errorf("a manifest this tool just wrote must have no rejected entries: %v", m.Rejected)
	}
	want := []string{"entity/service/api/index.html", "index.html"}
	if len(m.Paths) != len(want) {
		t.Fatalf("got %v, want %v", m.Paths, want)
	}
	for i := range want {
		if m.Paths[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, m.Paths[i], want[i])
		}
	}
}

func TestReadManifestReportsAbsence(t *testing.T) {
	if readManifest(t.TempDir()).Found {
		t.Error("an empty directory has no manifest")
	}
}

// Ruling R16: a deleted service's page must not stay on the portal. A stale
// page is a lie with a URL.
func TestWriteSitePrunesPagesTheBuildNoLongerProduces(t *testing.T) {
	dir := t.TempDir()
	first := []emit.File{
		{Path: "index.html", Data: []byte("one")},
		{Path: "entity/service/gone/index.html", Data: []byte("bye")},
	}
	if err := writeSite(dir, first, false, os.Stderr); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entity/service/gone/index.html")); err != nil {
		t.Fatalf("first build did not write the page: %v", err)
	}

	second := []emit.File{{Path: "index.html", Data: []byte("two")}}
	if err := writeSite(dir, second, false, os.Stderr); err != nil {
		t.Fatalf("second build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entity/service/gone/index.html")); !os.IsNotExist(err) {
		t.Error("the page of a deleted entity survived the rebuild")
	}
	data, err := os.ReadFile(filepath.Join(dir, "index.html"))
	if err != nil || string(data) != "two" {
		t.Errorf("index.html = %q, %v; want \"two\"", data, err)
	}
}

// `landsraad build -o .` must not be able to delete somebody's repository.
func TestWriteSiteRefusesAnUnknownNonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "important.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := writeSite(dir, []emit.File{{Path: "index.html", Data: []byte("a")}}, false, os.Stderr)
	if err == nil {
		t.Fatal("writeSite must refuse a non-empty directory with no manifest")
	}
	want := "refusing to write into " + dir + ": it is not empty and was not written by landsraad build"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "important.txt")); statErr != nil {
		t.Error("the refusal must not have touched the existing files")
	}
}

func TestWriteSiteAcceptsAnUnknownDirectoryUnderForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "important.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeSite(dir, []emit.File{{Path: "index.html", Data: []byte("a")}}, true, os.Stderr); err != nil {
		t.Fatalf("--force must permit it: %v", err)
	}
	// force permits writing; it does not license deleting files landsraad
	// never wrote.
	if _, err := os.Stat(filepath.Join(dir, "important.txt")); err != nil {
		t.Error("--force must not delete files outside the manifest")
	}
}

func TestWriteSiteAcceptsAnEmptyDirectory(t *testing.T) {
	if err := writeSite(t.TempDir(), []emit.File{{Path: "index.html", Data: []byte("a")}}, false, os.Stderr); err != nil {
		t.Fatalf("an empty directory is fine: %v", err)
	}
}

// writeSite's prune loop deletes every path the manifest names. The manifest
// is a file on disk — in dist/, a directory people commit for GitHub Pages and
// CI restores from a cache — so its contents are input, not something this
// build produced. A line naming "../victim.txt" survived filepath.Join, which
// cleans, and turned `landsraad build` into arbitrary file deletion relative
// to -o.
func TestWriteSiteRefusesAManifestThatEscapesTheOutputDirectory(t *testing.T) {
	parent := t.TempDir()
	victim := filepath.Join(parent, "victim.txt")
	if err := os.WriteFile(victim, []byte("not landsraad's to delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(parent, "dist")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, manifestName),
		[]byte(manifestHeader+"index.html\n../victim.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	err := writeSite(outDir, []emit.File{{Path: "index.html", Data: []byte("a")}}, false, &errOut)

	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("writeSite deleted a file outside the output directory: %v", statErr)
	}
	if err == nil {
		t.Fatal("a manifest naming a path outside the output directory must be refused")
	}
	want := "refusing to write into " + outDir + ": its " + manifestName +
		" names 1 path outside the directory, so it is not evidence that landsraad build wrote this directory; " +
		"delete " + outDir + " and build again, or pass --force"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	wantWarn := "warn: ignoring the " + manifestName + " entry \"../victim.txt\": it names a path outside " + outDir + "\n"
	if errOut.String() != wantWarn {
		t.Errorf("stderr = %q, want %q", errOut.String(), wantWarn)
	}
}

// Every shape that escapes, or that filepath.Join would silently rewrite into
// something other than what the line says. An absolute path ignores outDir
// entirely; "./x" and "a//b" are unclean spellings a manifest this tool wrote
// never contains, so their presence means the file was edited by something
// else and none of it can be trusted.
func TestReadManifestRejectsEveryUntrustworthyPathShape(t *testing.T) {
	for _, line := range []string{
		"../victim.txt",
		"../../etc/passwd",
		"a/../../victim.txt",
		"/etc/passwd",
		"./index.html",
		"a//b.html",
		"..",
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, manifestName),
			[]byte(manifestHeader+line+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		m := readManifest(dir)
		if len(m.Paths) != 0 {
			t.Errorf("%q was accepted as a manifest path: %v", line, m.Paths)
		}
		if len(m.Rejected) != 1 || m.Rejected[0] != line {
			t.Errorf("%q: Rejected = %v, want exactly [%q]", line, m.Rejected, line)
		}
	}
}

// --force permits writing into a directory landsraad did not create. It has
// never licensed deleting a file landsraad did not write, and an untrusted
// manifest is exactly that case.
func TestWriteSiteUnderForceStillWillNotPruneAnEscapingPath(t *testing.T) {
	parent := t.TempDir()
	victim := filepath.Join(parent, "victim.txt")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(parent, "dist")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, manifestName),
		[]byte(manifestHeader+"../victim.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var errOut bytes.Buffer
	if err := writeSite(outDir, []emit.File{{Path: "index.html", Data: []byte("a")}}, true, &errOut); err != nil {
		t.Fatalf("--force must permit the write: %v", err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("--force must not delete a file outside the output directory: %v", err)
	}
}

func TestWriteSiteCreatesAMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dist")
	if err := writeSite(dir, []emit.File{{Path: "index.html", Data: []byte("a")}}, false, os.Stderr); err != nil {
		t.Fatalf("writeSite: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Errorf("index.html was not written: %v", err)
	}
}
