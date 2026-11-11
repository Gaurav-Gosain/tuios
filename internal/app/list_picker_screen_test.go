package app_test

import (
	"os"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/apptest"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/layouttmpl"
)

// The list pickers share one list state and one line editor (list_picker.go).
// These tests drive three of them as a person does and read the screen.
//
// How they could pass wrongly, written down first:
//   - Typing could append at the end whatever the caret does. The filter is
//     typed with the caret moved back into it, and the text on screen must be
//     the edited one, not the typed order.
//   - A list could still move on a hard-coded key. The list keys are rebound
//     to one key in [keybindings.lists]; the default key must then do nothing
//     and the rebound one must move the cursor.
//   - A refilter could leave the cursor on a row that is gone. After typing,
//     the cursor row must be the first row of the filtered list.
//   - A paste could go around the editor. A paste into the filter must filter
//     as typing does.
//   - The save field could ignore the caret, or save the typed order. The
//     name saved on disk must be the edited one.

// openEffectPicker opens the screen saver effect picker from its settings row.
func openEffectPicker(t *testing.T, opts ...apptest.Option) *apptest.Fixture {
	t.Helper()
	f := apptest.New(t, append([]apptest.Option{apptest.Size(120, 40), apptest.Colors16()}, opts...)...)
	openFromSettings(t, f, "Effect", "Screen saver effect")
	return f
}

// openFromSettings opens the picker behind a settings row, found by search.
// marker is text the picker shows and the settings page does not.
func openFromSettings(t *testing.T, f *apptest.Fixture, row, marker string) {
	t.Helper()
	f.Action("open_settings")
	f.Keys("/")
	f.Type(row)
	// The first enter leaves the search on the row it found; the row's own
	// enter opens the picker. The search may take one more, so press until
	// the picker is there, at most three times.
	for range 3 {
		f.Keys("enter")
		if _, _, ok := f.Find(marker); ok {
			return
		}
	}
	t.Fatalf("the %s picker did not open:\n%s", row, f.Text())
}

// highlighted is the cursor row's text, trimmed.
func highlighted(f *apptest.Fixture) string {
	return strings.TrimSpace(strings.Join(f.Highlighted(), " "))
}

func TestPickerFilterEditsAtTheCaret(t *testing.T) {
	f := openEffectPicker(t)
	f.Type("mtr")
	f.Keys("left", "left")
	f.Type("a")
	if _, _, ok := f.Find("matr"); !ok {
		t.Fatalf("ASSERTION: typing a after moving the caret back two did not give matr:\n%s", f.Text())
	}
	if got := highlighted(f); !strings.Contains(got, "matrix") {
		t.Errorf("ASSERTION: after the refilter the cursor is on %q, want the first match, matrix", got)
	}

	// ctrl+u clears the filter, and the whole list comes back with the
	// cursor on its first row.
	f.Keys("ctrl+u")
	if _, _, ok := f.Find("matr "); ok {
		t.Errorf("ASSERTION: ctrl+u left the filter on screen:\n%s", f.Text())
	}
	if got := highlighted(f); !strings.Contains(got, "random") {
		t.Errorf("ASSERTION: after ctrl+u the cursor is on %q, want the first row, random", got)
	}
}

func TestPickerPasteFilters(t *testing.T) {
	f := openEffectPicker(t)
	f.Send(tea.PasteMsg{Content: "matr\n"})
	if got := highlighted(f); !strings.Contains(got, "matrix") {
		t.Errorf("ASSERTION: a paste of matr did not filter to matrix, the cursor is on %q:\n%s", got, f.Text())
	}
}

// saveLayouts writes three saved layouts, the rows of the layout picker in a
// session with no daemon.
func saveLayouts(t *testing.T) {
	t.Helper()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := layouttmpl.Save(layouttmpl.Template{Name: name}); err != nil {
			t.Fatalf("save layout %s: %v", name, err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(layouttmpl.Dir()) })
}

func TestPickerListKeysComeFromConfig(t *testing.T) {
	saveLayouts(t)
	f := apptest.New(t, apptest.Size(120, 40), apptest.Colors16(),
		apptest.Config(func(c *config.UserConfig) {
			if c.Keybindings.Lists == nil {
				c.Keybindings.Lists = map[string][]string{}
			}
			c.Keybindings.Lists[config.ActionListDown] = []string{"ctrl+j"}
		}))
	f.Action("layout_prefix_load")
	first := f.Highlighted()
	if !strings.Contains(highlighted(f), "alpha") {
		t.Fatalf("the layout picker did not open on alpha:\n%s", f.Text())
	}
	f.Keys("down")
	if got := f.Highlighted(); !slices.Equal(got, first) {
		t.Errorf("ASSERTION: down moved the cursor to %q though [keybindings.lists] gives list_down only ctrl+j", got)
	}
	f.Keys("ctrl+j")
	if got := highlighted(f); !strings.Contains(got, "beta") {
		t.Errorf("ASSERTION: ctrl+j, the configured list_down, left the cursor on %q, want beta", got)
	}
}

func TestPickerClickAccepts(t *testing.T) {
	f := openEffectPicker(t)
	f.Type("matr")
	x, y, ok := f.Find("matrix")
	if !ok {
		t.Fatalf("no matrix row:\n%s", f.Text())
	}
	f.Send(tea.MouseClickMsg(tea.Mouse{X: x + 1, Y: y, Button: tea.MouseLeft}))
	f.Send(tea.MouseReleaseMsg(tea.Mouse{X: x + 1, Y: y, Button: tea.MouseLeft}))
	if _, _, ok := f.Find("Screen saver effect"); ok {
		t.Fatalf("ASSERTION: a click on a row did not accept it and close the picker:\n%s", f.Text())
	}
	if _, _, ok := f.Find("matrix"); !ok {
		t.Errorf("ASSERTION: the settings row does not show the clicked effect, matrix:\n%s", f.Text())
	}
}

func TestPickerWheelStops(t *testing.T) {
	saveLayouts(t)
	f := apptest.New(t, apptest.Size(120, 40), apptest.Colors16(),
		apptest.Settings(func(s *config.Settings) { s.WrapLists = true }))
	f.Action("layout_prefix_load")
	first := f.Highlighted()
	x, y, ok := f.Find("alpha")
	if !ok {
		t.Fatalf("no alpha row:\n%s", f.Text())
	}
	f.Wheel(x, y, -1)
	if got := f.Highlighted(); !slices.Equal(got, first) {
		t.Errorf("ASSERTION: the wheel up on the first row went to %q; a wheel never wraps", got)
	}
	f.Keys("up")
	if got := highlighted(f); !strings.Contains(got, "gamma") {
		t.Errorf("ASSERTION: up on the first row went to %q, want the last, gamma, with wrap_lists on", got)
	}
}

func TestLayoutSaveNameEditsAtTheCaret(t *testing.T) {
	t.Cleanup(func() { _ = os.RemoveAll(layouttmpl.Dir()) })
	f := apptest.New(t, apptest.Size(120, 40), apptest.Colors16(), apptest.Panes(2))
	f.Action("layout_prefix_save")
	f.Type("wrk")
	f.Keys("left", "left")
	f.Type("o")
	if _, _, ok := f.Find("work"); !ok {
		t.Fatalf("ASSERTION: the name field does not show work after editing at the caret:\n%s", f.Text())
	}
	f.Keys("enter")
	if _, err := os.Stat(layouttmpl.Path("work")); err != nil {
		t.Errorf("ASSERTION: enter did not save the layout as work: %v", err)
	}
}

// TestPickersShareTheEditorAndTheListKeys runs the same two checks on every
// picker that rides the shared list state: the filter edits at the caret and
// ctrl+u clears it, and the cursor moves on the list key [keybindings.lists]
// gives and not on the default it replaced.
func TestPickersShareTheEditorAndTheListKeys(t *testing.T) {
	doors := []struct {
		name    string
		prepare func(t *testing.T)
		open    func(t *testing.T, f *apptest.Fixture)
	}{
		{name: "command palette", open: func(t *testing.T, f *apptest.Fixture) { f.Action("command_palette") }},
		{name: "launcher", prepare: programsOnPath, open: func(t *testing.T, f *apptest.Fixture) { f.Action("launcher") }},
		{name: "theme picker", open: func(t *testing.T, f *apptest.Fixture) { openFromSettings(t, f, "Theme", "preview") }},
		{name: "glyph picker", open: func(t *testing.T, f *apptest.Fixture) { openFromSettings(t, f, "Glyph set", "preview") }},
		{name: "effect picker", open: func(t *testing.T, f *apptest.Fixture) {
			openFromSettings(t, f, "Effect", "Screen saver effect")
		}},
	}
	for _, d := range doors {
		t.Run(d.name, func(t *testing.T) {
			if d.prepare != nil {
				d.prepare(t)
			}
			f := apptest.New(t, apptest.Size(120, 40), apptest.Colors16(),
				apptest.Config(func(c *config.UserConfig) {
					if c.Keybindings.Lists == nil {
						c.Keybindings.Lists = map[string][]string{}
					}
					c.Keybindings.Lists[config.ActionListDown] = []string{"ctrl+j"}
				}))
			d.open(t, f)
			f.Keys("home")
			first := f.Highlighted()
			f.Keys("down")
			if got := f.Highlighted(); !slices.Equal(got, first) {
				t.Errorf("ASSERTION: down moved the cursor to %q though list_down is only ctrl+j", got)
			}
			f.Keys("ctrl+j")
			if got := f.Highlighted(); slices.Equal(got, first) {
				t.Errorf("ASSERTION: ctrl+j, the configured list_down, did not move the cursor from %q", first)
			}

			f.Type("qz")
			f.Keys("left")
			f.Type("x")
			if _, _, ok := f.Find("qxz"); !ok {
				t.Fatalf("ASSERTION: typing x after moving the caret back one did not give qxz:\n%s", f.Text())
			}
			f.Keys("ctrl+u")
			if _, _, ok := f.Find("qxz"); ok {
				t.Errorf("ASSERTION: ctrl+u left the filter on screen:\n%s", f.Text())
			}
		})
	}
}

// TestListsWithoutAFilterTakeTheConfiguredKeys: a list that used to hard-code
// up, k, down and j now moves on the keys [keybindings.lists] gives. The
// accent picker stands for them, as the one a person opens with a key; the
// dropdown, the integration question, the copy dialog and the task form read
// the same keys through listDelta.
//
// How it could pass wrongly: a screen that does not change at all would make
// the first half pass, so the second half needs the rebound key to change it.
func TestListsWithoutAFilterTakeTheConfiguredKeys(t *testing.T) {
	f := apptest.New(t, apptest.Size(120, 40), apptest.Colors16(), apptest.Panes(1),
		apptest.Config(func(c *config.UserConfig) {
			if c.Keybindings.Lists == nil {
				c.Keybindings.Lists = map[string][]string{}
			}
			c.Keybindings.Lists[config.ActionListDown] = []string{"ctrl+j"}
		}))
	f.Action("set_accent")
	before := f.Text()
	f.Keys("down")
	if got := f.Text(); got != before {
		t.Errorf("ASSERTION: down moved the accent picker though list_down is only ctrl+j:\n%s", got)
	}
	f.Keys("ctrl+j")
	if got := f.Text(); got == before {
		t.Errorf("ASSERTION: ctrl+j, the configured list_down, did not move the accent picker:\n%s", got)
	}
}

// TestTheOldSwitchersOpenGoto: the keys and entries that opened the session
// switcher, the workspace switcher and the window list open the go-to picker
// with the filter that lists the same places, and goto does what the
// switchers did with a row.
//
// How it could pass wrongly: goto could open with an empty query, which also
// lists sessions and workspaces among everything else, so the query on
// screen is checked, not only the title.
func TestTheOldSwitchersOpenGoto(t *testing.T) {
	open := func(t *testing.T) *apptest.Fixture {
		t.Helper()
		return apptest.New(t, apptest.Size(120, 40), apptest.Colors16(),
			apptest.Workspaces(map[int]int{1: 1, 2: 1}))
	}
	queryLine := func(f *apptest.Fixture) string {
		for _, row := range f.Screen() {
			if strings.Contains(row, "› ") && !strings.Contains(row, "Go to") {
				return row
			}
		}
		return ""
	}

	t.Run("the session switcher's key lists the sessions", func(t *testing.T) {
		f := open(t)
		f.Action("prefix_session_switcher")
		if _, _, ok := f.Find("Go to"); !ok || !strings.Contains(queryLine(f), "#") {
			t.Fatalf("ASSERTION: prefix_session_switcher did not open go-to on #:\n%s", f.Text())
		}
		// A name nothing has: enter makes that session, and the list says so.
		f.Type("newsess")
		if _, _, ok := f.Find("No session 'newsess': enter makes it"); !ok {
			t.Errorf("ASSERTION: a session name nothing has does not say enter makes it:\n%s", f.Text())
		}
		// ctrl+d on the session the client shows is refused, as before.
		f.Keys("ctrl+u")
		f.Type("# ")
		f.Keys("ctrl+d")
		if _, _, ok := f.Find("Cannot delete the current session"); !ok {
			t.Errorf("ASSERTION: ctrl+d on the current session was not refused:\n%s", f.Text())
		}
	})

	t.Run("the workspace switcher's key lists this session's workspaces", func(t *testing.T) {
		f := open(t)
		f.Action("prefix_workspace_switcher")
		if !strings.Contains(queryLine(f), "!") {
			t.Fatalf("ASSERTION: prefix_workspace_switcher did not open go-to on !:\n%s", f.Text())
		}
		// ctrl+r renames the workspace under the cursor. The rename takes the
		// keys, and go-to stays open under it, as the switcher did. The row
		// reading the new name needs a daemon: the e2e rename test checks it.
		f.Keys("ctrl+r")
		f.Type("deploys")
		f.Keys("enter")
		if f.M.Renaming() {
			t.Fatalf("ASSERTION: enter did not commit the workspace rename:\n%s", f.Text())
		}
		if _, _, ok := f.Find("Go to"); !ok {
			t.Errorf("ASSERTION: the rename closed go-to:\n%s", f.Text())
		}
		if strings.Contains(queryLine(f), "deploys") {
			t.Errorf("ASSERTION: the rename's keys reached go-to's query:\n%s", f.Text())
		}
	})

	t.Run("the window list lists this session's panes", func(t *testing.T) {
		f := open(t)
		f.Action("command_palette")
		f.Type("All windows")
		f.Keys("enter")
		if !strings.Contains(queryLine(f), "kind:pane") {
			t.Fatalf("ASSERTION: All windows did not open go-to on kind:pane:\n%s", f.Text())
		}
	})
}
