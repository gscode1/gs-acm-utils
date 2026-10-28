package fakecluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

const basicCohort = "../../testdata/cohorts/basic"

var (
	gvrCM      = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	gvrSecret  = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	gvrNS      = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	gvrNode    = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}
	gvrMC      = schema.GroupVersionResource{Group: ocmGroup, Version: "v1", Resource: "managedclusters"}
	gvrClaim   = schema.GroupVersionResource{Group: ocmGroup, Version: "v1alpha1", Resource: "clusterclaims"}
	gvrWidgets = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}
)

func loadIndex(t *testing.T, dir string) *mockdata.Index {
	t.Helper()

	set, err := mockdata.Load(dir)
	if err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}

	return set.Managed
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()

	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// customCohort has a namespaced and a cluster-scoped custom Kind plus an explicit Namespace file.
func customCohort(t *testing.T) *mockdata.Index {
	t.Helper()

	root := t.TempDir()
	write(t, root, "objects/example.com/v1/Widget/ns-w/w1.yaml",
		"apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w1\n  namespace: ns-w\n  labels: {tier: gold}\n")
	write(t, root, "objects/example.com/v1/Widget/ns-w/w2.yaml",
		"apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w2\n  namespace: ns-w\n  labels: {tier: tin}\n")
	write(t, root, "objects/example.com/v1/Gadget/g1.yaml",
		"apiVersion: example.com/v1\nkind: Gadget\nmetadata:\n  name: g1\n")
	write(t, root, "objects/v1/Namespace/ns-w.yaml",
		"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: ns-w\n  labels: {explicit: \"yes\"}\n")

	return loadIndex(t, root)
}

func TestGetListPerKind(t *testing.T) {
	dyn, _, err := New(loadIndex(t, basicCohort))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	cm, err := dyn.Resource(gvrCM).Namespace("ns-a").Get(ctx, "my-config", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if v, _, _ := nested(cm.Object, "data", "color"); v != "blue" {
		t.Errorf("configmap data.color = %q", v)
	}

	if _, err = dyn.Resource(gvrSecret).Namespace("ns-a").Get(ctx, "db-creds", metav1.GetOptions{}); err != nil {
		t.Errorf("secret get: %v", err)
	}

	if _, err = dyn.Resource(gvrClaim).Get(ctx, "id.k8s.io", metav1.GetOptions{}); err != nil {
		t.Errorf("clusterclaim get: %v", err)
	}

	// Missing objects of an advertised kind are NotFound.
	if _, err = dyn.Resource(gvrCM).Namespace("ns-a").Get(ctx, "nope", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("want NotFound, got %v", err)
	}

	// List across all namespaces, per namespace, and by label selector.
	all, err := dyn.Resource(gvrCM).List(ctx, metav1.ListOptions{})
	if err != nil || len(all.Items) != 2 {
		t.Fatalf("list all configmaps: %v (%d)", err, len(all.Items))
	}

	inA, err := dyn.Resource(gvrCM).Namespace("ns-a").List(ctx, metav1.ListOptions{})
	if err != nil || len(inA.Items) != 1 {
		t.Fatalf("list ns-a: %v (%d)", err, len(inA.Items))
	}

	yellow, err := dyn.Resource(gvrCM).List(ctx, metav1.ListOptions{LabelSelector: "color=yellow"})
	if err != nil || len(yellow.Items) != 1 || yellow.Items[0].GetName() != "other-config" {
		t.Fatalf("label selector: %v %v", err, yellow.Items)
	}

	// Kinds with zero objects still list (empty), never panic.
	for _, gvr := range []schema.GroupVersionResource{gvrNode, gvrMC} {
		l, err := dyn.Resource(gvr).List(ctx, metav1.ListOptions{})
		if err != nil || len(l.Items) != 0 {
			t.Errorf("empty list %v: %v (%d)", gvr, err, len(l.Items))
		}
	}
}

func TestCustomKinds(t *testing.T) {
	dyn, disco, err := New(customCohort(t))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	l, err := dyn.Resource(gvrWidgets).Namespace("ns-w").List(ctx, metav1.ListOptions{LabelSelector: "tier=gold"})
	if err != nil || len(l.Items) != 1 || l.Items[0].GetName() != "w1" {
		t.Fatalf("widget selector: %v %v", err, l.Items)
	}

	g, err := dyn.Resource(schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "gadgets"}).
		Get(ctx, "g1", metav1.GetOptions{})
	if err != nil || g.GetName() != "g1" {
		t.Fatalf("gadget get: %v", err)
	}

	rl, err := disco.ServerResourcesForGroupVersion("example.com/v1")
	if err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, r := range rl.APIResources {
		got[r.Kind+"/"+r.Name] = r.Namespaced
	}

	want := map[string]bool{"Widget/widgets": true, "Gadget/gadgets": false}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("example.com/v1 resources = %v, want %v", got, want)
	}
}

func TestNamespaceSynthesis(t *testing.T) {
	ctx := context.Background()

	// basic: no Namespace files, so ns-a and ns-b are synthesized.
	dyn, _, err := New(loadIndex(t, basicCohort))
	if err != nil {
		t.Fatal(err)
	}

	l, err := dyn.Resource(gvrNS).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, i := range l.Items {
		names = append(names, i.GetName())
	}

	sort.Strings(names)

	if fmt.Sprint(names) != "[ns-a ns-b]" {
		t.Errorf("namespaces = %v", names)
	}

	// custom: an explicit Namespace file wins (keeps its labels, no duplicate).
	dyn, _, err = New(customCohort(t))
	if err != nil {
		t.Fatal(err)
	}

	ns, err := dyn.Resource(gvrNS).Get(ctx, "ns-w", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if ns.GetLabels()["explicit"] != "yes" {
		t.Errorf("explicit namespace was replaced: %v", ns.GetLabels())
	}

	l, _ = dyn.Resource(gvrNS).List(ctx, metav1.ListOptions{})
	if len(l.Items) != 1 {
		t.Errorf("want exactly 1 namespace, got %d", len(l.Items))
	}
}

// resourceSummary renders a GroupVersion's advertised resources as "Kind/plural/scope" strings.
func resourceSummary(t *testing.T, ix *mockdata.Index) map[string][]string {
	t.Helper()

	_, disco, err := New(ix)
	if err != nil {
		t.Fatal(err)
	}

	_, lists, err := disco.ServerGroupsAndResources()
	if err != nil {
		t.Fatal(err)
	}

	out := map[string][]string{}

	for _, l := range lists {
		for _, r := range l.APIResources {
			scope := "cluster"
			if r.Namespaced {
				scope = "namespaced"
			}

			out[l.GroupVersion] = append(out[l.GroupVersion], r.Kind+"/"+r.Name+"/"+scope)
		}

		sort.Strings(out[l.GroupVersion])
	}

	return out
}

func TestDiscoveryContentsExact(t *testing.T) {
	want := map[string][]string{
		"v1": {
			"ConfigMap/configmaps/namespaced", "Namespace/namespaces/cluster",
			"Node/nodes/cluster", "Secret/secrets/namespaced",
		},
		"cluster.open-cluster-management.io/v1":       {"ManagedCluster/managedclusters/cluster"},
		"cluster.open-cluster-management.io/v1alpha1": {"ClusterClaim/clusterclaims/cluster"},
	}

	check := func(name string, got, want map[string][]string) {
		t.Helper()

		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s discovery:\n got %v\nwant %v", name, got, want)
		}
	}

	check("basic", resourceSummary(t, loadIndex(t, basicCohort)), want)

	// Zero objects of the always-advertised kinds: the same set. Use a cohort of only a custom kind.
	root := t.TempDir()
	write(t, root, "objects/example.com/v1/Widget/ns-w/w1.yaml",
		"apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: w1\n  namespace: ns-w\n")

	want["example.com/v1"] = []string{"Widget/widgets/namespaced"}
	check("custom-only", resourceSummary(t, loadIndex(t, root)), want)

	// Every indexed kind is advertised with the index's scope.
	ix := customCohort(t)
	got := resourceSummary(t, ix)

	for _, k := range ix.Kinds() {
		scope := "cluster"
		if k.Namespaced {
			scope = "namespaced"
		}

		entry := k.GVK.Kind + "/" + k.Plural + "/" + scope

		found := false
		for _, e := range got[k.GVK.GroupVersion().String()] {
			found = found || e == entry
		}

		if !found {
			t.Errorf("indexed kind %s not advertised (got %v)", entry, got)
		}
	}

	// An absent GroupVersion is "not installed".
	_, disco, _ := New(ix)
	if _, err := disco.ServerResourcesForGroupVersion("nope.io/v1"); err == nil {
		t.Error("expected error for unadvertised group version")
	}
}

func TestNilIndex(t *testing.T) {
	if _, _, err := New(nil); err == nil {
		t.Fatal("expected error for nil index")
	}
}

func TestConcurrentUse(t *testing.T) {
	dyn, disco, err := New(loadIndex(t, basicCohort))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	errs := make(chan error, 16)

	for range 16 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			ctx := context.Background()

			for range 50 {
				if _, err := dyn.Resource(gvrCM).Namespace("ns-a").Get(ctx, "my-config", metav1.GetOptions{}); err != nil {
					errs <- err

					return
				}

				if _, err := dyn.Resource(gvrCM).List(ctx, metav1.ListOptions{LabelSelector: "color=blue"}); err != nil {
					errs <- err

					return
				}

				if _, err := disco.ServerResourcesForGroupVersion("v1"); err != nil {
					errs <- err

					return
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func nested(m map[string]any, path ...string) (string, bool, error) {
	var cur any = m

	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return "", false, nil
		}

		cur = mm[p]
	}

	s, ok := cur.(string)

	return s, ok, nil
}
