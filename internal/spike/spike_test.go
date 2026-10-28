// Package spike holds the T0 feasibility tests proving that go-template-utils
// can run entirely against fake dynamic + discovery clients.
package spike

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/stolostron/go-template-utils/v7/pkg/templates"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	discfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"
)

// kindSpec describes one API resource that the fake discovery must advertise.
type kindSpec struct {
	groupVersion string
	kind         string
	plural       string
	namespaced   bool
}

var kinds = []kindSpec{
	{"v1", "ConfigMap", "configmaps", true},
	{"v1", "Secret", "secrets", true},
	{"v1", "Namespace", "namespaces", false},
	{"v1", "Node", "nodes", false},
	{"cluster.open-cluster-management.io/v1", "ManagedCluster", "managedclusters", false},
	{"cluster.open-cluster-management.io/v1alpha1", "ClusterClaim", "clusterclaims", false},
}

// newFakes is THE recipe: a static FakeDiscovery plus a dynamic fake seeded with
// objects, with list kinds registered for every advertised resource.
func newFakes(objs ...*unstructured.Unstructured) (dynamic.Interface, discovery.DiscoveryInterface) {
	byGV := map[string]*metav1.APIResourceList{}
	listKinds := map[schema.GroupVersionResource]string{}

	var order []string

	for _, k := range kinds {
		gv, _ := schema.ParseGroupVersion(k.groupVersion)

		list, ok := byGV[k.groupVersion]
		if !ok {
			list = &metav1.APIResourceList{GroupVersion: k.groupVersion}
			byGV[k.groupVersion] = list
			order = append(order, k.groupVersion)
		}

		list.APIResources = append(list.APIResources, metav1.APIResource{
			Name:       k.plural,
			Kind:       k.kind,
			Namespaced: k.namespaced,
			Verbs:      metav1.Verbs{"get", "list", "watch"},
		})
		listKinds[gv.WithResource(k.plural)] = k.kind + "List"
	}

	disco := &discfake.FakeDiscovery{Fake: &clienttesting.Fake{}}
	for _, gv := range order {
		disco.Resources = append(disco.Resources, byGV[gv])
	}

	runtimeObjs := make([]runtime.Object, 0, len(objs))
	for _, o := range objs {
		runtimeObjs = append(runtimeObjs, o)
	}

	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, runtimeObjs...)

	return dyn, disco
}

func parseObjects(t *testing.T, data []byte) []*unstructured.Unstructured {
	t.Helper()

	dec := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)

	var out []*unstructured.Unstructured

	for {
		obj := &unstructured.Unstructured{}

		err := dec.Decode(obj)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		if len(obj.Object) == 0 {
			continue
		}

		out = append(out, obj)
	}

	return out
}

func obj(yamlStr string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(yamlStr), &u.Object); err != nil {
		panic(err)
	}

	return u
}

func seed() []*unstructured.Unstructured {
	return []*unstructured.Unstructured{
		obj(`{apiVersion: v1, kind: ConfigMap, metadata: {name: cm1, namespace: ns1, labels: {color: yellow}}, data: {key: val1, other: o1}}`),
		obj(`{apiVersion: v1, kind: ConfigMap, metadata: {name: cm2, namespace: ns1, labels: {color: blue}}, data: {key: val2}}`),
		// Secret data is stored base64-encoded, as on a real cluster.
		obj(`{apiVersion: v1, kind: Secret, metadata: {name: s1, namespace: ns1}, data: {pw: c2VjcmV0MTIz}}`),
		obj(`{apiVersion: cluster.open-cluster-management.io/v1, kind: ManagedCluster, metadata: {name: local-cluster, labels: {env: prod}}}`),
		obj(`{apiVersion: cluster.open-cluster-management.io/v1alpha1, kind: ClusterClaim, metadata: {name: id.k8s.io}, spec: {value: abc-123}}`),
	}
}

func resolveJSON(
	t *testing.T, r *templates.TemplateResolver, in string, ctx any, opts *templates.ResolveOptions,
) (map[string]any, error) {
	t.Helper()

	res, err := r.ResolveTemplate([]byte(in), ctx, opts)
	if err != nil {
		return nil, err
	}

	var out map[string]any
	if err := json.Unmarshal(res.ResolvedJSON, &out); err != nil {
		t.Fatalf("unmarshal resolved: %v", err)
	}

	return out, nil
}

func TestManagedFunctions(t *testing.T) {
	dyn, disco := newFakes(seed()...)

	r, err := templates.NewResolverWithClients(dyn, disco, templates.Config{})
	if err != nil {
		t.Fatal(err)
	}

	in := `{
  "cm":      "{{ fromConfigMap \"ns1\" \"cm1\" \"key\" }}",
  "secret":  "{{ fromSecret \"ns1\" \"s1\" \"pw\" | base64dec }}",
  "secretRaw": "{{ fromSecret \"ns1\" \"s1\" \"pw\" }}",
  "get":     "{{ (lookup \"v1\" \"ConfigMap\" \"ns1\" \"cm2\").data.key }}",
  "listLen": "{{ len (lookup \"v1\" \"ConfigMap\" \"ns1\" \"\").items }}",
  "label":   "{{ range (lookup \"v1\" \"ConfigMap\" \"ns1\" \"\" \"color=yellow\").items }}{{ .metadata.name }}{{ end }}",
  "copy":    "{{ copyConfigMapData \"ns1\" \"cm2\" }}",
  "claim":   "{{ fromClusterClaim \"id.k8s.io\" }}",
  "mc":      "{{ (lookup \"cluster.open-cluster-management.io/v1\" \"ManagedCluster\" \"\" \"local-cluster\").metadata.name }}",
  "missing": "{{ (lookup \"v1\" \"ConfigMap\" \"ns1\" \"nope\").data }}",
  "missingKey": "{{ fromConfigMap \"ns1\" \"cm1\" \"nokey\" }}"
}`

	got, err := resolveJSON(t, r, in, templates.TemplateContext{}, &templates.ResolveOptions{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	want := map[string]any{
		"cm":         "val1",
		"secret":     "secret123",
		"secretRaw":  "c2VjcmV0MTIz",
		"get":        "val2",
		"listLen":    "2",
		"label":      "cm1",
		"copy":       map[string]any{"key": "val2"}, // JSON input: parsed back as a map
		"claim":      "abc-123",
		"mc":         "local-cluster",
		"missing":    "<no value>", // library swallows NotFound in lookup (empty map)
		"missingKey": "",           // library returns "" for a missing key
	}

	for k, w := range want {
		if !equalJSON(got[k], w) {
			t.Errorf("%s: got %#v, want %#v", k, got[k], w)
		}
	}

	// used-resource tracking also works against the fakes
	if n := len(r.GetUsedResources()); n == 0 {
		t.Error("expected used resources to be recorded")
	}
}

func TestUnknownGVK(t *testing.T) {
	dyn, disco := newFakes(seed()...)

	r, err := templates.NewResolverWithClients(dyn, disco, templates.Config{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = resolveJSON(t, r,
		`{"x": "{{ (lookup \"example.com/v1\" \"Widget\" \"ns1\" \"w\").metadata.name }}"}`,
		templates.TemplateContext{}, &templates.ResolveOptions{})
	if !errors.Is(err, templates.ErrMissingAPIResource) {
		t.Fatalf("want ErrMissingAPIResource, got %v", err)
	}
}

func TestCustomFunctionOverridesBuiltin(t *testing.T) {
	dyn, disco := newFakes(seed()...)

	r, err := templates.NewResolverWithClients(dyn, disco, templates.Config{})
	if err != nil {
		t.Fatal(err)
	}

	opts := &templates.ResolveOptions{CustomFunctions: template.FuncMap{
		"fromConfigMap": func(ns, name, key string) (string, error) { return "custom:" + ns + "/" + name + "/" + key, nil },
	}}

	got, err := resolveJSON(t, r, `{"x": "{{ fromConfigMap \"ns1\" \"cm1\" \"key\" }}"}`, templates.TemplateContext{}, opts)
	if err != nil {
		t.Fatal(err)
	}

	if got["x"] != "custom:ns1/cm1/key" {
		t.Fatalf("override not applied: %v", got["x"])
	}

	// A custom function can also return a typed error (strict-mode building block).
	errBoom := errors.New("boom")
	opts2 := &templates.ResolveOptions{CustomFunctions: template.FuncMap{
		"fromConfigMap": func(_, _, _ string) (string, error) { return "", errBoom },
	}}

	if _, err = resolveJSON(t, r, `{"x": "{{ fromConfigMap \"ns1\" \"cm1\" \"key\" }}"}`,
		templates.TemplateContext{}, opts2); !errors.Is(err, errBoom) {
		t.Fatalf("want wrapped errBoom, got %v", err)
	}
}

func TestLookupNamespaceRestriction(t *testing.T) {
	dyn, disco := newFakes(seed()...)

	r, err := templates.NewResolverWithClients(dyn, disco, templates.Config{})
	if err != nil {
		t.Fatal(err)
	}

	// Cluster-scoped lookup is denied under LookupNamespace unless allow-listed; this needs
	// discovery to report Namespaced correctly.
	in := `{"x": "{{ (lookup \"cluster.open-cluster-management.io/v1\" \"ManagedCluster\" \"\" \"local-cluster\").metadata.name }}"}`

	_, err = resolveJSON(t, r, in, templates.TemplateContext{}, &templates.ResolveOptions{LookupNamespace: "ns1"})
	if err == nil {
		t.Fatal("expected cluster-scoped lookup to be denied")
	}

	t.Logf("denied as expected: %v", err)

	got, err := resolveJSON(t, r, in, templates.TemplateContext{}, &templates.ResolveOptions{
		LookupNamespace: "ns1",
		ClusterScopedAllowList: []templates.ClusterScopedObjectIdentifier{
			{Group: "cluster.open-cluster-management.io", Kind: "ManagedCluster", Name: "local-cluster"},
		},
	})
	if err != nil || got["x"] != "local-cluster" {
		t.Fatalf("allow-listed lookup failed: %v %v", got, err)
	}
}

type hubCtx struct {
	ManagedClusterName   string
	ManagedClusterLabels map[string]string
	PolicyMetadata       map[string]any
}

func readFile(t *testing.T, p ...string) []byte {
	t.Helper()

	b, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, p...)...))
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func hubResolve(t *testing.T, policy map[string]any, hubObjs []*unstructured.Unstructured, ns string) map[string]any {
	t.Helper()

	dyn, disco := newFakes(hubObjs...)

	hubResolver, err := templates.NewResolverWithClients(dyn, disco, templates.Config{
		AdditionalIndentation: 8,
		DisabledFunctions:     []string{},
		StartDelim:            "{{hub",
		StopDelim:             "hub}}",
	})
	if err != nil {
		t.Fatal(err)
	}

	u := unstructured.Unstructured{Object: policy}

	ctx := hubCtx{
		ManagedClusterName:   "local-cluster",
		ManagedClusterLabels: map[string]string{"env": "prod"},
	}
	if u.GetKind() == "Policy" {
		ctx.PolicyMetadata = map[string]any{
			"annotations": u.GetAnnotations(),
			"labels":      u.GetLabels(),
			"name":        u.GetName(),
			"namespace":   u.GetNamespace(),
		}
	}

	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}

	res, err := hubResolver.ResolveTemplate(raw, ctx, &templates.ResolveOptions{
		LookupNamespace: ns,
		ClusterScopedAllowList: []templates.ClusterScopedObjectIdentifier{
			{Group: "cluster.open-cluster-management.io", Kind: "ManagedCluster", Name: "local-cluster"},
		},
	})
	if err != nil {
		t.Fatalf("hub pass: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(res.ResolvedJSON, &out); err != nil {
		t.Fatal(err)
	}

	return out
}

func normalize(t *testing.T, b []byte) any {
	t.Helper()

	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}

	return v
}

func toYAMLValue(t *testing.T, v any) any {
	t.Helper()

	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return normalize(t, b)
}

func equalJSON(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)

	return bytes.Equal(aj, bj)
}

// Fixture test_config-policy-directly_hub: hub pass only (ConfigurationPolicy).
func TestHubFixtureDirect(t *testing.T) {
	var policy map[string]any
	if err := yaml.Unmarshal(readFile(t, "input.yaml"), &policy); err != nil {
		t.Fatal(err)
	}

	hubObjs := parseObjects(t, readFile(t, "save_hub_resources.yaml"))
	hubObjs = append(hubObjs, seed()[3]) // ManagedCluster local-cluster

	// ConfigurationPolicy (non-Policy) requires an explicit hub namespace; the fixture uses
	// the hub-namespace "policies" as that is where the ConfigMap lives.
	got := hubResolve(t, policy, hubObjs, "policies")

	// No managed templates remain, so the second pass is the identity here; the
	// upstream CLI output removes nothing else.
	want := normalize(t, readFile(t, "output.yaml"))
	if !equalJSON(toYAMLValue(t, got), want) {
		g, _ := yaml.Marshal(got)
		t.Fatalf("mismatch:\n%s", g)
	}
}

// Fixture test_label-configmaps_hub: hub pass, then managed pass on object-templates-raw.
func TestHubThenManagedFixture(t *testing.T) {
	var policy map[string]any
	if err := yaml.Unmarshal(readFile(t, "test_label-configmaps_hub", "input.yaml"), &policy); err != nil {
		t.Fatal(err)
	}

	hubObjs := []*unstructured.Unstructured{seed()[3]}
	policy = hubResolve(t, policy, hubObjs, "policies")

	// Managed pass
	managedObjs := parseObjects(t, readFile(t, "test_label-configmaps_hub", "save_resources.yaml"))

	dyn, disco := newFakes(managedObjs...)

	managed, err := templates.NewResolverWithClients(dyn, disco, templates.Config{})
	if err != nil {
		t.Fatal(err)
	}

	pt, _, _ := unstructured.NestedSlice(policy, "spec", "policy-templates")
	od := pt[0].(map[string]any)["objectDefinition"].(map[string]any)
	spec := od["spec"].(map[string]any)
	raw := spec["object-templates-raw"].(string)

	res, err := managed.ResolveTemplate([]byte(raw), templates.TemplateContext{},
		&templates.ResolveOptions{InputIsYAML: true})
	if err != nil {
		t.Fatalf("managed pass: %v", err)
	}

	var items []any
	if err := json.Unmarshal(res.ResolvedJSON, &items); err != nil {
		t.Fatal(err)
	}

	delete(spec, "object-templates-raw")
	spec["object-templates"] = items

	if err := unstructured.SetNestedSlice(policy, pt, "spec", "policy-templates"); err != nil {
		t.Fatal(err)
	}

	want := normalize(t, readFile(t, "test_label-configmaps_hub", "output.yaml"))
	if !equalJSON(toYAMLValue(t, policy), want) {
		g, _ := yaml.Marshal(policy)
		t.Fatalf("mismatch:\n%s", g)
	}
}

// Concurrent resolvers over one shared set of fakes must be race-free.
func TestSharedFakesConcurrent(t *testing.T) {
	dyn, disco := newFakes(seed()...)

	done := make(chan error, 8)

	for range 8 {
		go func() {
			r, err := templates.NewResolverWithClients(dyn, disco, templates.Config{})
			if err != nil {
				done <- err

				return
			}

			_, err = r.ResolveTemplate([]byte(`{"x": "{{ fromConfigMap \"ns1\" \"cm1\" \"key\" }}"}`),
				templates.TemplateContext{}, &templates.ResolveOptions{})
			done <- err
		}()
	}

	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
