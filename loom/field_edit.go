package loom

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"tuios.dev/tml"
)

const maxFieldRunes = 4096

func (w *fieldWidget) editable() bool {
	return (w.kind == "input" || w.kind == "textarea") && !w.disabled
}

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

func (s *Surface) fieldKey(b *box, w *fieldWidget, key string) (Result, bool, error) {
	text := []rune(w.value)
	caret := clampInt(w.caret, 0, len(text))
	if t := typedText(key); t != "" {
		res, err := s.fieldInsert(b, w, t)
		return res, true, err
	}
	switch key {
	case "esc":
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
	s.full = true
	if acts := s.actionsOf(b.src, "on-change"); len(acts) > 0 {
		r2, err := s.run(b, acts, map[string]any{"value": text})
		res.Changed = append(res.Changed, r2.Changed...)
		res.Effects = append(res.Effects, r2.Effects...)
		return res, err
	}
	return res, nil
}

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

func isSubmit(b *box) bool {
	return b.src != nil && b.src.AttrString("submit") == "true"
}

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
