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

const hostProgramStatusGap = 250 * time.Millisecond

const hostProgramStatusMax = 64

const hostProgramStatusQuery = "\x1b]7501;?\x1b\\"

type hostProgramStatus struct {
	asked, supported bool
	sent             map[string]string
	dirty, pending   bool
}

type hostProgramStatusFlushMsg struct{}

func (m *OS) hostProgramStatusOn() bool {
	if m.LearnMode || !m.agentsOn() {
		return false
	}
	if m.Client != ClientLocal && m.Client != ClientSSH {
		return false
	}
	return m.hostProgramStatusMode() != config.HostProgramStatusOff
}

func (m *OS) hostProgramStatusMode() string {
	if m.UserConfig == nil {
		return config.HostProgramStatusSession
	}
	return config.NormalizeHostProgramStatus(m.UserConfig.Agents.HostProgramStatus)
}

func (m *OS) hostProgramStatusProbe() tea.Cmd {
	if m.hostPS.asked || !m.hostProgramStatusOn() {
		return nil
	}
	m.hostPS.asked = true
	return tea.Raw(hostProgramStatusQuery)
}

func (m *OS) handleHostProgramStatusMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg.(type) {
	case tea.ProgramStatusSupportMsg, uv.ProgramStatusSupportEvent:
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

func (m *OS) markHostProgramStatus() {
	if m.hostPS.supported {
		m.hostPS.dirty = true
	}
}

func (m *OS) hostProgramStatusAfter() tea.Cmd {
	if !m.hostPS.supported || !m.hostPS.dirty || m.hostPS.pending {
		return nil
	}
	m.hostPS.dirty = false
	m.hostPS.pending = true
	return tea.Tick(hostProgramStatusGap, func(time.Time) tea.Msg { return hostProgramStatusFlushMsg{} })
}

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

func (m *OS) hostProgramStatusReports() map[string]string {
	want := make(map[string]string)
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

func hostStatusText(s string, limit int) string {
	s = strings.Join(strings.Fields(progstatus.FitText(invisible.Strip(s), 4*limit)), " ")
	return progstatus.FitText(s, limit)
}
