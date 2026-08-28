package app

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// # File actions on the rail
//
// Six of them: create, rename, delete, copy, cut and paste. It is the set the
// maintainer named and no more. There is no multi-select, no tree, no filter
// and no drag and drop, because a twenty-six column rail beside a terminal is
// not the place to grow a file manager and yeetui already is one.
//
// # Where the prompts live
//
// Not in the rail. A create prompt, a rename field and a delete confirmation
// each have to show a name and a question, and the rail has about twenty-two
// columns left after its gutter and its glyph. "Delete /home/g/dev/tuios/in…?"
// with the answer off the bottom is not a confirmation, it is a shape that
// looks like one.
//
// So they are centred dialogs, which is what this codebase already does for
// every focused question: the rename editor is one, the close-session
// confirmation is one, and both are drawn by the same overlay.Dialog. A user
// who has renamed a pane has already used this dialog. See
// render_file_prompt.go for the frames.
//
// # Nothing touches the disk on the update goroutine
//
// Every one of the six runs inside a tea.Cmd. This file decides what to do and
// writes down what was asked for; the command does it. A paste of a large tree
// takes as long as the disk takes and the rail keeps drawing for all of it.
//
// When an operation finishes it does not edit the listing in memory. It asks
// for the listing again through requestFileList, which stamps a generation and
// drops a superseded reply. Editing the slice instead would mean a read already
// in flight, from before the delete, could land afterwards and put the deleted
// name back on the rail.

// filePromptKind is what the open dialog is asking.
type filePromptKind int

const (
	// filePromptNone means no dialog is up.
	filePromptNone filePromptKind = iota
	// filePromptCreate asks for a name to make. A trailing slash means a folder.
	filePromptCreate
	// filePromptRename asks for a new name for one entry.
	filePromptRename
	// filePromptConfirm asks a yes or no question about something destructive.
	filePromptConfirm
)

// The confirmation's two rows, in drawn order. Cancel is first and is what the
// dialog opens on, so enter on a dialog nobody has touched destroys nothing.
const (
	fileConfirmRowCancel = iota
	fileConfirmRowGo
	fileConfirmRowCount
)

// filePromptState is the open dialog. The zero value is no dialog.
type filePromptState struct {
	Kind filePromptKind
	// Dir is the folder the dialog was opened over. It is remembered rather
	// than re-read on submit: the listing can be replaced by a reply that was
	// already in flight, and an operation must act on what the user was looking
	// at when they pressed the key.
	Dir string
	// Target is the entry name a rename acts on, for the same reason.
	Target string
	// Input is the typed name.
	Input string
	// Err is the last refusal, drawn under the field so a second try can see
	// what was wrong with the first.
	Err string
	// Paths is what a confirmation would act on, captured when it opened.
	Paths []string
	// Trash says the delete goes to the trash rather than being permanent. It
	// decides the sentence the dialog shows and the operation that runs.
	Trash bool
	// Selected is the confirmation's cursor row.
	Selected int
	// Busy is true while an operation is running. It closes the dialog and
	// stops a second operation starting on top of the first.
	Busy bool
}

// fileOpMsg is one finished operation on its way back to the loop.
type fileOpMsg struct {
	// OK is the sentence for a run that worked, and Err the one for a run that
	// did not. Both can be set: a batch that half worked has to say so.
	OK  string
	Err string
}

// FilePromptOpen reports whether a file dialog owns the keyboard.
func (m *OS) FilePromptOpen() bool { return m.filePrompt.Kind != filePromptNone }

// FileConfirmOpen reports whether the open dialog is the yes or no one.
func (m *OS) FileConfirmOpen() bool { return m.filePrompt.Kind == filePromptConfirm }

// FileActionsOn reports whether the rail offers file actions at all.
//
// Two gates. The section has to be on screen, because an action on a listing
// nobody can see has no target a person could have chosen. And the setting has
// to allow it: a rail beside a terminal is not everyone's idea of where to
// delete things from, so it can be switched off and the keys then do nothing.
func (m *OS) FileActionsOn() bool {
	return m.Settings.SidebarFileActions && m.filesSectionEnabled() && m.filesView.Dir != ""
}

// fileActionTarget is the entry an action acts on, as an absolute path.
//
// The row a context menu was opened on comes first, and it is the only place
// the mouse has to say what it aimed at: a right-click does not move the
// keyboard cursor, and requiring it to would mean clicking a name twice to act
// on it. The carry lives for one dispatch, so the same action reached by key
// falls straight through to the cursor row. See fileMenuTarget.
//
// The ".." row names the folder above and is not a target on either path:
// deleting the folder you are standing in from a row that means "go up" is the
// kind of aim nobody takes on purpose.
func (m *OS) fileActionTarget() (name, path string, ok bool) {
	if t := m.menuFile; t.Active() && t.Name != "" {
		// The folder the menu was opened over, not the one on screen now. A
		// reply already in flight can replace the listing between the
		// right-click and the row being chosen, and the action has to run on
		// what the user was pointing at.
		return t.Name, filepath.Join(t.Dir, t.Name), true
	}
	row, have := m.sidebarCursorRow()
	if !have || row.Kind != sidebarRowFileEntry {
		return "", "", false
	}
	// The index the row carries comes from the listing the render drew, which
	// can be one reply behind the one in memory. A row past the end is that
	// race, and it names nothing.
	i := row.WindowIndex
	if i < 0 || i >= len(m.filesView.Entries) {
		return "", "", false
	}
	name = m.filesView.Entries[i].Name
	return name, filepath.Join(m.filesView.Dir, name), true
}

// fileActionRefuse says why an action cannot run, once, in the notification
// area. It answers true when it refused.
func (m *OS) fileActionRefuse() bool {
	if !m.FileActionsOn() {
		// One gate, two reasons. Both go through FileActionsOn so there is a
		// single place that decides, and the setting cannot be honoured on one
		// path and forgotten on another.
		if !m.Settings.SidebarFileActions {
			m.ShowNotification("File actions are off. Turn them on in the settings.",
				"info", m.Settings.NotificationDuration)
		} else {
			m.ShowNotification("Open the files section first.", "info", m.Settings.NotificationDuration)
		}
		return true
	}
	if m.filePrompt.Busy {
		m.ShowNotification("Wait for the last file action to finish.", "info", m.Settings.NotificationDuration)
		return true
	}
	return false
}

// fileActionDir is the folder an action that needs no name acts in: the one the
// menu was opened over, or the one on screen. Same carry, same reason.
func (m *OS) fileActionDir() string {
	if t := m.menuFile; t.Active() {
		return t.Dir
	}
	return m.filesView.Dir
}

// SidebarFileCreate opens the create prompt over the listed folder.
func (m *OS) SidebarFileCreate() {
	if m.fileActionRefuse() {
		return
	}
	m.filePrompt = filePromptState{Kind: filePromptCreate, Dir: m.fileActionDir()}
}

// SidebarFileRename opens the rename prompt on the cursor row, seeded with the
// name it already has.
func (m *OS) SidebarFileRename() bool {
	if !m.FileActionsOn() {
		return false
	}
	name, _, ok := m.fileActionTarget()
	if !ok {
		return false
	}
	if m.filePrompt.Busy {
		m.ShowNotification("Wait for the last file action to finish.", "info", m.Settings.NotificationDuration)
		return true
	}
	m.filePrompt = filePromptState{
		Kind:   filePromptRename,
		Dir:    m.fileActionDir(),
		Target: name,
		Input:  name,
	}
	return true
}

// SidebarFileDelete raises the delete confirmation for the cursor row.
//
// permanent forces the permanent delete whatever the setting says. It is the
// explicit alternative the trash needs: a file on another disk cannot go to the
// home trash, and somebody who means it should not have to edit a config file.
//
// Nothing is removed here. This opens a dialog, and the dialog opens on Cancel.
func (m *OS) SidebarFileDelete(permanent bool) {
	if m.fileActionRefuse() {
		return
	}
	_, path, ok := m.fileActionTarget()
	if !ok {
		m.ShowNotification("Put the cursor on a file first.", "info", m.Settings.NotificationDuration)
		return
	}
	m.filePrompt = filePromptState{
		Kind:     filePromptConfirm,
		Dir:      m.fileActionDir(),
		Paths:    []string{path},
		Trash:    !permanent && m.Settings.SidebarFileDelete == config.SidebarFileDeleteTrash && trashAvailable(),
		Selected: fileConfirmRowCancel,
	}
}

// SidebarFileOpen acts on a listing row the way a plain click on it does: a
// folder opens, a file's path goes to the clipboard, and ".." goes up.
//
// It is not one of the six. It touches no file and asks nothing, so it is live
// whenever the section is, whatever appearance.sidebar.file_actions says: a
// listing whose names could not be clicked would be a different feature.
//
// Reached from a menu it acts on the row the menu was opened on. Reached by key
// it hands straight to SidebarActivateCursor, so the key and the rail's own
// enter run one implementation and cannot drift.
func (m *OS) SidebarFileOpen() tea.Cmd {
	t := m.menuFile
	if !t.Active() {
		m.SidebarActivateCursor()
		return m.TakeSidebarCmd()
	}
	if t.Up {
		return m.fileViewUpFrom(t.Dir)
	}
	return m.fileViewOpen(t.Dir, t.Name, t.IsDir)
}

// SidebarFileCopy puts the cursor row on the file clipboard for a copy.
func (m *OS) SidebarFileCopy() { m.captureFileClipboard(false) }

// SidebarFileCut puts the cursor row on the file clipboard for a move.
func (m *OS) SidebarFileCut() { m.captureFileClipboard(true) }

// captureFileClipboard records what a paste would act on.
//
// It takes no confirmation and touches nothing. A cut is a note about what to
// move later, not the move: the source goes only when a paste has written the
// destination, and even then only if the write worked.
func (m *OS) captureFileClipboard(move bool) {
	if m.fileActionRefuse() {
		return
	}
	name, path, ok := m.fileActionTarget()
	if !ok {
		m.ShowNotification("Put the cursor on a file first.", "info", m.Settings.NotificationDuration)
		return
	}
	m.fileClip = fileClipboard{Paths: []string{path}, Move: move}
	verb := "Copied"
	if move {
		verb = "Cut"
	}
	m.ShowNotification(verb+" "+name+".", "success", m.Settings.NotificationDuration)
}

// SidebarFilePaste puts the clipboard into the listed folder.
//
// It raises no confirmation, and that is a decision rather than an omission.
// pastePaths never overwrites: a name already taken gets the first free
// "name (N).ext" instead, so there is no destructive branch in a paste for a
// dialog to gate. The notification says how many were renamed, because a file
// that arrived under a different name is a fact the user has to be told.
func (m *OS) SidebarFilePaste() tea.Cmd {
	if m.fileActionRefuse() {
		return nil
	}
	if m.fileClip.Empty() {
		m.ShowNotification("Copy or cut a file first.", "info", m.Settings.NotificationDuration)
		return nil
	}
	dir, clip := m.fileActionDir(), m.fileClip
	m.filePrompt.Busy = true
	// A cut is spent by the paste that moves it. Leaving it on the clipboard
	// would let a second paste try to move a source that is no longer there.
	if clip.Move {
		m.fileClip = fileClipboard{}
	}
	return func() tea.Msg {
		done, renamed, err := pastePaths(dir, clip)
		verb := "Copied"
		if clip.Move {
			verb = "Moved"
		}
		msg := fileOpMsg{}
		if done > 0 {
			msg.OK = verb + " " + countOf(done, "file") + "."
			if renamed > 0 {
				msg.OK += " " + countOf(renamed, "file") + " got a new name."
			}
		}
		if err != nil {
			msg.Err = fileOpError(err)
		}
		return msg
	}
}

// FilePromptType adds typed text to the open name prompt.
//
// It launders the text through printableRunes, which is the gate the rename
// editor and every name the rail draws already pass. A codepoint the chrome
// cannot draw is one the user cannot see they typed.
func (m *OS) FilePromptType(s string) {
	if m.filePrompt.Kind != filePromptCreate && m.filePrompt.Kind != filePromptRename {
		return
	}
	add := printableRunes(s)
	if add == "" || combiningOnly(add) {
		return
	}
	m.filePrompt.Input += add
	m.filePrompt.Err = ""
}

// FilePromptBackspace drops the last rune of the typed name.
func (m *OS) FilePromptBackspace() {
	if m.filePrompt.Input == "" {
		return
	}
	r := []rune(m.filePrompt.Input)
	m.filePrompt.Input = string(r[:len(r)-1])
	m.filePrompt.Err = ""
}

// FilePromptClearInput empties the field.
func (m *OS) FilePromptClearInput() {
	m.filePrompt.Input = ""
	m.filePrompt.Err = ""
}

// FilePromptCancel shuts the dialog and changes nothing.
func (m *OS) FilePromptCancel() { m.closeFilePrompt() }

// closeFilePrompt drops the dialog, keeping the busy flag: an operation already
// running outlives the dialog that started it.
func (m *OS) closeFilePrompt() {
	busy := m.filePrompt.Busy
	m.filePrompt = filePromptState{Busy: busy}
}

// FileConfirmMove steps the confirmation's cursor, clamped to its rows.
func (m *OS) FileConfirmMove(delta int) {
	if m.filePrompt.Kind != filePromptConfirm {
		return
	}
	m.filePrompt.Selected = clampInt(m.filePrompt.Selected+delta, 0, fileConfirmRowCount-1)
}

// FilePromptSubmit runs what the open dialog asks for.
//
// A name prompt validates before it closes. A refusal keeps the dialog up with
// the typed name in it and the reason under it, because the answer to "that
// name is already in use" is to type another one, and a dialog that closed
// would have thrown away the one that was nearly right.
func (m *OS) FilePromptSubmit() tea.Cmd {
	p := m.filePrompt
	switch p.Kind {
	case filePromptConfirm:
		return m.FileConfirmActivate(p.Selected)
	case filePromptCreate:
		if _, err := relativeUnder(trimTrailingSeparators(p.Input)); err != nil {
			m.filePrompt.Err = fileOpError(err)
			return nil
		}
		dir, raw := p.Dir, p.Input
		m.closeFilePrompt()
		m.filePrompt.Busy = true
		return func() tea.Msg {
			what, err := createPath(dir, raw)
			if err != nil {
				return fileOpMsg{Err: fileOpError(err)}
			}
			return fileOpMsg{OK: what + "."}
		}
	case filePromptRename:
		if _, err := relativeUnder(p.Input); err != nil {
			m.filePrompt.Err = fileOpError(err)
			return nil
		}
		dir, old, next := p.Dir, p.Target, p.Input
		m.closeFilePrompt()
		m.filePrompt.Busy = true
		return func() tea.Msg {
			what, err := renameEntry(dir, old, next)
			if err != nil {
				return fileOpMsg{Err: fileOpError(err)}
			}
			return fileOpMsg{OK: what + "."}
		}
	}
	return nil
}

// trimTrailingSeparators drops the trailing slash a create prompt uses to mean
// "a folder", so the validator sees the name and not the mark.
func trimTrailingSeparators(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '/' || s[len(s)-1] == os.PathSeparator) {
		s = s[:len(s)-1]
	}
	return s
}

// FileConfirmActivate answers the confirmation.
//
// Cancel is row zero and is where the dialog opened, so this is the only path
// that can destroy anything and it needs a deliberate move onto the other row
// first. Nothing here removes a file either: it starts the command that does.
func (m *OS) FileConfirmActivate(idx int) tea.Cmd {
	if m.filePrompt.Kind != filePromptConfirm {
		return nil
	}
	paths, trash := m.filePrompt.Paths, m.filePrompt.Trash
	m.closeFilePrompt()
	if idx != fileConfirmRowGo || len(paths) == 0 {
		return nil
	}
	m.filePrompt.Busy = true
	if trash {
		return func() tea.Msg {
			done, err := trashPaths(paths, time.Now())
			msg := fileOpMsg{}
			if done > 0 {
				msg.OK = "Moved " + countOf(done, "file") + " to the trash."
			}
			if err != nil {
				msg.Err = trashError(err)
			}
			return msg
		}
	}
	return func() tea.Msg {
		done, err := deletePaths(paths)
		msg := fileOpMsg{}
		if done > 0 {
			msg.OK = "Deleted " + countOf(done, "file") + "."
		}
		if err != nil {
			msg.Err = fileOpError(err)
		}
		return msg
	}
}

// HandleFileOp applies a finished operation: it says what happened and asks for
// the listing again.
//
// The re-read goes through RefreshFileView, which is requestFileList with a new
// generation. Nothing here edits m.filesView.Entries. A read that was already
// in flight when the delete ran carries an older generation and is dropped, so
// a slow answer cannot put a removed name back on the rail.
func (m *OS) HandleFileOp(msg fileOpMsg) tea.Cmd {
	m.filePrompt.Busy = false
	switch {
	case msg.Err != "" && msg.OK != "":
		// A batch that half worked. The failure is the half the user has to act
		// on, so it is the half that gets the sentence and the warning colour.
		m.ShowNotification(msg.OK+" "+msg.Err, "warning", m.Settings.NotificationDuration)
	case msg.Err != "":
		m.ShowNotification(msg.Err, "error", m.Settings.NotificationDuration)
	case msg.OK != "":
		m.ShowNotification(msg.OK, "success", m.Settings.NotificationDuration)
	}
	return m.RefreshFileView()
}

// fileConfirmQuestion is the confirmation's first line: what goes, by name.
func (m *OS) fileConfirmQuestion() string {
	paths := m.filePrompt.Paths
	switch len(paths) {
	case 0:
		return "Delete this file?"
	case 1:
		return "Delete " + printableTitle(filepath.Base(paths[0])) + "?"
	default:
		return "Delete " + strconv.Itoa(len(paths)) + " files?"
	}
}

// fileConfirmOutcome is the line that says whether the file comes back.
//
// The two sentences have to be told apart at a glance, because they are the
// whole difference between the two deletes. One says where the file goes and
// that it can be got back; the other says it is permanent.
func (m *OS) fileConfirmOutcome() string {
	if m.filePrompt.Trash {
		return "The file goes to the trash. You can get it back."
	}
	return "This delete is permanent. You can not undo it."
}

// SidebarCursorOnFile reports whether the rail's keyboard cursor is on a row of
// the files section's listing, and file actions are on.
//
// It is the gate the key routing asks before the files section's own bindings
// are consulted, which is what lets three of those keys share a key with a rail
// binding that already exists. A cursor anywhere else answers false and the
// rail's own binding runs untouched.
func (m *OS) SidebarCursorOnFile() bool {
	if !m.SidebarFocused || !m.FileActionsOn() {
		return false
	}
	row, ok := m.sidebarCursorRow()
	return ok && (row.Kind == sidebarRowFileEntry || row.Kind == sidebarRowFileUp)
}

// FileActionTargetNameForTest is the name a file action would act on, or "".
// It is exported for the input package's key-routing tests, which drive the
// real handler and need to know which row the cursor landed on without
// re-deriving the rail's layout.
func (m *OS) FileActionTargetNameForTest() string {
	name, _, ok := m.fileActionTarget()
	if !ok {
		return ""
	}
	return name
}
