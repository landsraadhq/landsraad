package main

import (
	"fmt"
	"io"
	"os"
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

// readManifest returns the paths the previous build wrote, and whether there
// was a manifest at all. Absent and empty are different: absent means this
// directory was not written by landsraad.
func readManifest(outDir string) ([]string, bool) {
	data, err := os.ReadFile(filepath.Join(outDir, manifestName))
	if err != nil {
		return nil, false
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, true
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
	previous, known := readManifest(outDir)
	if !known {
		empty, err := isEmptyOrMissing(outDir)
		if err != nil {
			return err
		}
		if !empty && !force {
			return fmt.Errorf("refusing to write into %s: it is not empty and was not written by landsraad build", outDir)
		}
	}

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
