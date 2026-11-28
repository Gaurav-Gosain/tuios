package lint

import (
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var updateComments = flag.Bool("update-comments", false, "rewrite the comment baseline in testdata/comments")

func commentArea(dir string) string {
	parts := strings.Split(dir, "/")
	switch {
	case dir == ".":
		return "root"
	case len(parts) >= 2 && slices.Contains([]string{"internal/app", "internal/session", "internal/input", "cmd/tuios"}, parts[0]+"/"+parts[1]):
		return parts[0] + "-" + parts[1]
	}
	return parts[0]
}

type commentFinding struct {
	at   string
	rule string
}

func scanComments(t *testing.T, root string) (map[string]int, map[string][]commentFinding) {
	t.Helper()
	counts := map[string]int{}
	found := map[string][]commentFinding{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		for _, c := range CommentsToRemove(fset, f, strings.HasSuffix(name, "_test.go")) {
			counts[dir] += c.Lines
			found[dir] = append(found[dir], commentFinding{rel + ":" + strconv.Itoa(fset.Position(c.Comment.Pos()).Line), c.Rule})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return counts, found
}

func readCommentBaseline(t *testing.T, dir string) map[string]int {
	t.Helper()
	base := map[string]int{}
	files, err := filepath.Glob(filepath.Join(dir, "*.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for n, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			pkg, num, ok := strings.Cut(line, " ")
			count, err := strconv.Atoi(num)
			if !ok || err != nil {
				t.Fatalf("%s:%d: want \"DIR COUNT\", got %q", name, n+1, line)
			}
			base[pkg] = count
		}
	}
	return base
}

func writeCommentBaseline(t *testing.T, dir string, counts map[string]int) {
	t.Helper()
	old, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	for _, name := range old {
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	areas := map[string]*strings.Builder{}
	for _, pkg := range slices.Sorted(maps.Keys(counts)) {
		if counts[pkg] == 0 {
			continue
		}
		a := commentArea(pkg)
		if areas[a] == nil {
			areas[a] = &strings.Builder{}
		}
		fmt.Fprintf(areas[a], "%s %d\n", pkg, counts[pkg])
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for a, b := range areas {
		if err := os.WriteFile(filepath.Join(dir, a+".txt"), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNoNewInlineComments holds each package to at most the comment lines in
// testdata/comments, and the count only goes down. A comment that is not a doc
// comment on exported API, a test doc comment, a directive or a marker counts
// (see CommentsToRemove). A package with fewer than its baseline fails too,
// so the change that deletes comments lowers the baseline in the same commit:
//
//	go test ./internal/lint -run TestNoNewInlineComments -update-comments
func TestNoNewInlineComments(t *testing.T) {
	root := repoRoot(t)
	baseDir := filepath.Join("testdata", "comments")
	counts, found := scanComments(t, root)
	if *updateComments {
		writeCommentBaseline(t, baseDir, counts)
		return
	}
	base := readCommentBaseline(t, baseDir)
	for _, pkg := range slices.Sorted(maps.Keys(counts)) {
		got, want := counts[pkg], base[pkg]
		if got <= want {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s: %d comment lines, the baseline allows %d. Delete the new comment, or keep only doc comments on exported API, test doc comments, directives and markers:", pkg, got, want)
		for i, c := range found[pkg] {
			if i == 40 {
				fmt.Fprintf(&b, "\n\t... and %d more", len(found[pkg])-i)
				break
			}
			fmt.Fprintf(&b, "\n\t%s: %s", c.at, c.rule)
		}
		t.Error(b.String())
	}
	for _, pkg := range slices.Sorted(maps.Keys(base)) {
		if got := counts[pkg]; got < base[pkg] {
			t.Errorf("%s: %d comment lines, below the baseline of %d. Lower the baseline: go test ./internal/lint -run TestNoNewInlineComments -update-comments", pkg, got, base[pkg])
		}
	}
}

// TestCommentRulesKeepAndRemove keeps the comment rules honest: each kind the
// rules keep stays, and each other kind is found with its rule.
func TestCommentRulesKeepAndRemove(t *testing.T) {
	src := "// Copyright 2026 the authors. License: MIT.\n\n" +
		"//go:build linux\n\n" +
		"// Package p is kept.\n" +
		"package p\n\n" +
		"// Exported is kept.\n" +
		"type Exported struct {\n" +
		"\t// Field is kept.\n" +
		"\tField int // kept\n" +
		"\t// field goes.\n" +
		"\tfield int\n" +
		"}\n\n" +
		"// hidden goes.\n" +
		"type hidden struct {\n" +
		"\t// Field goes.\n" +
		"\tField int\n" +
		"}\n\n" +
		"// Run is kept.\n" +
		"func (Exported) Run() {\n" +
		"\t// goes\n" +
		"\t_ = 1 //nolint:gosec // kept with its reason\n" +
		"\t//tuios:allow-color kept\n" +
		"}\n\n" +
		"// Run goes: hidden is not API.\n" +
		"func (hidden) Run() {}\n\n" +
		"// helper goes.\n" +
		"//\n" +
		"//go:noinline\n" +
		"func helper() {}\n\n" +
		"// a freestanding note goes.\n\n" +
		"// NEGATIVE CONTROL: kept.\n\n" +
		"// Limit is kept.\n" +
		"const Limit = 3 // kept\n\n" +
		"var x = 1 // goes\n"
	want := []string{
		"12 " + RuleNotAPI, "16 " + RuleNotAPI, "18 " + RuleNotAPI,
		"24 " + RuleInBody, "29 " + RuleNotAPI,
		"32 " + RuleNotAPI, "33 " + RuleNotAPI,
		"37 " + RuleFreestanding, "44 " + RuleNotAPI,
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range CommentsToRemove(fset, f, false) {
		got = append(got, strconv.Itoa(fset.Position(c.Comment.Pos()).Line)+" "+c.Rule)
	}
	if !slices.Equal(got, want) {
		t.Errorf("found\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}

	mainSrc := "// Command m is kept.\npackage main\n\n// Exported goes in package main.\nfunc Exported() {}\n"
	f, err = parser.ParseFile(fset, "m.go", mainSrc, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if got := CommentsToRemove(fset, f, false); len(got) != 1 {
		t.Errorf("package main: found %d comments, want 1 (the doc of Exported)", len(got))
	}

	testSrc := "package p\n\n// TestX is kept.\nfunc TestX(t *testing.T) {}\n\n" +
		"// ExampleRun is kept.\nfunc ExampleRun() {\n\t// kept\n\t// Output: 1\n}\n\n" +
		"// Exported goes in a test file.\nfunc Exported() {}\n"
	f, err = parser.ParseFile(fset, "p_test.go", testSrc, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if got := CommentsToRemove(fset, f, true); len(got) != 1 {
		t.Errorf("test file: found %d comments, want 1 (the doc of Exported)", len(got))
	}
}
