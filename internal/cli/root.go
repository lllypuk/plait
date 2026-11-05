// Package cli wires cobra commands to internal packages.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

const devVersion = "dev"

// NewRoot builds the plait command tree; an empty version means a build without -ldflags.
func NewRoot(version string) *cobra.Command {
	if version == "" {
		version = devVersion
	}
	root := &cobra.Command{
		Use:          "plait",
		Short:        "Guides a coding agent through the team dev-flow",
		SilenceUsage: true,
	}
	root.AddCommand(newVersionCmd(version))
	return root
}

func newVersionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the plait version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)
			return err
		},
	}
}
