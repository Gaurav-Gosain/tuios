package app_test

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The tiling switch has one implementation and several doors: the keys, the
// palette rows, the tape commands and the set-layout verb behind them. Each
// door used to carry its own copy of the switch, and each copy forgot a
// different step. These tests open every door a person has and hold them to
// one screen, so a sixth copy cannot be added without being caught.

// tilingDoor is one way of switching tiling.
type tilingDoor struct {
	name string
	run  func(f *apptest.Fixture)
}

// tilingDoors lists every door: the leader key and the window-management
// key, every tape command whose name ends in Tiling (found by reflection, so
// a new one is included without being listed), and every palette row in the
// Layout category, chosen by typing its name into the palette.
func tilingDoors(t *testing.T) []tilingDoor {
	t.Helper()
	doors := []tilingDoor{
		{"key ctrl+b space", func(f *apptest.Fixture) { f.Keys("ctrl+b", "space") }},
		{"key t", func(f *apptest.Fixture) { windowMode(f); f.Keys("t") }},
	}
	tapeDoors := 0
	for method := range reflect.TypeFor[*app.OS]().Methods() {
		if !strings.HasSuffix(method.Name, "Tiling") || method.Type.NumIn() != 1 || method.Type.NumOut() != 1 ||
			method.Type.Out(0) != reflect.TypeFor[error]() {
			continue
		}
		tapeDoors++
		doors = append(doors, tilingDoor{"tape " + method.Name, func(f *apptest.Fixture) {
			out := method.Func.Call([]reflect.Value{reflect.ValueOf(f.M)})
			if err, _ := out[0].Interface().(error); err != nil {
				t.Errorf("%s: %v", method.Name, err)
			}
		}})
	}
	if tapeDoors < 3 {
		t.Fatalf("reflection found %d tape tiling commands, the tree has at least ToggleTiling, EnableTiling and DisableTiling", tapeDoors)
	}
	paletteDoors := 0
	for _, item := range app.GetCommandPaletteItems(&config.Global) {
		if item.Category != "Layout" || item.Action == nil {
			continue
		}
		paletteDoors++
		doors = append(doors, tilingDoor{"palette " + item.Name, palette(item.Name)})
	}
	if paletteDoors == 0 {
		t.Fatal("no palette rows in the Layout category, the palette has several")
	}
	return doors
}

// windowMode leaves terminal mode for window management, as the leader and
// escape do, so the single-letter keys act on panes.
func windowMode(f *apptest.Fixture) { f.Keys("ctrl+b", "esc") }

// sharedBorders is the setting under which a tiled pane draws no frame of
// its own and a floating one does, which is what makes a missed step visible.
var sharedBorders = apptest.Settings(func(s *config.Settings) { s.SharedBorders = true })

// panesArea is the screen below the dock: what the doors must agree on. The
// dock's message line says which door ran, in that door's words.
func panesArea(f *apptest.Fixture) string {
	return strings.Join(f.Screen()[2:], "\n")
}

// visiblePanes counts the panes on the current workspace that are not
// minimized.
func visiblePanes(f *apptest.Fixture) int {
	n := 0
	for _, w := range f.M.Windows {
		if w.Workspace == f.M.CurrentWorkspace && !w.Minimized {
			n++
		}
	}
	return n
}

// TestEveryWayOfTurningTilingOffAgrees drives each door from the same start
// and holds every one that turns tiling off to the same screen: every pane
// drawn whole with its own frame, none off screen, no preselection armed, and
// the layout mode kept for when tiling comes back.
//
// Two starts. A scrolled strip, which is the niri report. And a shared-border
// BSP layout with one pane minimized, because the minimized pane is the one
// the reclaim never places: it kept its borderless flag unless the switch
// cleared it by hand, and came back from the dock with no frame.
func TestEveryWayOfTurningTilingOffAgrees(t *testing.T) {
	starts := []struct {
		name  string
		setup func(t *testing.T) *apptest.Fixture
	}{
		{"scrolled strip", func(t *testing.T) *apptest.Fixture {
			f := apptest.New(t, apptest.Panes(4), sharedBorders)
			palette("Layout: scrolling (niri-style)")(f)
			// Focus the last column; the strip scrolls to keep it in view.
			for range 3 {
				f.Keys("tab")
			}
			// Under shared borders a tiled pane draws no frame to look for,
			// so the precondition reads where the panes are.
			if !slices.ContainsFunc(f.M.Windows, func(w *terminal.Window) bool { return w.X < f.M.GetLeftMargin() }) {
				t.Fatalf("the strip fits on screen, so nothing can be left behind:\n%s", f.Text())
			}
			return f
		}},
		{"bsp with a minimized pane", func(t *testing.T) *apptest.Fixture {
			f := apptest.New(t, apptest.Panes(3), sharedBorders)
			windowMode(f)
			f.Keys("m")
			if visiblePanes(f) != 2 {
				t.Fatalf("m did not minimize a pane: %d visible", visiblePanes(f))
			}
			return f
		}},
	}
	for _, start := range starts {
		t.Run(start.name, func(t *testing.T) {
			var reference string
			referenceName := ""
			turnedOff := map[string]bool{}
			for _, door := range tilingDoors(t) {
				t.Run(door.name, func(t *testing.T) {
					f := start.setup(t)
					mode := f.M.LayoutModeName()
					f.Action("preselect_left")
					door.run(f)
					if f.M.AutoTiling {
						return
					}
					turnedOff[door.name] = true
					// A minimized pane comes back from the dock as it left.
					windowMode(f)
					f.Keys("M")
					checkEveryPaneWhole(t, f)
					if f.M.PreselectionDir != 0 {
						t.Errorf("a preselection stayed armed across the switch")
					}
					if f.M.LayoutModeName() != mode {
						t.Errorf("the layout mode was forgotten: turning tiling back on would build a different layout")
					}
					got := panesArea(f)
					if referenceName == "" {
						reference, referenceName = got, door.name
						return
					}
					if got != reference {
						t.Errorf("%s draws a different screen from %s:\n got\n%s\nwant\n%s", door.name, referenceName, got, reference)
					}
				})
			}
			for _, name := range []string{"key ctrl+b space", "key t", "tape ToggleTiling", "tape DisableTiling", "palette Toggle tiling", "palette Layout: disable tiling"} {
				if !turnedOff[name] {
					t.Errorf("%s did not turn tiling off", name)
				}
			}
		})
	}
}

// checkEveryPaneWhole brings each pane to the front in turn, as a person
// does with tab, and checks the front pane draws its own frame whole on
// screen: none kept the borderless look of tiling, and none was left off the
// screen with nothing to scroll it back.
func checkEveryPaneWhole(t *testing.T, f *apptest.Fixture) {
	t.Helper()
	seen := map[string]bool{}
	for range visiblePanes(f) {
		f.Keys("tab")
		whole, cut := f.PaneFrames()
		for _, why := range cut {
			t.Errorf("a pane is cut by the screen with tiling off: %s\n%s", why, f.Text())
		}
		for _, b := range whole {
			seen[b.Title] = true
		}
	}
	for _, w := range f.M.Windows {
		if w.Workspace == f.M.CurrentWorkspace && !w.Minimized && !seen[w.Title()] {
			t.Errorf("%q never showed a whole frame of its own with tiling off (seen %q)\n%s", w.Title(), slices.Sorted(maps.Keys(seen)), f.Text())
		}
	}
}

// TestEveryWayOfTurningTilingOnAgrees is the other direction: from tiling off,
// every door that turns it back on without choosing a new layout lands on the
// same tiled screen, where under shared borders no pane draws its own frame.
func TestEveryWayOfTurningTilingOnAgrees(t *testing.T) {
	var reference string
	referenceName := ""
	turnedOn := map[string]bool{}
	for _, door := range tilingDoors(t) {
		t.Run(door.name, func(t *testing.T) {
			f := apptest.New(t, apptest.Panes(3), sharedBorders)
			windowMode(f)
			f.Keys("t")
			if f.M.AutoTiling {
				t.Fatal("t did not turn tiling off")
			}
			door.run(f)
			if !f.M.AutoTiling || f.M.LayoutModeName() != app.LayoutModeBSP {
				return
			}
			turnedOn[door.name] = true
			whole, cut := f.PaneFrames()
			if len(whole) != 0 || len(cut) != 0 {
				t.Errorf("tiled under shared borders, %d panes still draw their own frame\n%s", len(whole)+len(cut), f.Text())
			}
			got := panesArea(f)
			if referenceName == "" {
				reference, referenceName = got, door.name
				return
			}
			if got != reference {
				t.Errorf("%s draws a different screen from %s:\n got\n%s\nwant\n%s", door.name, referenceName, got, reference)
			}
		})
	}
	for _, name := range []string{"key ctrl+b space", "key t", "tape ToggleTiling", "tape EnableTiling", "palette Toggle tiling"} {
		if !turnedOn[name] {
			t.Errorf("%s did not turn tiling on", name)
		}
	}
}

// TestTilingOnKeepsTheBSPArrangement: a layout the user shaped survives a trip
// through tiling off and back, and a pane the daemon closes in between is
// dropped from the arrangement rather than taking it with it.
func TestTilingOnKeepsTheBSPArrangement(t *testing.T) {
	f := apptest.New(t, apptest.Panes(3))
	windowMode(f)
	// Focus the first pane and widen it.
	f.Keys("shift+tab", "shift+tab")
	plain := panesArea(f)
	f.Keys("L", "L", "L")
	shaped := panesArea(f)
	if shaped == plain {
		t.Fatalf("the resize did not move the split, so there is nothing to remember:\n%s", shaped)
	}
	f.Keys("t", "t")
	if got := panesArea(f); got != shaped {
		t.Errorf("the arrangement was rebuilt from scratch:\n got\n%s\nwant\n%s", got, shaped)
	}

	whole, _ := f.PaneFrames()
	firstWidth := frameWidthAt(whole, 0)
	f.Keys("t")
	f.Push(func(st *session.SessionState) { st.Windows = st.Windows[:2] })
	f.Keys("t")
	if visiblePanes(f) != 2 {
		t.Fatalf("%d panes after the close, want 2", visiblePanes(f))
	}
	whole, _ = f.PaneFrames()
	if got := frameWidthAt(whole, 0); got != firstWidth {
		t.Errorf("closing a pane while tiling was off cost the first pane its width: %d columns, had %d\n%s", got, firstWidth, f.Text())
	}
}

// frameWidthAt is the width of the frame that starts at column x, or -1.
func frameWidthAt(frames []apptest.Box, x int) int {
	i := slices.IndexFunc(frames, func(b apptest.Box) bool { return b.X0 == x })
	if i < 0 {
		return -1
	}
	return frames[i].X1 - frames[i].X0 + 1
}

// TestSessionInfoNamesTilingLikeTheDaemon: the client-side session-info used
// to answer "bsp" for every tiling layout, scrolling included, where the
// daemon's answers "tiling". Both sides now use the daemon's two words and
// name the layout separately.
func TestSessionInfoNamesTilingLikeTheDaemon(t *testing.T) {
	f := apptest.New(t, apptest.Panes(2))
	palette("Layout: scrolling (niri-style)")(f)
	info := f.M.GetSessionInfoData()
	if got := info["tiling_mode"]; got != "tiling" {
		t.Errorf("tiling_mode = %v, want \"tiling\"", got)
	}
	if got := info["layout_mode"]; got != app.LayoutModeScrolling {
		t.Errorf("layout_mode = %v, want %q", got, app.LayoutModeScrolling)
	}
	windowMode(f)
	f.Keys("t")
	if got := f.M.GetSessionInfoData()["tiling_mode"]; got != "floating" {
		t.Errorf("tiling_mode with tiling off = %v, want \"floating\"", got)
	}
}
