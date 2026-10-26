package loom

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// nextGraphemeBySegmenter is nextGrapheme as it was when every character
// went through the grapheme segmenter. It is the reference nextGrapheme is
// held to.
func nextGraphemeBySegmenter(s *string) (string, int) {
	g, w := ansi.FirstGraphemeCluster(*s, ansi.GraphemeWidth)
	if g == "" {
		// Defensive: never loop on input the segmenter cannot cut.
		_, size := utf8.DecodeRuneInString(*s)
		*s = (*s)[max(size, 1):]
		return "", 0
	}
	*s = (*s)[len(g):]
	r, _ := utf8.DecodeRuneInString(g)
	switch {
	case r == utf8.RuneError && !utf8.ValidString(g):
		return "�", 1
	case r < 0x20 || (r >= 0x7f && r < 0xa0):
		return "", 0
	case w > 2:
		w = 2
	}
	return g, w
}

// TestNextGraphemeAgreesWithTheSegmenter holds nextGrapheme to the
// segmenter on the cluster, the width and the string left, step by step to
// the end of each input: every ASCII byte alone and before every ASCII byte,
// before every byte from 0x80 up, and before the starts of clusters that
// join a character before them, and seeded random strings that mix them.
func TestNextGraphemeAgreesWithTheSegmenter(t *testing.T) {
	nonASCII := []string{
		"\u0301",     // combining acute accent
		"\u200D",     // zero width joiner
		"\uFE0F",     // variation selector 16
		"\u4E2D",     // CJK ideograph
		"\U0001F600", // emoji
		"\u0E31",     // Thai MAI HAN-AKAT
		"\u1100",     // Hangul choseong
		"\u0085",     // next line, a C1 control
	}
	// check consumes input with both functions and reports the first step
	// at which they differ. It stops after len(input)+1 steps, so a
	// function that never advances fails rather than hangs.
	check := func(t *testing.T, input string) bool {
		t.Helper()
		got, want := input, input
		for step := 0; step <= len(input); step++ {
			if got == "" && want == "" {
				return true
			}
			before := got
			g, gw := nextGrapheme(&got)
			wg, ww := nextGraphemeBySegmenter(&want)
			if g != wg || gw != ww || got != want {
				t.Errorf("input %+q, step %d on %+q: nextGrapheme gives (%+q, %d) leaving %+q, the segmenter gives (%+q, %d) leaving %+q",
					input, step, before, g, gw, got, wg, ww, want)
				return false
			}
		}
		t.Errorf("input %+q: not consumed in %d steps, %+q left", input, len(input)+1, got)
		return false
	}

	t.Run("every ASCII byte alone", func(t *testing.T) {
		for a := range 0x80 {
			if !check(t, string([]byte{byte(a)})) {
				return
			}
		}
	})

	t.Run("every pair of ASCII bytes", func(t *testing.T) {
		for a := range 0x80 {
			for b := range 0x80 {
				if !check(t, string([]byte{byte(a), byte(b)})) {
					return
				}
			}
		}
	})

	t.Run("every ASCII byte before every byte from 0x80", func(t *testing.T) {
		for a := range 0x80 {
			for b := 0x80; b <= 0xff; b++ {
				pair := string([]byte{byte(a), byte(b)})
				for _, input := range []string{pair, pair + "a", pair + "\x80"} {
					if !check(t, input) {
						return
					}
				}
			}
		}
	})

	t.Run("every ASCII byte before a non-ASCII start", func(t *testing.T) {
		for a := range 0x80 {
			for _, n := range nonASCII {
				if !check(t, string([]byte{byte(a)})+n) {
					return
				}
			}
		}
	})

	t.Run("seeded random strings", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(0x6c6f6f6d, 0x67726170))
		piece := func() string {
			switch rng.IntN(3) {
			case 0:
				return string([]byte{byte(rng.IntN(0x80))})
			case 1:
				return string([]byte{byte(0x80 + rng.IntN(0x80))})
			default:
				return nonASCII[rng.IntN(len(nonASCII))]
			}
		}
		for range 20000 {
			n := 1 + rng.IntN(64)
			var b strings.Builder
			for b.Len() < n {
				p := piece()
				if b.Len()+len(p) > 64 {
					break
				}
				b.WriteString(p)
			}
			if !check(t, b.String()) {
				return
			}
		}
	})
}

// TestNextGraphemeBudget holds nextGrapheme on a line of plain ASCII to at
// most half the time the segmenter takes on it.
func TestNextGraphemeBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("timing budget")
	}
	const printable = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.,;:!?-_()[]{}<>/=+*&%$#@'\"|~^`\\"
	var line strings.Builder
	for i := 0; line.Len() < 4000; i++ {
		if i%7 == 6 {
			line.WriteByte(' ')
		} else {
			line.WriteByte(printable[i%len(printable)])
		}
	}
	text := line.String()
	sink := 0
	// consume cuts the whole line with next and reports whether it reached
	// the end within len(text)+1 steps.
	consume := func(next func(*string) (string, int)) bool {
		s := text
		for range len(text) + 1 {
			if s == "" {
				return true
			}
			_, w := next(&s)
			sink += w
		}
		return s == ""
	}
	if !consume(nextGrapheme) {
		t.Fatalf("nextGrapheme did not consume the %d-byte line in %d steps", len(text), len(text)+1)
	}
	if !consume(nextGraphemeBySegmenter) {
		t.Fatalf("the segmenter did not consume the %d-byte line in %d steps", len(text), len(text)+1)
	}
	best := func(next func(*string) (string, int)) float64 {
		var ns float64
		for i := range 2 {
			r := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					consume(next)
				}
			})
			if v := float64(r.NsPerOp()); i == 0 || v < ns {
				ns = v
			}
		}
		return ns
	}
	fast := best(nextGrapheme)
	ref := best(nextGraphemeBySegmenter)
	t.Logf("a %d-byte ASCII line: nextGrapheme %.0f ns/op, the segmenter %.0f ns/op, ratio %.2f (%d)", len(text), fast, ref, fast/ref, sink%2)
	if fast > 0.5*ref {
		t.Errorf("nextGrapheme takes %.2fx the segmenter's time on a %d-byte ASCII line, want at most 0.5x", fast/ref, len(text))
	}
}
