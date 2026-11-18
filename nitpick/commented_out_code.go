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
must be a call to count as a statement, so "0 <= i && i < n" is prose. The
rule is off by default.`,
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

// indented reports whether a line comment starts with a tab or two
// spaces: the form of an example, not of a line that an editor commented
// out.
func indented(c string) bool {
	t := strings.TrimPrefix(c, "//")
	return strings.HasPrefix(t, "\t") || strings.HasPrefix(t, "  ") || strings.HasPrefix(t, " \t")
}

// docComments returns the doc comments of the declarations of f.
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

// codeText returns the text of a line comment that is not a directive or
// a note.
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
	// A bare statement inside a block of commented code: "return", "break".
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

// parseCode parses the lines as Go statements or declarations, and
// returns nil when they are not code.
func parseCode(run []*ast.Comment) *ast.File {
	var b strings.Builder
	for _, c := range run {
		t, _ := codeText(c.Text)
		b.WriteString(t)
		b.WriteByte('\n')
	}
	text := b.String()
	// A lone keyword such as "fallthrough" marks a case; it is not code.
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

// realStatements reports whether the statements are ones a person writes:
// an expression statement must be a call or a receive, an assignment must
// assign to a variable, and a label is prose ("Have: i++"). The parser
// accepts "a < b" and "f(x) = y" as statements, and the type checker would
// not.
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

// namesResolve reports whether most names in the code are names at pos in
// the package: dead code uses the variables around it, and a formula or a
// sketch of syntax uses names of its own. A selector's package, such as
// fmt in fmt.Println, counts when the file imports it.
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
			return false // the field or method name is not a name in scope
		case *ast.KeyValueExpr:
			ast.Inspect(n.Value, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok {
					seen[id.Name] = true
				}
				return true
			})
			return false
		case *ast.Ident:
			if n.Name != "_" && n.Name != "p" { // p is the wrapper package
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
