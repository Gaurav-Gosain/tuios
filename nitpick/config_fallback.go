package nitpick

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// ConfigFallback reports an environment variable read that falls back to a
// value for a development machine, such as localhost.
var ConfigFallback = &analysis.Analyzer{
	Name: "config_fallback",
	Doc: `report an environment variable that falls back to a development value

	host := os.Getenv("DB_HOST")
	if host == "" {
		host = "localhost"
	}

A deployment that forgets the variable then talks to localhost, or runs with
a password of "changeme", and nothing fails. The rule knows these forms: an
if on the value that sets or returns the default, a variable with a default
that the read replaces, a call such as cmp.Or(os.Getenv("X"), "localhost"),
and a function of the package that takes the name and the default. Test files
are skipped, and so are packages for tests and examples (a folder named
e2e, example, examples, testutil or testenv, or a package name that starts
or ends with test), where a development value is the right default. The rule is off by
default.`,
	URL: "https://pkg.go.dev/tuios.dev/nitpick#ConfigFallback",
	Run: runConfigFallback,
}

// devValue matches a value meant for a development machine.
var devValue = regexp.MustCompile(`(?i)(\blocalhost\b|\b127\.0\.0\.1\b|\b0\.0\.0\.0\b|\[::1\]|\bchange-?me\b|sslmode=disable|\bexample\.(com|org|net)\b|^(dev|development|debug|password|secret|admin)$)`)

func isDevValue(e ast.Expr) bool {
	s, ok := stringLit(e)
	return ok && devValue.MatchString(s)
}

// isEnvRead reports whether call reads an environment variable.
func isEnvRead(info *types.Info, call *ast.CallExpr) bool {
	fn, ok := typeutil.Callee(info, call).(*types.Func)
	if !ok {
		return false
	}
	switch fn.FullName() {
	case "os.Getenv", "os.LookupEnv", "syscall.Getenv":
		return true
	}
	return false
}

// envName returns the variable name a read names, or a stand-in.
func envName(call *ast.CallExpr) string {
	if len(call.Args) > 0 {
		if s, ok := stringLit(call.Args[0]); ok {
			return s
		}
	}
	return "an environment variable"
}

// forTests reports whether the package is test support or an example.
func forTests(pass *analysis.Pass) bool {
	name := pass.Pkg.Name()
	if strings.HasPrefix(name, "test") || strings.HasSuffix(name, "test") {
		return true
	}
	if len(pass.Files) == 0 {
		return false
	}
	dir := filepath.ToSlash(filepath.Dir(pass.Fset.File(pass.Files[0].FileStart).Name()))
	for part := range strings.SplitSeq(dir, "/") {
		switch part {
		case "e2e", "example", "examples", "_examples", "testutil", "testutils", "testenv", "testhelper", "testhelpers":
			return true
		}
	}
	return false
}

func runConfigFallback(pass *analysis.Pass) (any, error) {
	if forTests(pass) {
		return nil, nil
	}
	info := pass.TypesInfo
	files := handWritten(pass)
	helpers := envHelpers(pass, files)
	reported := map[token.Pos]bool{}
	report := func(lit ast.Expr, name string) {
		if reported[lit.Pos()] {
			return
		}
		reported[lit.Pos()] = true
		s, _ := stringLit(lit)
		pass.Report(analysis.Diagnostic{
			Pos:     lit.Pos(),
			End:     lit.End(),
			Message: fmt.Sprintf("%s falls back to the development value %q: fail when it is not set", name, s),
		})
	}
	for _, f := range pass.Files {
		if !files[f] || isTestFile(pass, f) {
			continue
		}
		// envVars maps a variable to the read that set it.
		envVars := map[types.Object]*ast.CallExpr{}
		// devVars maps a variable to the development literal it starts with.
		devVars := map[types.Object]ast.Expr{}
		record := func(lhs []ast.Expr, rhs []ast.Expr) {
			if len(rhs) != 1 {
				if len(lhs) == len(rhs) {
					for i := range lhs {
						if id, ok := lhs[i].(*ast.Ident); ok && isDevValue(rhs[i]) {
							if obj := info.ObjectOf(id); obj != nil {
								devVars[obj] = rhs[i]
							}
						}
					}
				}
				return
			}
			if len(lhs) == 1 && isDevValue(rhs[0]) {
				if id, ok := lhs[0].(*ast.Ident); ok && info.ObjectOf(id) != nil {
					devVars[info.ObjectOf(id)] = rhs[0]
				}
				return
			}
			call, ok := ast.Unparen(rhs[0]).(*ast.CallExpr)
			if !ok || !isEnvRead(info, call) {
				return
			}
			for _, l := range lhs {
				if id, ok := l.(*ast.Ident); ok && id.Name != "_" && info.ObjectOf(id) != nil {
					envVars[info.ObjectOf(id)] = call
				}
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				record(n.Lhs, n.Rhs)
			case *ast.ValueSpec:
				lhs := make([]ast.Expr, len(n.Names))
				for i, id := range n.Names {
					lhs[i] = id
				}
				record(lhs, n.Values)
			case *ast.CallExpr:
				checkFallbackCall(info, n, helpers, report)
			case *ast.IfStmt:
				if as, ok := n.Init.(*ast.AssignStmt); ok {
					record(as.Lhs, as.Rhs)
				}
				read := condRead(info, n.Cond, envVars)
				if read == nil {
					return true
				}
				for _, blk := range []ast.Stmt{n.Body, n.Else} {
					b, ok := blk.(*ast.BlockStmt)
					if !ok {
						continue
					}
					for _, st := range b.List {
						checkFallbackStmt(info, st, read, envVars, devVars, report)
					}
				}
			}
			return true
		})
	}
	return nil, nil
}

// checkFallbackCall reports cmp.Or(os.Getenv("X"), "localhost") and a call
// to a helper of the package with a development default.
func checkFallbackCall(info *types.Info, call *ast.CallExpr, helpers map[*types.Func]bool, report func(ast.Expr, string)) {
	if isEnvRead(info, call) {
		return
	}
	var read *ast.CallExpr
	for _, a := range call.Args {
		if c, ok := ast.Unparen(a).(*ast.CallExpr); ok && isEnvRead(info, c) {
			read = c
		}
	}
	fn, _ := typeutil.Callee(info, call).(*types.Func)
	if read == nil && !helpers[fn] {
		return
	}
	for _, a := range call.Args {
		if !isDevValue(a) {
			continue
		}
		name := "an environment variable"
		if read != nil {
			name = envName(read)
		} else if s, ok := stringLit(call.Args[0]); ok {
			name = s
		}
		report(a, name)
	}
}

// checkFallbackStmt reports a statement in an if on a read value that sets
// or returns a development literal, or copies the read into a variable that
// starts as one.
func checkFallbackStmt(info *types.Info, st ast.Stmt, read *ast.CallExpr, envVars map[types.Object]*ast.CallExpr, devVars map[types.Object]ast.Expr, report func(ast.Expr, string)) {
	switch st := st.(type) {
	case *ast.AssignStmt:
		for i, r := range st.Rhs {
			if isDevValue(r) {
				report(r, envName(read))
				continue
			}
			id, ok := ast.Unparen(r).(*ast.Ident)
			if !ok || envVars[info.ObjectOf(id)] == nil || i >= len(st.Lhs) {
				continue
			}
			if l, ok := st.Lhs[i].(*ast.Ident); ok {
				if lit := devVars[info.ObjectOf(l)]; lit != nil {
					report(lit, envName(read))
				}
			}
		}
	case *ast.ReturnStmt:
		for _, r := range st.Results {
			if isDevValue(r) {
				report(r, envName(read))
			}
		}
	}
}

// condRead returns the read behind a variable that cond uses.
func condRead(info *types.Info, cond ast.Expr, envVars map[types.Object]*ast.CallExpr) *ast.CallExpr {
	var read *ast.CallExpr
	ast.Inspect(cond, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && read == nil {
			read = envVars[info.ObjectOf(id)]
		}
		return read == nil
	})
	return read
}

// envHelpers finds the functions of the package that read the variable a
// parameter names: func getenv(key, def string) string.
func envHelpers(pass *analysis.Pass, files map[*ast.File]bool) map[*types.Func]bool {
	info := pass.TypesInfo
	out := map[*types.Func]bool{}
	for _, f := range pass.Files {
		if !files[f] || isTestFile(pass, f) {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Type.Params.NumFields() < 2 {
				continue
			}
			params := map[types.Object]bool{}
			for _, fld := range fd.Type.Params.List {
				for _, id := range fld.Names {
					params[info.ObjectOf(id)] = true
				}
			}
			fn, _ := info.Defs[fd.Name].(*types.Func)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if ok && isEnvRead(info, call) && len(call.Args) == 1 {
					if id, ok := ast.Unparen(call.Args[0]).(*ast.Ident); ok && params[info.ObjectOf(id)] {
						out[fn] = true
					}
				}
				return true
			})
		}
	}
	return out
}
