package app

import (
	"strconv"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// RenameKind names what an in-progress rename points at. There is one rename
// editor and one dialog for all three, so a user who has renamed a pane already
// knows how to rename a session.
type RenameKind int

const (
	// RenameNone means no rename is running.
	RenameNone RenameKind = iota
	// RenameWindow targets a window's custom name.
	RenameWindow
	// RenameSession targets a session's name, which the daemon owns. The
	// rename goes to the daemon, and the new name comes back on a state push.
	RenameSession
	// RenameWorkspace targets a workspace's name. The number stays its identity.
	RenameWorkspace
	// RenameNote edits a session's return note, which the daemon owns. The
	// same one-line editor, and an empty note clears it.
	RenameNote
)

// Renaming reports whether a rename editor is open, whatever it targets.
func (m *OS) Renaming() bool { return m.RenameKind != RenameNone }

// BeginRenameWindow starts an inline rename of a window, seeded with the name it
// already carries. There is one rename in flight at a time and it names the
// window it targets, so the editor can be drawn wherever that window shows: the
// pane's title bar, its sidebar row, or both at once.
func (m *OS) BeginRenameWindow(w *terminal.Window) {
	if w == nil {
		return
	}
	m.RenameKind = RenameWindow
	m.RenameTargetID = w.ID
	m.rename.Set(w.CustomName)
	w.InvalidateCache()
}

// BeginRenameSession starts a rename of a session, seeded with what the
// session shows now: its name, or its display label when a script set one.
func (m *OS) BeginRenameSession(name string) {
	if name == "" {
		return
	}
	m.RenameKind = RenameSession
	m.RenameTargetID = name
	m.rename.Set(m.SessionLabel(name))
}

// BeginEditNote opens the one-line editor on a session's return note, seeded
// with the note it has now.
func (m *OS) BeginEditNote(name string) {
	if name == "" {
		return
	}
	m.RenameKind = RenameNote
	m.RenameTargetID = name
	m.rename.Set(m.sessionNote(name))
}

// BeginRenameWorkspace starts a rename of a workspace, seeded with its current
// name. An unnamed workspace opens empty: its number is not a name it was given.
func (m *OS) BeginRenameWorkspace(ws int) {
	// A scratch group's workspace has no name to give: its box shows the
	// group's name.
	if ws <= 0 || session.IsScratchWorkspace(ws) {
		return
	}
	m.RenameKind = RenameWorkspace
	m.RenameTargetID = strconv.Itoa(ws)
	m.rename.Set(m.WorkspaceNames[ws])
}

// RenameText is the rename editor's text.
func (m *OS) RenameText() string { return m.rename.String() }

// SetRenameText replaces the rename editor's text, the caret at its end.
func (m *OS) SetRenameText(s string) { m.rename.Set(s) }

// renameClean is cleanName, and a note takes no more than the daemon keeps.
func (m *OS) renameClean(text string) string {
	add := cleanName(text)
	if add != "" && m.RenameKind == RenameNote && len(m.rename.String())+len(add) > session.NoteMaxBytes {
		return ""
	}
	return add
}

// RenameAppend types text at the rename editor's caret, as a paste does.
func (m *OS) RenameAppend(text string) {
	if m.Renaming() && lineInsert(&m.rename, text, m.renameClean) {
		m.renameChanged()
	}
}

// RenameKey applies one key press to the rename editor (see lineKey). It
// reports whether the key was the editor's.
func (m *OS) RenameKey(key, text string) bool {
	if !m.Renaming() {
		return false
	}
	changed, consumed := lineKey(&m.rename, key, text, m.renameClean)
	if changed {
		m.renameChanged()
	}
	return consumed
}

// RenameClear empties the editor, so a name can be retyped from scratch.
func (m *OS) RenameClear() {
	if !m.Renaming() || m.rename.String() == "" {
		return
	}
	m.rename.Set("")
	m.renameChanged()
}

// renameChanged redraws the thing being renamed, whose title shows the draft.
func (m *OS) renameChanged() {
	if t := m.RenameTarget(); t != nil {
		t.InvalidateCache()
	}
}

// combiningOnly reports whether s is nothing but marks that attach to a
// preceding character.
func combiningOnly(s string) bool {
	for _, r := range s {
		if !unicode.In(r, unicode.Mn, unicode.Me) {
			return false
		}
	}
	return true
}

// BeginRenameCurrentWorkspace starts a rename of the workspace the user meant:
// the one whose pill menu the action came from, or the one they are on when it
// came from a key. Both entry points land here so the dialog is the same either
// way.
func (m *OS) BeginRenameCurrentWorkspace() {
	ws := m.TakeMenuWorkspace()
	if ws <= 0 {
		ws = m.CurrentWorkspace
	}
	m.BeginRenameWorkspace(ws)
}

// RenameTarget is the window an in-progress rename applies to, or nil when no
// window rename is running or the window went away under it.
func (m *OS) RenameTarget() *terminal.Window {
	if m.RenameKind != RenameWindow {
		return nil
	}
	if i := m.windowIndexByID(m.RenameTargetID); i >= 0 {
		return m.Windows[i]
	}
	return nil
}

// RenameDialogTitle names what the open editor is renaming, so the dialog says
// which of the three it is about to change.
//
// A session is named as well as typed, the way a workspace already was. The
// editor is reachable from any session's rail row, so a title that said only
// "rename session" left the user to work out whether it had landed on the row
// they pointed at or on the session they happen to be attached to.
func (m *OS) RenameDialogTitle() string {
	switch m.RenameKind {
	case RenameSession:
		if label := m.SessionLabel(m.RenameTargetID); label != "" {
			return "rename session " + label
		}
		return "rename session"
	case RenameWorkspace:
		return "rename workspace " + m.RenameTargetID
	case RenameNote:
		return "note for " + m.SessionLabel(m.RenameTargetID)
	default:
		return "rename"
	}
}

// CommitRename applies the rename in progress and clears the editor. A window
// name is client-owned and applied here; a session or workspace name is
// daemon-owned and returned as a command, because reaching the daemon is a
// blocking round trip that must not run on the Update goroutine.
func (m *OS) CommitRename() tea.Cmd {
	kind, target := m.RenameKind, m.RenameTargetID
	label := strings.TrimSpace(m.rename.String())
	defer m.EndRename()

	if kind == RenameWindow {
		if w := m.RenameTarget(); w != nil {
			_ = m.RenameWindowByID(w.ID, m.rename.String())
			w.InvalidateCache()
		}
		return nil
	}
	if kind == RenameNote {
		if target == "" || label == m.sessionNote(target) {
			return nil
		}
		return labelVerbCmd(m.verbDialer(), "Note", "set-note", map[string]any{"session": target, "note": label})
	}
	if kind == RenameSession && label == m.SessionLabel(target) {
		// Nothing changed, so there is nothing to send. A session that
		// shows a display label keeps it.
		return nil
	}
	verb, params, ok := renameVerb(kind, target, m.SessionName, label)
	if !ok {
		return nil
	}
	return labelVerbCmd(m.verbDialer(), "Rename", verb, params)
}

// renameVerb picks the daemon verb a rename goes through and builds its params.
// A session rename changes the session's name with rename-session, so the name
// the UI shows is the name ls lists and attach takes (issue #266). It used to
// send set-session-name, which only set a display label.
// An empty name is not sent: a session cannot be without a name.
func renameVerb(kind RenameKind, target, sessionName, label string) (string, map[string]any, bool) {
	switch kind {
	case RenameSession:
		if target == "" || label == "" {
			return "", nil, false
		}
		return "rename-session", map[string]any{"session": target, "name": label}, true
	case RenameWorkspace:
		ws, err := strconv.Atoi(target)
		if err != nil || ws <= 0 {
			return "", nil, false
		}
		return "set-workspace-name", map[string]any{
			"session": sessionName, "workspace": ws, "name": label,
		}, true
	default:
		return "", nil, false
	}
}

// EndRename clears the rename state, committed or cancelled.
func (m *OS) EndRename() {
	m.RenameKind = RenameNone
	m.RenameTargetID = ""
	m.rename.Set("")
	m.renameHit = overlay.Rect{}
	m.renameClearHit = overlay.Rect{}
}

// RenameMouseClick routes a click while the rename dialog is up: the clear
// hint empties the field, the rest of the dialog is a no-op, and outside
// cancels. The dialog is modal to the mouse either way, so a stray click can
// never leave an editor open over a pane the user has moved on to. The mouse
// is never required: the same keys that opened it commit or cancel it.
func (m *OS) RenameMouseClick(x, y int) bool {
	if !m.Renaming() {
		return false
	}
	switch {
	case m.renameClearHit.Contains(x, y):
		m.RenameClear()
	case !m.renameHit.Contains(x, y):
		m.EndRename()
	}
	return true
}
