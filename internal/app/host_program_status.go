package app

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/invisible"
	"tuios.dev/progstatus"
)

// tuios as a program that reports to the terminal it runs in.
//
// A terminal that supports the Program Status Protocol (OSC 7501), such as
// ghostty or Rex, shows what the programs in it are doing. Inside tuios the
// program is tuios, so tuios reports its panes' agent states there. The
// terminal then shows that a pane in tuios waits on you even while tuios is in
// another tab.
//
// By default (host_program_status = "session") the terminal gets one record
// for the attached session, under the id <session>: the most urgent state of
// its panes, and a message that counts them, such as "1 needs input,
// 2 working". A terminal shows one record for each of its own panes, the most
// urgent, so one record per tuios pane would show only the single most urgent
// pane. With "panes" it gets one record per pane instead, under the id
// <session>/<pane>, with the pane's name as the title.
//
// Nothing a pane wrote is passed through. A pane's own OSC 7501 is read by its
// emulator and becomes the pane's agent state; what reaches the host terminal
// is tuios's own report about that state, built here, inside the protocol's
// limits. The host is asked first (OSC 7501 ; ?), and only a terminal that
// answers gets reports. [agents] host_program_status = "off" turns it off.
//
// Reports are rate limited: a change is held for hostProgramStatusGap, and
// everything that changed in that time goes out together, so a pane that
// flips state as fast as it can write costs the host a few sequences a second
// at most. Only the records that changed are sent. On exit the client's
// terminal reset (RIS) removes every record, as the protocol says it does.

// hostProgramStatusGap is the shortest time between two batches of reports.
const hostProgramStatusGap = 250 * time.Millisecond

// hostProgramStatusMax is how many pane records tuios keeps on the host. The
// specification lets a terminal hold as few as 64, and tuios stays under that
// so the host never evicts a record tuios still means.
const hostProgramStatusMax = 64

// hostProgramStatusQuery is the feature detection query.
const hostProgramStatusQuery = "\x1b]7501;?\x1b\\"

// hostProgramStatus is what this client knows about its terminal's support.
type hostProgramStatus struct {
	// asked is set once the query went out, supported once the terminal
	// answered it.
	asked, supported bool
	// sent is the body last sent for each record id.
	sent map[string]string
	// dirty says the panes changed since the last batch, pending that a
	// batch is scheduled.
	dirty, pending bool
}

// hostProgramStatusFlushMsg sends the batch that is due.
type hostProgramStatusFlushMsg struct{}

// hostProgramStatusOn reports whether this client may report to its terminal:
// the setting allows it, the agent features are on, and the client is a
// terminal tuios writes to directly (this machine or an ssh client). A
// browser tab draws through tuios-web and has no such terminal.
func (m *OS) hostProgramStatusOn() bool {
	if m.LearnMode || !m.agentsOn() {
		return false
	}
	if m.Client != ClientLocal && m.Client != ClientSSH {
		return false
	}
	return m.hostProgramStatusMode() != config.HostProgramStatusOff
}

// hostProgramStatusMode is [agents] host_program_status, read.
func (m *OS) hostProgramStatusMode() string {
	if m.UserConfig == nil {
		return config.HostProgramStatusSession
	}
	return config.NormalizeHostProgramStatus(m.UserConfig.Agents.HostProgramStatus)
}

// hostProgramStatusProbe asks the terminal whether it supports OSC 7501.
func (m *OS) hostProgramStatusProbe() tea.Cmd {
	if m.hostPS.asked || !m.hostProgramStatusOn() {
		return nil
	}
	m.hostPS.asked = true
	return tea.Raw(hostProgramStatusQuery)
}

// handleHostProgramStatusMsg takes the terminal's answer and the batch timer.
// It reports whether msg was one of them.
func (m *OS) handleHostProgramStatusMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uv.UnknownOscEvent:
		if !strings.HasPrefix(string(msg), progstatus.QueryReply) {
			return nil, false
		}
		// Anything after the "?" is for a later revision, and is ignored.
		if m.hostPS.asked && !m.hostPS.supported {
			m.hostPS.supported = true
			m.hostPS.dirty = true
			m.LogInfo("Host terminal supports OSC 7501: reporting pane states")
		}
		return nil, true
	case hostProgramStatusFlushMsg:
		m.hostPS.pending = false
		return m.hostProgramStatusFlush(), true
	}
	return nil, false
}

// markHostProgramStatus notes that a pane's state may have changed.
func (m *OS) markHostProgramStatus() {
	if m.hostPS.supported {
		m.hostPS.dirty = true
	}
}

// hostProgramStatusAfter schedules a batch when something changed and none is
// on its way. Update calls it after every message.
func (m *OS) hostProgramStatusAfter() tea.Cmd {
	if !m.hostPS.supported || !m.hostPS.dirty || m.hostPS.pending {
		return nil
	}
	m.hostPS.dirty = false
	m.hostPS.pending = true
	return tea.Tick(hostProgramStatusGap, func(time.Time) tea.Msg { return hostProgramStatusFlushMsg{} })
}

// hostProgramStatusFlush sends what changed since the last batch.
func (m *OS) hostProgramStatusFlush() tea.Cmd {
	if !m.hostPS.supported || !m.hostProgramStatusOn() {
		return nil
	}
	want := m.hostProgramStatusReports()
	var out strings.Builder
	for id, body := range want {
		if m.hostPS.sent[id] == body {
			continue
		}
		out.WriteString("\x1b]7501;")
		out.WriteString(body)
		out.WriteString("\x1b\\")
	}
	for id := range m.hostPS.sent {
		if _, ok := want[id]; !ok {
			out.WriteString(progstatus.Sequence(progstatus.Report{State: progstatus.Clear, ID: id}))
		}
	}
	m.hostPS.sent = want
	if out.Len() == 0 {
		return nil
	}
	return tea.Raw(out.String())
}

// hostProgramStatusReports is the body of the report each pane should have
// on the host now, by record id. A pane with no agent state, or one nothing
// says anything about (unknown), has no record.
func (m *OS) hostProgramStatusReports() map[string]string {
	want := make(map[string]string)
	// In session mode, the default, the pane reports are only counted.
	rollUp := m.hostProgramStatusMode() == config.HostProgramStatusSession
	var panes []progstatus.Report
	session := progstatus.FitSegment(m.SessionName, progstatus.MaxSegment)
	if m.SessionName == "" {
		session = "local"
	}
	for _, w := range m.Windows {
		if w == nil || hiddenFromLists(w) || len(want) >= hostProgramStatusMax {
			continue
		}
		stateWord, seen := m.railAgentState(w.ID, w.AgentState, w.AgentCompletionSeq)
		r := progstatus.Report{Progress: progstatus.NoProgress}
		switch stateWord {
		case "working":
			r.State = progstatus.Working
		case "needs_input":
			r.State = progstatus.Blocked
			switch w.AgentKind {
			case "approval":
				r.Kind = progstatus.Permission
			case "question":
				r.Kind = progstatus.Question
			case "auth":
				r.Kind = progstatus.Auth
			}
		case "done":
			// A finished turn the person has looked at is at rest.
			r.State = progstatus.Done
			if seen {
				r.State = progstatus.Idle
			}
		case "errored":
			r.State = progstatus.Error
		case "idle":
			r.State = progstatus.Idle
		default:
			continue
		}
		pane := w.ID
		if len(pane) > 8 {
			pane = pane[:8]
		}
		r.ID = session + "/" + progstatus.FitSegment(pane, progstatus.MaxSegment)
		app := w.AgentHarness
		if sum, ok := programSummary(w.ProgramStatus); ok {
			if app == "" {
				app = sum.App
			}
			if r.State == progstatus.Working || r.State == progstatus.Blocked {
				r.Progress = sum.Progress
			}
		}
		if app != "" {
			r.App = progstatus.FitSegment(app, progstatus.MaxApp)
		}
		r.Title = hostStatusText(m.railTitleShown(w), progstatus.MaxTitleDecoded)
		r.Msg = hostStatusText(w.AgentMessage, progstatus.MaxMsgDecoded)
		if rollUp {
			panes = append(panes, r)
			continue
		}
		want[r.ID] = progstatus.Encode(r)
	}
	if len(panes) > 0 {
		r := sessionStatusReport(panes)
		r.ID = session
		name := m.SessionName
		if name == "" {
			name = session
		}
		r.Title = hostStatusText(name, progstatus.MaxTitleDecoded)
		want[r.ID] = progstatus.Encode(r)
	}
	return want
}

// sessionStateWords are the words of the session record's message, by
// state, in the order the message lists them: the most urgent first.
var sessionStateWords = []struct {
	state progstatus.State
	word  string
}{
	{progstatus.Blocked, "needs input"},
	{progstatus.Error, "errored"},
	{progstatus.Done, "done"},
	{progstatus.Working, "working"},
	{progstatus.Idle, "idle"},
}

// sessionStatusReport rolls the reports of a session's panes into one: the
// most urgent state, the kind when every blocked pane waits for the same
// kind of answer, and a message that counts the panes in each state, such
// as "1 needs input, 2 working". The app is tuios. panes is not empty.
func sessionStatusReport(panes []progstatus.Report) progstatus.Report {
	r := progstatus.Report{State: panes[0].State, Progress: progstatus.NoProgress, App: "tuios"}
	counts := make(map[progstatus.State]int, len(sessionStateWords))
	var kind progstatus.Kind
	blocked, sameKind := 0, true
	for _, p := range panes {
		counts[p.State]++
		if progstatus.Urgency(p.State) > progstatus.Urgency(r.State) {
			r.State = p.State
		}
		if p.State == progstatus.Blocked {
			if blocked == 0 {
				kind = p.Kind
			} else if p.Kind != kind {
				sameKind = false
			}
			blocked++
		}
	}
	if blocked > 0 && sameKind {
		r.Kind = kind
	}
	parts := make([]string, 0, len(sessionStateWords))
	for _, w := range sessionStateWords {
		if n := counts[w.state]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+w.word)
		}
	}
	r.Msg = strings.Join(parts, ", ")
	return r
}

// HostProgramStatusClear returns the reports that remove every record this
// client left on its terminal, and forgets them, or "" when it left none. The
// caller writes it to the client's terminal once the program has stopped: an
// ssh client's terminal gets no reset when the connection ends, and a local
// one gets it before the reset, which removes the records too.
func (m *OS) HostProgramStatusClear() string {
	if len(m.hostPS.sent) == 0 {
		return ""
	}
	ids := make([]string, 0, len(m.hostPS.sent))
	for id := range m.hostPS.sent {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	var out strings.Builder
	for _, id := range ids {
		out.WriteString(progstatus.Sequence(progstatus.Report{State: progstatus.Clear, ID: id}))
	}
	m.hostPS.sent = nil
	return out.String()
}

// hostStatusText makes a pane's text fit a title or msg: one line, no control
// or invisible formatting characters, at most limit bytes.
func hostStatusText(s string, limit int) string {
	s = strings.Join(strings.Fields(progstatus.FitText(invisible.Strip(s), 4*limit)), " ")
	return progstatus.FitText(s, limit)
}
