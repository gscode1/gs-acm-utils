package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/gscode1/gs-acm-utils/internal/input"
)

func newValidateCmd() *cobra.Command {
	var f commonFlags

	cmd := &cobra.Command{
		Use:   "validate PATH...",
		Short: "Resolve many inputs and report every failure",
		Long: "Validate resolves every document of the given files and directories (recursively, " +
			".yaml/.yml, dotfiles skipped) against the mock data, which is loaded once up front.\n" +
			"Each failure is printed to stderr as <path>[#<doc>]: <error>; the document number is 1-based and " +
			"shown only for multi-document files. A summary \"N passed, M failed\" (counted in documents) " +
			"is printed to stdout. Nothing else is printed on success.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd, &f, args)
		},
	}

	f.bind(cmd)

	return cmd
}

func runValidate(cmd *cobra.Command, f *commonFlags, args []string) error {
	opts, err := f.docOptions()
	if err != nil {
		return err
	}

	sources, err := input.Collect(args)
	if err != nil {
		return asUsage(err)
	}

	engine, err := f.loadEngine(cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	var passed, failed int

	stderr := cmd.ErrOrStderr()

	for _, s := range sources {
		for _, d := range s.Docs {
			if _, err := engine.ResolveDocument(d.Data, opts); err != nil {
				failed++

				fmt.Fprintf(stderr, "%s: %v\n", s.Label(d), err)

				continue
			}

			passed++
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%d passed, %d failed\n", passed, failed)

	if failed > 0 {
		return &failureError{err: errors.New("validation failed"), Reported: true}
	}

	return nil
}
