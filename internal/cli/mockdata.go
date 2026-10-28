package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

func newMockdataCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mockdata",
		Short: "Work with mock-data cohorts",
	}

	cmd.AddCommand(newMockdataCheckCmd())

	return cmd
}

func newMockdataCheckCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Load and validate a mock-data cohort",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			set, err := mockdata.Load(dir)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "ok: %d managed objects, %d hub objects\n",
				len(set.Managed.Objects()), len(set.Hub.Objects()))

			return err
		},
	}

	cmd.Flags().StringVar(&dir, "mock-data-dir", "", "cohort directory to check (required)")
	_ = cmd.MarkFlagRequired("mock-data-dir")

	return cmd
}
