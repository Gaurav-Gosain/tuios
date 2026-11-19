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
	// ckMarkerOther is a label the list does not know, drawn as context.
	ckMarkerOther ckMarkerGroup = iota
	// ckMarkerProblem is something that went wrong.
	ckMarkerProblem
	// ckMarkerAttention is something that wants the person.
	ckMarkerAttention
	// ckMarkerSettled is something finished and good.
	ckMarkerSettled
	// ckMarkerDanger is a consequence: it costs money or cannot be undone.
	ckMarkerDanger
	// ckMarkerWork is the kind of work, such as Investigate.
	ckMarkerWork
	// ckMarkerOwner says whose the thing is.
	ckMarkerOwner
	// ckMarkerContext is context, such as New or Changed.
	ckMarkerContext
)

// ckMarkerGroups are the groups' letters in the order of ckMarkerGroup.
const ckMarkerGroups = "_pasdwoc"

// ckMarkerKnown are the known words, each as its group's letter (see
// ckMarkerGroups), the word, and its Nerd Font icon. One string costs less
// in the binary than a table of structs or a map.
const ckMarkerKnown = "sDone\uf00c|sPassed\uf00c|sObserved\uf06e|sFixed\uf0ad|sLive\uf135|sRecommended\uf005|sKeep\uf08d|sVerified\uf00c|sMerged\uf126|sShipped\uf135|sApproved\uf164|sReady\uf00c|sSafe\uf132|sHealthy\uf004|" +
	"pFailed\uf00d|pBlocked\uf05e|pBroken\uf127|pRejected\uf00d|pUnsafe\uf132|pDown\uf063|" +
	"aNeeds you\uf0f3|aUnverified\uf128|aPending\uf017|aReview\uf06e|aPartial\uf042|aSkipped\uf04e|aIn progress\uf110|aAt risk\uf071|aTodo\uf0ae|aStale\uf1da|aDraft\uf044|" +
	"dCosts money\uf155|dDestructive\uf1f8|dIrreversible\uf023|" +
	"wBrainstorm\uf0eb|wInvestigate\uf002|wPreview\uf06e|wImplement\uf0ad|wQualify\uf0c3|" +
	"oOwned\uf007|oShared\uf0c0|oForeign\uf023|" +
	"cNew\uf067|cChanged\uf040|cInferred\uf0eb|cOptional\uf10c|cDecision\uf126"

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
			return w + label[n:], ckMarkerGroup(strings.IndexByte(ckMarkerGroups, e[0])), ic
		}
	}
	return label, ckMarkerOther, ""
}

// ckMarkerInk is a group's ink and the tint of its pill's ground. solid
// says the pill is the ink itself, for a consequence and for ownership.
func (l ckLook) ckMarkerInk(g ckMarkerGroup) (ink, tint color.Color, solid bool) {
	pal := l.pal
	switch g {
	case ckMarkerProblem:
		return pal.Warn, l.warnTint, false
	case ckMarkerAttention:
		return pal.Attention, l.attnTint, false
	case ckMarkerSettled:
		return pal.Success, l.okTint, false
	case ckMarkerDanger:
		return pal.Warn, l.warnTint, true
	case ckMarkerWork:
		return pal.Busy, l.busyTint, false
	case ckMarkerOwner:
		return pal.Info, l.infoTint, true
	case ckMarkerContext, ckMarkerOther:
	}
	return pal.Info, l.infoTint, false
}

// ckMarkerIcon is the Nerd Font icon of a label the list does not know.
const ckMarkerIcon = "\uf02b"

// The Nerd Font half circles that round a pill's ends.
const ckCapL, ckCapR = "\ue0b6", "\ue0b4"

// pill is a marker's segments: the label, cut to ckMarkerLabelMax cells,
// after its icon, in the group's ink on the group's tint, between rounded
// caps in the tint. Without Nerd Fonts the pill has square ends: a cell of
// its ground either side, and no icon. A consequence and an owner are solid:
// the ink is the ground. At 16 colours there are no tints, and the pill is
// the group's colour in reverse. It is bold and wraps as one word.
func (md *ckMD) pill(label string, attrs uint8) []ckSeg {
	l := md.l
	show, g, icon := ckMarkerRead(label)
	if ckTextWidth(show) > ckMarkerLabelMax {
		show = ckCutText(show, ckMarkerLabelMax-1) + overlay.Ellipsis()
	}
	ink, tint, solid := l.ckMarkerInk(g)
	s := ckSeg{t: show, fg: ink, attrs: attrs | ckBold}
	ground := tint
	switch {
	case noColor(tint):
		s.attrs |= uv.AttrReverse
		ground = ink
	case solid:
		s.bg, s.fg, ground = ink, overlay.Readable(l.pal.PillFg, ink), ink
	default:
		s.bg, s.fg = tint, overlay.Readable(ink, tint)
	}
	if !l.nerd {
		s.t = " " + show + " "
		return []ckSeg{s}
	}
	if icon == "" {
		icon = ckMarkerIcon
	}
	// The caps and the icon are segments of their own, so copy mode leaves
	// them out, as it does the thread's other marks.
	capL, mark, gap, capR := ckSeg{t: ckCapL, fg: ground}, s, s, ckSeg{t: ckCapR, fg: ground}
	mark.t, gap.t = icon, " "
	return []ckSeg{capL, mark, gap, s, capR}
}

// ckLabelIcons are the words a heading or a bold lead label starts with
// that take an icon, each with its Nerd Font icon. ckActionIcons are the
// lead-ins that name an action, which take an icon with their colon
// anywhere in a line.
const (
	ckLabelIcons  = "finding\uf002|result\uf11e|status\uf012|evidence\uf06e|cause\uf0e7|effect\uf140|fix\uf0ad|risk\uf071|limits\uf071|cost\uf155|decision\uf126|options\uf0ca|plan\uf0ae|next\uf061|why\uf059|how\uf013|rule\uf0e3|example\uf121|checked\uf00c|verdict\uf0e3|rollback\uf0e2|undo\uf0e2|review\uf06e|scoreboard\uf080|terminal\uf120|folder\uf07b|file\uf15b|branch\uf126|ideas\uf0eb|summary\uf0ca|changes\uf040|tests\uf0c3"
	ckActionIcons = "check\uf046|do\uf0a4|next\uf061|unblock\uf09c|left\uf141|run\uf04b|guard\uf132|undo\uf0e2|see\uf08e"
)

// ckSegsText is the text of segments.
func ckSegsText(segs []ckSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.t)
	}
	return b.String()
}

// ckIconFor is the icon table's icon for label, read without case and
// without a colon at its end, or "".
func ckIconFor(table, label string) string {
	key := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(label), ":")))
	for s := table; s != "" && key != ""; {
		var e string
		e, s, _ = strings.Cut(s, "|")
		r, size := utf8.DecodeLastRuneInString(e)
		if r >= 0xe000 && r <= 0xf8ff && e[:len(e)-size] == key {
			return e[len(e)-size:]
		}
	}
	return ""
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
