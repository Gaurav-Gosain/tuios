package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The tiling switch has one implementation and several doors: the key, two
// palette rows, three tape commands and the set-layout verb behind them. Each
// door used to carry its own copy of the switch, and each copy forgot a
// different step. The tests here drive every door the code declares and hold
// them to one answer, so a sixth copy cannot be added without being caught.

const switchCols, switchRows = 120, 40

// newSwitchFixture builds an attached client with n daemon-created panes tiled
// under the named layout mode, the way daemon_tiling_test.go does. Animations
// are off so every rectangle is applied directly.
func newSwitchFixture(t *testing.T, mode string, n int, sharedBorders bool) *OS {
	t.Helper()
	prevAnim := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = false
	t.Cleanup(func() { config.Global.AnimationsEnabled = prevAnim })

	m := &OS{
		Settings:             config.Global,
		SharedBorders:        sharedBorders,
		NumWorkspaces:        9,
		CurrentWorkspace:     1,
		WorkspaceFocus:       make(map[int]int),
		WorkspaceLayouts:     make(map[int][]WindowLayout),
		WorkspaceMasterRatio: make(map[int]float64),
		WorkspaceHasCustom:   make(map[int]bool),
		Width:                switchCols,
		Height:               switchRows,
		AutoTiling:           true,
	}
	m.ApplyLayoutModeName(mode)

	state := &session.SessionState{
		Name:             "switch",
		CurrentWorkspace: 1,
		AutoTiling:       true,
		WorkspaceFocus:   map[int]string{},
		Version:          1,
	}
	for i := range n {
		id := fmt.Sprintf("win-%036d", i+1)
		state.Windows = append(state.Windows, session.WindowState{
			ID:        id,
			PTYID:     fmt.Sprintf("pty-%d", i+1),
			Title:     id,
			Width:     switchCols,
			Height:    switchRows,
			Workspace: 1,
			Unplaced:  true,
		})
		state.FocusedWindowID = id
		state.Version++
		if err := m.ApplyStateSync(state); err != nil {
			t.Fatalf("window %d: ApplyStateSync: %v", i+1, err)
		}
		state = m.BuildSessionState()
		state.Version = i + 2
	}
	t.Cleanup(func() {
		for _, w := range m.Windows {
			w.Close()
		}
	})
	if len(m.Windows) != n {
		t.Fatalf("fixture holds %d windows, want %d", len(m.Windows), n)
	}
	return m
}

// TestSessionInfoNamesTilingLikeTheDaemon: the client-side session-info used
// to answer "bsp" for every tiling layout, scrolling included, where the
// daemon's answers "tiling". Both sides now use the daemon's two words and
// name the layout separately.
func TestSessionInfoNamesTilingLikeTheDaemon(t *testing.T) {
	m := newSwitchFixture(t, LayoutModeScrolling, 2, false)
	info := m.GetSessionInfoData()
	if got := info["tiling_mode"]; got != "tiling" {
		t.Errorf("tiling_mode = %v, want \"tiling\"", got)
	}
	if got := info["layout_mode"]; got != LayoutModeScrolling {
		t.Errorf("layout_mode = %v, want %q", got, LayoutModeScrolling)
	}
	m.SetAutoTiling(false)
	if got := m.GetSessionInfoData()["tiling_mode"]; got != "floating" {
		t.Errorf("tiling_mode with tiling off = %v, want \"floating\"", got)
	}
}
