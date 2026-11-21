package session

import (
	"slices"
	"strings"
	"sync"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"tuios.dev/vt"
)

const shellCmdlineMax = 512

const shellHistoryMax = 200

type shellCommandRecord struct {
	Seq          uint64
	Cmdline      string
	Exit         *int
	Started      time.Time
	Duration     time.Duration
	Cwd          string
	promptOrigin int
}

type markContext struct {
	origin int
	cwd    string
}

type shellPhase uint8

const (
	shellUnknown shellPhase = iota
	shellPrompt
	shellRunning
	shellFinished
)

type shellTrack struct {
	mu           sync.Mutex
	seen         bool
	phase        shellPhase
	promptReady  bool
	cmdline      string
	started      time.Time
	startedLine  int
	seq          uint64
	lastCmdline  string
	lastExit     *int
	lastDuration time.Duration
	lastAt       time.Time

	commands      bool
	promptLine    int
	expecting     bool
	expectingLine int
	promptOnly    bool

	promptTop     int
	startedPrompt int
	startedCwd    string
	history       []shellCommandRecord
}

// ShellFacts is what a pane's shell has said about its commands. Seen is false
// for a pane whose shell never sent an OSC 133 mark, and every other field is
// then zero.
type ShellFacts struct {
	Seen bool
	// AtPrompt is true when the shell is reading a command line: after a
	// prompt mark, or after a command finished and before the next prompt.
	AtPrompt bool
	// PromptReady follows a fresh A or B mark; C and D clear it. Unlike
	// AtPrompt, a finished command alone does not make it true.
	PromptReady bool
	// Running is the command line running now, empty at a prompt.
	Running string
	// CommandSeq is how many commands the pane has finished.
	CommandSeq uint64
	// LastCmdline, LastExit and LastDuration describe the most recent
	// finished command. LastExit is nil when the shell sent no status.
	LastCmdline  string
	LastExit     *int
	LastDuration time.Duration
	// LastAt is when that command finished, zero when none has.
	LastAt time.Time
	// MarksCommands is true once the shell has sent a C mark, so the daemon
	// has seen it mark a command start.
	MarksCommands bool
	// PromptOnly is true when a command ran in the pane and the shell did
	// not mark it: its integration sends prompt marks only. AtPrompt is then
	// false, since the daemon cannot tell a prompt from a running command,
	// and run refuses the pane.
	PromptOnly bool
}

func (t *shellTrack) facts() ShellFacts {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := ShellFacts{
		Seen:          t.seen,
		AtPrompt:      !t.promptOnly && (t.phase == shellPrompt || t.phase == shellFinished),
		PromptReady:   !t.promptOnly && t.phase == shellPrompt && t.promptReady,
		CommandSeq:    t.seq,
		LastCmdline:   t.lastCmdline,
		LastDuration:  t.lastDuration,
		LastAt:        t.lastAt,
		MarksCommands: t.commands,
		PromptOnly:    t.promptOnly,
	}
	if t.phase == shellRunning {
		f.Running = t.cmdline
	}
	if t.lastExit != nil {
		code := *t.lastExit
		f.LastExit = &code
	}
	return f
}

func (t *shellTrack) note(m vt.SemanticMarker, now time.Time, mc markContext) []SessionEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen = true
	var out []SessionEvent
	switch m.Type {
	case vt.MarkerPromptStart, vt.MarkerCommandStart:
		t.promptReady = true
		if t.phase == shellRunning {
			out = append(out, t.finishLocked(nil, now))
		}
		newPrompt := t.phase != shellPrompt
		if m.Type == vt.MarkerPromptStart || t.phase != shellPrompt {
			t.promptTop = m.AbsLine + mc.origin
		}
		if t.expecting {
			switch {
			case t.phase != shellPrompt, m.Type == vt.MarkerCommandStart:
				t.expectingLine = max(t.expectingLine, m.AbsLine)
			case m.AbsLine > t.expectingLine:
				t.expecting = false
				t.promptOnly = true
				newPrompt = true
			}
		}
		if newPrompt {
			out = append(out, SessionEvent{Type: EventPrompt})
		}
		t.phase = shellPrompt
		t.promptLine = m.AbsLine
	case vt.MarkerCommandExecuted:
		t.promptReady = false
		if t.phase == shellRunning {
			if m.AbsLine == t.startedLine {
				return out
			}
			out = append(out, t.finishLocked(nil, now))
		}
		t.startedPrompt = m.AbsLine + mc.origin
		if t.phase == shellPrompt {
			t.startedPrompt = t.promptTop
		}
		t.commands, t.promptOnly, t.expecting = true, false, false
		t.phase = shellRunning
		t.cmdline = shellCmdline(m.CapturedText)
		t.started = now
		t.startedLine = m.AbsLine
		t.startedCwd = mc.cwd
		out = append(out, SessionEvent{Type: EventCommandStarted, Cmdline: t.cmdline})
	case vt.MarkerCommandFinished:
		t.promptReady = false
		if t.phase != shellRunning {
			return out
		}
		var code *int
		if m.ExitCode >= 0 {
			c := m.ExitCode
			code = &c
		}
		out = append(out, t.finishLocked(code, now))
		t.phase = shellFinished
	}
	return out
}

func (t *shellTrack) expect() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expecting = true
	t.expectingLine = t.promptLine
}

func (t *shellTrack) stopExpecting() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expecting = false
}

func (t *shellTrack) finishLocked(code *int, now time.Time) SessionEvent {
	t.seq++
	t.lastCmdline = t.cmdline
	t.lastExit = code
	t.lastDuration = max(now.Sub(t.started), 0)
	t.lastAt = now
	if t.cmdline != "" {
		rec := shellCommandRecord{
			Seq:          t.seq,
			Cmdline:      t.cmdline,
			Started:      t.started,
			Duration:     t.lastDuration,
			Cwd:          t.startedCwd,
			promptOrigin: t.startedPrompt,
		}
		if code != nil {
			c := *code
			rec.Exit = &c
		}
		if len(t.history) >= shellHistoryMax {
			t.history = append(t.history[:0], t.history[len(t.history)-shellHistoryMax+1:]...)
		}
		t.history = append(t.history, rec)
	}
	t.cmdline = ""
	t.phase = shellUnknown
	ev := SessionEvent{
		Type:       EventCommandFinished,
		Cmdline:    t.lastCmdline,
		DurationMS: t.lastDuration.Milliseconds(),
		CommandSeq: t.seq,
	}
	if code != nil {
		c := *code
		ev.ExitCode = &c
	}
	return ev
}

func (t *shellTrack) commandHistory() []shellCommandRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.history)
}

func shellCmdline(s string) string {
	return attentionText(strings.TrimSpace(s), shellCmdlineMax)
}

// ShellFacts returns what the pane's shell has said about its commands.
func (p *PTY) ShellFacts() ShellFacts {
	return p.shell.facts()
}

func (p *PTY) noteShellMark(m vt.SemanticMarker) {
	mc := markContext{cwd: p.place.announcedCwd()}
	if p.marks != nil {
		mc.origin = p.marks.Trimmed()
	}
	for _, ev := range p.shell.note(m, time.Now(), mc) {
		if p.emit != nil {
			p.emit(ev)
		}
	}
}

const shellCommandOutputMax = 256 * 1024

// LastCommandOutput returns what the most recent finished command printed,
// read from the emulator between its C and D marks, and whether there was such
// a command. truncated is true when the output was cut to its last
// shellCommandOutputMax bytes, or when its start has already left the
// scrollback. unwrap joins the rows the terminal soft-wrapped.
func (p *PTY) LastCommandOutput(unwrap bool) (output string, truncated, ok bool) {
	p.terminalMu.RLock()
	defer p.terminalMu.RUnlock()
	if p.terminal == nil {
		return "", false, false
	}
	marks := p.terminal.SemanticMarkers()
	if marks == nil {
		return "", false, false
	}
	c, d, found := lastFinishedCommand(marks.Markers())
	if !found {
		return "", false, false
	}
	lines, cut := readTerminalLines(p.terminal, c, d)
	if unwrap {
		lines = unwrapRows(p.terminal, lines, max(c.AbsLine, 0), true)
	}
	out := strings.Join(lines, "\n")
	if len(out) > shellCommandOutputMax {
		out = out[len(out)-shellCommandOutputMax:]
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
		cut = true
	}
	return out, cut, true
}

func lastFinishedCommand(marks []vt.SemanticMarker) (c, d vt.SemanticMarker, ok bool) {
	var pendingD *vt.SemanticMarker
	for i := len(marks) - 1; i >= 0; i-- {
		switch marks[i].Type {
		case vt.MarkerCommandFinished:
			pendingD = &marks[i]
		case vt.MarkerCommandExecuted:
			if pendingD != nil {
				return marks[i], *pendingD, true
			}
		}
	}
	return vt.SemanticMarker{}, vt.SemanticMarker{}, false
}

func readTerminalLines(term vt.Terminal, c, d vt.SemanticMarker) ([]string, bool) {
	from, to := c.AbsLine, d.AbsLine
	if d.Col == 0 {
		to--
	}
	cut := false
	if from < 0 {
		from, cut = 0, true
	}
	sbLen := term.ScrollbackLen()
	width, height := term.Width(), term.Height()
	if last := sbLen + height - 1; to > last {
		to = last
	}
	var lines []string
	add := func(abs int, line string) {
		if abs == d.AbsLine && d.Col > 0 {
			line = truncateCells(line, d.Col)
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	if from < sbLen {
		term.ScrollbackRows(from, min(to+1, sbLen), func(abs int, line uv.Line) bool {
			add(abs, vt.StripSixelMarkers(line.String()))
			return true
		})
	}
	for abs := max(from, sbLen); abs <= to; abs++ {
		var b strings.Builder
		y := abs - sbLen
		for x := 0; x < width; x++ {
			cell := term.MainCellAt(x, y)
			if cell == nil || cell.Content == "" {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(cell.Content)
			if cell.Width > 1 {
				x += cell.Width - 1
			}
		}
		add(abs, b.String())
	}
	return lines, cut
}

func truncateCells(line string, n int) string {
	i := 0
	for pos := range line {
		if i == n {
			return line[:pos]
		}
		i++
	}
	return line
}
