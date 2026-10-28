package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/gscode1/gs-acm-utils/internal/input"
)

func newResolveCmd() *cobra.Command {
	var f commonFlags

	cmd := &cobra.Command{
		Use:   "resolve [FILE|-]",
		Short: "Resolve the templates of one input and print the resulting YAML",
		Long: "Resolve reads one input (a file, or stdin when FILE is - or absent), resolves " +
			"its templates against the mock data and prints the result to stdout.\n" +
			"A multi-document input is resolved document by document and the results are joined with ---.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResolve(cmd, &f, args)
		},
	}

	f.bind(cmd)

	return cmd
}

func runResolve(cmd *cobra.Command, f *commonFlags, args []string) error {
	opts, err := f.docOptions()
	if err != nil {
		return err
	}

	src, err := readSingleInput(cmd.InOrStdin(), args)
	if err != nil {
		return asUsage(err)
	}

	if len(src.Docs) == 0 {
		return usagef("%s: no YAML documents found", src.Path)
	}

	engine, err := f.loadEngine(cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	var (
		out    bytes.Buffer
		failed int
		stderr = cmd.ErrOrStderr()
	)

	for i, d := range src.Docs {
		res, err := engine.ResolveDocument(d.Data, opts)
		if err != nil {
			failed++

			if src.Multi() {
				fmt.Fprintf(stderr, "document #%d: %v\n", d.Index, err)
			} else {
				fmt.Fprintf(stderr, "error: %v\n", err)
			}

			continue
		}

		if i > 0 {
			out.WriteString("---\n")
		}

		out.Write(res)
	}

	if failed > 0 {
		// Nothing is written to stdout when any document fails, so a partial
		// result is never mistaken for a good one.
		return &failureError{err: fmt.Errorf("%d of %d documents failed to resolve", failed, len(src.Docs)), Reported: true}
	}

	_, err = cmd.OutOrStdout().Write(out.Bytes())

	return err
}

func readSingleInput(stdin io.Reader, args []string) (*input.Source, error) {
	if len(args) == 0 || args[0] == "-" {
		return input.FromReader(stdin, input.StdinName)
	}

	st, err := os.Stat(args[0])
	if err != nil {
		return nil, err
	}

	if st.IsDir() {
		return nil, fmt.Errorf("%s is a directory; use the validate command for directories", args[0])
	}

	return input.FromFile(args[0])
}
