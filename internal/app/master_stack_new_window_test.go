package app

import (
	"fmt"
	"testing"
)

// TestNewPaneKeepsTheMasterStackLayout is the report from the browser demo:
// switch to master-stack, open a pane, and the panes come out as a BSP
// spiral while the session still says master-stack. A new pane went through
// the BSP tree whatever the mode, and that path places panes as BSP does.
// Native clients on a daemon place a new pane through TileAllWindows, so only
// the standalone path, which the browser build always takes, showed it.
func TestNewPaneKeepsTheMasterStackLayout(t *testing.T) {
	for _, grid := range []bool{false, true} {
		t.Run(fmt.Sprintf("grid=%v", grid), func(t *testing.T) {
			m := newStartupOS(t, false, true)
			defer closeWindows(m)
			m.Settings.MasterNoGrid = !grid
			m.AutoTiling = true
			m.UseBSPLayout = true
			for range 2 {
				m.AddWindow("")
			}
			m.EnableMasterStackLayout()
			m.CompleteAllAnimations()
			for range 3 {
				m.AddWindow("")
				m.CompleteAllAnimations()
			}
			if got := m.LayoutModeName(); got != LayoutModeMasterStack {
				t.Fatalf("layout mode is %q, want %q", got, LayoutModeMasterStack)
			}
			created := boxes(m)
			// What master-stack places for these panes, asked for directly.
			m.TileAllWindows()
			m.CompleteAllAnimations()
			if want := boxes(m); fmt.Sprint(created) != fmt.Sprint(want) {
				t.Errorf("after new panes the boxes are %v, master-stack places them at %v", created, want)
			}
		})
	}
}

func boxes(m *OS) [][4]int {
	out := make([][4]int, 0, len(m.Windows))
	for _, w := range m.Windows {
		out = append(out, [4]int{w.X, w.Y, w.Width, w.Height})
	}
	return out
}

// TestFloatToggleKeepsTheMasterStackLayout is the same report on the float
// key: floating a pane and tiling it again went through the BSP tree, which
// ends in ApplyBSPLayout, so the panes came back as a BSP spiral.
func TestFloatToggleKeepsTheMasterStackLayout(t *testing.T) {
	m := newStartupOS(t, false, true)
	defer closeWindows(m)
	m.AutoTiling = true
	m.UseBSPLayout = true
	for range 5 {
		m.AddWindow("")
	}
	m.EnableMasterStackLayout()
	m.CompleteAllAnimations()
	m.FocusWindow(2)

	for _, step := range []string{"float", "tile again"} {
		m.ToggleFloating()
		m.CompleteAllAnimations()
		got := boxes(m)
		m.TileAllWindows()
		m.CompleteAllAnimations()
		if want := boxes(m); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("after %s the boxes are %v, master-stack places them at %v", step, got, want)
		}
	}
}
