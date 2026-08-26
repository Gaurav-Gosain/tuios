package app

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// The rects the renderer records must point at the rows it actually drew.
//
// This is the constraint the whole overlay family is held to: a hit rectangle
// is recorded by the renderer as it draws, never recomputed in a handler. The
// way that goes wrong is not a crash, it is every click landing one row off, so
// the test reads the rendered panel back and checks that the line each rect
// names contains the row it claims.
func TestKeybindRowRectsPointAtTheRowsDrawn(t *testing.T) {
	for _, tab := range []struct {
		name string
		idx  int
		// key returns the text that must appear on row i of this tab.
		key func(m *OS, i int) string
		// seed puts rows on the tab. The conflicts tab needs one, because the
		// defaults no longer contest any key (TestDefaultConfigHasNoConflicts)
		// and a tab with no rows records no rects. It used to rely on the four
		// dead digit bindings that shipped, which made this test another reason
		// they were awkward to fix.
		seed func(*config.UserConfig)
	}{
		{
			name: "bindings", idx: KeybindTabBindings,
			key: func(m *OS, i int) string { return m.FilteredKeybindRows()[i].Press },
		},
		{
			name: "conflicts", idx: KeybindTabConflicts,
			key: func(m *OS, i int) string { return m.KeybindReport().Collisions[i].Winner },
			seed: func(c *config.UserConfig) {
				// Two actions in one scope on one key, in two tables.
				c.Keybindings.WindowManagement["select_window_1"] = []string{"1"}
				c.Keybindings.Layout["snap_left"] = []string{"1"}
			},
		},
		{
			name: "guests", idx: KeybindTabGuests,
			key: func(m *OS, i int) string { return m.KeybindReport().GuestClashes[i].Key },
		},
	} {
		t.Run(tab.name, func(t *testing.T) {
			m := newNarrowOS(t, 140, 45)
			if tab.seed != nil {
				tab.seed(m.UserConfig)
				m.KeybindRegistry.Reload(m.UserConfig)
			}
			m.OpenKeybindManager()
			m.KeybindSetTab(tab.idx)

			content, _, rows := m.renderKeybindManager()
			if len(rows) == 0 {
				t.Fatalf("no hit rects recorded for the %s tab", tab.name)
			}
			lines := strings.Split(content, "\n")

			for _, r := range rows {
				if r.Rect.Y0 < 0 || r.Rect.Y0 >= len(lines) {
					t.Fatalf("rect for row %d points at line %d of a %d-line panel",
						r.Idx, r.Rect.Y0, len(lines))
				}
				want := tab.key(m, r.Idx)
				got := ansiEscape.ReplaceAllString(lines[r.Rect.Y0], "")
				if !strings.Contains(got, want) {
					t.Errorf("rect for row %d points at line %d, which reads %q and does not contain %q",
						r.Idx, r.Rect.Y0, strings.TrimSpace(got), want)
				}
			}
		})
	}
}

// Scrolling moves the rects with the rows. A rect that kept its index while the
// window moved under it is the same off-by-one bug, arrived at from the other
// side.
func TestKeybindRowRectsFollowTheScroll(t *testing.T) {
	m := newNarrowOS(t, 140, 30)
	m.OpenKeybindManager()
	m.KeybindMove(40)

	content, _, rows := m.renderKeybindManager()
	if len(rows) == 0 {
		t.Fatal("no hit rects after scrolling")
	}
	lines := strings.Split(content, "\n")
	all := m.FilteredKeybindRows()

	if rows[0].Idx == 0 {
		t.Fatal("the list did not scroll, so this proves nothing")
	}
	for _, r := range rows {
		got := ansiEscape.ReplaceAllString(lines[r.Rect.Y0], "")
		if !strings.Contains(got, all[r.Idx].Press) {
			t.Errorf("after scrolling, rect for row %d reads %q, want it to contain %q",
				r.Idx, strings.TrimSpace(got), all[r.Idx].Press)
		}
	}
}

// The Record tab records no rows, because there is nothing on it to click. A
// stale rect left over from a list tab would route a click to a row that is no
// longer drawn.
func TestRecordTabRecordsNoRows(t *testing.T) {
	m := newNarrowOS(t, 140, 45)
	m.OpenKeybindManager()
	if _, _, rows := m.renderKeybindManager(); len(rows) == 0 {
		t.Fatal("the bindings tab should record rows")
	}
	m.KeybindSetTab(KeybindTabRecord)
	if _, _, rows := m.renderKeybindManager(); len(rows) != 0 {
		t.Errorf("the record tab has no clickable rows, got %d rects", len(rows))
	}
}

// Every row spans the panel, so the selection highlight and the click target
// are the same rectangle. A row narrower than the panel is a strip the user can
// see highlighted but cannot click.
func TestKeybindRowsSpanThePanel(t *testing.T) {
	m := newNarrowOS(t, 140, 45)
	m.OpenKeybindManager()
	_, geo, rows := m.renderKeybindManager()
	for _, r := range rows {
		if r.Rect.X0 != 0 || r.Rect.X1 != geo.Width {
			t.Errorf("row %d spans [%d,%d), panel is %d wide", r.Idx, r.Rect.X0, r.Rect.X1, geo.Width)
		}
	}
}

// A guest clash for the program actually running is marked in words, not only
// in colour, and the mark says which of the two things it is.
func TestRunningGuestIsMarkedInWords(t *testing.T) {
	cfg := config.DefaultConfig()
	m := newNarrowOS(t, 140, 45)
	m.UserConfig = cfg
	m.KeybindRegistry = config.NewKeybindRegistry(cfg)
	m.OpenKeybindManager()
	// Stand in for a pane running tmux, which is what the default leader
	// collides with.
	m.keybinds.report = m.KeybindRegistry.Report(config.PaneFacts{Command: "tmux"})
	m.KeybindSetTab(KeybindTabGuests)

	content, _, _ := m.renderKeybindManager()
	plain := ansiEscape.ReplaceAllString(content, "")
	if !strings.Contains(plain, "now") {
		t.Errorf("the running program's row must say so in words:\n%s", plain)
	}
	if !strings.Contains(strings.ToLower(plain), "program list") {
		t.Errorf("the tab must say its findings come from the program list:\n%s", plain)
	}
}
