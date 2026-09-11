package scorecard

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/schema"
)

//go:embed ingest.schema.json
var CheckResultsSchema []byte

// ChecksDir is where CI jobs write their results.
const ChecksDir = ".landsraad/checks"

// IsCheckResultsFile reports whether name is a check-results file by
// extension. validate and Ingest must agree on this predicate: a divergence
// means a file validate accepts as well-formed silently vanishes from score,
// or vice versa.
func IsCheckResultsFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// Reported is one ingested result with the provenance needed to resolve
// precedence and to name a producer in a diagnostic.
type Reported struct {
	Result      Result
	GeneratedAt time.Time
	Producer    string
	SourceFile  string
	// SourceRepo is the repository the CheckResults file came from. With
	// R31 the file may live somewhere other than the entity it reports on,
	// so the path alone no longer identifies it.
	SourceRepo string
}

// Location renders where a reported result came from, matching
// Entity.Location: "repo:path" when the repository is known, "path" when it
// is not.
func (r Reported) Location() string {
	if r.SourceRepo == "" {
		return r.SourceFile
	}
	return r.SourceRepo + ":" + r.SourceFile
}

type checkResultsFile struct {
	APIVersion  string `yaml:"apiVersion"`
	Kind        string `yaml:"kind"`
	Producer    string `yaml:"producer"`
	GeneratedAt string `yaml:"generatedAt"`
	Results     []struct {
		Entity string `yaml:"entity"`
		Check  string `yaml:"check"`
		Status string `yaml:"status"`
		Detail string `yaml:"detail"`
		URL    string `yaml:"url"`
	} `yaml:"results"`
}

// Ingest is pipeline stage 6: read every results file in every repository,
// resolve entities, apply precedence, and age out results older than
// staleAfterDays.
//
// Spec §6 fixes three rules here, because this file is written by CI jobs in
// other people's repositories and is the most expensive contract in the
// product to change later:
//
//   - entity is a ref; a bare name is a deprecated alias resolved only when
//     unambiguous
//   - generatedAt is RFC 3339 with an explicit offset
//   - newest generatedAt wins, and a tie is an error rather than a coin flip
//
// Ruling R31: a CheckResults file in one repository may report on an entity
// defined in another, because a platform repository running one image-scan
// job for every service is the natural shape and forbidding it would mean a
// CI job may only report on entities it happens to sit beside. Spec §6
// describes the file without saying whose entities it may name; this is the
// answer.
//
// An absent directory is not an error. A repository reporting no external
// results is normal, and every external check then renders not-reported —
// which is visible in the scorecard rather than hidden.
func Ingest(src catalog.Sources, cat *catalog.Catalog, staleAfterDays int, now time.Time, c *diag.Collector) map[catalog.Ref]map[string]Reported {
	out := map[catalog.Ref]map[string]Reported{}
	// Sorted, via Sources.Names, so precedence and every tie diagnostic read
	// the same on every run regardless of map iteration order.
	for _, repo := range src.Names() {
		fsys, ok := src.Get(repo)
		if !ok {
			continue
		}
		ingestRepo(repo, fsys, cat, out, c)
	}
	applyStaleness(out, staleAfterDays, now)
	return out
}

// ingestRepo reads every results file in one repository's ChecksDir and
// folds it into out, which is shared across every repository in the
// catalog so precedence is resolved across all of them, not just within
// one.
func ingestRepo(repo string, fsys fs.FS, cat *catalog.Catalog, out map[catalog.Ref]map[string]Reported, c *diag.Collector) {
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
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !IsCheckResultsFile(n) {
			continue
		}
		names = append(names, n)
	}
	// Sorted so a tie's diagnostic names producers in a stable order and the
	// message is reproducible between runs.
	sort.Strings(names)

	v, err := schema.New(CheckResultsSchema)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: repo, File: ChecksDir, Line: 1,
			Check:   "checks-schema",
			Message: fmt.Sprintf("cannot compile the check-results schema: %v", err),
		})
		return
	}

	for _, name := range names {
		path := ChecksDir + "/" + name
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, Repo: repo, File: path, Line: 1,
				Check:   "checks-unreadable",
				Message: Unreadable(path, err),
			})
			continue
		}
		if !v.Validate(repo, path, data, c) {
			continue
		}
		var f checkResultsFile
		if err := yaml.Unmarshal(data, &f); err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, Repo: repo, File: path, Line: 1,
				Check:   "checks-parse",
				Message: fmt.Sprintf("cannot read %s as check results", path),
				Hint:    "the file must be a landsraad/v1 CheckResults document",
			})
			continue
		}
		// The schema asserts format: date-time, so this parses — but an
		// unparseable value must never be silently treated as the zero time,
		// which is 1 January year 1 and would render every result stale.
		generatedAt, err := time.Parse(time.RFC3339, f.GeneratedAt)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, Repo: repo, File: path, Line: 1,
				Check:   "checks-generated-at",
				Message: fmt.Sprintf("generatedAt %q is not RFC 3339 with an offset", f.GeneratedAt),
				Hint:    "for example 2026-09-08T14:00:00Z; the staleness arithmetic depends on it",
			})
			continue
		}

		for _, r := range f.Results {
			ref, ok := resolveEntity(repo, r.Entity, cat, path, c)
			if !ok {
				continue
			}
			cand := Reported{
				Result: Result{
					Check:  r.Check,
					Status: Status(r.Status),
					Detail: r.Detail,
					URL:    r.URL,
				},
				GeneratedAt: generatedAt,
				Producer:    f.Producer,
				SourceFile:  path,
				SourceRepo:  repo,
			}
			if out[ref] == nil {
				out[ref] = map[string]Reported{}
			}
			prev, exists := out[ref][r.Check]
			switch {
			case !exists || cand.GeneratedAt.After(prev.GeneratedAt):
				out[ref][r.Check] = cand
			case cand.GeneratedAt.Equal(prev.GeneratedAt) && prev.Producer != cand.Producer:
				// Spec §6: a tie is an error rather than a coin flip.
				c.Add(diag.Diagnostic{
					Severity: diag.SevError, Repo: repo, File: path, Line: 1,
					Entity: ref.Name,
					Check:  "checks-tie",
					Message: fmt.Sprintf("producers %q and %q both report %s for %s at %s, so neither can win",
						prev.Producer, cand.Producer, r.Check, ref, f.GeneratedAt),
					Hint: "give the producers different generatedAt values, or have only one report this check",
				})
			case cand.GeneratedAt.Equal(prev.GeneratedAt) && prev.Location() != cand.Location():
				// Same producer name, two repositories. Unreachable while
				// one repository held every CheckResults file, which is why
				// the rule above only ever compared producers. Under R31 it
				// is reachable, and first-wins would be the coin flip.
				c.Add(diag.Diagnostic{
					Severity: diag.SevError, Repo: repo, File: path, Line: 1,
					Entity: ref.Name,
					Check:  "checks-tie",
					Message: fmt.Sprintf("producer %q reports %s for %s at %s from both %s and %s, so neither can win",
						cand.Producer, r.Check, ref, f.GeneratedAt, prev.Location(), cand.Location()),
					Hint: "give the producers different generatedAt values, or have only one report this check",
				})
			}
		}
	}
}

// applyStaleness ages out results older than staleAfterDays, once every
// repository has been ingested and precedence has picked a winner for every
// (entity, check) pair.
//
// It must run after the whole repository loop, not per repository: ageing
// out before precedence has picked a winner could let a stale-but-newer
// result lose to a fresh-but-older one, and under R31 that argument spans
// repositories too.
func applyStaleness(out map[catalog.Ref]map[string]Reported, staleAfterDays int, now time.Time) {
	limit := time.Duration(staleAfterDays) * 24 * time.Hour
	for ref, byCheck := range out {
		for id, rep := range byCheck {
			age := now.Sub(rep.GeneratedAt)
			if age > limit {
				days := int(age.Hours() / 24)
				// Captured before Status is overwritten below: the detail
				// message names what was originally reported (e.g. "pass"),
				// not the "stale" verdict this same assignment is about to
				// produce.
				originalStatus := rep.Result.Status
				rep.Result.Status = StatusStale
				rep.Result.Detail = fmt.Sprintf("reported %s %s by %s, older than the %d-day limit",
					originalStatus, daysAgo(days), rep.Producer, staleAfterDays)
				byCheck[id] = rep
			}
		}
		out[ref] = byCheck
	}
}

// resolveEntity turns a results file's entity field into a ref.
//
// A bare name is a deprecated alias (spec §6) and is resolved only when
// exactly one entity has that name. The ambiguity is not hypothetical:
// "orders" resolves fine until someone adds topic:orders, and at that moment a
// working CI job starts erroring. That is correct — picking one silently would
// attach evidence to the wrong entity — so the diagnostic says what to write
// instead.
//
// repo identifies which repository's ChecksDir path is being read. Under R31
// that path is no longer unique on its own — three repositories can each
// have a .landsraad/checks/scan.yaml — so without Repo a diagnostic's File
// no longer names one locatable file.
func resolveEntity(repo, s string, cat *catalog.Catalog, path string, c *diag.Collector) (catalog.Ref, bool) {
	if ref, err := catalog.ParseRef(s); err == nil {
		if _, ok := cat.Lookup(ref); !ok {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, Repo: repo, File: path, Line: 1,
				Entity:  ref.Name,
				Check:   "checks-unknown-entity",
				Message: fmt.Sprintf("result reported for %s, which is not in the catalog", ref),
				Hint:    "the entity may have been renamed; metadata.aliases makes a rename additive",
			})
			return catalog.Ref{}, false
		}
		return ref, true
	}

	var matches []catalog.Ref
	for _, e := range cat.Entities() {
		if e.Metadata.Name == s {
			matches = append(matches, e.Ref())
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].String() < matches[j].String() })

	switch len(matches) {
	case 0:
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: repo, File: path, Line: 1,
			Check:   "checks-unknown-entity",
			Message: fmt.Sprintf("result reported for %q, which is not in the catalog", s),
			Hint:    "entity is a ref, for example service:payments-worker",
		})
		return catalog.Ref{}, false
	case 1:
		c.Add(diag.Diagnostic{
			Severity: diag.SevWarn, Repo: repo, File: path, Line: 1,
			Entity:  matches[0].Name,
			Check:   "checks-bare-name",
			Message: fmt.Sprintf("entity %q is a bare name; write it as the ref %q", s, matches[0].String()),
			Hint:    "bare names are accepted for now and resolved only while unambiguous",
		})
		return matches[0], true
	default:
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: repo, File: path, Line: 1,
			Check: "checks-ambiguous-name",
			Message: fmt.Sprintf("entity %q is ambiguous: it could be %s or %s",
				s, matches[0], matches[1]),
			Hint: fmt.Sprintf("write the full ref, for example %s", matches[0]),
		})
		return catalog.Ref{}, false
	}
}
