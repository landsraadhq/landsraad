package config

import (
	"bytes"
	"fmt"
	"io"
	"net/url"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
)

// Repo is one entry in repos.yaml.
//
// Local, Ref, Host and Name arrived with Plan 4 and are all optional, so a
// repos.yaml written before them still loads. Ruling R26 records why each
// one is a stated key rather than something inferred: every inference
// available here — the first entry is local, the hostname names the host,
// the basename is unique — is true right up until it is not, and each has a
// failure that is silent rather than loud.
type Repo struct {
	URL   string   `yaml:"url"`
	Paths []string `yaml:"paths"`
	// Local marks the repository the command is standing in. At most one
	// entry may set it; see LocalPatterns for what happens when none does.
	Local bool `yaml:"local"`
	// Ref is a branch or tag. Empty means "ask the host for its default
	// branch", which costs one request and is honest — assuming "main"
	// fetches nothing from every repository that still uses "master" and
	// reports it as a missing repository.
	Ref string `yaml:"ref"`
	// Host is "github" or "gitlab", needed only when the hostname does not
	// say (a self-hosted instance). See HostKinds.
	Host string `yaml:"host"`
	// Name is this repository's identity: the string in every diagnostic's
	// Repo field, the key in catalog.Sources, and the value of
	// Entity.SourceRepo. Empty means Identity derives it from the URL.
	Name string `yaml:"name"`

	// Line is where this entry's `url:` appears in repos.yaml. Not a YAML
	// field: it is provenance, filled in by attachLines, and it exists so a
	// fetch failure can point at the line that named the repository rather
	// than at the top of the file.
	Line int `yaml:"-"`

	// pathsRejected is set by validateRepos when paths: named patterns and
	// repos-path rejected every one. See PathsRejected.
	pathsRejected bool
}

// Repos is the loaded repos.yaml.
type Repos struct {
	Repos []Repo `yaml:"repos"`
	// loaded is false when the file had syntax or unknown-key errors, mirroring
	// Teams.loaded. A caller uses it to avoid stacking a second diagnostic onto
	// a failure that has already been reported loudly.
	loaded bool
}

// Loaded reports whether repos.yaml parsed. A Repos that did not parse still
// yields DefaultPatterns, so callers need no nil checks, but the fallback is
// a consequence of the error rather than a separate thing to announce.
func (r *Repos) Loaded() bool { return r.loaded }

// defaultPatterns is where entities live when repos.yaml says nothing. Kept
// unexported and array-shaped for the reason in catalog.allKinds: an exported
// mutable slice is state any importer, or any test in the same process, can
// rewrite underneath everything else.
//
// "." matches a service.yaml at the repository root — the single-service repo
// shape in spec §5.1. Without it such a repo validates zero entities and
// exits 0, which is the worst possible first run.
var defaultPatterns = [...]string{".", "services/*", "workers/*", "libs/*", "topics/*"}

// DefaultPatterns returns the conventional layout, fresh on each call.
func DefaultPatterns() []string {
	out := make([]string, len(defaultPatterns))
	copy(out, defaultPatterns[:])
	return out
}

// hostKinds are the hosts landsraad can fetch from (decision D5).
//
// Array-shaped and unexported for the reason in catalog.allKinds: an
// exported mutable slice is state any importer can rewrite underneath
// everything else.
var hostKinds = [...]string{"github", "gitlab"}

// HostKinds returns the supported hosts, fresh on each call.
func HostKinds() []string {
	out := make([]string, len(hostKinds))
	copy(out, hostKinds[:])
	return out
}

// Identity is the repository's name in diagnostics and in catalog.Sources.
//
// The basename rather than owner/repo, deliberately: spec §12's worked
// example prints "monorepo services/api/service.yaml:4", and lengthening
// every diagnostic to buy uniqueness is the wrong trade when uniqueness can
// be bought by rejecting the collision instead. LoadRepos does exactly that.
//
// Empty when there is neither a Name nor a URL to derive one from — the
// synthetic single-local-repository entry loadReposFile builds when there
// is no repos.yaml at all. path.Base("") is ".", which is not a name; using
// it would make Entity.Location() print ".:services/api/service.yaml"
// instead of the bare path Location() exists to produce for exactly this
// case.
func (r *Repo) Identity() string {
	if r.Name != "" {
		return r.Name
	}
	u := strings.TrimSuffix(r.URL, "/")
	u = strings.TrimSuffix(u, ".git")
	if u == "" {
		return ""
	}
	return path.Base(u)
}

// PathsRejected reports whether paths: named patterns and repos-path rejected
// every one (ruling R36). Such an entry falls back to DefaultPatterns like one
// that named none, but a caller announcing that fallback stays silent for it:
// the rejection is the diagnostic and the fallback is its consequence, the
// rule patternsFor already applies to a repos.yaml that failed to parse.
func (r *Repo) PathsRejected() bool { return r.pathsRejected }

// HostKind reports which adapter fetches this repository.
//
// known is false when the hostname is not one landsraad recognises and the
// entry did not say. That is a question, not a default: guessing github for
// a self-hosted GitLab produces 404s from an API that was never there.
func (r *Repo) HostKind() (kind string, known bool) {
	if r.Host != "" {
		return r.Host, slices.Contains(hostKinds[:], r.Host)
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Hostname()) {
	case "github.com":
		return "github", true
	case "gitlab.com":
		return "gitlab", true
	}
	return "", false
}

// LocalSource says how LocalPatterns decided which entry is local, so the
// caller can report an assumption rather than making one silently
// (spec §12: degraded mode is visible in the artifact, not only in a log).
type LocalSource int

const (
	// LocalMarked: an entry said `local: true`, or there is exactly one
	// entry and it is unambiguous.
	LocalMarked LocalSource = iota
	// LocalAssumedFirst: several entries, none marked, so the first was
	// used — which is what repos.yaml meant before `local:` existed.
	LocalAssumedFirst
	// LocalDefaulted: no entry named any paths, so DefaultPatterns are in use.
	LocalDefaulted
	// LocalRejected: the local entry named paths and repos-path rejected every
	// one, so DefaultPatterns are in use. Not a separate thing to announce;
	// see Repo.PathsRejected.
	LocalRejected
)

// LocalRepo returns the entry marked `local: true`, or the only entry when
// there is exactly one. ok is false when neither applies.
func (r *Repos) LocalRepo() (*Repo, bool) {
	for i := range r.Repos {
		if r.Repos[i].Local {
			return &r.Repos[i], true
		}
	}
	if len(r.Repos) == 1 {
		return &r.Repos[0], true
	}
	return nil, false
}

// LocalPatterns returns the glob patterns for the local repository.
//
// The second return replaced a bool in Plan 4. Two of its three states used
// to be one: "no repos.yaml, so DefaultPatterns" and "three entries and no
// idea which one you are standing in" both reported `defaulted`, and the
// second is the condition that stamped a banner naming the wrong two
// repositories into every page of a generated site.
func (r *Repos) LocalPatterns() ([]string, LocalSource) {
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
}

// reposParseHint is the same advice whatever went wrong with the file.
const reposParseHint = "repos.yaml is a list under `repos:`, each entry with url and paths"

// LoadRepos reads repos.yaml, always returning a usable value.
//
// KnownFields(true) rejects unknown keys, as LoadTeams does. The argument is
// stronger here: teams.yaml decides where alerts are routed, but repos.yaml
// decides what the tool examines at all. `path:` for `paths:` used to fall
// through to DefaultPatterns, which cover services/*, so a repository whose
// services live in apps/ validated nothing, found no problems, and exited 0.
func LoadRepos(path string, data []byte, c *diag.Collector) *Repos {
	r := &Repos{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(r); err != nil && err != io.EOF {
		for _, d := range yamlDiagnostics(path, "repos-parse", "repos file", reposParseHint, err) {
			c.Add(d)
		}
		return &Repos{}
	}
	r.loaded = true
	attachLines(data, r)
	validateRepos(path, r, c)
	return r
}

// attachLines records where each entry's `url:` key appears.
//
// A second pass over the same bytes, deliberately. yaml.Node decoding does
// not honour KnownFields, and the strict decode above is load-bearing — a
// `path:` typo for `paths:` used to fall through to DefaultPatterns and
// validate an entire repository's worth of nothing. So the strict decode
// stays exactly as it was, and this pass does one job: provenance. Two
// passes over a file with a handful of entries is not worth a cleverer
// scheme.
func attachLines(data []byte, r *Repos) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "repos" {
			continue
		}
		seq := root.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return
		}
		for j, item := range seq.Content {
			if j >= len(r.Repos) {
				return
			}
			r.Repos[j].Line = urlLine(item)
		}
		return
	}
}

// urlLine is the line of an entry's `url:` key, falling back to the line the
// entry itself starts on. Never zero for a node that parsed: a Line of 0 is
// a bug, and the Global Constraints say to use 1 when there is nothing
// better — but here there always is.
func urlLine(item *yaml.Node) int {
	if item.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(item.Content); i += 2 {
			if item.Content[i].Value == "url" {
				return item.Content[i].Line
			}
		}
	}
	if item.Line == 0 {
		return 1
	}
	return item.Line
}

// validateRepos reports the configuration mistakes that would otherwise
// surface as a fetch failure against a host that was never going to answer.
//
// Every check here runs in `validate` too, which never fetches anything.
// That is deliberate: repos.yaml is configuration, and a malformed entry is
// wrong in the PR that introduced it rather than days later in the platform
// build. It is the same argument spec §7.1 makes for checking the shape of
// .landsraad/checks files hermetically.
func validateRepos(file string, r *Repos, c *diag.Collector) {
	// firstLocal and local both answer "which entry is local", by design for
	// two different checks that cannot share one answer. firstLocal tracks
	// only entries that said local: true, in loop order, so the *second* one
	// is reportable as a duplicate marker the moment it's seen — LocalRepo()
	// has no such notion of "second". local is LocalRepo()'s own answer,
	// including its sole-entry case, computed once before the loop because
	// the host check below needs to know the effective local entry even when
	// nothing in the file says so explicitly.
	var firstLocal *Repo
	byName := map[string]*Repo{}
	local, _ := r.LocalRepo()
	for i := range r.Repos {
		e := &r.Repos[i]

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

		if !strings.HasPrefix(e.URL, "https://") {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: file, Line: e.Line,
				Check:   "repos-url",
				Message: fmt.Sprintf("repository url must begin with https://, got %q", e.URL),
				Hint:    httpsHint(e.URL),
			})
			continue
		}

		if e.Local {
			if firstLocal != nil {
				c.Add(diag.Diagnostic{
					Severity: diag.SevError, File: file, Line: e.Line,
					Check: "repos-local",
					Message: fmt.Sprintf(
						"two entries in repos.yaml are marked local: true — %q (line %d) and %q (line %d)",
						firstLocal.Identity(), firstLocal.Line, e.Identity(), e.Line),
					Hint: "exactly one entry is the repository you are standing in; remove local: true from the other",
				})
				continue
			}
			firstLocal = e
		}

		if prev, dup := byName[e.Identity()]; dup {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: file, Line: e.Line,
				Check: "repos-duplicate-name",
				Message: fmt.Sprintf(
					"two repositories resolve to the name %q: %s (line %d) and %s (line %d)",
					e.Identity(), prev.URL, prev.Line, e.URL, e.Line),
				Hint: "the name is the last path segment of the url unless you set name:; give one of them an explicit name:",
			})
			continue
		}
		byName[e.Identity()] = e

		// Two different mistakes, two different rules. A stated host: value
		// that landsraad does not support is wrong regardless of whether
		// this entry is ever fetched — the user typed something the tool
		// does not accept, and that's true of the local repository too.
		// An un-inferable hostname with no host: key is not the same kind
		// of problem: it only matters for an entry that will actually be
		// fetched, so the local repository — read from disk, never
		// fetched — is exempt from it. "The local one" is whatever
		// LocalRepo() says it is, computed once above: an unmarked sole
		// entry is exempt for the same reason an entry marked local: true
		// is.
		if e.Host != "" && !slices.Contains(hostKinds[:], e.Host) {
			c.Add(hostDiagnostic(file, e))
			continue
		}
		if e == local {
			continue
		}
		if _, known := e.HostKind(); !known {
			c.Add(hostDiagnostic(file, e))
		}
	}
}

// hostDiagnostic distinguishes "you named a host landsraad does not support"
// from "landsraad cannot tell what this is". They are different mistakes
// with different fixes, and one message for both would be wrong for one of
// them: telling somebody with a self-hosted GitLab that "gitlab" is not a
// valid host is a false statement about their configuration.
func hostDiagnostic(file string, e *Repo) diag.Diagnostic {
	if e.Host != "" {
		return diag.Diagnostic{
			Severity: diag.SevError, File: file, Line: e.Line,
			Check:   "repos-host",
			Message: fmt.Sprintf("unknown host %q for %s", e.Host, e.URL),
			Hint: fmt.Sprintf("host must be %s; landsraad v1 supports no others (design decision D5)",
				strings.Join(HostKinds(), " or ")),
		}
	}
	return diag.Diagnostic{
		Severity: diag.SevError, File: file, Line: e.Line,
		Check:   "repos-host",
		Message: fmt.Sprintf("cannot tell which host %s is", e.URL),
		Hint:    "add host: github or host: gitlab to this entry",
	}
}

// httpsHint rewrites the url the user actually wrote, when it can. An ssh
// remote is what `git remote -v` prints and what people paste, so "must
// begin with https://" alone would make them work out the translation.
func httpsHint(raw string) string {
	if at := strings.Index(raw, "@"); at >= 0 && !strings.Contains(raw, "://") {
		rest := raw[at+1:]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			host := rest[:colon]
			p := strings.TrimSuffix(strings.TrimPrefix(rest[colon+1:], "/"), ".git")
			return fmt.Sprintf("write it as https://%s/%s", host, p)
		}
	}
	return "write it as https://<host>/<owner>/<repo>"
}
