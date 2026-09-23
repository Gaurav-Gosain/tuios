package vt_test

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestSemanticMarkCallback holds the SemanticMark callback to what a shell
// with OSC 133 integration sends around one command: a prompt, the input, the
// command running and the command finishing with its status. The daemon turns
// these into command-started and command-finished events, so every mark has
// to reach it, in order, with the command line on C and the status on D.
//
// It uses New, so it runs against whichever backend the build selected: the
// pure emulator by default and libghostty under -tags ghostty.
func TestSemanticMarkCallback(t *testing.T) {
	term := vt.NewWithScrollback(40, 6, 100)
	var marks []vt.SemanticMarker
	term.SetCallbacks(vt.Callbacks{
		SemanticMark: func(m vt.SemanticMarker) { marks = append(marks, m) },
	})

	write := func(s string) {
		t.Helper()
		if _, err := term.Write([]byte(s)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("\x1b]133;A\x07$ \x1b]133;B\x07")
	write("make test\r\n")
	write("\x1b]133;C\x07")
	write("ok\r\n")
	write("\x1b]133;D;2\x07")
	write("\x1b]133;D\x07")

	want := []vt.SemanticMarkerType{
		vt.MarkerPromptStart, vt.MarkerCommandStart, vt.MarkerCommandExecuted,
		vt.MarkerCommandFinished, vt.MarkerCommandFinished,
	}
	if len(marks) != len(want) {
		t.Fatalf("got %d marks, want %d: %+v", len(marks), len(want), marks)
	}
	for i, typ := range want {
		if marks[i].Type != typ {
			t.Errorf("mark %d is %q, want %q", i, marks[i].Type, typ)
		}
	}
	if got := marks[2].CapturedText; got != "make test" {
		t.Errorf("C mark command line = %q, want %q", got, "make test")
	}
	if got := marks[3].ExitCode; got != 2 {
		t.Errorf("D;2 exit code = %d, want 2", got)
	}
	if got := marks[4].ExitCode; got != -1 {
		t.Errorf("D with no status: exit code = %d, want -1", got)
	}
	// The mark the callback saw is the one the list recorded.
	if last := term.SemanticMarkers().Last(vt.MarkerCommandExecuted); last == nil || last.AbsLine != marks[2].AbsLine {
		t.Errorf("recorded C mark %+v does not match the callback's %+v", last, marks[2])
	}
}
