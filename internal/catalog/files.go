package catalog

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// CheckFiles verifies that every path an entity points at exists in the given
// filesystem. Empty paths are skipped: these fields are optional, and "not
// set" is a scorecard question, not a validation error.
//
// Taking an fs.FS rather than a root path is what lets the platform build run
// this against a fetched remote repo with no change.
func CheckFiles(fsys fs.FS, cat *Catalog, c *diag.Collector) {
	for _, e := range cat.entities {
		for _, f := range []struct {
			field string
			path  string
			dir   bool
		}{
			// spec.path first: it is the anchor, the field that says which
			// directory this entity *is*, and the one CODEOWNERS generation
			// joins against. A wrong runbook breaks a link; a wrong path
			// assigns ownership to a directory that does not exist, which git
			// ignores without complaint. Checked for existence but not for
			// directory-ness — a Library may name a single file.
			{"spec.path", e.Spec.Path, false},
			{"spec.runbook", e.Spec.Runbook, false},
			{"spec.docs", e.Spec.Docs, true},
			{"spec.alerts", e.Spec.Alerts, false},
		} {
			if f.path == "" {
				continue
			}
			// fs.Stat rejects a path that is not a valid io/fs path before it
			// ever reaches the filesystem, so without this the answer for
			// "/etc" or "../shared/runbook.md" would be "does not exist" — a
			// statement about the user's repository that is simply false.
			// Rejecting them is spec §14.1's security property and is correct;
			// only the diagnosis was wrong.
			//
			// The classification below is the second instance of the one in
			// discover.invalidPatternReason, phrased for a service.yaml field
			// rather than a repos.yaml pattern. Two examples, not three: they
			// stay separate until a third caller shows what to share.
			if !fs.ValidPath(f.path) {
				c.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "invalid-path",
					Message:  invalidPathReason(f.field, f.path),
					Hint:     "paths are relative to the repository root, slash-separated",
				})
				continue
			}
			info, err := fs.Stat(fsys, f.path)
			if err != nil {
				c.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "missing-file",
					Message:  fmt.Sprintf("%s points at %q, which does not exist", f.field, f.path),
					Hint:     "paths are relative to the repository root, slash-separated",
				})
				continue
			}
			if f.dir && !info.IsDir() {
				c.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "missing-file",
					Message:  fmt.Sprintf("%s points at %q, which is a file but must be a directory", f.field, f.path),
				})
			}
		}
	}
}

// invalidPathReason names the actual mistake in a path field rather than
// saying "invalid". Checked in the order fs.ValidPath itself would hit them:
// a leading slash makes the whole path absolute, so it is reported before
// anything about individual elements.
func invalidPathReason(field, p string) string {
	switch {
	case strings.HasPrefix(p, "/"):
		return fmt.Sprintf(
			"%s points at %q, which is an absolute path; write it relative to the repository root, for example %q",
			field, p, strings.TrimPrefix(p, "/"))
	case strings.HasSuffix(p, "/"):
		return fmt.Sprintf(
			"%s points at %q, which has a trailing slash; write %q instead",
			field, p, strings.TrimSuffix(p, "/"))
	}
	for _, elem := range strings.Split(p, "/") {
		switch elem {
		case "..":
			return fmt.Sprintf(
				"%s points at %q, which escapes the repository root via %q; a service.yaml can only point at files in its own repository",
				field, p, "..")
		case ".":
			return fmt.Sprintf(
				"%s points at %q, which contains a redundant %q element; write it without that segment",
				field, p, ".")
		case "":
			return fmt.Sprintf(
				"%s points at %q, which contains an empty path element (a doubled %q); remove it",
				field, p, "/")
		}
	}
	return fmt.Sprintf("%s points at %q, which is not a valid relative path", field, p)
}
