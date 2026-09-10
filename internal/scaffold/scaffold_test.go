package scaffold

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/schema"
)

// asFS renders the scaffold as the repository it would become, without
// touching a disk. This is the point of Files() being a pure value: the
// property that matters — what init produces is already valid — is checked
// here in memory, and the cmd/ test is left to check only the file-writing
// loop, which genuinely needs a filesystem.
func asFS(t *testing.T) fs.FS {
	t.Helper()
	fsys := fstest.MapFS{}
	for _, f := range Files() {
		fsys[f.Path] = &fstest.MapFile{Data: f.Data}
	}
	return fsys
}

// The acceptance criterion for init: a fresh repository validates on the first
// run. A scaffold that needs editing before it passes teaches a new user that
// the tool's own output does not meet the tool's own standard.
func TestScaffoldValidatesOnTheFirstRun(t *testing.T) {
	fsys := asFS(t)
	var c diag.Collector

	data, err := fs.ReadFile(fsys, "repos.yaml")
	if err != nil {
		t.Fatalf("scaffold must include repos.yaml: %v", err)
	}
	patterns, why := config.LoadRepos("repos.yaml", data, &c).LocalPatterns()
	if why == config.LocalDefaulted {
		t.Error("the scaffold's own repos.yaml must list paths, not fall back to defaults")
	}

	paths, err := discover.Find(fsys, patterns)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected exactly one service.yaml, found %v", paths)
	}

	validator, err := schema.Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	files := discover.Load(fsys, paths, &c)
	for _, f := range files {
		validator.Validate("", f.Path, f.Data, &c)
	}

	cat := catalog.NewCatalog(catalog.ParseAll("", files, &c), &c)
	catalog.CheckFiles(fsys, cat, &c)
	cat.Resolve(catalog.LocalOnly, &c)

	teams, err := fs.ReadFile(fsys, "teams.yaml")
	if err != nil {
		t.Fatalf("scaffold must include teams.yaml: %v", err)
	}
	config.LoadTeams("teams.yaml", teams, &c).ValidateOwners(cat, &c)

	if c.HasErrors() {
		t.Errorf("what init writes must validate as-is: %+v", c.Diagnostics())
	}
}

// The modeline in the example service.yaml points at a relative path. If the
// schema is not written where it points, editor autocompletion silently does
// nothing in a fresh repository — the failure a user never reports.
func TestScaffoldWritesTheSchemaTheModelinePointsAt(t *testing.T) {
	fsys := asFS(t)
	if _, err := fs.Stat(fsys, "schema/service.schema.json"); err != nil {
		t.Fatalf("the modeline points at schema/service.schema.json: %v", err)
	}
	if _, err := fs.Stat(fsys, "services/example/service.yaml"); err != nil {
		t.Fatalf("services/example/service.yaml: %v", err)
	}
}

// Files() is a value, so a caller mutating what it got back must not change
// what the next caller sees. This is the property AllKinds and DefaultPatterns
// did not have.
func TestFilesIsFreshOnEachCall(t *testing.T) {
	first := Files()
	first[0].Path = "clobbered"
	first[0].Data = []byte("clobbered")
	if got := Files(); got[0].Path == "clobbered" || string(got[0].Data) == "clobbered" {
		t.Error("Files() handed out state a caller could rewrite")
	}
}
