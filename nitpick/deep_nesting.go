package nitpick

import (
	"fmt"
	"go/ast"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// MaxNesting is the deepest control nesting that DeepNesting allows.
const MaxNesting = 4

// DeepNesting reports a function whose control statements nest five deep
// or more.
var DeepNesting = &analysis.Analyzer{
	Name: "deep_nesting",
	Doc: `report a function with control nesting of 5 or more

Each if, for, switch, type switch and select adds a level. An else if stays
on the level of its if, and a case adds nothing beyond its switch. A function
literal starts again at 0. The rule reports once per function, at the first
statement on level 5.`,
	URL:      "https://pkg.go.dev/tuios.dev/nitpick#DeepNesting",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      runDeepNesting,
}

func runDeepNesting(pass *analysis.Pass) (any, error) {
	ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	files := handWritten(pass)
	var file *ast.File
	ins.Preorder([]ast.Node{(*ast.File)(nil), (*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}, func(n ast.Node) {
		var body *ast.BlockStmt
		name := "the function literal"
		switch n := n.(type) {
		case *ast.File:
			file = n
			return
		case *ast.FuncDecl:
			body, name = n.Body, n.Name.Name
		case *ast.FuncLit:
			body = n.Body
		}
		if body == nil || !files[file] {
			return
		}
		var d nesting
		d.list(body.List, 0)
		if d.first == nil {
			return
		}
		pass.Report(analysis.Diagnostic{
			Pos:     d.first.Pos(),
			End:     d.first.End(),
			Message: fmt.Sprintf("control nesting in %s reaches %d levels, and the limit is %d: return early, or move the inner part to a function", name, d.max, MaxNesting),
		})
	})
	return nil, nil
}

type nesting struct {
	max   int
	first ast.Stmt
}

func (d *nesting) enter(s ast.Stmt, depth int) {
	d.max = max(d.max, depth)
	if depth > MaxNesting && d.first == nil {
		d.first = s
	}
}

func (d *nesting) list(stmts []ast.Stmt, depth int) {
	for _, s := range stmts {
		d.stmt(s, depth)
	}
}

func (d *nesting) stmt(s ast.Stmt, depth int) {
	switch s := s.(type) {
	case *ast.IfStmt:
		d.enter(s, depth+1)
		d.list(s.Body.List, depth+1)
		switch e := s.Else.(type) {
		case *ast.IfStmt:
			d.stmt(e, depth)
		case *ast.BlockStmt:
			d.list(e.List, depth+1)
		}
	case *ast.ForStmt:
		d.enter(s, depth+1)
		d.list(s.Body.List, depth+1)
	case *ast.RangeStmt:
		d.enter(s, depth+1)
		d.list(s.Body.List, depth+1)
	case *ast.SwitchStmt:
		d.enter(s, depth+1)
		d.clauses(s.Body, depth+1)
	case *ast.TypeSwitchStmt:
		d.enter(s, depth+1)
		d.clauses(s.Body, depth+1)
	case *ast.SelectStmt:
		d.enter(s, depth+1)
		d.clauses(s.Body, depth+1)
	case *ast.BlockStmt:
		d.list(s.List, depth)
	case *ast.LabeledStmt:
		d.stmt(s.Stmt, depth)
	}
}

func (d *nesting) clauses(body *ast.BlockStmt, depth int) {
	for _, c := range body.List {
		switch c := c.(type) {
		case *ast.CaseClause:
			d.list(c.Body, depth)
		case *ast.CommClause:
			d.list(c.Body, depth)
		}
	}
}
