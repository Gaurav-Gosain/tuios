package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
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

type paneRect struct{ X, Y, W, H int }

func paneRects(m *OS) []paneRect {
	rects := make([]paneRect, 0, len(m.Windows))
	for _, w := range m.Windows {
		rects = append(rects, paneRect{w.X, w.Y, w.Width, w.Height})
	}
	return rects
}

// layoutFingerprint is everything a tiling switch writes, in one comparable
// value.
type layoutFingerprint struct {
	AutoTiling, Scrolling, BSP bool
	Preselect                  int
	Rects                      []paneRect
	Tiled                      []bool
}

func fingerprint(m *OS) layoutFingerprint {
	fp := layoutFingerprint{
		AutoTiling: m.AutoTiling,
		Scrolling:  m.UseScrollingLayout,
		BSP:        m.UseBSPLayout,
		Preselect:  int(m.PreselectionDir),
		Rects:      paneRects(m),
	}
	for _, w := range m.Windows {
		fp.Tiled = append(fp.Tiled, w.Tiled)
	}
	return fp
}

func offScreen(m *OS) []paneRect {
	left, top := m.GetLeftMargin(), m.GetTopMargin()
	right, bottom := left+m.GetContentWidth(), top+m.GetUsableHeight()
	var out []paneRect
	for _, w := range m.Windows {
		if w.X < left || w.Y < top || w.X+w.Width > right || w.Y+w.Height > bottom {
			out = append(out, paneRect{w.X, w.Y, w.Width, w.Height})
		}
	}
	return out
}

// tilingDoor is one way of switching tiling.
type tilingDoor struct {
	name string
	run  func(m *OS)
}

// tilingDoors lists every door the code declares: the two app-level entry
// points, every tape command whose name ends in Tiling (found by reflection,
// so a new one is included without being listed), and every palette row in
// the Layout category (the rows that end up switching tiling are the ones
// the tests hold to the shared answer).
func tilingDoors(t *testing.T) []tilingDoor {
	t.Helper()
	doors := []tilingDoor{
		{"ToggleAutoTiling", func(m *OS) { m.ToggleAutoTiling() }},
		{"DisableAllTiling", func(m *OS) { m.DisableAllTiling() }},
	}
	osType := reflect.TypeOf(&OS{})
	tapeDoors := 0
	for i := range osType.NumMethod() {
		method := osType.Method(i)
		if !strings.HasSuffix(method.Name, "Tiling") || method.Type.NumIn() != 1 || method.Type.NumOut() != 1 {
			continue
		}
		if method.Name == "ToggleAutoTiling" {
			continue
		}
		tapeDoors++
		doors = append(doors, tilingDoor{"tape " + method.Name, func(m *OS) {
			out := method.Func.Call([]reflect.Value{reflect.ValueOf(m)})
			if err, _ := out[0].Interface().(error); err != nil {
				t.Errorf("%s: %v", method.Name, err)
			}
		}})
	}
	if tapeDoors < 3 {
		t.Fatalf("reflection found %d tape tiling commands, the tree has at least ToggleTiling, EnableTiling and DisableTiling", tapeDoors)
	}
	paletteDoors := 0
	for _, item := range GetCommandPaletteItems(&config.Global) {
		if item.Category != "Layout" || item.Action == nil {
			continue
		}
		paletteDoors++
		doors = append(doors, tilingDoor{"palette " + item.Name, func(m *OS) { item.Action(m) }})
	}
	if paletteDoors == 0 {
		t.Fatal("no palette rows in the Layout category, the palette has several")
	}
	return doors
}

// scrolledPastTheEdge puts the focus on the last column of a strip longer than
// the screen, so that at least one pane sits off the left edge.
func scrolledPastTheEdge(t *testing.T, m *OS) {
	t.Helper()
	m.FocusWindow(len(m.Windows) - 1)
	sl := m.GetOrCreateScrollingLayout()
	sl.EnsureFocusedVisible(m.ScrollingViewWidth())
	m.scrollingSetPositions()
	if len(offScreen(m)) == 0 {
		t.Fatalf("the strip fits on screen, so nothing can be left behind: %v", paneRects(m))
	}
}

// TestEveryWayOfTurningTilingOffAgrees drives each door from the same start
// and holds every one that turns tiling off to the same state: tiling off,
// every pane bordered, no preselection armed and no pane off screen.
//
// Two starts. A scrolled strip, which is the niri report. And a shared-border
// BSP layout with one pane minimized, because the minimized pane is the one
// the reclaim never places: it keeps its borderless flag unless the switch
// clears it by hand, and it comes back from the dock with no border.
func TestEveryWayOfTurningTilingOffAgrees(t *testing.T) {
	starts := []struct {
		name  string
		setup func(t *testing.T) *OS
	}{
		{"scrolled strip", func(t *testing.T) *OS {
			m := newSwitchFixture(t, LayoutModeScrolling, 4, true)
			scrolledPastTheEdge(t, m)
			return m
		}},
		{"bsp with a minimized pane", func(t *testing.T) *OS {
			m := newSwitchFixture(t, LayoutModeBSP, 3, true)
			m.MinimizeWindow(0)
			if !m.Windows[0].Minimized || !m.Windows[0].Tiled {
				t.Fatalf("the minimized pane is not the borderless one the test needs")
			}
			return m
		}},
	}
	for _, start := range starts {
		t.Run(start.name, func(t *testing.T) {
			var reference *layoutFingerprint
			referenceName := ""
			mode := ""
			turnedOff := map[string]bool{}
			for _, door := range tilingDoors(t) {
				t.Run(door.name, func(t *testing.T) {
					m := start.setup(t)
					mode = m.LayoutModeName()
					m.PreselectionDir = 1
					door.run(m)
					if m.AutoTiling {
						return
					}
					turnedOff[door.name] = true
					got := fingerprint(m)
					for i, w := range m.Windows {
						if w.Tiled {
							t.Errorf("pane %d is still flagged borderless with tiling off", i)
						}
					}
					if off := offScreen(m); len(off) > 0 {
						t.Errorf("panes left off screen with nothing to scroll them back: %v", off)
					}
					if got.Preselect != 0 {
						t.Errorf("a preselection stayed armed across the switch")
					}
					if m.LayoutModeName() != mode {
						t.Errorf("the layout mode was forgotten: turning tiling back on would build a different layout")
					}
					if reference == nil {
						reference, referenceName = &got, door.name
						return
					}
					if !reflect.DeepEqual(got, *reference) {
						t.Errorf("%s lands in a different state from %s:\n got %+v\nwant %+v", door.name, referenceName, got, *reference)
					}
				})
			}
			for _, name := range []string{"ToggleAutoTiling", "DisableAllTiling", "tape ToggleTiling", "tape DisableTiling", "palette Toggle tiling", "palette Layout: disable tiling"} {
				if !turnedOff[name] {
					t.Errorf("%s did not turn tiling off", name)
				}
			}
		})
	}
}

// TestEveryWayOfTurningTilingOnAgrees is the other direction: from tiling off,
// every door that turns it back on without choosing a new layout lands on the
// same tiled state, with the border flag each pane draws matching the layout.
func TestEveryWayOfTurningTilingOnAgrees(t *testing.T) {
	var reference *layoutFingerprint
	referenceName := ""
	turnedOn := map[string]bool{}
	for _, door := range tilingDoors(t) {
		t.Run(door.name, func(t *testing.T) {
			m := newSwitchFixture(t, LayoutModeBSP, 3, true)
			m.SetAutoTiling(false)
			door.run(m)
			if !m.AutoTiling || m.LayoutModeName() != LayoutModeBSP {
				return
			}
			turnedOn[door.name] = true
			got := fingerprint(m)
			for i, w := range m.Windows {
				if w.Tiled != m.panesBorderless() {
					t.Errorf("pane %d draws its own border (%v), the layout says %v", i, !w.Tiled, !m.panesBorderless())
				}
			}
			if off := offScreen(m); len(off) > 0 {
				t.Errorf("panes tiled off screen: %v", off)
			}
			if reference == nil {
				reference, referenceName = &got, door.name
				return
			}
			if !reflect.DeepEqual(got, *reference) {
				t.Errorf("%s lands in a different state from %s:\n got %+v\nwant %+v", door.name, referenceName, got, *reference)
			}
		})
	}
	for _, name := range []string{"ToggleAutoTiling", "tape ToggleTiling", "tape EnableTiling", "palette Toggle tiling"} {
		if !turnedOn[name] {
			t.Errorf("%s did not turn tiling on", name)
		}
	}
}

// TestTilingOffBringsTheStripOnScreen is the niri report: turn tiling off with
// the strip scrolled past its first columns, and the panes past the edge come
// back where they can be reached.
func TestTilingOffBringsTheStripOnScreen(t *testing.T) {
	m := newSwitchFixture(t, LayoutModeScrolling, 4, false)
	scrolledPastTheEdge(t, m)
	before := paneRects(m)
	m.ToggleAutoTiling()
	if m.AutoTiling {
		t.Fatal("tiling is still on")
	}
	if off := offScreen(m); len(off) > 0 {
		t.Errorf("panes still off screen: %v (before the switch: %v)", off, before)
	}
	for i, w := range m.Windows {
		if w.Width != before[i].W || w.Height != before[i].H {
			t.Errorf("pane %d changed size from %dx%d to %dx%d; only its position should move", i, before[i].W, before[i].H, w.Width, w.Height)
		}
	}
}

// TestTilingOnKeepsTheBSPArrangement: a layout the user shaped survives a trip
// through tiling off and back, and a pane closed in between is dropped from
// the tree rather than taking the arrangement with it.
func TestTilingOnKeepsTheBSPArrangement(t *testing.T) {
	m := newSwitchFixture(t, LayoutModeBSP, 3, false)
	m.FocusWindow(0)
	m.ResizeFocusedWindowWidth(20)
	shaped := paneRects(m)
	if shaped[0].W == switchCols/2 {
		t.Fatalf("the resize did not move the split, so there is nothing to remember: %v", shaped)
	}

	m.ToggleAutoTiling()
	m.ToggleAutoTiling()
	if got := paneRects(m); !reflect.DeepEqual(got, shaped) {
		t.Errorf("the arrangement was rebuilt from scratch:\n got %v\nwant %v", got, shaped)
	}

	m.ToggleAutoTiling()
	m.DeleteWindow(2)
	m.ToggleAutoTiling()
	if len(m.Windows) != 2 {
		t.Fatalf("%d windows after the close, want 2", len(m.Windows))
	}
	if got := paneRects(m); got[0].W != shaped[0].W {
		t.Errorf("closing a pane while tiling was off cost the first pane its width: got %v, had %v", got, shaped)
	}
}

// TestPeerTurningTilingOffClearsTheBorderFlags: tiling switched off by another
// client arrives as state, and this client's panes draw their own borders again.
func TestPeerTurningTilingOffClearsTheBorderFlags(t *testing.T) {
	m := newSwitchFixture(t, LayoutModeBSP, 2, true)
	for i, w := range m.Windows {
		if !w.Tiled {
			t.Fatalf("pane %d is not borderless under shared borders, the fixture is not tiled", i)
		}
	}
	state := m.BuildSessionState()
	state.AutoTiling = false
	state.Version = m.DaemonStateVersion + 1
	if err := m.ApplyStateSync(state); err != nil {
		t.Fatal(err)
	}
	if m.AutoTiling {
		t.Fatal("the sync did not turn tiling off")
	}
	for i, w := range m.Windows {
		if w.Tiled {
			t.Errorf("pane %d is still borderless after a peer turned tiling off", i)
		}
	}
}

// TestRecalcZOrderKeepsTheStackingOrder: raising a window moves that window
// and nothing else. Three panes stacked A, C, B from the bottom: raising A
// leaves C under B.
func TestRecalcZOrderKeepsTheStackingOrder(t *testing.T) {
	m := &OS{
		Windows: []*terminal.Window{
			{ID: "A"}, {ID: "B"}, {ID: "C"},
		},
		WorkspaceFocus: map[int]int{},
	}
	zs := func() []int {
		out := make([]int, len(m.Windows))
		for i, w := range m.Windows {
			out[i] = w.Z
		}
		return out
	}
	m.FocusedWindow = 2
	m.RecalcZOrder()
	m.FocusedWindow = 1
	m.RecalcZOrder()
	if got := zs(); !reflect.DeepEqual(got, []int{0, 2, 1}) {
		t.Fatalf("after raising C then B: %v, want [0 2 1]", got)
	}
	m.FocusedWindow = 0
	m.RecalcZOrder()
	if got := zs(); !reflect.DeepEqual(got, []int{2, 1, 0}) {
		t.Errorf("raising A reordered B and C: %v, want [2 1 0]", got)
	}

	// A floating pane stays above every tiled pane, focused or not, and the
	// floating band keeps its own order too.
	m.Windows = append(m.Windows, &terminal.Window{ID: "F1", IsFloating: true}, &terminal.Window{ID: "F2", IsFloating: true})
	m.FocusedWindow = 4
	m.RecalcZOrder()
	m.FocusedWindow = 3
	m.RecalcZOrder()
	m.FocusedWindow = 0
	m.RecalcZOrder()
	if got := zs(); !reflect.DeepEqual(got, []int{2, 1, 0, 4, 3}) {
		t.Errorf("with two floats: %v, want [2 1 0 4 3]", got)
	}
}

// TestFloatingWindowsStayBelowEveryOverlay: however many panes are open, a
// floating pane is drawn above the separators and below the dock and every
// overlay, and the floating band keeps the panes' relative order.
func TestFloatingWindowsStayBelowEveryOverlay(t *testing.T) {
	overlays := map[string]int{
		"dock":              config.ZIndexDock,
		"help":              config.ZIndexHelp,
		"time":              config.ZIndexTime,
		"logs":              config.ZIndexLogs,
		"which-key":         config.ZIndexWhichKey,
		"scrollback":        config.ZIndexScrollbackBrowser,
		"palette":           config.ZIndexCommandPalette,
		"session switcher":  config.ZIndexSessionSwitcher,
		"layout picker":     config.ZIndexLayoutPicker,
		"overlay panels":    config.ZIndexOverlayBase,
		"context menu":      config.ZIndexContextMenu,
		"notifications":     config.ZIndexNotifications,
		"screensaver":       config.ZIndexScreensaver,
		"floating band top": config.ZIndexFloatingTop + 1,
	}
	prev := -1
	for z := range 2000 {
		floating := &terminal.Window{Z: z, IsFloating: true}
		got := windowLayerZ(floating, false)
		if got <= config.ZIndexSeparators || got <= config.ZIndexAnimating {
			t.Fatalf("Z=%d: a floating pane at %d is under the separators (%d) or an animating pane (%d)", z, got, config.ZIndexSeparators, config.ZIndexAnimating)
		}
		for name, top := range overlays {
			if got >= top {
				t.Fatalf("Z=%d: a floating pane at %d reaches the %s (%d)", z, got, name, top)
			}
		}
		if got < prev {
			t.Fatalf("Z=%d: the floating band is not monotonic (%d after %d)", z, got, prev)
		}
		prev = got
		// A float being dragged keeps its place in the band rather than
		// dropping to the animating layer under the other floats.
		floating.IsBeingManipulated = true
		if moving := windowLayerZ(floating, true); moving != got {
			t.Fatalf("Z=%d: a dragged float moves from %d to %d", z, got, moving)
		}
		if z < config.ZIndexSeparators {
			tiled := &terminal.Window{Z: z}
			if tz := windowLayerZ(tiled, false); tz >= config.ZIndexSeparators {
				t.Fatalf("Z=%d: a tiled pane at %d covers the separators", z, tz)
			}
		}
	}
	// The first two floats are in distinct layers, so a click can raise one
	// above the other.
	if windowLayerZ(&terminal.Window{Z: 1, IsFloating: true}, false) <= windowLayerZ(&terminal.Window{Z: 0, IsFloating: true}, false) {
		t.Error("two floats share a layer")
	}
}

// TestFloatingAPaneLeavesTheStrip: under a scrolling layout, floating a pane
// takes its column out of the strip, the way a peer's sync already does, and
// tiling it again gives it a column back.
func TestFloatingAPaneLeavesTheStrip(t *testing.T) {
	m := newSwitchFixture(t, LayoutModeScrolling, 3, false)
	sl := m.GetOrCreateScrollingLayout()
	if sl.WindowCount() != 3 {
		t.Fatalf("the strip holds %d panes, want 3", sl.WindowCount())
	}
	m.FocusWindow(1)
	m.ToggleFloating()
	if !m.Windows[1].IsFloating {
		t.Fatal("the pane did not float")
	}
	if sl.WindowCount() != 2 {
		t.Errorf("the strip still holds %d panes after one floated, want 2", sl.WindowCount())
	}
	if m.FocusedWindow != 1 {
		t.Fatalf("floating a pane moved the focus to window %d", m.FocusedWindow)
	}
	m.ToggleFloating()
	if sl.WindowCount() != 3 {
		t.Errorf("the strip holds %d panes after the float was tiled again, want 3", sl.WindowCount())
	}
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
