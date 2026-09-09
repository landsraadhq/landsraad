package catalog

import "testing"

func TestKindValid(t *testing.T) {
	for _, k := range AllKinds {
		if !k.Valid() {
			t.Errorf("%q is in AllKinds but reports invalid", k)
		}
	}
	for _, bad := range []Kind{"", "service", "SERVICE", "Widget"} {
		if bad.Valid() {
			t.Errorf("%q must be invalid — kinds are case-sensitive", bad)
		}
	}
}

func TestAPIVersionConstant(t *testing.T) {
	if APIVersion != "landsraad/v1" {
		t.Errorf("APIVersion = %q, want %q", APIVersion, "landsraad/v1")
	}
}
