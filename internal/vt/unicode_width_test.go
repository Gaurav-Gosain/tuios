package vt_test

// Character width, driven by the official UAX #11 East Asian Width property
// file.
//
// Width is the property that breaks layout rather than just looking wrong. A
// character the emulator thinks is one cell wide and the renderer thinks is two
// pushes everything after it along by a column, and in a multiplexer that
// column belongs to the pane next door.
//
// See testdata/unicode/README.md for provenance and licence.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// eawRange is one line of EastAsianWidth.txt.
type eawRange struct {
	lo, hi rune
	class  string // one of A, F, H, N, Na, W
}

func loadEastAsianWidth(t *testing.T) []eawRange {
	t.Helper()
	path := filepath.Join("testdata", "unicode", "EastAsianWidth.txt")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close() //nolint:errcheck

	var out []eawRange
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) != 2 {
			continue
		}
		codes := strings.TrimSpace(fields[0])
		class := strings.TrimSpace(fields[1])
		lo, hi := codes, codes
		if i := strings.Index(codes, ".."); i >= 0 {
			lo, hi = codes[:i], codes[i+2:]
		}
		l, err := strconv.ParseUint(lo, 16, 32)
		if err != nil {
			continue
		}
		h, err := strconv.ParseUint(hi, 16, 32)
		if err != nil {
			continue
		}
		out = append(out, eawRange{lo: rune(l), hi: rune(h), class: class})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(out) < 100 {
		t.Fatalf("%s parsed to only %d ranges, which means the parser is wrong", path, len(out))
	}
	return out
}

// wantWidth is the number of cells a terminal gives a character.
//
// Wide and Fullwidth take two. Everything else takes one, Ambiguous included: a
// character is Ambiguous because its width depends on the font the reader
// happens to be using, and a multiplexer that guessed wide would misplace every
// pane boundary for every reader whose font guessed narrow. Narrow is what
// xterm, ghostty and kitty all default to.
//
// The regional indicators are the one place where East Asian Width is not the
// last word. UAX #11 calls them Neutral, but UTS #51 gives them default emoji
// presentation, and a character that renders as emoji takes two cells whatever
// its width class says. Running the sweep is what established that this is the
// only such disagreement in the whole of Unicode: 26 code points out of 147,000
// printable ones.
func wantWidth(r rune, class string) int {
	if r >= 0x1F1E6 && r <= 0x1F1FF {
		return 2
	}
	switch class {
	case "W", "F":
		return 2
	default:
		return 1
	}
}

// printableForWidth reports whether a code point is one whose cell width is
// meaningful. Zero-width categories, unassigned code points, surrogates and
// controls are all excluded: none of them is a character a guest can put in a
// cell and expect to see.
func printableForWidth(r rune) bool {
	switch {
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return false
	case unicode.Is(unicode.Cs, r), unicode.Is(unicode.Co, r), unicode.Is(unicode.Cn, r):
		return false
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Cf, r):
		return false
	case !unicode.IsPrint(r):
		return false
	}
	return true
}

// TestUnicode_CellWidthMatchesEastAsianWidth walks the whole property file and
// checks that the number of cells the emulator actually consumes for a
// character matches its East Asian Width class.
//
// It measures the emulator rather than asking a width function, because the
// question is what ends up on the grid. A width function that agrees with
// Unicode while the placement code does something else is exactly the kind of
// disagreement this is looking for.
func TestUnicode_CellWidthMatchesEastAsianWidth(t *testing.T) {
	ranges := loadEastAsianWidth(t)

	emu := vt.NewEmulator(8, 1)
	var checked, mismatched int
	byClass := map[string]int{}

	for _, rg := range ranges {
		for r := rg.lo; r <= rg.hi; r++ {
			if !printableForWidth(r) {
				continue
			}
			checked++

			if _, err := emu.WriteString("\x1b[H\x1b[2J" + string(r)); err != nil {
				t.Fatalf("write U+%04X: %v", r, err)
			}
			c := emu.CellAt(0, 0)
			if c == nil {
				t.Fatalf("U+%04X: no cell at the origin", r)
			}
			got := c.Width
			want := wantWidth(r, rg.class)
			if got != want {
				mismatched++
				byClass[rg.class]++
				if mismatched <= 25 {
					t.Errorf("U+%04X (East_Asian_Width=%s) occupies %d cells, want %d",
						r, rg.class, got, want)
				}
			}
		}
	}

	if mismatched > 0 {
		t.Errorf("%d of %d printable code points have the wrong cell width; by class: %v",
			mismatched, checked, byClass)
	}
	if checked < 50000 {
		t.Fatalf("only %d code points were checked, which means the filter is wrong", checked)
	}
	t.Logf("checked %d printable code points against UAX #11", checked)
}

// TestUnicode_MeasuringAgreesWithPlacing is the divergence that matters most in
// a multiplexer, stated directly.
//
// The emulator decides how many cells a string occupies when it places it. The
// user interface decides how wide the same string is when it lays out a title,
// a tab or a border, and it does that with ansi.StringWidth rather than by
// asking the emulator. If those two ever disagree by a column, the interface
// draws a border in a cell the text is already using, or leaves a gap where it
// expected one.
func TestUnicode_MeasuringAgreesWithPlacing(t *testing.T) {
	samples := []struct {
		name string
		s    string
	}{
		{"ascii", "hello"},
		{"cjk", "世界"},
		{"fullwidth latin", "ＡＢ"},
		{"halfwidth katakana", "ｱｲｳ"},
		{"hangul", "한글"},
		{"ambiguous latin", "¡±"},
		{"ambiguous cyrillic", "Ыф"},
		{"ambiguous dagger", "†‡"},
		{"box drawing", "─│┌┐"},
		{"block elements", "█░"},
		{"combining acute", "é"},
		{"combining stack", "é̂̃"},
		{"precomposed", "é"},
		{"zero width space", "a​b"},
		{"zero width joiner alone", "a‍b"},
		{"emoji presentation", "\U0001f600"},
		{"emoji skin tone", "\U0001f44d\U0001f3fd"},
		{"emoji zwj family", "\U0001f469‍\U0001f469‍\U0001f467"},
		{"emoji zwj profession", "\U0001f469‍\U0001f4bb"},
		{"regional indicator pair", "\U0001f1fa\U0001f1f8"},
		{"regional indicator triple", "\U0001f1fa\U0001f1f8\U0001f1fa"},
		{"text presentation selector", "❤︎"},
		{"emoji presentation selector", "❤️"},
		{"keycap", "1️⃣"},
		{"tag sequence flag", "\U0001f3f4\U000e0067\U000e0062\U000e0073\U000e0063\U000e0074\U000e007f"},
		{"mixed", "a世b\U0001f600c"},
	}

	for _, tc := range samples {
		t.Run(tc.name, func(t *testing.T) {
			placed := cellsConsumed(t, tc.s)
			measured := ansi.StringWidth(tc.s)
			if placed != measured {
				t.Errorf("the emulator puts %q in %d cells but ansi.StringWidth calls it %d wide; "+
					"anything laid out with the second and drawn with the first is off by %d columns",
					escapeRunes(tc.s), placed, measured, placed-measured)
			}
		})
	}
}

// cellsConsumed writes s to a fresh wide screen and returns how many columns it
// took up, counting each cluster once by its recorded width.
func cellsConsumed(t *testing.T, s string) int {
	t.Helper()
	emu := vt.NewEmulator(200, 1)
	if _, err := emu.WriteString(s); err != nil {
		t.Fatalf("write: %v", err)
	}
	total := 0
	for x := 0; x < emu.Width(); {
		c := emu.CellAt(x, 0)
		if c == nil {
			x++
			continue
		}
		step := c.Width
		if step < 1 {
			step = 1
		}
		if c.Content != "" && c.Content != " " {
			total += c.Width
		}
		x += step
	}
	return total
}

// TestUnicode_WideClusterSurvivesAResize checks that narrowing the screen never
// leaves half a wide character behind. A wide cluster whose second cell is cut
// away by a resize would otherwise be drawn whole by every reader, producing a
// row one column wider than the screen it came from, which in a multiplexer
// lands on the pane next door.
func TestUnicode_WideClusterSurvivesAResize(t *testing.T) {
	for cols := 2; cols <= 12; cols++ {
		t.Run(fmt.Sprintf("from-12-to-%d", cols), func(t *testing.T) {
			emu := vt.NewEmulator(12, 2)
			if _, err := emu.WriteString("世界世界世界"); err != nil {
				t.Fatalf("write: %v", err)
			}
			emu.Resize(cols, 2)

			for y := range 2 {
				for x := range cols {
					c := emu.CellAt(x, y)
					if c == nil {
						continue
					}
					if c.Width > 1 && x+c.Width > cols {
						t.Errorf("after narrowing to %d columns, cell (%d,%d) holds %q "+
							"claiming %d cells, which runs off the edge",
							cols, x, y, c.Content, c.Width)
					}
				}
			}
		})
	}
}
