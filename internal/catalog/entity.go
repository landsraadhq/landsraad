// Package catalog holds the entity model and the pipeline stages that turn
// service.yaml files into a validated, resolved catalog.
package catalog

// APIVersion is the only apiVersion this release accepts. It carries no
// domain, matching Kubernetes core groups such as apps/v1.
const APIVersion = "landsraad/v1"

// Kind is the type of a catalog entity. Kinds are case-sensitive.
type Kind string

const (
	KindService  Kind = "Service"
	KindWorker   Kind = "Worker"
	KindCron     Kind = "Cron"
	KindLibrary  Kind = "Library"
	KindTopic    Kind = "Topic"
	KindDatabase Kind = "Database"
	KindAPI      Kind = "API"
	// KindResource is the escape hatch: an S3 bucket, an SQS queue, a Redis
	// cache, a Terraform module. Without it users model those as Database or
	// Topic — a lie that then flows into the tier matrix, the dependency
	// graph and the portal. Backstage converged on the same answer: a small
	// kind set plus a free-string spec.type.
	KindResource Kind = "Resource"
)

// allKinds is the complete set, used by validation and by the JSON Schema
// test. It is an array rather than a slice, and unexported, because an
// exported mutable slice is package-level state any importer can rewrite:
// `catalog.AllKinds[0] = "Nonsense"` used to make ParseRef reject
// "service:ledger-api" as an unknown kind. The practical cost in a single
// binary is small, but tests in a package share a process, so one test
// mutating it without a t.Cleanup poisons every test that runs after it and
// the failure surfaces somewhere else entirely.
var allKinds = [...]Kind{
	KindService, KindWorker, KindCron, KindLibrary,
	KindTopic, KindDatabase, KindAPI, KindResource,
}

// AllKinds returns the complete set of kinds. The slice is fresh on each
// call, so a caller cannot reach back and change what the package believes.
func AllKinds() []Kind {
	out := make([]Kind, len(allKinds))
	copy(out, allKinds[:])
	return out
}

func (k Kind) Valid() bool {
	for _, known := range allKinds {
		if k == known {
			return true
		}
	}
	return false
}

// Metadata is the identity and ownership block.
type Metadata struct {
	Name string `yaml:"name"`
	// Aliases are former names. References resolve through them, so renaming
	// an entity is additive rather than a break for every repo that depends
	// on it — and the scorecard time series survives the rename.
	Aliases     []string          `yaml:"aliases"`
	Description string            `yaml:"description"`
	Owner       string            `yaml:"owner"`
	Tier        int               `yaml:"tier"`
	Lifecycle   string            `yaml:"lifecycle"`
	Tags        []string          `yaml:"tags"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

// Link is one external destination: dashboard, deploy, traces, and so on.
type Link struct {
	Title string `yaml:"title"`
	URL   string `yaml:"url"`
	Type  string `yaml:"type"`
}

// SLO is one service level objective.
type SLO struct {
	Name   string `yaml:"name"`
	Target string `yaml:"target"`
	Window string `yaml:"window"`
}

// Runtime carries the selector used to match live workloads. It is unused in
// this plan; the runtime agent is a later sub-project.
type Runtime struct {
	Selector map[string]string `yaml:"selector"`
}

// Exemption waives one scorecard check with a stated reason. Without this a
// tier-1 nightly backfill that genuinely has no runbook fails forever, and the
// team's only lever is to lie about its tier — corrupting the dataset the
// whole product is built on.
type Exemption struct {
	Check  string `yaml:"check"`
	Reason string `yaml:"reason"`
	Until  string `yaml:"until"`
}

// Spec is the operational block.
type Spec struct {
	// Type is a free string, deliberately unconstrained, matching Backstage's
	// spec.type. It is how a Resource says what kind of resource it is.
	Type         string      `yaml:"type"`
	Language     string      `yaml:"language"`
	Path         string      `yaml:"path"`
	Docs         string      `yaml:"docs"`
	Runbook      string      `yaml:"runbook"`
	Oncall       string      `yaml:"oncall"`
	RepoURL      string      `yaml:"repoUrl"`
	Links        []Link      `yaml:"links"`
	DependsOn    []string    `yaml:"dependsOn"`
	ProvidesApis []string    `yaml:"providesApis"`
	SLO          []SLO       `yaml:"slo"`
	Exemptions   []Exemption `yaml:"exemptions"`
	Alerts       string      `yaml:"alerts"`
	Runtime      *Runtime    `yaml:"runtime"`
}

// Entity is one parsed service.yaml.
//
// SourceRepo, SourcePath and NameLine are provenance: they are not present in
// the file, they are attached at parse time. They exist so a collision or a
// dangling reference can name both sides with a file and a line.
type Entity struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       Kind     `yaml:"kind"`
	Metadata   Metadata `yaml:"metadata"`
	Spec       Spec     `yaml:"spec"`

	SourceRepo string `yaml:"-"`
	SourcePath string `yaml:"-"`
	NameLine   int    `yaml:"-"`

	// refLines holds the 1-indexed line of each item of the reference fields,
	// keyed by FieldDependsOn and FieldProvidesApis. Provenance like the three
	// above: not present in the file, attached at parse time, and absent for
	// an entity built without ParseFile.
	//
	// Unexported where SourceRepo and NameLine are not, because it is a
	// mutable map on a pointer type this package hands to render, scorecard
	// and generate — the reachable-mutable-state the accessors on allKinds
	// and docsIndexNames exist to prevent. RefLine is the read path, and
	// nothing outside this package constructs provenance.
	refLines map[string][]int `yaml:"-"`
}

// FieldDependsOn and FieldProvidesApis name the two spec fields that carry
// references. One constant each, because the same string is the key refLines
// records lines under, the label resolveRefs prints in its diagnostics, and
// the YAML key the parser walks to — three uses that must not drift.
const (
	FieldDependsOn    = "dependsOn"
	FieldProvidesApis = "providesApis"
)

// RefLine returns the 1-indexed line of the i-th item of a reference field.
//
// It falls back to NameLine, and that is a documented degradation rather than
// a silent one. An entity built without ParseFile carries no refLines, and a
// zero would render through internal/diag as "service.yaml:0" — a location
// that does not exist, in a program whose product is the quality of its
// diagnostics. NameLine names the right file and the right entity, merely not
// the right line, which is precisely the behaviour this method exists to
// replace, so the floor here is the old behaviour and not corruption. A
// reference that decoded into Spec.DependsOn came from a sequence node that
// exists, and seqItemLines follows a YAML alias to reach it, so no parsed
// entity is known to reach the fallback today. "Known" is doing real work in
// that sentence: the alias case was claimed unreachable here and was not,
// until an audit ran it. The fallback stays because a gap in provenance must
// not turn a diagnostic into a panic, and because the claim has been wrong
// once already.
func (e *Entity) RefLine(field string, i int) int {
	lines := e.refLines[field]
	if i < 0 || i >= len(lines) || lines[i] == 0 {
		return e.NameLine
	}
	return lines[i]
}

// Ref returns this entity's canonical reference, e.g. "service:ledger-api".
func (e *Entity) Ref() Ref {
	return Ref{Kind: e.Kind, Name: e.Metadata.Name}
}

// Location renders where this entity came from: "repo:path" when the repo is
// known, "path" when it is not. `landsraad validate` runs inside one repo and
// often has no name for it, and "…in :services/api/service.yaml" is worse
// than no prefix at all.
func (e *Entity) Location() string {
	if e.SourceRepo == "" {
		return e.SourcePath
	}
	return e.SourceRepo + ":" + e.SourcePath
}
