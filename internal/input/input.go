// Package input collects policy input for the CLI: files, directory trees and
// stdin, split into individual YAML documents.
package input

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// StdinName is the display name used for documents read from standard input.
const StdinName = "<stdin>"

// Doc is one non-empty YAML document of an input source.
type Doc struct {
	// Index is the 1-based position among the non-empty documents of the source.
	Index int
	Data  []byte
}

// Source is one input file (or stdin) split into its non-empty documents.
type Source struct {
	// Path is the file path as discovered (or StdinName).
	Path string
	Docs []Doc
}

// Multi reports whether the source holds more than one document, in which case
// messages should carry the document index.
func (s *Source) Multi() bool { return len(s.Docs) > 1 }

// Label returns the message prefix for document d: "path" for single-document
// sources and "path#N" (N is 1-based) for multi-document ones.
func (s *Source) Label(d Doc) string {
	if s.Multi() {
		return fmt.Sprintf("%s#%d", s.Path, d.Index)
	}

	return s.Path
}

// ErrNoInput is returned when the given paths contain no YAML files.
var ErrNoInput = errors.New("no input files found (looking for .yaml/.yml files)")

// Split cuts a multi-document YAML stream at document separator lines ("---",
// optionally followed by a comment or by content) and drops documents that hold
// nothing but blank lines and comments. A leading "---" yields no empty first
// document.
func Split(data []byte) [][]byte {
	var (
		docs [][]byte
		cur  bytes.Buffer
	)

	flush := func() {
		if hasContent(cur.Bytes()) {
			docs = append(docs, bytes.Clone(cur.Bytes()))
		}

		cur.Reset()
	}

	for _, line := range strings.SplitAfter(string(data), "\n") {
		if line == "" {
			continue
		}

		if rest, ok := separator(line); ok {
			flush()
			// Content on the separator line ("--- !tag" or "--- key: v") belongs to the new document.
			cur.WriteString(rest)

			continue
		}

		cur.WriteString(line)
	}

	flush()

	return docs
}

// separator reports whether line is a document start marker and returns any
// same-line content that follows it (comments are dropped).
func separator(line string) (string, bool) {
	l := strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(l, "---") {
		return "", false
	}

	rest := l[3:]
	if rest == "" {
		return "", true
	}

	if rest[0] != ' ' && rest[0] != '\t' {
		return "", false
	}

	rest = strings.TrimSpace(rest)
	if rest == "" || strings.HasPrefix(rest, "#") {
		return "", true
	}

	return rest + "\n", true
}

func hasContent(b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			return true
		}
	}

	return false
}

func newSource(path string, data []byte) *Source {
	s := &Source{Path: path}

	for i, d := range Split(data) {
		s.Docs = append(s.Docs, Doc{Index: i + 1, Data: d})
	}

	return s
}

// FromReader reads all of r (typically stdin) as a Source named name.
func FromReader(r io.Reader, name string) (*Source, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", name, err)
	}

	return newSource(name, data), nil
}

// FromFile reads one file as a Source.
func FromFile(path string) (*Source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, unwrapPathError(err)
	}

	return newSource(path, data), nil
}

// Collect expands files and directories into Sources. Directories are walked
// recursively for .yaml/.yml files in lexical order; dotfiles and dot-directories
// below a given directory are skipped. A file named explicitly is always read.
// Files without any document (empty or comments only) yield a Source with no Docs.
// It returns ErrNoInput when nothing was found and a path error for an
// unreadable or nonexistent path.
func Collect(paths []string) ([]*Source, error) {
	var files []string

	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, unwrapPathError(err)
		}

		if !st.IsDir() {
			files = append(files, p)

			continue
		}

		found, err := walk(p)
		if err != nil {
			return nil, err
		}

		files = append(files, found...)
	}

	if len(files) == 0 {
		return nil, ErrNoInput
	}

	sources := make([]*Source, 0, len(files))

	for _, f := range files {
		s, err := FromFile(f)
		if err != nil {
			return nil, err
		}

		sources = append(sources, s)
	}

	return sources, nil
}

func walk(root string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}

			return nil
		}

		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		// Follow symlinks to files; ignore links to directories and broken links.
		if d.Type()&fs.ModeSymlink != 0 {
			if st, err := os.Stat(path); err != nil || !st.Mode().IsRegular() {
				return nil
			}
		}

		files = append(files, path)

		return nil
	})
	if err != nil {
		return nil, unwrapPathError(err)
	}

	sort.Strings(files)

	return files, nil
}

// unwrapPathError turns *fs.PathError into "path: reason" without the operation
// name, e.g. "x.yaml: no such file or directory".
func unwrapPathError(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", pe.Path, pe.Err)
	}

	return err
}
