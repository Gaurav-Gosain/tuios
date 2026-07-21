package app

import (
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/tape/trust"
)

// tapeSeedSettle is how long the seeded window in a freshly created project
// session is given to materialize before the tape body runs. In a daemon
// session the seed window is created asynchronously (the daemon makes it and
// pushes it back), so the body waits this out before its first keystroke.
const tapeSeedSettle = 400 * time.Millisecond

// tapeFinishRefreshDelay is how long after a tape finishes the client waits
// before re-fetching every pane's content from the daemon. It lets the last
// commands' output land on the daemon first.
const tapeFinishRefreshDelay = 700 * time.Millisecond

// tapeLayoutRefreshMsg asks the Update loop to refresh all panes from the daemon
// after a project tape has built its layout. See refreshAllPanesAfterTape.
type tapeLayoutRefreshMsg struct{}

// refreshAllPanesAfterTape re-fetches terminal content for every daemon pane in
// the session and repaints. A project tape creates panes rapidly; a split
// pane's first output can land before the client subscribed to its PTY, so its
// content sits on the daemon but not in the client's emulator. Re-fetching every
// pane's state (not just unsubscribed ones, which SubscribeWorkspaceWindows
// limits itself to) reconciles the client with the daemon and marks the frame
// dirty so the finished layout shows what actually ran.
func (m *OS) refreshAllPanesAfterTape() {
	if m.DaemonClient == nil {
		return
	}
	// Best-effort repaint: re-fetch each current-workspace pane's content from the
	// daemon, drop the stale cached layer, flag new output, and nudge the render
	// path. This reconciles panes whose output landed while the client was still
	// building the layout.
	for _, w := range m.Windows {
		if w == nil || !w.DaemonMode || w.PTYID == "" || w.Workspace != m.CurrentWorkspace {
			continue
		}
		if state, err := m.DaemonClient.GetTerminalState(w.PTYID, true); err == nil && state != nil {
			m.restoreTerminalContent(w, state)
		}
		w.InvalidateCache()
		w.HasNewOutput.Store(true)
	}
	m.MarkAllDirty()
	if m.PTYDataChan != nil {
		select {
		case m.PTYDataChan <- struct{}{}:
		default:
		}
	}
	m.renderSkipped = false
}

// runProjectTape executes a reviewed, eligible tape. content is the exact bytes
// that were hashed and shown to the user (trust.Result.Content); it is never
// re-read from disk. dir is the project root (the directory holding the tape).
//
// It is the single execution entry point shared by "Run once", "Trust and run",
// and auto-mode. It parses the declarative header, honors Require, and dispatches
// on scope. A tape never runs while another tape is executing.
func (m *OS) runProjectTape(content []byte, dir string) {
	if m.ScriptMode {
		m.ShowNotification("A tape is already running", "warning", config.NotificationDuration)
		return
	}
	if len(content) == 0 {
		m.ShowNotification("Tape is empty; nothing to run", "warning", config.NotificationDuration)
		return
	}

	header, body := tape.ParseProjectHeader(string(content))

	// Require: skip with a notice if a named binary is missing, rather than
	// typing a command into a shell that cannot run it.
	if missing := missingRequirements(header.Requires); missing != "" {
		m.ShowNotification("Tape skipped: requires "+missing, "warning", config.NotificationDuration*2)
		return
	}

	// Suppress detection while this tape runs, and mark the project root handled
	// so re-entering it (or a tape that cd's onward) cannot chain another run.
	if m.tapeDetect.handled == nil {
		m.tapeDetect.handled = make(map[string]bool)
	}
	m.tapeDetect.handled[dir] = true

	scope := header.Scope
	if scope != tape.ScopeCurrent {
		scope = tape.ScopeSession
	}

	if scope == tape.ScopeSession {
		m.runTapeSessionScope(header, body, dir)
		return
	}
	// Scope current: build in the current session from the focused window,
	// best-effort. The body is compiled to the executor commands that work
	// (see tape_body.go); it is not forced into tiling, so it composes with
	// whatever layout state exists.
	m.startTapePlayback(compileProjectBody(body), header.Workspace)
}

// runTapeSessionScope implements the default session-per-project behavior: the
// session named after the project (or the header's Session) is the durable
// artifact. If it already exists, switch to it and do not rebuild. Otherwise
// create it, seed a window at the project root, and build it from the tape.
func (m *OS) runTapeSessionScope(header tape.ProjectHeader, body, dir string) {
	name := header.Session
	if name == "" {
		name = sanitizeSessionName(filepath.Base(dir))
	}
	if name == "" {
		name = "project"
	}

	// Session scope needs the daemon's named sessions. Outside a daemon session
	// there is nowhere to create one, so fall back to the current session, which
	// is honest best-effort and documented.
	if m.DaemonClient == nil {
		m.ShowNotification("Tape: no session backend, running in current session", "warning", config.NotificationDuration*2)
		m.startTapePlayback(compileProjectBody(body), header.Workspace)
		return
	}

	if m.sessionExists(name) {
		// The session is the constructor's output, run once. Re-entry just takes
		// the user back to it; it never re-runs the tape.
		if err := m.SwitchToSession(name); err != nil {
			m.ShowNotification("Tape: switch to "+name+" failed: "+err.Error(), "error", config.NotificationDuration*2)
		}
		return
	}

	if err := m.SwitchToSession(name); err != nil {
		m.ShowNotification("Tape: create session "+name+" failed: "+err.Error(), "error", config.NotificationDuration*2)
		return
	}

	// A freshly created session is empty. Seed a single window whose shell is put
	// at the project root, turn tiling on so Split creates real tiled panes, then
	// build the layout from the compiled body. The seed, the EnableTiling, and the
	// body run as one command list through the interactive player, which spaces
	// them across ticks so the daemon's asynchronous window creation keeps up.
	m.AddWindow("")
	cmds := seedCommands(dir)
	cmds = append(cmds, tape.Command{Type: tape.CommandTypeEnableTiling})
	cmds = append(cmds, compileProjectBody(body)...)
	m.startTapePlayback(cmds, header.Workspace)
	m.ShowNotification("Building project session "+name, "info", config.NotificationDuration)
}

// startTapePlayback starts the interactive tape player over an already-compiled
// command list. workspace, when non-zero, is the workspace to build in.
func (m *OS) startTapePlayback(commands []tape.Command, workspace int) {
	if len(commands) == 0 {
		return
	}
	if workspace > 0 && workspace <= m.NumWorkspaces {
		m.SwitchToWorkspace(workspace)
	}

	player := tape.NewPlayer(commands)
	m.ScriptPlayer = player
	m.ScriptMode = true
	m.ScriptPaused = false
	m.ScriptFinishedTime = time.Time{}
	m.ScriptExecutor = tape.NewCommandExecutor(m)
}

// seedCommands builds the leading commands that give an asynchronously created
// session window time to appear and put its shell at the project root, so a
// session-scoped tape starts from a known, deterministic state.
func seedCommands(dir string) []tape.Command {
	return []tape.Command{
		{Type: tape.CommandTypeSleep, Delay: tapeSeedSettle},
		{Type: tape.CommandTypeType, Args: []string{"cd " + shellSingleQuote(dir)}},
		{Type: tape.CommandTypeEnter},
		{Type: tape.CommandTypeSleep, Delay: 200 * time.Millisecond},
	}
}

// sessionExists reports whether a daemon session with the given name is present.
func (m *OS) sessionExists(name string) bool {
	for _, s := range m.RefreshSessionList() {
		if s.Name == name {
			return true
		}
	}
	return false
}

// missingRequirements returns the first required command not found on PATH, or
// the empty string when all are present.
func missingRequirements(requires []string) string {
	for _, cmd := range requires {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			continue
		}
		if _, err := exec.LookPath(cmd); err != nil {
			return cmd
		}
	}
	return ""
}

// sanitizeSessionName turns a directory basename into a session name safe for
// the switcher: it keeps letters, digits, dash, underscore and dot, and folds
// any other run into a single dash.
func sanitizeSessionName(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// shellSingleQuote wraps s in single quotes, escaping embedded single quotes,
// so it survives as one argument when typed into a POSIX shell.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// reCheckTape re-reads and re-classifies the tape at path through the trust
// store, so a caller can confirm nothing changed between detection and
// execution. It returns the fresh result; the caller decides what a status or
// hash change means.
func (m *OS) reCheckTape(path string) (trust.Result, bool) {
	store := m.ensureTapeTrust()
	if store == nil {
		return trust.Result{}, false
	}
	res, err := store.Check(path)
	if err != nil {
		m.LogInfo("tape re-check: %v", err)
	}
	return res, true
}
