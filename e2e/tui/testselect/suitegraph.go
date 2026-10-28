package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// suiteGraph is which top-level declarations of the suite's own package name
// which others, read from the test files at the head. The suite's code is not
// in the coverage run (go test instruments no _test.go file), so a change to a
// helper, or a name of another package that a helper uses, reaches the tests
// whose code names it, directly or through other helpers.
//
// Names are matched by identifier alone: a local variable or a field of the
// same name counts as a use. That only selects more.
type suiteGraph struct {
	tests map[string]bool            // the Test functions
	users map[string]map[string]bool // declaration to the declarations that name it
}

// loadSuiteGraph parses e2e/tui/*_test.go under root.
func loadSuiteGraph(root string) (*suiteGraph, error) {
	files, err := filepath.Glob(filepath.Join(root, "e2e/tui/*_test.go"))
	if err != nil {
		return nil, err
	}
	g := &suiteGraph{tests: map[string]bool{}, users: map[string]map[string]bool{}}
	// Each declaration's name, with the identifiers its code names.
	uses := map[string]map[string]bool{}
	add := func(decl string, n ast.Node) {
		set := uses[decl]
		if set == nil {
			set = map[string]bool{}
			uses[decl] = set
		}
		if n == nil {
			return
		}
		ast.Inspect(n, func(x ast.Node) bool {
			switch x := x.(type) {
			case *ast.Ident:
				set[x.Name] = true
			case *ast.SelectorExpr:
				set[x.Sel.Name] = true
			}
			return true
		})
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range file.Decls {
			switch x := d.(type) {
			case *ast.FuncDecl:
				// A method is named by its own name, as x.name; the receiver
				// type's methods are reached through that.
				decl := x.Name.Name
				if name, ok := testName(x); ok {
					g.tests[name] = true
				}
				add(decl, x.Type)
				if x.Recv != nil {
					add(decl, x.Recv)
				}
				if x.Body != nil {
					add(decl, x.Body)
				}
			case *ast.GenDecl:
				if x.Tok == token.IMPORT {
					continue
				}
				for _, s := range x.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						add(s.Name.Name, s.Type)
					case *ast.ValueSpec:
						for _, n := range s.Names {
							add(n.Name, s.Type)
							for _, v := range s.Values {
								add(n.Name, v)
							}
						}
					}
				}
			}
		}
	}
	for decl, set := range uses {
		for ident := range set {
			if _, declared := uses[ident]; !declared || ident == decl {
				continue
			}
			u := g.users[ident]
			if u == nil {
				u = map[string]bool{}
				g.users[ident] = u
			}
			u[decl] = true
		}
	}
	return g, nil
}

// testsUsing returns the tests whose code names one of decls, directly or
// through the suite's other declarations, the tests among decls included.
func (g *suiteGraph) testsUsing(decls []string) []string {
	seen := map[string]bool{}
	work := slices.Clone(decls)
	var out []string
	for len(work) > 0 {
		d := work[len(work)-1]
		work = work[:len(work)-1]
		if seen[d] {
			continue
		}
		seen[d] = true
		if g.tests[d] {
			out = append(out, d)
		}
		for u := range g.users[d] {
			work = append(work, u)
		}
	}
	slices.Sort(out)
	return out
}

// suiteFile reports whether p, relative to the repository root, is a test
// file of the suite's own package.
func suiteFile(p string) bool {
	return strings.HasPrefix(p, "e2e/tui/") && !strings.Contains(strings.TrimPrefix(p, "e2e/tui/"), "/") && strings.HasSuffix(p, "_test.go")
}
