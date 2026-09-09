package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/scaffold"
)

// diagText exists so tests can name the default formatter without importing
// the whole registry surface.
func diagText() diag.Formatter { return diag.Text{} }

func runInit(root string, out io.Writer) error {
	// What to write is scaffold.Files(), a pure value. This loop is the only
	// part that touches the filesystem, and it lives in cmd/ for the reason in
	// spec §3.1. Plan 2's generators produce the same shape and reuse it.
	for _, f := range scaffold.Files() {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		if _, err := os.Stat(full); err == nil {
			fmt.Fprintf(out, "  skip    %s (already exists)\n", f.Path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, f.Data, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "  created %s\n", f.Path)
	}
	fmt.Fprintf(out, "\nNext: run `landsraad validate`. Editor autocompletion is already wired up:\n"+
		"service.yaml points at schema/service.schema.json.\n")
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
