package cli

import (
	"strings"
	"testing"
)

const basicFixture = "../../testdata/fixtures/basic.yaml"

func TestResolveWithMockDataFileMatchesDir(t *testing.T) {
	in := policies + "/policy-basic.yaml"

	code, want, errs := run(t, "", "resolve", "--mock-data-dir", basic, in)
	wantCode(t, code, ExitOK, errs)

	code, got, errs := run(t, "", "resolve", "--mock-data-file", basicFixture, in)
	wantCode(t, code, ExitOK, errs)

	if got != want || errs != "" {
		t.Fatalf("file output differs from dir output:\n--- dir\n%s\n--- file\n%s\nstderr: %s", want, got, errs)
	}
}

func TestValidateWithMockDataFile(t *testing.T) {
	code, out, errs := run(t, "", "validate", "--mock-data-file", basicFixture, policies+"/")
	wantCode(t, code, ExitOK, errs)
	contains(t, "stdout", out, "0 failed")
}

func TestMockdataCheckFile(t *testing.T) {
	code, out, errs := run(t, "", "mockdata", "check", "--mock-data-file", basicFixture)
	wantCode(t, code, ExitOK, errs)

	if out != "ok: 4 managed objects, 2 hub objects\n" {
		t.Fatalf("stdout %q", out)
	}

	bad := writeTemp(t, "fixture.yaml", "apiVersion: acm-utils.gskode.com/v1alpha1\nkind: MockData\nspec: {}\n")

	code, out, errs = run(t, "", "mockdata", "check", "--mock-data-file", bad)
	wantCode(t, code, ExitMockData, errs)

	if out != "" {
		t.Fatalf("stdout %q", out)
	}

	contains(t, "stderr", errs, "fixture.yaml: spec.managed: no objects found", "mock data failed to load: 1 error")

	code, _, errs = run(t, "", "mockdata", "check", "--mock-data-file", "/nonexistent/f.yaml")
	wantCode(t, code, ExitMockData, errs)
	contains(t, "stderr", errs, "/nonexistent/f.yaml: does not exist")
}

func TestMockDataFlagExclusivity(t *testing.T) {
	in := policies + "/policy-basic.yaml"

	cases := map[string][]string{
		"resolve both":     {"resolve", "--mock-data-dir", basic, "--mock-data-file", basicFixture, in},
		"resolve neither":  {"resolve", in},
		"validate both":    {"validate", "--mock-data-dir", basic, "--mock-data-file", basicFixture, policies},
		"validate neither": {"validate", policies},
		"check both":       {"mockdata", "check", "--mock-data-dir", basic, "--mock-data-file", basicFixture},
		"check neither":    {"mockdata", "check"},
	}

	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, out, errs := run(t, "", args...)
			wantCode(t, code, ExitUsage, errs)

			if out != "" || !strings.HasPrefix(errs, "error: ") || !strings.Contains(errs, "mock-data-dir") {
				t.Fatalf("stdout %q stderr %q", out, errs)
			}
		})
	}
}

// --params merges over the file's spec.params; --cluster-name wins over hubContext.clusterName.
func TestMockDataFilePrecedence(t *testing.T) {
	fixture := writeTemp(t, "fixture.yaml", `apiVersion: acm-utils.gskode.com/v1alpha1
kind: MockData
spec:
  managed:
    - apiVersion: v1
      kind: ConfigMap
      metadata: {name: c, namespace: ns-a}
      data: {k: v}
  hub:
    - apiVersion: v1
      kind: ConfigMap
      metadata: {name: h, namespace: ns-a}
      data: {k: v}
  hubContext:
    clusterName: from-file
  params:
    Env: file
    Region: eu
`)

	tmpl := writeTemp(t, "t.yaml", "cluster: '{{hub .ManagedClusterName hub}}'\nenv: '{{ .Env }}'\nregion: '{{ .Region }}'\n")
	params := writeTemp(t, "params.yaml", "Env: cli\n")

	run1 := func(extra ...string) string {
		args := append([]string{"resolve", "--skip-policy-validation", "--hub-namespace", "ns-a", "--mock-data-file", fixture}, extra...)
		code, out, errs := run(t, "", append(args, tmpl)...)
		wantCode(t, code, ExitOK, errs)

		return out
	}

	contains(t, "defaults", run1(), "cluster: from-file", "env: file", "region: eu")
	contains(t, "overrides", run1("--cluster-name", "flag", "--params", params),
		"cluster: flag", "env: cli", "region: eu")
}

func TestMockDataFileErrorMessagesReadNaturally(t *testing.T) {
	fixture := writeTemp(t, "fixture.yaml", `apiVersion: acm-utils.gskode.com/v1alpha1
kind: MockData
spec:
  managed:
    - apiVersion: v1
      kind: ConfigMap
      metadata: {name: c, namespace: ns-a}
      data: {k: v}
`)

	cases := map[string]struct {
		tmpl string
		want string
	}{
		"hub missing":      {`'{{hub "x" hub}}'`, "spec.hub (" + fixture + ") is absent"},
		"namespace":        {`'{{ fromConfigMap "ns-z" "c" "k" }}'`, "no v1/ConfigMap objects in namespace ns-z under spec.managed (" + fixture + ")"},
		"object":           {`'{{ (lookup "v1" "ConfigMap" "ns-a" "zzz").data }}'`, "object not found: v1/ConfigMap ns-a/zzz in spec.managed (" + fixture + ")"},
		"unsupported kind": {`'{{ lookup "apps/v1" "Deployment" "ns-a" "x" }}'`, "no objects of that Kind in spec.managed (" + fixture + ")"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tmpl := writeTemp(t, "t.yaml", "v: "+c.tmpl+"\n")

			code, _, errs := run(t, "", "resolve", "--skip-policy-validation", "--mock-data-file", fixture, tmpl)
			wantCode(t, code, ExitFailed, errs)
			contains(t, "stderr", errs, c.want)

			if strings.Contains(errs, "objects/") {
				t.Fatalf("directory wording leaked into file-format error:\n%s", errs)
			}
		})
	}
}
