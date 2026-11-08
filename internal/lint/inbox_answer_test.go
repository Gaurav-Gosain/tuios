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

const allowAnswer = "//tuios:allow-answer"

// answerWriter is the one file that may write the Inbox's answer step.
const answerWriter = "internal/app/inbox_answer.go"

// TestOnlyInboxAnswerWritesTheAnswerStep is the answer flow lint. Where an
// answer to a held prompt stands (armed by a first risky press, asked and
// waiting for enter, asked in the peek, or the say-why line open) was four
// fields set and cleared by hand at fifteen places, so one step could be left
// standing behind the next. internal/app/inbox_answer.go now holds the step
// and its data as one value with one writer, setAnswer.
//
// Rejected in internal/app outside inbox_answer.go: an assignment to
// approvals.answer or to anything under it. Test files may stage a state. A
// deliberate exception carries a comment on its line or the line above:
//
//	//tuios:allow-answer <reason>
//
// It fails on injected writes; see TestAnswerLintCatchesADirectWrite.
func TestOnlyInboxAnswerWritesTheAnswerStep(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "internal/app", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no Go files in internal/app, so the lint reads nothing")
	}
	var found []string
	for _, file := range files {
		name := "internal/app/" + filepath.Base(file)
		if strings.HasSuffix(file, "_test.go") || name == answerWriter {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, answerWrites(t, name, src)...)
	}
	if len(found) > 0 {
		t.Errorf("the answer step is written outside %s; call answerArm, answerAsk, answerPeekAsk, answerReason or "+
			"answerEnd, or mark a deliberate one with %s <reason>:\n  %s", answerWriter, allowAnswer, strings.Join(found, "\n  "))
	}
}

// TestAnswerLintCatchesADirectWrite feeds the lint the forms it rejects and
// the forms it allows.
func TestAnswerLintCatchesADirectWrite(t *testing.T) {
	src := `package x

func a(m *OS) { m.Inbox.approvals.answer = inboxAnswer{} }
func b(m *OS) { m.Inbox.approvals.answer.confirm = nil }
func c(st *inboxApprovalState) { st.answer.armed.key = "x" }
func d(m *OS) { m.Inbox.approvals.answer.step = 0 } //tuios:allow-answer a test of the escape
func e(m *OS) { _ = m.Inbox.approvals.answer.step == answerIdle }
func f(m *OS) { m.Inbox.approvals.lastKey = "a" }
func g(m *OS) { m.answer = nil }
`
	got := answerWrites(t, "x.go", []byte(src))
	if len(got) != 3 {
		t.Fatalf("the lint found %d writes, want the 3 in a, b and c:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// answerWrites returns file:line for every write of the answer step in src:
// an assignment whose left side runs through approvals.answer, or through a
// value named st, as the approval code names &m.Inbox.approvals.
func answerWrites(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	allowed := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, allowAnswer) {
				line := fset.Position(c.Slash).Line
				allowed[line], allowed[line+1] = true, true
			}
		}
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			if !throughAnswer(lhs) {
				continue
			}
			if line := fset.Position(lhs.Pos()).Line; !allowed[line] {
				out = append(out, fmt.Sprintf("%s:%d: %s", name, line, exprString(lhs)))
			}
		}
		return true
	})
	return out
}

// throughAnswer reports whether e is answer, or a field under it, of
// approvals or of st.
func throughAnswer(e ast.Expr) bool {
	for {
		sel, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if sel.Sel.Name == "answer" {
			switch x := sel.X.(type) {
			case *ast.SelectorExpr:
				return x.Sel.Name == "approvals"
			case *ast.Ident:
				return x.Name == "st"
			}
			return false
		}
		e = sel.X
	}
}

// exprString is e as source, for the report.
func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	}
	return "?"
}
