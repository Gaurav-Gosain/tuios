package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// A name is one top-level identifier a change touched, in the package where
// it is declared.
type name struct {
	dir   string // the package directory
	ident string
	typ   bool
	recv  string // for a method, its receiver's type
}

// goChange is what changed in one Go file of a binary package.
type goChange struct {
	built  bool     // the file is in the linux/amd64 build at either commit
	global string   // a reason the change reaches every importer, or ""
	bodies []string // functions whose body alone changed, as funcKey names them
	funcs  []string // the same functions, as go tool cover names them
	names  []name   // declarations that changed, were added or were removed
	fields []string // struct fields that changed, as Type.field, for the reason
}

// The CI build: linux/amd64 with cgo and no tags.
var buildCtx = func() build.Context {
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH, ctx.CgoEnabled, ctx.BuildTags = "linux", "amd64", true, nil
	return ctx
}()

// builtOnLinux reports whether the go command compiles a file with this name
// and content into the CI binary.
func builtOnLinux(file, src string) bool {
	ctx := buildCtx
	ctx.OpenFile = func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(src)), nil }
	ok, err := ctx.MatchFile("/x", path.Base(file))
	return err != nil || ok
}

// header is the text before the package clause, where build constraints and
// cgo preambles live. The package's doc comment is in it too, which only
// makes a doc change count for more.
func header(src string) string {
	if loc := packageClause.FindStringIndex(src); loc != nil {
		return src[:loc[0]]
	}
	return src
}

var packageClause = regexp.MustCompile(`(?m)^package `)

func analyzeGoFile(root, base, head, dir string, c change, initFuncs []string) goChange {
	var oldSrc, newSrc string
	if c.status != 'A' {
		oldSrc, _ = git(root, "show", base+":"+c.path)
	}
	if c.status != 'D' {
		newSrc, _ = git(root, "show", head+":"+c.path)
	}
	oldBuilt := c.status != 'A' && builtOnLinux(c.path, oldSrc)
	newBuilt := c.status != 'D' && builtOnLinux(c.path, newSrc)
	switch {
	case !oldBuilt && !newBuilt:
		return goChange{}
	case oldBuilt && newBuilt && header(oldSrc) != header(newSrc):
		return goChange{built: true, global: "its build constraints or cgo preamble changed"}
	}

	fset := token.NewFileSet()
	var oldFile, newFile *ast.File
	var err error
	if oldBuilt {
		if oldFile, err = parser.ParseFile(fset, c.path, oldSrc, parser.ParseComments|parser.SkipObjectResolution); err != nil {
			return goChange{built: true, global: "the old version cannot be parsed"}
		}
	}
	if newBuilt {
		if newFile, err = parser.ParseFile(fset, c.path, newSrc, parser.ParseComments|parser.SkipObjectResolution); err != nil {
			return goChange{built: true, global: "it cannot be parsed"}
		}
	}
	oldDecls, oldGlobal := declTexts(fset, oldFile)
	newDecls, newGlobal := declTexts(fset, newFile)
	if oldGlobal != newGlobal {
		return goChange{built: true, global: "its init functions, blank or dot imports, or cgo use changed"}
	}

	out := goChange{built: true}
	seen := map[name]bool{}
	add := func(n name) {
		if !seen[n] {
			seen[n] = true
			out.names = append(out.names, n)
		}
	}
	keys := map[string]bool{}
	for k := range oldDecls {
		keys[k] = true
	}
	for k := range newDecls {
		keys[k] = true
	}
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range keys {
			if !yield(k) {
				return
			}
		}
	}) {
		o, inOld := oldDecls[k]
		n, inNew := newDecls[k]
		d := n
		if !inNew {
			d = o
		}
		switch {
		case inOld && inNew && o.text == n.text && o.body == n.body:
			continue
		case inOld && inNew && o.text == n.text && d.isFunc:
			out.bodies = append(out.bodies, k)
			out.funcs = append(out.funcs, d.ident)
			// A function every process runs at start-up feeds the package
			// variables it initialises, which code anywhere may read.
			if slices.Contains(initFuncs, d.ident) {
				add(name{dir, d.ident, false, ""})
			}
		case inOld && inNew && d.typ:
			fields, ok := changedFields(o, n)
			if !ok {
				add(name{dir, d.ident, true, ""})
				break
			}
			// Only code that names a changed field can observe it.
			for _, f := range fields {
				add(name{dir, f, false, ""})
				out.fields = append(out.fields, d.ident+"."+f)
			}
		case d.recv != "":
			// The caller decides whether the method can change which
			// interfaces the type satisfies (see methodNames).
			add(name{dir, d.ident, false, d.recv})
		default:
			add(name{dir, d.ident, d.typ, ""})
		}
	}
	return out
}

// stripComments drops the comments the printer would print with a node:
// those of fields, specs and declarations. A comment inside a body is the
// file's, and a node printed alone does not print it.
func stripComments(n ast.Node) {
	ast.Inspect(n, func(x ast.Node) bool {
		switch x := x.(type) {
		case *ast.Field:
			x.Doc, x.Comment = nil, nil
		case *ast.TypeSpec:
			x.Doc, x.Comment = nil, nil
		case *ast.ValueSpec:
			x.Doc, x.Comment = nil, nil
		case *ast.ImportSpec:
			x.Doc, x.Comment = nil, nil
		case *ast.FuncDecl:
			x.Doc = nil
		case *ast.GenDecl:
			x.Doc = nil
		}
		return true
	})
}

// declText is one top-level declaration, printed without comments.
type declText struct {
	ident  string
	recv   string // the receiver's type, for a method
	typ    bool
	isFunc bool
	text   string // the declaration without a function's body
	body   string // a function's body
	// For a struct type: the declaration with its field list left empty,
	// and each field by name in order. An embedded field is named by its
	// type after embeddedPrefix.
	shape  string
	fields []fieldText
}

// embeddedPrefix starts the name of an embedded field. No identifier holds
// a space.
const embeddedPrefix = "embedded "

// fieldText is one field of a struct type, printed without comments.
type fieldText struct {
	name string
	text string // the type and the tag
}

// changedFields compares two versions of a struct type. When the only
// change is to unexported named fields, it returns those fields' names: no
// code outside the package can name them, the encoders that read fields by
// reflection skip them, and code in the package that reads or writes one
// names it. The fields both versions hold must keep their order, so an
// unkeyed literal cannot shift a value into another field. ok is false for
// any other change, which counts as a change to the type.
func changedFields(o, n declText) (fields []string, ok bool) {
	if o.shape == "" || o.shape != n.shape {
		return nil, false
	}
	index := func(fs []fieldText) map[string]string {
		m := map[string]string{}
		for _, f := range fs {
			m[f.name] = f.text
		}
		return m
	}
	om, nm := index(o.fields), index(n.fields)
	var common []string
	for _, f := range o.fields {
		if _, in := nm[f.name]; in {
			common = append(common, f.name)
		}
	}
	var order []string
	for _, f := range n.fields {
		if _, in := om[f.name]; in {
			order = append(order, f.name)
		}
	}
	if !slices.Equal(common, order) {
		return nil, false
	}
	seen := map[string]bool{}
	for _, fs := range [][]fieldText{o.fields, n.fields} {
		for _, f := range fs {
			if seen[f.name] || om[f.name] == nm[f.name] {
				continue
			}
			seen[f.name] = true
			if strings.HasPrefix(f.name, embeddedPrefix) || ast.IsExported(f.name) {
				return nil, false
			}
			fields = append(fields, f.name)
		}
	}
	return fields, len(fields) > 0
}

// declTexts keys every top-level declaration of a file, and sums up what
// reaches beyond names: init functions, blank and dot imports, and cgo.
func declTexts(fset *token.FileSet, f *ast.File) (map[string]declText, string) {
	out := map[string]declText{}
	if f == nil {
		return out, ""
	}
	print := func(n ast.Node) string {
		// The printer prints the comments of a field or a spec with it, so
		// a changed field comment read as a changed type.
		stripComments(n)
		var b bytes.Buffer
		_ = (&printer.Config{Mode: printer.RawFormat}).Fprint(&b, fset, n)
		return b.String()
	}
	var global []string
	for _, d := range f.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if x.Recv == nil && x.Name.Name == "init" {
				global = append(global, "init:"+print(x.Body))
				continue
			}
			k := funcKey(x)
			recv := ""
			if x.Recv != nil {
				recv, _, _ = strings.Cut(k, ".")
			}
			sig := print(x.Type)
			if x.Recv != nil {
				sig = print(x.Recv) + sig
			}
			body := ""
			if x.Body != nil {
				body = print(x.Body)
			}
			out["func "+k] = declText{ident: x.Name.Name, recv: recv, isFunc: true, text: sig, body: body}
		case *ast.GenDecl:
			if x.Tok == token.IMPORT {
				for _, s := range x.Specs {
					imp := s.(*ast.ImportSpec)
					if imp.Path.Value == `"C"` || (imp.Name != nil && (imp.Name.Name == "_" || imp.Name.Name == ".")) {
						global = append(global, "import:"+print(imp))
					}
				}
				continue
			}
			for i, s := range x.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					dt := declText{ident: s.Name.Name, typ: true, text: print(s)}
					if st, ok := s.Type.(*ast.StructType); ok && st.Fields != nil {
						for _, f := range st.Fields.List {
							text := print(f.Type)
							if f.Tag != nil {
								text += " " + f.Tag.Value
							}
							if len(f.Names) == 0 {
								dt.fields = append(dt.fields, fieldText{embeddedPrefix + text, text})
							}
							for _, n := range f.Names {
								dt.fields = append(dt.fields, fieldText{n.Name, text})
							}
						}
						list := st.Fields.List
						st.Fields.List = nil
						dt.shape = print(s)
						st.Fields.List = list
					}
					out["type "+s.Name.Name] = dt
				case *ast.ValueSpec:
					// Read before print, which drops the comments.
					embed := embedDirective(x.Doc) || embedDirective(s.Doc)
					text := print(s)
					// In a const block an implicit value repeats the one above
					// with the next iota, so a spec's place is part of it.
					if x.Tok == token.CONST {
						text += "#" + strconv.Itoa(i)
					}
					if embed {
						text += " embed"
					}
					for _, n := range s.Names {
						if n.Name != "_" {
							out["value "+n.Name] = declText{ident: n.Name, text: text}
						} else {
							global = append(global, "blank:"+text)
						}
					}
				}
			}
		}
	}
	slices.Sort(global)
	return out, strings.Join(global, "\n")
}

func embedDirective(g *ast.CommentGroup) bool {
	if g == nil {
		return false
	}
	for _, c := range g.List {
		if strings.HasPrefix(c.Text, "//go:embed") {
			return true
		}
	}
	return false
}

// embedVars names the variables of a package that hold embedded files.
func embedVars(root, dir string) []string {
	var out []string
	for _, f := range packageFiles(root, dir) {
		for _, d := range f.file.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, s := range g.Specs {
				v := s.(*ast.ValueSpec)
				if embedDirective(g.Doc) || embedDirective(v.Doc) {
					for _, n := range v.Names {
						out = append(out, n.Name)
					}
				}
			}
		}
	}
	return out
}

type parsedFile struct {
	path string // relative to the repository root
	src  []byte
	file *ast.File
}

var parsedPackages = map[string][]parsedFile{}

// packageFiles parses the Go files of a package directory that the CI build
// compiles, at the checkout, which is the head.
func packageFiles(root, dir string) []parsedFile {
	if files, ok := parsedPackages[dir]; ok {
		return files
	}
	var files []parsedFile
	matches, _ := filepath.Glob(filepath.Join(root, dir, "*.go"))
	for _, m := range matches {
		// The suite's package is all test files. Any other package's test
		// files are not in the binary or the test process.
		if strings.HasSuffix(m, "_test.go") && dir != suiteDir {
			continue
		}
		src, err := os.ReadFile(m)
		if err != nil || !builtOnLinux(m, string(src)) {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), m, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		files = append(files, parsedFile{path: path.Join(dir, filepath.Base(m)), src: src, file: f})
	}
	parsedPackages[dir] = files
	return files
}

// reach follows changed names to the files of code that can observe them.
// ok is false when the walk grew past its bound, and the caller falls back to
// whole packages and their importers.
//
// A declaration is observed only by code that names it: in its own package
// by its name, elsewhere as pkg.Name. A package-level declaration that names
// it changes with it, so its own name joins the walk. A type also reaches code
// that never names it, through values of the type. A value is made where
// the type is named (a literal, a var, new or make), or where a type that
// holds it is named, which the walk follows too. A test that uses the value
// runs the code that made it, so it ran one of the files found here.
func reach(root string, g *graph, start []name) (funcs map[funcRef]bool, ok bool) {
	funcs = map[funcRef]bool{}
	seen := map[name]bool{}
	work := slices.Clone(start)
	const bound = 400
	for len(work) > 0 {
		n := work[0]
		work = work[1:]
		if seen[n] {
			continue
		}
		seen[n] = true
		if len(seen) > bound {
			return nil, false
		}
		look := func(dir string, qualified bool) {
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(n.ident) + `\b`)
			if qualified {
				re = regexp.MustCompile(`\.` + regexp.QuoteMeta(n.ident) + `\b`)
			}
			for _, f := range packageFiles(root, dir) {
				if !re.Match(f.src) {
					continue
				}
				for _, d := range f.file.Decls {
					if fn, ok := d.(*ast.FuncDecl); ok && refers(fn, n.ident, qualified) {
						funcs[funcRef{f.path, fn.Name.Name}] = true
					}
				}
				for _, dep := range dependents(f.file, n.ident, qualified) {
					work = append(work, name{dir, dep.ident, dep.typ, ""})
				}
			}
		}
		look(n.dir, false)
		if ast.IsExported(n.ident) {
			for _, imp := range g.importers[n.dir] {
				look(imp, true)
			}
		}
	}
	return funcs, true
}

// refers reports whether a node names ident: as a plain identifier in its
// own package, or as the selector of x.ident from another. Any identifier or
// selector of that name counts, a local one included, which only selects
// more. The name of a field or a parameter declares it and is no reference:
// a struct with a field named ident does not depend on ident.
func refers(n ast.Node, ident string, qualified bool) bool {
	found := false
	var visit func(ast.Node) bool
	visit = func(x ast.Node) bool {
		switch x := x.(type) {
		case *ast.Field:
			if x.Type != nil {
				ast.Inspect(x.Type, visit)
			}
			return false
		case *ast.SelectorExpr:
			if x.Sel.Name == ident {
				found = true
			}
		case *ast.Ident:
			if !qualified && x.Name == ident {
				found = true
			}
		}
		return !found
	}
	ast.Inspect(n, visit)
	return found
}

// dependents lists the package-level declarations of a file whose
// definition names ident: their meaning changes when it does.
func dependents(f *ast.File, ident string, qualified bool) []declText {
	names := func(n ast.Node) bool { return refers(n, ident, qualified) }
	var out []declText
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok == token.IMPORT {
			continue
		}
		// A const spec with no values repeats the last values above it.
		var last []ast.Expr
		for _, s := range g.Specs {
			switch s := s.(type) {
			case *ast.TypeSpec:
				if s.Name.Name != ident && names(s.Type) {
					out = append(out, declText{ident: s.Name.Name, typ: true})
				}
			case *ast.ValueSpec:
				values := s.Values
				if g.Tok == token.CONST {
					if len(values) == 0 {
						values = last
					} else {
						last = values
					}
				}
				hit := (s.Type != nil && names(s.Type)) || slices.ContainsFunc(values, func(e ast.Expr) bool { return names(e) })
				if hit {
					for _, n := range s.Names {
						if n.Name != ident && n.Name != "_" {
							out = append(out, declText{ident: n.Name})
						}
					}
				}
			}
		}
	}
	return out
}

// interfaceMethods returns the method names of every interface type in the
// packages the binary builds, the standard library's included. A method by
// one of those names can make a type satisfy an interface, or stop it, which
// changes code that never names the method. Any other method is reached only
// through its name.
func interfaceMethods(root string) (map[string]bool, error) {
	cmd := exec.Command("go", "list", "-deps", "-f", "{{.Dir}}\t{{join .GoFiles \" \"}}", "./cmd/tuios")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	names := map[string]bool{}
	for line := range strings.Lines(string(out)) {
		dir, files, _ := strings.Cut(strings.TrimRight(line, "\n"), "\t")
		for _, f := range strings.Fields(files) {
			src, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil || !bytes.Contains(src, []byte("interface")) {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), f, src, parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				if it, ok := n.(*ast.InterfaceType); ok {
					for _, m := range it.Methods.List {
						for _, id := range m.Names {
							names[id.Name] = true
						}
					}
				}
				return true
			})
		}
	}
	return names, nil
}
