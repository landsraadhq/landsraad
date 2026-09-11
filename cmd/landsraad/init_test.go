package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/schema"
)

func TestInitWritesAWorkingCatalog(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runInit(dir, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	for _, f := range []string{".gitignore", "teams.yaml", "repos.yaml", "services/example/service.yaml", "schema/service.schema.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("init did not create %s", f)
		}
	}

	// The whole point: what init writes must pass validate immediately.
	var vout, verr bytes.Buffer
	if code := Validate(os.DirFS(dir), &vout, &verr, diagText(), false); code != exitOK {
		t.Errorf("a freshly initialised repo must validate, got exit %d\n%s", code, vout.String())
	}
}

// The scaffolded service.yaml carries a yaml-language-server modeline, which
// is the entire reason `landsraad schema` exists. init used to leave that
// path dangling and print `landsraad schema > schema/service.schema.json` as
// the next step — a command that fails, because schema/ did not exist. A new
// user's first minute must end with autocompletion that works.
func TestInitResolvesTheEditorModeline(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runInit(dir, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(dir, "services", "example", "service.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	const modeline = "# yaml-language-server: $schema=../../schema/service.schema.json"
	if !strings.HasPrefix(string(body), modeline) {
		t.Fatalf("the scaffolded service.yaml no longer starts with the modeline:\n%s", body)
	}
	// Resolve the modeline the way an editor does: relative to the file.
	target := filepath.Join(dir, "services", "example", "..", "..", "schema", "service.schema.json")
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the modeline points at a file that does not exist: %v", err)
	}
	if !bytes.Equal(got, schema.Raw) {
		t.Error("the schema init writes must be the schema the binary validates against")
	}
	if strings.Contains(out.String(), "landsraad schema >") {
		t.Errorf("init must not print a next step the user has to run to get a working repo:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "landsraad validate") {
		t.Errorf("init must say what to run next:\n%s", out.String())
	}
}

// A cache committed to a repository would be an ever-growing directory of
// other repositories' file contents showing up in diffs (ruling R27), so a
// fresh repository must ignore it from the first commit.
func TestInitGitignoresTheBlobCache(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runInit(dir, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("init did not create .gitignore: %v", err)
	}
	want := "# landsraad's fetched-blob cache. Content-addressed, safe to delete.\n" +
		".landsraad/cache/\n"
	if string(got) != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestInitDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	keep := []byte("teams:\n  - name: mine\n")
	if err := os.WriteFile(filepath.Join(dir, "teams.yaml"), keep, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runInit(dir, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "teams.yaml"))
	if string(got) != string(keep) {
		t.Error("init must never overwrite a file that already exists")
	}
}
