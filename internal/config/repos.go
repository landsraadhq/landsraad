package config

import (
	"bytes"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Repo is one entry in repos.yaml.
type Repo struct {
	URL   string   `yaml:"url"`
	Paths []string `yaml:"paths"`
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
	return r
}

// LocalPatterns returns the glob patterns for the first repo entry, which by
// convention is the repository the command is running in. `landsraad validate`
// is hermetic and never fetches the others; that is the platform build's job.
//
// defaulted reports that the file named no paths and DefaultPatterns are in
// use. It is a return value rather than a diagnostic because this package
// takes no collector here, and a bool the caller must assign is harder to
// forget than a fallback it cannot see: spec §12, degraded mode must be
// visible in the artifact.
func (r *Repos) LocalPatterns() (patterns []string, defaulted bool) {
	if len(r.Repos) == 0 || len(r.Repos[0].Paths) == 0 {
		return DefaultPatterns(), true
	}
	return r.Repos[0].Paths, false
}
