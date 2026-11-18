package nitpick

import (
	"go/ast"
	"go/token"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
)

// NarratorComment reports a comment in a function body that says what the
// next line says and nothing more.
var NarratorComment = &analysis.Analyzer{
	Name: "narrator_comment",
	Doc: `report a comment that restates the next line

	// increment the counter
	counter++

A comment in a function body is reported when it has six words or fewer and
the next statement holds each of its words, after common words are dropped
and short forms (cfg, err, ctx) are read as the full word. A comment that
gives a reason (because, so, must, never, ...) is left alone, and so are a
formula or an invariant (a comment with =, < or >) and a comment at the end
of a line of code.`,
	URL: "https://pkg.go.dev/tuios.dev/nitpick#NarratorComment",
	Run: runNarratorComment,
}

func runNarratorComment(pass *analysis.Pass) (any, error) {
	files := handWritten(pass)
	for _, f := range pass.Files {
		if !files[f] {
			continue
		}
		tf := pass.Fset.File(f.FileStart)
		src, err := pass.ReadFile(tf.Name())
		if err != nil {
			continue
		}
		// next maps a line to the statement in a function body that starts
		// on it, and to where that body starts.
		type stmtAt struct {
			stmt  ast.Stmt
			start token.Pos
		}
		next := map[int]stmtAt{}
		ast.Inspect(f, func(n ast.Node) bool {
			var list []ast.Stmt
			var start token.Pos
			switch n := n.(type) {
			case *ast.BlockStmt:
				list, start = n.List, n.Lbrace
			case *ast.CaseClause:
				list, start = n.Body, n.Colon
			case *ast.CommClause:
				list, start = n.Body, n.Colon
			}
			for _, s := range list {
				// A comment above a case names the case; it does not narrate.
				switch s.(type) {
				case *ast.CaseClause, *ast.CommClause:
					continue
				}
				if _, ok := next[tf.Line(s.Pos())]; !ok {
					next[tf.Line(s.Pos())] = stmtAt{s, start}
				}
			}
			return true
		})
		for _, g := range f.Comments {
			if len(g.List) > 2 || strings.HasPrefix(g.List[0].Text, "/*") || !ownLine(src, tf.Offset(g.Pos())) {
				continue
			}
			// The comment must be in the body, not the doc of a one-line func.
			at, ok := next[tf.Line(g.End())+1]
			if !ok || g.Pos() < at.start || !narrates(g.Text(), stmtWords(at.stmt)) {
				continue
			}
			pass.Report(analysis.Diagnostic{
				Pos:     g.Pos(),
				End:     g.End(),
				Message: "the comment says what the next line says: give the reason, or remove the comment",
			})
		}
	}
	return nil, nil
}

// ownLine reports whether only blanks come before offset on its line.
func ownLine(src []byte, offset int) bool {
	for i := offset - 1; i >= 0; i-- {
		switch src[i] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		default:
			return false
		}
	}
	return true
}

// narrates reports whether every content word of the comment is in code.
func narrates(text string, code map[string]bool) bool {
	// A formula or an invariant (x = a*b, 0 <= i < n) is not narration.
	if strings.ContainsAny(text, "=<>") {
		return false
	}
	raw := splitWords(text)
	var words []string
	for _, w := range raw {
		if reasonWords[w] {
			return false
		}
		if stopWords[w] || len(w) < 2 {
			continue
		}
		words = append(words, canon(w))
	}
	if len(words) == 0 || len(words) > 6 || len(raw) > 10 {
		return false
	}
	miss := 0
	for _, w := range words {
		if !code[w] {
			miss++
		}
	}
	allowed := 0
	if len(words) >= 4 {
		allowed = 1
	}
	return miss <= allowed
}

// stmtWords returns the words of the head of a statement: the condition of
// an if, the clause of a for, the whole of a simple statement. Function
// literal bodies and nested blocks are left out.
func stmtWords(s ast.Stmt) map[string]bool {
	out := map[string]bool{}
	add := func(ws ...string) {
		for _, w := range ws {
			out[canon(w)] = true
		}
	}
	var nodes []ast.Node
	switch s := s.(type) {
	case *ast.LabeledStmt:
		return stmtWords(s.Stmt)
	case *ast.IfStmt:
		add("check", "if")
		nodes = []ast.Node{s.Init, s.Cond}
	case *ast.ForStmt:
		add("loop")
		nodes = []ast.Node{s.Init, s.Cond, s.Post}
	case *ast.RangeStmt:
		add("loop")
		nodes = []ast.Node{s.Key, s.Value, s.X}
	case *ast.SwitchStmt:
		add("switch", "check")
		nodes = []ast.Node{s.Init, s.Tag}
	case *ast.TypeSwitchStmt:
		add("switch", "check", "type")
		nodes = []ast.Node{s.Init, s.Assign}
	case *ast.BlockStmt, *ast.SelectStmt:
		return out
	case *ast.IncDecStmt:
		if s.Tok == token.INC {
			add("increment")
		} else {
			add("decrement")
		}
		nodes = []ast.Node{s}
	case *ast.ReturnStmt:
		add("return")
		nodes = []ast.Node{s}
	case *ast.AssignStmt:
		add("set")
		nodes = []ast.Node{s}
	case *ast.GoStmt:
		add("start", "goroutine")
		nodes = []ast.Node{s}
	case *ast.DeferStmt:
		add("defer")
		nodes = []ast.Node{s}
	case *ast.SendStmt:
		add("send")
		nodes = []ast.Node{s}
	default:
		nodes = []ast.Node{s}
	}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		ast.Inspect(n, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.Ident:
				add(splitWords(n.Name)...)
			case *ast.BasicLit:
				if n.Kind == token.STRING {
					add(splitWords(n.Value)...)
				}
			case *ast.BinaryExpr:
				if n.Op == token.NEQ || n.Op == token.EQL {
					add("check")
				}
			}
			return true
		})
	}
	return out
}

// splitWords splits text and identifiers into lower-case words:
// "parseHTTPRequest" gives parse, http and request.
func splitWords(s string) []string {
	var out []string
	rs := []rune(s)
	start := -1
	flush := func(end int) {
		if start >= 0 && end > start {
			out = append(out, strings.ToLower(string(rs[start:end])))
		}
		start = -1
	}
	for i, r := range rs {
		if !unicode.IsLetter(r) {
			flush(i)
			continue
		}
		if start < 0 {
			start = i
			continue
		}
		prev := rs[i-1]
		nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
		if unicode.IsUpper(r) && (unicode.IsLower(prev) || (unicode.IsUpper(prev) && nextLower)) {
			flush(i)
			start = i
		}
	}
	flush(len(rs))
	return out
}

// canon maps a word to one form: plurals and short forms join the word.
func canon(w string) string {
	if c, ok := synonyms[w]; ok {
		return c
	}
	switch {
	case strings.HasSuffix(w, "ies") && len(w) > 4:
		w = w[:len(w)-3] + "y"
	case strings.HasSuffix(w, "ing") && len(w) > 5:
		w = w[:len(w)-3]
	case strings.HasSuffix(w, "ed") && len(w) > 4:
		w = w[:len(w)-2]
	case strings.HasSuffix(w, "es") && len(w) > 4 && strings.ContainsRune("sxz", rune(w[len(w)-3])):
		w = w[:len(w)-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && len(w) > 3:
		w = w[:len(w)-1]
	}
	if c, ok := synonyms[w]; ok {
		return c
	}
	return w
}

var stopWords = setOf("the", "a", "an", "to", "of", "and", "or", "in", "on", "for", "with",
	"from", "into", "this", "that", "these", "those", "it", "its", "is", "are", "be", "been",
	"we", "our", "us", "now", "then", "here", "there", "by", "as", "at", "all", "each", "some",
	"any", "up", "out", "via", "my", "your", "their", "will", "just", "do", "does")

// reasonWords mark a comment that explains rather than narrates.
var reasonWords = setOf("because", "since", "why", "so", "otherwise", "unless", "until", "avoid",
	"prevent", "must", "should", "need", "needs", "never", "always", "only", "instead",
	"workaround", "hack", "todo", "fixme", "xxx", "note", "bug", "but", "however", "although",
	"though", "already", "still", "yet", "later", "may", "might", "can", "cannot", "could",
	"would", "not", "don", "won", "isn", "doesn", "nolint", "lint", "see", "https", "http",
	"deprecated", "else", "first", "last", "before", "after", "when", "while", "once", "unused",
	"intentionally", "deliberately", "ignore", "ignored", "safe", "fine", "ok")

var synonyms = map[string]string{
	"get": "get", "fetch": "get", "retrieve": "get", "obtain": "get", "grab": "get",
	"create": "new", "make": "new", "initialize": "new", "initialise": "new", "construct": "new", "allocate": "new",
	"increment": "increment", "inc": "increment", "increase": "increment", "bump": "increment",
	"decrement": "decrement", "dec": "decrement", "decrease": "decrement",
	"check": "check", "verify": "check", "validate": "check", "ensure": "check", "test": "check",
	"loop": "loop", "iterate": "loop", "range": "loop", "every": "loop", "through": "loop", "over": "loop",
	"err": "error", "fail": "error", "failure": "error",
	"set": "set", "assign": "set", "store": "set", "update": "set",
	"start": "start", "launch": "start", "spawn": "start", "run": "start",
	"remove": "delete", "del": "delete",
	"len": "length", "size": "length", "count": "length",
	"print": "print", "println": "print", "printf": "print", "log": "print", "logf": "print",
	"str": "string", "num": "number", "msg": "message", "cfg": "config", "conf": "config",
	"configuration": "config", "ctx": "context", "req": "request", "resp": "response",
	"res": "result", "idx": "index", "val": "value", "buf": "buffer", "conn": "connection",
	"db": "database", "dir": "directory", "arg": "argument", "args": "argument", "param": "parameter",
	"opt": "option", "opts": "option", "info": "information", "max": "maximum", "min": "minimum",
	"env": "environment", "pkg": "package", "fn": "function", "func": "function", "srv": "server",
	"addr": "address", "tmp": "temporary", "temp": "temporary", "init": "new", "ret": "return",
	"cnt": "length", "sz": "length", "cb": "callback", "ch": "channel", "chan": "channel",
}

func setOf(ws ...string) map[string]bool {
	m := make(map[string]bool, len(ws))
	for _, w := range ws {
		m[w] = true
	}
	return m
}
