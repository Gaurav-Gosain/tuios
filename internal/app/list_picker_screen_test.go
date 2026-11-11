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
	f.Action("open_settings")
	f.Keys("/")
	f.Type("Effect")
	// The first enter leaves the search on the row it found; the row's own
	// enter opens the picker. The search may take one more, so press until
	// the picker is there, at most three times.
	for range 3 {
		f.Keys("enter")
		if _, _, ok := f.Find("Screen saver effect"); ok {
			return f
		}
	}
	t.Fatalf("the effect picker did not open:\n%s", f.Text())
	return nil
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
