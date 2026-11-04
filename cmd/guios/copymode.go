package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/cmd/guios/locales"
)

// Copy mode: the TUI's (internal/input/copymode_*), over a pane's text.
//
// The prefix then [ shows the pane's screen and history as rows of text,
// with a cursor that moves as in vim: h j k l, w b e and W B E, 0 ^ $,
// gg G, H M L, { }, %, f F t T and ; ,, Ctrl+U Ctrl+D Ctrl+B Ctrl+F, with
// a count before any of them. v, V and Ctrl+V select by character, line
// and block, o goes to the selection's other end, and y or c copies it
// to the clipboard. / and ? search down and up, n and N go on, Ctrl+L
// clears the search. q or Escape leaves, i leaves for the pane.
//
// The rows are the pane's, at its width: the terminal's text, its long
// lines cut where the screen wraps them.

// copyVisual is a copy mode selection's kind.
type copyVisual int

const (
	visualNone copyVisual = iota
	visualChar
	visualLine
	visualBlock
)

// copyMode is the state of copy mode, on one pane at a time.
type copyMode struct {
	pane string
	rows [][]rune
	// hist is the terminal's rows as VT sequences, read as copy mode
	// opened; styles are the style of each rune of rows, read from it in
	// the theme of the window's darkness (dark), nil where the terminal
	// gave none (a full-screen program's screen).
	hist   []byte
	cols   int
	styles [][]cellStyle
	dark   bool
	cy, cx int // the cursor's row and column
	top    int // the first row shown
	shown  int // how many rows show, as the last frame laid them out
	visual copyVisual
	ay, ax int // the selection's other end

	count   int    // a count typed before a motion
	pending string // g, f, F, t or T, waiting for the next key
	find    string // the last f F t T and its character, for ; and ,

	query     string // the search
	searching bool   // the search is being typed
	back      bool   // the search goes up (?)
	editing   string // the search as it is typed

	message string // the status line's message, until the next key
	ref     ui.Handle

	// hints are the labeled texts of hint mode (the prefix and F), nil
	// outside it, and typed what of a label has been typed.
	hints []copyHint
	typed string
}

// copyHint is a text hint mode labels on the screen: a URL, a path, a
// commit hash, an address.
type copyHint struct {
	y, x, n int
	text    string
	label   string
}

// hintPattern finds what hint mode labels, as the TUI's hints do.
var hintPattern = regexp.MustCompile(`https?://[^\s'"<>()]+|(?:~|\.{1,2})?/[\w.@%+-]+(?:/[\w.@%+-]*)*|\b[0-9a-f]{7,40}\b|\b\d{1,3}(?:\.\d{1,3}){3}(?::\d+)?\b`)

// hintLabels are the keys of the labels, the home row first.
const hintLabels = "asdfghjklqwertyuiopzxcvbnm"

// openHints labels the texts on the pane's screen to copy one by its
// label: the prefix and F.
func (a *App) openHints() {
	a.openCopyMode()
	cm := a.cm
	if cm == nil {
		return
	}
	first := max(0, len(cm.rows)-cm.shown)
	var hints []copyHint
	for y := first; y < len(cm.rows); y++ {
		row := string(cm.rows[y])
		for _, m := range hintPattern.FindAllStringIndex(row, -1) {
			text := row[m[0]:m[1]]
			if len(text) < 3 {
				continue
			}
			x := utf8.RuneCountInString(row[:m[0]])
			hints = append(hints, copyHint{y: y, x: x, n: utf8.RuneCountInString(text), text: text})
		}
	}
	if len(hints) == 0 {
		a.cm = nil
		a.toast(locales.CopyModeNoHints())
		return
	}
	two := len(hints) > len(hintLabels)
	for i := range hints {
		if two {
			hints[i].label = string(hintLabels[i/len(hintLabels)%len(hintLabels)]) + string(hintLabels[i%len(hintLabels)])
		} else {
			hints[i].label = string(hintLabels[i])
		}
	}
	cm.hints = hints
	cm.top = first
	cm.message = locales.CopyModeHintsHint()
}

// hintKey takes a key in hint mode: a label's letters copy its text.
func (a *App) hintKey(k string) {
	cm := a.cm
	if k == "esc" || k == "q" && cm.typed == "" {
		a.closeCopyMode()
		return
	}
	if k == "backspace" && cm.typed != "" {
		cm.typed = cm.typed[:len(cm.typed)-1]
		return
	}
	if len(k) != 1 {
		return
	}
	cm.typed += k
	any := false
	for _, h := range cm.hints {
		if h.label == cm.typed {
			a.copyText = h.text
			a.addBuffer(h.text)
			a.closeCopyMode()
			return
		}
		any = any || strings.HasPrefix(h.label, cm.typed)
	}
	if !any {
		cm.typed = ""
	}
}

// hintSpans draws a row in hint mode: each text marked, its label over
// its first characters.
func (cm *copyMode) hintSpans(y int, pal *palette) []ui.Span {
	row := cm.rows[y]
	var spans []ui.Span
	x := 0
	for _, h := range cm.hints {
		if h.y != y {
			continue
		}
		if h.x > x {
			spans = append(spans, ui.Span{Text: string(row[x:h.x])})
		}
		label := h.label
		live := strings.HasPrefix(label, cm.typed)
		rest := string(row[h.x+min(len(label), h.n) : h.x+h.n])
		if live {
			spans = append(spans, ui.Span{Text: label, Background: pal.accent, Color: pal.popover, Weight: 700})
			spans = append(spans, ui.Span{Text: rest, Background: pal.attention.Alpha(0.3)})
		} else {
			spans = append(spans, ui.Span{Text: string(row[h.x : h.x+h.n])})
		}
		x = h.x + h.n
	}
	if x < len(row) {
		spans = append(spans, ui.Span{Text: string(row[x:])})
	}
	return spans
}

// openCopyMode starts copy mode on the pane the user is in.
func (a *App) openCopyMode() {
	s := a.session(a.selected)
	if s == nil {
		return
	}
	id := a.focusOf(s)
	e := a.terms[id]
	if e == nil || e.term == nil {
		return
	}
	cols, _ := e.term.Size()
	cm := &copyMode{pane: id, cols: cols}
	if cm.hist = e.term.History(); len(cm.hist) > 0 {
		light, _ := a.termThemes()
		lines, styles := styledLines(cm.hist, light)
		cm.rows, cm.styles = wrapStyled(lines, styles, cols)
	} else {
		cm.rows = wrapRows(e.term.Text(), cols)
	}
	// The cursor starts on the last row with text, as the shell's prompt.
	cm.cy = len(cm.rows) - 1
	for cm.cy > 0 && len(cm.rows[cm.cy]) == 0 {
		cm.cy--
	}
	cm.cx = len(cm.rows[cm.cy])
	cm.shown = 30
	cm.top = max(0, cm.cy-cm.shown+1)
	cm.message = locales.CopyModeHint()
	a.cm = cm
}

// wrapRows cuts the terminal's text into the rows the screen shows at
// cols columns: a long line takes as many rows as the screen gives it.
func wrapRows(text string, cols int) [][]rune {
	cols = max(cols, 1)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var rows [][]rune
	for _, l := range lines {
		r := []rune(strings.TrimRight(l, " "))
		for len(r) > cols {
			rows = append(rows, r[:cols])
			r = r[cols:]
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		rows = [][]rune{{}}
	}
	return rows
}

// copyKey takes a key in copy mode, as the keymap spells it ("j",
// "ctrl+u", "esc"). It reports whether copy mode took it: every key, while
// copy mode lasts.
func (a *App) copyKey(k string) {
	cm := a.cm
	if cm == nil {
		return
	}
	cm.message = ""
	if cm.hints != nil {
		a.hintKey(k)
		return
	}
	if cm.searching {
		a.copySearchKey(k)
		return
	}
	if cm.pending != "" {
		p := cm.pending
		cm.pending = ""
		if p == "g" {
			if k == "g" {
				cm.cy, cm.cx = max(cm.takeCount(1), 1)-1, 0
				if cm.count == 0 && cm.cy >= len(cm.rows) {
					cm.cy = len(cm.rows) - 1
				}
				cm.clamp()
			}
			cm.count = 0
			return
		}
		if utf8.RuneCountInString(k) == 1 || k == "space" {
			if k == "space" {
				k = " "
			}
			cm.find = p + k
			n := cm.takeCount(1)
			for range n {
				cm.findChar(p, []rune(k)[0])
			}
		}
		cm.count = 0
		return
	}
	// A count: 1 to 9, and 0 after them.
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' || k == "0" && cm.count > 0 {
		cm.count = min(cm.count*10+int(k[0]-'0'), 99999)
		cm.message = strconv.Itoa(cm.count)
		return
	}
	n := cm.takeCount(1)
	half := max(cm.shown/2, 1)
	switch k {
	case "q", "esc":
		if cm.visual != visualNone && k == "esc" {
			cm.visual = visualNone
			return
		}
		a.closeCopyMode()
		return
	case "i":
		a.closeCopyMode()
		return
	case "h", "left":
		cm.cx = max(cm.cx-n, 0)
	case "l", "right":
		cm.cx = min(cm.cx+n, max(len(cm.rows[cm.cy])-1, 0))
	case "j", "down", "enter":
		cm.cy = min(cm.cy+n, len(cm.rows)-1)
	case "k", "up":
		cm.cy = max(cm.cy-n, 0)
	case "w", "W", "b", "B", "e", "E":
		for range n {
			cm.word(k)
		}
	case "0", "home":
		cm.cx = 0
	case "^":
		cm.cx = firstNonBlank(cm.rows[cm.cy])
	case "$", "end":
		cm.cx = max(len(cm.rows[cm.cy])-1, 0)
	case "ctrl+u":
		cm.cy, cm.top = max(cm.cy-half*n, 0), max(cm.top-half*n, 0)
	case "ctrl+d":
		cm.cy, cm.top = min(cm.cy+half*n, len(cm.rows)-1), cm.top+half*n
	case "ctrl+b", "pgup":
		cm.cy, cm.top = max(cm.cy-cm.shown*n, 0), max(cm.top-cm.shown*n, 0)
	case "ctrl+f", "pgdown":
		cm.cy, cm.top = min(cm.cy+cm.shown*n, len(cm.rows)-1), cm.top+cm.shown*n
	case "g":
		cm.count = n
		if n == 1 {
			cm.count = 0
		}
		cm.pending = "g"
		return
	case "G":
		cm.cy = len(cm.rows) - 1
		if n > 1 || cm.count > 0 {
			cm.cy = min(n, len(cm.rows)) - 1
		}
		cm.cx = 0
	case "H":
		cm.cy = cm.top
	case "M":
		cm.cy = min(cm.top+cm.shown/2, len(cm.rows)-1)
	case "L":
		cm.cy = min(cm.top+cm.shown-1, len(cm.rows)-1)
	case "{", "}":
		for range n {
			cm.paragraph(k == "}")
		}
		cm.cx = 0
	case "%":
		cm.matchBracket()
	case "f", "F", "t", "T":
		cm.count = n
		if n == 1 {
			cm.count = 0
		}
		cm.pending = k
		return
	case ";", ",":
		if len(cm.find) >= 2 {
			p, ch := cm.find[:1], []rune(cm.find[1:])[0]
			if k == "," {
				p = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[p]
			}
			for range n {
				cm.findChar(p, ch)
			}
		}
	case "/", "?":
		cm.searching, cm.back, cm.editing = true, k == "?", ""
		return
	case "n", "N":
		if cm.query == "" {
			return
		}
		for range n {
			if !cm.search(cm.back != (k == "N")) {
				cm.message = locales.CopyModeNotFound(cm.query)
				break
			}
		}
	case "ctrl+l":
		cm.query = ""
	case "v", "V", "ctrl+v":
		v := map[string]copyVisual{"v": visualChar, "V": visualLine, "ctrl+v": visualBlock}[k]
		switch {
		case cm.visual == v:
			cm.visual = visualNone
		case cm.visual == visualNone:
			cm.visual, cm.ay, cm.ax = v, cm.cy, cm.cx
		default:
			cm.visual = v
		}
	case "o":
		if cm.visual != visualNone {
			cm.cy, cm.cx, cm.ay, cm.ax = cm.ay, cm.ax, cm.cy, cm.cx
		}
	case "y", "c":
		if cm.visual == visualNone {
			return
		}
		text := cm.selected()
		a.copyText = text
		a.addBuffer(text)
		cm.visual = visualNone
		cm.message = locales.CopyModeYanked(strconv.Itoa(utf8.RuneCountInString(text)))
	}
	cm.clamp()
}

// copySearchKey takes a key while the search is typed.
func (a *App) copySearchKey(k string) {
	cm := a.cm
	switch k {
	case "esc":
		cm.searching = false
	case "enter":
		cm.searching = false
		if cm.editing != "" {
			cm.query = cm.editing
		}
		if cm.query != "" && !cm.search(cm.back) {
			cm.message = locales.CopyModeNotFound(cm.query)
		}
		cm.clamp()
	case "backspace":
		if r := []rune(cm.editing); len(r) > 0 {
			cm.editing = string(r[:len(r)-1])
		} else {
			cm.searching = false
		}
	case "space":
		cm.editing += " "
	default:
		if utf8.RuneCountInString(k) == 1 {
			cm.editing += k
		}
	}
}

// copyText types text into copy mode, key by key, as a terminal's text
// comes on Linux.
func (a *App) copyTyped(text string) {
	for _, r := range text {
		k := string(r)
		if r == ' ' {
			k = "space"
		}
		a.copyKey(k)
	}
}

func (a *App) closeCopyMode() {
	a.cm = nil
	a.defer_(a.focusTerminal)
}

func (cm *copyMode) takeCount(def int) int {
	n := cm.count
	cm.count = 0
	if n == 0 {
		return def
	}
	return n
}

// clamp keeps the cursor on the text and in view.
func (cm *copyMode) clamp() {
	cm.cy = min(max(cm.cy, 0), len(cm.rows)-1)
	cm.cx = min(max(cm.cx, 0), max(len(cm.rows[cm.cy])-1, 0))
	shown := max(cm.shown, 1)
	cm.top = min(max(cm.top, 0), max(len(cm.rows)-shown, 0))
	if cm.cy < cm.top {
		cm.top = cm.cy
	}
	if cm.cy >= cm.top+shown {
		cm.top = cm.cy - shown + 1
	}
}

func firstNonBlank(r []rune) int {
	for i, c := range r {
		if !unicode.IsSpace(c) {
			return i
		}
	}
	return 0
}

// class is a character's kind for word motions: 0 space, 1 word, 2 other;
// a WORD (W, B, E) has only space and the rest.
func class(r rune, big bool) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case big || unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
		return 1
	}
	return 2
}

// at is the character at a position, a space past a row's end; rows end
// in a newline, which is space.
func (cm *copyMode) at(y, x int) rune {
	if x < len(cm.rows[y]) {
		return cm.rows[y][x]
	}
	return ' '
}

// step moves a position one character forward or back across rows. It
// reports false at either end of the text.
func (cm *copyMode) step(y, x *int, fwd bool) bool {
	if fwd {
		if *x+1 < len(cm.rows[*y]) {
			*x++
			return true
		}
		if *y+1 < len(cm.rows) {
			*y, *x = *y+1, 0
			return true
		}
		return false
	}
	if *x > 0 {
		*x--
		return true
	}
	if *y > 0 {
		*y--
		*x = max(len(cm.rows[*y])-1, 0)
		return true
	}
	return false
}

// word moves as w, b, e and W, B, E do.
func (cm *copyMode) word(k string) {
	big := k == "W" || k == "B" || k == "E"
	y, x := cm.cy, cm.cx
	cls := func() int {
		if x >= len(cm.rows[y]) {
			return 0
		}
		return class(cm.rows[y][x], big)
	}
	switch strings.ToLower(k) {
	case "w":
		c0 := cls()
		row := y
		for cls() == c0 && c0 != 0 && y == row {
			if !cm.step(&y, &x, true) {
				return
			}
		}
		for cls() == 0 {
			if len(cm.rows[y]) == 0 && y != row {
				break // an empty row is a word
			}
			if !cm.step(&y, &x, true) {
				break
			}
		}
	case "e":
		if !cm.step(&y, &x, true) {
			return
		}
		for cls() == 0 {
			if !cm.step(&y, &x, true) {
				return
			}
		}
		c0 := cls()
		for {
			ny, nx := y, x
			if !cm.step(&ny, &nx, true) || ny != y {
				break
			}
			if nx >= len(cm.rows[ny]) || class(cm.rows[ny][nx], big) != c0 {
				break
			}
			y, x = ny, nx
		}
	case "b":
		if !cm.step(&y, &x, false) {
			return
		}
		for cls() == 0 {
			if !cm.step(&y, &x, false) {
				break
			}
		}
		c0 := cls()
		for {
			ny, nx := y, x
			if !cm.step(&ny, &nx, false) || ny != y {
				break
			}
			if class(cm.rows[ny][nx], big) != c0 {
				break
			}
			y, x = ny, nx
		}
	}
	cm.cy, cm.cx = y, x
}

// paragraph moves to the next or the previous empty row.
func (cm *copyMode) paragraph(down bool) {
	d := -1
	if down {
		d = 1
	}
	y := cm.cy + d
	for y > 0 && y < len(cm.rows)-1 && len(cm.rows[y]) != 0 {
		y += d
	}
	cm.cy = min(max(y, 0), len(cm.rows)-1)
}

// matchBracket goes to the bracket that matches the one at the cursor, or
// the first one after it on the row.
func (cm *copyMode) matchBracket() {
	pairs := map[rune]rune{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}
	row := cm.rows[cm.cy]
	x := cm.cx
	for x < len(row) && pairs[row[x]] == 0 {
		x++
	}
	if x >= len(row) {
		return
	}
	open := row[x]
	want := pairs[open]
	fwd := strings.ContainsRune("([{", open)
	y, depth := cm.cy, 0
	for cm.step(&y, &x, fwd) {
		switch cm.at(y, x) {
		case open:
			depth++
		case want:
			if depth == 0 {
				cm.cy, cm.cx = y, x
				return
			}
			depth--
		}
	}
}

// findChar moves as f, F, t and T do, on the cursor's row.
func (cm *copyMode) findChar(p string, ch rune) {
	row := cm.rows[cm.cy]
	switch p {
	case "f", "t":
		start := cm.cx + 1
		if p == "t" {
			start++
		}
		for x := start; x < len(row); x++ {
			if row[x] == ch {
				cm.cx = x
				if p == "t" {
					cm.cx = x - 1
				}
				return
			}
		}
	case "F", "T":
		start := cm.cx - 1
		if p == "T" {
			start--
		}
		for x := start; x >= 0; x-- {
			if row[x] == ch {
				cm.cx = x
				if p == "T" {
					cm.cx = x + 1
				}
				return
			}
		}
	}
}

// search moves to the next match of the query, up or down, round past
// the end. A query in lower case finds any case.
func (cm *copyMode) search(up bool) bool {
	q := []rune(cm.query)
	fold := strings.ToLower(cm.query) == cm.query
	match := func(y, x int) bool {
		row := cm.rows[y]
		if x+len(q) > len(row) {
			return false
		}
		for i, r := range q {
			c := row[x+i]
			if fold {
				c = unicode.ToLower(c)
			}
			if c != r {
				return false
			}
		}
		return true
	}
	y, x := cm.cy, cm.cx
	total := 0
	for _, r := range cm.rows {
		total += len(r) + 1
	}
	for range total {
		if !cm.step(&y, &x, !up) {
			if up {
				y = len(cm.rows) - 1
				x = max(len(cm.rows[y])-1, 0)
			} else {
				y, x = 0, 0
			}
		}
		if match(y, x) {
			cm.cy, cm.cx = y, x
			return true
		}
	}
	return false
}

// span is the selection's columns on a row, end excluded; ok false when
// the row has none.
func (cm *copyMode) span(y int) (from, to int, ok bool) {
	if cm.visual == visualNone {
		return 0, 0, false
	}
	y0, x0, y1, x1 := cm.ay, cm.ax, cm.cy, cm.cx
	if y0 > y1 || y0 == y1 && x0 > x1 {
		y0, x0, y1, x1 = y1, x1, y0, x0
	}
	if y < y0 || y > y1 {
		return 0, 0, false
	}
	n := len(cm.rows[y])
	switch cm.visual {
	case visualLine:
		return 0, max(n, 1), true
	case visualBlock:
		l, r := min(cm.ax, cm.cx), max(cm.ax, cm.cx)
		return l, r + 1, true
	}
	from, to = 0, max(n, 1)
	if y == y0 {
		from = x0
	}
	if y == y1 {
		to = x1 + 1
	}
	return from, to, true
}

// selected is the selection's text: rows joined by newlines, the block's
// rows each cut to its columns.
func (cm *copyMode) selected() string {
	var lines []string
	for y := range cm.rows {
		from, to, ok := cm.span(y)
		if !ok {
			continue
		}
		row := cm.rows[y]
		from, to = min(from, len(row)), min(to, len(row))
		lines = append(lines, strings.TrimRight(string(row[from:to]), " "))
	}
	return strings.Join(lines, "\n")
}

// copyView draws copy mode over a pane: its rows of text in the
// terminal's font, the selection, the search's matches, the cursor, and a
// status line. It takes every key.
func (a *App) copyView(c *ui.Context, pal *palette) {
	cm := a.cm
	size := a.set.FontSize
	// The terminal's rows: its cell's height, as the view last sized it.
	rowH := float32(int(size*a.set.LineHeight*1.3 + 0.5))
	if e := a.terms[cm.pane]; e != nil && e.stream != nil {
		if _, h := e.stream.CellSize(); h > 0 {
			rowH = float32(h)
		}
	}
	box := ui.Column(c.Key("copymode")).Fill().Padding(6, 16).Focusable().FocusRing(false).AutoFocus().Bind(&cm.ref).Clip().Label(locales.CopyModeLabel())
	box.HandleInput(func(ev ui.InputEvent) bool {
		switch ev.Kind {
		case ui.InputKeyDown:
			if typesText(ev.Mods, ev.Key) {
				return true // its text comes next
			}
			if k, ok := keyString(ev.Mods, ev.Key); ok {
				a.copyKey(k)
			}
			return true
		case ui.InputText:
			a.copyTyped(ev.Text)
			return true
		}
		return false
	})
	box.TextCaret(ui.Rect{})
	if b := cm.ref.Bounds(c); b.H > 0 {
		if n := int((b.H - 12 - rowH) / rowH); n > 0 && n != cm.shown {
			cm.shown = n
			cm.clamp()
		}
	}
	mono := a.termFamily()
	if cm.hist != nil && cm.dark != pal.dark {
		// The colours by index are the theme's of the window's darkness.
		light, dark := a.termThemes()
		th := light
		if pal.dark {
			th = dark
		}
		lines, styles := styledLines(cm.hist, th)
		if _, st := wrapStyled(lines, styles, cm.cols); len(st) == len(cm.rows) {
			cm.styles = st
		}
		cm.dark = pal.dark
	}
	var tfg, tbg ui.Color
	{
		light, dark := a.termThemes()
		th := light
		if pal.dark {
			th = dark
		}
		tfg, tbg = th.Foreground, th.Background
	}
	q := []rune(cm.query)
	fold := strings.ToLower(cm.query) == cm.query
	box.Children(func() {
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			for y := cm.top; y < min(cm.top+cm.shown, len(cm.rows)); y++ {
				row := cm.rows[y]
				from, to, sel := cm.span(y)
				hit := make([]bool, len(row)+1)
				if len(q) > 0 {
					for x := 0; x+len(q) <= len(row); x++ {
						ok := true
						for i, r := range q {
							ch := row[x+i]
							if fold {
								ch = unicode.ToLower(ch)
							}
							if ch != r {
								ok = false
								break
							}
						}
						if ok {
							for i := range q {
								hit[x+i] = true
							}
						}
					}
				}
				var spans []ui.Span
				n := max(len(row), cm.cx+1)
				style := func(x int) int {
					switch {
					case y == cm.cy && x == cm.cx && cm.hints == nil:
						return 3
					case sel && x >= from && x < to:
						return 2
					case x < len(row) && hit[x]:
						return 1
					}
					return 0
				}
				var rowSt []cellStyle
				if y < len(cm.styles) {
					rowSt = cm.styles[y]
				}
				cell := func(x int) cellStyle {
					if x < len(rowSt) {
						return rowSt[x]
					}
					return cellStyle{}
				}
				for x := 0; x < n; {
					st := style(x)
					cs := cell(x)
					end := x + 1
					for end < n && style(end) == st && cell(end) == cs {
						end++
					}
					var text string
					for i := x; i < end; i++ {
						if i < len(row) {
							text += string(row[i])
						} else {
							text += " "
						}
					}
					// The terminal's own colours and attributes, and copy
					// mode's marks on top.
					sp := cs.span(text, tfg, tbg)
					switch st {
					case 1:
						sp.Background = pal.attention.Alpha(0.35)
					case 2:
						sp.Background = pal.accent.Alpha(0.35)
					case 3:
						sp.Background, sp.Color = pal.accent, pal.popover
					}
					spans = append(spans, sp)
					x = end
				}
				if cm.hints != nil {
					spans = cm.hintSpans(y, pal)
				}
				if len(spans) == 0 {
					spans = []ui.Span{{Text: " "}}
				}
				ui.RichText(c, spans...).Font(mono).FontSize(size).TextColor(pal.text).Height(rowH).NoWrap().Shrink(0)
			}
		})
		ui.Row(c.Key("status")).Height(rowH + 6).Shrink(0).Gap(10).AlignItems(ui.Center).Children(func() {
			mode := locales.CopyModeNormal()
			if cm.hints != nil {
				mode = locales.CopyModeHints()
			}
			switch cm.visual {
			case visualChar:
				mode = locales.CopyModeVisual()
			case visualLine:
				mode = locales.CopyModeVisualLine()
			case visualBlock:
				mode = locales.CopyModeVisualBlock()
			}
			chip(c, pal, pal.accent.Alpha(0.18), pal.accent, func() {
				ui.Text(c, mode).FontSize(11.5).FontWeight(650).SingleLine()
			})
			switch {
			case cm.searching:
				prompt := "/"
				if cm.back {
					prompt = "?"
				}
				ui.Text(c, prompt+cm.editing+"▏").Font(mono).FontSize(12.5).TextColor(pal.text).SingleLine()
			case cm.message != "":
				ui.Text(c, cm.message).FontSize(12).TextColor(pal.textMuted).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
			}
			ui.Spacer(c)
			ui.Text(c, strconv.Itoa(cm.cy+1)+":"+strconv.Itoa(cm.cx+1)+"  "+strconv.Itoa(len(cm.rows))).
				Font(mono).FontSize(11.5).TextColor(pal.textFaint).SingleLine().Shrink(0)
		})
	})
}
