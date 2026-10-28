// Package fakecluster builds a fake dynamic client and a fake discovery client
// from a mockdata.Index so go-template-utils can run without a cluster.
//
// It uses the recipe proven in the T0 spike (docs/spike-T0.md): static
// FakeDiscovery resources, and a dynamic fake created over an empty scheme with
// a list kind registered for every advertised resource. It depends only on
// mockdata.
package fakecluster

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	discfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

const ocmGroup = "cluster.open-cluster-management.io"

// AlwaysAdvertised returns the Kinds that are advertised by discovery even when
// the index holds no objects of that Kind, so a missing object is NotFound
// rather than "API resource not installed".
func AlwaysAdvertised() []mockdata.KindInfo {
	mk := func(group, version, kind string, namespaced bool) mockdata.KindInfo {
		gvk := schema.GroupVersionKind{Group: group, Version: version, Kind: kind}
		plural, _ := meta.UnsafeGuessKindToResource(gvk)

		return mockdata.KindInfo{GVK: gvk, Plural: plural.Resource, Namespaced: namespaced}
	}

	return []mockdata.KindInfo{
		mk("", "v1", "ConfigMap", true),
		mk("", "v1", "Secret", true),
		mk("", "v1", "Namespace", false),
		mk("", "v1", "Node", false),
		mk(ocmGroup, "v1", "ManagedCluster", false),
		mk(ocmGroup, "v1alpha1", "ClusterClaim", false),
	}
}

// Kinds returns every Kind that New advertises for ix: AlwaysAdvertised plus all
// indexed Kinds (the index wins on conflict), sorted by group/version then Kind.
func Kinds(ix *mockdata.Index) []mockdata.KindInfo {
	type key struct{ gv, kind string }

	merged := map[key]mockdata.KindInfo{}

	for _, k := range AlwaysAdvertised() {
		merged[key{k.GVK.GroupVersion().String(), k.GVK.Kind}] = k
	}

	for _, k := range ix.Kinds() {
		merged[key{k.GVK.GroupVersion().String(), k.GVK.Kind}] = k
	}

	out := make([]mockdata.KindInfo, 0, len(merged))
	for _, k := range merged {
		out = append(out, k)
	}

	sort.Slice(out, func(i, j int) bool {
		gi, gj := out[i].GVK.GroupVersion().String(), out[j].GVK.GroupVersion().String()
		if gi != gj {
			return gi < gj
		}

		return out[i].GVK.Kind < out[j].GVK.Kind
	})

	return out
}

// New returns a fake dynamic client seeded with every object of ix (plus
// synthesized Namespace objects) and a fake discovery client advertising the
// Kinds returned by Kinds. Both are safe for concurrent use and are meant to be
// shared by many template resolvers.
func New(ix *mockdata.Index) (dynamic.Interface, discovery.DiscoveryInterface, error) {
	if ix == nil {
		return nil, nil, fmt.Errorf("fakecluster: nil index")
	}

	kinds := Kinds(ix)
	byGV := map[string]*metav1.APIResourceList{}
	listKinds := map[schema.GroupVersionResource]string{}

	var order []string

	for _, k := range kinds {
		gv := k.GVK.GroupVersion()

		list, ok := byGV[gv.String()]
		if !ok {
			list = &metav1.APIResourceList{GroupVersion: gv.String()}
			byGV[gv.String()] = list
			order = append(order, gv.String())
		}

		list.APIResources = append(list.APIResources, metav1.APIResource{
			Name:       k.Plural,
			Kind:       k.GVK.Kind,
			Namespaced: k.Namespaced,
			Verbs:      metav1.Verbs{"get", "list", "watch"},
		})
		listKinds[gv.WithResource(k.Plural)] = k.GVK.Kind + "List"
	}

	disco := &discfake.FakeDiscovery{Fake: &clienttesting.Fake{}}
	for _, gv := range order {
		disco.Resources = append(disco.Resources, byGV[gv])
	}

	objs := ix.Objects()
	seed := make([]runtime.Object, 0, len(objs))
	haveNS := map[string]bool{}

	for i := range objs {
		o := &objs[i]
		if o.GetAPIVersion() == "v1" && o.GetKind() == "Namespace" {
			haveNS[o.GetName()] = true
		}

		seed = append(seed, o)
	}

	for _, ns := range ix.Namespaces() {
		if haveNS[ns] {
			continue
		}

		seed = append(seed, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata":   map[string]any{"name": ns},
		}})
	}

	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, seed...)

	return dyn, disco, nil
}
