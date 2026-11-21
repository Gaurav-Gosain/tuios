package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

type suiteGraph struct {
	tests map[string]bool
	users map[string]map[string]bool
}

func loadSuiteGraph(root string) (*suiteGraph, error) {
	files, err := filepath.Glob(filepath.Join(root, "e2e/tui/*_test.go"))
	if err != nil {
		return nil, err
	}
	g := &suiteGraph{tests: map[string]bool{}, users: map[string]map[string]bool{}}
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

func suiteSources(root string) (map[string][]byte, error) {
	files, err := filepath.Glob(filepath.Join(root, "e2e/tui/*_test.go"))
	if err != nil {
		return nil, err
	}
	srcs := make(map[string][]byte, len(files))
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		srcs[file] = src
	}
	return srcs, nil
}

func readers(srcs map[string][]byte, p string) []string {
	base := path.Base(p)
	var tests []string
	for file, src := range srcs {
		if !bytes.Contains(src, []byte(`"`+base+`"`)) && !bytes.Contains(src, []byte("/"+base+`"`)) {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		tests = append(tests, testsIn(f)...)
	}
	return tests
}

func suiteFile(p string) bool {
	return strings.HasPrefix(p, "e2e/tui/") && !strings.Contains(strings.TrimPrefix(p, "e2e/tui/"), "/") && strings.HasSuffix(p, "_test.go")
}
