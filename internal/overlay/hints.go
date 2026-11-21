package overlay

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

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

func (h Hint) rank() HintPriority {
	if h.Key == "esc" || h.Priority >= HintEssential {
		return HintEssential
	}
	return h.Priority
}

type fittedHints struct {
	Hints     []Hint
	Index     []int
	Truncated bool
}

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

func identity(n int) []int {
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	return idx
}

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
		for _, i := range slices.Backward(dropped) {
			kept[i] = true
			if hintsWidth(pick().Hints, sep, false) > width {
				kept[i] = false
			}
		}
		return pick()
	}

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
	for n := range slices.Backward(f.Hints) {
		if hintsWidth(f.Hints[:n], sep, true) <= width {
			return fittedHints{Hints: f.Hints[:n], Index: f.Index[:n], Truncated: true}
		}
	}
	return fittedHints{Truncated: true}
}

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

const footerSep = 3

const dialogSep = 2
