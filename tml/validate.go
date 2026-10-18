package tml

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// ValidateOptions changes what Validate checks. The zero value uses
// DefaultLimits and checks bindings against the document's state.
type ValidateOptions struct {
	Limits *Limits
	// NoStateCheck skips the binding_missing warnings, for a document whose
	// state arrives later in a patch.
	NoStateCheck bool
}

// Validate checks a tree against the catalog: component types, attributes
// and their types, required attributes, allowed children, theme roles,
// bindings, actions and limits. It returns every problem it found, up to
// Limits.Problems, errors and warnings both. The error is a *Error when at
// least one problem is an error. A document with warnings alone renders.
func Validate(doc *Node, opts *ValidateOptions) ([]Problem, error) {
	v := newValidator(doc, opts)
	v.run()
	if v.errors > 0 {
		return v.problems, &Error{Problems: v.problems}
	}
	return v.problems, nil
}

// Check parses and validates a document in either encoding. A parse error
// comes back as the one problem.
func Check(src []byte, opts *ValidateOptions) (*Node, []Problem, error) {
	var lim *Limits
	if opts != nil {
		lim = opts.Limits
	}
	doc, err := Parse(src, &ParseOptions{Limits: lim})
	if err != nil {
		if e, ok := err.(*Error); ok {
			for i := range e.Problems {
				e.Problems[i].Severity = SevError
			}
			return nil, e.Problems, e
		}
		return nil, nil, err
	}
	problems, err := Validate(doc, opts)
	return doc, problems, err
}

type validator struct {
	doc      *Node
	lim      Limits
	opts     ValidateOptions
	state    map[string]any
	problems []Problem
	errors   int
	ids      map[string]string // id -> pointer
	tabRefs  []tabRef
	nodes    int
}

type tabRef struct {
	id  string
	at  string
	pos Pos
}

// scope is what names an expression may start with at a node.
type scope struct {
	roots  map[string]bool
	inForm bool
}

func newValidator(doc *Node, opts *ValidateOptions) *validator {
	v := &validator{doc: doc, ids: map[string]string{}}
	if opts != nil {
		v.opts = *opts
	}
	v.lim = v.opts.Limits.resolve()
	return v
}

// room reports whether a problem of severity sev would be kept. Every error
// is counted, kept or not. The last slot is kept for an error, so warnings
// cannot push every error out of the report.
func (v *validator) room(sev Severity) bool {
	if sev == SevWarning {
		return len(v.problems) < v.lim.Problems-1
	}
	return len(v.problems) < v.lim.Problems
}

func (v *validator) add(p Problem, pos Pos) {
	if p.Severity == "" {
		p.Severity = SevError
	}
	if p.Severity == SevError {
		v.errors++
	}
	if !v.room(p.Severity) {
		return
	}
	if p.Line == 0 && pos.Line > 0 {
		p.Line, p.Col = pos.Line, pos.Col
	}
	if len(p.Message) > 400 {
		p.Message = cutRunes(p.Message, 400) + "..."
	}
	v.problems = append(v.problems, p)
}

func (v *validator) run() {
	root := v.doc
	if root == nil || root.IsText() {
		v.add(Problem{Code: CodeBadRoot, Message: "The document is empty. Start with a tml root: {\"type\": \"tml\", \"version\": 1}."}, Pos{})
		return
	}
	if comp := lookup(root.Type); comp != nil && root.Type != "tml" && !comp.Block {
		v.add(Problem{Code: CodeBadRoot, Message: fmt.Sprintf("<%s> cannot be the root. Use tml, or a block component such as row.", root.Type)}, root.Pos)
	}
	if sv, ok := root.Attr("state"); ok && root.Type == "tml" {
		if sv.Kind != KindObject {
			v.add(Problem{At: "/state", Code: CodeBadValue, Message: "state must be a JSON object, such as {\"count\": 0}."}, root.Pos)
		} else if st, err := root.State(); err != nil {
			// The store loads the state the same way, so a state the store
			// refuses fails here too.
			v.add(Problem{At: "/state", Code: CodeBadValue, Message: fmt.Sprintf("The state does not load: %v.", err)}, root.Pos)
		} else if _, err := NewStore(st, &v.lim); err != nil {
			v.add(Problem{At: "/state", Code: CodeLimit, Message: sentence(strings.ToUpper(err.Error()[:1]) + err.Error()[1:])}, root.Pos)
		} else {
			v.state = st
		}
	}
	sc := scope{roots: map[string]bool{"state": true, "view": true}}
	v.node(root, "", nil, sc, 1)
	for _, r := range v.tabRefs {
		ptr, ok := v.ids[r.id]
		if !ok || v.doc.At(ptr) == nil || v.doc.At(ptr).Type != "tab" {
			v.add(Problem{At: r.at, Code: CodeBadAction, Message: fmt.Sprintf("tab('%s') names no tab. Give a <tab> that id.", r.id), Accepted: v.tabIDs()}, r.pos)
		}
	}
	for _, msg := range measure(root, v.state).check(v.lim) {
		v.add(Problem{Code: CodeLimit, Message: msg}, Pos{})
	}
}

func (v *validator) tabIDs() []string {
	var out []string
	v.doc.Walk(func(n *Node, _ string) bool {
		if n.Type == "tab" && n.ID() != "" {
			out = append(out, n.ID())
		}
		return true
	})
	sort.Strings(out)
	return out
}

// childPointer is the pointer of child i of the node at ptr. A node parsed
// from JSON keeps its index in the source list, so the pointer matches what
// the author wrote even where empty strings were dropped.
func childPointer(ptr string, c *Node, i int) string {
	if c.src > 0 {
		i = c.src - 1
	}
	return ptr + "/children/" + strconv.Itoa(i)
}

// candidates are the component types legal where a child of parent sits.
func candidates(parent *Component) []string {
	if parent != nil {
		return parent.accepted()
	}
	out := []string{"tml"}
	for i := range table {
		if table[i].Block {
			out = append(out, table[i].Name)
		}
	}
	return out
}

func (v *validator) node(n *Node, ptr string, parent *Component, sc scope, depth int) {
	v.nodes++
	if v.nodes == v.lim.Nodes+1 {
		v.add(Problem{At: ptr, Code: CodeLimit, Message: fmt.Sprintf("The document has more than %d nodes.", v.lim.Nodes)}, n.Pos)
	}
	if depth == v.lim.Depth+1 {
		v.add(Problem{At: ptr, Code: CodeLimit, Message: fmt.Sprintf("The document nests deeper than %d levels.", v.lim.Depth)}, n.Pos)
		return
	}
	if n.IsText() {
		// Raw content (code, markdown, diff, mermaid, kbd) is literal text.
		if parent == nil || parent.Content != ContentRaw {
			v.text(n, ptr, sc)
		}
		return
	}
	if n.Type == "state" {
		v.add(Problem{At: ptr, Code: CodeMisplacedState, Message: "state belongs on the tml root only."}, n.Pos)
		return
	}
	comp := lookup(n.Type)
	if comp == nil && v.room(SevError) {
		p := Problem{At: ptr, Code: CodeUnknownComponent, Message: fmt.Sprintf("<%s> is not a TML component.", n.Type)}
		legal := candidates(parent)
		if a := componentAlias(strings.ToLower(n.Type)); a != "" && slices.Contains(legal, a) {
			p.DidYouMean = a
		} else {
			p.DidYouMean = suggest(n.Type, legal)
		}
		if parent != nil {
			p.Accepted = legal
		}
		v.add(p, n.Pos)
	} else if comp == nil {
		v.errors++
	}

	if id := n.ID(); id != "" {
		if prev, dup := v.ids[id]; dup {
			v.add(Problem{At: ptr + "/id", Code: CodeDuplicateID, Message: fmt.Sprintf("The id %q is also used at %s. Each id names one node.", id, pointerOrRoot(prev))}, n.Pos)
		} else {
			v.ids[id] = ptr
		}
	}

	// each binds its element name for the node itself.
	nodeScope := sc
	if _, ok := n.Attr("each"); ok {
		nodeScope = sc.with(aliasOf(n))
	}
	if n.Type == "form" {
		nodeScope = nodeScope.form()
	}
	if comp != nil {
		v.attrs(n, comp, ptr, nodeScope)
	}

	// Children.
	childScope := nodeScope
	if n.Type == "list" {
		alias := n.AttrString("as")
		if alias == "" {
			alias = "item"
		}
		childScope = nodeScope.with(alias)
	}
	content := ContentBlocks
	if comp != nil {
		content = comp.Content
	}
	count := 0
	for i, c := range n.Children {
		cptr := childPointer(ptr, c, i)
		if c.IsText() {
			if comp != nil && content != ContentInline && content != ContentRaw {
				v.add(Problem{At: cptr, Code: CodeTextNotAllowed, Message: fmt.Sprintf("<%s> holds components, not text. Put the text in <text>.", n.Type), DidYouMean: "text"}, c.Pos)
			}
		} else {
			count++
			if comp != nil && lookup(c.Type) != nil && !comp.Accepts(c.Type) {
				msg := fmt.Sprintf("<%s> cannot hold <%s>.", n.Type, c.Type)
				if content == ContentNone || content == ContentRaw {
					msg = fmt.Sprintf("<%s> holds no components.", n.Type)
				}
				v.add(Problem{At: cptr, Code: CodeBadChild, Message: msg, Accepted: comp.accepted()}, c.Pos)
			}
		}
		next := childScope
		if n.Type == "table" && c.Type == "column" {
			next = childScope.with("value").with("row")
		}
		v.node(c, cptr, comp, next, depth+1)
	}
	if comp != nil {
		if comp.MinChildren > 0 && count < comp.MinChildren {
			v.add(Problem{At: ptr, Code: CodeChildCount, Message: fmt.Sprintf("<%s> needs at least %d %s.", n.Type, comp.MinChildren, childWord(comp)), Accepted: comp.accepted()}, n.Pos)
		}
		if comp.MaxChildren > 0 && count > comp.MaxChildren {
			v.add(Problem{At: ptr, Code: CodeChildCount, Message: fmt.Sprintf("<%s> holds at most %d %s.", n.Type, comp.MaxChildren, childWord(comp))}, n.Pos)
		}
	}
}

func childWord(c *Component) string {
	if c.Content == ContentOnly && len(c.Children) == 1 {
		return "<" + c.Children[0] + "> children"
	}
	return "children"
}

func (s scope) with(name string) scope {
	roots := make(map[string]bool, len(s.roots)+1)
	for k := range s.roots {
		roots[k] = true
	}
	roots[name] = true
	return scope{roots: roots, inForm: s.inForm}
}

func (s scope) form() scope {
	out := s.with("form")
	out.inForm = true
	return out
}

func (s scope) names() []string {
	out := make([]string, 0, len(s.roots))
	for k := range s.roots {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (v *validator) text(n *Node, ptr string, sc scope) {
	if !strings.Contains(n.Text, "{") && !strings.Contains(n.Text, "}") {
		return
	}
	t, err := ParseTemplate(n.Text)
	if err != nil {
		te := err.(*TemplateError)
		v.add(Problem{At: ptr, Code: CodeBadExpression, Message: fmt.Sprintf("The binding in this text does not parse: %s Write {{ for a literal {.", sentence(te.Message))}, n.Pos)
		return
	}
	for _, e := range t.Exprs() {
		v.expr(e, ptr, n.Pos, sc)
	}
}

func sentence(s string) string {
	if s == "" {
		return s
	}
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s
}

// expr checks the roots, functions and state paths of one expression.
func (v *validator) expr(e *Expr, at string, pos Pos, sc scope) {
	rs := map[string]int{}
	roots(e.root, rs)
	names := make([]string, 0, len(rs))
	for name := range rs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !sc.roots[name] {
			p := Problem{At: at, Code: CodeUnknownRoot, Message: fmt.Sprintf("%s is not a name here. A path starts with one of: %s.", name, strings.Join(sc.names(), ", ")),
				DidYouMean: suggest(name, sc.names()), Accepted: sc.names()}
			if name == "form" {
				p.Message = "form is only known inside a <form>."
			}
			v.add(p, pos)
		}
	}
	calls(e.root, func(c *callNode) {
		if isFunc(c.name) {
			return
		}
		p := Problem{At: at, Code: CodeUnknownFunction, Message: fmt.Sprintf("%s is not a function.", c.name), Accepted: Funcs()}
		if _, isAction := findAction(c.name); isAction {
			p.Message = fmt.Sprintf("%s is an action. Use it in an on-* attribute, such as on-press.", c.name)
		} else if alias := funcAlias(strings.ToLower(c.name)); alias != "" {
			p.DidYouMean = alias
		} else {
			p.DidYouMean = suggest(c.name, Funcs())
		}
		v.add(p, pos)
	})
	if v.state == nil || v.opts.NoStateCheck {
		return
	}
	for _, d := range e.Deps() {
		if d.Root != "state" || d.Path == "" {
			continue
		}
		toks, _ := splitPointer(d.Path)
		if _, err := get(v.state, toks); err == nil {
			continue
		}
		avail := v.available(toks)
		want := pointerToPath(d.Path)
		v.add(Problem{At: at, Code: CodeBindingMissing, Severity: SevWarning,
			Message:    fmt.Sprintf("{%s} reads a path the state does not have.", want),
			DidYouMean: suggestPath(want, avail), Available: avail}, pos)
	}
}

// available lists the paths beside a missing one, at the deepest level that
// exists.
func (v *validator) available(toks []string) []string {
	var cur any = v.state
	prefix := "state"
	for _, t := range toks[:len(toks)-1] {
		next, err := get(cur, []string{t})
		if err != nil {
			break
		}
		cur = next
		prefix += "." + t
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, prefix+"."+k)
	}
	sort.Strings(out)
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// suggestPath picks the available path whose last name is closest to the
// last name of want: state.cuont suggests state.count.
func suggestPath(want string, avail []string) string {
	last := func(p string) string { return p[strings.LastIndexAny(p, ".[")+1:] }
	names := make([]string, len(avail))
	for i, a := range avail {
		names[i] = last(a)
	}
	s := suggest(last(want), names)
	if s == "" {
		return ""
	}
	for i, n := range names {
		if n == s {
			return avail[i]
		}
	}
	return ""
}

func pointerToPath(ptr string) string {
	toks, _ := splitPointer(ptr)
	var b strings.Builder
	b.WriteString("state")
	for _, t := range toks {
		if _, err := strconv.Atoi(t); err == nil {
			b.WriteString("[" + t + "]")
		} else {
			b.WriteString("." + t)
		}
	}
	return b.String()
}

// funcAlias maps a function name from another language to the TML one.
func funcAlias(s string) string {
	switch s {
	case "percent", "percentage":
		return "pct"
	case "length", "size":
		return "len"
	case "average", "mean":
		return "avg"
	case "floor", "ceil":
		return "round"
	case "uppercase":
		return "upper"
	case "lowercase":
		return "lower"
	case "includes", "has":
		return "contains"
	case "startswith":
		return "starts"
	case "duration":
		return "dur"
	case "since", "timeago":
		return "ago"
	case "concat":
		return "join"
	case "pluck", "map":
		return "pick"
	case "sprintf", "printf", "str", "string", "tostring":
		return "fmt"
	case "filesize":
		return "bytes"
	}
	return ""
}

// Byte checks for the small value grammars. Each is what a regular
// expression would say, written out, so nothing is compiled at start-up.

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}
func isHex(c byte) bool { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

func allBytes(s string, ok func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

// isHexColor matches #rgb up to #rrggbbaa.
func isHexColor(s string) bool {
	return len(s) >= 4 && len(s) <= 9 && s[0] == '#' && allBytes(s[1:], isHex)
}

// isDuration matches 300ms, 5s, 2m and 1h.
func isDuration(s string) bool {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i == 0 {
		return false
	}
	switch s[i:] {
	case "ms", "s", "m", "h":
		return true
	}
	return false
}

// isIdent matches a name: a letter or _, then letters, digits, - and _.
func isIdent(s string) bool {
	return s != "" && len(s) <= 64 && isLetter(s[0]) &&
		allBytes(s, func(c byte) bool { return isLetter(c) || isDigit(c) || c == '-' })
}

// isField matches a field name, or a comma list of them.
func isField(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimLeft(part, " \t")
		if part == "" || !isLetter(part[0]) ||
			!allBytes(part, func(c byte) bool { return isLetter(c) || isDigit(c) || c == '-' || c == '.' }) {
			return false
		}
	}
	return true
}

// isKey matches a key name: modifiers joined by +, then a key such as enter,
// r or ?.
func isKey(s string) bool {
	parts := strings.Split(s, "+")
	for i, p := range parts {
		if p == "" {
			return false
		}
		if i < len(parts)-1 {
			if !allBytes(p, func(c byte) bool { return c >= 'a' && c <= 'z' }) {
				return false
			}
			continue
		}
		if len(p) == 1 {
			return p[0] >= '!' && p[0] <= '~'
		}
		return allBytes(p, func(c byte) bool { return c >= 'a' && c <= 'z' || isDigit(c) })
	}
	return false
}

// isSize matches cells (12), a percentage (50%), fill and fill:2.
func isSize(s string) bool {
	switch {
	case s == "fill":
		return true
	case strings.HasPrefix(s, "fill:"):
		return len(s) > 5 && allBytes(s[5:], isDigit)
	case strings.HasSuffix(s, "%"):
		return len(s) > 1 && allBytes(s[:len(s)-1], isDigit)
	}
	return s != "" && allBytes(s, isDigit)
}

// dataImagePrefix returns the data:image prefix of s, or "".
func dataImagePrefix(s string) string {
	for _, t := range [...]string{"png", "jpeg", "gif", "webp"} {
		p := "data:image/" + t + ";base64,"
		if strings.HasPrefix(s, p) {
			return p
		}
	}
	return ""
}

func looksLikeColor(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return isHexColor(l) || strings.HasPrefix(l, "rgb(") || strings.HasPrefix(l, "rgba(") || strings.HasPrefix(l, "hsl(")
}

func (v *validator) attrs(n *Node, comp *Component, ptr string, sc scope) {
	for _, a := range n.Attrs {
		at := ptr + "/" + escapePointer(a.Name)
		if a.Name == "state" && n.Type == "tml" {
			continue
		}
		spec, ok := comp.Spec(a.Name)
		if !ok {
			v.unknownAttr(n, comp, a, at)
			continue
		}
		v.value(n, a, spec, at, sc)
	}
	var missing []string
	for _, name := range comp.AttrNames() {
		spec, _ := comp.Spec(name)
		if _, has := n.Attr(name); spec.Required && !has {
			missing = append(missing, name)
		}
	}
	for _, name := range missing {
		spec, _ := comp.Spec(name)
		v.add(Problem{At: ptr, Code: CodeMissingAttribute, Message: fmt.Sprintf("<%s> needs the %s attribute. %s", n.Type, name, spec.Doc), Accepted: []string{name}}, n.Pos)
	}
}

func (v *validator) unknownAttr(n *Node, comp *Component, a Attr, at string) {
	if !v.room(SevError) {
		v.errors++
		return
	}
	lname := strings.ToLower(a.Name)
	if n.Type == "input" && (lname == "password" || lname == "secret" || a.Value.Text() == "password") {
		v.add(Problem{At: at, Code: CodePasswordRefused, Message: "TML has no password input in version 1. Ask for secrets outside the document."}, a.Pos)
		return
	}
	p := Problem{At: at, Code: CodeUnknownAttribute, Message: fmt.Sprintf("%s has no attribute '%s'.", n.Type, a.Name),
		DidYouMean: suggest(a.Name, comp.AttrNames()), Accepted: comp.AttrNames()}
	switch lname {
	case "color", "colour", "fg", "bg", "background", "foreground", "style":
		if _, hasRole := comp.Spec("role"); hasRole {
			p.DidYouMean = "role"
		}
	case "onclick", "on-click", "onpress", "on_press":
		if _, has := comp.Spec("on-press"); has {
			p.DidYouMean = "on-press"
		}
	}
	v.add(p, a.Pos)
}

func (v *validator) bad(a Attr, at, format string, args ...any) {
	v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf(format, args...)}, a.Pos)
}

// value checks one attribute value against its spec.
func (v *validator) value(n *Node, a Attr, spec AttrSpec, at string, sc scope) {
	val := a.Value
	text := val.Text()
	// A binding stands in for any value of a bindable attribute.
	if spec.Bind && val.Kind == KindString && strings.ContainsAny(text, "{}") {
		t, err := ParseTemplate(text)
		if err != nil {
			te := err.(*TemplateError)
			v.add(Problem{At: at, Code: CodeBadExpression, Message: fmt.Sprintf("The binding in %s does not parse: %s Write {{ for a literal {.", a.Name, sentence(te.Message))}, a.Pos)
			return
		}
		for _, e := range t.Exprs() {
			v.expr(e, at, a.Pos, sc)
		}
		if t.HasExpr() {
			if spec.Type != TString && len(t.parts) != 1 {
				v.bad(a, at, "%s takes a %s or one binding such as {state.x}, not text around a binding.", a.Name, spec.Type)
			}
			return
		}
	}
	switch spec.Type {
	case TString:
	case TInt, TNumber:
		f, ok := val.Float()
		if !ok {
			v.bad(a, at, "%s must be a number, not %q.", a.Name, text)
			return
		}
		if spec.Type == TInt && f != float64(int64(f)) {
			v.bad(a, at, "%s must be a whole number, not %s.", a.Name, text)
			return
		}
		if spec.HasRange && (f < spec.Min || f > spec.Max) {
			v.bad(a, at, "%s must be from %s to %s, not %s.", a.Name, formatNum(spec.Min), formatNum(spec.Max), text)
		}
	case TBool:
		if val.Kind != KindBool {
			v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("%s must be true or false, not %q.", a.Name, text), Accepted: []string{"false", "true"}}, a.Pos)
		}
	case TEnum:
		if !contains(spec.Enum, text) {
			v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("%s cannot be %q.", a.Name, text), DidYouMean: suggest(text, spec.Enum), Accepted: spec.Enum}, a.Pos)
		}
	case TRole:
		v.role(a, at, text, false)
	case TAccent:
		v.role(a, at, text, true)
	case TSize:
		if val.Kind == KindNumber {
			if f, _ := val.Float(); f < 0 || f != float64(int64(f)) {
				v.bad(a, at, "%s must be a whole number of cells, not %s.", a.Name, text)
			}
			return
		}
		if !isSize(text) {
			v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("%s must be cells (12), a percentage (50%%), fill or fill:2, not %q.", a.Name, text)}, a.Pos)
			return
		}
		if strings.HasSuffix(text, "%") {
			if p, _ := strconv.Atoi(strings.TrimSuffix(text, "%")); p > 100 {
				v.bad(a, at, "%s cannot be more than 100%%.", a.Name)
			}
		}
	case TPad:
		parts := strings.Fields(text)
		ok := len(parts) == 1 || len(parts) == 2 || len(parts) == 4
		for _, p := range parts {
			if n, err := strconv.Atoi(p); err != nil || n < 0 || n > 100 {
				ok = false
			}
		}
		if !ok {
			v.bad(a, at, "pad must be 1, 2 or 4 cell counts, such as \"1\" or \"0 1\", not %q.", text)
		}
	case TData, TPath:
		v.dataAttr(a, spec, at, sc)
	case TExpr:
		e, off, err := exprOrBinding(text)
		if err != nil {
			v.add(Problem{At: at, Code: CodeBadExpression, Message: fmt.Sprintf("%s does not parse at character %d: %s", a.Name, off+1, sentence(exprMsg(err)))}, a.Pos)
			return
		}
		esc := sc
		if n.Type == "column" && a.Name == "format" {
			esc = sc.with("value").with("row")
		}
		v.expr(e, at, a.Pos, esc)
	case TAction:
		v.actions(a, at, sc)
	case TDuration:
		if !isDuration(text) {
			v.bad(a, at, "%s must be a duration such as 300ms, 5s or 2m, not %q.", a.Name, text)
		}
	case THref:
		switch {
		case strings.HasPrefix(text, "file:"):
			if err := checkFilePath(strings.TrimPrefix(text, "file:")); err != nil {
				v.bad(a, at, "%s", sentence(err.Error()))
			}
		case strings.HasPrefix(text, "https://"):
		default:
			v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("href must start with file: or https://, not %q.", text), Accepted: []string{"file:", "https://"}}, a.Pos)
		}
	case TSrc:
		v.src(a, at, text)
	case TIdent:
		if !isIdent(text) {
			v.bad(a, at, "%s must be a name of letters, digits, - and _, not %q.", a.Name, text)
		}
	case TField:
		if !isField(text) {
			v.bad(a, at, "%s must be a field name, or a comma list of them, not %q.", a.Name, text)
		}
	case TSort:
		if !parseSort(text) {
			v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("sort must be field, field:asc or field:desc, not %q.", text)}, a.Pos)
		}
	case TKey:
		if !isKey(text) {
			v.bad(a, at, "%s must be a key name such as enter, esc, r or ctrl+s, not %q.", a.Name, text)
		}
	case TPattern:
		if len(text) > 256 {
			v.bad(a, at, "pattern is longer than 256 characters.")
		} else if _, err := regexp.Compile(text); err != nil {
			// The regexp package's own text varies by Go release, so it
			// stays out of the message.
			v.bad(a, at, "pattern is not a valid RE2 regular expression. Check its brackets, parentheses and escapes.")
		}
	case TObject:
		if val.Kind != KindObject {
			v.bad(a, at, "%s must be a JSON object.", a.Name)
		}
	}
}

func exprMsg(err error) string {
	if se, ok := err.(*SyntaxError); ok {
		return se.Message
	}
	return err.Error()
}

func (v *validator) role(a Attr, at, text string, accent bool) {
	if slices.Contains(roles[:], text) {
		return
	}
	if accent && strings.HasPrefix(text, "slot:") {
		slot := strings.TrimPrefix(text, "slot:")
		if !slices.Contains(slots[:], slot) {
			p := Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("%q is not an ANSI slot.", cutRunes(slot, 64)), Accepted: Slots()}
			if s := suggest(slot, slots[:]); s != "" {
				p.DidYouMean = "slot:" + s
			}
			v.add(p, a.Pos)
		}
		return
	}
	if looksLikeColor(text) {
		v.add(Problem{At: at, Code: CodeHexColor, Message: fmt.Sprintf("%s takes a theme role, never a colour value such as %q. tuios picks the colour from the theme.", a.Name, text),
			Accepted: Roles()}, a.Pos)
		return
	}
	p := Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("%q is not a theme role.", cutRunes(text, 64)), Accepted: Roles()}
	if r := roleAlias(strings.ToLower(text)); r != "" {
		p.DidYouMean = r
	} else {
		p.DidYouMean = suggest(text, roles[:])
	}
	v.add(p, a.Pos)
}

func (v *validator) dataAttr(a Attr, spec AttrSpec, at string, sc scope) {
	val := a.Value
	if val.Kind == KindArray {
		if spec.Type == TPath {
			v.bad(a, at, "%s takes a state path such as state.tab, not a literal.", a.Name)
		}
		return
	}
	text := val.Text()
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(text), "{"), "}")
		v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("%s takes a path without braces.", a.Name), DidYouMean: inner}, a.Pos)
		return
	}
	e, err := ParseExpr(text)
	if err != nil {
		v.add(Problem{At: at, Code: CodeBadExpression, Message: fmt.Sprintf("%s must be a path such as state.items: %s", a.Name, sentence(exprMsg(err)))}, a.Pos)
		return
	}
	root, _, dyn, ok := staticPath(e.root)
	if !ok || dyn {
		v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("%s must be a path of names, such as state.items, not %q.", a.Name, text)}, a.Pos)
		return
	}
	if spec.Type == TPath && root != "state" {
		v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("%s must be a path into the state, such as state.tab.", a.Name)}, a.Pos)
		return
	}
	v.expr(e, at, a.Pos, sc)
}

func (v *validator) actions(a Attr, at string, sc scope) {
	acts, err := ParseActions(a.Value.Text())
	if err != nil {
		p := Problem{At: at, Code: CodeBadAction, Message: fmt.Sprintf("%s does not parse: %s", a.Name, sentence(exprMsg(err))), Accepted: ActionNames()}
		if se, ok := err.(*SyntaxError); ok {
			p.DidYouMean = se.Suggest
		}
		v.add(p, a.Pos)
		return
	}
	asc := sc.with("event")
	for _, act := range acts {
		if act.Name == "tab" {
			v.tabRefs = append(v.tabRefs, tabRef{act.Literal(0), at, a.Pos})
		}
		for i, arg := range act.Args {
			if spec, _ := findAction(act.Name); i < spec.literal {
				continue
			}
			v.expr(arg, at, a.Pos, asc)
		}
	}
}

func (v *validator) src(a Attr, at, text string) {
	switch {
	case strings.HasPrefix(text, "file:"):
		if err := checkFilePath(strings.TrimPrefix(text, "file:")); err != nil {
			v.bad(a, at, "%s", sentence(err.Error()))
		}
	case strings.HasPrefix(text, "data:"):
		m := dataImagePrefix(text)
		if m == "" {
			v.add(Problem{At: at, Code: CodeBadValue, Message: "A data: image must be data:image/png, jpeg, gif or webp with ;base64,.", Accepted: []string{"data:image/png;base64,", "data:image/jpeg;base64,", "data:image/gif;base64,", "data:image/webp;base64,"}}, a.Pos)
			return
		}
		payload := text[len(m):]
		if base64.StdEncoding.DecodedLen(len(payload)) > v.lim.ImageBytes {
			v.add(Problem{At: at, Code: CodeLimit, Message: fmt.Sprintf("The image is over %d bytes.", v.lim.ImageBytes)}, a.Pos)
			return
		}
		if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
			v.bad(a, at, "The image data is not valid base64.")
		}
	default:
		v.add(Problem{At: at, Code: CodeBadValue, Message: fmt.Sprintf("src must be a file: path or a data:image URI, not %q. TML fetches nothing from the network.", text), Accepted: []string{"file:", "data:image/"}}, a.Pos)
	}
}

// checkFilePath refuses a path that leaves the owner's files folder.
func checkFilePath(p string) error {
	if p == "" {
		return fmt.Errorf("the file: path is empty")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.Contains(p, "\\") || (len(p) > 1 && p[1] == ':') {
		return fmt.Errorf("a file: path is relative to the owner's files folder; %q is not", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return fmt.Errorf("a file: path cannot hold .. segments")
		}
	}
	return nil
}
