package main

import (
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
	got, ok := readManifest(dir)
	if !ok {
		t.Fatal("readManifest reported no manifest just after writing one")
	}
	want := []string{"entity/service/api/index.html", "index.html"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReadManifestReportsAbsence(t *testing.T) {
	if _, ok := readManifest(t.TempDir()); ok {
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

func TestWriteSiteCreatesAMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dist")
	if err := writeSite(dir, []emit.File{{Path: "index.html", Data: []byte("a")}}, false, os.Stderr); err != nil {
		t.Fatalf("writeSite: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
		t.Errorf("index.html was not written: %v", err)
	}
}
