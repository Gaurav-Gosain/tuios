package loom

import (
	"image"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"tuios.dev/tml"
)

// MouseButton is the button of a mouse event.
type MouseButton uint8

// The mouse buttons.
const (
	MouseNone MouseButton = iota
	MouseLeft
	MouseMiddle
	MouseRight
	WheelUp
	WheelDown
)

// MouseAction is what the button did.
type MouseAction uint8

// The mouse actions.
const (
	Press MouseAction = iota
	Release
	Motion
)

// Mouse is a mouse event in surface cells.
type Mouse struct {
	X, Y   int
	Button MouseButton
	Action MouseAction
}

// focusable reports whether b takes keys.
func focusable(s *Surface, b *box) bool {
	if b.hidden {
		return false
	}
	switch w := b.w.(type) {
	case *tableWidget:
		return w.selectable || len(w.onSelect) > 0
	case *listWidget:
		return w.selectable
	case *tabsWidget:
		return len(w.ids) > 1
	case *cardWidget:
		return !w.modal
	case *buttonWidget:
		return !w.disabled
	case *scrollWidget:
		return w.over
	case *fieldWidget:
		return w.editable()
	}
	return len(s.actionsOf(b.src, "on-press")) > 0 && b.kind != "tml"
}

// Focus returns the key of the focused node: its id, or its JSON Pointer.
func (s *Surface) Focus() string {
	if s.view.Focus == "" && len(s.focus) > 0 {
		return s.defaultFocus().key
	}
	return s.view.Focus
}

// defaultFocus is the first table or list, else the first focusable box
// that is not a text field. A text field takes the focus only from tab or a
// click: with it focused a letter types, so a document whose buttons have
// keys keeps them until the person goes into the field.
func (s *Surface) defaultFocus() *box {
	for _, b := range s.focus {
		switch b.w.(type) {
		case *tableWidget, *listWidget:
			return b
		}
	}
	for _, b := range s.focus {
		if w, ok := b.w.(*fieldWidget); !ok || !w.editable() {
			return b
		}
	}
	return s.focus[0]
}

func (s *Surface) focused() *box {
	if len(s.focus) == 0 {
		return nil
	}
	if s.view.Focus != "" {
		for _, b := range s.focus {
			if b.key == s.view.Focus {
				return b
			}
		}
		// The focused node is gone: no node has the focus until the person
		// gives it again, so enter cannot fire some other node.
		return nil
	}
	return s.defaultFocus()
}

func (s *Surface) setFocus(b *box) {
	if old := s.focused(); old != nil && old != b {
		s.markView(old.key)
	}
	s.view.Focus = b.key
	s.markView(b.key)
}

// Key handles a key press, named as tuios names keys: up, down, enter,
// tab, shift+tab, esc, r, ctrl+b. A button whose key matches takes it
// first, then the focused node, then the root's on-key. Local actions run
// at once; actions for the owner and the host come back as Effects.
func (s *Surface) Key(key string) (Result, error) {
	if s.root == nil {
		return Result{}, nil
	}
	s.flush()
	// A text field with the focus takes the keys that type and edit before
	// a button's key, so a letter goes into the field.
	if b, w := s.textField(); b != nil {
		if res, ok, err := s.fieldKey(b, w, key); ok || err != nil {
			return res, err
		}
	}
	for _, b := range s.focus {
		if bw, ok := b.w.(*buttonWidget); ok && bw.key != "" && strings.EqualFold(bw.key, key) && !bw.disabled && visible(b) {
			return s.press(b, bw.press)
		}
	}
	switch key {
	case "tab", "shift+tab":
		if len(s.focus) == 0 {
			return Result{}, nil
		}
		cur := s.focused()
		i := 0
		for k, b := range s.focus {
			if b == cur {
				i = k
			}
		}
		step := 1
		if key == "shift+tab" {
			step = len(s.focus) - 1
		}
		next := s.focus[(i+step)%len(s.focus)]
		s.setFocus(next)
		s.focusMoved = true
		return Result{View: true}, nil
	}
	if b := s.focused(); b != nil {
		if res, ok, err := s.keyOn(b, key); ok || err != nil {
			return res, err
		}
	}
	if acts := s.actionsOf(s.doc, "on-key"); len(acts) > 0 {
		return s.run(s.root, acts, map[string]any{"key": key})
	}
	return Result{}, nil
}

// keyOn handles a key on one focused box. ok reports that it took the key.
func (s *Surface) keyOn(b *box, key string) (Result, bool, error) {
	press := key == "enter" || key == "space" || key == " "
	switch w := b.w.(type) {
	case *tableWidget:
		page := max(b.inner().Dy()-2, 1)
		if move, ok := cursorMove(key, w.cursor, len(w.rows), page); ok {
			w.cursor = move
			s.view.Cursor[b.key] = w.rowKey(move)
			s.markView(b.key)
			return Result{View: true}, true, nil
		}
		if press && w.cursor < len(w.rows) {
			res, err := s.run(b, w.onSelect, map[string]any{"row": w.rows[w.cursor], "value": w.rows[w.cursor]})
			return res, true, err
		}
		if key == "s" {
			s.cycleSort(b, w)
			return Result{View: true}, true, nil
		}
	case *listWidget:
		if move, ok := cursorMove(key, w.cursor, len(w.items), 5); ok {
			w.cursor = move
			s.view.Cursor[b.key] = strconv.Itoa(move)
			s.markView(b.key)
			return Result{View: true}, true, nil
		}
		if press && w.cursor < len(w.items) {
			acts := s.actionsOf(b.src, "on-select")
			res, err := s.run(b, acts, map[string]any{"item": w.items[w.cursor], "value": w.items[w.cursor]})
			return res, true, err
		}
	case *tabsWidget:
		switch key {
		case "left", "h", "right", "l":
			next := w.active + 1
			if key == "left" || key == "h" {
				next = w.active - 1 + len(w.ids)
			}
			res, err := s.openTab(b, w, next%len(w.ids))
			return res, true, err
		}
	case *cardWidget:
		if press {
			s.toggleFold(b, w)
			return Result{View: true}, true, nil
		}
	case *scrollWidget:
		page := max(b.inner().Dy()-1, 1)
		off := w.offset(s, b)
		if move, ok := cursorMove(key, off, w.content-b.inner().Dy()+1, page); ok {
			s.view.Scroll[b.key] = move
			s.markView(b.key)
			return Result{View: true}, true, nil
		}
	case *buttonWidget:
		if press {
			res, err := s.press(b, w.press)
			return res, true, err
		}
	}
	if press {
		if acts := s.actionsOf(b.src, "on-press"); len(acts) > 0 {
			res, err := s.run(b, acts, nil)
			return res, true, err
		}
	}
	return Result{}, false, nil
}

// cursorMove returns where a cursor goes on a key, among n positions.
func cursorMove(key string, cur, n, page int) (int, bool) {
	if n <= 0 {
		return 0, false
	}
	next := cur
	switch key {
	case "up", "k":
		next--
	case "down", "j":
		next++
	case "pgup":
		next -= page
	case "pgdown":
		next += page
	case "home", "g":
		next = 0
	case "end", "G":
		next = n - 1
	default:
		return cur, false
	}
	return clampInt(next, 0, n-1), true
}

// cycleSort sorts the table by the next sortable column.
func (s *Surface) cycleSort(b *box, w *tableWidget) {
	start := -1
	for i, c := range w.cols {
		if c.field == w.sort.Field {
			start = i
		}
	}
	for k := 1; k <= len(w.cols); k++ {
		i := (start + k) % len(w.cols)
		if w.cols[i].sortable {
			s.sortBy(b, w, w.cols[i].field)
			return
		}
	}
}

// sortBy sorts by field, or flips the order when the table is sorted by it.
func (s *Surface) sortBy(b *box, w *tableWidget, field string) {
	spec := w.sort
	if spec.Field == field {
		spec.Desc = !spec.Desc
	} else {
		spec = SortSpec{Field: field}
	}
	s.view.Sort[b.key] = spec
	if w.cursor < len(w.rows) {
		s.view.Cursor[b.key] = w.rowKey(w.cursor)
	}
	s.markView(b.key)
}

func (s *Surface) toggleFold(b *box, w *cardWidget) {
	s.view.Fold[b.key] = !w.folded
	s.markView(b.key)
}

// openTab opens tab i: in the state when the tabs name a value path, else in
// the view. on-change runs with event.value set to the tab's id.
func (s *Surface) openTab(b *box, w *tabsWidget, i int) (Result, error) {
	if i < 0 || i >= len(w.ids) || i == w.active {
		return Result{}, nil
	}
	id := w.ids[i]
	res := Result{View: true}
	if w.statePath != "" {
		ch, err := s.store.Set(w.statePath, id)
		if err != nil {
			return res, err
		}
		s.pending = append(s.pending, ch...)
		res.Changed = ch
	} else {
		s.view.Tabs[b.key] = id
	}
	s.markView(b.key)
	if len(w.change) > 0 {
		r2, err := s.run(b, w.change, map[string]any{"value": id})
		res.Changed = append(res.Changed, r2.Changed...)
		res.Effects = append(res.Effects, r2.Effects...)
		return res, err
	}
	return res, nil
}

// run runs actions from box b with an event value. State changes mark
// their readers; a view change from an action repaints the surface.
func (s *Surface) run(b *box, acts []tml.Action, event map[string]any) (Result, error) {
	if len(acts) == 0 {
		return Result{}, nil
	}
	vars := map[string]any{}
	b.scope.vars(vars)
	vars["view"] = map[string]any{"width": float64(s.w), "height": float64(s.h), "band": band(s.w)}
	if event != nil {
		vars["event"] = event
	}
	res, err := runActions(s.doc, s.store, s.view, b.ptr, acts, &tml.Env{Vars: vars, Now: s.opts.Now})
	s.pending = append(s.pending, res.Changed...)
	if res.View {
		// tab, sort, filter, fold and focus name their targets by id; the
		// view keys are the same, so mark every box that has one changed.
		s.full = true
	}
	return res, err
}

// Mouse handles a mouse event: a click on a tab, a table header or row, a
// list item, a card title or a button, the wheel over anything that
// scrolls, and a drag of a split's divider.
func (s *Surface) Mouse(m Mouse) (Result, error) {
	if s.root == nil {
		return Result{}, nil
	}
	s.flush()
	pt := image.Point{m.X, m.Y}
	if s.drag != nil && m.Action == Press {
		// The release was lost: a new press ends the drag.
		s.drag = nil
	}
	if s.drag != nil {
		return s.dragSplit(m)
	}
	path := s.hit(s.root, pt, nil)
	if m.Button == WheelUp || m.Button == WheelDown {
		key := "down"
		if m.Button == WheelUp {
			key = "up"
		}
		for _, p := range slices.Backward(path) {
			switch p.w.(type) {
			case *tableWidget, *scrollWidget:
				for range 3 {
					s.scrollBy(p, key)
				}
				return Result{View: true}, nil
			}
		}
		return Result{}, nil
	}
	if m.Button != MouseLeft || m.Action != Press {
		return Result{}, nil
	}
	if href := s.links[pt]; href != "" {
		// A link opens only after the host asks the person: it comes back
		// as an effect, never as an OSC 8 link the outer terminal follows.
		return Result{Effects: []Effect{{Action: "link", Name: href, Value: href, HasValue: true}}}, nil
	}
	for _, b := range slices.Backward(path) {
		if b.kind == "split" && s.onDivider(b, pt) {
			s.drag = b
			return Result{}, nil
		}
		if res, ok, err := s.clickOn(b, pt); ok || err != nil {
			if focusable(s, b) {
				s.setFocus(b)
			}
			return res, err
		}
	}
	return Result{}, nil
}

// scrollBy moves a table's view or a scroll box by one row.
func (s *Surface) scrollBy(b *box, key string) {
	d := 1
	if key == "up" {
		d = -1
	}
	switch w := b.w.(type) {
	case *tableWidget:
		s.view.Scroll[b.key] = max(w.offset+d, 0)
		w.offset = s.view.Scroll[b.key]
		// The cursor stays where it is unless it leaves the view.
		vis := max(b.inner().Dy()-1, 1)
		if w.cursor < w.offset || w.cursor >= w.offset+vis {
			w.cursor = clampInt(w.cursor, w.offset, w.offset+vis-1)
			s.view.Cursor[b.key] = w.rowKey(w.cursor)
		}
	case *scrollWidget:
		s.view.Scroll[b.key] = max(w.offset(s, b)+d, 0)
	}
	s.markView(b.key)
}

// clickOn handles a left press at pt on box b. ok reports that it took it.
func (s *Surface) clickOn(b *box, pt image.Point) (Result, bool, error) {
	in := b.inner()
	switch w := b.w.(type) {
	case *tabsWidget:
		if pt.Y == in.Min.Y {
			if i := w.titleAt(pt.X); i >= 0 {
				res, err := s.openTab(b, w, i)
				return res, true, err
			}
		}
	case *tableWidget:
		if pt.Y == in.Min.Y {
			if i := w.colAt(pt.X); i >= 0 && w.cols[i].sortable {
				s.sortBy(b, w, w.cols[i].field)
				return Result{View: true}, true, nil
			}
			return Result{}, true, nil
		}
		row := w.offset + pt.Y - w.bodyTop
		if row >= 0 && row < len(w.rows) {
			w.cursor = row
			s.view.Cursor[b.key] = w.rowKey(row)
			s.markView(b.key)
			res, err := s.run(b, w.onSelect, map[string]any{"row": w.rows[row], "value": w.rows[row]})
			res.View = true
			return res, true, err
		}
	case *listWidget:
		for i, g := range b.kids {
			if pt.In(g.rect) {
				w.cursor = i
				s.view.Cursor[b.key] = strconv.Itoa(i)
				s.markView(b.key)
				var res Result
				var err error
				if w.selectable {
					res, err = s.run(b, s.actionsOf(b.src, "on-select"), map[string]any{"item": w.items[i], "value": w.items[i]})
				}
				res.View = true
				return res, true, err
			}
		}
	case *cardWidget:
		if pt.Y == in.Min.Y && !w.modal {
			s.toggleFold(b, w)
			return Result{View: true}, true, nil
		}
	case *buttonWidget:
		if !w.disabled {
			res, err := s.press(b, w.press)
			return res, true, err
		}
	case *fieldWidget:
		if w.editable() {
			// The click puts the caret at the end of the text; the focus
			// follows (Mouse).
			return s.moveCaret(b, w, utf8.RuneCountInString(w.value)), true, nil
		}
	}
	if acts := s.actionsOf(b.src, "on-press"); len(acts) > 0 && b.kind != "tml" {
		res, err := s.run(b, acts, nil)
		return res, true, err
	}
	return Result{}, false, nil
}

// hit returns the boxes under pt, outermost first.
func (s *Surface) hit(b *box, pt image.Point, path []*box) []*box {
	if !pt.In(b.rect) || b.hidden {
		return path
	}
	path = append(path, b)
	if cw, ok := b.w.(*cardWidget); ok && cw.folded {
		return path
	}
	if !pt.In(viewport(b)) {
		return path
	}
	var top []*box
	for _, v := range slices.Backward(b.kids) {
		if !pt.In(v.rect) {
			continue
		}
		sub := s.hit(v, pt, path)
		if b.kind != "stack" {
			return sub
		}
		// In a stack, a layer that takes no input lets the click through
		// to the layers under it.
		if top == nil {
			top = sub
		}
		if slices.ContainsFunc(sub[len(path):], s.clickable) {
			return sub
		}
	}
	if top != nil {
		return top
	}
	return path
}

// clickable reports whether a click on b does anything.
func (s *Surface) clickable(b *box) bool {
	switch b.w.(type) {
	case *tabsWidget, *tableWidget, *listWidget, *cardWidget, *buttonWidget, *scrollWidget:
		return true
	}
	return len(s.actionsOf(b.src, "on-press")) > 0
}

// onDivider reports whether pt is on a split's divider.
func (s *Surface) onDivider(b *box, pt image.Point) bool {
	if len(b.kids) < 2 {
		return false
	}
	first := b.kids[0].rect
	if b.src.AttrString("dir") == "v" {
		return pt.Y == first.Max.Y
	}
	return pt.X == first.Max.X
}

func (s *Surface) dragSplit(m Mouse) (Result, error) {
	b := s.drag
	if m.Action == Release {
		s.drag = nil
		return Result{View: true}, nil
	}
	in := b.inner()
	var r float64
	if b.src.AttrString("dir") == "v" {
		r = float64(m.Y-in.Min.Y) / float64(max(in.Dy()-1, 1))
	} else {
		r = float64(m.X-in.Min.X) / float64(max(in.Dx()-1, 1))
	}
	s.view.Split[b.key] = max(0.05, min(0.95, r))
	s.markView(b.key)
	return Result{View: true}, nil
}
