package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

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

// version reports what this binary is, so that a bug report — and the footer
// of every page the portal generates — quotes something that identifies the
// build. `go install ...@latest` applies no -ldflags, which is why this
// cannot simply be the Version variable.
//
// The choice itself is versionFrom, which states the order and the reasons.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		bi = nil
	}
	return versionFrom(Version, bi)
}

// versionFrom is version's decision, with the build info taken as a parameter
// so every branch can be tested. Reading it inside version() left the choice
// untestable: a test binary always reports Main.Version "(devel)" and carries
// no VCS stamp, so a test calling version() exercises the fallback and
// nothing else — which is exactly how the wrong answer survived.
//
// stamped is the -ldflags value, "dev" when nobody set one.
func versionFrom(stamped string, bi *debug.BuildInfo) string {
	if stamped != "dev" {
		// Somebody deliberately stamped a release. Nothing outranks that.
		return stamped
	}
	if bi == nil {
		return stamped
	}
	if v := buildRevision(bi.Settings); v != "" {
		return v
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" && !strings.HasPrefix(v, "v0.0.0-") {
		// A real tag, which is the `go install ...@v1.2.3` case: no VCS
		// stamp, and the tag is the honest answer.
		return v
	}
	return stamped
}

// buildRevision describes a build by the commit it came from, and is empty
// when the binary carries no VCS stamp — an installed module, or
// -buildvcs=false.
//
// This exists because the obvious answer, bi.Main.Version, lies about a build
// from a checkout. Go fills it in with a pseudo-version like
// v0.0.0-20260910182806-2f17ea624567+dirty, and that leading v0.0.0 is not a
// release: it is the placeholder for "no tag is reachable from here". The
// portal stamps this string into the footer of every page it generates, so
// the old behaviour published "v0.0.0" — a version that was never cut — on
// every page of every preview, while burying the commit that would actually
// identify the build. A release stamps -ldflags -X main.Version and never
// reaches this path; `go install ...@v1.2.3` carries the real tag in
// Main.Version and carries no VCS stamp, so it does not reach it either.
func buildRevision(settings []debug.BuildSetting) string {
	var rev string
	var modified bool
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if rev == "" {
		return ""
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if modified {
		// Uncommitted changes: the commit alone would name a tree this
		// binary was not built from.
		return "dev-" + rev + "-dirty"
	}
	return "dev-" + rev
}
