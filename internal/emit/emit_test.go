package emit

import (
	"testing"
	"testing/fstest"
)

func TestDiffReportsAMissingFile(t *testing.T) {
	want := []File{{Path: "CODEOWNERS", Data: []byte("* @team\n")}}
	got := fstest.MapFS{}

	diffs := Diff(want, got)

	if len(diffs) != 1 {
		t.Fatalf("got %d differences, want 1: %+v", len(diffs), diffs)
	}
	if diffs[0].Path != "CODEOWNERS" {
		t.Errorf("Path = %q, want %q", diffs[0].Path, "CODEOWNERS")
	}
	if diffs[0].Kind != DiffMissing {
		t.Errorf("Kind = %v, want DiffMissing", diffs[0].Kind)
	}
}

func TestDiffReportsAStaleFile(t *testing.T) {
	want := []File{{Path: "CODEOWNERS", Data: []byte("* @team-payments\n")}}
	got := fstest.MapFS{"CODEOWNERS": {Data: []byte("* @team-old\n")}}

	diffs := Diff(want, got)

	if len(diffs) != 1 {
		t.Fatalf("got %d differences, want 1: %+v", len(diffs), diffs)
	}
	if diffs[0].Kind != DiffStale {
		t.Errorf("Kind = %v, want DiffStale", diffs[0].Kind)
	}
}

func TestDiffIsEmptyWhenEverythingMatches(t *testing.T) {
	want := []File{
		{Path: "CODEOWNERS", Data: []byte("* @team\n")},
		{Path: "a/b.yaml", Data: []byte("k: v\n")},
	}
	got := fstest.MapFS{
		"CODEOWNERS": {Data: []byte("* @team\n")},
		"a/b.yaml":   {Data: []byte("k: v\n")},
	}

	if diffs := Diff(want, got); len(diffs) != 0 {
		t.Errorf("identical content must produce no differences, got %+v", diffs)
	}
}

// A file on disk that the generator does not produce is NOT a difference:
// landsraad generates a named set of artifacts and has no opinion about the
// rest of the repository. Reporting them would make `gen --check` fail on
// every repository that has any other file.
func TestDiffIgnoresFilesTheGeneratorDoesNotProduce(t *testing.T) {
	want := []File{{Path: "CODEOWNERS", Data: []byte("* @team\n")}}
	got := fstest.MapFS{
		"CODEOWNERS": {Data: []byte("* @team\n")},
		"README.md":  {Data: []byte("# hello\n")},
	}

	if diffs := Diff(want, got); len(diffs) != 0 {
		t.Errorf("unrelated files are not differences, got %+v", diffs)
	}
}

func TestDiffKindStringsAreHumanReadable(t *testing.T) {
	if got := DiffMissing.String(); got != "not generated yet" {
		t.Errorf("DiffMissing.String() = %q, want %q", got, "not generated yet")
	}
	if got := DiffStale.String(); got != "out of date" {
		t.Errorf("DiffStale.String() = %q, want %q", got, "out of date")
	}
}
