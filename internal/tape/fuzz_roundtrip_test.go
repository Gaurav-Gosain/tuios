package tape_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

// Fuzzing the tape recorder against the tape parser. The tape manager records
// what the user types into a tape file and plays the file back later, so the
// text a Type line carries has to come back as the text that was typed.
//
// The ways this could fail, written down before the target:
//
//  1. The recorder writes a Type line with Go's %q, which escapes anything
//     strconv does not call printable as \x, \u or \U, and the lexer does not
//     read those escapes: a zero-width joiner in an emoji, a no-break space
//     from a paste, or an escape byte comes back as the letters u200d.
//  2. A NUL in the text ends the string early, because the lexer uses NUL as
//     its end of input.
//  3. The parsed Type command's Raw, which the tape editor writes back out,
//     does not parse to the same text either.
//  4. The recorded tape has a parse error, so playback stops at that line.

func FuzzTapeRecordRoundTrip(f *testing.F) {
	for _, s := range []string{
		"hello",
		"a\tb\nc",
		`quote " and backslash \`,
		"👨‍👩‍👧",
		"no break",
		"\x1b[31mred",
		"nul\x00byte",
		"\xff\xfe",
		"`back'tick",
		"\\x41 is not an escape here",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, text string) {
		if text == "" || len(text) > 1<<12 {
			return
		}
		rec := tape.NewRecorder()
		rec.Start()
		rec.RecordType(text)
		rec.Stop()
		src := rec.String("")

		p := tape.NewParser(tape.New(src))
		cmds := p.Parse()
		if errs := p.Errors(); len(errs) > 0 {
			t.Fatalf("the recorded tape does not parse: %v\n%s", errs, src)
		}
		var typed []string
		var raws []string
		for _, c := range cmds {
			if c.Type == tape.CommandTypeType {
				typed = append(typed, c.Args...)
				raws = append(raws, c.Raw)
			}
		}
		if got := strings.Join(typed, ""); got != text {
			t.Fatalf("typed %q, the recorded tape types %q\n%s", text, got, src)
		}

		// The parser's own Raw is what the tape editor writes back.
		again := tape.NewParser(tape.New(strings.Join(raws, "\n") + "\n")).Parse()
		var retyped []string
		for _, c := range again {
			if c.Type == tape.CommandTypeType {
				retyped = append(retyped, c.Args...)
			}
		}
		if got := strings.Join(retyped, ""); got != text {
			t.Fatalf("typed %q, the tape written back from Raw types %q", text, got)
		}
	})
}
