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

// daemonDirs are the packages the daemon runs code from that start child
// processes. The list is what `GOOS=windows go list -deps ./internal/session`
// reaches inside this module, less the packages that start no process; add a
// package here when the daemon starts importing one that does.
var daemonDirs = []string{
	"internal/agentproto",
	"internal/capture",
	"internal/federation",
	"internal/ghpr",
	"internal/gitstate",
	"internal/herdrplugin",
	"internal/hooks",
	"internal/review",
	"internal/session",
	"internal/winpath",
	"internal/worktree",
}

const allowExec = "//tuios:allow-exec"

// TestDaemonChildrenGoThroughChildproc is the child process lint. The daemon
// runs without a console on Windows, so a console program it starts gets a
// console of its own, and with Windows Terminal as the default terminal that
// console is a window on the person's screen (issue #656). childproc.Command
// and childproc.CommandContext start the program with no window; exec.Command,
// exec.CommandContext and an exec.Cmd literal do not, so they are rejected in
// daemon code.
//
// A process that sets its own creation flags, or a pane program that
// ptyspawn starts on a pseudo console, carries a comment on its line or the
// line above:
//
//	//tuios:allow-exec <reason>
//
// It fails on an injected call; see TestChildprocLintCatchesEachForm.
func TestDaemonChildrenGoThroughChildproc(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, dir := range daemonDirs {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := dir + "/" + name
			src, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatal(err)
			}
			found = append(found, bareExecs(t, rel, src)...)
		}
	}
	if len(found) > 0 {
		t.Errorf("daemon code starts a process without childproc; use childproc.Command or "+
			"childproc.CommandContext, or mark a deliberate one with %s <reason>:\n  %s",
			allowExec, strings.Join(found, "\n  "))
	}
}

// TestChildprocLintCatchesEachForm feeds the lint one file holding each form
// it rejects, and each allowed form, so a lint that quietly stopped matching
// cannot pass the check above by finding nothing.
func TestChildprocLintCatchesEachForm(t *testing.T) {
	src := `package x

func f() {
	_ = exec.Command("git", "status")
	_ = exec.CommandContext(ctx, "git", "status")
	_ = &exec.Cmd{Path: "git"}
	_ = exec.Cmd{}

	_ = childproc.Command("git", "status")
	_ = childproc.CommandContext(ctx, "git", "status")
	_, _ = exec.LookPath("git")
	_ = exec.Command("cmd") //tuios:allow-exec a test of the escape
	//tuios:allow-exec the line above counts too
	_ = exec.CommandContext(ctx, "cmd")
}
`
	got := bareExecs(t, "x.go", []byte(src))
	if len(got) != 4 {
		t.Fatalf("the lint found %d calls, want the 4 rejected forms:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// bareExecs returns file:line: form for every rejected process start in src.
func bareExecs(t *testing.T, name string, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	allowed := map[int]bool{}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, allowExec) {
				line := fset.Position(c.Slash).Line
				allowed[line], allowed[line+1] = true, true
			}
		}
	}
	var out []string
	report := func(n ast.Node, what string) {
		line := fset.Position(n.Pos()).Line
		if !allowed[line] {
			out = append(out, fmt.Sprintf("%s:%d: %s", name, line, what))
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if pkg, fn := selector(v.Fun); pkg == "exec" && (fn == "Command" || fn == "CommandContext") {
				report(v, "exec."+fn)
			}
		case *ast.CompositeLit:
			if pkg, typ := selector(v.Type); pkg == "exec" && typ == "Cmd" {
				report(v, "exec.Cmd literal")
			}
		}
		return true
	})
	return out
}
