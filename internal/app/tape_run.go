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
	m.startTapePlayback(body, header.Workspace)
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
		m.startTapePlayback(body, header.Workspace)
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
	// at the project root, then build the layout from the tape body. The seed and
	// cd are prepended to the body so the whole thing runs through the one
	// interactive player.
	m.AddWindow("")
	seeded := seedPrefix(dir) + body
	m.startTapePlayback(seeded, header.Workspace)
	m.ShowNotification("Building project session "+name, "info", config.NotificationDuration)
}

// startTapePlayback parses body and starts the interactive tape player, exactly
// like the tape manager does for a saved tape. workspace, when non-zero, is the
// workspace to build in.
func (m *OS) startTapePlayback(body string, workspace int) {
	if strings.TrimSpace(body) == "" {
		return
	}
	if workspace > 0 && workspace <= m.NumWorkspaces {
		m.SwitchToWorkspace(workspace)
	}

	lexer := tape.New(body)
	parser := tape.NewParser(lexer)
	commands := parser.Parse()

	player := tape.NewPlayer(commands)
	m.ScriptPlayer = player
	m.ScriptMode = true
	m.ScriptPaused = false
	m.ScriptFinishedTime = time.Time{}
	m.ScriptExecutor = tape.NewCommandExecutor(m)
}

// seedPrefix builds the leading tape fragment that gives an asynchronously
// created session window time to appear and puts its shell at the project root,
// so a session-scoped tape starts from a known, deterministic state.
func seedPrefix(dir string) string {
	var b strings.Builder
	b.WriteString("Sleep ")
	b.WriteString(tapeSeedSettle.String())
	b.WriteString("\n")
	b.WriteString("Type ")
	b.WriteString(quoteTapeString("cd " + shellSingleQuote(dir)))
	b.WriteString(" Enter\n")
	b.WriteString("Sleep 150ms\n")
	return b.String()
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

// quoteTapeString renders s as a double-quoted tape string literal for the Type
// command, escaping backslashes and double quotes.
func quoteTapeString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
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
