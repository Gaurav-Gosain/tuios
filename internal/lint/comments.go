package lint

import (
	"go/ast"
	"go/token"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Rules that a comment outside the allowed kinds breaks. RemovedComment.Rule
// holds one of them.
const (
	RuleInBody       = "a comment inside a function body"
	RuleNotAPI       = "a doc comment on a name that is not exported API"
	RuleFreestanding = "a comment that documents no exported API"
)

// RemovedComment is one comment that the comment rules do not allow.
type RemovedComment struct {
	Comment *ast.Comment
	// Lines is the number of source lines the comment spans.
	Lines int
	// Rule says which rule the comment breaks.
	Rule string
}

var (
	directive  = regexp.MustCompile(`^//([a-z0-9]+:[a-z0-9]|line |export |extern | \+build|nolint)`)
	markerText = regexp.MustCompile(`nolint|#nosec|NEGATIVE CONTROL`)
	license    = regexp.MustCompile(`(?i)copyright|license|spdx`)
	testFunc   = regexp.MustCompile(`^(Test|Benchmark|Fuzz|Example)`)
)

// KeptComment reports whether a single comment line is a directive or a
// machine-read marker, which every rule keeps: //go: and other directives,
// //nolint with its reason, #nosec, build tags and NEGATIVE CONTROL notes.
func KeptComment(text string) bool {
	return directive.MatchString(text) || markerText.MatchString(text)
}

// CommentsToRemove returns every comment in f that the comment rules do not
// allow, in source order. The rules keep doc comments on exported API (outside
// package main and test files), package docs, license headers, the doc
// comments of Test, Benchmark, Fuzz and Example functions, everything inside
// an Example function, the C preamble of import "C", and each comment that
// KeptComment accepts. A generated file has no comments to remove.
func CommentsToRemove(fset *token.FileSet, f *ast.File, isTest bool) []RemovedComment {
	if ast.IsGenerated(f) {
		return nil
	}
	api := !isTest && f.Name.Name != "main"
	keep := map[*ast.CommentGroup]bool{}
	docOf := map[*ast.CommentGroup]bool{}
	var bodies, kept [][2]token.Pos

	if f.Doc != nil {
		keep[f.Doc] = true
	}
	for _, cg := range f.Comments {
		if cg.Pos() < f.Package && license.MatchString(cg.Text()) {
			keep[cg] = true
		}
		if markerText.MatchString(cg.Text()) && strings.Contains(cg.Text(), "NEGATIVE CONTROL") {
			keep[cg] = true
		}
	}
	mark := func(cg *ast.CommentGroup, ok bool) {
		if cg == nil {
			return
		}
		docOf[cg] = true
		if ok {
			keep[cg] = true
		}
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			name := d.Name.Name
			isTestFunc := isTest && d.Recv == nil && testFunc.MatchString(name) || isTest && name == "TestMain"
			mark(d.Doc, isTestFunc || api && exported(name) && (d.Recv == nil || exported(recvName(d.Recv))))
			if d.Body != nil {
				span := [2]token.Pos{d.Body.Lbrace, d.Body.Rbrace}
				if isTest && d.Recv == nil && strings.HasPrefix(name, "Example") {
					kept = append(kept, span)
				} else {
					bodies = append(bodies, span)
				}
			}
		case *ast.GenDecl:
			anyExported := false
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.ImportSpec:
					if s.Path.Value == `"C"` {
						keep[d.Doc] = true
					}
					mark(s.Doc, false)
					mark(s.Comment, false)
				case *ast.TypeSpec:
					ok := api && exported(s.Name.Name)
					anyExported = anyExported || ok
					mark(s.Doc, ok)
					mark(s.Comment, ok)
					markFields(s.Type, ok, mark)
				case *ast.ValueSpec:
					ok := false
					for _, n := range s.Names {
						ok = ok || api && exported(n.Name)
					}
					anyExported = anyExported || ok
					mark(s.Doc, ok)
					mark(s.Comment, ok)
				}
			}
			if d.Tok != token.IMPORT || !keep[d.Doc] {
				mark(d.Doc, anyExported)
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if fl, ok := n.(*ast.FuncLit); ok {
			bodies = append(bodies, [2]token.Pos{fl.Body.Lbrace, fl.Body.Rbrace})
		}
		return true
	})
	within := func(p token.Pos, spans [][2]token.Pos) bool {
		for _, s := range spans {
			if p > s[0] && p < s[1] {
				return true
			}
		}
		return false
	}

	var out []RemovedComment
	for _, cg := range f.Comments {
		if keep[cg] || within(cg.Pos(), kept) {
			continue
		}
		rule := RuleFreestanding
		switch {
		case within(cg.Pos(), bodies):
			rule = RuleInBody
		case docOf[cg]:
			rule = RuleNotAPI
		}
		for _, c := range cg.List {
			if KeptComment(c.Text) {
				continue
			}
			lines := fset.Position(c.End()).Line - fset.Position(c.Pos()).Line + 1
			out = append(out, RemovedComment{Comment: c, Lines: lines, Rule: rule})
		}
	}
	return out
}

func markFields(t ast.Expr, api bool, mark func(*ast.CommentGroup, bool)) {
	var fields *ast.FieldList
	switch t := t.(type) {
	case *ast.StructType:
		fields = t.Fields
	case *ast.InterfaceType:
		fields = t.Methods
	default:
		return
	}
	if fields == nil {
		return
	}
	for _, fld := range fields.List {
		ok := false
		for _, n := range fld.Names {
			ok = ok || api && exported(n.Name)
		}
		if len(fld.Names) == 0 {
			ok = api && exported(embeddedName(fld.Type))
		}
		mark(fld.Doc, ok)
		mark(fld.Comment, ok)
		markFields(fld.Type, ok, mark)
	}
}

func recvName(fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	return embeddedName(fl.List[0].Type)
}

func embeddedName(t ast.Expr) string {
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.SelectorExpr:
			return x.Sel.Name
		case *ast.Ident:
			return x.Name
		default:
			return ""
		}
	}
}

func exported(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}
