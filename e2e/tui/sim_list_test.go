package tuie2e

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSimListNamesTestsThatCanRunInProcess checks shardplan/sim.txt, the
// tests the sim shards of e2e-tui.yml run in process. Each line names a test
// of this package, once, and no named test starts with requireReal, which
// skips it there.
func TestSimListNamesTestsThatCanRunInProcess(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]bool{}
	for _, file := range files {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Recv != nil || !strings.HasPrefix(fd.Name.Name, "Test") {
				continue
			}
			skips := false
			if len(fd.Body.List) > 0 {
				if es, ok := fd.Body.List[0].(*ast.ExprStmt); ok {
					if c, ok := es.X.(*ast.CallExpr); ok {
						if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "requireReal" {
							skips = true
						}
					}
				}
			}
			tests[fd.Name.Name] = skips
		}
	}
	f, err := os.Open(filepath.Join("shardplan", "sim.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		name := strings.TrimSpace(sc.Text())
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		skips, ok := tests[name]
		switch {
		case !ok:
			t.Errorf("shardplan/sim.txt:%d: %s is not a test of this package", n, name)
		case skips:
			t.Errorf("shardplan/sim.txt:%d: %s calls requireReal, so the sim shards skip it", n, name)
		case seen[name]:
			t.Errorf("shardplan/sim.txt:%d: %s is listed twice", n, name)
		}
		seen[name] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}
