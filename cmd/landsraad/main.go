package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
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
	root.AddCommand(newInitCmd())
	root.AddCommand(newGenCmd())
	root.AddCommand(newSchemaCmd())
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "landsraad %s\n", version())
		},
	})
	if err := root.Execute(); err != nil {
		os.Exit(exitUsage)
	}
}
