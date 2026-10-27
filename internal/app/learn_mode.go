package app

import (
	"maps"
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
	learnNoteFiles    = "Your files are not in the browser demo, so this is off."
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
	"kill_session":            learnNoteSessions,
	"kill_session_next":       learnNoteSessions,
	"rename_session":          learnNoteSessions,
	"set_note":                learnNoteSessions,
	"session_new_worktree":    learnNoteSessions,
	"repo_new_worktree":       learnNoteSessions,
	"new_worktree":            learnNoteSessions,
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
	"file_open":               learnNoteFiles,
	"file_edit":               learnNoteFiles,
	"file_copy_path":          learnNoteFiles,
	"file_send":               learnNoteFiles,
	"file_search":             learnNoteFiles,
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
		text += " Press " + m.Settings.LeaderKey + " ctrl+t for the tuios keys it moved."
	}
	m.ShowNotification(text, "success", m.Settings.NotificationWarningDuration)
	return cmd
}
