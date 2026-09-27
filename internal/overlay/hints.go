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
//  3. labels dropped one at a time from the end, so the keys stay and the
//     first hints, which a host lists first because they matter most, keep
//     their words longest;
//  4. whole hints dropped from the end, and an ellipsis where they were.
//
// The hints that survive are always a prefix of the ones asked for. A host
// that makes the hints pressable reads Geometry.Hints by index, and a prefix
// keeps that index meaning the same hint.

// fittedHints is a strip as it will be drawn: the hints in the form that fits,
// and whether any were dropped off the end.
type fittedHints struct {
	Hints     []Hint
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

// fitHints applies the tiers until the strip fits in width cells with sep
// cells between pairs.
func fitHints(hints []Hint, width, sep int) fittedHints {
	if len(hints) == 0 || hintsWidth(hints, sep, false) <= width {
		return fittedHints{Hints: hints}
	}
	out := make([]Hint, len(hints))
	for i, h := range hints {
		out[i] = h
		out[i].Key = ShortKey(h.Key)
	}
	if hintsWidth(out, sep, false) <= width {
		return fittedHints{Hints: out}
	}
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Label == "" {
			continue
		}
		out[i].Label = ""
		if hintsWidth(out, sep, false) <= width {
			return fittedHints{Hints: out}
		}
	}
	for n := len(out) - 1; n >= 0; n-- {
		if hintsWidth(out[:n], sep, true) <= width {
			return fittedHints{Hints: out[:n], Truncated: true}
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
