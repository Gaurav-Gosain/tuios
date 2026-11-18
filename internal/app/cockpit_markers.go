package app

import (
	"image/color"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Reply markers. An agent writes [[Done]], [[Failed: 3 tests]] or [[Needs
// you]] beside the thing it describes, and the thread draws the marker as a
// pill in the colour of its group: a problem, something that wants the
// person, something settled, or anything else. internal/markdown reads the
// marker as a run of its own and never inside code, so the stored text, and a
// Markdown yank of it, stay what the agent wrote.
//
// The idea and the word list come from pi-emphasis by John Lindquist (MIT).

// ckMarkerGroup is what a marker says about the thing it marks.
type ckMarkerGroup uint8

const (
	// ckMarkerOther is a label the list does not know.
	ckMarkerOther ckMarkerGroup = iota
	// ckMarkerProblem is something that went wrong.
	ckMarkerProblem
	// ckMarkerAttention is something that wants the person.
	ckMarkerAttention
	// ckMarkerSettled is something finished and good.
	ckMarkerSettled
)

// ckMarkerKnown are the known words, each as its group's letter (s for
// settled, p for a problem, a for attention), the word, and its Nerd Font
// icon when it has one of its own. One string costs less in the binary than
// a table of structs or a map.
const ckMarkerKnown = "sDone\uf00c|sPassed\uf00c|sFixed|sVerified|sMerged|sShipped|sApproved|sReady|sHealthy|pFailed\uf00d|pBlocked\uf05e|pBroken|pRejected|pUnsafe|pDown|aNeeds you\uf0f3|aUnverified\uf128|aPending\uf017|aReview\uf06e|aDecision|aPartial|aSkipped|aIn progress|aAt risk|aTodo|aStale|aDraft"

// ckMarkerSynonyms are the synonyms of known words, as synonym=word.
const ckMarkerSynonyms = "complete=Done|completed=Done|finished=Done|ok=Done|pass=Passed|fail=Failed|failing=Failed|error=Failed|waiting=Pending|needs review=Review"

// ckMarkerLabelMax is the most cells a pill's label takes. A longer one is cut.
const ckMarkerLabelMax = 32

// ckMarkerRead reads a marker's label: the known word it starts with, as the
// pill shows it, with the rest of the label after it, the word's group and
// its icon ("" for the group's). A synonym shows the word it stands for. A
// label that starts with no known word is the other group, shown as written.
// A known word ends the label or is followed by a space or a colon, so
// [[Doneness]] is not Done.
func ckMarkerRead(label string) (show string, g ckMarkerGroup, icon string) {
	// The keys are ASCII, so a key's length in the label is its own.
	starts := func(key string) bool {
		n := len(key)
		return len(label) >= n && strings.EqualFold(label[:n], key) && (len(label) == n || label[n] == ' ' || label[n] == ':')
	}
	word, n := "", 0
	for s := ckMarkerSynonyms; s != "" && word == ""; {
		var e string
		e, s, _ = strings.Cut(s, "|")
		if k, w, _ := strings.Cut(e, "="); starts(k) {
			word, n = w, len(k)
		}
	}
	for s := ckMarkerKnown; s != ""; {
		var e string
		e, s, _ = strings.Cut(s, "|")
		w, ic := e[1:], ""
		if r, size := utf8.DecodeLastRuneInString(w); r >= 0xe000 && r <= 0xf8ff {
			w, ic = w[:len(w)-size], w[len(w)-size:]
		}
		if (word == "" && starts(w)) || w == word {
			if word == "" {
				n = len(w)
			}
			return w + label[n:], ckMarkerGroup(strings.IndexByte("_pas", e[0])), ic
		}
	}
	return label, ckMarkerOther, ""
}

// ckMarkerInk is a group's ink and the tint of its pill's ground.
func (l ckLook) ckMarkerInk(g ckMarkerGroup) (ink, tint color.Color) {
	pal := l.pal
	switch g {
	case ckMarkerProblem:
		return pal.Warn, l.warnTint
	case ckMarkerAttention:
		return pal.Attention, l.attnTint
	case ckMarkerSettled:
		return pal.Success, l.okTint
	}
	return pal.Accent, l.accTint
}

// ckMarkerIcon is the Nerd Font icon of a group with no icon of its own.
func ckMarkerIcon(g ckMarkerGroup) string {
	// Nerd Font: tag, times-circle, warning and check-circle.
	return [...]string{"\uf02b", "\uf057", "\uf071", "\uf058"}[g]
}

// pill is a marker's segments: a cell of its ground either side, the icon
// when Nerd Fonts are on, and the label cut to ckMarkerLabelMax cells. It is
// bold, in the group's ink on the group's tint, so it reads as one thing
// and wraps as one word. At 16 colours there are no tints, and the pill is
// the group's colour in reverse.
func (md *ckMD) pill(label string, attrs uint8) []ckSeg {
	l := md.l
	show, g, icon := ckMarkerRead(label)
	if ckTextWidth(show) > ckMarkerLabelMax {
		show = ckCutText(show, ckMarkerLabelMax-1) + overlay.Ellipsis()
	}
	ink, tint := l.ckMarkerInk(g)
	s := ckSeg{t: " " + show + " ", fg: ink, attrs: attrs | ckBold}
	if noColor(tint) {
		s.attrs |= uv.AttrReverse
	} else {
		s.bg, s.fg = tint, overlay.Readable(ink, tint)
	}
	if !l.nerd {
		return []ckSeg{s}
	}
	if icon == "" {
		icon = ckMarkerIcon(g)
	}
	// The icon is a segment of its own, so copy mode leaves it out as it
	// does the thread's other marks.
	pad, mark := s, s
	pad.t, mark.t = " ", icon
	return []ckSeg{pad, mark, s}
}

// marks colours the checks, crosses and arrows of a plain run, and dims the
// lines of a tree (├── and └──), so the items stand out. A run with none of
// them is returned as it is.
func (md *ckMD) marks(s ckSeg) []ckSeg {
	if !strings.ContainsAny(s.t, "✓✗→├└│─") {
		return []ckSeg{s}
	}
	pal := md.l.pal
	var out []ckSeg
	start, cur := 0, s.fg
	for i, r := range s.t {
		fg := s.fg
		switch r {
		case '✓':
			fg = pal.Success
		case '✗':
			fg = pal.Warn
		case '→':
			fg = pal.Accent
		case '├', '└', '│', '─':
			fg = pal.FgMute
		}
		if fg != cur {
			p := s
			p.t, p.fg = s.t[start:i], cur
			out = append(out, p)
			start, cur = i, fg
		}
	}
	s.t, s.fg = s.t[start:], cur
	return append(out, s)
}

// ckMarkerRun reports a run that is a reply marker.
func ckMarkerRun(r markdown.Run) bool { return r.Style&markdown.Marker != 0 }
