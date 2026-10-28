package resolve

import (
	"fmt"
	"reflect"
	"slices"
	"unicode"
)

// Context key names, identical to upstream's templates.TemplateContext and the
// template-resolver CLI's hub context.
const (
	keyObjectNamespace      = "ObjectNamespace"
	keyObjectName           = "ObjectName"
	keyManagedClusterName   = "ManagedClusterName"
	keyManagedClusterLabels = "ManagedClusterLabels"
	keyPolicyMetadata       = "PolicyMetadata"
)

// buildContext turns a key->value map into the struct context that
// go-template-utils requires (its getValidContext rejects a top-level map, so a
// map[string]any cannot be passed directly). Every key becomes an exported
// field of an anonymous struct, so templates read it as .Key. Keys must
// therefore be valid exported Go identifiers (start with an uppercase letter).
func buildContext(values map[string]any) (any, error) {
	names := make([]string, 0, len(values))
	for k := range values {
		if !validContextKey(k) {
			return nil, fmt.Errorf("invalid template context key %q: keys must be exported identifiers "+
				"(start with an uppercase letter, then letters, digits or underscores)", k)
		}

		names = append(names, k)
	}

	slices.Sort(names)

	anyType := reflect.TypeFor[any]()
	fields := make([]reflect.StructField, len(names))

	for i, n := range names {
		fields[i] = reflect.StructField{Name: n, Type: anyType}
	}

	v := reflect.New(reflect.StructOf(fields)).Elem()

	for i, n := range names {
		if val := values[n]; val != nil {
			v.Field(i).Set(reflect.ValueOf(val))
		}
	}

	return v.Interface(), nil
}

func validContextKey(k string) bool {
	if k == "" {
		return false
	}

	for i, r := range k {
		switch {
		case i == 0 && !unicode.IsUpper(r):
			return false
		case i > 0 && r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r):
			return false
		}
	}

	return true
}

// deepCopyValue copies maps and slices so a template function that mutates its
// context (sprig set/unset) cannot leak into other documents.
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopyValue(e)
		}

		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}

		return out
	default:
		return v
	}
}

// mergeParams returns a deep copy of base overlaid with over (over wins).
func mergeParams(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over)+2)

	for _, m := range []map[string]any{base, over} {
		for k, v := range m {
			out[k] = deepCopyValue(v)
		}
	}

	return out
}

// managedContext builds the managed-pass context: params (cohort params.yaml,
// then DocOptions.Params) with ObjectNamespace/ObjectName on top; non-empty
// flag values win over params. Both keys are always present (empty by default),
// like upstream's TemplateContext.
func (e *Engine) managedContext(o *DocOptions) (any, error) {
	values := mergeParams(e.params, o.Params)

	if _, ok := values[keyObjectNamespace]; !ok || o.ObjectNamespace != "" {
		values[keyObjectNamespace] = o.ObjectNamespace
	}

	if _, ok := values[keyObjectName]; !ok || o.ObjectName != "" {
		values[keyObjectName] = o.ObjectName
	}

	return buildContext(values)
}
