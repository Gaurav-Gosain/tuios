package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Shared borders draw one line between two panes, so the focused pane cannot be
// signalled by giving it its own border the way the non-shared mode does. These
// tests pin the three signals that replace it: the focused window's perimeter is
// tinted with the focus color, drawn bold, and capped with corner glyphs that
// bend into the window that owns the divider. The corner caps are what keep two
// side-by-side panes distinguishable, since the divider between them is the same
// column whichever one is focused.

// sharedBorderOS builds an auto-tiling model in shared-border mode holding n
// windows, and restores the SharedBorders global when the test ends.
func sharedBorderOS(t *testing.T, n int) *OS {
	t.Helper()
	originalShared, originalAnim := config.SharedBorders, config.AnimationsEnabled
	originalStyle, originalASCII := config.BorderStyle, config.UseASCIIOnly
	config.SharedBorders = true
	// Tiling applies geometry through an animation when animations are on, which
	// would leave the windows at their nominal size for the duration of the test.
	config.AnimationsEnabled = false
	// Pin the border style: other tests in this package mutate it, and the ASCII
	// set draws every corner as "+", which would hide a difference in the caps.
	config.BorderStyle = "rounded"
	config.UseASCIIOnly = false
	t.Cleanup(func() {
		config.SharedBorders = originalShared
		config.AnimationsEnabled = originalAnim
		config.BorderStyle = originalStyle
		config.UseASCIIOnly = originalASCII
	})

	m := &OS{
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		WorkspaceFocus:   make(map[int]int),
		Width:            120,
		Height:           40,
		AutoTiling:       true,
		UseBSPLayout:     true,
		FocusedWindow:    0,
	}
	for i := range n {
		m.Windows = append(m.Windows, &terminal.Window{
			ID:        "win-" + string(rune('a'+i)),
			Workspace: 1,
			Width:     120,
			Height:    40,
			Tiled:     true,
		})
	}
	m.TileAllWindows()

	// Window.Resize is a no-op without a terminal emulator, so a bare model never
	// picks up the tiled size. Assign the layout rects directly, which is what an
	// emulator-backed resize would have produced.
	tree := m.WorkspaceTrees[m.CurrentWorkspace]
	if tree == nil {
		t.Fatal("sharedBorderOS: no BSP tree")
	}
	for intID, rect := range tree.ApplyLayout(m.GetBSPBounds()) {
		if win := m.getWindowByIntID(intID); win != nil {
			win.X, win.Y, win.Width, win.Height = rect.X, rect.Y, rect.W, rect.H
		}
	}
	return m
}

// separatorText concatenates the rendered separator runs, and reports the text
// of the runs that carry the focus styling separately.
func separatorText(t *testing.T, m *OS) (all string, focused string) {
	t.Helper()
	layers := m.renderSeparatorOverlay()
	if len(layers) == 0 {
		t.Fatal("renderSeparatorOverlay produced no layers")
	}
	var allSB, focusSB strings.Builder
	for _, l := range layers {
		content := l.GetContent()
		allSB.WriteString(content)
		// The focused runs are the bold ones; renderSeparatorOverlay emits SGR 1
		// only for the focused perimeter.
		if strings.Contains(content, "\x1b[1m") {
			focusSB.WriteString(content)
		}
	}
	return allSB.String(), focusSB.String()
}

// TestSharedBorderFocusIsDistinct is the regression: before this, every
// separator was drawn in theme.BorderUnfocused() no matter which pane was
// focused, so the frame looked identical for every focus state.
func TestSharedBorderFocusIsDistinct(t *testing.T) {
	m := sharedBorderOS(t, 3)

	seen := make(map[string]int)
	for i := range m.Windows {
		m.FocusedWindow = i
		all, focused := separatorText(t, m)

		if focused == "" {
			t.Fatalf("window %d focused: no separator run carried the focus styling", i)
		}
		if focused == all {
			t.Errorf("window %d focused: every separator run is focused, so focus is not distinguishing", i)
		}
		if prev, ok := seen[all]; ok {
			t.Errorf("windows %d and %d render an identical separator frame; focus is ambiguous", prev, i)
		}
		seen[all] = i
	}
}

// TestSharedBorderFocusUsesThemeColor pins that the perimeter is tinted from the
// theme rather than a hardcoded color, and that it tracks the mode the way the
// non-shared border does (cyan in window mode, green in terminal mode).
func TestSharedBorderFocusUsesThemeColor(t *testing.T) {
	m := sharedBorderOS(t, 3)

	m.Mode = WindowManagementMode
	_, windowFocus := separatorText(t, m)
	m.Mode = TerminalMode
	_, terminalFocus := separatorText(t, m)

	if windowFocus == terminalFocus {
		t.Error("focused perimeter did not change color between window and terminal mode")
	}
	for _, s := range []string{windowFocus, terminalFocus} {
		if !strings.Contains(s, "\x1b[38;2;") {
			t.Errorf("focused perimeter is not tinted with a truecolor SGR: %q", s)
		}
	}
}

// TestSharedBorderFocusIsNotColorAlone covers the accessibility requirement: the
// focus signal has to survive a monochrome or low-contrast theme, so it must not
// be carried by hue alone. Weight (bold) and shape (corner caps) both carry it.
func TestSharedBorderFocusIsNotColorAlone(t *testing.T) {
	m := sharedBorderOS(t, 3)

	_, focused := separatorText(t, m)
	if !strings.Contains(focused, "\x1b[1m") {
		t.Error("focused perimeter is not bold, leaving color as the only signal")
	}

	border := config.GetBorderForStyle()
	corners := border.TopLeft + border.TopRight + border.BottomLeft + border.BottomRight
	if !strings.ContainsAny(focused, corners) {
		t.Errorf("focused perimeter has no corner cap; expected one of %q in %q", corners, focused)
	}
}

// TestSharedBorderCapsBendTowardTheFocusedPane is the two-pane case that a naive
// implementation cannot express: one divider, two windows. Colouring it is
// symmetric, so the caps have to differ.
func TestSharedBorderCapsBendTowardTheFocusedPane(t *testing.T) {
	m := sharedBorderOS(t, 2)
	border := config.GetBorderForStyle()

	m.FocusedWindow = 0
	_, left := separatorText(t, m)
	m.FocusedWindow = 1
	_, right := separatorText(t, m)

	// The left pane owns the divider's right edge, the right pane its left edge.
	if !strings.Contains(left, border.TopRight) {
		t.Errorf("left pane focused: divider should cap with %q, got %q", border.TopRight, left)
	}
	if !strings.Contains(right, border.TopLeft) {
		t.Errorf("right pane focused: divider should cap with %q, got %q", border.TopLeft, right)
	}
	if left == right {
		t.Error("two panes sharing one divider render identically; focus is ambiguous")
	}
}

// TestSharedBorderFocusHandlesEdgeCases covers the shapes where there is no
// perimeter to draw, which must not panic or mis-tint the frame.
func TestSharedBorderFocusHandlesEdgeCases(t *testing.T) {
	t.Run("single window has no separators", func(t *testing.T) {
		m := sharedBorderOS(t, 1)
		if layers := m.renderSeparatorOverlay(); len(layers) != 0 {
			t.Errorf("a lone tiled window should draw no separators, got %d layers", len(layers))
		}
	})

	t.Run("zoomed window suppresses the frame", func(t *testing.T) {
		m := sharedBorderOS(t, 3)
		m.Windows[m.FocusedWindow].Zoomed = true
		if layers := m.renderSeparatorOverlay(); len(layers) != 0 {
			t.Errorf("a zoomed window should draw no separators, got %d layers", len(layers))
		}
	})

	t.Run("floating focus leaves the frame unfocused", func(t *testing.T) {
		m := sharedBorderOS(t, 3)
		m.Windows[m.FocusedWindow].IsFloating = true
		_, focused := separatorText(t, m)
		if focused != "" {
			t.Errorf("a floating window owns no tiled perimeter, got focus styling %q", focused)
		}
	})

	t.Run("minimized focus leaves the frame unfocused", func(t *testing.T) {
		m := sharedBorderOS(t, 3)
		m.Windows[m.FocusedWindow].Minimized = true
		_, focused := separatorText(t, m)
		if focused != "" {
			t.Errorf("a minimized window owns no tiled perimeter, got focus styling %q", focused)
		}
	})
}

// TestFocusPerimeterOwnershipAtJunctions pins the ownership rule at a cell where
// several windows meet: the cell belongs to the focused window when it sits on
// that window's one-cell ring, and to nobody otherwise. Exactly one window is
// focused, so the rule never has to break a tie.
func TestFocusPerimeterOwnershipAtJunctions(t *testing.T) {
	m := sharedBorderOS(t, 4)
	bounds := m.GetBSPBounds()

	for i, win := range m.Windows {
		m.FocusedWindow = i
		p := m.focusPerimeter(bounds)
		if !p.ok {
			t.Fatalf("window %d: expected a perimeter", i)
		}
		// A cell inside the window is never part of its own perimeter.
		if p.contains(win.X+win.Width/2, win.Y+win.Height/2) {
			t.Errorf("window %d: interior cell reported as perimeter", i)
		}
		// The ring sits one cell outside the content box on every side.
		if !p.contains(win.X-1, win.Y) && !p.contains(win.X+win.Width, win.Y) {
			t.Errorf("window %d: neither vertical side of the ring matched", i)
		}
	}
}
