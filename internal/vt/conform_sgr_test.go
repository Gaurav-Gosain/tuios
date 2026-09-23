package vt_test

// Conformance for SGR on the default, unthemed path.
//
// The themed path had its own reader and the unthemed path used uv.ReadStyle,
// so a case written against one said nothing about the other. These run on a
// fresh emulator with no theme, which is the path most panes take.

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestConform_SGR(t *testing.T) {
	runConform(t, []conformCase{
		{
			// ECMA-48 3rd edition and xterm's ctlseqs: "Ps = 2 1 => Doubly
			// underlined". ghostty and kitty agree. uv.ReadStyle drops it.
			name: "SGR 21 is a double underline",
			in:   "\x1b[21mX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", underline: ptr(ansi.UnderlineDouble)},
			},
		},
		{
			// There is no underline style 7. The subparameter belongs to the
			// 4 and has to be consumed with it; read on as a parameter of its
			// own it is SGR 7, and the cell turns reverse.
			name: "an unknown underline style draws no underline and does not reverse",
			in:   "\x1b[4:7mX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", underline: ptr(underlineNone), attrs: ptr(uint8(0))},
			},
		},
		{
			name: "a known underline style after an unknown one still applies",
			in:   "\x1b[4:7;4:3mX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", underline: ptr(ansi.UnderlineCurly), attrs: ptr(uint8(0))},
			},
		},
		{
			// 38;0 is the implementation defined colour type. xterm and tmux
			// consume the 0 as the type, so it is not a reset.
			name: "SGR 38;0 does not reset the pen",
			in:   "\x1b[1;38;0mX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", attrs: ptr(uint8(uv.AttrBold))},
			},
		},
	})
}
