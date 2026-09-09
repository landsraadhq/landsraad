package catalog

import (
	"fmt"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// yamlLineRE pulls the line number out of a yaml.v3 error string, which has
// the form "yaml: line 3: mapping values are not allowed in this context".
var yamlLineRE = regexp.MustCompile(`line (\d+):`)

// ParseFile turns one service.yaml into an Entity, attaching provenance and
// the line number of metadata.name. It reports at most one diagnostic and
// returns ok=false only when the bytes are not YAML at all — structural
// validation is a separate stage (see internal/schema).
func ParseFile(repo, path string, data []byte, c *diag.Collector) (*Entity, bool) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     lineFromYAMLError(err),
			Check:    "yaml-parse",
			Message:  fmt.Sprintf("cannot parse YAML: %v", err),
		})
		return nil, false
	}

	// An empty file unmarshals into a zero Node with no content.
	if root.Kind == 0 || len(root.Content) == 0 {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     1,
			Check:    "yaml-parse",
			Message:  "file is empty",
			Hint:     "a service.yaml needs at least apiVersion, kind and metadata.name",
		})
		return nil, false
	}

	var e Entity
	if err := root.Decode(&e); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     lineFromYAMLError(err),
			Check:    "yaml-decode",
			Message:  fmt.Sprintf("cannot read as a catalog entity: %v", err),
		})
		return nil, false
	}

	e.SourceRepo = repo
	e.SourcePath = path
	e.NameLine = fieldLine(&root, "metadata", "name")
	if e.NameLine == 0 {
		e.NameLine = 1
	}
	return &e, true
}

// lineFromYAMLError extracts a 1-indexed line from a yaml.v3 error, falling
// back to 1 so no diagnostic is ever emitted without a line.
func lineFromYAMLError(err error) int {
	if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			return n
		}
	}
	return 1
}

// fieldLine walks a document node down the given key path and returns the
// 1-indexed line of that key's value, or 0 when the path does not exist.
func fieldLine(root *yaml.Node, path ...string) int {
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, key := range path {
		if node.Kind != yaml.MappingNode {
			return 0
		}
		found := false
		// Mapping content alternates key, value, key, value.
		for i := 0; i+1 < len(node.Content); i += 2 {
			if node.Content[i].Value == key {
				node = node.Content[i+1]
				found = true
				break
			}
		}
		if !found {
			return 0
		}
	}
	return node.Line
}
