package app

import (
	"bufio"
	"bytes"
	"cmp"
	"errors"
	"image/color"
	"io"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/release"
	"github.com/Gaurav-Gosain/tuios/internal/restartplan"
)

const paletteRestartDaemonName = "Restart the daemon"

func (m *OS) restartHint() string {
	if m.DaemonRestartAvailable() {
		return `To update it, use "Restart the daemon" in the command palette.`
	}
	return restartHintPlain
}

const restartHintPlain = "To update it, run 'tuios restart-server' on the machine it runs on."

type restartPhase int

const (
	restartClosed restartPhase = iota
	restartReading
	restartConfirm
	restartStopping
	restartStarting
	restartReattaching
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

type restartReport = restartplan.Report

type daemonRestart struct {
	phase                         restartPhase
	gen                           int
	list                          listnav.State
	plan                          restartReport
	scroll, scrollMax, scrollPage int
	done                          *restartReport
	err, fix                      string
	lost                          bool
	asked                         bool
	steps                         chan tea.Msg
}

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
	m.restart = daemonRestart{phase: restartReading, gen: m.restart.gen + 1, list: listnav.State{Cursor: RestartRowCancel}}
}

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

// DaemonRestartMove moves the cursor between the two answers. A move of a
// page or more (page down, page up, home, end) scrolls the plan instead.
func (m *OS) DaemonRestartMove(delta int) {
	if m.restart.phase != restartConfirm {
		return
	}
	if delta > 1 || delta < -1 {
		m.DaemonRestartScroll(delta / max(abs(delta), 1) * max(m.restart.scrollPage, 1))
		return
	}
	m.restart.list.Move(delta, restartRowCount, restartRowCount, false, nil)
}

// DaemonRestartScroll scrolls the plan by delta lines, when it is taller than
// the screen.
func (m *OS) DaemonRestartScroll(delta int) {
	r := &m.restart
	if r.phase != restartConfirm || r.scrollMax == 0 {
		return
	}
	r.scroll = max(min(r.scroll+delta, r.scrollMax), 0)
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

func readRestartPlan() (restartReport, error) {
	bin, _, err := release.NewestInstalled()
	if err != nil {
		return restartReport{}, err
	}
	out, err := exec.Command(bin, "restart-server", "--dry-run", "--json").Output()
	line, _, _ := strings.Cut(string(out), "\n")
	var plan restartReport
	if jerr := jsonx.Unmarshal([]byte(line), &plan); jerr != nil || plan.Step != restartplan.StepPlan {
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

func (m *OS) startDaemonRestart() tea.Cmd {
	r := &m.restart
	m.SyncStateToDaemon()
	r.phase = restartStopping
	r.steps = make(chan tea.Msg, 8)
	go runRestartCommand(r.plan.Binary, restartplan.Digest(&r.plan), r.gen, r.steps)
	return listenRestart(m.stopSignal(), r.steps)
}

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
		if jsonx.Unmarshal(sc.Bytes(), &rep) == nil {
			ch <- restartStepMsg{gen: gen, rep: rep}
		}
	}
	scanErr := sc.Err()
	if scanErr != nil {
		_, _ = io.Copy(io.Discard, out)
	}
	err = cmd.Wait()
	if err == nil && scanErr != nil {
		err = errfmt.Errorf("read restart-server output: %w", scanErr)
	}
	ch <- restartEndMsg{gen: gen, err: err, stderr: stderr.String()}
}

func listenRestart(stop <-chan struct{}, ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-ch:
			return msg
		case <-stop:
			return nil
		}
	}
}

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
		return listenRestart(m.stopSignal(), r.steps)

	case restartEndMsg:
		if msg.gen != r.gen {
			return nil
		}
		if d := r.done; d != nil && d.Error == "" && d.Restarted && slices.Contains(d.Missing, m.SessionName) {
			r.lost = true
			m.failRestart("The daemon restarted on "+d.After+", and session "+m.SessionLabel(m.SessionName)+" did not come back.",
				"Run 'tuios resurrect' to see the saved sessions, then 'tuios attach' to one.")
			return nil
		}
		if d := r.done; d != nil && d.Error == "" && d.Restarted {
			r.phase = restartReattaching
			m.RestartExec = &RestartExec{Binary: r.plan.Binary, Session: m.SessionName}
			gen := r.gen
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
		return m.QuitCmd()
	}
	return nil
}

func (m *OS) failRestart(what, fix string) {
	r := &m.restart
	r.phase = restartFailed
	r.err, r.fix = what, fix
	m.LogError("Daemon restart: %s %s", what, fix)
}

func (m *OS) quitAfterRestartFailure() tea.Cmd {
	m.RestartFailure = &RestartFailure{What: m.restart.err, Fix: m.restart.fix}
	m.ExitReason = ExitRestartFailed
	return m.QuitCmd()
}

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

func capitalFix(fix string) string {
	if fix == "" {
		return ""
	}
	return strings.ToUpper(fix[:1]) + fix[1:]
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func plainOneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}

// DaemonRestartSelected is the row under the cursor.
func (m *OS) DaemonRestartSelected() int { return m.restart.list.Cursor }

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
