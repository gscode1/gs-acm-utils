package resolve

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

// writeCohort writes files (cohort-relative path -> body) into a temp dir and loads it.
func writeCohort(t *testing.T, files map[string]string) *mockdata.Set {
	t.Helper()

	root := t.TempDir()

	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return loadSet(t, root)
}

// policyCohort has two ConfigMaps in ns-a and nothing else.
func policyCohort(t *testing.T) *Engine {
	t.Helper()

	return newEngine(t, writeCohort(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/one.yaml": `apiVersion: v1
kind: ConfigMap
metadata: {name: one, namespace: ns-a}
data: {k: v1, color: blue}
`,
		"objects/v1/ConfigMap/ns-a/two.yaml": `apiVersion: v1
kind: ConfigMap
metadata: {name: two, namespace: ns-a}
data: {k: v2}
`,
	}), true)
}

// sameYAML fails unless got and want are the same YAML document.
func sameYAML(t *testing.T, got []byte, want string) {
	t.Helper()

	var g, w any
	if err := yaml.Unmarshal(got, &g); err != nil {
		t.Fatalf("parse got: %v\n%s", err, got)
	}

	if err := yaml.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("parse want: %v", err)
	}

	if !reflect.DeepEqual(g, w) {
		t.Fatalf("output mismatch\n--- got\n%s\n--- want\n%s", got, want)
	}
}

// Mandatory (plan T5): >=2 object-templates in one ConfigurationPolicy. Strict
// functions resolve in both, and missing mock data in the 2nd fails with the
// catalog error.
func TestConfigurationPolicyMultipleObjectTemplates(t *testing.T) {
	e := policyCohort(t)

	const head = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: multi}
spec:
  remediationAction: inform
  object-templates:
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: first
          labels:
            val: '{{ fromConfigMap "ns-a" "one" "k" }}'
`

	t.Run("both resolve", func(t *testing.T) {
		out, err := e.ResolveDocument([]byte(head+`    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: second
          labels:
            val: '{{ fromConfigMap "ns-a" "two" "k" }}'
            looked: '{{ (lookup "v1" "ConfigMap" "ns-a" "one").data.color }}'
`), DocOptions{})
		if err != nil {
			t.Fatal(err)
		}

		sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: multi}
spec:
  remediationAction: inform
  object-templates:
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata: {name: first, labels: {val: v1}}
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata: {name: second, labels: {val: v2, looked: blue}}
`)
	})

	t.Run("missing object in 2nd is E2", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(head+`    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: second
          labels:
            val: '{{ fromConfigMap "ns-a" "gone" "k" }}'
`), DocOptions{})

		var e2 *mockdata.ErrObjectMissing
		if !errors.As(err, &e2) {
			t.Fatalf("want E2, got %v", err)
		}

		want := `mock data: object not found: objects/v1/ConfigMap/ns-a/gone.yaml ` +
			`(referenced by fromConfigMap "ns-a" "gone" "k")`
		if err.Error() != want {
			t.Fatalf("got %q\nwant %q", err.Error(), want)
		}
	})

	t.Run("missing key in 2nd is E3", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(head+`    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: second
          labels:
            val: '{{ fromConfigMap "ns-a" "two" "nokey" }}'
`), DocOptions{})

		var e3 *mockdata.ErrKeyMissing
		if !errors.As(err, &e3) {
			t.Fatalf("want E3, got %v", err)
		}

		if want := `mock data: key "nokey" not found in objects/v1/ConfigMap/ns-a/two.yaml (data has: k)`; err.Error() != want {
			t.Fatalf("got %q\nwant %q", err.Error(), want)
		}
	})
}

func TestPolicyKind(t *testing.T) {
	e := policyCohort(t)

	out, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata: {name: p, namespace: policies}
spec:
  disabled: false
  policy-templates:
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata: {name: cp}
        spec:
          object-templates:
            - complianceType: musthave
              objectDefinition:
                apiVersion: v1
                kind: ConfigMap
                metadata:
                  name: '{{ fromConfigMap "ns-a" "one" "k" }}'
                  namespace: '{{ .ObjectNamespace }}'
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1beta1
        kind: OperatorPolicy
        metadata: {name: op}
        spec:
          subscription: {name: '{{ fromConfigMap "ns-a" "two" "k" }}'}
    - objectDefinition:
        apiVersion: v1
        kind: Other
        data: '{{ not touched }}'
`), DocOptions{ObjectNamespace: "ns-obj"})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata: {name: p, namespace: policies}
spec:
  disabled: false
  policy-templates:
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata: {name: cp}
        spec:
          object-templates:
            - complianceType: musthave
              objectDefinition:
                apiVersion: v1
                kind: ConfigMap
                metadata: {name: v1, namespace: ns-obj}
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1beta1
        kind: OperatorPolicy
        metadata: {name: op}
        spec:
          subscription: {name: v2}
    - objectDefinition:
        apiVersion: v1
        kind: Other
        data: '{{ not touched }}'
`)

	t.Run("error carries policy-templates index", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: Policy
metadata: {name: p, namespace: policies}
spec:
  policy-templates:
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata: {name: cp}
        spec:
          object-templates:
            - objectDefinition:
                data: '{{ fromConfigMap "ns-a" "nope" "k" }}'
`), DocOptions{})

		var e2 *mockdata.ErrObjectMissing
		if !errors.As(err, &e2) || !strings.HasSuffix(err.Error(), "(in policy-templates at index 0)") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("invalid policy-templates", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte("apiVersion: policy.open-cluster-management.io/v1\nkind: Policy\nspec: {}\n"),
			DocOptions{})
		if err == nil || !strings.Contains(err.Error(), "spec.policy-templates keys not found") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestConfigurationPolicyKind(t *testing.T) {
	e := policyCohort(t)

	out, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata: {name: '{{ .ObjectName }}'}
        data: {k: '{{ fromConfigMap "ns-a" "one" "k" }}'}
`), DocOptions{ObjectName: "obj-1"})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        apiVersion: v1
        kind: ConfigMap
        metadata: {name: obj-1}
        data: {k: v1}
`)
}

func TestSkipObject(t *testing.T) {
	e := policyCohort(t)

	out, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata: {name: skipped, labels: {s: '{{ skipObject (eq (fromConfigMap "ns-a" "one" "k") "v1") }}'}}
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata: {name: kept, labels: {s: '{{ skipObject false }}'}}
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata: {name: also-kept}
`), DocOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// skipObject state must not leak from the skipped object into the next ones.
	sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata: {name: kept, labels: {s: ""}}
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata: {name: also-kept}
`)

	_, err = e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - objectDefinition: {x: '{{ skipObject "notabool" }}'}
`), DocOptions{})
	if err == nil || !strings.Contains(err.Error(), "expected boolean but received 'notabool'") {
		t.Fatalf("got %v", err)
	}
}

func TestOperatorPolicyKind(t *testing.T) {
	e := policyCohort(t)

	out, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1beta1
kind: OperatorPolicy
metadata: {name: op}
spec:
  complianceType: musthave
  operatorGroup: {name: '{{ .ObjectName }}-og'}
  subscription:
    name: '{{ fromConfigMap "ns-a" "one" "k" }}'
    namespace: openshift-operators
  versions:
    - ' {{ fromConfigMap "ns-a" "two" "k" }} '
    - '{{ "" }}'
`), DocOptions{ObjectName: "x"})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1beta1
kind: OperatorPolicy
metadata: {name: op}
spec:
  complianceType: musthave
  operatorGroup: {name: x-og}
  subscription: {name: v1, namespace: openshift-operators}
  versions: [v2]
`)

	t.Run("subscription required", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(
			"apiVersion: policy.open-cluster-management.io/v1beta1\nkind: OperatorPolicy\nspec: {}\n"), DocOptions{})
		if err == nil || err.Error() != "spec.subscription must be set in OperatorPolicies" {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("missing mock data is E2", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1beta1
kind: OperatorPolicy
spec:
  subscription: {name: '{{ fromConfigMap "ns-a" "nope" "k" }}'}
`), DocOptions{})

		var e2 *mockdata.ErrObjectMissing
		if !errors.As(err, &e2) {
			t.Fatalf("want E2, got %v", err)
		}
	})
}

func TestObjectTemplatesRaw(t *testing.T) {
	e := policyCohort(t)

	const raw = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates-raw: |
    {{- range (lookup "v1" "ConfigMap" "ns-a" "").items }}
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata: {name: '{{ .metadata.name }}-copy'}
    {{- end }}
`

	out, err := e.ResolveDocument([]byte(raw), DocOptions{})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition: {kind: ConfigMap, metadata: {name: one-copy}}
    - complianceType: musthave
      objectDefinition: {kind: ConfigMap, metadata: {name: two-copy}}
`)

	// Top-level object-templates-raw (no ConfigurationPolicy wrapper).
	out, err = e.ResolveDocument([]byte(`object-templates-raw: |
  - objectDefinition: {data: '{{ fromConfigMap "ns-a" "two" "k" }}'}
`), DocOptions{})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, "object-templates:\n  - objectDefinition: {data: v2}\n")

	// Raw template with strict failure.
	_, err = e.ResolveDocument([]byte("object-templates-raw: |\n  - {x: '{{ fromConfigMap \"ns-a\" \"nope\" \"k\" }}'}\n"),
		DocOptions{})

	var e2 *mockdata.ErrObjectMissing
	if !errors.As(err, &e2) {
		t.Fatalf("want E2, got %v", err)
	}

	// Empty and non-array results.
	out, err = e.ResolveDocument([]byte("object-templates-raw: |\n  {{ \"\" }}\n"), DocOptions{})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, "object-templates: []\n")

	_, err = e.ResolveDocument([]byte("object-templates-raw: |\n  a: b\n"), DocOptions{})
	if err == nil || err.Error() != "object-templates-raw was not an array after templates were resolved" {
		t.Fatalf("got %v", err)
	}
}

func TestSkipPolicyValidationRaw(t *testing.T) {
	e := policyCohort(t)

	const in = `apiVersion: v1
kind: ConfigMap
metadata: {name: '{{ .ObjectName }}', namespace: ns-a}
data:
  v: '{{ fromConfigMap "ns-a" "one" "k" }}'
`

	t.Run("without flag the kind is rejected", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(in), DocOptions{})
		if err == nil || !strings.HasPrefix(err.Error(), "invalid YAML. Supported types: Policy,") {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("with flag it is a raw template", func(t *testing.T) {
		out, err := e.ResolveDocument([]byte(in), DocOptions{SkipPolicyValidation: true, ObjectName: "raw"})
		if err != nil {
			t.Fatal(err)
		}

		sameYAML(t, out, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: raw, namespace: ns-a}\ndata: {v: v1}\n")
	})

	t.Run("strict failure is reported, not a cast error", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(strings.ReplaceAll(in, `"one"`, `"nope"`)),
			DocOptions{SkipPolicyValidation: true})

		var e2 *mockdata.ErrObjectMissing
		if !errors.As(err, &e2) {
			t.Fatalf("want E2, got %v", err)
		}
	})
}

func TestResolveDocumentInputErrors(t *testing.T) {
	e := policyCohort(t)

	if _, err := e.ResolveDocument([]byte("a: [unterminated"), DocOptions{}); err == nil ||
		!strings.HasPrefix(err.Error(), "failed to parse input to YAML:") {
		t.Fatalf("got %v", err)
	}

	if _, err := e.ResolveDocument([]byte("kind: ConfigMap\n"), DocOptions{HubMode: "bogus"}); err == nil ||
		!strings.Contains(err.Error(), `invalid hub mode "bogus"`) {
		t.Fatalf("got %v", err)
	}
}

// ResolveDocument must give identical results when repeated on one Engine
// (fresh options and a fresh Session per call; nothing is carried over).
func TestResolveDocumentRepeatable(t *testing.T) {
	e := policyCohort(t)
	in := []byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - objectDefinition: {a: '{{ skipObject }}'}
    - objectDefinition: {b: '{{ fromConfigMap "ns-a" "one" "k" }}'}
`)

	first, err := e.ResolveDocument(in, DocOptions{})
	if err != nil {
		t.Fatal(err)
	}

	second, err := e.ResolveDocument(in, DocOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Fatalf("not repeatable:\n%s\n---\n%s", first, second)
	}

	if !strings.Contains(string(first), "b: v1") || strings.Count(string(first), "objectDefinition") != 1 {
		t.Fatalf("unexpected output:\n%s", first)
	}
}

// T4 follow-up: a Kind that is always advertised but has zero files is
// supported-but-absent: E1 (namespaced) or E2 (cluster-scoped), never E4.
func TestAlwaysAdvertisedKindWithoutFiles(t *testing.T) {
	set := writeCohort(t, map[string]string{
		"objects/v1/Secret/ns-a/s.yaml": "apiVersion: v1\nkind: Secret\nmetadata: {name: s, namespace: ns-a}\nstringData: {k: v}\n",
	})
	e := newEngine(t, set, true)

	cases := []struct {
		name, expr, msg string
		as              func(error) bool
	}{
		{
			"fromConfigMap", `fromConfigMap "ns-a" "x" "k"`,
			`mock data: namespace directory missing: objects/v1/ConfigMap/ns-a/ (referenced by fromConfigMap "ns-a" "x" "k")`,
			func(err error) bool { var t *mockdata.ErrNamespaceDirMissing; return errors.As(err, &t) },
		},
		{
			"lookup ConfigMap", `(lookup "v1" "ConfigMap" "ns-a" "x").data`,
			`mock data: namespace directory missing: objects/v1/ConfigMap/ns-a/ (referenced by lookup "v1" "ConfigMap" "ns-a" "x")`,
			func(err error) bool { var t *mockdata.ErrNamespaceDirMissing; return errors.As(err, &t) },
		},
		{
			"lookup Node", `(lookup "v1" "Node" "" "w1").metadata`,
			`mock data: object not found: objects/v1/Node/w1.yaml (referenced by lookup "v1" "Node" "" "w1")`,
			func(err error) bool { var t *mockdata.ErrObjectMissing; return errors.As(err, &t) },
		},
		{
			"lookup ManagedCluster", `(lookup "cluster.open-cluster-management.io/v1" "ManagedCluster" "" "c1").metadata`,
			`mock data: object not found: objects/cluster.open-cluster-management.io/v1/ManagedCluster/c1.yaml ` +
				`(referenced by lookup "cluster.open-cluster-management.io/v1" "ManagedCluster" "" "c1")`,
			func(err error) bool { var t *mockdata.ErrObjectMissing; return errors.As(err, &t) },
		},
		{
			"lookup ClusterClaim", `(lookup "cluster.open-cluster-management.io/v1alpha1" "ClusterClaim" "" "id.k8s.io").spec`,
			`mock data: object not found: objects/cluster.open-cluster-management.io/v1alpha1/ClusterClaim/id.k8s.io.yaml ` +
				`(referenced by lookup "cluster.open-cluster-management.io/v1alpha1" "ClusterClaim" "" "id.k8s.io")`,
			func(err error) bool { var t *mockdata.ErrObjectMissing; return errors.As(err, &t) },
		},
		{
			"lookup Namespace", `(lookup "v1" "Namespace" "" "nope").metadata`,
			`mock data: object not found: objects/v1/Namespace/nope.yaml (referenced by lookup "v1" "Namespace" "" "nope")`,
			func(err error) bool { var t *mockdata.ErrObjectMissing; return errors.As(err, &t) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := render(e, tc.expr)
			if err == nil {
				t.Fatal("expected error")
			}

			if err.Error() != tc.msg {
				t.Errorf("message:\n got %s\nwant %s", err.Error(), tc.msg)
			}

			if !tc.as(err) {
				t.Errorf("errors.As failed for %T", err)
			}

			var e4 *mockdata.ErrLookupUnsupported
			if errors.As(err, &e4) {
				t.Errorf("must not be E4: %v", err)
			}
		})
	}

	// A synthesized Namespace (from the ns-a directory) is still found, and a Kind
	// that is not advertised stays E4.
	if got, err := render(e, `(lookup "v1" "Namespace" "" "ns-a").metadata.name`); err != nil || got != "ns-a" {
		t.Errorf("synthesized namespace: %q, %v", got, err)
	}

	_, err := render(e, `(lookup "example.com/v1" "Widget" "ns-a" "w").metadata`)

	var e4 *mockdata.ErrLookupUnsupported
	if !errors.As(err, &e4) {
		t.Errorf("unadvertised kind must stay E4, got %v", err)
	}
}
