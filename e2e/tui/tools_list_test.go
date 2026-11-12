package tuie2e

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// managedTools are the tools the workflow installs on the shard that holds a
// test needing them. The rest (bash, tmux, git) are on every runner.
var managedTools = []string{"fish", "zsh"}

// TestToolsListMatchesTheTests keeps tools.txt whole. The CI workflow reads
// that file to decide which shard installs fish, zsh or Lua. A hand-kept list
// of test names in the workflow let TestPastedPathReadsBackBehindTmux skip for
// want of fish while a broken path picker passed on main.
//
// The source says what a test needs: needTool, requireOnCI and siShellPath
// with a literal tool name, reached from the test through the helpers it
// calls. The file must name exactly those tests with exactly those tools. A
// direct exec.LookPath of a managed tool is rejected, because it skips on CI
// where needTool fails.
func TestToolsListMatchesTheTests(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	type fn struct {
		callees map[string]bool
		tools   map[string]bool
	}
	funcs := map[string]*fn{}
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			cur := funcs[fd.Name.Name]
			if cur == nil {
				cur = &fn{callees: map[string]bool{}, tools: map[string]bool{}}
				funcs[fd.Name.Name] = cur
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, recv := callName(call)
				cur.callees[name] = true
				lits := stringArgs(call)
				switch name {
				case "needTool", "requireOnCI":
					for _, l := range lits {
						cur.tools[l] = true
					}
				case "siShellPath":
					for _, l := range lits {
						if slices.Contains(managedTools, l) {
							cur.tools[l] = true
						}
					}
				case "LookPath":
					if recv != "exec" || file == "tools_test.go" {
						break
					}
					for _, l := range lits {
						if slices.Contains(managedTools, l) {
							t.Errorf("%s: exec.LookPath(%q) skips on CI; use needTool or requireOnCI", fset.Position(call.Pos()), l)
						}
					}
				}
				return true
			})
		}
	}
	// What each test reaches, through the helpers it calls.
	got := map[string][]string{}
	for name := range funcs {
		if !strings.HasPrefix(name, "Test") {
			continue
		}
		seen := map[string]bool{}
		var walk func(string)
		walk = func(n string) {
			if seen[n] {
				return
			}
			seen[n] = true
			if f := funcs[n]; f != nil {
				for c := range f.callees {
					walk(c)
				}
			}
		}
		walk(name)
		set := map[string]bool{}
		for n := range seen {
			if f := funcs[n]; f != nil {
				for tool := range f.tools {
					if slices.Contains(managedTools, tool) {
						set[tool] = true
					}
				}
			}
		}
		if len(set) > 0 {
			var tools []string
			for tool := range set {
				tools = append(tools, tool)
			}
			sort.Strings(tools)
			got[name] = tools
		}
	}
	want := readToolsList(t)
	for name, tools := range got {
		w, ok := want[name]
		switch {
		case !ok:
			t.Errorf("tools.txt has no line for %s, which needs %s", name, strings.Join(tools, " "))
		case !slices.Equal(w, tools):
			t.Errorf("tools.txt says %s needs %s, the source says %s", name, strings.Join(w, " "), strings.Join(tools, " "))
		}
	}
	for name := range want {
		if _, ok := got[name]; !ok {
			t.Errorf("tools.txt names %s, which needs no managed tool (or is not a test)", name)
		}
	}
}

// readToolsList reads tools.txt: one test per line, then the tools it needs.
func readToolsList(t *testing.T) map[string][]string {
	t.Helper()
	f, err := os.Open("tools.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string][]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			t.Errorf("tools.txt: %q needs a test and at least one tool", line)
			continue
		}
		for _, tool := range fields[1:] {
			if !slices.Contains(managedTools, tool) {
				t.Errorf("tools.txt: %s is not a managed tool (%s)", tool, strings.Join(managedTools, ", "))
			}
		}
		tools := slices.Clone(fields[1:])
		sort.Strings(tools)
		out[fields[0]] = tools
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// callName is the name a call goes to, and for pkg.Name the package.
func callName(call *ast.CallExpr) (name, recv string) {
	switch f := call.Fun.(type) {
	case *ast.Ident:
		return f.Name, ""
	case *ast.SelectorExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return f.Sel.Name, id.Name
		}
		return f.Sel.Name, ""
	}
	return "", ""
}

// stringArgs are the arguments of call that are string literals.
func stringArgs(call *ast.CallExpr) []string {
	var out []string
	for _, a := range call.Args {
		if bl, ok := a.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			if s, err := strconv.Unquote(bl.Value); err == nil {
				out = append(out, s)
			}
		}
	}
	return out
}
