// Package editreview turns a held edit's body into the lines a card draws,
// and keeps track of which of them the person was shown.
//
// An allow of a held edit (integration.EditBody) is sent only when the
// person saw the whole body. A card cannot show a 32 KiB line or a thousand
// hunks at once, so the card never draws the body itself: it draws the lines
// Lines gives for its width, which are wrapped (never clipped), with tabs as
// spaces, trailing spaces marked, and a head on every hunk that says what the
// hunk does. It reports each span it drew with Review.Shown, and an allow
// goes only once Review.Complete is true at that width.
//
// It draws nothing and imports no UI, so the rule can be tested here.
package editreview

import (
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/charmbracelet/x/ansi"
)

// Kind is what a line shows.
type Kind int

// Kinds of line.
const (
	// KindHead says what a hunk does: its number, and whether it replaces
	// every match or writes the whole file.
	KindHead Kind = iota
	// KindOld is text the hunk takes out, after "- ".
	KindOld
	// KindNew is text the hunk puts in, after "+ ".
	KindNew
	// KindNote marks text with no line break at its end.
	KindNote
)

// MinWidth is the narrowest card Lines draws for, in cells.
const MinWidth = 20

// TabWidth is how many cells a tab stop is apart.
const TabWidth = 4

// TrailingSpace stands for each space or tab at the end of a line, which
// would otherwise draw as nothing.
const TrailingSpace = "·"

// TrailingTab stands for a tab at the end of a line. Expanded to spaces it
// would read as trailing spaces, and the two are different edits.
const TrailingTab = "→"

// Line is one line of the card, at most the width Lines was given, in cells.
// Cont marks a row that goes on from the row above: the line was wider than
// the card and wrapped there, so the card draws a mark beside it and a
// person does not read one line as two.
type Line struct {
	Hunk int
	Kind Kind
	Text string
	Cont bool
}

// Lines is the body as a card of width cells draws it, or nil when width is
// under MinWidth. Every line is at most width cells wide and holds no tab or
// line break.
func Lines(e integration.EditBody, width int) []Line {
	if width < MinWidth {
		return nil
	}
	var out []Line
	add := func(hunk int, kind Kind, prefix, text string) {
		wrapped := ansi.Hardwrap(text, width-len(prefix), true)
		if kind == KindHead || kind == KindNote {
			// The words are tuios's own, so they wrap between words.
			wrapped = ansi.Wrap(text, width-len(prefix), "")
		}
		for k, part := range strings.Split(wrapped, "\n") {
			lead := prefix
			if k > 0 && (kind == KindOld || kind == KindNew) {
				// A wrapped row of code keeps the column under the text,
				// so the "- " or "+ " is said once, on its first row.
				lead = strings.Repeat(" ", len(prefix))
			}
			out = append(out, Line{Hunk: hunk, Kind: kind, Text: lead + part, Cont: k > 0})
		}
	}
	for i, h := range e.Hunks {
		add(i, KindHead, "", head(e, i))
		if e.Tool != "Write" {
			side(add, i, KindOld, "- ", h.Old)
		}
		side(add, i, KindNew, "+ ", h.New)
	}
	return out
}

// head is what a hunk's head line says.
func head(e integration.EditBody, i int) string {
	if e.Tool == "Write" {
		return "Writes the whole file " + e.Path + ". It replaces the file if the file exists."
	}
	h := e.Hunks[i]
	s := "Change " + strconv.Itoa(i+1) + " of " + strconv.Itoa(len(e.Hunks)) + " in " + e.Path
	if h.ReplaceAll {
		s += ". It replaces every match, not only the first."
	}
	if h.Old == "" {
		s += ". The old text is empty."
	}
	return s
}

// side adds the lines of one string of a hunk. An empty string is one empty
// line, so the person sees that side is empty, and text with no line break at
// its end is marked, since "a" and "a\n" are different edits.
func side(add func(int, Kind, string, string), hunk int, kind Kind, prefix, s string) {
	if s == "" {
		add(hunk, kind, prefix, "")
		return
	}
	body, ends := strings.CutSuffix(s, "\n")
	for _, l := range strings.Split(body, "\n") {
		add(hunk, kind, prefix, visible(l))
	}
	if !ends {
		add(hunk, KindNote, "  ", "(no line break at the end)")
	}
}

// visible is one line with its tabs as spaces to the next tab stop and its
// trailing spaces and tabs marked, each by its own mark.
func visible(l string) string {
	body := strings.TrimRight(l, " \t")
	var b strings.Builder
	col := 0
	for _, r := range body {
		if r == '\t' {
			n := TabWidth - col%TabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col += ansi.StringWidth(string(r))
	}
	for _, r := range l[len(body):] {
		if r == '\t' {
			b.WriteString(TrailingTab)
		} else {
			b.WriteString(TrailingSpace)
		}
	}
	return b.String()
}

// Review is which lines of a card the person was shown, at one width.
type Review struct {
	width int
	seen  []bool
	left  int
}

// New is a review of n lines drawn at width.
func New(n, width int) *Review {
	return &Review{width: width, seen: make([]bool, max(n, 0)), left: max(n, 0)}
}

// Shown records that lines from up to, not including, to were on screen at
// width. A span at another width, or outside the lines, counts for nothing
// past the lines' ends.
func (r *Review) Shown(width, from, to int) {
	if r == nil || width != r.width {
		return
	}
	from, to = max(from, 0), min(to, len(r.seen))
	for i := from; i < to; i++ {
		if !r.seen[i] {
			r.seen[i] = true
			r.left--
		}
	}
}

// Complete reports whether every line was shown. A review of no lines never
// is.
func (r *Review) Complete() bool {
	return r != nil && len(r.seen) > 0 && r.left == 0
}

// Left is how many lines were not shown yet.
func (r *Review) Left() int {
	if r == nil {
		return 0
	}
	return r.left
}

// Width is the width the review counts lines at.
func (r *Review) Width() int {
	if r == nil {
		return 0
	}
	return r.width
}
