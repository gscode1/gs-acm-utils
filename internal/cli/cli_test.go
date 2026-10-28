package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	basic    = "../../testdata/cohorts/basic"
	policies = "../../testdata/policies"
	failing  = "../../testdata/policies-failing"
	broken   = "../../testdata/errors/aggregate-two-broken"
)

func run(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errb bytes.Buffer

	code = Run(args, strings.NewReader(stdin), &out, &errb)

	return code, out.String(), errb.String()
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	return p
}

func wantCode(t *testing.T, got, want int, stderr string) {
	t.Helper()

	if got != want {
		t.Fatalf("exit code %d, want %d; stderr:\n%s", got, want, stderr)
	}
}

func contains(t *testing.T, what, got string, subs ...string) {
	t.Helper()

	for _, s := range subs {
		if !strings.Contains(got, s) {
			t.Errorf("%s missing %q:\n%s", what, s, got)
		}
	}
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, "", "version")
	wantCode(t, code, ExitOK, "")

	if strings.TrimSpace(out) != version {
		t.Fatalf("got %q", out)
	}
}

func TestResolveBasicPolicy(t *testing.T) {
	code, out, errs := run(t, "", "resolve", "--mock-data-dir", basic, policies+"/policy-basic.yaml")
	wantCode(t, code, ExitOK, errs)

	contains(t, "stdout", out,
		"cluster: local-cluster",
		"color: blue",
		"password: s3cr3t-plaintext",
		`size: "3"`,
	)

	if strings.Contains(out, "{{") || errs != "" {
		t.Fatalf("unresolved template or stderr noise:\n%s\n---\n%s", out, errs)
	}
}

func TestResolveStdin(t *testing.T) {
	in, err := os.ReadFile(policies + "/config-policy.yaml")
	if err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"resolve", "--mock-data-dir", basic},
		{"resolve", "--mock-data-dir", basic, "-"},
	} {
		code, out, errs := run(t, string(in), args...)
		wantCode(t, code, ExitOK, errs)
		contains(t, "stdout", out, "clusterId: 7b1c0f4e-8f1d-4f0a-9a57-000000000001", "regions: eu-west-1")
	}
}

func TestResolveMultiDoc(t *testing.T) {
	code, out, errs := run(t, "", "resolve", "--mock-data-dir", basic, policies+"/multi-doc.yaml")
	wantCode(t, code, ExitOK, errs)

	if n := strings.Count(out, "\n---\n"); n != 1 {
		t.Fatalf("want 1 separator, got %d:\n%s", n, out)
	}

	contains(t, "stdout", out, "color: blue", "region: eu-west-1")

	if strings.HasPrefix(out, "---") {
		t.Fatal("output must not start with a separator")
	}
}

func TestResolveFailureExit1(t *testing.T) {
	code, out, errs := run(t, "", "resolve", "--mock-data-dir", basic, failing+"/missing-key.yaml")
	wantCode(t, code, ExitFailed, errs)
	contains(t, "stderr", errs, `error: mock data: key "nope" not found`)

	if out != "" {
		t.Fatalf("stdout must be empty on failure, got %q", out)
	}

	if strings.Count(errs, "nope") != 1 {
		t.Fatalf("error should be printed once (klog must be quiet):\n%s", errs)
	}
}

func TestResolveMultiDocFailureNumbersDocument(t *testing.T) {
	code, out, errs := run(t, "", "resolve", "--mock-data-dir", basic, failing+"/multi-doc-mixed.yaml")
	wantCode(t, code, ExitFailed, errs)
	contains(t, "stderr", errs, "document #2: mock data: namespace directory missing")

	if out != "" {
		t.Fatalf("stdout must be empty on failure, got %q", out)
	}
}

func TestStrictFalseLetsMissingKeyPass(t *testing.T) {
	file := failing + "/missing-key.yaml"

	code, _, errs := run(t, "", "resolve", "--mock-data-dir", basic, file)
	wantCode(t, code, ExitFailed, errs)

	code, out, errs := run(t, "", "resolve", "--mock-data-dir", basic, "--strict=false", file)
	wantCode(t, code, ExitOK, errs)
	contains(t, "stdout", out, "kind: ConfigMap")
}

func TestHubModes(t *testing.T) {
	file := policies + "/policy-basic.yaml"

	code, _, errs := run(t, "", "resolve", "--mock-data-dir", basic, "--hub-mode=forbid", file)
	wantCode(t, code, ExitFailed, errs)

	code, out, errs := run(t, "", "resolve", "--mock-data-dir", basic, "--hub-mode=passthrough", file)
	wantCode(t, code, ExitOK, errs)
	contains(t, "stdout", out, "cluster: .ManagedClusterName")
	contains(t, "stderr", errs, "warning: --hub-mode=passthrough")
}

func TestHubFlags(t *testing.T) {
	cp := writeTemp(t, "cp.yaml", `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata:
          name: x
        data:
          hub: '{{hub fromConfigMap "policies" "hub-config" "logLevel" hub}}'
          cluster: '{{hub .ManagedClusterName hub}}'
          objNs: '{{ .ObjectNamespace }}/{{ .ObjectName }}'
`)

	code, _, errs := run(t, "", "resolve", "--mock-data-dir", basic, cp)
	wantCode(t, code, ExitFailed, errs)
	contains(t, "stderr", errs, "a hub namespace must be provided (--hub-namespace) when resolving hub templates")

	code, out, errs := run(t, "", "resolve", "--mock-data-dir", basic, "--hub-namespace", "policies",
		"--cluster-name", "other", "--object-namespace", "ns1", "--object-name", "n1", cp)
	wantCode(t, code, ExitOK, errs)
	contains(t, "stdout", out, "hub: debug", "cluster: other", "objNs: ns1/n1")
}

func TestParamsAndDisable(t *testing.T) {
	cp := writeTemp(t, "cp.yaml", `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata: {name: x}
        data:
          env: '{{ .Env }}'
`)
	params := writeTemp(t, "params.yaml", "Env: prod\n")

	code, out, errs := run(t, "", "resolve", "--mock-data-dir", basic, "--params", params, cp)
	wantCode(t, code, ExitOK, errs)
	contains(t, "stdout", out, "env: prod")

	bad := writeTemp(t, "bad.yaml", "env: prod\n")
	code, _, errs = run(t, "", "resolve", "--mock-data-dir", basic, "--params", bad, cp)
	wantCode(t, code, ExitUsage, errs)
	contains(t, "stderr", errs, "exported identifier")

	code, _, errs = run(t, "", "resolve", "--mock-data-dir", basic, "--params", "/nonexistent/p.yaml", cp)
	wantCode(t, code, ExitUsage, errs)

	code, _, errs = run(t, "", "resolve", "--mock-data-dir", basic, "--disable", "fromConfigMap",
		policies+"/multi-doc.yaml")
	wantCode(t, code, ExitFailed, errs)
}

func TestValidatePassingDir(t *testing.T) {
	code, out, errs := run(t, "", "validate", "--mock-data-dir", basic, policies)
	wantCode(t, code, ExitOK, errs)

	if out != "4 passed, 0 failed\n" || errs != "" {
		t.Fatalf("stdout %q stderr %q", out, errs)
	}
}

func TestValidateMixedDir(t *testing.T) {
	code, out, errs := run(t, "", "validate", "--mock-data-dir", basic, failing)
	wantCode(t, code, ExitFailed, errs)

	if out != "2 passed, 2 failed\n" {
		t.Fatalf("stdout %q", out)
	}

	lines := strings.Split(strings.TrimSpace(errs), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 stderr lines, got:\n%s", errs)
	}

	// single-document file: no index; multi-document file: 1-based index.
	if !strings.HasPrefix(lines[0], failing+"/missing-key.yaml: mock data: key \"nope\" not found") {
		t.Errorf("line 0: %s", lines[0])
	}

	if !strings.HasPrefix(lines[1], failing+"/multi-doc-mixed.yaml#2: mock data: namespace directory missing") {
		t.Errorf("line 1: %s", lines[1])
	}

	if strings.Contains(errs, "ok.yaml") || strings.Contains(errs, "multi-doc-mixed.yaml#1") {
		t.Errorf("passing documents must not be reported:\n%s", errs)
	}
}

func TestValidateBrokenCohortProcessesNothing(t *testing.T) {
	code, out, errs := run(t, "", "validate", "--mock-data-dir", broken, policies)
	wantCode(t, code, ExitMockData, errs)

	if out != "" {
		t.Fatalf("no summary expected, got %q", out)
	}

	contains(t, "stderr", errs, "mock data: file objects/v1/ConfigMap/ns-a/a.yaml", "mock data failed to load: 2 errors")

	if strings.Count(errs, "\n") != 3 {
		t.Fatalf("want 2 error lines + count line:\n%s", errs)
	}
}

func TestValidateYAMLParseErrorIsFailure(t *testing.T) {
	f := writeTemp(t, "bad.yaml", "key: [unclosed\n")

	code, out, errs := run(t, "", "validate", "--mock-data-dir", basic, f)
	wantCode(t, code, ExitFailed, errs)

	if out != "0 passed, 1 failed\n" {
		t.Fatalf("stdout %q", out)
	}
}

func TestValidateNoInputs(t *testing.T) {
	code, _, errs := run(t, "", "validate", "--mock-data-dir", basic, t.TempDir())
	wantCode(t, code, ExitUsage, errs)
	contains(t, "stderr", errs, "no input files found")
}

func TestResolveBrokenCohortExit2(t *testing.T) {
	code, _, errs := run(t, "", "resolve", "--mock-data-dir", broken, policies+"/policy-basic.yaml")
	wantCode(t, code, ExitMockData, errs)

	code, _, errs = run(t, "", "resolve", "--mock-data-dir", "/nonexistent/cohort", policies+"/policy-basic.yaml")
	wantCode(t, code, ExitMockData, errs)
	contains(t, "stderr", errs, "does not exist")
}

func TestUsageErrorsExit3(t *testing.T) {
	file := policies + "/policy-basic.yaml"

	cases := map[string][]string{
		"missing --mock-data-dir":   {"resolve", file},
		"missing --mock-data-dir v": {"validate", policies},
		"bad --hub-mode":            {"resolve", "--mock-data-dir", basic, "--hub-mode", "bogus", file},
		"nonexistent input":         {"resolve", "--mock-data-dir", basic, "/nonexistent/in.yaml"},
		"nonexistent validate path": {"validate", "--mock-data-dir", basic, "/nonexistent"},
		"unknown flag":              {"resolve", "--mock-data-dir", basic, "--nope", file},
		"validate without paths":    {"validate", "--mock-data-dir", basic},
		"too many resolve args":     {"resolve", "--mock-data-dir", basic, file, file},
		"directory to resolve":      {"resolve", "--mock-data-dir", basic, policies},
		"unknown command":           {"bogus"},
		"mockdata check no flag":    {"mockdata", "check"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, out, errs := run(t, "", args...)
			wantCode(t, code, ExitUsage, errs)

			if out != "" || !strings.HasPrefix(errs, "error: ") || strings.Count(errs, "\n") != 1 {
				t.Fatalf("want one clean error line; stdout %q stderr %q", out, errs)
			}
		})
	}
}

func TestResolveEmptyInputExit3(t *testing.T) {
	code, _, errs := run(t, "# nothing\n---\n", "resolve", "--mock-data-dir", basic)
	wantCode(t, code, ExitUsage, errs)
	contains(t, "stderr", errs, "no YAML documents")
}

func TestMockdataCheck(t *testing.T) {
	code, out, errs := run(t, "", "mockdata", "check", "--mock-data-dir", basic)
	wantCode(t, code, ExitOK, errs)

	if out != "ok: 4 managed objects, 2 hub objects\n" {
		t.Fatalf("stdout %q", out)
	}

	code, out, errs = run(t, "", "mockdata", "check", "--mock-data-dir", broken)
	wantCode(t, code, ExitMockData, errs)

	if out != "" {
		t.Fatalf("stdout %q", out)
	}

	contains(t, "stderr", errs, "mock data failed to load: 2 errors")
}

func TestExitCodeMapping(t *testing.T) {
	if ExitCode(nil) != 0 {
		t.Fatal("nil must map to 0")
	}

	if ExitCode(usagef("x")) != 3 || ExitCode(&failureError{err: os.ErrInvalid}) != 1 {
		t.Fatal("mapping")
	}
}

// Definition of done (plan section 10): validating the example policies against
// the basic cohort needs no kubeconfig and no network.
func TestDoDValidateWithoutKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "/nonexistent")
	t.Setenv("HOME", t.TempDir())

	code, out, errs := run(t, "", "validate", "--mock-data-dir", basic, policies+"/")
	wantCode(t, code, ExitOK, errs)
	contains(t, "stdout", out, "0 failed")

	if errs != "" {
		t.Fatalf("unexpected stderr:\n%s", errs)
	}
}
