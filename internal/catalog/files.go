package catalog

import (
	"fmt"
	"io/fs"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// CheckFiles verifies that every path an entity points at exists in the given
// filesystem. Empty paths are skipped: these fields are optional, and "not
// set" is a scorecard question, not a validation error.
//
// Taking an fs.FS rather than a root path is what lets the platform build run
// this against a fetched remote repo with no change.
func CheckFiles(fsys fs.FS, cat *Catalog, c *diag.Collector) {
	for _, e := range cat.Entities {
		for _, f := range []struct {
			field string
			path  string
			dir   bool
		}{
			{"spec.runbook", e.Spec.Runbook, false},
			{"spec.docs", e.Spec.Docs, true},
			{"spec.alerts", e.Spec.Alerts, false},
		} {
			if f.path == "" {
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
