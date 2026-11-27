package app

import (
	"image/color"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/review"
	"github.com/charmbracelet/x/ansi"
)

// Showing the hunks of some authors only. @ opens a menu of the authors in
// the diff on the footer line, each with its hunk count. Space or a click
// shows or hides one, and "everyone" shows them all. An author is an agent
// by its name, or the person ("you"), whatever the turn.
//
// A hunk is shown when any author of its changed lines is, and the lines of
// a hidden author in it are dim. The file list keeps only the files with a
// hunk shown, and the header says what is shown and what is hidden. The
// filter lasts while the review is open, through live reads.
//
// The mouse: the author on a hunk's header and each author in the header
// show that author only (a second click shows everyone again), and a click
// on what is hidden shows everyone.

// reviewFilter is the filter's state.
type reviewFilter struct {
	// hidden are the authors not shown, by name.
	hidden map[string]bool
	// menu is open, with its cursor at at.
	menu bool
	at   int
	// heads are the header's authors and its hidden count, and items the
	// menu's, where the last frame drew them, for the mouse.
	heads, items []reviewWhoSpan
	// chips are where each hunk header's author is in the diff column, by
	// hunk, as [x0, x1) from the column's first cell.
	chips map[int][2]int
}

// reviewWhoSpan is an author's cells on a line: who is shown or hidden by a
// click there, all for "everyone".
type reviewWhoSpan struct {
	x0, x1 int
	who    string
	all    bool
}

// reviewWhoCount is one author of the diff and how many hunks are mainly
// theirs.
type reviewWhoCount struct {
	who   string
	hunks int
}

// reviewWho names the author of origin for the filter: the agent's name, or
// "you".
func (r *reviewState) reviewWho(origin int) string {
	a, ok := r.reviewAuthorOf(origin)
	if !ok || a.Kind != "agent" {
		return "you"
	}
	return reviewText(a.Agent)
}

// reviewWhoCounts are the authors of the diff's hunks, the agents in the
// order they first appear and then the person, each with the hunks mainly
// theirs. Empty when the diff has no origins.
func (r *reviewState) reviewWhoCounts() []reviewWhoCount {
	if r.diff == nil || len(r.diff.Authors) == 0 {
		return nil
	}
	count := map[string]int{}
	for i := range r.diff.Files {
		for _, h := range r.diff.Files[i].Hunks {
			if main, _ := reviewMainOrigin(h.Lines); main > 0 {
				count[r.reviewWho(main)]++
			}
		}
	}
	var out []reviewWhoCount
	for i, a := range r.diff.Authors {
		who := r.reviewWho(i + 1)
		if a.Kind != "agent" || count[who] == 0 || slices.ContainsFunc(out, func(c reviewWhoCount) bool { return c.who == who }) {
			continue
		}
		out = append(out, reviewWhoCount{who, count[who]})
	}
	if n := count["you"]; n > 0 {
		out = append(out, reviewWhoCount{"you", n})
	}
	return out
}

// reviewFiltered reports whether some author is hidden.
func (r *reviewState) reviewFiltered() bool { return len(r.filter.hidden) > 0 }

// reviewLineHidden reports whether ln is a changed line of a hidden author.
func (r *reviewState) reviewLineHidden(ln review.Line) bool {
	return r.reviewFiltered() && ln.Op != review.OpContext && ln.Origin > 0 && r.filter.hidden[r.reviewWho(ln.Origin)]
}

// reviewHunkShown reports whether h has a changed line of an author shown,
// or no author at all.
func (r *reviewState) reviewHunkShown(h review.Hunk) bool {
	if !r.reviewFiltered() {
		return true
	}
	known := false
	for _, ln := range h.Lines {
		if ln.Op == review.OpContext || ln.Origin == 0 {
			continue
		}
		known = true
		if !r.filter.hidden[r.reviewWho(ln.Origin)] {
			return true
		}
	}
	return !known
}

// reviewFileShown reports whether f has a hunk shown. A file with no hunks
// (binary, too large, not read) is shown unless its author is hidden.
func (r *reviewState) reviewFileShown(f *review.File) bool {
	if f == nil || !r.reviewFiltered() {
		return true
	}
	if len(f.Hunks) == 0 {
		o := reviewFileOrigin(f)
		return o == 0 || !r.filter.hidden[r.reviewWho(o)]
	}
	return slices.ContainsFunc(f.Hunks, r.reviewHunkShown)
}

// reviewHiddenHunks is how many hunks the filter hides.
func (r *reviewState) reviewHiddenHunks() int {
	n := 0
	if r.diff == nil || !r.reviewFiltered() {
		return 0
	}
	for i := range r.diff.Files {
		for _, h := range r.diff.Files[i].Hunks {
			if !r.reviewHunkShown(h) {
				n++
			}
		}
	}
	return n
}

// ReviewWhoMenuOpen reports whether the @ menu has the keys.
func (m *OS) ReviewWhoMenuOpen() bool { return m.review.open && m.review.filter.menu }

// ReviewWhoMenu opens the @ menu, or closes it. A diff with no authors has
// no menu, and the status line says so.
func (m *OS) ReviewWhoMenu() {
	f := &m.review.filter
	if f.menu {
		f.menu = false
		return
	}
	if len(m.review.reviewWhoCounts()) == 0 {
		m.reviewNotify("No author is known for these changes.", "info", m.Settings.NotificationDuration)
		return
	}
	f.menu, f.at = true, 0
}

// ReviewWhoMove moves the menu's cursor by delta, "everyone" last.
func (m *OS) ReviewWhoMove(delta int) {
	f := &m.review.filter
	n := len(m.review.reviewWhoCounts()) + 1
	f.at = ((f.at+delta)%n + n) % n
}

// ReviewWhoToggle shows or hides the author under the menu's cursor, or shows
// everyone.
func (m *OS) ReviewWhoToggle() {
	counts := m.review.reviewWhoCounts()
	if at := m.review.filter.at; at < len(counts) {
		m.reviewWhoSet(counts[at].who, false)
	} else {
		m.reviewWhoSet("", true)
	}
}

// reviewWhoOnly shows who only, or everyone when who is all that is shown.
func (m *OS) reviewWhoOnly(who string) {
	r := &m.review
	counts := r.reviewWhoCounts()
	only := len(counts) > 1 && !r.filter.hidden[who]
	for _, c := range counts {
		if c.who != who && !r.filter.hidden[c.who] {
			only = false
		}
	}
	if only {
		m.reviewWhoSet("", true)
		return
	}
	hidden := map[string]bool{}
	for _, c := range counts {
		if c.who != who {
			hidden[c.who] = true
		}
	}
	m.reviewFilterTo(hidden)
}

// reviewWhoSet shows or hides who, or shows everyone with all. The last
// author shown stays shown.
func (m *OS) reviewWhoSet(who string, all bool) {
	r := &m.review
	if all {
		m.reviewFilterTo(nil)
		return
	}
	hidden := map[string]bool{}
	for k := range r.filter.hidden {
		hidden[k] = true
	}
	if hidden[who] {
		delete(hidden, who)
	} else {
		hidden[who] = true
		shown := 0
		for _, c := range r.reviewWhoCounts() {
			if !hidden[c.who] {
				shown++
			}
		}
		if shown == 0 {
			m.reviewNotify("One author stays shown. Choose everyone to show them all.", "info", m.Settings.NotificationDuration)
			return
		}
	}
	m.reviewFilterTo(hidden)
}

// reviewFilterTo hides the authors in hidden, and keeps the cursor on its
// file and hunk while they are shown, or goes to the first file shown.
func (m *OS) reviewFilterTo(hidden map[string]bool) {
	r := &m.review
	if len(hidden) == 0 {
		hidden = nil
	}
	path, hunk := "", -1
	if e := r.currentFile(); e != nil {
		path = e.path
		if row, ok := m.reviewRowUnderCursor(); ok {
			hunk = row.hunk
		}
	}
	r.filter.hidden = hidden
	r.rebuildFiles()
	at := slices.IndexFunc(r.files, func(e reviewFileEntry) bool { return e.path == path })
	if at < 0 {
		r.file, r.cursor, r.scroll = 0, 0, 0
		return
	}
	r.file = at
	if e := r.currentFile(); hunk >= 0 && e != nil && e.file != nil && hunk < len(e.file.Hunks) && r.reviewHunkShown(e.file.Hunks[hunk]) {
		m.reviewGoToHunk(at, hunk)
		return
	}
	r.cursor, r.scroll = 0, 0
}

// reviewWhoWords is the header's word for the filter: who is shown, then
// "claude hidden (4 hunks)". It records where each part is, from x, for the
// mouse.
func (m *OS) reviewWhoWords(x int, pal overlay.Palette) []reviewSeg {
	r := &m.review
	r.filter.heads = r.filter.heads[:0]
	if !r.reviewFiltered() {
		return nil
	}
	segs := []reviewSeg{{pal.FgDim, "  showing", false}}
	x += ansi.StringWidth(segs[0].text)
	var hidden []string
	for _, c := range r.reviewWhoCounts() {
		if r.filter.hidden[c.who] {
			hidden = append(hidden, c.who)
			continue
		}
		ink := pal.Warning
		if c.who != "you" {
			ink = r.reviewWhoInk(c.who, pal)
		}
		segs = append(segs, reviewSeg{pal.FgDim, " ", false}, reviewSeg{ink, c.who, true})
		r.filter.heads = append(r.filter.heads, reviewWhoSpan{x0: x + 1, x1: x + 1 + ansi.StringWidth(c.who), who: c.who})
		x += 1 + ansi.StringWidth(c.who)
	}
	if len(hidden) > 0 {
		words := strings.Join(hidden, ", ") + " hidden (" + reviewCount(r.reviewHiddenHunks(), "hunk") + ")"
		segs = append(segs, reviewSeg{pal.FgDim, "  " + words, false})
		r.filter.heads = append(r.filter.heads, reviewWhoSpan{x0: x + 2, x1: x + 2 + ansi.StringWidth(words), all: true})
	}
	return segs
}

// reviewWhoInk is the colour of the agent named who.
func (r *reviewState) reviewWhoInk(who string, pal overlay.Palette) color.Color {
	if who == "you" {
		return pal.Warning
	}
	for i := range r.diff.Authors {
		if r.reviewWho(i+1) == who {
			return r.reviewAuthorInk(i+1, pal)
		}
	}
	return pal.Fg
}

// reviewWhoMenuLine draws the @ menu on the footer line: each author with
// its hunk count and whether it is shown, then everyone, then the keys.
func (m *OS) reviewWhoMenuLine(width int, pal overlay.Palette) string {
	r := &m.review
	r.filter.items = r.filter.items[:0]
	shown, hidden := "●", "○"
	if overlay.UseASCII() {
		shown, hidden = "*", "-"
	}
	segs := []reviewSeg{{pal.AccentBright, " Who:", true}}
	x := 1 + ansi.StringWidth(segs[0].text)
	cur := pal.Ground(overlay.RowState{Cursor: true, Focused: true}, pal.Surface)
	// An item is its dot, in the author's colour as on the gutter bars, and
	// its words. The dot is the menu's key to the colours.
	item := func(i int, dot string, dotInk color.Color, text string, ink color.Color, span reviewWhoSpan) {
		segs = append(segs, reviewSeg{pal.FgDim, "  ", false})
		x += 2
		if dot != "" {
			text = " " + text
		}
		w := ansi.StringWidth(dot) + ansi.StringWidth(text)
		span.x0, span.x1 = x, x+w
		r.filter.items = append(r.filter.items, span)
		// The cursor's item is in the cursor's ground.
		bg := pal.Surface
		if i == r.filter.at {
			bg = cur
		}
		if dot != "" {
			segs = append(segs, reviewSeg{nil, reviewSGR(overlay.Readable(dotInk, bg), bg, true, dot), false})
		}
		segs = append(segs, reviewSeg{nil, reviewSGR(overlay.Readable(ink, bg), bg, true, text), false})
		x += w
	}
	counts := r.reviewWhoCounts()
	for i, c := range counts {
		mark, ink := shown, pal.Fg
		if r.filter.hidden[c.who] {
			mark, ink = hidden, pal.FgDim
		}
		item(i, mark, r.reviewWhoInk(c.who, pal), c.who+" "+strconv.Itoa(c.hunks), ink, reviewWhoSpan{who: c.who})
	}
	item(len(counts), "", nil, "everyone", pal.Fg, reviewWhoSpan{all: true})
	hints := []overlay.Hint{{Key: "space", Label: "show or hide"}, {Key: "esc", Label: "done"}}
	keys := reviewHintStrip(hints, pal)
	at := width - ansi.StringWidth(keys) - 1
	spans := make([]reviewHintSpan, 0, len(hints))
	for i, p := range reviewHintParts(hints, pal) {
		w := ansi.StringWidth(p)
		spans = append(spans, reviewHintSpan{x0: at, x1: at + w, key: reviewHintKey(hints[i])})
		at += w + 2
	}
	m.reviewRecordHints(spans, 1)
	return reviewPaint(segs, width-ansi.StringWidth(keys)-1, pal.Surface) + keys + reviewSpaces(1, pal.Surface)
}

// reviewWhoClick handles a click at x, y on the menu or the header, and
// reports whether it was on one of their authors.
func (m *OS) reviewWhoClick(x, y int) bool {
	r := &m.review
	var spans []reviewWhoSpan
	switch {
	case y == 1:
		spans = r.filter.heads
	case r.filter.menu && y == r.hintRow:
		spans = r.filter.items
	}
	for i, s := range spans {
		if x < s.x0 || x >= s.x1 {
			continue
		}
		switch {
		case y != 1:
			r.filter.at = i
			m.ReviewWhoToggle()
		case s.all:
			m.reviewWhoSet("", true)
		default:
			m.reviewWhoOnly(s.who)
		}
		return true
	}
	return false
}

// reviewChipClick shows only the main author of hunk h, when x, from the
// diff column's first cell, is on the author on its header.
func (m *OS) reviewChipClick(h, x int) bool {
	r := &m.review
	span, ok := r.filter.chips[h]
	e := r.currentFile()
	if !ok || x < span[0] || x >= span[1] || e == nil || e.file == nil || h >= len(e.file.Hunks) {
		return false
	}
	main, _ := reviewMainOrigin(e.file.Hunks[h].Lines)
	if main == 0 {
		return false
	}
	m.reviewWhoOnly(r.reviewWho(main))
	return true
}
