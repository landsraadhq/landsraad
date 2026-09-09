package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
)

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
