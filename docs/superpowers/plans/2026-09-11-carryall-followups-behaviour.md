# Carryall follow-ups: behaviour — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every answer landsraad gives about a user's repository honest and consistent: errors it used to swallow are reported, a fetched GitLab repository's listing proves what it claims, and one mistake gets one exit code, one check id and one location wherever it is reported.

**Architecture:** Rulings R36–R45 of the spec, in its order. R40 lands first, because it turns a planner bug into a loud error. Then R45 makes every `listed` flag in `*fetch.FS` one that a real listing earned. Then the user-visible rulings follow. Each task is test-first against the smallest seam that shows the behaviour — a `*fetch.FS`, an `httptest` host, `Validate`/`Build` in memory — and ends green.

**Tech Stack:** Go 1.23, go-task, cobra, `github.com/google/go-cmp`, `net/http/httptest`, `testing/fstest`.

**Spec:** `docs/superpowers/specs/2026-09-11-carryall-followups-design.md`. Read it first. Each task names the ruling it implements, and the spec says why.

## Global Constraints

- No non-test file under `internal/` imports `os` or any `os/*` package. Only `cmd/` touches the filesystem. (hook: `no-os-in-internal.py`)
- No `sync.Once` and no `init()` below `cmd/`. (hook: `no-package-state.py`)
- A test asserts a diagnostic's message, hint and check id as **exact strings**, never with `strings.Contains`. (hook: `exact-message-tests.py`)
- Only `internal/fetch` imports `net/http` or bare `net`. (hook: `no-network-in-stages.py`)
- gofmt-clean and vet-clean. `task ci` runs `task lint` and `task test`, and `task test` runs the suite under `-race`.
- Test-first. The failing test is written and seen to fail before the fix.
- Build, test and lint through `task`, never `make`. A single test is `go test ./<pkg>/ -run '<Name>' -v`.
- Commits: `git add` each file by name, never `git add .`. No AI attribution lines in commit messages. Work happens on the branch `carryall-followups`.
- Every check id, message and hint below is copied verbatim from the spec. Do not reword one without changing the spec first.

## File structure

| File | What changes | Task |
|---|---|---|
| `internal/scorecard/hermetic.go` | `unreadable` becomes the exported `Unreadable`, and gains an `ErrNotListed` branch | 1 |
| `internal/scorecard/ingest.go` | `ingestRepo` reports `ReadDir` and `ReadFile` errors | 1 |
| `cmd/landsraad/validate.go` | `validateCheckResults` reports them too (1); a `discover.Find` error becomes a diagnostic (5); `--satellite` (6); the `missing-teams` wording (7); `localRepoName` (11) | 1, 5, 6, 7, 11 |
| `cmd/landsraad/fakehost_test.go` | the fake host learns GitLab's tree, blob and project endpoints | 2 |
| `internal/fetch/gitlab.go` | `listDir`; `Open` and `Expand` descend from a real root listing; a 404 is never evidence | 3 |
| `internal/fetch/fs.go` | `NewFS` marks nothing; `FromEntries` marks `"."`; the `lookup` comment | 4 |
| `cmd/landsraad/repos.go` | the `docsDirs` comment (4); `repoDefaultPatternsNote`'s `Repo` (9) | 4, 9 |
| `internal/discover/discover.go` | exported `CheckPattern`, shared with `repos.yaml` loading | 5 |
| `internal/config/repos.go` | `repos-path`, and rejected patterns dropped | 5 |
| `cmd/landsraad/build.go` | exit 2 for `repos.yaml` diagnostics (5); `ShowRepo` (9); the fetch-failure line (10) | 5, 9, 10 |
| `cmd/landsraad/gen.go` | `missing-teams` (7); `teams.yaml` read after an all-parse failure (8); `parseRepo`'s `Repo` (9) | 7, 8, 9 |
| `internal/diag/format.go` | `Text.ShowRepo` | 9 |
| `internal/fetch/client.go` | `ClientOptions.Now`, and no retry before `RateReset` | 12 |
| `README.md` | the exit-code rule (5); `--satellite` (6) | 5, 6 |

---

### Task 1: `.landsraad/checks` errors are reported, not swallowed (R40)

**Files:**
- Modify: `internal/scorecard/hermetic.go`: `unreadable` becomes `Unreadable` and gains an `ErrNotListed` branch. Update its two callers in `runbookPresent` and `alertsParse`.
- Modify: `internal/scorecard/ingest.go`: the `fs.ReadDir` and `fs.ReadFile` error branches of `ingestRepo`, and add the `"errors"` import.
- Modify: `cmd/landsraad/validate.go`: the same two branches of `validateCheckResults`, and add the `"errors"` import.
- Test: `internal/scorecard/hermetic_test.go`, `internal/scorecard/ingest_test.go`, `cmd/landsraad/validate_test.go`

**Interfaces:**
- Produces: `func Unreadable(p string, err error) string` in package `scorecard`. It is the one wording for a path a stage could not read, shared by the hermetic checks, `Ingest` and `validate`. Task 4 relies on it to make an `ErrNotListed` loud.

- [ ] **Step 1: Write the failing test for `Unreadable`'s new branch**

Add to `internal/scorecard/hermetic_test.go`, directly after `TestUnfetchedFilesReadAsALandsraadBugNotAMissingFile`:

```go
// ErrNotListed is the same kind of fact as ErrNotFetched: a gap in what
// landsraad asked the host for, never evidence about the repository.
// Unreadable used to word only ErrNotFetched as a landsraad bug. This one
// fell through to "cannot read X: directory was never listed", which reads
// as a problem with the user's files.
func TestUnlistedFilesReadAsALandsraadBugNotAMissingFile(t *testing.T) {
	// The root listing saw services/, and nothing ever listed inside it.
	remote := fetch.NewFS()
	remote.AddDir(".", []fetch.Entry{{Path: "services", Dir: true}})
	env := Env{Sources: catalog.SingleSource("", remote), Now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}

	e := svc("api")
	e.Spec.Runbook = "services/api/runbook.md"
	got := run(t, "runbook-present", e, env)

	if got.Status != StatusError {
		t.Errorf("Status = %q, want %q", got.Status, StatusError)
	}
	want := "services/api/runbook.md was never listed, so landsraad cannot read it; " +
		"this is a landsraad bug, not a problem with your catalog"
	if got.Detail != want {
		t.Errorf("Detail = %q, want %q", got.Detail, want)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/scorecard/ -run 'TestUnlistedFilesReadAsALandsraadBugNotAMissingFile' -v`
Expected: FAIL, with `Detail = "cannot read services/api/runbook.md: open services/api/runbook.md: directory was never listed", want "services/api/runbook.md was never listed, …"`

- [ ] **Step 3: Implement `Unreadable`**

In `internal/scorecard/hermetic.go`, replace the whole `unreadable` function and its doc comment with:

```go
// Unreadable says why a file or directory a stage needed could not be read.
//
// sparsefs.ErrNotFetched means the file is sitting in the repository and cmd/'s
// content planner never asked the host for its bytes. The sentinel exists
// precisely so this is not reported as a missing file — the package comment
// says a missing-file diagnostic "would send somebody to look for a file
// that is sitting in their repository" — and then every consumer dropped the
// error and said "cannot read X", which sends them exactly there.
//
// sparsefs.ErrNotListed is the same kind of fact one level up: the path sits
// below a directory the planner never asked the host to list. That is equally
// a gap in what landsraad fetched and equally no evidence about the user's
// files, so it gets the same wording rather than falling through to the
// generic branch.
//
// Everything else gets the error itself. "cannot read X" collapsed a
// permission problem, an EISDIR and a truncated read into one sentence that
// says nothing about any of them.
//
// Exported because cmd/'s validate reads .landsraad/checks too, and a second
// copy of this wording is how the two would drift apart.
func Unreadable(p string, err error) string {
	if errors.Is(err, sparsefs.ErrNotFetched) {
		return fmt.Sprintf("%s is in the repository but its content was never fetched; "+
			"this is a landsraad bug, not a problem with your catalog", p)
	}
	if errors.Is(err, sparsefs.ErrNotListed) {
		return fmt.Sprintf("%s was never listed, so landsraad cannot read it; "+
			"this is a landsraad bug, not a problem with your catalog", p)
	}
	return fmt.Sprintf("cannot read %s: %v", p, err)
}
```

Then update both callers in the same file. `runbookPresent`'s `Detail: unreadable(e.Spec.Runbook, err)` becomes `Detail: Unreadable(e.Spec.Runbook, err)`, and `alertsParse`'s `Detail: unreadable(e.Spec.Alerts, err)` becomes `Detail: Unreadable(e.Spec.Alerts, err)`.

- [ ] **Step 4: Run the hermetic tests and watch them pass**

Run: `go test ./internal/scorecard/ -run 'TestUnlistedFilesReadAsALandsraadBugNotAMissingFile|TestUnfetchedFilesReadAsALandsraadBugNotAMissingFile' -v`
Expected: PASS for both.

- [ ] **Step 5: Write the failing `Ingest` tests**

In `internal/scorecard/ingest_test.go`, extend the imports to:

```go
import (
	"fmt"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/fetch"
)
```

Add these after `TestIngestWithNoChecksDirectoryIsNotAnError`:

```go
// failPathFS is a MapFS on which one path cannot be read. ReadDir and
// ReadFile of it fail with a permission error, the shape os.DirFS gives for
// a path the build user cannot read, without depending on the test
// process's own permissions. Compare render's failFS.
type failPathFS struct {
	fstest.MapFS
	path string
}

func (f failPathFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadDir(name)
}

func (f failPathFS) ReadFile(name string) ([]byte, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadFile(name)
}

// A .landsraad/checks that exists but cannot be listed is a different fact
// from no directory at all. ingestRepo used to treat both as "no results",
// so every result the repository reported vanished from the scorecard
// without a word.
func TestIngestReportsAnUnreadableChecksDirectory(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	var c diag.Collector

	Ingest(catalog.SingleSource("platform", failPathFS{MapFS: fstest.MapFS{}, path: ChecksDir}), cat, 14, now, &c)

	want := []diag.Diagnostic{{
		Severity: diag.SevError, Repo: "platform", File: ".landsraad/checks", Line: 1,
		Check:   "checks-unreadable",
		Message: "cannot read .landsraad/checks: readdir .landsraad/checks: permission denied",
	}}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// ErrNotListed here means cmd/'s planner never listed a directory it always
// expands (docsDirs seeds ChecksDir), so it is a landsraad bug. It has to be
// loud: once every listed flag is earned (R45), a planner mistake would
// otherwise surface as exactly the silence above.
func TestIngestReportsAnUnlistedChecksDirectoryAsALandsraadBug(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	remote := fetch.NewFS()
	// The root listing saw .landsraad, and nothing ever listed inside it.
	remote.AddDir(".", []fetch.Entry{{Path: ".landsraad", Dir: true}})
	var c diag.Collector

	Ingest(catalog.SingleSource("edge-gateway", remote), cat, 14, now, &c)

	want := []diag.Diagnostic{{
		Severity: diag.SevError, Repo: "edge-gateway", File: ".landsraad/checks", Line: 1,
		Check: "checks-unreadable",
		Message: ".landsraad/checks was never listed, so landsraad cannot read it; " +
			"this is a landsraad bug, not a problem with your catalog",
	}}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// A results file that was listed but whose bytes were never fetched. The
// ReadFile branch used to say "cannot read .landsraad/checks/scan.yaml" and
// stop there, which sends somebody to look at a file that is fine.
func TestIngestReportsAnUnfetchedResultsFileAsALandsraadBug(t *testing.T) {
	cat := catalogOf(t, svc("api"))
	remote := fetch.NewFS()
	remote.AddDir(".", []fetch.Entry{{Path: ".landsraad", Dir: true}})
	remote.AddDir(".landsraad", []fetch.Entry{{Path: ".landsraad/checks", Dir: true}})
	remote.AddDir(".landsraad/checks", []fetch.Entry{
		{Path: ".landsraad/checks/scan.yaml", SHA: "0123456789abcdef0123456789abcdef01234567", Size: 40},
	})
	var c diag.Collector

	Ingest(catalog.SingleSource("edge-gateway", remote), cat, 14, now, &c)

	want := []diag.Diagnostic{{
		Severity: diag.SevError, Repo: "edge-gateway", File: ".landsraad/checks/scan.yaml", Line: 1,
		Check: "checks-unreadable",
		Message: ".landsraad/checks/scan.yaml is in the repository but its content was never fetched; " +
			"this is a landsraad bug, not a problem with your catalog",
	}}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 6: Run them and watch them fail**

Run: `go test ./internal/scorecard/ -run 'TestIngestReports(AnUnreadableChecksDirectory|AnUnlistedChecksDirectoryAsALandsraadBug|AnUnfetchedResultsFileAsALandsraadBug)' -v`
Expected: FAIL, all three.
- The two directory tests show a `-want` diagnostic and no `+got` line, because the error was swallowed.
- The unfetched test shows `+got` with `Message: "cannot read .landsraad/checks/scan.yaml"`.

- [ ] **Step 7: Implement the two branches in `ingestRepo`**

In `internal/scorecard/ingest.go`, add `"errors"` to the standard-library imports. Replace the opening of `ingestRepo`:

```go
	entries, err := fs.ReadDir(fsys, ChecksDir)
	if err != nil {
		return
	}
```

with:

```go
	entries, err := fs.ReadDir(fsys, ChecksDir)
	if errors.Is(err, fs.ErrNotExist) {
		// No directory is not a problem: most repositories report no
		// external results. Only this answer means that. A directory that
		// exists but cannot be listed used to land here too, and dropped
		// every result the repository reported without a word.
		return
	}
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: repo, File: ChecksDir, Line: 1,
			Check:   "checks-unreadable",
			Message: Unreadable(ChecksDir, err),
		})
		return
	}
```

In the per-file loop, replace the `fs.ReadFile` branch's

```go
				Message: fmt.Sprintf("cannot read %s", path),
```

with

```go
				Message: Unreadable(path, err),
```

- [ ] **Step 8: Run the scorecard package and watch it pass**

Run: `go test ./internal/scorecard/ -v -run 'TestIngest|TestUnlisted|TestUnfetched'`
Expected: PASS, including the untouched `TestIngestWithNoChecksDirectoryIsNotAnError`.
Then run: `go test ./internal/scorecard/`
Expected: `ok`.

- [ ] **Step 9: Write the failing `validate` test**

In `cmd/landsraad/validate_test.go`, add `"io/fs"` and `"github.com/google/go-cmp/cmp"` to the imports. Add this after `TestValidateAcceptsAWellFormedCheckResultsFile`:

```go
// failPathFS is a MapFS on which one path cannot be read: ReadDir and
// ReadFile of it fail with a permission error, the shape os.DirFS gives
// without depending on the test process's own permissions.
type failPathFS struct {
	fstest.MapFS
	path string
}

func (f failPathFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadDir(name)
}

func (f failPathFS) ReadFile(name string) ([]byte, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadFile(name)
}

// A .landsraad/checks that cannot be read used to validate clean.
// validateCheckResults treated every ReadDir error as "no directory", so a
// PR could break the directory and still ship green, and a results file it
// could not read was reported without saying why.
func TestValidateReportsUnreadableCheckResults(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		want       diag.Diagnostic
	}{
		{
			name: "directory", path: ".landsraad/checks",
			want: diag.Diagnostic{
				Severity: diag.SevError, File: ".landsraad/checks", Line: 1,
				Check:   "checks-unreadable",
				Message: "cannot read .landsraad/checks: readdir .landsraad/checks: permission denied",
			},
		},
		{
			name: "file", path: ".landsraad/checks/scan.yaml",
			want: diag.Diagnostic{
				Severity: diag.SevError, File: ".landsraad/checks/scan.yaml", Line: 1,
				Check:   "checks-unreadable",
				Message: "cannot read .landsraad/checks/scan.yaml: open .landsraad/checks/scan.yaml: permission denied",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := genFS()
			files[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
				"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/x\ngeneratedAt: 2026-09-08T14:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")}

			var out, errOut bytes.Buffer
			code := Validate(failPathFS{MapFS: files, path: tt.path}, &out, &errOut, diag.JSON{})
			if code != exitValidation {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
			}
			var ds []diag.Diagnostic
			if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
				t.Fatalf("out is not diagnostics JSON: %v\n%s", err, out.String())
			}
			if diff := cmp.Diff([]diag.Diagnostic{tt.want}, ds); diff != "" {
				t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
```

- [ ] **Step 10: Run it and watch it fail**

Run: `go test ./cmd/landsraad/ -run 'TestValidateReportsUnreadableCheckResults' -v`
Expected: FAIL.
- `directory`: `exit = 0, want 2`.
- `file`: the diff shows `+got` `Message: "cannot read .landsraad/checks/scan.yaml"`.

- [ ] **Step 11: Implement the two branches in `validateCheckResults`**

In `cmd/landsraad/validate.go`, add `"errors"` to the imports. Replace the opening of `validateCheckResults`:

```go
	entries, err := fs.ReadDir(fsys, scorecard.ChecksDir)
	if err != nil {
		// No directory is not a problem: most repositories report no external
		// results.
		return
	}
```

with:

```go
	entries, err := fs.ReadDir(fsys, scorecard.ChecksDir)
	if errors.Is(err, fs.ErrNotExist) {
		// No directory is not a problem: most repositories report no external
		// results. Only this answer means that; a directory that exists and
		// cannot be listed is reported below, not validated as empty.
		return
	}
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: scorecard.ChecksDir, Line: 1,
			Check:   "checks-unreadable",
			Message: scorecard.Unreadable(scorecard.ChecksDir, err),
		})
		return
	}
```

In the per-file loop, replace

```go
				Message: fmt.Sprintf("cannot read %s", path),
```

with

```go
				Message: scorecard.Unreadable(path, err),
```

- [ ] **Step 12: Run everything this task touched and watch it pass**

Run: `go test ./cmd/landsraad/ -run 'TestValidate' -v`
Expected: PASS, including `TestValidateReportsUnreadableCheckResults` and the three existing check-results tests.

Run: `task ci`
Expected: lint clean, and every package `ok` under `-race`.

- [ ] **Step 13: Commit**

```bash
git add internal/scorecard/hermetic.go internal/scorecard/hermetic_test.go \
  internal/scorecard/ingest.go internal/scorecard/ingest_test.go \
  cmd/landsraad/validate.go cmd/landsraad/validate_test.go
git commit -m "fix: an unreadable .landsraad/checks is reported, not read as empty"
```

---

### Task 2: `cmd/`'s fake host speaks GitLab

Test infrastructure for Task 3's end-to-end proof. No test has ever driven `openRepos` and `Build` through the GitLab adapter, because `fakehost_test.go` speaks only GitHub.

**Files:**
- Modify: `cmd/landsraad/fakehost_test.go`: extract `readFixtureRepo` from `fakeGitHubHost` and add `fakeGitLab`.
- Modify: `cmd/landsraad/integration_test.go`: `remotePlatformRoot` delegates to a new `remotePlatformRootOn`, and there's a new baseline test.

**Interfaces:**
- Produces:
  - `func fakeGitLab(t *testing.T, dir string) *httptest.Server`, a TLS server answering GitLab's tree and raw-blob endpoints for `dir`.
  - `func remotePlatformRootOn(t *testing.T, kind, host, patterns string) string`, which is `remotePlatformRoot` with the remote entry's `host:` set to `kind`.
  - `type fixtureRepo` and `func readFixtureRepo(t *testing.T, dir string) fixtureRepo`.

- [ ] **Step 1: Extract `readFixtureRepo` and keep GitHub green**

In `cmd/landsraad/fakehost_test.go`, add this above `multirepoRoot`:

```go
// fixtureRepo is a directory described the way a git host describes a
// repository: every file with the blob sha git would give it, and every
// directory. Both fake hosts serve one.
type fixtureRepo struct {
	blobs map[string]fixtureBlob // path -> blob
	bySHA map[string][]byte      // sha  -> content
	dirs  []string
}

type fixtureBlob struct {
	sha  string
	data []byte
}

// readFixtureRepo walks dir into a fixtureRepo, computing each blob sha the
// way git does, so fetchBlobs' verification runs for real against either
// fake host.
func readFixtureRepo(t *testing.T, dir string) fixtureRepo {
	t.Helper()
	repo := fixtureRepo{blobs: map[string]fixtureBlob{}, bySHA: map[string][]byte{}}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			repo.dirs = append(repo.dirs, rel)
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha1.New()
		fmt.Fprintf(h, "blob %d\x00", len(data))
		h.Write(data)
		sha := hex.EncodeToString(h.Sum(nil))
		repo.blobs[rel] = fixtureBlob{sha, data}
		repo.bySHA[sha] = data
		return nil
	})
	if err != nil {
		t.Fatalf("building the fake host from %s: %v", dir, err)
	}
	return repo
}
```

In `fakeGitHubHost`, delete everything from `type blob struct {` through the `if err != nil { t.Fatalf("building the fake host …") }` that follows the walk. Put these two lines in its place:

```go
	repo := readFixtureRepo(t, dir)
	blobs, bySHA, dirs := repo.blobs, repo.bySHA, repo.dirs
```

The rest of `fakeGitHubHost` compiles unchanged, because `fixtureBlob` has the same `sha` and `data` fields its closures read.

- [ ] **Step 2: Run the GitHub remote tests and watch them pass**

Run: `go test ./cmd/landsraad/ -run 'TestBuildMergesALocalAndARemoteRepository|TestBuildReportsADanglingRemoteRunbookInsteadOfDroppingTheRepository|TestBuildFetchesARunbookOutsideTheConfiguredPaths' -v`
Expected: PASS, all three. The extraction changed nothing they depend on.

- [ ] **Step 3: Add `fakeGitLab`**

In `cmd/landsraad/fakehost_test.go`, add after `readFixtureRepo`:

```go
// fakeGitLab serves a directory as if it were a GitLab repository, through
// the two endpoints a build with a configured ref calls: the tree listing
// and raw blobs. The project and commit endpoints are not served; a test
// that needs resolveRef or docs-fresh adds them, and until then a request
// for either fails the test rather than being answered with a guess.
//
// A listing for a path that is not a directory answers 404, as GitLab has
// since 17.7 (earlier versions answered 200 and []). Ruling R45 means the
// adapter never asks for a directory no listing has shown, so it must not
// care which; this fake takes the stricter of the two.
//
// Every listing fits one page. Pagination is TestGitLabFollowsEveryPage's
// subject, not this fixture's.
func fakeGitLab(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	repo := readFixtureRepo(t, dir)
	isDir := map[string]bool{".": true}
	for _, d := range repo.dirs {
		isDir[d] = true
	}

	// rows is one tree listing in GitLab's shape: full paths from the
	// repository root, the opposite of GitHub's relative ones.
	rows := func(parent string, recursive bool) []map[string]any {
		under := func(p string) bool {
			if recursive {
				return parent == "." || strings.HasPrefix(p, parent+"/")
			}
			return path.Dir(p) == parent
		}
		out := []map[string]any{}
		for _, d := range repo.dirs {
			if under(d) {
				out = append(out, map[string]any{"id": "t-" + d, "name": path.Base(d), "type": "tree", "path": d})
			}
		}
		for p, b := range repo.blobs {
			if under(p) {
				out = append(out, map[string]any{"id": b.sha, "name": path.Base(p), "type": "blob", "path": p})
			}
		}
		return out
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/repository/tree"):
			parent := r.URL.Query().Get("path")
			if parent == "" {
				parent = "."
			}
			if !isDir[parent] {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"message":"404 Tree Not Found"}`))
				return
			}
			json.NewEncoder(w).Encode(rows(parent, r.URL.Query().Get("recursive") == "true"))
		case strings.Contains(r.URL.Path, "/repository/blobs/"):
			rest := r.URL.Path[strings.Index(r.URL.Path, "/repository/blobs/")+len("/repository/blobs/"):]
			data, ok := repo.bySHA[strings.TrimSuffix(rest, "/raw")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(data)
		default:
			t.Errorf("fake GitLab got an unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
```

- [ ] **Step 4: Let the platform fixture name either adapter, and add the baseline test**

In `cmd/landsraad/integration_test.go`, replace the opening of `remotePlatformRoot`:

```go
func remotePlatformRoot(t *testing.T, host, patterns string) string {
	t.Helper()
	return materialize(t, map[string]string{
```

with:

```go
func remotePlatformRoot(t *testing.T, host, patterns string) string {
	t.Helper()
	return remotePlatformRootOn(t, "github", host, patterns)
}

// remotePlatformRootOn is remotePlatformRoot with the remote entry's host:
// named, so the same platform can stand in front of either adapter.
func remotePlatformRootOn(t *testing.T, kind, host, patterns string) string {
	t.Helper()
	return materialize(t, map[string]string{
```

In the same function's `repos.yaml` string, replace the remote entry's line

```go
			"  - url: https://" + host + "/org/edge-gateway\n    host: github\n    ref: main\n    paths: [" + patterns + "]\n",
```

with

```go
			"  - url: https://" + host + "/org/edge-gateway\n    host: " + kind + "\n    ref: main\n    paths: [" + patterns + "]\n",
```

Then append this test to the end of the file:

```go
// cmd/'s fake host used to speak only GitHub, so no test drove openRepos and
// Build through the GitLab adapter end to end. This is the baseline R45 is
// measured against: a satellite whose whole tree sits inside `paths: [.]`,
// which GitLab.Open covers with one recursive listing.
func TestBuildMergesARemoteGitLabRepository(t *testing.T) {
	remote := materialize(t, map[string]string{
		"service.yaml": `apiVersion: landsraad/v1
kind: Service
metadata:
  name: edge
  description: Edge gateway, hosted on GitLab.
  owner: team-platform
  tier: 1
  lifecycle: production
spec:
  language: go
  path: .
  runbook: RUNBOOK.md
`,
		"RUNBOOK.md": "# Edge runbook\n\nDrain the pool, then page the on-call.\n",
	})
	srv := fakeGitLab(t, remote)
	root := remotePlatformRootOn(t, "gitlab", srv.Listener.Addr().String(), ".")

	w := openRemoteWorkspace(t, root, srv)
	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("openRepos failed for %+v", got)
	}
	var errOut bytes.Buffer
	files, code := Build(os.DirFS(root), w, &errOut, BuildOptions{
		Now: testNow, Version: "test", LastEdit: noLastEdit(),
	})
	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	page, ok := byPath["entity/service/edge/runbook.html"]
	if !ok {
		t.Fatalf("the GitLab satellite's runbook was not rendered; pages: %v", sortedPaths(byPath))
	}
	if !strings.Contains(string(page), "Drain the pool") {
		t.Errorf("the rendered runbook does not carry the fetched content:\n%s", page)
	}
}
```

- [ ] **Step 5: Run the baseline and watch it pass**

Run: `go test ./cmd/landsraad/ -run 'TestBuildMergesARemoteGitLabRepository' -v`
Expected: PASS. This task adds infrastructure, and the layout it drives already works on today's code, so there is no red step. Its job is to be the known-good baseline Task 3 is measured against.

Run: `task ci`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/landsraad/fakehost_test.go cmd/landsraad/integration_test.go
git commit -m "test: cmd's fake host speaks GitLab"
```

---

### Task 3: GitLab proves every directory with a listing (R45, the adapter)

**Files:**
- Modify: `internal/fetch/gitlab.go`: `listPath` becomes `list` with a `recursive` flag. `Open` lists the root, then reaches prefixes shallowest first. There's a new `reach`, plus the helpers `showsDir` and `covered`. `Expand` uses `reach`, and its 404 tolerance goes. Add the `"io/fs"` and `"sort"` imports.
- Test: `internal/fetch/gitlab_test.go`: a new `gitlabTreeServer` fake and six tests; a root case in `TestGitLabExpandListsADirectoryOutsideAnyPattern`'s handler; `TestGitLabExpandTreatsA404AsAbsent` deleted.
- Test: `cmd/landsraad/integration_test.go`: one end-to-end test.

**Interfaces:**
- Consumes: `fakeGitLab` and `remotePlatformRootOn` (Task 2); `ancestorsOf` (`internal/fetch/githubwalk.go`, unchanged).
- Produces: after `GitLab.Open`, the root of the returned `*FS` is listed because a listing covered it. Task 4 relies on this, so it can stop `NewFS` marking `"."`.

This task deliberately lists the root in `Open` *unconditionally*, not "if not already listed". Until Task 4, `NewFS` still marks `"."` listed, and a check on that flag would skip the very listing this task adds.

- [ ] **Step 1: Write the failing end-to-end test**

Append to `cmd/landsraad/integration_test.go`:

```go
// Ruling R45, end to end. A GitLab satellite whose services live under
// services/ and whose runbook sits at its root. GitLab.Open listed only
// services/, and NewFS had marked the root listed anyway, so RUNBOOK.md read
// as fs.ErrNotExist: contentSet skipped it, and CheckFiles reported
// missing-file for a runbook sitting in the repository.
func TestBuildFetchesARootLevelRunbookFromAGitLabSatellite(t *testing.T) {
	remote := materialize(t, map[string]string{
		"services/edge/service.yaml": `apiVersion: landsraad/v1
kind: Service
metadata:
  name: edge
  description: Edge gateway, whose runbook is at the repository root.
  owner: team-platform
  tier: 1
  lifecycle: production
spec:
  language: go
  path: services/edge
  runbook: RUNBOOK.md
`,
		"RUNBOOK.md": "# Edge runbook\n\nDrain the pool, then page the on-call.\n",
	})
	srv := fakeGitLab(t, remote)
	root := remotePlatformRootOn(t, "gitlab", srv.Listener.Addr().String(), "services/*")

	w := openRemoteWorkspace(t, root, srv)
	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("openRepos failed for %+v", got)
	}
	var errOut bytes.Buffer
	files, code := Build(os.DirFS(root), w, &errOut, BuildOptions{
		Now: testNow, Version: "test", LastEdit: noLastEdit(),
	})
	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	page, ok := byPath["entity/service/edge/runbook.html"]
	if !ok {
		t.Fatalf("the root-level runbook was not rendered; pages: %v", sortedPaths(byPath))
	}
	if !strings.Contains(string(page), "Drain the pool") {
		t.Errorf("the rendered runbook does not carry the fetched content:\n%s", page)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./cmd/landsraad/ -run 'TestBuildFetchesARootLevelRunbookFromAGitLabSatellite' -v`
Expected: FAIL with

```
exit = 2, want 0; stderr:
error: services/edge/service.yaml:4 [missing-file]
  spec.runbook points at "RUNBOOK.md", which does not exist
refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth
```

This is the user-facing bug: a runbook sitting in the repository, reported as missing.

- [ ] **Step 3: Write the failing adapter tests**

In `internal/fetch/gitlab_test.go`, add `"path"` to the imports, after `"net/http/httptest"`.

In `TestGitLabExpandListsADirectoryOutsideAnyPattern`, the handler's `switch r.URL.Query().Get("path") {` gains a root case before `case "services":`:

```go
		case "":
			// The root listing ruling R45 has Open make before any prefix.
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "t-services", "name": "services", "type": "tree", "path": "services"},
				{"id": "t-docs", "name": "docs", "type": "tree", "path": "docs"},
			})
```

The same test's doc comment becomes false twice over after this task. `listPath` is renamed `list`, and it stops being the only test that sends tree rows. Replace its first paragraph

```go
// spec.docs can name a directory outside every pattern's literal prefix —
// Open only covered "services", so "docs" is discovered solely by Expand.
// The mock response gives docs a realistic nested shape: a file directly
// inside it, plus a "sub" subdirectory (a "type": "tree" row) with a file
// of its own beneath that — this is what exercises the tree-row branch in
// listPath, which no other test in this file reaches.
```

with

```go
// spec.docs can name a directory outside every pattern's literal prefix.
// Open's root listing shows that "docs" exists but lists only "services" in
// full, so what is inside "docs" is learned solely by Expand. The mock
// response gives docs a realistic nested shape: a file directly inside it,
// plus a "sub" subdirectory (a "type": "tree" row) with a file of its own
// beneath that, which drives list's tree-row branch on a recursive listing.
```

Delete `TestGitLabExpandTreatsA404AsAbsent`, together with its comment block, which begins `// A directory a service.yaml names in spec.docs but which does not exist on`. In its place, before `TestGitLabConcurrentRefAccess`'s comment, insert:

```go
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
```

- [ ] **Step 4: Run them and watch them fail**

Run: `go test ./internal/fetch/ -run 'TestGitLab' -v`
Expected: FAIL. These were observed against the unmodified adapter:
- `TestGitLabOpenListsTheRootAtANonRootPrefix`: `Stat(RUNBOOK.md) = stat RUNBOOK.md: file does not exist, want the root-level file`, plus a listings mismatch.
- `TestGitLabOpenSkipsAPrefixTheRootDoesNotShow`: `Open: GET /projects/group%2Fsub%2Fbilling/repository/tree: HTTP 404: {"message":"404 Tree Not Found"}`. This is finding N1, a whole repository failing over a pattern that matches nothing.
- `TestGitLabOpenReachesANestedPrefixShallowestFirst`, `TestGitLabExpandProvesAbsenceWithAListing`, `TestGitLabExpandListsAllOfADirectoryTheDescentOnlyListedOneLevelOf`: listings mismatch.
- `TestGitLabA404ForAListedDirectoryFailsTheRepository`: both subtests, `err = <nil>, want the 404 returned as a failure`.

`TestGitLabExpandListsADirectoryOutsideAnyPattern` still passes: its new root case is never requested yet.

- [ ] **Step 5: Implement the descent**

In `internal/fetch/gitlab.go`:

1. Add `"io/fs"` after `"fmt"` and `"sort"` after `"path"` in the imports.
2. Replace everything from the comment `// listPath lists everything under prefix, following every page.` up to (not including) the comment `// literalPrefixes reduces glob patterns`. That span holds `listPath` and the old `Open`. Replace it with:

```go
// list lists dir, following every page: its immediate children, or with
// recursive set everything beneath it.
//
// per_page is set to 100 because GitLab's default is 20, and a listing that
// stops after twenty files is a portal missing services with nothing to
// notice it by. X-Next-Page is empty on the last page, which is the
// documented end condition.
//
// A successful listing marks dir listed even when it returns no rows, and a
// recursive one marks every directory it names, as FromEntries does for a
// complete listing: the response described their whole contents.
func (g *GitLab) list(ctx context.Context, f *FS, ref, dir string, recursive bool) error {
	page := 1
	for {
		q := url.Values{
			"ref":      {ref},
			"per_page": {"100"},
			"page":     {strconv.Itoa(page)},
		}
		if recursive {
			q.Set("recursive", "true")
		}
		if dir != "." && dir != "" {
			q.Set("path", dir)
		}
		body, header, err := g.c.Get(ctx, g.project()+"/repository/tree", q, "")
		if err != nil {
			return err
		}
		var rows []glRow
		if err := json.Unmarshal(body, &rows); err != nil {
			return fmt.Errorf("cannot read the tree listing: %w", err)
		}
		byDir := map[string][]Entry{dir: nil}
		for _, r := range rows {
			switch r.Type {
			case "blob":
				byDir[path.Dir(r.Path)] = append(byDir[path.Dir(r.Path)], Entry{Path: r.Path, SHA: r.ID})
			case "tree":
				byDir[path.Dir(r.Path)] = append(byDir[path.Dir(r.Path)], Entry{Path: r.Path, Dir: true})
				if _, seen := byDir[r.Path]; recursive && !seen {
					byDir[r.Path] = nil
				}
				// "commit" is a submodule; see GitHub.Open.
			}
		}
		for d, entries := range byDir {
			f.AddDir(d, entries)
		}
		next := header.Get("X-Next-Page")
		if next == "" {
			return nil
		}
		n, err := strconv.Atoi(next)
		if err != nil {
			return fmt.Errorf("the host returned an unreadable X-Next-Page %q", next)
		}
		page = n
	}
}

// Open lists what the patterns can reach, proving each step with a listing
// (ruling R45).
//
// GitLab has no truncation flag — it simply paginates — so a hundred-thousand
// file monorepo would be a thousand requests for a listing of which
// landsraad reads a handful of directories. Bounding by prefix is the same
// economy ruling R28 buys on GitHub, taken on the cheap path rather than as
// a fallback.
//
// Every prefix is reached from a real listing of the root. Before R45 the
// root was only marked listed, by NewFS, so every root-level path read as
// fs.ErrNotExist, and a runbook sitting in the repository was reported
// missing. Prefixes go shallowest first, so a recursive listing of services
// covers services/api before anything asks for it on its own.
func (g *GitLab) Open(ctx context.Context, patterns []string) (*FS, error) {
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return nil, err
	}
	f := NewFS()
	prefixes := literalPrefixes(patterns)
	if prefixes[0] == "." {
		// literalPrefixes returns "." alone. One recursive listing of the
		// whole repository lists the root along with everything else.
		if err := g.list(ctx, f, ref, ".", true); err != nil {
			return nil, err
		}
		return f, nil
	}
	if err := g.list(ctx, f, ref, ".", false); err != nil {
		return nil, err
	}
	sort.SliceStable(prefixes, func(i, j int) bool {
		return strings.Count(prefixes[i], "/") < strings.Count(prefixes[j], "/")
	})
	for _, p := range prefixes {
		if err := g.reach(ctx, f, ref, p); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// reach makes dir and everything beneath it known, proving each step the
// only way this adapter accepts: a listing of the parent that shows it.
// Ancestors are listed one level at a time, root to parent, each skipped if
// already listed — the discipline GitHub's truncated-tree descent follows —
// and dir itself is listed recursively unless everything beneath it is
// already known.
//
// A step its parent's listing does not show is absent. reach stops there,
// with no request, and f answers fs.ErrNotExist beneath it because the
// parent is listed. Open lists the root before anything calls this.
func (g *GitLab) reach(ctx context.Context, f *FS, ref, dir string) error {
	for _, a := range ancestorsOf(dir) {
		shown, err := showsDir(f, a)
		if err != nil || !shown {
			return err
		}
		if !f.Listed(a) {
			if err := g.list(ctx, f, ref, a, false); err != nil {
				return err
			}
		}
	}
	shown, err := showsDir(f, dir)
	if err != nil || !shown || covered(f, dir) {
		return err
	}
	return g.list(ctx, f, ref, dir, true)
}

// showsDir reports whether dir's parent, which reach has already listed,
// shows dir as a directory. fs.ErrNotExist is a plain no. Any other answer
// means the parent was not listed after all: a bug in reach, not a fact
// about the repository, so it is returned as an error.
func showsDir(f *FS, dir string) (bool, error) {
	info, err := fs.Stat(f, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// covered reports whether dir and every directory beneath it are listed,
// which is what a recursive listing of dir establishes. Listed(dir) alone no
// longer says that: reach lists ancestors one level deep, so a directory can
// be listed with nothing beneath it known.
func covered(f *FS, dir string) bool {
	if !f.Listed(dir) {
		return false
	}
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	for _, e := range f.Entries() {
		if e.Dir && strings.HasPrefix(e.Path, prefix) && !f.Listed(e.Path) {
			return false
		}
	}
	return true
}

```

3. Replace `Expand`, including its doc comment, with:

```go
// Expand lists a directory no pattern prefix covered — a spec.docs, or the
// directory of a runbook or an alerts file — reaching it the way Open
// reaches a prefix.
//
// A directory no listing shows costs no request and reads as absent: its
// parent's listing is the proof. That replaced treating a 404 as absence.
// GitLab answers 404 for a missing path only from 17.7 (200 and [] before
// that), and also when Gitaly is down, so a 404 could turn an outage into
// "your runbook is missing". Now a 404 can only come back for a directory
// some listing showed, which means the listing is wrong, and it fails the
// repository like any other error.
func (g *GitLab) Expand(ctx context.Context, f *FS, dirs []string) error {
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if err := g.reach(ctx, f, ref, d); err != nil {
			return err
		}
	}
	return nil
}
```

`literalPrefixes` does not change.

- [ ] **Step 6: Run everything this task touched and watch it pass**

Run: `go test ./internal/fetch/ -run 'TestGitLab' -v`
Expected: PASS, every GitLab test, including the unchanged `TestGitLabFollowsEveryPage`, `TestGitLabExpandIsFreeOnAnAlreadyListedDirectory` and `TestGitLabConcurrentRefAccess`.

Run: `go test ./cmd/landsraad/ -run 'TestBuildFetchesARootLevelRunbookFromAGitLabSatellite|TestBuildMergesARemoteGitLabRepository' -v`
Expected: PASS, both.

Run: `task ci`
Expected: clean. This was observed with the whole suite under `-race` and `scripts/check-rules.sh` passing.

- [ ] **Step 7: Commit**

```bash
git add internal/fetch/gitlab.go internal/fetch/gitlab_test.go cmd/landsraad/integration_test.go
git commit -m "fix(fetch): GitLab proves every directory with a listing, never with a 404"
```

---

### Task 4: Only a listing marks a directory listed (R45, the filesystem)

**Files:**
- Modify: `internal/fetch/fs.go`: `NewFS` marks nothing; `FromEntries` marks `"."`; the `NewFS` doc comment and the `lookup` comment.
- Modify: `cmd/landsraad/repos.go`: the `docsDirs` comment. No code changes.
- Test: `internal/fetch/fs_test.go`

**Interfaces:**
- Consumes: Task 3's guarantee that `GitLab.Open` lists the root. GitHub's `walk` already does, through `record(f, ".", rows)`.
- Produces: every key in `*FS.listed` was earned by a listing. Nothing later depends on `NewFS` marking anything.

- [ ] **Step 1: Write the tests**

Append to `internal/fetch/fs_test.go`:

```go
// Ruling R45: a directory is listed only when a listing covered it. NewFS
// used to mark "." at construction, which is how a GitLab repository opened
// at a non-root prefix came to answer fs.ErrNotExist for every root-level
// file.
func TestNewFSListsNothing(t *testing.T) {
	f := NewFS()
	if f.Listed(".") {
		t.Error(`NewFS marked "." listed without a listing`)
	}
	if _, err := fs.Stat(f, "README.md"); !errors.Is(err, ErrNotListed) {
		t.Errorf("Stat(README.md) on an unlisted root = %v, want ErrNotListed", err)
	}
}

// A complete listing did enumerate the root, so FromEntries marks it — even
// for a repository whose every file sits at the top level, where no entry's
// parent chain would otherwise reach ".".
func TestFromEntriesListsTheRoot(t *testing.T) {
	f := FromEntries([]Entry{{Path: "README.md", SHA: "a"}})
	if !f.Listed(".") {
		t.Error(`FromEntries did not mark "." listed`)
	}
	if _, err := fs.Stat(f, "missing.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(missing.md) = %v, want fs.ErrNotExist", err)
	}
}
```

- [ ] **Step 2: Run them. One fails, and one pins what must survive**

Run: `go test ./internal/fetch/ -run 'TestNewFSListsNothing|TestFromEntriesListsTheRoot' -v`
Expected:
- `TestNewFSListsNothing` FAILS with `NewFS marked "." listed without a listing` and `Stat(README.md) on an unlisted root = stat README.md: file does not exist, want ErrNotListed`.
- `TestFromEntriesListsTheRoot` PASSES today, and has to keep passing.

- [ ] **Step 3: Stop `NewFS` marking the root**

In `internal/fetch/fs.go`, replace `NewFS`, including its one-line doc comment, with:

```go
// NewFS returns an empty filesystem with nothing listed.
//
// A directory is listed only when a listing covered it (ruling R45).
// FromEntries marks the root because a complete listing did; an adapter
// building a sparse filesystem with NewFS lists the root itself before
// anything else — walk() and GitLab.Open both do. NewFS used to mark "."
// here, unconditionally, and that one unearned flag is how a GitLab
// repository opened at non-root prefixes reported every root-level file as
// missing.
func NewFS() *FS {
	return &FS{
		entries: map[string]Entry{},
		listed:  map[string]bool{},
		blobs:   map[string][]byte{},
	}
}
```

- [ ] **Step 4: Run the suite and watch `FromEntries`' dependence show**

Run: `go test ./internal/fetch/ ./cmd/landsraad/`
Expected: FAIL, in exactly these four tests. This is the evidence that `FromEntries` leaned on `NewFS` for its root.
- `TestFromEntriesListsTheRoot`: `FromEntries did not mark "." listed`
- `TestACompleteListingProvesAbsenceAtAnyDepth`: `Stat apps/edge/runbook.md = ErrNotListed; the whole repository was listed, …`
- `TestBuildReportsADanglingRemoteRunbookInsteadOfDroppingTheRepository`: `stderr =` mismatch
- `TestBuildMergesALocalAndARemoteRepository`: `stderr =` mismatch, carrying a `checks-unreadable` diagnostic. This one reaches the same cause through Task 1: a local repository now has nothing listed, so reading `.landsraad/checks` answers `ErrNotListed`, and the branch Task 1 added — the one nothing could reach until now — fires. Step 5 restores the flag and it goes green with the other three.

- [ ] **Step 5: `FromEntries` marks the root it enumerated**

In `FromEntries`, directly after `f := NewFS()`, add:

```go
	// The loop below marks every entry's parents but stops short of ".",
	// so the root is marked here: the response described the whole
	// repository, root included.
	f.listed["."] = true
```

- [ ] **Step 6: Rewrite the two comments that described the old flag**

In `internal/fetch/fs.go`, in `lookup`'s doc comment, replace the final paragraph, the one beginning `// The climb is exactly as trustworthy as listed, and "." is the one key`, with:

```go
// The climb is exactly as trustworthy as listed, and every key in listed is
// earned by a listing: NewFS marks nothing, FromEntries marks the root
// because a complete listing enumerated it, and both adapters list the root
// before anything else (ruling R45). Until R45, NewFS marked "." at
// construction, and a GitLab repository opened at non-root prefixes answered
// fs.ErrNotExist for every root-level path — a runbook sitting in the
// repository, reported missing.
```

In `cmd/landsraad/repos.go`, in `docsDirs`' doc comment, replace the final paragraph, the one beginning `// "." is deliberately never returned, and expanding it would not help`, with:

```go
// "." is deliberately never returned, because it never needs to be: both
// adapters list the root at Open (ruling R45). GitHub's complete listing
// covers it and walk() records it; GitLab lists it before any prefix. A
// root-level runbook.md is therefore already in the listing. Asking GitHub
// to expand "." would recursively list the entire repository, the one thing
// ruling R28's descent exists to avoid.
```

- [ ] **Step 7: Run everything and watch it pass**

Run: `task ci`
Expected: clean. The whole suite passes under `-race`, including `TestBuildFetchesARootLevelRunbookFromAGitLabSatellite`, Task 1's `ErrNotListed` tests (their fixtures `AddDir(".")` explicitly), and the fixtures in `catalog`, `render` and `scorecard` that build a `*fetch.FS` by hand. This was observed.

- [ ] **Step 8: Commit**

```bash
git add internal/fetch/fs.go internal/fetch/fs_test.go cmd/landsraad/repos.go
git commit -m "fix(fetch): only a listing marks a directory listed"
```

---

### Task 5: A mistake in a file you wrote exits 2 (R36)

**Files:**
- Modify: `internal/discover/discover.go`: a new exported `CheckPattern`, which `Find` calls.
- Modify: `internal/config/repos.go`: `validateRepos` reports `repos-path` and drops rejected patterns. Also `Repo.pathsRejected` with `Repo.PathsRejected()`, a `LocalRejected` value for `LocalSource`, and `LocalPatterns`. Adds the `discover` import.
- Modify: `cmd/landsraad/repos.go`: `openRepos` does not announce the default fallback for an entry whose patterns were all rejected.
- Modify: `cmd/landsraad/validate.go`: a `discover.Find` error becomes a `discover` diagnostic, and the `no-entities` check is skipped when `Find` failed.
- Modify: `cmd/landsraad/build.go`: `newBuildCmd` exits `exitValidation` for `repos.yaml` diagnostics.
- Modify: `README.md`, `docs/superpowers/specs/2026-09-08-landsraad-design.md` (§12's exit-code table), and `cmd/landsraad/score.go` (the comment that quotes §12).
- Test: `internal/discover/discover_test.go`, `internal/config/repos_test.go`, `cmd/landsraad/validate_test.go`, `cmd/landsraad/integration_test.go`, `cmd/landsraad/build_test.go`

**Interfaces:**
- Consumes: the `go-cmp` import that Task 1 added to `cmd/landsraad/validate_test.go`.
- Produces:
  - `func CheckPattern(pattern string) error` in package `discover`.
  - `func (r *Repo) PathsRejected() bool` and the constant `LocalRejected LocalSource` in package `config`.
  - The rule every later task keeps: **2** means a file the user wrote has a problem a diagnostic points at, and **1** means landsraad could not run.

- [ ] **Step 1: Write the failing tests**

In `internal/discover/discover_test.go`, append:

```go
// CheckPattern is the one definition of a usable pattern. Find applies it,
// and repos.yaml loading reports it at the line that wrote the pattern
// (ruling R36), so the two cannot disagree.
func TestCheckPattern(t *testing.T) {
	for _, tt := range []struct{ pattern, want string }{
		{".", ""},
		{"", ""},
		{"services/*", ""},
		{"services/api", ""},
		{"/etc/*", `path pattern "/etc/*" must not be absolute; write a path relative to the repository root, for example "etc/*"`},
		{"../shared/*", `path pattern "../shared/*" escapes the repository root via ".."; patterns must stay under the repository root`},
		{"services/[", `bad path pattern "services/[": syntax error in pattern`},
	} {
		err := CheckPattern(tt.pattern)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tt.want {
			t.Errorf("CheckPattern(%q) = %q, want %q", tt.pattern, got, tt.want)
		}
	}
}
```

In `internal/config/repos_test.go`, add three rows to `TestLoadReposDiagnostics`' table. Put them directly before the row whose comment begins `// Pins the ordering the host-check split depends on`:

```go
		{
			name:        "absolute path pattern",
			yaml:        "repos:\n  - url: https://github.com/org/api\n    paths: [/services/*]\n",
			wantCheck:   "repos-path",
			wantLine:    2,
			wantMessage: `path pattern "/services/*" must not be absolute; write a path relative to the repository root, for example "services/*"`,
			wantHint:    "paths: are globs relative to the repository root, such as services/*",
		},
		{
			name:        "path pattern escaping the root",
			yaml:        "repos:\n  - url: https://github.com/org/api\n    paths: [../shared/*]\n",
			wantCheck:   "repos-path",
			wantLine:    2,
			wantMessage: `path pattern "../shared/*" escapes the repository root via ".."; patterns must stay under the repository root`,
			wantHint:    "paths: are globs relative to the repository root, such as services/*",
		},
		{
			name:        "malformed glob",
			yaml:        "repos:\n  - url: https://github.com/org/api\n    paths: [\"services/[\"]\n",
			wantCheck:   "repos-path",
			wantLine:    2,
			wantMessage: `bad path pattern "services/[": syntax error in pattern`,
			wantHint:    "paths: are globs relative to the repository root, such as services/*",
		},
```

Then append to the same file:

```go
// Ruling R36: a rejected pattern is reported once, where it is written, and
// never reaches discover.Find. The valid patterns beside it still search. An
// entry left with none falls back to DefaultPatterns the way a repos.yaml
// that failed to parse does — silently, because the rejection is the
// diagnostic and the fallback is its consequence.
func TestLoadReposDropsRejectedPatterns(t *testing.T) {
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte("repos:\n  - url: https://github.com/org/api\n    paths: [services/*, /workers/*]\n"), &c)
	if got, why := r.LocalPatterns(); !slices.Equal(got, []string{"services/*"}) || why != LocalMarked {
		t.Errorf("LocalPatterns = %v, %v; want [services/*], LocalMarked", got, why)
	}
	if r.Repos[0].PathsRejected() {
		t.Error("PathsRejected() = true for an entry with a pattern left to search")
	}

	c = diag.Collector{}
	r = LoadRepos("repos.yaml", []byte("repos:\n  - url: https://github.com/org/api\n    paths: [/services/*]\n"), &c)
	if got, why := r.LocalPatterns(); !slices.Equal(got, DefaultPatterns()) || why != LocalRejected {
		t.Errorf("LocalPatterns = %v, %v; want DefaultPatterns, LocalRejected", got, why)
	}
	if !r.Repos[0].PathsRejected() {
		t.Error("PathsRejected() = false for an entry whose every pattern was rejected")
	}
}
```

In `cmd/landsraad/validate_test.go`, append:

```go
// Ruling R36: a bad paths: entry is a mistake in a file the user wrote, so it
// is a diagnostic at its line and exit 2. validate used to exit 1 for it,
// through discover.Find's error, while exiting 2 for a bad url: in the same
// file. The entry falls back to the default paths without a second word:
// the rejection is the diagnostic.
func TestValidateReportsARejectedPathPatternAtItsLine(t *testing.T) {
	fsys := genFS()
	fsys["repos.yaml"] = &fstest.MapFile{Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [/services/*]\n")}

	var out, errOut bytes.Buffer
	code := Validate(fsys, &out, &errOut, diag.JSON{})
	if code != exitValidation {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("out is not diagnostics JSON: %v\n%s", err, out.String())
	}
	want := []diag.Diagnostic{{
		Severity: diag.SevError, File: "repos.yaml", Line: 2,
		Check:   "repos-path",
		Message: `path pattern "/services/*" must not be absolute; write a path relative to the repository root, for example "services/*"`,
		Hint:    "paths: are globs relative to the repository root, such as services/*",
	}}
	if diff := cmp.Diff(want, ds); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}
```

In `cmd/landsraad/integration_test.go`, append:

```go
// Ruling R36: a bad paths: entry on a remote repository used to reach
// discover.Find inside openRepos and come back as a fetch failure — exit 1,
// and a mere warning under --allow-partial, for a mistake in repos.yaml. It is
// now repos-path at the entry's line, like every other repos.yaml mistake,
// and the repository is still read, on the default paths.
func TestOpenReposReportsARejectedRemotePatternAsConfiguration(t *testing.T) {
	remote := materialize(t, map[string]string{
		"service.yaml": `apiVersion: landsraad/v1
kind: Service
metadata:
  name: edge
  description: Edge gateway.
  owner: team-platform
  tier: 1
  lifecycle: production
spec:
  language: go
  path: .
`,
	})
	srv := fakeGitHub(t, remote)
	root := remotePlatformRoot(t, srv.Listener.Addr().String(), "/services/*")

	var c diag.Collector
	w := openRepos(context.Background(), reposOptions{
		Root: root, RootFS: os.DirFS(root),
		Cache:  fetch.NopCache{},
		Lookup: func(string) (string, bool) { return "test-token", true },
		ErrOut: io.Discard,
		HTTP:   srv.Client(),
	}, &c)

	if got := w.Failures(); len(got) != 0 {
		t.Errorf("a repos.yaml mistake was reported as a fetch failure: %+v", got)
	}
	want := diag.Diagnostic{
		Severity: diag.SevError, File: "repos.yaml", Line: 5,
		Check:   "repos-path",
		Message: `path pattern "/services/*" must not be absolute; write a path relative to the repository root, for example "services/*"`,
		Hint:    "paths: are globs relative to the repository root, such as services/*",
	}
	if ds := c.Diagnostics(); len(ds) != 1 || ds[0] != want {
		t.Errorf("diagnostics = %+v, want exactly [%+v]", ds, want)
	}
}
```

In `cmd/landsraad/build_test.go`, make `TestBuildExitsWhenReposYAMLIsMalformed` expect 2. Replace its doc comment with:

```go
// TestBuildExitsWhenReposYAMLIsMalformed pins newBuildCmd's own openRepos
// gate, which had no subprocess test of its own even though serve's
// identical gate (TestServeWithoutWatchExitsWhenReposYAMLIsMalformed) does.
//
// build used to exit 1 (exitUsage) here while validate and serve exited 2
// for the same repos.yaml. Ruling R36 settled it: a mistake in a file the
// user wrote exits 2 in every command, and 1 is kept for landsraad being
// unable to run.
```

Then in the body, replace both uses of `exitUsage` in

```go
	if r.exitCode != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", r.exitCode, exitUsage, r.stderr)
	}
```

with `exitValidation`.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/discover/ ./internal/config/`
Expected: FAIL to compile, with `undefined: CheckPattern`, `undefined: LocalRejected` and `r.Repos[0].PathsRejected undefined`.

Run: `go test ./cmd/landsraad/ -run 'TestValidateReportsARejectedPathPatternAtItsLine|TestOpenReposReportsARejectedRemotePatternAsConfiguration|TestBuildExitsWhenReposYAMLIsMalformed' -v`
Expected: FAIL, all three. These were observed:
- `TestValidateReportsARejectedPathPatternAtItsLine`: `exit = 1, want 2`
- `TestBuildExitsWhenReposYAMLIsMalformed`: `exit = 1, want 2`
- `TestOpenReposReportsARejectedRemotePatternAsConfiguration`: `a repos.yaml mistake was reported as a fetch failure: [{Name:edge-gateway … Err:path pattern "/services/*" must not be absolute; …}]`, and `diagnostics = []`. This is finding N4, a configuration mistake downgraded to a network failure.

- [ ] **Step 3: `CheckPattern`, and `Find` uses it**

In `internal/discover/discover.go`, replace the start of `Find`'s loop:

```go
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
```

with:

```go
	for _, pattern := range patterns {
		if err := CheckPattern(pattern); err != nil {
			return nil, err
		}
		var dirs []string
		if pattern == "." || pattern == "" {
			dirs = []string{"."}
		} else {
			matches, err := fs.Glob(fsys, pattern)
			if err != nil {
				// CheckPattern has already refused every pattern fs.Glob
				// could; ErrBadPattern is its only error.
				return nil, fmt.Errorf("bad path pattern %q: %w", pattern, err)
			}
			dirs = matches
		}
```

Add, directly above `invalidPatternReason`:

```go
// CheckPattern says why pattern can never name anything under the repository
// root, or returns nil when it can. "." and "" mean the root itself.
//
// It is the one definition of a usable pattern. Find applies it before
// searching, and repos.yaml loading applies it where the pattern is written,
// so a mistake is reported at its line (ruling R36) and the two cannot
// disagree about what counts as one.
func CheckPattern(pattern string) error {
	if pattern == "." || pattern == "" {
		return nil
	}
	if !fs.ValidPath(pattern) {
		return errors.New(invalidPatternReason(pattern))
	}
	// An empty name makes path.Match check the whole pattern's syntax. It is
	// the same check fs.Glob opens with.
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("bad path pattern %q: %w", pattern, err)
	}
	return nil
}
```

- [ ] **Step 4: `repos.yaml` rejects a bad pattern where it is written**

In `internal/config/repos.go`:

1. Add `"github.com/landsraadhq/landsraad/internal/discover"` to the imports, after the `diag` import.
2. At the end of `type Repo struct`, after the `Line` field, add:

```go

	// pathsRejected is set by validateRepos when paths: named patterns and
	// repos-path rejected every one. See PathsRejected.
	pathsRejected bool
```

3. Directly above `HostKind`'s doc comment, add:

```go
// PathsRejected reports whether paths: named patterns and repos-path rejected
// every one (ruling R36). Such an entry falls back to DefaultPatterns like one
// that named none, but a caller announcing that fallback stays silent for it:
// the rejection is the diagnostic and the fallback is its consequence, the
// rule patternsFor already applies to a repos.yaml that failed to parse.
func (r *Repo) PathsRejected() bool { return r.pathsRejected }
```

4. In the `LocalSource` constants, after `LocalDefaulted`, add:

```go
	// LocalRejected: the local entry named paths and repos-path rejected every
	// one, so DefaultPatterns are in use. Not a separate thing to announce;
	// see Repo.PathsRejected.
	LocalRejected
```

5. Replace the body of `LocalPatterns` with:

```go
	local, ok := r.LocalRepo()
	if !ok {
		if len(r.Repos) == 0 || len(r.Repos[0].Paths) == 0 {
			if len(r.Repos) > 0 && r.Repos[0].pathsRejected {
				return DefaultPatterns(), LocalRejected
			}
			return DefaultPatterns(), LocalDefaulted
		}
		return r.Repos[0].Paths, LocalAssumedFirst
	}
	if len(local.Paths) == 0 {
		if local.pathsRejected {
			return DefaultPatterns(), LocalRejected
		}
		return DefaultPatterns(), LocalDefaulted
	}
	return local.Paths, LocalMarked
```

6. In `validateRepos`, make this the first thing in the loop body, directly after `e := &r.Repos[i]` and before the `https://` check:

```go
		// Checked before the url, and without a continue: a bad pattern and a
		// bad url are two mistakes, and both are reported. A rejected pattern
		// is dropped so it never reaches discover.Find, which would refuse it
		// again as an error of its own (ruling R36).
		if len(e.Paths) > 0 {
			var kept []string
			for _, p := range e.Paths {
				if err := discover.CheckPattern(p); err != nil {
					c.Add(diag.Diagnostic{
						Severity: diag.SevError, File: file, Line: e.Line,
						Check:   "repos-path",
						Message: err.Error(),
						Hint:    "paths: are globs relative to the repository root, such as services/*",
					})
					continue
				}
				kept = append(kept, p)
			}
			e.Paths = kept
			e.pathsRejected = len(kept) == 0
		}
```

`patternsFor` needs no change. Its `switch` announces only `LocalDefaulted` and `LocalAssumedFirst`, so `LocalRejected` passes through silently, as intended.

- [ ] **Step 5: The other consumers of the rule**

In `cmd/landsraad/repos.go`, in `openRepos`, replace:

```go
			// artifact, not only in a log.
			c.Add(repoDefaultPatternsNote(name, r.Line))
			patterns = config.DefaultPatterns()
```

with:

```go
			// artifact, not only in a log. The one silent case is an entry
			// whose every pattern was rejected: repos-path already said so,
			// and the default paths are that diagnostic's consequence.
			if !r.PathsRejected() {
				c.Add(repoDefaultPatternsNote(name, r.Line))
			}
			patterns = config.DefaultPatterns()
```

In `cmd/landsraad/validate.go`, in `Validate`, replace:

```go
	paths, err := discover.Find(fsys, patterns)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return exitUsage
	}
```

with:

```go
	paths, err := discover.Find(fsys, patterns)
	if err != nil {
		// Unreachable for a pattern repos.yaml wrote: validateRepos rejects
		// exactly what Find would (repos-path, ruling R36). Still a problem
		// with a file the user wrote, so a diagnostic and exit 2, as
		// parseRepo reports it, not exit 1: 1 is landsraad unable to run.
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files: %v", discover.Filename, err),
		})
	}
```

Directly below, change `if len(paths) == 0 {` (the `no-entities` check) to `if err == nil && len(paths) == 0 {`. When `Find` fails, finding nothing is not a second problem.

In `cmd/landsraad/build.go`, in `newBuildCmd`'s `RunE`, replace:

```go
			if c.HasErrors() {
				os.Exit(exitUsage)
			}
```

with:

```go
			if c.HasErrors() {
				// A repos.yaml mistake: a file the user wrote, so 2, as
				// validate and serve exit for the same file (ruling R36).
				os.Exit(exitValidation)
			}
```

- [ ] **Step 6: State the rule where it is documented**

In `README.md`, replace the two lines

```markdown
`validate` exits `0` clean, `1` on a usage or config error, `2` when it found
a problem in the catalog — script off the exit code, not the output.
```

with:

```markdown
Every command exits `0` clean, `2` when a file you wrote has a problem a
diagnostic points at — a `service.yaml`, `teams.yaml`, `repos.yaml` or
`.landsraad/checks` file — and `1` when landsraad could not run: a bad flag,
an unreadable directory, a repository it could not fetch. `score` also exits
`3` when the catalog is valid and a service fails a check its tier requires.
Script off the exit code, not the output.
```

In `docs/superpowers/specs/2026-09-08-landsraad-design.md`, §12's **Exit codes** table, replace rows 1 and 2:

```markdown
| 1 | usage or config error | whoever ran it |
| 2 | validation error — schema, collision, cycle, dangling ref | the YAML's author |
```

with:

```markdown
| 1 | landsraad could not run — a bad flag, an unreadable root, a repository it could not fetch | whoever ran it |
| 2 | a file you wrote has a problem a diagnostic points at — schema, collision, cycle, dangling ref, `repos.yaml` (ruling R36) | the YAML's author |
```

In `cmd/landsraad/score.go`, the comment above `if !historyOK {` quotes the old row. Replace its last four lines

```go
	// --history did nothing at all. Exit 1 is spec §12's "usage or config
	// error, whoever ran it", which is precisely who fixes a file this process
	// cannot read. Exit 0 was the wrong answer either way: the run printed
	// "error:" and then reported itself clean.
```

with

```go
	// --history did nothing at all. Exit 1 is spec §12's "landsraad could not
	// run", fixed by whoever ran it, which is precisely who fixes a file this
	// process cannot read. Exit 0 was the wrong answer either way: the run
	// printed "error:" and then reported itself clean.
```

- [ ] **Step 7: Run everything and watch it pass**

Run: `go test ./internal/discover/ ./internal/config/ -v -run 'TestCheckPattern|TestLoadReposDiagnostics|TestLoadReposDropsRejectedPatterns|TestFind'`
Expected: PASS. That includes the existing `TestFindRejectsAbsolutePattern`, whose message `Find` still returns through `CheckPattern`.

Run: `task ci`
Expected: clean. This was observed: the whole suite passes under `-race`, including the three `cmd` tests from Step 2 and the serve tests that already expected 2.

- [ ] **Step 8: Commit**

```bash
git add internal/discover/discover.go internal/discover/discover_test.go \
  internal/config/repos.go internal/config/repos_test.go \
  cmd/landsraad/repos.go cmd/landsraad/validate.go cmd/landsraad/validate_test.go \
  cmd/landsraad/build.go cmd/landsraad/build_test.go cmd/landsraad/integration_test.go \
  cmd/landsraad/score.go README.md docs/superpowers/specs/2026-09-08-landsraad-design.md
git commit -m "fix: a mistake in a file you wrote exits 2, and says where"
```

---

### Task 6: `validate --satellite` (R37)

**Files:**
- Modify: `cmd/landsraad/validate.go`:
  - `Validate` gains a trailing `satellite bool`, following the house precedent (`Gen` takes a trailing bool).
  - When `satellite` is set, `Validate` refuses a repository that has a root `teams.yaml`, and swaps `checkOwners` for an `owners-deferred` note.
  - `newValidateCmd` gains the flag.
- Modify: every existing `Validate(…)` call in `cmd/landsraad`. They are rewritten mechanically in Step 3.
- Modify: `README.md`, with one paragraph in "Multi-repository catalogs".
- Test: `cmd/landsraad/validate_test.go`

**Interfaces:**
- Consumes: Task 1's `cmp` import in `validate_test.go`.
- Produces: `func Validate(fsys fs.FS, out, errOut io.Writer, f diag.Formatter, satellite bool) int`, and the `--satellite` flag. Task 7's `validate` hint names the flag.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/landsraad/validate_test.go`:

```go
// Ruling R37. A satellite's own CI has no teams.yaml to resolve owners
// against — ruling R34 keeps it in the platform repository — so validate
// failed every satellite's PR on missing-teams. --satellite leaves owners to
// the platform build, and says so, so the skipped check is visible rather
// than silent.
func TestValidateSatelliteDefersOwnersToThePlatformBuild(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(os.DirFS("../../testdata/multirepo/edge-gateway"), &out, &errOut, diag.JSON{}, true)
	if code != exitOK {
		t.Fatalf("exit = %d, want %d; out:\n%s\nstderr:\n%s", code, exitOK, out.String(), errOut.String())
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("out is not diagnostics JSON: %v\n%s", err, out.String())
	}
	want := []diag.Diagnostic{
		{
			Severity: diag.SevInfo, File: "repos.yaml", Line: 1,
			Check:   "default-patterns",
			Message: "no repos.yaml found; using default paths (., services/*, workers/*, libs/*, topics/*)",
			Hint:    "add repos.yaml if your services live elsewhere",
		},
		{
			Severity: diag.SevInfo, File: "teams.yaml", Line: 1,
			Check:   "owners-deferred",
			Message: "owners are not checked in a satellite repository; the platform build resolves them against its teams.yaml",
		},
	}
	if diff := cmp.Diff(want, ds); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// Refusing --satellite beside a teams.yaml is the reversible choice (R37): it
// can be relaxed later, where accepting it could never be tightened, and it
// stops a platform repository switching off its own owner checks by copying
// a satellite's CI configuration.
func TestValidateSatelliteRefusesARepositoryWithATeamsFile(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(genFS(), &out, &errOut, diag.Text{}, true)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitUsage, errOut.String())
	}
	want := "error: --satellite skips owner checks, but this repository has a teams.yaml; " +
		"drop the flag, or delete the file if the platform repository's teams.yaml is the real one\n"
	if got := errOut.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing: a refusal has no diagnostics to format", out.String())
	}
}
```

In the same file, update `TestValidateToleratesTheFixturesCrossRepoRef`'s doc comment. Its reasoning was the open question R37 answers. Replace

```go
// It does NOT exit 0. edge-gateway, read on its own, carries no teams.yaml —
// only the platform root does (ruling R34); a real user only ever validates
// it as part of a checkout that has one. That is an unrelated, expected
// failure, and asserting it here — rather than picking a repo-less fixture
// that would hide it — is what proves the *only* diagnostic in play is the
// one about ownership, and specifically not one about the cross-repo ref.
```

with

```go
// It does NOT exit 0. edge-gateway, read on its own, carries no teams.yaml —
// only the platform root does (ruling R34) — so without --satellite this is
// missing-teams. Its own CI passes --satellite (ruling R37), which
// TestValidateSatelliteDefersOwnersToThePlatformBuild covers. The failure is
// unrelated and expected, and asserting it here — rather than picking a
// repo-less fixture that would hide it — is what proves the *only*
// diagnostic in play is the one about ownership, and specifically not one
// about the cross-repo ref.
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./cmd/landsraad/ -run 'TestValidateSatellite' -v`
Expected: FAIL to compile, with `too many arguments in call to Validate`.

- [ ] **Step 3: Give every existing caller the new argument, mechanically**

Run: `gofmt -r 'Validate(a, b, c, d) -> Validate(a, b, c, d, false)' -w cmd/landsraad/`

This rewrites only calls to the bare identifier `Validate`. Selector calls such as `validator.Validate(…)` and `v.Validate(…)` in `validate.go` are a different expression shape, and are not touched. Check it:

Run: `git diff --stat cmd/landsraad/`
Expected: `validate.go`, `validate_test.go` and `init_test.go` changed, and nothing else.

Run: `grep -n 'validator.Validate\|v.Validate' cmd/landsraad/validate.go`
Expected: both lines still have four arguments.

- [ ] **Step 4: Implement `satellite`**

In `cmd/landsraad/validate.go`, replace the end of `Validate`'s doc comment and its opening:

```go
// It takes an fs.FS rather than a path, so the same pipeline runs against a
// local checkout, a fetched remote repo, or a test fixture in memory.
func Validate(fsys fs.FS, out, errOut io.Writer, f diag.Formatter) int {
	var c diag.Collector
```

with:

```go
// It takes an fs.FS rather than a path, so the same pipeline runs against a
// local checkout, a fetched remote repo, or a test fixture in memory.
//
// satellite says this repository's owners are defined in the platform
// repository's teams.yaml, not here (ruling R37). Owner resolution is then
// left to the platform build, and a note says so, rather than failing every
// satellite's PR on a file ruling R34 says it must not have.
func Validate(fsys fs.FS, out, errOut io.Writer, f diag.Formatter, satellite bool) int {
	var c diag.Collector

	// Refused rather than tolerated, because refusing is the reversible
	// choice (R37): it can be relaxed later, and it stops a platform
	// repository switching off its own owner checks by copying a satellite's
	// CI configuration.
	if satellite {
		if _, err := fs.Stat(fsys, "teams.yaml"); err == nil {
			fmt.Fprintf(errOut, "error: --satellite skips owner checks, but this repository has a teams.yaml; "+
				"drop the flag, or delete the file if the platform repository's teams.yaml is the real one\n")
			return exitUsage
		}
	}
```

Replace:

```go
	// 5. semantic checks
	checkOwners(fsys, cat, &c)
```

with:

```go
	// 5. semantic checks
	if satellite {
		c.Add(diag.Diagnostic{
			Severity: diag.SevInfo, File: "teams.yaml", Line: 1,
			Check:   "owners-deferred",
			Message: "owners are not checked in a satellite repository; the platform build resolves them against its teams.yaml",
		})
	} else {
		checkOwners(fsys, cat, &c)
	}
```

In `newValidateCmd`, replace `var format string` with:

```go
	var (
		format    string
		satellite bool
	)
```

In `RunE`, Step 3 left the call as `Validate(os.DirFS(resolved), cmd.OutOrStdout(), cmd.ErrOrStderr(), f, false)`. Change its `false` to `satellite`. Directly before `return cmd`, add:

```go
	cmd.Flags().BoolVar(&satellite, "satellite", false,
		"this repository's owners are defined in the platform repository's teams.yaml; leave them to the platform build")
```

- [ ] **Step 5: Document the flag**

In `README.md`, directly before the paragraph that begins `**\`build --allow-partial\`** renders the portal`, add:

```markdown
**`validate --satellite`** is for a repository the platform's `repos.yaml`
fetches. It has no `teams.yaml` of its own — owners are defined once, in the
platform repository — so plain `validate` in its CI would fail on
`missing-teams`. `--satellite` checks everything else and leaves owners to
the platform build, with a note saying so. It refuses to run in a repository
that does have a `teams.yaml`.

```

- [ ] **Step 6: Run everything and watch it pass**

Run: `go test ./cmd/landsraad/ -run 'TestValidateSatellite|TestValidateToleratesTheFixturesCrossRepoRef' -v`
Expected: PASS, all three.

Run: `task ci`
Expected: clean. This was observed.

- [ ] **Step 7: Commit**

```bash
git add cmd/landsraad/validate.go cmd/landsraad/validate_test.go cmd/landsraad/init_test.go README.md
git commit -m "feat: validate --satellite leaves owners to the platform build"
```

---

### Task 7: One id for a missing `teams.yaml` (R43)

**Files:**
- Modify: `cmd/landsraad/validate.go`: `checkOwners`' message and hint.
- Modify: `cmd/landsraad/gen.go`: `assemble`'s check id and message. The hint is unchanged.
- Modify: `cmd/landsraad/root.go`: the `findRoot` comment that quotes the message.
- Test: `cmd/landsraad/validate_test.go`

**Interfaces:**
- Consumes: Task 6's `--satellite` flag, which the `validate` hint names.
- Produces: the check id `missing-teams` in every command, with one message. Task 8 reads `teams.yaml` in more cases and emits this same diagnostic.

- [ ] **Step 1: Write the failing tests**

In `cmd/landsraad/validate_test.go`, in `TestCheckOwnersReportsMissingTeamsFileExactMessage`, replace the expected strings:

```go
	want := "teams.yaml not found at the repository root"
```

becomes

```go
	want := "teams.yaml not found at the repository root, so no owner can be resolved"
```

and

```go
	wantHint := "every entity's owner must resolve to a team defined there"
```

becomes

```go
	wantHint := "run `landsraad init` to create one, or pass --satellite if this repository's owners are defined in the platform repository's teams.yaml"
```

Directly after that test, add:

```go
// Ruling R43: one condition, one check id. gen, score and build said
// teams-missing while validate said missing-teams, with a different message
// and hint, about the same absent file. The id and message are now shared;
// the hint is each command's own, because the remedy differs: validate has
// --satellite, and gen, score and build have no such mode.
func TestLoadCatalogReportsMissingTeamsLikeValidate(t *testing.T) {
	fsys := genFS()
	delete(fsys, "teams.yaml")
	var c diag.Collector

	loadCatalog(fsys, &c)

	want := []diag.Diagnostic{{
		Severity: diag.SevError, File: "teams.yaml", Line: 1,
		Check:   "missing-teams",
		Message: "teams.yaml not found at the repository root, so no owner can be resolved",
		Hint:    "run `landsraad init` to create one",
	}}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./cmd/landsraad/ -run 'TestCheckOwnersReportsMissingTeamsFileExactMessage|TestLoadCatalogReportsMissingTeamsLikeValidate' -v`
Expected: FAIL, both. This was observed:
- `TestCheckOwnersReportsMissingTeamsFileExactMessage`: `Message got: teams.yaml not found at the repository root` and `Hint got: every entity's owner must resolve to a team defined there`.
- `TestLoadCatalogReportsMissingTeamsLikeValidate`: a diff of exactly one diagnostic, `Check: "missing-teams"` against `"teams-missing"`, with the message missing ` at the repository root`.

- [ ] **Step 3: One id, one message**

In `cmd/landsraad/validate.go`, in `checkOwners`, replace:

```go
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root",
			Hint:    "every entity's owner must resolve to a team defined there",
```

with:

```go
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root, so no owner can be resolved",
			// Not gen's hint: validate is the one command a satellite runs,
			// and --satellite is its remedy (ruling R43).
			Hint: "run `landsraad init` to create one, or pass --satellite if this repository's owners are defined in the platform repository's teams.yaml",
```

In `cmd/landsraad/gen.go`, in `assemble`, replace:

```go
			Check:   "teams-missing",
			Message: "teams.yaml not found, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
```

with:

```go
			// validate's id and message for the same absent file (ruling R43).
			// The hint is this command's own: gen, score and build run only in
			// the platform repository, where --satellite is not an answer.
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
```

In `cmd/landsraad/root.go`, in `findRoot`'s doc comment, replace:

```go
// findRoot walks up from start looking for a repository root, the way every
// linter does. Without it, running `landsraad validate` from inside
// services/foo/ reports "teams.yaml not found at the repository root" while
// standing in a subdirectory of a perfectly valid repo.
```

with:

```go
// findRoot walks up from start looking for a repository root, the way every
// linter does. Without it, running `landsraad validate` from inside
// services/foo/ reports "teams.yaml not found at the repository root, so no
// owner can be resolved" while standing in a subdirectory of a perfectly
// valid repo.
```

- [ ] **Step 4: Run everything and watch it pass**

Run: `go test ./cmd/landsraad/ -run 'TestCheckOwnersReportsMissingTeamsFileExactMessage|TestLoadCatalogReportsMissingTeamsLikeValidate|TestValidateToleratesTheFixturesCrossRepoRef' -v`
Expected: PASS, all three.

Run: `task ci`
Expected: clean. This was observed.

- [ ] **Step 5: Commit**

```bash
git add cmd/landsraad/validate.go cmd/landsraad/validate_test.go cmd/landsraad/gen.go cmd/landsraad/root.go
git commit -m "fix: a missing teams.yaml has one check id, missing-teams, in every command"
```

---

### Task 8: An all-parse failure keeps its other diagnostics (R42)

This task has two test-first cycles, because the ruling has two halves:
- Cycle 1: `teams.yaml` is read even for an empty catalog.
- Cycle 2: "no service.yaml found" becomes a claim about files, not entities.

**Files:**
- Modify: `cmd/landsraad/gen.go`:
  - Cycle 1 reorders `assemble`.
  - Cycle 2 adds `parseResult`, `parseRepo` returns it, `loadCatalogScoped` passes it on, and `assemble` takes it.
- Modify: `cmd/landsraad/repos.go`: `workspace.ParseAll` returns a `parseResult`, and `openRepos`' scratch parse takes `.entities` (both cycle 2).
- Test: `cmd/landsraad/validate_test.go`, `cmd/landsraad/repos_test.go`

**Interfaces:**
- Consumes: Task 7's `missing-teams` diagnostic.
- Produces: `type parseResult struct { entities []*catalog.Entity; found int }`, plus these signatures:
  - `func parseRepo(name string, fsys fs.FS, patterns []string, solo bool, v *schema.Validator, c *diag.Collector) parseResult`
  - `func (w *workspace) ParseAll(v *schema.Validator, c *diag.Collector) parseResult`
  - `func assemble(p parseResult, src catalog.Sources, scope catalog.Scope, cfg fs.FS, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams)`

#### Cycle 1: `teams.yaml` is read whether or not anything parsed

- [ ] **Step 1: Write the failing test**

Append to `cmd/landsraad/validate_test.go`:

```go
// Ruling R42. Since Plan 4, assemble returned on an empty catalog before it
// read teams.yaml, so a run where every service.yaml failed to parse hid
// every teams.yaml problem too, and the user met them one run later. The
// catalog is still empty; teams.yaml is still read.
func TestLoadCatalogStillReportsTeamsWhenEveryServiceFailsToParse(t *testing.T) {
	fsys := genFS()
	delete(fsys, "teams.yaml")
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte("apiVersion: [unterminated\n")}
	var c diag.Collector

	loadCatalog(fsys, &c)

	// Collector.Diagnostics sorts by file, so services/ comes before teams.yaml.
	want := []diag.Diagnostic{
		{
			Severity: diag.SevError, Repo: "monorepo", File: "services/api/service.yaml", Line: 1,
			Check:   "yaml-parse",
			Message: "cannot parse YAML: yaml: line 1: did not find expected ',' or ']'",
		},
		{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		},
	}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./cmd/landsraad/ -run 'TestLoadCatalogStillReportsTeamsWhenEveryServiceFailsToParse' -v`
Expected: FAIL. The diff shows the `missing-teams` entry as `-want` only, with no `+got` line for it; the `yaml-parse` entry is common to both. This was observed.

- [ ] **Step 3: Read `teams.yaml` before the empty-catalog return**

In `cmd/landsraad/gen.go`, replace the whole of `assemble`, from `func assemble(` to its closing brace, with:

```go
func assemble(entities []*catalog.Entity, src catalog.Sources, scope catalog.Scope, cfg fs.FS, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	// teams.yaml first, so its own mistakes are reported whether or not any
	// entity parsed. The empty-catalog return below used to come before this
	// read, so since Plan 4 a run where every service.yaml failed to parse
	// also hid every teams.yaml problem, and the user met them one run later
	// (ruling R42).
	var teams *config.Teams
	if data, err := fs.ReadFile(cfg, "teams.yaml"); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			// validate's id and message for the same absent file (ruling R43).
			// The hint is this command's own: gen, score and build run only in
			// the platform repository, where --satellite is not an answer.
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		})
	} else {
		teams = config.LoadTeams("teams.yaml", data, c)
	}

	if len(entities) == 0 {
		// len(src) > 1 mirrors parseRepo's solo flag exactly, from the same
		// source of truth every caller already has (SingleSource for a
		// single repository; workspace.Sources() for the fetched-workspace
		// path). Below that count, a solo parseRepo call has already
		// reported the one rich error a single-repository run produces —
		// adding a second, thinner "no entities" diagnostic here would be
		// the duplicate-with-lost-detail regression a single-repository run
		// must never show. Above it, no per-repository warning can say
		// whether the WHOLE catalog is empty, which is what this reports.
		if len(src) > 1 {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "repos.yaml", Line: 1,
				Check:   "no-entities",
				Message: fmt.Sprintf("no %s found in any configured repository", discover.Filename),
				Hint:    "add a repos.yaml listing the paths your services live under",
			})
		}
		return nil, nil, nil
	}
	cat := catalog.NewCatalog(entities, c)
	catalog.CheckFiles(src, cat, c)
	g := cat.Resolve(scope, c)
	reportCycles(cat, g, c)
	if teams == nil {
		return nil, nil, nil
	}
	teams.ValidateOwners(cat, c)
	return cat, g, teams
}
```

- [ ] **Step 4: Run the package. The new test passes, and three old ones now see `teams.yaml`**

Run: `go test ./cmd/landsraad/ -run 'TestLoadCatalogStillReportsTeamsWhenEveryServiceFailsToParse|TestAssemble|TestWorkspaceParseAll' -v`
Expected: the new test PASSES. Three existing tests FAIL, because they gave `assemble` no `teams.yaml`, or one holding just `x`, and until now it never read it for an empty catalog. This was observed:
- `TestAssembleZeroEntitiesSoloAddsNoDiagnostic`: `assemble added 1 diagnostics for a solo empty catalog, want 0` (`missing-teams`).
- `TestAssembleZeroEntitiesMultiExactMessage`: `got 2 diagnostics, want exactly 1`.
- `TestWorkspaceParseAllThenAssembleMultiRepoZeroMatchKeepsBothDiagnostics`: `got 4 diagnostics, want exactly 3`. The extra one is the teams parse error for `x`.

Those tests are about the `no-entities` diagnostics, not about teams, so they get a `teams.yaml` with nothing to report. Append to `cmd/landsraad/repos_test.go`:

```go
// teamsOnly is a repository root holding nothing but a valid teams.yaml.
// Since ruling R42, assemble reads teams.yaml even for an empty catalog, so
// a test about something else gives it one with nothing to report.
func teamsOnly() fstest.MapFS {
	return fstest.MapFS{"teams.yaml": genFS()["teams.yaml"]}
}
```

Then point the three at it. Each replacement changes only the fourth argument to `assemble`:
- In `TestAssembleZeroEntitiesSoloAddsNoDiagnostic`: `assemble(nil, catalog.SingleSource("monorepo", fstest.MapFS{}), catalog.FullCatalog, fstest.MapFS{}, &c)` becomes `assemble(nil, catalog.SingleSource("monorepo", fstest.MapFS{}), catalog.FullCatalog, teamsOnly(), &c)`.
- In `TestAssembleZeroEntitiesMultiExactMessage`: `assemble(nil, src, catalog.FullCatalog, fstest.MapFS{}, &c)` becomes `assemble(nil, src, catalog.FullCatalog, teamsOnly(), &c)`.
- In `TestWorkspaceParseAllThenAssembleMultiRepoZeroMatchKeepsBothDiagnostics`: `assemble(entities, w.Sources(), catalog.FullCatalog, fstest.MapFS{"teams.yaml": {Data: []byte("x")}}, &c)` becomes `assemble(entities, w.Sources(), catalog.FullCatalog, teamsOnly(), &c)`.

Run: `go test ./cmd/landsraad/`
Expected: `ok`.

#### Cycle 2: "no service.yaml found" is a claim about files

- [ ] **Step 5: Write the failing test**

Append to `cmd/landsraad/repos_test.go`:

```go
// The other half of ruling R42. In a multi-repository run, "no service.yaml
// found in any configured repository" is a claim about files, and it used to
// be made about entities: when every file that was found failed to parse, it
// fired on top of the parse errors that already said why the catalog was
// empty, and told the reader their files were not there.
func TestAssembleDoesNotSayNothingWasFoundWhenFilesFailedToParse(t *testing.T) {
	src := catalog.Sources{"repo-a": fstest.MapFS{}, "repo-b": fstest.MapFS{}}
	var c diag.Collector

	cat, g, teams := assemble(parseResult{found: 2}, src, catalog.FullCatalog, teamsOnly(), &c)

	if cat != nil || g != nil || teams != nil {
		t.Fatalf("assemble = (%v, %v, %v), want all nil for an empty catalog", cat, g, teams)
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Errorf("assemble added %+v; the parse errors that emptied the catalog have already said why", ds)
	}
}
```

- [ ] **Step 6: Run it and watch it fail**

Run: `go test ./cmd/landsraad/ -run 'TestAssembleDoesNotSayNothingWasFoundWhenFilesFailedToParse' -v`
Expected: FAIL to compile, with `undefined: parseResult`.

- [ ] **Step 7: Carry the found count from `parseRepo` to `assemble`**

In `cmd/landsraad/gen.go`, directly above `parseRepo`'s doc comment, add:

```go
// parseResult is stage 3's output: the entities, and how many catalog files
// were found to parse them from. The count travels with the entities because
// assemble's "no service.yaml found" is a claim about files, and only the
// thing that looked for them knows (ruling R42). It used to be inferred from
// the entities, which reported files that were found and failed to parse as
// files that were never there.
type parseResult struct {
	entities []*catalog.Entity
	found    int
}
```

Change `parseRepo`'s signature to return `parseResult`:

```go
func parseRepo(name string, fsys fs.FS, patterns []string, solo bool, v *schema.Validator, c *diag.Collector) parseResult {
```

In its body, each of the three `return nil` statements (after the `discover` error, after the solo `no-entities` error, and after the per-repository warning) becomes `return parseResult{}`. The final `return catalog.ParseAll(name, files, c)` becomes:

```go
	return parseResult{entities: catalog.ParseAll(name, files, c), found: len(found)}
```

In `loadCatalogScoped`, replace:

```go
	entities := parseRepo(repo, fsys, patternsFor(fsys, c), true, v, c)
	return assemble(entities, catalog.SingleSource(repo, fsys), scope, fsys, c)
```

with:

```go
	p := parseRepo(repo, fsys, patternsFor(fsys, c), true, v, c)
	return assemble(p, catalog.SingleSource(repo, fsys), scope, fsys, c)
```

Replace `assemble` again, with its final form:

```go
func assemble(p parseResult, src catalog.Sources, scope catalog.Scope, cfg fs.FS, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	// teams.yaml first, so its own mistakes are reported whether or not any
	// entity parsed. The empty-catalog return below used to come before this
	// read, so since Plan 4 a run where every service.yaml failed to parse
	// also hid every teams.yaml problem, and the user met them one run later
	// (ruling R42).
	var teams *config.Teams
	if data, err := fs.ReadFile(cfg, "teams.yaml"); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			// validate's id and message for the same absent file (ruling R43).
			// The hint is this command's own: gen, score and build run only in
			// the platform repository, where --satellite is not an answer.
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		})
	} else {
		teams = config.LoadTeams("teams.yaml", data, c)
	}

	if len(p.entities) == 0 {
		// len(src) > 1 mirrors parseRepo's solo flag exactly, from the same
		// source of truth every caller already has (SingleSource for a
		// single repository; workspace.Sources() for the fetched-workspace
		// path). Below that count, a solo parseRepo call has already
		// reported the one rich error a single-repository run produces —
		// adding a second, thinner "no entities" diagnostic here would be
		// the duplicate-with-lost-detail regression a single-repository run
		// must never show. Above it, no per-repository warning can say
		// whether the WHOLE catalog is empty, which is what this reports.
		//
		// p.found == 0 because the message is a claim about files. When
		// every file that was found failed to parse, the parse errors have
		// already said why the catalog is empty (ruling R42).
		if len(src) > 1 && p.found == 0 {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "repos.yaml", Line: 1,
				Check:   "no-entities",
				Message: fmt.Sprintf("no %s found in any configured repository", discover.Filename),
				Hint:    "add a repos.yaml listing the paths your services live under",
			})
		}
		return nil, nil, nil
	}
	cat := catalog.NewCatalog(p.entities, c)
	catalog.CheckFiles(src, cat, c)
	g := cat.Resolve(scope, c)
	reportCycles(cat, g, c)
	if teams == nil {
		return nil, nil, nil
	}
	teams.ValidateOwners(cat, c)
	return cat, g, teams
}
```

In `cmd/landsraad/repos.go`, change `ParseAll`'s signature to `func (w *workspace) ParseAll(v *schema.Validator, c *diag.Collector) parseResult`. Replace its loop:

```go
	var out []*catalog.Entity
	for _, name := range w.sources.Names() {
		fsys, ok := w.sources.Get(name)
		if !ok {
			continue
		}
		out = append(out, parseRepo(name, fsys, w.patterns[name], solo, v, c)...)
	}
	return out
```

with:

```go
	var out parseResult
	for _, name := range w.sources.Names() {
		fsys, ok := w.sources.Get(name)
		if !ok {
			continue
		}
		p := parseRepo(name, fsys, w.patterns[name], solo, v, c)
		out.entities = append(out.entities, p.entities...)
		out.found += p.found
	}
	return out
```

In `openRepos`, the scratch parse keeps its variable name and takes the entities:

```go
			parsed := parseRepo(name, fsys, patterns, false, v, &scratch).entities
```

`build.go`'s `assemble(w.ParseAll(v, &c), w.Sources(), …)` needs no change, because `ParseAll` now returns exactly what `assemble` takes.

Update the tests that read the old return types, in `cmd/landsraad/repos_test.go`:
- The test calling `w.ParseAll` near `Sources().Names()` has `entities := w.ParseAll(v, &c)`. It becomes `entities := w.ParseAll(v, &c).entities`.
- In both `TestParseRepoZeroFoundSoloExactMessage` and `TestParseRepoZeroFoundMultiExactMessage`, replace

  ```go
  	entities := parseRepo("monorepo", fsys, []string{"services/*"}, true, v, &c)
  	if entities != nil {
  		t.Fatalf("entities = %+v, want nil", entities)
  	}
  ```

  (with `false` in the multi test) with

  ```go
  	p := parseRepo("monorepo", fsys, []string{"services/*"}, true, v, &c)
  	if p.entities != nil || p.found != 0 {
  		t.Fatalf("parseRepo = %+v, want no entities and nothing found", p)
  	}
  ```

  using `false` in the multi test.
- In `TestAssembleZeroEntitiesSoloAddsNoDiagnostic` and `TestAssembleZeroEntitiesMultiExactMessage`, `assemble(nil, …)` becomes `assemble(parseResult{}, …)`.
- In `TestWorkspaceParseAllThenAssembleMultiRepoZeroMatchKeepsBothDiagnostics`, replace

  ```go
  	entities := w.ParseAll(v, &c)
  	if len(entities) != 0 {
  		t.Fatalf("entities = %+v, want none", entities)
  	}
  	cat, g, teams := assemble(entities, w.Sources(), catalog.FullCatalog, teamsOnly(), &c)
  ```

  with

  ```go
  	p := w.ParseAll(v, &c)
  	if len(p.entities) != 0 || p.found != 0 {
  		t.Fatalf("ParseAll = %+v, want no entities and nothing found", p)
  	}
  	cat, g, teams := assemble(p, w.Sources(), catalog.FullCatalog, teamsOnly(), &c)
  ```

- [ ] **Step 8: Run everything and watch it pass**

Run: `go test ./cmd/landsraad/ -run 'TestLoadCatalogStillReportsTeams|TestAssemble|TestParseRepoZeroFound|TestWorkspaceParseAll' -v`
Expected: PASS, all of them.

Run: `task ci`
Expected: clean. This was observed.

- [ ] **Step 9: Commit**

```bash
git add cmd/landsraad/gen.go cmd/landsraad/repos.go cmd/landsraad/validate_test.go cmd/landsraad/repos_test.go
git commit -m "fix: an all-parse failure still reports teams.yaml, and never says nothing was found"
```

---

### Task 9: `Repo` names the repository that holds `File` (R41)

The spec's rule is that `Repo` names the repository that holds `File`. For the four sites that point at `repos.yaml`, the implementation leaves `Repo` **empty**. Empty already means "the repository the command is standing in" for every diagnostic about the root's own configuration: `repos-url`, `missing-teams` and `default-patterns` all leave it empty. Those four sites' messages already name the repository they are about. The alternative, stamping the root's name on them, would mean threading it through `parseRepo` and `openRepos` only to print `platform:repos.yaml:4`.

**Files:**
- Modify: `internal/diag/format.go`: `Text` gains `ShowRepo`.
- Modify: `internal/diag/diag.go`: a doc comment on `Diagnostic.Repo`.
- Modify: `cmd/landsraad/root.go`: `reportDiagnostics` takes `showRepo`.
- Modify: `cmd/landsraad/build.go`: four `reportDiagnostics` calls, and `lastEditDiagnostic` drops `Repo`.
- Modify: `cmd/landsraad/serve.go`, `cmd/landsraad/score.go`: `reportDiagnostics` calls.
- Modify: `cmd/landsraad/gen.go`: `parseRepo`'s search error and per-repository warning drop `Repo`, and the search error names the repository.
- Modify: `cmd/landsraad/repos.go`: `repoDefaultPatternsNote` drops `Repo`.
- Test: `internal/diag/format_test.go`, `cmd/landsraad/repos_test.go`, `cmd/landsraad/integration_test.go`, `cmd/landsraad/serve_test.go` (a `reportDiagnostics` caller).

**Interfaces:**
- Produces:
  - `type Text struct { ShowRepo bool }`.
  - `func reportDiagnostics(errOut io.Writer, ds []diag.Diagnostic, showRepo bool)`.

  Task 10 adds a line to `lastEditDiagnostic`, and the diagnostic it builds keeps `Repo` empty.

- [ ] **Step 1: Write the failing tests**

Append to `internal/diag/format_test.go`:

```go
// Ruling R41: in a multi-repository build, "service.yaml:4" does not say which
// of ten repositories to open. ShowRepo prefixes the location with the
// repository that holds the file, when a diagnostic names one. A diagnostic
// about the root's own configuration names none, and stays unprefixed.
func TestTextShowRepoPrefixesTheRepositoryHoldingTheFile(t *testing.T) {
	ds := []Diagnostic{
		{Severity: SevError, Repo: "edge-gateway", File: "service.yaml", Line: 4, Check: "missing-file",
			Message: `spec.runbook points at "runbook.md", which does not exist`},
		{Severity: SevWarn, File: "repos.yaml", Line: 5, Check: "default-patterns",
			Message: "edge-gateway names no paths; using default paths (., services/*)"},
	}
	for _, tt := range []struct {
		name string
		text Text
		want string
	}{
		{"on", Text{ShowRepo: true},
			"error: edge-gateway:service.yaml:4 [missing-file]\n" +
				"  spec.runbook points at \"runbook.md\", which does not exist\n" +
				"warn: repos.yaml:5 [default-patterns]\n" +
				"  edge-gateway names no paths; using default paths (., services/*)\n"},
		{"off", Text{},
			"error: service.yaml:4 [missing-file]\n" +
				"  spec.runbook points at \"runbook.md\", which does not exist\n" +
				"warn: repos.yaml:5 [default-patterns]\n" +
				"  edge-gateway names no paths; using default paths (., services/*)\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tt.text.Write(&buf, ds); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("Write =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
```

In `cmd/landsraad/repos_test.go`, in the test that checks the `default-patterns` note, replace:

```go
	if d.Repo != "monorepo" {
		t.Errorf("Repo = %q, want %q", d.Repo, "monorepo")
	}
```

with:

```go
	// Empty, not "monorepo": the note is about repos.yaml, which lives in the
	// repository the command is standing in, and Repo names the repository
	// that holds File (ruling R41). The message names the entry it is about.
	if d.Repo != "" {
		t.Errorf("Repo = %q, want empty", d.Repo)
	}
```

In `TestWorkspaceParseAllThenAssembleMultiRepoZeroMatchKeepsBothDiagnostics`, the warning check builds its expectation from `d.Repo`, which is now empty. Make these three changes:

1. Change `var warns, errs int` to:

```go
	var warns, errs int
	var warnMessages []string
```

2. Replace the `case diag.SevWarn:` branch:

```go
		case diag.SevWarn:
			warns++
			want := "no service.yaml found in " + d.Repo + " under any configured path (services/*)"
			if d.Message != want {
				t.Errorf("warning Message\n got: %s\nwant: %s", d.Message, want)
			}
```

with:

```go
		case diag.SevWarn:
			warns++
			// Repo is empty: the warning is about repos.yaml, in the repository
			// the command is standing in (ruling R41). The message names the
			// repository it is about.
			if d.Repo != "" {
				t.Errorf("warning Repo = %q, want empty", d.Repo)
			}
			warnMessages = append(warnMessages, d.Message)
```

3. After the `if warns != 2 { … }` block, add:

```go
	// Collector.Diagnostics sorts by message after file, line and check.
	wantWarnings := []string{
		"no service.yaml found in repo-a under any configured path (services/*)",
		"no service.yaml found in repo-b under any configured path (services/*)",
	}
	if diff := cmp.Diff(wantWarnings, warnMessages); diff != "" {
		t.Errorf("warning messages mismatch (-want +got):\n%s", diff)
	}
```

In `cmd/landsraad/integration_test.go`, `TestBuildReportsADanglingRemoteRunbookInsteadOfDroppingTheRepository` is the one intended change a user sees: a two-repository build now names the repository. Replace

```go
	want := "error: service.yaml:4 [missing-file]\n" +
```

with

```go
	want := "error: edge-gateway:service.yaml:4 [missing-file]\n" +
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/diag/`
Expected: FAIL to compile, with `unknown field ShowRepo in struct literal of type Text`.

Run: `go test ./cmd/landsraad/ -run 'TestBuildReportsADanglingRemoteRunbookInsteadOfDroppingTheRepository|TestOpenReposEntryWithNoPathsAnnouncesDefaultPatterns|TestWorkspaceParseAllThenAssembleMultiRepoZeroMatchKeepsBothDiagnostics' -v`
Expected: FAIL, all three. This was observed:
- the dangling-runbook `stderr =` mismatch, still `service.yaml:4`;
- `Repo = "monorepo", want empty`;
- `warning Repo = "repo-a", want empty` and `warning Repo = "repo-b", want empty`.

- [ ] **Step 3: `Text.ShowRepo`**

In `internal/diag/format.go`, replace:

```go
// Text renders diagnostics for a human terminal.
type Text struct{}
```

with:

```go
// Text renders diagnostics for a human terminal.
//
// ShowRepo prefixes each location with the repository that holds the file —
// "edge-gateway:service.yaml:4" — when the diagnostic names one. A
// multi-repository build sets it, because there "service.yaml:4" does not say
// which of ten repositories to open (ruling R41). A single-repository run
// leaves it off, and its output is what it always was.
type Text struct {
	ShowRepo bool
}
```

and replace the opening of `Write`:

```go
func (Text) Write(w io.Writer, ds []Diagnostic) error {
	for _, d := range ds {
		if _, err := fmt.Fprintf(w, "%s: %s:%d", d.Severity, d.File, d.Line); err != nil {
			return err
		}
```

with:

```go
func (t Text) Write(w io.Writer, ds []Diagnostic) error {
	for _, d := range ds {
		loc := fmt.Sprintf("%s:%d", d.File, d.Line)
		if t.ShowRepo && d.Repo != "" {
			loc = d.Repo + ":" + loc
		}
		if _, err := fmt.Fprintf(w, "%s: %s", d.Severity, loc); err != nil {
			return err
		}
```

In `internal/diag/diag.go`, `type Diagnostic struct` becomes the form below. A comment line starts a new gofmt alignment block, so `Repo` through `Message` re-align to `Message`'s width:

```go
type Diagnostic struct {
	Severity Severity `json:"severity"`
	// Repo is the repository that holds File. Empty means the repository the
	// command is standing in, which is where repos.yaml, teams.yaml and
	// standards.yaml always are (ruling R34): a diagnostic about one of those
	// leaves it empty and names the repository it is about in its Message
	// (ruling R41).
	Repo    string `json:"repo,omitempty"`
	File    string `json:"file"`
	Line    int    `json:"line"`
	Entity  string `json:"entity,omitempty"`
	Check   string `json:"check,omitempty"`
	Message string `json:"message"`
	// Hint is an optional suggested fix, e.g. "did you mean 'team-payments'?".
	Hint string `json:"hint,omitempty"`
}
```

- [ ] **Step 4: Every terminal report says whether to show the repository**

In `cmd/landsraad/root.go`, replace:

```go
func reportDiagnostics(errOut io.Writer, ds []diag.Diagnostic) {
	if err := (diag.Text{}).Write(errOut, ds); err != nil {
```

with:

```go
func reportDiagnostics(errOut io.Writer, ds []diag.Diagnostic, showRepo bool) {
	if err := (diag.Text{ShowRepo: showRepo}).Write(errOut, ds); err != nil {
```

Update every caller. There are eight, found with `grep -rn 'reportDiagnostics(' cmd/`, test files included:
- `cmd/landsraad/build.go`: all four calls, three in `Build` and one in `newBuildCmd`'s `RunE`, gain the argument `len(w.Sources()) > 1`.
- `cmd/landsraad/serve.go`: the call in `newServeCmd`'s `RunE` gains `len(w.Sources()) > 1`.
- `cmd/landsraad/score.go`: both calls gain `false`, since `score` reads one repository.
- `cmd/landsraad/serve_test.go`: in `TestOpenReposReportsAMalformedReposYAML`, `reportDiagnostics(&errOut, c.Diagnostics())` becomes `reportDiagnostics(&errOut, c.Diagnostics(), false) // one repository: serve would pass false`.

- [ ] **Step 5: The four sites that pointed `Repo` away from `File`**

In `cmd/landsraad/gen.go`, in `parseRepo`, replace the search error's opening lines:

```go
			Severity: diag.SevError, Repo: name, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files: %v", discover.Filename, err),
```

with:

```go
			Severity: diag.SevError, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files in %s: %v", discover.Filename, repoLabel(name), err),
```

In the per-repository warning, change `Severity: diag.SevWarn, Repo: name, File: "repos.yaml", Line: 1,` to `Severity: diag.SevWarn, File: "repos.yaml", Line: 1,`. Its message already names the repository through `repoLabel`. Leave the solo error between the two alone: there, the one repository does hold `repos.yaml`.

In `cmd/landsraad/repos.go`, in `repoDefaultPatternsNote`'s doc comment, replace

```go
// pointed out, so this carries Repo and the entry's own line rather than
// reusing defaultPatternsNote's file-level Line 1.
```

with

```go
// pointed out, so this names the repository in its message and carries the
// entry's own line rather than reusing defaultPatternsNote's file-level Line
// 1. Repo stays empty: the file is repos.yaml, in the repository the command
// is standing in (ruling R41).
```

and delete the `Repo:     name,` line from its diagnostic.

In `cmd/landsraad/build.go`, delete the `Repo:     f.Repo,` line from `lastEditDiagnostic`'s diagnostic. Its message already names the repository. `TestBuildReportsAHostThatCannotAnswerDocsFresh` guards this site: it expects `warn: repos.yaml:1 [docs-fresh-unavailable]` in a multi-repository build, and leaving `Repo` set would turn that into `edge-gateway:repos.yaml:1`.

- [ ] **Step 6: Run everything and watch it pass**

Run: `gofmt -l internal/ cmd/`
Expected: no output.

Run: `go test ./internal/diag/ ./cmd/landsraad/ -run 'TestTextShowRepo|TestBuildReportsADanglingRemoteRunbook|TestOpenReposEntryWithNoPaths|TestWorkspaceParseAllThenAssemble|TestBuildReportsAHostThatCannotAnswerDocsFresh|TestOpenReposReportsAMalformedReposYAML' -v`
Expected: PASS, all of them.

Run: `task ci`
Expected: clean. This was observed.

- [ ] **Step 7: Commit**

```bash
git add internal/diag/format.go internal/diag/format_test.go internal/diag/diag.go \
  cmd/landsraad/root.go cmd/landsraad/build.go cmd/landsraad/serve.go cmd/landsraad/score.go \
  cmd/landsraad/gen.go cmd/landsraad/repos.go \
  cmd/landsraad/repos_test.go cmd/landsraad/integration_test.go cmd/landsraad/serve_test.go
git commit -m "fix: a multi-repository build says which repository a diagnostic is in"
```

---

### Task 10: A fetch failure cites the line that named the repository (R38)

This task has two test-first cycles:
- Cycle 1: a fetch failure prints its entry's line.
- Cycle 2: the docs-fresh warning points at the entry's line instead of line 1. That needs the workspace to remember each entry's line, and `WithLocal` (every `serve --watch` rebuild) to keep it.

**Files:**
- Modify: `cmd/landsraad/repos.go`:
  - `repoFailure` loses `URL`, and `Line` gets a doc comment.
  - `openRepos` stops setting `URL` and records each entry's line.
  - `workspace` gains `lines`, which `WithLocal` copies.
  - `repoEditFailure` gains `Line`, and `TakeLastEditFailures` fills it in.
- Modify: `cmd/landsraad/build.go`: `reportFetchFailures` prefixes the line, and `lastEditDiagnostic` uses it.
- Test: `cmd/landsraad/build_test.go`, `cmd/landsraad/serve_test.go`

**Interfaces:**
- Produces: `reportFetchFailures` prints `"<level>: repos.yaml:<line>: <failureMessage>"` when `repoFailure.Line > 0`, and `"<level>: <failureMessage>"` otherwise. `failureMessage` itself is unchanged; the hygiene plan's N7 task edits it and relies on this split.

#### Cycle 1: the fetch failure

- [ ] **Step 1: Write the failing expectations**

In `cmd/landsraad/build_test.go`:

1. In `TestBuildRefusesAFailedFetch`'s `repoFailure` literal, delete `URL: "https://github.com/org/edge-gateway", `. It becomes `Name: "edge-gateway", Line: 4, Kind: "github",`. Change its expected text

   ```go
   	want := "error: cannot read edge-gateway: not found. A private repository with no token " +
   ```

   to

   ```go
   	want := "error: repos.yaml:4: cannot read edge-gateway: not found. A private repository with no token " +
   ```

2. In `TestBuildAllowPartialNamesTheFailures`, replace the two literals

   ```go
   			{Name: "edge-gateway", URL: "https://github.com/org/edge-gateway", Line: 4, Err: errors.New("boom")},
   			{Name: "billing", URL: "https://gitlab.com/org/billing", Line: 7, Err: errors.New("boom")},
   ```

   with

   ```go
   			{Name: "edge-gateway", Line: 4, Err: errors.New("boom")},
   			{Name: "billing", Line: 7, Err: errors.New("boom")},
   ```

   and its expectation

   ```go
   	wantWarnings := "warn: cannot read billing: boom\n" +
   		"warn: cannot read edge-gateway: boom\n"
   ```

   with

   ```go
   	wantWarnings := "warn: repos.yaml:7: cannot read billing: boom\n" +
   		"warn: repos.yaml:4: cannot read edge-gateway: boom\n"
   ```

3. In `TestFailureMessage`'s table, delete `URL: "https://git.example.com/org/edge",` from the two `repoFailure` literals that set it, in the "self-hosted gitlab…" and "unknown kind…" rows. `failureMessage` never read it.

The literals in `TestBuildAllowPartialWithEveryRepositoryFailedExplainsItself`, `TestReportFetchFailuresRefusalTrailerIsPlural` and `serve_test.go`'s `TestBothCommandsHaveTheFlagTheRefusalTrailerAdvises` set no `Line`, and their output does not change.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./cmd/landsraad/ -run 'TestBuildRefusesAFailedFetch|TestBuildAllowPartial' -v`
Expected, as observed:
- `TestBuildRefusesAFailedFetch` and `TestBuildAllowPartialNamesTheFailures` FAIL with `stderr =` mismatches, because the prefix is missing.
- `TestBuildAllowPartialWithEveryRepositoryFailedExplainsItself` PASSES. Its literals have no line.

- [ ] **Step 3: Print the line; drop the field nothing read**

In `cmd/landsraad/repos.go`, in `type repoFailure struct`, replace

```go
	Name string
	URL  string
	Line int
```

with

```go
	Name string
	// Line is where the entry that named this repository sits in
	// repos.yaml. reportFetchFailures prints it, so a failure points at the
	// entry to fix rather than at the top of the file (ruling R38).
	Line int
```

In `openRepos`, the `fail` closure's literal `repoFailure{Name: name, URL: r.URL, Line: r.Line, Kind: kind, Err: err}` becomes `repoFailure{Name: name, Line: r.Line, Kind: kind, Err: err}`.

In `cmd/landsraad/build.go`, in `reportFetchFailures`, replace

```go
	for _, f := range sorted {
		fmt.Fprintf(errOut, "%s: %s\n", level, failureMessage(f))
	}
```

with

```go
	for _, f := range sorted {
		// The entry that named the repository, when the failure knows it:
		// config.Repo.Line exists so that a fetch failure points at the line
		// to fix rather than at the top of the file (ruling R38).
		if f.Line > 0 {
			fmt.Fprintf(errOut, "%s: repos.yaml:%d: %s\n", level, f.Line, failureMessage(f))
			continue
		}
		fmt.Fprintf(errOut, "%s: %s\n", level, failureMessage(f))
	}
```

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./cmd/landsraad/ -run 'TestBuildRefusesAFailedFetch|TestBuildAllowPartial|TestFailureMessage|TestReportFetchFailures|TestBothCommands' -v`
Expected: PASS, all of them.

#### Cycle 2: the docs-fresh warning

- [ ] **Step 5: Write the failing tests**

In `cmd/landsraad/build_test.go`, `TestBuildReportsAHostThatCannotAnswerDocsFresh` builds its workspace by hand. Give it the entries' lines by adding this field after `local:    "platform",`:

```go
		// Where each entry sits in repos.yaml: the warning points at the one
		// that named the repository whose host failed (ruling R38).
		lines: map[string]int{"platform": 2, "edge-gateway": 5},
```

and change

```go
	wantDiag := "warn: repos.yaml:1 [docs-fresh-unavailable]\n" +
```

to

```go
	wantDiag := "warn: repos.yaml:5 [docs-fresh-unavailable]\n" +
```

Append to `cmd/landsraad/serve_test.go`:

```go
// serve --watch builds every rebuild from WithLocal's copy of the startup
// workspace. A copy that dropped lines would put every docs-fresh warning
// after the first rebuild back at an entry's line 0 (ruling R38).
func TestWithLocalKeepsEachRepositorysLine(t *testing.T) {
	w := &workspace{
		sources: catalog.Sources{"platform": fstest.MapFS{}},
		local:   "platform",
		lines:   map[string]int{"platform": 2, "edge-gateway": 5},
		edits:   newLastEditLog(),
	}
	rebuilt := w.WithLocal(fstest.MapFS{})
	rebuilt.RecordLastEditFailure("edge-gateway", errors.New("boom"))

	got := rebuilt.TakeLastEditFailures()
	if len(got) != 1 || got[0].Repo != "edge-gateway" || got[0].Line != 5 {
		t.Errorf("TakeLastEditFailures = %+v, want edge-gateway at line 5", got)
	}
}
```

- [ ] **Step 6: Run them and watch them fail**

Run: `go test ./cmd/landsraad/ -run 'TestBuildReportsAHostThatCannotAnswerDocsFresh|TestWithLocalKeepsEachRepositorysLine' -v`
Expected: FAIL to compile, with `unknown field lines in struct literal of type workspace` and `got[0].Line undefined (type repoEditFailure has no field or method Line)`.

- [ ] **Step 7: The workspace remembers each entry's line**

In `cmd/landsraad/repos.go`:

1. In `type workspace struct`, after the `fetchers` field, add:

```go
	// lines is where each repository's entry sits in repos.yaml, so a
	// diagnostic about a repository points at the entry that named it
	// (ruling R38).
	lines map[string]int
```

2. Replace `TakeLastEditFailures` with:

```go
func (w *workspace) TakeLastEditFailures() []repoEditFailure {
	out := w.edits.take()
	for i := range out {
		out[i].Line = w.lines[out[i].Repo]
	}
	return out
}
```

(`out`, not `fs`: this file imports `io/fs`.)

3. In `type repoEditFailure struct`, between `Repo` and `Err`, add:

```go
	// Line is the repository's entry in repos.yaml, filled in by
	// TakeLastEditFailures from the workspace that knows it.
	Line int
```

4. In `WithLocal`, change `fetchers: w.fetchers,` to `fetchers: w.fetchers, lines: w.lines,`.

5. In `openRepos`, the main workspace literal (the one with `edits:    newLastEditLog(),` after the defaults) gains `lines:    map[string]int{},` before `edits`. In the loop, directly after `name := r.Identity()`, add:

```go
		w.lines[name] = r.Line
```

In `cmd/landsraad/build.go`, in `lastEditDiagnostic`, change `Line:     1,` to `Line:     f.Line,`. There is no fallback to 1. Every entry `openRepos` records has a line of at least 1 (`urlLine` never returns 0, and the synthetic local entry uses 1), and a diagnostic with Line 0 is a bug the output should show, not hide.

- [ ] **Step 8: Run everything and watch it pass**

Run: `go test ./cmd/landsraad/ -run 'TestBuildReportsAHostThatCannotAnswerDocsFresh|TestWithLocalKeepsEachRepositorysLine' -v`
Expected: PASS, both.

Run: `task ci`
Expected: clean. This was observed.

- [ ] **Step 9: Commit**

```bash
git add cmd/landsraad/repos.go cmd/landsraad/build.go cmd/landsraad/build_test.go cmd/landsraad/serve_test.go
git commit -m "fix: a repository's failures point at the repos.yaml line that named it"
```

---

### Task 11: The local repository has one name (R39)

R39's other half is that `local: true` without a `url:` stays rejected. That needs no change, and the table test in `internal/config` already pins the `repos-url` rule.

**Files:**
- Modify: `cmd/landsraad/validate.go`: `localRepoName`, plus removing the `"path"` import that only it used.
- Test: `cmd/landsraad/validate_test.go`

**Interfaces:**
- Consumes: `(*config.Repos).LocalRepo()` and `(*config.Repo).Identity()`, both unchanged.

- [ ] **Step 1: Write the failing test**

Append to `cmd/landsraad/validate_test.go`:

```go
// Ruling R39: validate, gen and score name the repository they stand in by
// the entry LocalPatterns reads its paths from — the one marked local: true,
// or else the first — and by that entry's Identity(), as build does.
// localRepoName used to take the first entry's url basename whatever local:
// and name: said, and kept a trailing .git that Identity() trims.
func TestLocalRepoNameIsTheLocalEntrysIdentity(t *testing.T) {
	for _, tt := range []struct{ name, reposYAML, want string }{
		{"no repos.yaml", "", ""},
		{"sole entry", "repos:\n  - url: https://github.com/org/monorepo\n", "monorepo"},
		{"sole entry with .git", "repos:\n  - url: https://github.com/org/monorepo.git\n", "monorepo"},
		{"local entry is not first",
			"repos:\n  - url: https://github.com/org/edge-gateway\n  - url: https://github.com/org/platform\n    local: true\n",
			"platform"},
		{"name: wins over the url",
			"repos:\n  - url: https://github.com/org/platform\n    local: true\n    name: core\n",
			"core"},
		{"none marked: the first, as LocalPatterns assumes",
			"repos:\n  - url: https://github.com/org/platform\n  - url: https://github.com/org/edge-gateway\n",
			"platform"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			if tt.reposYAML != "" {
				fsys["repos.yaml"] = &fstest.MapFile{Data: []byte(tt.reposYAML)}
			}
			if got := localRepoName(fsys); got != tt.want {
				t.Errorf("localRepoName = %q, want %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./cmd/landsraad/ -run 'TestLocalRepoNameIsTheLocalEntrysIdentity' -v`
Expected: three subtests FAIL and three PASS. This was observed:
- `sole entry with .git`: `localRepoName = "monorepo.git", want "monorepo"`
- `local entry is not first`: `localRepoName = "edge-gateway", want "platform"`
- `name: wins over the url`: `localRepoName = "platform", want "core"`

- [ ] **Step 3: Name the entry `LocalPatterns` reads**

In `cmd/landsraad/validate.go`, replace `localRepoName`, including its doc comment, with:

```go
// localRepoName names the repo being validated, for provenance in diagnostics.
// It is empty when repos.yaml is absent, and Entity.Location() renders that
// case without a dangling prefix.
//
// It names the entry LocalPatterns reads the paths from — the one marked
// local: true, or else the first — by that entry's Identity(), which honours
// name: and trims .git. It used to take the first entry's url basename
// whatever local: and name: said, so validate, gen and score could name the
// repository they stand in differently from build (ruling R39).
func localRepoName(fsys fs.FS) string {
	data, err := fs.ReadFile(fsys, "repos.yaml")
	if err != nil {
		return ""
	}
	var discard diag.Collector
	r := config.LoadRepos("repos.yaml", data, &discard)
	if local, ok := r.LocalRepo(); ok {
		return local.Identity()
	}
	if len(r.Repos) == 0 {
		return ""
	}
	return r.Repos[0].Identity()
}
```

`path.Base` was the file's only use of `"path"`. Delete `"path"` from `validate.go`'s import block, or the package fails to compile with `"path" imported and not used`. `"strings"` stays, because `Validate` still calls `strings.Join`.

- [ ] **Step 4: Run everything and watch it pass**

Run: `go test ./cmd/landsraad/ -run 'TestLocalRepoNameIsTheLocalEntrysIdentity|TestValidateThreadsTheRepoName' -v`
Expected: PASS. `TestValidateThreadsTheRepoNameIntoSchemaDiagnostics` still sees `monorepo`: `genFS()`'s one entry gives the same name either way.

Run: `task ci`
Expected: clean. This was observed.

- [ ] **Step 5: Commit**

```bash
git add cmd/landsraad/validate.go cmd/landsraad/validate_test.go
git commit -m "fix: validate, gen and score name the local repository the way build does"
```

---

### Task 12: No retry that cannot succeed (R44)

**Files:**
- Modify: `internal/fetch/client.go`:
  - `ClientOptions` and `Client` gain a clock, and `NewClient` defaults it.
  - `Get` asks a new `tooEarlyToRetry` before sleeping.
- Test: `internal/fetch/client_test.go`, which gains the `"strconv"` and `go-cmp` imports.

**Interfaces:**
- Produces: `ClientOptions.Now func() time.Time`, which defaults to `time.Now`. The hygiene plan's `maxAttempts` const task sits beside it. `cmd/` needs no change, because production uses the real clock.

- [ ] **Step 1: Write the failing test**

In `internal/fetch/client_test.go`, replace the import block's tail

```go
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)
```

with

```go
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)
```

and append:

```go
// Ruling R44. A 403 for a spent quota names when the quota returns, and a
// retry that fires before then cannot succeed: it spends a request, and the
// build's time, to be told the same thing. The client used to retry at 1s and
// 2s regardless. A reset inside the backoff is still worth waiting for, and a
// host that sends Retry-After is still obeyed.
func TestClientDoesNotRetryARateLimitBeforeItResets(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name      string
		headers   map[string]string
		wantCalls int
		wantSleep []time.Duration
	}{
		{
			name: "reset an hour away: one call, no sleeps",
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
			},
			wantCalls: 1,
		},
		{
			name: "reset inside the backoff: retried as before",
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(now.Unix(), 10),
			},
			wantCalls: 3, wantSleep: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name:      "no reset named: retried as before",
			headers:   map[string]string{"X-RateLimit-Remaining": "0"},
			wantCalls: 3, wantSleep: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name: "Retry-After is obeyed whatever the reset says",
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
				"Retry-After":           "7",
			},
			wantCalls: 3, wantSleep: []time.Duration{7 * time.Second, 7 * time.Second},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(http.StatusForbidden)
			}))
			t.Cleanup(srv.Close)
			var slept []time.Duration
			c := NewClient(ClientOptions{
				HTTP: srv.Client(), BaseURL: srv.URL, MaxAttempts: 3,
				Sleep: func(d time.Duration) { slept = append(slept, d) },
				Now:   func() time.Time { return now },
			})

			_, _, err := c.Get(context.Background(), "/repos/o/r", nil, "")

			if !IsRateLimited(err) {
				t.Fatalf("err = %v, want the rate limit returned", err)
			}
			if calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tt.wantCalls)
			}
			if diff := cmp.Diff(tt.wantSleep, slept); diff != "" {
				t.Errorf("sleeps mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/fetch/ -run 'TestClientDoesNotRetryARateLimitBeforeItResets' -v`
Expected: FAIL to compile, with `unknown field Now in struct literal of type ClientOptions`.

The behaviour this pins was also observed against the unmodified client, with the `Now:` line left out. Only `reset an hour away` failed there, with `calls = 3, want 1` and sleeps `{1s, 2s}`. The other three subtests already pass today, and they are what keep this change from touching anything else.

- [ ] **Step 3: A clock, and the check before sleeping**

In `internal/fetch/client.go`:

1. In `ClientOptions`, after the `Sleep` field, add:

```go
	// Now is the client's clock, read only to judge whether a retry could
	// outlast a spent rate limit (ruling R44). Injected for the reason Sleep
	// is, and defaulted the same way.
	Now func() time.Time
```

2. In `Client`, after `sleep       func(time.Duration)`, add `now         func() time.Time`.

3. In `NewClient`, the struct literal's last line `headers: o.Headers, maxAttempts: o.MaxAttempts, sleep: o.Sleep,` becomes `headers: o.Headers, maxAttempts: o.MaxAttempts, sleep: o.Sleep, now: o.Now,`. After the `if c.sleep == nil { … }` block, add:

```go
	if c.now == nil {
		c.now = time.Now
	}
```

4. In `Get`, replace:

```go
		last = err
		if !retryable(err) || attempt == c.maxAttempts {
			return nil, nil, err
		}
		c.sleep(backoff(attempt, err))
```

with:

```go
		last = err
		if !retryable(err) || attempt == c.maxAttempts {
			return nil, nil, err
		}
		wait := backoff(attempt, err)
		if c.tooEarlyToRetry(err, wait) {
			return nil, nil, err
		}
		c.sleep(wait)
```

5. Directly above `backoff`'s doc comment, add:

```go
// tooEarlyToRetry reports a spent rate limit that waiting wait cannot
// outlast: the host named when its quota returns, and a retry after wait
// would still come before that. Retrying then spends a request, and the
// build's time, to be told the same thing, and failureMessage already names
// the reset time, which is the advice that helps (ruling R44).
//
// A host that sent Retry-After is obeyed instead: it said how long to wait,
// and backoff already waits exactly that.
func (c *Client) tooEarlyToRetry(err error, wait time.Duration) bool {
	var se *StatusError
	if !IsRateLimited(err) || !errors.As(err, &se) || se.RetryAfter > 0 || se.RateReset.IsZero() {
		return false
	}
	return c.now().Add(wait).Before(se.RateReset)
}
```

- [ ] **Step 4: Run everything and watch it pass**

Run: `go test -race ./internal/fetch/ -run 'TestClient|TestGetDoesNotRetry' -v`
Expected: PASS, including every existing client test.

Run: `task ci`
Expected: clean, with no data races reported. This was observed.

- [ ] **Step 5: Commit**

```bash
git add internal/fetch/client.go internal/fetch/client_test.go
git commit -m "fix(fetch): a rate limit is not retried before its quota can have returned"
```

---

### Task 13: Checkpoint before the hygiene plan

No new behaviour. This task proves the branch is in the state the hygiene plan (`docs/superpowers/plans/2026-09-11-carryall-followups-hygiene.md`) assumes, and that nothing refers to what this plan removed.

**Files:** none unless Step 2 finds something.

- [ ] **Step 1: The whole suite, as CI runs it**

Run: `task ci`
Expected: lint clean, and every package `ok` under `-race`.

- [ ] **Step 2: Nothing still names what this plan removed or renamed**

Run: `grep -rn 'teams-missing\|listPath\|repoFailure{[^}]*URL\|MaxAttempts: *[0-9]' --include='*.go' . | grep -v '_test.go:.*MaxAttempts'`
Expected: exactly one line, the comment in Task 7's `TestLoadCatalogReportsMissingTeamsLikeValidate` that names the old id on purpose. Anything else is a leftover:
- `teams-missing` became `missing-teams` in Task 7.
- `listPath` became `list` in Task 3, which also rewrote the one comment that named it.
- `repoFailure.URL` was deleted in Task 10.
- `MaxAttempts` in tests is the hygiene plan's to change, so the grep excludes it.

Run: `grep -n '"\." listed\|marks "\." listed\|only its root listed' internal/fetch/*.go cmd/landsraad/*.go`
Expected: exactly two lines, both `t.Error` messages in Task 4's `TestNewFSListsNothing` and `TestFromEntriesListsTheRoot`. `fs.go`'s old `NewFS` doc ("only its root listed") and `docsDirs`' old paragraph ("marks \".\" listed regardless") are gone, because Task 4 replaced both.

- [ ] **Step 3: The project's own claims still hold**

`CLAUDE.md`'s rule table and "Project shape" section make no claim about exit codes, `NewFS` or check ids, so nothing there changes. Confirm it, because `composition-auditor` will:

Run: `grep -n 'exitUsage\|exit code\|NewFS\|teams-missing\|missing-teams' CLAUDE.md`
Expected: only the "one-way door" line that lists exit codes in general.

- [ ] **Step 4: Hand over**

Nothing to commit if Steps 1–3 were clean. Continue with the hygiene plan on the same branch. Its last task closes the follow-ups file and runs `composition-auditor` before merge.
