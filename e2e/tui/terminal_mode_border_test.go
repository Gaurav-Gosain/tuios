package tuie2e

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// TestTerminalModeRecolorsTheFocusedBorder is issue #642: pressing i on an idle
// pane changed the mode pill at once, but the focused border kept the window
// mode colour until the guest drew something. The renderer reused the pane's
// cached layer, which was drawn with the old border, because nothing marked the
// pane dirty on the way into terminal mode.
//
// The pane runs an idle /bin/sh, so nothing but the mode switch can repaint
// it. Every assertion is the colour the terminal was given for the pane's top
// left corner. Both directions are checked, and the two colours have to
// differ, or the first half would pass on a theme that draws both modes alike.
//
// It runs with the shipped looks. With the pre-v0.8.0 looks the suite pins,
// something else repaints the pane on the switch and the bug does not show.
//
// Frames: before.styled.txt (window mode), terminal.styled.txt (after i) and
// after.styled.txt (after Alt+Esc), under artifactDir.
func TestTerminalModeRecolorsTheFocusedBorder(t *testing.T) {
	term, _ := start(t, startOpts{env: []string{"COLORTERM=truecolor"}, shippedLooks: true})
	waitBoot(t, term)
	newWindow(t, term)

	dir := artifactDir(t)

	// The window mode colour, read once the pane has stopped changing.
	var windowInk tuitest.Color
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		if !ok {
			return false
		}
		same := ink == windowInk
		windowInk = ink
		return same
	}, uiTimeout); err != nil {
		t.Fatalf("no settled pane corner in window mode: %v\n%s", err, term.Snapshot())
	}
	if windowInk.Kind != tuitest.ColorRGB {
		t.Fatalf("the border is not drawn in an RGB colour (%+v), so the modes cannot be told apart", windowInk)
	}
	// The pane must be quiet before i, so a late prompt redraw cannot repaint
	// the border for the mode switch.
	time.Sleep(500 * time.Millisecond)
	saveArtifact(t, term, dir, "before")

	enterTerminalMode(t, term)
	err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		return ok && ink != windowInk
	}, 3*time.Second)
	saveArtifact(t, term, dir, "terminal")
	if err != nil {
		t.Fatalf("the focused border kept the window mode colour %+v after entering terminal mode: %v\n%s",
			windowInk, err, term.SnapshotStyled())
	}
	terminalInk := borderInk(t, term)
	if terminalInk.Kind != tuitest.ColorRGB {
		t.Fatalf("the terminal mode border is not an RGB colour (%+v)", terminalInk)
	}

	leaveTerminalMode(t, term)
	err = term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		return ok && ink == windowInk
	}, 3*time.Second)
	saveArtifact(t, term, dir, "after")
	if err != nil {
		t.Fatalf("the focused border is %+v after leaving terminal mode, want the window mode colour %+v: %v\n%s",
			borderInk(t, term), windowInk, err, term.SnapshotStyled())
	}
	t.Logf("window mode border %+v, terminal mode border %+v; frames in %s", windowInk, terminalInk, dir)
}

// modeBorderInks starts tuios under base with config, opens one pane, and
// returns the focused border's colour in window mode and in terminal mode. It
// saves both frames, as text and as PNG painted with host, before it asserts
// anything, so a failing run still leaves the frames to look at.
func modeBorderInks(t *testing.T, config string, env []string, host *shot.Palette) (window, terminal tuitest.Color) {
	t.Helper()
	base := t.TempDir()
	useShippedLooks(base)
	writeConfig(t, base, config)
	term := startIn(t, base, startOpts{env: env, shippedLooks: true})
	waitBoot(t, term)
	newWindow(t, term)
	dir := artifactDir(t)

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		same := ok && ink == window
		window = ink
		return same
	}, uiTimeout); err != nil {
		t.Fatalf("no settled pane corner in window mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(500 * time.Millisecond)
	window = borderInk(t, term)
	saveArtifact(t, term, dir, "window")
	savePNG(t, term.Screen(), host, dir, "window")

	enterTerminalMode(t, term)
	// The colour may stay the same, which is what the shared key asks for, so
	// this waits for the border to settle rather than to change.
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		ink, ok := screenBorderInk(s)
		same := ok && ink == terminal
		terminal = ink
		return same
	}, uiTimeout); err != nil {
		t.Fatalf("no settled pane corner in terminal mode: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	terminal = borderInk(t, term)
	saveArtifact(t, term, dir, "terminal")
	savePNG(t, term.Screen(), host, dir, "terminal")
	t.Logf("window mode border %+v, terminal mode border %+v; frames in %s", window, terminal, dir)
	return window, terminal
}

// TestFocusedBorderColourPerMode is discussion #850: border_focused_color set
// the focused border in both modes at once, and on most themes the two default
// colours had the same lightness, so the mode was hard to read.
//
// per-mode and shared check the keys: a mode's own key wins over
// border_focused_color, and border_focused_color alone sets both modes.
// lightness checks the defaults: on no theme and on catppuccin mocha, where the
// cyan and the green sat 0.024 and 0.000 apart in OKLab lightness, the two
// borders now differ in lightness too. 16 checks that at 16 colours the modes
// still take two different slots of the terminal's palette.
func TestFocusedBorderColourPerMode(t *testing.T) {
	truecolor := []string{"TERM=xterm-256color", "COLORTERM=truecolor"}
	rgb := func(c tuitest.Color) string {
		if c.Kind != tuitest.ColorRGB {
			return fmt.Sprintf("%+v", c)
		}
		return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
	}

	t.Run("per-mode", func(t *testing.T) {
		window, terminal := modeBorderInks(t, `[appearance]
border_focused_color = "#ff00ff"
border_focused_color_window = "#ff8800"
border_focused_color_terminal = "#0088ff"
`, truecolor, hostPalette(t, ""))
		if got := rgb(window); got != "#ff8800" {
			t.Errorf("the window mode border is %s, want #ff8800 from border_focused_color_window", got)
		}
		if got := rgb(terminal); got != "#0088ff" {
			t.Errorf("the terminal mode border is %s, want #0088ff from border_focused_color_terminal", got)
		}
	})

	t.Run("shared", func(t *testing.T) {
		window, terminal := modeBorderInks(t, "[appearance]\nborder_focused_color = \"#ff00ff\"\n", truecolor, hostPalette(t, ""))
		if rgb(window) != "#ff00ff" || rgb(terminal) != "#ff00ff" {
			t.Errorf("with border_focused_color alone the borders are %s and %s, want #ff00ff in both modes",
				rgb(window), rgb(terminal))
		}
	})

	for _, look := range []struct{ name, theme string }{{"lightness-no-theme", ""}, {"lightness-catppuccin-mocha", "catppuccin_mocha"}} {
		t.Run(look.name, func(t *testing.T) {
			cfg := "[appearance]\n"
			if look.theme != "" {
				cfg += "theme = \"" + look.theme + "\"\n"
			}
			window, terminal := modeBorderInks(t, cfg, truecolor, hostPalette(t, look.theme))
			if window.Kind != tuitest.ColorRGB || terminal.Kind != tuitest.ColorRGB {
				t.Fatalf("the borders are not RGB colours: %+v and %+v", window, terminal)
			}
			lw, lt := overlay.Lightness(tuiColor(window)), overlay.Lightness(tuiColor(terminal))
			t.Logf("OKLab lightness: window %s %.3f, terminal %s %.3f", rgb(window), lw, rgb(terminal), lt)
			if math.Abs(lw-lt) < 0.08 {
				t.Errorf("the window mode border %s (L %.3f) and the terminal mode border %s (L %.3f) are under 0.08 apart in lightness",
					rgb(window), lw, rgb(terminal), lt)
			}
		})
	}

	t.Run("16", func(t *testing.T) {
		window, terminal := modeBorderInks(t, "[appearance]\n", []string{"TERM=xterm", "COLORTERM="}, hostPalette(t, ""))
		if window.Kind != tuitest.ColorIndexed || terminal.Kind != tuitest.ColorIndexed || window.Index == terminal.Index {
			t.Errorf("at 16 colours the borders are %+v and %+v, want two different palette slots", window, terminal)
		}
	})
}

// markerPaneEdge is the left border cell of the pane showing marker: the nearest
// vertical rule left of the marker on its row.
func markerPaneEdge(s tuitest.Screen, marker string) (tuitest.Cell, bool) {
	_, rows := s.Size()
	for r := range rows {
		before, _, ok := strings.Cut(s.Line(r), marker)
		if !ok {
			continue
		}
		for c := len([]rune(before)) - 1; c >= 0; c-- {
			if cell := s.Cell(c, r); cell.Content == "│" {
				return cell, true
			}
		}
	}
	return tuitest.Cell{}, false
}

// TestFocusedBorderStandsOutOnTheHostGround is the rest of discussion #850:
// on ghostty's Nord theme with no tuios theme, the focused border sat at
// nearly the lightness of the unfocused one, and with session_border on the
// focused border could not be told apart between window and terminal mode.
//
// Each border is held by OKLab lightness, the part of a colour that reads
// without telling hues apart: the two focused borders differ from each other,
// and each of them stands further from the ground than the unfocused border.
//
// Frames: <subtest>/window.png and terminal.png under artifactDir, drawn with
// the Nord palette.
func TestFocusedBorderStandsOutOnTheHostGround(t *testing.T) {
	ground := overlay.Lightness(tuiColor(tuitest.Color{Kind: tuitest.ColorRGB, R: 0x2e, G: 0x34, B: 0x40}))
	for _, sessionBorder := range []bool{false, true} {
		t.Run(fmt.Sprintf("session_border=%v", sessionBorder), func(t *testing.T) {
			term := startNordPanes(t, fmt.Sprintf("theme = \"none\"\nsession_border = %v\n", sessionBorder), false, "start")
			dir := artifactDir(t)
			// An attached client starts in terminal mode. The dock of the
			// shipped looks names no mode, so each switch is given a beat
			// rather than waited on by text.
			if err := term.SendKeys(tuitest.Alt(tuitest.Esc)); err != nil {
				t.Fatalf("send alt+esc: %v", err)
			}
			time.Sleep(800 * time.Millisecond)
			saveArtifact(t, term, dir, "window")
			savePNG(t, term.Screen(), nordPalette(), dir, "window")
			read := func(marker string) float64 {
				t.Helper()
				cell, ok := markerPaneEdge(term.Screen(), marker)
				if !ok || cell.Fg.Kind != tuitest.ColorRGB {
					t.Fatalf("no RGB border left of %s (%+v)\n%s", marker, cell.Fg, term.Snapshot())
				}
				c, _ := cellRGB(cell.Fg)
				return overlay.Lightness(c)
			}
			lw, lu := read("PLAINBX"), read("PLAINAX")

			if err := term.SendKeys("i"); err != nil {
				t.Fatalf("send i: %v", err)
			}
			time.Sleep(800 * time.Millisecond)
			saveArtifact(t, term, dir, "terminal")
			savePNG(t, term.Screen(), nordPalette(), dir, "terminal")
			lt := read("PLAINBX")
			t.Logf("OKLab lightness on a ground of %.3f: window %.3f, terminal %.3f, unfocused %.3f", ground, lw, lt, lu)

			if math.Abs(lw-lt) < 0.08 {
				t.Errorf("the window mode border (L %.3f) and the terminal mode border (L %.3f) are under 0.08 apart", lw, lt)
			}
			for _, focused := range []struct {
				mode string
				l    float64
			}{{"window", lw}, {"terminal", lt}} {
				if math.Abs(focused.l-ground)-math.Abs(lu-ground) < 0.1 {
					t.Errorf("the %s mode border (L %.3f) stands under 0.1 further from the ground (L %.3f) than the unfocused border (L %.3f)",
						focused.mode, focused.l, ground, lu)
				}
			}
		})
	}
}
