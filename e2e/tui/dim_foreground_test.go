package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// dim_foreground is a colour transform, so it is read where a host terminal
// would see it: the fg and bg of a marker the shell printed, in a pane that is
// not focused, against the same marker in the pane that is.
//
// Both markers carry a colour of their own in both layers. A cell that names
// none is dimmed toward the theme's ground whether or not the option is set,
// and would not tell its two values apart.

// dimUnfocusedConfig is a tiled catppuccin_mocha session with dim_unfocused
// on and extra appended to [appearance].
func dimUnfocusedConfig(extra string) string {
	return "[appearance]\ntheme = \"catppuccin_mocha\"\ndim_unfocused = 60\n" + extra +
		"\n[startup]\ntiled = true\n"
}

// markerCell is the first cell of marker on screen.
func markerCell(s tuitest.Screen, marker string) (tuitest.Cell, bool) {
	_, rows := s.Size()
	for r := range rows {
		line := s.Line(r)
		if i := strings.Index(line, marker); i >= 0 {
			return s.Cell(len([]rune(line[:i])), r), true
		}
	}
	return tuitest.Cell{}, false
}

// dimPair boots tuios with body, prints a marker in each of two panes, and
// returns the marker's cell in the unfocused pane (A) and in the focused one
// (B) once A has been dimmed enough to differ from B. A frame in which A is
// still drawn like B has not been dimmed yet, and reading it would read the
// state before the option applied.
func dimPair(t *testing.T, body string) (unfocused, focused tuitest.Cell) {
	t.Helper()
	base := spotlightConfigFile(t, body)
	// True colour is asked for: the default host here is a 256-colour one, which
	// snaps colours to palette entries and could blur a small dim into none.
	term := startIn(t, base, startOpts{cols: 140, rows: 40, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)

	// The marker is assembled by the shell so the command line does not hold
	// it, in a colour on a ground of its own.
	for _, name := range []string{"A", "B"} {
		newWindow(t, term)
		enterTerminalMode(t, term)
		runInShell(t, term,
			`printf '\033[38;2;220;220;220;48;2;40;80;120m%s\033[0m\n' "DIM`+name+`""X"`,
			"DIM"+name+"X", shellTimeout)
		leaveTerminalMode(t, term)
	}

	if err := term.WaitFor(func(s tuitest.Screen) bool {
		a, okA := markerCell(s, "DIMAX")
		b, okB := markerCell(s, "DIMBX")
		unfocused, focused = a, b
		return okA && okB && (a.Fg != b.Fg || a.Bg != b.Bg)
	}, uiTimeout); err != nil {
		t.Fatalf("pane A is not dimmed at all, so this proves nothing: %v\n%s", err, term.Snapshot())
	}
	return unfocused, focused
}

// TestDimForeground drives appearance.dim_foreground. On (the default), the
// unfocused pane's text is dimmed and so is its background. Off, the text keeps
// the colour the focused pane has while the background is still dimmed.
//
// Each half is the other's positive half: the default fixture shows the text
// does dim, so the unchanged text with the option off is a result of the
// option and not of a dim that never reached text.
func TestDimForeground(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		a, b := dimPair(t, dimUnfocusedConfig(""))
		if a.Fg == b.Fg {
			t.Errorf("dim_foreground unset: the unfocused text is %+v, the same as the focused text; it was not dimmed", a.Fg)
		}
		if a.Bg == b.Bg {
			t.Errorf("dim_foreground unset: the unfocused background is %+v, the same as the focused one; it was not dimmed", a.Bg)
		}
	})
	t.Run("off", func(t *testing.T) {
		a, b := dimPair(t, dimUnfocusedConfig("dim_foreground = false\n"))
		if a.Fg != b.Fg {
			t.Errorf("dim_foreground = false: the unfocused text is %+v, want the focused text's %+v", a.Fg, b.Fg)
		}
		if a.Bg == b.Bg {
			t.Errorf("dim_foreground = false: the unfocused background is %+v, the same as the focused one; "+
				"the background must still dim", a.Bg)
		}
	})
}
