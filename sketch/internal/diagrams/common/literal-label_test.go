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

// proportional measures i as 3, a hyphen as 4, W as 12 and every other rune as 8.
type proportional struct{}

func (proportional) Width(s string, _ float64, _ int) float64 {
	w := 0.0
	for _, r := range s {
		switch r {
		case 'i':
			w += 3
		case '-':
			w += 4
		case 'W':
			w += 12
		default:
			w += 8
		}
	}
	return w
}

func oneLine(s string) text.Label {
	return oneLineWith(tenPerRune{}, s)
}

func oneLineWith(m text.TextMeasurer, s string) text.Label {
	w := m.Width(s, 12, 0)
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
		{"a hyphen after a space is not a break", "foo -wbarbaz", 100, []string{"foo", "-wbarbaz"}},
		{"a minus sign is not a break", "x = -123456789", 100, []string{"x =", "-123456789"}},
		{"a hyphen at the start is not a break", "-abcdefghijkl", 100, []string{"-abcdefghi", "jkl"}},
		{"the issue's label with a flag", "send-agent-message -w B", 200, []string{"send-agent-message", "-w B"}},
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

// A carried line that would overflow with its next character is committed on
// its own, so no line comes out wider than the room.
func TestBreakLabelCommitsACarryThatDoesNotFit(t *testing.T) {
	got := BreakLabel(oneLineWith(proportional{}, "i-bcdefghjWWWW"), proportional{}, 71)
	want := []string{"i-", "bcdefghj", "WWWW"}
	if lines := lineTexts(got); strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines %q, want %q", lines, want)
	}
	for i, w := range got.Widths {
		if w > 71 {
			t.Errorf("line %d is %v wide, room 71", i, w)
		}
	}
}
