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
	d := c.Diagnostics()[0]
	if want := "teams.yaml is a list under `teams:` with name, members, slack and pagerduty"; d.Hint != want {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, want)
	}
}

func TestLoadTeamsReportsRealLineNumbers(t *testing.T) {
	in := "teams:\n  - name: team-a\n  - name: team-a\n"
	var c diag.Collector
	LoadTeams("teams.yaml", []byte(in), &c)
	if !c.HasErrors() {
		t.Fatal("a duplicate team must be an error")
	}
	d := c.Diagnostics()[0]
	if got := d.Line; got != 3 {
		t.Errorf("the duplicate is on line 3, diagnostic points at line %d", got)
	}
	if want := `team "team-a" is defined twice`; d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if want := "merge the two entries"; d.Hint != want {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, want)
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

func TestLoadTeamsRejectsNamelessTeam(t *testing.T) {
	in := "teams:\n  - members: [alice]\n    slack: \"#team\"\n"
	var c diag.Collector
	LoadTeams("teams.yaml", []byte(in), &c)
	if !c.HasErrors() {
		t.Fatal("a nameless team must be an error")
	}
	d := c.Diagnostics()[0]
	if want := "a team entry has no name"; d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if want := "every team needs a name; it is what service.yaml owner fields refer to"; d.Hint != want {
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

func TestValidateOwnersHintsWhenNoCloseMatch(t *testing.T) {
	var c diag.Collector
	teams := LoadTeams("teams.yaml", []byte(teamsYAML), &c)

	e := &catalog.Entity{Kind: catalog.KindService}
	e.Metadata.Name = "api"
	e.Metadata.Owner = "xyz" // completely different, no close match
	e.SourcePath = "services/api/service.yaml"
	e.NameLine = 4
	cat := catalog.NewCatalog([]*catalog.Entity{e}, &c)

	teams.ValidateOwners(cat, &c)

	if !c.HasErrors() {
		t.Fatal("an unknown owner must be an error")
	}
	d := c.Diagnostics()[0]
	if want := `owner "xyz" is not defined in teams.yaml`; d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	// When there's no close match, hint lists all known teams
	if want := "known teams: [team-payments team-sre]"; d.Hint != want {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, want)
	}
}

func TestValidateOwnersSkipsOnBrokenTeamsYAML(t *testing.T) {
	// When teams.yaml has a parse error, ValidateOwners must not produce
	// false "owner is not defined" diagnostics. It must return early having
	// reported that owner validation was skipped, not cascade false errors.
	brokenTeamsYAML := "teams:\n  - name: team-a\n    pagerDuty: PAY\n" // typo: pagerDuty should be pagerduty

	var c diag.Collector
	teams := LoadTeams("teams.yaml", []byte(brokenTeamsYAML), &c)

	// Create entities with valid owners (if the teams had loaded properly)
	e1 := &catalog.Entity{Kind: catalog.KindService}
	e1.Metadata.Name = "api"
	e1.Metadata.Owner = "team-a"
	e1.SourcePath = "services/api/service.yaml"
	e1.NameLine = 4

	e2 := &catalog.Entity{Kind: catalog.KindService}
	e2.Metadata.Name = "worker"
	e2.Metadata.Owner = "team-a"
	e2.SourcePath = "services/worker/service.yaml"
	e2.NameLine = 4

	cat := catalog.NewCatalog([]*catalog.Entity{e1, e2}, &c)

	// Fresh collector for ValidateOwners so we can verify it produces exactly one diagnostic
	var vc diag.Collector
	teams.ValidateOwners(cat, &vc)

	// Must report exactly one diagnostic: that owner validation was skipped.
	// Must not report two false "team-a is not defined" errors.
	if len(vc.Diagnostics()) != 1 {
		t.Fatalf("expected 1 diagnostic (owner validation skipped), got %d: %+v", len(vc.Diagnostics()), vc.Diagnostics())
	}
	d := vc.Diagnostics()[0]
	if want := "owner validation skipped: teams.yaml could not be read"; d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}
