package lint

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	modulePath = "github.com/Gaurav-Gosain/tuios"
	allowExec  = "//tuios:allow-exec"
)

// daemonRoot is the package the daemon runs from. Every package of this
// module it imports, on any of daemonOSes, is daemon code.
const daemonRoot = "internal/session"

// daemonOSes are the platforms whose build constraints decide which files and
// imports count. Windows is the one this lint is for; the others keep a file
// that only they build from slipping past it.
var daemonOSes = []string{"windows", "linux", "darwin"}

// notDaemon are packages the walk reaches that are exempt: childproc is the
// one place a process is started with exec directly.
var notDaemon = map[string]bool{"internal/childproc": true}

// TestDaemonChildrenGoThroughChildproc is the child process lint. The daemon
// runs without a console window on Windows, and a console program it starts
// without CREATE_NO_WINDOW can get a console of its own. With Windows
// Terminal as the default terminal that console is a window on the person's
// screen (issue #656). childproc.Command and childproc.CommandContext start
// the program with no window, so daemon code starts processes through them.
//
// Rejected in daemon code: exec.Command, exec.CommandContext, an exec.Cmd
// literal and new(exec.Cmd), under any name os/exec is imported as, a dot
// import of os/exec, os.StartProcess, and syscall.StartProcess and ForkExec.
// A deliberate exception, such as a pane program that ptyspawn starts on a
// pseudo console, carries a comment on its line or the line above:
//
//	//tuios:allow-exec <reason>
//
// The package list is the daemon's own import graph, not a hand list, so a
// package the daemon starts importing is checked from that commit on. It
// fails on an injected call; see TestChildprocLintCatchesEachForm.
func TestDaemonChildrenGoThroughChildproc(t *testing.T) {
	root := repoRoot(t)
	dirs := daemonPackages(t, root)
	if !slices.Contains(dirs, "internal/worktree") {
		t.Fatalf("the import walk from %s did not reach internal/worktree, so it is broken: %v", daemonRoot, dirs)
	}
	var found []string
	for _, dir := range dirs {
		for _, name := range daemonFiles(t, root, dir) {
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

// daemonPackages walks the imports of daemonRoot inside this module and
// returns each package's directory relative to root. go list would do the
// same, but the test binary runs with HOME moved to a temporary folder, where
// the go command has no module cache.
func daemonPackages(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{daemonRoot: true}
	queue := []string{daemonRoot}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		for _, goos := range daemonOSes {
			pkg := importDir(t, root, dir, goos)
			if pkg == nil {
				continue
			}
			for _, imp := range pkg.Imports {
				rel, ok := strings.CutPrefix(imp, modulePath+"/")
				if !ok || seen[rel] {
					continue
				}
				seen[rel] = true
				queue = append(queue, rel)
			}
		}
	}
	var out []string
	for dir := range seen {
		if !notDaemon[dir] {
			out = append(out, dir)
		}
	}
	slices.Sort(out)
	return out
}

// daemonFiles are the non-test files of dir that any of daemonOSes builds.
func daemonFiles(t *testing.T, root, dir string) []string {
	t.Helper()
	var names []string
	for _, goos := range daemonOSes {
		if pkg := importDir(t, root, dir, goos); pkg != nil {
			names = append(names, pkg.GoFiles...)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// importDir reads the package in dir as goos builds it, or returns nil when
// goos builds no file of it.
func importDir(t *testing.T, root, dir, goos string) *build.Package {
	t.Helper()
	ctx := build.Default
	ctx.GOOS, ctx.GOARCH = goos, "amd64"
	pkg, err := ctx.ImportDir(filepath.Join(root, dir), 0)
	if err != nil {
		if _, none := errors.AsType[*build.NoGoError](err); none {
			return nil
		}
		t.Fatalf("read %s for %s: %v", dir, goos, err)
	}
	return pkg
}

// TestChildprocLintCatchesEachForm feeds the lint files holding each form it
// rejects, and each allowed form, so a lint that quietly stopped matching
// cannot pass the check above by finding nothing.
func TestChildprocLintCatchesEachForm(t *testing.T) {
	src := `package x

import (
	"os"
	"os/exec"
	"syscall"
)

func f() {
	_ = exec.Command("git", "status")
	_ = exec.CommandContext(ctx, "git", "status")
	_ = &exec.Cmd{Path: "git"}
	_ = exec.Cmd{}
	_ = new(exec.Cmd)
	_, _ = os.StartProcess("git", nil, nil)
	_, _, _ = syscall.StartProcess("git", nil, nil)
	_, _ = syscall.ForkExec("git", nil, nil)

	_ = childproc.Command("git", "status")
	_ = childproc.CommandContext(ctx, "git", "status")
	_, _ = exec.LookPath("git")
	_ = new(bytes.Buffer)
	_ = exec.Command("cmd") //tuios:allow-exec a test of the escape
	//tuios:allow-exec the line above counts too
	_ = exec.CommandContext(ctx, "cmd")
}
`
	if got := bareExecs(t, "x.go", []byte(src)); len(got) != 8 {
		t.Fatalf("the lint found %d calls, want the 8 rejected forms:\n%s", len(got), strings.Join(got, "\n"))
	}

	aliased := `package x

import (
	run "os/exec"
	sys "os"
)

func f() {
	_ = run.Command("git")
	_ = new(run.Cmd)
	_, _ = sys.StartProcess("git", nil, nil)
	_ = exec.Command("git") // exec here is not os/exec
}
`
	if got := bareExecs(t, "x.go", []byte(aliased)); len(got) != 3 {
		t.Fatalf("the lint found %d calls under aliased imports, want 3:\n%s", len(got), strings.Join(got, "\n"))
	}

	dot := `package x

import . "os/exec"

func f() { _ = Command("git") }
`
	if got := bareExecs(t, "x.go", []byte(dot)); len(got) != 1 {
		t.Fatalf("the lint found %d problems in a dot import of os/exec, want 1:\n%s", len(got), strings.Join(got, "\n"))
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

	// The name each package is imported under in this file. A file with no
	// import of it has no name for it, so an identifier that happens to be
	// called exec is not mistaken for the package.
	names := map[string]string{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		local := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if p == "os/exec" && local == "." {
			report(imp, "dot import of os/exec")
			continue
		}
		names[p] = local
	}
	is := func(e ast.Expr, path string, sels ...string) (string, bool) {
		pkg, sel := selector(e)
		if local, ok := names[path]; !ok || pkg != local || !slices.Contains(sels, sel) {
			return "", false
		}
		return path + "." + sel, true
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if what, ok := is(v.Fun, "os/exec", "Command", "CommandContext"); ok {
				report(v, what)
			}
			if what, ok := is(v.Fun, "os", "StartProcess"); ok {
				report(v, what)
			}
			if what, ok := is(v.Fun, "syscall", "StartProcess", "ForkExec"); ok {
				report(v, what)
			}
			if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "new" && len(v.Args) == 1 {
				if _, ok := is(v.Args[0], "os/exec", "Cmd"); ok {
					report(v, "new(exec.Cmd)")
				}
			}
		case *ast.CompositeLit:
			if _, ok := is(v.Type, "os/exec", "Cmd"); ok {
				report(v, "exec.Cmd literal")
			}
		}
		return true
	})
	return out
}
