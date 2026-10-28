package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gscode1/gs-acm-utils/internal/mockdata"
)

func TestConvertFile(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "res.yaml")

	src := `---
apiVersion: v1
kind: Secret
metadata: {name: creds, namespace: ns-a, resourceVersion: "7"}
data: {password: czNjcjN0}
---
apiVersion: v1
kind: Namespace
metadata: {name: ns-a}
---
# comment-only document
---
apiVersion: cluster.open-cluster-management.io/v1alpha1
kind: ClusterClaim
metadata: {name: id.k8s.io}
spec: {value: abc}
`
	if err := os.WriteFile(in, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "cohort")

	n, err := convertFile(in, filepath.Join(out, "objects"))
	if err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}

	for _, p := range []string{
		"objects/v1/Secret/ns-a/creds.yaml",
		"objects/v1/Namespace/ns-a.yaml",
		"objects/cluster.open-cluster-management.io/v1alpha1/ClusterClaim/id.k8s.io.yaml",
	} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}

	b, _ := os.ReadFile(filepath.Join(out, "objects/v1/Secret/ns-a/creds.yaml"))
	if !strings.Contains(string(b), "gs-acm-utils.io/data-encoding: base64") {
		t.Fatalf("Secret lacks the encoding annotation:\n%s", b)
	}

	// The generated tree must pass the real loader (including the kubectl guard).
	if _, err := mockdata.Load(out); err != nil {
		t.Fatalf("generated cohort does not load: %v", err)
	}
}
