package config

import (
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
}

// DefaultPatterns is where entities live when repos.yaml says nothing.
//
// "." matches a service.yaml at the repository root — the single-service repo
// shape in spec §5.1. Without it such a repo validates zero entities and
// exits 0, which is the worst possible first run.
var DefaultPatterns = []string{".", "services/*", "workers/*", "libs/*", "topics/*"}

// reposParseHint is the same advice whatever went wrong with the file.
const reposParseHint = "repos.yaml is a list under `repos:`, each entry with url and paths"

// LoadRepos reads repos.yaml, always returning a usable value.
func LoadRepos(path string, data []byte, c *diag.Collector) *Repos {
	r := &Repos{}
	if err := yaml.Unmarshal(data, r); err != nil {
		for _, d := range yamlDiagnostics(path, "repos-parse", "repos file", reposParseHint, err) {
			c.Add(d)
		}
		return &Repos{}
	}
	return r
}

// LocalPatterns returns the glob patterns for the first repo entry, which by
// convention is the repository the command is running in. `landsraad validate`
// is hermetic and never fetches the others; that is the platform build's job.
func (r *Repos) LocalPatterns() []string {
	if len(r.Repos) == 0 || len(r.Repos[0].Paths) == 0 {
		return DefaultPatterns
	}
	return r.Repos[0].Paths
}
