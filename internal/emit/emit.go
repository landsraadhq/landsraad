// Package emit holds the type every landsraad generator returns.
//
// Spec §7's stage 8 is EMIT. Spec §3.1 puts writes at the command layer, and
// Plan 1 settled the shape: a generator is a pure function returning the files
// it would write, and cmd/ owns the single loop that puts them on disk. An
// io.Writer is one stream and every generator here produces a set.
//
// Keeping the type here rather than in any one producer's package is what lets
// scaffold, generate and scorecard's history writer share cmd/'s write loop
// without importing each other.
package emit

import (
	"bytes"
	"fmt"
	"io/fs"
	"sort"
)

// File is one file to write: a slash-separated path relative to a root the
// generator never names, and its contents.
type File struct {
	Path string
	Data []byte
}

func (f File) String() string {
	return fmt.Sprintf("%s (%d bytes)", f.Path, len(f.Data))
}

// DiffKind says how a generated file differs from what is on disk.
type DiffKind int

const (
	// DiffMissing: the generator produces this file and it is not committed.
	DiffMissing DiffKind = iota
	// DiffStale: it is committed, with different content.
	DiffStale
)

func (k DiffKind) String() string {
	switch k {
	case DiffMissing:
		return "not generated yet"
	case DiffStale:
		return "out of date"
	}
	return "unknown"
}

// Difference is one generated file that does not match the repository.
type Difference struct {
	Path string
	Kind DiffKind
}

// Diff compares what a generator would write against what is in fsys,
// returning one Difference per mismatch, sorted by path.
//
// This is the mechanism behind `gen --check` (spec §8) and therefore the
// mechanism that makes metadata rot break something visible. It runs entirely
// in memory: the spec describes regenerating to a temp dir, but the comparison
// is the point and a temp dir is only one way to hold the bytes.
//
// Files present in fsys that no generator produces are deliberately not
// reported. landsraad generates a named set of artifacts and has no opinion
// about the rest of the repository; reporting them would make --check fail on
// every repository that contains anything else.
func Diff(want []File, fsys fs.FS) []Difference {
	var out []Difference
	for _, f := range want {
		got, err := fs.ReadFile(fsys, f.Path)
		if err != nil {
			out = append(out, Difference{Path: f.Path, Kind: DiffMissing})
			continue
		}
		if !bytes.Equal(got, f.Data) {
			out = append(out, Difference{Path: f.Path, Kind: DiffStale})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
