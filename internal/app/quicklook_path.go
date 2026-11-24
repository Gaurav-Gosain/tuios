package app

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

// OpenQuickLookPath opens quick look on a path of this machine, at line when
// it is more than 0: the preview a click on a file link or a path opens, and a
// hint's Ctrl. A folder shows its listing.
//
// It is the rail's preview with no rail row under it. It does not follow the
// rail's cursor, it opens over whatever is on screen (a pane, the agents
// view), and the keyboard goes inside the file or the folder at once, since
// there is no list for keys to walk. esc closes it.
func (m *OS) OpenQuickLookPath(path string, line int, dir bool) tea.Cmd {
	if m.quickLook.open {
		m.CloseQuickLook()
	}
	t := quickLookTarget{path: path, name: filepath.Base(path), dir: dir}
	if m.learnFiles() {
		t.refuse = "Quick look is not in this demo."
	}
	ql := &m.quickLook
	ql.open, ql.free = true, true
	cmd := m.quickLookShow(t)
	ql.inside = true
	ql.wantLine = line
	return cmd
}

// QuickLookFree reports whether quick look shows a path a click or a hint
// opened, which takes the keyboard itself (see OpenQuickLookPath).
func (m *OS) QuickLookFree() bool { return m.quickLook.open && m.quickLook.free }

// QuickLookOpenEditor opens the previewed file in the person's editor, in a
// new pane, at the line at the top of the preview. A file of another machine
// has no pane here to open it in.
func (m *OS) QuickLookOpenEditor() tea.Cmd {
	ql := &m.quickLook
	t := ql.target
	if !ql.open || t.dir || t.up || t.path == "" {
		return nil
	}
	if t.host != "" {
		m.ShowNotification("The file is on another machine. Open it in a pane there.", "info", m.Settings.NotificationDuration)
		return nil
	}
	line := 0
	if r := ql.res; quickLookText(r) && r.firstLine > 0 {
		line = r.firstLine + ql.scroll
	}
	m.CloseQuickLook()
	return m.openInEditor(t.path, line)
}
