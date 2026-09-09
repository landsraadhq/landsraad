package config

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

const teamsYAML = `teams:
  - name: team-payments
    members: [alice, bob]
    slack: "#payments"
    pagerduty: PAY
  - name: team-sre
    members: [carol]
    slack: "#sre"
    pagerduty: SRE
`

func TestLoadTeams(t *testing.T) {
	var c diag.Collector
	teams := LoadTeams("teams.yaml", []byte(teamsYAML), &c)
	if c.HasErrors() {
		t.Fatalf("valid teams.yaml must load: %+v", c.Diagnostics())
	}
	got, ok := teams.Get("team-payments")
	if !ok {
		t.Fatal("team-payments must be found")
	}
	if len(got.Members) != 2 || got.Slack != "#payments" {
		t.Errorf("team fields not read: %+v", got)
	}
	if names := teams.Names(); len(names) != 2 || names[0] != "team-payments" {
		t.Errorf("Names() must be sorted, got %v", names)
	}
}

func TestLoadTeamsRejectsUnknownFields(t *testing.T) {
	var c diag.Collector
	LoadTeams("teams.yaml", []byte("teams:\n  - name: team-a\n    pagerDuty: PAY\n"), &c)
	if !c.HasErrors() {
		t.Fatal("a misspelled key in the file that routes alerts must be rejected, not ignored")
	}
}

func TestLoadTeamsReportsRealLineNumbers(t *testing.T) {
	in := "teams:\n  - name: team-a\n  - name: team-a\n"
	var c diag.Collector
	LoadTeams("teams.yaml", []byte(in), &c)
	if !c.HasErrors() {
		t.Fatal("a duplicate team must be an error")
	}
	if got := c.Diagnostics()[0].Line; got != 3 {
		t.Errorf("the duplicate is on line 3, diagnostic points at line %d", got)
	}
}

func TestValidateOwnersRejectsUnknownTeam(t *testing.T) {
	var c diag.Collector
	teams := LoadTeams("teams.yaml", []byte(teamsYAML), &c)

	e := &catalog.Entity{Kind: catalog.KindService}
	e.Metadata.Name = "api"
	e.Metadata.Owner = "team-payment" // typo: missing trailing s
	e.SourcePath = "services/api/service.yaml"
	e.NameLine = 4
	cat := catalog.NewCatalog([]*catalog.Entity{e}, &c)

	teams.ValidateOwners(cat, &c)

	if !c.HasErrors() {
		t.Fatal("an unknown owner must be an error")
	}
	d := c.Diagnostics()[0]
	if want := `owner "team-payment" is not defined in teams.yaml`; d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if want := `did you mean "team-payments"?`; d.Hint != want {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, want)
	}
}

func TestValidateOwnersAcceptsKnownTeam(t *testing.T) {
	var c diag.Collector
	teams := LoadTeams("teams.yaml", []byte(teamsYAML), &c)

	e := &catalog.Entity{Kind: catalog.KindService}
	e.Metadata.Name = "api"
	e.Metadata.Owner = "team-sre"
	e.SourcePath = "services/api/service.yaml"
	e.NameLine = 4
	cat := catalog.NewCatalog([]*catalog.Entity{e}, &c)

	teams.ValidateOwners(cat, &c)

	if c.HasErrors() {
		t.Errorf("a known owner must pass: %+v", c.Diagnostics())
	}
}
