package render

import (
	"fmt"
	"strings"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// EntityURL is the site-relative directory for an entity's page.
//
// Keyed on the ref, never the bare name (spec §12): service:orders and
// topic:orders are distinct entities that may coexist, and a portal that
// gave them one URL would show one team's service under the other's name.
//
// The name needs no escaping. The schema constrains it to
// ^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$ at 63 characters, which is already a
// legal path segment — no slash, no space, no uppercase. Team names have no
// such constraint, which is why they go through Slug and entity names do not.
func EntityURL(r catalog.Ref) string {
	return "entity/" + strings.ToLower(string(r.Kind)) + "/" + r.Name + "/"
}

// EntityPath is the file EntityURL resolves to.
func EntityPath(r catalog.Ref) string { return EntityURL(r) + "index.html" }

// TeamURL is the site-relative directory for a team's page.
func TeamURL(slug string) string { return "team/" + slug + "/" }

// TeamPath is the file TeamURL resolves to.
func TeamPath(slug string) string { return TeamURL(slug) + "index.html" }

// Slug maps an arbitrary string to a URL path segment, reporting ok=false
// when nothing usable survives.
//
// It does not invent a fallback. A team called "###" getting the page
// "team/team-1/" would be a URL nobody can guess from the name they know,
// and the tool would never mention it — the silent-corruption shape this
// project forbids. The caller reports it instead.
func Slug(s string) (string, bool) {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	return out, out != ""
}

// TeamSlugs maps every team name to its page slug, reporting names that
// cannot produce one and names that collide.
//
// The collision matters because two teams sharing a slug share a file, and
// the second write silently replaces the first: one team's members, on-call
// and owned services vanish from the portal with nothing to notice it by.
//
// Which of the two keeps the slug is decided by config.Teams.Names(), which
// sorts. That is arbitrary from the user's point of view and it does not
// matter — the collision is an error and the build stops. What matters is
// that it is deterministic, so the diagnostic reads the same on every run.
func TeamSlugs(t *config.Teams, c *diag.Collector) map[string]string {
	out := map[string]string{}
	seen := map[string]string{} // slug -> the name that claimed it
	for _, name := range t.Names() {
		slug, ok := Slug(name)
		if !ok {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "teams.yaml", Line: 1,
				Entity:  name,
				Check:   "team-url",
				Message: fmt.Sprintf("team %q has no usable page URL: its name contains no letters or digits", name),
				Hint:    "give the team a name with at least one letter or digit",
			})
			continue
		}
		if first, dup := seen[slug]; dup {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "teams.yaml", Line: 1,
				Entity: name,
				Check:  "team-url",
				Message: fmt.Sprintf("teams %q and %q both produce the page %s",
					first, name, TeamURL(slug)),
				Hint: "rename one of them; a team's page URL is derived from its name",
			})
			// The first claimant keeps the slug so every other page still has
			// somewhere to link. The diagnostic is what stops the build.
			continue
		}
		seen[slug] = name
		out[name] = slug
	}
	return out
}

// rootRel is the relative path from a generated file back to the site root
// (ruling R12).
//
// Every href in every template is prefixed with it, so the portal works
// unchanged at https://internal/ and at https://internal/portal/ — which is
// how most teams will actually host it. An absolute "/assets/style.css"
// would 404 in the second case, on every page, with no error anywhere.
func rootRel(outputPath string) string {
	depth := strings.Count(outputPath, "/")
	if depth == 0 {
		return ""
	}
	return strings.Repeat("../", depth)
}
