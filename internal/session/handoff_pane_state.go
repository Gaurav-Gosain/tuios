package session

import (
	"cmp"
	"slices"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sortx"
	"tuios.dev/progstatus"
	"tuios.dev/vt"
)

// What a pane holds that its emulator's snapshot does not, carried across a
// live handoff (docs/design/live-handoff.md, section 2.3): its title, where its
// shell is, its OSC 133 marks and the commands they delimit, its OSC 7501
// records, and the stream position and ring a resuming subscriber reads from.

// handoffHistoryRows is how much history each pane's emulator carries: the
// depth a client gets on attach.
const handoffHistoryRows = 1000

// handoffLive is a pane's live section.
type handoffLive struct {
	Title string
	Place handoffPlace
	// Marks are the OSC 133 marks, on the rows of the carried emulator.
	Marks []vt.SemanticMarker
	Shell handoffShell
	// ProgStatus is the OSC 7501 store, oldest update first.
	ProgStatus []progstatus.Record

	// The stream. Ring is the last bytes of it, ending at OutputSeq.
	OutputSeq   int64
	Ring        []byte
	ResizeMarks []handoffResizeMark
	VTResizes   int64
	LastOutput  int64

	// vtSeq is the stream position the applied snapshot was taken at. It
	// is the snapshot's own Seq, kept here once the snapshot is dropped.
	vtSeq int64
}

// handoffPlace is a placeRecord.
type handoffPlace struct {
	Cwd, Branch   string
	Announced     bool
	ElsewhereHost string
	ElsewhereDir  string
}

// handoffResizeMark is a resizeMark.
type handoffResizeMark struct {
	Seq           int64
	Width, Height int
}

// handoffShell is a shellTrack, with its rows on the carried emulator.
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

// handoffCommand is a shellCommandRecord.
type handoffCommand struct {
	Seq          uint64
	Cmdline      string
	Exit         *int
	Started      time.Time
	Duration     time.Duration
	Cwd          string
	PromptOrigin int
}

// handoffCapture snapshots a quiesced pane's emulator and its live section.
//
// The emulator carries handoffHistoryRows of its history, so a row the pane
// counts from the top of its history moves up by the rows left behind. Marks
// and the shell's rows are moved here, and the ones above the carried
// history are dropped. A row stored plus the marker list's origin is stored
// against an origin of zero, which is where the new emulator's list starts.
func (p *PTY) handoffCapture() (*TerminalState, handoffLive) {
	state := p.GetTerminalStatePacked(handoffHistoryRows, 0)
	live := handoffLive{Title: p.Title(), Place: p.place.handoffState(), ProgStatus: p.progStatus.Records()}
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

// restoreLive puts a pane's live section back on a pane adopted around an
// emulator the carried snapshot was applied to. The pane has not started.
func (p *PTY) restoreLive(live handoffLive) {
	if live.Title != "" {
		title := live.Title
		p.title.Store(&title)
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
	// The ring grows with the output, so an adopted pane's starts empty.
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

// handoffState is the record as it stands.
func (r *placeRecord) handoffState() handoffPlace {
	host, dir := r.ElsewhereReport()
	return handoffPlace{Cwd: r.Cwd(), Branch: r.Branch(), Announced: r.announced.Load(), ElsewhereHost: host, ElsewhereDir: dir}
}

// restore puts a record back as it was, with no branch read: the branch
// came with it.
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

// handoffState is the track, with its plain rows moved by plain and the rows
// stored plus the marker list's origin moved by origin.
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

// restore puts a carried track back.
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
