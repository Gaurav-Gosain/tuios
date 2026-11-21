package nitpick

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// CommentedOutCode reports line comments that hold Go code.
var CommentedOutCode = &analysis.Analyzer{
	Name: "commented_out_code",
	Doc: `report Go code that is kept in a comment

A comment group on lines of its own is reported when each line has code
marks (a parenthesis, a brace, := or =, or a statement keyword) and the group
parses as Go statements or declarations. A group with prose in it, an
indented example, a comment at the end of a line of code and a doc comment
are skipped: they show an example or a formula on purpose. An expression
must be a call to count as a statement, so "0 <= i && i < n" is prose.`,
	URL: "https://pkg.go.dev/tuios.dev/nitpick#CommentedOutCode",
	Run: runCommentedOutCode,
}

func runCommentedOutCode(pass *analysis.Pass) (any, error) {
	files := handWritten(pass)
	for _, f := range pass.Files {
		if !files[f] {
			continue
		}
		tf := pass.Fset.File(f.FileStart)
		src, err := pass.ReadFile(tf.Name())
		if err != nil {
			continue
		}
		docs := docComments(f)
		for _, g := range f.Comments {
			if docs[g] || !isReason(g) || !ownLine(src, tf.Offset(g.Pos())) || indented(g.List[0].Text) {
				continue
			}
			if !all(g.List, func(c *ast.Comment) bool { return looksLikeCode(c.Text) }) {
				continue
			}
			code := parseCode(g.List)
			if code == nil || !namesResolve(pass, f, g.Pos(), code) {
				continue
			}
			pass.Report(analysis.Diagnostic{
				Pos:     g.Pos(),
				End:     g.End(),
				Message: "the comment holds code: remove it, because version control keeps old code",
			})
		}
	}
	return nil, nil
}

func all[T any](xs []T, f func(T) bool) bool {
	for _, x := range xs {
		if !f(x) {
			return false
		}
	}
	return true
}

func indented(c string) bool {
	t := strings.TrimPrefix(c, "//")
	return strings.HasPrefix(t, "\t") || strings.HasPrefix(t, "  ") || strings.HasPrefix(t, " \t")
}

func docComments(f *ast.File) map[*ast.CommentGroup]bool {
	out := map[*ast.CommentGroup]bool{}
	if f.Doc != nil {
		out[f.Doc] = true
	}
	ast.Inspect(f, func(n ast.Node) bool {
		var g *ast.CommentGroup
		switch n := n.(type) {
		case *ast.FuncDecl:
			g = n.Doc
		case *ast.GenDecl:
			g = n.Doc
		case *ast.TypeSpec:
			g = n.Doc
		case *ast.ValueSpec:
			g = n.Doc
		case *ast.Field:
			g = n.Doc
		}
		if g != nil {
			out[g] = true
		}
		return true
	})
	return out
}

func codeText(c string) (string, bool) {
	if !strings.HasPrefix(c, "//") {
		return "", false
	}
	t := strings.TrimSpace(c[2:])
	for _, p := range []string{"go:", "nolint", "lint:", "+build", "export ", "line ", "#", "TODO", "FIXME", "XXX", "NOTE", "BUG", "Output:", "Deprecated:"} {
		if strings.HasPrefix(t, p) {
			return "", false
		}
	}
	return t, t != ""
}

func looksLikeCode(c string) bool {
	t, ok := codeText(c)
	if !ok {
		return false
	}
	if strings.ContainsAny(t, "(){}") || strings.Contains(t, ":=") || strings.Contains(t, " = ") ||
		strings.HasSuffix(t, "++") || strings.HasSuffix(t, "--") {
		return true
	}
	switch t {
	case "return", "break", "continue", "fallthrough", "default:":
		return true
	}
	for _, p := range []string{"return ", "case ", "defer ", "go ", "var ", "if ", "for ", "else ", "switch "} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func parseCode(run []*ast.Comment) *ast.File {
	var b strings.Builder
	for _, c := range run {
		t, _ := codeText(c.Text)
		b.WriteString(t)
		b.WriteByte('\n')
	}
	text := b.String()
	switch strings.TrimSpace(text) {
	case "return", "break", "continue", "fallthrough", "default:":
		return nil
	}
	fset := token.NewFileSet()
	if f, err := parser.ParseFile(fset, "", "package p\nfunc _() {\n"+text+"\n}", parser.SkipObjectResolution); err == nil {
		if realStatements(f) {
			return f
		}
		return nil
	}
	f, err := parser.ParseFile(fset, "", "package p\n"+text, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	return f
}

func realStatements(f *ast.File) bool {
	ok := true
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ExprStmt:
			switch x := ast.Unparen(n.X).(type) {
			case *ast.CallExpr:
			case *ast.UnaryExpr:
				ok = ok && x.Op == token.ARROW
			default:
				ok = false
			}
		case *ast.AssignStmt:
			for _, l := range n.Lhs {
				switch ast.Unparen(l).(type) {
				case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr, *ast.StarExpr:
				default:
					ok = false
				}
			}
		case *ast.LabeledStmt:
			ok = false
		}
		return ok
	})
	return ok
}

func namesResolve(pass *analysis.Pass, f *ast.File, pos token.Pos, code *ast.File) bool {
	imports := map[string]bool{}
	for _, im := range f.Imports {
		path := strings.Trim(im.Path.Value, `"`)
		name := path[strings.LastIndex(path, "/")+1:]
		if im.Name != nil {
			name = im.Name.Name
		}
		imports[name] = true
	}
	scope := pass.Pkg.Scope().Innermost(pos)
	if scope == nil {
		scope = pass.Pkg.Scope()
	}
	declared := map[string]bool{}
	seen := map[string]bool{}
	ast.Inspect(code, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			declared[n.Name.Name] = true
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, l := range n.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						declared[id.Name] = true
					}
				}
			}
		case *ast.SelectorExpr:
			ast.Inspect(n.X, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					seen[id.Name] = true
				}
				return true
			})
			return false
		case *ast.KeyValueExpr:
			ast.Inspect(n.Value, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					seen[id.Name] = true
				}
				return true
			})
			return false
		case *ast.Ident:
			if n.Name != "_" && n.Name != "p" {
				seen[n.Name] = true
			}
		}
		return true
	})
	known, total := 0, 0
	for name := range seen {
		if declared[name] {
			continue
		}
		total++
		if imports[name] {
			known++
			continue
		}
		if _, obj := scope.LookupParent(name, pos); obj != nil {
			known++
		}
	}
	return total > 0 && known*2 >= total
}
