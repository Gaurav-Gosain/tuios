package tml

import (
	"strings"
)

// Template is text with {expr} bindings, as an attribute value or a text run
// holds it. {{ and }} write a literal brace.
type Template struct {
	parts []tmplPart
}

type tmplPart struct {
	lit  string
	expr *Expr
	off  int // byte offset of the expression source in the template
}

// TemplateError is a binding that does not parse. Offset is a byte offset
// into the template text.
type TemplateError struct {
	Offset  int
	Message string
}

func (e *TemplateError) Error() string { return e.Message }

// ParseTemplate splits text into literal runs and bindings.
func ParseTemplate(s string) (*Template, error) {
	t := &Template{}
	var lit strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '{' && i+1 < len(s) && s[i+1] == '{':
			lit.WriteByte('{')
			i += 2
		case c == '}' && i+1 < len(s) && s[i+1] == '}':
			lit.WriteByte('}')
			i += 2
		case c == '}':
			return nil, &TemplateError{i, "a } with no { before it. Write }} for a literal brace."}
		case c == '{':
			end, err := matchBrace(s, i)
			if err != nil {
				return nil, err
			}
			src := s[i+1 : end]
			if strings.TrimSpace(src) == "" {
				return nil, &TemplateError{i, "the binding {} is empty. Put an expression inside, such as {state.count}."}
			}
			e, err := ParseExpr(src)
			if err != nil {
				if se, ok := err.(*SyntaxError); ok {
					return nil, &TemplateError{i + 1 + se.Offset, se.Message}
				}
				return nil, &TemplateError{i + 1, err.Error()}
			}
			if lit.Len() > 0 {
				t.parts = append(t.parts, tmplPart{lit: lit.String()})
				lit.Reset()
			}
			t.parts = append(t.parts, tmplPart{expr: e, off: i + 1})
			i = end + 1
		default:
			lit.WriteByte(c)
			i++
		}
	}
	if lit.Len() > 0 {
		t.parts = append(t.parts, tmplPart{lit: lit.String()})
	}
	return t, nil
}

// matchBrace finds the } that closes the { at start, past strings and nested
// object literals.
func matchBrace(s string, start int) (int, error) {
	depth := 0
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		}
	}
	return 0, &TemplateError{start, "the binding has no closing }."}
}

// HasExpr reports whether the template holds a binding.
func (t *Template) HasExpr() bool {
	for _, p := range t.parts {
		if p.expr != nil {
			return true
		}
	}
	return false
}

// Exprs returns the bindings of the template.
func (t *Template) Exprs() []*Expr {
	var out []*Expr
	for _, p := range t.parts {
		if p.expr != nil {
			out = append(out, p.expr)
		}
	}
	return out
}

// Eval evaluates the template. A template that is one binding and nothing
// else gives the binding's value with its type. Any other template gives a
// string, at most 64 KiB. Every string and key passes through Sanitize, so
// state cannot carry an escape sequence to the screen.
func (t *Template) Eval(env *Env) (any, error) {
	env = withBudget(env)
	if len(t.parts) == 1 && t.parts[0].expr != nil {
		return t.parts[0].expr.Eval(env)
	}
	var b strings.Builder
	for _, p := range t.parts {
		if p.expr == nil {
			b.WriteString(p.lit)
			continue
		}
		v, err := p.expr.Eval(env)
		if err != nil {
			return nil, err
		}
		if err := writeText(&b, v, env.Budget); err != nil {
			if err == errTooLong {
				break
			}
			return nil, err
		}
		if b.Len() > maxString {
			break
		}
	}
	return Sanitize(cutRunes(b.String(), maxString)), nil
}

// Render evaluates the template to text. A binding that runs out of budget
// renders as "!".
func (t *Template) Render(env *Env) string {
	v, err := t.Eval(env)
	if err != nil {
		return "!"
	}
	return Stringify(v)
}

// ParseBinding parses an attribute that holds one expression, such as if,
// show or a column format: a bare expression such as form.canary, or one
// binding such as {state.cpu > 90}.
func ParseBinding(s string) (*Expr, error) {
	e, _, err := exprOrBinding(s)
	return e, err
}

func exprOrBinding(s string) (*Expr, int, error) {
	trim := strings.TrimSpace(s)
	lead := strings.Index(s, trim)
	if strings.HasPrefix(trim, "{") && !strings.HasPrefix(trim, "{{") {
		if end, err := matchBrace(trim, 0); err == nil && end == len(trim)-1 {
			e, err := ParseExpr(trim[1:end])
			if se, ok := err.(*SyntaxError); ok {
				return nil, lead + 1 + se.Offset, err
			}
			return e, lead + 1, err
		}
	}
	e, err := ParseExpr(s)
	if se, ok := err.(*SyntaxError); ok {
		return nil, se.Offset, err
	}
	return e, 0, err
}
