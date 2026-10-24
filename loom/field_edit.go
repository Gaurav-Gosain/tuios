package loom

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"tuios.dev/tml"
)

// Text fields: input and textarea take typed text.
//
// A field with bind writes the state path it names, so the host sends the
// change on as any state change. A field without bind keeps its text in the
// view, keyed like the rest of the view state, and form.NAME reads it. The
// caret is view state too.
//
// While a text field has the focus it takes every key that types or edits:
// a printable key, space, backspace, delete, the arrows along the line, home
// and end, and the readline keys ctrl+a, ctrl+e, ctrl+b, ctrl+f, ctrl+u,
// ctrl+k, ctrl+w and alt+backspace. A button's key does not fire while a
// field has the focus, so a letter goes into the field. tab, shift+tab and
// esc keep their meaning. enter submits the field's form in an input, and
// starts a new line in a textarea. esc leaves the field for the first
// control that is not a text field.

// maxFieldRunes bounds the text of one field.
const maxFieldRunes = 4096

// editable reports whether a field takes typed text.
func (w *fieldWidget) editable() bool {
	return (w.kind == "input" || w.kind == "textarea") && !w.disabled
}

// textField is the focused box when it is an editable text field.
func (s *Surface) textField() (*box, *fieldWidget) {
	b := s.focused()
	if b == nil {
		return nil, nil
	}
	if w, ok := b.w.(*fieldWidget); ok && w.editable() {
		return b, w
	}
	return nil, nil
}

// typedText is the text a key types, or "" for a key that types nothing.
func typedText(key string) string {
	if key == "space" {
		return " "
	}
	if utf8.RuneCountInString(key) != 1 {
		return ""
	}
	r, _ := utf8.DecodeRuneInString(key)
	if !unicode.IsPrint(r) {
		return ""
	}
	return key
}

// fieldKey handles a key on a focused text field. ok reports that the field
// took it.
func (s *Surface) fieldKey(b *box, w *fieldWidget, key string) (Result, bool, error) {
	text := []rune(w.value)
	caret := clampInt(w.caret, 0, len(text))
	if t := typedText(key); t != "" {
		res, err := s.fieldInsert(b, w, t)
		return res, true, err
	}
	switch key {
	case "esc":
		// esc leaves the field for the first control that is not one, so
		// a second esc reaches the host, which may close a popup. The text
		// stays.
		for _, o := range s.focus {
			if ow, ok := o.w.(*fieldWidget); (!ok || !ow.editable()) && visible(o) {
				s.setFocus(o)
				return Result{View: true}, true, nil
			}
		}
		return Result{}, false, nil
	case "enter":
		if w.kind == "textarea" {
			res, err := s.fieldInsert(b, w, "\n")
			return res, true, err
		}
		res, err := s.submit(b)
		return res, true, err
	case "ctrl+j":
		if w.kind == "textarea" {
			res, err := s.fieldInsert(b, w, "\n")
			return res, true, err
		}
		return Result{}, true, nil
	case "backspace", "ctrl+h":
		if caret == 0 {
			return Result{}, true, nil
		}
		res, err := s.setField(b, w, string(text[:caret-1])+string(text[caret:]), caret-1)
		return res, true, err
	case "delete", "ctrl+d":
		if caret >= len(text) {
			return Result{}, true, nil
		}
		res, err := s.setField(b, w, string(text[:caret])+string(text[caret+1:]), caret)
		return res, true, err
	case "ctrl+u":
		start := lineStart(text, caret)
		res, err := s.setField(b, w, string(text[:start])+string(text[caret:]), start)
		return res, true, err
	case "ctrl+k":
		end := lineEnd(text, caret)
		res, err := s.setField(b, w, string(text[:caret])+string(text[end:]), caret)
		return res, true, err
	case "ctrl+w", "alt+backspace":
		start := wordStart(text, caret)
		res, err := s.setField(b, w, string(text[:start])+string(text[caret:]), start)
		return res, true, err
	case "left", "ctrl+b":
		return s.moveCaret(b, w, caret-1), true, nil
	case "right", "ctrl+f":
		return s.moveCaret(b, w, caret+1), true, nil
	case "alt+left", "alt+b", "ctrl+left":
		return s.moveCaret(b, w, wordStart(text, caret)), true, nil
	case "alt+right", "alt+f", "ctrl+right":
		return s.moveCaret(b, w, wordEnd(text, caret)), true, nil
	case "home", "ctrl+a":
		return s.moveCaret(b, w, lineStart(text, caret)), true, nil
	case "end", "ctrl+e":
		return s.moveCaret(b, w, lineEnd(text, caret)), true, nil
	}
	return Result{}, false, nil
}

// Paste puts text into the focused text field at its caret. An input takes
// it on one line. With no text field focused it does nothing.
func (s *Surface) Paste(text string) (Result, error) {
	if s.root == nil {
		return Result{}, nil
	}
	s.flush()
	b, w := s.textField()
	if b == nil {
		return Result{}, nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if w.kind == "input" {
		// One line: the line break a copied line ends with goes, and the
		// others become spaces.
		text = strings.ReplaceAll(strings.TrimRight(text, "\n"), "\n", " ")
	}
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) {
			return r
		}
		return -1
	}, text)
	return s.fieldInsert(b, w, text)
}

// fieldInsert puts t at the caret.
func (s *Surface) fieldInsert(b *box, w *fieldWidget, t string) (Result, error) {
	text := []rune(w.value)
	caret := clampInt(w.caret, 0, len(text))
	add := []rune(t)
	if room := maxFieldRunes - len(text); len(add) > room {
		add = add[:max(room, 0)]
	}
	if len(add) == 0 {
		return Result{}, nil
	}
	next := string(text[:caret]) + string(add) + string(text[caret:])
	return s.setField(b, w, next, caret+len(add))
}

// moveCaret puts the caret at i.
func (s *Surface) moveCaret(b *box, w *fieldWidget, i int) Result {
	i = clampInt(i, 0, utf8.RuneCountInString(w.value))
	if i == w.caret {
		return Result{}
	}
	w.caret = i
	s.view.Caret[w.key] = i
	s.markView(b.key)
	return Result{View: true}
}

// setField writes a field's new text: into the state path it binds, or into
// the view. on-change runs with event.value set to the text.
func (s *Surface) setField(b *box, w *fieldWidget, text string, caret int) (Result, error) {
	res := Result{View: true}
	w.value, w.caret = text, caret
	s.view.Caret[w.key] = caret
	if w.bind != "" {
		ch, err := s.store.Set(w.bind, text)
		if err != nil {
			return res, err
		}
		s.pending = append(s.pending, ch...)
		res.Changed = ch
	} else {
		s.view.Text[w.key] = text
	}
	// A form reads its fields when it is built, so form.NAME follows.
	s.full = true
	if acts := s.actionsOf(b.src, "on-change"); len(acts) > 0 {
		r2, err := s.run(b, acts, map[string]any{"value": text})
		res.Changed = append(res.Changed, r2.Changed...)
		res.Effects = append(res.Effects, r2.Effects...)
		return res, err
	}
	return res, nil
}

// submit runs the on-submit of the form around b, with form set to the
// values of its fields as they are now. A form with a required field that
// is empty does not submit.
func (s *Surface) submit(b *box) (Result, error) {
	form := b
	for form != nil && form.kind != "form" {
		form = form.parent
	}
	if form == nil {
		return Result{}, nil
	}
	vars := s.vars(form.scope)
	values := s.formValues(form.src, form.ptr, vars)
	missing := false
	form.src.Walk(func(n *tml.Node, _ string) bool {
		if name := n.AttrString("name"); name != "" && s.truthy(n, "required", vars) {
			if v := values[name]; v == nil || tml.Stringify(v) == "" {
				missing = true
			}
		}
		return !missing
	})
	if missing {
		return Result{}, nil
	}
	acts := s.actionsOf(form.src, "on-submit")
	if len(acts) == 0 {
		return Result{}, nil
	}
	env := map[string]any{}
	form.scope.vars(env)
	env["form"] = values
	env["view"] = map[string]any{"width": float64(s.w), "height": float64(s.h), "band": band(s.w)}
	env["event"] = map[string]any{"value": values}
	res, err := runActions(s.doc, s.store, s.view, form.ptr, acts, &tml.Env{Vars: env, Now: s.opts.Now})
	s.pending = append(s.pending, res.Changed...)
	if res.View {
		s.full = true
	}
	return res, err
}

// isSubmit reports whether a button submits its form.
func isSubmit(b *box) bool {
	return b.src != nil && b.src.AttrString("submit") == "true"
}

// press runs a button: its on-press, then the submit of its form when it
// is a submit button.
func (s *Surface) press(b *box, acts []tml.Action) (Result, error) {
	res, err := s.run(b, acts, nil)
	if err != nil || !isSubmit(b) {
		return res, err
	}
	r2, err := s.submit(b)
	res.Changed = append(res.Changed, r2.Changed...)
	res.Effects = append(res.Effects, r2.Effects...)
	res.View = res.View || r2.View
	return res, err
}

func lineStart(t []rune, i int) int {
	for i > 0 && t[i-1] != '\n' {
		i--
	}
	return i
}

func lineEnd(t []rune, i int) int {
	for i < len(t) && t[i] != '\n' {
		i++
	}
	return i
}

func wordStart(t []rune, i int) int {
	for i > 0 && unicode.IsSpace(t[i-1]) {
		i--
	}
	for i > 0 && !unicode.IsSpace(t[i-1]) {
		i--
	}
	return i
}

func wordEnd(t []rune, i int) int {
	for i < len(t) && unicode.IsSpace(t[i]) {
		i++
	}
	for i < len(t) && !unicode.IsSpace(t[i]) {
		i++
	}
	return i
}

// caretLine draws a field's text on one row of w cells with the caret: the
// part of the text around the caret that fits, and the caret's cell in
// reverse video.
func caretLine(text []rune, caret, w int, ink style) line {
	if w <= 0 {
		return nil
	}
	start := 0
	cells := 0
	for i := caret - 1; i >= 0; i-- {
		cells += width(string(text[i]))
		if cells > w-1 {
			start = i + 1
			break
		}
	}
	var out line
	var b strings.Builder
	for i := start; i < caret; i++ {
		b.WriteRune(text[i])
	}
	if b.Len() > 0 {
		out = append(out, span{text: b.String(), st: ink})
	}
	cur := " "
	if caret < len(text) && text[caret] != '\n' {
		cur = string(text[caret])
	}
	out = append(out, span{text: cur, st: style{fg: ink.fg, attrs: ink.attrs | attrReverse}})
	b.Reset()
	for i := caret + 1; i < len(text) && text[i] != '\n'; i++ {
		b.WriteRune(text[i])
	}
	if b.Len() > 0 {
		out = append(out, span{text: b.String(), st: ink})
	}
	return out
}
