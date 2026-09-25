package app

import (
	"fmt"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layout"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/ui"
)

// openAnimHarness is a tiling client attached to a daemon, the arrangement the
// report came from: the daemon creates the pane and marks it Unplaced, and the
// client is the only thing that can place it and animate it into its tile.
type openAnimHarness struct {
	m     *OS
	state *session.SessionState
	next  int
}

func newOpenAnimHarness(width, height int) *openAnimHarness {
	return newOpenAnimHarnessWithLayout(width, height, true)
}

// newOpenAnimHarnessWithLayout is the same harness under either tiled layout, so
// the open animation can be checked in both. bsp false is master-stack.
func newOpenAnimHarnessWithLayout(width, height int, bsp bool) *openAnimHarness {
	h := &openAnimHarness{
		m: &OS{
			Settings:         config.Global,
			NumWorkspaces:    9,
			CurrentWorkspace: 1,
			WorkspaceFocus:   make(map[int]int),
			Width:            width,
			Height:           height,
			AutoTiling:       true,
			UseBSPLayout:     bsp,
		},
		state: &session.SessionState{
			Name:             "open-anim",
			CurrentWorkspace: 1,
			AutoTiling:       true,
			WorkspaceFocus:   map[int]string{},
			Version:          1,
		},
	}
	return h
}

// createWindow runs one daemon-side creation and the client sync it produces,
// and returns the animations the client armed for it.
func (h *openAnimHarness) createWindow(t *testing.T) []*ui.Animation {
	t.Helper()
	h.next++
	id := fmt.Sprintf("win-%036d", h.next)
	h.state.Windows = append(h.state.Windows, session.WindowState{
		ID:        id,
		PTYID:     fmt.Sprintf("pty-%d", h.next),
		Title:     id,
		Width:     h.m.Width,
		Height:    h.m.Height,
		Workspace: 1,
		Unplaced:  true,
	})
	h.state.FocusedWindowID = id
	h.state.Version++

	h.m.Animations = nil
	if err := h.m.ApplyStateSync(h.state); err != nil {
		t.Fatalf("window %d: ApplyStateSync: %v", h.next, err)
	}
	anims := h.m.Animations
	h.m.CompleteAllAnimations()

	// The client syncs the geometry it just chose back, so the next creation
	// carries the client's tree exactly as the daemon would have stored it.
	h.state = h.m.BuildSessionState()
	h.state.Version = h.next + 1
	return anims
}

// TestRepeatCreationSyncLeavesAPlacedPaneAlone is the second half of the bug,
// and the half that kept it on screen after the start rectangle was already
// right. The daemon re-broadcasts the creating state after a following mutation,
// still carrying Unplaced, because this client's placing push has not landed
// yet. Acting on that echo re-placed a pane the client had already placed: a few
// frames into the open animation the pane was moved back to the raw placement
// box in the middle of the screen and the layout restarted the snap from there,
// which is the corner-to-tile flight the report described.
//
// So the echo must move nothing at all, and must not arm a fresh animation.
func TestRepeatCreationSyncLeavesAPlacedPaneAlone(t *testing.T) {
	prev := config.Global.AnimationsEnabled
	config.Global.AnimationsEnabled = true
	defer func() { config.Global.AnimationsEnabled = prev }()

	h := newOpenAnimHarness(120, 40)
	h.createWindow(t)
	h.createWindow(t)

	target := h.m.Windows[len(h.m.Windows)-1]
	before := layout.Rect{X: target.X, Y: target.Y, W: target.Width, H: target.Height}

	// The echo, as the daemon actually sends it: the same windows the client
	// already knows, but the newest one still carrying the flag AND the nominal
	// full-size box that goes with it, because the client's answer has not
	// reached the daemon yet. Rebuilding the state from the client and only
	// flipping the flag is not the same test: it hands the echo the client's own
	// correct geometry, so a client that wrongly adopts it looks fine.
	replay := h.m.BuildSessionState()
	replay.Version = h.state.Version + 1
	nominal := &replay.Windows[len(replay.Windows)-1]
	nominal.Unplaced = true
	nominal.X, nominal.Y = 0, 0
	nominal.Width, nominal.Height = h.m.Width, h.m.Height

	h.m.Animations = nil
	if err := h.m.ApplyStateSync(replay); err != nil {
		t.Fatalf("replayed sync: %v", err)
	}

	if target.X != before.X || target.Y != before.Y ||
		target.Width != before.W || target.Height != before.H {
		t.Errorf("the echo moved a placed pane from (%d,%d %dx%d) to (%d,%d %dx%d)",
			before.X, before.Y, before.W, before.H,
			target.X, target.Y, target.Width, target.Height)
	}
	if target.Opening {
		t.Error("the echo marked an already placed pane as opening")
	}
	for _, a := range h.m.Animations {
		if a.Window == target {
			t.Errorf("the echo armed an animation on a settled pane: (%d,%d %dx%d) -> (%d,%d %dx%d)",
				a.StartX, a.StartY, a.StartWidth, a.StartHeight,
				a.EndX, a.EndY, a.EndWidth, a.EndHeight)
		}
	}
}
