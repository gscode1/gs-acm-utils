package mockdata

import (
	"errors"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func basicSet(t *testing.T) *Set {
	t.Helper()

	return mustLoad(t, "../../testdata/cohorts/basic")
}

func TestIndexGetErrors(t *testing.T) {
	ix := basicSet(t).Managed

	tests := []struct {
		name                             string
		av, kind, ns, objName, ref, want string
		as                               func(error) bool
	}{
		{
			"E1 namespace dir missing", "v1", "ConfigMap", "ns-zzz", "my-config",
			`fromConfigMap "ns-zzz" "my-config" "key"`,
			`mock data: namespace directory missing: objects/v1/ConfigMap/ns-zzz/ (referenced by fromConfigMap "ns-zzz" "my-config" "key")`,
			func(e error) bool { var t *ErrNamespaceDirMissing; return errors.As(e, &t) },
		},
		{
			"E2 object missing", "v1", "ConfigMap", "ns-a", "nope",
			`fromConfigMap "ns-a" "nope" "key"`,
			`mock data: object not found: objects/v1/ConfigMap/ns-a/nope.yaml (referenced by fromConfigMap "ns-a" "nope" "key")`,
			func(e error) bool { var t *ErrObjectMissing; return errors.As(e, &t) },
		},
		{
			"E2 cluster-scoped object missing", "cluster.open-cluster-management.io/v1alpha1", "ClusterClaim", "", "nope", "",
			"mock data: object not found: objects/cluster.open-cluster-management.io/v1alpha1/ClusterClaim/nope.yaml",
			func(e error) bool { var t *ErrObjectMissing; return errors.As(e, &t) },
		},
		{
			"E4 unknown kind", "v1", "Pod", "ns-a", "p", "",
			"mock data: lookup not supported for apiVersion=v1, Kind=Pod (no files under objects/v1/Pod/)",
			func(e error) bool { var t *ErrLookupUnsupported; return errors.As(e, &t) },
		},
		{
			"E4 unknown apiVersion", "apps/v1", "Deployment", "ns-a", "d", "",
			"mock data: lookup not supported for apiVersion=apps/v1, Kind=Deployment (no files under objects/apps/v1/Deployment/)",
			func(e error) bool { var t *ErrLookupUnsupported; return errors.As(e, &t) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obj, err := ix.Get(tc.av, tc.kind, tc.ns, tc.objName)
			if err == nil || obj != nil {
				t.Fatalf("want error, got obj=%v err=%v", obj, err)
			}

			if tc.ref != "" {
				err = WithReference(err, tc.ref)
			}

			if !tc.as(err) {
				t.Errorf("errors.As failed for %T", err)
			}

			if err.Error() != tc.want {
				t.Errorf("got:  %s\nwant: %s", err, tc.want)
			}
		})
	}

	t.Run("hub index uses hub prefix", func(t *testing.T) {
		_, err := basicSet(t).Hub.Get("v1", "ConfigMap", "nope", "x")
		if want := "mock data: namespace directory missing: hub/objects/v1/ConfigMap/nope/"; err.Error() != want {
			t.Errorf("got %q want %q", err, want)
		}
	})
}

func TestE3KeyMissingHelper(t *testing.T) {
	ix := basicSet(t).Managed

	obj, err := ix.Get("v1", "ConfigMap", "ns-a", "my-config")
	if err != nil {
		t.Fatal(err)
	}

	if err := ix.RequireKey(obj, "data", "color"); err != nil {
		t.Errorf("present key: %v", err)
	}

	err = ix.RequireKey(obj, "data", "key")

	var e *ErrKeyMissing
	if !errors.As(err, &e) {
		t.Fatalf("want *ErrKeyMissing, got %v", err)
	}

	want := `mock data: key "key" not found in objects/v1/ConfigMap/ns-a/my-config.yaml (data has: color, size)`
	if err.Error() != want {
		t.Errorf("got  %s\nwant %s", err, want)
	}

	if got := NewKeyMissing("p.yaml", "data", "k", nil).Error(); got != `mock data: key "k" not found in p.yaml (data has: (none))` {
		t.Errorf("empty: %s", got)
	}

	// available keys are sorted regardless of input order
	if got := NewKeyMissing("p.yaml", "data", "k", []string{"b", "a"}).Available; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("sorted: %v", got)
	}
}

func TestIndexListObjectsKindsNamespaces(t *testing.T) {
	ix := basicSet(t).Managed

	all := ix.List("v1", "ConfigMap", "")
	if len(all) != 2 || all[0].GetName() != "my-config" || all[1].GetName() != "other-config" {
		t.Errorf("List all: %v", all)
	}

	if got := ix.List("v1", "ConfigMap", "ns-b"); len(got) != 1 || got[0].GetName() != "other-config" {
		t.Errorf("List ns-b: %v", got)
	}

	if got := ix.List("v1", "ConfigMap", "nope"); len(got) != 0 {
		t.Errorf("List nope: %v", got)
	}

	if got := ix.List("v1", "Pod", ""); got != nil {
		t.Errorf("List unknown kind: %v", got)
	}

	if !ix.HasKind("v1", "Secret") || ix.HasKind("v1", "Pod") {
		t.Error("HasKind")
	}

	var paths []string
	for _, o := range ix.Objects() {
		p, ok := ix.PathOf(&o)
		if !ok {
			t.Errorf("no path for %s", o.GetName())
		}

		paths = append(paths, p)
	}

	wantPaths := []string{
		"objects/cluster.open-cluster-management.io/v1alpha1/ClusterClaim/id.k8s.io.yaml",
		"objects/v1/ConfigMap/ns-a/my-config.yaml",
		"objects/v1/ConfigMap/ns-b/other-config.yaml",
		"objects/v1/Secret/ns-a/db-creds.yaml",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("Objects paths:\n%v\nwant\n%v", paths, wantPaths)
	}

	var kinds []string
	for _, k := range ix.Kinds() {
		kinds = append(kinds, k.GVK.GroupVersion().String()+"/"+k.GVK.Kind+"="+k.Plural)
	}

	wantKinds := []string{
		"cluster.open-cluster-management.io/v1alpha1/ClusterClaim=clusterclaims",
		"v1/ConfigMap=configmaps",
		"v1/Secret=secrets",
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Errorf("Kinds: %v want %v", kinds, wantKinds)
	}
}

func TestIndexReturnsCopies(t *testing.T) {
	ix := basicSet(t).Managed

	a, _ := ix.Get("v1", "ConfigMap", "ns-a", "my-config")
	if err := unstructured.SetNestedField(a.Object, "mutated", "data", "color"); err != nil {
		t.Fatal(err)
	}

	for _, o := range ix.List("v1", "ConfigMap", "ns-a") {
		_ = unstructured.SetNestedField(o.Object, "mutated", "data", "color")
	}

	objs := ix.Objects()
	_ = unstructured.SetNestedField(objs[1].Object, "mutated", "data", "color")

	b, _ := ix.Get("v1", "ConfigMap", "ns-a", "my-config")
	if v, _, _ := unstructured.NestedString(b.Object, "data", "color"); v != "blue" {
		t.Errorf("index was mutated through a returned copy: %q", v)
	}
}
