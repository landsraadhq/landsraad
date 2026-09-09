package catalog

import "github.com/landsraadhq/landsraad/internal/yamlerr"

// catalogNouns is this package's vocabulary for internal/yamlerr: the Go types
// reachable in a service.yaml decode error, named as someone editing that file
// would name them.
//
// service.yaml is the file every user edits, so this is the vocabulary that
// matters most. TestNounsCoverEveryFieldType walks Entity and fails if a
// field's type is missing here.
var catalogNouns = yamlerr.Nouns{
	"catalog.Kind":      {Singular: "kind", Plural: "kinds"},
	"catalog.Metadata":  {Singular: "metadata block", Plural: "metadata blocks"},
	"catalog.Spec":      {Singular: "spec block", Plural: "spec blocks"},
	"catalog.Link":      {Singular: "link", Plural: "links"},
	"catalog.SLO":       {Singular: "SLO", Plural: "SLOs"},
	"catalog.Exemption": {Singular: "exemption", Plural: "exemptions"},
	"catalog.Runtime":   {Singular: "runtime block", Plural: "runtime blocks"},
}
