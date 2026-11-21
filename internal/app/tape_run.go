package app

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/shquote"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
)

const tapeSeedSettle = 400 * time.Millisecond

const tapeFinishRefreshDelay = 1200 * time.Millisecond

type tapeLayoutRefreshMsg struct{}

func (m *OS) refreshAllPanesAfterTape() {
	if m.DaemonClient == nil {
		return
	}
	for _, w := range m.Windows {
		if w == nil || !w.DaemonMode || w.PTYID == "" || w.Workspace != m.CurrentWorkspace || w.Terminal == nil {
			continue
		}
		w.Resize(w.Width, w.Height)
		if state, err := m.DaemonClient.GetTerminalState(w.PTYID, 0, w.ScrollbackLenSync()); err == nil && state != nil {
			m.restoreTerminalContent(w, state)
		}
		w.InvalidateCache()
		w.MarkContentDirty()
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

func (m *OS) runProjectTape(content []byte, dir string) {
	if m.ScriptMode {
		m.ShowNotification("A tape is already running", "warning", m.Settings.NotificationDuration)
		return
	}
	if len(content) == 0 {
		m.ShowNotification("Tape is empty. Nothing to run", "warning", m.Settings.NotificationDuration)
		return
	}

	header, body := tape.ParseProjectHeader(string(content))

	if missing := missingRequirements(header.Requires); missing != "" {
		m.ShowNotification("Tape skipped: requires "+missing, "warning", m.Settings.NotificationDuration*2)
		return
	}

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
	m.startTapePlayback(compileProjectBody(body), header.Workspace)
}

func (m *OS) runTapeSessionScope(header tape.ProjectHeader, body, dir string) {
	name := header.Session
	if name == "" {
		name = sanitizeSessionName(filepath.Base(dir))
	}
	if name == "" {
		name = "project"
	}

	if m.DaemonClient == nil {
		m.ShowNotification("Tape: no session backend, running in current session", "warning", m.Settings.NotificationDuration*2)
		m.startTapePlayback(compileProjectBody(body), header.Workspace)
		return
	}

	if m.sessionExists(name) {
		if err := m.SwitchToSession(name); err != nil {
			m.ShowNotification("Tape: switch to "+name+" failed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
		}
		return
	}

	if err := m.SwitchToSession(name); err != nil {
		m.ShowNotification("Tape: create session "+name+" failed: "+err.Error(), "error", m.Settings.NotificationDuration*2)
		return
	}

	m.AddWindow("")
	cmds := seedCommands(dir)
	cmds = append(cmds, tape.Command{Type: tape.CommandTypeEnableTiling})
	cmds = append(cmds, compileProjectBody(body)...)
	m.startTapePlayback(cmds, header.Workspace)
	m.ShowNotification("Building project session "+name, "info", m.Settings.NotificationDuration)
}

func (m *OS) startTapePlayback(commands []tape.Command, workspace int) {
	if len(commands) == 0 {
		return
	}
	if workspace > 0 && workspace <= m.NumWorkspaces {
		m.switchToWorkspace(workspace, -1)
	}

	player := tape.NewPlayer(commands)
	m.ScriptPlayer = player
	m.ScriptMode = true
	m.ScriptPaused = false
	m.ScriptFinishedTime = time.Time{}
	m.ScriptAwaitWindows = 0
	m.ScriptAwaitDeadline = time.Time{}
	m.ScriptWait = nil
	m.ScriptFailure = ""
	m.scriptInFlight = false
	m.ScriptExecutor = tape.NewCommandExecutor(m)
}

// PlayTapeMsg asks the Update loop to play a tape script. The browser build
// sends it when a lesson, or the fake shell's `tuios tape play`, plays one.
type PlayTapeMsg struct {
	// Name is what the notification calls the tape.
	Name string
	// Script is the tape source.
	Script string
}

// PlayTape parses a tape script and plays it in this session through the
// interactive player, the one `tuios tape play` and the tape manager use. It
// returns the tick that drives playback.
func (m *OS) PlayTape(name, script string) (tea.Cmd, error) {
	if m.scriptBusy() {
		return nil, errors.New("a tape is already playing")
	}
	if m.ScriptMode {
		m.exitScriptMode()
	}
	commands, errs := tape.ParseFile(script)
	if len(errs) > 0 {
		return nil, errfmt.Errorf("tape %s: %s", name, errs[0])
	}
	if len(commands) == 0 {
		return nil, errfmt.Errorf("tape %s is empty", name)
	}
	m.startTapePlayback(commands, 0)
	if name != "" {
		m.ShowNotification("Playing "+name, "info", 2*time.Second)
	}
	return m.armTick(m.framePeriod()), nil
}

func seedCommands(dir string) []tape.Command {
	return []tape.Command{
		{Type: tape.CommandTypeSleep, Delay: tapeSeedSettle},
		{Type: tape.CommandTypeType, Args: []string{"cd " + shquote.Quote(dir)}},
		{Type: tape.CommandTypeEnter},
		{Type: tape.CommandTypeSleep, Delay: 200 * time.Millisecond},
	}
}

func (m *OS) sessionExists(name string) bool {
	for _, s := range m.RefreshSessionList() {
		if s.Title == name {
			return true
		}
	}
	return false
}

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
