package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// reportDiagnostics writes diagnostics to a terminal in full.
//
// Every command that prints diagnostics outside a --format payload goes
// through here. The alternative that grew up in build.go and score.go —
// fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message) — threw away the file,
// the line, the check name and the hint: everything diag.Text renders and
// everything the plan's Global Constraints promise, which is that every
// diagnostic carries a file and a line and says what to do.
//
// It matters most in build. Plan 3 is the first stage whose diagnostics name
// paths in the user's repository (docs-unreadable at services/api/docs/
// broken.md) and carry load-bearing hints ("this is a landsraad bug, not a
// problem with your catalog: cmd/ must read the file named by --mermaid-src
// before calling Site"). Those hints are written, asserted character for
// character in tests, and were then dropped on the floor at the last step.
//
// diag.Text writes to a terminal, so a write error means stderr itself is
// gone; there is nowhere left to report it except the same broken stream, so
// this says so once and returns rather than looping.
func reportDiagnostics(errOut io.Writer, ds []diag.Diagnostic) {
	if err := (diag.Text{}).Write(errOut, ds); err != nil {
		fmt.Fprintf(errOut, "error: cannot write diagnostics: %v\n", err)
	}
}

// rootMarkers identify a repository root, most specific first.
var rootMarkers = []string{"repos.yaml", "teams.yaml", ".git"}

// findRoot walks up from start looking for a repository root, the way every
// linter does. Without it, running `landsraad validate` from inside
// services/foo/ reports "teams.yaml not found at the repository root" while
// standing in a subdirectory of a perfectly valid repo.
//
// start itself must exist: without this check, a typo'd argument inside a
// real repo (`validate serivces/api`) walked upward, found the repo's real
// root by accident, and validated that instead — a clean pass for a
// directory that was never actually inspected.
func findRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", start)
	}
	for {
		for _, marker := range rootMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no repository root above %s: looked for %v "+
				"(run `landsraad init` to create them)", start, rootMarkers)
		}
		dir = parent
	}
}

// version reports the module version recorded at build time, so a bug report
// quotes something useful. `go install ...@latest` applies no -ldflags, which
// is why every user would otherwise report "dev".
func version() string {
	if Version != "dev" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return Version
}
