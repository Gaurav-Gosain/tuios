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

// TestTaskEventsArePublishedInsideTheirChange is the task event lint. A task
// verb changed its card under the session's state lock and published the
// event after the lock was released and the state had been written to the
// clients, so two changes to one card could publish in the reverse order, and
// a board, which replaces a card's row with each event, stayed on the stage
// the card had left. A task event now leaves inside the state change that
// made it, as the lifecycle events do (Session.emitLifecycleLocked), so the
// events leave in the order of the changes.
//
// Rejected in internal/session's non-test files:
//   - a streamEvent composite literal with a Task key, or an assignment to a
//     selector named Task (ev.Task = &row), outside the function taskEvent,
//     which is the one place that builds a task event;
//   - a call of taskEvent whose innermost enclosing function literal is not an
//     argument of a call of mutateState or mutateStateLocked, or that has a
//     defer or go statement between it and that literal: so a call after the
//     change, in a literal passed to changeTask, in a nested literal, in a
//     defer or in a go. A call inside publishReleased is allowed; it runs
//     from the session's event sink, already under the state lock;
//   - a call of publishReleased in any file but daemon.go;
//   - a function or a call named publishTask.
//
// What it cannot see: that the call is the change's last step and on its
// success path, and a call made through a method value or a function
// variable. internal/session TestTaskEventsLeaveInTheOrderOfTheirChanges and
// reading hold those. A lint that quietly stopped matching would pass here by
// finding nothing; TestTaskEventLintCatchesAPublishAfterTheChange feeds it
// every form it rejects and the forms it allows.
func TestTaskEventsArePublishedInsideTheirChange(t *testing.T) {
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
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, taskEventFaults(t, name, src)...)
	}
	if len(found) > 0 {
		t.Errorf("a task event is published outside the state change that made it; call Daemon.taskEvent inside the mutateState closure, as its last step:\n  %s",
			strings.Join(found, "\n  "))
	}
}

// TestTaskEventLintCatchesAPublishAfterTheChange feeds the lint the forms it
// rejects, each on a line marked want, and the forms it allows, so a lint
// that quietly stopped matching cannot pass the check above by finding
// nothing.
func TestTaskEventLintCatchesAPublishAfterTheChange(t *testing.T) {
	src := `package x

func (d *Daemon) taskEvent(sess *Session, typ, action, window, reason string, row TaskRow) {
	d.events.publish(streamEvent{Type: typ, Session: sess.Name(), Task: &row})
	ev := streamEvent{Type: typ}
	ev.Task = &row
}

func (d *Daemon) publishReleased(sess *Session, rows []TaskRow, reason string) {
	for _, row := range rows {
		d.taskEvent(sess, "task-changed", "released", "", reason, row)
	}
}

func allowed(d *Daemon, sess *Session, row TaskRow) {
	_ = sess.mutateState(func(st *SessionState) error {
		d.taskEvent(sess, "task-changed", "added", "", "", row)
		return nil
	})
	_, _ = sess.mutateStateLocked(func(st *SessionState) error {
		for range st.Tasks {
			if row.ID != "" {
				d.taskEvent(sess, "task-changed", "staged", "", "", row)
			}
		}
		return nil
	})
	d.events.publish(streamEvent{Type: "layout-changed"})
	var other streamEvent
	_ = other.Task == nil
}

func rejected(d *Daemon, sess *Session, row TaskRow) {
	d.events.publish(streamEvent{Type: "task-changed", Task: &row}) // want
	ev := streamEvent{Type: "task-changed"}
	ev.Task = &row // want
	_ = sess.mutateState(func(st *SessionState) error { return nil })
	d.taskEvent(sess, "task-changed", "staged", "", "", row) // want
	d.changeTask(sess, "t1", func(st *SessionState, tk *TaskState, now int64) (string, *verbError) {
		d.taskEvent(sess, "task-changed", "staged", "", "", row) // want
		return "staged", nil
	})
	_ = sess.mutateState(func(st *SessionState) error {
		func() {
			d.taskEvent(sess, "task-changed", "staged", "", "", row) // want
		}()
		defer d.taskEvent(sess, "task-changed", "staged", "", "", row) // want
		go d.taskEvent(sess, "task-changed", "staged", "", "", row) // want
		return nil
	})
	d.publishReleased(sess, nil, "restart") // want
	d.publishTask(sess, "task-changed", "added", "", "", row) // want
}

func (d *Daemon) publishTask() {} // want
`
	want := map[int]bool{}
	for i, line := range strings.Split(src, "\n") {
		if strings.HasSuffix(line, "// want") {
			want[i+1] = true
		}
	}
	got := map[int]int{}
	faults := taskEventFaults(t, "x.go", []byte(src))
	for _, f := range faults {
		var line int
		if _, err := fmt.Sscanf(strings.TrimPrefix(f, "x.go:"), "%d:", &line); err != nil {
			t.Fatalf("fault %q has no line: %v", f, err)
		}
		got[line]++
	}
	for line := range want {
		if got[line] != 1 {
			t.Errorf("line %d: the lint found %d faults, want 1", line, got[line])
		}
	}
	for line, n := range got {
		if !want[line] {
			t.Errorf("line %d: the lint found %d faults in a form it allows", line, n)
		}
	}
	if t.Failed() {
		t.Fatalf("the lint found:\n  %s", strings.Join(faults, "\n  "))
	}

	sink := `package x

func (d *Daemon) sink(s *Session, ev SessionEvent) {
	d.publishReleased(s, []TaskRow{*ev.taskRow}, ev.taskReason)
}
`
	if got := taskEventFaults(t, "daemon.go", []byte(sink)); len(got) != 0 {
		t.Errorf("the lint rejects publishReleased called from daemon.go:\n  %s", strings.Join(got, "\n  "))
	}
}

// taskEventFaults returns file:line and the rule for every task event in src
// published outside the change that made it.
func taskEventFaults(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	var out []string
	fault := func(n ast.Node, why string) {
		out = append(out, fmt.Sprintf("%s:%d: %s", name, fset.Position(n.Pos()).Line, why))
	}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		fn := ""
		for _, s := range stack {
			if fd, ok := s.(*ast.FuncDecl); ok {
				fn = fd.Name.Name
			}
		}
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Name.Name == "publishTask" {
				fault(n, "a function named publishTask")
			}
		case *ast.CompositeLit:
			if fn != "taskEvent" && taskEventLiteral(n) {
				fault(n, "a streamEvent with a Task built outside taskEvent")
			}
		case *ast.AssignStmt:
			if fn == "taskEvent" {
				break
			}
			for _, lhs := range n.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "Task" {
					fault(n, "a Task set outside taskEvent")
				}
			}
		case *ast.CallExpr:
			switch taskEventCallName(n) {
			case "publishTask":
				fault(n, "a call of publishTask")
			case "publishReleased":
				if name != "daemon.go" {
					fault(n, "publishReleased called outside the session's event sink in daemon.go")
				}
			case "taskEvent":
				if fn != "publishReleased" && !taskEventInItsChange(stack) {
					fault(n, "taskEvent called outside a function literal passed to mutateState or mutateStateLocked")
				}
			}
		}
		return true
	})
	return out
}

// taskEventLiteral reports whether lit is a streamEvent with a Task key.
func taskEventLiteral(lit *ast.CompositeLit) bool {
	if id, ok := lit.Type.(*ast.Ident); !ok || id.Name != "streamEvent" {
		return false
	}
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Task" {
				return true
			}
		}
	}
	return false
}

// taskEventCallName is the name a call calls: f in f(...) and x.f(...).
func taskEventCallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// taskEventInItsChange reports whether the call on top of stack has as its
// innermost enclosing function literal an argument of a call of mutateState
// or mutateStateLocked, with no defer or go statement between the two.
func taskEventInItsChange(stack []ast.Node) bool {
	for j := len(stack) - 2; j > 0; j-- {
		lit, ok := stack[j].(*ast.FuncLit)
		if !ok {
			continue
		}
		for _, n := range stack[j+1 : len(stack)-1] {
			switch n.(type) {
			case *ast.DeferStmt, *ast.GoStmt:
				return false
			}
		}
		call, ok := stack[j-1].(*ast.CallExpr)
		if !ok {
			return false
		}
		if name := taskEventCallName(call); name != "mutateState" && name != "mutateStateLocked" {
			return false
		}
		for _, arg := range call.Args {
			if arg == lit {
				return true
			}
		}
		return false
	}
	return false
}
