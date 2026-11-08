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

// transferDir is the package that holds the file transfer jobs.
const transferDir = "internal/session"

// TestOnlyToWritesATransferState is the transfer state lint. A job's state
// had 20 writers in transfer.go and transfer_journal.go, and the attempt's
// own writers did not look for a final state: a revoke that set the job
// failed was overwritten by the attempt's verifying or done, and a job left
// in a state that is not final holds its destination. transferJob.to now
// refuses to leave done, failed or cancelled, and is the one writer, with
// reopen for the retry the person asks for.
//
// Rejected in internal/session outside to and reopen: an assignment to a
// state field in a transfer*.go file, and an assignment of a transfer state
// constant (transferRunning and the rest) to any state field. Test files
// may set a state to stage one.
//
// It fails on injected writes; see TestTransferStateLintCatchesADirectWrite.
func TestOnlyToWritesATransferState(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, transferDir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files in %s, so the lint reads nothing", transferDir)
	}
	var found []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, transferStateWrites(t, filepath.Base(file), src)...)
	}
	if len(found) > 0 {
		t.Errorf("a transfer job's state is written outside transferJob.to; call j.to(state), which "+
			"refuses to leave a final state, or reopen for a retry:\n  %s", strings.Join(found, "\n  "))
	}
}

// TestTransferStateLintCatchesADirectWrite feeds the lint the forms it
// rejects and the forms it allows, so a lint that quietly stopped matching
// cannot pass the check above by finding nothing.
func TestTransferStateLintCatchesADirectWrite(t *testing.T) {
	src := `package x

func (j *transferJob) to(s string) bool { j.state = s; return true }
func (j *transferJob) reopen()          { j.state = transferQueued }
func a(j *transferJob)                  { j.state = transferDone }
func b(m *transferManager)              { m.set(func(j *transferJob) { j.state = transferPaused }) }
func c(e *entry)                        { e.state = queueWaiting }
`
	if got := transferStateWrites(t, "other.go", []byte(src)); len(got) != 2 {
		t.Fatalf("in other.go the lint found %d writes, want the 2 in a and b:\n%s", len(got), strings.Join(got, "\n"))
	}
	if got := transferStateWrites(t, "transfer_x.go", []byte(src)); len(got) != 3 {
		t.Fatalf("in transfer_x.go the lint found %d writes, want the 3 in a, b and c:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// transferStateWrites returns file:line for every write of a transfer job's
// state in src outside to and reopen.
func transferStateWrites(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	transferFile := strings.HasPrefix(name, "transfer")
	var out []string
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil && (fn.Name.Name == "to" || fn.Name.Name == "reopen") {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for i, lhs := range as.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "state" {
					continue
				}
				constant := false
				if i < len(as.Rhs) {
					if id, ok := as.Rhs[i].(*ast.Ident); ok && strings.HasPrefix(id.Name, "transfer") {
						constant = true
					}
				}
				if transferFile || constant {
					out = append(out, fmt.Sprintf("%s:%d: state =", name, fset.Position(as.Pos()).Line))
				}
			}
			return true
		})
	}
	return out
}
