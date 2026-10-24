package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

func parseAt(root, rev, p string) (*token.FileSet, *ast.File, error) {
	src, err := git(root, "show", rev+":"+p)
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
	return fset, f, err
}

// funcKey names a function the same way in both versions of a file: the
// receiver's type, if any, and the name.
func funcKey(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	t := fn.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
			continue
		case *ast.IndexExpr:
			t = x.X
			continue
		case *ast.IndexListExpr:
			t = x.X
			continue
		}
		break
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return "?." + fn.Name.Name
}

// span is the lines strictly between a function's braces.
type span struct {
	key    string
	name   string
	lb, rb int // the lines of the opening and the closing brace
}

func bodies(fset *token.FileSet, f *ast.File) []span {
	var out []span
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		out = append(out, span{funcKey(fn), fn.Name.Name, fset.Position(fn.Body.Lbrace).Line, fset.Position(fn.Body.Rbrace).Line})
	}
	return out
}

// within returns the function whose body holds lines from to to, or, for a
// length of 0, the gap after line from.
func within(spans []span, from, length int) (span, bool) {
	for _, s := range spans {
		if length == 0 {
			if from >= s.lb && from < s.rb {
				return s, true
			}
			continue
		}
		if from > s.lb && from+length-1 < s.rb {
			return s, true
		}
	}
	return span{}, false
}

// bodyOnly reports whether every change to a Go file is inside the body of a
// function that both versions have, and names those functions. A change to
// a function every process runs at start-up, or to an init function, does not
// count: what it sets up reaches tests that never run its file again.
func bodyOnly(root, base, head string, c change, initFuncs []string) ([]string, bool) {
	if c.status != 'M' {
		return nil, false
	}
	oldSet, oldFile, err := parseAt(root, base, c.path)
	if err != nil {
		return nil, false
	}
	newSet, newFile, err := parseAt(root, head, c.path)
	if err != nil {
		return nil, false
	}
	hs, err := hunks(root, base, head, c.path)
	if err != nil || len(hs) == 0 {
		return nil, false
	}
	oldSpans, newSpans := bodies(oldSet, oldFile), bodies(newSet, newFile)
	var funcs []string
	for _, h := range hs {
		o, ok1 := within(oldSpans, h.oldStart, h.oldLen)
		n, ok2 := within(newSpans, h.newStart, h.newLen)
		if !ok1 || !ok2 || o.key != n.key {
			return nil, false
		}
		if n.name == "init" || slices.Contains(initFuncs, n.name) {
			return nil, false
		}
		if !slices.Contains(funcs, n.key) {
			funcs = append(funcs, n.key)
		}
	}
	return funcs, true
}

// testFileChange judges a change to a test file of the suite. It returns the
// tests to run, or nil when the change can reach tests in other files.
func testFileChange(root, base, head string, c change) ([]string, string) {
	switch c.status {
	case 'D':
		return []string{}, "a removed test file"
	case 'A':
		_, f, err := parseAt(root, head, c.path)
		if err != nil {
			return nil, "cannot be parsed"
		}
		if slices.ContainsFunc(f.Decls, isInit) {
			return nil, "adds an init function"
		}
		tests := testsIn(f)
		return tests, fmt.Sprintf("a new test file with %d tests", len(tests))
	}
	oldSet, oldFile, err := parseAt(root, base, c.path)
	if err != nil {
		return nil, "the old version cannot be parsed"
	}
	newSet, newFile, err := parseAt(root, head, c.path)
	if err != nil {
		return nil, "cannot be parsed"
	}
	hs, err := hunks(root, base, head, c.path)
	if err != nil {
		return nil, "the diff cannot be read"
	}
	tests := map[string]bool{}
	helpers := map[string]bool{}
	fileWide := false
	touch := func(fset *token.FileSet, f *ast.File, from, length int, side string) {
		if length == 0 {
			return
		}
		to := from + length - 1
		for _, d := range f.Decls {
			// The doc comment counts as part of the declaration: a change
			// there changes no code, and treating it as code only runs more.
			start := fset.Position(d.Pos()).Line
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Doc != nil {
				start = fset.Position(fn.Doc.Pos()).Line
			}
			end := fset.Position(d.End()).Line
			if to < start || from > end {
				continue
			}
			switch x := d.(type) {
			case *ast.FuncDecl:
				if name, ok := testName(x); ok {
					if side == "new" {
						tests[name] = true
					}
					continue
				}
				helpers[x.Name.Name] = true
			case *ast.GenDecl:
				if x.Tok == token.IMPORT {
					fileWide = true
					continue
				}
				for _, s := range x.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						helpers[s.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range s.Names {
							helpers[n.Name] = true
						}
					}
				}
			}
		}
	}
	for _, h := range hs {
		touch(oldSet, oldFile, h.oldStart, h.oldLen, "old")
		touch(newSet, newFile, h.newStart, h.newLen, "new")
		// An insertion between declarations touches no line of either; the
		// declaration it adds is on the new side, which the call above sees.
	}
	if helpers["init"] || helpers["TestMain"] || helpers["_"] {
		return nil, "changes package set-up"
	}
	if len(helpers) > 0 {
		others, _ := filepath.Glob(filepath.Join(root, "e2e/tui/*_test.go"))
		for _, other := range others {
			if filepath.ToSlash(mustRel(root, other)) == c.path {
				continue
			}
			data, err := os.ReadFile(other)
			if err != nil {
				return nil, "cannot read the other test files"
			}
			for h := range helpers {
				if regexp.MustCompile(`\b` + regexp.QuoteMeta(h) + `\b`).Match(data) {
					return nil, fmt.Sprintf("changes %s, which %s also uses", h, filepath.Base(other))
				}
			}
		}
		fileWide = true
	}
	if fileWide {
		all := testsIn(newFile)
		return all, fmt.Sprintf("changes code all %d tests in the file share", len(all))
	}
	out := make([]string, 0, len(tests))
	for t := range tests {
		out = append(out, t)
	}
	slices.Sort(out)
	return out, "changes " + strings.Join(out, ", ")
}

func isInit(d ast.Decl) bool {
	fn, ok := d.(*ast.FuncDecl)
	return ok && fn.Recv == nil && fn.Name.Name == "init"
}

func testName(fn *ast.FuncDecl) (string, bool) {
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
		return "", false
	}
	return fn.Name.Name, true
}

func testsIn(f *ast.File) []string {
	out := []string{}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			if name, ok := testName(fn); ok {
				out = append(out, name)
			}
		}
	}
	return out
}
