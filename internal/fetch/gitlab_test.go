package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func newTestGitLab(t *testing.T, srv *httptest.Server, ref string) *GitLab {
	t.Helper()
	r, err := ParseRepo("billing", "https://gitlab.com/group/sub/billing", ref)
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}
	c := NewClient(ClientOptions{
		HTTP: srv.Client(), BaseURL: srv.URL, Token: "glpat-x",
		AuthHeader: "PRIVATE-TOKEN", AuthPrefix: "",
		Sleep: func(time.Duration) {},
	})
	return NewGitLab(r, c, NopCache{}, 4)
}

// The project id is the whole path, percent-encoded as one segment. Getting
// this wrong produces a 404 that looks exactly like a missing repository.
func TestGitLabEncodesTheProjectPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "")
	_, _ = g.Open(context.Background(), []string{"."})
	if want := "/projects/group%2Fsub%2Fbilling"; gotPath[:len(want)] != want {
		t.Errorf("project path = %q, want it to start with %q", gotPath, want)
	}
}

func TestGitLabSendsThePrivateTokenHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("PRIVATE-TOKEN")
		w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	_, _ = g.Open(context.Background(), []string{"."})
	if got != "glpat-x" {
		t.Errorf("PRIVATE-TOKEN = %q, want %q", got, "glpat-x")
	}
}

// per_page defaults to 20, so a repository with more than twenty files
// returns a first page that looks like the whole tree. Every page must be
// followed.
func TestGitLabFollowsEveryPage(t *testing.T) {
	const total = 250
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling" {
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
			return
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage != 100 {
			t.Errorf("per_page = %d, want 100", perPage)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		start := (page - 1) * perPage
		var rows []map[string]any
		for i := start; i < min(start+perPage, total); i++ {
			rows = append(rows, map[string]any{
				"id": fmt.Sprintf("blob-%d", i), "name": fmt.Sprintf("f%d.md", i),
				"type": "blob", "path": fmt.Sprintf("f%d.md", i),
			})
		}
		if start+perPage < total {
			w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
		}
		json.NewEncoder(w).Encode(rows)
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fs.Stat(f, "f249.md"); err != nil {
		t.Errorf("Stat of the last file across three pages: %v", err)
	}
	if got := len(f.Entries()); got != total {
		t.Errorf("listed %d entries, want %d", got, total)
	}
}

func TestGitLabFetchAndLastEdit(t *testing.T) {
	body := "# Billing\n"
	sha := gitBlobSHA([]byte(body))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/tree":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": sha, "name": "runbook.md", "type": "blob", "path": "runbook.md"},
			})
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/blobs/"+sha+"/raw":
			w.Write([]byte(body))
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/commits":
			json.NewEncoder(w).Encode([]map[string]any{{"committed_date": "2026-07-15T11:00:00+02:00"}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := g.Fetch(context.Background(), f, []string{"runbook.md"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := fs.ReadFile(f, "runbook.md")
	if err != nil || string(got) != body {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}

	edited, ok, err := g.LastEdit(context.Background(), "runbook.md")
	if err != nil || !ok {
		t.Fatalf("LastEdit = (%v, %v, %v)", edited, ok, err)
	}
	want := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	if !edited.Equal(want) {
		t.Errorf("LastEdit = %v, want %v (the offset must be honoured)", edited, want)
	}
}

// spec.docs can name a directory outside every pattern's literal prefix.
// Open's root listing shows that "docs" exists but lists only "services" in
// full, so what is inside "docs" is learned solely by Expand. The mock
// response gives docs a realistic nested shape: a file directly inside it,
// plus a "sub" subdirectory (a "type": "tree" row) with a file of its own
// beneath that, which drives list's tree-row branch on a recursive listing.
//
// GitLab's tree endpoint, queried at path=docs, returns docs's CHILDREN —
// it never reports a row for "docs" itself, the same way `ls docs` never
// prints "docs". Without AddDir also recording that dir itself exists
// (fs.go), "docs" would never gain an Entry of its own from this call, and
// fs.Stat(f, "docs") — what render/docs.go's fs.WalkDir needs first — would
// fail even though docs unambiguously exists. This test asserts on "docs"
// itself for exactly that reason.
func TestGitLabExpandListsADirectoryOutsideAnyPattern(t *testing.T) {
	blobs := map[string]string{"b-idx": "# Docs\n", "b-deep": "deep page\n"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/raw") {
			for sha, body := range blobs {
				if strings.Contains(r.URL.Path, sha) {
					w.Write([]byte(body))
					return
				}
			}
			t.Errorf("unexpected blob request: %s", r.URL.Path)
			return
		}
		switch r.URL.Query().Get("path") {
		case "":
			// The root listing ruling R45 has Open make before any prefix.
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "t-services", "name": "services", "type": "tree", "path": "services"},
				{"id": "t-docs", "name": "docs", "type": "tree", "path": "docs"},
			})
		case "services":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "b-svc", "name": "service.yaml", "type": "blob", "path": "services/api/service.yaml"},
			})
		case "docs":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "b-idx", "name": "index.md", "type": "blob", "path": "docs/index.md"},
				{"id": "d-sub", "name": "sub", "type": "tree", "path": "docs/sub"},
				{"id": "b-deep", "name": "deep.md", "type": "blob", "path": "docs/sub/deep.md"},
			})
		default:
			t.Errorf("unexpected path query: %q", r.URL.Query().Get("path"))
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"services/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := g.Expand(context.Background(), f, []string{"docs"}); err != nil {
		t.Fatalf("Expand: %v", err)
	}

	// docs itself — the exact argument passed to Expand, covered by no
	// pattern's prefix and never returned by GitLab as a row of itself —
	// must be recognised as a directory. This is the property render/docs.go
	// depends on: it calls fs.WalkDir(fsys, e.Spec.Docs, ...), which Stats
	// the root before walking it.
	for _, dir := range []string{"docs", "docs/sub"} {
		info, err := fs.Stat(f, dir)
		if err != nil {
			t.Fatalf("Stat %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("%s.IsDir() = false, want true", dir)
		}
	}

	// Its children, one level and two levels down, are reachable.
	for _, p := range []string{"docs/index.md", "docs/sub/deep.md"} {
		if _, err := fs.Stat(f, p); err != nil {
			t.Errorf("Stat %s after Expand: %v", p, err)
		}
	}

	// Walking from docs itself — what render/docs.go actually does —
	// visits every file beneath it, at every depth.
	var walked []string
	err = fs.WalkDir(f, "docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			walked = append(walked, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir docs: %v", err)
	}
	want := []string{"docs/index.md", "docs/sub/deep.md"}
	if diff := cmp.Diff(want, walked); diff != "" {
		t.Errorf("WalkDir docs mismatch (-want +got):\n%s", diff)
	}

	// Expand made the content fetchable, not just listed.
	if err := g.Fetch(context.Background(), f, []string{"docs/index.md", "docs/sub/deep.md"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got, err := fs.ReadFile(f, "docs/index.md"); err != nil || string(got) != "# Docs\n" {
		t.Errorf("ReadFile docs/index.md = %q, %v", got, err)
	}
	if got, err := fs.ReadFile(f, "docs/sub/deep.md"); err != nil || string(got) != "deep page\n" {
		t.Errorf("ReadFile docs/sub/deep.md = %q, %v", got, err)
	}
}

// Expand on an already-listed directory costs nothing — this is what lets
// cmd/ call it unconditionally rather than checking Listed itself first.
func TestGitLabExpandIsFreeOnAnAlreadyListedDirectory(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "b-idx", "name": "index.md", "type": "blob", "path": "docs/index.md"},
		})
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	// "." lists the whole repository recursively in one call — GitLab has
	// no truncation to fall back from, so this single request already
	// covers docs.
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !f.Listed("docs") {
		t.Fatalf("docs was not listed by Open(\".\") — test setup is wrong")
	}

	before := requests
	if err := g.Expand(context.Background(), f, []string{"docs"}); err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if requests != before {
		t.Errorf("Expand made %d requests against an already-listed directory, want 0", requests-before)
	}
}

// gitlabTreeServer serves a repository holding exactly files — slash paths,
// every parent directory implied — through GitLab's tree endpoint, the way
// GitLab 17.7 and later answer it: full paths in every row, a directory's
// immediate children unless recursive=true, and 404 for a path that is not
// a directory. asked returns each listing served so far, in order, as the
// path listed ("." for the root) with " (recursive)" appended when it was,
// so a test can pin exactly what the adapter asked for.
func gitlabTreeServer(t *testing.T, files ...string) (srv *httptest.Server, asked func() []string) {
	t.Helper()
	dirs := map[string]bool{".": true}
	for _, f := range files {
		for d := path.Dir(f); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	var mu sync.Mutex
	var log []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/projects/group%2Fsub%2Fbilling/repository/tree" {
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		dir := r.URL.Query().Get("path")
		if dir == "" {
			dir = "."
		}
		recursive := r.URL.Query().Get("recursive") == "true"
		mu.Lock()
		if recursive {
			log = append(log, dir+" (recursive)")
		} else {
			log = append(log, dir)
		}
		mu.Unlock()
		if !dirs[dir] {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"404 Tree Not Found"}`))
			return
		}
		under := func(p string) bool {
			if recursive {
				return dir == "." || strings.HasPrefix(p, dir+"/")
			}
			return path.Dir(p) == dir
		}
		rows := []map[string]any{}
		for d := range dirs {
			if d != "." && under(d) {
				rows = append(rows, map[string]any{"id": "t-" + d, "name": path.Base(d), "type": "tree", "path": d})
			}
		}
		for _, f := range files {
			if under(f) {
				rows = append(rows, map[string]any{"id": gitBlobSHA([]byte(f)), "name": path.Base(f), "type": "blob", "path": f})
			}
		}
		json.NewEncoder(w).Encode(rows)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), log...)
	}
}

// Ruling R45. Open at a non-root prefix used to leave the root marked listed
// with nothing in it, so a root-level file read as fs.ErrNotExist — the
// false answer ErrNotListed exists to prevent. The root is now listed because
// something listed it.
func TestGitLabOpenListsTheRootAtANonRootPrefix(t *testing.T) {
	srv, asked := gitlabTreeServer(t, "RUNBOOK.md", "services/api/service.yaml")
	g := newTestGitLab(t, srv, "main")

	f, err := g.Open(context.Background(), []string{"services/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fs.Stat(f, "RUNBOOK.md"); err != nil {
		t.Errorf("Stat(RUNBOOK.md) = %v, want the root-level file", err)
	}
	if _, err := fs.Stat(f, "nope.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(nope.md) = %v, want fs.ErrNotExist from the root listing", err)
	}
	if diff := cmp.Diff([]string{".", "services (recursive)"}, asked()); diff != "" {
		t.Errorf("listings mismatch (-want +got):\n%s", diff)
	}
}

// A prefix the root listing does not show is absent and costs nothing: no
// request, and no failure. On GitLab 17.7 and later a listing of it answers
// 404, which used to fail the whole repository (finding N1). GitHub and
// discover.Find have always treated a pattern that matches nothing as not an
// error.
func TestGitLabOpenSkipsAPrefixTheRootDoesNotShow(t *testing.T) {
	srv, asked := gitlabTreeServer(t, "services/api/service.yaml")
	g := newTestGitLab(t, srv, "main")

	f, err := g.Open(context.Background(), []string{"services/*", "workers/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fs.Stat(f, "workers/billing/service.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat under the absent prefix = %v, want fs.ErrNotExist", err)
	}
	if diff := cmp.Diff([]string{".", "services (recursive)"}, asked()); diff != "" {
		t.Errorf("listings mismatch (-want +got):\n%s", diff)
	}
}

// A nested prefix is reached the way GitHub's descent reaches one: each
// ancestor listed one level deep, and the prefix listed whole once its
// parent shows it. Prefixes go shallowest first, so services/api, which a
// recursive listing of services already covers, costs nothing more.
func TestGitLabOpenReachesANestedPrefixShallowestFirst(t *testing.T) {
	srv, asked := gitlabTreeServer(t,
		"apps/team-a/api/service.yaml", "apps/team-b/web/service.yaml", "services/api/service.yaml")
	g := newTestGitLab(t, srv, "main")

	f, err := g.Open(context.Background(), []string{"services/api/*", "apps/team-a/*", "services/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fs.Stat(f, "apps/team-a/api/service.yaml"); err != nil {
		t.Errorf("Stat under the nested prefix: %v", err)
	}
	// apps/team-b is in apps' one-level listing and was never descended into:
	// that is "never looked", not "absent".
	if _, err := fs.Stat(f, "apps/team-b/web/service.yaml"); !errors.Is(err, ErrNotListed) {
		t.Errorf("Stat beside the nested prefix = %v, want ErrNotListed", err)
	}
	want := []string{".", "services (recursive)", "apps", "apps/team-a (recursive)"}
	if diff := cmp.Diff(want, asked()); diff != "" {
		t.Errorf("listings mismatch (-want +got):\n%s", diff)
	}
}

// Expand descends the same way, and a directory its parent's listing does
// not show reads as absent, with no request for it. This is the case the
// spec traces for R45: listing the root alone would have turned
// docs/runbooks, under a docs that exists, from "does not exist" into "never
// looked". It replaces TestGitLabExpandTreatsA404AsAbsent, which proved
// absence with a 404 that GitLab also sends when Gitaly is down.
func TestGitLabExpandProvesAbsenceWithAListing(t *testing.T) {
	srv, asked := gitlabTreeServer(t, "docs/index.md", "services/api/service.yaml")
	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"services/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := g.Expand(context.Background(), f, []string{"docs/runbooks", "missing"}); err != nil {
		t.Fatalf("Expand: %v", err)
	}

	for _, p := range []string{"docs/runbooks/api.md", "missing/index.md"} {
		if _, err := fs.Stat(f, p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%s) = %v, want fs.ErrNotExist", p, err)
		}
	}
	// docs is listed one level deep to learn it holds no runbooks. Nothing
	// asks for docs/runbooks or missing, which no listing showed.
	if diff := cmp.Diff([]string{".", "services (recursive)", "docs"}, asked()); diff != "" {
		t.Errorf("listings mismatch (-want +got):\n%s", diff)
	}
}

// Because reach lists ancestors one level deep, "listed" no longer means
// "everything beneath it is known". Expand of a directory Open only listed as
// an ancestor must still list all of it: spec.docs names a tree, and
// contentSet walks every page in it.
func TestGitLabExpandListsAllOfADirectoryTheDescentOnlyListedOneLevelOf(t *testing.T) {
	srv, asked := gitlabTreeServer(t,
		"apps/team-a/api/service.yaml", "apps/guide/index.md", "apps/guide/deep/page.md")
	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"apps/team-a/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := g.Expand(context.Background(), f, []string{"apps"}); err != nil {
		t.Fatalf("Expand: %v", err)
	}

	if _, err := fs.Stat(f, "apps/guide/deep/page.md"); err != nil {
		t.Errorf("Stat two levels below the expanded directory: %v", err)
	}
	want := []string{".", "apps", "apps/team-a (recursive)", "apps (recursive)"}
	if diff := cmp.Diff(want, asked()); diff != "" {
		t.Errorf("listings mismatch (-want +got):\n%s", diff)
	}
}

// A 404 from GitLab is not evidence that a directory is absent: GitLab also
// answers it when Gitaly is down, and for a repository with no commits.
// landsraad only asks for a directory some listing has shown, so a 404 for
// one means the listing is not what landsraad believes, and the repository
// fails instead of reading as a repository with nothing in it.
func TestGitLabA404ForAListedDirectoryFailsTheRepository(t *testing.T) {
	root := []map[string]any{
		{"id": "t-services", "name": "services", "type": "tree", "path": "services"},
		{"id": "t-docs", "name": "docs", "type": "tree", "path": "docs"},
	}
	for _, tt := range []struct {
		name   string
		gone   string // the path query that answers 404; "" is the root
		expand []string
	}{
		{name: "the root listing", gone: ""},
		{name: "a directory the root listing showed", gone: "docs", expand: []string{"docs"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Query().Get("path") {
				case tt.gone:
					w.WriteHeader(http.StatusNotFound)
				case "":
					json.NewEncoder(w).Encode(root)
				default:
					json.NewEncoder(w).Encode([]map[string]any{})
				}
			}))
			t.Cleanup(srv.Close)
			g := newTestGitLab(t, srv, "main")

			f, err := g.Open(context.Background(), []string{"services/*"})
			if err == nil && tt.expand != nil {
				err = g.Expand(context.Background(), f, tt.expand)
			}
			if !IsNotFound(err) {
				t.Errorf("err = %v, want the 404 returned as a failure", err)
			}
		})
	}
}

// A non-recursive listing only proves what it enumerated one level down
// from dir. GitLab's documented contract is that every row is an immediate
// child, but a host that violates it and returns a deeper row must not get
// to mark that row's directory listed — nothing here actually enumerated
// it, and trusting the row would turn "I cannot say" into a false "it does
// not exist" (ruling R45).
func TestGitLabNonRecursiveListingIgnoresADeeperRowAndDoesNotMarkItListed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rows := []map[string]any{
			{"id": "t-apps", "name": "apps", "type": "tree", "path": "apps"},
			// A host violating the non-recursive contract: two levels below
			// ".", not an immediate child of it.
			{"id": gitBlobSHA([]byte("rogue")), "name": "rogue.md", "type": "blob", "path": "apps/team-a/rogue.md"},
		}
		json.NewEncoder(w).Encode(rows)
	}))
	t.Cleanup(srv.Close)
	g := newTestGitLab(t, srv, "main")

	f := NewFS()
	if err := g.list(context.Background(), f, "main", ".", false); err != nil {
		t.Fatalf("list: %v", err)
	}

	if f.Listed("apps/team-a") {
		t.Error("the deeper row's directory must not be marked listed — nothing here enumerated it")
	}
	if _, err := fs.Stat(f, "apps/team-a/other.md"); !errors.Is(err, ErrNotListed) {
		t.Errorf("Stat beneath the not-earned directory = %v, want ErrNotListed", err)
	}
}

// When two methods are called concurrently on the same *GitLab with an
// unset ref, they must not race on the ref field. This is the same
// regression a prior review caught on GitHub — resolveRef must return the
// ref rather than callers reading g.ref directly.
func TestGitLabConcurrentRefAccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling":
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/tree":
			json.NewEncoder(w).Encode([]map[string]any{})
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/commits":
			json.NewEncoder(w).Encode([]map[string]any{
				{"committed_date": "2026-08-01T09:30:00Z"},
			})
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "") // no ref configured, will race on resolution

	var wg sync.WaitGroup
	var errOpen, errLastEdit error

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errOpen = g.Open(context.Background(), nil)
	}()
	go func() {
		defer wg.Done()
		_, _, errLastEdit = g.LastEdit(context.Background(), "file.txt")
	}()

	wg.Wait()

	if errOpen != nil {
		t.Errorf("Open: %v", errOpen)
	}
	if errLastEdit != nil {
		t.Errorf("LastEdit: %v", errLastEdit)
	}
}

func TestGitLabBaseURL(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"https://gitlab.com/group/project", "https://gitlab.com/api/v4"},
		{"https://gl.internal/group/project", "https://gl.internal/api/v4"},
	} {
		r, err := ParseRepo("n", tt.url, "")
		if err != nil {
			t.Fatalf("ParseRepo: %v", err)
		}
		if got := GitLabBaseURL(r); got != tt.want {
			t.Errorf("GitLabBaseURL(%s) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
