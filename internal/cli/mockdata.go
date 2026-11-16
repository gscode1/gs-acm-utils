package cli

import (
	"fmt"

	"github.com/spf13/cobra"
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
	var dir, file string

	cmd := &cobra.Command{
		Use:   "check",
		Short: "Load and validate mock data (a cohort directory or a mock-data file)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			set, _, err := loadMockData(dir, file)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "ok: %d managed objects, %d hub objects\n",
				len(set.Managed.Objects()), len(set.Hub.Objects()))

			return err
		},
	}

	cmd.Flags().StringVar(&dir, "mock-data-dir", "", "cohort directory to check")
	cmd.Flags().StringVar(&file, "mock-data-file", "", "mock-data file to check")
	markMockDataFlags(cmd)

	return cmd
}
