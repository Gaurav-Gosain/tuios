package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/mygo"
	"github.com/Gaurav-Gosain/mygo/ui"
	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"tuios.dev/loom"
	"tuios.dev/tml"
)

// TML inputs in guios (part 2): button, checkbox, toggle, input, textarea,
// select, slider and form.
//
// An input runs its document's actions with loom.RunActions, the code a
// loom Surface runs, so a press does in guios what it does in the TUI: set,
// sort, tab and fold change the state or the view here, and emit, close,
// copy and pane come back as effects. What changed the state goes to the
// daemon in one tml-input, with the person's nonce, as the TUI sends it:
// the state ops, the emits for the owner, the pane() actions and a close.
// A field's value goes to its bind path, or stays in the view's text when
// it has none, and a form's on-submit gets form.NAME for each named field,
// as in loom.
//
// A pane() action that types into a pane or starts a program waits for the
// person's yes the first time a surface asks for it, as in the TUI: the
// question shows over the surface, with the command it runs.

// tmlAsk is a pane() action waiting for the person's yes.
type tmlAsk struct {
	verb   string
	params map[string]any
	key    string
}

// tmlActionsPerPress is the most pane actions one press runs, as the daemon
// holds a tml-input to.
const tmlActionsPerPress = 4

// actions are the parsed actions of an attribute, parsed once.
func (r *tmlDraw) actions(n *tml.Node, attr string) []tml.Action {
	src := n.AttrString(attr)
	if src == "" {
		return nil
	}
	if r.v.acts == nil {
		r.v.acts = map[string][]tml.Action{}
	}
	acts, ok := r.v.acts[src]
	if !ok {
		acts, _ = tml.ParseActions(src)
		r.v.acts[src] = acts
	}
	return acts
}

// run runs actions from the node at ptr, with event as event.value when it
// is not nil, and carries out the result.
func (r *tmlDraw) run(acts []tml.Action, ptr string, vars map[string]any, event map[string]any) {
	if len(acts) == 0 {
		return
	}
	env := make(map[string]any, len(vars)+1)
	for k, x := range vars {
		env[k] = x
	}
	if event != nil {
		env["event"] = event
	}
	res, err := loom.RunActions(r.v.doc, r.v.store, r.v.view, ptr, acts, &tml.Env{Vars: env})
	if err != nil {
		r.a.toast(err.Error())
	}
	r.a.tmlResult(r.v, res)
}

// fieldKey is the key of a field's value in the view's text: its id, else
// its pointer, as loom keys it.
func fieldKey(n *tml.Node, ptr string) string {
	if id := n.ID(); id != "" {
		return id
	}
	return ptr
}

// fieldValue is a field's value: its bind path's, else the view's text,
// else its value attribute.
func (r *tmlDraw) fieldValue(n *tml.Node, ptr string, vars map[string]any) any {
	if p := n.AttrString("bind"); p != "" {
		if ptr, err := tml.PathPointer(p); err == nil {
			if v := r.v.store.Get(ptr); v != nil {
				return v
			}
		}
	} else if t, ok := r.v.view.Text[fieldKey(n, ptr)]; ok {
		return t
	}
	v, _ := r.value(n, "value", vars)
	return v
}

// setField gives a field a value and runs its on-change, as loom's
// setField does.
func (r *tmlDraw) setField(n *tml.Node, ptr string, vars map[string]any, value any) {
	res := loom.Result{View: true}
	if p := n.AttrString("bind"); p != "" {
		ch, err := r.v.store.Set(p, value)
		if err != nil {
			r.a.toast(err.Error())
			return
		}
		res.Changed = ch
	} else {
		r.v.view.Text[fieldKey(n, ptr)] = tml.Stringify(value)
	}
	if acts := r.actions(n, "on-change"); len(acts) > 0 {
		env := make(map[string]any, len(vars)+1)
		for k, x := range vars {
			env[k] = x
		}
		env["event"] = map[string]any{"value": value}
		r2, err := loom.RunActions(r.v.doc, r.v.store, r.v.view, ptr, acts, &tml.Env{Vars: env})
		if err != nil {
			r.a.toast(err.Error())
		}
		res.Changed = append(res.Changed, r2.Changed...)
		res.Effects = append(res.Effects, r2.Effects...)
	}
	r.a.tmlResult(r.v, res)
}

// submit runs the on-submit of the form being drawn, with form.NAME for
// each named field. A form with a required field that is empty does not
// submit.
func (r *tmlDraw) submit() {
	f := r.form
	if f == nil {
		return
	}
	values := map[string]any{}
	missing := false
	var walk func(n *tml.Node, ptr string)
	walk = func(n *tml.Node, ptr string) {
		for i, ch := range n.Children {
			if ch.IsText() {
				continue
			}
			cp := ptr + "/children/" + strconv.Itoa(i)
			if name := ch.AttrString("name"); name != "" {
				v := r.fieldValue(ch, cp, f.vars)
				if s, ok := v.(string); ok && (ch.Type == "toggle" || ch.Type == "checkbox") {
					v = s == "true"
				}
				values[name] = v
				if r.truthy(ch, "required", f.vars) && ch.AttrString("required") != "" && (v == nil || tml.Stringify(v) == "") {
					missing = true
				}
			}
			walk(ch, cp)
		}
	}
	walk(f.n, f.ptr)
	if missing {
		r.a.toast(locales.TmlRequired())
		return
	}
	env := make(map[string]any, len(f.vars)+2)
	for k, x := range f.vars {
		env[k] = x
	}
	env["form"] = values
	r.run(r.actions(f.n, "on-submit"), f.ptr, env, map[string]any{"value": values})
}

// tmlForm is the form whose fields are being drawn.
type tmlForm struct {
	n    *tml.Node
	ptr  string
	vars map[string]any
}

// input draws an input element.
func (r *tmlDraw) input(n *tml.Node, ptr string, vars map[string]any) {
	c, pal := r.c, r.pal
	disabled := false
	if v, ok := r.value(n, "disabled", vars); ok {
		disabled = tml.Truthy(v)
	}
	label := r.str(n, "label", vars)
	switch n.Type {
	case "form":
		was := r.form
		r.form = &tmlForm{n: n, ptr: ptr, vars: vars}
		ui.Column(c.Key(ptr)).Gap(tmlCellH / 2).AlignItems(ui.Stretch).Children(func() {
			r.children(n, ptr, vars)
		})
		r.form = was
	case "button":
		text := strings.TrimSpace(spanText(r.inline(n, vars, ui.Span{})))
		primary := n.AttrString("kind") == "primary" || n.AttrString("submit") == "true"
		ui.Row(c).Children(func() {
			if sizedButton(c, ptr, text, n.AttrString("key"), primary, 26, 7, pal) && !disabled {
				if n.AttrString("submit") == "true" {
					r.submit()
				} else {
					r.run(r.actions(n, "on-press"), ptr, vars, nil)
				}
			}
		})
	case "checkbox", "toggle":
		on := tml.Truthy(r.fieldValue(n, ptr, vars))
		if s, ok := r.fieldValue(n, ptr, vars).(string); ok {
			on = s == "true"
		}
		was := on
		var e ui.Element
		if n.Type == "toggle" {
			e = ui.Toggle(c.Key(ptr), &on, label)
		} else {
			e = ui.Checkbox(c.Key(ptr), &on, label)
		}
		e.Disabled(disabled)
		if on != was && !disabled {
			r.setField(n, ptr, vars, on)
		}
	case "input", "textarea":
		key := fieldKey(n, ptr)
		cur := tml.Stringify(r.fieldValue(n, ptr, vars))
		if r.v.text == nil {
			r.v.text = map[string]*string{}
		}
		p := r.v.text[key]
		if p == nil {
			s := cur
			p = &s
			r.v.text[key] = p
		}
		ui.Column(c.Key(ptr)).Gap(4).AlignItems(ui.Stretch).Children(func() {
			if label != "" {
				ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textMuted)
			}
			var in ui.Element
			if n.Type == "textarea" {
				in = ui.TextArea(c.Key("ta"), p).MinHeight(float32(r.num(n, "rows", vars, 3)) * tmlCellH).FontSize(fsBody).Placeholder(r.str(n, "placeholder", vars))
			} else {
				in = textField(c, "in", p, r.str(n, "placeholder", vars), 0, pal)
			}
			in.Disabled(disabled)
			switch {
			case !in.Focused() && *p != cur:
				// Not being typed in: the state is the truth.
				*p = cur
			case *p != cur && !disabled:
				r.setField(n, ptr, vars, *p)
			}
			if in.Submitted() && r.form != nil {
				r.submit()
			}
		})
	case "select":
		ids, labels := tmlOptions(r.data(n, "options", vars))
		cur := tml.Stringify(r.fieldValue(n, ptr, vars))
		sel := cur
		ui.Column(c.Key(ptr)).Gap(4).Children(func() {
			if label != "" {
				ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textMuted)
			}
			if len(ids) > 0 {
				segmentedIn(c, "seg", &sel, ids, labels, false)
			}
		})
		if sel != cur && !disabled {
			r.setField(n, ptr, vars, sel)
		}
	case "slider":
		lo, hi := r.num(n, "min", vars, 0), r.num(n, "max", vars, 100)
		cur := r.num(n, "value", vars, lo)
		if f, ok := r.fieldValue(n, ptr, vars).(float64); ok {
			cur = f
		}
		val := cur
		ui.Column(c.Key(ptr)).Gap(4).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Children(func() {
				ui.Text(c, label).FontSize(fsSmall).TextColor(pal.textMuted).Grow(1)
				ui.Text(c, strconv.FormatFloat(val, 'f', -1, 64)).FontSize(fsSmall).TextColor(pal.textFaint).FontFeatures("tnum")
			})
			slider(c, "sl", &val, lo, hi, 0, pal)
		})
		if step := r.num(n, "step", vars, 0); step > 0 {
			val = lo + float64(int((val-lo)/step+0.5))*step
		}
		if val != cur && !disabled {
			r.setField(n, ptr, vars, val)
		}
	}
}

// tmlOptions are a select's choices: strings, or objects with value and
// label.
func tmlOptions(opts []any) (ids, labels []string) {
	for _, o := range opts {
		switch x := o.(type) {
		case map[string]any:
			id := tml.Stringify(x["value"])
			l := tml.Stringify(x["label"])
			if l == "" {
				l = id
			}
			ids, labels = append(ids, id), append(labels, l)
		default:
			s := tml.Stringify(x)
			ids, labels = append(ids, s), append(labels, s)
		}
	}
	return ids, labels
}

// spanText is the text of styled runs.
func spanText(spans []ui.Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

// tmlResult carries out what an input did: the state and the emits for the
// daemon, the pane() actions (a first one that types or starts a program
// waits for the person), a copy, and a close.
func (a *App) tmlResult(v *tmlView, res loom.Result) {
	ops := tmlStateOps(v.store.State(), res.Changed)
	var events []map[string]any
	var actions []map[string]any
	closing, hasValue := false, false
	var value any
	for _, e := range res.Effects {
		switch e.Action {
		case "emit":
			events = append(events, map[string]any{"name": e.Name, "value": e.Value})
		case "close":
			if !closing {
				closing, value, hasValue = true, e.Value, e.HasValue
			}
		case "copy":
			mygo.Clipboard.WriteText(tml.Stringify(e.Value))
		case "pane":
			params, _ := e.Value.(map[string]any)
			if e.Name == "send-text" || e.Name == "new-window" {
				key := tmlAskKey(e.Name, params)
				if !v.confirmed[key] {
					v.ask = &tmlAsk{verb: e.Name, params: params, key: key}
					continue
				}
			}
			actions = append(actions, map[string]any{"verb": e.Name, "params": params})
		}
	}
	if len(actions) > tmlActionsPerPress {
		a.toast(locales.TmlTooManyActions(strconv.Itoa(tmlActionsPerPress)))
		actions = nil
	}
	if len(ops) == 0 && len(events) == 0 && len(actions) == 0 && !closing {
		return
	}
	p := map[string]any{"surface": v.surface}
	if len(ops) > 0 {
		p["ops"] = ops
	}
	if len(events) > 0 {
		p["events"] = events
	}
	if len(actions) > 0 {
		p["actions"] = actions
	}
	if closing {
		p["close"] = true
		if hasValue {
			raw, err := jsonx.Marshal(value)
			if err != nil {
				raw = []byte("null")
			}
			p["value"] = json.RawMessage(raw)
		}
	}
	a.tmlSend(p)
}

// tmlSend sends a tml-input with the person's nonce, off the main thread.
func (a *App) tmlSend(p map[string]any) {
	if a.tmlSent != nil {
		a.tmlSent(p)
		return
	}
	cl := a.cockpitClient()
	if cl == nil {
		return
	}
	go func() {
		nonce, err := cl.HumanNonce()
		if err == nil {
			p["human_nonce"] = nonce
			_, err = cl.Verb("tml-input", p, true)
		}
		if err != nil {
			a.update(func() { a.toast(err.Error()) })
		}
	}()
}

// tmlAskKey names a pane() action for the person's yes: its verb and what
// it types or runs.
func tmlAskKey(verb string, params map[string]any) string {
	b, _ := jsonx.Marshal(params)
	return verb + " " + string(b)
}

// tmlAskBar asks the person whether a surface may run a pane() action that
// types into a pane or starts a program, the first time it asks.
func (a *App) tmlAskBar(c *ui.Context, pal *palette, v *tmlView) {
	ask := v.ask
	what := tmlAskText(ask)
	ui.Column(c.Key("ask")).Gap(8).Padding(10, 12).Radius(rControl).Background(pal.attnSoft).Children(func() {
		ui.Text(c, what).FontSize(fsBody).TextColor(pal.text)
		ui.Row(c).Gap(8).Children(func() {
			if sizedButton(c, "allow", locales.TmlAllow(), "", true, 26, 7, pal) {
				if v.confirmed == nil {
					v.confirmed = map[string]bool{}
				}
				v.confirmed[ask.key] = true
				v.ask = nil
				a.tmlSend(map[string]any{"surface": v.surface, "actions": []map[string]any{{"verb": ask.verb, "params": ask.params}}})
			}
			if sizedButton(c, "deny", locales.TmlDoNotRun(), "", false, 26, 7, pal) {
				v.ask = nil
			}
		})
	})
}

// tmlAskText is the question for a pane() action, with the whole text it
// types or the whole command it starts.
func tmlAskText(ask *tmlAsk) string {
	switch ask.verb {
	case "send-text":
		return locales.TmlAskType(strconv.Quote(tml.Stringify(ask.params["text"])))
	case "new-window":
		var words []string
		if cmd, ok := ask.params["command"].([]any); ok {
			for _, w := range cmd {
				s := tml.Stringify(w)
				if strings.ContainsAny(s, " \"'") {
					s = strconv.Quote(s)
				}
				words = append(words, s)
			}
		}
		if len(words) == 0 {
			return locales.TmlAskOpenShell()
		}
		return locales.TmlAskOpenCommand(strings.Join(words, " "))
	}
	return locales.TmlAskRun(ask.verb)
}

// tmlStateOps is the JSON Patch that brings the daemon's copy of the state
// to state at the paths changed: one add per changed path (a child of a
// path already sent is left out), or a remove when the path is gone.
func tmlStateOps(state map[string]any, changed []string) []tml.Op {
	var ops []tml.Op
	var done []string
	for _, p := range changed {
		if p == "" || slices.ContainsFunc(done, func(d string) bool { return p == d || strings.HasPrefix(p, d+"/") }) {
			continue
		}
		done = append(done, p)
		val, ok := tmlLookup(state, p)
		if !ok {
			ops = append(ops, tml.Op{Op: "remove", Path: p})
			continue
		}
		raw, err := jsonx.Marshal(val)
		if err != nil {
			continue
		}
		// A path in an array replaces its element: add would insert one.
		op := "add"
		if i := strings.LastIndex(p, "/"); i > 0 {
			if parent, ok := tmlLookup(state, p[:i]); ok {
				if _, inArray := parent.([]any); inArray {
					op = "replace"
				}
			}
		}
		ops = append(ops, tml.Op{Op: op, Path: p, Value: raw})
	}
	return ops
}

// tmlLookup reads a JSON Pointer in state.
func tmlLookup(state map[string]any, ptr string) (any, bool) {
	var cur any = state
	for _, seg := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		switch x := cur.(type) {
		case map[string]any:
			v, ok := x[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(x) {
				return nil, false
			}
			cur = x[i]
		default:
			return nil, false
		}
	}
	return cur, true
}
