package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// appearance.window_mode_indicator, read where a host terminal sees it: the
// colour of a marker the shell printed and of the focused pane's border, in
// terminal mode and then in window mode. Discussion 2249 asked for a window
// mode that cannot be missed: people came back from another window and typed
// into the wrong mode.

// wmiConfig is a tiled catppuccin_mocha session with the indicator set.
func wmiConfig(kind string) string {
	return "[appearance]\ntheme = \"catppuccin_mocha\"\nwindow_mode_indicator = \"" + kind + "\"\n[startup]\ntiled = true\n"
}

// wmiCells are the marker's first cell and the border cell left of it.
func wmiCells(s tuitest.Screen) (marker, border tuitest.Cell, ok bool) {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if before, _, found := strings.Cut(line, "WMIX"); found {
			col := len([]rune(before))
			if col == 0 {
				return tuitest.Cell{}, tuitest.Cell{}, false
			}
			return s.Cell(col, r), s.Cell(col-1, r), true
		}
	}
	return tuitest.Cell{}, tuitest.Cell{}, false
}

// dockPill is the ground of the dock's mode pill: the first cell of the last
// row that has a background.
func dockPill(s tuitest.Screen) tuitest.Cell {
	cols, rows := s.Size()
	for c := range cols {
		if cell := s.Cell(c, rows-1); cell.Bg != (tuitest.Color{}) {
			return cell
		}
	}
	return tuitest.Cell{}
}

// wmiStart boots kind, prints the marker in terminal mode, and returns the
// terminal and the marker and border cells in terminal mode.
func wmiStart(t *testing.T, kind string, env []string) (*tuitest.Terminal, tuitest.Cell, tuitest.Cell) {
	t.Helper()
	base := spotlightConfigFile(t, wmiConfig(kind))
	term := startIn(t, base, startOpts{cols: 120, rows: 30, env: env})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, `printf '\033[38;2;220;220;220m%s\033[0m\n' "WMI""X"`, "WMIX", shellTimeout)
	marker, border, ok := wmiCells(term.Screen())
	if !ok {
		t.Fatalf("no marker on screen\n%s", term.Snapshot())
	}
	return term, marker, border
}

func TestWindowModeIndicator(t *testing.T) {
	for _, depth := range []struct {
		name string
		env  []string
	}{
		{"truecolor", []string{"COLORTERM=truecolor"}},
		{"256", []string{"TERM=xterm-256color", "COLORTERM="}},
		{"16", []string{"TERM=xterm", "COLORTERM="}},
	} {
		t.Run(depth.name, func(t *testing.T) { windowModeIndicatorAt(t, depth.name, depth.env) })
	}
}

// windowModeIndicatorAt runs the three indicators at one colour depth.
func windowModeIndicatorAt(t *testing.T, depth string, env []string) {
	t.Run("bar", func(t *testing.T) {
		term, marker, _ := wmiStart(t, "bar", env)
		leaveTerminalMode(t, term)
		stayTrue(t, "the marker keeps its colour in window mode under bar", time.Second, func() (bool, string) {
			m, _, ok := wmiCells(term.Screen())
			return ok && m.Fg == marker.Fg, "the marker's ink"
		})
		_, border, _ := wmiCells(term.Screen())
		if pill := dockPill(term.Screen()); border.Fg == pill.Bg {
			t.Errorf("under bar the focused border wears the pill colour %v; the control is void", pill.Bg)
		}
		saveFrame(t, term, "window-mode-indicator-bar-"+depth)
		savePNG(t, term.Screen(), mochaScheme.palette(), artifactDir(t), "window-mode-indicator-bar-"+depth)
	})

	t.Run("border", func(t *testing.T) {
		term, _, termBorder := wmiStart(t, "border", env)
		leaveTerminalMode(t, term)
		waitUntil(t, "the focused border to wear the mode pill's window mode colour", uiTimeout, func() (bool, string) {
			_, border, ok := wmiCells(term.Screen())
			pill := dockPill(term.Screen())
			return ok && pill.Bg != (tuitest.Color{}) && border.Fg == pill.Bg, "border and pill"
		})
		saveFrame(t, term, "window-mode-indicator-border-"+depth)
		savePNG(t, term.Screen(), mochaScheme.palette(), artifactDir(t), "window-mode-indicator-border-"+depth)
		enterTerminalMode(t, term)
		waitUntil(t, "the terminal mode border back", uiTimeout, func() (bool, string) {
			_, border, ok := wmiCells(term.Screen())
			return ok && border.Fg == termBorder.Fg, "border"
		})
	})

	t.Run("tint", func(t *testing.T) {
		term, marker, _ := wmiStart(t, "tint", env)
		leaveTerminalMode(t, term)
		waitUntil(t, "the marker to dim in window mode", uiTimeout, func() (bool, string) {
			m, _, ok := wmiCells(term.Screen())
			return ok && (m.Fg != marker.Fg || m.Faint && !marker.Faint), "the marker's ink"
		})
		saveFrame(t, term, "window-mode-indicator-tint-"+depth)
		savePNG(t, term.Screen(), mochaScheme.palette(), artifactDir(t), "window-mode-indicator-tint-"+depth)
		enterTerminalMode(t, term)
		waitUntil(t, "the marker's own colour back in terminal mode", uiTimeout, func() (bool, string) {
			m, _, ok := wmiCells(term.Screen())
			return ok && m.Fg == marker.Fg && m.Faint == marker.Faint, "the marker's ink"
		})
	})
}
