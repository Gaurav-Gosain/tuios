package lint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gitExempt are the directories that may start git without childproc.Git:
// childproc builds it, and testutil sets up test repositories.
var gitExempt = map[string]bool{"internal/childproc": true, "internal/testutil": true}

// TestGitGoesThroughChildprocGit is the git lint. tuios runs git in folders a
// pane controls, and a repository there can name programs git runs: hooks,
// filters, diff drivers. childproc.Git is the one place that turns them off,
// so every git in the app's code, daemon and client alike, starts through
// it. Rejected: a Command or CommandContext call, from any package, whose
// program is the literal "git". A deliberate exception carries
// //tuios:allow-exec <reason>.
func TestGitGoesThroughChildprocGit(t *testing.T) {
	root := repoRoot(t)
	var found []string
	for _, top := range []string{"internal", "cmd", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if gitExempt[rel] || d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			found = append(found, gitStarts(t, rel, src)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Errorf("code starts git without childproc.Git, so a repository's hooks and filters can run; "+
			"use childproc.Git, or mark a deliberate one with %s <reason>:\n  %s",
			allowExec, strings.Join(found, "\n  "))
	}
}

// TestGitLintCatchesEachForm feeds the git lint each form it rejects.
func TestGitLintCatchesEachForm(t *testing.T) {
	src := `package x

import (
	"context"
	"os/exec"

	"example/childproc"
)

func f(ctx context.Context) {
	_ = exec.Command("git", "status")
	_ = exec.CommandContext(ctx, "git", "status")
	_ = childproc.Command("git", "status")
	_ = childproc.CommandContext(ctx, "git", "status")
	_ = exec.Command("git") //tuios:allow-exec a test of the escape
	_ = childproc.Git(ctx, ".", "status")
	_ = exec.Command("gh", "auth")
}
`
	got := gitStarts(t, "x.go", []byte(src))
	if len(got) != 4 {
		t.Fatalf("the git lint found %d problems, want 4:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// gitStarts returns file:line for every Command or CommandContext call in src
// whose program is the literal "git".
func gitStarts(t *testing.T, name string, src []byte) []string {
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
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		_, sel := selector(call.Fun)
		if id, ok := call.Fun.(*ast.Ident); ok {
			sel = id.Name
		}
		arg := -1
		switch sel {
		case "Command":
			arg = 0
		case "CommandContext":
			arg = 1
		}
		if arg < 0 || len(call.Args) <= arg {
			return true
		}
		lit, ok := call.Args[arg].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, _ := strconv.Unquote(lit.Value); s == "git" {
			line := fset.Position(call.Pos()).Line
			if !allowed[line] {
				out = append(out, fmt.Sprintf("%s:%d: %s starts git", name, line, sel))
			}
		}
		return true
	})
	return out
}
