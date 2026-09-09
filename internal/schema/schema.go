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
		if _, ok := leaf.ErrorKind.(*kind.FalseSchema); ok {
			// leaf.Error() renders this as "false schema" — jargon for "this
			// location matched a schema that is literally `false`". The only
			// `false` subschemas in service.schema.json are
			// unevaluatedProperties guards (there are seven, one per object),
			// so a FalseSchema leaf always means "this field is not defined
			// here". That mapping breaks if a `false` subschema is ever added
			// for a different purpose — revisit this special case if so.
			field := leaf.InstanceLocation[len(leaf.InstanceLocation)-1]
			message = fmt.Sprintf("at '%s': unknown field '%s'", pointer(leaf.InstanceLocation), field)
			hint = "this schema rejects fields it doesn't define, rather than silently ignoring them — remove it, or check for a typo"
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
