package app

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// ActionRunner runs a keybinding action by its registry name, the way the
// key bound to it would. handled is false when no handler knows the name.
//
// The handlers live in internal/input, which imports this package, so the
// tape executor reaches them through this hook. internal/input sets it from
// an init function, so every program that handles keys can also run actions.
type ActionRunner func(name string, o *OS) (handled bool, cmd tea.Cmd)

var actionRunner atomic.Pointer[ActionRunner]

// SetActionRunner installs the function Action and run-command use to run an
// action by name.
func SetActionRunner(r ActionRunner) {
	actionRunner.Store(&r)
}

// scriptWait is a WaitFor that playback is holding for.
type scriptWait struct {
	cmd      tape.Command
	cond     tape.Condition
	deadline time.Time
}

// RunAction runs the keybinding action name, as the key bound to it would.
// It implements tape.Executor for the Action command.
func (m *OS) RunAction(name string) error {
	r := actionRunner.Load()
	if r == nil {
		return errors.New("actions cannot run here: this program has no key handling")
	}
	handled, cmd := (*r)(name, m)
	if !handled {
		if _, known := config.ActionDescriptions[name]; known {
			return fmt.Errorf("%s is a key of its own view, such as the Inbox, mail or the rail, and runs only there. Open the view, then use Press", name)
		}
		return fmt.Errorf("%q is not an action. Run 'tuios keybinds list' for the action names", name)
	}
	m.queueScriptCmd(cmd)
	m.MarkAllDirty()
	return nil
}

// PressKeys presses keys through the same handler a person's keys go
// through, so a leader key opens the prefix, copy mode gets its motions and an
// open dialog gets its keys. It implements tape.Executor for Press.
func (m *OS) PressKeys(keys []string) error {
	var msgs []tea.KeyPressMsg
	for _, k := range keys {
		msgs = append(msgs, m.parseKeysToMessages(k)...)
	}
	if len(msgs) == 0 {
		return fmt.Errorf("no keys in %q", strings.Join(keys, " "))
	}
	handler := getInputHandler()
	if handler == nil {
		return errors.New("keys cannot be pressed here: this program has no key handling")
	}
	// The script scope answers only while a tape plays, and its keys (ctrl+p
	// pauses) are not what a tape pressing keys means: the tape presses them
	// as the person would outside playback.
	wasScript := m.ScriptMode
	m.ScriptMode = false
	defer func() { m.ScriptMode = wasScript }()
	for _, msg := range msgs {
		_, cmd := handler(msg, m)
		m.queueScriptCmd(cmd)
	}
	m.MarkAllDirty()
	return nil
}

// CheckCondition reports whether c holds now. The error says what was seen
// instead. It implements tape.Executor for Expect and backs WaitFor.
func (m *OS) CheckCondition(c tape.Condition) error {
	switch c.Kind {
	case "text":
		w, err := m.conditionPane(c.Pane)
		if err != nil {
			return err
		}
		if w.Terminal == nil {
			return fmt.Errorf("pane %s has no screen yet", m.paneLabel(w))
		}
		w.RLockIO()
		content := w.Terminal.String()
		w.RUnlockIO()
		if c.Pattern.MatchString(content) {
			return nil
		}
		return fmt.Errorf("pane %s does not show it", m.paneLabel(w))
	case "pane":
		if len(m.windowsNamed(c.Value)) > 0 {
			return nil
		}
		return fmt.Errorf("no pane is named %q", c.Value)
	case "gone":
		if n := len(m.windowsNamed(c.Value)); n > 0 {
			return fmt.Errorf("%s %s still named %q", plural.Count(n, "pane"), plural.Word(n, "is", "are"), c.Value)
		}
		return nil
	case "focus":
		w := m.GetFocusedWindow()
		if w == nil {
			return errors.New("no pane has focus")
		}
		if w.ID == c.Value || m.getWindowDisplayName(w) == c.Value {
			return nil
		}
		return fmt.Errorf("the focused pane is %s", m.paneLabel(w))
	case "panes":
		if n := m.GetWorkspaceWindowCount(m.CurrentWorkspace); n != c.Count {
			return fmt.Errorf("workspace %d has %s", m.CurrentWorkspace, plural.Count(n, "pane"))
		}
		return nil
	case "agent":
		w, err := m.conditionPane(c.Pane)
		if err != nil {
			return err
		}
		if strings.EqualFold(w.AgentState, c.Value) {
			return nil
		}
		state := w.AgentState
		if state == "" {
			state = "none"
		}
		return fmt.Errorf("the agent state of pane %s is %s", m.paneLabel(w), state)
	case "workspace":
		if m.CurrentWorkspace != c.Count {
			return fmt.Errorf("workspace %d is on screen", m.CurrentWorkspace)
		}
		return nil
	case "mode":
		mode := "window"
		if m.Mode == TerminalMode {
			mode = "terminal"
		}
		if mode != c.Value {
			return fmt.Errorf("tuios is in %s mode", mode)
		}
		return nil
	}
	return fmt.Errorf("%q is not a condition", c.Kind)
}

// conditionPane is the pane a condition reads: the one named, or the focused
// one when no name is given.
func (m *OS) conditionPane(name string) (*terminal.Window, error) {
	if name == "" {
		if w := m.GetFocusedWindow(); w != nil {
			return w, nil
		}
		return nil, errors.New("no pane has focus")
	}
	switch matches := m.windowsNamed(name); len(matches) {
	case 0:
		return nil, fmt.Errorf("no pane is named %q", name)
	case 1:
		return matches[0], nil
	default:
		return nil, fmt.Errorf("%d panes are named %q", len(matches), name)
	}
}

// windowsNamed is every window whose name or id is name.
func (m *OS) windowsNamed(name string) []*terminal.Window {
	var out []*terminal.Window
	for _, w := range m.Windows {
		if w.ID == name || m.getWindowDisplayName(w) == name {
			out = append(out, w)
		}
	}
	return out
}

// paneLabel names a pane for a message: its name in quotes, or its short id.
func (m *OS) paneLabel(w *terminal.Window) string {
	if name := m.getWindowDisplayName(w); name != "" {
		return fmt.Sprintf("%q", name)
	}
	id := w.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return id
}

// queueScriptCmd keeps a command an action or a key returned, so the Update
// call that ran the tape command can return it. A tape command only returns
// an error, and the work an action starts (a dialog's first load, a quit)
// would be lost without this.
func (m *OS) queueScriptCmd(cmd tea.Cmd) {
	if cmd != nil {
		m.scriptCmds = append(m.scriptCmds, cmd)
	}
}

// takeScriptCmds returns the commands queueScriptCmd kept, and forgets them.
func (m *OS) takeScriptCmds() tea.Cmd {
	cmds := m.scriptCmds
	m.scriptCmds = nil
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// startScriptWait arms a WaitFor. Playback holds until checkScriptWait says
// the condition held or the wait failed.
func (m *OS) startScriptWait(cmd *tape.Command) {
	cond, err := tape.ParseCondition(cmd)
	if err != nil {
		m.failScript(cmd, err)
		return
	}
	m.ScriptWait = &scriptWait{cmd: *cmd, cond: cond, deadline: time.Now().Add(cond.Timeout)}
}

// checkScriptWait reports whether playback may go on: the condition holds, or
// the wait ran out of time and failed the tape.
func (m *OS) checkScriptWait() bool {
	w := m.ScriptWait
	if w == nil {
		return true
	}
	err := m.CheckCondition(w.cond)
	if err == nil {
		m.ScriptWait = nil
		return true
	}
	if time.Now().Before(w.deadline) {
		return false
	}
	m.ScriptWait = nil
	m.failScript(&w.cmd, fmt.Errorf("the wait for %s timed out after %s: %w", w.cond, w.cond.Timeout, err))
	return true
}

// failScript stops the tape at cmd. The rest of it does not run: a tape that
// carried on past a failed step typed into the wrong pane, or checked a
// screen that was never going to show what it waited for, and reported
// success. The failure is shown, kept for tuios tape play to exit with, and
// sent to a tuios tape exec that is waiting for the result.
func (m *OS) failScript(cmd *tape.Command, err error) {
	msg := cmd.Where() + ": " + err.Error()
	m.ScriptFailure = msg
	m.ScriptWait = nil
	m.ScriptWaitRegex = nil
	m.ScriptSleepUntil = time.Time{}
	m.ScriptAwaitWindows = 0
	if m.ScriptPlayer != nil {
		m.ScriptPlayer.Stop()
	}
	if m.ScriptFinishedTime.IsZero() {
		m.ScriptFinishedTime = time.Now()
	}
	m.ShowNotification("Tape stopped at "+msg, "error", m.Settings.NotificationDuration*3)
	m.reportScriptResult(false, msg)
}

// reportScriptResult answers the tuios tape exec that started this tape, if
// one did, and gives back the animations the run held off.
func (m *OS) reportScriptResult(ok bool, msg string) {
	if m.scriptRestoreAnimations {
		m.Settings.AnimationsSuppressed = false
		m.scriptRestoreAnimations = false
	}
	id := m.scriptRequestID
	m.scriptRequestID = ""
	if id == "" || m.DaemonClient == nil {
		return
	}
	if ok {
		msg = "script executed"
	}
	_ = m.DaemonClient.SendCommandResult(id, ok, msg)
}

// scriptBusy reports whether a tape is playing now. A finished tape keeps
// script mode for a moment to show that it is done; that tape is not busy.
func (m *OS) scriptBusy() bool {
	return m.ScriptMode && m.ScriptFinishedTime.IsZero()
}

// executeTapeScript plays a tape sent by tuios tape exec. It runs on the same
// player as tuios tape play, so a tape behaves the same way in both: Wait,
// WaitUntilRegex and WaitFor hold playback, a command waits for the pane the
// one before it asked for, and the first failure stops the tape. The result
// goes back to the caller when the tape ends, not when it starts.
func (m *OS) executeTapeScript(script string, requestID string) (tea.Cmd, error) {
	commands, errs := tape.ParseFile(script)
	if len(errs) > 0 {
		return nil, fmt.Errorf("the tape has %s. The first is at %s", plural.Count(len(errs), "error"), errs[0])
	}
	if len(commands) == 0 {
		return nil, errors.New("the tape has no commands")
	}
	if m.scriptBusy() {
		return nil, errors.New("a tape is already playing in this session. Wait for it to finish")
	}
	if m.ScriptMode {
		m.exitScriptMode()
	}
	m.startTapePlayback(commands, 0)
	m.scriptRequestID = requestID
	if !m.Settings.AnimationsSuppressed {
		m.Settings.AnimationsSuppressed = true
		m.scriptRestoreAnimations = true
	}
	return TickCmd(&m.Settings), nil
}
