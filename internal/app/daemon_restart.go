package app

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"image/color"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/restartplan"
)

// Restarting the daemon from the app.
//
// The daemon runs the build it started with until it stops, so after an
// update it runs the old one. The way to move it used to be to detach, run
// tuios kill-server and start tuios again, and nothing on the way said what
// that stopped. The app now does it in place: the command palette, the
// Daemon tab of the settings, the rail's menu for this machine and the
// version notice all open one dialog.
//
// The dialog runs `tuios restart-server` of the newest tuios installed, which
// is also the binary the new daemon runs (release.NewestInstalled). It runs it
// twice: once with --dry-run for the plan the dialog shows, and once to
// restart, reading the steps it writes as they happen. So the list the person
// agrees to and the restart that runs come from the same code, and that code
// is the new build's.
//
// The daemon going away is what a restart is, so while one runs the lost
// connection is expected and does not end the client. When the new daemon
// answers, the client quits and the local entry point starts the newest tuios
// again as `tuios attach <session>`: an exec on Unix, a child on Windows (see
// RestartExec). The person lands back in the session, on the new build of
// both the daemon and the client.

// paletteRestartDaemonName is the palette's row for the dialog.
const paletteRestartDaemonName = "Restart the daemon"

// restartHint ends a message about a daemon too old for something: the way
// to move it to the new build. The palette entry is only on a client that
// can restart its daemon, so an ssh or a browser client is told the command.
func (m *OS) restartHint() string {
	if m.DaemonRestartAvailable() {
		return `To update it, use "Restart the daemon" in the command palette.`
	}
	return restartHintPlain
}

// restartHintPlain is restartHint where the client is not known: the command,
// which works from any shell on the daemon's machine.
const restartHintPlain = "To update it, run 'tuios restart-server' on the machine it runs on."

// restartPhase is where the restart dialog is.
type restartPhase int

const (
	restartClosed restartPhase = iota
	// restartReading waits for the plan.
	restartReading
	// restartConfirm shows the plan and asks.
	restartConfirm
	// restartStopping, restartStarting and restartReattaching are the steps.
	restartStopping
	restartStarting
	restartReattaching
	// restartFailed says what went wrong and what to do next.
	restartFailed
)

// Restart dialog rows, in drawn order. Cancel is the row the dialog opens on:
// the row that stops every program is never the default.
const (
	// RestartRowRestart restarts the daemon.
	RestartRowRestart = iota
	// RestartRowCancel closes the dialog and changes nothing.
	RestartRowCancel
	restartRowCount
)

// restartReport is one line of `tuios restart-server --json`. The CLI writes
// it and this file reads it, with the one type in internal/restartplan.
type restartReport = restartplan.Report

// daemonRestart is the dialog's state. The zero value is closed.
type daemonRestart struct {
	phase restartPhase
	// gen makes a message from an abandoned dialog ignorable.
	gen      int
	selected int
	plan     restartReport
	// done is the restart's result, once the command wrote it.
	done *restartReport
	// err and fix are what a failure says.
	err, fix string
	// lost says the connection to the daemon is gone, so the only way on
	// from a failure is out.
	lost bool
	// asked says the plan was asked for.
	asked bool
	steps chan tea.Msg
}

// running reports whether a restart is under way: the steps between the
// answer and the exec, when the daemon going away is expected.
func (r *daemonRestart) running() bool {
	return r.phase >= restartStopping && r.phase <= restartReattaching
}

// RestartExec is how the local entry point brings the client back after a
// restart: it runs Binary as `tuios attach Session`, and the new client says
// the restart worked. Set only with ExitRestarted.
type RestartExec struct {
	Binary  string
	Session string
}

// RestartFailure is what a restart that failed after the daemon stopped
// said, for the entry point to print. Set only with ExitRestartFailed.
type RestartFailure struct {
	What, Fix string
}

type (
	restartPlanMsg struct {
		gen  int
		plan restartReport
		err  error
	}
	restartStepMsg struct {
		gen int
		rep restartReport
	}
	restartEndMsg struct {
		gen    int
		err    error
		stderr string
	}
	restartQuitMsg struct{ gen int }
)

// DaemonRestartAvailable reports whether this client can restart its daemon:
// a local client attached to the daemon on this machine. A served client (ssh,
// the browser) runs on the daemon's machine for someone who is not, and a
// session on another host has its daemon there.
func (m *OS) DaemonRestartAvailable() bool {
	return m.IsDaemonSession && m.DaemonClient != nil && m.Client == ClientLocal &&
		!m.RemoteClient && m.AttachedHost == "" && runtime.GOOS != "js"
}

// DaemonRestartOpen reports whether the restart dialog is up.
func (m *OS) DaemonRestartOpen() bool { return m.restart.phase != restartClosed }

// OpenDaemonRestart opens the restart dialog. The plan is asked for after
// the message that opened it (restartPlanCmd), so every surface can open the
// dialog from a handler that returns no command.
func (m *OS) OpenDaemonRestart() {
	if !m.DaemonRestartAvailable() {
		m.ShowNotification("This window cannot restart the daemon. Run 'tuios restart-server' on the machine that runs it.", "info", m.Settings.NotificationDuration)
		return
	}
	if m.restart.running() {
		return
	}
	m.restart = daemonRestart{phase: restartReading, gen: m.restart.gen + 1, selected: RestartRowCancel}
}

// restartPlanCmd reads the plan for a dialog that has just opened. It runs
// after every message, and costs one comparison when no dialog waits.
func (m *OS) restartPlanCmd() tea.Cmd {
	r := &m.restart
	if r.phase != restartReading || r.asked {
		return nil
	}
	r.asked = true
	gen := r.gen
	return func() tea.Msg {
		plan, err := readRestartPlan()
		return restartPlanMsg{gen: gen, plan: plan, err: err}
	}
}

// CloseDaemonRestart closes the dialog, unless a restart is under way: the
// steps cannot be stopped halfway, and the dialog is where they show.
func (m *OS) CloseDaemonRestart() tea.Cmd {
	if m.restart.running() {
		return nil
	}
	if m.restart.phase == restartFailed && m.restart.lost {
		return m.quitAfterRestartFailure()
	}
	m.restart.phase = restartClosed
	m.restart.gen++
	return nil
}

// DaemonRestartMove moves the cursor between the two answers.
func (m *OS) DaemonRestartMove(delta int) {
	if m.restart.phase != restartConfirm {
		return
	}
	m.restart.selected = clampInt(m.restart.selected+delta, 0, restartRowCount-1)
}

// DaemonRestartActivate runs the row at idx: in the question, restart or
// cancel; after a failure, the one way on.
func (m *OS) DaemonRestartActivate(idx int) tea.Cmd {
	switch m.restart.phase {
	case restartConfirm:
		if idx == RestartRowRestart {
			return m.startDaemonRestart()
		}
		return m.CloseDaemonRestart()
	case restartFailed, restartReading:
		return m.CloseDaemonRestart()
	}
	return nil
}

// readRestartPlan runs restart-server --dry-run of the newest tuios.
func readRestartPlan() (restartReport, error) {
	bin, _, err := release.NewestInstalled()
	if err != nil {
		return restartReport{}, err
	}
	out, err := exec.Command(bin, "restart-server", "--dry-run", "--json").Output()
	line, _, _ := strings.Cut(string(out), "\n")
	var plan restartReport
	if jerr := json.Unmarshal([]byte(line), &plan); jerr != nil || plan.Step != restartplan.StepPlan {
		if err == nil {
			err = errors.New("its answer could not be read")
		}
		return restartReport{Binary: bin}, err
	}
	if plan.Binary == "" {
		plan.Binary = bin
	}
	return plan, nil
}

// startDaemonRestart pushes the layout, then runs the restart in the
// background and listens to its steps.
func (m *OS) startDaemonRestart() tea.Cmd {
	r := &m.restart
	// The daemon saves what it holds when it stops, so it must hold this
	// client's layout first.
	m.SyncStateToDaemon()
	r.phase = restartStopping
	r.steps = make(chan tea.Msg, 8)
	go runRestartCommand(r.plan.Binary, restartplan.Digest(&r.plan), r.gen, r.steps)
	return listenRestart(r.steps)
}

// runRestartCommand runs restart-server --yes and sends each step it writes.
// The digest is of the plan the person agreed to: the command refuses when
// what a restart stops changed since.
func runRestartCommand(bin, digest string, gen int, ch chan<- tea.Msg) {
	cmd := exec.Command(bin, "restart-server", "--yes", "--json", "--expect", digest)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err == nil {
		err = cmd.Start()
	}
	if err != nil {
		ch <- restartEndMsg{gen: gen, err: err}
		return
	}
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		var rep restartReport
		if json.Unmarshal(sc.Bytes(), &rep) == nil {
			ch <- restartStepMsg{gen: gen, rep: rep}
		}
	}
	ch <- restartEndMsg{gen: gen, err: cmd.Wait(), stderr: stderr.String()}
}

// listenRestart waits for the next step.
func listenRestart(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// handleRestartMsg applies one message of the restart.
func (m *OS) handleRestartMsg(msg tea.Msg) tea.Cmd {
	r := &m.restart
	switch msg := msg.(type) {
	case restartPlanMsg:
		if msg.gen != r.gen || r.phase != restartReading {
			return nil
		}
		switch {
		case msg.err != nil:
			m.failRestart("tuios could not read what a restart stops: "+msg.err.Error()+".",
				"Run 'tuios restart-server' in a shell to see why.")
		case msg.plan.Error != "":
			m.failRestart(msg.plan.Error, capitalFix(msg.plan.Fix))
		default:
			r.plan = msg.plan
			r.phase = restartConfirm
		}
		return nil

	case restartStepMsg:
		if msg.gen != r.gen {
			return nil
		}
		switch msg.rep.Step {
		case restartplan.StepStopping:
			r.phase = restartStopping
		case restartplan.StepStarting:
			r.phase = restartStarting
		case restartplan.StepDone:
			rep := msg.rep
			r.done = &rep
		}
		return listenRestart(r.steps)

	case restartEndMsg:
		if msg.gen != r.gen {
			return nil
		}
		if d := r.done; d != nil && d.Error == "" && d.Restarted && slices.Contains(d.Missing, m.SessionName) {
			// The new daemon answers, and this session is not on it. An
			// attach to it would fail, so the dialog says so instead.
			r.lost = true
			m.failRestart("The daemon restarted on "+d.After+", and session "+m.SessionLabel(m.SessionName)+" did not come back.",
				"Run 'tuios resurrect' to see the saved sessions, then 'tuios attach' to one.")
			return nil
		}
		if d := r.done; d != nil && d.Error == "" && d.Restarted {
			r.phase = restartReattaching
			m.RestartExec = &RestartExec{Binary: r.plan.Binary, Session: m.SessionName}
			gen := r.gen
			// One frame of "reattaching" before the screen goes.
			return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return restartQuitMsg{gen: gen} })
		}
		switch {
		case r.done != nil && r.done.Error != "":
			m.failRestart(r.done.Error, capitalFix(r.done.Fix))
		case msg.err != nil:
			m.failRestart("The restart did not finish: "+plainOneLine(cmp.Or(lastNonEmptyLine(msg.stderr), msg.err.Error()))+".",
				"Run 'tuios restart-server' in a shell to see why.")
		default:
			m.failRestart("The restart did not finish.", "Run 'tuios restart-server' in a shell to see why.")
		}
		return nil

	case restartQuitMsg:
		if msg.gen != r.gen || r.phase != restartReattaching {
			return nil
		}
		m.ExitReason = ExitRestarted
		return tea.Quit
	}
	return nil
}

// failRestart moves the dialog to its failure.
func (m *OS) failRestart(what, fix string) {
	r := &m.restart
	r.phase = restartFailed
	r.err, r.fix = what, fix
	m.LogError("Daemon restart: %s %s", what, fix)
}

// quitAfterRestartFailure ends the client after a failure that left no
// daemon to go back to, with the failure for the entry point to print.
func (m *OS) quitAfterRestartFailure() tea.Cmd {
	m.RestartFailure = &RestartFailure{What: m.restart.err, Fix: m.restart.fix}
	m.ExitReason = ExitRestartFailed
	return tea.Quit
}

// noteRestartDisconnect takes a lost connection while a restart runs. It
// reports whether it took it: the caller quits the client otherwise.
func (m *OS) noteRestartDisconnect() bool {
	r := &m.restart
	if r.running() {
		r.lost = true
		return true
	}
	if r.phase == restartFailed && r.lost {
		return true
	}
	return false
}

// capitalFix starts a fix from the command with a capital, as a sentence of
// its own in the dialog.
func capitalFix(fix string) string {
	if fix == "" {
		return ""
	}
	return strings.ToUpper(fix[:1]) + fix[1:]
}

// lastNonEmptyLine is the last line of s with text on it.
func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// plainOneLine drops what a terminal would read as control from one line.
func plainOneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// DaemonRestartSelected is the row under the cursor.
func (m *OS) DaemonRestartSelected() int { return m.restart.selected }

// restartDaemonItem is the Daemon tab's row: the version the daemon runs, in
// the warning colour when it is not this window's, and enter or a click opens
// the restart dialog.
func (m *OS) restartDaemonItem() settingItem {
	return settingItem{
		Label:   "Restart the daemon",
		Desc:    "Stop the daemon and start it with the newest tuios installed. The dialog lists what stops before it asks.",
		Control: controlStatus,
		value: func(m *OS) string {
			if m.DaemonClient == nil {
				return ""
			}
			if c, d := m.DaemonClient.BuildMismatch(); c != "" {
				return "runs " + d + ", this window " + c
			}
			return "runs " + m.DaemonClient.DaemonVersion()
		},
		ink: func(pal overlay.Palette) color.Color {
			if m.DaemonClient != nil {
				if c, _ := m.DaemonClient.BuildMismatch(); c != "" {
					return pal.Warn
				}
			}
			return pal.FgDim
		},
		activate: func(m *OS) tea.Cmd {
			m.CloseSettings()
			m.OpenDaemonRestart()
			return nil
		},
	}
}
