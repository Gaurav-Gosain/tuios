package app

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
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
	return []wrapCase{
		{
			name: "layout picker",
			setup: func(m *OS) {
				m.LayoutPickerItems = []LayoutTemplate{{Name: "a"}, {Name: "b"}, {Name: "c"}}
			},
			move:   func(m *OS, d int) { layoutPicker().move(m, d) },
			cursor: func(m *OS) int { return m.layoutList.Cursor },
			set:    func(m *OS, i int) { m.layoutList.Cursor = i },
			first:  0, last: 2,
		},
		{
			name: "machine picker",
			setup: func(m *OS) {
				m.HostPickerItems = []HostPickerItem{{Name: "", Label: "here"}, {Name: "b", Label: "b"}, {Name: "c", Label: "c"}}
			},
			move:   func(m *OS, d int) { hostPicker().move(m, d) },
			cursor: func(m *OS) int { return m.hostList.Cursor },
			set:    func(m *OS, i int) { m.hostList.Cursor = i },
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
	}
}

func wrapOS() *OS {
	m := &OS{Settings: config.Global, Width: 120, Height: 40, UserConfig: config.DefaultConfig()}
	m.Settings.WrapLists = true
	return m
}

// TestDaemonListsWrapAtTheirEnds is TestListsWrapAtTheirEnds
// (list_wrap_screen_test.go) for the lists whose rows only a daemon or a
// machine link provides. They move here, onto apptest's live tier, when it
// lands; until then the model is driven directly.
func TestDaemonListsWrapAtTheirEnds(t *testing.T) {
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
