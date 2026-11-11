package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cardWriter is the one file that may write a task card's lifecycle fields.
const cardWriter = "task_move.go"

// cardFields are the lifecycle fields of a TaskState.
var cardFields = map[string]bool{
	"Stage": true, "StageAt": true, "Holder": true, "HeldAt": true, "HolderName": true, "HolderHarness": true,
	"Dispatched": true, "Blocked": true, "Attempts": true, "Note": true, "Work": true, "Verify": true,
	"Attachments": true,
}

// TestOnlyTaskMoveWritesACardsLifecycle is the task card lint. A card's
// stage, holder, dispatch mark, block, attempts, verify record and work had
// 22 writers in 7 files, and each held its own copy of the rules: which
// moves lift a block, what the dispatch mark becomes, where a verify record
// is made. A release that did not know about a block left an errored card
// stuck (state machine plan S4). internal/session/task_move.go now holds
// every write: TaskState.apply, through Session.moveCard.
//
// Rejected in internal/session outside task_move.go: an assignment or an
// increment of a lifecycle field on a TaskState, as the code names one: a
// parameter or receiver typed TaskState or *TaskState, a variable made from
// an element of a Tasks slice (tk := &st.Tasks[i], or a range over Tasks),
// or the element itself (st.Tasks[i].Stage = ...). A composite literal that
// makes a card is allowed. Test files may set fields to stage a state.
//
// It fails on injected writes; see TestTaskMoveLintCatchesADirectWrite.
func TestOnlyTaskMoveWritesACardsLifecycle(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "internal", "session", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files in internal/session, so the lint reads nothing")
	}
	var found []string
	for _, file := range files {
		name := filepath.Base(file)
		if strings.HasSuffix(name, "_test.go") || name == cardWriter {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, cardWrites(t, name, src)...)
	}
	if len(found) > 0 {
		t.Errorf("a task card's lifecycle is written outside %s; ask for the move with Session.moveCard:\n  %s",
			cardWriter, strings.Join(found, "\n  "))
	}
}

// TestTaskMoveLintCatchesADirectWrite feeds the lint the forms it rejects and
// the forms it allows, so a lint that quietly stopped matching cannot pass
// the check above by finding nothing.
func TestTaskMoveLintCatchesADirectWrite(t *testing.T) {
	src := `package x

func (tk *TaskState) a()            { tk.Stage = "done" }
func b(tk *TaskState)                { tk.Holder, tk.HeldAt = "", 0 }
func c(st *SessionState)             { tk := &st.Tasks[0]; tk.Attempts++ }
func d(st *SessionState)             { st.Tasks[1].Blocked = nil }
func e(st *SessionState)             { for i := range st.Tasks { st.Tasks[i].Verify = nil } }
func f(st *SessionState)             { for _, tk := range st.Tasks { tk.Note = "x" } }
func g(row *TaskRow)                 { row.Stage, row.Note = "todo", "" }
func h() TaskState                   { return TaskState{Stage: "todo"} }
func i(tk *TaskState)                { tk.Fan = "x" }
`
	got := cardWrites(t, "x.go", []byte(src))
	if len(got) != 7 {
		t.Fatalf("the lint found %d writes, want the 7 in a to f (b writes two):\n%s", len(got), strings.Join(got, "\n"))
	}
}

// cardWrites returns file:line for every write of a card's lifecycle field
// in src.
func cardWrites(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var out []string
	for _, decl := range f.Decls {
		cards := map[string]bool{}
		// Names typed TaskState, in the signature and in any func literal.
		ast.Inspect(decl, func(n ast.Node) bool {
			var fields []*ast.FieldList
			switch n := n.(type) {
			case *ast.FuncDecl:
				fields = []*ast.FieldList{n.Recv, n.Type.Params}
			case *ast.FuncLit:
				fields = []*ast.FieldList{n.Type.Params}
			case *ast.AssignStmt:
				// tk := &st.Tasks[i] and tk := st.Tasks[i].
				for i, rhs := range n.Rhs {
					if i < len(n.Lhs) && isTasksElem(rhs) {
						if id, ok := n.Lhs[i].(*ast.Ident); ok {
							cards[id.Name] = true
						}
					}
				}
			case *ast.RangeStmt:
				if sel, ok := n.X.(*ast.SelectorExpr); ok && sel.Sel.Name == "Tasks" {
					if id, ok := n.Value.(*ast.Ident); ok {
						cards[id.Name] = true
					}
				}
			}
			for _, fl := range fields {
				if fl == nil {
					continue
				}
				for _, field := range fl.List {
					if isTaskStateType(field.Type) {
						for _, id := range field.Names {
							cards[id.Name] = true
						}
					}
				}
			}
			return true
		})
		written := func(e ast.Expr) bool {
			sel, ok := e.(*ast.SelectorExpr)
			if !ok || !cardFields[sel.Sel.Name] {
				return false
			}
			if id, ok := sel.X.(*ast.Ident); ok {
				return cards[id.Name]
			}
			return isTasksElem(sel.X)
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if written(lhs) {
						out = append(out, fmt.Sprintf("%s:%d: %s", name, fset.Position(n.Pos()).Line, lhs.(*ast.SelectorExpr).Sel.Name))
					}
				}
			case *ast.IncDecStmt:
				if written(n.X) {
					out = append(out, fmt.Sprintf("%s:%d: %s", name, fset.Position(n.Pos()).Line, n.X.(*ast.SelectorExpr).Sel.Name))
				}
			}
			return true
		})
	}
	return out
}

// isTaskStateType reports whether e is TaskState or *TaskState.
func isTaskStateType(e ast.Expr) bool {
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "TaskState"
}

// isTasksElem reports whether e is X.Tasks[i] or &X.Tasks[i].
func isTasksElem(e ast.Expr) bool {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == token.AND {
		e = u.X
	}
	idx, ok := e.(*ast.IndexExpr)
	if !ok {
		return false
	}
	sel, ok := idx.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Tasks"
}
