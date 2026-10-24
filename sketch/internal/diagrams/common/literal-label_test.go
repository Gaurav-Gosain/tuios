package common

import (
	"strings"
	"testing"
	"unicode/utf8"

	"tuios.dev/sketch/internal/text"
)

// tenPerRune measures every rune as ten units wide, so a room of n*10 holds n runes.
type tenPerRune struct{}

func (tenPerRune) Width(s string, _ float64, _ int) float64 {
	return float64(utf8.RuneCountInString(s)) * 10
}

func oneLine(s string) text.Label {
	w := tenPerRune{}.Width(s, 12, 0)
	return text.Label{
		Lines:      [][]text.Span{{{Text: s, Width: w}}},
		Widths:     []float64{w},
		Width:      w,
		Height:     18,
		Size:       12,
		LineHeight: 18,
	}
}

func lineTexts(l text.Label) []string {
	var out []string
	for _, line := range l.Lines {
		var b strings.Builder
		for _, s := range line {
			b.WriteString(s.Text)
		}
		out = append(out, b.String())
	}
	return out
}

// Issue 1032: a line wider than its room breaks at the last hyphen or space
// that fits, and inside a word only when the line has neither.
func TestBreakLabelPrefersHyphenOrSpace(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		room float64
		want []string
	}{
		{"fits", "send-agent-message", 180, []string{"send-agent-message"}},
		{"after the last hyphen that fits", "send-agent-message", 160, []string{"send-agent-", "message"}},
		{"hyphen beats an earlier space", "foo bar-bazqux", 100, []string{"foo bar-", "bazqux"}},
		{"after a space, which is dropped", "hello world", 100, []string{"hello", "world"}},
		{"inside a word with no break", "abcdefghijklmnop", 100, []string{"abcdefghij", "klmnop"}},
		{"inside a word when the hyphen does not fit", "abcdefghijkl-mn", 100, []string{"abcdefghij", "kl-mn"}},
		{"unicode hyphen", "abcd‐efghijklmn", 100, []string{"abcd‐", "efghijklmn"}},
		{"non-breaking hyphen is not a break", "abcd‑efghijklmn", 100, []string{"abcd‑efghi", "jklmn"}},
		{"every line breaks on its own", "send-agent-message read-agent-messages", 160, []string{"send-agent-", "message read-", "agent-messages"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := BreakLabel(oneLine(tc.in), tenPerRune{}, tc.room)
			if lines := lineTexts(got); strings.Join(lines, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("lines %q, want %q", lines, tc.want)
			}
			for i, w := range got.Widths {
				if want := (tenPerRune{}).Width(lineTexts(got)[i], 12, 0); w != want {
					t.Errorf("line %d width %v, want %v", i, w, want)
				}
				if w > tc.room {
					t.Errorf("line %d is %v wide, room %v", i, w, tc.room)
				}
			}
			if got.Height != float64(len(got.Lines))*got.LineHeight {
				t.Errorf("height %v for %d lines of %v", got.Height, len(got.Lines), got.LineHeight)
			}
		})
	}
}
