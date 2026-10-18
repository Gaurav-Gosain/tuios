package tml

import (
	"fmt"
	"strings"
)

// Action is one call in an action attribute such as on-press.
type Action struct {
	Name string
	Args []*Expr
	Off  int // byte offset of the call in the attribute
}

// actionSpec is the shape of one action.
type actionSpec struct {
	name     string
	min, max int
	literal  int // how many leading arguments must be string literals
}

// actionSpecs lists the actions, sorted by name. set, toggle, tab, sort,
// filter, fold and focus run locally. emit and close go to the owner. copy,
// open and pane go to the host.
var actionSpecs = [...]actionSpec{
	{"close", 0, 1, 0},
	{"copy", 1, 1, 0},
	{"emit", 1, 2, 1},
	{"filter", 2, 2, 1},
	{"focus", 1, 1, 1},
	{"fold", 1, 1, 1},
	{"open", 1, 1, 1},
	{"pane", 1, 2, 1},
	{"set", 2, 2, 1},
	{"sort", 1, 2, 2},
	{"tab", 1, 1, 1},
	{"toggle", 1, 1, 1},
}

func findAction(name string) (actionSpec, bool) {
	for _, a := range actionSpecs {
		if a.name == name {
			return a, true
		}
	}
	return actionSpec{}, false
}

// ActionNames lists the actions, sorted.
func ActionNames() []string {
	out := make([]string, len(actionSpecs))
	for i, a := range actionSpecs {
		out[i] = a.name
	}
	return out
}

var paneVerbs = []string{"focus", "new-window", "send-text"}

// ParseActions parses an action attribute: calls separated by ;.
func ParseActions(s string) ([]Action, error) {
	var out []Action
	for _, seg := range splitTop(s, ';') {
		src := s[seg[0]:seg[1]]
		if strings.TrimSpace(src) == "" {
			continue
		}
		e, err := ParseExpr(src)
		if err != nil {
			if se, ok := err.(*SyntaxError); ok {
				return nil, &SyntaxError{Offset: seg[0] + se.Offset, Message: se.Message}
			}
			return nil, err
		}
		call, ok := e.root.(*callNode)
		if !ok {
			return nil, &SyntaxError{Offset: seg[0], Message: fmt.Sprintf("%q is not an action call; write one such as emit('name')", strings.TrimSpace(src))}
		}
		spec, ok := findAction(call.name)
		if !ok {
			msg := fmt.Sprintf("%s is not an action", call.name)
			return nil, &SyntaxError{Offset: seg[0] + call.off, Message: msg, Suggest: suggest(call.name, ActionNames())}
		}
		if len(call.args) < spec.min || len(call.args) > spec.max {
			return nil, &SyntaxError{Offset: seg[0] + call.off, Message: fmt.Sprintf("%s takes %s", call.name, argCount(spec.min, spec.max))}
		}
		a := Action{Name: call.name, Off: seg[0] + call.off}
		for i, arg := range call.args {
			if i < spec.literal {
				if _, ok := literalString(arg); !ok {
					return nil, &SyntaxError{Offset: seg[0] + call.off, Message: fmt.Sprintf("argument %d of %s must be a quoted string", i+1, call.name)}
				}
			}
			sub := &Expr{src: src, root: arg}
			collectDeps(arg, &sub.deps)
			a.Args = append(a.Args, sub)
		}
		if err := checkActionArgs(a); err != nil {
			return nil, &SyntaxError{Offset: a.Off, Message: err.Error()}
		}
		out = append(out, a)
	}
	return out, nil
}

func argCount(lo, hi int) string {
	if lo == hi {
		if lo == 1 {
			return "1 argument"
		}
		return fmt.Sprintf("%d arguments", lo)
	}
	return fmt.Sprintf("%d to %d arguments", lo, hi)
}

func literalString(n exprNode) (string, bool) {
	l, ok := n.(*litNode)
	if !ok {
		return "", false
	}
	s, ok := l.v.(string)
	return s, ok
}

// Literal returns the argument as a string literal.
func (a Action) Literal(i int) string {
	if i >= len(a.Args) {
		return ""
	}
	s, _ := literalString(a.Args[i].root)
	return s
}

func checkActionArgs(a Action) error {
	switch a.Name {
	case "set", "toggle":
		if _, err := PathPointer(a.Literal(0)); err != nil {
			return err
		}
	case "open":
		p := a.Literal(0)
		if !strings.HasPrefix(p, "file:") {
			return fmt.Errorf("open takes a file: path, such as open('file:notes.md')")
		}
		if err := checkFilePath(strings.TrimPrefix(p, "file:")); err != nil {
			return err
		}
	case "pane":
		if !contains(paneVerbs, a.Literal(0)) {
			return fmt.Errorf("pane takes one of %s", strings.Join(paneVerbs, ", "))
		}
	}
	return nil
}

// splitTop splits s at sep outside quotes and brackets, as byte ranges.
func splitTop(s string, sep byte) [][2]int {
	var out [][2]int
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
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
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case sep:
			if depth == 0 {
				out = append(out, [2]int{start, i})
				start = i + 1
			}
		}
	}
	return append(out, [2]int{start, len(s)})
}

// parseSort reads field, field:asc or field:desc.
func parseSort(s string) bool {
	f, dir, has := strings.Cut(s, ":")
	return f != "" && (!has || dir == "asc" || dir == "desc")
}
