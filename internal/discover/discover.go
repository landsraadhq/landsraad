// Package discover locates and reads the catalog files in a repository.
//
// It works against any io/fs.FS: os.DirFS for a local checkout, a tar reader
// for a fetched remote repo, fstest.MapFS in tests. Nothing here calls os.
package discover

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

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
// error: a repo may legitimately have no services under a configured path —
// that is an empty directory, not a mistake.
//
// A pattern that can never denote anything under the repository root is a
// different thing entirely: absolute, containing "..", or otherwise not a
// valid io/fs path per fs.ValidPath. That is a configuration mistake in
// repos.yaml, not an empty directory, and fs.Glob silently returns no error
// and no matches for it — which would otherwise look identical to a
// legitimately quiet repo. Find rejects it instead, for the same reason it
// already rejects malformed glob syntax: a validate run that silently looks
// at nothing must not exit clean.
func Find(fsys fs.FS, patterns []string) ([]string, error) {
	seen := map[string]bool{}
	for _, pattern := range patterns {
		var dirs []string
		if pattern == "." || pattern == "" {
			dirs = []string{"."}
		} else {
			if !fs.ValidPath(pattern) {
				return nil, errors.New(invalidPatternReason(pattern))
			}
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

// invalidPatternReason diagnoses why fs.ValidPath rejected pattern, so the
// error names the actual mistake instead of just saying "invalid". Checked
// in the same order fs.ValidPath itself would hit them: a leading slash
// makes the whole pattern absolute, so it is reported before anything about
// individual elements.
func invalidPatternReason(pattern string) string {
	if strings.HasPrefix(pattern, "/") {
		return fmt.Sprintf(
			"path pattern %q must not be absolute; write a path relative to the repository root, for example %q",
			pattern, strings.TrimPrefix(pattern, "/"))
	}
	if strings.HasSuffix(pattern, "/") {
		return fmt.Sprintf(
			"path pattern %q has a trailing slash; write %q instead",
			pattern, strings.TrimSuffix(pattern, "/"))
	}
	for _, elem := range strings.Split(pattern, "/") {
		switch elem {
		case "..":
			return fmt.Sprintf(
				"path pattern %q escapes the repository root via \"..\"; patterns must stay under the repository root",
				pattern)
		case ".":
			return fmt.Sprintf(
				"path pattern %q contains a redundant \".\" element; write it without that segment",
				pattern)
		case "":
			return fmt.Sprintf(
				"path pattern %q contains an empty path element (a doubled \"/\"); remove it",
				pattern)
		}
	}
	return fmt.Sprintf("path pattern %q is not a valid relative path", pattern)
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
