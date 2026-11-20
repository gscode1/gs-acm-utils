package mockdata

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeSecret(t *testing.T) {
	cases := []struct {
		name string
		obj map[string]any
		want map[string]any
		errKind string
		key string
	}{
		{name: "plain values and stringData override", obj: map[string]any{
			"data": map[string]any{"password": "old", "other": "hello"},
			"stringData": map[string]any{"password": "new", "unicode": "é"},
		}, want: map[string]any{"data": map[string]any{"password": "bmV3", "other": "aGVsbG8=", "unicode": "w6k="}}},
		{name: "annotated values preserved and stringData encoded", obj: map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{EncodingAnnotation: "base64"}},
			"data": map[string]any{"a": "aGVsbG8="}, "stringData": map[string]any{"b": "world"},
		}, want: map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{EncodingAnnotation: "base64"}},
			"data": map[string]any{"a": "aGVsbG8=", "b": "d29ybGQ="},
		}},
		{name: "null stringData removed", obj: map[string]any{"stringData": nil}, want: map[string]any{}},
		{name: "kubectl encoded data requires annotation", obj: map[string]any{
			"metadata": map[string]any{"resourceVersion": "1"}, "data": map[string]any{"a": "aGVsbG8="},
		}, errKind: "looks"},
		{name: "kubectl plain data encoded", obj: map[string]any{
			"metadata": map[string]any{"resourceVersion": "1"}, "data": map[string]any{"a": "not base64!"},
		}, want: map[string]any{"metadata": map[string]any{"resourceVersion": "1"}, "data": map[string]any{"a": "bm90IGJhc2U2NCE="}}},
		{name: "annotated invalid base64 sorted key", obj: map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{EncodingAnnotation: "base64"}},
			"data": map[string]any{"z": "bad!", "a": "YQ==\n"},
		}, errKind: "base64", key: "a"},
		{name: "data must be a map", obj: map[string]any{"data": "oops"}, errKind: "invalid"},
		{name: "stringData values must be strings", obj: map[string]any{"stringData": map[string]any{"a": 7}}, errKind: "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := normalizeSecret("secret.yaml", tc.obj)
			switch tc.errKind {
			case "":
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !reflect.DeepEqual(tc.obj, tc.want) {
					t.Fatalf("got %#v, want %#v", tc.obj, tc.want)
				}
			case "looks":
				var target *ErrSecretLooksEncoded
				if !errors.As(err, &target) || target.Path != "secret.yaml" {
					t.Fatalf("got %v", err)
				}
			case "base64":
				var target *ErrSecretBadBase64
				if !errors.As(err, &target) || target.Path != "secret.yaml" || target.Key != tc.key {
					t.Fatalf("got %v", err)
				}
			case "invalid":
				var target *ErrInvalidFile
				if !errors.As(err, &target) || target.Path != "secret.yaml" {
					t.Fatalf("got %v", err)
				}
			}
		})
	}
}
