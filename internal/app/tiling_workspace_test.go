package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// TestRestoreWorkspaceLayoutDoesNotForceCustom verifies that restoring a saved
// layout does not mark the workspace as custom. SaveCurrentLayout runs on every
// workspace switch, so a saved layout always exists after the first switch;
// forcing the custom flag here previously suppressed the retile-if-not-custom
// check permanently, disabling auto-retiling after a single round-trip.
func TestRestoreWorkspaceLayoutDoesNotForceCustom(t *testing.T) {
	m := &OS{
		AutoTiling: true,
		WorkspaceLayouts: map[int][]WindowLayout{
			2: {{WindowID: "nonexistent", X: 0, Y: 0, Width: 10, Height: 10}},
		},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
	}

	m.RestoreWorkspaceLayout(2)

	if m.WorkspaceHasCustom[2] {
		t.Error("RestoreWorkspaceLayout must not mark a workspace custom; only MarkLayoutCustom (a real user resize) may")
	}
}

// TestRestoreWorkspaceLayoutRoundTripKeepsRetiling simulates two workspace-switch
// saves followed by a restore and confirms auto-retiling stays enabled (the
// workspace is not stuck as custom), which is what previously broke.
func TestRestoreWorkspaceLayoutRoundTripKeepsRetiling(t *testing.T) {
	m := &OS{
		AutoTiling:           true,
		WorkspaceLayouts:     map[int][]WindowLayout{},
		WorkspaceMasterRatio: map[int]float64{},
		WorkspaceHasCustom:   map[int]bool{},
		CurrentWorkspace:     1,
	}

	// Simulate a switch away from workspace 1 (saves its layout).
	m.SaveCurrentLayout()
	// Simulate switching back to workspace 1 (restores the just-saved layout).
	m.RestoreWorkspaceLayout(1)

	if m.WorkspaceHasCustom[1] {
		t.Error("auto-retiling permanently disabled: workspace 1 marked custom after a plain save/restore round-trip")
	}
}

// twoWorkspaceOS builds an auto-tiling model with two panes tiled on workspace 1
// and one on workspace 2, in the given border mode.
func twoWorkspaceOS(t *testing.T, shared bool) *OS {
	t.Helper()
	origShared, origAnim := config.SharedBorders, config.AnimationsEnabled
	origStyle, origASCII, origSidebar := config.BorderStyle, config.UseASCIIOnly, config.SidebarEnabled
	config.SharedBorders = shared
	// Tiling applies geometry through an animation when animations are on, which
	// would leave the panes at their nominal size for the length of the test.
	config.AnimationsEnabled = false
	// The ASCII set draws every corner as "+", which would hide a pane box among
	// the separator glyphs.
	config.BorderStyle = "rounded"
	config.UseASCIIOnly = false
	config.SidebarEnabled = false
	t.Cleanup(func() {
		config.SharedBorders, config.AnimationsEnabled = origShared, origAnim
		config.BorderStyle, config.UseASCIIOnly = origStyle, origASCII
		config.SidebarEnabled = origSidebar
	})

	m := &OS{
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceHasCustom:   make(map[int]bool),
		WorkspaceMasterRatio: make(map[int]float64),
		Width:                120,
		Height:               40,
		AutoTiling:           true,
		UseBSPLayout:         true,
		FocusedWindow:        0,
	}
	for i := range 2 {
		w := benchWindow(t, fmt.Sprintf("ws1-%d", i), 120, 40)
		w.Workspace, w.Width, w.Height = 1, 120, 40
		m.Windows = append(m.Windows, w)
	}
	other := benchWindow(t, "ws2-0", 120, 40)
	other.Workspace, other.Width, other.Height = 2, 120, 40
	m.Windows = append(m.Windows, other)

	m.TileAllWindows()
	return m
}

// separatorCarriesFocusColor reports whether any separator run is drawn in the
// focus color rather than the unfocused one.
//
// The composed frame is where the structural half of this is checked, but it
// carries no color under test: lipgloss resolves to a profile with no color when
// the process has no terminal, so every cell composes as bare text. The runs the
// overlay hands the compositor are the last point the color still exists, so the
// hue half of the assertion is made there.
func separatorCarriesFocusColor(t *testing.T, m *OS) bool {
	t.Helper()
	focusColor := theme.BorderFocusedWindow()
	if m.Mode == TerminalMode {
		focusColor = theme.BorderFocusedTerminal()
	}
	want := sgrForeground(focusColor)
	for _, l := range m.renderSeparatorOverlay() {
		if strings.Contains(l.GetContent(), want) {
			return true
		}
	}
	return false
}

// TestTilingToggleOnAnotherWorkspaceKeepsRestingBorders is the reported fault:
// two tiled panes came back from a workspace round-trip with the divider between
// them in the unfocused color while a pane beside it drew a border of its own.
//
// Disabling tiling clears Tiled on every window in the session; enabling it again
// only tiles the workspace that is current. A workspace holding a custom layout
// skips the retile that would otherwise have settled the flag on the way back in,
// so its panes drew boxes inside rectangles whose separator gaps were already
// reserved, and the overlay filled those gaps as well. A pane that is not Tiled
// also contributes no focus perimeter, which is what left the divider in the
// unfocused red beside a focused green pane.
func TestTilingToggleOnAnotherWorkspaceKeepsRestingBorders(t *testing.T) {
	for _, shared := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared-%t", shared), func(t *testing.T) {
			m := twoWorkspaceOS(t, shared)

			// A user resize is what marks the workspace custom, and the custom flag
			// is what makes the switch back skip its retile.
			m.MarkLayoutCustom()

			m.SwitchToWorkspace(2)
			m.ToggleAutoTiling()
			m.ToggleAutoTiling()
			m.SwitchToWorkspace(1)

			ownBoxes, strayRules, _ := borderAudit(t, m)
			if ownBoxes > 0 && strayRules > 0 {
				t.Errorf("both border systems drew: %d panes boxed and %d separator cells outside every pane",
					ownBoxes, strayRules)
			}

			if shared {
				if ownBoxes != 0 {
					t.Errorf("shared borders on: %d panes drew a box of their own", ownBoxes)
				}
				if strayRules == 0 {
					t.Error("shared borders on: no separator drawn between the two panes")
				}
				if !separatorCarriesFocusColor(t, m) {
					t.Error("every separator run is drawn in the unfocused color, so the divider reads red beside a focused pane")
				}
			} else {
				if ownBoxes != 2 {
					t.Errorf("shared borders off: %d panes drew a box, want 2", ownBoxes)
				}
				if strayRules != 0 {
					t.Errorf("shared borders off: %d separator cells drawn outside every pane", strayRules)
				}
			}
		})
	}
}

// TestAbandonedGestureLeavesNoWindowFlagged pins the other way a pane can be
// reached carrying state from a gesture that is over. IsBeingManipulated freezes
// a pane at its cached frame, and the release that clears it is exactly what goes
// missing when the user leaves the pane by some other route.
func TestAbandonedGestureLeavesNoWindowFlagged(t *testing.T) {
	abandon := map[string]func(m *OS){
		"workspace-switch": func(m *OS) { m.SwitchToWorkspace(2) },
		"zoom":             func(m *OS) { m.ToggleZoom() },
	}
	for name, leave := range abandon {
		t.Run(name, func(t *testing.T) {
			m := twoWorkspaceOS(t, true)

			// A resize drag in flight on the focused pane.
			m.Resizing = true
			m.InteractionMode = true
			m.DraggedWindowIndex = 0
			m.Windows[0].IsBeingManipulated = true

			leave(m)
			// The pointer is not held any more, which is all the per-frame backstop
			// needs. It is the only thing that runs for a pane no longer on screen.
			m.pointerDown = false
			m.endGestureWithoutButton()
			m.clearStaleManipulation()

			for _, w := range m.Windows {
				if w.IsBeingManipulated {
					t.Errorf("window %s still flagged as being manipulated after the gesture was abandoned by a %s", w.ID, name)
				}
			}
			if m.Resizing || m.InteractionMode {
				t.Errorf("gesture state survived a %s: Resizing=%v InteractionMode=%v", name, m.Resizing, m.InteractionMode)
			}
		})
	}
}
