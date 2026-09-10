package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/landsraadhq/landsraad/internal/emit"
)

// manifestName records what the last build wrote, relative to the output
// directory (ruling R16).
//
// It exists so a rebuild can delete the page of a service that has been
// removed from the catalog. Without it a deleted entity keeps a live URL on
// the portal forever — a page that says a service exists, hosted by the tool
// whose entire thesis is that the metadata is the source.
const manifestName = ".landsraad-manifest"

const manifestHeader = "# Written by `landsraad build`. Do not edit: the next build deletes\n" +
	"# every path listed here that it no longer produces.\n"

// manifest is what the previous build recorded, as far as this build is
// willing to believe it.
type manifest struct {
	// Paths are the entries that name a file inside the output directory.
	Paths []string
	// Found reports whether there was a manifest file at all. Absent and
	// empty are different: absent means this directory was not written by
	// landsraad.
	Found bool
	// Rejected holds the lines that named something else, verbatim. They are
	// never pruned and never counted as evidence — see writeSite.
	Rejected []string
}

// safeManifestPath reports whether a manifest line names a file inside the
// output directory.
//
// It exists because the manifest is a file on DISK, so its contents are input
// to this build, not output of it: dist/ is commonly committed for GitHub
// Pages and restored from a CI cache, and the prune loop below deletes every
// path it names. filepath.Join CLEANS its result, so "../victim.txt" joined
// against dist/ resolves to a sibling of dist/ and os.Remove deletes it — a
// manifest nobody here wrote turning `landsraad build` into arbitrary file
// deletion relative to -o. Demonstrated, not theorised:
// TestWriteSiteRefusesAManifestThatEscapesTheOutputDirectory fails against the
// unguarded version by watching a file outside the directory disappear.
//
// The rule is deliberately stricter than "does not escape". A path this tool
// wrote is always already clean and slash-separated — writeManifest emits
// emit.File.Path unaltered — so "./index.html" or "a//b.html" cannot have come
// from a landsraad build either, and a file that has been edited by something
// else is not evidence about what this directory contains.
func safeManifestPath(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return false
	}
	// A Windows-absolute or drive-relative path is absolute on the machine
	// that reads it even when it is not on the machine that wrote it.
	if strings.Contains(p, `\`) || strings.Contains(p, ":") {
		return false
	}
	if p != path.Clean(p) {
		return false
	}
	return p != ".." && !strings.HasPrefix(p, "../")
}

// readManifest returns the paths the previous build wrote, split into the ones
// this build is prepared to act on and the ones it is not.
func readManifest(outDir string) manifest {
	data, err := os.ReadFile(filepath.Join(outDir, manifestName))
	if err != nil {
		return manifest{}
	}
	out := manifest{Found: true}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !safeManifestPath(line) {
			out.Rejected = append(out.Rejected, line)
			continue
		}
		out.Paths = append(out.Paths, line)
	}
	return out
}

func writeManifest(outDir string, files []emit.File) error {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	body := manifestHeader + strings.Join(paths, "\n") + "\n"
	return os.WriteFile(filepath.Join(outDir, manifestName), []byte(body), 0o644)
}

// writeSite puts a rendered site on disk, pruning what the previous build
// wrote and no longer produces.
//
// It refuses a non-empty directory it did not write. `landsraad build -o .`
// is one keystroke away from `landsraad build -o dist`, and the difference
// between the two must not be somebody's repository.
func writeSite(outDir string, files []emit.File, force bool, errOut io.Writer) error {
	m := readManifest(outDir)
	for _, bad := range m.Rejected {
		fmt.Fprintf(errOut, "warn: ignoring the %s entry %q: it names a path outside %s\n", manifestName, bad, outDir)
	}

	// A manifest carrying an entry this build will not act on is not evidence
	// that landsraad wrote this directory: a file we cannot trust the contents
	// of cannot be trusted about its own provenance either. Forfeiting "known"
	// puts the directory back through the non-empty guard below, so a
	// tampered-with manifest cannot be used to bypass it.
	known := m.Found && len(m.Rejected) == 0
	if !known {
		empty, err := isEmptyOrMissing(outDir)
		if err != nil {
			return err
		}
		if !empty && !force {
			if len(m.Rejected) > 0 {
				return fmt.Errorf("refusing to write into %s: its %s names %s outside the directory, "+
					"so it is not evidence that landsraad build wrote this directory; "+
					"delete %s and build again, or pass --force",
					outDir, manifestName, plural(len(m.Rejected), "path", "paths"), outDir)
			}
			return fmt.Errorf("refusing to write into %s: it is not empty and was not written by landsraad build", outDir)
		}
	}
	previous := m.Paths

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Prune before writing, and only paths this tool put there. force
	// permits writing into an unknown directory; it never licenses deleting
	// a file landsraad did not write.
	current := map[string]bool{}
	for _, f := range files {
		current[f.Path] = true
	}
	pruned := 0
	for _, p := range previous {
		if current[p] {
			continue
		}
		full := filepath.Join(outDir, filepath.FromSlash(p))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot remove the stale page %s: %w", p, err)
		}
		pruned++
	}
	if pruned > 0 {
		fmt.Fprintf(errOut, "  removed %s no longer in the catalog\n", plural(pruned, "page", "pages"))
	}

	for _, f := range files {
		// The same guard as the prune loop, applied to this build's own
		// output. render.Site derives every path from a schema-validated
		// entity name and a test asserts none of them escape, but this is the
		// one line in the program that turns a site-relative path into a
		// filesystem path, so the property is checked where it is relied on
		// rather than two packages away.
		if !safeManifestPath(f.Path) {
			return fmt.Errorf("refusing to write %q: a rendered path must be relative to %s and must not escape it; "+
				"this is a landsraad bug, not a problem with your catalog", f.Path, outDir)
		}
		full := filepath.Join(outDir, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, f.Data, 0o644); err != nil {
			return err
		}
	}
	return writeManifest(outDir, files)
}

func isEmptyOrMissing(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}
