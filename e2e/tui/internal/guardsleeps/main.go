// Command guardsleeps deletes the e2e sleeps that waited out an insert guard
// tuios no longer has.
//
// tuios used to drop every unmodified printable key for 150 ms after it
// entered terminal mode (insertGuard in harness_test.go). Commit 815cb8a02
// took that guard out of internal/input/keyboard_terminal.go, but the suite
// kept waiting it out after every mode switch. This command deletes, in the
// *_test.go files of one directory:
//
//   - every time.Sleep statement whose argument is insertGuard, or a sum that
//     starts with insertGuard (insertGuard + 150*time.Millisecond), and
//   - every time.Sleep statement inside enterTerminalMode, leaveTerminalMode
//     and afterModeSwitch.
//
// It deletes nothing else. The gap between keys in sendKeys stays: it is
// pacing, not a guard wait (see keep). Comments stay, and so do sleeps that only scale
// insertGuard (2 * insertGuard) and pause calls, which state a reason of their
// own. A "time" import the deletions leave unused goes, because the file
// would not compile with it. Each change prints as file:line: statement. A
// second run finds nothing to change.
//
// Usage, from e2e/tui:
//
//	go run ./internal/guardsleeps [-n] [dir]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// keep are helpers whose insertGuard sleep is no guard wait. sendKeys sends
// each key as its own write a moment apart, as a person types: without the gap
// two keys arrive in one read, and esc, esc decodes as alt+esc.
var keep = map[string]bool{
	"sendKeys": true,
}

// helpers are the mode-switch helpers whose fixed sleeps all go.
var helpers = map[string]bool{
	"enterTerminalMode": true,
	"leaveTerminalMode": true,
	"afterModeSwitch":   true,
}

func main() {
	dry := flag.Bool("n", false, "print the sites and write nothing")
	flag.Parse()
	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	slices.Sort(files)
	total, changed := 0, 0
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		out, sites, err := rewrite(name, src)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, s := range sites {
			fmt.Println(s)
		}
		if len(sites) == 0 {
			continue
		}
		total += len(sites)
		changed++
		if !*dry {
			if err := os.WriteFile(name, out, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
	}
	fmt.Printf("%d sleeps deleted in %d files\n", total, changed)
}

type span struct{ start, end int }

// rewrite returns src with the guard sleeps deleted, and one line per site.
func rewrite(name string, src []byte) ([]byte, []string, error) {
	if formatted, err := format.Source(src); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", name, err)
	} else if !bytes.Equal(formatted, src) {
		return nil, nil, fmt.Errorf("%s: not gofmt clean, run gofmt first", name)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	timeName := importName(f, "time")
	if timeName == "" {
		return src, nil, nil
	}
	var dels []span
	var sites []string
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil || (fd.Recv == nil && keep[fd.Name.Name]) {
			continue
		}
		inHelper := fd.Recv == nil && helpers[fd.Name.Name]
		var err error
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if err != nil {
				return false
			}
			es, ok := n.(*ast.ExprStmt)
			if !ok {
				return true
			}
			arg, ok := sleepArg(es.X, timeName)
			if !ok || !(inHelper || startsWithGuard(arg)) {
				return true
			}
			var sp span
			sp, err = lineSpan(fset, src, es)
			if err != nil {
				return false
			}
			dels = append(dels, sp)
			pos := fset.Position(es.Pos())
			sites = append(sites, fmt.Sprintf("%s:%d: %s", name, pos.Line, src[fset.Position(es.Pos()).Offset:fset.Position(es.End()).Offset]))
			return false
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if len(dels) == 0 {
		return src, nil, nil
	}
	out := cut(src, dels)
	out, err = dropUnusedImport(name, out, "time")
	if err != nil {
		return nil, nil, err
	}
	out, err = format.Source(out)
	if err != nil {
		return nil, nil, fmt.Errorf("%s after the rewrite: %w", name, err)
	}
	return out, sites, nil
}

// importName is the name a file uses for an import path, or "".
func importName(f *ast.File, path string) string {
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p != path {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return ""
			}
			return imp.Name.Name
		}
		return filepath.Base(path)
	}
	return ""
}

// sleepArg returns the argument of a time.Sleep call.
func sleepArg(e ast.Expr, timeName string) (ast.Expr, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Sleep" {
		return nil, false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok || x.Name != timeName {
		return nil, false
	}
	return call.Args[0], true
}

// startsWithGuard reports whether e is insertGuard, or a sum whose first term
// is insertGuard.
func startsWithGuard(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.ParenExpr:
		return startsWithGuard(v.X)
	case *ast.Ident:
		return v.Name == "insertGuard"
	case *ast.BinaryExpr:
		return v.Op == token.ADD && startsWithGuard(v.X)
	}
	return false
}

// lineSpan is the whole line of a statement that has its line to itself.
func lineSpan(fset *token.FileSet, src []byte, n ast.Node) (span, error) {
	start := fset.Position(n.Pos()).Offset
	end := fset.Position(n.End()).Offset
	ls := bytes.LastIndexByte(src[:start], '\n') + 1
	le := bytes.IndexByte(src[end:], '\n')
	if le < 0 {
		le = len(src) - end
	}
	le += end
	if strings.TrimSpace(string(src[ls:start])) != "" || strings.TrimSpace(string(src[end:le])) != "" {
		return span{}, fmt.Errorf("%s: the sleep shares its line with other code, edit it by hand", fset.Position(n.Pos()))
	}
	if le < len(src) {
		le++
	}
	return span{ls, le}, nil
}

func cut(src []byte, dels []span) []byte {
	slices.SortFunc(dels, func(a, b span) int { return a.start - b.start })
	var out []byte
	at := 0
	for _, d := range dels {
		out = append(out, src[at:d.start]...)
		at = d.end
	}
	return append(out, src[at:]...)
}

// dropUnusedImport removes the import of path when nothing in src uses it.
func dropUnusedImport(name string, src []byte, path string) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("%s after the rewrite: %w", name, err)
	}
	local := importName(f, path)
	used := false
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == local {
				used = true
			}
		}
		return !used
	})
	if used {
		return src, nil
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, spec := range gd.Specs {
			is := spec.(*ast.ImportSpec)
			if p, _ := strconv.Unquote(is.Path.Value); p != path {
				continue
			}
			var node ast.Node = is
			if !gd.Lparen.IsValid() {
				node = gd
			}
			sp, err := lineSpan(fset, src, node)
			if err != nil {
				return nil, err
			}
			return cut(src, []span{sp}), nil
		}
	}
	return src, nil
}
