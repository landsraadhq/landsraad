package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	pathpkg "path"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/yamlerr"
)

// ParseFile turns one service.yaml into an Entity, attaching provenance and
// the line number of metadata.name. It reports at most one diagnostic and
// returns ok=false only when the bytes are not YAML at all — structural
// validation is a separate stage (see internal/schema).
func ParseFile(repo, path string, data []byte, c *diag.Collector) (*Entity, bool) {
	// A decoder rather than yaml.Unmarshal, because Unmarshal reads the first
	// document of the stream and reports nothing about the rest. See
	// extraDocuments below.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var root yaml.Node
	err := dec.Decode(&root)
	if err != nil && !errors.Is(err, io.EOF) {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     yamlerr.Line(err),
			Check:    "yaml-parse",
			Message:  fmt.Sprintf("cannot parse YAML: %v", err),
		})
		return nil, false
	}

	// An empty file ends the stream immediately (io.EOF) or decodes into a
	// zero Node with no content.
	if err != nil || root.Kind == 0 || len(root.Content) == 0 {
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

	// Reject the whole file rather than validating the first document and
	// ignoring the rest: an entity nothing ever looked at that still counts
	// towards "no problems found" is the failure this tool exists to prevent.
	// Rejecting is also the reversible direction — supporting more documents
	// later is additive, withdrawing support would break every user (spec D6).
	if extra, line := extraDocuments(dec); extra > 0 {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     repo,
			File:     path,
			Line:     line,
			Check:    "yaml-documents",
			Message: fmt.Sprintf("%s must contain exactly one document, found %d",
				pathpkg.Base(path), extra+1),
			Hint: "landsraad reads one entity per file: move the document after `---` into its own service.yaml",
		})
		return nil, false
	}

	var e Entity
	if err := root.Decode(&e); err != nil {
		// The schema stage reports most of these too, on the same line, and
		// two diagnostics for one cause is noise this codebase avoids
		// elsewhere. It is kept anyway: schema.Validate returns false both
		// when it reported a violation and when it deliberately stayed silent
		// for bytes that are not YAML, so suppressing on it would make a
		// malformed file vanish with no diagnostic at all, and skipping
		// schema-failing files here would drop their owner and reference
		// diagnostics — landsraad reports every problem in one run. Noise is
		// the safe direction; silence is not.
		for _, p := range yamlerr.Problems(path, catalogNouns, err) {
			message := p.Message
			if !p.Translated {
				message = fmt.Sprintf("cannot read as a catalog entity: %s", p.Message)
			}
			c.Add(diag.Diagnostic{
				Severity: diag.SevError,
				Repo:     repo,
				File:     path,
				Line:     p.Line,
				Check:    "yaml-decode",
				Message:  message,
			})
		}
		return nil, false
	}

	e.SourceRepo = repo
	e.SourcePath = path
	e.NameLine = fieldLine(&root, "metadata", "name")
	if e.NameLine == 0 {
		e.NameLine = 1
	}
	e.refLines = map[string][]int{
		FieldDependsOn:    seqItemLines(&root, "spec", FieldDependsOn),
		FieldProvidesApis: seqItemLines(&root, "spec", FieldProvidesApis),
	}
	return &e, true
}

// extraDocuments counts the documents left in the stream after the first one
// and returns the line where the first of them starts. A YAML stream may hold
// several documents separated by `---`; yaml.Unmarshal decodes document one
// and returns no error for the rest, which is how a second entity in the same
// file — the Kubernetes habit of a Service and its Topic together — reached a
// clean run without ever being validated.
//
// A `---` with nothing after it produces an empty document. That is
// punctuation, not a second entity, so it does not count.
func extraDocuments(dec *yaml.Decoder) (extra, line int) {
	for {
		var doc yaml.Node
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return extra, line
		}
		if err != nil {
			// A document the parser cannot read is still a document. Report
			// it as one, pointing at where the parser gave up: the count is
			// what the reader needs, and the fix is the same either way.
			if extra == 0 {
				line = yamlerr.Line(err)
			}
			return extra + 1, line
		}
		if isEmptyDocument(&doc) {
			continue
		}
		if extra == 0 {
			line = doc.Line
		}
		extra++
	}
}

// isEmptyDocument reports whether a decoded document holds nothing — the case
// of a trailing `---`, whose content is a single null scalar.
func isEmptyDocument(doc *yaml.Node) bool {
	if doc.Kind == 0 || len(doc.Content) == 0 {
		return true
	}
	n := doc.Content[0]
	return len(doc.Content) == 1 && n.Kind == yaml.ScalarNode && n.Tag == "!!null"
}

// nodeAt walks a document node down the given key path and returns the node
// holding that key's value, or nil when the path does not exist.
func nodeAt(root *yaml.Node, path ...string) *yaml.Node {
	node := root
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	for _, key := range path {
		if node.Kind != yaml.MappingNode {
			return nil
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
			return nil
		}
	}
	return node
}

// fieldLine walks a document node down the given key path and returns the
// 1-indexed line of that key's value, or 0 when the path does not exist.
func fieldLine(root *yaml.Node, path ...string) int {
	node := nodeAt(root, path...)
	if node == nil {
		return 0
	}
	return node.Line
}

// seqItemLines returns the 1-indexed line of each item of the sequence at the
// given key path, or nil when that path is absent or is not a sequence.
//
// Ruling R52: the index into the returned slice matches the index into the
// decoded []string, because both are built from the same sequence node in the
// same order. That correspondence is what lets a diagnostic about the i-th
// reference cite the line the i-th reference is written on.
func seqItemLines(root *yaml.Node, path ...string) []int {
	node := nodeAt(root, path...)
	// A field written as a YAML alias — `dependsOn: *shared` — is an
	// AliasNode pointing at the anchored sequence. The decoder resolves it,
	// so Spec.DependsOn is populated either way; without following it here
	// the lines would not be, and this ruling's diagnostic would fall back to
	// NameLine for precisely the files that use an anchor. An anchor target
	// is a real node and never another alias, so one hop is the whole of it.
	if node != nil && node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	lines := make([]int, len(node.Content))
	for i, item := range node.Content {
		lines[i] = item.Line
	}
	return lines
}
