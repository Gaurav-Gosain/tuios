package app

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Learn mode is the guided tour at tuios.dev/learn, where tuios runs as
// WebAssembly in a browser tab with a fake shell in every pane (see
// cmd/tuios-wasm). Two things differ from a normal session there:
//
//   - Nothing quits. Closing the tab is the way out, and a quit key that ended
//     the program would leave a beginner staring at a dead terminal. Every quit
//     path shows a friendly note instead.
//   - Anything that needs a daemon, a real process, the network or the host's
//     files is not there. Those actions show a note that says so, instead of the
//     error the missing piece would otherwise produce.
//
// Everything else is the real tuios.

// Notes shown in Learn mode. Short, friendly, and never an error.
const (
	learnNoteQuit     = "No need to quit here. Just close the tab when you're done."
	learnNoteSessions = "Sessions are not in the browser demo. Install tuios for them."
	learnNoteFiles    = "The demo does not change files or use the clipboard."
	learnNoteShot     = "Screenshots are not in the browser demo."
	learnNoteTapes    = "The tape manager is not in the demo. Try: tuios tape play demo.tape"
	learnNoteRecord   = "Recording is not in the browser demo. Try: tuios tape play demo.tape"
	learnNoteMail     = "Agent mail is not in the browser demo."
	learnNotePaste    = "Paste with your browser here: Cmd+V or Ctrl+Shift+V."
)

// learnUnavailable maps each action Learn mode turns off to the note it shows.
// The dispatcher consults it for keys, prefix chords and menu rows; the OS
// methods behind the command palette rows check learnOff themselves.
var learnUnavailable = map[string]string{
	"quit":                    learnNoteQuit,
	"prefix_quit":             learnNoteQuit,
	"kill_session_quit":       learnNoteQuit,
	"prefix_detach":           learnNoteSessions,
	"prefix_close_session":    learnNoteSessions,
	"prefix_session_switcher": learnNoteSessions,
	"new_session":             learnNoteSessions,
	"next_session":            learnNoteSessions,
	"prev_session":            learnNoteSessions,
	"next_host":               learnNoteSessions,
	"prev_host":               learnNoteSessions,
	"kill_session":            learnNoteSessions,
	"kill_session_next":       learnNoteSessions,
	"rename_session":          learnNoteSessions,
	"set_note":                learnNoteSessions,
	"session_new_worktree":    learnNoteSessions,
	"repo_new_worktree":       learnNoteSessions,
	"repo_toggle_fold":        learnNoteSessions,
	"prefix_mail":             learnNoteMail,
	"screenshot":              learnNoteShot,
	"screenshot_window":       learnNoteShot,
	"screenshot_screen":       learnNoteShot,
	"prefix_screenshot":       learnNoteShot,
	"toggle_tape_manager":     learnNoteTapes,
	"tape_prefix_manager":     learnNoteTapes,
	"tape_prefix_review":      learnNoteTapes,
	"tape_prefix_record":      learnNoteRecord,
	"layout_prefix_save":      learnNoteFiles,
	"file_create":             learnNoteFiles,
	"file_rename":             learnNoteFiles,
	"file_delete":             learnNoteFiles,
	"file_delete_forever":     learnNoteFiles,
	"file_copy":               learnNoteFiles,
	"file_cut":                learnNoteFiles,
	"file_paste":              learnNoteFiles,
	"file_copy_path":          learnNoteFiles,
	"file_send":               learnNoteFiles,
	"paste_clipboard":         learnNotePaste,
	"terminal_paste_host":     learnNotePaste,
}

// LearnUnavailableActions lists the actions Learn mode turns off, with the
// note each one shows. The browser build hands it to the page, so a lesson can
// tell a key that did nothing from one that is off in the demo.
func LearnUnavailableActions() map[string]string {
	out := make(map[string]string, len(learnUnavailable))
	maps.Copy(out, learnUnavailable)
	return out
}

// LearnBlocksAction reports whether Learn mode turns action off, and shows the
// note when it does. The action dispatcher calls it before running a handler.
func (m *OS) LearnBlocksAction(action string) bool {
	if m == nil || !m.LearnMode {
		return false
	}
	note, ok := learnUnavailable[action]
	if !ok {
		return false
	}
	m.showLearnNote(note)
	return true
}

// learnOff shows note and reports true in Learn mode. The OS methods a
// command palette row reaches without the dispatcher call it first.
func (m *OS) learnOff(note string) bool {
	if m == nil || !m.LearnMode {
		return false
	}
	m.showLearnNote(note)
	return true
}

func (m *OS) showLearnNote(note string) {
	if r, ok := m.LearnNotes[note]; ok {
		if r == "" {
			return
		}
		note = r
	}
	m.ShowNotification(note, "info", 2*m.Settings.NotificationDuration)
	m.MarkAllDirty()
}

// learnQuitMsg stands in for a quit in Learn mode. See FilterLearnMode.
type learnQuitMsg struct{}

// FilterLearnMode turns every way the program could end into a note, in Learn
// mode. Bubble Tea runs the program's filter before it acts on a QuitMsg, so
// this one place catches every path: the quit keys, the quit menu, Ctrl+C at
// the last resort, and anything added later. Outside Learn mode it returns msg
// unchanged.
func FilterLearnMode(m *OS, msg tea.Msg) tea.Msg {
	if m == nil || !m.LearnMode {
		return msg
	}
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg, tea.SuspendMsg:
		return learnQuitMsg{}
	}
	return msg
}

// handleLearnQuit is what a quit does in Learn mode: close the quit menu if it
// is up, and say why nothing else happened.
func (m *OS) handleLearnQuit() {
	if m.ShowQuitMenu {
		m.CloseQuitMenu()
	}
	m.showLearnNote(learnNoteQuit)
}

// LearnApplyPreset puts a keybind preset in use in this tab only, in Learn
// mode, where there is no config file to write it to. The keys come from the
// same fill a config.toml with that preset gets on load, so the tour shows
// the keys tuios gives a person who picks it. tuios brings back the shipped
// keys.
func (m *OS) LearnApplyPreset(name string) tea.Cmd {
	if !m.LearnMode || m.UserConfig == nil || !slices.Contains(config.Presets(), name) {
		return nil
	}
	if name == m.currentPreset() {
		return nil
	}
	src := "[keybindings]\n"
	if name != config.PresetTuios {
		src += "preset = " + strconv.Quote(name) + "\n"
	}
	parsed, err := config.ParseUserConfig([]byte(src))
	if err != nil {
		m.ShowNotification("Keys not changed: "+err.Error(), "error", 0)
		return nil
	}
	// The rest of the tab's config stays: the tour's pinned look and any
	// option a lesson set.
	cfg := *m.UserConfig
	cfg.Keybindings = parsed.Keybindings
	cmd := m.ApplyReloadedConfig(&cfg)
	text := "The " + name + " keys are in use."
	if name != config.PresetTuios {
		text += " Press " + m.Settings.LeaderKey + " ctrl+t to see the tuios keys that this preset moved."
	}
	m.ShowNotification(text, "success", m.Settings.NotificationWarningDuration)
	return cmd
}

// LearnBackend is what Learn mode reads in place of the disk and git: the
// fake shell's files and its one repository. The browser build sets it. With
// it the rail lists the fake files, a file opens in the fake viewer, ctrl+b U
// makes a worktree of the fake repository, and the review reads its diff.
type LearnBackend interface {
	// ReadDir lists a folder, as session.ReadDirCapped does on a real
	// machine.
	ReadDir(dir string, limit int) ([]os.DirEntry, bool, error)
	// RepoRoot is the main checkout of the repository dir is in, or "".
	RepoRoot(dir string) string
	// NewWorktree makes a worktree of repoRoot on a new branch. It returns
	// the worktree's folder and the session a real machine would make.
	NewWorktree(repoRoot, branch string) (path, session string, err error)
}

// SetLearnBackend makes Learn mode read b in place of the disk and git. The
// rail's files section, its tree and the file search all read folders
// through it.
func (m *OS) SetLearnBackend(b LearnBackend) {
	m.learn = b
	if b != nil {
		readDirFunc = b.ReadDir
	}
}

// learnFiles reports whether Learn mode reads the fake files.
func (m *OS) learnFiles() bool { return m.LearnMode && m.learn != nil }

// LearnFilesChangedMsg says a file of the fake shell was written, made or
// removed. The rail reads its folders again, as a watch on the folder makes
// it do on a real machine.
type LearnFilesChangedMsg struct{}

// learnFilesChanged reads the listed folder and the tree's open folders
// again, quietly.
func (m *OS) learnFilesChanged() tea.Cmd {
	if !m.learnFiles() || !m.filesOn() {
		return nil
	}
	return tea.Batch(m.refreshChangedFolder(), m.refreshTreeAll())
}

// learnOpenFile opens a file of the fake shell in a new pane: in the viewer
// that stands in for vim with edit, in the one that stands in for less
// otherwise. On a real machine enter copies the path and shift+enter opens
// the editor, and the dock says so.
func (m *OS) learnOpenFile(path string, edit bool) {
	program, note := "less", "Opened %s. On your machine, enter copies the path and shift+enter opens your editor."
	if edit {
		program, note = "vim", "Opened %s in a viewer. On your machine, it opens in your editor."
	}
	name := filepath.Base(path)
	before := len(m.Windows)
	m.AddWindowIn(filepath.Dir(path), name, program, path)
	if len(m.Windows) == before {
		return
	}
	m.ShowNotification(fmt.Sprintf(note, name), "info", m.Settings.NotificationDuration*2)
	m.clearSidebarReturn()
	m.ExitSidebarFocus()
	m.pendingStartTerminalMode = true
	m.maybeEnterPendingTerminalMode()
}

// learnNewWorktree makes a worktree of the fake repository and opens a pane
// in it. A real machine makes a session there and attaches to it. The tab
// has no sessions, so the pane opens here, and the dock names the session.
func (m *OS) learnNewWorktree(repoRoot, branch string) {
	path, session, err := m.learn.NewWorktree(repoRoot, branch)
	if err != nil {
		m.ShowNotification("Could not make the worktree: "+err.Error()+".", "error", m.Settings.NotificationDuration*2)
		return
	}
	m.AddWindowIn(path, branch)
	m.ShowNotification("Created worktree '"+branch+"' in "+shortenHome(path)+". On your machine, it opens in the session "+session+".",
		"success", m.Settings.NotificationDuration*2)
	m.pendingStartTerminalMode = true
	m.maybeEnterPendingTerminalMode()
}

// LearnFiles is the rail's files section as a lesson checks it: the folder it
// lists, and the path of the row under the rail's cursor while the rail has
// the keyboard. Both are "" when there is none.
func (m *OS) LearnFiles() (dir, cursor string) {
	if !m.filesOn() {
		return "", ""
	}
	dir = m.filesView.Dir
	if !m.SidebarFocused {
		return dir, ""
	}
	if row, ok := m.sidebarCursorRow(); !ok || row.Kind != sidebarRowFileEntry {
		return dir, ""
	}
	if d, e, ok := m.fileActionEntry(); ok {
		cursor = filepath.Join(d, e.Name)
	}
	return dir, cursor
}

// LearnReview is the review overlay as a lesson checks it: whether it is
// open, the file it shows, and the hunk the cursor is in, -1 for none.
func (m *OS) LearnReview() (open bool, file string, hunk int) {
	r := &m.review
	if !r.open {
		return false, "", -1
	}
	if e := r.currentFile(); e != nil {
		file = e.path
	}
	hunk = -1
	if row, ok := m.reviewRowUnderCursor(); ok {
		hunk = row.hunk
	}
	return true, file, hunk
}

// FileSearchOpen reports whether the palette shows a file search.
func (m *OS) FileSearchOpen() bool { return m.fileSearch && m.ShowCommandPalette }
