package mockdata

import (
	"fmt"
	"strings"
	"unicode"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// This file holds the object-level validation shared by the directory and file
// loaders, so both formats accept and reject exactly the same objects.

// CheckParamKey reports whether k is a usable template-context key: Go
// templates only expose exported identifiers, so a key must start with an
// uppercase letter. It is the single definition used by the loaders (params.yaml,
// spec.params) and by the CLI's --params file.
func CheckParamKey(k string) error {
	if r := []rune(k); len(r) == 0 || !unicode.IsUpper(r[0]) {
		return fmt.Errorf("key %q must be an exported identifier (start with an uppercase letter)", k)
	}

	return nil
}

// paramKeyProblems returns one reason per invalid key of params, in key order.
func paramKeyProblems(params map[string]any) []string {
	var out []string

	for _, k := range sortedAnyKeys(params) {
		if err := CheckParamKey(k); err != nil {
			out = append(out, "params "+err.Error())
		}
	}

	return out
}

// wellKnownFields lists the allowed top-level fields of the kinds whose shape
// is fixed by Kubernetes, to catch misspelled fields (for example "dta:").
// Every other kind is free-form.
var wellKnownFields = map[[2]string][]string{
	{"v1", "ConfigMap"}: {"apiVersion", "kind", "metadata", "data", "binaryData", "immutable"},
	{"v1", "Secret"}:    {"apiVersion", "kind", "metadata", "data", "stringData", "type", "immutable"},
	{"v1", "Namespace"}: {"apiVersion", "kind", "metadata", "spec", "status"},
}

// unknownFieldProblem returns a reason naming the top-level fields of obj that
// the well-known kind apiVersion/kind does not have, or "". Other kinds are
// free-form.
func unknownFieldProblem(apiVersion, kind string, obj map[string]any) string {
	allowed, ok := wellKnownFields[[2]string{apiVersion, kind}]
	if !ok {
		return ""
	}

	known := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		known[a] = true
	}

	var unknown []string

	for _, k := range sortedAnyKeys(obj) {
		if !known[k] {
			unknown = append(unknown, fmt.Sprintf("%q", k))
		}
	}

	if len(unknown) == 0 {
		return ""
	}

	noun := "field"
	if len(unknown) > 1 {
		noun = "fields"
	}

	return fmt.Sprintf("unknown %s %s for %s %s (allowed: %s)",
		noun, strings.Join(unknown, ", "), apiVersion, kind, strings.Join(allowed, ", "))
}

// identityProblem returns a reason when metadata.name or metadata.namespace is
// present but not a string. YAML 1.1 turns unquoted values such as no, yes, on,
// off or 123 into bools and numbers, which would otherwise be reported as
// missing. A null value counts as not set.
func identityProblem(u *unstructured.Unstructured) string {
	md, ok := u.Object["metadata"].(map[string]any)
	if !ok {
		return ""
	}

	for _, field := range []string{"name", "namespace"} {
		v, present := md[field]
		if !present || v == nil {
			continue
		}

		if _, isStr := v.(string); isStr {
			continue
		}

		return fmt.Sprintf("metadata.%s must be a string, got %s%s", field, describeType(v), quoteHint(v))
	}

	return ""
}

func describeType(v any) string {
	switch x := v.(type) {
	case bool:
		return fmt.Sprintf("bool %v", x)
	case int, int32, int64, float32, float64:
		return fmt.Sprintf("number %v", x)
	case []any:
		return "list"
	case map[string]any:
		return "mapping"
	}

	return fmt.Sprintf("%T", v)
}

func quoteHint(v any) string {
	switch v.(type) {
	case []any, map[string]any:
		return ""
	}

	return "; quote the value"
}
