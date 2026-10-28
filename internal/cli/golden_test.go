package cli

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "regenerate testdata/golden output.yaml / error.txt files")

const (
	goldenDir = "../../testdata/golden"
	// sharedCohort holds the resources of upstream's testdata/setup; cases
	// without their own mock/ directory resolve against it.
	sharedCohort = goldenDir + "/_cohorts/upstream-setup"
)

// templateErr is the regexp upstream's client_test.go applies to a resolver
// error before comparing it with error.txt.
var templateErr = regexp.MustCompile("template: tmpl:([0-9]+:){1,2} .*")

// TestGolden runs the resolve command for every testdata/golden/<case>. A case
// directory holds input.yaml, optionally mock/ (its own cohort) or a one-line
// cohort file (path of a cohort relative to the case; otherwise the shared
// upstream-setup cohort is used), optionally args (extra CLI flags,
// whitespace separated), and either output.yaml (expected stdout) or error.txt
// (expected error text). See testdata/golden/README.md.
func TestGolden(t *testing.T) {
	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatal(err)
	}

	n := 0

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}

		n++

		t.Run(e.Name(), func(t *testing.T) { goldenCase(t, filepath.Join(goldenDir, e.Name())) })
	}

	if n == 0 {
		t.Fatal("no golden cases found")
	}
}

func goldenCase(t *testing.T, dir string) {
	t.Helper()

	cohort := sharedCohort
	if _, err := os.Stat(filepath.Join(dir, "mock")); err == nil {
		cohort = filepath.Join(dir, "mock")
	} else if b, err := os.ReadFile(filepath.Join(dir, "cohort")); err == nil {
		cohort = filepath.Join(dir, strings.TrimSpace(string(b)))
	}

	// Same defaults upstream's client_test.go uses for every case.
	args := []string{
		"resolve", "--mock-data-dir", cohort,
		"--object-namespace", "my-obj-namespace", "--object-name", "my-obj-name",
	}

	if b, err := os.ReadFile(filepath.Join(dir, "args")); err == nil {
		args = append(args, strings.Fields(string(b))...)
	}

	args = append(args, filepath.Join(dir, "input.yaml"))

	code, stdout, stderr := run(t, "", args...)

	outPath, errPath := filepath.Join(dir, "output.yaml"), filepath.Join(dir, "error.txt")
	_, errErr := os.Stat(errPath)
	wantErrCase := errErr == nil

	if *update {
		updateGolden(t, code, stdout, stderr, outPath, errPath, wantErrCase)

		return
	}

	if wantErrCase {
		wantCode(t, code, ExitFailed, stderr)

		if stdout != "" {
			t.Fatalf("stdout must be empty on failure, got %q", stdout)
		}

		want, err := os.ReadFile(errPath)
		if err != nil {
			t.Fatal(err)
		}

		if got := goldenErrText(string(want), stderr); got != strings.TrimSpace(string(want)) {
			t.Fatalf("error text differs\n--- got\n%s\n--- want\n%s", got, want)
		}

		return
	}

	wantCode(t, code, ExitOK, stderr)

	want, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal([]byte(stdout), want) {
		t.Fatalf("output differs\n--- got\n%s\n--- want\n%s", stdout, want)
	}
}

// goldenErrText extracts the comparable error text from stderr: the first
// "error: " line without its prefix. When the expectation is a template error
// (as in upstream's error.txt files) only the "template: tmpl:N: ..." part is
// compared, exactly like upstream's test.
func goldenErrText(want, stderr string) string {
	got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stderr), "error: "))

	if strings.HasPrefix(want, "template: tmpl:") {
		if m := templateErr.FindString(got); m != "" {
			return m
		}
	}

	return got
}

// updateGolden rewrites the expectation. A case that has an error.txt must
// keep failing; otherwise the exit code decides: 0 writes output.yaml, 1 writes
// error.txt (create the case directory with just input.yaml and run -update).
func updateGolden(t *testing.T, code int, stdout, stderr, outPath, errPath string, hadErr bool) {
	t.Helper()

	if hadErr && code != ExitFailed {
		t.Fatalf("exit code %d, want %d for an error case; stderr:\n%s", code, ExitFailed, stderr)
	}

	if code == ExitFailed {

		text := goldenErrText("template: tmpl:", stderr)
		if err := os.WriteFile(errPath, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}

		return
	}

	wantCode(t, code, ExitOK, stderr)

	if err := os.WriteFile(outPath, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
}
