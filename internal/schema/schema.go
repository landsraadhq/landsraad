// Package schema validates catalog files against a JSON Schema. The schema
// shipped with the binary is the single source of truth for structure:
// validating against it, rather than with hand-written Go checks, is what
// keeps the published schema and the tool's behaviour from drifting apart.
package schema

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
)

//go:embed service.schema.json
var Raw []byte

// Validator validates catalog files against one compiled schema.
//
// It is a value rather than package state so that two schema versions can
// coexist during a migration, and so a test can supply its own schema. See
// spec §3.1.
type Validator struct {
	sch *jsonschema.Schema
}

const schemaID = "service.schema.json"

// New compiles the given JSON Schema document.
func New(schemaJSON []byte) (*Validator, error) {
	var doc any
	if err := json.Unmarshal(schemaJSON, &doc); err != nil {
		return nil, fmt.Errorf("schema is not valid JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	// "format" is annotation-only in JSON Schema 2020-12 unless the validator
	// is told to assert it. The published schema declares `"format": "date"`
	// on spec.exemptions[].until, so not asserting it means shipping a schema
	// that advertises a constraint the tool does not apply — `until: banana`
	// passed. Asserting is the reversible choice: it enforces what the schema
	// already says, where adding a `pattern` would change the published
	// document, which users pin and copy into their editors (a one-way door).
	c.AssertFormat()
	if err := c.AddResource(schemaID, doc); err != nil {
		return nil, fmt.Errorf("cannot add schema resource: %w", err)
	}
	sch, err := c.Compile(schemaID)
	if err != nil {
		return nil, fmt.Errorf("cannot compile schema: %w", err)
	}
	return &Validator{sch: sch}, nil
}

// Default compiles the schema embedded in the binary.
func Default() (*Validator, error) { return New(Raw) }

// Validate checks one file's bytes. It returns true when the file is
// structurally valid, and adds one diagnostic per violation.
//
// It reports nothing for bytes that are not YAML at all: that is
// catalog.ParseFile's diagnostic to make, and two errors for one cause is
// noise.
func (v *Validator) Validate(repo, path string, data []byte, c *diag.Collector) bool {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return false
	}
	jsonTypes(&root)
	var inst any
	if err := root.Decode(&inst); err != nil {
		return false
	}

	err := v.sch.Validate(inst)
	if err == nil {
		return true
	}

	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: repo, File: path, Line: 1,
			Check: "schema", Message: err.Error(),
		})
		return false
	}
	for _, leaf := range leaves(ve) {
		// InstanceLocation is []string in jsonschema/v6 — one element per
		// path segment — which is exactly what fieldLine wants.
		line := fieldLine(&root, leaf.InstanceLocation...)
		if line == 0 {
			line = 1
		}
		message, hint := leaf.Error(), ""
		switch k := leaf.ErrorKind.(type) {
		case *kind.FalseSchema:
			// leaf.Error() renders this as "false schema" — jargon for "this
			// location matched a schema that is literally `false`". The only
			// `false` subschemas in service.schema.json are
			// unevaluatedProperties guards (there are seven, one per object),
			// so a FalseSchema leaf always means "this field is not defined
			// here". That mapping breaks if a `false` subschema is ever added
			// for a different purpose — revisit this special case if so.
			//
			// The location is empty when the whole document matched a `false`
			// schema, which a caller-supplied schema (New is exported) may
			// well be. There is no field to name then, and leaf.Error()
			// already says what happened, so leave it alone rather than
			// indexing past the start of the slice.
			if len(leaf.InstanceLocation) > 0 {
				field := leaf.InstanceLocation[len(leaf.InstanceLocation)-1]
				message = fmt.Sprintf("at '%s': unknown field '%s'", pointer(leaf.InstanceLocation), field)
				hint = "this schema rejects fields it doesn't define, rather than silently ignoring them — remove it, or check for a typo"
			}
		case *kind.Format:
			// leaf.Error() appends Go's time-parsing internals ("parsing time
			// \"banana\" as \"2006-01-02\"…"), which tells a user nothing they
			// can act on.
			message = fmt.Sprintf("at '%s': '%v' is not a valid %s",
				pointer(leaf.InstanceLocation), k.Got, k.Want)
			if k.Want == "date" {
				hint = "dates are written YYYY-MM-DD, for example 2027-01-01"
			}
		}
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     line,
			Check:    "schema",
			// message already names the instance location (either
			// leaf.Error()'s own rendering, or ours above), so do not
			// prefix it again.
			Message: message,
			Hint:    hint,
		})
	}
	return false
}

// jsonTypes rewrites the node tags that yaml.v3 would resolve into Go types
// JSON Schema has no concept of, so the validator sees a JSON document.
//
// An unquoted `until: 2027-01-01` resolves to a time.Time and a non-string
// mapping key resolves to a map[any]any; both reached the user as "invalid
// jsonType time.Time" — raw library internals, for input the spec itself
// documents. Rewriting the tag rather than the decoded value keeps the text
// the user actually wrote ("2027-01-01", not "2027-01-01T00:00:00Z"), which
// is what the schema's `"format": "date"` expects and what any diagnostic
// then quotes back at them.
func jsonTypes(n *yaml.Node) {
	switch n.Kind {
	case yaml.MappingNode:
		// Content alternates key, value.
		for i := 0; i+1 < len(n.Content); i += 2 {
			// JSON object keys are strings. `<<` is yaml.v3's merge key, whose
			// resolution is the point of writing it — leave that one alone.
			if k := n.Content[i]; k.Kind == yaml.ScalarNode && k.Tag != mergeTag {
				k.Tag = strTag
			}
			jsonTypes(n.Content[i+1])
		}
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!null", "!!bool", "!!int", "!!float", strTag, mergeTag:
			// Already a JSON type.
		default:
			// !!timestamp, !!binary and any custom tag: keep the source text.
			n.Tag = strTag
		}
	default:
		for _, child := range n.Content {
			jsonTypes(child)
		}
	}
}

const (
	strTag   = "!!str"
	mergeTag = "!!merge"
)

// pointer renders a JSON Pointer the same way jsonschema/v6 does internally
// (e.g. "/spec/nonsense"), so the unknown-field message below reads
// consistently with every other diagnostic's "at '<location>': …" prefix.
func pointer(loc []string) string {
	esc := strings.NewReplacer("~", "~0", "/", "~1")
	var sb strings.Builder
	for _, tok := range loc {
		sb.WriteByte('/')
		sb.WriteString(esc.Replace(tok))
	}
	return sb.String()
}

// leaves returns the most specific errors in the tree. The root error is a
// generic "does not validate"; the causes carry the useful detail.
func leaves(ve *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return []*jsonschema.ValidationError{ve}
	}
	var out []*jsonschema.ValidationError
	for _, c := range ve.Causes {
		out = append(out, leaves(c)...)
	}
	return out
}

// fieldLine walks a document node down a key path and returns the 1-indexed
// line of the value it lands on. Numeric segments are array indices. It
// returns the nearest enclosing node's line when the path runs out, so a
// diagnostic always points somewhere useful.
func fieldLine(root *yaml.Node, path ...string) int {
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, key := range path {
		switch node.Kind {
		case yaml.MappingNode:
			found := false
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == key {
					node = node.Content[i+1]
					found = true
					break
				}
			}
			if !found {
				return node.Line
			}
		case yaml.SequenceNode:
			idx := -1
			if _, err := fmt.Sscanf(key, "%d", &idx); err != nil || idx < 0 || idx >= len(node.Content) {
				return node.Line
			}
			node = node.Content[idx]
		default:
			return node.Line
		}
	}
	return node.Line
}
