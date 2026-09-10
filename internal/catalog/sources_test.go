package catalog

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

func TestSourcesFor(t *testing.T) {
	mono := fstest.MapFS{"services/api/runbook.md": {Data: []byte("mono")}}
	edge := fstest.MapFS{"runbook.md": {Data: []byte("edge")}}
	s := Sources{"monorepo": mono, "edge-gateway": edge}

	for _, tt := range []struct {
		name   string
		entity *Entity
		want   string
		wantOK bool
	}{
		{"first repo", &Entity{SourceRepo: "monorepo"}, "mono", true},
		{"second repo", &Entity{SourceRepo: "edge-gateway"}, "edge", true},
		{"unknown repo", &Entity{SourceRepo: "nope"}, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := s.For(tt.entity)
			if ok != tt.wantOK {
				t.Fatalf("For() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			paths := []string{"services/api/runbook.md", "runbook.md"}
			var data []byte
			for _, p := range paths {
				if b, err := fs.ReadFile(got, p); err == nil {
					data = b
					break
				}
			}
			if string(data) != tt.want {
				t.Errorf("read %q, want %q", data, tt.want)
			}
		})
	}
}

// An entity parsed from a repository with no repos.yaml has an empty
// SourceRepo (validate.go's localRepoName returns ""). That must resolve,
// not miss: every single-repo command in the tool is in that state.
func TestSingleSourceResolvesTheEmptyName(t *testing.T) {
	fsys := fstest.MapFS{"service.yaml": {Data: []byte("x")}}
	s := SingleSource("", fsys)
	if _, ok := s.For(&Entity{SourceRepo: ""}); !ok {
		t.Fatal("For() with an empty SourceRepo missed; single-repo commands depend on it")
	}
}

// Methods on a nil map are nil-safe for free. This is load-bearing: a check
// calls env.Sources.For(e) deep inside Score, and an Env built without one
// must produce a diagnostic naming the repository, not a panic pointing at
// a scorecard check for a mistake made in cmd/.
func TestNilSourcesAnswersRatherThanPanics(t *testing.T) {
	var s Sources
	if _, ok := s.Get("x"); ok {
		t.Fatal("nil Sources returned ok")
	}
	if got := s.Names(); len(got) != 0 {
		t.Errorf("Names() on nil = %v, want empty", got)
	}
	if got := s.With("a", fstest.MapFS{}); len(got) != 1 {
		t.Errorf("With on nil = %v, want one entry", got)
	}
}

func TestSourcesWithReturnsANewValue(t *testing.T) {
	before := fstest.MapFS{"x": {Data: []byte("before")}}
	after := fstest.MapFS{"x": {Data: []byte("after")}}
	s1 := Sources{"repo": before}
	s2 := s1.With("repo", after)

	got1, _ := s1.Get("repo")
	b1, _ := fs.ReadFile(got1, "x")
	if string(b1) != "before" {
		t.Errorf("With() mutated the receiver: read %q, want %q", b1, "before")
	}
	got2, _ := s2.Get("repo")
	b2, _ := fs.ReadFile(got2, "x")
	if string(b2) != "after" {
		t.Errorf("With() result read %q, want %q", b2, "after")
	}
}

func TestSourcesNamesAreSorted(t *testing.T) {
	s := Sources{"zeta": fstest.MapFS{}, "alpha": fstest.MapFS{}, "mu": fstest.MapFS{}}
	if diff := cmp.Diff([]string{"alpha", "mu", "zeta"}, s.Names()); diff != "" {
		t.Errorf("Names() mismatch (-want +got):\n%s", diff)
	}
}

func TestMissingSourceDiagnostic(t *testing.T) {
	e := &Entity{SourceRepo: "ghost", SourcePath: "services/api/service.yaml", NameLine: 4}
	e.Kind = "Service"
	e.Metadata.Name = "api"
	d := MissingSourceDiagnostic(e, "docs-unreadable")

	if d.Message != `no filesystem for repository "ghost", which defines service:api` {
		t.Errorf("Message = %q", d.Message)
	}
	if d.Hint != "this is a landsraad bug, not a problem with your catalog: cmd/ must put every repository it parsed into catalog.Sources" {
		t.Errorf("Hint = %q", d.Hint)
	}
	if d.Check != "docs-unreadable" {
		t.Errorf("Check = %q, want %q", d.Check, "docs-unreadable")
	}
	if d.Line != 4 {
		t.Errorf("Line = %d, want 4", d.Line)
	}
}
