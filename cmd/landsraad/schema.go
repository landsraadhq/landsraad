package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/schema"
)

// newSchemaCmd prints one of the embedded JSON Schemas, chosen by --kind, so
// editors can autocomplete against it.
func newSchemaCmd() *cobra.Command {
	var kind string
	cmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for service.yaml or standards.yaml",
		Long: "Print an embedded JSON Schema. Redirect it to schema/service.schema.json " +
			"(or, with --kind standards, schema/standards.schema.json) and point " +
			"yaml-language-server at that file for editor autocompletion.",
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw []byte
			switch kind {
			case "service":
				raw = schema.Raw
			case "standards":
				raw = config.StandardsSchema
			default:
				return fmt.Errorf("unknown --kind %q, want one of [service standards]", kind)
			}
			_, err := cmd.OutOrStdout().Write(raw)
			return err
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "service", "which schema to print: service or standards")
	return cmd
}
