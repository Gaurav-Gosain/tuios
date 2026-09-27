package overlay

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// A key-hint strip keeps to one row. When the hints do not fit across it they
// shorten in tiers rather than wrap, because every row a footer wraps onto is a
// row taken from the body it describes:
//
//  1. every hint as written;
//  2. modifier names shortened: ctrl+ becomes ^, alt+ becomes M-, shift+
//     becomes S-;
//  3. whole hints dropped, lowest priority first and, within a priority, the
//     last listed first, until the strip fits. An essential hint (see
//     HintEssential, and every hint on the esc key) is never dropped here.
//     Once the strip fits, a dropped hint that fits in the room left is put
//     back, so a long label dropped early does not take a short one with it;
//  4. only when the essential hints alone do not fit: their labels dropped
//     from the end, so a key shows without its label only when nothing else
//     can;
//  5. hints dropped from the end, and an ellipsis where they were.
//
// A key never shows without its label while dropping a whole hint could make
// room instead: a footer of bare keys says nothing a person can act on. The
// hints that survive keep the order they were given in, and Geometry.Hints
// keeps one entry per hint asked for, so a host that makes the hints pressable
// reads it by the index it gave.

// HintPriority orders the hints of a strip for dropping. The zero value is
// HintNormal, so a hint that says nothing is dropped after the optional ones
// and before the essential ones.
type HintPriority int8

const (
	// HintOptional is a hint the strip gives up first: a second way to do
	// something another hint already offers, or a key that is in help.
	HintOptional HintPriority = -1
	// HintNormal is the default.
	HintNormal HintPriority = 0
	// HintEssential is a hint the strip keeps whole while anything else can
	// go: the way out of a panel. A hint on the esc key is essential whatever
	// its priority says.
	HintEssential HintPriority = 1
)

// Optional returns hints with each marked HintOptional.
func Optional(hints []Hint) []Hint {
	for i := range hints {
		hints[i].Priority = HintOptional
	}
	return hints
}

// Essential returns hints with each marked HintEssential.
func Essential(hints []Hint) []Hint {
	for i := range hints {
		hints[i].Priority = HintEssential
	}
	return hints
}

// rank is the priority fitHints drops by.
func (h Hint) rank() HintPriority {
	if h.Key == "esc" || h.Priority >= HintEssential {
		return HintEssential
	}
	return h.Priority
}

// fittedHints is a strip as it will be drawn: the hints in the form that fits,
// where each came from in the strip asked for, and whether any were dropped
// off the end.
type fittedHints struct {
	Hints     []Hint
	Index     []int
	Truncated bool
}

// hintsWidth is the width of hints laid out with sep cells between pairs, plus
// the ellipsis cell and its separator when truncated is set.
func hintsWidth(hints []Hint, sep int, truncated bool) int {
	w := 0
	for i, h := range hints {
		if i > 0 {
			w += sep
		}
		w += hintWidth(h)
	}
	if truncated {
		if len(hints) > 0 {
			w += sep
		}
		w += lipgloss.Width(Ellipsis())
	}
	return w
}

// HintsFit reports whether hints fit across width cells as written, with no
// tier applied. A host that would rather drop a hint of its own choosing than
// see every label shortened asks this first.
func HintsFit(hints []Hint, width int) bool {
	return hintsWidth(hints, footerSep, false) <= width
}

// identity is 0..n-1.
func identity(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// fitHints applies the tiers until the strip fits in width cells with sep
// cells between pairs.
func fitHints(hints []Hint, width, sep int) fittedHints {
	if len(hints) == 0 || hintsWidth(hints, sep, false) <= width {
		return fittedHints{Hints: hints, Index: identity(len(hints))}
	}
	out := make([]Hint, len(hints))
	for i, h := range hints {
		out[i] = h
		out[i].Key = ShortKey(h.Key)
	}
	if hintsWidth(out, sep, false) <= width {
		return fittedHints{Hints: out, Index: identity(len(out))}
	}

	// Tier 3: drop whole hints by priority, the last listed first.
	kept := make([]bool, len(out))
	for i := range kept {
		kept[i] = true
	}
	pick := func() fittedHints {
		var f fittedHints
		for i, k := range kept {
			if k {
				f.Hints = append(f.Hints, out[i])
				f.Index = append(f.Index, i)
			}
		}
		return f
	}
	var dropped []int
	fits := false
	for _, pr := range []HintPriority{HintOptional, HintNormal} {
		for i := len(out) - 1; i >= 0 && !fits; i-- {
			if !kept[i] || out[i].rank() > pr {
				continue
			}
			kept[i] = false
			dropped = append(dropped, i)
			fits = hintsWidth(pick().Hints, sep, false) <= width
		}
		if fits {
			break
		}
	}
	if fits {
		// Put back what fits in the room left, the last dropped first: it is
		// the one of highest priority, or the earliest listed of its priority.
		for j := len(dropped) - 1; j >= 0; j-- {
			i := dropped[j]
			kept[i] = true
			if hintsWidth(pick().Hints, sep, false) > width {
				kept[i] = false
			}
		}
		return pick()
	}

	// Tier 4: only essential hints are left and they do not fit whole.
	f := pick()
	for i := len(f.Hints) - 1; i >= 0; i-- {
		if f.Hints[i].Label == "" {
			continue
		}
		f.Hints[i].Label = ""
		if hintsWidth(f.Hints, sep, false) <= width {
			return f
		}
	}
	// Tier 5.
	for n := len(f.Hints) - 1; n >= 0; n-- {
		if hintsWidth(f.Hints[:n], sep, true) <= width {
			return fittedHints{Hints: f.Hints[:n], Index: f.Index[:n], Truncated: true}
		}
	}
	return fittedHints{Truncated: true}
}

// shortModifiers maps a modifier as a hint spells it to its short form. The
// short forms are the ones tmux and Emacs print, which a person reading a
// multiplexer's footer has most likely met.
var shortModifiers = []struct{ long, short string }{
	{"ctrl+", "^"},
	{"alt+", "M-"},
	{"opt+", "M-"},
	{"shift+", "S-"},
}

// ShortKey is key with its modifier names shortened, the second tier of a hint
// strip: "ctrl+p" becomes "^p". A key with no modifier comes back as it is.
func ShortKey(key string) string {
	if !strings.Contains(key, "+") {
		return key
	}
	var b strings.Builder
	for i := 0; i < len(key); {
		matched := false
		for _, m := range shortModifiers {
			end := i + len(m.long)
			if end < len(key) && strings.EqualFold(key[i:end], m.long) {
				b.WriteString(m.short)
				i += len(m.long)
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(key[i])
			i++
		}
	}
	return b.String()
}

// footerSep is the gap between two hints in a panel footer.
const footerSep = 3

// dialogSep is the gap between two hints in a dialog's border.
const dialogSep = 2
