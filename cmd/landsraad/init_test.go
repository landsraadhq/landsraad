package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestInitWritesAWorkingCatalog(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	if err := runInit(dir, &out); err != nil {
		t.Fatalf("runInit: %v", err)
	}
	for _, f := range []string{"teams.yaml", "repos.yaml", "services/example/service.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("init did not create %s", f)
		}
	}

	// The whole point: what init writes must pass validate immediately.
	var vout, verr bytes.Buffer
	if code := Validate(os.DirFS(dir), &vout, &verr, diagText()); code != exitOK {
		t.Errorf("a freshly initialised repo must validate, got exit %d\n%s", code, vout.String())
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
