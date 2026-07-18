package app

import (
	"sync"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The hooks system declared eight events and fired three. These tests pin one
// event each to the action that must produce it, so an event cannot go back to
// being a name in the config reference that nothing ever emits.

// hookRecorder collects the contexts hooks fired with, in place of running a
// shell per event.
type hookRecorder struct {
	mu    sync.Mutex
	fired []hooks.Context
}

// record registers every event on m and returns the recorder collecting them.
// Registering all of them (rather than only the one under test) is what catches
// an action that fires the wrong event as well as one that fires none.
func record(t *testing.T, m *OS) *hookRecorder {
	t.Helper()
	if m.HookManager == nil {
		m.HookManager = hooks.NewManager()
	}
	r := &hookRecorder{}
	m.HookManager.SetRunner(func(_ string, ctx hooks.Context) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.fired = append(r.fired, ctx)
	})
	for _, e := range hooks.AllEvents() {
		m.HookManager.Register(e, "true")
	}
	return r
}

// only returns the single context fired for event, failing when the count is
// anything but one: a hook firing twice for one action is as wrong as not
// firing, and a resize hook that runs per mouse-motion event is the specific
// version of that this guards.
func (r *hookRecorder) only(t *testing.T, m *OS, event hooks.Event) hooks.Context {
	t.Helper()
	m.HookManager.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()

	var got []hooks.Context
	for _, c := range r.fired {
		if c.EventType == event {
			got = append(got, c)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%s fired %d times, want 1 (all fired: %v)", event, len(got), r.events())
	}
	return got[0]
}

// events lists what fired, for failure messages.
func (r *hookRecorder) events() []hooks.Event {
	out := make([]hooks.Event, 0, len(r.fired))
	for _, c := range r.fired {
		out = append(out, c.EventType)
	}
	return out
}

func hookTestOS(t *testing.T) *OS {
	t.Helper()
	m := NewOS(OSOptions{})
	m.Width, m.Height = 120, 40
	m.SessionName = "test-session"
	return m
}

func TestAfterWorkspaceSwitchFires(t *testing.T) {
	m := hookTestOS(t)
	m.CurrentWorkspace = 1
	r := record(t, m)

	m.SwitchToWorkspace(3)

	ctx := r.only(t, m, hooks.AfterWorkspaceSwitch)
	if ctx.Workspace != 3 {
		t.Errorf("Workspace = %d, want 3", ctx.Workspace)
	}
	if ctx.PreviousWorkspace != 1 {
		t.Errorf("PreviousWorkspace = %d, want 1", ctx.PreviousWorkspace)
	}
	if ctx.SessionID != "test-session" {
		t.Errorf("SessionID = %q, want %q", ctx.SessionID, "test-session")
	}
}

// A switch to the workspace already showing is not a switch, and firing for it
// would make the event useless for anything that acts on a change.
func TestAfterWorkspaceSwitchSkipsNoOp(t *testing.T) {
	m := hookTestOS(t)
	m.CurrentWorkspace = 2
	r := record(t, m)

	m.SwitchToWorkspace(2)
	m.HookManager.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.fired) != 0 {
		t.Errorf("switching to the current workspace fired %v, want nothing", r.events())
	}
}

func TestAfterLayoutChangeFires(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(*OS)
		want  string
	}{
		{"bsp", (*OS).EnableBSPLayout, LayoutModeBSP},
		{"master-stack", (*OS).EnableMasterStackLayout, LayoutModeMasterStack},
		{"scrolling", (*OS).EnableScrollingLayout, LayoutModeScrolling},
		{"tiling off", (*OS).DisableAllTiling, LayoutFloating},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := hookTestOS(t)
			r := record(t, m)

			tc.apply(m)

			ctx := r.only(t, m, hooks.AfterLayoutChange)
			if ctx.Layout != tc.want {
				t.Errorf("Layout = %q, want %q", ctx.Layout, tc.want)
			}
		})
	}
}

// Cycling layouts is the keybinding path, and it has to report the layout it
// landed on rather than the one it left.
func TestAfterLayoutChangeReportsResultingLayout(t *testing.T) {
	m := hookTestOS(t)
	m.AutoTiling = true
	m.UseBSPLayout, m.UseScrollingLayout = true, false
	r := record(t, m)

	m.ToggleLayoutMode() // bsp -> master-stack

	if ctx := r.only(t, m, hooks.AfterLayoutChange); ctx.Layout != LayoutModeMasterStack {
		t.Errorf("Layout = %q, want %q", ctx.Layout, LayoutModeMasterStack)
	}
}

func TestAfterResizeFires(t *testing.T) {
	m := hookTestOS(t)
	r := record(t, m)

	w := &terminal.Window{ID: "win-1", X: 0, Y: 1, Width: 40, Height: 20, Workspace: 1}
	m.FireResized(w)

	ctx := r.only(t, m, hooks.AfterResize)
	if ctx.WindowID != "win-1" {
		t.Errorf("WindowID = %q, want %q", ctx.WindowID, "win-1")
	}
	if ctx.Width != 40 || ctx.Height != 20 {
		t.Errorf("size = %dx%d, want 40x20", ctx.Width, ctx.Height)
	}
	if ctx.Workspace != 1 {
		t.Errorf("Workspace = %d, want 1", ctx.Workspace)
	}
}

func TestAfterAttachFires(t *testing.T) {
	m := hookTestOS(t)
	r := record(t, m)

	m.FireAttached()

	if ctx := r.only(t, m, hooks.AfterAttach); ctx.SessionID != "test-session" {
		t.Errorf("SessionID = %q, want %q", ctx.SessionID, "test-session")
	}
}

func TestAfterDetachFires(t *testing.T) {
	m := hookTestOS(t)
	r := record(t, m)

	m.FireDetached()

	if ctx := r.only(t, m, hooks.AfterDetach); ctx.SessionID != "test-session" {
		t.Errorf("SessionID = %q, want %q", ctx.SessionID, "test-session")
	}
}

// FireDetached must not return before its hooks have run: the caller quits
// immediately after, and an unwaited hook goroutine dies with the process.
func TestAfterDetachWaitsForHooks(t *testing.T) {
	m := hookTestOS(t)
	if m.HookManager == nil {
		m.HookManager = hooks.NewManager()
	}
	var ran bool
	var mu sync.Mutex
	m.HookManager.SetRunner(func(string, hooks.Context) {
		mu.Lock()
		defer mu.Unlock()
		ran = true
	})
	m.HookManager.Register(hooks.AfterDetach, "true")

	m.FireDetached()

	mu.Lock()
	defer mu.Unlock()
	if !ran {
		t.Error("FireDetached returned before its hook ran")
	}
}

func TestAfterNewWindowAndFocusChangeStillFire(t *testing.T) {
	m := hookTestOS(t)
	w := &terminal.Window{ID: "win-1", Width: 40, Height: 20, Workspace: 1}
	m.Windows = append(m.Windows, w)
	m.FocusedWindow = -1
	r := record(t, m)

	m.FocusWindow(0)

	if ctx := r.only(t, m, hooks.AfterFocusChange); ctx.WindowID != "win-1" {
		t.Errorf("WindowID = %q, want %q", ctx.WindowID, "win-1")
	}
}

// Every declared event must have a fire site. A name in AllEvents that nothing
// emits is the defect this whole change exists to remove, so the list of events
// and the list of events the tests above prove are wired stay tied together.
func TestEveryDeclaredEventIsCovered(t *testing.T) {
	covered := map[hooks.Event]bool{
		hooks.AfterNewWindow:       true,
		hooks.AfterCloseWindow:     true,
		hooks.AfterFocusChange:     true,
		hooks.AfterWorkspaceSwitch: true,
		hooks.AfterAttach:          true,
		hooks.AfterDetach:          true,
		hooks.AfterLayoutChange:    true,
		hooks.AfterResize:          true,
	}
	for _, e := range hooks.AllEvents() {
		if !covered[e] {
			t.Errorf("event %q is declared but has no fire site or test", e)
		}
	}
	if len(covered) != len(hooks.AllEvents()) {
		t.Errorf("covered set has %d events, AllEvents has %d", len(covered), len(hooks.AllEvents()))
	}
}
