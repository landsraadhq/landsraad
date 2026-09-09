// Package discover locates and reads the catalog files in a repository.
//
// It works against any io/fs.FS: os.DirFS for a local checkout, a tar reader
// for a fetched remote repo, fstest.MapFS in tests. Nothing here calls os.
package discover

import (
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Filename is the fixed name of a catalog file.
const Filename = "service.yaml"

// File is one catalog file's bytes with the path it came from. Parsing and
// validation take these, so neither of those stages does any IO.
type File struct {
	Path string
	Data []byte
}

// Find returns the paths of every service.yaml matching the given glob
// patterns, sorted and deduplicated. Paths are slash-separated and relative to
// the filesystem root, as io/fs requires.
//
// A pattern of "." means the root itself. A pattern matching nothing is not an
// error: a repo may legitimately have no services under a configured path.
func Find(fsys fs.FS, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	for _, pattern := range patterns {
		var dirs []string
		if pattern == "." || pattern == "" {
			dirs = []string{"."}
		} else {
			matches, err := fs.Glob(fsys, pattern)
			if err != nil {
				// Only ErrBadPattern is possible, and that is a config bug.
				return nil, fmt.Errorf("bad path pattern %q: %w", pattern, err)
			}
			dirs = matches
		}
		for _, dir := range dirs {
			info, err := fs.Stat(fsys, dir)
			if err != nil || !info.IsDir() {
				continue
			}
			candidate := Filename
			if dir != "." {
				candidate = path.Join(dir, Filename)
			}
			if _, err := fs.Stat(fsys, candidate); err != nil {
				continue
			}
			seen[candidate] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// Load reads each path, reporting a diagnostic for any it cannot read and
// omitting it from the result. Never fails fast: one unreadable file does not
// hide problems in the rest.
func Load(fsys fs.FS, paths []string, c *diag.Collector) []File {
	out := make([]File, 0, len(paths))
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError,
				File:     p,
				Line:     1,
				Check:    "unreadable",
				Message:  fmt.Sprintf("cannot read file: %v", err),
			})
			continue
		}
		out = append(out, File{Path: p, Data: data})
	}
	return out
}
