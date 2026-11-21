package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/textedit"
)

const fileSpoofRefusal = "This pane reports a folder it is not in. " +
	"Open the folder in another pane to change files."

type filePromptKind int

const (
	filePromptNone filePromptKind = iota
	filePromptCreate
	filePromptRename
	filePromptConfirm
)

const (
	fileConfirmRowCancel = iota
	fileConfirmRowGo
	fileConfirmRowCount
)

type filePromptState struct {
	Kind   filePromptKind
	Dir    string
	Target string
	Input  textedit.Line
	Err    string
	Paths  []string
	Trash  bool
	List   listnav.State
	Busy   bool
}

type fileOpMsg struct {
	OK  string
	Err string
}

// FilePromptOpen reports whether a file dialog owns the keyboard.
func (m *OS) FilePromptOpen() bool { return m.filePrompt.Kind != filePromptNone }

// FileConfirmOpen reports whether the open dialog is the yes or no one.
func (m *OS) FileConfirmOpen() bool { return m.filePrompt.Kind == filePromptConfirm }

// FileActionsOn reports whether the rail offers file actions at all.
//
// Three gates. The section has to be on screen, because an action on a listing
// nobody can see has no target a person could have chosen. The setting has to
// allow it: a rail beside a terminal is not everyone's idea of where to delete
// things from, so it can be switched off and the keys then do nothing. And the
// pane has to have told the truth about where it is.
//
// The third gate is the narrow one. The folder came from OSC 7, which is a
// string the pane printed, and it decides where a delete, a rename and a paste
// land. When /proc says the pane's shell is somewhere else, the folder on
// screen is not the folder the user is working in, and acting on it is acting
// on a target the pane chose. When nothing can corroborate the pane, which is
// every daemon-backed pane and every pane on a system with no /proc, this gate
// is open and the actions are exactly what they were. See cwdIsSpoofed.
func (m *OS) FileActionsOn() bool {
	return m.Settings.SidebarFileActions && m.filesSectionEnabled() &&
		m.filesView.Dir != "" && !m.filesView.Spoofed
}

// FileViewSpoofed reports whether the listing on screen is one the pane steered
// somewhere /proc says it is not. It is exported for the rail's own render and
// for the tests that read the frame.
func (m *OS) FileViewSpoofed() bool {
	return m.filesView.Spoofed && m.filesView.Dir != ""
}

func (m *OS) fileActionTarget() (name, path string, ok bool) {
	dir, e, ok := m.fileActionEntry()
	if !ok {
		return "", "", false
	}
	return e.Name, filepath.Join(dir, e.Name), true
}

func (m *OS) fileActionEntry() (dir string, e fileEntry, ok bool) {
	if t := m.menuFile; t.Active() && t.Name != "" {
		return t.Dir, fileEntry{Name: t.Name, Dir: t.IsDir}, true
	}
	row, have := m.sidebarCursorRow()
	if !have || row.Kind != sidebarRowFileEntry {
		return "", fileEntry{}, false
	}
	if m.filesTreeOn() {
		return m.fileTreeEntry(row.WindowID)
	}
	i := row.WindowIndex
	if i < 0 || i >= len(m.filesView.Entries) {
		return "", fileEntry{}, false
	}
	return m.filesView.Dir, m.filesView.Entries[i], true
}

func (m *OS) fileActionRefuse() bool {
	if !m.FileActionsOn() {
		switch {
		case !m.Settings.SidebarFileActions:
			m.ShowNotification("File actions are off. Turn them on in the settings.",
				"info", m.Settings.NotificationDuration)
		case m.FileViewSpoofed():
			m.ShowNotification(fileSpoofRefusal, "warning", m.Settings.NotificationDuration)
		default:
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

func (m *OS) fileActionDir() string {
	if t := m.menuFile; t.Active() {
		if full := filepath.Join(t.Dir, t.Name); t.Name != "" && t.IsDir && m.filesTree.dirs[full] != nil {
			return full
		}
		return t.Dir
	}
	if m.filesTreeOn() {
		if dir, e, ok := m.fileActionEntry(); ok {
			if full := filepath.Join(dir, e.Name); e.Dir && m.filesTree.dirs[full] != nil {
				return full
			}
			return dir
		}
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
		if m.FileViewSpoofed() {
			m.ShowNotification(fileSpoofRefusal, "warning", m.Settings.NotificationDuration)
			return true
		}
		return false
	}
	name, path, ok := m.fileActionTarget()
	if !ok {
		return false
	}
	if m.filePrompt.Busy {
		m.ShowNotification("Wait for the last file action to finish.", "info", m.Settings.NotificationDuration)
		return true
	}
	m.filePrompt = filePromptState{
		Kind:   filePromptRename,
		Dir:    filepath.Dir(path),
		Target: name,
	}
	m.filePrompt.Input.Set(name)
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
		Kind:  filePromptConfirm,
		Dir:   filepath.Dir(path),
		Paths: []string{path},
		Trash: !permanent && m.Settings.SidebarFileDelete == config.SidebarFileDeleteTrash && trashAvailable(),
		List:  listnav.State{Cursor: fileConfirmRowCancel},
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

// SidebarFileCopyPath puts the absolute path of the row the menu was opened
// on, or of the row under the cursor, on the clipboard. It acts on a folder
// row as on a file row. It reads nothing on disk, so like the open action it
// asks only that the section is on screen.
func (m *OS) SidebarFileCopyPath() tea.Cmd {
	if !m.filesOn() {
		return nil
	}
	_, path, ok := m.fileActionTarget()
	if !ok {
		return nil
	}
	return m.copyFilePath(path)
}

type fileEditMsg struct {
	Path  string
	Argv  []string
	IsDir bool
	Note  string
	Err   error
}

// SidebarFileEdit opens the selected text file in the configured terminal
// editor. Folders keep their navigation action.
func (m *OS) SidebarFileEdit() tea.Cmd {
	if !m.filesOn() || m.FileViewSpoofed() {
		if m.FileViewSpoofed() {
			m.ShowNotification(fileSpoofRefusal, "warning", m.Settings.NotificationDuration)
		}
		return nil
	}
	dir, e, ok := m.fileActionEntry()
	if !ok {
		return nil
	}
	path := filepath.Join(dir, e.Name)
	if m.learnFiles() {
		if e.Dir {
			return m.fileViewOpen(dir, e.Name, true)
		}
		m.learnOpenFile(path, true)
		return nil
	}
	editor := m.Settings.SidebarEditor
	if otherHost := m.filesView.Host != ""; otherHost || m.AttachedHost != "" {
		return m.handleFileEdit(remoteEditTarget(path, editor, e.Dir, otherHost))
	}
	return func() tea.Msg {
		return inspectEditTarget(path, editor)
	}
}

func inspectEditTarget(path, editor string) fileEditMsg {
	msg := fileEditMsg{Path: path}
	info, err := os.Stat(path)
	if err != nil {
		return msg.refuse("tuios could not read that file.", err)
	}
	if info.IsDir() {
		msg.IsDir = true
		return msg
	}
	if !info.Mode().IsRegular() {
		return msg.refuse("tuios can edit only text files.", nil)
	}
	file, err := os.Open(path)
	if err != nil {
		return msg.refuse("tuios could not read that file.", err)
	}
	defer file.Close()
	var sample [512]byte
	n, err := file.Read(sample[:])
	if err != nil && err != io.EOF {
		return msg.refuse("tuios could not read that file.", err)
	}
	if n > 0 && !looksLikeText(sample[:n]) {
		return msg.refuse("tuios can not edit that file. It is not a text file.", nil)
	}
	msg.Argv, err = editorArgv(editor)
	if err != nil {
		return msg.refuse(editorCommandNote, err)
	}
	if _, err := exec.LookPath(msg.Argv[0]); err != nil {
		return msg.refuse("tuios could not find the editor "+msg.Argv[0]+". Set the File editor in the sidebar settings.", err)
	}
	return msg
}

func (msg fileEditMsg) refuse(note string, err error) fileEditMsg {
	msg.Note, msg.Err = note, err
	return msg
}

func remoteEditTarget(path, editor string, isDir, otherHost bool) fileEditMsg {
	msg := fileEditMsg{Path: path, IsDir: isDir}
	if isDir {
		return msg
	}
	if otherHost {
		return msg.refuse("tuios can not edit files on another machine.", nil)
	}
	var err error
	if msg.Argv, err = editorArgv(editor); err != nil {
		return msg.refuse(editorCommandNote, err)
	}
	return msg
}

func looksLikeText(sample []byte) bool {
	if bytes.IndexByte(sample, 0) >= 0 {
		return false
	}
	for cut := 0; cut < utf8.UTFMax && cut < len(sample); cut++ {
		if utf8.Valid(sample[:len(sample)-cut]) {
			return true
		}
	}
	return false
}

func remoteEditIntentNote(host, name string, argv []string) string {
	editor := "the editor"
	if len(argv) > 0 && argv[0] != "" {
		editor = argv[0]
	}
	return fmt.Sprintf(
		"tuios asked %s to open %s in %s. If no pane opens, check that %s is on %s.",
		host, name, editor, editor, host,
	)
}

func (m *OS) handleFileEdit(msg fileEditMsg) tea.Cmd {
	if msg.Note != "" {
		if msg.Err != nil {
			m.LogError("Could not edit %s: %v", msg.Path, msg.Err)
		}
		m.ShowNotification(msg.Note, "warning", m.Settings.NotificationDuration)
		return nil
	}
	if msg.IsDir {
		return m.fileViewOpen(filepath.Dir(msg.Path), filepath.Base(msg.Path), true)
	}
	remoteHost := m.AttachedHost
	before := len(m.Windows)
	m.AddWindowIn(filepath.Dir(msg.Path), filepath.Base(msg.Path), append(msg.Argv, msg.Path)...)
	if !m.daemonWindowIntent && len(m.Windows) == before {
		m.ShowNotification("tuios could not start the editor.", "error", m.Settings.NotificationDuration)
		return nil
	}
	if remoteHost != "" && m.daemonWindowIntent {
		m.ShowNotification(remoteEditIntentNote(remoteHost, filepath.Base(msg.Path), msg.Argv),
			"info", m.Settings.NotificationDuration)
	}
	m.clearSidebarReturn()
	m.ExitSidebarFocus()
	m.pendingStartTerminalMode = true
	if !m.daemonWindowIntent {
		m.maybeEnterPendingTerminalMode()
	}
	return nil
}

// SidebarFileCopy puts the cursor row on the file clipboard for a copy.
func (m *OS) SidebarFileCopy() { m.captureFileClipboard(false) }

// SidebarFileCut puts the cursor row on the file clipboard for a move.
func (m *OS) SidebarFileCut() { m.captureFileClipboard(true) }

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
//
// With no dialog, the notification is the only place a paste can name where it
// landed, and it names it. The folder came from the pane, so a paste that says
// only "Copied 3 files" says nothing about the one thing the user cannot see.
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
			msg.OK = verb + " " + countOf(done, "file") + " to " + shortenHome(dir) + "."
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

// FilePromptType types text at the name prompt's caret, as a paste does. The
// text goes through cleanName, the gate the rename editor and every name the
// rail draws already pass: a codepoint the chrome cannot draw is one the user
// cannot see they typed.
func (m *OS) FilePromptType(s string) {
	if !m.filePromptTyping() {
		return
	}
	if lineInsert(&m.filePrompt.Input, s, cleanName) {
		m.filePrompt.Err = ""
	}
}

// FilePromptKey applies one key press to the name prompt (see lineKey).
func (m *OS) FilePromptKey(key, text string) {
	if !m.filePromptTyping() {
		return
	}
	if changed, _ := lineKey(&m.filePrompt.Input, key, text, cleanName); changed {
		m.filePrompt.Err = ""
	}
}

func (m *OS) filePromptTyping() bool {
	return m.filePrompt.Kind == filePromptCreate || m.filePrompt.Kind == filePromptRename
}

// FilePromptCancel shuts the dialog and changes nothing.
func (m *OS) FilePromptCancel() { m.closeFilePrompt() }

func (m *OS) closeFilePrompt() {
	busy := m.filePrompt.Busy
	m.filePrompt = filePromptState{Busy: busy}
}

// FileConfirmMove steps the confirmation's cursor, clamped to its rows. It
// does not wrap, for the reason SessionCloseMove gives: up from Cancel must
// not land on the answer that deletes.
func (m *OS) FileConfirmMove(delta int) {
	if m.filePrompt.Kind != filePromptConfirm {
		return
	}
	m.filePrompt.List.Move(delta, fileConfirmRowCount, fileConfirmRowCount, false, nil)
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
		return m.FileConfirmActivate(p.List.Cursor)
	case filePromptCreate:
		if _, err := relativeUnder(trimTrailingSeparators(p.Input.String())); err != nil {
			m.filePrompt.Err = fileOpError(err)
			return nil
		}
		dir, raw := p.Dir, p.Input.String()
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
		if _, err := relativeUnder(p.Input.String()); err != nil {
			m.filePrompt.Err = fileOpError(err)
			return nil
		}
		dir, old, next := p.Dir, p.Target, p.Input.String()
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
		m.ShowNotification(msg.OK+" "+msg.Err, "warning", m.Settings.NotificationDuration)
	case msg.Err != "":
		m.ShowNotification(msg.Err, "error", m.Settings.NotificationDuration)
	case msg.OK != "":
		m.ShowNotification(msg.OK, "success", m.Settings.NotificationDuration)
	}
	return m.RefreshFileView()
}

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
