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

// TestShellTrackTellsPromptMarksOnly covers a shell whose integration marks its
// prompts and not its commands, as bash before 4.4 does with the bash recipe.
// Such a pane looks like one at a prompt while a command runs. A line run
// typed that comes back to a prompt on a later row with no C shows it, and the
// pane is then not at a prompt as far as anyone can tell. The cases that must
// not count are a prompt redrawn in place and the prompt the line is read at.
func TestShellTrackTellsPromptMarksOnly(t *testing.T) {
	at := func(typ vt.SemanticMarkerType, row int) vt.SemanticMarker {
		return vt.SemanticMarker{Type: typ, AbsLine: row, ExitCode: -1}
	}
	type step struct {
		mark   vt.SemanticMarker
		expect bool // run types a line before this mark
	}
	tests := []struct {
		name         string
		steps        []step
		wantOnly     bool
		wantPrompt   bool
		wantCommands bool
		wantEvents   int // prompt events
	}{
		{
			name:       "a typed line comes back to a new prompt with no C",
			steps:      []step{{mark: at(vt.MarkerPromptStart, 0)}, {mark: at(vt.MarkerCommandStart, 0)}, {mark: at(vt.MarkerPromptStart, 2), expect: true}},
			wantOnly:   true,
			wantEvents: 2,
		},
		{
			name:       "a prompt redrawn in place is not a command",
			steps:      []step{{mark: at(vt.MarkerPromptStart, 3)}, {mark: at(vt.MarkerCommandStart, 3)}, {mark: at(vt.MarkerPromptStart, 3), expect: true}},
			wantPrompt: true,
			wantEvents: 1,
		},
		{
			name:       "the input row of a prompt of two rows is not a new prompt",
			steps:      []step{{mark: at(vt.MarkerPromptStart, 0)}, {mark: at(vt.MarkerCommandStart, 1), expect: true}},
			wantPrompt: true,
			wantEvents: 1,
		},
		{
			name: "a line typed after D is read at the prompt that follows",
			steps: []step{
				{mark: at(vt.MarkerCommandExecuted, 1)}, {mark: vt.SemanticMarker{Type: vt.MarkerCommandFinished, AbsLine: 2}},
				{mark: at(vt.MarkerPromptStart, 3), expect: true}, {mark: at(vt.MarkerCommandStart, 3)},
			},
			wantPrompt:   true,
			wantCommands: true,
			wantEvents:   1,
		},
		{
			name: "a C mark later shows the shell marks commands after all",
			steps: []step{
				{mark: at(vt.MarkerPromptStart, 0)}, {mark: at(vt.MarkerPromptStart, 2), expect: true},
				{mark: at(vt.MarkerCommandExecuted, 3)}, {mark: vt.SemanticMarker{Type: vt.MarkerCommandFinished, AbsLine: 4}},
			},
			wantPrompt:   true,
			wantCommands: true,
			wantEvents:   2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var track shellTrack
			prompts := 0
			for _, s := range tt.steps {
				if s.expect {
					track.expect()
				}
				for _, ev := range track.note(s.mark, time.Unix(100, 0)) {
					if ev.Type == EventPrompt {
						prompts++
					}
				}
			}
			f := track.facts()
			if f.PromptOnly != tt.wantOnly || f.AtPrompt != tt.wantPrompt || f.MarksCommands != tt.wantCommands || prompts != tt.wantEvents {
				t.Fatalf("facts = %+v with %d prompt events, want prompt_only=%v at_prompt=%v marks_commands=%v and %d prompt events",
					f, prompts, tt.wantOnly, tt.wantPrompt, tt.wantCommands, tt.wantEvents)
			}
		})
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
