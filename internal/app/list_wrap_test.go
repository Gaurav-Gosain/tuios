package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
)

// wrapCase is one list: how to put rows in it, how to move it, and where its
// cursor is. first and last are the rows a wrap lands on, which differ from 0
// and n-1 in a list with headings.
type wrapCase struct {
	name        string
	setup       func(m *OS)
	move        func(m *OS, delta int)
	cursor      func(m *OS) int
	set         func(m *OS, i int)
	first, last int
}

func wrapCases(t *testing.T) []wrapCase {
	t.Helper()
	sessions := []sessiontree.Node{{ID: "a", Title: "a"}, {ID: "b", Title: "b"}, {ID: "c", Title: "c"}}
	return []wrapCase{
		{
			name:   "settings rows",
			setup:  func(m *OS) { m.OpenSettings() },
			move:   (*OS).SettingsMove,
			cursor: func(m *OS) int { return m.SettingsSelected },
			set:    func(m *OS, i int) { m.SettingsSelected = i },
			first:  0, last: len(m0(t).settingsCategories()[0].Items) - 1,
		},
		{
			name:   "settings tabs",
			setup:  func(m *OS) { m.OpenSettings() },
			move:   func(m *OS, d int) { m.settingsStepCategory(d) },
			cursor: func(m *OS) int { return m.SettingsCategory },
			set:    func(m *OS, i int) { m.SettingsCategory = i },
			first:  0, last: len(m0(t).settingsCategories()) - 1,
		},
		{
			name: "command palette",
			setup: func(m *OS) {
				m.ShowCommandPalette = true
				m.PaletteItems = []CommandPaletteItem{{Name: "one"}, {Name: "two"}, {Name: "three"}}
			},
			move:   (*OS).PaletteMove,
			cursor: func(m *OS) int { return m.CommandPaletteSelected },
			set:    func(m *OS, i int) { m.CommandPaletteSelected = i },
			first:  0, last: 2,
		},
		{
			name: "launcher",
			setup: func(m *OS) {
				*m = *runTestOS(t)
				m.Settings.WrapLists = true
				seedLauncher(t, m, "aa", "bb", "cc")
			},
			move:   (*OS).LauncherMove,
			cursor: func(m *OS) int { return m.LauncherSelected },
			set:    func(m *OS, i int) { m.LauncherSelected = i },
			first:  0, last: 2,
		},
		{
			name: "keybind manager",
			setup: func(m *OS) {
				m.KeybindRegistry = config.NewKeybindRegistry(m.UserConfig)
				m.OpenKeybindManager()
			},
			move:   (*OS).KeybindMove,
			cursor: func(m *OS) int { return m.KeybindSelected() },
			set:    func(m *OS, i int) { m.KeybindMove(i - m.KeybindSelected()) },
			first:  0, last: keybindRows(t) - 1,
		},
		{
			name:   "session switcher",
			setup:  func(m *OS) { m.SessionSwitcherItems = sessions },
			move:   (*OS).SessionSwitcherMove,
			cursor: func(m *OS) int { return m.SessionSwitcherSelected },
			set:    func(m *OS, i int) { m.SessionSwitcherSelected = i },
			first:  0, last: 2,
		},
		{
			name:   "workspace switcher",
			move:   func(m *OS, d int) { m.WorkspaceSwitcherMove(d, 4) },
			cursor: func(m *OS) int { return m.WorkspaceSwitcherSelected },
			set:    func(m *OS, i int) { m.WorkspaceSwitcherSelected = i },
			first:  0, last: 3,
		},
		{
			name: "layout picker",
			setup: func(m *OS) {
				m.LayoutPickerItems = []LayoutTemplate{{Name: "a"}, {Name: "b"}, {Name: "c"}}
			},
			move:   (*OS).LayoutPickerMove,
			cursor: func(m *OS) int { return m.LayoutPickerSelected },
			set:    func(m *OS, i int) { m.LayoutPickerSelected = i },
			first:  0, last: 2,
		},
		{
			name: "machine picker",
			setup: func(m *OS) {
				m.HostPickerItems = []HostPickerItem{{Name: "", Label: "here"}, {Name: "b", Label: "b"}, {Name: "c", Label: "c"}}
			},
			move:   (*OS).HostPickerMove,
			cursor: func(m *OS) int { return m.HostPickerSelected },
			set:    func(m *OS, i int) { m.HostPickerSelected = i },
			first:  0, last: 2,
		},
		{
			name: "quit menu",
			setup: func(m *OS) {
				m.QuitMenuItems = []QuitMenuItem{{Label: "a"}, {Label: "b"}, {Label: "c"}}
			},
			move:   (*OS).QuitMenuMove,
			cursor: func(m *OS) int { return m.QuitMenuSelected },
			set:    func(m *OS, i int) { m.QuitMenuSelected = i },
			first:  0, last: 2,
		},
		{
			name: "context menu",
			setup: func(m *OS) {
				// A separator at the end and a dimmed row at the start: the wrap
				// lands on the rows that can be run.
				m.ContextMenu = &ContextMenu{Items: []ContextMenuItem{
					{Label: "off", Dim: true}, {Label: "a"}, {Label: "b"}, {Label: "c"}, {Sep: true},
				}}
			},
			move:   (*OS).ContextMenuMove,
			cursor: func(m *OS) int { return m.ContextMenu.Selected },
			set:    func(m *OS, i int) { m.ContextMenu.Selected = i },
			first:  1, last: 3,
		},
		{
			name: "tape manager",
			setup: func(m *OS) {
				m.TapeManager = &TapeManagerState{Files: []TapeFile{{Name: "a"}, {Name: "b"}, {Name: "c"}}}
			},
			move:   (*OS).TapeManagerMove,
			cursor: func(m *OS) int { return m.TapeManager.SelectedIndex },
			set:    func(m *OS, i int) { m.TapeManager.SelectedIndex = i },
			first:  0, last: 2,
		},
		{
			name:   "dock editor",
			setup:  func(m *OS) { m.OpenDockEditor() },
			move:   (*OS).DockEditorMove,
			cursor: func(m *OS) int { return m.DockEditorSelected },
			set:    func(m *OS, i int) { m.DockEditorSelected = i },
			first:  firstRestable(m0(t).dockEditorRowsFresh(), false), last: firstRestable(m0(t).dockEditorRowsFresh(), true),
		},
		{
			name:   "rail section editor",
			setup:  func(m *OS) { m.OpenSectionEditor() },
			move:   (*OS).SectionEditorMove,
			cursor: func(m *OS) int { return m.SectionEditorSelected },
			set:    func(m *OS, i int) { m.SectionEditorSelected = i },
			first:  firstRestable(m0(t).sectionEditorRowsFresh(), false), last: firstRestable(m0(t).sectionEditorRowsFresh(), true),
		},
	}
}

// keybindRows is how many rows the keybind manager opens with.
func keybindRows(t *testing.T) int {
	t.Helper()
	return len(keybindOS(t).FilteredKeybindRows())
}

// m0 is a session with a default config, for reading how long a list is.
func m0(t *testing.T) *OS {
	t.Helper()
	return wrapOS()
}

func wrapOS() *OS {
	m := &OS{Settings: config.Global, Width: 120, Height: 40, UserConfig: config.DefaultConfig()}
	m.Settings.WrapLists = true
	return m
}

// dockEditorRowsFresh and sectionEditorRowsFresh are each editor's rows as a
// person first sees them, reported as whether each row can hold the cursor.
func (m *OS) dockEditorRowsFresh() []bool {
	m.OpenDockEditor()
	var out []bool
	for _, r := range m.dockEditorRows() {
		out = append(out, r.Kind != dockRowHeader)
	}
	return out
}

func (m *OS) sectionEditorRowsFresh() []bool {
	m.OpenSectionEditor()
	var out []bool
	for _, r := range m.sectionEditorRows() {
		out = append(out, r.Kind != railRowHeader)
	}
	return out
}

// firstRestable is the first row that can hold the cursor, from the end when
// fromEnd is set.
func firstRestable(rows []bool, fromEnd bool) int {
	if fromEnd {
		for i := len(rows) - 1; i >= 0; i-- {
			if rows[i] {
				return i
			}
		}
		return -1
	}
	for i, ok := range rows {
		if ok {
			return i
		}
	}
	return -1
}

// TestListsWrapAtTheirEnds is the maintainer's rule, on every list it
// applies to: up on the first row goes to the last, down on the last goes to
// the first, and with appearance.wrap_lists off both stop where they are.
func TestListsWrapAtTheirEnds(t *testing.T) {
	for _, c := range wrapCases(t) {
		t.Run(c.name, func(t *testing.T) {
			m := wrapOS()
			if c.setup != nil {
				c.setup(m)
			}

			c.set(m, c.first)
			c.move(m, -1)
			if got := c.cursor(m); got != c.last {
				t.Errorf("up on the first row (%d) went to %d, want the last (%d)", c.first, got, c.last)
			}
			c.move(m, 1)
			if got := c.cursor(m); got != c.first {
				t.Errorf("down on the last row (%d) went to %d, want the first (%d)", c.last, got, c.first)
			}

			// A page never wraps.
			c.set(m, c.last)
			c.move(m, 10)
			if got := c.cursor(m); got != c.last {
				t.Errorf("a page down from the last row went to %d, want it to stay at %d", got, c.last)
			}

			m.Settings.WrapLists = false
			c.set(m, c.first)
			c.move(m, -1)
			if got := c.cursor(m); got != c.first {
				t.Errorf("with wrap_lists off, up on the first row went to %d", got)
			}
			c.set(m, c.last)
			c.move(m, 1)
			if got := c.cursor(m); got != c.last {
				t.Errorf("with wrap_lists off, down on the last row went to %d", got)
			}
		})
	}
}

// TestTheWheelNeverWraps pins the one exception: a wheel is flung, not
// pressed, so a list scrolled with it stops at its ends whatever the setting.
func TestTheWheelNeverWraps(t *testing.T) {
	m := wrapOS()
	m.QuitMenuItems = []QuitMenuItem{{Label: "a"}, {Label: "b"}, {Label: "c"}}
	m.wheelMoving = true
	m.QuitMenuMove(-1)
	if m.QuitMenuSelected != 0 {
		t.Errorf("a wheel move up from the first row went to %d", m.QuitMenuSelected)
	}
	m.wheelMoving = false
	m.QuitMenuMove(-1)
	if m.QuitMenuSelected != 2 {
		t.Errorf("a key move up from the first row went to %d, want 2", m.QuitMenuSelected)
	}
}

// TestConfirmationsDoNotWrap pins the exception for the two-row confirmations:
// the cursor opens on Cancel, and up from Cancel must not land on the answer
// that kills or deletes.
func TestConfirmationsDoNotWrap(t *testing.T) {
	m := wrapOS()
	m.OpenSessionCloseFor("")
	m.SessionCloseMove(-1)
	if m.SessionCloseSelected != SessionCloseRowCancel {
		t.Errorf("up from Cancel in the close-session dialog went to row %d", m.SessionCloseSelected)
	}

	m.filePrompt.Kind = filePromptConfirm
	m.filePrompt.Selected = fileConfirmRowCancel
	m.FileConfirmMove(-1)
	if m.filePrompt.Selected != fileConfirmRowCancel {
		t.Errorf("up from Cancel in the file dialog went to row %d", m.filePrompt.Selected)
	}
}
