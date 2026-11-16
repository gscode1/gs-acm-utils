package mockdata

import (
	"strings"
	"testing"
)

// objCase is one object-level problem exercised through both loaders.
type objCase struct {
	name string
	// dirPath is the object's path under objects/ (the directory layout).
	dirPath string
	// body is the object YAML.
	body string
	// want is a substring of the reported reason.
	want string
}

func indent(s string) string {
	return "    - " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n      ") + "\n"
}

func TestObjectValidationBothFormats(t *testing.T) {
	cases := []objCase{
		{
			name:    "namespace bool",
			dirPath: "v1/ConfigMap/ns-a/a.yaml",
			body:    "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n  namespace: no\ndata: {k: v}\n",
			want:    "metadata.namespace must be a string, got bool false; quote the value",
		},
		{
			name:    "name number",
			dirPath: "v1/ConfigMap/ns-a/123.yaml",
			body:    "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: 123\n  namespace: ns-a\ndata: {k: v}\n",
			want:    "metadata.name must be a string, got number 123; quote the value",
		},
		{
			name:    "misspelled ConfigMap field",
			dirPath: "v1/ConfigMap/ns-a/a.yaml",
			body:    "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n  namespace: ns-a\ndta: {k: v}\n",
			want:    `unknown field "dta" for v1 ConfigMap (allowed: apiVersion, kind, metadata, data, binaryData, immutable)`,
		},
		{
			name:    "misspelled Secret field",
			dirPath: "v1/Secret/ns-a/s.yaml",
			body:    "apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\n  namespace: ns-a\nstringdata: {k: v}\n",
			want:    `unknown field "stringdata" for v1 Secret (allowed: apiVersion, kind, metadata, data, stringData, type, immutable)`,
		},
		{
			name:    "misspelled Namespace field",
			dirPath: "v1/Namespace/ns-a.yaml",
			body:    "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: ns-a\nspce: {}\n",
			want:    `unknown field "spce" for v1 Namespace (allowed: apiVersion, kind, metadata, spec, status)`,
		},
	}

	for _, tc := range cases {
		t.Run("dir/"+tc.name, func(t *testing.T) {
			root := write(t, map[string]string{
				"objects/v1/ConfigMap/ns-ok/ok.yaml": cm("ok", "ns-ok"),
				"objects/" + tc.dirPath:              tc.body,
			})

			le := loadErr(t, root)
			if len(le.Errors) != 1 || !strings.Contains(le.Error(), tc.want) {
				t.Fatalf("errors %q, want one containing %q", le.Error(), tc.want)
			}
		})

		t.Run("file/"+tc.name, func(t *testing.T) {
			errs := fileErrs(t, fileHeader+"  managed:\n"+item("ok")+indent(tc.body))
			if len(errs) != 1 || !strings.Contains(errs[0], tc.want) || !strings.Contains(errs[0], "spec.managed[1]") {
				t.Fatalf("errors %q, want one at spec.managed[1] containing %q", errs, tc.want)
			}
		})
	}
}

func TestMissingStillMissingBothFormats(t *testing.T) {
	// A null or absent name is still "missing", not a type error.
	root := write(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/a.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name:\n  namespace: ns-a\n",
	})
	if le := loadErr(t, root); !strings.Contains(le.Error(), "missing metadata.name") {
		t.Errorf("dir: %q", le.Error())
	}

	errs := fileErrs(t, fileHeader+"  managed:\n"+indent("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name:\n  namespace: ns-a\n"))
	if len(errs) != 1 || !strings.Contains(errs[0], "missing metadata.name") {
		t.Errorf("file: %q", errs)
	}
}

func TestOtherKindsStayFreeForm(t *testing.T) {
	obj := "apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w\n  namespace: ns-a\nanything: goes\n"

	mustLoad(t, write(t, map[string]string{"objects/example.com/v1/Widget/ns-a/w.yaml": obj}))
	mustLoadFile(t, "fixture.yaml", fileHeader+"  managed:\n"+indent(obj))
}

func TestParamsKeysBothFormats(t *testing.T) {
	const want = `key "env" must be an exported identifier (start with an uppercase letter)`

	root := write(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/a.yaml": cm("a", "ns-a"),
		"params.yaml":                      "Good: 1\nenv: x\n",
	})

	le := loadErr(t, root)
	if got := le.Error(); got != "mock data: file params.yaml: params "+want {
		t.Errorf("dir: %q", got)
	}

	errs := fileErrs(t, fileHeader+"  managed:\n"+item("a")+"  params: {Good: 1, env: x}\n")
	wantErrs(t, errs, "fixture.yaml: spec.params: params "+want)

	// Exported keys load fine.
	mustLoadFile(t, "fixture.yaml", fileHeader+"  managed:\n"+item("a")+"  params: {Env: x}\n")
}

func TestCheckParamKey(t *testing.T) {
	for _, k := range []string{"Env", "Ünïcode"} {
		if err := CheckParamKey(k); err != nil {
			t.Errorf("%q: %v", k, err)
		}
	}

	for _, k := range []string{"", "env", "_X", "1A"} {
		if err := CheckParamKey(k); err == nil {
			t.Errorf("%q: want error", k)
		}
	}
}
