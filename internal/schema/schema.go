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

	"github.com/santhosh-tekuri/jsonschema/v6"
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
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     line,
			Check:    "schema",
			// leaf.Error() already names the instance location, so do not
			// prefix it again.
			Message: leaf.Error(),
		})
	}
	return false
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
