// Package cli implements the acm-mock-resolver command line.
package cli

import (
	"flag"
	"io"

	"github.com/spf13/cobra"
	klogv1 "k8s.io/klog"
	klogv2 "k8s.io/klog/v2"
)

// version is set at build time via:
//
//	-ldflags "-X github.com/gscode1/gs-acm-utils/internal/cli.version=v1.2.3"
var version = "dev"

// Run executes the CLI with args (without the program name) and returns the
// process exit code. It prints a returned error to stderr exactly once.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := NewRootCmd()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.Execute()
	reportError(stderr, err)

	return ExitCode(err)
}

// NewRootCmd builds the root command with all subcommands attached.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "acm-mock-resolver",
		Short:         "Resolve and validate OCM/ACM policy templates against mock data",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return asUsage(err) })

	bindKlog(root)

	root.AddCommand(newVersionCmd(), newResolveCmd(), newValidateCmd(), newMockdataCmd())

	return root
}

// bindKlog exposes the -v flag. go-template-utils logs through k8s.io/klog (v1)
// and client-go through k8s.io/klog/v2; both are driven from the one flag. Log
// output is discarded at -v 0 (the library logs a template error itself, and the
// CLI reports it once in its own format); with -v > 0 klog writes to stderr.
func bindKlog(root *cobra.Command) {
	fs1 := flag.NewFlagSet("klog", flag.ContinueOnError)
	klogv1.InitFlags(fs1)

	fs2 := flag.NewFlagSet("klog/v2", flag.ContinueOnError)
	klogv2.InitFlags(fs2)

	for _, fs := range []*flag.FlagSet{fs1, fs2} {
		_ = fs.Set("logtostderr", "false")
		_ = fs.Set("stderrthreshold", "FATAL")
	}

	vflag := fs1.Lookup("v")
	vflag.Usage = "klog verbosity level (library diagnostics on stderr)"
	root.PersistentFlags().AddGoFlag(vflag)

	root.PersistentPreRun = func(*cobra.Command, []string) {
		level := vflag.Value.String()
		_ = fs2.Set("v", level)

		if level == "0" {
			klogv1.SetOutput(io.Discard)
			klogv2.SetOutput(io.Discard)

			return
		}

		_ = fs1.Set("logtostderr", "true")
		_ = fs2.Set("logtostderr", "true")
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the acm-mock-resolver version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := io.WriteString(cmd.OutOrStdout(), version+"\n")

			return err
		},
	}
}
