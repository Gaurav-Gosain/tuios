package main

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/invisible"
)

// plainText strips control characters from text another program wrote, so a
// body that carries an escape sequence cannot reach the terminal this prints
// to. Invisible characters go too (invisible.Strip): they would let a body
// read as something other than what it holds. Newlines and tabs stay: they are layout, and the fence around the body
// is what says the layout is the sender's.
func plainText(s string) string {
	s = invisible.Strip(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7f && r < 0xa0):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// plainLine is plainText for a value that must stay on one line.
func plainLine(s string) string {
	return strings.NewReplacer("\n", " ", "\t", " ").Replace(plainText(s))
}
