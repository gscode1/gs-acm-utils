package mockdata

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Identity of the consolidated mock-data file format.
const (
	FileAPIVersion = "acm-utils.gskode.com/v1alpha1"
	FileKind       = "MockData"
)

const (
	viewManaged = "managed"
	viewHub     = "hub"
)

// LoadFile reads and validates one consolidated mock-data file (a single
// YAML/JSON MockData document holding one resolver context) and returns the
// same Set that the equivalent cohort directory would. The loader synthesizes
// nothing: Namespace objects are derived downstream from Index.Namespaces(),
// exactly as for directories.
//
// Unlike directory files, the document is decoded with duplicate-key
// detection, so a repeated mapping key anywhere in the file is an error.
//
// Every failure, including an unreadable file, is returned as a *LoadErrors
// holding all problems found, sorted by position.
func LoadFile(path string) (*Set, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		reason := err.Error()

		var pe *fs.PathError
		if errors.As(err, &pe) {
			reason = pe.Err.Error()
		}

		if errors.Is(err, fs.ErrNotExist) {
			reason = "does not exist"
		}

		return nil, &LoadErrors{Errors: []error{&ErrCohortUnreadable{Cohort: path, Reason: reason, File: true}}}
	}

	fl := &fileLoader{file: path}
	set := fl.load(data)

	if len(fl.errs) > 0 {
		sortErrors(fl.errs)

		return nil, &LoadErrors{Errors: fl.errs}
	}

	return set, nil
}

type fileLoader struct {
	file string
	errs []error
}

// loc builds a location. section orders problems: 0 document/spec, 1 managed, 2 hub.
func (fl *fileLoader) loc(path string, section, index int) *FileLoc {
	return &FileLoc{File: fl.file, Path: path, section: section, index: index}
}

func (fl *fileLoader) invalid(path, reason string) {
	fl.errs = append(fl.errs, &ErrInvalidFile{Loc: fl.loc(path, 0, -1), Reason: reason})
}

func (fl *fileLoader) missing(path, field string) {
	fl.errs = append(fl.errs, &ErrMissingName{Loc: fl.loc(path, 0, -1), Field: field})
}

func (fl *fileLoader) unknownFields(path string, m map[string]any, allowed ...string) {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}

	for _, k := range sortedAnyKeys(m) {
		if !ok[k] {
			fl.invalid(path, fmt.Sprintf("unknown field %q", k))
		}
	}
}

func (fl *fileLoader) load(data []byte) *Set {
	if filepath.Ext(fl.file) != ".json" && multiDoc(data) {
		fl.invalid("", "parse error: multiple YAML documents; exactly one MockData document is required")

		return nil
	}

	doc, err := decodeMapMode(data, true)
	if err != nil {
		fl.invalid("", "parse error: "+err.Error())

		return nil
	}

	if doc == nil {
		fl.invalid("", "parse error: empty document")

		return nil
	}

	fl.unknownFields("", doc, "apiVersion", "kind", "metadata", "spec")

	switch v := doc["apiVersion"]; {
	case v == nil || v == "":
		fl.missing("", "apiVersion")
	case v != FileAPIVersion:
		fl.invalid("", fmt.Sprintf("unsupported apiVersion %v (want %s)", quoteAny(v), FileAPIVersion))
	}

	switch v := doc["kind"]; {
	case v == nil || v == "":
		fl.missing("", "kind")
	case v != FileKind:
		fl.invalid("", fmt.Sprintf("unsupported kind %v (want %s)", quoteAny(v), FileKind))
	}

	fl.checkMetadata(doc["metadata"])

	rawSpec := doc["spec"]
	if rawSpec == nil {
		fl.missing("", "spec")

		return nil
	}

	spec, ok := rawSpec.(map[string]any)
	if !ok {
		fl.invalid("spec", "must be a mapping")

		return nil
	}

	fl.unknownFields("spec", spec, "managed", "hub", "hubContext", "params")

	set := &Set{}

	var managedCount, hubCount int

	set.Managed, _, managedCount = fl.loadView(spec, viewManaged, 1)
	set.Hub, set.HubPresent, hubCount = fl.loadView(spec, viewHub, 2)

	// Hub-only is valid: managed may be empty when hub holds objects. A view
	// that is not a list (-1) already reported its own error; no extra E15.
	if managedCount == 0 && hubCount == 0 {
		fl.errs = append(fl.errs, &ErrEmptyCohort{Cohort: fl.file, Loc: fl.loc("spec", 0, -1)})
	}

	set.HubContext = fl.freeMap(spec, "hubContext")
	set.Params = fl.freeMap(spec, "params")

	for _, reason := range paramKeyProblems(set.Params) {
		fl.invalid("spec.params", reason)
	}

	return set
}

func quoteAny(v any) string {
	if s, ok := v.(string); ok {
		return fmt.Sprintf("%q", s)
	}

	return fmt.Sprintf("%v", v)
}

func (fl *fileLoader) checkMetadata(raw any) {
	if raw == nil {
		return
	}

	md, ok := raw.(map[string]any)
	if !ok {
		fl.invalid("metadata", "must be a mapping")

		return
	}

	fl.unknownFields("metadata", md, "name")

	if name, present := md["name"]; present {
		if _, isStr := name.(string); !isStr {
			fl.invalid("metadata.name", "must be a string")
		}
	}
}

// freeMap reads an optional free-form mapping (hubContext, params) exactly like
// the directory loader's loadMap: absent or null is nil, {} is a non-nil empty map.
func (fl *fileLoader) freeMap(spec map[string]any, key string) map[string]any {
	raw := spec[key]
	if raw == nil {
		return nil
	}

	m, ok := raw.(map[string]any)
	if !ok {
		fl.invalid("spec."+key, "must be a mapping")

		return nil
	}

	if m == nil {
		m = map[string]any{}
	}

	return m
}

// fileObj is one validated list item.
type fileObj struct {
	loc        *FileLoc
	apiVersion string
	kind       string
	ns         string
	name       string
	obj        *unstructured.Unstructured
	index      int
}

// loadView loads spec.<view>. It returns the index (never nil), whether the key
// is present (and not null), and how many items the list holds (-1 when it is not a list).
func (fl *fileLoader) loadView(spec map[string]any, view string, section int) (*Index, bool, int) {
	ix := newIndex(fileSource{file: fl.file, view: "spec." + view})

	raw := spec[view]
	if raw == nil {
		return ix, false, 0
	}

	listPath := "spec." + view

	list, ok := raw.([]any)
	if !ok {
		fl.errs = append(fl.errs, &ErrInvalidFile{Loc: fl.loc(listPath, section, -1), Reason: "must be a list"})

		return ix, true, -1
	}

	var items []*fileObj

	for i, item := range list {
		loc := fl.loc(fmt.Sprintf("%s[%d]", listPath, i), section, i)

		if fo := fl.parseItem(loc, item); fo != nil {
			fo.index = i
			items = append(items, fo)
		}
	}

	fl.buildIndex(ix, items)

	return ix, true, len(list)
}

func (fl *fileLoader) parseItem(loc *FileLoc, item any) *fileObj {
	m, ok := item.(map[string]any)
	if !ok || m == nil {
		fl.errs = append(fl.errs, &ErrInvalidFile{Loc: loc, Reason: "must be a mapping"})

		return nil
	}

	u := &unstructured.Unstructured{Object: m}

	if reason := identityProblem(u); reason != "" {
		fl.errs = append(fl.errs, &ErrInvalidFile{Loc: loc, Reason: reason})

		return nil
	}

	apiVersion, kind, name, ns, missing := requiredFields(u)
	if missing != "" {
		fl.errs = append(fl.errs, &ErrMissingName{Loc: loc, Field: missing})

		return nil
	}

	loc.Ident = identString(apiVersion, kind, ns, name)

	if reason := unknownFieldProblem(apiVersion, kind, m); reason != "" {
		fl.errs = append(fl.errs, &ErrInvalidFile{Loc: loc, Reason: reason})

		return nil
	}

	if err := normalizeIfSecret(loc.File+": "+loc.Path, apiVersion, kind, m); err != nil {
		fl.errs = append(fl.errs, attachLoc(err, loc))

		return nil
	}

	return &fileObj{loc: loc, apiVersion: apiVersion, kind: kind, ns: ns, name: name, obj: u}
}

// attachLoc rewrites a Secret-normalization error so it reports the YAML
// location instead of a file path.
func attachLoc(err error, loc *FileLoc) error {
	switch e := err.(type) {
	case *ErrInvalidFile:
		return &ErrInvalidFile{Loc: loc, Reason: e.Reason}
	case *ErrSecretLooksEncoded:
		return &ErrSecretLooksEncoded{Loc: loc}
	case *ErrSecretBadBase64:
		return &ErrSecretBadBase64{Loc: loc, Key: e.Key}
	}

	return err
}

// buildIndex enforces scope consistency (E7) and uniqueness (E8) within one
// view and fills the index. Managed and hub are independent views.
func (fl *fileLoader) buildIndex(ix *Index, items []*fileObj) {
	type scopeRef struct {
		namespaced bool
		path       string // locator of the item that set the scope (unknown Kinds only)
	}

	scopes := map[kindKey]scopeRef{}
	first := map[objID]string{}

	for _, f := range items {
		namespaced := f.ns != ""
		kk := kindKey{f.apiVersion, f.kind}

		ref, seen := scopes[kk]
		if !seen {
			gv, _ := schema.ParseGroupVersion(f.apiVersion)
			if known, ok := knownScope(gv, f.kind); ok {
				ref = scopeRef{namespaced: known}
			} else {
				ref = scopeRef{namespaced: namespaced, path: f.loc.Path}
			}

			scopes[kk] = ref
		}

		if ref.namespaced != namespaced {
			loc := fl.loc(f.loc.Path, f.loc.section, f.index)
			loc.Ident = f.loc.Ident

			fl.errs = append(fl.errs, &ErrScopeMismatch{
				Loc: loc, Kind: f.kind, Namespaced: ref.namespaced, Ref: ref.path,
			})

			continue
		}

		id := objID{f.apiVersion, f.kind, f.ns, f.name}
		if prev, dup := first[id]; dup {
			fl.errs = append(fl.errs, &ErrDuplicateObject{
				APIVersion: f.apiVersion, Kind: f.kind, Namespace: f.ns, Name: f.name,
				Path1: prev, Path2: f.loc.Path,
				Loc: fl.loc(f.loc.Path, f.loc.section, f.index),
			})

			continue
		}

		first[id] = f.loc.Path

		ix.add(f.apiVersion, f.kind, f.ns, f.name, namespaced, f.obj, f.loc.String(), f.index)
	}
}
