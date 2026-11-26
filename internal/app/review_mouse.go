package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The review with the mouse. Every place on the frame comes from the layout
// the frame was drawn with (reviewLayout), except the footer's key hints and
// the scroll bar, which the frame records as it draws them:
//
//   - a click on a file in the list opens it, and gives the diff the keys
//   - a click on a diff row puts the cursor there; on a hunk header it also
//     folds or unfolds the hunk
//   - the wheel scrolls the side under the pointer: the list, or the diff
//   - a press on the rule between the two sides drags it, which sets the
//     list's width for this and every later review
//   - a press on the diff's scroll bar drags it; a press on its track jumps
//     there first
//   - a click on a key hint in the footer is that key (see ReviewHintKeyAt)
//
// The rows are 0 for the top edge, 1 for the header, 2 for its rule, and the
// panes from reviewPaneTop. The columns: the edge, a space, the list, a
// space, the rule, a space, the diff, the scroll bar and the edge.

// reviewPaneTop is the first screen row of the two panes.
const reviewPaneTop = 3

// reviewDiffMin is the narrowest diff column the overlay lays out. A drag
// on the rule stops there.
const reviewDiffMin = 20

// reviewDrag is what a held press on the review moves.
type reviewDrag int

const (
	reviewDragNone reviewDrag = iota
	reviewDragRule
	reviewDragBar
)

// reviewHintSpan is where one key hint of the footer is on screen.
type reviewHintSpan struct {
	x0, x1 int // the cells x0 to x1-1
	key    string
}

// reviewFold names a folded hunk: its file and its header, so the fold
// outlasts a reload that keeps the hunk.
type reviewFold struct{ path, header string }

// reviewScrollBar is the diff's scroll bar as the last frame drew it.
type reviewScrollBar struct {
	shown              bool
	x, top, rows       int
	thumbTop, thumbLen int
	total              int
}

// reviewGeometry is where the parts of the frame are on screen.
type reviewGeometry struct {
	w, h, listW, diffW, paneH int
	listX, ruleX, diffX       int
}

func (m *OS) reviewGeometry() reviewGeometry {
	w, h, listW, diffW, paneH := m.reviewLayout()
	return reviewGeometry{w: w, h: h, listW: listW, diffW: diffW, paneH: paneH,
		listX: 2, ruleX: 3 + listW, diffX: 5 + listW}
}

// reviewPaneRow is the pane row of screen row y, and whether y is in the
// panes.
func (g reviewGeometry) reviewPaneRow(y int) (int, bool) {
	row := y - reviewPaneTop
	return row, row >= 0 && row < g.paneH
}

// reviewLaidOut reports whether the frame shows the two panes, which a
// screen too small for them does not.
func (g reviewGeometry) reviewLaidOut() bool {
	return g.w >= reviewMinW && g.h >= reviewMinH && g.diffW >= reviewDiffMin
}

// ReviewMouseClick handles a left press at x, y while the review is open,
// other than on a key hint (see ReviewHintKeyAt).
func (m *OS) ReviewMouseClick(x, y int) {
	r := &m.review
	g := m.reviewGeometry()
	if !g.reviewLaidOut() {
		return
	}
	// The header's count of new hunks goes to the next one.
	if y == 1 && r.follow.x1 > r.follow.x0 && x >= r.follow.x0 && x < r.follow.x1 {
		m.reviewPersonMoved()
		m.ReviewNextNew()
		return
	}
	if m.ReviewCompareShown() {
		m.reviewCompareClick(y)
		return
	}
	if r.editor != nil {
		// The line being typed keeps the keys. Its footer hints save or drop it.
		return
	}
	row, inPanes := g.reviewPaneRow(y)
	if !inPanes {
		return
	}
	defer m.reviewMoved()
	switch {
	case r.bar.shown && x == r.bar.x:
		m.reviewBarPress(row)
	case x >= g.ruleX-1 && x <= g.ruleX+1:
		r.drag = reviewDragRule
	case x >= g.listX && x < g.listX+g.listW:
		m.reviewListClick(row)
	case x >= g.diffX && x < g.diffX+g.diffW:
		m.reviewDiffClick(row)
	}
}

// reviewListClick opens the file on list row row.
func (m *OS) reviewListClick(row int) {
	r := &m.review
	idx := r.listScroll + row
	if idx < 0 || idx >= len(r.files) {
		return
	}
	if idx != r.file {
		r.file, r.cursor, r.scroll = idx, 0, 0
	}
	r.listFocus, r.listBack = false, 0
}

// reviewDiffClick puts the cursor on diff row row, and folds or unfolds the
// hunk when it is a hunk's header.
func (m *OS) reviewDiffClick(row int) {
	r := &m.review
	rows := m.reviewRows(m.reviewRowsWidth())
	idx := r.scroll + row
	if idx < 0 || idx >= len(rows) {
		return
	}
	r.listFocus = false
	r.cursor = reviewNoteStart(rows, idx)
	if rows[idx].kind == reviewRowHunk {
		m.reviewToggleFold(rows[idx].hunk)
	}
}

// reviewCompareClick puts the compare view's cursor on the attempt at
// screen row y. The attempts start under the header, its rule and the
// column names.
func (m *OS) reviewCompareClick(y int) {
	c := m.review.compare
	if c == nil || c.confirmKeep != "" || m.review.editor != nil {
		return
	}
	_, h := m.GetRenderWidth(), m.GetRenderHeight()
	listH := h - 2 - 6
	start := 0
	if c.cursor >= listH {
		start = c.cursor - listH + 1
	}
	i := start + y - 4
	if y < 4 || y-4 >= listH || i >= len(c.rows) {
		return
	}
	c.cursor = i
}

// ReviewFoldHunk folds or unfolds the hunk under the cursor. It reports
// whether the cursor is on a hunk's header.
func (m *OS) ReviewFoldHunk() bool {
	row, ok := m.reviewRowUnderCursor()
	if !ok || row.kind != reviewRowHunk {
		return false
	}
	m.reviewToggleFold(row.hunk)
	return true
}

// reviewToggleFold folds or unfolds hunk h of the file shown, and keeps the
// cursor on its header.
func (m *OS) reviewToggleFold(h int) {
	r := &m.review
	e := r.currentFile()
	if e == nil || e.file == nil || h < 0 || h >= len(e.file.Hunks) {
		return
	}
	k := reviewFold{e.path, e.file.Hunks[h].Header}
	if r.folds == nil {
		r.folds = map[reviewFold]bool{}
	}
	if r.folds[k] {
		delete(r.folds, k)
	} else {
		r.folds[k] = true
	}
	for i, row := range m.reviewRows(m.reviewRowsWidth()) {
		if row.kind == reviewRowHunk && row.hunk == h {
			r.cursor = i
			break
		}
	}
}

// reviewFolded reports whether hunk h of the file at path is folded.
func (r *reviewState) reviewFolded(path, header string) bool {
	return r.folds[reviewFold{path, header}]
}

// ReviewMouseWheel scrolls the side under x, y by delta rows.
func (m *OS) ReviewMouseWheel(x, y, delta int) {
	defer m.reviewMoved()
	r := &m.review
	g := m.reviewGeometry()
	if !m.ReviewCompareShown() && g.reviewLaidOut() && r.editor == nil && x < g.ruleX {
		if _, in := g.reviewPaneRow(y); in {
			r.listScroll = min(max(r.listScroll+delta, 0), max(len(r.files)-g.paneH, 0))
			return
		}
	}
	if m.ReviewCompareShown() || r.editor != nil || !g.reviewLaidOut() {
		m.ReviewWheel(delta)
		return
	}
	m.reviewScrollTo(r.scroll + delta)
}

// ReviewMouseWheelX scrolls the code sideways by delta cells.
func (m *OS) ReviewMouseWheelX(delta int) {
	if m.ReviewCompareShown() || m.review.editor != nil {
		return
	}
	m.review.listFocus = false
	m.ReviewScrollX(delta)
}

// ReviewMouseDrag follows the pointer while a press on the rule or the
// scroll bar is held. It reports whether one is.
func (m *OS) ReviewMouseDrag(x, y int) bool {
	r := &m.review
	switch r.drag {
	case reviewDragRule:
		g := m.reviewGeometry()
		inner := g.w - 2
		r.listWidth = min(max(x-3, reviewListMin), max(inner-5-reviewDiffMin, reviewListMin))
		return true
	case reviewDragBar:
		m.reviewBarMove(y - reviewPaneTop - r.barGrab)
		m.reviewMoved()
		return true
	}
	return false
}

// ReviewMouseRelease ends a drag. It reports whether one was going on.
func (m *OS) ReviewMouseRelease() bool {
	r := &m.review
	held := r.drag != reviewDragNone
	r.drag = reviewDragNone
	return held
}

// reviewBarPress starts a drag of the scroll bar from pane row row. A press
// on the track first moves the thumb's middle there.
func (m *OS) reviewBarPress(row int) {
	r := &m.review
	b := r.bar
	if row < b.thumbTop || row >= b.thumbTop+b.thumbLen {
		r.barGrab = b.thumbLen / 2
		m.reviewBarMove(row - r.barGrab)
	} else {
		r.barGrab = row - b.thumbTop
	}
	r.drag = reviewDragBar
}

// reviewBarMove scrolls the diff so the thumb starts on pane row top, and
// keeps the cursor on a row in view.
func (m *OS) reviewBarMove(top int) {
	r := &m.review
	b := r.bar
	if !b.shown || b.total <= b.rows {
		return
	}
	s := m.reviewBarSettings()
	hidden := b.total - b.rows
	perRow, _, _ := scrollbarTravel(b.rows, hidden, s)
	offset := scrollbarOffsetForStart(b.rows, hidden, top*perRow, s)
	m.reviewScrollTo(hidden - offset)
}

// reviewScrollTo shows the diff from row top, and moves the cursor onto a
// row in view, since the frame keeps the cursor's row in view.
func (m *OS) reviewScrollTo(top int) {
	r := &m.review
	rows := m.reviewRows(m.reviewRowsWidth())
	page := max(r.pageRows, 1)
	if len(rows) == 0 {
		return
	}
	r.listFocus = false
	r.scroll = min(max(top, 0), max(len(rows)-page, 0))
	if r.cursor < r.scroll {
		r.cursor = reviewStep(rows, r.scroll, 0)
		// A note's later row moves back to the note's first, above the view.
		if r.cursor < r.scroll {
			r.cursor = reviewStep(rows, r.cursor, 1)
		}
	}
	if last := min(r.scroll+page-1, len(rows)-1); r.cursor > last {
		r.cursor = reviewNoteStart(rows, last)
	}
}

// reviewBarCells draws the diff's scroll bar, one cell per pane row, for
// total rows of which the panes show rows from r.scroll, and records it for
// the mouse. Blank cells when every row is in view.
func (m *OS) reviewBarCells(x, total, rows int, pal overlay.Palette) []string {
	r := &m.review
	out := make([]string, rows)
	blank := reviewSpaces(1, pal.Surface)
	r.bar = reviewScrollBar{}
	if total <= rows || rows <= 0 {
		for i := range out {
			out[i] = blank
		}
		return out
	}
	s := m.reviewBarSettings()
	hidden := total - rows
	glyphs, thumbTop, thumbLen := scrollbarRows(rows, hidden, hidden-min(r.scroll, hidden), s)
	thumb := scrollbarQuietInk(pal.Surface, scrollbarQuietThumbContrast)
	track := scrollbarQuietInk(pal.Surface, scrollbarQuietTrackContrast)
	for i, gl := range glyphs {
		fg := track
		if i >= thumbTop && i < thumbTop+thumbLen {
			fg = thumb
		}
		if gl == "" {
			gl = " "
		}
		out[i] = reviewInk(gl, fg, pal.Surface)
	}
	r.bar = reviewScrollBar{shown: true, x: x, top: reviewPaneTop, rows: rows, thumbTop: thumbTop, thumbLen: thumbLen, total: total}
	return out
}

// reviewBarSettings is the scroll bar's look, the panes' own.
func (m *OS) reviewBarSettings() *config.Settings { return &m.Settings }

// ReviewHintKeyAt is the key of the footer hint at x, y, empty when there is
// none. A click there is that key: the input layer runs it through the
// review's own keys, so a click does what the hint says the key does.
func (m *OS) ReviewHintKeyAt(x, y int) string {
	r := &m.review
	if y != r.hintRow {
		return ""
	}
	for _, s := range r.hintHits {
		if x >= s.x0 && x < s.x1 {
			return s.key
		}
	}
	return ""
}

// reviewHintKey is the key a hint stands for, as the review's keys read it.
func reviewHintKey(h overlay.Hint) string {
	if h.Key == overlay.EnterGlyph {
		return "enter"
	}
	return h.Key
}
