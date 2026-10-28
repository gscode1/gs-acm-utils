package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unicode"

	"github.com/spf13/cobra"
	k8syaml "sigs.k8s.io/yaml"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
	"github.com/gscode1/gs-acm-utils/internal/resolve"
)

// commonFlags are the flags shared by resolve and validate (plan section 6).
type commonFlags struct {
	mockDataDir          string
	hubMode              string
	clusterName          string
	hubNamespace         string
	objectNamespace      string
	objectName           string
	paramsFile           string
	strict               bool
	skipPolicyValidation bool
	disable              []string
}

func (f *commonFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.mockDataDir, "mock-data-dir", "", "cohort directory holding the mock objects (required)")
	fl.StringVar(&f.hubMode, "hub-mode", string(resolve.HubModeResolve),
		"how {{hub ... hub}} templates are handled: resolve|passthrough|forbid")
	fl.StringVar(&f.clusterName, "cluster-name", "", "value of .ManagedClusterName (default: hub/context.yaml clusterName)")
	fl.StringVar(&f.hubNamespace, "hub-namespace", "", "namespace for hub template lookups (default: the Policy's namespace)")
	fl.StringVar(&f.objectNamespace, "object-namespace", "", "value of .ObjectNamespace")
	fl.StringVar(&f.objectName, "object-name", "", "value of .ObjectName")
	fl.StringVar(&f.paramsFile, "params", "", "YAML/JSON file with extra template context keys (exported identifiers)")
	fl.BoolVar(&f.strict, "strict", true, "fail on missing mock objects and keys (--strict=false mimics a live cluster)")
	fl.BoolVar(&f.skipPolicyValidation, "skip-policy-validation", false,
		"treat input that is not a policy kind as a raw Go template")
	fl.StringSliceVar(&f.disable, "disable", nil, "template functions to disable (comma-separated or repeated)")

	_ = cmd.MarkFlagRequired("mock-data-dir")
}

// docOptions validates the flag values and builds the per-document options.
// Problems are usage errors.
func (f *commonFlags) docOptions() (resolve.DocOptions, error) {
	mode, err := resolve.ParseHubMode(f.hubMode)
	if err != nil {
		return resolve.DocOptions{}, asUsage(fmt.Errorf("invalid --hub-mode: %w", err))
	}

	o := resolve.DocOptions{
		HubMode:              mode,
		ClusterName:          f.clusterName,
		HubNamespace:         f.hubNamespace,
		ObjectNamespace:      f.objectNamespace,
		ObjectName:           f.objectName,
		SkipPolicyValidation: f.skipPolicyValidation,
	}

	if f.paramsFile != "" {
		if o.Params, err = readParams(f.paramsFile); err != nil {
			return o, err
		}
	}

	return o, nil
}

func readParams(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, asUsage(fmt.Errorf("reading --params file: %w", unwrapPath(err)))
	}

	var params map[string]any
	if err := k8syaml.Unmarshal(data, &params); err != nil {
		return nil, usagef("--params file %s: %v", path, err)
	}

	for k := range params {
		if r := []rune(k); len(r) == 0 || !unicode.IsUpper(r[0]) {
			return nil, usagef("--params file %s: key %q must be an exported identifier (start with an uppercase letter)", path, k)
		}
	}

	return params, nil
}

func unwrapPath(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", pe.Path, pe.Err)
	}

	return err
}

// loadEngine loads the cohort (once) and builds the resolve engine. A load
// failure is returned as *mockdata.LoadErrors (exit 2).
func (f *commonFlags) loadEngine(warn io.Writer) (*resolve.Engine, error) {
	set, err := mockdata.Load(f.mockDataDir)
	if err != nil {
		return nil, err
	}

	e, err := resolve.NewEngine(set, resolve.Options{
		Strict:   f.strict,
		Disabled: f.disable,
		Cohort:   f.mockDataDir,
		Warn:     warn,
	})
	if err != nil {
		return nil, fmt.Errorf("building resolver: %w", err)
	}

	return e, nil
}
