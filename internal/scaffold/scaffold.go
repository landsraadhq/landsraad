// Package scaffold holds what `landsraad init` writes into a fresh
// repository, as values rather than as filesystem calls.
//
// Spec §3.1 puts writes at the command layer, and says they take an
// io.Writer. That works for one stream and not for a tree: init produces four
// files in three directories, and Plan 2's `gen` produces a page per entity
// plus CODEOWNERS and a history file. The shape that does work is this one —
// a pure function returns the files, and cmd/ owns the single loop that puts
// them on disk. Deciding it here, with one producer, is cheaper than
// discovering it with three.
//
// File was local to this package in Plan 1, pending a second producer. Plan 2's
// generators are that producer, so the type now lives in internal/emit and
// cmd/ runs one write loop for all of them.
package scaffold

import (
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/schema"
)

// Files returns the starter catalog, in the order it should be created.
//
// Every file is valid on the first run: the acceptance test for init is that
// `landsraad validate` passes immediately afterwards. Pure, so that property
// is checked in memory rather than against a temporary directory.
func Files() []emit.File {
	return []emit.File{
		{Path: "teams.yaml", Data: []byte(teamsYAML)},
		{Path: "repos.yaml", Data: []byte(reposYAML)},
		// The modeline in service.yaml below points here. Writing it is what
		// makes editor autocompletion work in a fresh repository without a
		// second manual step; `landsraad schema` regenerates it after an
		// upgrade.
		{Path: "schema/service.schema.json", Data: schema.Raw},
		{Path: "services/example/service.yaml", Data: []byte(exampleService)},
	}
}

const teamsYAML = `teams:
  - name: team-example
    members: [you]
    slack: "#team-example"
    pagerduty: EXAMPLE
`

const reposYAML = `# The first entry is the repository you are in.
repos:
  - url: https://github.com/your-org/your-repo
    paths: [services/*, workers/*, libs/*]
`

const exampleService = `# yaml-language-server: $schema=../../schema/service.schema.json
apiVersion: landsraad/v1
kind: Service
metadata:
  name: example
  description: Replace me with a real service.
  owner: team-example
  tier: 3
  lifecycle: experimental
spec:
  language: go
  path: services/example
`
