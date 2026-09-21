package catalog

import (
	"io/fs"
	"path"
)

// docsIndexNames are the file names that count as an entity's documentation
// index, in precedence order.
//
// index.md first, so a repository that has one is scored and rendered off
// exactly the file it was before ruling R51 widened this — the change cannot
// move a verdict that already existed. _index.md is Hugo's spelling, and it is
// what a real documentation tree mostly holds: the monorepo that prompted R51
// has one file of the first name and 53 of the second, which is why all 18 of
// its documented services reported having no documentation at all.
//
// Unexported, and an array rather than an exported slice, for the reason
// allKinds is: an exported slice is package-level state any importer can
// rewrite.
var docsIndexNames = [...]string{"index.md", "_index.md"}

// DocsIndexNames returns the accepted documentation index file names in
// precedence order. The slice is fresh on each call, so a caller cannot reach
// back and change what the package believes.
func DocsIndexNames() []string {
	out := make([]string, 0, len(docsIndexNames))
	for _, n := range docsIndexNames {
		out = append(out, n)
	}
	return out
}

// DocsIndex finds an entity's documentation index under docsDir and returns
// its repository-relative path.
//
// One definition, because the scorecard and the renderer both have to answer
// "which file is this entity's documentation index", and two answers is how a
// service comes to pass docs-fresh while the portal refuses to hoist the very
// file that passed it.
//
// An empty docsDir means the entity has no documentation at all, which is a
// different fact from a directory holding no index — only one of the two is
// the user's to fix, so the caller distinguishes them rather than this.
func DocsIndex(fsys fs.FS, docsDir string) (string, bool) {
	if docsDir == "" {
		return "", false
	}
	for _, name := range docsIndexNames {
		p := path.Join(docsDir, name)
		if _, err := fs.Stat(fsys, p); err == nil {
			return p, true
		}
	}
	return "", false
}
