package resolve

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/stolostron/go-template-utils/v7/pkg/templates"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/gscode1/gs-acm-utils/internal/fakecluster"
	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

// strictFuncs implements the strict fromConfigMap / fromSecret / lookup over a
// mock index. It mirrors go-template-utils v7.3.0 (generic_lookup_func.go
// getNamespace/getOrList/lookup and k8sresource_funcs.go) except that missing
// objects, namespaces and keys are errors. It is bound to one index, one
// lookupNamespace and one allow-list, i.e. to a single pass.
type strictFuncs struct {
	ix              *mockdata.Index
	lookupNamespace string
	allowList       []templates.ClusterScopedObjectIdentifier
}

// reference renders the template call for the "(referenced by ...)" suffix.
func reference(fn string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, fn)

	for _, a := range args {
		parts = append(parts, strconv.Quote(a))
	}

	return strings.Join(parts, " ")
}

// getNamespace replicates TemplateResolver.getNamespace.
func (s *strictFuncs) getNamespace(namespace string) (string, error) {
	if s.lookupNamespace != "" {
		if namespace == "" {
			return s.lookupNamespace, nil
		}

		if s.lookupNamespace != namespace {
			return "", fmt.Errorf("%w to %s", templates.ErrRestrictedNamespace, s.lookupNamespace)
		}
	}

	return namespace, nil
}

func (s *strictFuncs) fromConfigMap(namespace, name, key string) (string, error) {
	return s.fromObject("fromConfigMap", "ConfigMap", namespace, name, key)
}

func (s *strictFuncs) fromSecret(namespace, name, key string) (string, error) {
	return s.fromObject("fromSecret", "Secret", namespace, name, key)
}

// fromObject is the shared body of fromConfigMap/fromSecret. For a Secret the
// value is returned as stored (base64), exactly like the library.
func (s *strictFuncs) fromObject(fn, kind, namespace, name, key string) (string, error) {
	if name == "" || (s.lookupNamespace == "" && namespace == "") || key == "" {
		return "", fmt.Errorf("%w: namespace, name, and key must be specified", templates.ErrInvalidInput)
	}

	ns, err := s.getNamespace(namespace)
	if err != nil {
		return "", fmt.Errorf("failed getting the %s %s from %s: %w", kind, name, namespace, err)
	}

	obj, err := s.get("v1", kind, ns, name)
	if err != nil {
		return "", mockdata.WithReference(err, reference(fn, namespace, name, key))
	}

	if err := s.ix.RequireKey(obj, "data", key); err != nil {
		return "", err
	}

	val, _, _ := unstructured.NestedString(obj.Object, "data", key)

	return val, nil
}

// scope reports whether a Kind is namespaced and whether it is known at all
// (indexed, or always advertised by the fake cluster).
func (s *strictFuncs) scope(apiVersion, kind string) (namespaced, known bool) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return false, false
	}

	for _, k := range s.ix.Kinds() {
		if k.GVK.GroupVersion() == gv && k.GVK.Kind == kind {
			return k.Namespaced, true
		}
	}

	for _, k := range fakecluster.AlwaysAdvertised() {
		if k.GVK.GroupVersion() == gv && k.GVK.Kind == kind {
			return k.Namespaced, true
		}
	}

	return false, false
}

// get is Index.Get with one refinement: a Kind that the fake cluster always
// advertises (ConfigMap, Secret, Namespace, Node, ManagedCluster, ClusterClaim)
// is supported even when it has zero files, so an absent object is E1 (namespaced
// Kinds: the namespace directory is missing) or E2 (cluster-scoped Kinds), not E4.
func (s *strictFuncs) get(apiVersion, kind, ns, name string) (*unstructured.Unstructured, error) {
	obj, err := s.ix.Get(apiVersion, kind, ns, name)
	if err == nil {
		return obj, nil
	}

	var e4 *mockdata.ErrLookupUnsupported
	if !errors.As(err, &e4) {
		return nil, err
	}

	gv, perr := schema.ParseGroupVersion(apiVersion)
	if perr != nil {
		return nil, err
	}

	for _, k := range fakecluster.AlwaysAdvertised() {
		if k.GVK.GroupVersion() != gv || k.GVK.Kind != kind {
			continue
		}

		dir := e4.Dir

		if k.Namespaced {
			return nil, &mockdata.ErrNamespaceDirMissing{Dir: dir + ns + "/"}
		}

		return nil, &mockdata.ErrObjectMissing{Path: dir + name + ".yaml"}
	}

	return nil, err
}

func (s *strictFuncs) onAllowList(group, kind, name string) bool {
	for _, item := range s.allowList {
		if item.Group != "*" && item.Group != group {
			continue
		}

		if item.Kind != "*" && item.Kind != kind {
			continue
		}

		if item.Name == "*" || item.Name == name {
			return true
		}
	}

	return false
}

// lookup replicates the library's lookup: a name selects one object (NotFound is
// an error in strict mode), an empty name lists (optionally filtered by label
// selectors; an empty list is not an error).
func (s *strictFuncs) lookup(
	apiVersion, kind, namespace, name string, labelSelector ...string,
) (map[string]any, error) {
	if apiVersion == "" || kind == "" {
		return nil, errors.New("the apiVersion and kind are required")
	}

	ns, err := s.getNamespace(namespace)
	if err != nil {
		return nil, err
	}

	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, err
	}

	selector := labels.NewSelector()

	if len(labelSelector) > 0 && labelSelector[0] != "" {
		if selector, err = labels.Parse(strings.Join(labelSelector, ",")); err != nil {
			return nil, err
		}
	}

	namespaced, known := s.scope(apiVersion, kind)
	if !known {
		return nil, &mockdata.ErrLookupUnsupported{
			APIVersion: apiVersion,
			Kind:       kind,
			Dir:        s.ix.Prefix() + "/" + apiVersion + "/" + kind + "/",
		}
	}

	if !namespaced && s.lookupNamespace != "" {
		if !s.onAllowList(gv.Group, kind, name) {
			return nil, fmt.Errorf("lookup of cluster-scoped resource '%v/%v' is not allowed", kind, name)
		}

		ns = ""
	}

	if name != "" {
		obj, err := s.get(apiVersion, kind, ns, name)
		if err != nil {
			if syn := s.synthesizedNamespace(apiVersion, kind, name); syn != nil {
				return syn.UnstructuredContent(), nil
			}

			ref := append([]string{apiVersion, kind, namespace, name}, labelSelector...)

			return nil, mockdata.WithReference(err, reference("lookup", ref...))
		}

		return obj.UnstructuredContent(), nil
	}

	list := unstructured.UnstructuredList{}

	objs := s.ix.List(apiVersion, kind, ns)
	for _, name := range s.missingNamespaces(apiVersion, kind) {
		objs = append(objs, newNamespace(name))
	}

	for _, obj := range objs {
		if selector.Matches(labels.Set(obj.GetLabels())) {
			list.Items = append(list.Items, *obj)
		}
	}

	return list.UnstructuredContent(), nil
}

func newNamespace(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": name},
	}}
}

// missingNamespaces returns the namespaces used by indexed objects that have no
// explicit Namespace file; fakecluster synthesizes Namespace objects for them,
// so strict lookups must see them too. Empty unless the Kind is v1 Namespace.
func (s *strictFuncs) missingNamespaces(apiVersion, kind string) []string {
	if apiVersion != "v1" || kind != "Namespace" {
		return nil
	}

	var out []string

	for _, ns := range s.ix.Namespaces() {
		if _, err := s.ix.Get("v1", "Namespace", "", ns); err != nil {
			out = append(out, ns)
		}
	}

	return out
}

func (s *strictFuncs) synthesizedNamespace(apiVersion, kind, name string) *unstructured.Unstructured {
	for _, ns := range s.missingNamespaces(apiVersion, kind) {
		if ns == name {
			return newNamespace(name)
		}
	}

	return nil
}
