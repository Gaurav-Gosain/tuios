package app_test

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// listDoor is one list a person opens, and the keys that step it.
type listDoor struct {
	name string
	// prepare runs before the model is built, for a list whose rows come
	// from the machine (programs on PATH, tape files).
	prepare func(t *testing.T)
	open    func(f *apptest.Fixture)
	// prev and next step the cursor one row; page jumps further; end goes to
	// the last row. Lists move on up and down unless they say otherwise.
	prev, next, page, end string
}

func listDoors() []listDoor {
	settingsRow := func(label string) func(f *apptest.Fixture) {
		return func(f *apptest.Fixture) {
			f.Action("open_settings")
			f.Keys("/")
			f.Type(label)
			f.Keys("enter", "enter")
		}
	}
	return []listDoor{
		{name: "settings rows", open: func(f *apptest.Fixture) { f.Action("open_settings") }},
		{name: "settings tabs", open: func(f *apptest.Fixture) { f.Action("open_settings") }, prev: "shift+tab", next: "tab", page: "-", end: "-"},
		{name: "command palette", open: func(f *apptest.Fixture) { f.Action("command_palette") }},
		{name: "keybind manager", open: func(f *apptest.Fixture) { f.Action("prefix_keybinds") }},
		{name: "workspace switcher", open: func(f *apptest.Fixture) { f.Action("prefix_workspace_switcher") }},
		{name: "dock editor", open: settingsRow("Components")},
		{name: "rail section editor", open: settingsRow("Sections")},
		{name: "launcher", prepare: programsOnPath, open: func(f *apptest.Fixture) { f.Action("launcher") }},
		{name: "tape manager", prepare: tapeFiles, open: func(f *apptest.Fixture) { f.Action("toggle_tape_manager") }},
		{name: "context menu", open: func(f *apptest.Fixture) {
			windowMode(f)
			// A right click inside the pane, away from its frame.
			x, y := f.PaneCenter()
			f.Send(apptest.RightClick(x, y))
			f.Send(apptest.RightRelease(x, y))
		}},
	}
}

func (d listDoor) keys() (prev, next, page, end string) {
	prev, next, page, end = "up", "down", "pgdown", "end"
	if d.prev != "" {
		prev, next = d.prev, d.next
	}
	if d.page != "" {
		page, end = d.page, d.end
	}
	return prev, next, page, end
}

func openList(t *testing.T, d listDoor, wrap bool) *apptest.Fixture {
	t.Helper()
	if d.prepare != nil {
		d.prepare(t)
	}
	f := apptest.New(t,
		apptest.Workspaces(map[int]int{1: 1, 2: 1, 3: 1, 4: 1}),
		apptest.Size(110, 36),
		apptest.Colors16(),
		apptest.Settings(func(s *config.Settings) { s.WrapLists = wrap }),
	)
	d.open(f)
	if d.prev == "" {
		// Some lists open on the row last used, not the first.
		f.Keys("home")
	}
	if len(f.Highlighted()) == 0 {
		t.Fatalf("%s did not open with a cursor row\nscreen:\n%s", d.name, f.Text())
	}
	return f
}

// TestListsWrapAtTheirEnds is the maintainer's rule, on every list it
// applies to, as a person meets it: up on the first row goes to the last,
// down on the last goes to the first, a page never wraps, and with
// appearance.wrap_lists off both ends stop.
//
// The cursor is read off the screen (reverse video at sixteen colours), so
// the test holds for whatever a list keeps its cursor in.
func TestListsWrapAtTheirEnds(t *testing.T) {
	for _, d := range listDoors() {
		t.Run(d.name, func(t *testing.T) {
			prev, next, page, end := d.keys()

			f := openList(t, d, true)
			first := f.Highlighted()
			f.Keys(prev)
			last := f.Highlighted()
			if slices.Equal(last, first) {
				t.Fatalf("%s on the first row did not move: %q", prev, first)
			}
			f.Keys(next)
			if got := f.Highlighted(); !slices.Equal(got, first) {
				t.Errorf("%s on the last row went to %q, want the first %q", next, got, first)
			}
			if page != "-" {
				f.Keys(prev)
				f.Keys(page)
				if got := f.Highlighted(); !slices.Equal(got, last) {
					t.Errorf("%s from the last row went to %q, want it to stay on %q", page, got, last)
				}
			}

			f = openList(t, d, false)
			first = f.Highlighted()
			f.Keys(prev)
			if got := f.Highlighted(); !slices.Equal(got, first) {
				t.Errorf("with wrap_lists off, %s on the first row went to %q", prev, got)
			}
			if end == "-" {
				return
			}
			f.Keys(end)
			atEnd := f.Highlighted()
			f.Keys(next)
			if got := f.Highlighted(); !slices.Equal(got, atEnd) {
				t.Errorf("with wrap_lists off, %s on the last row went to %q", next, got)
			}
		})
	}
}

// TestTheWheelNeverWraps pins the one exception: a wheel is flung, not
// pressed, so a list scrolled with it stops at its ends whatever the setting.
func TestTheWheelNeverWraps(t *testing.T) {
	d := listDoor{name: "workspace switcher", open: func(f *apptest.Fixture) { f.Action("prefix_workspace_switcher") }}
	f := openList(t, d, true)
	first := f.Highlighted()
	x, y, ok := f.Find("current")
	if !ok {
		t.Fatalf("cannot find the current workspace's row on screen:\n%s", f.Text())
	}
	f.Wheel(x, y, -1)
	if got := f.Highlighted(); !slices.Equal(got, first) {
		t.Errorf("a wheel move up from the first row went to %q", got)
	}
	f.Keys("up")
	if got := f.Highlighted(); slices.Equal(got, first) {
		t.Errorf("a key move up from the first row did not wrap")
	}
}
