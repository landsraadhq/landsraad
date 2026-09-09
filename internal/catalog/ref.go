package catalog

import (
	"fmt"
	"strings"
)

// Ref identifies one catalog entity. Names are flat and globally unique, so a
// Ref is a kind plus a name and nothing more. Written as "service:ledger-api".
type Ref struct {
	Kind Kind
	Name string
}

// String renders the canonical form, with a lowercase kind prefix.
func (r Ref) String() string {
	return strings.ToLower(string(r.Kind)) + ":" + r.Name
}

// ParseRef reads "kind:name". The kind prefix is case-insensitive so both
// "service:x" and "Service:x" work, but the name is taken verbatim.
func ParseRef(s string) (Ref, error) {
	kindStr, name, found := strings.Cut(s, ":")
	if !found {
		return Ref{}, fmt.Errorf("reference %q has no kind prefix, want the form kind:name (for example service:ledger-api)", s)
	}
	if name == "" {
		return Ref{}, fmt.Errorf("reference %q has an empty name", s)
	}
	if kindStr == "" {
		return Ref{}, fmt.Errorf("reference %q has an empty kind", s)
	}
	for _, k := range allKinds {
		if strings.EqualFold(kindStr, string(k)) {
			return Ref{Kind: k, Name: name}, nil
		}
	}
	return Ref{}, fmt.Errorf("reference %q has unknown kind %q", s, kindStr)
}
