package vt

import "testing"

// A row that fills the width and then ends with a newline looks exactly like
// a row that wrapped. Only the emulator knows which it was, and anything that
// joins rows into lines (hints, the link hover) has to ask it rather than
// guess from a full row. Runs against whichever backend the build selects.
func TestSoftWrapFlag(t *testing.T) {
	const w = 10
	cases := []struct {
		name  string
		input string
		want  []bool // rows 0.. of the screen
	}{
		{"a long line wraps", "0123456789abcde", []bool{true, false}},
		{"a full row then a newline does not", "0123456789\r\nabcde", []bool{false, false}},
		{"a line that stops short does not", "01234\r\nabcde", []bool{false, false}},
		{"a wide glyph pushed off the edge wraps", "012345678漢x", []bool{true, false}},
		{"two wraps in one line", "0123456789abcdefghijKL", []bool{true, true, false}},
		{"erasing the row drops the wrap", "0123456789abc\x1b[1;1H\x1b[2K", []bool{false, false}},
		// The rest pin what ghostty does, so the two backends agree.
		{"erasing to the end drops the wrap", "0123456789abc\x1b[1;5H\x1b[K", []bool{false, false}},
		{"erasing to the start keeps the wrap", "0123456789abc\x1b[1;5H\x1b[1K", []bool{true, false}},
		{"DCH drops the wrap", "0123456789abc\x1b[1;3H\x1b[2P", []bool{false, false}},
		{"ECH short of the end drops the wrap", "0123456789abc\x1b[1;2H\x1b[2X", []bool{false, false}},
		{"ECH to the end drops the wrap", "0123456789abc\x1b[1;5H\x1b[20X", []bool{false, false}},
		{"ICH keeps the wrap", "0123456789abc\x1b[1;3H\x1b[2@", []bool{true, false}},
		{"writing over the row keeps the wrap", "0123456789abc\x1b[1;10Hz", []bool{true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			term := New(w, 5)
			defer term.Close()
			if _, err := term.Write([]byte(c.input)); err != nil {
				t.Fatal(err)
			}
			for y, want := range c.want {
				got, known := term.RowSoftWrapped(y)
				if !known {
					t.Fatalf("row %d: the backend does not know", y)
				}
				if got != want {
					t.Errorf("row %d: soft-wrapped = %v, want %v", y, got, want)
				}
			}
		})
	}
}

// The flag scrolls into the history with its row, and a line the program
// ended with a newline keeps reading as ended there.
func TestSoftWrapFlagInScrollback(t *testing.T) {
	term := NewWithScrollback(10, 2, 100)
	defer term.Close()
	// Row one wraps into row two; row three fills the width and ends with a
	// newline; the last lines push all of it into the history.
	input := "0123456789abc\r\n" + "ABCDEFGHIJ\r\n" + "x\r\ny\r\nz"
	if _, err := term.Write([]byte(input)); err != nil {
		t.Fatal(err)
	}
	want := []bool{true, false, false}
	if n := term.ScrollbackLen(); n < len(want) {
		t.Fatalf("scrollback holds %d lines, want at least %d", n, len(want))
	}
	for i, w := range want {
		got, known := term.ScrollbackSoftWrapped(i)
		if !known {
			t.Fatalf("line %d: the backend does not know", i)
		}
		if got != w {
			t.Errorf("history line %d (%q): soft-wrapped = %v, want %v", i, term.ScrollbackLine(i).String(), got, w)
		}
	}
}
