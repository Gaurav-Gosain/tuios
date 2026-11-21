package nitpick

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var errorType = types.Universe.Lookup("error").Type()

func isError(t types.Type) bool {
	if t == nil {
		return false
	}
	return types.Identical(t, errorType) || types.Implements(t, errorType.Underlying().(*types.Interface))
}

func isNil(info *types.Info, e ast.Expr) bool {
	tv, ok := info.Types[e]
	return ok && tv.IsNil()
}

func errNotNil(info *types.Info, cond ast.Expr) ast.Expr {
	b, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ {
		return nil
	}
	x, y := b.X, b.Y
	if isNil(info, x) {
		x, y = y, x
	}
	if !isNil(info, y) || !isError(info.TypeOf(x)) {
		return nil
	}
	return x
}

func handWritten(pass *analysis.Pass) map[*ast.File]bool {
	out := make(map[*ast.File]bool, len(pass.Files))
	for _, f := range pass.Files {
		if !ast.IsGenerated(f) {
			out[f] = true
		}
	}
	return out
}

func isTestFile(pass *analysis.Pass, f *ast.File) bool {
	return strings.HasSuffix(pass.Fset.File(f.FileStart).Name(), "_test.go")
}

func isReason(g *ast.CommentGroup) bool {
	return !strings.HasPrefix(g.List[0].Text, "// want ")
}

func commentIn(f *ast.File, from, to token.Pos) bool {
	for _, g := range f.Comments {
		if g.Pos() >= from && g.Pos() <= to && isReason(g) {
			return true
		}
	}
	return false
}

func commentNear(fset *token.FileSet, f *ast.File, n ast.Node) bool {
	tf := fset.File(n.Pos())
	first, last := tf.Line(n.Pos()), tf.Line(n.End())
	for _, g := range f.Comments {
		if !isReason(g) || tf != fset.File(g.Pos()) {
			continue
		}
		start, end := tf.Line(g.Pos()), tf.Line(g.End())
		if end == first-1 || (start >= first && start <= last) {
			return true
		}
	}
	return false
}

func enclosingSig(info *types.Info, stack []ast.Node) *types.Signature {
	for _, n := range slices.Backward(stack) {
		switch n := n.(type) {
		case *ast.FuncLit:
			sig, _ := info.TypeOf(n).(*types.Signature)
			return sig
		case *ast.FuncDecl:
			if obj, ok := info.Defs[n.Name].(*types.Func); ok {
				return obj.Signature()
			}
			return nil
		}
	}
	return nil
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := ast.Unparen(e).(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s := lit.Value
	if len(s) >= 2 {
		s = s[1 : len(s)-1]
	}
	return s, true
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
