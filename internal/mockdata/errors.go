package mockdata

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Typed errors for every row (E1-E15) of the error catalog (plan section 7).
// All are returned as pointers so callers can use errors.As with *ErrXxx targets.

const msgPrefix = "mock data: "

func refSuffix(ref string) string {
	if ref == "" {
		return ""
	}

	return " (referenced by " + ref + ")"
}

// ErrNamespaceDirMissing (E1): the namespace directory for a namespaced Kind does not exist.
type ErrNamespaceDirMissing struct {
	// Dir is the missing directory relative to the cohort root, with a trailing slash.
	Dir string
	// Ref optionally names the referencing template call, e.g. `fromConfigMap "ns-a" "my-config" "key"`.
	Ref string
}

func (e *ErrNamespaceDirMissing) Error() string {
	return msgPrefix + "namespace directory missing: " + e.Dir + refSuffix(e.Ref)
}

// ErrObjectMissing (E2): the object file does not exist.
type ErrObjectMissing struct {
	Path string
	Ref  string
}

func (e *ErrObjectMissing) Error() string {
	return msgPrefix + "object not found: " + e.Path + refSuffix(e.Ref)
}

// ErrKeyMissing (E3): the key is not present in the object's data section.
type ErrKeyMissing struct {
	Path string
	Key  string
	// Field is the section searched ("data" by default).
	Field string
	// Available are the sorted keys that do exist.
	Available []string
}

func (e *ErrKeyMissing) Error() string {
	field := e.Field
	if field == "" {
		field = "data"
	}

	have := "(none)"
	if len(e.Available) > 0 {
		have = strings.Join(e.Available, ", ")
	}

	return fmt.Sprintf("%skey %q not found in %s (%s has: %s)", msgPrefix, e.Key, e.Path, field, have)
}

// ErrLookupUnsupported (E4): no files exist for the requested apiVersion/Kind.
type ErrLookupUnsupported struct {
	APIVersion string
	Kind       string
	// Dir is the directory that would hold the files, relative to the cohort root, with trailing slash.
	Dir string
}

func (e *ErrLookupUnsupported) Error() string {
	return fmt.Sprintf("%slookup not supported for apiVersion=%s, Kind=%s (no files under %s)",
		msgPrefix, e.APIVersion, e.Kind, e.Dir)
}

// ErrMissingName (E5): a required identity field is absent. Field defaults to
// "metadata.name"; the loader also uses it for a missing apiVersion or kind.
type ErrMissingName struct {
	Path  string
	Field string
}

func (e *ErrMissingName) Error() string {
	field := e.Field
	if field == "" {
		field = "metadata.name"
	}

	return fmt.Sprintf("%sfile %s: missing %s", msgPrefix, e.Path, field)
}

// ErrPathMismatch (E6): a path segment disagrees with the file body.
type ErrPathMismatch struct {
	Path      string
	Field     string
	BodyValue string
	PathValue string
}

func (e *ErrPathMismatch) Error() string {
	return fmt.Sprintf("%sfile %s: %s is %q but directory says %q", msgPrefix, e.Path, e.Field, e.BodyValue, e.PathValue)
}

// ErrScopeMismatch (E7): the file layout contradicts the Kind's scope.
type ErrScopeMismatch struct {
	Path string
	Kind string
	// Namespaced is the Kind's scope (true: namespaced, false: cluster-scoped).
	Namespaced bool
	// Ref is the file that established the scope for a Kind not in the built-in table.
	Ref string
}

func (e *ErrScopeMismatch) Error() string {
	detail := "is cluster-scoped but file is under a namespace directory"
	if e.Namespaced {
		detail = "is namespaced but file is not under a namespace directory"
	}

	ref := ""
	if e.Ref != "" {
		ref = " (scope set by " + e.Ref + ")"
	}

	return fmt.Sprintf("%sfile %s: Kind %s %s%s", msgPrefix, e.Path, e.Kind, detail, ref)
}

// ErrDuplicateObject (E8): the same object identity is defined by two files.
type ErrDuplicateObject struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	Path1      string
	Path2      string
}

func (e *ErrDuplicateObject) Error() string {
	id := e.Name
	if e.Namespace != "" {
		id = e.Namespace + "/" + e.Name
	}

	return fmt.Sprintf("%sduplicate object %s/%s %s in %s and %s",
		msgPrefix, e.APIVersion, e.Kind, id, e.Path1, e.Path2)
}

// ErrInvalidFile (E9): the file (or directory entry) cannot be used.
type ErrInvalidFile struct {
	Path string
	// Reason is one of: dotfile, broken symlink, unsupported extension,
	// symlink escapes root, or "parse error: ..." (plus a few layout reasons).
	Reason string
}

func (e *ErrInvalidFile) Error() string {
	return fmt.Sprintf("%sfile %s: %s", msgPrefix, e.Path, e.Reason)
}

// ErrSecretLooksEncoded (E10): a Secret looks like unmodified kubectl output.
type ErrSecretLooksEncoded struct{ Path string }

func (e *ErrSecretLooksEncoded) Error() string {
	return fmt.Sprintf("%sfile %s: Secret data looks base64-encoded (kubectl output?); "+
		"decode values or add annotation gs-acm-utils.io/data-encoding: base64", msgPrefix, e.Path)
}

// ErrSecretBadBase64 (E11): data.<key> is not valid base64 although the annotation promises it.
type ErrSecretBadBase64 struct {
	Path string
	Key  string
}

func (e *ErrSecretBadBase64) Error() string {
	return fmt.Sprintf("%sfile %s: data.%s is not valid base64 (annotation data-encoding: base64 is set)",
		msgPrefix, e.Path, e.Key)
}

// ErrHubDataMissing (E12): hub templates present but the cohort has no hub/objects/.
type ErrHubDataMissing struct{ Cohort string }

func (e *ErrHubDataMissing) Error() string {
	return fmt.Sprintf("%sinput contains {{hub templates but %s/hub/objects/ does not exist "+
		"(use --hub-mode=passthrough to skip)", msgPrefix, e.Cohort)
}

// ErrUnresolvedHub (E13): a hub template survived the hub pass.
type ErrUnresolvedHub struct{}

func (e *ErrUnresolvedHub) Error() string { return "unresolved hub template remains after hub pass" }

// ErrEncryptionUnsupported (E14): the protect function is disabled in mock mode.
type ErrEncryptionUnsupported struct{}

func (e *ErrEncryptionUnsupported) Error() string {
	return `function "protect" is not available in mock mode: encryption is not supported`
}

// ErrEmptyCohort (E15): objects/ is missing or contains no files.
type ErrEmptyCohort struct{ Cohort string }

func (e *ErrEmptyCohort) Error() string {
	return fmt.Sprintf("%sno objects found under %s/objects/", msgPrefix, e.Cohort)
}

// ErrCohortUnreadable is not a catalog row: the cohort root itself cannot be read.
type ErrCohortUnreadable struct {
	Cohort string
	Reason string
}

func (e *ErrCohortUnreadable) Error() string {
	return fmt.Sprintf("%scohort directory %s: %s", msgPrefix, e.Cohort, e.Reason)
}

// WithReference returns a copy of an E1/E2 error annotated with the referencing
// template call (for example `fromConfigMap "ns-a" "my-config" "key"`). Other
// errors are returned unchanged.
func WithReference(err error, ref string) error {
	var e1 *ErrNamespaceDirMissing
	if errors.As(err, &e1) {
		c := *e1
		c.Ref = ref

		return &c
	}

	var e2 *ErrObjectMissing
	if errors.As(err, &e2) {
		c := *e2
		c.Ref = ref

		return &c
	}

	return err
}

// LoadErrors aggregates every problem found while loading a cohort, sorted
// deterministically by path.
type LoadErrors struct {
	Errors []error
}

func (e *LoadErrors) Error() string {
	lines := make([]string, len(e.Errors))
	for i, err := range e.Errors {
		lines[i] = err.Error()
	}

	return strings.Join(lines, "\n")
}

// Unwrap exposes the individual errors to errors.Is / errors.As.
func (e *LoadErrors) Unwrap() []error { return e.Errors }

// errSortPath returns the cohort-relative path an error is about.
func errSortPath(err error) string {
	switch e := err.(type) {
	case *ErrMissingName:
		return e.Path
	case *ErrPathMismatch:
		return e.Path
	case *ErrScopeMismatch:
		return e.Path
	case *ErrDuplicateObject:
		return e.Path2
	case *ErrInvalidFile:
		return e.Path
	case *ErrSecretLooksEncoded:
		return e.Path
	case *ErrSecretBadBase64:
		return e.Path
	case *ErrEmptyCohort:
		return "objects"
	}

	return ""
}

func sortErrors(errs []error) {
	sort.SliceStable(errs, func(i, j int) bool {
		pi, pj := errSortPath(errs[i]), errSortPath(errs[j])
		if pi != pj {
			return pi < pj
		}

		return errs[i].Error() < errs[j].Error()
	})
}
