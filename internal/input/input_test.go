package input

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := map[string]struct {
		in   string
		want []string
	}{
		"single":                 {"a: 1\n", []string{"a: 1\n"}},
		"leading separator":      {"---\na: 1\n", []string{"a: 1\n"}},
		"two docs":               {"a: 1\n---\nb: 2\n", []string{"a: 1\n", "b: 2\n"}},
		"trailing separator":     {"a: 1\n---\n", []string{"a: 1\n"}},
		"comment only doc":       {"a: 1\n---\n# nothing\n\n---\nb: 2\n", []string{"a: 1\n", "b: 2\n"}},
		"empty input":            {"", nil},
		"only separators":        {"---\n---\n", nil},
		"separator w/ comment":   {"a: 1\n--- # next\nb: 2\n", []string{"a: 1\n", "b: 2\n"}},
		"separator w/ content":   {"a: 1\n--- b: 2\n", []string{"a: 1\n", "b: 2\n"}},
		"crlf":                   {"a: 1\r\n---\r\nb: 2\r\n", []string{"a: 1\r\n", "b: 2\r\n"}},
		"dashes in value":        {"a: ---\nb: '--- x'\n", []string{"a: ---\nb: '--- x'\n"}},
		"key starting w/ dashes": {"---x: 1\n", []string{"---x: 1\n"}},
		"no trailing newline":    {"a: 1\n---\nb: 2", []string{"a: 1\n", "b: 2"}},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var got []string
			for _, d := range Split([]byte(c.in)) {
				got = append(got, string(d))
			}

			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollect(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "b.yaml"), "a: 1\n---\nb: 2\n")
	write(t, filepath.Join(dir, "a.yml"), "a: 1\n")
	write(t, filepath.Join(dir, "sub", "c.yaml"), "c: 1\n")
	write(t, filepath.Join(dir, "notes.txt"), "x")
	write(t, filepath.Join(dir, ".hidden.yaml"), "x: 1\n")
	write(t, filepath.Join(dir, ".git", "d.yaml"), "x: 1\n")

	srcs, err := Collect([]string{dir})
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, s := range srcs {
		names = append(names, strings.TrimPrefix(s.Path, dir+string(filepath.Separator)))
	}

	want := []string{"a.yml", "b.yaml", filepath.Join("sub", "c.yaml")}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v want %v", names, want)
	}

	if !srcs[1].Multi() || srcs[0].Multi() {
		t.Fatal("Multi mismatch")
	}

	if got := srcs[1].Label(srcs[1].Docs[1]); !strings.HasSuffix(got, "b.yaml#2") {
		t.Fatalf("label %q", got)
	}
}

func TestCollectErrors(t *testing.T) {
	dir := t.TempDir()

	if _, err := Collect([]string{dir}); !errors.Is(err, ErrNoInput) {
		t.Fatalf("got %v", err)
	}

	if _, err := Collect([]string{filepath.Join(dir, "missing.yaml")}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}

	// an explicitly named file is read whatever its extension
	write(t, filepath.Join(dir, "p.txt"), "a: 1\n")

	if srcs, err := Collect([]string{filepath.Join(dir, "p.txt")}); err != nil || len(srcs) != 1 {
		t.Fatalf("%v %v", srcs, err)
	}
}

func TestFromReader(t *testing.T) {
	s, err := FromReader(strings.NewReader("---\na: 1\n---\nb: 2\n"), StdinName)
	if err != nil || len(s.Docs) != 2 || s.Docs[1].Index != 2 {
		t.Fatalf("%+v %v", s, err)
	}
}
