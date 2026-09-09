package config

import (
	"reflect"
	"testing"

	"github.com/landsraadhq/landsraad/internal/yamlerr"
)

// Audit finding 3. The translator degrades an unnamed Go type to "a value" —
// true, but vague, and nothing at runtime says so. Adding Repo.Fetch for
// Plan 3's tokens would have silently turned a message into "expected a value,
// found a string" with the whole suite still green.
//
// Degraded mode has to be visible. Since the set of types reachable in a
// yaml.TypeError is exactly the field types of the structs yaml decodes into,
// it is knowable, so this walks them and fails the moment one has no noun.
func TestNounsCoverEveryFieldType(t *testing.T) {
	for _, root := range []reflect.Type{
		reflect.TypeOf(teamsFile{}),
		reflect.TypeOf(Repos{}),
	} {
		for _, name := range reachableTypes(root, map[reflect.Type]bool{}) {
			if !yamlerr.Known(name, configNouns) {
				t.Errorf("no noun for %q: a decode error naming it would read "+
					"\"expected a value\"; add it to configNouns", name)
			}
		}
	}
}

// reachableTypes returns the Go type names yaml.v3 could print for any field
// under root, spelled the way it prints them.
func reachableTypes(t reflect.Type, seen map[reflect.Type]bool) []string {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if seen[t] || t.Kind() != reflect.Struct {
		return nil
	}
	seen[t] = true
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		out = append(out, f.Type.String())
		out = append(out, reachableTypes(f.Type, seen)...)
	}
	return out
}
