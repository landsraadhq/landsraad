package catalog

import (
	"reflect"
	"testing"

	"github.com/landsraadhq/landsraad/internal/yamlerr"
)

// Audit finding 3, applied to the file every user edits. A Go type with no
// noun still produces a message — "expected a value" — but a vaguer one than
// service.yaml deserves, and nothing at runtime would say so. The set of types
// reachable in a yaml.TypeError is exactly the field types of Entity, so this
// walks them and fails the moment one is added without a noun.
func TestNounsCoverEveryFieldType(t *testing.T) {
	for _, name := range reachableTypes(reflect.TypeOf(Entity{}), map[reflect.Type]bool{}) {
		if !yamlerr.Known(name, catalogNouns) {
			t.Errorf("no noun for %q: a decode error naming it would read "+
				"\"expected a value\"; add it to catalogNouns", name)
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
