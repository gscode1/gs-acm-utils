package resolve

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

const upstreamDir = "testdata/upstream/"

// upstreamOpts are the arguments upstream's client_test.go passes for *_hub cases.
func upstreamOpts() DocOptions {
	return DocOptions{
		ClusterName:     "local-cluster",
		HubNamespace:    "policies",
		ObjectNamespace: "my-obj-namespace",
		ObjectName:      "my-obj-name",
	}
}

func readFixture(t *testing.T, rel string) []byte {
	t.Helper()

	b, err := os.ReadFile(upstreamDir + rel)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// The upstream *_hub fixtures (cmd/template-resolver/testdata at v7.3.0): the
// cohort in testdata/upstream/cohort is the upstream setup/ resources, used as
// both the hub and the managed view (upstream resolves both against one cluster).
// Output must equal upstream's output.yaml byte for byte.
func TestUpstreamHubFixtures(t *testing.T) {
	e := newEngine(t, loadSet(t, upstreamDir+"cohort"), true)

	for _, name := range []string{
		"config-policy-directly_hub",
		"custom-sa_hub",
		"label-configmaps_hub",
		"label-match-configmaps_hub",
		"operator-policy_hub",
		"linting_valid_template_hub",
		"linting_var_used_hub",
		"linting_var_valid_raw_hub",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := e.ResolveDocument(readFixture(t, name+"/input.yaml"), upstreamOpts())
			if err != nil {
				t.Fatal(err)
			}

			if want := readFixture(t, name+"/output.yaml"); !bytes.Equal(got, want) {
				t.Fatalf("output differs from upstream\n--- got\n%s\n--- want\n%s", got, want)
			}
		})
	}
}

// The two lint fixtures whose upstream expectation is lint output plus a
// template error: the resolver half of the expectation is the template error.
func TestUpstreamHubFixtureErrors(t *testing.T) {
	e := newEngine(t, loadSet(t, upstreamDir+"cohort"), true)

	for name, want := range map[string]string{
		"linting_mismatched_delimiters_hub": `template: tmpl:19: unexpected "{" in operand`,
		"linting_var_unused_hub":            `template: tmpl:18: undefined variable "$managed_var"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := e.ResolveDocument(readFixture(t, name+"/input.yaml"), upstreamOpts())
			if err == nil {
				t.Fatal("expected an error")
			}

			m := regexp.MustCompile("template: tmpl:([0-9]+:){1,2} .*").FindString(err.Error())
			if m != want {
				t.Fatalf("got %q (from %q)\nwant %q", m, err.Error(), want)
			}
		})
	}
}

// hubCohort is a small cohort with hub data and an optional extra file set.
func hubCohort(t *testing.T, extra map[string]string) *Engine {
	t.Helper()

	files := map[string]string{
		"objects/v1/ConfigMap/ns-a/mc.yaml": `apiVersion: v1
kind: ConfigMap
metadata: {name: mc, namespace: ns-a}
data: {k: managed-value}
`,
		"hub/objects/v1/ConfigMap/policies/hc.yaml": `apiVersion: v1
kind: ConfigMap
metadata: {name: hc, namespace: policies}
data: {k: hub-value}
`,
		"hub/objects/v1/ConfigMap/other/oc.yaml": `apiVersion: v1
kind: ConfigMap
metadata: {name: oc, namespace: other}
data: {k: other-value}
`,
	}
	for k, v := range extra {
		files[k] = v
	}

	return newEngine(t, writeCohort(t, files), true)
}

const hubCP = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata:
          name: x
          labels:
            h: '{{hub fromConfigMap "policies" "hc" "k" hub}}'
            m: '{{ fromConfigMap "ns-a" "mc" "k" }}'
`

const hubCPResolved = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata:
          name: x
          labels: {h: hub-value, m: managed-value}
`

func TestHubResolveBothPasses(t *testing.T) {
	e := hubCohort(t, nil)

	out, err := e.ResolveDocument([]byte(hubCP), DocOptions{HubNamespace: "policies"})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, hubCPResolved)

	// HubMode "resolve" given explicitly is the same as the default.
	out, err = e.ResolveDocument([]byte(hubCP), DocOptions{HubMode: HubModeResolve, HubNamespace: "policies"})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, hubCPResolved)
}

func TestHubLookupNamespaceIsEnforced(t *testing.T) {
	e := hubCohort(t, nil)

	_, err := e.ResolveDocument([]byte(strings.Replace(hubCP, `"policies" "hc"`, `"other" "oc"`, 1)),
		DocOptions{HubNamespace: "policies"})
	if err == nil || !strings.Contains(err.Error(), "restricted") {
		t.Fatalf("want namespace restriction error, got %v", err)
	}
}

func TestHubMissingMockDataIsCatalogError(t *testing.T) {
	e := hubCohort(t, nil)

	_, err := e.ResolveDocument([]byte(strings.Replace(hubCP, `"hc"`, `"gone"`, 1)), DocOptions{HubNamespace: "policies"})

	var e2 *mockdata.ErrObjectMissing
	if !errors.As(err, &e2) {
		t.Fatalf("want E2, got %v", err)
	}

	want := `mock data: object not found: hub/objects/v1/ConfigMap/policies/gone.yaml ` +
		`(referenced by fromConfigMap "policies" "gone" "k")`
	if err.Error() != want {
		t.Fatalf("got %q\nwant %q", err.Error(), want)
	}
}

// E12: {{hub present but the cohort has no hub/objects.
func TestE12HubDataMissing(t *testing.T) {
	e := policyCohort(t)

	_, err := e.ResolveDocument([]byte(hubCP), DocOptions{HubNamespace: "policies"})

	var e12 *mockdata.ErrHubDataMissing
	if !errors.As(err, &e12) {
		t.Fatalf("want E12, got %v", err)
	}

	want := "mock data: input contains {{hub templates but cohort/hub/objects/ does not exist " +
		"(use --hub-mode=passthrough to skip)"
	if err.Error() != want {
		t.Fatalf("got %q\nwant %q", err.Error(), want)
	}

	// Without {{hub the missing hub tree is irrelevant.
	if _, err := e.ResolveDocument([]byte("object-templates-raw: |\n  - {a: b}\n"), DocOptions{}); err != nil {
		t.Fatal(err)
	}
}

// E13: a hub template survives the hub pass.
func TestE13UnresolvedHub(t *testing.T) {
	e := hubCohort(t, nil)

	const want = "unresolved hub template remains after hub pass"

	t.Run("hub output re-creates a hub template", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition: {x: '{{hub printf "%s%s" "{{" "hub y" hub}}'}
`), DocOptions{HubNamespace: "policies"})

		var e13 *mockdata.ErrUnresolvedHub
		if !errors.As(err, &e13) || err.Error() != want {
			t.Fatalf("want E13, got %v", err)
		}
	})

	t.Run("passthrough with unbalanced delimiters", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition: {x: '{{hub .ManagedClusterName }'}
`), DocOptions{HubMode: HubModePassthrough})

		var e13 *mockdata.ErrUnresolvedHub
		if !errors.As(err, &e13) || err.Error() != want {
			t.Fatalf("want E13, got %v", err)
		}
	})
}

func TestHubForbid(t *testing.T) {
	e := hubCohort(t, nil)

	_, err := e.ResolveDocument([]byte(hubCP), DocOptions{HubMode: HubModeForbid, HubNamespace: "policies"})

	var f *HubForbiddenError
	if !errors.As(err, &f) {
		t.Fatalf("want HubForbiddenError, got %v", err)
	}

	if want := "input contains {{hub templates but hub templates are forbidden by --hub-mode=forbid"; err.Error() != want {
		t.Fatalf("got %q\nwant %q", err.Error(), want)
	}

	// No hub templates: forbid is a no-op.
	if _, err := e.ResolveDocument([]byte("object-templates-raw: |\n  - {a: b}\n"),
		DocOptions{HubMode: HubModeForbid}); err != nil {
		t.Fatal(err)
	}
}

func TestHubPassthroughSnapshot(t *testing.T) {
	var warn bytes.Buffer

	set := writeCohort(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/mc.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: mc, namespace: ns-a}\ndata: {k: managed-value}\n",
	})

	e, err := NewEngine(set, Options{Strict: true, Cohort: "cohort", Warn: &warn})
	if err != nil {
		t.Fatal(err)
	}

	out, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata:
          name: x
          labels:
            a: '{{hub fromConfigMap "policies" "hc" "k" hub}}'
            b: 'pre-{{hub- .ManagedClusterName -hub}}-post'
            c: '{{hub .PolicyMetadata.name hub}}/{{hub .ManagedClusterName hub}}'
            d: '{{ fromConfigMap "ns-a" "mc" "k" }}'
          annotations:
            multi: |
              {{hub
                 lookup "v1" "ConfigMap" "ns" "n"
              hub}}
`), DocOptions{HubMode: HubModePassthrough})
	if err != nil {
		t.Fatal(err)
	}

	// Snapshot: hub templates become their trimmed expression text (quotes and
	// newlines survive JSON escaping); managed templates still resolve.
	sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
metadata: {name: cp}
spec:
  object-templates:
    - complianceType: musthave
      objectDefinition:
        kind: ConfigMap
        metadata:
          name: x
          labels:
            a: 'fromConfigMap "policies" "hc" "k"'
            b: 'pre-.ManagedClusterName-post'
            c: '.PolicyMetadata.name/.ManagedClusterName'
            d: managed-value
          annotations:
            multi: "lookup \"v1\" \"ConfigMap\" \"ns\" \"n\"\n"
`)

	if n := strings.Count(warn.String(), "warning: --hub-mode=passthrough"); n != 1 {
		t.Fatalf("want exactly one warning, got %d: %q", n, warn.String())
	}

	// No warning (and no hub processing) for an input without hub templates.
	warn.Reset()

	if _, err := e.ResolveDocument([]byte("object-templates-raw: |\n  - {a: b}\n"),
		DocOptions{HubMode: HubModePassthrough}); err != nil {
		t.Fatal(err)
	}

	if warn.Len() != 0 {
		t.Fatalf("unexpected warning %q", warn.String())
	}
}

// Namespace rules of the hub pass, with the same error texts as upstream.
func TestHubNamespaceRules(t *testing.T) {
	e := hubCohort(t, nil)

	const (
		policyHead = "apiVersion: policy.open-cluster-management.io/v1\nkind: Policy\nmetadata: {name: p%s}\n"
		policyTail = `spec:
  %s
  policy-templates:
    - objectDefinition:
        apiVersion: policy.open-cluster-management.io/v1
        kind: ConfigurationPolicy
        metadata: {name: cp}
        spec:
          object-templates:
            - objectDefinition: {l: '{{hub .PolicyMetadata.namespace hub}}'}
`
	)

	policy := func(ns, sa string) []byte {
		meta := ""
		if ns != "" {
			meta = ", namespace: " + ns
		}

		if sa == "" {
			sa = "disabled: false"
		}

		return []byte(strings.Replace(policyHead, "%s", meta, 1) + strings.Replace(policyTail, "%s", sa, 1))
	}

	t.Run("Policy: neither namespace", func(t *testing.T) {
		_, err := e.ResolveDocument(policy("", ""), DocOptions{})
		want := "a namespace must be specified for hub templates, either in the input Policy or as an argument " +
			"if spec.hubTemplateOptions.serviceAccountName is not specified"

		if err == nil || err.Error() != want {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("Policy: namespaces differ", func(t *testing.T) {
		_, err := e.ResolveDocument(policy("policies", ""), DocOptions{HubNamespace: "other"})
		want := "the namespace specified in the Policy and the hub-namespace argument must match"

		if err == nil || err.Error() != want {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("Policy: namespace comes from the Policy", func(t *testing.T) {
		out, err := e.ResolveDocument(policy("policies", ""), DocOptions{})
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(out), "l: policies") {
			t.Fatalf("unexpected output:\n%s", out)
		}
	})

	t.Run("Policy: namespace comes from the argument", func(t *testing.T) {
		if _, err := e.ResolveDocument(policy("", ""), DocOptions{HubNamespace: "policies"}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("non-Policy kinds always need the argument", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(hubCP), DocOptions{})
		want := "a hub namespace must be provided (--hub-namespace) when resolving hub templates " +
			"and spec.hubTemplateOptions.serviceAccountName is not specified"

		if err == nil || err.Error() != want {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("custom service account needs no namespace and is unrestricted", func(t *testing.T) {
		in := policy("", "hubTemplateOptions: {serviceAccountName: sa}")
		in = []byte(strings.Replace(string(in), "{{hub .PolicyMetadata.namespace hub}}",
			`{{hub (lookup "v1" "ConfigMap" "other" "oc").data.k hub}}`, 1))

		out, err := e.ResolveDocument(in, DocOptions{})
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(out), "l: other-value") {
			t.Fatalf("unexpected output:\n%s", out)
		}
	})
}

func TestHubContextAndParams(t *testing.T) {
	e := hubCohort(t, map[string]string{
		"hub/context.yaml": "clusterName: edge-1\nclusterLabels: {env: dev, tier: \"3\"}\n",
		"params.yaml":      "Region: eu\nObjectName: from-params\n",
	})

	const in = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition:
        name: '{{hub .ManagedClusterName hub}}'
        env: '{{hub .ManagedClusterLabels.env hub}}'
        tier: '{{hub .ManagedClusterLabels.tier hub}}'
        hubRegion: '{{hub .Region hub}}'
        managedRegion: '{{ .Region }}'
        obj: '{{ .ObjectName }}'
        objns: '{{ .ObjectNamespace }}'
`

	t.Run("hub/context.yaml and params.yaml defaults", func(t *testing.T) {
		out, err := e.ResolveDocument([]byte(in), DocOptions{HubNamespace: "policies"})
		if err != nil {
			t.Fatal(err)
		}

		sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition: {name: edge-1, env: dev, tier: "3", hubRegion: eu, managedRegion: eu, obj: from-params, objns: ""}
`)
	})

	t.Run("flags and DocOptions.Params win", func(t *testing.T) {
		out, err := e.ResolveDocument([]byte(in), DocOptions{
			HubNamespace:    "policies",
			ClusterName:     "flag-cluster",
			ObjectName:      "flag-obj",
			ObjectNamespace: "flag-ns",
			Params:          map[string]any{"Region": "us", "ManagedClusterName": "ignored"},
		})
		if err != nil {
			t.Fatal(err)
		}

		sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition: {name: flag-cluster, env: dev, tier: "3", hubRegion: us, managedRegion: us, obj: flag-obj, objns: flag-ns}
`)
	})

	t.Run("params keys must be exported identifiers", func(t *testing.T) {
		_, err := e.ResolveDocument([]byte(in), DocOptions{HubNamespace: "policies", Params: map[string]any{"region": "x"}})
		if err == nil || !strings.Contains(err.Error(), `invalid template context key "region"`) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("params are not shared between documents", func(t *testing.T) {
		const mutate = `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition: {a: '{{ $_ := set .Nested "k" "mutated" }}{{ .Nested.k }}'}
`
		params := map[string]any{"Nested": map[string]any{"k": "orig"}}

		if _, err := e.ResolveDocument([]byte(mutate), DocOptions{Params: params}); err != nil {
			t.Fatal(err)
		}

		if params["Nested"].(map[string]any)["k"] != "orig" {
			t.Fatal("caller's params were mutated")
		}
	})
}

// ManagedClusterLabels come from the ManagedCluster object in hub/objects when it exists.
func TestHubManagedClusterLabelsFromObject(t *testing.T) {
	e := hubCohort(t, map[string]string{
		"hub/objects/cluster.open-cluster-management.io/v1/ManagedCluster/c1.yaml": `apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata: {name: c1, labels: {env: prod, region: eu}}
`,
		"hub/context.yaml": "clusterName: c1\nclusterLabels: {env: ignored}\n",
	})

	out, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition:
        env: '{{hub .ManagedClusterLabels.env hub}}'
        region: '{{hub .ManagedClusterLabels.region hub}}'
        self: '{{hub (lookup "cluster.open-cluster-management.io/v1" "ManagedCluster" "" .ManagedClusterName).metadata.name hub}}'
`), DocOptions{HubNamespace: "policies"})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition: {env: prod, region: eu, self: c1}
`)

	// Only the allow-listed ManagedCluster is readable from the hub pass.
	_, err = e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition:
        x: '{{hub (lookup "cluster.open-cluster-management.io/v1" "ManagedCluster" "" "c2").metadata.name hub}}'
`), DocOptions{HubNamespace: "policies"})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("want cluster-scoped lookup restriction, got %v", err)
	}
}

// With neither a ManagedCluster object nor hub/context.yaml the labels are an
// empty map (documented: a missing ManagedCluster is not an error).
func TestHubManagedClusterLabelsDefaultEmpty(t *testing.T) {
	e := hubCohort(t, nil)

	out, err := e.ResolveDocument([]byte(`apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition:
        name: '{{hub .ManagedClusterName hub}}'
        n: '{{hub len .ManagedClusterLabels hub}}'
`), DocOptions{HubNamespace: "policies", ClusterName: "ghost"})
	if err != nil {
		t.Fatal(err)
	}

	sameYAML(t, out, `apiVersion: policy.open-cluster-management.io/v1
kind: ConfigurationPolicy
spec:
  object-templates:
    - objectDefinition: {name: ghost, n: "0"}
`)
}
