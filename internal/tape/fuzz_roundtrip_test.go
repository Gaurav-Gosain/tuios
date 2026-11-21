package tape_test

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

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
