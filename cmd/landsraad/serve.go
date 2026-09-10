package main

import (
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/emit"
)

// debounce is how long the watcher waits for the filesystem to go quiet.
//
// An editor saving one file commonly emits several events — a write, a
// chmod, a rename into place — and rebuilding per event means three renders
// per save.
const debounce = 150 * time.Millisecond

// siteServer serves a rendered site from memory.
//
// There is no output directory, which is not a shortcut: a preview server
// that rebuilt into dist/ while watching the tree that contains dist/ would
// trigger itself forever. Build returning values rather than writing files
// is what removes the loop instead of managing it.
type siteServer struct {
	mu    sync.RWMutex
	files map[string][]byte
}

func (s *siteServer) set(files []emit.File) {
	next := make(map[string][]byte, len(files))
	for _, f := range files {
		next[f.Path] = f.Data
	}
	s.mu.Lock()
	s.files = next
	s.mu.Unlock()
}

func (s *siteServer) lookup(p string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.files[p]
	return data, ok
}

func (s *siteServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// path.Clean resolves any "..", and the leading slash is then stripped,
	// so a request can only ever name a key that a generator produced.
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" || strings.HasSuffix(r.URL.Path, "/") {
		// Directory-per-page URLs (ruling R11) resolve to their index
		// without a rewrite rule, which is what the static hosts do too.
		p = path.Join(p, "index.html")
	}
	data, ok := s.lookup(p)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// A preview server must never serve yesterday's page after a rebuild.
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// watchDirs adds root and every directory beneath it to w.
//
// fsnotify is NOT recursive — verified, not assumed: a watcher holding every
// directory that existed at startup receives nothing for a file created
// inside a directory made afterwards. The serve loop therefore calls this
// again for every new directory.
//
// .git is skipped. One `git status` touches hundreds of files under it and a
// rebase produces thousands, so watching it means a rebuild storm on every
// ordinary git command.
func watchDirs(root string, w *fsnotify.Watcher) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory that vanished mid-walk is not a reason to stop
			// watching the rest of the tree.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		return w.Add(p)
	})
}

// newRebuild returns a function that renders root into srv, serialised so a
// burst of triggers can never run Build concurrently.
//
// Build's LastEdit closure (gitLastEdit, lastedit.go) caches into a plain,
// unsynchronised map. The debounce timer's Stop-then-AfterFunc pattern can
// let a second rebuild start while the first is still running: Stop reports
// false, not "still running", when the timer already fired, so the caller
// cannot tell the difference. Two Build calls in flight at once then write
// that map from two goroutines at once, which panics with "fatal error:
// concurrent map writes" -- unrecoverable, and it takes the whole preview
// server down mid-session. A mutex around the entire rebuild serialises
// every trigger, so nothing Build touches can ever be entered twice at once
// -- not just this one cache.
func newRebuild(root string, opts BuildOptions, srv *siteServer, errOut io.Writer) func() {
	var mu sync.Mutex
	return func() {
		mu.Lock()
		defer mu.Unlock()
		files, code := Build(os.DirFS(root), errOut, opts)
		if code != exitOK {
			// Keep serving the last good site. A preview that goes blank
			// the moment you make a typo is a preview you stop trusting;
			// the diagnostics are already on stderr.
			fmt.Fprintf(errOut, "  build failed; still serving the previous version\n")
			return
		}
		srv.set(files)
	}
}

// Serve renders the site and serves it, rebuilding on change when watch is
// set.
func Serve(root, addr string, opts BuildOptions, watch bool, errOut io.Writer) error {
	srv := &siteServer{}
	rebuild := newRebuild(root, opts, srv, errOut)
	rebuild()

	if watch {
		w, err := fsnotify.NewWatcher()
		if err != nil {
			return fmt.Errorf("cannot watch %s: %w", root, err)
		}
		defer w.Close()
		if err := watchDirs(root, w); err != nil {
			return fmt.Errorf("cannot watch %s: %w", root, err)
		}
		go func() {
			var timer *time.Timer
			for {
				select {
				case event, ok := <-w.Events:
					if !ok {
						return
					}
					if event.Op&fsnotify.Create != 0 {
						if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
							// A new directory is invisible until added.
							_ = watchDirs(event.Name, w)
						}
					}
					if timer != nil {
						timer.Stop()
					}
					timer = time.AfterFunc(debounce, func() {
						fmt.Fprintf(errOut, "  change detected, rebuilding\n")
						rebuild()
					})
				case err, ok := <-w.Errors:
					if !ok {
						return
					}
					fmt.Fprintf(errOut, "watch error: %v\n", err)
				}
			}
		}()
		fmt.Fprintf(errOut, "watching %s for changes\n", root)
	}

	fmt.Fprintf(errOut, "serving on http://%s\n", addr)
	server := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func newServeCmd() *cobra.Command {
	var (
		addr       string
		watch      bool
		mermaidSrc string
	)
	cmd := &cobra.Command{
		Use:   "serve [root]",
		Short: "Preview the portal locally",
		Long: "Render the portal and serve it from memory. Nothing is written to " +
			"disk, so --watch cannot trigger itself by rebuilding into the tree it " +
			"is watching.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start := "."
			if len(args) == 1 {
				start = args[0]
			}
			resolved, err := findRoot(start)
			if err != nil {
				return err
			}
			mermaid, err := mermaidFor(mermaidSrc)
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			return Serve(resolved, addr, BuildOptions{
				Mermaid: mermaid,
				// A preview rebuilt on every save re-reads the clock, which
				// is what makes the footer's timestamp meaningful here.
				Now:      time.Now().UTC(),
				LastEdit: gitLastEdit(resolved),
				Version:  version(),
			}, watch, cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "localhost:8080", "address to listen on")
	cmd.Flags().BoolVar(&watch, "watch", false, "rebuild when a file changes")
	cmd.Flags().StringVar(&mermaidSrc, "mermaid-src", "",
		"Mermaid bundle: a URL, a path to a local file, or \"none\"")
	return cmd
}
