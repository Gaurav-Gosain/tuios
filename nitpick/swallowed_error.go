package nitpick

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
	"golang.org/x/tools/go/types/typeutil"
)

// SwallowedError reports an error that the code drops with no word of why.
var SwallowedError = &analysis.Analyzer{
	Name: "swallowed_error",
	Doc: `report an error that is dropped with no reason

The rule reports two forms:

	_ = f()               // f returns an error
	if err != nil {}      // an empty block

The first form is reported only in a function that returns an error itself,
where the error had a place to go. Elsewhere _ = is how Go code says that
it drops an error on purpose. A comment on the line, or on the line above, gives the reason and silences
the finding. Some calls are not reported: calls that cannot fail, such as the
Write methods of bytes.Buffer and strings.Builder, output with fmt.Fprint
and its kin, and cleanup (Close, os.Remove, os.RemoveAll, Kill, Signal,
Release and the Set*Deadline methods).`,
	URL:      "https://pkg.go.dev/tuios.dev/nitpick#SwallowedError",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runSwallowedError,
}

func runSwallowedError(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	files := handWritten(pass)
	nodes := []ast.Node{(*ast.AssignStmt)(nil), (*ast.IfStmt)(nil)}
	ins.WithStack(nodes, func(n ast.Node, push bool, stack []ast.Node) bool {
		file := stack[0].(*ast.File)
		if !push || !files[file] {
			return true
		}
		switch n := n.(type) {
		case *ast.AssignStmt:
			if returnsErr(enclosingSig(pass.TypesInfo, stack)) {
				checkDiscard(pass, file, n)
			}
		case *ast.IfStmt:
			checkEmptyErrBlock(pass, file, n)
		}
		return true
	})
	return nil, nil
}

func checkDiscard(pass *analysis.Pass, f *ast.File, n *ast.AssignStmt) {
	if n.Tok != token.ASSIGN || len(n.Rhs) != 1 {
		return
	}
	for _, l := range n.Lhs {
		if id, ok := l.(*ast.Ident); !ok || id.Name != "_" {
			return
		}
	}
	call, ok := ast.Unparen(n.Rhs[0]).(*ast.CallExpr)
	if !ok || !returnsError(pass.TypesInfo, call) || cannotFail(pass.TypesInfo, call) {
		return
	}
	if commentNear(pass.Fset, f, n) {
		return
	}
	pass.Report(analysis.Diagnostic{
		Pos:     n.Pos(),
		End:     n.End(),
		Message: fmt.Sprintf("the error from %s is dropped: handle it, or say why in a comment", shorten(types.ExprString(call.Fun), 40)),
	})
}

func checkEmptyErrBlock(pass *analysis.Pass, f *ast.File, n *ast.IfStmt) {
	if len(n.Body.List) != 0 || n.Else != nil || errNotNil(pass.TypesInfo, n.Cond) == nil {
		return
	}
	if commentIn(f, n.Body.Lbrace, n.Body.Rbrace) {
		return
	}
	pass.Report(analysis.Diagnostic{
		Pos:     n.Pos(),
		End:     n.End(),
		Message: "the block for err != nil is empty, so the error is lost",
	})
}

// returnsErr reports whether a function with this signature returns an
// error as its last result.
func returnsErr(sig *types.Signature) bool {
	if sig == nil || sig.Results().Len() == 0 {
		return false
	}
	return isError(sig.Results().At(sig.Results().Len() - 1).Type())
}

// returnsError reports whether the last result of call is an error.
func returnsError(info *types.Info, call *ast.CallExpr) bool {
	if tv, ok := info.Types[call.Fun]; ok && tv.IsType() {
		return false // a conversion
	}
	t := info.TypeOf(call)
	if tuple, ok := t.(*types.Tuple); ok {
		if tuple.Len() == 0 {
			return false
		}
		t = tuple.At(tuple.Len() - 1).Type()
	}
	return isError(t)
}

// cannotFail reports whether the error of call cannot happen or has no
// use: a write to a buffer, output with fmt.Fprint, and cleanup such as
// Close, Remove and Kill, which code calls on a path where it has nothing
// left to do with a failure.
func cannotFail(info *types.Info, call *ast.CallExpr) bool {
	fn, ok := typeutil.Callee(info, call).(*types.Func)
	if !ok {
		return false
	}
	name := fn.FullName()
	if strings.HasPrefix(name, "(*bytes.Buffer).Write") || strings.HasPrefix(name, "(*strings.Builder).Write") ||
		strings.HasPrefix(name, "fmt.Fprint") {
		return true
	}
	switch name {
	case "os.Remove", "os.RemoveAll", "os.Unsetenv":
		return true
	}
	if fn.Signature().Recv() != nil {
		switch fn.Name() {
		case "Close", "Kill", "Signal", "Release", "SetDeadline", "SetReadDeadline", "SetWriteDeadline":
			return true
		}
	}
	return false
}
