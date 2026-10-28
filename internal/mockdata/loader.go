// Package mockdata loads and validates a mock-data cohort directory (plan section 5).
// It deliberately has no dependency on the templates package.
package mockdata

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8sjson "k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/yaml"
)

// Set is a fully loaded and validated cohort.
type Set struct {
	// Managed is the managed-cluster view (objects/). Never nil.
	Managed *Index
	// Hub is the hub view (hub/objects/). Never nil; empty when HubPresent is false.
	Hub *Index
	// HubPresent reports whether hub/objects/ exists.
	HubPresent bool
	// Params is params.yaml (nil when absent).
	Params map[string]any
	// HubContext is hub/context.yaml (nil when absent).
	HubContext map[string]any
}

var versionRE = regexp.MustCompile(`^v[0-9]+((alpha|beta)[0-9]+)?$`)

var supportedExt = map[string]bool{".yaml": true, ".yml": true, ".json": true}

type loader struct {
	root string // absolute, symlinks resolved
	errs []error
}

// parsedFile is one successfully decoded object file.
type parsedFile struct {
	path       string
	apiVersion string
	kind       string
	ns         string
	name       string
	namespaced bool // layout scope
	obj        *unstructured.Unstructured
}

// Load reads and validates the cohort at root. On failure the returned error is
// a *LoadErrors holding every problem found, sorted by path.
func Load(root string) (*Set, error) {
	cohort := filepath.ToSlash(filepath.Clean(root))

	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, &LoadErrors{Errors: []error{&ErrCohortUnreadable{Cohort: cohort, Reason: err.Error()}}}
	}

	realRoot, err := filepath.EvalSymlinks(abs)
	if err == nil {
		var st os.FileInfo
		if st, err = os.Stat(realRoot); err == nil && !st.IsDir() {
			err = errors.New("not a directory")
		}
	}

	if err != nil {
		reason := err.Error()
		if errors.Is(err, fs.ErrNotExist) {
			reason = "does not exist"
		}

		return nil, &LoadErrors{Errors: []error{&ErrCohortUnreadable{Cohort: cohort, Reason: reason}}}
	}

	l := &loader{root: realRoot}
	set := &Set{}

	var seen int

	set.Managed, _, seen = l.loadTree("objects")
	if set.Managed == nil {
		set.Managed = newIndex("objects")
	}

	if seen == 0 {
		l.errs = append(l.errs, &ErrEmptyCohort{Cohort: cohort})
	}

	hub, present, _ := l.loadTree("hub/objects")
	if hub == nil {
		hub = newIndex("hub/objects")
	}

	set.Hub, set.HubPresent = hub, present

	set.Params = l.loadMap("params.yaml")

	if _, statErr := os.Stat(filepath.Join(l.root, "hub")); statErr == nil {
		set.HubContext = l.loadMap("hub/context.yaml")
	}

	if len(l.errs) > 0 {
		sortErrors(l.errs)

		return nil, &LoadErrors{Errors: l.errs}
	}

	return set, nil
}

// checkSymlink validates a symlink entry. It returns a reason, or "" when the
// link resolves to a file inside the cohort.
func (l *loader) checkSymlink(abs string) string {
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "broken symlink"
	}

	rel, err := filepath.Rel(l.root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "symlink escapes root"
	}

	if st, err := os.Stat(resolved); err != nil {
		return "broken symlink"
	} else if st.IsDir() {
		return "symlink to directory is not supported"
	}

	return ""
}

// loadMap reads an optional YAML/JSON mapping file relative to the cohort root.
func (l *loader) loadMap(rel string) map[string]any {
	abs := filepath.Join(l.root, filepath.FromSlash(rel))

	lst, err := os.Lstat(abs)
	if err != nil {
		return nil
	}

	if lst.Mode()&fs.ModeSymlink != 0 {
		if reason := l.checkSymlink(abs); reason != "" {
			l.errs = append(l.errs, &ErrInvalidFile{Path: rel, Reason: reason})

			return nil
		}
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		l.errs = append(l.errs, &ErrInvalidFile{Path: rel, Reason: "parse error: " + err.Error()})

		return nil
	}

	m, err := decodeMap(data)
	if err != nil {
		l.errs = append(l.errs, &ErrInvalidFile{Path: rel, Reason: "parse error: " + err.Error()})

		return nil
	}

	if m == nil {
		m = map[string]any{}
	}

	return m
}

// decodeMap converts YAML (or JSON) to a map using sigs.k8s.io/yaml and the
// Kubernetes JSON decoder (integers stay int64). An empty document yields nil.
func decodeMap(data []byte) (map[string]any, error) {
	js, err := yaml.YAMLToJSON(data)
	if err != nil {
		return nil, err
	}

	var m map[string]any
	if err := k8sjson.Unmarshal(js, &m); err != nil {
		return nil, errors.New("document must be a mapping")
	}

	return m, nil
}

func multiDoc(data []byte) bool {
	n := 0

	for _, doc := range strings.Split("\n"+string(data), "\n---") {
		// A segment counts if it has any line that is neither blank nor a comment.
		for _, line := range strings.Split(doc, "\n") {
			t := strings.TrimSpace(line)
			if t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "...") {
				n++

				break
			}
		}
	}

	return n > 1
}

// loadTree loads <root>/<prefix> and returns the index, whether the directory
// exists, and how many candidate files were encountered (valid or not).
func (l *loader) loadTree(prefix string) (*Index, bool, int) {
	dir := filepath.Join(l.root, filepath.FromSlash(prefix))

	lst, err := os.Lstat(dir)
	if err != nil {
		return nil, false, 0
	}

	if lst.Mode()&fs.ModeSymlink != 0 {
		l.errs = append(l.errs, &ErrInvalidFile{Path: prefix, Reason: "symlink to directory is not supported"})

		return nil, true, 1
	}

	if !lst.IsDir() {
		return nil, false, 0
	}

	var (
		files []*parsedFile
		seen  int
	)

	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)

		display := prefix
		if rel != "." {
			display = prefix + "/" + rel
		}

		if err != nil {
			l.errs = append(l.errs, &ErrInvalidFile{Path: display, Reason: "parse error: " + err.Error()})

			return nil
		}

		if rel == "." {
			return nil
		}

		if strings.HasPrefix(d.Name(), ".") {
			seen++

			l.errs = append(l.errs, &ErrInvalidFile{Path: display, Reason: "dotfile"})

			if d.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if d.IsDir() {
			return nil
		}

		seen++

		if d.Type()&fs.ModeSymlink != 0 {
			if reason := l.checkSymlink(p); reason != "" {
				l.errs = append(l.errs, &ErrInvalidFile{Path: display, Reason: reason})

				return nil
			}
		} else if !d.Type().IsRegular() {
			l.errs = append(l.errs, &ErrInvalidFile{Path: display, Reason: "not a regular file"})

			return nil
		}

		if !supportedExt[filepath.Ext(d.Name())] {
			l.errs = append(l.errs, &ErrInvalidFile{Path: display, Reason: "unsupported extension"})

			return nil
		}

		pf, err := l.parseFile(p, rel, display)
		if err != nil {
			l.errs = append(l.errs, err)

			return nil
		}

		files = append(files, pf)

		return nil
	})
	if walkErr != nil {
		l.errs = append(l.errs, &ErrInvalidFile{Path: prefix, Reason: "parse error: " + walkErr.Error()})
	}

	return l.buildIndex(prefix, files), true, seen
}

// splitLayout decodes <apiVersion>/<Kind>/[<ns>/]<file> relative to the tree root.
func splitLayout(rel string) (apiVersion, kind, ns, file string, namespaced, ok bool) {
	segs := strings.Split(rel, "/")

	i := 1
	if versionRE.MatchString(segs[0]) {
		apiVersion = segs[0]
	} else {
		if len(segs) < 2 || !versionRE.MatchString(segs[1]) {
			return "", "", "", "", false, false
		}

		apiVersion, i = segs[0]+"/"+segs[1], 2
	}

	rest := segs[i:]
	switch len(rest) {
	case 2:
		return apiVersion, rest[0], "", rest[1], false, true
	case 3:
		return apiVersion, rest[0], rest[1], rest[2], true, true
	}

	return "", "", "", "", false, false
}

func (l *loader) parseFile(abs, rel, display string) (*parsedFile, error) {
	invalid := func(reason string) error { return &ErrInvalidFile{Path: display, Reason: reason} }

	apiVersion, kind, ns, file, namespaced, ok := splitLayout(rel)
	if !ok {
		return nil, invalid("unexpected layout: want <apiVersion>/<Kind>/[<namespace>/]<name>.<yaml|yml|json>")
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, invalid("parse error: " + err.Error())
	}

	if filepath.Ext(file) != ".json" && multiDoc(data) {
		return nil, invalid("parse error: multiple YAML documents; one object per file")
	}

	m, err := decodeMap(data)
	if err != nil {
		return nil, invalid("parse error: " + err.Error())
	}

	if m == nil {
		return nil, invalid("parse error: empty document")
	}

	u := &unstructured.Unstructured{Object: m}

	bodyAV, bodyKind, bodyName, bodyNS := u.GetAPIVersion(), u.GetKind(), u.GetName(), u.GetNamespace()

	for _, f := range []struct{ name, val string }{
		{"apiVersion", bodyAV}, {"kind", bodyKind}, {"metadata.name", bodyName},
	} {
		if f.val == "" {
			return nil, &ErrMissingName{Path: display, Field: f.name}
		}
	}

	stem := strings.TrimSuffix(file, filepath.Ext(file))

	for _, f := range []struct{ name, body, dir string }{
		{"apiVersion", bodyAV, apiVersion},
		{"kind", bodyKind, kind},
		{"metadata.namespace", bodyNS, ns},
		{"metadata.name", bodyName, stem},
	} {
		if f.body == f.dir {
			continue
		}

		// A namespaced file may omit metadata.namespace; the directory supplies it.
		if f.name == "metadata.namespace" && f.body == "" {
			continue
		}

		return nil, &ErrPathMismatch{Path: display, Field: f.name, BodyValue: f.body, PathValue: f.dir}
	}

	if namespaced {
		u.SetNamespace(ns)
	}

	if kind == "Secret" && apiVersion == "v1" {
		if err := normalizeSecret(display, m); err != nil {
			return nil, err
		}
	}

	return &parsedFile{
		path: display, apiVersion: apiVersion, kind: kind, ns: ns, name: stem,
		namespaced: namespaced, obj: u,
	}, nil
}

// buildIndex enforces scope consistency (E7) and uniqueness (E8) and builds the index.
func (l *loader) buildIndex(prefix string, files []*parsedFile) *Index {
	ix := newIndex(prefix)

	sort.SliceStable(files, func(i, j int) bool { return files[i].path < files[j].path })

	type scopeRef struct {
		namespaced bool
		path       string
	}

	scopes := map[kindKey]scopeRef{}
	firstPath := map[objID]string{}

	for _, f := range files {
		kk := kindKey{f.apiVersion, f.kind}

		gv, _ := schema.ParseGroupVersion(f.apiVersion)

		ref, seen := scopes[kk]
		if !seen {
			if ns, known := knownScope(gv, f.kind); known {
				ref = scopeRef{namespaced: ns}
			} else {
				ref = scopeRef{namespaced: f.namespaced, path: f.path}
			}

			scopes[kk] = ref
		}

		if ref.namespaced != f.namespaced {
			l.errs = append(l.errs, &ErrScopeMismatch{
				Path: f.path, Kind: f.kind, Namespaced: ref.namespaced, Ref: ref.path,
			})

			continue
		}

		id := objID{f.apiVersion, f.kind, f.ns, f.name}
		if first, dup := firstPath[id]; dup {
			l.errs = append(l.errs, &ErrDuplicateObject{
				APIVersion: f.apiVersion, Kind: f.kind, Namespace: f.ns, Name: f.name,
				Path1: first, Path2: f.path,
			})

			continue
		}

		firstPath[id] = f.path

		ix.add(f.apiVersion, f.kind, f.ns, f.name, f.namespaced, f.obj, f.path)
	}

	return ix
}

type objID struct{ apiVersion, kind, ns, name string }
