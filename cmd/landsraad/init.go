package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// diagText exists so tests can name the default formatter without importing
// the whole registry surface.
func diagText() diag.Formatter { return diag.Text{} }

// scaffold is what `landsraad init` writes. Every file is valid on the first
// run: the acceptance test for init is that validate passes immediately after.
var scaffold = []struct {
	path string
	body string
}{
	{"teams.yaml", `teams:
  - name: team-example
    members: [you]
    slack: "#team-example"
    pagerduty: EXAMPLE
`},
	{"repos.yaml", `# The first entry is the repository you are in.
repos:
  - url: https://github.com/your-org/your-repo
    paths: [services/*, workers/*, libs/*]
`},
	{"services/example/service.yaml", `# yaml-language-server: $schema=../../schema/service.schema.json
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
`},
}

func runInit(root string, out io.Writer) error {
	for _, f := range scaffold {
		full := filepath.Join(root, filepath.FromSlash(f.path))
		if _, err := os.Stat(full); err == nil {
			fmt.Fprintf(out, "  skip    %s (already exists)\n", f.path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, []byte(f.body), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "  created %s\n", f.path)
	}
	fmt.Fprintf(out, "\nNext: run `landsraad schema > schema/service.schema.json` for editor\n"+
		"autocompletion, then `landsraad validate`.\n")
	return nil
}

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [root]",
		Short: "Missionaria — scaffold teams.yaml, repos.yaml and a first service",
		Long: "Write a minimal, valid catalog into this repository. Existing files " +
			"are never overwritten. What init produces passes `landsraad validate` " +
			"on the first run.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			return runInit(root, cmd.OutOrStdout())
		},
	}
}
