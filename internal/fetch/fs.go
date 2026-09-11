// Package fetch turns a remote repository into an io/fs.FS.
//
// It is the only package under internal/ that speaks HTTP, and it does so
// only when cmd/ tells it to: every request happens before stage 1 or
// between stages 1 and 3, never underneath a pipeline stage (ruling R25).
// What the stages see is this package's *FS, whose bytes are already in
// memory — which is what keeps "nothing under internal/ calls the network"
// true of every stage in the program.
//
// Nothing here imports os. The blob cache is a Cache interface implemented
// in cmd/, for the same reason every other write in this codebase is.
package fetch

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"time"
)

// Entry is one path in a repository's tree.
//
// SHA is the git blob SHA the host's tree listing returns, and it is the
// cache key in ruling R27: content-addressed, so a hit cannot be stale. It
// is empty for a directory.
type Entry struct {
	Path string
	SHA  string
	Size int64
	Dir  bool
}

// ErrNotFetched means the path is in the tree and its content was never
// requested. It is a bug in cmd/'s content planner, never a user's mistake,
// and it is deliberately not ErrNotExist: a missing-file diagnostic would
// send somebody to look for a file that is sitting in their repository.
var ErrNotFetched = errors.New("content was listed but never fetched")

// ErrNotListed means nothing is known about this path's directory, because
// no listing ever covered it. Also a planner bug, and also deliberately not
// ErrNotExist — see spec §14.1 on why telling somebody a file does not
// exist, when the truth is that landsraad never looked, is worse than
// saying nothing.
var ErrNotListed = errors.New("directory was never listed")

// FS is a repository whose metadata arrives before its content.
//
// Stat, ReadDir, Glob and WalkDir are answered from the listing alone and
// cost nothing. Open — and therefore fs.ReadFile — needs the blob, which
// cmd/ must have fetched first.
//
// *FS is not safe for concurrent mutation: entries, listed and blobs are
// plain maps with no lock. AddDir and Put must be driven from a single
// goroutine, with no other goroutine reading or writing the same *FS at the
// same time. This is relied on rather than merely assumed: fetchBlobs fans
// blob requests out across worker goroutines but funnels every f.Put
// through the single goroutine that collects their results, and cmd/ drives
// one repository's phases — Open, Expand, Fetch — one at a time. A caller
// wanting to run several mutating calls against the same *FS concurrently
// (Expand for two entities' docs directories, say) has to serialize them
// itself; *FS does not add a lock to do it for them, the same way
// Catalog.Entities() hands back its own slice rather than guarding against
// a caller that does not exist.
type FS struct {
	entries map[string]Entry
	// listed holds the directories whose contents are known. A complete
	// recursive listing marks every directory; R28's fallback marks only
	// what it walked.
	listed map[string]bool
	blobs  map[string][]byte
}

// NewFS returns an empty filesystem with only its root listed.
func NewFS() *FS {
	return &FS{
		entries: map[string]Entry{},
		listed:  map[string]bool{".": true},
		blobs:   map[string][]byte{},
	}
}

// FromEntries builds a filesystem from a COMPLETE listing — one recursive
// response that describes the whole repository.
//
// It marks every directory in the listing, and every *parent* of every
// entry whether or not the host returned a row for it: a recursive listing
// that mentions docs/index.md has told us what is in docs/.
//
// A partial listing never comes through here. GitHub's truncated-tree
// descent and GitLab's prefix listing both build with NewFS plus AddDir,
// which marks exactly the directories that were actually walked — and that
// distinction is the only thing letting *FS tell "absent" from "never
// looked". A flag on this function would be a second way to express it, and
// the wrong value would silently turn the second answer into the first.
func FromEntries(entries []Entry) *FS {
	f := NewFS()
	for _, e := range entries {
		f.entries[e.Path] = e
		if e.Dir {
			f.listed[e.Path] = true
		}
		for d := path.Dir(e.Path); d != "." && d != "/"; d = path.Dir(d) {
			f.listed[d] = true
		}
	}
	return f
}

// AddDir records the contents of one directory. Used by R28's fallback,
// which learns the repository one listing at a time. A directory row among
// entries says that directory exists; only AddDir on that directory says
// what is inside it.
func (f *FS) AddDir(dir string, entries []Entry) {
	f.listed[dir] = true
	for _, e := range entries {
		f.entries[e.Path] = e
	}
}

// Put stores a blob's content.
func (f *FS) Put(p string, data []byte) { f.blobs[p] = data }

// Entries returns every known entry, sorted by path.
func (f *FS) Entries() []Entry {
	out := make([]Entry, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// Listed reports whether a directory's contents are known.
func (f *FS) Listed(dir string) bool { return f.listed[dir] }

// Stat implements fs.StatFS.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	e, err := f.lookup("stat", name)
	if err != nil {
		return nil, err
	}
	return fileInfo{e}, nil
}

// Open implements fs.FS. Opening a directory yields something ReadDir can
// walk; opening a file needs its content to have been fetched.
func (f *FS) Open(name string) (fs.File, error) {
	e, err := f.lookup("open", name)
	if err != nil {
		return nil, err
	}
	if e.Dir {
		return &dir{fs: f, info: fileInfo{e}}, nil
	}
	data, ok := f.blobs[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: ErrNotFetched}
	}
	return &file{info: fileInfo{e}, r: bytes.NewReader(data)}, nil
}

// ReadDir implements fs.ReadDirFS.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	if _, err := f.lookup("readdir", name); err != nil {
		return nil, err
	}
	if !f.listed[name] {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: ErrNotListed}
	}
	var out []fs.DirEntry
	for p, e := range f.entries {
		if path.Dir(p) == name && p != name {
			out = append(out, fileInfo{e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// lookup resolves a path to its entry, distinguishing the three answers a
// sparse filesystem has and a complete one does not.
func (f *FS) lookup(op, name string) (Entry, error) {
	if !fs.ValidPath(name) {
		return Entry{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return Entry{Path: ".", Dir: true}, nil
	}
	if e, ok := f.entries[name]; ok {
		return e, nil
	}
	// Absent. Whether that is a fact about the repository or a fact about
	// what landsraad bothered to list depends on the parent.
	if f.listed[path.Dir(name)] {
		return Entry{}, &fs.PathError{Op: op, Path: name, Err: fs.ErrNotExist}
	}
	return Entry{}, &fs.PathError{Op: op, Path: name, Err: ErrNotListed}
}

// fileInfo is one entry as both an fs.FileInfo and an fs.DirEntry.
//
// ModTime is the zero time: a tree listing carries no timestamps, and
// inventing time.Now() would be a lie that also makes every golden test
// fail one second after it is written. Nothing in landsraad reads ModTime —
// docs-fresh asks LastEditFunc precisely because a fetched repository has
// no useful mtimes (ruling R35).
type fileInfo struct{ e Entry }

func (i fileInfo) Name() string { return path.Base(i.e.Path) }
func (i fileInfo) Size() int64  { return i.e.Size }
func (i fileInfo) Mode() fs.FileMode {
	if i.e.Dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i fileInfo) ModTime() time.Time         { return time.Time{} }
func (i fileInfo) IsDir() bool                { return i.e.Dir }
func (i fileInfo) Sys() any                   { return nil }
func (i fileInfo) Type() fs.FileMode          { return i.Mode().Type() }
func (i fileInfo) Info() (fs.FileInfo, error) { return i, nil }

// file is an open blob.
type file struct {
	info fileInfo
	r    *bytes.Reader
}

func (f *file) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *file) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *file) Close() error               { return nil }

// dir is an open directory. fs.WalkDir reaches it through ReadDirFile.
type dir struct {
	fs     *FS
	info   fileInfo
	offset int
}

func (d *dir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *dir) Close() error               { return nil }
func (d *dir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.e.Path, Err: errors.New("is a directory")}
}

func (d *dir) ReadDir(n int) ([]fs.DirEntry, error) {
	all, err := d.fs.ReadDir(d.info.e.Path)
	if err != nil {
		return nil, err
	}
	if n <= 0 {
		d.offset = len(all)
		return all, nil
	}
	if d.offset >= len(all) {
		return nil, io.EOF
	}
	end := min(d.offset+n, len(all))
	out := all[d.offset:end]
	d.offset = end
	return out, nil
}

// Compile-time proof that *FS is everything the pipeline asks of it. Without
// StatFS and ReadDirFS, io/fs falls back to Open, which for this type means
// a directory walk would demand blob content it never needed.
var (
	_ fs.FS        = (*FS)(nil)
	_ fs.StatFS    = (*FS)(nil)
	_ fs.ReadDirFS = (*FS)(nil)
)
