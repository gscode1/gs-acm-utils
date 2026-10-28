// Command fixture2cohort converts multi-document Kubernetes resource YAML (for
// example upstream's local_resources.yaml or kubectl/save-resources output)
// into a mock-data cohort tree:
//
//	<out>/objects/<apiVersion>/<Kind>/<namespace>/<name>.yaml   (namespaced)
//	<out>/objects/<apiVersion>/<Kind>/<name>.yaml               (cluster-scoped)
//
// An object is namespaced when it has metadata.namespace. With -hub the tree is
// written under <out>/hub/objects instead. Secrets are copied as-is, so their
// data values (already base64 in kubectl output) get the annotation
// gs-acm-utils.io/data-encoding: base64.
//
// Usage:
//
//	go run ./hack/fixture2cohort -out DIR [-hub] FILE...
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

const encodingAnnotation = "gs-acm-utils.io/data-encoding"

var docSeparator = regexp.MustCompile(`(?m)^---[ \t]*$`)

func main() {
	out := flag.String("out", "", "cohort directory to write into (required)")
	hub := flag.Bool("hub", false, "write under <out>/hub/objects instead of <out>/objects")
	flag.Parse()

	if *out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: fixture2cohort -out DIR [-hub] FILE...")
		os.Exit(3)
	}

	root := filepath.Join(*out, "objects")
	if *hub {
		root = filepath.Join(*out, "hub", "objects")
	}

	n := 0

	for _, f := range flag.Args() {
		c, err := convertFile(f, root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fixture2cohort: %s: %v\n", f, err)
			os.Exit(1)
		}

		n += c
	}

	fmt.Printf("wrote %d objects under %s\n", n, root)
}

func convertFile(path, root string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	n := 0

	for _, doc := range docSeparator.Split(string(data), -1) {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			return n, err
		}

		if len(obj) == 0 {
			continue
		}

		if err := writeObject(root, obj); err != nil {
			return n, err
		}

		n++
	}

	return n, nil
}

func writeObject(root string, obj map[string]any) error {
	apiVersion, _ := obj["apiVersion"].(string)
	kind, _ := obj["kind"].(string)
	meta, _ := obj["metadata"].(map[string]any)
	name, _ := meta["name"].(string)
	ns, _ := meta["namespace"].(string)

	if apiVersion == "" || kind == "" || name == "" {
		return fmt.Errorf("object without apiVersion/kind/metadata.name: %v", obj)
	}

	if kind == "Secret" && apiVersion == "v1" {
		if _, ok := obj["data"]; ok {
			ann, _ := meta["annotations"].(map[string]any)
			if ann == nil {
				ann = map[string]any{}
			}

			ann[encodingAnnotation] = "base64"
			meta["annotations"] = ann
		}
	}

	parts := []string{root, filepath.FromSlash(apiVersion), kind}
	if ns != "" {
		parts = append(parts, ns)
	}

	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	b, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(dir, strings.TrimSpace(name)+".yaml"), b, 0o644)
}
