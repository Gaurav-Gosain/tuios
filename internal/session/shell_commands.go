package session

import (
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A shell's commands, followed through its OSC 133 marks.
//
// A shell with prompt integration (zsh, fish and bash with a setup, and
// every shell a terminal like Ghostty, kitty or WezTerm injects its script
// into) brackets each command with four marks: A where the prompt starts, B
// where the input starts, C when the command runs and D;<status> when it
// finishes. The daemon's emulator already records them for the scrollback
// browser. This file turns them into facts about the pane:
//
//   - whether the shell is at a prompt, which is what run checks before it
//     types, so a command is never typed into a program that is running;
//   - the command running now, or the last one, with its exit status and
//     how long it took;
//   - command_seq, how many commands the pane has finished, which makes a
//     wait for "the next one" free of races;
//
// and into the prompt, command-started and command-finished events, which
// wait-for, run and the after-command-finished hook consume.
//
// Nothing here guesses. A pane whose shell never sent a mark has no shell
// facts at all, and the verbs that need them say so with no_shell_integration
// rather than inventing an answer from the screen.
//
// A shell can also mark its prompts and never its commands: bash before 4.4
// ignores the PS0 the bash recipe sends C from, and some prompt themes send A
// alone. Its pane looks like one at a prompt even while a command runs. Before
// any command that cannot be told from a shell that has not run one yet, so
// run types its first line there. The tracker then watches for the C: when the
// shell instead draws a new prompt on a later row, the pane is prompt-only,
// at_prompt is false from then on, and run refuses it.

// shellCmdlineMax bounds a command line as the daemon reports it, in bytes.
// It goes to every subscriber and every hook, so it is cut, and likely secrets
// in it are masked, the way an Inbox summary is.
const shellCmdlineMax = 512

// shellPhase is where the shell is between its marks.
type shellPhase uint8

const (
	// shellUnknown: no mark yet.
	shellUnknown shellPhase = iota
	// shellPrompt: an A or B mark, and no C since. The shell reads a line.
	shellPrompt
	// shellRunning: a C mark, and no D or A since.
	shellRunning
	// shellFinished: a D mark ended a command and no A has come yet. The
	// shell is about to draw its prompt, and a line typed now is read by it.
	shellFinished
)

// shellTrack is one pane's command state. Its lock is a leaf: note runs from
// the emulator's callback with the terminal lock held, so it takes nothing
// else and calls nothing out.
type shellTrack struct {
	mu      sync.Mutex
	seen    bool
	phase   shellPhase
	cmdline string
	started time.Time
	// startedLine is the row the running command's C mark was on.
	startedLine int
	// seq counts finished commands. A command a new prompt cut short, with no
	// D, counts too, with no status.
	seq          uint64
	lastCmdline  string
	lastExit     *int
	lastDuration time.Duration

	// commands is true once the shell has sent a C mark. A shell whose
	// integration marks only its prompts never sends one: bash older than
	// 4.4 ignores the PS0 the bash recipe sends C from, and some prompt
	// themes send A alone. Such a pane looks like one at a prompt forever.
	commands bool
	// promptLine is the row of the last A or B mark.
	promptLine int
	// expecting is set by run just before it types a line, with the prompt
	// row it typed at. The shell has then read a command and must mark it
	// with C before it draws its next prompt.
	expecting     bool
	expectingLine int
	// promptOnly is true once a line run typed came back to a new prompt, on
	// a later row, with no C before it: the shell ran a command and did not
	// mark it. It stays true until a C mark shows the shell does mark
	// commands after all.
	promptOnly bool
}

// ShellFacts is what a pane's shell has said about its commands. Seen is false
// for a pane whose shell never sent an OSC 133 mark, and every other field is
// then zero.
type ShellFacts struct {
	Seen bool
	// AtPrompt is true when the shell is reading a command line: after a
	// prompt mark, or after a command finished and before the next prompt.
	AtPrompt bool
	// Running is the command line running now, empty at a prompt.
	Running string
	// CommandSeq is how many commands the pane has finished.
	CommandSeq uint64
	// LastCmdline, LastExit and LastDuration describe the most recent
	// finished command. LastExit is nil when the shell sent no status.
	LastCmdline  string
	LastExit     *int
	LastDuration time.Duration
	// MarksCommands is true once the shell has sent a C mark, so the daemon
	// has seen it mark a command start.
	MarksCommands bool
	// PromptOnly is true when a command ran in the pane and the shell did
	// not mark it: its integration sends prompt marks only. AtPrompt is then
	// false, since the daemon cannot tell a prompt from a running command,
	// and run refuses the pane.
	PromptOnly bool
}

// facts returns the pane's shell facts.
func (t *shellTrack) facts() ShellFacts {
	t.mu.Lock()
	defer t.mu.Unlock()
	f := ShellFacts{
		Seen:          t.seen,
		AtPrompt:      !t.promptOnly && (t.phase == shellPrompt || t.phase == shellFinished),
		CommandSeq:    t.seq,
		LastCmdline:   t.lastCmdline,
		LastDuration:  t.lastDuration,
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

// note folds one mark into the state and returns the events it raises, in the
// order they happened. The caller emits them.
func (t *shellTrack) note(m vt.SemanticMarker, now time.Time) []SessionEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen = true
	var out []SessionEvent
	switch m.Type {
	case vt.MarkerPromptStart, vt.MarkerCommandStart:
		// A prompt while a command runs is a command that ended without a
		// D mark, which bash integrations commonly do on ctrl+c.
		if t.phase == shellRunning {
			out = append(out, t.finishLocked(nil, now))
		}
		newPrompt := t.phase != shellPrompt
		if t.expecting {
			switch {
			case t.phase != shellPrompt, m.Type == vt.MarkerCommandStart:
				// The prompt the line is read at is still being drawn:
				// after a D, or the input row of a prompt of several rows.
				t.expectingLine = max(t.expectingLine, m.AbsLine)
			case m.AbsLine > t.expectingLine:
				// A line run typed came back to a new prompt on a later
				// row with no C: the shell ran it without marking it. A
				// prompt redrawn in place, on a resize, stays on its row
				// and proves nothing.
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
		if t.phase == shellRunning {
			// Two integrations at once, a shell's own and one from an rc
			// file, send two C marks for one command at the same place. The
			// second is the same command, not a new one after it.
			if m.AbsLine == t.startedLine {
				return out
			}
			out = append(out, t.finishLocked(nil, now))
		}
		t.commands, t.promptOnly, t.expecting = true, false, false
		t.phase = shellRunning
		t.cmdline = shellCmdline(m.CapturedText)
		t.started = now
		t.startedLine = m.AbsLine
		out = append(out, SessionEvent{Type: EventCommandStarted, Cmdline: t.cmdline})
	case vt.MarkerCommandFinished:
		// zsh and fish send D before every prompt, including one after an
		// empty line where nothing ran. Only a D that ends a command counts.
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

// expect records that run is about to type a command line at the prompt, so
// the next prompt without a C in between shows the shell does not mark its
// commands.
func (t *shellTrack) expect() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expecting = true
	t.expectingLine = t.promptLine
}

// stopExpecting ends what expect started, when the run call ends.
func (t *shellTrack) stopExpecting() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expecting = false
}

// finishLocked ends the running command. The caller holds mu.
func (t *shellTrack) finishLocked(code *int, now time.Time) SessionEvent {
	t.seq++
	t.lastCmdline = t.cmdline
	t.lastExit = code
	t.lastDuration = max(now.Sub(t.started), 0)
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

// shellCmdline is a command line as the daemon reports it: one line, no
// control characters, likely secrets masked, at most shellCmdlineMax bytes.
func shellCmdline(s string) string {
	return attentionText(strings.TrimSpace(s), shellCmdlineMax)
}

// ShellFacts returns what the pane's shell has said about its commands.
func (p *PTY) ShellFacts() ShellFacts {
	return p.shell.facts()
}

// noteShellMark is the emulator's SemanticMark callback: it records the mark
// and raises the events it implies.
func (p *PTY) noteShellMark(m vt.SemanticMarker) {
	for _, ev := range p.shell.note(m, time.Now()) {
		if p.emit != nil {
			p.emit(ev)
		}
	}
}

// shellCommandOutputMax bounds the output last-command-output returns, in
// bytes. A command that printed more keeps its end, which is where a build or
// a test run says how it went.
const shellCommandOutputMax = 256 * 1024

// LastCommandOutput returns what the most recent finished command printed,
// read from the emulator between its C and D marks, and whether there was such
// a command. truncated is true when the output was cut to its last
// shellCommandOutputMax bytes, or when its start has already left the
// scrollback.
func (p *PTY) LastCommandOutput() (output string, truncated, ok bool) {
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

// lastFinishedCommand finds the newest C mark that a D mark follows before
// any other C, and that D. A C still running has no D after it and is skipped,
// so the answer is the last command that finished.
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

// readTerminalLines reads the main screen and its scrollback from the C mark
// to the D mark as plain text. A C mark is sent after the command line's own
// newline, so its row is the first row of output. The D mark sits where the
// output left the cursor: its row is output only when something was printed on
// it before the mark. cut reports rows that had already left the scrollback.
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
	for abs := from; abs <= to; abs++ {
		var b strings.Builder
		if abs < sbLen {
			b.WriteString(term.ScrollbackLine(abs).String())
		} else {
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
		}
		line := b.String()
		if abs == d.AbsLine && d.Col > 0 {
			line = truncateCells(line, d.Col)
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return lines, cut
}

// truncateCells keeps the first n runes of a row read cell by cell.
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
