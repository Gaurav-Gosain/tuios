package tml

import (
	"fmt"
	"strconv"
	"strings"
)

// Expr is a parsed expression. Expressions compute values: they cannot loop,
// assign, call out or keep state. Parse one with ParseExpr and evaluate it
// with Eval.
type Expr struct {
	src  string
	root exprNode
	deps []Dep
	fns  []string
}

// Dep is one value an expression reads: a root name (state, item, form,
// event, view, value or row) and a JSON Pointer below it. An index computed
// at run time ends the pointer, so the dependency covers the whole array.
type Dep struct {
	Root string
	Path string
}

func (d Dep) String() string {
	if d.Path == "" {
		return d.Root
	}
	return d.Root + d.Path
}

// Source returns the text the expression was parsed from.
func (e *Expr) Source() string { return e.src }

// Deps returns the values the expression reads, in source order, without
// duplicates.
func (e *Expr) Deps() []Dep { return e.deps }

// Funcs returns the functions the expression calls.
func (e *Expr) Funcs() []string { return e.fns }

// SyntaxError is an expression that does not parse. Offset is a byte offset
// into the expression source.
type SyntaxError struct {
	Offset  int
	Message string
	Suggest string // a likely fix, when there is one
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("at %d: %s", e.Offset, e.Message) }

type exprNode interface{}

type (
	litNode struct{ v any }
	// pathNode reads a root name: state, item, form and the others.
	pathNode struct {
		name string
		off  int
	}
	memberNode struct {
		x    exprNode
		name string
	}
	indexNode struct{ x, idx exprNode }
	unaryNode struct {
		op string
		x  exprNode
	}
	binaryNode struct {
		op   string
		l, r exprNode
	}
	condNode struct{ c, a, b exprNode }
	callNode struct {
		name string
		args []exprNode
		off  int
	}
	arrayNode  struct{ elems []exprNode }
	objectNode struct {
		keys []string
		vals []exprNode
	}
)

type tokKind uint8

const (
	tEOF tokKind = iota
	tNum
	tStr
	tIdent
	tOp
)

type token struct {
	kind tokKind
	text string
	num  float64
	off  int
}

// maxExprLen bounds one expression's source. maxExprDepth bounds its nesting,
// so a hostile expression cannot exhaust the stack.
const (
	maxExprLen   = 4096
	maxExprDepth = 64
)

func lex(src string) ([]token, error) {
	var toks []token
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c >= '0' && c <= '9' || (c == '.' && i+1 < len(src) && src[i+1] >= '0' && src[i+1] <= '9'):
			start := i
			for i < len(src) && (src[i] >= '0' && src[i] <= '9' || src[i] == '.') {
				i++
			}
			if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
				i++
				if i < len(src) && (src[i] == '+' || src[i] == '-') {
					i++
				}
				for i < len(src) && src[i] >= '0' && src[i] <= '9' {
					i++
				}
			}
			f, err := strconv.ParseFloat(src[start:i], 64)
			if err != nil {
				return nil, &SyntaxError{Offset: start, Message: fmt.Sprintf("%q is not a number", src[start:i])}
			}
			toks = append(toks, token{kind: tNum, text: src[start:i], num: f, off: start})
		case c == '\'' || c == '"':
			start := i
			i++
			var b strings.Builder
			closed := false
			for i < len(src) {
				ch := src[i]
				if ch == '\\' && i+1 < len(src) {
					switch src[i+1] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(src[i+1])
					}
					i += 2
					continue
				}
				if ch == c {
					closed = true
					i++
					break
				}
				b.WriteByte(ch)
				i++
			}
			if !closed {
				return nil, &SyntaxError{Offset: start, Message: "the string has no closing quote"}
			}
			toks = append(toks, token{kind: tStr, text: b.String(), off: start})
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			start := i
			for i < len(src) && (src[i] == '_' || src[i] >= 'a' && src[i] <= 'z' || src[i] >= 'A' && src[i] <= 'Z' || src[i] >= '0' && src[i] <= '9') {
				i++
			}
			toks = append(toks, token{kind: tIdent, text: src[start:i], off: start})
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "==", "!=", "<=", ">=", "&&", "||", "??":
				toks = append(toks, token{kind: tOp, text: two, off: i})
				i += 2
				continue
			}
			if strings.IndexByte("+-*/%<>!?:()[],.{}", c) >= 0 {
				toks = append(toks, token{kind: tOp, text: string(c), off: i})
				i++
				continue
			}
			if c == '=' {
				return nil, &SyntaxError{Offset: i, Message: "assignment is not allowed; use == to compare"}
			}
			return nil, &SyntaxError{Offset: i, Message: fmt.Sprintf("unexpected character %q", c)}
		}
	}
	return append(toks, token{kind: tEOF, off: len(src)}), nil
}

type exprParser struct {
	toks  []token
	i     int
	depth int
	fns   []string
}

// ParseExpr parses one expression.
func ParseExpr(src string) (*Expr, error) {
	if len(src) > maxExprLen {
		return nil, &SyntaxError{Offset: 0, Message: fmt.Sprintf("the expression is longer than %d bytes", maxExprLen)}
	}
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &exprParser{toks: toks}
	root, err := p.expr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, &SyntaxError{Offset: t.off, Message: fmt.Sprintf("unexpected %q", t.text)}
	}
	e := &Expr{src: src, root: root, fns: p.fns}
	collectDeps(root, &e.deps)
	return e, nil
}

func (p *exprParser) peek() token { return p.toks[p.i] }
func (p *exprParser) next() token {
	t := p.toks[p.i]
	if t.kind != tEOF {
		p.i++
	}
	return t
}

func (p *exprParser) isOp(s string) bool {
	t := p.peek()
	return t.kind == tOp && t.text == s
}

func (p *exprParser) expect(s string) error {
	t := p.next()
	if t.kind != tOp || t.text != s {
		got := t.text
		if t.kind == tEOF {
			got = "the end"
		}
		return &SyntaxError{Offset: t.off, Message: fmt.Sprintf("expected %q, found %q", s, got)}
	}
	return nil
}

func (p *exprParser) enter() error {
	p.depth++
	if p.depth > maxExprDepth {
		return &SyntaxError{Offset: p.peek().off, Message: fmt.Sprintf("the expression nests deeper than %d levels", maxExprDepth)}
	}
	return nil
}

func (p *exprParser) expr() (exprNode, error) {
	if err := p.enter(); err != nil {
		return nil, err
	}
	defer func() { p.depth-- }()
	c, err := p.binary(0)
	if err != nil {
		return nil, err
	}
	if !p.isOp("?") {
		return c, nil
	}
	p.next()
	a, err := p.expr()
	if err != nil {
		return nil, err
	}
	if err := p.expect(":"); err != nil {
		return nil, err
	}
	b, err := p.expr()
	if err != nil {
		return nil, err
	}
	return &condNode{c, a, b}, nil
}

var precedence = [][]string{
	{"??"},
	{"||"},
	{"&&"},
	{"==", "!="},
	{"<", "<=", ">", ">="},
	{"+", "-"},
	{"*", "/", "%"},
}

func (p *exprParser) binary(level int) (exprNode, error) {
	if level == len(precedence) {
		return p.unary()
	}
	l, err := p.binary(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tOp || !contains(precedence[level], t.text) {
			return l, nil
		}
		p.next()
		r, err := p.binary(level + 1)
		if err != nil {
			return nil, err
		}
		l = &binaryNode{t.text, l, r}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (p *exprParser) unary() (exprNode, error) {
	if p.isOp("!") || p.isOp("-") {
		op := p.next().text
		if err := p.enter(); err != nil {
			return nil, err
		}
		defer func() { p.depth-- }()
		x, err := p.unary()
		if err != nil {
			return nil, err
		}
		return &unaryNode{op, x}, nil
	}
	return p.postfix()
}

func (p *exprParser) postfix() (exprNode, error) {
	x, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.isOp("."):
			p.next()
			t := p.next()
			if t.kind != tIdent {
				return nil, &SyntaxError{Offset: t.off, Message: "expected a field name after ."}
			}
			x = &memberNode{x, t.text}
		case p.isOp("["):
			p.next()
			idx, err := p.expr()
			if err != nil {
				return nil, err
			}
			if err := p.expect("]"); err != nil {
				return nil, err
			}
			x = &indexNode{x, idx}
		default:
			return x, nil
		}
	}
}

func (p *exprParser) primary() (exprNode, error) {
	t := p.next()
	switch t.kind {
	case tNum:
		return &litNode{t.num}, nil
	case tStr:
		return &litNode{t.text}, nil
	case tIdent:
		switch t.text {
		case "true":
			return &litNode{true}, nil
		case "false":
			return &litNode{false}, nil
		case "null":
			return &litNode{nil}, nil
		}
		if p.isOp("(") {
			p.next()
			var args []exprNode
			for !p.isOp(")") {
				if len(args) > 0 {
					if err := p.expect(","); err != nil {
						return nil, err
					}
				}
				a, err := p.expr()
				if err != nil {
					return nil, err
				}
				args = append(args, a)
			}
			p.next()
			if !contains(p.fns, t.text) {
				p.fns = append(p.fns, t.text)
			}
			return &callNode{t.text, args, t.off}, nil
		}
		return &pathNode{t.text, t.off}, nil
	case tOp:
		switch t.text {
		case "(":
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			return x, p.expect(")")
		case "[":
			arr := &arrayNode{}
			for !p.isOp("]") {
				if len(arr.elems) > 0 {
					if err := p.expect(","); err != nil {
						return nil, err
					}
				}
				x, err := p.expr()
				if err != nil {
					return nil, err
				}
				arr.elems = append(arr.elems, x)
			}
			p.next()
			return arr, nil
		case "{":
			obj := &objectNode{}
			for !p.isOp("}") {
				if len(obj.keys) > 0 {
					if err := p.expect(","); err != nil {
						return nil, err
					}
				}
				k := p.next()
				if k.kind != tIdent && k.kind != tStr {
					return nil, &SyntaxError{Offset: k.off, Message: "expected a key in the object"}
				}
				if err := p.expect(":"); err != nil {
					return nil, err
				}
				v, err := p.expr()
				if err != nil {
					return nil, err
				}
				obj.keys = append(obj.keys, k.text)
				obj.vals = append(obj.vals, v)
			}
			p.next()
			return obj, nil
		}
	case tEOF:
		return nil, &SyntaxError{Offset: t.off, Message: "the expression ends too early"}
	}
	return nil, &SyntaxError{Offset: t.off, Message: fmt.Sprintf("unexpected %q", t.text)}
}

// staticPath returns the root and pointer a chain of member and constant
// index nodes reads, and whether the chain ends at a root name. dyn reports
// that an index computed at run time ended the pointer: nothing after it is
// added, so the dependency covers the whole array.
func staticPath(n exprNode) (root, ptr string, dyn, ok bool) {
	switch x := n.(type) {
	case *pathNode:
		return x.name, "", false, true
	case *memberNode:
		r, p, dyn, ok := staticPath(x.x)
		if !ok || dyn {
			return r, p, dyn, ok
		}
		return r, p + "/" + escapePointer(x.name), false, true
	case *indexNode:
		r, p, dyn, ok := staticPath(x.x)
		if !ok || dyn {
			return r, p, dyn, ok
		}
		if lit, isLit := x.idx.(*litNode); isLit {
			switch v := lit.v.(type) {
			case float64:
				if v >= 0 && v == float64(int64(v)) {
					return r, p + "/" + strconv.FormatInt(int64(v), 10), false, true
				}
			case string:
				return r, p + "/" + escapePointer(v), false, true
			}
		}
		return r, p, true, true
	}
	return "", "", false, false
}

func collectDeps(n exprNode, out *[]Dep) {
	add := func(d Dep) {
		for _, e := range *out {
			if e == d {
				return
			}
		}
		*out = append(*out, d)
	}
	switch x := n.(type) {
	case *pathNode, *memberNode, *indexNode:
		if r, p, _, ok := staticPath(x); ok {
			add(Dep{r, p})
			indexDeps(x, out)
			return
		}
		switch y := x.(type) {
		case *memberNode:
			collectDeps(y.x, out)
		case *indexNode:
			collectDeps(y.x, out)
			collectDeps(y.idx, out)
		}
	case *unaryNode:
		collectDeps(x.x, out)
	case *binaryNode:
		collectDeps(x.l, out)
		collectDeps(x.r, out)
	case *condNode:
		collectDeps(x.c, out)
		collectDeps(x.a, out)
		collectDeps(x.b, out)
	case *callNode:
		for _, a := range x.args {
			collectDeps(a, out)
		}
	case *arrayNode:
		for _, e := range x.elems {
			collectDeps(e, out)
		}
	case *objectNode:
		for _, v := range x.vals {
			collectDeps(v, out)
		}
	}
}

// indexDeps collects the dependencies of the computed indexes in a chain of
// member and index nodes: state.i in state.items[state.i].name.
func indexDeps(n exprNode, out *[]Dep) {
	switch x := n.(type) {
	case *memberNode:
		indexDeps(x.x, out)
	case *indexNode:
		indexDeps(x.x, out)
		if _, lit := x.idx.(*litNode); !lit {
			collectDeps(x.idx, out)
		}
	}
}

// roots returns the root names an expression reads, with their offsets.
func roots(n exprNode, out map[string]int) {
	switch x := n.(type) {
	case *pathNode:
		if _, ok := out[x.name]; !ok {
			out[x.name] = x.off
		}
	case *memberNode:
		roots(x.x, out)
	case *indexNode:
		roots(x.x, out)
		roots(x.idx, out)
	case *unaryNode:
		roots(x.x, out)
	case *binaryNode:
		roots(x.l, out)
		roots(x.r, out)
	case *condNode:
		roots(x.c, out)
		roots(x.a, out)
		roots(x.b, out)
	case *callNode:
		for _, a := range x.args {
			roots(a, out)
		}
	case *arrayNode:
		for _, e := range x.elems {
			roots(e, out)
		}
	case *objectNode:
		for _, v := range x.vals {
			roots(v, out)
		}
	}
}

// calls returns each call in an expression with its offset.
func calls(n exprNode, fn func(c *callNode)) {
	switch x := n.(type) {
	case *callNode:
		fn(x)
		for _, a := range x.args {
			calls(a, fn)
		}
	case *memberNode:
		calls(x.x, fn)
	case *indexNode:
		calls(x.x, fn)
		calls(x.idx, fn)
	case *unaryNode:
		calls(x.x, fn)
	case *binaryNode:
		calls(x.l, fn)
		calls(x.r, fn)
	case *condNode:
		calls(x.c, fn)
		calls(x.a, fn)
		calls(x.b, fn)
	case *arrayNode:
		for _, e := range x.elems {
			calls(e, fn)
		}
	case *objectNode:
		for _, v := range x.vals {
			calls(v, fn)
		}
	}
}
