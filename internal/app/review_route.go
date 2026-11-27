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

// Who a note goes to (DESIGN.md section 5, and the lead's answer 2 in section
// 12): a note on a line goes to the agent that made the line, and a note on a
// hunk to the hunk's main author by lines. A note on the person's own lines
// has no agent to go to and stays in the list for the person. The note
// editor names the receiver, and @ as its first key changes it, as does a
// click on the receiver's name. It never asks. S sends each agent its notes,
// one message each (send-review groups them in the daemon).
//
// A receiver is review.Note.To: "" for the pane the review is on, the window
// id of another agent pane of the same session, or review.NoteToPerson.

// reviewReceiver is one receiver a note can go to.
type reviewReceiver struct {
	to   string
	name string
	// origin is an origin of the receiver's agent, for its colour. 0 for
	// none.
	origin int
}

// reviewAgentReceiver is the To a note to author a takes, and false when a
// note cannot go to it: the person, an agent with no pane, or a pane this
// client does not see in the review's session, which the daemon would refuse.
func (m *OS) reviewAgentReceiver(a reviewAuthorInfo) (string, bool) {
	r := &m.review
	if r.diff == nil || a.Kind != "agent" || a.Pane == "" {
		return "", false
	}
	if a.Pane == r.diff.Window {
		return "", true
	}
	if a.Session != "" && a.Session != r.diff.Session || m.windowByID(a.Pane) == nil {
		return "", false
	}
	return a.Pane, true
}

// reviewReceivers are the receivers @ goes through, in this order: the pane
// the review is on, each other agent that made lines of this diff, and the
// person.
func (m *OS) reviewReceivers() []reviewReceiver {
	r := &m.review
	out := []reviewReceiver{{to: "", name: m.reviewReceiverName(""), origin: m.reviewPaneOrigin()}}
	if r.diff != nil {
		for i, a := range r.diff.Authors {
			to, ok := m.reviewAgentReceiver(a)
			if !ok || to == "" || slices.ContainsFunc(out, func(x reviewReceiver) bool { return x.to == to }) {
				continue
			}
			out = append(out, reviewReceiver{to: to, name: reviewText(a.Agent), origin: i + 1})
		}
	}
	return append(out, reviewReceiver{to: review.NoteToPerson, name: "you"})
}

// reviewPaneOrigin is an origin of the agent in the pane the review is on, 0
// when it made none of the lines.
func (m *OS) reviewPaneOrigin() int {
	r := &m.review
	if r.diff == nil {
		return 0
	}
	for i, a := range r.diff.Authors {
		if a.Kind == "agent" && a.Pane == r.diff.Window {
			return i + 1
		}
	}
	return 0
}

// reviewReceiverName names receiver to: the agent, or "you".
func (m *OS) reviewReceiverName(to string) string {
	r := &m.review
	switch {
	case to == review.NoteToPerson:
		return "you"
	case to == "":
		if o := m.reviewPaneOrigin(); o > 0 {
			return reviewText(r.diff.Authors[o-1].Agent)
		}
		return r.who
	}
	if r.diff != nil {
		for _, a := range r.diff.Authors {
			if a.Kind == "agent" && a.Pane == to {
				return reviewText(a.Agent)
			}
		}
	}
	if w := m.windowByID(to); w != nil {
		if name := printableTitle(m.railTitleShown(w)); name != "" {
			return name
		}
	}
	return shortWindowLabel(to)
}

// reviewReceiverInk is the colour receiver to is named in: its agent's, or
// the person's.
func (m *OS) reviewReceiverInk(to string, pal overlay.Palette) color.Color {
	for _, rc := range m.reviewReceivers() {
		if rc.to == to && rc.origin > 0 {
			return m.review.reviewAuthorInk(rc.origin, pal)
		}
	}
	if to == review.NoteToPerson {
		return pal.Warning
	}
	return pal.AccentBright
}

// reviewDefaultReceiver is who a note on line lineIdx of h goes to, or on
// the whole hunk when lineIdx is -1: the line's author, else the hunk's main
// author. A line the person may not have made (unsure) goes to the pane. So
// does every line of a diff no agent turn made: with no turn known, "you" is
// what an unknown author reads as, not a sign the person wrote it, and the
// pane's agent is who the notes went to before authors were known.
func (m *OS) reviewDefaultReceiver(h review.Hunk, lineIdx int) string {
	origin, unsure := 0, false
	if lineIdx >= 0 && lineIdx < len(h.Lines) && h.Lines[lineIdx].Op != review.OpContext {
		origin, unsure = h.Lines[lineIdx].Origin, h.Lines[lineIdx].Unsure
	}
	if origin == 0 {
		origin, _ = reviewMainOrigin(h.Lines)
		unsure = reviewHunkUnsure(h, origin)
	}
	a, ok := m.review.reviewAuthorOf(origin)
	switch {
	case !ok:
		return ""
	case a.Kind != "agent":
		if unsure || !m.reviewAgentsKnown() {
			return ""
		}
		return review.NoteToPerson
	}
	to, _ := m.reviewAgentReceiver(a)
	return to
}

// reviewAgentsKnown reports whether an agent made any line of the diff.
func (m *OS) reviewAgentsKnown() bool {
	r := &m.review
	return r.diff != nil && slices.ContainsFunc(r.diff.Authors, func(a reviewAuthorInfo) bool { return a.Kind == "agent" })
}

// ReviewEditorAt is @ in an editor. In the note editor with nothing typed
// yet it gives the note to the next receiver and reports true. Anywhere else
// @ is text.
func (m *OS) ReviewEditorAt() bool {
	ed := m.review.editor
	if ed == nil || ed.kind != reviewEditNote || ed.draft != "" {
		return false
	}
	m.reviewNextReceiver()
	return true
}

// reviewNextReceiver gives the note being typed to the next receiver.
func (m *OS) reviewNextReceiver() {
	ed := m.review.editor
	if ed == nil || ed.kind != reviewEditNote {
		return
	}
	if m.ProcessingRemoteKeys {
		ed.automated = true
	}
	rs := m.reviewReceivers()
	i := slices.IndexFunc(rs, func(x reviewReceiver) bool { return x.to == ed.to })
	ed.to = rs[(i+1)%len(rs)].to
	ed.toChanged = true
}

// reviewEditorWords are what the note editor says before the draft: who the
// note is from, who it goes to, and the rest of the label. "you → claude: ",
// or "you → you, not sent: " for a note kept for the person.
func (m *OS) reviewEditorWords(ed *reviewEditor) (lead, name, rest string) {
	hunk := ""
	if ed.noteID == "" && ed.hunk != "" {
		hunk = " (hunk)"
	}
	lead, name, rest = reviewBar()+" you → ", m.reviewReceiverName(ed.to), hunk+": "
	if ed.to == review.NoteToPerson {
		rest = hunk + ", not sent: "
	}
	return lead, name, rest
}

// reviewEditorLabel is reviewEditorWords as segments, the receiver in its
// colour.
func (m *OS) reviewEditorLabel(ed *reviewEditor, pal overlay.Palette, bg color.Color) []reviewSeg {
	lead, name, rest := m.reviewEditorWords(ed)
	return []reviewSeg{
		{overlay.Readable(pal.AccentBright, bg), lead, true},
		{overlay.Readable(m.reviewReceiverInk(ed.to, pal), bg), name, true},
		{overlay.Readable(pal.AccentBright, bg), rest, true},
	}
}

// reviewEditorNameAt reports whether cell dx of a diff column width cells
// wide is on the receiver's name in the note editor's row.
func (m *OS) reviewEditorNameAt(dx, width int) bool {
	r := &m.review
	ed := r.editor
	e := r.currentFile()
	if ed == nil || ed.kind != reviewEditNote || e == nil {
		return false
	}
	indent := reviewNoteIndent(e.file, m.reviewSplitOn(width))
	lead, name, _ := m.reviewEditorWords(ed)
	at := indent + ansi.StringWidth(lead)
	return dx >= at && dx < at+ansi.StringWidth(name)
}

// reviewNoteTo is how a note row names its receiver after its label: empty
// for the pane the review is on.
func (m *OS) reviewNoteTo(n review.Note) string {
	switch n.To {
	case "":
		return ""
	case review.NoteToPerson:
		return " for you, not sent"
	}
	return " to " + m.reviewReceiverName(n.To)
}

// reviewSendCount is how many unsent notes go to one receiver.
type reviewSendCount struct {
	to, name string
	n        int
}

// reviewSendCounts are the unsent notes S sends, by receiver, in the order
// the notes are listed.
func (m *OS) reviewSendCounts() []reviewSendCount {
	var out []reviewSendCount
	var keys []string
	for _, n := range m.review.notes {
		if n.SentAt != 0 || n.To == review.NoteToPerson {
			continue
		}
		i := slices.Index(keys, n.To)
		if i < 0 {
			keys = append(keys, n.To)
			out = append(out, reviewSendCount{to: n.To, name: m.reviewReceiverName(n.To)})
			i = len(out) - 1
		}
		out[i].n++
	}
	return out
}

// reviewSendWords is the S hint: "send 2 notes" to the pane the review is
// on, "send 2 notes to codex" to another, "send 2 to claude, 1 to codex", or
// "send 5 notes to 3 agents".
func (m *OS) reviewSendWords() string {
	counts := m.reviewSendCounts()
	total := 0
	for _, c := range counts {
		total += c.n
	}
	switch len(counts) {
	case 0:
		return ""
	case 1:
		if counts[0].to != "" {
			return "send " + reviewCount(total, "note") + " to " + counts[0].name
		}
		return "send " + reviewCount(total, "note")
	case 2:
		parts := make([]string, len(counts))
		for i, c := range counts {
			parts[i] = strconv.Itoa(c.n) + " to " + c.name
		}
		return "send " + strings.Join(parts, ", ")
	}
	return "send " + reviewCount(total, "note") + " to " + strconv.Itoa(len(counts)) + " agents"
}
