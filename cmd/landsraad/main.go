package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/schema"
)

// Version is the binary version, overridden at release time with -ldflags.
var Version = "dev"

// Exit codes. See the spec, §12.
const (
	exitOK         = 0
	exitUsage      = 1
	exitValidation = 2
)

func main() {
	root := &cobra.Command{
		Use:   "landsraad",
		Short: "A lightweight developer portal for small teams",
	}
	root.AddCommand(newValidateCmd())
	root.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for service.yaml",
		Long: "Print the embedded JSON Schema. Redirect it to schema/service.schema.json " +
			"and point yaml-language-server at that file for editor autocompletion.",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := cmd.OutOrStdout().Write(schema.Raw)
			return err
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "landsraad %s\n", Version)
		},
	})
	if err := root.Execute(); err != nil {
		os.Exit(exitUsage)
	}
}
