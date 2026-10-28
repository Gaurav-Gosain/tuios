package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
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

// testFileChange judges a change to a test file of the suite. It returns the
// tests to run, or nil when every test has to run: the change sets up the
// whole package (TestMain, an init function, a blank declaration, or a blank
// or dot import). A changed helper selects the tests whose code names it,
// in any file of the suite, through sg.
func testFileChange(root, base, head string, c change, sg *suiteGraph) ([]string, string) {
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
	if globalImports(oldFile) != globalImports(newFile) {
		return nil, "changes a blank or dot import"
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
	var why []string
	if len(helpers) > 0 {
		names := slices.Sorted(maps.Keys(helpers))
		users := sg.testsUsing(names)
		for _, t := range users {
			tests[t] = true
		}
		why = append(why, fmt.Sprintf("changes %s, which %d tests use", listNames(names), len(users)))
	}
	if fileWide {
		all := testsIn(newFile)
		for _, t := range all {
			tests[t] = true
		}
		why = append(why, fmt.Sprintf("changes the imports all %d tests in the file share", len(all)))
	}
	out := slices.Sorted(maps.Keys(tests))
	if len(why) == 0 {
		why = append(why, "changes "+listNames(out))
	}
	return out, strings.Join(why, "; ")
}

// globalImports is the blank and dot imports of a file, which change what
// the whole package sees.
func globalImports(f *ast.File) string {
	var out []string
	for _, imp := range f.Imports {
		if imp.Name != nil && (imp.Name.Name == "_" || imp.Name.Name == ".") {
			out = append(out, imp.Name.Name+" "+imp.Path.Value)
		}
	}
	slices.Sort(out)
	return strings.Join(out, "\n")
}

// listNames joins names for a reason line, at most eight of them.
func listNames(names []string) string {
	if len(names) > 8 {
		return strings.Join(names[:8], ", ") + fmt.Sprintf(" and %d more", len(names)-8)
	}
	return strings.Join(names, ", ")
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
