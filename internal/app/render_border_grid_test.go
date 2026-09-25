package app

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

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
	originalShared, originalAnim := config.Global.SharedBorders, config.Global.AnimationsEnabled
	originalStyle, originalASCII := config.Global.BorderStyle, config.Global.UseASCIIOnly
	originalDock := config.Global.DockbarPosition
	// The caps sit where the perimeter turns, and the dock's hairline is what a
	// full-height divider turns on, so the dock's position is part of the shape
	// these tests read. Other tests in this package move it.
	config.Global.DockbarPosition = "bottom"
	config.Global.SharedBorders = true
	// Tiling applies geometry through an animation when animations are on, which
	// would leave the windows at their nominal size for the duration of the test.
	config.Global.AnimationsEnabled = false
	// Pin the border style: other tests in this package mutate it, and the ASCII
	// set draws every corner as "+", which would hide a difference in the caps.
	config.Global.BorderStyle = "rounded"
	config.Global.UseASCIIOnly = false
	t.Cleanup(func() {
		config.Global.SharedBorders = originalShared
		config.Global.AnimationsEnabled = originalAnim
		config.Global.BorderStyle = originalStyle
		config.Global.UseASCIIOnly = originalASCII
		config.Global.DockbarPosition = originalDock
	})

	m := &OS{
		Settings: config.Global,
		// The layout reads the model's session-settled geometry, seeded from
		// the globals the way NewOS seeds it.
		SharedBorders:    config.Global.SharedBorders,
		PaneGap:          config.Global.PaneGap,
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
	for intID, rect := range tree.ApplyLayout(m.GetBSPBounds(), m.separatorGap()) {
		if win := m.GetWindowByIntID(intID); win != nil {
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

// TestSharedBorderFocusHandlesEdgeCases covers the shapes where there is no
// perimeter to draw, which must not panic or mis-tint the frame.
func TestSharedBorderFocusHandlesEdgeCases(t *testing.T) {
	t.Run("single window has no separators", func(t *testing.T) {
		m := sharedBorderOS(t, 1)
		if layers := m.renderSeparatorOverlay(); len(layers) != 0 {
			t.Errorf("a lone tiled window should draw no separators, got %d layers", len(layers))
		}
	})

	t.Run("a zoom that covers the region suppresses the frame", func(t *testing.T) {
		m := sharedBorderOS(t, 3)
		m.Settings = config.Global
		zw := m.Windows[m.FocusedWindow]
		zw.Zoomed = true
		// The rectangle a full zoom gives it. The flag alone is not the zoom:
		// what suppresses the dividers is one pane covering the region, because
		// a divider drawn then lands across that pane.
		zw.X, zw.Y = m.GetLeftMargin(), m.GetTopMargin()
		zw.Width, zw.Height = m.GetContentWidth(), m.GetUsableHeight()
		if layers := m.renderSeparatorOverlay(); len(layers) != 0 {
			t.Errorf("a zoom covering the region should draw no separators, got %d layers", len(layers))
		}
	})

	t.Run("a zoom of part of the region keeps the frame", func(t *testing.T) {
		m := sharedBorderOS(t, 3)
		m.Settings = config.Global
		// appearance.zoom_size draws the whole layout through a camera, so
		// every pane is still on screen and the dividers between them are still
		// the dividers. Suppressing them on any zoom at all took the shared
		// borders away the moment the setting was used.
		zw := m.Windows[m.FocusedWindow]
		zw.Zoomed = true
		zw.Width = m.GetContentWidth() * 3 / 4
		if layers := m.renderSeparatorOverlay(); len(layers) == 0 {
			t.Error("a zoom of part of the region drew no separators, so the shared borders vanished")
		}
	})

	t.Run("floating focus leaves the frame unfocused", func(t *testing.T) {
		m := sharedBorderOS(t, 3)
		m.Settings = config.Global
		m.Windows[m.FocusedWindow].IsFloating = true
		_, focused := separatorText(t, m)
		if focused != "" {
			t.Errorf("a floating window owns no tiled perimeter, got focus styling %q", focused)
		}
	})

	t.Run("minimized focus leaves the frame unfocused", func(t *testing.T) {
		m := sharedBorderOS(t, 3)
		m.Settings = config.Global
		m.Windows[m.FocusedWindow].Minimized = true
		_, focused := separatorText(t, m)
		if focused != "" {
			t.Errorf("a minimized window owns no tiled perimeter, got focus styling %q", focused)
		}
	})
}

// separatorSnapshot is a divider overlay reduced to what the compositor reads
// from it: each layer's place, depth and text.
func separatorSnapshot(layers []*lipgloss.Layer) string {
	var sb strings.Builder
	for _, l := range layers {
		fmt.Fprintf(&sb, "%d,%d,%d:%q\n", l.GetX(), l.GetY(), l.GetZ(), l.GetContent())
	}
	return sb.String()
}

// TestSeparatorOverlayMemoFollowsItsInputs holds the reused divider overlay to
// the one a fresh draw produces. renderSeparatorOverlay hands back the last
// frame's layers when its inputs match, so an input left out of the key would
// leave a stale divider on screen: the old focus outline, the old colours, the
// old glyphs. Each step changes one input and checks both that the overlay
// moved and that it is exactly what drawing from scratch gives.
//
// Negative control: dropping the focus perimeter, the SGR strings or the
// border from separatorKey fails the matching step here.
func TestSeparatorOverlayMemoFollowsItsInputs(t *testing.T) {
	withTheme(t, "catppuccin_mocha")
	m := sharedBorderOS(t, 4)

	fresh := func() string {
		m.separatorMemo = separatorMemo{}
		return separatorSnapshot(m.renderSeparatorOverlay())
	}

	first := m.renderSeparatorOverlay()
	if len(first) == 0 {
		t.Fatal("setup: no divider overlay")
	}
	again := m.renderSeparatorOverlay()
	if len(again) != len(first) || &again[0] != &first[0] {
		t.Error("an unchanged frame redrew the overlay instead of reusing it")
	}

	prev := separatorSnapshot(first)
	steps := []struct {
		name   string
		change func()
	}{
		{"focus moves", func() { m.FocusedWindow = 2 }},
		{"focus moves again", func() { m.FocusedWindow = 3 }},
		{"terminal mode", func() { m.Mode = TerminalMode }},
		{"window management mode", func() { m.Mode = WindowManagementMode }},
		{"border style", func() { m.Settings.BorderStyle = "thick" }},
		{"theme", func() { withTheme(t, "nord") }},
		{"dock moves to the top", func() { m.Settings.DockbarPosition = "top" }},
		{"screen resizes", func() {
			m.Width, m.Height = 100, 30
			m.TileAllWindows()
			tree := m.WorkspaceTrees[m.CurrentWorkspace]
			for intID, rect := range tree.ApplyLayout(m.GetBSPBounds(), m.separatorGap()) {
				if win := m.GetWindowByIntID(intID); win != nil {
					win.X, win.Y, win.Width, win.Height = rect.X, rect.Y, rect.W, rect.H
				}
			}
		}},
	}
	for _, step := range steps {
		step.change()
		got := separatorSnapshot(m.renderSeparatorOverlay())
		if got == prev {
			t.Errorf("%s: the overlay did not change", step.name)
		}
		if want := fresh(); got != want {
			t.Errorf("%s: the reused overlay differs from a fresh draw\n got: %s\nwant: %s", step.name, got, want)
		}
		prev = got
	}
}
