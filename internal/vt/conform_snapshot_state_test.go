package vt_test

// Conformance for the state a snapshot carries that no cell shows and that a
// screen dump cannot see: the tab stops, the titles and the title stack, the
// colours a guest set with OSC 4, 10, 11 and 12, the ANSI modes, and the input
// the parser is part way through. Every case runs on the backend the test
// binary was built with, so `go test -tags ghostty` runs them on libghostty.
//
// Ways this can go wrong, each a group of cases below:
//   - the tab stop table is read wrong after HTS, TBC 0, TBC 3, DECST8C, RIS
//     or a resize that changes the width;
//   - RestoreTabStops does not replace the table, keeps a column past the
//     right edge, or a tab after it does not stop where the table says;
//   - the title stack loses an entry, the icon name, or which of the two an
//     entry saved, or keeps more than ten entries;
//   - RestoreTitles does not put the stack back, so a pop after it restores
//     nothing;
//   - a colour the guest reset with OSC 104, 110, 111 or 112 is still
//     reported as set, or a restored palette entry does not paint what the
//     guest prints next;
//   - an ANSI mode is restored in the mode table and not in the cached flag
//     the print path reads, so insert mode and newline mode do nothing;
//   - PendingInput holds bytes from before the sequence started, which a
//     replay would paint twice;
//   - PendingInput holds a control the parser already carried out inside the
//     sequence, which a replay would carry out twice;
//   - PendingInput misses part of the sequence, so the rest prints as text;
//   - RestorePendingInput keeps an unfinished sequence the emulator was in,
//     so what comes next lands inside it.

import (
	"image/color"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

func newStateEmulator(t *testing.T, cols, rows int) vt.Terminal {
	t.Helper()
	term := vt.New(cols, rows)
	t.Cleanup(func() { _ = term.Close() })
	return term
}

func feed(t *testing.T, term vt.Terminal, s string) {
	t.Helper()
	if _, err := term.Write([]byte(s)); err != nil {
		t.Fatalf("write %q: %v", s, err)
	}
}

func TestSnapshotState_TabStops(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		resize int // a width to resize to after the input, or 0
		want   []int
	}{
		{name: "the default table", want: []int{0, 8, 16, 24}},
		{name: "TBC 3 clears every stop", in: "\x1b[3g", want: []int{}},
		{name: "HTS sets stops at the cursor", in: "\x1b[3g\x1b[1;5H\x1bH\x1b[1;12H\x1bH", want: []int{4, 11}},
		{name: "TBC 0 clears the stop under the cursor", in: "\x1b[1;17H\x1b[0g", want: []int{0, 8, 24}},
		{name: "DECST8C puts the default table back", in: "\x1b[3g\x1b[?5W", want: []int{0, 8, 16, 24}},
		{name: "RIS puts the default table back", in: "\x1b[3g\x1b[1;3H\x1bH\x1bc", want: []int{0, 8, 16, 24}},
		{name: "a new width puts the default table back", in: "\x1b[3g\x1b[1;3H\x1bH", resize: 40, want: []int{0, 8, 16, 24, 32}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := newStateEmulator(t, 30, 4)
			feed(t, term, tc.in)
			if tc.resize > 0 {
				term.Resize(tc.resize, 4)
			}
			if got := term.TabStops(); !slices.Equal(got, tc.want) {
				t.Errorf("tab stops %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("a restore replaces the table and a tab stops on it", func(t *testing.T) {
		term := newStateEmulator(t, 30, 4)
		term.RestoreTabStops([]int{3, 7, 29, 30, 45, -1})
		if got, want := term.TabStops(), []int{3, 7, 29}; !slices.Equal(got, want) {
			t.Fatalf("tab stops after the restore %v, want %v", got, want)
		}
		feed(t, term, "\tA\tB\tC")
		got := []string{term.CellAt(3, 0).Content, term.CellAt(7, 0).Content, term.CellAt(29, 0).Content}
		if !slices.Equal(got, []string{"A", "B", "C"}) {
			t.Errorf("cells at the restored stops %q, want A B C", got)
		}
	})

	t.Run("a restore with no stops clears the table", func(t *testing.T) {
		term := newStateEmulator(t, 30, 4)
		term.RestoreTabStops(nil)
		if got := term.TabStops(); len(got) != 0 {
			t.Fatalf("tab stops after an empty restore %v, want none", got)
		}
		feed(t, term, "\tZ")
		if c := term.CellAt(29, 0).Content; c != "Z" {
			t.Errorf("a tab with no stops did not go to the last column: cell 29 is %q", c)
		}
	})
}

func TestSnapshotState_Titles(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want vt.Titles
	}{
		{name: "OSC 2 sets the title", in: "\x1b]2;one\x07", want: vt.Titles{Title: "one"}},
		{name: "OSC 0 sets both", in: "\x1b]0;both\x07", want: vt.Titles{Title: "both", Icon: "both"}},
		{name: "OSC 1 sets the icon name", in: "\x1b]1;ic\x07", want: vt.Titles{Icon: "ic"}},
		{
			name: "XTWINOPS 22 saves both",
			in:   "\x1b]2;shell\x07\x1b[22;0t\x1b]2;vim\x07",
			want: vt.Titles{Title: "vim", Stack: []vt.TitleEntry{{Title: "shell", HasTitle: true, HasIcon: true}}},
		},
		{
			name: "XTWINOPS 22;1 saves the icon name only",
			in:   "\x1b]0;both\x07\x1b[22;1t",
			want: vt.Titles{Title: "both", Icon: "both", Stack: []vt.TitleEntry{{Title: "both", Icon: "both", HasIcon: true}}},
		},
		{
			name: "XTWINOPS 22;2 saves the title only",
			in:   "\x1b]0;both\x07\x1b[22;2t",
			want: vt.Titles{Title: "both", Icon: "both", Stack: []vt.TitleEntry{{Title: "both", Icon: "both", HasTitle: true}}},
		},
		{
			name: "XTWINOPS 23 puts the saved title back",
			in:   "\x1b]2;shell\x07\x1b[22t\x1b]2;vim\x07\x1b[23t",
			want: vt.Titles{Title: "shell"},
		},
		{
			name: "XTWINOPS 23;2 leaves the icon name alone",
			in:   "\x1b]0;a\x07\x1b[22t\x1b]0;b\x07\x1b[23;2t",
			want: vt.Titles{Title: "a", Icon: "b"},
		},
		{
			name: "the stack keeps the ten newest entries",
			in:   strings.Repeat("\x1b[22;2t", 11),
			want: vt.Titles{Stack: slices.Repeat([]vt.TitleEntry{{HasTitle: true}}, 10)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := newStateEmulator(t, 20, 4)
			feed(t, term, tc.in)
			if got := term.Titles(); !titlesEqual(got, tc.want) {
				t.Errorf("titles %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("RIS empties the stack", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		feed(t, term, "\x1b[22t\x1b[22t\x1bc")
		if got := term.Titles().Stack; len(got) != 0 {
			t.Errorf("stack after RIS %+v, want empty", got)
		}
	})

	t.Run("a restore puts the stack back and a pop reads it", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		want := vt.Titles{
			Title: "now", Icon: "nowicon",
			Stack: []vt.TitleEntry{
				{Title: "first", HasTitle: true},
				{Title: "second", Icon: "secondicon", HasTitle: true, HasIcon: true},
			},
		}
		term.RestoreTitles(want)
		if got := term.Titles(); !titlesEqual(got, want) {
			t.Fatalf("titles after the restore %+v, want %+v", got, want)
		}
		feed(t, term, "\x1b[23t")
		if got, w := term.Titles(), (vt.Titles{Title: "second", Icon: "secondicon", Stack: want.Stack[:1]}); !titlesEqual(got, w) {
			t.Fatalf("titles after one pop %+v, want %+v", got, w)
		}
		feed(t, term, "\x1b[23t")
		if got, w := term.Titles(), (vt.Titles{Title: "first", Icon: "secondicon"}); !titlesEqual(got, w) {
			t.Errorf("titles after two pops %+v, want %+v", got, w)
		}
	})

	t.Run("a restore keeps the ten newest entries", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		var stack []vt.TitleEntry
		for i := range 12 {
			stack = append(stack, vt.TitleEntry{Title: string(rune('a' + i)), HasTitle: true})
		}
		term.RestoreTitles(vt.Titles{Stack: stack})
		got := term.Titles().Stack
		if len(got) != 10 || got[0].Title != "c" || got[9].Title != "l" {
			t.Errorf("stack after restoring twelve entries %+v, want c to l", got)
		}
	})
}

func titlesEqual(a, b vt.Titles) bool {
	return a.Title == b.Title && a.Icon == b.Icon && slices.Equal(a.Stack, b.Stack)
}

func rgb(r, g, b uint8) color.Color { return color.RGBA{R: r, G: g, B: b, A: 0xff} }

func TestSnapshotState_GuestColors(t *testing.T) {
	red, green, blue := rgb(0xff, 0, 0), rgb(0, 0xff, 0), rgb(0, 0, 0xff)
	cases := []struct {
		name    string
		in      string
		palette map[int]color.Color
		fg, bg  color.Color
		cursor  color.Color
	}{
		{name: "nothing set"},
		{name: "OSC 4 sets a slot", in: "\x1b]4;1;rgb:ff/00/00\x07", palette: map[int]color.Color{1: red}},
		{name: "OSC 4 sets a slot past sixteen", in: "\x1b]4;200;#00ff00\x07", palette: map[int]color.Color{200: green}},
		{name: "OSC 104 resets one slot", in: "\x1b]4;1;#ff0000\x07\x1b]4;2;#00ff00\x07\x1b]104;1\x07", palette: map[int]color.Color{2: green}},
		{name: "OSC 104 resets every slot", in: "\x1b]4;1;#ff0000\x07\x1b]4;2;#00ff00\x07\x1b]104\x07"},
		{name: "OSC 10 11 12 set the defaults", in: "\x1b]10;#ff0000\x07\x1b]11;#00ff00\x07\x1b]12;#0000ff\x07", fg: red, bg: green, cursor: blue},
		{name: "OSC 10 sets the next ones too", in: "\x1b]10;#ff0000;#00ff00\x07", fg: red, bg: green},
		{name: "OSC 110 111 112 reset them", in: "\x1b]10;#ff0000\x07\x1b]11;#00ff00\x07\x1b]12;#0000ff\x07\x1b]110\x07\x1b]111\x07\x1b]112\x07"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := newStateEmulator(t, 20, 4)
			feed(t, term, tc.in)
			got := term.GuestColors()
			for i := range got.Palette {
				if !sameColor(got.Palette[i], tc.palette[i]) {
					t.Errorf("palette slot %d is %v, want %v", i, got.Palette[i], tc.palette[i])
				}
			}
			if !sameColor(got.Fg, tc.fg) || !sameColor(got.Bg, tc.bg) || !sameColor(got.Cursor, tc.cursor) {
				t.Errorf("defaults fg %v bg %v cursor %v, want %v %v %v", got.Fg, got.Bg, got.Cursor, tc.fg, tc.bg, tc.cursor)
			}
		})
	}

	t.Run("a restore replaces them and paints the next print", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		feed(t, term, "\x1b]4;3;#00ff00\x07\x1b]10;#00ff00\x07")
		var want vt.GuestColors
		want.Palette[5] = red
		want.Bg = blue
		term.RestoreGuestColors(want)
		got := term.GuestColors()
		for i := range got.Palette {
			if !sameColor(got.Palette[i], want.Palette[i]) {
				t.Errorf("palette slot %d after the restore is %v, want %v", i, got.Palette[i], want.Palette[i])
			}
		}
		if got.Fg != nil || !sameColor(got.Bg, blue) || got.Cursor != nil {
			t.Errorf("defaults after the restore fg %v bg %v cursor %v, want nil blue nil", got.Fg, got.Bg, got.Cursor)
		}
		feed(t, term, "\x1b[35mX")
		if c := term.CellAt(0, 0); c == nil || !sameColor(c.Style.Fg, red) {
			t.Errorf("SGR 35 after restoring slot 5 painted %v, want the restored red", c)
		}
	})
}

func TestSnapshotState_ANSIModes(t *testing.T) {
	t.Run("insert and newline mode are reported", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		if m := term.ANSIModes(); m[4] || m[20] {
			t.Fatalf("a fresh emulator reports IRM %v LNM %v, want both off", m[4], m[20])
		}
		feed(t, term, "\x1b[4h\x1b[20h")
		if m := term.ANSIModes(); !m[4] || !m[20] {
			t.Errorf("after 4h and 20h the emulator reports IRM %v LNM %v, want both on", m[4], m[20])
		}
	})

	t.Run("restored insert mode inserts", func(t *testing.T) {
		// The restore comes first: on the libghostty backend a restore is
		// replayed from a reset, which clears the screen.
		term := newStateEmulator(t, 20, 4)
		term.RestoreANSIModes(map[int]bool{4: true})
		feed(t, term, "abc\rX")
		if got := rowText(term, 0); got != "Xabc" {
			t.Errorf("row after restoring IRM and printing X is %q, want Xabc", got)
		}
	})

	t.Run("restored newline mode returns the carriage", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		term.RestoreANSIModes(map[int]bool{20: true})
		feed(t, term, "a\nb")
		if got := rowText(term, 1); got != "b" {
			t.Errorf("row 1 after restoring LNM and sending a LF b is %q, want b", got)
		}
	})

	t.Run("a mode the emulator does not have is not taken", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		term.RestoreANSIModes(map[int]bool{9999: true})
		if _, ok := term.ANSIModes()[9999]; ok {
			t.Error("restoring an unknown ANSI mode put it in the table")
		}
	})
}

func rowText(term vt.Terminal, y int) string {
	var b strings.Builder
	for x := range term.Width() {
		c := term.CellAt(x, y)
		if c == nil || c.Content == "" {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(c.Content)
	}
	return strings.TrimRight(b.String(), " ")
}

func TestSnapshotState_PendingInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "ground", in: "abc", want: ""},
		{name: "a complete sequence", in: "\x1b[1mabc\x1b[0m", want: ""},
		{name: "a CSI cut in its parameters", in: "ab\x1b[1;3", want: "\x1b[1;3"},
		{name: "a control inside the CSI is not kept", in: "\x1b[1\n;2", want: "\x1b[1;2"},
		{name: "a CSI cut after its introducer", in: "\x1b[1m\x1b[", want: "\x1b["},
		{name: "an ESC alone", in: "x\x1b", want: "\x1b"},
		{name: "a charset designation cut", in: "x\x1b(", want: "\x1b("},
		{name: "an OSC cut in its payload", in: "x\x1b]2;tit", want: "\x1b]2;tit"},
		{name: "an OSC 8 link cut", in: "\x1b]8;;https://e.exa", want: "\x1b]8;;https://e.exa"},
		{name: "a UTF-8 character cut", in: "a\xe6\x97", want: "\xe6\x97"},
		{name: "a DCS cut", in: "a\x1bP$q", want: "\x1bP$q"},
		{name: "an APC cut", in: "a\x1b_Gf=1;", want: "\x1b_Gf=1;"},
		{name: "CAN ends the sequence", in: "\x1b[1\x18x", want: ""},
		{name: "a sequence started over", in: "\x1b[12\x1b[3", want: "\x1b[3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			term := newStateEmulator(t, 20, 4)
			feed(t, term, tc.in)
			if got := string(term.PendingInput()); got != tc.want {
				t.Errorf("pending input %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("a restored sequence is finished by what follows", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		term.RestorePendingInput([]byte("\x1b]2;ti"))
		feed(t, term, "tle\x07X")
		if got := term.Titles().Title; got != "title" {
			t.Errorf("title %q, want title", got)
		}
		if got := rowText(term, 0); got != "X" {
			t.Errorf("row 0 %q, want X", got)
		}
	})

	t.Run("a restore drops the sequence the emulator was in", func(t *testing.T) {
		for _, cut := range []string{"\x1b]2;abc", "\x1b[12", "\x1bP$q", "\x1b_Gx", "\x1b"} {
			term := newStateEmulator(t, 20, 4)
			feed(t, term, cut)
			term.RestorePendingInput(nil)
			feed(t, term, "XY")
			if got := rowText(term, 0); got != "XY" {
				t.Errorf("after %q and an empty restore, row 0 is %q, want XY", cut, got)
			}
			if p := term.PendingInput(); len(p) != 0 {
				t.Errorf("after %q and an empty restore, pending input is %q", cut, p)
			}
		}
	})

	t.Run("pending input is cut at the sequence limit", func(t *testing.T) {
		term := newStateEmulator(t, 20, 4)
		feed(t, term, "\x1b]2;"+strings.Repeat("a", 5<<20))
		if n := len(term.PendingInput()); n > 4<<20+4096 {
			t.Errorf("pending input holds %d bytes of one OSC, over the sequence limit", n)
		}
	})
}
