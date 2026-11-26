package app

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/progresume"
	"github.com/Gaurav-Gosain/tuios/internal/serverscope"
)

// tuios as a program that tells the terminal it runs in how to bring it back
// (OSC 7577, the Program Resume Protocol).
//
// A terminal that supports the protocol puts OSC7577_TOKEN in the
// environment of the first process of each of its panes. A client attached
// from such a pane reports its own attach command, tuios attach and the
// session, signed with that token: when it attaches, and again after each
// session switch. The terminal can then attach a new client to the same
// session after it restarts. The entry point takes the token out of the
// client's environment before anything is started (OSOptions.HostResumeToken),
// so no pane, editor or daemon this client starts can report in its place.
//
// The client sends clear only when the person ends it on purpose: a detach,
// a quit, or a kill of the session. A hangup or a SIGTERM is what a client
// gets when its terminal ends, which is when the record is needed, so those
// send nothing. The report goes out on the client's own output, as the OSC
// 7501 reports do, and never into a pane.

// hostResume is what this client reported to its terminal.
type hostResume struct {
	// token is the terminal's token, "" when it gave none.
	token string
	// reported says a report went out, for the session and the machine
	// named here.
	reported           bool
	session, onMachine string
	// bin is this binary's absolute path, read once.
	bin string
}

// hostResumeArgv is the command that attaches a new client to the session
// this client shows: the absolute path of this binary, the server and the
// machine it reaches, and the session.
func (m *OS) hostResumeArgv() []string {
	if m.hostResume.bin == "" {
		bin, err := os.Executable()
		if err != nil {
			return nil
		}
		m.hostResume.bin = bin
	}
	argv := []string{m.hostResume.bin}
	if name := serverscope.Name(); name != "" {
		argv = append(argv, "--server", name)
	}
	argv = append(argv, "attach")
	if m.AttachedHost != "" {
		argv = append(argv, "--host", m.AttachedHost)
	}
	return append(argv, "--", m.SessionName)
}

// hostResumeOn reports whether this client reports its command: its terminal
// gave a token, it is a local client of a daemon session, and it is not on
// its way out.
func (m *OS) hostResumeOn() bool {
	return m.hostResume.token != "" && m.Client == ClientLocal && m.IsDaemonSession &&
		!m.LearnMode && !m.quitting && m.SessionName != ""
}

// hostResumeAfter reports the attach command when it changed. Update calls
// it after every message, so the first report goes out with the first
// frame and a session switch is reported at once.
func (m *OS) hostResumeAfter() tea.Cmd {
	if !m.hostResumeOn() {
		return nil
	}
	// The command changes with the session and the machine only, so this
	// costs two compares per message.
	h := &m.hostResume
	if h.reported && h.session == m.SessionName && h.onMachine == m.AttachedHost {
		return nil
	}
	argv := m.hostResumeArgv()
	if len(argv) == 0 {
		return nil
	}
	h.reported, h.session, h.onMachine = true, m.SessionName, m.AttachedHost
	return tea.Raw(progresume.Sequence(progresume.Report{PID: os.Getpid(), Time: time.Now().Unix(), Argv: argv}, m.hostResume.token))
}

// HostResumeClear is the clear report for a client the person ended on
// purpose, or "" when there is nothing to clear or the client ended for
// another reason. The caller writes it to the client's terminal once the
// program has stopped.
func (m *OS) HostResumeClear() string {
	if m.hostResume.token == "" || !m.hostResume.reported || !m.quitting {
		return ""
	}
	if m.ExitReason != ExitNormal && m.ExitReason != ExitSessionKilled {
		return ""
	}
	m.hostResume.reported = false
	return progresume.Sequence(progresume.Report{PID: os.Getpid(), Time: time.Now().Unix(), Clear: true}, m.hostResume.token)
}

// TakeHostResumeToken returns the OSC 7577 token the terminal gave this
// process, and removes it from the environment, so nothing this process
// starts can report with it. "" when there is none or it is not a valid
// token.
func TakeHostResumeToken() string {
	tok := os.Getenv(progresume.TokenEnv)
	_ = os.Unsetenv(progresume.TokenEnv)
	if !progresume.ValidToken(tok) {
		return ""
	}
	return tok
}
