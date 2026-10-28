package mockdata

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const errDir = "../../testdata/errors/"

// write creates files (relative path -> content) under a fresh cohort dir.
func write(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func cm(name, ns string) string {
	return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n  namespace: " + ns + "\ndata:\n  k: v\n"
}

func mustLoad(t *testing.T, root string) *Set {
	t.Helper()

	set, err := Load(root)
	if err != nil {
		t.Fatalf("Load(%s): %v", root, err)
	}

	return set
}

func loadErr(t *testing.T, root string) *LoadErrors {
	t.Helper()

	_, err := Load(root)

	var le *LoadErrors
	if !errors.As(err, &le) {
		t.Fatalf("Load(%s): want *LoadErrors, got %v", root, err)
	}

	return le
}

func TestCatalogErrors(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want string
		as   func(error) bool
	}{
		{
			"E5 missing name", "e5-missing-name",
			"mock data: file objects/v1/ConfigMap/ns-a/my-config.yaml: missing metadata.name",
			func(e error) bool { var t *ErrMissingName; return errors.As(e, &t) },
		},
		{
			"E6 name mismatch", "e6-path-mismatch",
			`mock data: file objects/v1/ConfigMap/ns-a/my-config.yaml: metadata.name is "other-name" but directory says "my-config"`,
			func(e error) bool { var t *ErrPathMismatch; return errors.As(e, &t) },
		},
		{
			"E6 namespace mismatch", "e6-namespace-mismatch",
			`mock data: file objects/v1/ConfigMap/ns-a/my-config.yaml: metadata.namespace is "ns-b" but directory says "ns-a"`,
			func(e error) bool { var t *ErrPathMismatch; return errors.As(e, &t) },
		},
		{
			"E7 namespaced kind without ns dir", "e7-namespaced-kind-no-ns-dir",
			"mock data: file objects/v1/ConfigMap/my-config.yaml: Kind ConfigMap is namespaced but file is not under a namespace directory",
			func(e error) bool { var t *ErrScopeMismatch; return errors.As(e, &t) },
		},
		{
			"E7 cluster kind in ns dir", "e7-cluster-kind-in-ns-dir",
			"mock data: file objects/v1/Namespace/ns-a/ns-a.yaml: Kind Namespace is cluster-scoped but file is under a namespace directory",
			func(e error) bool { var t *ErrScopeMismatch; return errors.As(e, &t) },
		},
		{
			"E7 unknown kind inconsistent", "e7-unknown-kind-inconsistent",
			"mock data: file objects/example.com/v1/Widget/ns-a/b-ns.yaml: Kind Widget is cluster-scoped but file is under a namespace directory (scope set by objects/example.com/v1/Widget/a-cluster.yaml)",
			func(e error) bool { var t *ErrScopeMismatch; return errors.As(e, &t) },
		},
		{
			"E8 duplicate", "e8-duplicate-object",
			"mock data: duplicate object v1/ConfigMap ns-a/x in objects/v1/ConfigMap/ns-a/x.json and objects/v1/ConfigMap/ns-a/x.yaml",
			func(e error) bool { var t *ErrDuplicateObject; return errors.As(e, &t) },
		},
		{
			"E9 parse error", "e9-parse-error",
			"", // message checked by prefix below (yaml library text)
			func(e error) bool { var t *ErrInvalidFile; return errors.As(e, &t) },
		},
		{
			"E10 secret looks encoded", "e10-secret-looks-encoded",
			"mock data: file objects/v1/Secret/ns-a/db-creds.yaml: Secret data looks base64-encoded (kubectl output?); decode values or add annotation gs-acm-utils.io/data-encoding: base64",
			func(e error) bool { var t *ErrSecretLooksEncoded; return errors.As(e, &t) },
		},
		{
			"E11 secret bad base64", "e11-secret-bad-base64",
			"mock data: file objects/v1/Secret/ns-a/db-creds.yaml: data.password is not valid base64 (annotation data-encoding: base64 is set)",
			func(e error) bool { var t *ErrSecretBadBase64; return errors.As(e, &t) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			le := loadErr(t, errDir+tc.dir)
			if len(le.Errors) != 1 {
				t.Fatalf("want 1 error, got %d: %v", len(le.Errors), le)
			}

			if !tc.as(le) {
				t.Errorf("errors.As failed for %T", le.Errors[0])
			}

			if tc.want == "" {
				prefix := "mock data: file objects/v1/ConfigMap/ns-a/my-config.yaml: parse error: "
				if !strings.HasPrefix(le.Error(), prefix) {
					t.Errorf("got %q, want prefix %q", le.Error(), prefix)
				}

				return
			}

			if le.Error() != tc.want {
				t.Errorf("message:\n got: %s\nwant: %s", le.Error(), tc.want)
			}
		})
	}
}

func TestE15EmptyCohort(t *testing.T) {
	tests := map[string]func(root string){
		"objects dir absent": func(string) {},
		"only empty subdirs": func(root string) {
			if err := os.MkdirAll(filepath.Join(root, "objects", "v1", "ConfigMap", "ns-a"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}

	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			root := write(t, map[string]string{"params.yaml": "a: b\n"})
			setup(root)

			le := loadErr(t, root)

			want := "mock data: no objects found under " + filepath.ToSlash(filepath.Clean(root)) + "/objects/"
			if le.Error() != want {
				t.Errorf("got %q, want %q", le.Error(), want)
			}

			var e *ErrEmptyCohort
			if !errors.As(le, &e) {
				t.Errorf("errors.As(*ErrEmptyCohort) failed")
			}
		})
	}
}

func TestCohortUnreadable(t *testing.T) {
	le := loadErr(t, filepath.Join(t.TempDir(), "nope"))

	var e *ErrCohortUnreadable
	if !errors.As(le, &e) || !strings.HasSuffix(le.Error(), "does not exist") {
		t.Errorf("unexpected: %v", le)
	}
}

func TestWalkRejections(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte(cm("x", "ns-a")), 0o600); err != nil {
		t.Fatal(err)
	}

	root := write(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/good.yaml":   cm("good", "ns-a"),
		"objects/v1/ConfigMap/ns-a/.hidden.yml": cm("hidden", "ns-a"),
		"objects/v1/ConfigMap/ns-a/notes.txt":   "hi",
		"objects/v1/ConfigMap/.cache/x.yaml":    cm("x", ".cache"),
		"objects/v1/ConfigMap/ns-a/inside.yaml": cm("inside", "ns-a"),
	})
	dir := filepath.Join(root, "objects/v1/ConfigMap/ns-a")

	must := func(err error) {
		t.Helper()

		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Symlink(filepath.Join(root, "nonexistent.yaml"), filepath.Join(dir, "broken.yaml")))
	must(os.Symlink(outside, filepath.Join(dir, "escape.yaml")))
	must(os.Symlink("inside.yaml", filepath.Join(dir, "alias.yaml"))) // legal, but name mismatch

	le := loadErr(t, root)

	got := strings.Split(le.Error(), "\n")
	prefix := "mock data: file objects/v1/ConfigMap/"
	want := []string{
		prefix + ".cache: dotfile",
		prefix + "ns-a/.hidden.yml: dotfile",
		prefix + `ns-a/alias.yaml: metadata.name is "inside" but directory says "alias"`,
		prefix + "ns-a/broken.yaml: broken symlink",
		prefix + "ns-a/escape.yaml: symlink escapes root",
		prefix + "ns-a/notes.txt: unsupported extension",
	}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAggregation(t *testing.T) {
	le := loadErr(t, errDir+"aggregate-two-broken")

	want := "mock data: file objects/v1/ConfigMap/ns-a/a.yaml: missing metadata.name\n" +
		`mock data: file objects/v1/ConfigMap/ns-b/b.yaml: metadata.name is "wrong" but directory says "b"`
	if le.Error() != want || len(le.Errors) != 2 {
		t.Errorf("got:\n%s\nwant:\n%s", le.Error(), want)
	}
}

func TestMultiDocAndEmptyFile(t *testing.T) {
	root := write(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/multi.yaml": cm("multi", "ns-a") + "---\n" + cm("second", "ns-a"),
		"objects/v1/ConfigMap/ns-a/empty.yaml": "# nothing\n",
	})

	want := "mock data: file objects/v1/ConfigMap/ns-a/empty.yaml: parse error: empty document\n" +
		"mock data: file objects/v1/ConfigMap/ns-a/multi.yaml: parse error: multiple YAML documents; one object per file"
	if got := loadErr(t, root).Error(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestJSONYMLAndClusterScopedLayout(t *testing.T) {
	root := write(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/j.json":       `{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"j","namespace":"ns-a"},"data":{"n":"1"}}`,
		"objects/v1/ConfigMap/ns-a/yml.yml":      cm("yml", "ns-a"),
		"objects/v1/Namespace/ns-a.yaml":         "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: ns-a\n",
		"objects/v1/Node/n1.json":                `{"apiVersion":"v1","kind":"Node","metadata":{"name":"n1"}}`,
		"objects/apps/v1/Deployment/ns-a/d.yaml": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: d\n  namespace: ns-a\nspec:\n  replicas: 3\n",
		// namespace omitted in body: taken from directory
		"objects/v1/ConfigMap/ns-b/nons.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: nons\n",
	})
	set := mustLoad(t, root)

	j, err := set.Managed.Get("v1", "ConfigMap", "ns-a", "j")
	if err != nil {
		t.Fatal(err)
	}

	if v, _, _ := unstructured.NestedString(j.Object, "data", "n"); v != "1" {
		t.Errorf("json data.n = %q", v)
	}

	ns, err := set.Managed.Get("v1", "Namespace", "ignored", "ns-a")
	if err != nil || ns.GetNamespace() != "" {
		t.Errorf("Namespace: %v %v", ns, err)
	}

	d, _ := set.Managed.Get("apps/v1", "Deployment", "ns-a", "d")
	if r, _, _ := unstructured.NestedInt64(d.Object, "spec", "replicas"); r != 3 {
		t.Errorf("replicas not int64: %v", d.Object["spec"])
	}

	nons, _ := set.Managed.Get("v1", "ConfigMap", "ns-b", "nons")
	if nons.GetNamespace() != "ns-b" {
		t.Errorf("namespace not defaulted: %q", nons.GetNamespace())
	}

	kinds := map[string]KindInfo{}
	for _, k := range set.Managed.Kinds() {
		kinds[k.GVK.Kind] = k
	}

	if k := kinds["Namespace"]; k.Namespaced || k.Plural != "namespaces" {
		t.Errorf("Namespace kind info: %+v", k)
	}

	if k := kinds["ConfigMap"]; !k.Namespaced || k.Plural != "configmaps" || k.GVK.Version != "v1" {
		t.Errorf("ConfigMap kind info: %+v", k)
	}

	if k := kinds["Deployment"]; !k.Namespaced || k.GVK.Group != "apps" || k.Plural != "deployments" {
		t.Errorf("Deployment kind info: %+v", k)
	}
}

func TestParamsAndHubContext(t *testing.T) {
	root := write(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/a.yaml": cm("a", "ns-a"),
		"params.yaml":                      "env: prod\nreplicas: 3\nnested:\n  k: v\n",
		"hub/context.yaml":                 "clusterName: local-cluster\n",
	})
	set := mustLoad(t, root)

	if set.Params["env"] != "prod" || set.Params["replicas"] != int64(3) {
		t.Errorf("params: %#v", set.Params)
	}

	if set.HubContext["clusterName"] != "local-cluster" {
		t.Errorf("hub context: %#v", set.HubContext)
	}

	if set.HubPresent || set.Hub == nil || len(set.Hub.Objects()) != 0 {
		t.Errorf("hub should be absent but non-nil: present=%v", set.HubPresent)
	}

	bad := write(t, map[string]string{
		"objects/v1/ConfigMap/ns-a/a.yaml": cm("a", "ns-a"),
		"params.yaml":                      "- just\n- a list\n",
	})

	le := loadErr(t, bad)
	if want := "mock data: file params.yaml: parse error: document must be a mapping"; le.Error() != want {
		t.Errorf("got %q want %q", le.Error(), want)
	}

	none := mustLoad(t, write(t, map[string]string{"objects/v1/ConfigMap/ns-a/a.yaml": cm("a", "ns-a")}))
	if none.Params != nil || none.HubContext != nil {
		t.Errorf("absent files should give nil maps")
	}
}

func TestBasicCohortLoadsCleanly(t *testing.T) {
	set := mustLoad(t, "../../testdata/cohorts/basic")

	if !set.HubPresent {
		t.Fatal("hub should be present")
	}

	if got := set.Managed.Namespaces(); strings.Join(got, ",") != "ns-a,ns-b" {
		t.Errorf("managed namespaces: %v", got)
	}

	if got := set.Hub.Namespaces(); strings.Join(got, ",") != "policies" {
		t.Errorf("hub namespaces: %v", got)
	}

	if set.HubContext["clusterName"] != "local-cluster" {
		t.Errorf("hub context: %#v", set.HubContext)
	}

	mc, err := set.Hub.Get("cluster.open-cluster-management.io/v1", "ManagedCluster", "", "local-cluster")
	if err != nil || mc.GetLabels()["environment"] != "dev" {
		t.Errorf("ManagedCluster: %v %v", mc, err)
	}

	cc, err := set.Managed.Get("cluster.open-cluster-management.io/v1alpha1", "ClusterClaim", "", "id.k8s.io")
	if err != nil {
		t.Fatal(err)
	}

	if v, _, _ := unstructured.NestedString(cc.Object, "spec", "value"); v == "" {
		t.Error("ClusterClaim spec.value empty")
	}

	if got := len(set.Managed.Objects()); got != 4 {
		t.Errorf("managed objects: %d", got)
	}

	if got := len(set.Hub.Objects()); got != 2 {
		t.Errorf("hub objects: %d", got)
	}
}

func TestSecretNormalization(t *testing.T) {
	const head = "apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\n  namespace: ns-a\n"

	enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

	tests := []struct {
		name    string
		body    string
		want    map[string]string // expected stored (base64) values
		wantErr string
	}{
		{
			name: "plaintext data and stringData round-trip",
			body: head + "data:\n  password: admin123\n  multi: |\n    line1\n    line2\nstringData:\n  user: admin\n",
			want: map[string]string{
				"password": enc("admin123"), "multi": enc("line1\nline2\n"), "user": enc("admin"),
			},
		},
		{
			name: "stringData wins over data",
			body: head + "data:\n  k: old\nstringData:\n  k: new\n",
			want: map[string]string{"k": enc("new")},
		},
		{
			name: "annotation keeps base64 as-is",
			body: head + "  annotations:\n    gs-acm-utils.io/data-encoding: base64\ndata:\n  k: " + enc("hello") + "\nstringData:\n  other: plain\n",
			want: map[string]string{"k": enc("hello"), "other": enc("plain")},
		},
		{
			name: "kubectl metadata without annotation but non-base64 data is plaintext",
			body: head + "  resourceVersion: \"7\"\ndata:\n  k: not base64!\n",
			want: map[string]string{"k": enc("not base64!")},
		},
		{
			name: "base64-looking plaintext without kubectl metadata is plaintext",
			body: head + "data:\n  k: admin123\n",
			want: map[string]string{"k": enc("admin123")},
		},
		{
			name:    "kubectl guard via managedFields",
			body:    head + "  managedFields:\n  - manager: kubectl\ndata:\n  k: " + enc("v") + "\n",
			wantErr: "mock data: file objects/v1/Secret/ns-a/s.yaml: Secret data looks base64-encoded (kubectl output?); decode values or add annotation gs-acm-utils.io/data-encoding: base64",
		},
		{
			name:    "bad base64 with annotation",
			body:    head + "  annotations:\n    gs-acm-utils.io/data-encoding: base64\ndata:\n  good: " + enc("x") + "\n  bad: \"abc\"\n",
			wantErr: "mock data: file objects/v1/Secret/ns-a/s.yaml: data.bad is not valid base64 (annotation data-encoding: base64 is set)",
		},
		{
			name:    "non-string data value",
			body:    head + "data:\n  port: 8080\n",
			wantErr: "mock data: file objects/v1/Secret/ns-a/s.yaml: data.port must be a string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := write(t, map[string]string{"objects/v1/Secret/ns-a/s.yaml": tc.body})

			if tc.wantErr != "" {
				if got := loadErr(t, root).Error(); got != tc.wantErr {
					t.Errorf("got %q\nwant %q", got, tc.wantErr)
				}

				return
			}

			s, err := mustLoad(t, root).Managed.Get("v1", "Secret", "ns-a", "s")
			if err != nil {
				t.Fatal(err)
			}

			if _, found := s.Object["stringData"]; found {
				t.Error("stringData must be dropped")
			}

			data, _, _ := unstructured.NestedStringMap(s.Object, "data")
			if len(data) != len(tc.want) {
				t.Fatalf("data = %v, want %v", data, tc.want)
			}

			for k, want := range tc.want {
				if data[k] != want {
					t.Errorf("data[%s] = %q, want %q", k, data[k], want)
				}

				if dec, err := base64.StdEncoding.DecodeString(data[k]); err != nil || enc(string(dec)) != want {
					t.Errorf("data[%s] does not round-trip: %v", k, err)
				}
			}
		})
	}
}

func TestSecretRoundTripPlaintext(t *testing.T) {
	root := write(t, map[string]string{
		"objects/v1/Secret/ns-a/s.yaml": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: s\n  namespace: ns-a\ndata:\n  a: plain-a\nstringData:\n  b: plain-b\n",
	})

	s, _ := mustLoad(t, root).Managed.Get("v1", "Secret", "ns-a", "s")
	data, _, _ := unstructured.NestedStringMap(s.Object, "data")

	for k, want := range map[string]string{"a": "plain-a", "b": "plain-b"} {
		dec, err := base64.StdEncoding.DecodeString(data[k])
		if err != nil || string(dec) != want {
			t.Errorf("%s: decoded %q (%v), want %q", k, dec, err, want)
		}
	}
}
