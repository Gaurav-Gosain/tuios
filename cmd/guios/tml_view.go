package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

// TML surfaces in guios (polish wave 1, item 10; notes/plan-laneC.md).
//
// A program, a script or an agent opens a TML surface on the daemon, which
// holds its tree, its state and its version in a window of kind tml. The
// TUI draws it with tuios.dev/loom, into cells. guios draws the same tree
// with its own widgets, built every frame as the rest of guios is, and keeps
// loom's meaning: tuios.dev/tml evaluates the bindings and expressions,
// loom's RunActions runs the actions, and each client keeps its own view
// state in a loom.View.
//
// The surface comes from tml-get, once, and stays current through the
// daemon's tml-surface events: a patch to the version held is applied to
// the store; anything else (a replace, a version missed) reads the surface
// again.
//
// Sizes in a TML document are in terminal cells, so a cell here is the
// terminal font's cell: w="30", pad and gap look as they do in the TUI.
//
// Part 1 draws layout, text and status: col, row, grid, spacer, scroll,
// stack, card, text and its inline styles, heading, code, markdown, badge,
// status, progress, spinner, stat and kv. Any other element says that
// guios does not draw it yet, and the document is never blank.

// tmlView is the app's copy of one surface.
type tmlView struct {
	surface string
	doc     *tml.Node
	store   *tml.Store
	view    *loom.View
	version uint64
	title   string
	loading bool
	err     string
	// tmpl holds the parsed templates of the document's attributes and
	// text runs, and expr the paths of its each attributes, by source.
	tmpl map[string]*tml.Template
	expr map[string]*tml.Expr
	// acts holds the parsed actions of its action attributes, by source.
	acts map[string][]tml.Action
	// text holds what each text field shows while it is typed in, by the
	// field's key (tml_input.go).
	text map[string]*string
	// ask is a pane() action waiting for the person's yes, and confirmed
	// the ones the person said yes to.
	ask       *tmlAsk
	confirmed map[string]bool
	// toasts are when each toast with a ttl first showed (tml_data.go).
	toasts map[string]time.Time
}

// tmlCellW and tmlCellH are a terminal cell in DIPs, at the code font.
const (
	tmlCellW = fsCode * 0.6
	tmlCellH = fsCode * 1.5
)

// tmlFor returns the view of a surface, reading it the first time.
func (a *App) tmlFor(surface string) *tmlView {
	if a.tmls == nil {
		a.tmls = map[string]*tmlView{}
	}
	v := a.tmls[surface]
	if v == nil {
		v = &tmlView{surface: surface, view: loom.NewView(), tmpl: map[string]*tml.Template{}}
		a.tmls[surface] = v
		a.fetchTML(v)
	}
	return v
}

// fetchTML reads a surface whole with tml-get.
func (a *App) fetchTML(v *tmlView) {
	cl := a.cockpitClient()
	if cl == nil || v.loading {
		return
	}
	v.loading = true
	go func() {
		var out struct {
			Doc     json.RawMessage `json:"doc"`
			State   map[string]any  `json:"state"`
			Version uint64          `json:"version"`
			Title   string          `json:"title"`
		}
		err := cl.VerbInto("tml-get", map[string]any{"surface": v.surface}, false, &out)
		var doc *tml.Node
		var store *tml.Store
		if err == nil {
			doc, err = tml.ParseJSON(out.Doc, nil)
		}
		if err == nil {
			store, err = tml.NewStore(out.State, nil)
		}
		a.update(func() {
			v.loading = false
			if err != nil {
				v.err = err.Error()
				return
			}
			v.err, v.doc, v.store, v.version, v.title = "", doc, store, out.Version, out.Title
			v.tmpl, v.expr = map[string]*tml.Template{}, nil
		})
	}()
}

// tmlEvent follows a surface the app holds: a patch to the version it has
// goes to its store, a close drops it, and anything else reads it again.
func (a *App) tmlEvent(ev *proto.DaemonEvent) {
	if ev == nil || ev.TML == nil {
		return
	}
	ch := ev.TML
	v := a.tmls[ch.Surface]
	if v == nil {
		return
	}
	if ch.Closed {
		delete(a.tmls, ch.Surface)
		return
	}
	if v.store != nil && len(ch.Ops) > 0 && ch.Version == v.version+1 {
		ops, err := tml.DecodePatch(ch.Ops, nil)
		if err == nil {
			if _, err = v.store.Apply(ops); err == nil {
				v.version = ch.Version
				return
			}
		}
	}
	if ch.Version != v.version {
		a.fetchTML(v)
	}
}

// paneTML draws a TML pane's surface under its header.
func (a *App) paneTML(c *ui.Context, pal *palette, p *proto.Pane) {
	v := a.tmlFor(p.Surface)
	body := ui.Scroll(c.Key("tml")).Grow(1).MinHeight(0).Padding(10, 14, 14, 14)
	switch {
	case v.doc == nil && v.err != "":
		body.Children(func() {
			ui.Text(c, v.err).FontSize(fsSmall).TextColor(pal.dangerText)
		})
		return
	case v.doc == nil:
		body.Children(func() { ui.Spinner(c) })
		return
	}
	cols := float64(body.Bounds().W / tmlCellW)
	r := &tmlDraw{a: a, c: c, pal: pal, v: v, mono: a.termFamily()}
	r.vars = map[string]any{
		"state": v.store.State(),
		"view":  map[string]any{"width": cols, "height": float64(body.Bounds().H / tmlCellH), "band": tmlBand(cols), "placement": "pane"},
	}
	body.Children(func() {
		ui.Column(c).Gap(tmlCellH / 2).AlignItems(ui.Stretch).Children(func() {
			if v.ask != nil {
				r.a.tmlAskBar(c, pal, v)
			}
			r.children(v.doc, "", r.vars)
		})
	})
}

// tmlBand is loom's width class: narrow under 72 cells, medium under 120.
func tmlBand(cols float64) string {
	switch {
	case cols < 72:
		return "narrow"
	case cols < 120:
		return "medium"
	}
	return "wide"
}

// tmlDraw draws one surface in one frame.
type tmlDraw struct {
	a    *App
	c    *ui.Context
	pal  *palette
	v    *tmlView
	mono string
	vars map[string]any
	// form is the form whose fields are being drawn, for submit.
	form *tmlForm
}

// template is the parsed template of s, parsed once per surface.
func (r *tmlDraw) template(s string) *tml.Template {
	t, ok := r.v.tmpl[s]
	if !ok {
		t, _ = tml.ParseTemplate(s)
		r.v.tmpl[s] = t
	}
	return t
}

// value is an attribute's value: a binding evaluated, else the literal.
func (r *tmlDraw) value(n *tml.Node, attr string, vars map[string]any) (any, bool) {
	v, ok := n.Attr(attr)
	if !ok {
		return nil, false
	}
	switch v.Kind {
	case tml.KindBool:
		return v.Bool, true
	case tml.KindNumber:
		f, _ := v.Float()
		return f, true
	case tml.KindArray, tml.KindObject:
		d, err := v.Decode()
		return tml.SanitizeValue(d), err == nil
	}
	t := r.template(v.Str)
	if t == nil {
		return v.Str, true
	}
	out, err := t.Eval(&tml.Env{Vars: vars})
	if err != nil {
		return nil, true
	}
	return out, true
}

// str is an attribute as text.
func (r *tmlDraw) str(n *tml.Node, attr string, vars map[string]any) string {
	v, ok := r.value(n, attr, vars)
	if !ok || v == nil {
		return ""
	}
	return tml.Stringify(v)
}

// num is an attribute as a number.
func (r *tmlDraw) num(n *tml.Node, attr string, vars map[string]any, def float64) float64 {
	v, ok := r.value(n, attr, vars)
	if !ok {
		return def
	}
	switch x := v.(type) {
	case float64:
		return x
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return f
		}
	}
	return def
}

// truthy is a condition attribute, true when absent.
func (r *tmlDraw) truthy(n *tml.Node, attr string, vars map[string]any) bool {
	v, ok := r.value(n, attr, vars)
	if !ok {
		return true
	}
	return tml.Truthy(v)
}

// children draws the component children of n, each once per element of
// its each, as loom builds them.
func (r *tmlDraw) children(n *tml.Node, ptr string, vars map[string]any) {
	for i, ch := range n.Children {
		if ch.IsText() || ch.Type == "state" {
			continue
		}
		cp := ptr + "/children/" + strconv.Itoa(i)
		if _, ok := ch.Attr("each"); !ok {
			r.node(ch, cp, vars)
			continue
		}
		arr := r.data(ch, "each", vars)
		alias := ch.AttrString("as")
		if alias == "" {
			alias = "item"
		}
		for j, el := range arr {
			scope := make(map[string]any, len(vars)+2)
			for k, x := range vars {
				scope[k] = x
			}
			scope[alias], scope["index"] = el, float64(j)
			// The element's key holds its index, so each copy is its own.
			r.node(ch, cp+"#"+strconv.Itoa(j), scope)
		}
	}
}

// node draws one element, or nothing when its if is false.
func (r *tmlDraw) node(n *tml.Node, ptr string, vars map[string]any) {
	if !r.truthy(n, "if", vars) {
		return
	}
	c, pal := r.c, r.pal
	gap := float32(r.num(n, "gap", vars, 0)) * tmlCellW
	switch n.Type {
	case "col", "stack":
		// A column's children take its width unless it aligns them, as in
		// loom.
		align := ui.Stretch
		if a := n.AttrString("align"); a != "" {
			align = tmlAlign(a)
		}
		r.box(ui.Column(c.Key(ptr)).Gap(gap*tmlCellH/tmlCellW).AlignItems(align), n, vars).Children(func() {
			r.children(n, ptr, vars)
		})
	case "row":
		r.box(ui.Row(c.Key(ptr)).Gap(gap).AlignItems(tmlAlign(n.AttrString("align"))), n, vars).Children(func() {
			r.children(n, ptr, vars)
		})
	case "grid":
		cols := int(r.num(n, "cols", vars, 2))
		r.box(ui.Grid(c.Key(ptr)).Columns(max(cols, 1)).Gap(gap), n, vars).Children(func() {
			r.children(n, ptr, vars)
		})
	case "scroll":
		r.box(ui.Scroll(c.Key(ptr)).MinHeight(0), n, vars).Children(func() {
			ui.Column(c).Children(func() { r.children(n, ptr, vars) })
		})
	case "spacer":
		ui.Spacer(c)
	case "card":
		r.box(ui.Column(c.Key(ptr)).Gap(tmlCellH/2).Padding(10, 12).Radius(rControl).Background(ckCodeBg(pal)), n, vars).Children(func() {
			if t := r.str(n, "title", vars); t != "" {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, t).FontSize(fsBody).FontWeight(wSemibold).TextColor(r.role(n.AttrString("role"), pal.text)).Grow(1).MinWidth(0)
					if m := r.str(n, "meta", vars); m != "" {
						ui.Text(c, m).FontSize(fsSmall).TextColor(pal.textFaint)
					}
				})
			}
			r.children(n, ptr, vars)
		})
	case "text":
		ui.RichText(c, r.inline(n, vars, ui.Span{Color: r.role(n.AttrString("role"), pal.text)})...).FontSize(fsBody)
	case "heading":
		size := float32(fsBody + 4)
		if r.num(n, "level", vars, 1) > 1 {
			size = fsBody + 1
		}
		ui.RichText(c, r.inline(n, vars, ui.Span{Color: r.role(n.AttrString("role"), pal.text), Weight: wSemibold})...).FontSize(size)
	case "code":
		ui.Text(c, strings.TrimRight(r.raw(n, vars), "\n")).Font(r.mono).FontSize(fsCode).LineHeight(1.5).TextColor(pal.text).
			Padding(8, 10).Radius(rControl).Background(ckCodeBg(pal))
	case "markdown":
		blocks, _ := markdown.Parse(r.raw(n, vars))
		ui.Column(c.Key(ptr)).Children(func() { r.a.markdownView(c, pal, blocks, r.mono) })
	case "badge":
		col := r.role(n.AttrString("role"), pal.accent)
		ui.Row(c).Children(func() {
			ui.RichText(c, r.inline(n, vars, ui.Span{Color: col})...).FontSize(fsSmall).FontWeight(wMedium).
				Padding(2, 8).Radius(99).Background(col.Alpha(0.16))
		})
	case "status":
		st, unread := tmlStatus(r.str(n, "state", vars))
		label := r.str(n, "label", vars)
		if label == "" {
			label = strings.ReplaceAll(r.str(n, "state", vars), "_", " ")
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			statusIconP(c, pal, st, unread, -1, 12)
			ui.Text(c, label).FontSize(fsBody).TextColor(pal.text)
		})
	case "progress":
		frac := min(max(r.num(n, "value", vars, 0), 0), 1)
		col := r.role(n.AttrString("role"), pal.accent)
		ui.Column(c.Key(ptr)).Gap(4).AlignItems(ui.Stretch).Children(func() {
			if l := r.str(n, "label", vars); l != "" {
				ui.Text(c, l).FontSize(fsSmall).TextColor(pal.textMuted)
			}
			ui.Box(c).Height(6).Radius(3).Background(pal.control).Clip().Children(func() {
				ui.Box(c).Height(6).Radius(3).Background(col).WidthPercent(float32(frac * 100))
			})
		})
	case "spinner":
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c)
			if l := r.str(n, "label", vars); l != "" {
				ui.Text(c, l).FontSize(fsBody).TextColor(pal.textMuted)
			}
		})
	case "stat":
		col := r.role(n.AttrString("role"), pal.text)
		ui.Column(c.Key(ptr)).Gap(2).Children(func() {
			ui.Text(c, r.str(n, "value", vars)).FontSize(fsBody + 8).FontWeight(wSemibold).TextColor(col).FontFeatures("tnum")
			ui.Row(c).Gap(6).Children(func() {
				ui.Text(c, r.str(n, "label", vars)).FontSize(fsSmall).TextColor(pal.textMuted)
				if d := r.str(n, "delta", vars); d != "" {
					ui.Text(c, d).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum")
				}
			})
		})
	case "kv":
		// The keys' column is as wide as its longest key, as in loom, and a
		// long value wraps under its own column.
		keyW := float32(0)
		for _, it := range n.Children {
			if it.Type == "item" {
				keyW = max(keyW, float32(len([]rune(r.str(it, "k", vars)))))
			}
		}
		ui.Column(c.Key(ptr)).Gap(4).AlignItems(ui.Stretch).Children(func() {
			for i, it := range n.Children {
				if it.Type != "item" {
					continue
				}
				ui.Row(c.Key(i)).Gap(tmlCellW * 2).AlignItems(ui.Start).Children(func() {
					ui.Text(c, r.str(it, "k", vars)).FontSize(fsBody).TextColor(pal.textMuted).Width(keyW * fsBody * 0.62).Shrink(0)
					ui.Text(c, r.str(it, "v", vars)).FontSize(fsBody).TextColor(r.role(it.AttrString("role"), pal.text)).Grow(1).MinWidth(0)
				})
			}
		})
	case "form", "button", "checkbox", "toggle", "input", "textarea", "select", "slider":
		r.input(n, ptr, vars)
	case "list", "table", "tree", "tabs", "modal", "toast", "split":
		r.dataNode(n, ptr, vars)
	default:
		ui.Text(c.Key(ptr), locales.TmlNotDrawn(n.Type)).FontSize(fsSmall).TextColor(pal.textFaint)
	}
}

// box applies the common size attributes: w and h in cells, fill and grow.
func (r *tmlDraw) box(e ui.Element, n *tml.Node, vars map[string]any) ui.Element {
	if pad := n.AttrString("pad"); pad != "" {
		f := strings.Fields(pad)
		px := func(i int, cell float32) float32 {
			v, _ := strconv.ParseFloat(f[i], 64)
			return float32(v) * cell
		}
		switch len(f) {
		case 1:
			e = e.Padding(px(0, tmlCellH/2), px(0, tmlCellW))
		case 2:
			e = e.Padding(px(0, tmlCellH/2), px(1, tmlCellW))
		case 4:
			e = e.Padding(px(0, tmlCellH/2), px(1, tmlCellW), px(2, tmlCellH/2), px(3, tmlCellW))
		}
	}
	size := func(s string, cell float32, set func(float32) ui.Element) {
		switch {
		case s == "":
		case s == "fill" || strings.HasPrefix(s, "fill:"):
			e = e.Grow(1)
		case strings.HasSuffix(s, "%"):
			e = e.Grow(1)
		default:
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				e = set(float32(v) * cell)
			}
		}
	}
	size(r.str(n, "w", vars), tmlCellW, func(v float32) ui.Element { return e.Width(v).Shrink(0) })
	size(r.str(n, "h", vars), tmlCellH, func(v float32) ui.Element { return e.Height(v).Shrink(0) })
	if g := r.num(n, "grow", vars, 0); g > 0 {
		e = e.Grow(float32(g))
	}
	return e
}

// raw is the text content of an element whose content is raw (code,
// markdown): its text as written. Raw content holds no bindings, as in
// loom: a {state.x} in code is code.
func (r *tmlDraw) raw(n *tml.Node, _ map[string]any) string {
	var b strings.Builder
	for _, ch := range n.Children {
		if ch.IsText() {
			b.WriteString(ch.Text)
		}
	}
	return b.String()
}

// data is the array an each or a list attribute names: a literal array, or
// a path (state.rows, item.kids) without braces, as loom reads it.
func (r *tmlDraw) data(n *tml.Node, attr string, vars map[string]any) []any {
	v, ok := n.Attr(attr)
	if !ok {
		return nil
	}
	if v.Kind == tml.KindArray {
		d, err := v.Decode()
		if err != nil {
			return nil
		}
		arr, _ := tml.SanitizeValue(d).([]any)
		return arr
	}
	if v.Kind != tml.KindString {
		return nil
	}
	src := strings.TrimSpace(v.Str)
	e, ok := r.v.expr[src]
	if !ok {
		e, _ = tml.ParseExpr(strings.TrimSuffix(strings.TrimPrefix(src, "{"), "}"))
		if r.v.expr == nil {
			r.v.expr = map[string]*tml.Expr{}
		}
		r.v.expr[src] = e
	}
	if e == nil {
		return nil
	}
	out, err := e.Eval(&tml.Env{Vars: vars})
	if err != nil {
		return nil
	}
	arr, _ := out.([]any)
	return arr
}

// render is a text run with its bindings filled in.
func (r *tmlDraw) render(s string, vars map[string]any) string {
	if !strings.Contains(s, "{") {
		return s
	}
	if t := r.template(s); t != nil {
		return t.Render(&tml.Env{Vars: vars})
	}
	return s
}

// inline is an element's inline content as styled runs: b, i, u, s, c, kbd
// and a, nested, over the base style.
func (r *tmlDraw) inline(n *tml.Node, vars map[string]any, base ui.Span) []ui.Span {
	var out []ui.Span
	var walk func(n *tml.Node, st ui.Span)
	walk = func(n *tml.Node, st ui.Span) {
		for _, ch := range n.Children {
			if ch.IsText() {
				sp := st
				sp.Text = r.render(ch.Text, vars)
				if sp.Text != "" {
					out = append(out, sp)
				}
				continue
			}
			s := st
			switch ch.Type {
			case "b":
				s.Weight = wSemibold
			case "i":
				s.Italic = true
			case "u":
				s.Underline = true
			case "s":
				s.Strikethrough = true
			case "c":
				s.Color = r.role(ch.AttrString("role"), st.Color)
			case "kbd", "code":
				s.Font, s.Background, s.Radius = r.mono, r.pal.kbd, 3
			case "a":
				s.Color, s.Underline = r.pal.accentText, true
			}
			walk(ch, s)
		}
	}
	walk(n, base)
	if len(out) == 0 {
		out = append(out, ui.Span{Text: " ", Color: base.Color})
	}
	return out
}

// role is the colour of a TML role, as loom's Palette maps the roles to
// cells; def when the role is empty or one this part does not colour.
func (r *tmlDraw) role(name string, def ui.Color) ui.Color {
	pal := r.pal
	switch name {
	case "fg":
		return pal.text
	case "dim":
		return pal.textMuted
	case "mute":
		return pal.textFaint
	case "accent", "accent-bright", "info", "series1":
		return pal.accent
	case "success", "series2":
		return pal.done
	case "warning", "attention", "busy", "series3":
		return pal.attention
	case "error", "series4":
		return pal.danger
	case "edge":
		return pal.divider
	}
	return def
}

// tmlAlign is a TML align as a MyGo cross alignment.
func tmlAlign(s string) ui.Align {
	switch s {
	case "center":
		return ui.Center
	case "end":
		return ui.End
	case "stretch":
		return ui.Stretch
	}
	return ui.Start
}

// tmlStatus is a TML status state as the app's agent status, and whether
// it shows as a finished turn (the filled check).
func tmlStatus(s string) (proto.Status, bool) {
	switch s {
	case "working":
		return proto.Working, false
	case "needs_input":
		return proto.Waiting, false
	case "done":
		return proto.Idle, true
	case "errored":
		return proto.Failed, false
	}
	return proto.Idle, false
}
