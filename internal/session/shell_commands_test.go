package session

import (
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestShellTrackFollowsMarks walks the state machine through the marks the
// common shells send, and checks the events each raises and the facts left
// behind. The cases are the ones that differ between shells: zsh and fish
// send D before every prompt, even when nothing ran, and a bash integration
// often sends no D when a command is interrupted.
func TestShellTrackFollowsMarks(t *testing.T) {
	mark := func(typ vt.SemanticMarkerType) vt.SemanticMarker {
		return vt.SemanticMarker{Type: typ, ExitCode: -1}
	}
	cmd := func(text string) vt.SemanticMarker {
		return vt.SemanticMarker{Type: vt.MarkerCommandExecuted, ExitCode: -1, CapturedText: text}
	}
	done := func(code int) vt.SemanticMarker {
		return vt.SemanticMarker{Type: vt.MarkerCommandFinished, ExitCode: code}
	}
	tests := []struct {
		name       string
		marks      []vt.SemanticMarker
		wantEvents []string
		wantPrompt bool
		wantSeq    uint64
		wantExit   *int
		wantLast   string
	}{
		{
			name:       "first prompt",
			marks:      []vt.SemanticMarker{mark(vt.MarkerPromptStart), mark(vt.MarkerCommandStart)},
			wantEvents: []string{EventPrompt},
			wantPrompt: true,
		},
		{
			name:       "a command that fails",
			marks:      []vt.SemanticMarker{mark(vt.MarkerPromptStart), cmd("make test"), done(2), mark(vt.MarkerPromptStart)},
			wantEvents: []string{EventPrompt, EventCommandStarted, EventCommandFinished, EventPrompt},
			wantPrompt: true,
			wantSeq:    1,
			wantExit:   intPtr(2),
			wantLast:   "make test",
		},
		{
			name:       "zsh sends D before a prompt where nothing ran",
			marks:      []vt.SemanticMarker{mark(vt.MarkerPromptStart), done(0), mark(vt.MarkerPromptStart)},
			wantEvents: []string{EventPrompt},
			wantPrompt: true,
		},
		{
			name:       "a prompt with no D ends the command with no status",
			marks:      []vt.SemanticMarker{mark(vt.MarkerPromptStart), cmd("sleep 9"), mark(vt.MarkerPromptStart)},
			wantEvents: []string{EventPrompt, EventCommandStarted, EventCommandFinished, EventPrompt},
			wantPrompt: true,
			wantSeq:    1,
			wantLast:   "sleep 9",
		},
		{
			name:       "running is not at a prompt",
			marks:      []vt.SemanticMarker{mark(vt.MarkerPromptStart), cmd("vim")},
			wantEvents: []string{EventPrompt, EventCommandStarted},
		},
		{
			name:       "after D and before the prompt the shell reads a line",
			marks:      []vt.SemanticMarker{cmd("true"), done(0)},
			wantEvents: []string{EventCommandStarted, EventCommandFinished},
			wantPrompt: true,
			wantSeq:    1,
			wantExit:   intPtr(0),
			wantLast:   "true",
		},
		{
			// fish 4 marks its commands itself, and an rc file that also
			// does sends a second C at the same place. Counting it as a new
			// command finished the real one at once with no output.
			name:       "a second C at the same place is the same command",
			marks:      []vt.SemanticMarker{mark(vt.MarkerPromptStart), cmd("make"), cmd("make"), done(0)},
			wantEvents: []string{EventPrompt, EventCommandStarted, EventCommandFinished},
			wantPrompt: true,
			wantSeq:    1,
			wantExit:   intPtr(0),
			wantLast:   "make",
		},
		{
			name:       "the command line is masked and kept to one line",
			marks:      []vt.SemanticMarker{cmd("curl -H 'token=abc123'\n  x"), done(0)},
			wantEvents: []string{EventCommandStarted, EventCommandFinished},
			wantPrompt: true,
			wantSeq:    1,
			wantExit:   intPtr(0),
			wantLast:   "curl -H 'token=[redacted]' x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var track shellTrack
			var got []string
			now := time.Unix(100, 0)
			for _, m := range tt.marks {
				now = now.Add(time.Second)
				for _, ev := range track.note(m, now) {
					got = append(got, ev.Type)
				}
			}
			if len(got) != len(tt.wantEvents) {
				t.Fatalf("events = %v, want %v", got, tt.wantEvents)
			}
			for i := range got {
				if got[i] != tt.wantEvents[i] {
					t.Fatalf("events = %v, want %v", got, tt.wantEvents)
				}
			}
			f := track.facts()
			if !f.Seen {
				t.Fatal("facts say no mark was seen")
			}
			if f.AtPrompt != tt.wantPrompt || f.CommandSeq != tt.wantSeq || f.LastCmdline != tt.wantLast {
				t.Fatalf("facts = %+v, want at_prompt=%v seq=%d last=%q", f, tt.wantPrompt, tt.wantSeq, tt.wantLast)
			}
			if (f.LastExit == nil) != (tt.wantExit == nil) || (f.LastExit != nil && *f.LastExit != *tt.wantExit) {
				t.Fatalf("last exit = %v, want %v", f.LastExit, tt.wantExit)
			}
		})
	}
}

// TestShellTrackFinishedEventCarriesTheCommand checks what a command-finished
// event says: the command line, the status, how long it ran and the count.
func TestShellTrackFinishedEventCarriesTheCommand(t *testing.T) {
	var track shellTrack
	start := time.Unix(100, 0)
	track.note(vt.SemanticMarker{Type: vt.MarkerCommandExecuted, CapturedText: "go test ./...", ExitCode: -1}, start)
	evs := track.note(vt.SemanticMarker{Type: vt.MarkerCommandFinished, ExitCode: 1}, start.Add(1500*time.Millisecond))
	if len(evs) != 1 || evs[0].Type != EventCommandFinished {
		t.Fatalf("events = %+v, want one command-finished", evs)
	}
	ev := evs[0]
	if ev.Cmdline != "go test ./..." || ev.ExitCode == nil || *ev.ExitCode != 1 || ev.DurationMS != 1500 || ev.CommandSeq != 1 {
		t.Fatalf("event = %+v (exit %v), want the command, exit 1, 1500 ms, seq 1", ev, ev.ExitCode)
	}
}

// TestLastFinishedCommandSkipsTheRunningOne holds last-command-output to the
// command that finished: a command still running has a C with no D after it,
// and reading from it would return output that is not done yet.
func TestLastFinishedCommandSkipsTheRunningOne(t *testing.T) {
	marks := []vt.SemanticMarker{
		{Type: vt.MarkerPromptStart, AbsLine: 0},
		{Type: vt.MarkerCommandExecuted, AbsLine: 1},
		{Type: vt.MarkerCommandFinished, AbsLine: 4},
		{Type: vt.MarkerPromptStart, AbsLine: 4},
		{Type: vt.MarkerCommandExecuted, AbsLine: 5},
	}
	c, d, ok := lastFinishedCommand(marks)
	if !ok || c.AbsLine != 1 || d.AbsLine != 4 {
		t.Fatalf("got C@%d D@%d ok=%v, want C@1 D@4", c.AbsLine, d.AbsLine, ok)
	}
	if _, _, ok := lastFinishedCommand(marks[:2]); ok {
		t.Fatal("a command with no D was reported as finished")
	}
}

// TestLastCommandOutputReadsBetweenTheMarks feeds an emulator what a shell
// with integration prints around two commands, and reads the second one's
// output back.
func TestLastCommandOutputReadsBetweenTheMarks(t *testing.T) {
	term := vt.NewWithScrollback(40, 5, 100)
	p := &PTY{terminal: term}
	write := func(s string) {
		t.Helper()
		if _, err := term.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	write("\x1b]133;A\x07$ \x1b]133;B\x07echo one\r\n\x1b]133;C\x07one\r\n\x1b]133;D;0\x07")
	write("\x1b]133;A\x07$ \x1b]133;B\x07seq 3\r\n\x1b]133;C\x071\r\n2\r\n3\r\n\x1b]133;D;0\x07")
	write("\x1b]133;A\x07$ ")
	out, truncated, ok := p.LastCommandOutput()
	if !ok {
		t.Fatal("no finished command was found")
	}
	if out != "1\n2\n3" || truncated {
		t.Fatalf("output = %q (truncated %v), want %q", out, truncated, "1\n2\n3")
	}
}
