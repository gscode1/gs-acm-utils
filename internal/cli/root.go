// Package cli implements the acm-mock-resolver command line.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is set at build time via:
//
//	-ldflags "-X github.com/gscode1/gs-acm-utils/internal/cli.version=v1.2.3"
var version = "dev"

// NewRootCmd builds the root command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "acm-mock-resolver",
		Short:         "Resolve and validate OCM/ACM policy templates against mock data",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newVersionCmd())

	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the acm-mock-resolver version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version)

			return err
		},
	}
}
