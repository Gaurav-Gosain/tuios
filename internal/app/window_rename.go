package app

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
	// RenameSession targets a session's display name. It never touches the
	// session's identity, which stays the name it is addressed and persisted by.
	RenameSession
	// RenameWorkspace targets a workspace's name. The number stays its identity.
	RenameWorkspace
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
	m.RenameBuffer = w.CustomName
	w.InvalidateCache()
}

// BeginRenameSession starts a rename of a session's display label, seeded with
// the label it already has. The seed is the label and not the identity name, so
// an empty field means "no label" rather than "about to overwrite the identity".
func (m *OS) BeginRenameSession(name string) {
	if name == "" {
		return
	}
	m.RenameKind = RenameSession
	m.RenameTargetID = name
	m.RenameBuffer = ""
	if display, _ := m.daemonSessionLabel(name); display != "" {
		m.RenameBuffer = display
	}
}

// BeginRenameWorkspace starts a rename of a workspace, seeded with its current
// name. An unnamed workspace opens empty: its number is not a name it was given.
func (m *OS) BeginRenameWorkspace(ws int) {
	if ws <= 0 {
		return
	}
	m.RenameKind = RenameWorkspace
	m.RenameTargetID = strconv.Itoa(ws)
	m.RenameBuffer = m.WorkspaceNames[ws]
}

// RenameAppend adds typed text to the rename buffer, keeping only what the
// chrome will actually draw. The gate is printableRune, the same rule the rail,
// title bar, palette and dock launder names through, so the editor can never
// take a codepoint that would vanish or tofu the moment the name was shown.
func (m *OS) RenameAppend(text string) {
	if !m.Renaming() || text == "" {
		return
	}
	add := printableRunes(text)
	// A keypress that is nothing but combining marks has no base to sit on, so
	// it would stack an accent on whatever the field already ends with, or on
	// the cursor. Terminals send composed text as one precomposed rune.
	if add == "" || combiningOnly(add) {
		return
	}
	m.RenameBuffer += add
	if t := m.RenameTarget(); t != nil {
		t.InvalidateCache()
	}
}

// RenameBackspace drops the last rune of the buffer. It counts in runes because
// a name may hold multi-byte text, and cutting one byte off é leaves the buffer
// holding a broken sequence that renders as a replacement glyph.
func (m *OS) RenameBackspace() {
	if !m.Renaming() || m.RenameBuffer == "" {
		return
	}
	_, size := utf8.DecodeLastRuneInString(m.RenameBuffer)
	m.RenameBuffer = m.RenameBuffer[:len(m.RenameBuffer)-size]
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

// daemonSessionLabel reads the cached label for a session, preferring the live
// value for the attached one.
func (m *OS) daemonSessionLabel(name string) (display, accent string) {
	if name == m.SessionName {
		return m.SessionDisplayName, m.SessionAccent
	}
	if m.DaemonClient == nil {
		return "", ""
	}
	return m.DaemonClient.SessionLabel(name)
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
	label := strings.TrimSpace(m.RenameBuffer)
	defer m.EndRename()

	if kind == RenameWindow {
		if w := m.RenameTarget(); w != nil {
			_ = m.RenameWindowByID(w.ID, m.RenameBuffer)
			w.InvalidateCache()
		}
		return nil
	}
	verb, params, ok := renameVerb(kind, target, m.SessionName, label)
	if !ok {
		return nil
	}
	return labelVerbCmd("Rename", verb, params)
}

// renameVerb picks the daemon verb a rename goes through and builds its params.
// A session rename addresses the session by its identity and sends the label
// separately, which is the whole point: set-session-name changes display_name
// and leaves the name the session is addressed and persisted by alone.
func renameVerb(kind RenameKind, target, sessionName, label string) (string, map[string]any, bool) {
	switch kind {
	case RenameSession:
		if target == "" {
			return "", nil, false
		}
		return "set-session-name", map[string]any{"session": target, "name": label}, true
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
	m.RenameBuffer = ""
	m.renameHit = overlay.Rect{}
}

// RenameMouseClick routes a click while the rename dialog is up: inside is a
// no-op (the field has no clickable parts), outside cancels. The dialog is
// modal to the mouse either way, so a stray click can never leave an editor
// open over a pane the user has moved on to. The mouse is never required: the
// same keys that opened it commit or cancel it.
func (m *OS) RenameMouseClick(x, y int) bool {
	if !m.Renaming() {
		return false
	}
	if !m.renameHit.Contains(x, y) {
		m.EndRename()
	}
	return true
}
