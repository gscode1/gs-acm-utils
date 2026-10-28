package resolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stolostron/go-template-utils/v7/pkg/templates"
	"sigs.k8s.io/yaml"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

const basicCohort = "../../testdata/cohorts/basic"

func loadSet(t *testing.T, dir string) *mockdata.Set {
	t.Helper()

	set, err := mockdata.Load(dir)
	if err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}

	return set
}

func newEngine(t *testing.T, set *mockdata.Set, strict bool, disabled ...string) *Engine {
	t.Helper()

	e, err := NewEngine(set, Options{Strict: strict, Disabled: disabled, Cohort: "cohort"})
	if err != nil {
		t.Fatal(err)
	}

	return e
}

// extendedCohort is a temp cohort with Nodes, a custom namespaced Kind, labelled
// ConfigMaps and one cluster-scoped ManagedCluster, but no hub/ tree.
func extendedCohort(t *testing.T) *mockdata.Set {
	t.Helper()

	root := t.TempDir()
	files := map[string]string{
		"objects/v1/ConfigMap/ns-a/my-config.yaml": `apiVersion: v1
kind: ConfigMap
metadata: {name: my-config, namespace: ns-a, labels: {color: blue}}
data: {color: blue, size: "3"}
`,
		"objects/v1/ConfigMap/ns-a/second.yaml": `apiVersion: v1
kind: ConfigMap
metadata: {name: second, namespace: ns-a, labels: {color: yellow}}
data: {k: v}
`,
		"objects/v1/Secret/ns-a/db-creds.yaml": `apiVersion: v1
kind: Secret
metadata: {name: db-creds, namespace: ns-a}
stringData: {username: admin, password: s3cr3t-plaintext}
`,
		"objects/v1/Node/worker-1.yaml": `apiVersion: v1
kind: Node
metadata: {name: worker-1, labels: {node-role.kubernetes.io/worker: ""}}
`,
		"objects/cluster.open-cluster-management.io/v1/ManagedCluster/local-cluster.yaml": `apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata: {name: local-cluster, labels: {env: prod}}
`,
		"objects/cluster.open-cluster-management.io/v1alpha1/ClusterClaim/id.k8s.io.yaml": `apiVersion: cluster.open-cluster-management.io/v1alpha1
kind: ClusterClaim
metadata: {name: id.k8s.io}
spec: {value: abc-123}
`,
	}

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

// render resolves a single template expression as the value of key x.
func render(e *Engine, expr string) (string, error) {
	out, err := e.ResolveRaw([]byte("x: '{{ "+expr+" }}'\n"), nil)
	if err != nil {
		return "", err
	}

	var m map[string]any
	if err := yaml.Unmarshal(out, &m); err != nil {
		return "", err
	}

	return fmt.Sprint(m["x"]), nil
}

func TestRowsPresentDataIdenticalStrictAndLenient(t *testing.T) {
	set := extendedCohort(t)
	strict, lenient := newEngine(t, set, true), newEngine(t, set, false)

	cases := []struct{ name, expr, want string }{
		{"fromConfigMap", `fromConfigMap "ns-a" "my-config" "color"`, "blue"},
		{"fromSecret raw is base64", `fromSecret "ns-a" "db-creds" "password"`, "czNjcjN0LXBsYWludGV4dA=="},
		{"fromSecret base64dec", `fromSecret "ns-a" "db-creds" "password" | base64dec`, "s3cr3t-plaintext"},
		{"fromSecret b64dec stringData", `fromSecret "ns-a" "db-creds" "username" | b64dec`, "admin"},
		{"lookup get", `(lookup "v1" "ConfigMap" "ns-a" "second").data.k`, "v"},
		{"lookup list len", `len (lookup "v1" "ConfigMap" "ns-a" "").items`, "2"},
		{"lookup list names", `range (lookup "v1" "ConfigMap" "ns-a" "").items }}{{ .metadata.name }},{{ end`, "my-config,second,"},
		{"lookup label selector", `range (lookup "v1" "ConfigMap" "ns-a" "" "color=yellow").items }}{{ .metadata.name }}{{ end`, "second"},
		{"lookup two selectors", `len (lookup "v1" "ConfigMap" "ns-a" "" "color=blue" "!nope").items`, "1"},
		{"lookup empty selector result", `len (lookup "v1" "ConfigMap" "ns-a" "" "color=red").items`, "0"},
		{"lookup list in empty namespace", `len (lookup "v1" "ConfigMap" "ns-none" "").items`, "0"},
		{"lookup cluster-scoped get", `(lookup "cluster.open-cluster-management.io/v1" "ManagedCluster" "" "local-cluster").metadata.name`, "local-cluster"},
		{"lookup nodes list", `len (lookup "v1" "Node" "" "").items`, "1"},
		{"lookup synthesized namespace", `(lookup "v1" "Namespace" "" "ns-a").metadata.name`, "ns-a"},
		{"copyConfigMapData", `copyConfigMapData "ns-a" "second"`, "map[k:v]"},
		{"lookup namespace list", `len (lookup "v1" "Namespace" "" "").items`, "1"},
		{"fromClusterClaim", `fromClusterClaim "id.k8s.io"`, "abc-123"},
		{"lookupClusterClaim", `lookupClusterClaim "id.k8s.io"`, "abc-123"},
		{"hasNodesWithExactRoles", `hasNodesWithExactRoles "worker"`, "true"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs, errS := render(strict, tc.expr)
			gl, errL := render(lenient, tc.expr)

			if errS != nil || errL != nil {
				t.Fatalf("strict err=%v lenient err=%v", errS, errL)
			}

			if gs != gl {
				t.Errorf("strict %q != lenient %q", gs, gl)
			}

			if gs != tc.want {
				t.Errorf("got %q want %q", gs, tc.want)
			}
		})
	}
}

func TestCopySecretData(t *testing.T) {
	set := extendedCohort(t)

	for _, strict := range []bool{true, false} {
		e := newEngine(t, set, strict)

		out, err := e.ResolveRaw([]byte("data:\n  '{{ copySecretData \"ns-a\" \"db-creds\" }}'\n"), nil)
		if err != nil {
			t.Fatalf("strict=%v: %v", strict, err)
		}

		var m struct{ Data map[string]string }
		if err := yaml.Unmarshal(out, &m); err != nil {
			t.Fatal(err)
		}

		if m.Data["username"] != "YWRtaW4=" {
			t.Errorf("strict=%v: username = %q (out %s)", strict, m.Data["username"], out)
		}
	}
}

// Message shapes of the catalog (plan section 7) for strict mode.
func TestStrictCatalogMessages(t *testing.T) {
	e := newEngine(t, loadSet(t, basicCohort), true)

	cases := []struct {
		name, expr, msg string
		as              func(error) bool
	}{
		{
			"E1 fromConfigMap", `fromConfigMap "ns-zzz" "my-config" "color"`,
			`mock data: namespace directory missing: objects/v1/ConfigMap/ns-zzz/ (referenced by fromConfigMap "ns-zzz" "my-config" "color")`,
			func(err error) bool { var t *mockdata.ErrNamespaceDirMissing; return errors.As(err, &t) },
		},
		{
			"E2 fromConfigMap", `fromConfigMap "ns-a" "nope" "color"`,
			`mock data: object not found: objects/v1/ConfigMap/ns-a/nope.yaml (referenced by fromConfigMap "ns-a" "nope" "color")`,
			func(err error) bool { var t *mockdata.ErrObjectMissing; return errors.As(err, &t) },
		},
		{
			"E3 fromConfigMap", `fromConfigMap "ns-a" "my-config" "nokey"`,
			`mock data: key "nokey" not found in objects/v1/ConfigMap/ns-a/my-config.yaml (data has: color, size)`,
			func(err error) bool { var t *mockdata.ErrKeyMissing; return errors.As(err, &t) },
		},
		{
			"E1 fromSecret", `fromSecret "ns-zzz" "db-creds" "password"`,
			`mock data: namespace directory missing: objects/v1/Secret/ns-zzz/ (referenced by fromSecret "ns-zzz" "db-creds" "password")`,
			func(err error) bool { var t *mockdata.ErrNamespaceDirMissing; return errors.As(err, &t) },
		},
		{
			"E2 fromSecret", `fromSecret "ns-a" "nope" "password"`,
			`mock data: object not found: objects/v1/Secret/ns-a/nope.yaml (referenced by fromSecret "ns-a" "nope" "password")`,
			func(err error) bool { var t *mockdata.ErrObjectMissing; return errors.As(err, &t) },
		},
		{
			"E3 fromSecret", `fromSecret "ns-a" "db-creds" "nokey"`,
			`mock data: key "nokey" not found in objects/v1/Secret/ns-a/db-creds.yaml (data has: password, username)`,
			func(err error) bool { var t *mockdata.ErrKeyMissing; return errors.As(err, &t) },
		},
		{
			"E1 lookup", `(lookup "v1" "ConfigMap" "ns-zzz" "x").data`,
			`mock data: namespace directory missing: objects/v1/ConfigMap/ns-zzz/ (referenced by lookup "v1" "ConfigMap" "ns-zzz" "x")`,
			func(err error) bool { var t *mockdata.ErrNamespaceDirMissing; return errors.As(err, &t) },
		},
		{
			"E2 lookup", `(lookup "v1" "ConfigMap" "ns-a" "x").data`,
			`mock data: object not found: objects/v1/ConfigMap/ns-a/x.yaml (referenced by lookup "v1" "ConfigMap" "ns-a" "x")`,
			func(err error) bool { var t *mockdata.ErrObjectMissing; return errors.As(err, &t) },
		},
		{
			"E4 lookup get", `(lookup "example.com/v1" "Widget" "ns-a" "w").metadata`,
			`mock data: lookup not supported for apiVersion=example.com/v1, Kind=Widget (no files under objects/example.com/v1/Widget/)`,
			func(err error) bool { var t *mockdata.ErrLookupUnsupported; return errors.As(err, &t) },
		},
		{
			"E4 lookup list", `(lookup "example.com/v1" "Widget" "ns-a" "").items`,
			`mock data: lookup not supported for apiVersion=example.com/v1, Kind=Widget (no files under objects/example.com/v1/Widget/)`,
			func(err error) bool { var t *mockdata.ErrLookupUnsupported; return errors.As(err, &t) },
		},
		{
			"E2 advertised cluster-scoped kind without files", `(lookup "v1" "Node" "" "n1").metadata`,
			`mock data: object not found: objects/v1/Node/n1.yaml (referenced by lookup "v1" "Node" "" "n1")`,
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
		})
	}
}

// Non-strict mode keeps library behavior: missing objects error, missing keys are "",
// lookup of a missing object is an empty result.
func TestLenientLibraryBehavior(t *testing.T) {
	e := newEngine(t, loadSet(t, basicCohort), false)

	for _, expr := range []string{
		`fromConfigMap "ns-zzz" "my-config" "color"`,
		`fromConfigMap "ns-a" "nope" "color"`,
		`fromSecret "ns-a" "nope" "password"`,
		`copyConfigMapData "ns-a" "nope"`,
		`copySecretData "ns-a" "nope"`,
		`fromClusterClaim "nope"`,
	} {
		if _, err := render(e, expr); err == nil {
			t.Errorf("%s: expected error", expr)
		}
	}

	for expr, want := range map[string]string{
		`fromConfigMap "ns-a" "my-config" "nokey" | len`:          "0",
		`fromSecret "ns-a" "db-creds" "nokey" | len`:              "0",
		`(lookup "v1" "ConfigMap" "ns-a" "x").data`:               "<no value>",
		`len (lookup "v1" "ConfigMap" "ns-zzz" "").items`:         "0",
		`len (lookup "v1" "ConfigMap" "ns-a" "" "a=b").items`:     "0",
		`fromConfigMap "ns-a" "my-config" "color"`:                "blue",
		`(lookup "v1" "ConfigMap" "ns-a" "my-config").data.color`: "blue",
	} {
		got, err := render(e, expr)
		if err != nil {
			t.Errorf("%s: %v", expr, err)

			continue
		}

		if got != want {
			t.Errorf("%s: got %q want %q", expr, got, want)
		}
	}
}

func TestUnknownKindMapsToUnsupported(t *testing.T) {
	set := loadSet(t, basicCohort)

	t.Run("strict is E4", func(t *testing.T) {
		_, err := render(newEngine(t, set, true), `(lookup "example.com/v1" "Widget" "ns-a" "w").x`)

		var e4 *mockdata.ErrLookupUnsupported
		if !errors.As(err, &e4) {
			t.Fatalf("want E4, got %v", err)
		}
	})

	t.Run("lenient maps ErrMissingAPIResource", func(t *testing.T) {
		_, err := render(newEngine(t, set, false), `(lookup "example.com/v1" "Widget" "ns-a" "w").x`)

		var u *UnsupportedResourceError
		if !errors.As(err, &u) || !errors.Is(err, templates.ErrMissingAPIResource) {
			t.Fatalf("want UnsupportedResourceError wrapping ErrMissingAPIResource, got %v", err)
		}

		if !strings.HasPrefix(err.Error(), "mock data: lookup not supported") {
			t.Errorf("message: %s", err)
		}
	})
}

func TestProtectIsE14(t *testing.T) {
	set := loadSet(t, basicCohort)
	want := `function "protect" is not available in mock mode: encryption is not supported`

	for _, strict := range []bool{true, false} {
		e := newEngine(t, set, strict)

		for _, expr := range []string{
			`"abc" | protect`,
			`protect (fromConfigMap "ns-a" "my-config" "color")`,
		} {
			_, err := render(e, expr)

			var e14 *mockdata.ErrEncryptionUnsupported
			if !errors.As(err, &e14) {
				t.Fatalf("strict=%v %s: want E14, got %v", strict, expr, err)
			}

			if err.Error() != want {
				t.Errorf("message: %s", err)
			}
		}
	}

	// The library's own parse error for a disabled "protect" is mapped too.
	err := translateError(errors.New(`failed to parse: template: tmpl:1: function "protect" not defined`))

	var e14 *mockdata.ErrEncryptionUnsupported
	if !errors.As(err, &e14) {
		t.Errorf("fallback mapping failed: %v", err)
	}
}

func TestDisabledFunctions(t *testing.T) {
	e := newEngine(t, loadSet(t, basicCohort), true, "base64enc", "protect")

	if got := strings.Join(e.DisabledFunctions(), ","); got != "protect,base64enc" {
		t.Errorf("disabled = %s", got)
	}

	if _, err := render(e, `"a" | base64enc`); err == nil || !strings.Contains(err.Error(), "base64enc") {
		t.Errorf("expected disabled-function error, got %v", err)
	}

	if got, err := render(e, `"YQ==" | base64dec`); err != nil || got != "a" {
		t.Errorf("unrelated function should work: %q %v", got, err)
	}
}

func TestLookupNamespaceHonored(t *testing.T) {
	set := loadSet(t, basicCohort)

	for _, strict := range []bool{true, false} {
		e := newEngine(t, set, strict)

		s, err := e.NewSession()
		if err != nil {
			t.Fatal(err)
		}

		po := PassOptions{LookupNamespace: "ns-a", InputIsYAML: true}

		run := func(expr string, po PassOptions) (string, error) {
			res, err := s.Resolve(ManagedPass, []byte("x: '{{ "+expr+" }}'\n"), nil, po)
			if err != nil {
				return "", err
			}

			var m map[string]any
			if err := yaml.Unmarshal(res.ResolvedJSON, &m); err != nil {
				t.Fatal(err)
			}

			return fmt.Sprint(m["x"]), nil
		}

		// An empty namespace defaults to the lookup namespace.
		if got, err := run(`fromConfigMap "" "my-config" "color"`, po); err != nil || got != "blue" {
			t.Errorf("strict=%v default namespace: %q %v", strict, got, err)
		}

		if got, err := run(`(lookup "v1" "ConfigMap" "" "my-config").data.size`, po); err != nil || got != "3" {
			t.Errorf("strict=%v lookup default namespace: %q %v", strict, got, err)
		}

		// A different namespace is restricted.
		for _, expr := range []string{
			`fromConfigMap "ns-b" "other-config" "region"`,
			`(lookup "v1" "ConfigMap" "ns-b" "other-config").data`,
		} {
			if _, err := run(expr, po); !errors.Is(err, templates.ErrRestrictedNamespace) {
				t.Errorf("strict=%v %s: want ErrRestrictedNamespace, got %v", strict, expr, err)
			}
		}

		// Cluster-scoped lookups are denied unless allow-listed.
		cl := `(lookup "cluster.open-cluster-management.io/v1alpha1" "ClusterClaim" "" "id.k8s.io").spec.value`
		if _, err := run(cl, po); err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("strict=%v cluster-scoped should be denied, got %v", strict, err)
		}

		po.ClusterScopedAllowList = []templates.ClusterScopedObjectIdentifier{
			{Group: "cluster.open-cluster-management.io", Kind: "ClusterClaim", Name: "id.k8s.io"},
		}

		if got, err := run(cl, po); err != nil || got != "7b1c0f4e-8f1d-4f0a-9a57-000000000001" {
			t.Errorf("strict=%v allow-listed: %q %v", strict, got, err)
		}
	}
}

func TestHubPass(t *testing.T) {
	set := loadSet(t, basicCohort)

	for _, strict := range []bool{true, false} {
		e := newEngine(t, set, strict)

		s, err := e.NewSession()
		if err != nil {
			t.Fatal(err)
		}

		in := "x: '{{hub fromConfigMap \"\" \"hub-config\" \"logLevel\" hub}}'\n" +
			"y: '{{hub (lookup \"cluster.open-cluster-management.io/v1\" \"ManagedCluster\" \"\" \"local-cluster\").metadata.name hub}}'\n"

		res, err := s.Resolve(HubPass, []byte(in), nil, PassOptions{
			InputIsYAML:     true,
			LookupNamespace: "policies",
			ClusterScopedAllowList: []templates.ClusterScopedObjectIdentifier{
				{Group: "cluster.open-cluster-management.io", Kind: "ManagedCluster", Name: "local-cluster"},
			},
		})
		if err != nil {
			t.Fatalf("strict=%v: %v", strict, err)
		}

		var m map[string]string
		if err := yaml.Unmarshal(res.ResolvedJSON, &m); err != nil {
			t.Fatal(err)
		}

		if m["x"] != "debug" || m["y"] != "local-cluster" {
			t.Errorf("strict=%v: got %v", strict, m)
		}

		// Strict hub functions read the hub index, not the managed one.
		if strict {
			_, err = s.Resolve(HubPass, []byte("x: '{{hub fromConfigMap \"ns-a\" \"my-config\" \"color\" hub}}'\n"), nil,
				PassOptions{InputIsYAML: true})

			var e1 *mockdata.ErrNamespaceDirMissing
			if !errors.As(err, &e1) || !strings.Contains(err.Error(), "hub/objects/v1/ConfigMap/ns-a/") {
				t.Errorf("want hub-index E1, got %v", err)
			}
		}
	}
}

func TestHubDataMissing(t *testing.T) {
	e := newEngine(t, extendedCohort(t), true)
	if e.HubAvailable() {
		t.Fatal("no hub expected")
	}

	s, _ := e.NewSession()

	_, err := s.Resolve(HubPass, []byte("x: '{{hub 1 hub}}'\n"), nil, PassOptions{InputIsYAML: true})

	var e12 *mockdata.ErrHubDataMissing
	if !errors.As(err, &e12) {
		t.Fatalf("want E12, got %v", err)
	}
}

func TestOptionsFreshness(t *testing.T) {
	for _, strict := range []bool{true, false} {
		e := newEngine(t, loadSet(t, basicCohort), strict)

		po := PassOptions{LookupNamespace: "ns-a", ClusterScopedAllowList: []templates.ClusterScopedObjectIdentifier{{Group: "g"}}}
		a, b := e.resolveOptions(ManagedPass, po), e.resolveOptions(ManagedPass, po)

		if a == b {
			t.Fatal("resolveOptions returned the same pointer twice")
		}

		// Mutate the first, as ResolveTemplate does and more.
		a.DenylistFunctions = append(a.DenylistFunctions, "x", "y")
		a.ClusterScopedAllowList[0].Group = "mutated"
		a.LookupNamespace = "mutated"
		delete(a.CustomFunctions, "protect")
		a.CustomFunctions["extra"] = func() {}

		if len(b.DenylistFunctions) != 0 || b.LookupNamespace != "ns-a" || b.ClusterScopedAllowList[0].Group != "g" ||
			b.CustomFunctions["protect"] == nil || b.CustomFunctions["extra"] != nil {
			t.Fatalf("mutation leaked into second options: %+v", b)
		}

		if po.ClusterScopedAllowList[0].Group != "g" {
			t.Fatal("mutation leaked into caller's PassOptions")
		}

		if _, ok := b.CustomFunctions["fromConfigMap"]; ok != strict {
			t.Errorf("strict=%v but fromConfigMap custom=%v", strict, ok)
		}

		// Two consecutive resolutions through a session both succeed and neither leaves state behind.
		s, _ := e.NewSession()
		for i := range 2 {
			res, err := s.Resolve(ManagedPass, []byte("x: '{{ fromConfigMap \"ns-a\" \"my-config\" \"color\" }}'\n"), nil,
				PassOptions{InputIsYAML: true})
			if err != nil || !strings.Contains(string(res.ResolvedJSON), "blue") {
				t.Fatalf("strict=%v call %d: %s %v", strict, i, res.ResolvedJSON, err)
			}
		}

		if fresh := e.resolveOptions(ManagedPass, PassOptions{}); len(fresh.DenylistFunctions) != 0 {
			t.Errorf("fresh options carry denylist: %v", fresh.DenylistFunctions)
		}
	}
}

func TestConcurrentSessions(t *testing.T) {
	e := newEngine(t, loadSet(t, basicCohort), true)

	var wg sync.WaitGroup

	errs := make(chan error, 16)

	for range 16 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range 20 {
				got, err := render(e, `fromConfigMap "ns-a" "my-config" "color"`)
				if err != nil || got != "blue" {
					errs <- fmt.Errorf("got %q err %v", got, err)

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

func TestNewEngineNilSet(t *testing.T) {
	if _, err := NewEngine(nil, Options{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestInvalidArguments(t *testing.T) {
	set := loadSet(t, basicCohort)

	for _, strict := range []bool{true, false} {
		e := newEngine(t, set, strict)

		for _, expr := range []string{
			`fromConfigMap "" "my-config" "color"`,
			`fromConfigMap "ns-a" "" "color"`,
			`fromConfigMap "ns-a" "my-config" ""`,
			`fromSecret "ns-a" "db-creds" ""`,
		} {
			if _, err := render(e, expr); !errors.Is(err, templates.ErrInvalidInput) {
				t.Errorf("strict=%v %s: want ErrInvalidInput, got %v", strict, expr, err)
			}
		}
	}
}
