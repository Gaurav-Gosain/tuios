package session

import (
	"cmp"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"tuios.dev/progstatus"
	"tuios.dev/vt"
)

const handoffHistoryRows = 1000

type handoffLive struct {
	Title      string
	IconName   string
	Place      handoffPlace
	Marks      []vt.SemanticMarker
	Shell      handoffShell
	ProgStatus []progstatus.Record

	OutputSeq   int64
	Ring        []byte
	ResizeMarks []handoffResizeMark
	VTResizes   int64
	LastOutput  int64

	vtSeq int64
}

type handoffPlace struct {
	Cwd, Branch   string
	Announced     bool
	ElsewhereHost string
	ElsewhereDir  string
}

type handoffResizeMark struct {
	Seq           int64
	Width, Height int
}

type handoffShell struct {
	Seen          bool
	Phase         uint8
	Cmdline       string
	Started       time.Time
	StartedLine   int
	Seq           uint64
	LastCmdline   string
	LastExit      *int
	LastDuration  time.Duration
	LastAt        time.Time
	Commands      bool
	PromptLine    int
	Expecting     bool
	ExpectingLine int
	PromptOnly    bool
	PromptTop     int
	StartedPrompt int
	StartedCwd    string
	History       []handoffCommand
}

type handoffCommand struct {
	Seq          uint64
	Cmdline      string
	Exit         *int
	Started      time.Time
	Duration     time.Duration
	Cwd          string
	PromptOrigin int
}

func (p *PTY) handoffCapture() (*TerminalState, handoffLive) {
	state := p.GetTerminalStatePacked(handoffHistoryRows, 0)
	live := handoffLive{Title: p.Title(), IconName: p.IconName(), Place: p.place.handoffState(), ProgStatus: p.progStatus.Records()}
	sortx.SortFunc(live.ProgStatus, func(a, b progstatus.Record) int { return cmp.Compare(a.Seq, b.Seq) })

	dropped, origin := 0, 0
	if state != nil {
		dropped = state.ScrollbackLen - min(state.ScrollbackLen, handoffHistoryRows)
	}
	if p.marks != nil {
		origin = p.marks.Trimmed()
		for _, m := range p.marks.Markers() {
			if m.AbsLine -= dropped; m.AbsLine >= 0 {
				live.Marks = append(live.Marks, m)
			}
		}
	}
	live.Shell = p.shell.handoffState(
		func(row int) int { return row - dropped },
		func(row int) int { return row - origin - dropped },
	)

	p.outputMu.RLock()
	live.OutputSeq = p.outputSeq
	live.Ring = slices.Clone(p.outputBuffer[:p.outputPos])
	for _, m := range p.resizeMarks {
		live.ResizeMarks = append(live.ResizeMarks, handoffResizeMark{Seq: m.seq, Width: m.width, Height: m.height})
	}
	p.outputMu.RUnlock()
	p.terminalMu.RLock()
	live.VTResizes = p.vtResizes
	p.terminalMu.RUnlock()
	live.LastOutput = p.lastOutput.Load()
	return state, live
}

func (p *PTY) restoreLive(live handoffLive) {
	if live.Title != "" {
		title := live.Title
		p.title.Store(&title)
	}
	if live.IconName != "" {
		p.setIconName(live.IconName)
	}
	p.place.restore(live.Place)
	if p.marks != nil {
		for _, m := range live.Marks {
			p.marks.Add(m)
		}
	}
	p.shell.restore(live.Shell)
	for _, r := range live.ProgStatus {
		p.progStatus.ApplyFrom(progstatus.Report{
			State: r.State, ID: r.ID, Kind: r.Kind, Progress: r.Progress, App: r.App, Title: r.Title, Msg: r.Msg,
		}, r.At, r.Group)
	}

	p.outputSeq = live.OutputSeq
	p.outputBuffer = make([]byte, outputRingSize(len(live.Ring)))
	p.outputPos = copy(p.outputBuffer, live.Ring)
	p.resizeMarks = p.resizeMarks[:0]
	for _, m := range live.ResizeMarks {
		p.resizeMarks = append(p.resizeMarks, resizeMark{seq: m.Seq, width: m.Width, height: m.Height})
	}
	p.vtSeq = live.vtSeq
	p.vtResizes = live.VTResizes
	p.lastOutput.Store(live.LastOutput)
}

func (r *placeRecord) handoffState() handoffPlace {
	host, dir := r.ElsewhereReport()
	return handoffPlace{Cwd: r.Cwd(), Branch: r.Branch(), Announced: r.announced.Load(), ElsewhereHost: host, ElsewhereDir: dir}
}

func (r *placeRecord) restore(h handoffPlace) {
	if h.Cwd != "" {
		cwd := h.Cwd
		r.cwd.Store(&cwd)
	}
	branch := h.Branch
	r.branch.Store(&branch)
	r.announced.Store(h.Announced)
	if h.ElsewhereHost != "" {
		r.elsewhere.Store(&elsewhereReport{host: h.ElsewhereHost, dir: h.ElsewhereDir})
	}
}

func (t *shellTrack) handoffState(plain, origin func(int) int) handoffShell {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := handoffShell{
		Seen: t.seen, Phase: uint8(t.phase), Cmdline: t.cmdline, Started: t.started,
		StartedLine: plain(t.startedLine), Seq: t.seq, LastCmdline: t.lastCmdline,
		LastExit: t.lastExit, LastDuration: t.lastDuration, LastAt: t.lastAt,
		Commands: t.commands, PromptLine: plain(t.promptLine), Expecting: t.expecting,
		ExpectingLine: plain(t.expectingLine), PromptOnly: t.promptOnly,
		PromptTop: origin(t.promptTop), StartedPrompt: origin(t.startedPrompt), StartedCwd: t.startedCwd,
	}
	for _, c := range t.history {
		h.History = append(h.History, handoffCommand{
			Seq: c.Seq, Cmdline: c.Cmdline, Exit: c.Exit, Started: c.Started,
			Duration: c.Duration, Cwd: c.Cwd, PromptOrigin: origin(c.promptOrigin),
		})
	}
	return h
}

func (t *shellTrack) restore(h handoffShell) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen, t.phase, t.cmdline, t.started = h.Seen, shellPhase(h.Phase), h.Cmdline, h.Started
	t.startedLine, t.seq, t.lastCmdline = h.StartedLine, h.Seq, h.LastCmdline
	t.lastExit, t.lastDuration, t.lastAt = h.LastExit, h.LastDuration, h.LastAt
	t.commands, t.promptLine, t.expecting = h.Commands, h.PromptLine, h.Expecting
	t.expectingLine, t.promptOnly = h.ExpectingLine, h.PromptOnly
	t.promptTop, t.startedPrompt, t.startedCwd = h.PromptTop, h.StartedPrompt, h.StartedCwd
	t.history = t.history[:0]
	for _, c := range h.History {
		t.history = append(t.history, shellCommandRecord{
			Seq: c.Seq, Cmdline: c.Cmdline, Exit: c.Exit, Started: c.Started,
			Duration: c.Duration, Cwd: c.Cwd, promptOrigin: c.PromptOrigin,
		})
	}
}
