// Command assertset checks that a change to e2e test files kept every
// assertion and deleted nothing but sleeps.
//
// For each Go file that differs from a base revision it reads both versions
// with go/ast and compares three hashes:
//
//   - asserts: the format argument of every Fatalf and Errorf call, in order.
//   - waits: the full text of every wait call (a function whose name starts
//     with wait or Wait, and stayTrue), condition closures included, in order.
//   - rest: the tokens of the whole file with every time.Sleep statement and
//     the "time" import taken out, and comments ignored. Equal hashes mean the
//     change removed sleeps and nothing else.
//
// It prints one line per file and exits 1 when any hash differs.
//
// Usage, from e2e/tui:
//
//	go run ./internal/assertset [-base origin/main] [file...]
//
// With no files it checks every .go file in this directory that differs
// from the base.
package main

import (
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	base := flag.String("base", "origin/main", "the revision to compare with")
	flag.Parse()
	files := flag.Args()
	if len(files) == 0 {
		out, err := exec.Command("git", "diff", "--name-only", "--relative", *base, "--", ".").Output()
		if err != nil {
			fail("git diff: %v", err)
		}
		for f := range strings.FieldsSeq(string(out)) {
			if strings.HasSuffix(f, ".go") && !strings.Contains(f, "/") {
				files = append(files, f)
			}
		}
	}
	bad := 0
	for _, f := range files {
		old, err := exec.Command("git", "show", *base+":./"+f).Output()
		if err != nil {
			fmt.Printf("%s: new file, nothing to compare\n", f)
			continue
		}
		cur, err := os.ReadFile(f)
		if err != nil {
			fail("%v", err)
		}
		a, err := digest(f, old)
		if err != nil {
			fail("%s at %s: %v", f, *base, err)
		}
		b, err := digest(f, cur)
		if err != nil {
			fail("%s: %v", f, err)
		}
		same := a.assertHash == b.assertHash && a.waitHash == b.waitHash && a.restHash == b.restHash
		status := "same"
		if !same {
			status = "DIFFERS"
			bad++
		}
		fmt.Printf("%s: %s asserts=%d/%s waits=%d/%s rest=%s sleeps %d -> %d\n",
			f, status, b.asserts, b.assertHash, b.waits, b.waitHash, b.restHash, a.sleeps, b.sleeps)
		if !same {
			fmt.Printf("  base: asserts=%d/%s waits=%d/%s rest=%s\n", a.asserts, a.assertHash, a.waits, a.waitHash, a.restHash)
		}
	}
	fmt.Printf("%d files checked, %d differ\n", len(files), bad)
	if bad > 0 {
		os.Exit(1)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

type sums struct {
	asserts, waits, sleeps         int
	assertHash, waitHash, restHash string
}

func short(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b))[:12] }

func digest(name string, src []byte) (sums, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return sums{}, err
	}
	timeName := "time"
	for _, imp := range f.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p == "time" && imp.Name != nil {
			timeName = imp.Name.Name
		}
	}
	var s sums
	var asserts, waits bytes.Buffer
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(call)
		switch {
		case name == "Fatalf" || name == "Errorf":
			s.asserts++
			if len(call.Args) > 0 {
				printer.Fprint(&asserts, fset, call.Args[0])
			}
			asserts.WriteByte(0)
		case strings.HasPrefix(name, "wait") || strings.HasPrefix(name, "Wait") || name == "stayTrue":
			s.waits++
			printer.Fprint(&waits, fset, call)
			waits.WriteByte(0)
		}
		return true
	})
	s.assertHash = short(asserts.Bytes())
	s.waitHash = short(waits.Bytes())

	isSleep := func(st ast.Stmt) bool {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			return false
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sleep" {
			return false
		}
		x, ok := sel.X.(*ast.Ident)
		return ok && x.Name == timeName
	}
	filter := func(list []ast.Stmt) []ast.Stmt {
		var out []ast.Stmt
		for _, st := range list {
			if isSleep(st) {
				s.sleeps++
				continue
			}
			out = append(out, st)
		}
		return out
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BlockStmt:
			v.List = filter(v.List)
		case *ast.CaseClause:
			v.Body = filter(v.Body)
		case *ast.CommClause:
			v.Body = filter(v.Body)
		}
		return true
	})
	var decls []ast.Decl
	for _, d := range f.Decls {
		if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			var specs []ast.Spec
			for _, sp := range gd.Specs {
				if p, _ := strconv.Unquote(sp.(*ast.ImportSpec).Path.Value); p != "time" {
					specs = append(specs, sp)
				}
			}
			if len(specs) == 0 {
				continue
			}
			gd.Specs = specs
		}
		decls = append(decls, d)
	}
	f.Decls = decls
	var printed bytes.Buffer
	if err := printer.Fprint(&printed, fset, f); err != nil {
		return sums{}, err
	}
	var toks bytes.Buffer
	var sc scanner.Scanner
	tf := token.NewFileSet().AddFile(name, -1, printed.Len())
	sc.Init(tf, printed.Bytes(), nil, 0)
	for {
		_, tok, lit := sc.Scan()
		if tok == token.EOF {
			break
		}
		fmt.Fprintf(&toks, "%s %s\x00", tok, lit)
	}
	s.restHash = short(toks.Bytes())
	return s, nil
}

func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}
