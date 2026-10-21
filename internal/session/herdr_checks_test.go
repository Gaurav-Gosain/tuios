package session

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestHerdrMethodsNameTheirCheck holds the herdr socket to the rule the JSON
// verbs have: every method it answers names its check, and the table names
// no method the socket does not answer. A method missing here is refused at
// run time, so this test is what tells its author.
func TestHerdrMethodsNameTheirCheck(t *testing.T) {
	answered := herdrAnswered()
	for _, m := range answered {
		if herdrChecks[m] == herdrUnclassified {
			t.Errorf("herdr method %s has no entry in herdrChecks", m)
		}
	}
	for m := range herdrChecks {
		if !slices.Contains(answered, m) {
			t.Errorf("herdrChecks names %s, which the herdr socket does not answer", m)
		}
	}
}

// TestHerdrVerbMethodsRunTheGrantCheck holds the herdrVerbs label to what it
// says. Each method so labelled must reach admitVerb, callVerb or
// checkGrants from its handler, through the calls in this package's source.
// A handler that skips them would answer a pane or a plugin with no check
// at all while the table says it is checked.
func TestHerdrVerbMethodsRunTheGrantCheck(t *testing.T) {
	bodies := map[string][]*ast.BlockStmt{}
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				bodies[fn.Name.Name] = append(bodies[fn.Name.Name], fn.Body)
			}
		}
	}
	checks := map[string]bool{"admitVerb": true, "callVerb": true, "checkGrants": true}
	reaches := func(start string) bool {
		seen := map[string]bool{start: true}
		queue := []string{start}
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			for _, body := range bodies[name] {
				ast.Inspect(body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					callee := ""
					switch f := call.Fun.(type) {
					case *ast.Ident:
						callee = f.Name
					case *ast.SelectorExpr:
						callee = f.Sel.Name
					}
					if callee != "" && !seen[callee] {
						seen[callee] = true
						queue = append(queue, callee)
					}
					return true
				})
			}
		}
		for c := range checks {
			if seen[c] {
				return true
			}
		}
		return false
	}
	// handlerName is the function a handler value is, or for a closure the
	// function that made it.
	handlerName := func(fn any) string {
		full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
		parts := strings.Split(full[strings.LastIndex(full, "/")+1:], ".")
		for i := len(parts) - 1; i >= 0; i-- {
			if p := strings.TrimSuffix(parts[i], "-fm"); !strings.HasPrefix(p, "func") && p != "" {
				return p
			}
		}
		return full
	}
	handlers := map[string]string{"events.subscribe": "herdrSubscribe"}
	for m, h := range herdrMethods() {
		handlers[m] = handlerName(h.run)
	}
	for m, h := range herdrPluginMethods() {
		handlers[m] = handlerName(h)
	}
	for m, c := range herdrChecks {
		if c != herdrVerbs {
			continue
		}
		h, ok := handlers[m]
		switch {
		case !ok:
			t.Errorf("herdr method %s is labelled herdrVerbs and has no handler in the tables", m)
		case len(bodies[h]) == 0:
			t.Errorf("herdr method %s: no source for its handler %s", m, h)
		case !reaches(h):
			t.Errorf("herdr method %s is labelled herdrVerbs, and its handler %s never reaches admitVerb, callVerb or checkGrants", m, h)
		}
	}
}
