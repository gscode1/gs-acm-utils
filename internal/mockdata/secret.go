package mockdata

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// EncodingAnnotation marks a Secret whose data values are already base64
// (for example a file copied from `kubectl get secret -o yaml`).
const EncodingAnnotation = "gs-acm-utils.io/data-encoding"

// normalizeSecret rewrites obj in place so that data holds base64 values, as on
// a live cluster, and stringData is removed (plan section 5.3).
func normalizeSecret(path string, obj map[string]any) error {
	u := &unstructured.Unstructured{Object: obj}

	data, err := stringMap(path, obj, "data")
	if err != nil {
		return err
	}

	stringData, err := stringMap(path, obj, "stringData")
	if err != nil {
		return err
	}

	switch {
	case u.GetAnnotations()[EncodingAnnotation] == "base64":
		for _, k := range sortedKeys(data) {
			if !strictBase64(data[k]) {
				return &ErrSecretBadBase64{Path: path, Key: k}
			}
		}
	case len(data) > 0 && looksLikeKubectlOutput(u) && allBase64(data):
		return &ErrSecretLooksEncoded{Path: path}
	default:
		for k, v := range data {
			data[k] = base64.StdEncoding.EncodeToString([]byte(v))
		}
	}

	for k, v := range stringData { // stringData wins over data (Kubernetes semantics)
		data[k] = base64.StdEncoding.EncodeToString([]byte(v))
	}

	delete(obj, "stringData")

	if len(data) > 0 {
		out := make(map[string]any, len(data))
		for k, v := range data {
			out[k] = v
		}

		obj["data"] = out
	}

	return nil
}

func looksLikeKubectlOutput(u *unstructured.Unstructured) bool {
	if u.GetResourceVersion() != "" || string(u.GetUID()) != "" {
		return true
	}

	return len(u.GetManagedFields()) > 0
}

func allBase64(m map[string]string) bool {
	for _, v := range m {
		if !strictBase64(v) {
			return false
		}
	}

	return true
}

func strictBase64(s string) bool {
	if strings.ContainsAny(s, " \t\r\n") {
		return false
	}

	_, err := base64.StdEncoding.Strict().DecodeString(s)

	return err == nil
}

// stringMap reads obj[field] as map[string]string; absent or null yields an empty map.
func stringMap(path string, obj map[string]any, field string) (map[string]string, error) {
	raw, ok := obj[field]
	if !ok || raw == nil {
		return map[string]string{}, nil
	}

	m, ok := raw.(map[string]any)
	if !ok {
		return nil, &ErrInvalidFile{Path: path, Reason: field + " must be a map"}
	}

	out := make(map[string]string, len(m))

	for _, k := range sortedAnyKeys(m) {
		s, ok := m[k].(string)
		if !ok {
			return nil, &ErrInvalidFile{Path: path, Reason: fmt.Sprintf("%s.%s must be a string", field, k)}
		}

		out[k] = s
	}

	return out, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}
