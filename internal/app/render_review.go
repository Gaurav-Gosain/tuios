package app

import (
	"image/color"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/diffview"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/charmbracelet/x/ansi"
)

// Drawing the review overlay: one frame over the whole screen, a header line,
// the file list beside the diff of the file under it, and a footer of the keys
// that do something where the cursor is. Only the rows on screen are drawn.
//
// Everything in the diff and in a note is someone else's text: the
// repository's, or whoever wrote the note. It is drawn through printableRune,
// so a control character in a file cannot move the cursor or restyle the
// screen, and tabs are laid out as spaces.
//
// The diff itself is drawn by internal/diffview: line numbers, the code
// coloured by its file type, added and removed lines on tinted grounds and
// the changed part of a changed line marked, one column or the two sides
// next to each other (s). Every cell of the overlay carries a background, so
// nothing of the desktop or the host terminal shows through it. See
// review_look.go for where the colours come from.

// reviewMinW and reviewMinH are the smallest screen the overlay lays out on.
// On a smaller one it says so and esc still closes it.
const (
	reviewMinW = 48
	reviewMinH = 10
)

// reviewListMin is the file list's narrowest width, and reviewListShare the
// largest share of the frame it takes, in percent.
const (
	reviewListMin   = 16
	reviewListShare = 35
)

// reviewRowKind is what one row of the diff column is.
type reviewRowKind int

const (
	// reviewRowInfo is a line of words: a placeholder for a file with no
	// text to show, or the empty diff.
	reviewRowInfo reviewRowKind = iota
	reviewRowHunk
	reviewRowLine
	reviewRowNote
	reviewRowEditor
)

// reviewRow is one row of the diff column.
type reviewRow struct {
	kind reviewRowKind
	// hunk and line locate a hunk or line row in the file; hunk is also the
	// hunk a note row sits in, -1 outside every hunk. In the split layout a
	// row can hold two lines: line is the new side's when it has one, and
	// other the removed line beside it, -1 when there is none.
	hunk, line, other int
	// note is the index in reviewState.notes of a note row, -1 otherwise.
	note int
	// text is an info row's words, or one wrapped line of a note.
	text string
	// first marks a note's first row.
	first bool
}

// reviewSeg is a run of text in one colour.
type reviewSeg struct {
	fg   color.Color
	text string
	bold bool
}

// reviewPaint draws segments into exactly width cells over bg (nil for the
// terminal's own), cutting the last one with an ellipsis when they do not fit.
func reviewPaint(segs []reviewSeg, width int, bg color.Color) string {
	if width <= 0 {
		return ""
	}
	style := func(fg color.Color, bold bool) lipgloss.Style {
		s := lipgloss.NewStyle().Foreground(fg).Bold(bold)
		if bg != nil {
			s = s.Background(bg)
		}
		return s
	}
	var b strings.Builder
	used := 0
	for _, sg := range segs {
		if sg.text == "" {
			continue
		}
		w := ansi.StringWidth(sg.text)
		if used+w > width {
			ell := overlay.Ellipsis()
			room := width - used
			text := ansi.Truncate(sg.text, room, ell)
			if ansi.StringWidth(ell) >= room {
				text = ansi.Truncate(sg.text, room, "")
			}
			b.WriteString(style(sg.fg, sg.bold).Render(text))
			used += ansi.StringWidth(text)
			break
		}
		b.WriteString(style(sg.fg, sg.bold).Render(sg.text))
		used += w
	}
	if used < width {
		b.WriteString(style(nil, false).Render(strings.Repeat(" ", width-used)))
	}
	return b.String()
}

// reviewSpaces is n blank cells on bg.
func reviewSpaces(n int, bg color.Color) string {
	if n <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", n))
}

// reviewInk is text in fg on bg.
func reviewInk(text string, fg, bg color.Color) string {
	return lipgloss.NewStyle().Foreground(fg).Background(bg).Render(text)
}

// reviewText is a line of a file or a note as the overlay draws it: tabs as
// spaces, and nothing that is not printable.
func reviewText(s string) string {
	s = strings.ReplaceAll(s, "\t", "    ")
	ascii := overlay.UseASCII()
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if printableRune(r, ascii) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// reviewBar is the mark down the left of a note.
func reviewBar() string {
	if overlay.UseASCII() {
		return ">"
	}
	return "▌"
}

// reviewLayout is how the screen is split: the frame's size, the file list's
// width, the diff column's width and how many rows the panes have.
func (m *OS) reviewLayout() (w, h, listW, diffW, paneH int) {
	w, h = m.GetRenderWidth(), m.GetRenderHeight()
	inner := w - 2
	widest := 0
	for _, e := range m.review.files {
		if n := len(e.path) + 4 + len(reviewFileCounts(m.review, e)); n > widest {
			widest = n
		}
	}
	listW = min(max(widest, reviewListMin), inner*reviewListShare/100)
	// A space either side of the list and of the diff, and the rule between.
	diffW = inner - listW - 5
	paneH = h - 6
	return
}

// reviewRowsWidth is the diff column's width, which the keys lay the rows out
// at so they move over what is on screen.
func (m *OS) reviewRowsWidth() int {
	_, _, _, diffW, _ := m.reviewLayout()
	return max(diffW, 1)
}

// reviewFileCounts is the right side of a file list row.
func reviewFileCounts(r reviewState, e reviewFileEntry) string {
	if e.file == nil {
		n := 0
		for _, note := range r.notes {
			if note.Path == e.path {
				n++
			}
		}
		return reviewCount(n, "note")
	}
	if e.file.Binary {
		return "binary"
	}
	return "+" + strconv.Itoa(e.file.Added) + " -" + strconv.Itoa(e.file.Removed)
}

// reviewAuthor names who wrote a note, for a note not written by the person.
func reviewAuthor(by string) string {
	switch {
	case by == "shell":
		return "a script"
	case strings.HasPrefix(by, "link:"):
		return reviewText(strings.TrimPrefix(by, "link:"))
	default:
		return "pane " + shortWindowLabel(reviewText(by))
	}
}

// reviewNoteLines is a note laid out in width cells: what it is on and who
// wrote it, then its text, wrapped.
func reviewNoteLines(n review.Note, placed bool, width int, now time.Time) []string {
	label := "note"
	switch {
	case n.Outdated:
		label = "outdated note, line " + strconv.Itoa(n.Line)
		if n.Quote != "" {
			label += " \"" + reviewText(n.Quote) + "\""
		}
	case n.IsHunk():
		label = "note (hunk)"
		if !placed {
			label += " " + reviewText(n.HunkHeader)
		}
	case !placed:
		label = "note, line " + strconv.Itoa(n.Line)
	}
	if n.By != "" && n.By != "human" {
		label += " from " + reviewAuthor(n.By)
	}
	text := label + ": " + reviewText(strings.ReplaceAll(n.Text, "\n", " "))
	if n.SentAt > 0 {
		text += "  sent " + inboxWait(n.SentAt, now)
	}
	width = max(width, 8)
	return strings.Split(ansi.Wrap(text, width, ""), "\n")
}

// reviewNumW is the width of a line number in a file's diff: its largest
// number's, and never less than three.
func reviewNumW(f *review.File) int {
	numW := 3
	if f == nil {
		return numW
	}
	for _, hk := range f.Hunks {
		numW = max(numW, len(strconv.Itoa(hk.OldStart+hk.OldLines)), len(strconv.Itoa(hk.NewStart+hk.NewLines)))
	}
	return numW
}

// reviewSplitMinCode is the fewest cells of code each side gets in the split
// layout. Narrower than that, the two sides would show too little of a line
// to compare, and the diff stays in one column.
const reviewSplitMinCode = 24

// reviewSplitFits reports whether a file's two sides fit next to each other
// in width cells: the cursor mark, then on each side a line number, the
// sign and the code, with a rule between the sides.
func reviewSplitFits(f *review.File, width int) bool {
	if f == nil || len(f.Hunks) == 0 {
		return false
	}
	return width >= 2+2*(reviewNumW(f)+1+diffview.SignWidth+reviewSplitMinCode)
}

// reviewSplitOn reports whether the current file is drawn side by side at
// width cells: asked for, and wide enough.
func (m *OS) reviewSplitOn(width int) bool {
	e := m.review.currentFile()
	return m.review.split && e != nil && reviewSplitFits(e.file, width)
}

// reviewNoteIndent is how many cells come before a note's bar: the cursor
// mark and the line number columns, so a note sits under the code of the
// line it is on.
func reviewNoteIndent(f *review.File, split bool) int {
	if f == nil || len(f.Hunks) == 0 {
		return 1
	}
	if split {
		return 1 + reviewNumW(f) + 1
	}
	return 1 + 2*(reviewNumW(f)+1)
}

// reviewRows lays out the current file's diff column at width cells: notes
// the diff no longer holds first, then each hunk with its lines, a note on a
// line under that line, and a note on a hunk under its last line. The note
// editor takes the place of the note it edits, or sits where the new note
// will. In the split layout a removed line and the added line that replaced
// it share a row, and the notes on either sit under it.
func (m *OS) reviewRows(width int) []reviewRow {
	r := &m.review
	e := r.currentFile()
	if e == nil {
		return nil
	}
	now := time.Now()
	split := m.reviewSplitOn(width)
	noteW := width - reviewNoteIndent(e.file, split) - 2
	ed := r.editor
	if ed != nil && ed.kind != reviewEditNote {
		ed = nil
	}

	type key struct{ hunk, line int }
	atLine := map[key][]int{}
	atHunk := map[int][]int{}
	var loose []int
	for _, i := range r.notesOn(e.path) {
		n := r.notes[i]
		h, l := -1, -1
		if !n.Outdated && e.file != nil {
			h, l = reviewPlace(e.file, n)
		}
		switch {
		case h < 0:
			loose = append(loose, i)
		case l < 0:
			atHunk[h] = append(atHunk[h], i)
		default:
			atLine[key{h, l}] = append(atLine[key{h, l}], i)
		}
	}

	var rows []reviewRow
	addNote := func(i, hunk int, placed bool) {
		n := r.notes[i]
		if ed != nil && ed.noteID == n.ID {
			rows = append(rows, reviewRow{kind: reviewRowEditor, hunk: hunk, line: -1, other: -1, note: i})
			return
		}
		for j, text := range reviewNoteLines(n, placed, noteW, now) {
			rows = append(rows, reviewRow{kind: reviewRowNote, hunk: hunk, line: -1, other: -1, note: i, text: text, first: j == 0})
		}
	}
	info := func(text string) {
		rows = append(rows, reviewRow{kind: reviewRowInfo, hunk: -1, line: -1, other: -1, note: -1, text: text})
	}
	switch {
	case e.file == nil:
		info("No longer changed. Its notes are kept until you resolve them with x.")
	case e.file.Binary:
		info("Binary file, " + reviewFileCounts(*r, *e) + ". No text is shown.")
	case e.file.Truncated:
		info("Too large to show here: " + reviewFileCounts(*r, *e) + " lines. tuios review --json reads it.")
	case len(e.file.Hunks) == 0:
		info("No lines changed (" + reviewStatusWord(e.file.Status) + ").")
	}
	for _, i := range loose {
		addNote(i, -1, false)
	}
	if e.file == nil {
		return rows
	}
	// line adds the row of one line, or of a pair of lines side by side,
	// with the notes on each and the editor of a new note on either.
	line := func(h, l, other int) {
		rows = append(rows, reviewRow{kind: reviewRowLine, hunk: h, line: l, other: other, note: -1})
		for _, at := range []int{other, l} {
			if at < 0 {
				continue
			}
			for _, i := range atLine[key{h, at}] {
				addNote(i, h, true)
			}
		}
		if ed != nil && ed.noteID == "" && ed.hunk == "" && ed.hunkIdx == h && (ed.lineIdx == l || other >= 0 && ed.lineIdx == other) {
			rows = append(rows, reviewRow{kind: reviewRowEditor, hunk: h, line: ed.lineIdx, other: -1, note: -1})
		}
	}
	for h, hunk := range e.file.Hunks {
		rows = append(rows, reviewRow{kind: reviewRowHunk, hunk: h, line: -1, other: -1, note: -1})
		if split {
			for _, p := range r.reviewHunk(e.file, h, -1).pairs {
				switch {
				case p.Right < 0:
					line(h, p.Left, -1)
				case p.Left >= 0 && p.Left != p.Right:
					line(h, p.Right, p.Left)
				default:
					line(h, p.Right, -1)
				}
			}
		} else {
			for l := range hunk.Lines {
				line(h, l, -1)
			}
		}
		for _, i := range atHunk[h] {
			addNote(i, h, true)
		}
		if ed != nil && ed.noteID == "" && ed.hunk != "" && ed.hunkIdx == h {
			rows = append(rows, reviewRow{kind: reviewRowEditor, hunk: h, line: -1, other: -1, note: -1})
		}
	}
	return rows
}

// reviewStatusWord says a file's status in words.
func reviewStatusWord(status string) string {
	switch status {
	case review.StatusAdded:
		return "added"
	case review.StatusDeleted:
		return "deleted"
	case review.StatusRenamed:
		return "renamed"
	case review.StatusUntracked:
		return "new, not added to git"
	}
	return "modified"
}

// reviewPlace finds where a note sits in a file's diff: the hunk, and the
// line in it, -1 for a note on the whole hunk. hunk is -1 when the diff does
// not hold the note's line.
func reviewPlace(f *review.File, n review.Note) (hunk, line int) {
	if n.IsHunk() {
		for h, hk := range f.Hunks {
			if hk.Header == n.HunkHeader {
				return h, -1
			}
		}
		for h, hk := range f.Hunks {
			start, count := hk.NewStart, hk.NewLines
			if n.Side == review.SideOld {
				start, count = hk.OldStart, hk.OldLines
			}
			if n.Line >= start && n.Line < start+max(count, 1) {
				return h, -1
			}
		}
		return -1, -1
	}
	for h, hk := range f.Hunks {
		for l, ln := range hk.Lines {
			if n.Side == review.SideOld {
				if ln.Op != review.OpAdd && ln.Old == n.Line {
					return h, l
				}
			} else if ln.Op != review.OpDelete && ln.New == n.Line {
				return h, l
			}
		}
	}
	return -1, -1
}

// reviewFrame draws a frame of w by h cells around body, which holds h-2
// rows of w-2 cells, on bg. A title goes into the top edge.
func reviewFrame(w, h int, title string, body []string, border, bg color.Color) string {
	tl, tr, bl, br, hz, vt := "╭", "╮", "╰", "╯", "─", "│"
	lj, rj := "├", "┤"
	if overlay.UseASCII() {
		tl, tr, bl, br, hz, vt = "+", "+", "+", "+", "-", "|"
		lj, rj = "+", "+"
	}
	edge := lipgloss.NewStyle().Foreground(border).Background(bg)
	inner := w - 2
	top := strings.Repeat(hz, inner)
	if title != "" {
		t := " " + ansi.Truncate(title, max(inner-3, 0), "") + " "
		top = t + strings.Repeat(hz, max(inner-ansi.StringWidth(t), 0))
		top = edge.Render(tl) + edge.Bold(true).Render(ansi.Truncate(top, inner, "")) + edge.Render(tr)
	} else {
		top = edge.Render(tl + top + tr)
	}
	lines := make([]string, 0, h)
	lines = append(lines, top)
	blank := reviewSpaces(inner, bg)
	for i := 0; i < h-2; i++ {
		row := blank
		if i < len(body) {
			row = body[i]
		}
		// A rule across the whole frame meets the sides, rather than stopping
		// half a cell short of them on each end.
		l, r := vt, vt
		if inner > 0 && ansi.Strip(row) == strings.Repeat(hz, inner) {
			l, r = lj, rj
		}
		lines = append(lines, edge.Render(l)+row+edge.Render(r))
	}
	lines = append(lines, edge.Render(bl+strings.Repeat(hz, inner)+br))
	return strings.Join(lines, "\n")
}

// reviewHintStrip draws key hints the way every footer does, keys bright and
// labels muted, on the overlay's ground like the rest of the frame.
func reviewHintStrip(hints []overlay.Hint, pal overlay.Palette) string {
	key := lipgloss.NewStyle().Foreground(pal.AccentBright).Background(pal.Surface).Bold(true)
	label := lipgloss.NewStyle().Foreground(pal.FgDim).Background(pal.Surface)
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		k := h.Key
		if k == overlay.EnterGlyph {
			k = overlay.EnterKey()
		}
		parts = append(parts, key.Render(k)+label.Render(" "+h.Label))
	}
	return strings.Join(parts, reviewSpaces(2, pal.Surface))
}

// reviewHints draws key hints in width cells: as many as fit, in order, with
// the last one always shown.
func reviewHints(hints []overlay.Hint, width int, pal overlay.Palette) string {
	if len(hints) == 0 {
		return reviewSpaces(width, pal.Surface)
	}
	last := hints[len(hints)-1]
	room := width - 1 - ansi.StringWidth(reviewHintStrip([]overlay.Hint{last}, pal))
	var fit []overlay.Hint
	for _, h := range hints[:len(hints)-1] {
		try := append(append([]overlay.Hint{}, fit...), h)
		if ansi.StringWidth(reviewHintStrip(try, pal))+2 > room {
			continue
		}
		fit = try
	}
	strip := reviewSpaces(1, pal.Surface) + reviewHintStrip(append(fit, last), pal)
	return strip + reviewSpaces(width-ansi.StringWidth(strip), pal.Surface)
}

// renderReview draws the review overlay over the whole screen, or the
// compare view when it shows. Empty when the overlay is closed.
func (m *OS) renderReview() string {
	r := &m.review
	if !r.open {
		return ""
	}
	look := m.reviewLook()
	pal := look.pal
	w, h, listW, diffW, paneH := m.reviewLayout()
	if w < reviewMinW || h < reviewMinH || diffW < 20 {
		msg := "The screen is too small for the review. esc closes it."
		body := []string{reviewPaint([]reviewSeg{{pal.FgDim, " " + msg, false}}, max(w-2, 0), pal.Surface)}
		return reviewFrame(max(w, 4), max(h, 3), "", body, pal.Accent, pal.Surface)
	}
	if m.ReviewCompareShown() {
		return m.renderReviewCompare(w, h, pal)
	}
	inner := w - 2
	rule := reviewInk(strings.Repeat(hzGlyph(), inner), pal.FgMute, pal.Surface)
	body := make([]string, 0, h-2)
	body = append(body, reviewPaint(m.reviewHeader(pal), inner, pal.Surface), rule)

	listLines := m.reviewListLines(listW, paneH, pal)
	diffLines := m.reviewDiffLines(diffW, paneH, look)
	sp := reviewSpaces(1, pal.Surface)
	sep := sp + reviewInk(vtGlyph(), pal.FgMute, pal.Surface) + sp
	for i := range paneH {
		body = append(body, sp+listLines[i]+sep+diffLines[i]+sp)
	}
	body = append(body, m.reviewStatusRule(inner, pal), m.reviewFooter(inner, pal))
	return reviewFrame(w, h, "", body, pal.Accent, pal.Surface)
}

// reviewStatusRule is the rule above the footer, or in its place the last
// message the review raised while it is still up: the overlay covers the dock,
// and "queued", "kept" or a refusal would otherwise go unread until it closed.
// Messages from anything else stay the dock's. See reviewState.statusID.
func (m *OS) reviewStatusRule(width int, pal overlay.Palette) string {
	for i := len(m.Notifications) - 1; i >= 0 && m.review.statusID != ""; i-- {
		last := m.Notifications[i]
		if last.ID == m.review.statusID && !last.StartTime.Before(m.review.openedAt) && last.Message != "" {
			fg := pal.FgDim
			switch last.Type {
			case "error":
				fg = pal.Warn
			case "warning", "warn":
				fg = pal.Warning
			}
			return reviewPaint([]reviewSeg{{fg, " " + reviewText(last.Message), false}}, width, pal.Surface)
		}
	}
	return reviewInk(strings.Repeat(hzGlyph(), width), pal.FgMute, pal.Surface)
}

// hzGlyph and vtGlyph are the rule glyphs, ASCII where the glyphs are.
func hzGlyph() string {
	if overlay.UseASCII() {
		return "-"
	}
	return "─"
}

func vtGlyph() string {
	if overlay.UseASCII() {
		return "|"
	}
	return "│"
}

// reviewHeader is the top line: whose changes, how many, against what, and
// how many notes.
func (m *OS) reviewHeader(pal overlay.Palette) []reviewSeg {
	r := &m.review
	d := r.diff
	// The session, then the pane when it is named otherwise: "api-2  claude".
	name, pane := r.who, ""
	if d != nil && d.Session != "" {
		name = d.Session
		if who := printableTitle(r.who); who != "" && who != d.Session {
			pane = who
		}
	}
	segs := []reviewSeg{{pal.AccentBright, " Review", true}, {pal.Fg, "  " + reviewText(name), true}}
	dim := func(s string) { segs = append(segs, reviewSeg{pal.FgDim, "  " + s, false}) }
	if pane != "" {
		dim(reviewText(pane))
	}
	switch {
	case d == nil && r.loadErr != "":
		dim(r.loadErr)
		return segs
	case d == nil:
		dim("reading the changes")
		return segs
	}
	dim(reviewCount(d.Totals.Files, "file"))
	segs = append(segs, reviewSeg{pal.Success, "  +" + strconv.Itoa(d.Totals.Added), false}, reviewSeg{pal.Warn, " -" + strconv.Itoa(d.Totals.Removed), false})
	switch {
	case d.Against != "":
		dim("against " + reviewText(d.Against))
	case d.Uncommitted:
		dim("uncommitted only")
	default:
		dim("vs " + reviewText(d.Base))
	}
	if d.Against == "" {
		dim(reviewCount(len(r.notes), "note"))
	}
	if d.Truncated {
		dim("cut at the limits")
	}
	if r.loading {
		dim("reading")
	}
	return segs
}

// reviewListLines draws the file list, paneH rows of width cells.
func (m *OS) reviewListLines(width, paneH int, pal overlay.Palette) []string {
	r := &m.review
	out := make([]string, paneH)
	if r.file < r.listScroll {
		r.listScroll = r.file
	}
	if r.file >= r.listScroll+paneH {
		r.listScroll = r.file - paneH + 1
	}
	for i := range paneH {
		idx := r.listScroll + i
		if idx >= len(r.files) {
			out[i] = reviewSpaces(width, pal.Surface)
			continue
		}
		e := r.files[idx]
		status := "-"
		if e.file != nil {
			status = e.file.Status
		}
		counts := reviewFileCounts(*r, e)
		mark := " "
		bg := pal.Surface
		fg := pal.FgDim
		if idx == r.file {
			fg = pal.Fg
			if r.listFocus {
				mark, bg = overlay.SigilMark(), pal.RowSel
			}
		}
		pathW := max(width-4-ansi.StringWidth(counts), 1)
		path := reviewText(e.path)
		if ansi.StringWidth(path) > pathW {
			// The end of a path is the part that tells files apart.
			ell := overlay.Ellipsis()
			path = ell + ansi.TruncateLeft(path, ansi.StringWidth(path)-pathW+ansi.StringWidth(ell), "")
		}
		pad := max(width-3-ansi.StringWidth(path)-ansi.StringWidth(counts), 1)
		out[i] = reviewPaint([]reviewSeg{
			{pal.AccentBright, mark, false},
			{reviewStatusColor(status, pal), status + " ", false},
			{fg, path + strings.Repeat(" ", pad), idx == r.file},
			{pal.FgMute, counts, false},
		}, width, bg)
	}
	return out
}

// reviewStatusColor colours a file's status letter.
func reviewStatusColor(status string, pal overlay.Palette) color.Color {
	switch status {
	case review.StatusAdded, review.StatusUntracked:
		return pal.Success
	case review.StatusDeleted:
		return pal.Warn
	}
	return pal.Info
}

// reviewDiffLines draws the visible rows of the diff column, keeping the
// cursor (and an open note editor) on screen. Only these rows are drawn,
// and only their hunks are tokenised.
func (m *OS) reviewDiffLines(width, paneH int, look *reviewLook) []string {
	r := &m.review
	pal := look.pal
	out := make([]string, paneH)
	r.rowsWidth, r.pageRows = width, paneH
	var rows []reviewRow
	if r.diff != nil {
		rows = m.reviewRows(width)
	}
	if r.cursor >= len(rows) {
		r.cursor = max(len(rows)-1, 0)
	}
	r.cursor = reviewNoteStart(rows, r.cursor)
	// A note wrapped onto several rows is selected whole.
	selNote := -1
	if r.cursor < len(rows) && rows[r.cursor].kind == reviewRowNote {
		selNote = rows[r.cursor].note
	}
	focus := r.cursor
	if r.editor != nil && r.editor.kind == reviewEditNote {
		for i, row := range rows {
			if row.kind == reviewRowEditor {
				focus = i
			}
		}
	}
	if focus < r.scroll {
		r.scroll = focus
	}
	// The notes on the cursor's line come into view with it, so a cursor
	// moved down onto a noted line shows the note, not only the line.
	end := focus
	for end+1 < len(rows) && rows[end+1].kind == reviewRowNote && end+1-focus < paneH {
		end++
	}
	if end >= r.scroll+paneH {
		r.scroll = min(end-paneH+1, focus)
	}
	r.scroll = max(min(r.scroll, len(rows)-paneH), 0)

	var file *review.File
	if e := r.currentFile(); e != nil {
		file = e.file
		if e.path != r.xPath {
			r.xPath, r.xOff = e.path, 0
		}
	}
	d := reviewDraw{m: m, look: look, file: file, width: width, numW: reviewNumW(file), split: m.reviewSplitOn(width)}
	for i := range paneH {
		idx := r.scroll + i
		switch {
		case r.diff == nil && i == 0:
			text := "Reading the changes."
			if r.loadErr != "" {
				text = r.loadErr
			}
			out[i] = reviewPaint([]reviewSeg{{pal.FgDim, " " + text, false}}, width, pal.Surface)
			continue
		case r.diff != nil && len(r.files) == 0 && i == 0:
			text := "No changes against " + reviewText(r.diff.Base) + "."
			if r.diff.Against != "" {
				text = "No difference between the two attempts."
			} else if r.diff.Uncommitted {
				text = "No uncommitted changes. u shows the changes since the base."
			}
			out[i] = reviewPaint([]reviewSeg{{pal.FgDim, " " + text, false}}, width, pal.Surface)
			continue
		case idx >= len(rows):
			out[i] = reviewSpaces(width, pal.Surface)
			continue
		}
		sel := idx == r.cursor || (selNote >= 0 && rows[idx].kind == reviewRowNote && rows[idx].note == selNote)
		out[i] = d.row(rows[idx], sel && !r.listFocus)
	}
	return out
}

// reviewDraw draws rows of one file's diff column.
type reviewDraw struct {
	m     *OS
	look  *reviewLook
	file  *review.File
	width int
	numW  int
	split bool
}

// row draws one diff row in d.width cells.
func (d reviewDraw) row(row reviewRow, cursor bool) string {
	pal, dv := d.look.pal, d.look.dv
	mark := " "
	if cursor && (row.kind != reviewRowNote || row.first) {
		mark = overlay.SigilMark()
	}
	// markCell is the first cell, which holds the cursor's mark on the
	// ground of the column it starts.
	markCell := func(bg color.Color) string {
		return reviewInk(mark, overlay.Readable(pal.AccentBright, bg), bg)
	}
	rowBg := func(bg color.Color) color.Color {
		if cursor {
			return pal.RowSel
		}
		return bg
	}
	rest := d.width - 1
	switch row.kind {
	case reviewRowInfo:
		bg := rowBg(pal.Surface)
		return markCell(bg) + reviewPaint([]reviewSeg{{pal.FgDim, " " + row.text, false}}, rest, bg)
	case reviewRowHunk:
		bg := d.look.hunkBg
		if cursor {
			bg = overlay.MixColors(bg, pal.Accent, 0.24)
		}
		head, ctx := reviewSplitHeader(reviewText(d.file.Hunks[row.hunk].Header))
		return markCell(bg) + reviewPaint([]reviewSeg{
			{overlay.Readable(pal.Info, bg), " " + head, false},
			{overlay.Readable(pal.FgDim, bg), ctx, false},
		}, rest, bg)
	case reviewRowLine:
		if d.split {
			return d.splitLine(row, cursor, markCell)
		}
		return d.unifiedLine(row, cursor, markCell)
	case reviewRowNote, reviewRowEditor:
		indent := reviewNoteIndent(d.file, d.split)
		bg := d.look.noteBg
		if cursor {
			bg = overlay.MixColors(bg, pal.Accent, 0.24)
		}
		lead := markCell(dv.GutterBg(diffview.Context, cursor)) + dv.BlankGutter(diffview.Context, cursor, indent-1)
		if d.file == nil || len(d.file.Hunks) == 0 {
			lead = markCell(bg)
		}
		room := d.width - indent
		if row.kind == reviewRowNote {
			n := d.m.review.notes[row.note]
			fg := pal.Warning
			if n.SentAt > 0 {
				fg = pal.FgMute
			}
			if n.Outdated {
				fg = pal.FgDim
			}
			fg = overlay.Readable(fg, bg)
			return lead + reviewPaint([]reviewSeg{{fg, reviewBar() + " ", false}, {fg, row.text, false}}, room, bg)
		}
		ed := d.m.review.editor
		label := "note: "
		if ed != nil && ed.noteID == "" && ed.hunk != "" {
			label = "note (hunk): "
		}
		draft := ""
		if ed != nil {
			draft = reviewText(ed.draft)
		}
		// The end of what is typed stays in view on a long note.
		space := room - 2 - ansi.StringWidth(label) - 1
		if ansi.StringWidth(draft) > space && space > 0 {
			draft = ansi.TruncateLeft(draft, ansi.StringWidth(draft)-space, "")
		}
		return lead + reviewPaint([]reviewSeg{
			{overlay.Readable(pal.AccentBright, bg), reviewBar() + " " + label, true},
			{overlay.Readable(pal.Fg, bg), draft + "_", false},
		}, room, bg)
	}
	return reviewSpaces(d.width, pal.Surface)
}

// reviewSplitHeader splits a hunk header into its ranges, "@@ -1,3 +1,4 @@",
// and the context git wrote after them.
func reviewSplitHeader(h string) (string, string) {
	if strings.HasPrefix(h, "@@") {
		if i := strings.Index(h[2:], "@@"); i >= 0 {
			return h[:i+4], h[i+4:]
		}
	}
	return h, ""
}

// lineKind is the diff kind of a line.
func lineKind(ln review.Line) diffview.Kind {
	switch ln.Op {
	case review.OpAdd:
		return diffview.Add
	case review.OpDelete:
		return diffview.Delete
	}
	return diffview.Context
}

// code draws line l of hunk h in width cells, coloured.
func (d reviewDraw) code(h, l int, kind diffview.Kind, cursor bool, width int) string {
	hl := d.m.review.reviewHunk(d.file, h, l)
	text, spans := hl.text[l], hl.spans[l]
	if d.file.Hunks[h].Lines[l].NoNewline {
		end := len(text)
		text += "  (no newline at end)"
		spans = append(spans[:len(spans):len(spans)], diffview.Span{Start: end, End: len(text), Class: diffview.Meta})
	}
	return d.look.dv.Code(text, spans, hl.changed[l], kind, cursor, d.m.review.xOff, width)
}

// scrolled reports whether line l of hunk h has code to the left of what
// the sideways scroll shows.
func (d reviewDraw) scrolled(h, l int) bool {
	x := d.m.review.xOff
	if x <= 0 {
		return false
	}
	text := d.m.review.reviewHunk(d.file, h, -1).text[l]
	return strings.TrimSpace(ansi.Truncate(text, x, "")) != ""
}

// unifiedLine draws a line row in one column: both line numbers, the sign,
// and the code.
func (d reviewDraw) unifiedLine(row reviewRow, cursor bool, markCell func(color.Color) string) string {
	dv := d.look.dv
	ln := d.file.Hunks[row.hunk].Lines[row.line]
	kind := lineKind(ln)
	gw := d.numW + 1
	codeW := d.width - 1 - 2*gw - diffview.SignWidth
	return markCell(dv.GutterBg(kind, cursor)) +
		dv.Gutter(ln.Old, gw, kind, cursor) + dv.Gutter(ln.New, gw, kind, cursor) +
		dv.Sign(kind, cursor, d.scrolled(row.hunk, row.line)) + d.code(row.hunk, row.line, kind, cursor, codeW)
}

// splitLine draws a line row as its two sides: the old line on the left, the
// new on the right, and an empty side where a line has no counterpart.
func (d reviewDraw) splitLine(row reviewRow, cursor bool, markCell func(color.Color) string) string {
	dv, pal := d.look.dv, d.look.pal
	lines := d.file.Hunks[row.hunk].Lines
	left, right := -1, -1
	switch lines[row.line].Op {
	case review.OpDelete:
		left = row.line
	case review.OpContext:
		left, right = row.line, row.line
	default:
		left, right = row.other, row.line
	}
	gw := d.numW + 1
	avail := d.width - 2 - 2*(gw+diffview.SignWidth)
	leftW := avail / 2
	side := func(l int, num func(review.Line) int, w int) string {
		if l < 0 {
			return dv.BlankGutter(diffview.Missing, cursor, gw) + dv.Sign(diffview.Missing, cursor, false) + dv.Blank(diffview.Missing, cursor, w)
		}
		ln := lines[l]
		kind := lineKind(ln)
		return dv.Gutter(num(ln), gw, kind, cursor) + dv.Sign(kind, cursor, d.scrolled(row.hunk, l)) + d.code(row.hunk, l, kind, cursor, w)
	}
	first := diffview.Missing
	if left >= 0 {
		first = lineKind(lines[left])
	}
	rule := reviewInk(vtGlyph(), overlay.Structure(pal.Surface), pal.Surface)
	return markCell(dv.GutterBg(first, cursor)) +
		side(left, func(ln review.Line) int { return ln.Old }, leftW) + rule +
		side(right, func(ln review.Line) int { return ln.New }, avail-leftW)
}

// reviewFooter is the key line under the panes: the editor's line while one
// is open, the keys that do something here otherwise.
func (m *OS) reviewFooter(width int, pal overlay.Palette) string {
	r := &m.review
	if ed := r.editor; ed != nil {
		switch ed.kind {
		case reviewEditBase:
			return m.reviewPromptLine("Base: ", ed.draft, []overlay.Hint{{Key: overlay.EnterGlyph, Label: "diff from it (empty: the default)"}, {Key: "esc", Label: "cancel"}}, width, pal)
		case reviewEditVerify:
			return m.reviewPromptLine("Run in every attempt: ", ed.draft, []overlay.Hint{{Key: overlay.EnterGlyph, Label: "run"}, {Key: "esc", Label: "cancel"}}, width, pal)
		}
		return reviewHints([]overlay.Hint{{Key: overlay.EnterGlyph, Label: "save"}, {Key: "esc", Label: "drop"}}, width, pal)
	}
	return reviewHints(m.reviewKeyHints(), width, pal)
}

// reviewPromptLine draws a one-line prompt with its keys after it.
func (m *OS) reviewPromptLine(label, draft string, hints []overlay.Hint, width int, pal overlay.Palette) string {
	keys := reviewHintStrip(hints, pal)
	room := max(width-2-ansi.StringWidth(label)-1-2-ansi.StringWidth(keys), 4)
	text := reviewText(draft)
	if ansi.StringWidth(text) > room {
		text = ansi.TruncateLeft(text, ansi.StringWidth(text)-room, "")
	}
	line := reviewPaint([]reviewSeg{{pal.AccentBright, " " + label, true}, {pal.Fg, text + "_", false}}, width-ansi.StringWidth(keys)-1, pal.Surface)
	return line + keys + reviewSpaces(1, pal.Surface)
}

// reviewKeyHints are the review's keys that do something where the cursor
// is, most wanted first. The footer shows as many as fit, and always esc.
func (m *OS) reviewKeyHints() []overlay.Hint {
	r := &m.review
	var hints []overlay.Hint
	add := func(key, label string) { hints = append(hints, overlay.Hint{Key: key, Label: label}) }
	against := r.query.Against != ""
	_, onNote := m.reviewNoteUnderCursor()
	if r.listFocus {
		add(overlay.EnterGlyph, "open file")
		add("tab", "diff")
	} else if !against && r.diff != nil {
		if onNote {
			add("e", "edit")
			add("x", "resolve")
		} else {
			add("c", "note")
		}
	}
	if n := r.unsentNotes(); n > 0 && !against {
		add("S", "send "+reviewCount(n, "note"))
	}
	add("]", "next hunk")
	add("}", "next file")
	if e := r.currentFile(); e != nil && reviewSplitFits(e.file, m.reviewRowsWidth()) {
		if m.reviewSplitOn(m.reviewRowsWidth()) {
			add("s", "unified")
		} else {
			add("s", "split")
		}
	}
	if r.fan != nil && !against {
		add("w", "compare")
	}
	if !against {
		if r.query.Uncommitted {
			add("u", "since base")
		} else {
			add("u", "uncommitted only")
		}
		add("b", "base")
		if !onNote && !r.listFocus {
			add("C", "note hunk")
		}
	}
	if !r.listFocus {
		add("tab", "files")
	}
	add("r", "reload")
	if r.backToCompare {
		add("esc", "back")
	} else {
		add("esc", "close")
	}
	return hints
}
