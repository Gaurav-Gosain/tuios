package loom

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/plot"
	"tuios.dev/tml"
)

// inline turns the children of a text-like node into styled spans: text
// runs with their bindings, and b, i, u, s, code, c, kbd and a.
func (s *Surface) inline(n *tml.Node, vars map[string]any, base style) []span {
	var out []span
	var walk func(n *tml.Node, st style)
	walk = func(n *tml.Node, st style) {
		for _, c := range n.Children {
			if c.IsText() {
				t := s.template(c, "", c.Text)
				text := c.Text
				if t != nil && t.HasExpr() {
					text = t.Render(s.env(vars))
				} else if t != nil {
					v, _ := t.Eval(nil)
					text = tml.Stringify(v)
				}
				out = append(out, span{text: text, st: st})
				continue
			}
			cs := st
			switch c.Type {
			case "b":
				cs = cs.with(attrBold)
			case "i":
				cs = cs.with(attrItalic)
			case "u":
				cs = cs.with(attrUnderline)
			case "s":
				cs = cs.with(attrStrike)
			case "code":
				cs.fg = "info"
			case "kbd":
				cs.fg = "accent-bright"
				cs = cs.with(attrBold)
			case "c":
				if r := s.str(c, "role", vars); validRole(r) {
					cs.fg = r
				}
			case "a":
				cs.fg = "info"
				cs = cs.with(attrUnderline)
				if href := c.AttrString("href"); strings.HasPrefix(href, "https:") || strings.HasPrefix(href, "file:") {
					cs.link = href
				}
			}
			if tml.ContentOf(c.Type) == tml.ContentRaw {
				for _, r := range c.Children {
					if r.IsText() {
						out = append(out, span{text: r.Text, st: cs})
					}
				}
				continue
			}
			walk(c, cs)
		}
	}
	walk(n, base)
	return out
}

// plainText returns the text of a node's children without styles.
func (s *Surface) plainText(n *tml.Node, vars map[string]any) string {
	var b strings.Builder
	for _, sp := range s.inline(n, vars, style{}) {
		b.WriteString(sp.text)
	}
	return b.String()
}

// validRole reports whether a document may name r: a TML role. loom's own
// roles (track, cursor, pill, heat1 to heat5) are for its own marks.
func validRole(r string) bool { return r != "" && slices.Contains(docRoles, r) }

var docRoles = tml.Roles()

// roleOr returns the role attribute when it is valid, else def.
func (s *Surface) roleOr(n *tml.Node, vars map[string]any, def string) string {
	if r := s.str(n, "role", vars); validRole(r) {
		return r
	}
	return def
}

// buildLeaf builds the widget of a component with no child boxes.
func (s *Surface) buildLeaf(b *box, vars map[string]any) widget {
	n := b.src
	switch n.Type {
	case "spacer":
		return nil
	case "text":
		w := &textWidget{spans: s.inline(n, vars, style{fg: s.roleOr(n, vars, "fg")})}
		w.wrap = wrapModeOf(n.AttrString("wrap"))
		w.align = alignOf(n.AttrString("align"))
		w.press = s.actionsOf(n, "on-press")
		return w
	case "heading":
		return s.buildHeading(b, vars)
	case "toast":
		role := s.roleOr(n, vars, "info")
		return &badgeWidget{label: s.plainText(n, vars), role: role, block: true}
	case "badge":
		return &badgeWidget{label: s.plainText(n, vars), role: s.roleOr(n, vars, "accent"), press: s.actionsOf(n, "on-press")}
	case "stat":
		return &statWidget{label: s.str(n, "label", vars), value: s.str(n, "value", vars),
			delta: s.str(n, "delta", vars), role: s.roleOr(n, vars, "fg"), press: s.actionsOf(n, "on-press")}
	case "kv":
		return s.buildKV(b, vars)
	case "code", "markdown", "diff", "mermaid":
		return s.buildCode(b, vars)
	case "button":
		kind := n.AttrString("kind")
		return &buttonWidget{label: s.plainText(n, vars), key: n.AttrString("key"), primary: kind == "primary",
			danger: kind == "danger", disabled: s.flag(n, "disabled", vars, false), press: s.actionsOf(n, "on-press")}
	case "status":
		st := s.str(n, "state", vars)
		return &statusWidget{state: st, label: s.str(n, "label", vars)}
	case "spinner":
		return &statusWidget{state: "spinner", label: s.str(n, "label", vars)}
	case "input", "textarea", "select", "toggle", "slider", "checkbox":
		return s.buildField(b, vars)
	case "image":
		alt := s.str(n, "alt", vars)
		return &textWidget{spans: []span{{text: "[" + alt + "]", st: style{fg: "mute", attrs: attrItalic}}}, wrap: wrapNone}
	case "table":
		return s.buildTable(b, vars)
	case "tree":
		return s.buildTree(b, vars)
	case "bar", "line", "area", "sparkline", "gauge", "progress", "histogram", "heatmap", "pie":
		return s.buildChart(b, vars)
	}
	// A component loom does not draw yet, or an unknown one: its name, so
	// the gap is visible and never silent.
	return &textWidget{spans: []span{{text: "<" + n.Type + ">", st: style{fg: "mute"}}}, wrap: wrapNone}
}

func wrapModeOf(s string) wrapMode {
	switch s {
	case "char":
		return wrapChar
	case "none":
		return wrapNone
	}
	return wrapWord
}

func alignOf(s string) textAlign {
	switch s {
	case "center":
		return alignCenter
	case "right":
		return alignRight
	}
	return alignLeft
}

// textWidget is a paragraph of styled spans.
type textWidget struct {
	spans []span
	wrap  wrapMode
	align textAlign
	rule  bool // a hairline after the text to the right edge (heading 2)
	press []tml.Action

	cacheW int
	lines  []line
}

func (t *textWidget) linesFor(w int) []line {
	if t.lines == nil || t.cacheW != w {
		t.lines = wrap(t.spans, w, t.wrap)
		t.cacheW = w
	}
	return t.lines
}

func (t *textWidget) measure(_ *Surface, _ *box, maxW int) (int, int) {
	lines := t.linesFor(maxW)
	w := 0
	for _, l := range lines {
		w = max(w, l.width())
	}
	if t.rule {
		w = maxW
	}
	return min(w, maxW), len(lines)
}

func (t *textWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	lines := t.linesFor(r.Dx())
	if len(lines) > r.Dy() && r.Dy() > 0 {
		// Too little height: the rows that fit, the last one cut with an
		// ellipsis. One row shows the text unwrapped, so it reads from the
		// start.
		if r.Dy() == 1 {
			lines = wrap(t.spans, r.Dx(), wrapNone)[:1]
		} else {
			lines = append([]line(nil), lines[:r.Dy()]...)
			last := lines[len(lines)-1]
			lines[len(lines)-1] = cutLine(append(last, span{text: " ", st: style{}}), min(last.width()+1, r.Dx()), s.g.ellipsis)
		}
	}
	for i, l := range lines {
		if i >= r.Dy() {
			break
		}
		c.drawLine(r.Min.X, r.Min.Y+i, r.Dx(), l, t.align)
		if t.rule && i == 0 {
			x := r.Min.X + l.width() + 1
			if x < r.Max.X {
				c.hline(x, r.Min.Y, r.Max.X-x, s.g.hline, style{fg: "edge"})
			}
		}
	}
}

func (s *Surface) buildHeading(b *box, vars map[string]any) widget {
	n := b.src
	level := s.count(n, "level", vars, 1, 1, 3)
	st := style{fg: s.roleOr(n, vars, "fg"), attrs: attrBold}
	if level >= 3 {
		st.fg = s.roleOr(n, vars, "dim")
	}
	return &textWidget{spans: s.inline(n, vars, st), wrap: wrapWord, rule: level == 2}
}

// badgeWidget is a short tag on a tint of its role, or a toast.
type badgeWidget struct {
	label string
	role  string
	block bool // a toast: the tint takes the whole width
	press []tml.Action
}

func (w *badgeWidget) text(s *Surface) string {
	if !s.pal.grounded() {
		if s.pal.Depth == ASCII {
			return "[" + w.label + "]"
		}
		return w.label
	}
	return " " + w.label + " "
}

func (w *badgeWidget) measure(s *Surface, _ *box, maxW int) (int, int) {
	if w.block {
		return maxW, 1
	}
	return min(width(w.text(s)), maxW), 1
}

func (w *badgeWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	tint := ""
	switch w.role {
	case "success", "warning", "error", "accent":
		tint = w.role + "-tint"
	default:
		tint = "surface"
	}
	if w.block {
		c.fill(r, tint)
	}
	st := style{fg: w.role, bg: tint, attrs: attrBold}
	c.drawLine(r.Min.X, r.Min.Y, r.Dx(), plainLine(w.text(s), st), alignLeft)
}

// statWidget is the KPI tile: a number with a label, and a change. On one
// row it reads "412 passed +12". Given two rows or more it is a tile: a
// role bar, the label, then the number.
type statWidget struct {
	label, value, delta, role string
	press                     []tml.Action
}

func (w *statWidget) measure(_ *Surface, _ *box, maxW int) (int, int) {
	n := width(w.value) + 1 + width(w.label)
	if w.delta != "" {
		n += 1 + width(w.delta)
	}
	return min(n, maxW), 1
}

func (w *statWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	value := style{fg: w.role, attrs: attrBold}
	if r.Dy() >= 2 {
		c.fill(r, "surface")
		for y := r.Min.Y; y < r.Max.Y; y++ {
			c.text(r.Min.X, y, tileBar(s), style{fg: w.role}, 1)
		}
		in := r.Dx() - 2
		c.drawLine(r.Min.X+2, r.Min.Y, in, plainLine(w.label, style{fg: "dim"}), alignLeft)
		l := line{{text: w.value, st: value}}
		if w.delta != "" {
			l = append(l, span{text: " " + w.delta, st: style{fg: "mute"}})
		}
		c.drawLine(r.Min.X+2, r.Min.Y+1, in, l, alignLeft)
		return
	}
	l := line{{text: w.value, st: value}, {text: " " + w.label, st: style{fg: "dim"}}}
	if w.delta != "" {
		l = append(l, span{text: " " + w.delta, st: style{fg: "mute"}})
	}
	c.drawLine(r.Min.X, r.Min.Y, r.Dx(), l, alignLeft)
}

func tileBar(s *Surface) string {
	if s.pal.Depth == ASCII {
		return "|"
	}
	return "▎"
}

// kvWidget is key and value pairs in two aligned columns.
type kvWidget struct {
	keys, vals []string
	roles      []string
}

func (s *Surface) buildKV(b *box, vars map[string]any) widget {
	w := &kvWidget{}
	for _, c := range b.src.Children {
		if c.Type != "item" {
			continue
		}
		v := vars
		if !s.truthy(c, "if", v) {
			continue
		}
		w.keys = append(w.keys, s.str(c, "k", v))
		w.vals = append(w.vals, s.str(c, "v", v))
		w.roles = append(w.roles, s.roleOr(c, v, "fg"))
	}
	return w
}

func (w *kvWidget) keyW() int {
	n := 0
	for _, k := range w.keys {
		n = max(n, width(k))
	}
	return n + 2
}

func (w *kvWidget) measure(_ *Surface, _ *box, maxW int) (int, int) {
	vw := 0
	for _, v := range w.vals {
		vw = max(vw, width(v))
	}
	return min(w.keyW()+vw, maxW), len(w.keys)
}

func (w *kvWidget) paint(_ *Surface, _ *box, c *canvas, r Rect) {
	kw := min(w.keyW(), r.Dx()/2)
	for i := range w.keys {
		y := r.Min.Y + i
		if y >= r.Max.Y {
			return
		}
		c.drawLine(r.Min.X, y, kw-1, plainLine(w.keys[i], style{fg: "dim"}), alignLeft)
		c.drawLine(r.Min.X+kw, y, r.Dx()-kw, plainLine(w.vals[i], style{fg: w.roles[i]}), alignLeft)
	}
}

// codeWidget is preformatted text: code, and for now markdown, diff and
// mermaid source.
type codeWidget struct {
	lines  []string
	from   int
	number bool
	diff   bool
}

func (s *Surface) buildCode(b *box, vars map[string]any) widget {
	n := b.src
	var raw strings.Builder
	for _, c := range n.Children {
		if c.IsText() {
			raw.WriteString(c.Text)
		}
	}
	text := strings.ReplaceAll(raw.String(), "\t", "    ")
	text = strings.TrimSuffix(strings.TrimPrefix(text, "\n"), "\n")
	w := &codeWidget{lines: strings.Split(text, "\n"), diff: n.Type == "diff"}
	w.number = s.flag(n, "lines", vars, false)
	w.from = s.count(n, "from", vars, 1, 1, 1e9)
	return w
}

func (w *codeWidget) gutter() int {
	if !w.number {
		return 0
	}
	return len(strconv.Itoa(w.from+len(w.lines)-1)) + 1
}

func (w *codeWidget) measure(_ *Surface, _ *box, maxW int) (int, int) {
	return maxW, len(w.lines)
}

func (w *codeWidget) paint(_ *Surface, _ *box, c *canvas, r Rect) {
	c.fill(r, "canvas")
	g := w.gutter()
	for i, l := range w.lines {
		y := r.Min.Y + i
		if y >= r.Max.Y {
			return
		}
		x := r.Min.X + 1
		if g > 0 {
			num := strconv.Itoa(w.from + i)
			c.textRight(x+g-1, y, num, style{fg: "mute"})
			x += g
		}
		st := style{fg: "fg"}
		if w.diff {
			switch {
			case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"):
				st.fg = "dim"
				st = st.with(attrBold)
			case strings.HasPrefix(l, "+"):
				st.fg = "success"
			case strings.HasPrefix(l, "-"):
				st.fg = "error"
			case strings.HasPrefix(l, "@@"):
				st.fg = "info"
			}
		}
		c.drawLine(x, y, r.Max.X-x-1, plainLine(l, st), alignLeft)
	}
}

// buttonWidget is a button: the key in a cap, then the label.
type buttonWidget struct {
	label, key      string
	primary, danger bool
	disabled        bool
	press           []tml.Action
}

func (w *buttonWidget) spans(s *Surface) line {
	if !s.pal.grounded() {
		if w.primary && !w.disabled {
			// Reverse video carries the accent ground at 16 colours.
			st := style{fg: "accent", attrs: attrReverse | attrBold}
			if w.key == "" {
				return line{{text: " " + w.label + " ", st: st}}
			}
			return line{{text: " " + w.key + "  " + w.label + " ", st: st}}
		}
		ink := "fg"
		if w.disabled {
			ink = "mute"
		}
		l := line{{text: "[", st: style{fg: "mute"}}}
		if w.key != "" {
			l = append(l, span{text: w.key, st: style{fg: "accent-bright", attrs: attrBold}}, span{text: " ", st: style{fg: ink}})
		}
		return append(l, span{text: w.label, st: style{fg: ink}}, span{text: "]", st: style{fg: "mute"}})
	}
	// The key and the label share one ground, so the button reads as one
	// object; the key is told apart by its colour and weight.
	keyBg, keyFg, labelBg, labelFg := "surface", "accent-bright", "surface", "fg"
	switch {
	case w.disabled:
		keyBg, keyFg, labelBg, labelFg = "surface", "mute", "surface", "mute"
	case w.primary:
		keyBg, keyFg, labelBg, labelFg = "accent", RolePill, "accent", RolePill
	case w.danger:
		keyBg, keyFg, labelBg, labelFg = "error-tint", "error", "error-tint", "error"
	}
	var l line
	if w.key != "" {
		l = append(l, span{text: " " + w.key + " ", st: style{fg: keyFg, bg: keyBg, attrs: attrBold}})
	}
	return append(l, span{text: " " + w.label + " ", st: style{fg: labelFg, bg: labelBg}})
}

func (w *buttonWidget) measure(s *Surface, _ *box, maxW int) (int, int) {
	return min(w.spans(s).width(), maxW), 1
}

func (w *buttonWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	c.drawLine(r.Min.X, r.Min.Y, r.Dx(), w.spans(s), alignLeft)
}

// statusWidget is the tuios agent state glyph and word, or a spinner.
type statusWidget struct {
	state, label string
}

var statusLook = map[string][4]string{
	// state: unicode glyph, ascii glyph, role, word. The glyphs are the
	// Unicode forms the tuios cockpit uses, which common monospace fonts
	// have; ◐ is in neither JetBrains Mono nor Geist Mono.
	"working":     {"»", "~", "busy", "working"},
	"needs_input": {"●", "!", "attention", "needs input"},
	"idle":        {"○", "o", "mute", "idle"},
	"done":        {"✓", "v", "success", "done"},
	"errored":     {"✕", "x", "error", "errored"},
	"spinner":     {"⠹", "*", "accent-bright", ""},
}

func (w *statusWidget) look() [4]string {
	if l, ok := statusLook[w.state]; ok {
		return l
	}
	return [4]string{"?", "?", "mute", "unknown"}
}

func (w *statusWidget) line(s *Surface) line {
	l := w.look()
	glyph := l[0]
	if s.pal.Depth == ASCII {
		glyph = l[1]
	}
	label := w.label
	if label == "" {
		label = l[3]
	}
	return line{{text: glyph, st: style{fg: l[2]}}, {text: " " + label, st: style{fg: "dim"}}}
}

func (w *statusWidget) measure(s *Surface, _ *box, maxW int) (int, int) {
	return min(w.line(s).width(), maxW), 1
}

func (w *statusWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	c.drawLine(r.Min.X, r.Min.Y, r.Dx(), w.line(s), alignLeft)
}

// fieldWidget draws a form field. An input or a textarea takes typed
// text: see field_edit.go.
type fieldWidget struct {
	kind, label, value, placeholder string
	on, disabled                    bool
	num, lo, hi                     float64
	rows                            int
	// key names the field's view state, bind the state path it writes,
	// and caret is where the next typed text goes, in runes.
	key, bind string
	caret     int
}

const fieldLabelW = 14

func (s *Surface) buildField(b *box, vars map[string]any) widget {
	n := b.src
	w := &fieldWidget{kind: n.Type, label: s.str(n, "label", vars), placeholder: s.str(n, "placeholder", vars),
		disabled: s.flag(n, "disabled", vars, false), rows: s.count(n, "rows", vars, 3, 1, 100)}
	w.key, w.bind = b.base, n.AttrString("bind")
	var v any
	if p := w.bind; p != "" {
		if ptr, err := tml.PathPointer(p); err == nil {
			v = s.store.Get(ptr)
		}
	} else if t, ok := s.view.Text[w.key]; ok {
		v = t
	}
	if v == nil {
		v, _ = s.value(n, "value", vars)
	}
	switch n.Type {
	case "toggle", "checkbox":
		if str, ok := v.(string); ok {
			w.on = str == "true"
		} else {
			w.on = tml.Truthy(v)
		}
	case "slider":
		w.lo = s.num(n, "min", vars, 0)
		w.hi = s.num(n, "max", vars, 100)
		w.num = s.num(n, "value", vars, w.lo)
		if f, ok := v.(float64); ok {
			w.num = f
		}
	case "select":
		w.value = tml.Stringify(v)
		if v == nil {
			if opts := s.data(n, "options", vars); len(opts) > 0 {
				w.value = tml.Stringify(opts[0])
			}
		}
	default:
		if v != nil {
			w.value = tml.Stringify(v)
		}
		w.caret = utf8.RuneCountInString(w.value)
		if c, ok := s.view.Caret[w.key]; ok {
			w.caret = clampInt(c, 0, w.caret)
		}
	}
	return w
}

func (w *fieldWidget) measure(_ *Surface, _ *box, maxW int) (int, int) {
	if w.kind == "textarea" {
		return maxW, w.rows
	}
	return maxW, 1
}

func (w *fieldWidget) paint(s *Surface, b *box, c *canvas, r Rect) {
	x := r.Min.X
	if w.label != "" {
		lw := min(fieldLabelW, r.Dx()/2)
		c.drawLine(x, r.Min.Y, lw-1, plainLine(w.label, style{fg: "dim"}), alignLeft)
		x += lw
	}
	right := r.Max.X
	ink := "fg"
	if w.disabled {
		ink = "mute"
	}
	g := s.g
	switch w.kind {
	case "input", "textarea":
		edge := style{fg: "edge"}
		for row := 0; row < r.Dy(); row++ {
			y := r.Min.Y + row
			c.text(x, y, "▏", edge, 1)
			if s.pal.Depth == ASCII {
				c.text(x, y, "[", edge, 1)
			}
			lines := strings.Split(w.value, "\n")
			if w.kind == "input" {
				lines = []string{strings.ReplaceAll(w.value, "\n", " ")}
			}
			focused := w.editable() && s.focused() == b
			switch {
			case focused:
				// The caret's line, and the lines around it as they fit.
				text := []rune(w.value)
				caretRow := strings.Count(string(text[:clampInt(w.caret, 0, len(text))]), "\n")
				if w.kind == "input" {
					caretRow = 0
				}
				first := max(caretRow-r.Dy()+1, 0)
				i := first + row
				if i == caretRow {
					ls := lineStart(text, w.caret)
					le := lineEnd(text, w.caret)
					if w.kind == "input" {
						ls, le = 0, len(text)
					}
					c.drawLine(x+1, y, right-x-3, caretLine(text[ls:le], w.caret-ls, right-x-3, style{fg: ink}), alignLeft)
				} else if i < len(lines) {
					c.drawLine(x+1, y, right-x-3, plainLine(lines[i], style{fg: ink}), alignLeft)
				}
			case w.value != "" && row < len(lines):
				c.drawLine(x+1, y, right-x-3, plainLine(lines[row], style{fg: ink}), alignLeft)
			case w.value == "" && row == 0:
				c.drawLine(x+1, y, right-x-3, plainLine(w.placeholder, style{fg: "mute"}), alignLeft)
			}
			end := "▕"
			if s.pal.Depth == ASCII {
				end = "]"
			}
			c.text(right-1, y, end, edge, 1)
		}
	case "select":
		c.drawLine(x, r.Min.Y, right-x, line{
			{text: g.left + " ", st: style{fg: "mute"}}, {text: w.value, st: style{fg: ink}}, {text: " " + g.right, st: style{fg: "mute"}},
		}, alignLeft)
	case "toggle":
		var l line
		if w.on {
			l = line{{text: g.sliderKnob, st: style{fg: "accent-bright"}}, {text: g.heavy + g.heavy, st: style{fg: "accent"}}, {text: " on", st: style{fg: ink}}}
		} else {
			l = line{{text: g.hline + g.hline, st: style{fg: "mute"}}, {text: g.radioOff, st: style{fg: "mute"}}, {text: " off", st: style{fg: "mute"}}}
		}
		c.drawLine(x, r.Min.Y, right-x, l, alignLeft)
	case "checkbox":
		mark := "[ ]"
		if w.on {
			mark = "[x]"
		}
		c.drawLine(x, r.Min.Y, right-x, plainLine(mark, style{fg: "accent-bright"}), alignLeft)
	case "slider":
		if math.IsNaN(w.num) || math.IsInf(w.num, 0) {
			w.num = w.lo
		}
		val := plot.Compact(w.num)
		track := right - x - width(val) - 2
		if track < 3 {
			c.drawLine(x, r.Min.Y, right-x, plainLine(val, style{fg: ink}), alignLeft)
			return
		}
		f := 0.0
		if w.hi > w.lo && !math.IsInf(w.hi-w.lo, 0) {
			f = max(0, min(1, (w.num-w.lo)/(w.hi-w.lo)))
		}
		pos := int(f * float64(track-1))
		for i := range track {
			glyph, st := g.heavy, style{fg: "track"}
			if i < pos {
				st.fg = "accent"
			}
			if i == pos {
				glyph, st.fg = g.sliderKnob, "accent-bright"
			}
			c.text(x+i, r.Min.Y, glyph, st, 1)
		}
		c.textRight(right, r.Min.Y, val, style{fg: ink})
	}
}

// treeWidget is a tree of nested state objects, open to a depth.
type treeWidget struct {
	rows []treeRow
}

type treeRow struct {
	depth  int
	label  string
	branch bool
	open   bool
}

func (s *Surface) buildTree(b *box, vars map[string]any) widget {
	n := b.src
	label := n.AttrString("label")
	if label == "" {
		label = "name"
	}
	kids := n.AttrString("children")
	if kids == "" {
		kids = "children"
	}
	open := s.count(n, "open", vars, 1, 0, 32)
	w := &treeWidget{}
	var walk func(items []any, depth int)
	walk = func(items []any, depth int) {
		for _, it := range items {
			if len(w.rows) >= 2000 {
				return
			}
			m, _ := it.(map[string]any)
			sub, _ := m[kids].([]any)
			row := treeRow{depth: depth, label: tml.Stringify(m[label]), branch: len(sub) > 0, open: depth < open}
			w.rows = append(w.rows, row)
			if row.branch && row.open && depth < 32 {
				walk(sub, depth+1)
			}
		}
	}
	walk(s.data(n, "items", vars), 0)
	return w
}

func (w *treeWidget) measure(_ *Surface, _ *box, maxW int) (int, int) {
	mw := 0
	for _, r := range w.rows {
		mw = max(mw, 2*r.depth+2+width(r.label))
	}
	return min(mw, maxW), len(w.rows)
}

func (w *treeWidget) paint(s *Surface, _ *box, c *canvas, r Rect) {
	for i, row := range w.rows {
		y := r.Min.Y + i
		if y >= r.Max.Y {
			return
		}
		x := r.Min.X + 2*row.depth
		mark := " "
		if row.branch {
			mark = s.g.foldShut
			if row.open {
				mark = s.g.foldOpen
			}
		}
		c.text(x, y, mark, style{fg: "mute"}, 1)
		c.drawLine(x+2, y, r.Max.X-x-2, plainLine(row.label, style{fg: "fg"}), alignLeft)
	}
}
