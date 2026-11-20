package mockdata

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	basicDir  = "../../testdata/cohorts/basic"
	basicFile = "../../testdata/fixtures/basic.yaml"
)

const fileHeader = "apiVersion: acm-utils.gskode.com/v1alpha1\nkind: MockData\nspec:\n"

// writeFile writes content to <tmp>/<name> and returns the path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return p
}

func mustLoadFile(t *testing.T, name, content string) *Set {
	t.Helper()

	set, err := LoadFile(writeFile(t, name, content))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}

	return set
}

// fileErrs loads content and returns the error lines with the temp dir replaced by "<dir>".
func fileErrs(t *testing.T, content string) []string {
	t.Helper()

	p := writeFile(t, "fixture.yaml", content)

	_, err := LoadFile(p)

	var le *LoadErrors
	if !errors.As(err, &le) {
		t.Fatalf("want *LoadErrors, got %v", err)
	}

	out := make([]string, len(le.Errors))
	for i, e := range le.Errors {
		out[i] = strings.ReplaceAll(e.Error(), filepath.Dir(p)+string(filepath.Separator), "")
	}

	return out
}

func wantErrs(t *testing.T, got []string, want ...string) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("errors:\n got: %q\nwant: %q", got, want)
	}
}

const cmItem = `    - apiVersion: v1
      kind: ConfigMap
      metadata: {name: %s, namespace: ns-a}
      data: {k: v}
`

func item(name string) string { return strings.Replace(cmItem, "%s", name, 1) }

func sortedObjects(ix *Index) []unstructured.Unstructured {
	objs := ix.Objects()

	sort.Slice(objs, func(i, j int) bool {
		a, b := objs[i], objs[j]
		ka, kb := a.GetAPIVersion()+a.GetKind()+a.GetNamespace()+a.GetName(),
			b.GetAPIVersion()+b.GetKind()+b.GetNamespace()+b.GetName()

		return ka < kb
	})

	return objs
}

func TestLoadFileParityWithDir(t *testing.T) {
	dir := mustLoad(t, basicDir)

	file, err := LoadFile(basicFile)
	if err != nil {
		t.Fatal(err)
	}

	for name, pair := range map[string][2]*Index{"managed": {dir.Managed, file.Managed}, "hub": {dir.Hub, file.Hub}} {
		if !reflect.DeepEqual(sortedObjects(pair[0]), sortedObjects(pair[1])) {
			t.Errorf("%s objects differ:\n dir:  %v\n file: %v", name, sortedObjects(pair[0]), sortedObjects(pair[1]))
		}

		if !reflect.DeepEqual(pair[0].Kinds(), pair[1].Kinds()) {
			t.Errorf("%s Kinds differ: %v vs %v", name, pair[0].Kinds(), pair[1].Kinds())
		}

		if !reflect.DeepEqual(pair[0].Namespaces(), pair[1].Namespaces()) {
			t.Errorf("%s Namespaces differ: %v vs %v", name, pair[0].Namespaces(), pair[1].Namespaces())
		}

		for _, o := range pair[0].Objects() {
			g, err := pair[1].Get(o.GetAPIVersion(), o.GetKind(), o.GetNamespace(), o.GetName())
			if err != nil || !reflect.DeepEqual(g.Object, o.Object) {
				t.Errorf("%s Get(%s %s/%s): %v", name, o.GetKind(), o.GetNamespace(), o.GetName(), err)
			}
		}
	}

	if !reflect.DeepEqual(dir.Params, file.Params) || !reflect.DeepEqual(dir.HubContext, file.HubContext) ||
		dir.HubPresent != file.HubPresent {
		t.Errorf("params/hubContext/hubPresent differ: %v %v %v vs %v %v %v",
			dir.Params, dir.HubContext, dir.HubPresent, file.Params, file.HubContext, file.HubPresent)
	}

	// The loader synthesizes no Namespace objects; they come from Namespaces() downstream.
	if file.Managed.HasKind("v1", "Namespace") {
		t.Error("file loader must not synthesize Namespace objects")
	}
}

func TestLoadFileSecretNormalization(t *testing.T) {
	set, err := LoadFile(basicFile)
	if err != nil {
		t.Fatal(err)
	}

	s, err := set.Managed.Get("v1", "Secret", "ns-a", "db-creds")
	if err != nil {
		t.Fatal(err)
	}

	data, _, _ := unstructured.NestedStringMap(s.Object, "data")
	if data["password"] != "czNjcjN0LXBsYWludGV4dA==" || data["username"] != "YWRtaW4=" {
		t.Fatalf("data %v", data)
	}

	if _, ok := s.Object["stringData"]; ok {
		t.Fatal("stringData must be removed")
	}
}

func TestLoadFileJSON(t *testing.T) {
	set := mustLoadFile(t, "fixture.json", `{"apiVersion":"acm-utils.gskode.com/v1alpha1","kind":"MockData",
"spec":{"managed":[{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"a","namespace":"n"},"data":{"n":1}}]}}`)

	o, err := set.Managed.Get("v1", "ConfigMap", "n", "a")
	if err != nil {
		t.Fatal(err)
	}

	if v, _, _ := unstructured.NestedFieldNoCopy(o.Object, "data", "n"); v != int64(1) {
		t.Fatalf("want int64(1), got %T %v", v, v)
	}
}

func TestLoadFileHubAndContext(t *testing.T) {
	body := fileHeader + "  managed:\n" + item("a")

	t.Run("no hub", func(t *testing.T) {
		set := mustLoadFile(t, "f.yaml", body)
		if set.HubPresent || set.Hub == nil || len(set.Hub.Objects()) != 0 {
			t.Fatalf("HubPresent=%v hub=%v", set.HubPresent, set.Hub)
		}

		if set.HubContext != nil || set.Params != nil {
			t.Fatalf("want nil hubContext/params, got %v %v", set.HubContext, set.Params)
		}
	})

	t.Run("empty hub list", func(t *testing.T) {
		set := mustLoadFile(t, "f.yaml", body+"  hub: []\n")
		if !set.HubPresent || len(set.Hub.Objects()) != 0 {
			t.Fatalf("HubPresent=%v", set.HubPresent)
		}
	})

	t.Run("hub objects, hubContext, params", func(t *testing.T) {
		set := mustLoadFile(t, "f.yaml", body+`  hub:
    - apiVersion: v1
      kind: ConfigMap
      metadata: {name: a, namespace: ns-a}
      data: {mode: test}
  hubContext:
    clusterName: local-cluster
    clusterLabels: {environment: dev}
  params:
    Env: dev
    Count: 3
`)
		if !set.HubPresent || len(set.Hub.Objects()) != 1 || len(set.Managed.Objects()) != 1 {
			t.Fatalf("managed/hub independent: %d %d", len(set.Managed.Objects()), len(set.Hub.Objects()))
		}

		if set.HubContext["clusterName"] != "local-cluster" {
			t.Fatalf("hubContext %v", set.HubContext)
		}

		if set.Params["Env"] != "dev" || set.Params["Count"] != int64(3) {
			t.Fatalf("params %#v", set.Params)
		}
	})

	t.Run("hubContext without hub, empty maps", func(t *testing.T) {
		set := mustLoadFile(t, "f.yaml", body+"  hubContext: {}\n  params: {}\n")
		if set.HubPresent || set.HubContext == nil || set.Params == nil {
			t.Fatalf("HubPresent=%v hubContext=%v params=%v", set.HubPresent, set.HubContext, set.Params)
		}
	})
}

func TestLoadFileLookupErrorsReadNaturally(t *testing.T) {
	set := mustLoadFile(t, "fixture.yaml", fileHeader+"  managed:\n"+item("a"))

	_, err := set.Managed.Get("apps/v1", "Deployment", "ns-a", "x")

	var e4 *ErrLookupUnsupported
	if !errors.As(err, &e4) || !strings.Contains(err.Error(), "no objects of that Kind in spec.managed (") {
		t.Fatalf("E4: %v", err)
	}

	_, err = set.Managed.Get("v1", "ConfigMap", "ns-z", "a")

	var e1 *ErrNamespaceDirMissing
	if !errors.As(err, &e1) || !strings.Contains(err.Error(), "no v1/ConfigMap objects in namespace ns-z under spec.managed (") {
		t.Fatalf("E1: %v", err)
	}

	_, err = set.Managed.Get("v1", "ConfigMap", "ns-a", "zzz")

	var e2 *ErrObjectMissing
	if !errors.As(err, &e2) || !strings.Contains(err.Error(), "object not found: v1/ConfigMap ns-a/zzz in spec.managed (") {
		t.Fatalf("E2: %v", err)
	}

	if err := set.Managed.RequireKey(mustGet(t, set, "a"), "data", "nope"); err == nil ||
		!strings.Contains(err.Error(), "spec.managed[0] (v1/ConfigMap ns-a/a)") {
		t.Fatalf("E3: %v", err)
	}
}

func mustGet(t *testing.T, set *Set, name string) *unstructured.Unstructured {
	t.Helper()

	o, err := set.Managed.Get("v1", "ConfigMap", "ns-a", name)
	if err != nil {
		t.Fatal(err)
	}

	return o
}

func TestLoadFileDirMessagesUnchanged(t *testing.T) {
	set := mustLoad(t, basicDir)

	_, err := set.Managed.Get("apps/v1", "Deployment", "ns-a", "x")
	if err == nil || err.Error() != "mock data: lookup not supported for apiVersion=apps/v1, Kind=Deployment (no files under objects/apps/v1/Deployment/)" {
		t.Fatalf("E4: %v", err)
	}

	_, err = set.Managed.Get("v1", "ConfigMap", "ns-z", "a")
	if err == nil || err.Error() != "mock data: namespace directory missing: objects/v1/ConfigMap/ns-z/" {
		t.Fatalf("E1: %v", err)
	}
}

const e15File = "fixture.yaml: spec: no objects found: neither spec.managed nor spec.hub contains objects"

func TestLoadFileHubOnly(t *testing.T) {
	for name, managed := range map[string]string{
		"managed absent": "",
		"managed null":   "  managed: null\n",
		"managed empty":  "  managed: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			set := mustLoadFile(t, "fixture.yaml", fileHeader+managed+"  hub:\n"+item("h"))

			if set.Managed == nil || len(set.Managed.Objects()) != 0 {
				t.Fatalf("Managed = %v, want empty non-nil index", set.Managed)
			}

			if !set.HubPresent || set.Hub == nil || len(set.Hub.Objects()) != 1 {
				t.Fatalf("hub not loaded: present=%v", set.HubPresent)
			}
		})
	}
}

func TestLoadFileSchemaErrors(t *testing.T) {
	cases := map[string]struct {
		content string
		want    []string
	}{
		"empty":         {"", []string{"fixture.yaml: parse error: empty document"}},
		"comments only": {"# nothing\n", []string{"fixture.yaml: parse error: empty document"}},
		"multi-doc": {
			fileHeader + "  managed: []\n---\nfoo: bar\n",
			[]string{"fixture.yaml: parse error: multiple YAML documents; exactly one MockData document is required"},
		},
		"malformed":     {"apiVersion: [\n", nil},
		"not a mapping": {"- a\n- b\n", []string{"fixture.yaml: parse error: document must be a mapping"}},
		"duplicate key": {
			fileHeader + "  managed:\n" + item("a") + "  managed: []\n", nil,
		},
		"missing apiVersion": {
			"kind: MockData\nspec:\n  managed:\n" + item("a"),
			[]string{"fixture.yaml: missing apiVersion"},
		},
		"wrong apiVersion": {
			"apiVersion: v1\nkind: MockData\nspec:\n  managed:\n" + item("a"),
			[]string{`fixture.yaml: unsupported apiVersion "v1" (want acm-utils.gskode.com/v1alpha1)`},
		},
		"wrong kind": {
			"apiVersion: acm-utils.gskode.com/v1alpha1\nkind: Other\nspec:\n  managed:\n" + item("a"),
			[]string{`fixture.yaml: unsupported kind "Other" (want MockData)`},
		},
		"missing spec": {
			"apiVersion: acm-utils.gskode.com/v1alpha1\nkind: MockData\n",
			[]string{"fixture.yaml: missing spec"},
		},
		"spec not a mapping": {
			"apiVersion: acm-utils.gskode.com/v1alpha1\nkind: MockData\nspec: []\n",
			[]string{"fixture.yaml: spec: must be a mapping"},
		},
		"unknown fields": {
			"apiVersion: acm-utils.gskode.com/v1alpha1\nkind: MockData\nextra: 1\nmetadata: {name: x, foo: 1}\nspec:\n  managed:\n" +
				item("a") + "  managd: []\n",
			[]string{
				`fixture.yaml: unknown field "extra"`,
				`fixture.yaml: metadata: unknown field "foo"`,
				`fixture.yaml: spec: unknown field "managd"`,
			},
		},
		"managed not a list": {
			fileHeader + "  managed: {a: b}\n",
			[]string{"fixture.yaml: spec.managed: must be a list"},
		},
		"hub not a list": {
			fileHeader + "  managed:\n" + item("a") + "  hub: x\n",
			[]string{"fixture.yaml: spec.hub: must be a list"},
		},
		"params not a mapping": {
			fileHeader + "  managed:\n" + item("a") + "  params: [1]\n",
			[]string{"fixture.yaml: spec.params: must be a mapping"},
		},
		"managed absent, hub empty": {
			fileHeader + "  hub: []\n",
			[]string{e15File},
		},
		"managed empty, hub absent": {
			fileHeader + "  managed: []\n",
			[]string{e15File},
		},
		"managed null, hub null": {
			fileHeader + "  managed: null\n  hub: null\n",
			[]string{e15File},
		},
		"spec empty": {
			fileHeader + "  hubContext: {a: b}\n",
			[]string{e15File},
		},
		"managed not a list, hub empty": {
			fileHeader + "  managed: x\n  hub: []\n",
			[]string{"fixture.yaml: spec.managed: must be a list"},
		},
		"managed empty, hub not a list": {
			fileHeader + "  managed: []\n  hub: {a: b}\n",
			[]string{"fixture.yaml: spec.hub: must be a list"},
		},
		"managed empty, hub items all invalid": {
			fileHeader + "  managed: []\n  hub:\n    - {apiVersion: v1, kind: ConfigMap, metadata: {namespace: ns-a}}\n    - x\n",
			[]string{
				"fixture.yaml: spec.hub[0]: missing metadata.name",
				"fixture.yaml: spec.hub[1]: must be a mapping",
			},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := fileErrs(t, c.content)
			if c.want == nil {
				if len(got) == 0 || !strings.HasPrefix(got[0], "fixture.yaml: parse error: ") {
					t.Fatalf("want a parse error, got %q", got)
				}

				return
			}

			wantErrs(t, got, c.want...)
		})
	}
}

func TestLoadFileUnreadable(t *testing.T) {
	_, err := LoadFile("/nonexistent/fixture.yaml")

	var le *LoadErrors
	if !errors.As(err, &le) {
		t.Fatalf("got %v", err)
	}

	var e *ErrCohortUnreadable
	if !errors.As(err, &e) || le.Error() != "/nonexistent/fixture.yaml: does not exist" {
		t.Fatalf("got %q", le.Error())
	}

	_, err = LoadFile(t.TempDir())
	if !errors.As(err, &le) || !strings.Contains(le.Error(), "is a directory") {
		t.Fatalf("got %v", err)
	}
}

func TestLoadFileObjectErrors(t *testing.T) {
	secret := func(extra string) string {
		return "    - apiVersion: v1\n      kind: Secret\n      metadata: {name: s, namespace: ns-a" + extra + "\n"
	}

	cases := map[string]struct {
		managed string
		want    []string
	}{
		"missing name": {
			"    - apiVersion: v1\n      kind: ConfigMap\n      metadata: {namespace: ns-a}\n",
			[]string{"fixture.yaml: spec.managed[0]: missing metadata.name"},
		},
		"missing kind": {
			"    - apiVersion: v1\n      metadata: {name: a}\n",
			[]string{"fixture.yaml: spec.managed[0]: missing kind"},
		},
		"non-mapping entry": {
			"    - just-a-string\n",
			[]string{"fixture.yaml: spec.managed[0]: must be a mapping"},
		},
		"duplicate identity": {
			item("a") + item("a"),
			[]string{"fixture.yaml: spec.managed[1]: duplicate object v1/ConfigMap ns-a/a (first defined at spec.managed[0])"},
		},
		"namespaced kind without namespace": {
			"    - apiVersion: v1\n      kind: ConfigMap\n      metadata: {name: a}\n",
			[]string{"fixture.yaml: spec.managed[0] (v1/ConfigMap a): Kind ConfigMap is namespaced but metadata.namespace is not set"},
		},
		"cluster-scoped kind with namespace": {
			"    - apiVersion: v1\n      kind: Namespace\n      metadata: {name: a, namespace: x}\n",
			[]string{"fixture.yaml: spec.managed[0] (v1/Namespace x/a): Kind Namespace is cluster-scoped but metadata.namespace is set"},
		},
		"unknown kind scope mismatch": {
			"    - apiVersion: ex.io/v1\n      kind: Foo\n      metadata: {name: a, namespace: ns-a}\n" +
				"    - apiVersion: ex.io/v1\n      kind: Foo\n      metadata: {name: b}\n",
			[]string{"fixture.yaml: spec.managed[1] (ex.io/v1/Foo b): Kind Foo is namespaced but metadata.namespace is not set (scope set by spec.managed[0])"},
		},
		"secret looks encoded": {
			secret(", resourceVersion: '1'}\n      data: {k: dGVzdA==}"),
			[]string{"fixture.yaml: spec.managed[0] (v1/Secret ns-a/s): Secret data looks base64-encoded (kubectl output?); " +
				"decode values or add annotation gs-acm-utils.io/data-encoding: base64"},
		},
		"secret bad base64": {
			secret(", annotations: {gs-acm-utils.io/data-encoding: base64}}\n      data: {k: '!!!'}"),
			[]string{"fixture.yaml: spec.managed[0] (v1/Secret ns-a/s): data.k is not valid base64 (annotation data-encoding: base64 is set)"},
		},
		"secret data not a string": {
			secret("}\n      data: {k: 1}"),
			[]string{"fixture.yaml: spec.managed[0] (v1/Secret ns-a/s): data.k must be a string"},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			wantErrs(t, fileErrs(t, fileHeader+"  managed:\n"+c.managed), c.want...)
		})
	}
}

func TestLoadFileManagedAndHubIndependent(t *testing.T) {
	hubItem := item("a")

	set := mustLoadFile(t, "f.yaml", fileHeader+"  managed:\n"+item("a")+"  hub:\n"+hubItem)
	if len(set.Managed.Objects()) != 1 || len(set.Hub.Objects()) != 1 {
		t.Fatal("same identity must be allowed once in each view")
	}

	got := fileErrs(t, fileHeader+"  managed:\n"+item("a")+"  hub:\n"+hubItem+hubItem)
	wantErrs(t, got, "fixture.yaml: spec.hub[1]: duplicate object v1/ConfigMap ns-a/a (first defined at spec.hub[0])")
}

func TestLoadFileErrorOrderIsNumeric(t *testing.T) {
	var b strings.Builder

	b.WriteString(fileHeader + "  managed:\n")

	for i := 0; i < 12; i++ {
		b.WriteString("    - apiVersion: v1\n      kind: ConfigMap\n      metadata: {namespace: ns-a}\n")
	}

	got := fileErrs(t, b.String())
	if len(got) != 12 {
		t.Fatalf("got %d errors", len(got))
	}

	for i, line := range got {
		want := "fixture.yaml: spec.managed[" + strconv.Itoa(i) + "]: missing metadata.name"
		if line != want {
			t.Fatalf("error %d: %q, want %q", i, line, want)
		}
	}
}

func TestLoadFileObjectsOrderedByPosition(t *testing.T) {
	var b strings.Builder

	b.WriteString(fileHeader + "  managed:\n")

	for _, n := range []string{"zz", "aa", "mm"} {
		b.WriteString(item(n))
	}

	set := mustLoadFile(t, "f.yaml", b.String())

	var names []string
	for _, o := range set.Managed.Objects() {
		names = append(names, o.GetName())
	}

	if !reflect.DeepEqual(names, []string{"zz", "aa", "mm"}) {
		t.Fatalf("Objects() order %v", names)
	}
}
