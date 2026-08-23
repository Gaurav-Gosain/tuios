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
	return &openAnimHarness{
		m: &OS{
			NumWorkspaces:    9,
			CurrentWorkspace: 1,
			WorkspaceFocus:   make(map[int]int),
			Width:            width,
			Height:           height,
			AutoTiling:       true,
			UseBSPLayout:     true,
		},
		state: &session.SessionState{
			Name:             "open-anim",
			CurrentWorkspace: 1,
			AutoTiling:       true,
			WorkspaceFocus:   map[int]string{},
			Version:          1,
		},
	}
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

func (h *openAnimHarness) newestWindowAnim(t *testing.T, anims []*ui.Animation) *ui.Animation {
	t.Helper()
	newest := h.m.Windows[len(h.m.Windows)-1]
	for _, a := range anims {
		if a.Window == newest {
			return a
		}
	}
	t.Fatalf("no animation armed for the pane that was just created")
	return nil
}

// TestOpenAnimationGrowsFromItsOwnTile pins the reported bug: a pane created in
// tiling mode used to animate in from a half-screen box parked at the middle of
// the screen, whose top-left corner sits up and to the left of any tile but the
// middle one, so a new pane read as flying in from the corner and shrinking.
// It must instead grow outward from inside the tile it is about to fill.
func TestOpenAnimationGrowsFromItsOwnTile(t *testing.T) {
	prev := config.AnimationsEnabled
	config.AnimationsEnabled = true
	defer func() { config.AnimationsEnabled = prev }()

	h := newOpenAnimHarness(120, 40)

	for i := 1; i <= 4; i++ {
		anims := h.createWindow(t)
		anim := h.newestWindowAnim(t, anims)

		if anim.StartX < anim.EndX || anim.StartY < anim.EndY ||
			anim.StartX+anim.StartWidth > anim.EndX+anim.EndWidth ||
			anim.StartY+anim.StartHeight > anim.EndY+anim.EndHeight {
			t.Errorf("pane %d opens from (%d,%d %dx%d), which is not inside its tile (%d,%d %dx%d)",
				i, anim.StartX, anim.StartY, anim.StartWidth, anim.StartHeight,
				anim.EndX, anim.EndY, anim.EndWidth, anim.EndHeight)
		}

		// Centred on the tile, to within the odd cell integer division leaves.
		startCX := 2*anim.StartX + anim.StartWidth
		startCY := 2*anim.StartY + anim.StartHeight
		endCX := 2*anim.EndX + anim.EndWidth
		endCY := 2*anim.EndY + anim.EndHeight
		if diff := startCX - endCX; diff > 1 || diff < -1 {
			t.Errorf("pane %d opens off-centre horizontally: start centre %d, tile centre %d (half-cells)",
				i, startCX, endCX)
		}
		if diff := startCY - endCY; diff > 1 || diff < -1 {
			t.Errorf("pane %d opens off-centre vertically: start centre %d, tile centre %d (half-cells)",
				i, startCY, endCY)
		}

		if anim.StartWidth >= anim.EndWidth && anim.StartHeight >= anim.EndHeight {
			t.Errorf("pane %d does not grow: start %dx%d, tile %dx%d",
				i, anim.StartWidth, anim.StartHeight, anim.EndWidth, anim.EndHeight)
		}
	}
}

// TestOpenAnimationRespectsDisabledAnimations checks the option the fix has to
// leave alone: with animations off, nothing is armed and the pane is placed on
// its tile in one step. The open start rectangle is a property of the animation,
// so it must not survive into the path that has no animation.
func TestOpenAnimationRespectsDisabledAnimations(t *testing.T) {
	prev := config.AnimationsEnabled
	config.AnimationsEnabled = false
	defer func() { config.AnimationsEnabled = prev }()

	h := newOpenAnimHarness(120, 40)

	for i := 1; i <= 3; i++ {
		if anims := h.createWindow(t); len(anims) != 0 {
			t.Fatalf("pane %d armed %d animations with animations disabled", i, len(anims))
		}
	}

	for _, w := range h.m.Windows {
		if w.Opening {
			t.Errorf("window %s still marked opening after placement", w.ID)
		}
		if w.Width <= ui.MinAnimatedWidth || w.Height <= ui.MinAnimatedHeight {
			t.Errorf("window %s left at an animation start box (%dx%d) rather than its tile",
				w.ID, w.Width, w.Height)
		}
	}
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
	prev := config.AnimationsEnabled
	config.AnimationsEnabled = true
	defer func() { config.AnimationsEnabled = prev }()

	h := newOpenAnimHarness(120, 40)
	h.createWindow(t)
	h.createWindow(t)

	target := h.m.Windows[len(h.m.Windows)-1]
	before := layout.Rect{X: target.X, Y: target.Y, W: target.Width, H: target.Height}

	// The echo, as the daemon actually sends it: the same windows the client
	// already knows, but the newest one still carrying the flag AND the nominal
	// full-size box that goes with it, because the client's answer has not
	// reached the daemon yet. Rebuilding the state from the client and only
	// flipping the flag is not the same test - it hands the echo the client's own
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

// TestCloseAnimationUnchanged records that closing does not share the open
// path's start rectangle. The pane that closes is gone with no animation of its
// own; the surviving panes snap from where they were to the space they inherit,
// which is continuous motion and was never affected by the bug.
func TestCloseAnimationUnchanged(t *testing.T) {
	prev := config.AnimationsEnabled
	config.AnimationsEnabled = true
	defer func() { config.AnimationsEnabled = prev }()

	h := newOpenAnimHarness(120, 40)
	h.createWindow(t)
	h.createWindow(t)

	survivor := h.m.Windows[0]
	before := layout.Rect{X: survivor.X, Y: survivor.Y, W: survivor.Width, H: survivor.Height}

	closing := h.m.Windows[len(h.m.Windows)-1]
	closed := h.m.BuildSessionState()
	closed.Version = h.state.Version + 1
	for i, ws := range closed.Windows {
		if ws.ID == closing.ID {
			closed.Windows = append(closed.Windows[:i], closed.Windows[i+1:]...)
			break
		}
	}

	h.m.Animations = nil
	if err := h.m.ApplyStateSync(closed); err != nil {
		t.Fatalf("close sync: %v", err)
	}

	var found bool
	for _, a := range h.m.Animations {
		if a.Window != survivor {
			continue
		}
		found = true
		if a.StartX != before.X || a.StartY != before.Y ||
			a.StartWidth != before.W || a.StartHeight != before.H {
			t.Errorf("survivor snaps from (%d,%d %dx%d), want its own previous box (%d,%d %dx%d)",
				a.StartX, a.StartY, a.StartWidth, a.StartHeight, before.X, before.Y, before.W, before.H)
		}
	}
	if !found {
		t.Fatal("the surviving pane was never animated into the space the closed pane freed")
	}
}

// TestOpenStartRectFloor covers the tile too small to shrink into: the start box
// never goes below the size a minimize travels down to, and never exceeds the
// tile, so a caller comparing the two can tell there is nothing to animate.
func TestOpenStartRectFloor(t *testing.T) {
	cases := []struct {
		name string
		rect layout.Rect
	}{
		{"ordinary tile", layout.Rect{X: 10, Y: 4, W: 60, H: 20}},
		{"one column short of the floor", layout.Rect{X: 0, Y: 0, W: ui.MinAnimatedWidth - 1, H: 20}},
		{"at the floor", layout.Rect{X: 3, Y: 3, W: ui.MinAnimatedWidth, H: ui.MinAnimatedHeight}},
		{"degenerate", layout.Rect{X: 1, Y: 1, W: 0, H: 0}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			x, y, w, h := openStartRect(tc.rect)
			if w > tc.rect.W || h > tc.rect.H {
				t.Errorf("start box %dx%d exceeds the tile %dx%d", w, h, tc.rect.W, tc.rect.H)
			}
			if w < min(ui.MinAnimatedWidth, tc.rect.W) || h < min(ui.MinAnimatedHeight, tc.rect.H) {
				t.Errorf("start box %dx%d is below the floor", w, h)
			}
			if x < tc.rect.X || y < tc.rect.Y ||
				x+w > tc.rect.X+tc.rect.W || y+h > tc.rect.Y+tc.rect.H {
				t.Errorf("start box (%d,%d %dx%d) is not inside the tile %+v", x, y, w, h, tc.rect)
			}
		})
	}
}
