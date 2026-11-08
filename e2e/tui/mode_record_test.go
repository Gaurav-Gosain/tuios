package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestTapeRecordingKeepsEveryModeChoice: a tape records each mode the person
// chose, so a replay ends in the mode the recording ended in. Starting a
// recording puts the client in terminal mode, and alt+esc takes it back to
// window management. Both used to write the mode without telling the
// recorder: the saved tape had the typed line and no mode lines, and a replay
// begun in terminal mode stayed there, so the next window key went to the
// shell.
//
// Negative control (NEGATIVE_CONTROLS.md, "One writer of the client mode"):
// origin/main fails at the first check, with no TerminalMode line in the
// saved tape.
func TestTapeRecordingKeepsEveryModeChoice(t *testing.T) {
	term, base := tapeSession(t, "modes")
	leader := tuitest.Ctrl('b')
	for _, k := range []any{leader, "T", "r"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitForText("recording_", uiTimeout); err != nil {
		t.Fatalf("the recording name dialog never opened: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Recording started", uiTimeout); err != nil {
		t.Fatalf("the recording never started: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
	runInShell(t, term, "echo rec-$((2+3))", "rec-5", shellTimeout)
	leaveTerminalMode(t, term)
	for _, k := range []any{leader, "T", "s"} {
		if err := term.SendKeys(k); err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if err := term.WaitForText("Recording saved", uiTimeout); err != nil {
		t.Fatalf("the recording was never saved: %v\n%s", err, term.Snapshot())
	}

	path, saved := onlySavedTape(t, base)
	if err := os.WriteFile(filepath.Join(artifactDir(t), "recording.tape"), []byte(saved), 0o600); err != nil {
		t.Fatal(err)
	}
	enter := lineIndex(saved, "TerminalMode")
	typed := strings.Index(saved, "rec-$((2+3))")
	leave := lastLineIndex(saved, "WindowManagementMode")
	switch {
	case enter < 0:
		t.Fatalf("the tape has no TerminalMode line for the start of the recording:\n%s", saved)
	case typed < 0:
		t.Fatalf("the tape has no typed line:\n%s", saved)
	case leave < 0:
		t.Fatalf("the tape has no WindowManagementMode line for alt+esc:\n%s", saved)
	case enter > typed || leave < typed:
		t.Fatalf("the mode lines are out of order (TerminalMode at %d, typed at %d, WindowManagementMode at %d):\n%s",
			enter, typed, leave, saved)
	}

	// Replayed from terminal mode, the tape ends in window management, where
	// "," opens the settings panel instead of reaching the shell.
	enterTerminalMode(t, term)
	if out, errOut, err := splitCLI(t, base, "tape", "exec", "-s", "modes", path); err != nil {
		t.Fatalf("tape exec of the recording: %v\n%s%s", err, out, errOut)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys(","); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Settings", uiTimeout); err != nil {
		t.Fatalf("after the replay, ',' did not open settings: the client is not in window management mode\n%s", term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "replayed-ends-in-window-mode")
}

// onlySavedTape returns the path and text of the one recording under base.
func onlySavedTape(t *testing.T, base string) (string, string) {
	t.Helper()
	out, _, err := splitCLI(t, base, "tape", "list", "--json")
	if err != nil {
		t.Fatalf("tape list --json: %v\n%s", err, out)
	}
	var list struct {
		Tapes []struct {
			Path string `json:"path"`
		} `json:"tapes"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil || len(list.Tapes) != 1 {
		t.Fatalf("tape list --json = %s (%v), want one recording", out, err)
	}
	saved, err := os.ReadFile(list.Tapes[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	return list.Tapes[0].Path, string(saved)
}

// lineIndex is the byte offset of the first line of text that is exactly
// want, or -1.
func lineIndex(text, want string) int {
	if found := lineOffsets(text, want); len(found) > 0 {
		return found[0]
	}
	return -1
}

// lastLineIndex is the byte offset of the last line of text that is exactly
// want, or -1. A recording begun in window management mode opens with a
// WindowManagementMode line of its own.
func lastLineIndex(text, want string) int {
	if found := lineOffsets(text, want); len(found) > 0 {
		return found[len(found)-1]
	}
	return -1
}

func lineOffsets(text, want string) []int {
	var found []int
	off := 0
	for line := range strings.Lines(text) {
		if strings.TrimSpace(line) == want {
			found = append(found, off)
		}
		off += len(line)
	}
	return found
}
