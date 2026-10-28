package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gscode1/gs-acm-utils/internal/fakecluster"
	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

// NFR-2 benchmark corpus (plan T9): 500 files = 50 namespaces x 10 Kinds, one
// object per Kind and namespace.
const (
	benchNamespaces = 50
	// coldLoadCeiling is a deliberately coarse guard, not the performance budget
	// (see docs/performance.md).
	coldLoadCeiling = 2 * time.Second
)

type benchKind struct {
	apiVersion, kind, prefix string
}

var benchKinds = []benchKind{
	{"v1", "ConfigMap", "cm"},
	{"v1", "Secret", "sec"},
	{"v1", "ServiceAccount", "sa"},
	{"v1", "Service", "svc"},
	{"apps/v1", "Deployment", "dep"},
	{"rbac.authorization.k8s.io/v1", "Role", "role"},
	{"rbac.authorization.k8s.io/v1", "RoleBinding", "rb"},
	{"networking.k8s.io/v1", "Ingress", "ing"},
	{"example.com/v1", "Widget", "widget"},
	{"example.com/v1", "Gadget", "gadget"},
}

// writeBenchCohort generates the 500-file / 50-namespace / 10-Kind cohort.
func writeBenchCohort(tb testing.TB, root string) {
	tb.Helper()

	for n := range benchNamespaces {
		ns := fmt.Sprintf("ns-%02d", n)

		for _, k := range benchKinds {
			name := fmt.Sprintf("%s-%s", k.prefix, ns)
			dir := filepath.Join(root, "objects", filepath.FromSlash(k.apiVersion), k.kind, ns)

			if err := os.MkdirAll(dir, 0o755); err != nil {
				tb.Fatal(err)
			}

			body := fmt.Sprintf("apiVersion: %s\nkind: %s\nmetadata:\n  name: %s\n  namespace: %s\n  labels:\n    tier: t%d\n",
				k.apiVersion, k.kind, name, ns, n%5)

			switch k.kind {
			case "ConfigMap":
				body += fmt.Sprintf("data:\n  color: blue\n  size: \"%d\"\n  region: eu-west-1\n", n)
			case "Secret":
				body += "stringData:\n  password: s3cr3t\n  user: admin\n"
			default:
				body += "spec:\n  replicas: 1\n"
			}

			if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o644); err != nil {
				tb.Fatal(err)
			}
		}
	}
}

// benchPolicy is a ConfigurationPolicy with 20 template calls spread over the
// cohort: ConfigMap and Secret reads, single-object lookups and a list lookup.
func benchPolicy() []byte {
	var calls []string

	for i := range 6 {
		ns := fmt.Sprintf("ns-%02d", i*7)
		calls = append(calls,
			fmt.Sprintf(`{{ fromConfigMap "%s" "cm-%s" "color" }}`, ns, ns),
			fmt.Sprintf(`{{ fromSecret "%s" "sec-%s" "password" | base64dec }}`, ns, ns),
			fmt.Sprintf(`{{ (lookup "v1" "ServiceAccount" "%s" "sa-%s").metadata.name }}`, ns, ns),
		)
	}

	calls = append(calls,
		`{{ (lookup "apps/v1" "Deployment" "ns-10" "dep-ns-10").spec.replicas }}`,
		`{{ len (lookup "v1" "ConfigMap" "ns-20" "").items }}`,
	)

	var b strings.Builder

	b.WriteString("apiVersion: policy.open-cluster-management.io/v1\nkind: ConfigurationPolicy\nmetadata:\n  name: bench\nspec:\n" +
		"  remediationAction: inform\n  object-templates:\n    - complianceType: musthave\n      objectDefinition:\n" +
		"        apiVersion: v1\n        kind: ConfigMap\n        metadata:\n          name: bench\n        data:\n")

	for i, c := range calls {
		fmt.Fprintf(&b, "          k%02d: '%s'\n", i, c)
	}

	return []byte(b.String())
}

// coldLoad does everything a CLI invocation does before the first document.
func coldLoad(tb testing.TB, dir string) *Engine {
	tb.Helper()

	set, err := mockdata.Load(dir)
	if err != nil {
		tb.Fatal(err)
	}

	if _, _, err := fakecluster.New(set.Managed); err != nil {
		tb.Fatal(err)
	}

	e, err := NewEngine(set, Options{Strict: true, Cohort: dir})
	if err != nil {
		tb.Fatal(err)
	}

	return e
}

func reportPercentiles(b *testing.B, durs []time.Duration) {
	b.Helper()

	if len(durs) == 0 {
		return
	}

	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })

	at := func(p float64) float64 {
		idx := min(int(p*float64(len(durs))), len(durs)-1)

		return float64(durs[idx]) / float64(time.Millisecond)
	}

	b.ReportMetric(at(0.50), "p50-ms")
	b.ReportMetric(at(0.95), "p95-ms")
}

// BenchmarkColdLoad measures mockdata.Load + fakecluster.New + NewEngine.
func BenchmarkColdLoad(b *testing.B) {
	dir := b.TempDir()
	writeBenchCohort(b, dir)

	durs := make([]time.Duration, 0, b.N)

	b.ResetTimer()

	for range b.N {
		start := time.Now()
		coldLoad(b, dir)

		durs = append(durs, time.Since(start))
	}

	b.StopTimer()
	reportPercentiles(b, durs)
}

// BenchmarkResolvePolicy measures one ResolveDocument of a ConfigurationPolicy
// with 20 template calls against an already-loaded cohort.
func BenchmarkResolvePolicy(b *testing.B) {
	dir := b.TempDir()
	writeBenchCohort(b, dir)

	e := coldLoad(b, dir)
	policy := benchPolicy()

	if _, err := e.ResolveDocument(policy, DocOptions{}); err != nil {
		b.Fatal(err)
	}

	durs := make([]time.Duration, 0, b.N)

	b.ResetTimer()

	for range b.N {
		start := time.Now()

		if _, err := e.ResolveDocument(policy, DocOptions{}); err != nil {
			b.Fatal(err)
		}

		durs = append(durs, time.Since(start))
	}

	b.StopTimer()
	reportPercentiles(b, durs)
}

// TestColdLoadCeiling fails if loading the 500-file cohort takes longer than a
// coarse ceiling. Skipped with -short.
func TestColdLoadCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("performance ceiling skipped with -short")
	}

	dir := t.TempDir()
	writeBenchCohort(t, dir)

	start := time.Now()
	e := coldLoad(t, dir)
	elapsed := time.Since(start)

	if elapsed > coldLoadCeiling {
		t.Fatalf("cold load of the 500-file cohort took %v, ceiling %v", elapsed, coldLoadCeiling)
	}

	if _, err := e.ResolveDocument(benchPolicy(), DocOptions{}); err != nil {
		t.Fatal(err)
	}

	t.Logf("cold load of 500 files: %v", elapsed)
}
