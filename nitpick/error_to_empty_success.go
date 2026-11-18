package nitpick

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// ErrorToEmptySuccess reports an err != nil branch that returns empty
// values and a nil error, so the caller sees a success with nothing in it.
var ErrorToEmptySuccess = &analysis.Analyzer{
	Name: "error_to_empty_success",
	Doc: `report an error branch that returns empty values and a nil error

	if err != nil {
		return nil, nil
	}

The caller cannot tell the failure from an empty result. The rule reports a
return in the block when the function returns a value and an error, every
value is a zero literal (nil, "", 0, false or T{}) and the error is nil. A
function that returns only an error is left alone: there, return nil often
means "nothing to do". A block that uses err, or holds a comment, is left
alone too: it logs, records or explains the error.`,
	URL:      "https://pkg.go.dev/tuios.dev/nitpick#ErrorToEmptySuccess",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runErrorToEmptySuccess,
}

func runErrorToEmptySuccess(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	info := pass.TypesInfo
	files := handWritten(pass)
	ins.WithStack([]ast.Node{(*ast.IfStmt)(nil)}, func(n ast.Node, push bool, stack []ast.Node) bool {
		if !push {
			return true
		}
		f := stack[0].(*ast.File)
		if !files[f] {
			return false
		}
		ifs := n.(*ast.IfStmt)
		errExpr := errNotNil(info, ifs.Cond)
		if errExpr == nil {
			return true
		}
		sig := enclosingSig(info, stack)
		if sig == nil || sig.Results().Len() < 2 || !isError(sig.Results().At(sig.Results().Len()-1).Type()) {
			return true
		}
		if mentions(info, ifs.Body, errExpr) || commentIn(f, ifs.Body.Lbrace, ifs.Body.Rbrace) {
			return true
		}
		for _, st := range ifs.Body.List {
			ret, ok := st.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != sig.Results().Len() || !allZero(info, ret.Results) || !isNil(info, ret.Results[len(ret.Results)-1]) {
				continue
			}
			pass.Report(analysis.Diagnostic{
				Pos:     ret.Pos(),
				End:     ret.End(),
				Message: "the err != nil branch returns empty values and a nil error, so the caller sees a success",
			})
		}
		return true
	})
	return nil, nil
}

// mentions reports whether body refers to the error that the if checks.
func mentions(info *types.Info, body ast.Node, errExpr ast.Expr) bool {
	var obj types.Object
	if id, ok := ast.Unparen(errExpr).(*ast.Ident); ok {
		obj = info.ObjectOf(id)
	}
	want := types.ExprString(errExpr)
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.Ident:
			if obj != nil && info.ObjectOf(n) == obj {
				found = true
			}
		case *ast.SelectorExpr:
			if obj == nil && types.ExprString(n) == want {
				found = true
			}
		}
		return true
	})
	return found
}

// allZero reports whether every expression is a zero literal.
func allZero(info *types.Info, results []ast.Expr) bool {
	for _, e := range results {
		if !isZero(info, e) {
			return false
		}
	}
	return true
}

func isZero(info *types.Info, e ast.Expr) bool {
	e = ast.Unparen(e)
	if isNil(info, e) {
		return true
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.STRING:
			return e.Value == `""` || e.Value == "``"
		case token.INT, token.FLOAT:
			return e.Value == "0" || e.Value == "0.0"
		}
	case *ast.Ident:
		return e.Name == "false" && info.ObjectOf(e) == types.Universe.Lookup("false")
	case *ast.CompositeLit:
		return len(e.Elts) == 0
	}
	return false
}
