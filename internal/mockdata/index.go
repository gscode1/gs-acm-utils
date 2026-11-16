package mockdata

import (
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// KindInfo describes one Kind present in an Index.
type KindInfo struct {
	GVK        schema.GroupVersionKind
	Plural     string
	Namespaced bool
}

type entry struct {
	obj  *unstructured.Unstructured
	path string
	// ord is the position in the source file (file format only).
	ord int
}

type kindKey struct{ apiVersion, kind string }

type kindData struct {
	info KindInfo
	// byNS maps namespace ("" for cluster-scoped Kinds) -> name -> entry.
	byNS map[string]map[string]*entry
}

// Index is an immutable, in-memory view of one cluster view: an objects/ tree
// or one list of a mock-data file. It is safe for concurrent use; every object
// returned is a deep copy, so callers may mutate results freely.
type Index struct {
	src   source
	kinds map[kindKey]*kindData
}

func newIndex(src source) *Index {
	return &Index{src: src, kinds: map[kindKey]*kindData{}}
}

func (ix *Index) add(
	apiVersion, kind, ns, name string, namespaced bool, obj *unstructured.Unstructured, path string, ord int,
) {
	kk := kindKey{apiVersion, kind}

	kd := ix.kinds[kk]
	if kd == nil {
		gvk := obj.GroupVersionKind()
		plural, _ := meta.UnsafeGuessKindToResource(gvk)
		kd = &kindData{
			info: KindInfo{GVK: gvk, Plural: plural.Resource, Namespaced: namespaced},
			byNS: map[string]map[string]*entry{},
		}
		ix.kinds[kk] = kd
	}

	m := kd.byNS[ns]
	if m == nil {
		m = map[string]*entry{}
		kd.byNS[ns] = m
	}

	m[name] = &entry{obj: obj, path: path, ord: ord}
}

// Where names the file location this index was loaded from, for example
// "spec.managed (fixture.yaml)". It is empty for the directory format.
func (ix *Index) Where() string { return ix.src.where() }

// Unsupported builds E4: no objects exist for the apiVersion/Kind.
func (ix *Index) Unsupported(apiVersion, kind string) *ErrLookupUnsupported {
	return ix.src.unsupported(apiVersion, kind)
}

// NamespaceMissing builds E1: no object of the namespaced Kind exists in ns.
func (ix *Index) NamespaceMissing(apiVersion, kind, ns string) *ErrNamespaceDirMissing {
	return ix.src.namespaceMissing(apiVersion, kind, ns, ns+"/")
}

// ObjectMissing builds E2: the object does not exist. For cluster-scoped Kinds
// pass an empty ns.
func (ix *Index) ObjectMissing(apiVersion, kind, ns, name string) *ErrObjectMissing {
	return ix.src.objectMissing(apiVersion, kind, ns, name)
}

// Get returns a copy of the object, or a typed error: *ErrLookupUnsupported (E4)
// when no objects exist for the apiVersion/Kind, *ErrNamespaceDirMissing (E1) when
// the namespace directory is missing, *ErrObjectMissing (E2) when the object is.
// For cluster-scoped Kinds ns is ignored.
func (ix *Index) Get(apiVersion, kind, ns, name string) (*unstructured.Unstructured, error) {
	kd := ix.kinds[kindKey{apiVersion, kind}]
	if kd == nil {
		return nil, ix.Unsupported(apiVersion, kind)
	}

	if !kd.info.Namespaced {
		ns = ""
	}

	m, ok := kd.byNS[ns]
	if !ok {
		return nil, ix.src.namespaceMissing(apiVersion, kind, ns, nsSegment(ns))
	}

	e, ok := m[name]
	if !ok {
		return nil, ix.src.objectMissing(apiVersion, kind, ns, name)
	}

	return e.obj.DeepCopy(), nil
}

func nsSegment(ns string) string {
	if ns == "" {
		return ""
	}

	return ns + "/"
}

// HasKind reports whether any file exists for the apiVersion/Kind.
func (ix *Index) HasKind(apiVersion, kind string) bool {
	return ix.kinds[kindKey{apiVersion, kind}] != nil
}

// Path returns the source locator of an object: its cohort-relative path in the
// directory format, "<file>: spec.managed[N] (<identity>)" in the file format.
func (ix *Index) Path(apiVersion, kind, ns, name string) (string, bool) {
	kd := ix.kinds[kindKey{apiVersion, kind}]
	if kd == nil {
		return "", false
	}

	if !kd.info.Namespaced {
		ns = ""
	}

	e, ok := kd.byNS[ns][name]
	if !ok {
		return "", false
	}

	return e.path, true
}

// PathOf is Path for an object value.
func (ix *Index) PathOf(obj *unstructured.Unstructured) (string, bool) {
	return ix.Path(obj.GetAPIVersion(), obj.GetKind(), obj.GetNamespace(), obj.GetName())
}

// List returns copies of the objects of a Kind sorted by namespace then name.
// For namespaced Kinds an empty ns lists across all namespaces; for
// cluster-scoped Kinds ns is ignored. An unknown Kind yields an empty list.
func (ix *Index) List(apiVersion, kind, ns string) []*unstructured.Unstructured {
	kd := ix.kinds[kindKey{apiVersion, kind}]
	if kd == nil {
		return nil
	}

	var es []*entry

	for n, m := range kd.byNS {
		if kd.info.Namespaced && ns != "" && n != ns {
			continue
		}

		for _, e := range m {
			es = append(es, e)
		}
	}

	sortEntries(es)

	out := make([]*unstructured.Unstructured, len(es))
	for i, e := range es {
		out[i] = e.obj.DeepCopy()
	}

	return out
}

// Objects returns copies of every object sorted by source path (directory
// format) or by position in the file (file format).
func (ix *Index) Objects() []unstructured.Unstructured {
	var es []*entry

	for _, kd := range ix.kinds {
		for _, m := range kd.byNS {
			for _, e := range m {
				es = append(es, e)
			}
		}
	}

	sort.Slice(es, func(i, j int) bool { return ix.src.less(es[i], es[j]) })

	out := make([]unstructured.Unstructured, len(es))
	for i, e := range es {
		out[i] = *e.obj.DeepCopy()
	}

	return out
}

// Kinds returns every Kind present, sorted by apiVersion then Kind.
func (ix *Index) Kinds() []KindInfo {
	out := make([]KindInfo, 0, len(ix.kinds))
	for _, kd := range ix.kinds {
		out = append(out, kd.info)
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].GVK, out[j].GVK
		if a.GroupVersion().String() != b.GroupVersion().String() {
			return a.GroupVersion().String() < b.GroupVersion().String()
		}

		return a.Kind < b.Kind
	})

	return out
}

// Namespaces returns the sorted, de-duplicated namespaces that hold namespaced objects.
func (ix *Index) Namespaces() []string {
	set := map[string]struct{}{}

	for _, kd := range ix.kinds {
		if !kd.info.Namespaced {
			continue
		}

		for ns := range kd.byNS {
			set[ns] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}

	sort.Strings(out)

	return out
}

// RequireKey checks that obj.<field> (for example "data") has key and returns
// *ErrKeyMissing (E3) listing the available keys, sorted, when it does not.
// Strict template functions use it after a successful Get.
func (ix *Index) RequireKey(obj *unstructured.Unstructured, field, key string) error {
	section, _ := obj.Object[field].(map[string]any)
	if _, ok := section[key]; ok {
		return nil
	}

	path, _ := ix.PathOf(obj)

	return NewKeyMissing(path, field, key, sortedAnyKeys(section))
}

// NewKeyMissing builds E3. available is copied and sorted.
func NewKeyMissing(path, field, key string, available []string) *ErrKeyMissing {
	av := append([]string(nil), available...)
	sort.Strings(av)

	return &ErrKeyMissing{Path: path, Field: field, Key: key, Available: av}
}

func sortEntries(es []*entry) {
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i].obj, es[j].obj
		if a.GetNamespace() != b.GetNamespace() {
			return a.GetNamespace() < b.GetNamespace()
		}

		return a.GetName() < b.GetName()
	})
}

// source is the strategy that knows where an Index's objects came from and how
// to phrase locators and lookup errors for that origin. The directory format
// and the consolidated-file format each have one; error text for the directory
// format must stay byte-identical.
type source interface {
	// where is "" for directories, e.g. "spec.managed (fixture.yaml)" for files.
	where() string
	unsupported(apiVersion, kind string) *ErrLookupUnsupported
	// segment is the pre-rendered namespace directory segment (directory format only).
	namespaceMissing(apiVersion, kind, ns, segment string) *ErrNamespaceDirMissing
	objectMissing(apiVersion, kind, ns, name string) *ErrObjectMissing
	less(a, b *entry) bool
}

// dirSource is an objects/ tree below a cohort root.
type dirSource struct {
	prefix string // "objects" or "hub/objects"
}

func (d dirSource) where() string { return "" }

func (d dirSource) kindDir(apiVersion, kind string) string {
	return d.prefix + "/" + apiVersion + "/" + kind + "/"
}

func (d dirSource) unsupported(apiVersion, kind string) *ErrLookupUnsupported {
	return &ErrLookupUnsupported{APIVersion: apiVersion, Kind: kind, Dir: d.kindDir(apiVersion, kind)}
}

func (d dirSource) namespaceMissing(apiVersion, kind, _, segment string) *ErrNamespaceDirMissing {
	return &ErrNamespaceDirMissing{Dir: d.kindDir(apiVersion, kind) + segment}
}

func (d dirSource) objectMissing(apiVersion, kind, ns, name string) *ErrObjectMissing {
	return &ErrObjectMissing{Path: d.kindDir(apiVersion, kind) + nsSegment(ns) + name + ".yaml"}
}

func (d dirSource) less(a, b *entry) bool { return a.path < b.path }

// fileSource is one list (spec.managed or spec.hub) of a consolidated mock-data file.
type fileSource struct {
	file string // path as given
	view string // "spec.managed" or "spec.hub"
}

func (f fileSource) where() string { return f.view + " (" + f.file + ")" }

func (f fileSource) unsupported(apiVersion, kind string) *ErrLookupUnsupported {
	return &ErrLookupUnsupported{APIVersion: apiVersion, Kind: kind, Where: f.where()}
}

func (f fileSource) namespaceMissing(apiVersion, kind, ns, _ string) *ErrNamespaceDirMissing {
	return &ErrNamespaceDirMissing{Where: f.where(), APIVersion: apiVersion, Kind: kind, Namespace: ns}
}

func (f fileSource) objectMissing(apiVersion, kind, ns, name string) *ErrObjectMissing {
	return &ErrObjectMissing{Where: f.where(), APIVersion: apiVersion, Kind: kind, Namespace: ns, Name: name}
}

func (f fileSource) less(a, b *entry) bool { return a.ord < b.ord }
