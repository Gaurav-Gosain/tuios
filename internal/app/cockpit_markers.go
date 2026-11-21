package app

import (
	"image/color"
	"strings"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/markdown"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type ckMarkerGroup uint8

const (
	ckMarkerOther ckMarkerGroup = iota
	ckMarkerProblem
	ckMarkerAttention
	ckMarkerSettled
	ckMarkerDanger
	ckMarkerWork
	ckMarkerOwner
	ckMarkerContext
)

const ckMarkerGroups = "_pasdwoc"

const ckMarkerKnown = "sDone\uf00c|sPassed\uf00c|sObserved\uf06e|sFixed\uf0ad|sLive\uf135|sRecommended\uf005|sKeep\uf08d|sVerified\uf00c|sMerged\uf126|sShipped\uf135|sApproved\uf164|sReady\uf00c|sSafe\uf132|sHealthy\uf004|" +
	"pFailed\uf00d|pBlocked\uf05e|pBroken\uf127|pRejected\uf00d|pUnsafe\uf132|pDown\uf063|" +
	"aNeeds you\uf0f3|aUnverified\uf128|aPending\uf017|aReview\uf06e|aPartial\uf042|aSkipped\uf04e|aIn progress\uf110|aAt risk\uf071|aTodo\uf0ae|aStale\uf1da|aDraft\uf044|" +
	"dCosts money\uf155|dDestructive\uf1f8|dIrreversible\uf023|" +
	"wBrainstorm\uf0eb|wInvestigate\uf002|wPreview\uf06e|wImplement\uf0ad|wQualify\uf0c3|" +
	"oOwned\uf007|oShared\uf0c0|oForeign\uf023|" +
	"cNew\uf067|cChanged\uf040|cInferred\uf0eb|cOptional\uf10c|cDecision\uf126"

const ckMarkerSynonyms = "complete=Done|completed=Done|finished=Done|ok=Done|pass=Passed|fail=Failed|failing=Failed|error=Failed|waiting=Pending|needs review=Review"

const ckMarkerLabelMax = 32

func ckMarkerRead(label string) (show string, g ckMarkerGroup, icon string) {
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

const ckMarkerIcon = "\uf02b"

const ckCapL, ckCapR = "\ue0b6", "\ue0b4"

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
	capL, mark, gap, capR := ckSeg{t: ckCapL, fg: ground}, s, s, ckSeg{t: ckCapR, fg: ground}
	mark.t, gap.t = icon, " "
	return []ckSeg{capL, mark, gap, s, capR}
}

const (
	ckLabelIcons  = "finding\uf002|result\uf11e|status\uf012|evidence\uf06e|cause\uf0e7|effect\uf140|fix\uf0ad|risk\uf071|limits\uf071|cost\uf155|decision\uf126|options\uf0ca|plan\uf0ae|next\uf061|why\uf059|how\uf013|rule\uf0e3|example\uf121|checked\uf00c|verdict\uf0e3|rollback\uf0e2|undo\uf0e2|review\uf06e|scoreboard\uf080|terminal\uf120|folder\uf07b|file\uf15b|branch\uf126|ideas\uf0eb|summary\uf0ca|changes\uf040|tests\uf0c3"
	ckActionIcons = "check\uf046|do\uf0a4|next\uf061|unblock\uf09c|left\uf141|run\uf04b|guard\uf132|undo\uf0e2|see\uf08e"
)

func ckSegsText(segs []ckSeg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.t)
	}
	return b.String()
}

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

func ckMarkerRun(r markdown.Run) bool { return r.Style&markdown.Marker != 0 }
