package vt_test

import (
	"image/color"
	"regexp"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A program asks the terminal what its background is with OSC 11, and its
// default text colour with OSC 10. With a pane background painted, tuios tells
// the emulator the painted pair through SetReportColors, and the answer has to
// be that pair, on whichever backend this binary was built with. These run
// under the pure Go emulator by default and under libghostty-vt with -tags
// ghostty.

// oscAnswer writes in to t and returns the rgb: value of the first OSC 10 or
// 11 answer the emulator wrote back, or "" when it wrote none.
func oscAnswer(tb testing.TB, term vt.Terminal, in string) string {
	tb.Helper()
	if _, err := term.Write([]byte(in)); err != nil {
		tb.Fatalf("write %q: %v", in, err)
	}
	got := make(chan string, 1)
	go func() {
		buf := make([]byte, 512)
		n, _ := term.Read(buf)
		got <- string(buf[:n])
	}()
	select {
	case s := <-got:
		if m := oscRGB.FindStringSubmatch(s); m != nil {
			return m[1]
		}
		tb.Fatalf("the reply %q carries no colour", s)
	case <-time.After(2 * time.Second):
	}
	return ""
}

var oscRGB = regexp.MustCompile(`\x1b\]1[01];(rgb:[0-9a-fA-F/]+)`)

const (
	queryFg = "\x1b]10;?\x1b\\"
	queryBg = "\x1b]11;?\x1b\\"
)

func TestReportColorsAnswerOSC10And11(t *testing.T) {
	painted := color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xff}
	ink := color.RGBA{R: 0xee, G: 0xdd, B: 0xcc, A: 0xff}
	themeBg := color.RGBA{R: 0x1e, G: 0x1e, B: 0x2e, A: 0xff}
	themeFg := color.RGBA{R: 0xcd, G: 0xd6, B: 0xf4, A: 0xff}

	t.Run("the painted pair answers", func(t *testing.T) {
		term := vt.New(20, 4)
		t.Cleanup(func() { _ = term.Close() })
		term.SetThemeColors(themeFg, themeBg, nil, [16]color.Color{})
		term.SetReportColors(ink, painted)
		if got := oscAnswer(t, term, queryBg); got != "rgb:1212/3434/5656" {
			t.Errorf("OSC 11 answered %q, want the painted ground rgb:1212/3434/5656", got)
		}
		if got := oscAnswer(t, term, queryFg); got != "rgb:eeee/dddd/cccc" {
			t.Errorf("OSC 10 answered %q, want the painted ink rgb:eeee/dddd/cccc", got)
		}
	})

	t.Run("nil keeps the theme's answer", func(t *testing.T) {
		term := vt.New(20, 4)
		t.Cleanup(func() { _ = term.Close() })
		term.SetThemeColors(themeFg, themeBg, nil, [16]color.Color{})
		term.SetReportColors(nil, nil)
		if got := oscAnswer(t, term, queryBg); got != "rgb:1e1e/1e1e/2e2e" {
			t.Errorf("OSC 11 answered %q, want the theme's rgb:1e1e/1e1e/2e2e", got)
		}
		if got := oscAnswer(t, term, queryFg); got != "rgb:cdcd/d6d6/f4f4" {
			t.Errorf("OSC 10 answered %q, want the theme's rgb:cdcd/d6d6/f4f4", got)
		}
	})

	t.Run("a painted ground with the host's ink", func(t *testing.T) {
		// A colour of the user's with no theme paints the ground and leaves
		// the text to the host, so only OSC 11 changes.
		term := vt.New(20, 4)
		t.Cleanup(func() { _ = term.Close() })
		before := oscAnswer(t, term, queryFg)
		term.SetReportColors(nil, painted)
		if got := oscAnswer(t, term, queryBg); got != "rgb:1212/3434/5656" {
			t.Errorf("OSC 11 answered %q, want rgb:1212/3434/5656", got)
		}
		if got := oscAnswer(t, term, queryFg); got != before {
			t.Errorf("OSC 10 answered %q, want the unchanged %q", got, before)
		}
	})

	t.Run("the guest's own colour wins, and a reset gives the painted one back", func(t *testing.T) {
		term := vt.New(20, 4)
		t.Cleanup(func() { _ = term.Close() })
		term.SetReportColors(ink, painted)
		// The program sets its own background, then asks.
		if got := oscAnswer(t, term, "\x1b]11;rgb:ff/00/00\x1b\\"+queryBg); got != "rgb:ffff/0000/0000" {
			t.Errorf("after OSC 11 set, the query answered %q, want the guest's rgb:ffff/0000/0000", got)
		}
		if got := oscAnswer(t, term, "\x1b]111\x1b\\"+queryBg); got != "rgb:1212/3434/5656" {
			t.Errorf("after OSC 111, the query answered %q, want the painted rgb:1212/3434/5656", got)
		}
	})

	t.Run("switching the paint off puts the default back", func(t *testing.T) {
		term := vt.New(20, 4)
		t.Cleanup(func() { _ = term.Close() })
		before := oscAnswer(t, term, queryBg)
		term.SetReportColors(ink, painted)
		term.SetReportColors(nil, nil)
		if got := oscAnswer(t, term, queryBg); got != before {
			t.Errorf("OSC 11 answered %q after the paint was switched off, want the original %q", got, before)
		}
	})
	t.Logf("ran on the %s backend", vt.Backend)
}

// An OSC 4 query for one of the sixteen is answered with the host terminal's
// own colour for the slot when tuios knows it (SetReportPalette), on either
// backend. The E2E test of host colours runs the pure emulator in the daemon
// only, so the libghostty-vt backend is held to it here.
//
// The ways it could go wrong, written down before the cases:
//   - a slot the guest set with OSC 4 must keep the guest's answer;
//   - a slot the theme sets must keep the theme's answer, since that is the
//     colour the slot is drawn in;
//   - a nil entry must keep the xterm default rather than answer black;
//   - OSC 104 must bring the reported colour back, not the xterm default;
//   - a slot past 15 must not be touched.
func TestReportPaletteAnswersOSC4(t *testing.T) {
	hostRed := color.RGBA{R: 0xc4, G: 0x1a, B: 0x16, A: 0xff}
	themeRed := color.RGBA{R: 0xf3, G: 0x8b, B: 0xa8, A: 0xff}
	var pal [16]color.Color
	pal[1] = hostRed

	answer := func(t *testing.T, term vt.Terminal, in string) string {
		t.Helper()
		if _, err := term.Write([]byte(in)); err != nil {
			t.Fatalf("write %q: %v", in, err)
		}
		got := make(chan string, 1)
		go func() {
			buf := make([]byte, 512)
			n, _ := term.Read(buf)
			got <- string(buf[:n])
		}()
		select {
		case s := <-got:
			if m := osc4RGB.FindStringSubmatch(s); m != nil {
				return m[1]
			}
			t.Fatalf("the reply %q carries no colour", s)
		case <-time.After(2 * time.Second):
		}
		return ""
	}
	fresh := func(t *testing.T) vt.Terminal {
		term := vt.New(20, 4)
		t.Cleanup(func() { _ = term.Close() })
		return term
	}

	t.Run("the host's slot answers", func(t *testing.T) {
		term := fresh(t)
		term.SetReportPalette(pal)
		if got := answer(t, term, "\x1b]4;1;?\x1b\\"); got != "rgb:c4c4/1a1a/1616" {
			t.Errorf("OSC 4;1 answered %q, want the host's rgb:c4c4/1a1a/1616", got)
		}
	})
	t.Run("a nil slot keeps the default", func(t *testing.T) {
		term := fresh(t)
		before := answer(t, term, "\x1b]4;2;?\x1b\\")
		term.SetReportPalette(pal)
		if got := answer(t, term, "\x1b]4;2;?\x1b\\"); got != before || got == "" {
			t.Errorf("OSC 4;2 answered %q, want the unchanged %q", got, before)
		}
	})
	t.Run("the guest's own slot wins and a reset gives the host's back", func(t *testing.T) {
		term := fresh(t)
		term.SetReportPalette(pal)
		if got := answer(t, term, "\x1b]4;1;rgb:00/ff/00\x1b\\\x1b]4;1;?\x1b\\"); got != "rgb:0000/ffff/0000" {
			t.Errorf("after OSC 4 set, the query answered %q, want the guest's rgb:0000/ffff/0000", got)
		}
		if got := answer(t, term, "\x1b]104;1\x1b\\\x1b]4;1;?\x1b\\"); got != "rgb:c4c4/1a1a/1616" {
			t.Errorf("after OSC 104, the query answered %q, want the host's rgb:c4c4/1a1a/1616", got)
		}
	})
	t.Run("the theme's slot wins", func(t *testing.T) {
		term := fresh(t)
		var theme [16]color.Color
		theme[1] = themeRed
		term.SetThemeColors(color.White, color.Black, nil, theme)
		term.SetReportPalette(pal)
		if got := answer(t, term, "\x1b]4;1;?\x1b\\"); got != "rgb:f3f3/8b8b/a8a8" {
			t.Errorf("OSC 4;1 answered %q, want the theme's rgb:f3f3/8b8b/a8a8", got)
		}
	})
	t.Run("a slot past fifteen is untouched", func(t *testing.T) {
		term := fresh(t)
		before := answer(t, term, "\x1b]4;17;?\x1b\\")
		term.SetReportPalette(pal)
		if got := answer(t, term, "\x1b]4;17;?\x1b\\"); got != before {
			t.Errorf("OSC 4;17 answered %q, want the unchanged %q", got, before)
		}
	})
	t.Logf("ran on the %s backend", vt.Backend)
}

var osc4RGB = regexp.MustCompile(`\x1b\]4;\d+;(rgb:[0-9a-fA-F/]+)`)
