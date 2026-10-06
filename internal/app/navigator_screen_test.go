package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A security boundary test: a styled capture comes from a daemon, which may
// be on another machine, and the preview writes what navParseStyled returns
// to the host terminal. The e2e test TestNavigatorPreviewIsInert covers the
// sequences a real daemon's capture carries (OSC 8). This one covers what a
// daemon that is not tuios could send.
//
// How it could pass wrongly: the output could hold no escape because it holds
// nothing. It must still hold the text and the red.
func TestNavParseStyledPassesOnlySGR(t *testing.T) {
	hostile := strings.Join([]string{
		"\x1b[31mRED\x1b[0m plain",
		"\x1b]52;c;SU5KRUNURUQ=\x07clip \x1b]2;PWNTITLE\x07title",
		"\x1b]8;;http://evil.example/\x1b\\LINK\x1b]8;;\x1b\\",
		"\x1b[2J\x1b[H\x1b[6n\x1b[?1049h\x1b[5;5r\x1b[?6hmoved",
		"bell\x07 c1\u009b31m tab\tend \x1bP+q544e\x1b\\ \x1b_Gf=100;AAAA\x1b\\",
		"bidi \u202eesrever\u202c zero\u200bwidth",
	}, "\n")
	plain, styled := navParseStyled(hostile, navInk{}, navTextLines)
	if len(styled) == 0 {
		t.Fatal("the parse returned nothing")
	}
	all := strings.Join(styled, "\n")
	if !strings.Contains(ansi.Strip(all), "RED") || !strings.Contains(all, "31") {
		t.Fatalf("the parse lost the text or the red: %q", all)
	}
	for _, bad := range []string{"evil", "PWNTITLE", "SU5KRUNURUQ", "\x07", "\u009b", "\u202e", "\u200b"} {
		if strings.Contains(all, bad) || strings.Contains(strings.Join(plain, "\n"), bad) {
			t.Fatalf("the parse let %q through: %q", bad, all)
		}
	}
	// Every sequence left is SGR.
	var state byte
	for s := all; len(s) > 0; {
		seq, width, n, newState := ansi.DecodeSequence(s, state, nil)
		state = newState
		if width == 0 && n > 0 && seq != "\n" {
			if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
				t.Fatalf("the parse let a sequence that is not SGR through: %q in %q", seq, all)
			}
		}
		s = s[n:]
	}
}

// The pure Go emulator already keeps format characters out of its cells, so
// the parse above passes without navPrintable's strip. The strip is for an
// emulator that keeps them, and is checked on its own.
func TestNavPrintableDropsFormatCharacters(t *testing.T) {
	for in, want := range map[string]string{
		"a\u202eb": "ab", // right-to-left override
		"a\u2066b": "ab", // left-to-right isolate
		"a\u200bb": "ab", // zero-width space
		"a\x07b":   "ab",
		"é":        "é",
	} {
		if got := navPrintable(in); got != want {
			t.Errorf("navPrintable(%q) = %q, want %q", in, got, want)
		}
	}
}

// A load parses every pane of a machine with one navParser, so what one
// capture leaves set must not reach the next: a pen, the alternate screen,
// a scroll region, an origin mode.
func TestNavParserKeepsNothingBetweenCaptures(t *testing.T) {
	p := newNavParser(navInk{})
	defer p.Close()
	p.parse("\x1b[31;1mRED\x1b[?1049h\x1b[2;3r\x1b[?6h\x1b[?7h", navTextLines)
	_, styled := p.parse("PLAIN", navTextLines)
	if len(styled) != 1 || styled[0] != "PLAIN" {
		t.Fatalf("the second capture parsed as %q, want PLAIN with no styling", styled)
	}
}

// navScreenLine cuts a wide glyph that would cross the edge whole, so the
// row is never wider than the preview.
func TestNavScreenLineClipsAWideGlyph(t *testing.T) {
	for _, c := range []struct {
		in   string
		cols int
	}{
		{"ab界", 3},
		{"\x1b[31mab界界\x1b[m", 5},
		{"界", 1},
	} {
		got := navScreenLine(c.in, c.cols)
		if w := ansi.StringWidth(got); w != c.cols {
			t.Errorf("navScreenLine(%q, %d) is %d cells wide: %q", c.in, c.cols, w, got)
		}
	}
}
