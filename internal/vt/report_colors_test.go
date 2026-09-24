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
