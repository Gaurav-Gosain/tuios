package vt_test

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

func TestConform_SGR(t *testing.T) {
	runConform(t, []conformCase{
		{
			name: "SGR 21 is a double underline",
			in:   "\x1b[21mX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", underline: new(ansi.UnderlineDouble)},
			},
		},
		{
			name: "an unknown underline style draws no underline and does not reverse",
			in:   "\x1b[4:7mX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", underline: new(underlineNone), attrs: new(uint8(0))},
			},
		},
		{
			name: "a known underline style after an unknown one still applies",
			in:   "\x1b[4:7;4:3mX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", underline: new(ansi.UnderlineCurly), attrs: new(uint8(0))},
			},
		},
		{
			name: "SGR 38;0 does not reset the pen",
			in:   "\x1b[1;38;0mX",
			want: "X",
			cells: []cellWant{
				{x: 0, y: 0, content: "X", attrs: new(uint8(uv.AttrBold))},
			},
		},
	})
}
